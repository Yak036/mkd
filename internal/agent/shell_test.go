package agent

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// TestShellCommandPerPlatform comprueba que run_command usa un intérprete que
// existe en la plataforma actual. En Linux/macOS es /bin/sh; en Windows debe
// ser PowerShell o cmd.exe, nunca /bin/sh (que no existe).
func TestShellCommandPerPlatform(t *testing.T) {
	cmd := shellCommand(context.Background(), "echo hola")
	if cmd == nil || cmd.Path == "" {
		t.Fatal("shellCommand devolvió un comando vacío")
	}

	switch runtime.GOOS {
	case "windows":
		base := strings.ToLower(strings.TrimSuffix(strings.ToLower(cmd.Path), ".exe"))
		if strings.Contains(base, "sh") && !strings.Contains(base, "powershell") {
			t.Fatalf("windows usando /bin/sh: %q", cmd.Path)
		}
		if !strings.Contains(base, "powershell") && !strings.Contains(base, "pwsh") &&
			!strings.Contains(base, "cmd") {
			t.Fatalf("intérprete inesperado en windows: %q", cmd.Path)
		}
	default:
		if base := cmd.Path; !strings.HasSuffix(base, "sh") {
			t.Fatalf("%s: intérprete inesperado %q", runtime.GOOS, base)
		}
	}
}

// TestShellHintNoVacio garantiza que la descripción de la herramienta siempre
// menciona un intérprete concreto.
func TestShellHintNoVacio(t *testing.T) {
	hint := shellHint()
	if strings.TrimSpace(hint) == "" {
		t.Fatal("shellHint vacío")
	}
}

// TestExecuteRunCapture ejecuta un comando real y valida el formato de salida.
// Los comandos se adaptan por plataforma para no depender de /bin/sh en Windows.
func TestExecuteRunCapture(t *testing.T) {
	command := "echo mkd-ok"
	expect := "mkd-ok"
	if runtime.GOOS == "windows" {
		command = "Write-Output mkd-ok"
	}

	out, err := Execute(Proposal{
		Action:      ActionRun,
		Command:     command,
		timeoutSecs: 30,
	}, t.TempDir())
	if err != nil {
		t.Fatalf("Execute: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "exit=0") {
		t.Fatalf("falta exit=0 en %q", out)
	}
	if !strings.Contains(out, expect) {
		t.Fatalf("salida sin %q: %q", expect, out)
	}
}

// TestExecuteRunFailureReportaExit valida que un comando inválido devuelve
// código != 0 sin tratarse como error de ejecución.
func TestExecuteRunFailureReportaExit(t *testing.T) {
	command := "exit 3"
	if runtime.GOOS == "windows" {
		command = "exit 3"
	}

	out, err := Execute(Proposal{
		Action:      ActionRun,
		Command:     command,
		timeoutSecs: 30,
	}, t.TempDir())
	if err != nil {
		t.Fatalf("un exit code no debe ser error de Go: %v", err)
	}
	if !strings.Contains(out, "exit=3") {
		t.Fatalf("esperaba exit=3 en %q", out)
	}
}
