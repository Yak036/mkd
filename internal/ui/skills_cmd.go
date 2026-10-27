package ui

// Comandos slash de skills.
//
//	/skills               → lista instaladas
//	/skill add <nombre>   → abre un textarea para escribir el SKILL.md
//	/skill rm <nombre>    → elimina la skill del disco
//	/skills-dir <ruta>    → cambia la carpeta y la persiste en config

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/skills"
)

// listSkills muestra las skills instaladas en la barra de estado.
func (m Model) listSkills() (tea.Model, tea.Cmd) {
	dir := m.skillsDir()
	list, err := skills.Scan(m.cfg.SkillsDir)
	if err != nil {
		m.notice = "No se pudo leer " + dir + ": " + err.Error()
		return m, nil
	}
	if len(list) == 0 {
		m.notice = "No hay skills en " + dir + " · usa /skill add <nombre>"
		return m, nil
	}

	names := make([]string, 0, len(list))
	for _, skill := range list {
		names = append(names, "@"+skill.Name)
	}
	m.notes = append(m.notes, note{
		Kind: noteInfo,
		Text: fmt.Sprintf("✦ %d skills en %s: %s", len(list), dir, strings.Join(names, " · ")),
	})
	m.notice = fmt.Sprintf("%d skills instaladas · invócalas con :@nombre", len(list))
	m.refresh()
	return m, nil
}

// setSkillsDir guarda la nueva carpeta de skills y la crea si hace falta.
func (m Model) setSkillsDir(dir string) (tea.Model, tea.Cmd) {
	dir = skills.ResolveDir(dir)
	if dir == "" {
		m.notice = "Ruta de skills inválida."
		return m, nil
	}
	previous := m.cfg.SkillsDir
	m.cfg.SkillsDir = dir
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		m.cfg.SkillsDir = previous
		m.notice = "No se pudo guardar la carpeta: " + err.Error()
		return m, nil
	}

	list, err := skills.Scan(dir)
	if err != nil {
		m.notice = "Carpeta guardada, pero no se pudo leer: " + err.Error()
		return m, nil
	}
	m.notice = fmt.Sprintf("Carpeta de skills: %s (%d skills)", dir, len(list))
	m.refresh()
	return m, nil
}

// removeSkill elimina una skill del disco.
func (m Model) removeSkill(name string) (tea.Model, tea.Cmd) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	if name == "" {
		m.notice = "Uso: /skill rm <nombre>"
		return m, nil
	}
	if err := skills.Remove(m.cfg.SkillsDir, name); err != nil {
		m.notice = "No se pudo eliminar: " + err.Error()
		return m, nil
	}
	m.notice = "Skill eliminada: @" + name
	m.refresh()
	return m, nil
}

// createSkillPrompt abre el modal de system prompt reciclado como editor de
// SKILL.md: Ctrl+S crea la skill con el nombre indicado.
func (m Model) createSkillPrompt(name string) (tea.Model, tea.Cmd) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	if !skills.ValidName(name) {
		m.notice = "Nombre de skill inválido. Usa letras, números, - _ ."
		return m, nil
	}

	template := "---\nname: " + name + "\ndescription: Describe cuándo usar esta skill\n---\n\n# " + name + "\n\nInstrucciones para el modelo.\n"
	open, cmd := m.openSystemModal()
	editor := open
	editor.modal.system.textarea.SetValue(template)
	editor.modal.system.textarea.CursorEnd()
	editor.modal.system.pendingSkill = name
	editor.notice = "Guardando como skill @" + name + " · Ctrl+S"
	return editor, cmd
}

// saveSkillFromEditor persiste el textarea como SKILL.md.
func (m Model) saveSkillFromEditor(name, content string) (Model, tea.Cmd) {
	skill, err := skills.Overwrite(m.cfg.SkillsDir, name, content)
	if err != nil {
		m.modal.system.err = err.Error()
		return m, nil
	}
	m.modal.kind = modalNone
	m.notice = fmt.Sprintf("Skill creada: @%s (%s)", skill.Name, skill.Path)
	m.refresh()
	return m, m.textarea.Focus()
}
