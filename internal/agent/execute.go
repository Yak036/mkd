package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Execute aplica la propuesta en disco y devuelve el texto que recibirá el
// modelo como mensaje role=tool. Las acciones de lectura no llegan aquí: la UI
// las ejecuta con Read/List/Search de solo lectura.
func Execute(p Proposal, cwd string) (string, error) {
	switch p.Action {
	case ActionWrite:
		return executeWrite(p, cwd)
	case ActionPatch:
		return executePatch(p, cwd)
	case ActionRun:
		return executeRun(p, cwd)
	default:
		return "", fmt.Errorf("Execute no aplica a la acción %s", p.Action)
	}
}

// executeWrite escribe el contenido final de forma atómica (tmp + rename).
func executeWrite(p Proposal, cwd string) (string, error) {
	abs, rel, err := SafePath(cwd, cleanRel(p.Path))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("no se pudo crear el directorio de %s: %w", rel, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(abs), ".mkd-*.tmp")
	if err != nil {
		return "", fmt.Errorf("no se pudo crear temporal para %s: %w", rel, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op si el rename ya consumió el temporal

	if _, err := tmp.WriteString(p.content); err != nil {
		tmp.Close()
		return "", fmt.Errorf("no se pudo escribir %s: %w", rel, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("no se pudo sincronizar %s: %w", rel, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("no se pudo cerrar %s: %w", rel, err)
	}
	if info, statErr := os.Stat(abs); statErr == nil {
		_ = os.Chmod(tmpName, info.Mode().Perm())
	} else {
		_ = os.Chmod(tmpName, 0o644)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return "", fmt.Errorf("no se pudo reemplazar %s: %w", rel, err)
	}
	return fmt.Sprintf("escrito %s (+%d / -%d)", rel, p.Adds, p.Removes), nil
}

// executePatch relee el archivo, verifica la unicidad de search y reescribe.
func executePatch(p Proposal, cwd string) (string, error) {
	abs, rel, err := SafePath(cwd, cleanRel(p.Path))
	if err != nil {
		return "", err
	}
	current, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("no se pudo leer %s: %w", rel, err)
	}
	// Si el archivo cambió desde la propuesta, volvemos a validar contra el
	// contenido real para no aplicar sobre un estado obsoleto.
	updated, err := applyPatch(string(current), p.search, p.replace)
	if err != nil {
		return "", fmt.Errorf("%s: %w", rel, err)
	}
	if err := atomicWrite(abs, updated); err != nil {
		return "", fmt.Errorf("no se pudo escribir %s: %w", rel, err)
	}
	return fmt.Sprintf("parcheado %s (+%d / -%d)", rel, p.Adds, p.Removes), nil
}

// executeRun ejecuta el comando con timeout y devuelve exit code + salida.
//
//	exit=N
//	<salida combinada stdout+stderr, truncada a 200 KiB>
func executeRun(p Proposal, cwd string) (string, error) {
	timeout := p.timeoutSecs
	if timeout <= 0 {
		timeout = 120
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := shellCommand(ctx, p.Command)
	cmd.Dir = cwd
	out := &cappedBuffer{limit: RunLimit}
	cmd.Stdout = out
	cmd.Stderr = out

	err := cmd.Run()
	output := out.String()

	if ctx.Err() == context.DeadlineExceeded {
		return formatRunOutput(-1, output, timeout), nil
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return formatRunOutput(exitErr.ExitCode(), output, 0), nil
		}
		return "", fmt.Errorf("no se pudo ejecutar el comando: %w", err)
	}
	return formatRunOutput(0, output, 0), nil
}

func formatRunOutput(code int, output string, timeout int) string {
	var b strings.Builder
	if timeout > 0 {
		b.WriteString(fmt.Sprintf("exit=%d (timeout de %ds)", code, timeout))
	} else {
		b.WriteString(fmt.Sprintf("exit=%d", code))
	}
	b.WriteString("\n")
	if output == "" {
		b.WriteString("(sin salida)")
	} else {
		b.WriteString(output)
	}
	return b.String()
}

// truncatedMark se concatena cuando la salida supera el límite.
const truncatedMark = "\n… [salida truncada]"

// cappedBuffer acumula hasta limit bytes y descarta el resto. Debe ser seguro
// para escrituras concurrentes de stdout y stderr.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	if c.buf.Len() < c.limit {
		room := c.limit - c.buf.Len()
		if len(p) > room {
			p = p[:room]
		}
		c.buf.Write(p)
	}
	return original, nil
}

func (c *cappedBuffer) String() string {
	text := c.buf.String()
	if utf8.ValidString(text) {
		return text
	}
	return strings.ToValidUTF8(text, "�")
}

// atomicWrite escribe data en path vía temporal + rename preservando permisos.
func atomicWrite(path, data string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".mkd-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if info, statErr := os.Stat(path); statErr == nil {
		_ = os.Chmod(tmpName, info.Mode().Perm())
	} else {
		_ = os.Chmod(tmpName, 0o644)
	}
	return os.Rename(tmpName, path)
}
