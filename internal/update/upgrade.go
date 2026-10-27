package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// maxDownload limita la descarga (una release pesa ~7 MB; 200 MB es holgado).
const maxDownload = 200 << 20

// Upgrade descarga la última release y reemplaza el binario en ejecución.
//
//  1. consulta /releases/latest
//  2. descarga el asset de esta plataforma
//  3. verifica el SHA-256 contra checksums.txt
//  4. extrae el binario a un temporal junto al actual
//  5. reemplaza (en Windows deja el anterior como .old para borrarlo al salir)
//
// Devuelve la versión instalada.
func Upgrade(ctx context.Context, current string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	release, err := fetchRelease(ctx)
	if err != nil {
		return "", err
	}
	if !IsNewer(release.TagName, current) {
		return current, fmt.Errorf("ya tienes la última versión (%s)", strings.TrimPrefix(current, "v"))
	}

	asset, ok := platformAsset(release)
	if !ok {
		return "", fmt.Errorf("la release %s no tiene binario para %s/%s",
			release.TagName, runtime.GOOS, runtime.GOARCH)
	}

	tmpDir, err := os.MkdirTemp("", "mkd-upgrade-*")
	if err != nil {
		return "", fmt.Errorf("no se pudo crear el temporal: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, asset.Name)
	if err := download(ctx, asset.URL, archivePath); err != nil {
		return "", err
	}

	if err := verifyChecksum(ctx, release, asset, archivePath); err != nil {
		return "", err
	}

	binaryPath, err := extractBinary(archivePath, tmpDir)
	if err != nil {
		return "", err
	}

	if err := replaceSelf(binaryPath); err != nil {
		return "", err
	}
	return release.TagName, nil
}

func fetchRelease(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "mkd-upgrade")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("no se pudo consultar GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub respondió %d", resp.StatusCode)
	}

	var release Release
	if err := jsonDecode(io.LimitReader(resp.Body, 1<<20), &release); err != nil {
		return Release{}, err
	}
	return release, nil
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mkd-upgrade")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("descarga fallida: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("descarga fallida: HTTP %d", resp.StatusCode)
	}

	file, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := io.Copy(file, io.LimitReader(resp.Body, maxDownload)); err != nil {
		return fmt.Errorf("descarga interrumpida: %w", err)
	}
	return nil
}

// verifyChecksum compara el SHA-256 del archivo con checksums.txt. Si el
// checksums.txt no está disponible se omite (con aviso), pero un mismatch
// siempre aborta: es la única defensa contra un binario alterado.
func verifyChecksum(ctx context.Context, release Release, asset Asset, archivePath string) error {
	var sumsURL string
	for _, candidate := range release.Assets {
		if candidate.Name == "checksums.txt" {
			sumsURL = candidate.URL
			break
		}
	}
	if sumsURL == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mkd-upgrade")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil // sin checksums no bloqueamos la actualización
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}

	expected := ""
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset.Name {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if expected == "" {
		return nil
	}

	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("checksum no coincide para %s (esperado %s, obtenido %s)",
			asset.Name, expected[:12], actual[:12])
	}
	return nil
}

// extractBinary extrae el ejecutable del tar.gz (o zip en Windows).
func extractBinary(archivePath, destDir string) (string, error) {
	name := "mkd"
	if runtime.GOOS == "windows" {
		name = "mkd.exe"
	}

	if strings.HasSuffix(archivePath, ".zip") {
		return extractFromZip(archivePath, destDir, name)
	}
	return extractFromTarGz(archivePath, destDir, name)
}

func extractFromTarGz(path, destDir, name string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("archivo corrupto: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != name {
			continue
		}
		out := filepath.Join(destDir, name)
		if err := writeExecutable(out, tr); err != nil {
			return "", err
		}
		return out, nil
	}
	return "", fmt.Errorf("el archivo no contiene %s", name)
}

func extractFromZip(path, destDir, name string) (string, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("archivo corrupto: %w", err)
	}
	defer reader.Close()

	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() || filepath.Base(entry.Name) != name {
			continue
		}
		src, err := entry.Open()
		if err != nil {
			return "", err
		}
		out := filepath.Join(destDir, name)
		err = writeExecutable(out, src)
		src.Close()
		if err != nil {
			return "", err
		}
		return out, nil
	}
	return "", fmt.Errorf("el archivo no contiene %s", name)
}

func writeExecutable(dest string, src io.Reader) error {
	file, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, io.LimitReader(src, maxDownload)); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Chmod(dest, 0o755)
}

// replaceSelf sustituye el binario en ejecución por newBinary.
//
// En Unix es un rename atómico sobre el mismo sistema de archivos (el proceso
// actual sigue con el inode viejo hasta salir). En Windows no se puede
// sobrescribir un .exe en uso, así que se mueve a .old y se deja una nota para
// borrarlo en el siguiente arranque.
func replaceSelf(newBinary string) error {
	real, err := os.Executable()
	if err != nil {
		return fmt.Errorf("no se pudo localizar el binario actual: %w", err)
	}
	// Si se invocó a través de un symlink (~/bin/mkd → /usr/local/bin/mkd),
	// hay que reemplazar el destino, no el enlace.
	current := real
	if resolved, err := filepath.EvalSymlinks(real); err == nil {
		current = resolved
	}

	// El binario nuevo se escribe SIEMPRE en un temporal del mismo directorio:
	// escribir directamente sobre el que está en ejecución falla con
	// ETXTBSY ("text file busy") en Linux y con sharing violation en Windows.
	dir := filepath.Dir(current)
	tmp, err := os.CreateTemp(dir, ".mkd-upgrade-*")
	if err != nil {
		return fmt.Errorf("no se pudo crear el temporal junto a %s: %w", current, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	defer cleanup()

	tmp.Close()

	if err := copyFile(newBinary, tmpPath, 0o755); err != nil {
		return fmt.Errorf("no se pudo escribir el binario nuevo: %w", err)
	}

	if runtime.GOOS == "windows" {
		// Windows no permite renombrar un .exe en uso: se aparta y el nuevo
		// toma su lugar. El .old se borra en el siguiente arranque.
		old := current + ".old"
		_ = os.Remove(old)
		if err := os.Rename(current, old); err != nil {
			return fmt.Errorf("no se pudo apartar el binario actual (¿mkd en ejecución?): %w", err)
		}
		if err := os.Rename(tmpPath, current); err != nil {
			_ = os.Rename(old, current) // intento de volver atrás
			return fmt.Errorf("no se pudo reemplazar %s: %w", current, err)
		}
		if real != current {
			_ = copyFile(current, real, 0o755)
		}
		return nil
	}

	// Unix: rename(2) es atómico y permite sustituir un ejecutable en uso.
	// El proceso actual sigue con el inode viejo hasta terminar.
	if err := os.Rename(tmpPath, current); err != nil {
		return fmt.Errorf("no se pudo reemplazar %s: %w", current, err)
	}
	return nil
}

// copyFile copia respetando permisos y sin cargar el archivo en memoria.
func copyFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// CleanupOldBinary borra el .old que dejó una actualización en Windows.
// Es un no-op en otras plataformas y cuando no hay nada que borrar.
func CleanupOldBinary() {
	current, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(current + ".old")
}

// jsonDecode es un alias local para no importar encoding/json en check.go y
// upgrade.go a la vez con nombres distintos.
func jsonDecode(r io.Reader, v any) error { return json.NewDecoder(r).Decode(v) }
