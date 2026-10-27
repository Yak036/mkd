//go:build !windows

package agent

import (
	"context"
	"os"
	"os/exec"
)

// shellCommand usa /bin/sh -lc si existe (carga el PATH del login) y cae a
// sh -c en entornos mínimos.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	shell := "/bin/sh"
	if _, err := os.Stat(shell); err != nil {
		shell = "sh"
	}
	return exec.CommandContext(ctx, shell, "-lc", command)
}
