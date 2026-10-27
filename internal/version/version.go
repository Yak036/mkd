// Package version expone la versión de MKD en tiempo de ejecución.
//
//	$ mkd --version
//	mkd v0.4.0 (commit abc1234, built 2026-10-27T10:00:00Z, go1.27.3)
//
// Los valores se inyectan en el binario con -ldflags (ver .goreleaser.yml):
//
//	go build -ldflags "-X mkd/internal/version.Version=v1.2.3 ..."
//
// Sin ldflags el binario sigue funcionando y reporta "dev".
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Valores inyectables en build time.
var (
	// Version es la etiqueta semver (v1.2.3). "dev" si no se inyecta.
	Version = "dev"
	// Commit es el SHA corto del commit de build.
	Commit = ""
	// BuildDate es la fecha ISO-8601 del build.
	BuildDate = ""
)

// Info es la información de versión resuelta.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
	GoVersion string `json:"go_version"`
}

// Get resuelve la información de versión.
//
// Si no hubo -ldflags usa la build info embebida por el toolchain
// (`go install modulo@version` sí la incluye), y marca el binario como
// modificado cuando el árbol tenía cambios sin commitear.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
	}

	build, ok := debug.ReadBuildInfo()
	if ok {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = shortCommit(setting.Value)
				}
			case "vcs.time":
				if info.BuildDate == "" {
					info.BuildDate = setting.Value
				}
			case "vcs.modified":
				if setting.Value == "true" && info.Version != "dev" {
					info.Version += "-dirty"
				}
			}
		}
		// `go install module@version` embebe la versión del módulo.
		if info.Version == "dev" && build.Main.Version != "" && build.Main.Version != "(devel)" {
			info.Version = build.Main.Version
		}
	}

	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}

// String devuelve la línea compacta que imprime `mkd --version`.
func (i Info) String() string {
	parts := make([]string, 0, 4)
	parts = append(parts, "mkd "+i.Version)
	if i.Commit != "" {
		parts = append(parts, "commit "+i.Commit)
	}
	if i.BuildDate != "" {
		parts = append(parts, "built "+i.BuildDate)
	}
	parts = append(parts, i.GoVersion)
	return strings.Join(parts, ", ")
}

// Short imprime solo la etiqueta de versión (para banners de la TUI).
func (i Info) Short() string { return "mkd " + i.Version }

// UserAgent identifica la petición ante los proveedores de IA.
func (i Info) UserAgent() string {
	value := "github.com/Yak036/mkd/" + strings.TrimPrefix(i.Version, "v")
	return value
}

func shortCommit(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

// Debug devuelve un resumen multilínea para `mkd version`.
func (i Info) Debug() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Version:    %s\n", i.Version)
	if i.Commit != "" {
		fmt.Fprintf(&b, "Commit:     %s\n", i.Commit)
	}
	if i.BuildDate != "" {
		fmt.Fprintf(&b, "Built:      %s\n", i.BuildDate)
	}
	fmt.Fprintf(&b, "Go:         %s\n", i.GoVersion)
	fmt.Fprintf(&b, "Platform:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return strings.TrimRight(b.String(), "\n")
}
