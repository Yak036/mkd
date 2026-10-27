// Package git encapsula las operaciones de git que usa MKD para /commit y
// /review, y construye los prompts que alimentan al modelo.
//
//	┌──────────────┐  Status()   ┌───────────────┐
//	│ /commit      │────────────►│ staged/unstaged│
//	└──────┬───────┘             └───────┬───────┘
//	       │ CommitPrompt(diff,log)      │
//	       ▼                             ▼
//	  LLM ──► mensaje conventional ──► Commit()
//
//	┌──────────────┐  Diff(base)  ┌───────────────┐
//	│ /review      │─────────────►│ diff -U5      │
//	└──────────────┘              └───────────────┘
//
// Ningún comando se ejecuta fuera del repositorio indicado: todos los helpers
// usan `git -C <cwd>`, así que sesiones con cwd distinto no se mezclan.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Límites de tamaño que enviamos al modelo. Un diff de un repo grande no debe
// consumir todo el presupuesto de contexto: priorizamos nombres de archivo y
// recortamos el contenido por archivo.
const (
	// MaxDiffBytes es el techo de los bloques de diff enviados al modelo.
	MaxDiffBytes = 48 << 10
	// MaxFileDiffBytes es el techo por archivo dentro del diff.
	MaxFileDiffBytes = 12 << 10
	// diffContext son las líneas de contexto de /review.
	diffContext = 5
)

// ErrNotRepo se devuelve cuando el cwd no está dentro de un repositorio git.
var ErrNotRepo = errors.New("no es un repositorio git")

// Status resume el estado del árbol de trabajo.
type Status struct {
	Branch    string
	Staged    []string
	Unstaged  []string
	Untracked []string
	Adds      int
	Removes   int
}

// HasStaged indica si hay cambios preparados para commit.
func (s Status) HasStaged() bool { return len(s.Staged) > 0 }

// HasChanges indica si hay algo (stage, sin stage o untracked) que commitear.
func (s Status) HasChanges() bool {
	return len(s.Staged)+len(s.Unstaged)+len(s.Untracked) > 0
}

// ChangedFiles es el número de rutas afectadas (sin duplicar staged/unstaged).
func (s Status) ChangedFiles() int {
	seen := make(map[string]bool, len(s.Staged)+len(s.Unstaged)+len(s.Untracked))
	for _, list := range [][]string{s.Staged, s.Unstaged, s.Untracked} {
		for _, path := range list {
			seen[path] = true
		}
	}
	return len(seen)
}

// Run ejecuta git en cwd y devuelve stdout (stderr se adjunta al error).
func Run(cwd string, args ...string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return "", err
		}
	}
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if _, statErr := os.Stat(filepath.Join(cwd, ".git")); statErr != nil {
			return "", fmt.Errorf("%w: %s", ErrNotRepo, cwd)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLine(msg))
	}
	return stdout.String(), nil
}

// IsRepo comprueba si cwd pertenece a un repositorio git.
func IsRepo(cwd string) bool {
	_, err := Run(cwd, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

// Branch devuelve la rama actual (o "HEAD" si está desacoplada).
func Branch(cwd string) string {
	out, err := Run(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "—"
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "HEAD"
	}
	return branch
}

// StatusPorcelain parsea `git status --porcelain` y el diff numstat para
// construir el resumen que pinta el modal.
func StatusPorcelain(cwd string) (Status, error) {
	out, err := Run(cwd, "status", "--porcelain")
	if err != nil {
		return Status{}, err
	}

	status := Status{Branch: Branch(cwd)}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		code := line[:2]
		path := strings.TrimSpace(line[3:])

		switch {
		case strings.HasPrefix(code, "??"):
			status.Untracked = append(status.Untracked, path)
			continue
		case code[0] != ' ' && code[0] != '?' && code[0] != '!':
			status.Staged = append(status.Staged, path)
		}
		if len(code) > 1 && code[1] != ' ' {
			status.Unstaged = append(status.Unstaged, path)
		}
	}

	if adds, removes, err := numstat(cwd, "diff", "--cached"); err == nil {
		status.Adds += adds
		status.Removes += removes
	}
	if adds, removes, err := numstat(cwd, "diff"); err == nil {
		status.Adds += adds
		status.Removes += removes
	}
	return status, nil
}

// numstat suma añadidos y eliminados de `git diff [--cached] --numstat`.
func numstat(cwd string, args ...string) (int, int, error) {
	out, err := Run(cwd, args...)
	if err != nil {
		return 0, 0, err
	}
	adds, removes := 0, 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			continue
		}
		if n, err := strconv.Atoi(fields[0]); err == nil {
			adds += n
		}
		if n, err := strconv.Atoi(fields[1]); err == nil {
			removes += n
		}
	}
	return adds, removes, nil
}

// StagedDiff devuelve `git diff --staged` completo.
func StagedDiff(cwd string) (string, error) {
	return Run(cwd, "diff", "--staged")
}

// UnstagedDiff devuelve `git diff` (cambios sin stage).
func UnstagedDiff(cwd string) (string, error) {
	return Run(cwd, "diff")
}

// DiffBetween devuelve el diff contra base. base vacía significa: staged si
// hay stage, y si no, staged+unstaged (cambios actuales).
func DiffBetween(cwd, base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		staged, _ := StagedDiff(cwd)
		unstaged, _ := UnstagedDiff(cwd)
		return strings.TrimSpace(staged + "\n" + unstaged), nil
	}
	return Run(cwd, "diff", "-U"+strconv.Itoa(diffContext), base)
}

// RecentLog devuelve los últimos n commits en formato oneline.
func RecentLog(cwd string, n int) ([]string, error) {
	out, err := Run(cwd, "log", "-n", strconv.Itoa(n), "--oneline")
	if err != nil {
		// Repo recién inicializado sin commits: no es un error fatal.
		if strings.Contains(err.Error(), "does not have any commits") ||
			strings.Contains(err.Error(), "unknown revision") {
			return nil, nil
		}
		return nil, err
	}
	lines := make([]string, 0, n)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// AddAll ejecuta `git add -A`.
func AddAll(cwd string) error {
	_, err := Run(cwd, "add", "-A")
	return err
}

// AddTracked ejecuta `git add -u` (solo archivos ya rastreados).
func AddTracked(cwd string) error {
	_, err := Run(cwd, "add", "-u")
	return err
}

// CommitResult es el resultado de un git commit.
type CommitResult struct {
	Hash    string
	Subject string
	Output  string
}

// Commit ejecuta `git commit -m <message>` y devuelve el hash y el asunto.
func Commit(cwd, message string) (CommitResult, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return CommitResult{}, errors.New("mensaje de commit vacío")
	}
	out, err := Run(cwd, "commit", "-m", message)
	if err != nil {
		return CommitResult{}, err
	}

	result := CommitResult{Output: strings.TrimSpace(out)}
	if hash, hashErr := Run(cwd, "rev-parse", "--short", "HEAD"); hashErr == nil {
		result.Hash = strings.TrimSpace(hash)
	}
	for _, line := range strings.Split(result.Output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			result.Subject = line
			break
		}
	}
	return result, nil
}

// CommitPrompt construye el prompt que pide un Conventional Commit.
//
// El diff se recorta por archivo (MaxFileDiffBytes) y por total (MaxDiffBytes)
// priorizando: lista de archivos → cabeceras de hunk → líneas de cambio.
func CommitPrompt(diff string, log []string, status Status) string {
	var b strings.Builder
	b.WriteString("Eres un ingeniero senior. Genera UN mensaje de commit en formato Conventional Commits.\n\n")
	b.WriteString("Reglas:\n")
	b.WriteString("- Primera línea: `tipo(scope): descripción` en minúsculas, imperativo, ≤ 72 caracteres.\n")
	b.WriteString("- Tipos: feat, fix, refactor, perf, docs, test, build, ci, chore, style.\n")
	b.WriteString("- scope = módulo o área afectada (ej. mcp, ui, agent, config).\n")
	b.WriteString("- Cuerpo opcional (tras línea en blanco): bullets `- ...` con los cambios técnicos relevantes.\n")
	b.WriteString("- Respeta el estilo de los commits recientes del repositorio.\n")
	b.WriteString("- Responde ÚNICAMENTE con el mensaje de commit, sin markdown, sin tildes de código ni explicaciones.\n\n")

	if len(log) > 0 {
		b.WriteString("Commits recientes (estilo a seguir):\n")
		for _, entry := range log {
			b.WriteString("  " + entry + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("Rama: %s\n", valueOr(status.Branch, "—")))
	b.WriteString(fmt.Sprintf("Archivos cambiados: %d (+%d / -%d)\n\n",
		status.ChangedFiles(), status.Adds, status.Removes))

	b.WriteString("Diff:\n")
	b.WriteString(TruncateDiff(diff))
	return b.String()
}

// ReviewPrompt construye el prompt estructurado de code review.
func ReviewPrompt(diff string, files []string, base string) string {
	var b strings.Builder
	b.WriteString("Eres un Senior Code Reviewer. Revisa el diff con rigor y responde en Markdown, en el idioma del usuario.\n\n")
	if base != "" {
		b.WriteString(fmt.Sprintf("Revisión de los cambios contra `%s`.\n\n", base))
	} else {
		b.WriteString("Revisión de los cambios locales (staged + unstaged).\n\n")
	}

	b.WriteString("Estructura EXACTA de tu respuesta:\n")
	b.WriteString("## 🔴 Bugs Potenciales / Condiciones de Carrera / Fugas de Recursos\n")
	b.WriteString("## 🟡 Seguridad, Manejo de Errores y Casos Borde\n")
	b.WriteString("## 🟢 Sugerencias de Refactorización Idiomática\n\n")
	b.WriteString("Reglas:\n")
	b.WriteString("- Cita `archivo:línea` cuando señales un problema.\n")
	b.WriteString("- En 🟢 incluye bloques de código listos para aplicar.\n")
	b.WriteString("- Si una sección no aplica, indícalo en una línea.\n")
	b.WriteString("- No inventes problemas: si el diff está limpio, dilo.\n\n")

	if len(files) > 0 {
		b.WriteString("Archivos tocados:\n")
		for _, file := range files {
			b.WriteString("  - " + file + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("Diff (-U" + strconv.Itoa(diffContext) + "):\n")
	b.WriteString(TruncateDiff(diff))
	return b.String()
}

// FilesFromDiff extrae las rutas `+++ b/...` de un diff unificado.
func FilesFromDiff(diff string) []string {
	seen := make(map[string]bool)
	files := make([]string, 0, 8)
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "+++ b/") && !strings.HasPrefix(line, "--- a/") {
			continue
		}
		path := strings.TrimSpace(line[6:])
		if path == "" || path == "/dev/null" || seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, path)
	}
	return files
}

// TruncateDiff recorta el diff respetando archivos y hunks completos.
//
// Estrategia: recorrer por bloques `diff --git`; si un archivo supera
// MaxFileDiffBytes se corta en un límite de hunk; si el total supera
// MaxDiffBytes se detiene y se anota cuántos archivos quedaron fuera.
func TruncateDiff(diff string) string {
	diff = strings.TrimRight(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")
	if diff == "" {
		return "(sin cambios)\n"
	}
	if len(diff) <= MaxDiffBytes {
		return diff + "\n"
	}

	var b strings.Builder
	remaining := MaxDiffBytes
	skipped := 0

	for _, block := range splitFileBlocks(diff) {
		if remaining <= 0 {
			skipped++
			continue
		}
		if len(block) > MaxFileDiffBytes && len(block) > remaining {
			block = clipBlock(block, min(MaxFileDiffBytes, remaining))
		} else if len(block) > remaining {
			block = clipBlock(block, remaining)
		}
		b.WriteString(block)
		remaining -= len(block)
	}
	if skipped > 0 {
		b.WriteString(fmt.Sprintf("\n… [diff truncado: %d archivo(s) adicionales omitidos]\n", skipped))
	} else if remaining <= 0 {
		b.WriteString("\n… [diff truncado por límite de tamaño]\n")
	}
	return b.String()
}

// splitFileBlocks separa un diff multi-archivo por `diff --git`.
func splitFileBlocks(diff string) []string {
	lines := strings.Split(diff, "\n")
	blocks := make([]string, 0, 8)
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			blocks = append(blocks, current.String())
			current.Reset()
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	flush()
	return blocks
}

// clipBlock recorta un bloque a limit bytes en el último fin de hunk válido.
func clipBlock(block string, limit int) string {
	if limit <= 0 || len(block) <= limit {
		return block
	}
	cut := strings.LastIndex(block[:limit], "\n@@")
	if cut <= 0 {
		cut = limit
	}
	return block[:cut] + "\n… [archivo truncado]\n"
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return value[:index]
	}
	return value
}
