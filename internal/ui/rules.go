package ui

// Reglas del proyecto/globales en el popup `:@` y su gestión.
//
//	<proyecto>/.mkd/rules/*.md     ─┐
//	<sub>/.mkd/rules/*.md           ├─► mencionRuleMatches ─► popup `:@`
//	~/.config/mkd/rules/*.md       ─┘         │
//	                                          ▼
//	                     Enter → fuerza la regla para el turno
//
// Las reglas con `alwaysApply` o `globs` se inyectan solas; aquí solo se
// listan las disponibles y se permite forzar una concreta.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/rules"
)

// applyRulesConfig propaga la carpeta global y las reglas forzadas al
// ContextManager (que es quien las inyecta en el system prompt).
func (m *Model) applyRulesConfig() {
	if m.ctxMgr == nil {
		return
	}
	m.ctxMgr.SetRulesDir(m.cfg.RulesDir)
	m.ctxMgr.SetManualRules(m.agent.manualRules)
}

// loadRules descubre las reglas del proyecto y las globales.
func (m Model) loadRules() ([]rules.Rule, error) {
	return rules.Load(m.cfg.RulesDir, m.agentCwd())
}

// mentionRuleMatches lista reglas para el popup `:@`.
func (m *Model) mentionRuleMatches(query string) []mentionItem {
	list, err := m.loadRules()
	if err != nil {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(query))

	items := make([]mentionItem, 0, len(list))
	for _, rule := range list {
		if lower != "" && !mentionSpecialMatches(lower, rule.Name) {
			continue
		}
		state := "auto"
		switch {
		case rule.AlwaysApply:
			state = "siempre"
		case len(rule.Globs) > 0:
			state = "globs"
		default:
			state = "manual"
		}
		description := fmt.Sprintf("regla · %s · %s", state, oneLine(rule.Description, 48))
		if m.ruleActive(rule.Name) {
			description = "✓ forzada · " + description
		}
		items = append(items, mentionItem{
			Token:       ":@" + rule.Name,
			Label:       "@" + rule.Name,
			Description: description,
			Rule:        true,
		})
	}
	return items
}

// ruleActive indica si la regla está forzada manualmente en esta sesión.
func (m Model) ruleActive(name string) bool {
	for _, item := range m.agent.manualRules {
		if strings.EqualFold(item, name) {
			return true
		}
	}
	return false
}

// applyRule fuerza una regla para la sesión actual.
func (m *Model) applyRule(name string) (string, bool) {
	list, err := m.loadRules()
	if err != nil {
		return "No se pudieron leer las reglas: " + err.Error(), false
	}
	rule, ok := rules.Find(list, name)
	if !ok {
		return "Regla no encontrada: " + name, false
	}

	for _, existing := range m.agent.manualRules {
		if strings.EqualFold(existing, rule.Name) {
			return "Regla ya forzada: @" + rule.Name, true
		}
	}
	m.agent.manualRules = append(m.agent.manualRules, rule.Name)
	if m.ctxMgr != nil {
		m.ctxMgr.SetManualRules(m.agent.manualRules)
	}
	return "Regla forzada: @" + rule.Name, true
}

// clearRules quita todas las reglas forzadas manualmente.
func (m *Model) clearRules() {
	m.agent.manualRules = nil
	if m.ctxMgr != nil {
		m.ctxMgr.SetManualRules(nil)
	}
}

// listRules muestra las reglas disponibles y sus disparadores.
func (m Model) listRules() (tea.Model, tea.Cmd) {
	list, err := m.loadRules()
	if err != nil {
		m.notice = "No se pudieron leer las reglas: " + err.Error()
		return m, nil
	}
	if len(list) == 0 {
		m.notice = "No hay reglas · crea .mkd/rules/<nombre>.md o usa /rule add <nombre>"
		return m, nil
	}

	lines := make([]string, 0, len(list))
	for _, rule := range list {
		mode := "manual"
		switch {
		case rule.AlwaysApply:
			mode = "siempre"
		case len(rule.Globs) > 0:
			mode = "globs: " + strings.Join(rule.Globs, ",")
		}
		lines = append(lines, fmt.Sprintf("@%s [%s]", rule.Name, mode))
	}
	m.notes = append(m.notes, note{
		Kind: noteInfo,
		Text: fmt.Sprintf("⚖ %d reglas: %s", len(list), strings.Join(lines, " · ")),
	})
	m.notice = fmt.Sprintf("%d reglas · fuerza una con :@nombre", len(list))
	m.refresh()
	return m, nil
}

// removeRuleForced deja de forzar una regla.
func (m Model) removeRuleForced(name string) (tea.Model, tea.Cmd) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	if name == "" {
		m.notice = "Uso: /rule rm <nombre>"
		return m, nil
	}
	filtered := m.agent.manualRules[:0]
	found := false
	for _, item := range m.agent.manualRules {
		if strings.EqualFold(item, name) {
			found = true
			continue
		}
		filtered = append(filtered, item)
	}
	if !found {
		m.notice = "La regla no estaba forzada: @" + name
		return m, nil
	}
	m.agent.manualRules = filtered
	if m.ctxMgr != nil {
		m.ctxMgr.SetManualRules(m.agent.manualRules)
	}
	m.notice = "Regla liberada: @" + name
	m.refresh()
	return m, nil
}

// setRulesDir cambia la carpeta global de reglas y la persiste.
func (m Model) setRulesDir(dir string) (tea.Model, tea.Cmd) {
	dir = rules.ResolveDir(dir)
	if dir == "" {
		m.notice = "Ruta de reglas inválida."
		return m, nil
	}
	previous := m.cfg.RulesDir
	m.cfg.RulesDir = dir
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		m.cfg.RulesDir = previous
		m.notice = "No se pudo guardar la carpeta de reglas: " + err.Error()
		return m, nil
	}
	if m.ctxMgr != nil {
		m.ctxMgr.SetRulesDir(dir)
	}
	m.notice = "Carpeta global de reglas: " + dir
	m.refresh()
	return m, nil
}
