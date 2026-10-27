// Package rules implementa el sistema de reglas de MKD.
//
// Una regla es un archivo Markdown con frontmatter YAML simple:
//
//	---
//	description: Convenciones de Go del proyecto
//	globs: internal/**/*.go, cmd/*.go
//	alwaysApply: true
//	priority: 10
//	---
//
//	# Contexto
//	Contenido de la regla…
//
// Aplicación (de mayor a menor prioridad):
//
//	alwaysApply: true        → se inyecta siempre
//	globs                    → se inyecta si algún archivo de la petición casa
//	ninguno de los dos       → regla manual: solo con `:@rule-nombre`
//
// Búsqueda en cascada (las rutas más profundas ganan en caso de empate):
//
//	~/.config/mkd/rules/*.md          (global, configurable con rules_dir)
//	<raíz proyecto>/.mkd/rules/*.md
//	<sub>/.mkd/rules/*.md             …
//	<raíz proyecto>/.mkd/rules/<archivo>.md   (regla puntual)
//	<raíz proyecto>/MKD.md            (siempre, compatibilidad previa)
package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DirName es la carpeta que contiene las reglas dentro del proyecto.
const DirName = ".mkd/rules"

// FileName es la regla única de proyecto compatible con versiones anteriores.
const LegacyFileName = "MKD.md"

// MaxBytes limita el tamaño de una regla leída al contexto (256 KiB).
const MaxBytes = 256 << 10

// Rule es una regla descubierta en disco.
type Rule struct {
	// Name es el token de invocación (`:@rule-nombre`) y el nombre de archivo
	// sin extensión.
	Name string
	// Description viene del frontmatter `description:`.
	Description string
	// Globs lista los patrones de archivo que activan la regla.
	Globs []string
	// AlwaysApply fuerza la inyección en cada petición.
	AlwaysApply bool
	// Priority desempata cuando varias reglas dependen del mismo glob.
	Priority int
	// Path es la ruta absoluta del archivo de la regla.
	Path string
	// Source describe el origen para la UI ("global", "proyecto", ruta).
	Source string
	// Body es el contenido sin frontmatter.
	Body string
	// Scope es el directorio base de la regla, usado para resolver globs.
	Scope string
}

// GlobalDir devuelve la carpeta global de reglas (~/.config/mkd/rules).
func GlobalDir() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "mkd", "rules")
}

// ResolveDir normaliza la carpeta global configurada. Vacío → GlobalDir().
func ResolveDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return GlobalDir()
	}
	if dir == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home
		}
	}
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, dir[2:])
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// Load descubre las reglas del proyecto y las globales.
//
//   - globalDir: carpeta global (o vacía para usar la de por defecto).
//   - workDir:   directorio de trabajo de la sesión.
//
// Las reglas de directorios más profundos se aplican después (cascada).
func Load(globalDir, workDir string) ([]Rule, error) {
	if strings.TrimSpace(workDir) == "" {
		workDir, _ = os.Getwd()
	}
	workDir = filepath.Clean(workDir)

	var rules []Rule
	seen := make(map[string]bool)

	addRule := func(rule Rule, err error) {
		if err != nil || seen[rule.Path] {
			return
		}
		seen[rule.Path] = true
		rules = append(rules, rule)
	}

	// 1. Globales.
	globalRoot := ResolveDir(globalDir)
	if globalRoot != "" {
		for _, path := range ruleFiles(globalRoot) {
			addRule(load(globalRoot, "global", path))
		}
	}

	// 2. Cascada del proyecto: desde la raíz hasta el cwd.
	for _, dir := range cascade(workDir) {
		root := filepath.Join(dir, DirName)
		for _, path := range ruleFiles(root) {
			source := "proyecto"
			if dir != workDir {
				source = relativeSource(workDir, dir)
			}
			// El scope de los globs es el directorio del proyecto que contiene
			// `.mkd/rules`, no la carpeta de reglas en sí.
			rule, err := load(dir, source, path)
			addRule(rule, err)
		}
	}

	// 3. Compatibilidad: MKD.md en la raíz y en el cwd.
	for _, dir := range cascade(workDir) {
		path := filepath.Join(dir, LegacyFileName)
		rule, err := load(dir, "proyecto", path)
		rule.AlwaysApply = true
		rule.Name = legacyName(dir, workDir)
		rule.Scope = dir
		addRule(rule, err)
	}

	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority > rules[j].Priority
		}
		return rules[i].Path < rules[j].Path
	})
	return rules, nil
}

// cascade devuelve los directorios desde el ancestro común relevante hasta
// workDir. La raíz se detecta por la presencia de .git o go.mod y, si no hay,
// se usa el propio workDir.
func cascade(workDir string) []string {
	root := projectRoot(workDir)
	if root == "" || root == workDir {
		return []string{workDir}
	}
	rel, err := filepath.Rel(root, workDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return []string{workDir}
	}

	dirs := []string{root}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		dirs = append(dirs, current)
	}
	return dirs
}

// projectRoot busca hacia arriba el primer directorio con .git o go.mod.
func projectRoot(start string) string {
	current := filepath.Clean(start)
	for {
		if exists(filepath.Join(current, ".git")) || exists(filepath.Join(current, "go.mod")) {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// ruleFiles lista los .md de una carpeta de reglas (sin recursión).
func ruleFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(files)
	return files
}

// load parsea un archivo de regla. Devuelve ok=false si no existe.
func load(scope, source, path string) (Rule, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return Rule{}, fmt.Errorf("regla %s no disponible", path)
	}
	if info.Size() > MaxBytes {
		return Rule{}, fmt.Errorf("regla %s supera %d KiB", path, MaxBytes>>10)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Rule{}, fmt.Errorf("leer regla %s: %w", path, err)
	}

	meta, body := parseFrontmatter(string(data))
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	rule := Rule{
		Name:        name,
		Description: meta["description"],
		Globs:       splitList(meta["globs"]),
		AlwaysApply: parseBool(meta["alwaysapply"]),
		Priority:    parseInt(meta["priority"]),
		Path:        path,
		Source:      source,
		Body:        strings.TrimSpace(body),
		Scope:       scope,
	}
	if override := strings.TrimSpace(meta["name"]); override != "" {
		rule.Name = override
	}
	if rule.Description == "" {
		rule.Description = firstLine(rule.Body)
	}
	return rule, nil
}

// legacyName deriva el nombre de una regla MKD.md según su ubicación relativa
// a la raíz del proyecto: la raíz es `mkd`, y las subcarpetas `mkd-internal`.
func legacyName(dir, workDir string) string {
	root := projectRoot(workDir)
	if root == "" {
		root = workDir
	}
	if filepath.Clean(dir) == filepath.Clean(root) {
		return "mkd"
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "mkd"
	}
	return "mkd-" + strings.NewReplacer("/", "-", "\\", "-").Replace(rel)
}

func relativeSource(workDir, dir string) string {
	rel, err := filepath.Rel(workDir, dir)
	if err != nil {
		return dir
	}
	return rel
}

// parseFrontmatter separa el bloque `---…---` del cuerpo. Las claves se
// normalizan a minúsculas sin espacios para tolerar `alwaysApply`,
// `always_apply`, `Always-Apply`…
func parseFrontmatter(content string) (map[string]string, string) {
	meta := map[string]string{}
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return meta, content
	}

	rest := strings.TrimPrefix(content, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, content
	}

	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = normalizeKey(key)
		value = strings.TrimSpace(value)
		// Lista en bloque: `globs:` seguido de líneas `  - patrón`.
		if value == "" {
			continue
		}
		value = strings.Trim(value, `"'`)
		meta[key] = value
	}

	// Segunda pasada para listas YAML en bloque (globs: + "- x").
	for name, block := range blockLists(rest[:end]) {
		if existing := meta[name]; existing != "" {
			meta[name] = existing + "," + strings.Join(block, ",")
		} else {
			meta[name] = strings.Join(block, ",")
		}
	}

	return meta, rest[end+4:]
}

// blockLists extrae listas YAML con guiones: `globs:\n  - a\n  - b`.
func blockLists(front string) map[string][]string {
	result := map[string][]string{}
	lines := strings.Split(front, "\n")
	current := ""
	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "- ") {
			if current != "" {
				result[current] = append(result[current], strings.Trim(strings.TrimPrefix(trimmed, "- "), ` "'`))
			}
			continue
		}
		if leadingSpace(raw) {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if ok && strings.TrimSpace(value) == "" {
			current = normalizeKey(key)
			if _, exists := result[current]; !exists {
				result[current] = nil
			}
			continue
		}
		current = ""
	}
	// Descartar claves que quedaron vacías.
	for key, values := range result {
		if len(values) == 0 {
			delete(result, key)
		}
	}
	return result
}

func leadingSpace(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

func normalizeKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.NewReplacer("_", "", "-", "", " ", "").Replace(key)
	return key
}

func splitList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	value = strings.Trim(value, "[]")
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on", "1", "si", "sí":
		return true
	}
	return false
}

func parseInt(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return n
}

func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		if line != "" {
			return line
		}
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Find busca una regla por nombre (case-insensitive).
func Find(list []Rule, name string) (Rule, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, rule := range list {
		if strings.ToLower(rule.Name) == name {
			return rule, true
		}
	}
	return Rule{}, false
}

// Names devuelve los nombres de las reglas (para mensajes de ayuda).
func Names(list []Rule) []string {
	names := make([]string, 0, len(list))
	for _, rule := range list {
		names = append(names, rule.Name)
	}
	return names
}

// ErrNoDir indica que la carpeta de reglas no existe (no es un fallo).
var ErrNoDir = errors.New("carpeta de reglas inexistente")

// Block renderiza las reglas seleccionadas como bloque XML para el system
// prompt. El orden es estable (prioridad desc, ruta asc) para no invalidar la
// caché de prefijo entre peticiones equivalentes.
func Block(list []Rule) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<project_rules>\n")
	for _, rule := range list {
		b.WriteString("<rule name=\"")
		b.WriteString(rule.Name)
		b.WriteString("\"")
		if rule.Source != "" {
			b.WriteString(" source=\"")
			b.WriteString(rule.Source)
			b.WriteString("\"")
		}
		if len(rule.Globs) > 0 {
			b.WriteString(" globs=\"")
			b.WriteString(strings.Join(rule.Globs, ","))
			b.WriteString("\"")
		}
		b.WriteString(">\n")
		b.WriteString(strings.TrimRight(rule.Body, "\n"))
		b.WriteString("\n</rule>\n")
	}
	b.WriteString("</project_rules>")
	return b.String()
}

// TouchedFromFiles convierte rutas ancladas en rutas para Select.
func TouchedFromFiles(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
