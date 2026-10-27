// Package skills descubre, valida e instala "skills" de MKD.
//
// Una skill es una carpeta con un SKILL.md en su raíz:
//
//	<skills_dir>/
//	├── go-reviewer/
//	│   └── SKILL.md        ← obligatorio
//	├── sql-optimizer/
//	│   ├── SKILL.md
//	│   └── references/     ← opcional, se menciona pero no se inyecta
//	└── deploy/
//	    └── SKILL.md
//
// El nombre de la skill es el nombre de la carpeta. En el chat se invoca con
// `:@go-reviewer` y su cuerpo (frontmatter + markdown) se inyecta en el
// contexto igual que un archivo anclado.
package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileName es el archivo que define una skill.
const FileName = "SKILL.md"

// MaxBytes limita el tamaño de un SKILL.md leído al contexto (256 KiB).
const MaxBytes = 256 << 10

// Skill es una skill descubierta en disco.
type Skill struct {
	// Name es el nombre de la carpeta (token de invocación).
	Name string
	// Description viene del frontmatter `description:` si existe.
	Description string
	// Dir es la ruta absoluta de la carpeta de la skill.
	Dir string
	// Path es la ruta absoluta del SKILL.md.
	Path string
	// Size es el tamaño del SKILL.md en bytes.
	Size int64
	// Files lista los archivos auxiliares (rutas relativas a Dir).
	Files []string
}

// DefaultDir devuelve la carpeta por defecto de skills:
// ~/.local/share/mkd/skills.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share", "mkd", "skills")
}

// ResolveDir normaliza la carpeta configurada, expandiendo `~` y rutas
// relativas al home. Devuelve DefaultDir() si la entrada está vacía.
func ResolveDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return DefaultDir()
	}
	if dir == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home
		}
	}
	// "~/..." y "~\\..." (Windows) expanden al home del usuario.
	if strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, dir[2:])
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// Scan descubre todas las skills de la carpeta. Una skill sin SKILL.md se
// ignora (no es un error: puede ser una subcarpeta auxiliar).
func Scan(dir string) ([]Skill, error) {
	dir = ResolveDir(dir)
	if dir == "" {
		return nil, errors.New("no se pudo resolver la carpeta de skills")
	}

	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("leer %s: %w", dir, err)
	}

	found := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		skillDir := filepath.Join(dir, entry.Name())
		skill, ok := loadSkill(skillDir, entry.Name())
		if !ok {
			continue
		}
		found = append(found, skill)
	}

	sort.SliceStable(found, func(i, j int) bool {
		return strings.ToLower(found[i].Name) < strings.ToLower(found[j].Name)
	})
	return found, nil
}

// loadSkill valida y construye una skill desde su carpeta.
func loadSkill(dir, name string) (Skill, bool) {
	path := filepath.Join(dir, FileName)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return Skill{}, false
	}
	if info.Size() > MaxBytes {
		return Skill{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, false
	}

	return Skill{
		Name:        name,
		Description: descriptionFrom(string(data)),
		Dir:         dir,
		Path:        path,
		Size:        info.Size(),
		Files:       auxiliaryFiles(dir),
	}, true
}

// auxiliaryFiles lista los archivos junto al SKILL.md (sin recursión profunda:
// un nivel, suficiente para references/ y scripts/).
func auxiliaryFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if entry.Name() == FileName || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	return files
}

// descriptionFrom extrae `description:` del frontmatter YAML simple
// (`---\nname: x\ndescription: y\n---`). Si no hay frontmatter usa la primera
// línea de texto no vacía.
func descriptionFrom(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "---") {
		rest := strings.TrimPrefix(content, "---")
		if end := strings.Index(rest, "\n---"); end >= 0 {
			front := rest[:end]
			for _, line := range strings.Split(front, "\n") {
				key, value, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				if strings.EqualFold(strings.TrimSpace(key), "description") {
					return strings.Trim(strings.TrimSpace(value), `"'`)
				}
			}
			content = strings.TrimSpace(rest[end+4:])
		}
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		if line != "" {
			return line
		}
	}
	return ""
}

// Load devuelve el contenido completo del SKILL.md para inyectarlo al modelo.
func (s Skill) Load() (string, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return "", fmt.Errorf("leer skill %s: %w", s.Name, err)
	}
	if len(data) > MaxBytes {
		return "", fmt.Errorf("skill %s supera %d KiB", s.Name, MaxBytes>>10)
	}
	return string(data), nil
}

// Block envuelve el contenido en el bloque XML que consume el ensamblador de
// contexto, en el mismo estilo que <file> y <git_diff>.
func (s Skill) Block() (string, error) {
	content, err := s.Load()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("<skill name=\"")
	b.WriteString(s.Name)
	b.WriteString("\"")
	if len(s.Files) > 0 {
		b.WriteString(" files=\"")
		b.WriteString(strings.Join(s.Files, ","))
		b.WriteString("\"")
	}
	b.WriteString(">\n")
	b.WriteString(strings.TrimRight(content, "\n"))
	b.WriteString("\n</skill>")
	return b.String(), nil
}

// Find busca una skill por nombre exacto (case-insensitive).
func Find(list []Skill, name string) (Skill, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, skill := range list {
		if strings.ToLower(skill.Name) == name {
			return skill, true
		}
	}
	return Skill{}, false
}

// ValidName comprueba que un nombre de skill sea seguro para usar como carpeta.
func ValidName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// Install crea la carpeta de una skill con el contenido dado. Devuelve error si
// ya existe (Install). Para sobrescribir usa Overwrite.
func Install(dir, name, content string) (Skill, error) {
	return install(dir, name, content, false)
}

// Overwrite instala la skill reemplazando el SKILL.md si ya existía.
func Overwrite(dir, name, content string) (Skill, error) {
	return install(dir, name, content, true)
}

func install(dir, name, content string, overwrite bool) (Skill, error) {
	name = strings.TrimSpace(name)
	if !ValidName(name) {
		return Skill{}, fmt.Errorf("nombre de skill inválido %q (usa letras, números, - _ .)", name)
	}
	if strings.TrimSpace(content) == "" {
		return Skill{}, errors.New("el contenido de la skill está vacío")
	}

	dir = ResolveDir(dir)
	if dir == "" {
		return Skill{}, errors.New("no se pudo resolver la carpeta de skills")
	}
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return Skill{}, fmt.Errorf("crear %s: %w", skillDir, err)
	}

	path := filepath.Join(skillDir, FileName)
	if _, err := os.Stat(path); err == nil && !overwrite {
		return Skill{}, fmt.Errorf("la skill %q ya existe en %s", name, skillDir)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return Skill{}, fmt.Errorf("escribir %s: %w", path, err)
	}
	skill, ok := loadSkill(skillDir, name)
	if !ok {
		return Skill{}, errors.New("la skill se escribió pero no se pudo releer")
	}
	return skill, nil
}

// Remove elimina una skill del disco.
func Remove(dir, name string) error {
	dir = ResolveDir(dir)
	skillDir := filepath.Join(dir, strings.TrimSpace(name))
	// Guarda contra traversal: la carpeta debe colgar de dir.
	rel, err := filepath.Rel(dir, skillDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("ruta de skill inválida: %s", name)
	}
	if err := os.RemoveAll(skillDir); err != nil {
		return fmt.Errorf("eliminar %s: %w", skillDir, err)
	}
	return nil
}
