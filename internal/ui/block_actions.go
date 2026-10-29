package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/clipboard"
	"github.com/Yak036/mkd/internal/config"
)

// copyLastBlock implementa Ctrl+Y / /copy: extrae el último bloque de código
// de las respuestas del asistente y lo copia al portapapeles. Si no hay bloques
// de código, copia la última respuesta en texto plano como fallback razonable.
func (m Model) copyLastBlock() (tea.Model, tea.Cmd) {
	blocks := m.conversationBlocks()
	if len(blocks) == 0 {
		return m.copyLastResponse()
	}

	block, ok := lastCodeBlock(blocks)
	if !ok {
		return m.copyLastResponse()
	}

	note, err := copyBlock(block)
	if err != nil {
		m.notice = "No se pudo copiar: " + err.Error()
		return m, nil
	}
	m.notes = append(m.notes, noteText(noteSuccess, note))
	m.notice = note
	m.refresh()
	return m, nil
}

// copyLastPrompt copia el texto del último prompt enviado por el usuario al portapapeles.
func (m Model) copyLastPrompt() (tea.Model, tea.Cmd) {
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].Role == ai.RoleUser {
			text := m.history[i].Content
			if text == "" {
				text = m.history[i].Display
			}
			if err := clipboard.Copy(text); err != nil {
				m.notice = "No se pudo copiar el prompt: " + err.Error()
				return m, nil
			}
			m.notice = "✓ Prompt copiado al portapapeles"
			return m, nil
		}
	}
	m.notice = "No hay prompts enviados en la conversación."
	return m, nil
}

// copyLastResponse copia el texto plano de la última respuesta del asistente al portapapeles.
func (m Model) copyLastResponse() (tea.Model, tea.Cmd) {
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].Role == ai.RoleAssistant {
			text := m.history[i].Content
			if text == "" {
				text = m.history[i].Display
			}
			if err := clipboard.Copy(text); err != nil {
				m.notice = "No se pudo copiar la respuesta: " + err.Error()
				return m, nil
			}
			m.notice = "✓ Respuesta copiada al portapapeles"
			return m, nil
		}
	}
	m.notice = "No hay respuestas del asistente en la conversación."
	return m, nil
}

// conversationBlocks extrae bloques de todos los mensajes del asistente.
func (m Model) conversationBlocks() []CodeBlock {
	contents := make([]string, 0, len(m.history))
	for _, msg := range m.history {
		contents = append(contents, msg.Content)
	}
	return ExtractCodeBlocks(contents)
}

// cycleAgentMode rota chat → default → auto → plan → chat y persiste.
func (m Model) cycleAgentMode() (tea.Model, tea.Cmd) {
	m.cfg.AgentMode = config.NextMode(m.cfg.AgentMode)
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		m.notice = "No se pudo guardar el modo: " + err.Error()
		return m, nil
	}
	m.notice = "Modo agente: " + config.ModeLabel(m.cfg.AgentMode)
	m.refresh()
	return m, nil
}

// setAgentMode fija un modo concreto (usado por /mode <plan|default|auto|chat>).
func (m *Model) setAgentMode(mode string) bool {
	normalized := config.NormalizeMode(mode)
	if strings.EqualFold(normalized, config.ModeDefault) && !strings.EqualFold(mode, config.ModeDefault) {
		return false
	}
	m.cfg.AgentMode = normalized
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		m.notice = "No se pudo guardar el modo: " + err.Error()
		return false
	}
	m.notice = "Modo agente: " + config.ModeLabel(normalized)
	return true
}

// noteText construye una nota tipada para el bloque inferior del chat.
func noteText(kind noteKind, text string) note { return note{Kind: kind, Text: text} }
