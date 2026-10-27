package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Límites compartidos por las herramientas de solo lectura.
const (
	// RunLimit es el máximo de bytes de salida combinada de run_command.
	RunLimit = 200 << 10
	// searchMaxFileBytes omite archivos mayores al buscar en Go puro.
	searchMaxFileBytes = 1 << 20
	// searchTimeout acota toda la búsqueda walker.
	searchTimeout = 20 * time.Second
	// defaultMaxResults es el límite por defecto de coincidencias.
	defaultMaxResults = 200
	// searchWorkers es el número de goroutines del walker.
	searchWorkers = 8
)

// skipDirs son directorios que nunca se recorren en el walker.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"bin":          true,
}

// Read ejecuta una propuesta ActionRead sobre cwd y devuelve el contenido
// truncado a 256 KiB con números de línea y cabecera de rango.
func Read(p Proposal, cwd string) (string, error) {
	data, err := os.ReadFile(filepath.Join(cwd, filepath.FromSlash(p.Path)))
	if err != nil {
		return "", fmt.Errorf("no se pudo leer %s: %w", p.Path, err)
	}

	start, end := readRange(p)
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if start > 1 || end > 0 {
		if start < 1 {
			start = 1
		}
		if end == 0 || end > len(lines) {
			end = len(lines)
		}
		if start > len(lines) {
			return fmt.Sprintf("(el archivo %s no tiene línea %d)", p.Path, start), nil
		}
		lines = lines[start-1 : end]
	} else {
		start = 1
		end = len(lines)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("== %s (líneas %d-%d de %d) ==\n", p.Path, start, end, len(splitLines(string(data)))))
	for i, line := range lines {
		b.WriteString(fmt.Sprintf("%6d\t%s\n", start+i, line))
	}
	return truncateBytes(b.String(), ReadLimit), nil
}

// readRange extrae start_line/end_line de los argumentos originales.
func readRange(p Proposal) (start, end int) {
	var args readArgs
	if err := DecodeArguments(p.call, &args); err != nil {
		return 1, 0
	}
	start = intOr(args.StartLine, 1)
	end = intOr(args.EndLine, 0)
	return start, end
}

// List ejecuta una propuesta ActionList y devuelve entradas ordenadas con
// '/' para directorios y tamaño para archivos.
func List(p Proposal, cwd string) (string, error) {
	abs, rel, err := SafePath(cwd, cleanRel(p.Path))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", fmt.Errorf("no se pudo listar %s: %w", rel, err)
	}

	type row struct {
		dir  bool
		name string
		size int64
	}
	rows := make([]row, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() && name == ".git" {
			continue
		}
		r := row{name: name, dir: entry.IsDir()}
		if info, infoErr := entry.Info(); infoErr == nil {
			r.size = info.Size()
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].dir != rows[j].dir {
			return rows[i].dir // directorios primero
		}
		return rows[i].name < rows[j].name
	})

	label := rel
	if label == "." {
		label = "."
	} else {
		label += "/"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s (%d entradas)\n", label, len(rows)))
	for _, r := range rows {
		if r.dir {
			b.WriteString(r.name + "/\n")
			continue
		}
		b.WriteString(fmt.Sprintf("%s  %s\n", r.name, formatSize(r.size)))
	}
	return truncateBytes(b.String(), ReadLimit), nil
}

// Search ejecuta una propuesta ActionSearch: ripgrep si está en PATH, walker
// concurrente en Go puro si no.
func Search(p Proposal, cwd string) (string, error) {
	var args searchArgs
	if err := DecodeArguments(p.call, &args); err != nil {
		return "", err
	}
	limit := defaultMaxResults
	if args.MaxResults != nil && *args.MaxResults > 0 {
		limit = *args.MaxResults
	}

	scope := strings.TrimSpace(args.Path)
	if scope == "" {
		scope = "."
	}
	abs, rel, err := SafePath(cwd, cleanRel(scope))
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()

	if rgPath, lookErr := exec.LookPath("rg"); lookErr == nil {
		return searchRipgrep(ctx, rgPath, cwd, abs, rel, args.Query, limit)
	}
	return searchWalker(ctx, abs, rel, args.Query, limit)
}

// searchRipgrep lanza rg --line-number --no-heading --color never.
func searchRipgrep(ctx context.Context, rgPath, cwd, abs, rel, query string, limit int) (string, error) {
	cmd := exec.CommandContext(ctx, rgPath,
		"--line-number", "--no-heading", "--color", "never",
		"--max-count", fmt.Sprintf("%d", limit),
		"--", query, abs,
	)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		// rg devuelve 1 cuando no hay coincidencias: no es un error.
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			if ctx.Err() == context.DeadlineExceeded {
				return resultHeader(rel, query, 0) + "(tiempo de búsqueda agotado)", nil
			}
			return resultHeader(rel, query, 0) + "(sin coincidencias)", nil
		}
		if ctx.Err() == context.DeadlineExceeded {
			return resultHeader(rel, query, 0) + "(tiempo de búsqueda agotado)", nil
		}
		return "", fmt.Errorf("ripgrep: %w", err)
	}

	lines := normalizeRGPaths(string(out), abs, rel)
	capped := capLines(lines, limit)
	return resultHeader(rel, query, len(capped)) + strings.Join(capped, "\n"), nil
}

// normalizeRGPaths convierte las rutas absolutas de rg en rutas relativas
// al proyecto, conservando formato ruta:línea: texto.
func normalizeRGPaths(out, abs, rel string) []string {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	prefixAbs := abs
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, prefixAbs+string(filepath.Separator)) {
			line = rel + "/" + strings.TrimPrefix(line, prefixAbs+string(filepath.Separator))
		}
		lines = append(lines, line)
	}
	return lines
}

// searchWalker recorre el árbol en goroutines y busca query como subcadena
// (o regex si compila como tal).
func searchWalker(ctx context.Context, abs, rel, query string, limit int) (string, error) {
	matcher := newMatcher(query)

	fileCh := make(chan string, searchWorkers*4)
	resultCh := make(chan string, searchWorkers*4)

	var wg sync.WaitGroup
	for i := 0; i < searchWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range fileCh {
				if ctx.Err() != nil {
					continue
				}
				scanFile(ctx, path, abs, rel, matcher, resultCh)
			}
		}()
	}

	walkDone := make(chan struct{})
	go func() {
		defer close(fileCh)
		defer close(walkDone)
		_ = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return filepath.SkipAll
			}
			if err != nil {
				return nil
			}
			name := entry.Name()
			if entry.IsDir() {
				if skipDirs[name] || name == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			select {
			case fileCh <- path:
				return nil
			case <-ctx.Done():
				return filepath.SkipAll
			}
		})
	}()

	go func() {
		wg.Wait()
		close(resultCh)
		<-walkDone
	}()

	results := make([]string, 0, limit)
	for line := range resultCh {
		if len(results) >= limit {
			continue // drenar sin guardar
		}
		results = append(results, line)
	}
	sort.Strings(results)

	if ctx.Err() == context.DeadlineExceeded && len(results) == 0 {
		return resultHeader(rel, query, 0) + "(tiempo de búsqueda agotado)", nil
	}
	if len(results) == 0 {
		return resultHeader(rel, query, 0) + "(sin coincidencias)", nil
	}
	return resultHeader(rel, query, len(results)) + strings.Join(results, "\n"), nil
}

// scanFile busca query en un archivo y envía "ruta:línea: texto" al canal.
func scanFile(ctx context.Context, path, abs, rel string, matcher *matcher, out chan<- string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > searchMaxFileBytes {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || isBinary(data) {
		return
	}

	relPath := path
	if r, relErr := filepath.Rel(abs, path); relErr == nil {
		relPath = r
	}
	relPath = strings.TrimPrefix(filepath.ToSlash(relPath), "./")
	if rel != "." && rel != "" {
		relPath = strings.TrimPrefix(rel, "./") + "/" + relPath
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, line := range lines {
		if ctx.Err() != nil {
			return
		}
		if matcher.match(line) {
			out <- fmt.Sprintf("%s:%d: %s", relPath, i+1, strings.TrimRight(line, "\r"))
		}
	}
}

// matcher envuelve la búsqueda como regex o como texto literal. Coincide con
// la semántica de ripgrep: se intenta regex y, si no compila, literal.
type matcher struct {
	re  *regexp.Regexp
	lit string
}

func newMatcher(query string) *matcher {
	if re, err := regexp.Compile(query); err == nil {
		return &matcher{re: re}
	}
	return &matcher{lit: query}
}

func (m *matcher) match(line string) bool {
	if m.re != nil {
		return m.re.MatchString(line)
	}
	return strings.Contains(line, m.lit)
}

// isBinary detecta binarios por NUL byte en los primeros 512 bytes.
func isBinary(data []byte) bool {
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	return bytes.IndexByte(head, 0) >= 0 || !utf8.Valid(head)
}

func resultHeader(rel, query string, count int) string {
	return fmt.Sprintf("búsqueda %q en %s — %d coincidencias\n", query, rel, count)
}

func capLines(lines []string, limit int) []string {
	if len(lines) > limit {
		return lines[:limit]
	}
	return lines
}

// formatSize imita a ls -h sin dependencias.
func formatSize(size int64) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%dB", size)
	}
}

// truncateBytes recorta un string a limit bytes sin partir un UTF-8.
func truncateBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncatedMark
}
