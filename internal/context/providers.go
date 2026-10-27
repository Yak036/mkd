package context

// Menciones @ — proveedores especiales, resolución de tokens e indexador de
// archivos con fuzzy matching.
//
//	texto: "revisa @providers.go y @git:diff"
//	          │              │
//	          ▼              ▼
//	     archivo       SpecialProviders
//	          │              │
//	          ▼              ▼
//	 <file path=…>   <git_diff>…</git_diff>

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// mentionMaxFileBytes limita el tamaño de archivo resuelto por mención.
const mentionMaxFileBytes = MaxFileBytes

// mentionMaxIndexEntries topa el número de archivos indexados y de nodos del
// árbol para no bloquear la TUI en repos enormes.
const mentionMaxIndexEntries = 2000

// Provider resuelve un token especial (@git:diff, @tree…) a un bloque de texto.
type Provider struct {
	Name        string
	Description string
	Resolve     func(cwd string) (string, error)
}

// SpecialProviders son las menciones que no son archivos.
var SpecialProviders = []Provider{
	{
		Name:        "@git:diff",
		Description: "Cambios sin commitear (staged + unstaged)",
		Resolve:     resolveGitDiff,
	},
	{
		Name:        "@git:log",
		Description: "Últimos 5 commits con estadísticas",
		Resolve:     resolveGitLog,
	},
	{
		Name:        "@tree",
		Description: "Árbol del proyecto (3 niveles, respeta .gitignore)",
		Resolve:     resolveTree,
	},
}

// FindProvider devuelve el proveedor especial para el token, si existe.
func FindProvider(token string) (Provider, bool) {
	token = strings.TrimSpace(token)
	for _, provider := range SpecialProviders {
		if provider.Name == token {
			return provider, true
		}
	}
	return Provider{}, false
}

// ResolveMention resuelve un token @ (archivo o especial) dentro de cwd y
// devuelve el bloque listo para inyectar en el contexto.
func ResolveMention(token, cwd string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" || token == "@" {
		return "", fmt.Errorf("mención vacía")
	}
	if cwd = strings.TrimSpace(cwd); cwd == "" {
		cwd, _ = os.Getwd()
	}

	// Los especiales se comparan con "@" incluido (@git:diff, @tree).
	if provider, ok := FindProvider(token); ok {
		return provider.Resolve(cwd)
	}

	path := strings.TrimPrefix(token, "@")
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("no se encontró %s", token)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s es un directorio; usa @tree", token)
	}
	if info.Size() > mentionMaxFileBytes {
		return "", fmt.Errorf("%s supera 1 MiB", token)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if isBinaryData(data) {
		return "", fmt.Errorf("%s parece binario", token)
	}

	display := path
	if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
		display = filepath.ToSlash(rel)
	}
	body := string(data)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	// Mismo estilo que Manager.filesBlock.
	return fmt.Sprintf("<file path=%q>\n%s</file>", display, body), nil
}

func resolveGitDiff(cwd string) (string, error) {
	unstaged, err := gitOutput(cwd, "diff")
	if err != nil {
		return "", err
	}
	staged, err := gitOutput(cwd, "diff", "--staged")
	if err != nil {
		return "", err
	}

	parts := make([]string, 0, 2)
	if strings.TrimSpace(unstaged) != "" {
		parts = append(parts, unstaged)
	}
	if strings.TrimSpace(staged) != "" {
		parts = append(parts, staged)
	}
	body := "(sin cambios)"
	if len(parts) > 0 {
		body = strings.Join(parts, "\n")
	}
	return "<git_diff>\n" + strings.TrimRight(body, "\n") + "\n</git_diff>", nil
}

func resolveGitLog(cwd string) (string, error) {
	out, err := gitOutput(cwd, "log", "-n", "5", "--stat", "--oneline")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		out = "(sin commits)"
	}
	return "<git_log>\n" + strings.TrimRight(out, "\n") + "\n</git_log>", nil
}

// gitOutput ejecuta git y traduce sus errores a algo legible en la TUI.
func gitOutput(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return string(out), nil
	}
	if _, statErr := os.Stat(filepath.Join(cwd, ".git")); statErr != nil {
		return "", fmt.Errorf("no es un repositorio git: %s", cwd)
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = err.Error()
	}
	return "", fmt.Errorf("git: %s", oneLineText(msg))
}

func resolveTree(cwd string) (string, error) {
	root := cwd
	if root == "" {
		root, _ = os.Getwd()
	}
	ig := newIgnoreMatcher()
	ig.loadFile(filepath.Join(root, ".gitignore"))

	var b strings.Builder
	count := 0
	walkTree(&b, root, "", "", 0, 3, ig, &count)
	body := strings.TrimRight(b.String(), "\n")
	if body == "" {
		body = "(vacío)"
	}
	return "<project_tree>\n" + body + "\n</project_tree>", nil
}

// walkTree escribe el árbol con guías ├──/└──/│. prefix es el prefijo visual y
// rel la ruta relativa real usada para casar .gitignore.
func walkTree(b *strings.Builder, dir, prefix, rel string, depth, maxDepth int, ig *ignoreMatcher, count *int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type node struct {
		name  string
		isDir bool
	}
	nodes := make([]node, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if ig.ignored(joinRel(rel, name), entry.IsDir()) {
			continue
		}
		nodes = append(nodes, node{name: name, isDir: entry.IsDir()})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].isDir != nodes[j].isDir {
			return nodes[i].isDir
		}
		return nodes[i].name < nodes[j].name
	})

	for i, n := range nodes {
		if *count >= mentionMaxIndexEntries {
			b.WriteString(prefix + "└── …\n")
			return
		}
		*count++
		last := i == len(nodes)-1
		branch, nextPrefix := "├── ", prefix+"│   "
		if last {
			branch, nextPrefix = "└── ", prefix+"    "
		}
		label := n.name
		if n.isDir {
			label += "/"
		}
		b.WriteString(prefix + branch + label + "\n")
		if n.isDir && depth+1 < maxDepth {
			walkTree(b, filepath.Join(dir, n.name), nextPrefix, joinRel(rel, n.name), depth+1, maxDepth, ig, count)
		}
	}
}

// joinRel une una ruta relativa con un hijo usando "/" como separador.
func joinRel(rel, name string) string {
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

// ---------------------------------------------------------------------------
// .gitignore
// ---------------------------------------------------------------------------

// ignorePattern es una regla de .gitignore ya normalizada.
type ignorePattern struct {
	negate   bool
	dirOnly  bool
	anchored bool
	segs     []string
}

func (p ignorePattern) match(rel string, isDir bool) bool {
	if p.dirOnly && !isDir {
		return false
	}
	parts := strings.Split(rel, "/")
	if p.anchored {
		return matchSegments(p.segs, parts, 0, 0)
	}
	// Patrón no anclado (sin "/"): casa contra cualquier segmento, como el
	// nombre base en git.
	if len(p.segs) == 1 {
		for _, part := range parts {
			if matched, err := filepath.Match(p.segs[0], part); err == nil && matched {
				return true
			}
		}
		return false
	}
	// Patrones con "**" pero sin anclar: se prueban en cada profundidad.
	for start := 0; start < len(parts); start++ {
		if matchSegments(p.segs, parts[start:], 0, 0) {
			return true
		}
	}
	return false
}

// matchSegments compara segmentos de patrón con segmentos de ruta, soportando
// ** (zero o más directorios) y globs por segmento (*, ?, [..]). El patrón
// debe consumir la ruta completa.
func matchSegments(pattern, path []string, pi, si int) bool {
	if pi == len(pattern) {
		return si == len(path)
	}
	seg := pattern[pi]
	if seg == "**" {
		for skip := si; skip <= len(path); skip++ {
			if matchSegments(pattern, path, pi+1, skip) {
				return true
			}
		}
		return false
	}
	if si >= len(path) {
		return false
	}
	if matched, err := filepath.Match(seg, path[si]); err != nil || !matched {
		return false
	}
	return matchSegments(pattern, path, pi+1, si+1)
}

// parseIgnorePattern interpreta una línea de .gitignore; devuelve ok=false en
// comentarios y líneas vacías.
func parseIgnorePattern(line string) (ignorePattern, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ignorePattern{}, false
	}
	var p ignorePattern
	if strings.HasPrefix(line, "!") {
		p.negate = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\!`) {
		line = line[1:]
	}
	if strings.HasPrefix(line, "/") {
		p.anchored = true
		line = strings.TrimPrefix(line, "/")
	} else if strings.Contains(line, "/") {
		// Un "/" interno ancla el patrón a la raíz; "**/" se conserva porque
		// matchSegments ya lo interpreta como "cualquier profundidad".
		p.anchored = true
	}
	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	if line == "" {
		return ignorePattern{}, false
	}
	p.segs = strings.Split(line, "/")
	return p, true
}

// ignoreMatcher aplica los defaults + reglas de .gitignore en orden (la última
// regla que casa gana, igual que git).
type ignoreMatcher struct {
	rules []ignorePattern
}

func newIgnoreMatcher() *ignoreMatcher {
	m := &ignoreMatcher{}
	for _, name := range []string{".git", "node_modules", "vendor"} {
		m.rules = append(m.rules, ignorePattern{segs: []string{name}})
	}
	return m
}

func (m *ignoreMatcher) loadFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if pattern, ok := parseIgnorePattern(line); ok {
			m.rules = append(m.rules, pattern)
		}
	}
}

// ignored aplica las reglas de forma acumulativa. Si un directorio queda
// ignorado, su contenido no se explora (igual que git).
func (m *ignoreMatcher) ignored(rel string, isDir bool) bool {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	if rel == "" || rel == "." {
		return false
	}
	ignored := false
	for _, rule := range m.rules {
		if rule.match(rel, isDir) {
			ignored = !rule.negate
		}
	}
	return ignored
}

// ---------------------------------------------------------------------------
// FileIndex + fuzzy matching
// ---------------------------------------------------------------------------

// FileIndex es un índice de archivos del proyecto para el fuzzy finder de @.
type FileIndex struct {
	cwd       string
	files     []string
	dirs      []string
	sig       string
	built     bool
	ignoreSig string
}

// NewFileIndex crea un índice (vago) para el directorio dado.
func NewFileIndex(cwd string) *FileIndex {
	if strings.TrimSpace(cwd) == "" {
		cwd, _ = os.Getwd()
	}
	return &FileIndex{cwd: cwd}
}

// Build recorre cwd respetando .gitignore, defaults y límites de tamaño.
func (i *FileIndex) Build() error {
	if i == nil {
		return fmt.Errorf("índice nulo")
	}
	sig := ignoreSignature(i.cwd)
	if i.built && i.ignoreSig == sig {
		return nil
	}

	ig := newIgnoreMatcher()
	ig.loadFile(filepath.Join(i.cwd, ".gitignore"))

	files := make([]string, 0, 256)
	dirs := make([]string, 0, 64)
	err := filepath.WalkDir(i.cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(i.cwd, path)
		if relErr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if ig.ignored(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, rel)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(files) >= mentionMaxIndexEntries {
			return filepath.SkipAll
		}
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() > mentionMaxFileBytes {
			return nil
		}
		if isBinaryFile(path) {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)

	sort.Strings(dirs)
	i.files = files
	i.dirs = dirs
	i.ignoreSig = sig
	i.built = true
	return nil
}

// Files devuelve una copia de los archivos indexados (rutas relativas).
func (i *FileIndex) Files() []string {
	if i == nil {
		return nil
	}
	out := make([]string, len(i.files))
	copy(out, i.files)
	return out
}

// Match devuelve hasta limit rutas ordenadas por score descendente.
func (i *FileIndex) Match(query string, limit int) []string {
	if i == nil {
		return nil
	}
	if !i.built {
		_ = i.Build()
	}
	if limit <= 0 {
		limit = 1
	}

	query = strings.TrimSpace(query)
	if query == "" {
		out := make([]string, 0, min(limit, len(i.files)))
		for _, file := range i.files {
			if len(out) >= limit {
				break
			}
			out = append(out, file)
		}
		return out
	}

	type scored struct {
		path  string
		score int
	}
	hits := make([]scored, 0, len(i.files))
	for _, file := range i.files {
		score, ok := fuzzyScore(query, file)
		if !ok {
			continue
		}
		hits = append(hits, scored{path: file, score: score})
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].score != hits[b].score {
			return hits[a].score > hits[b].score
		}
		if len(hits[a].path) != len(hits[b].path) {
			return len(hits[a].path) < len(hits[b].path)
		}
		return hits[a].path < hits[b].path
	})

	out := make([]string, 0, min(limit, len(hits)))
	for _, hit := range hits {
		if len(out) >= limit {
			break
		}
		out = append(out, hit.path)
	}
	return out
}

// fuzzyScore puntúa la coincidencia de pattern como subsecuencia de target
// (case-insensitive). Devuelve ok=false si pattern no es subsecuencia.
//
//	q = "mgr"  target = "internal/context/manager.go"
//	  m─────┬─────────┬───┐    bonus: inicio de segmento (+8)
//	        g─────┐   │        no contiguo: sin bonus
//	              r───┘
func fuzzyScore(pattern, target string) (int, bool) {
	pat := []rune(strings.ToLower(pattern))
	orig := []rune(target)
	tgt := []rune(strings.ToLower(target))
	if len(pat) == 0 {
		return 0, true
	}
	if len(pat) > len(tgt) {
		return 0, false
	}

	score := 0
	pi := 0
	lastMatch := -2
	contiguous := 0
	for ti := 0; ti < len(tgt) && pi < len(pat); ti++ {
		if tgt[ti] != pat[pi] {
			continue
		}
		score += 1
		if ti == lastMatch+1 {
			contiguous++
			score += 2 * contiguous
		} else {
			contiguous = 0
		}
		if ti == 0 || isSegmentBoundary(orig[ti-1]) {
			score += 8
		}
		lastMatch = ti
		pi++
	}
	if pi < len(pat) {
		return 0, false
	}

	// Penaliza objetivos largos y coincidencias tardías en la ruta.
	score -= len(tgt) / 12
	score -= (lastMatch - len(pat) + 1) / 4
	return score, true
}

func isSegmentBoundary(r rune) bool {
	switch r {
	case '/', '-', '_', '.', '\\':
		return true
	}
	return unicode.IsSpace(r)
}

// ---------------------------------------------------------------------------
// Utilidades de binarios
// ---------------------------------------------------------------------------

var binaryExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true,
	".ico": true, ".webp": true, ".tiff": true, ".psd": true,
	".pdf": true, ".zip": true, ".gz": true, ".tar": true, ".bz2": true,
	".xz": true, ".7z": true, ".rar": true, ".zst": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".a": true,
	".o": true, ".obj": true, ".class": true, ".jar": true, ".wasm": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".mp3": true, ".wav": true, ".flac": true, ".ogg": true, ".m4a": true,
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".webm": true,
	".db": true, ".sqlite": true, ".lock": false,
}

// isBinaryFile detecta binarios por extensión y, si hace falta, por NUL en los
// primeros 512 bytes.
func isBinaryFile(path string) bool {
	if binaryExtensions[strings.ToLower(filepath.Ext(path))] {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := f.Read(head)
	return isBinaryData(head[:n])
}

func isBinaryData(data []byte) bool {
	limit := len(data)
	if limit > 512 {
		limit = 512
	}
	return bytes.IndexByte(data[:limit], 0) >= 0
}

// ignoreSignature resume mtime+tamaño de .gitignore para invalidar la caché.
func ignoreSignature(cwd string) string {
	info, err := os.Stat(filepath.Join(cwd, ".gitignore"))
	if err != nil {
		return "none"
	}
	return fmt.Sprintf("%d-%d", info.ModTime().UnixNano(), info.Size())
}

func oneLineText(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

// ---------------------------------------------------------------------------
// Matching tipo shell para menciones @
// ---------------------------------------------------------------------------

// PathMatches implementa el autocompletado tipo terminal:
//
//	query = "internal/ai/"  →  hijos directos de internal/ai (archivos y dirs)
//	query = "internal/a"    →  rutas que empiezan por "internal/a" + fuzzy
//	query = "mgr"           →  fuzzy sobre la ruta completa (comportamiento previo)
//
// Devuelve rutas relativas terminadas en "/" para directorios. Las coincidencias
// por prefijo se ordenan antes que las difusas para que teclear una ruta sea
// predecible, igual que en una shell.
func (i *FileIndex) PathMatches(query string, limit int) []string {
	if i == nil {
		return nil
	}
	if !i.built {
		_ = i.Build()
	}
	if limit <= 0 {
		limit = 1
	}

	query = strings.TrimSpace(query)
	query = strings.TrimPrefix(query, "./")
	if query == "" {
		return i.entriesUnder("", limit)
	}

	// Ruta que termina en "/": listamos SOLO los hijos de ese directorio.
	if strings.HasSuffix(query, "/") {
		return i.entriesUnder(query, limit)
	}

	// Prefijo de ruta: priorizamos coincidencias literales al comienzo.
	lower := strings.ToLower(query)
	dirHits := make([]string, 0, limit)
	fileHits := make([]string, 0, limit)
	for _, entry := range i.allEntries() {
		if !strings.HasPrefix(strings.ToLower(entry), lower) {
			continue
		}
		if i.isDir(entry) {
			dirHits = append(dirHits, strings.TrimSuffix(entry, "/")+"/")
		} else {
			fileHits = append(fileHits, entry)
		}
	}
	// Directorios primero: navegar por el árbol es el caso dominante.
	prefix := append(dirHits, fileHits...)
	if len(prefix) > limit {
		prefix = prefix[:limit]
	}
	if len(prefix) > 0 {
		return prefix
	}

	// Completamos con fuzzy sobre la ruta completa.
	seen := make(map[string]bool, len(prefix))
	for _, entry := range prefix {
		seen[entry] = true
	}
	out := append([]string(nil), prefix...)
	for _, hit := range i.Match(query, limit) {
		if seen[hit] {
			continue
		}
		out = append(out, hit)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// allEntries devuelve archivos y directorios (dirs con "/" final) ordenados.
func (i *FileIndex) allEntries() []string {
	if i == nil {
		return nil
	}
	entries := make([]string, 0, len(i.files)+len(i.dirs))
	entries = append(entries, i.dirs...)
	entries = append(entries, i.files...)
	sort.Strings(entries)
	return entries
}

// entriesUnder lista los hijos directos de un directorio (query con "/" final
// o vacío para la raíz).
func (i *FileIndex) entriesUnder(dir string, limit int) []string {
	prefix := dir
	out := make([]string, 0, limit)

	seenDirs := make(map[string]bool)
	appendEntry := func(path string, isDir bool) bool {
		if len(out) >= limit {
			return false
		}
		entry := path
		if isDir && !strings.HasSuffix(entry, "/") {
			entry += "/"
		}
		if !strings.HasPrefix(entry, prefix) || entry == prefix {
			return true
		}
		rest := strings.TrimPrefix(entry, prefix)
		if rest == "" {
			return true
		}
		// Solo hijos directos: sin más "/" (salvo el final en dirs).
		if strings.Contains(strings.TrimSuffix(rest, "/"), "/") {
			return true
		}
		if isDir {
			if seenDirs[entry] {
				return true
			}
			seenDirs[entry] = true
		}
		out = append(out, entry)
		return true
	}

	for _, dir := range i.dirs {
		if !appendEntry(dir, true) {
			break
		}
	}
	for _, file := range i.files {
		if !appendEntry(file, false) {
			break
		}
	}
	sort.Strings(out)
	return out
}

// isDir indica si la ruta (sin "/" final) es un directorio indexado.
func (i *FileIndex) isDir(path string) bool {
	if i == nil {
		return false
	}
	path = strings.TrimSuffix(path, "/")
	idx := sort.SearchStrings(i.dirs, path)
	return idx < len(i.dirs) && i.dirs[idx] == path
}
