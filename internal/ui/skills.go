package ui

// Skills en el popup de menciones (`:@nombre`).
//
//	                        consulta del textarea
//	                                │
//	              ┌─────────────────┴─────────────────┐
//	          "@ruta"                            ":@pref"
//	              │                                    │
//	     archivos + especiales            solo skills instaladas
//	              │                                    │
//	     Enter ancla el archivo             Enter inyecta <skill>
//
// La carpeta se toma de cfg.SkillsDir (o ~/.local/share/mkd/skills).

import (
	"sort"
	"strings"

	"github.com/Yak036/mkd/internal/skills"
)

// skillsDir devuelve la carpeta efectiva de skills.
func (m Model) skillsDir() string {
	return skills.ResolveDir(m.cfg.SkillsDir)
}

// loadSkills escanea la carpeta configurada. Un error se propaga para que el
// popup pueda informarlo en vez de mostrarse vacío sin explicación.
func (m Model) loadSkills() ([]skills.Skill, error) {
	return skills.Scan(m.cfg.SkillsDir)
}

// mentionSkillsTrigger indica si el token activo es `:@…` (skills) y no `@…`.
func (m *Model) mentionSkillsTrigger() bool {
	value := m.textarea.Value()
	line := value
	if idx := strings.LastIndexByte(line, '\n'); idx >= 0 {
		line = line[idx+1:]
	}
	start := strings.LastIndexByte(line, ' ')
	if start < 0 {
		start = 0
	} else {
		start++
	}
	return strings.HasPrefix(line[start:], ":@")
}

// mentionSkillMatches filtra las skills instaladas por nombre con la misma
// heurística de subsecuencia que el resto del popup. Las que ya están activas
// se marcan para que el usuario lo vea antes de volver a invocarlas.
func (m *Model) mentionSkillMatches(query string) []mentionItem {
	list, err := m.loadSkills()
	if err != nil {
		m.mentions.popup.suppressed = m.textarea.Value()
		return nil
	}

	lower := strings.ToLower(strings.TrimSpace(query))
	items := make([]mentionItem, 0, len(list))
	for _, skill := range list {
		if lower != "" && !mentionSpecialMatches(lower, skill.Name) {
			continue
		}
		description := skill.Description
		if description == "" {
			description = "skill"
		}
		if m.skillActive(skill.Name) {
			description = "✓ ya activa · " + description
		}
		items = append(items, mentionItem{
			// El token lleva ":" para que replaceMentionToken lo reconstruya.
			Token:       ":@" + skill.Name,
			Label:       "@" + skill.Name,
			Description: description,
			Skill:       true,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
	})
	return items
}

// skillActive comprueba si la skill ya está anclada en el contexto activo.
func (m Model) skillActive(name string) bool {
	for _, file := range m.contextFiles {
		if file.Skill && strings.EqualFold(file.Label, name) {
			return true
		}
	}
	return false
}

// applySkill resuelve la skill, la inyecta en el contexto y la registra en el
// sidebar. Devuelve el aviso para la barra de estado.
func (m *Model) applySkill(name string) (string, bool) {
	list, err := m.loadSkills()
	if err != nil {
		return "No se pudo leer la carpeta de skills: " + err.Error(), false
	}
	skill, ok := skills.Find(list, name)
	if !ok {
		return "Skill no encontrada: " + name, false
	}

	block, err := skill.Block()
	if err != nil {
		return "No se pudo cargar la skill " + name + ": " + err.Error(), false
	}

	if m.mentions.Mentions == nil {
		m.mentions.Mentions = map[string]string{}
	}
	m.mentions.Mentions[":"+skill.Name] = block

	// Feedback visual: la skill aparece en el sidebar con su propio estilo.
	m.upsertSkillContext(skill.Name, skill.Path, skill.Size)
	m.syncSession()
	m.refreshUsage()
	return "Skill activada: @" + skill.Name, true
}

// upsertSkillContext registra la skill como entrada del contexto activo sin
// duplicarla y sin pasar por el hasher de archivos (la skill no es un archivo
// anclado normal: su contenido vive en Mentions).
func (m *Model) upsertSkillContext(name, path string, size int64) {
	for i := range m.contextFiles {
		if m.contextFiles[i].Skill && strings.EqualFold(m.contextFiles[i].Label, name) {
			m.contextFiles[i].Size = size
			m.contextFiles[i].Path = path
			return
		}
	}
	m.contextFiles = append(m.contextFiles, ContextFile{
		Path:  path,
		Size:  size,
		Skill: true,
		Label: name,
	})
}
