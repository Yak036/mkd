//go:build windows

package agent

import (
	"context"
	"os"
	"os/exec"
)

// shellCommand elige el intérprete en Windows.
//
//	┌───────────────┐   ¿existe powershell?   ┌──────────────────────────────┐
//	│ run_command   │──────────sí────────────►│ powershell -NoProfile -Command│
//	│               │                         └──────────────────────────────┘
//	│               │──────────no────────────►┌──────────────────────────────┐
//	│               │                         │ cmd.exe /d /s /c             │
//	└───────────────┘                         └──────────────────────────────┘
//
// PowerShell es preferible porque el modelo genera comandos más idiomáticos
// (Get-ChildItem, Select-String) y porque `cmd.exe` no entiende `&&` de forma
// fiable en todas las versiones. Se usa COMSPEC si está definido para respetar
// terminales custom.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if ps, err := exec.LookPath("powershell.exe"); err == nil {
		return exec.CommandContext(ctx, ps,
			"-NoProfile",      // sin perfiles de usuario: arranque determinista
			"-NonInteractive", // nunca pedir input interactivo
			"-Command", command,
		)
	}
	if ps, err := exec.LookPath("pwsh.exe"); err == nil {
		return exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-Command", command)
	}

	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	return exec.CommandContext(ctx, comspec, "/d", "/s", "/c", command)
}

// shellHint describe el intérprete para el mensaje de las herramientas.
func shellHint() string {
	if _, err := exec.LookPath("powershell.exe"); err == nil {
		return "PowerShell"
	}
	if _, err := exec.LookPath("pwsh.exe"); err == nil {
		return "pwsh"
	}
	return "cmd.exe"
}
