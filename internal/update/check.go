// Package update comprueba si hay una versión nueva de MKD publicada en GitHub
// y, cuando el usuario lo pide, descarga e instala el binario nuevo.
//
//	┌──────────────┐  cada 24 h (cacheado)   ┌──────────────────────┐
//	│ mkd (arranque)│────────────────────────►│ api.github.com       │
//	│               │◄── tag_name ────────────│ /releases/latest     │
//	└──────┬───────┘                          └──────────────────────┘
//	       │ hay versión nueva
//	       ▼
//	  status bar: "↑ v0.7.0 disponible · mkd upgrade"
//
//	┌──────────────┐   descarga .tar.gz/.zip   ┌───────────────────┐
//	│ mkd upgrade   │──────────────────────────►│ github releases   │
//	└──────┬───────┘                            └───────────────────┘
//	       │ verifica checksum + reemplaza
//	       ▼
//	  binario actualizado (en Windows deja un .old para borrar al salir)
//
// Reglas de convivencia:
//   - Nunca bloquea el arranque: timeout de 2 s y se puede desactivar con
//     MKD_NO_UPDATE_CHECK=1.
//   - No consulta si stdout no es TTY (CI, pipes) ni en GitHub Actions.
//   - El resultado se cachea 24 h en ~/.local/share/mkd/update_check.json.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// Repo es el repositorio de releases.
	Repo = "Yak036/mkd"
	// checkInterval es cada cuánto se vuelve a consultar GitHub.
	checkInterval = 24 * time.Hour
	// httpTimeout acota la consulta de versión; nunca debe retrasar la TUI.
	httpTimeout = 2 * time.Second
	// downloadTimeout acota la descarga del binario.
	downloadTimeout = 3 * time.Minute
	// cacheFile es el nombre del archivo de caché en el directorio de datos.
	cacheFile = "update_check.json"
)

// Release es la información mínima de una release de GitHub.
type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// Asset es un archivo adjunto de la release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Check es el resultado de la comprobación.
type Check struct {
	// Current es la versión en ejecución.
	Current string `json:"current"`
	// Latest es la última versión publicada.
	Latest string `json:"latest"`
	// Available indica si Latest es más nueva que Current.
	Available bool `json:"available"`
	// URL es la página de la release.
	URL string `json:"url,omitempty"`
	// CheckedAt es cuándo se consultó por última vez.
	CheckedAt time.Time `json:"checked_at"`
	// Err guarda un fallo de red sin abortar el arranque.
	Err string `json:"err,omitempty"`
}

// Notice devuelve el texto para la status bar, o vacío si no hay novedad.
func (c Check) Notice() string {
	if !c.Available {
		return ""
	}
	return fmt.Sprintf("↑ %s disponible · mkd upgrade", c.Latest)
}

// cacheEntry es lo que se persiste en disco.
type cacheEntry struct {
	Latest    string    `json:"latest"`
	URL       string    `json:"url"`
	CheckedAt time.Time `json:"checked_at"`
}

// Disabled indica si el usuario desactivó las comprobaciones.
func Disabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MKD_NO_UPDATE_CHECK"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != ""
}

// Cached consulta la caché local sin red. Devuelve Available=true solo si hay
// una versión más nueva conocida.
func Cached(current string) Check {
	check := Check{Current: current}
	entry, err := readCache()
	if err != nil {
		return check
	}
	check.Latest = entry.Latest
	check.URL = entry.URL
	check.CheckedAt = entry.CheckedAt
	check.Available = IsNewer(entry.Latest, current)
	return check
}

// ShouldCheck indica si la caché está vencida o no existe.
func ShouldCheck(current string) bool {
	entry, err := readCache()
	if err != nil {
		return true
	}
	if entry.CheckedAt.IsZero() {
		return true
	}
	return time.Since(entry.CheckedAt) > checkInterval
}

// Fetch consulta GitHub y actualiza la caché. Es seguro llamarla en segundo
// plano: respeta timeout y nunca entra en pánico.
func Fetch(ctx context.Context, current string) Check {
	check := Check{Current: current, CheckedAt: time.Now()}

	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		check.Err = err.Error()
		return check
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "mkd-update-check")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		check.Err = err.Error()
		return check
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		check.Err = fmt.Sprintf("GitHub respondió %d", resp.StatusCode)
		return check
	}

	var release Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		check.Err = err.Error()
		return check
	}

	check.Latest = release.TagName
	check.URL = release.HTMLURL
	check.Available = IsNewer(release.TagName, current)

	// Una caché vieja nunca debe hacer que se pierda la novedad: solo se
	// escribe cuando la consulta fue correcta.
	if release.TagName != "" {
		_ = writeCache(cacheEntry{
			Latest:    release.TagName,
			URL:       release.HTMLURL,
			CheckedAt: check.CheckedAt,
		})
	}
	return check
}

// IsNewer indica si latest es estrictamente más nueva que current.
//
// Acepta "v0.5.0", "0.5.0" y pre-releases tipo "v0.7.0-rc1". Si alguna de las
// dos no es semver, intenta una comparación numérica por segmentos.
func IsNewer(latest, current string) bool {
	latest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(latest), "v"))
	current = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(current), "v"))
	if latest == "" || current == "" {
		return false
	}
	// Una build de desarrollo no se considera "vieja": evita avisos ruidosos
	// mientras se trabaja en el repo.
	if current == "dev" || strings.Contains(current, "dirty") {
		return false
	}

	l, lok := parseSemver(latest)
	c, cok := parseSemver(current)
	if !lok || !cok {
		return latest != current
	}

	for i := 0; i < 3; i++ {
		if l.num[i] != c.num[i] {
			return l.num[i] > c.num[i]
		}
	}
	// Mismo core: una versión estable es más nueva que una pre-release.
	if c.pre != "" && l.pre == "" {
		return true
	}
	if l.pre != "" && c.pre == "" {
		return false
	}
	return l.pre > c.pre
}

type semver struct {
	num [3]int
	pre string
}

func parseSemver(value string) (semver, bool) {
	var result semver
	if value == "" {
		return result, false
	}
	if idx := strings.IndexAny(value, "-+"); idx >= 0 {
		if value[idx] == '-' {
			result.pre = strings.TrimPrefix(value[idx:], "-")
		}
		value = value[:idx]
	}

	parts := strings.Split(value, ".")
	if len(parts) > 3 {
		return result, false
	}
	for i, part := range parts {
		if part == "" {
			return result, false
		}
		n, ok := atoi(part)
		if !ok {
			return result, false
		}
		result.num[i] = n
	}
	return result, true
}

// atoi convierte dígitos sin aceptar signos (evita que "+1" pase como número).
func atoi(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	n := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
		if n > 1<<30 {
			return 0, false
		}
	}
	return n, true
}

// --- caché ------------------------------------------------------------------

func cachePath() (string, error) {
	base, err := os.UserHomeDir()
	if err != nil || base == "" {
		return "", errors.New("no se pudo resolver el home del usuario")
	}
	dir := filepath.Join(base, ".local", "share", "mkd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, cacheFile), nil
}

func readCache() (cacheEntry, error) {
	path, err := cachePath()
	if err != nil {
		return cacheEntry{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cacheEntry{}, err
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return cacheEntry{}, err
	}
	return entry, nil
}

func writeCache(entry cacheEntry) error {
	path, err := cachePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// AssetName devuelve el nombre del artefacto de goreleaser para la plataforma
// actual (mkd_linux_amd64.tar.gz, mkd_windows_amd64.zip…).
func AssetName(goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("mkd_%s_%s.%s", goos, goarch, ext)
}

// platformAsset busca el asset correspondiente a la plataforma en ejecución.
func platformAsset(release Release) (Asset, bool) {
	want := AssetName(runtime.GOOS, runtime.GOARCH)
	for _, asset := range release.Assets {
		if asset.Name == want {
			return asset, true
		}
	}
	return Asset{}, false
}

// BeginCheck lanza la comprobación en segundo plano (si la caché está vencida)
// y devuelve un canal por el que llegará el resultado. El canal siempre recibe
// exactamente un valor, de modo que la UI puede esperarlo sin bloquear.
//
// Si la comprobación está desactivada o la caché está fresca, el canal recibe
// el estado cacheado de inmediato.
func BeginCheck(current string) <-chan Check {
	result := make(chan Check, 1)

	if Disabled() {
		result <- Check{Current: current}
		close(result)
		return result
	}

	if !ShouldCheck(current) {
		result <- Cached(current)
		close(result)
		return result
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				result <- Check{Current: current, Err: "panic en la comprobación"}
			}
			close(result)
		}()
		result <- Fetch(context.Background(), current)
	}()
	return result
}
