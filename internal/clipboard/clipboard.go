// Package clipboard copia texto al portapapeles del sistema.
//
// Estrategia en cascada (se detiene en el primer método que funciona):
//
//	┌──────────────────────────────────────────────────────────┐
//	│ Copy(text)                                               │
//	│   1. OSC 52  ──► portapapeles del terminal (si stdout TTY)│
//	│   2. pbcopy  ──► solo macOS                              │
//	│   3. wl-copy ──► Wayland (si $WAYLAND_DISPLAY != "")     │
//	│   4. xclip   ──► X11 (fallback en Linux)                 │
//	│   5. xsel    ──► X11 (último recurso)                    │
//	└──────────────────────────────────────────────────────────┘
//
// Solo stdlib: la detección de TTY se hace con os.Stdout.Stat().
package clipboard

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// copyTimeout acota la ejecución de cada binario externo; si un backend se
// queda colgado (p. ej. xclip sin DISPLAY) no bloquea la TUI.
const copyTimeout = 3 * time.Second

// errNoBackend se devuelve cuando ningún método logró copiar.
var errNoBackend = errors.New("clipboard: ningún método de portapapeles disponible")

// Copy intenta copiar text al portapapeles probando los backends en orden.
// Devuelve nil si OSC 52 se emitió con stdout como TTY o si algún binario
// del sistema terminó correctamente. Solo devuelve error si todos fallan.
func Copy(text string) error {
	if text == "" {
		return nil
	}

	// (1) OSC 52: funciona sobre SSH, tmux y screen; no depende de X11.
	if copyOSC52(text) == nil {
		return nil
	}

	// (2) macOS: pbcopy es el método nativo y más fiable.
	if runtime.GOOS == "darwin" {
		if err := copyCommand(text, "pbcopy"); err == nil {
			return nil
		}
	}

	// (3)-(5) Linux: Wayland primero, luego X11.
	if err := copyLinux(text); err == nil {
		return nil
	}

	return errNoBackend
}

// Available lista los backends detectados en el sistema, útil para
// diagnóstico y para mensajes de la UI.
func Available() []string {
	backends := make([]string, 0, 5)
	if stdoutIsTTY() {
		backends = append(backends, "osc52")
	}
	if os.Getenv("TMUX") != "" {
		backends = append(backends, "osc52+tmux")
	}
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("pbcopy"); err == nil {
			backends = append(backends, "pbcopy")
		}
		return backends
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-copy"); err == nil {
			backends = append(backends, "wl-copy")
		}
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		backends = append(backends, "xclip")
	}
	if _, err := exec.LookPath("xsel"); err == nil {
		backends = append(backends, "xsel")
	}
	return backends
}

// copyOSC52 escribe la secuencia OSC 52 al terminal. Si $TMUX o $STY están
// definidos, la envuelve en el passthrough correspondiente para que el
// multiplexor reenvíe la secuencia al terminal anfitrión.
func copyOSC52(text string) error {
	// Sin TTY no hay terminal que intercepte la secuencia.
	if !stdoutIsTTY() {
		return errors.New("clipboard: stdout no es TTY")
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	osc := "\x1b]52;c;" + encoded + "\x07"

	switch {
	case os.Getenv("TMUX") != "":
		// tmux exige el passthrough DCS: ESC P tmux; ESC <osc> ESC \
		osc = "\x1bPtmux;\x1b" + osc + "\x1b\\"
	case os.Getenv("STY") != "":
		// screen: ESC P ... ESC \
		osc = "\x1bP" + strings.ReplaceAll(osc, "\x1b", "\x1b\x1b") + "\x1b\\"
	}

	if _, err := os.Stdout.WriteString(osc); err != nil {
		return err
	}
	return nil
}

// copyLinux prueba wl-copy, xclip y xsel en ese orden.
func copyLinux(text string) error {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if err := copyCommand(text, "wl-copy"); err == nil {
			return nil
		}
	}
	if err := copyCommand(text, "xclip", "-selection", "clipboard"); err == nil {
		return nil
	}
	return copyCommand(text, "xsel", "--clipboard", "--input")
}

// copyCommand busca name en el PATH y le pasa text por stdin con timeout.
func copyCommand(text, name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), copyTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// stdoutIsTTY reporta si la salida estándar es un terminal interactivo.
func stdoutIsTTY() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
