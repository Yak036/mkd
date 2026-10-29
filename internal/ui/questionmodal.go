package ui

// Modal de pregunta interactiva (ask_user).
//
//	┌─ 🗨  MKD pregunta ────────────────────────────────┐
//	│  ¿Qué estrategia de caché prefieres?              │
//	│                                                    │
//	│  ❯ 1. Memoria en proceso                           │
//	│       simple, se pierde al reiniciar               │
//	│    2. Redis                                        │
//	│       sobrevive a reinicios, requiere servicio     │
//	│                                                    │
//	│  ✎ o escribe tu propia respuesta…                  │
//	│                                                    │
//	│  ↑/↓ elegir · 1-6 directo · Enter responder        │
//	│  Tab respuesta libre · Esc cancelar                 │
//	└────────────────────────────────────────────────────┘

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Yak036/mkd/internal/agent"
	"github.com/Yak036/mkd/internal/ai"
)

// customRowOffset marca el índice virtual de la fila de respuesta libre.
const customRowOffset = 100

type questionModal struct {
	question agent.Question
	callID   string
	// index es la fila seleccionada: 0..n-1 opciones, customRowOffset texto libre.
	index int
	// custom indica que el foco está en el campo de texto libre.
	custom bool
	// input es el texto libre escrito por el usuario.
	input []rune
	// focusCustom fuerza mostrar el campo de texto aunque haya opciones.
	focusCustom bool
	// swarm, si no es nil, indica que la pregunta viene del Orquestador.
	swarm *askRequest
}

// rows devuelve el número de filas navegables (opciones + texto libre).
func (qm questionModal) rows() int {
	count := len(qm.question.Options)
	if qm.question.AllowCustom || count == 0 {
		count++
	}
	return count
}

// isCustomRow indica si la fila i corresponde a la respuesta libre.
func (qm questionModal) isCustomRow(i int) bool {
	return i == qm.rows()-1 && (qm.question.AllowCustom || len(qm.question.Options) == 0)
}

// move desplaza la selección con wrap-around.
func (qm questionModal) move(delta int) questionModal {
	total := qm.rows()
	if total == 0 {
		return qm
	}
	qm.index = (qm.index + delta + total) % total
	qm.custom = qm.isCustomRow(qm.index)
	qm.focusCustom = qm.custom
	return qm
}

// answerForKey traduce una tecla en una respuesta. Es puro para poder testear
// el modal sin montar la TUI.
//
//	↑/↓, k/j, tab   → navegar
//	1..6            → elegir opción directa
//	enter           → opción seleccionada o texto libre
//	esc             → cancelar
func (qm questionModal) answerForKey(msg tea.KeyMsg) (agent.Answer, bool) {
	switch msg.String() {
	case "up", "k", "shift+tab":
		qm = qm.move(-1)
		return agent.Answer{}, false
	case "down", "j", "tab":
		qm = qm.move(1)
		return agent.Answer{}, false
	case "esc", "ctrl+c":
		return agent.Answer{CallID: qm.callID, Cancelled: true}, true
	}

	if digit, ok := optionDigit(msg.String()); ok && digit < len(qm.question.Options) {
		return agent.Answer{CallID: qm.callID, Label: qm.question.Options[digit].Label}, true
	}

	if msg.String() != "enter" {
		return agent.Answer{}, false
	}
	if qm.custom {
		text := strings.TrimSpace(string(qm.input))
		if text == "" {
			return agent.Answer{}, false
		}
		return agent.Answer{CallID: qm.callID, Text: text}, true
	}
	if qm.index >= 0 && qm.index < len(qm.question.Options) {
		return agent.Answer{CallID: qm.callID, Label: qm.question.Options[qm.index].Label}, true
	}
	return agent.Answer{}, false
}

// optionDigit convierte "1".."6" en un índice 0-based.
func optionDigit(key string) (int, bool) {
	if len(key) != 1 || key[0] < '1' || key[0] > '6' {
		return 0, false
	}
	return int(key[0] - '1'), true
}

// openQuestionModal muestra la pregunta y deja el foco en la primera opción.
func (m Model) openQuestionModal(callID string, question agent.Question) (Model, tea.Cmd) {
	m.hideSlashMenu()
	m.modal.kind = modalQuestion
	qm := questionModal{question: question, callID: callID}
	if qm.question.AllowCustom && len(qm.question.Options) == 0 {
		qm.index = 0
		qm.custom = true
		qm.focusCustom = true
	}
	m.modal.question = qm
	m.textarea.Blur()
	m.resizeModals()
	return m, nil
}

// handleQuestionKey procesa una tecla del modal. Devuelve la respuesta cuando
// el usuario la confirma; el bucle del agente resuelve el resto.
func (m Model) handleQuestionKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	qm := m.modal.question

	// Edición del texto libre.
	if qm.custom {
		switch msg.String() {
		case "enter":
			return m.answerQuestion(qm)
		case "backspace", "ctrl+h":
			if len(qm.input) > 0 {
				qm.input = qm.input[:len(qm.input)-1]
			}
			m.modal.question = qm
			return m, nil
		case "esc", "ctrl+c":
			return m.finishQuestion(agent.Answer{CallID: qm.callID, Cancelled: true})
		case "ctrl+u":
			qm.input = nil
			m.modal.question = qm
			return m, nil
		case "up", "shift+tab":
			m.modal.question = qm.move(-1)
			return m, nil
		case "down":
			m.modal.question = qm.move(1)
			return m, nil
		}
		// Dígitos directos a una opción, salvo que el texto libre vaya vacío:
		// entonces el usuario está escribiendo una respuesta libre.
		if len(qm.input) > 0 {
			if digit, ok := optionDigit(msg.String()); ok && digit < len(qm.question.Options) {
				return m.finishQuestion(agent.Answer{CallID: qm.callID, Label: qm.question.Options[digit].Label})
			}
		}
		switch msg.Type {
		case tea.KeyRunes:
			qm.input = append(qm.input, msg.Runes...)
		case tea.KeySpace:
			qm.input = append(qm.input, ' ')
		default:
			return m, nil
		}
		m.modal.question = qm
		return m, nil
	}

	next := qm
	switch msg.String() {
	case "up", "k", "shift+tab":
		next = qm.move(-1)
	case "down", "j":
		next = qm.move(1)
	case "tab":
		// Tab salta directo a la respuesta libre: es la acción más frecuente
		// cuando ninguna opción convence.
		next = questionModal{question: qm.question, callID: qm.callID, index: qm.rows() - 1}
		next.custom = true
		next.focusCustom = true
	default:
		answer, ok := qm.answerForKey(msg)
		if ok {
			return m.finishQuestion(answer)
		}
		return m, nil
	}
	m.modal.question = next
	return m, nil
}

// answerQuestion confirma el texto libre actual.
func (m Model) answerQuestion(qm questionModal) (Model, tea.Cmd) {
	answer, ok := questionModal{question: qm.question, callID: qm.callID, index: qm.index, custom: true, input: qm.input}.answerForKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !ok {
		m.notice = "Escribe una respuesta o elige una opción."
		return m, nil
	}
	return m.finishQuestion(answer)
}

// questionModalView pinta el modal completo.
//
// Se renderiza en dos partes: cuerpo (pregunta + filas) y footer de teclas. El
// cuerpo se recorta para que el footer siempre sea visible en terminales
// pequeñas, igual que en el editor de sub-agentes.
func (m Model) questionModalView() string {
	qm := m.modal.question
	width := m.modalContentWidth()

	var body strings.Builder
	header := "🗨  MKD pregunta"
	if qm.question.Header != "" {
		header = "🗨  " + qm.question.Header
	}
	if qm.swarm != nil {
		header += "  · Swarm"
	}
	body.WriteString(titleStyle.Render(header))
	body.WriteString("\n")
	body.WriteString(mutedStyle.Render(strings.Repeat("─", width)))
	body.WriteString("\n")
	body.WriteString(textStyle.Render(wrapLine(qm.question.Question, width)))
	body.WriteString("\n\n")

	customButton := " ✎ Escribir otra respuesta…"
	customSelected := qm.isCustomRow(qm.index)
	for i, option := range qm.question.Options {
		row := "  " + itoa(i+1) + ". " + option.Label
		if i == qm.index && !qm.custom {
			body.WriteString(selectedRowStyle.Render(fitWidth(" ❯ "+itoa(i+1)+". "+option.Label, width)))
		} else {
			body.WriteString(textStyle.Render(row))
		}
		body.WriteString("\n")
		if option.Description != "" {
			body.WriteString(mutedStyle.Render(wrapLine("     "+option.Description, width)))
			body.WriteString("\n")
		}
	}

	if qm.question.AllowCustom || len(qm.question.Options) == 0 {
		body.WriteString("\n")
		value := string(qm.input)
		switch {
		case qm.custom && value != "":
			body.WriteString(selectedRowStyle.Render(fitWidth(" ✎ "+value+"▍", width)))
		case qm.custom:
			body.WriteString(selectedRowStyle.Render(fitWidth(" ✎ "+mutedStyle.Render("escribe tu respuesta…")+"▍", width)))
		case customSelected:
			body.WriteString(selectedRowStyle.Render(fitWidth(customButton, width)))
		default:
			body.WriteString(mutedStyle.Render(customButton))
		}
		body.WriteString("\n")
	}

	footer := questionFooter(width)

	return m.modalBoxWithFooter(body.String(), footer)
}

// questionFooter elige el hint de teclas que cabe en el ancho del modal.
func questionFooter(width int) string {
	full := diffHintStyle.Render("[↑/↓]") + textStyle.Render(" elegir  ") +
		diffHintStyle.Render("[1-6]") + textStyle.Render(" directo  ") +
		diffHintStyle.Render("[Enter]") + textStyle.Render(" responder  ") +
		diffHintStyle.Render("[Tab]") + textStyle.Render(" libre  ") +
		diffHintStyle.Render("[Esc]") + textStyle.Render(" cancelar")
	short := diffHintStyle.Render("[↑/↓]") + textStyle.Render(" elegir  ") +
		diffHintStyle.Render("[Enter]") + textStyle.Render(" responder  ") +
		diffHintStyle.Render("[Esc]") + textStyle.Render(" cancelar")
	if lipgloss.Width(full) <= width {
		return full
	}
	return short
}

// wrapLine parte un texto en una sola cadena con saltos de línea.
func wrapLine(value string, width int) string {
	if width <= 0 {
		return value
	}
	var b strings.Builder
	line := ""
	for _, word := range strings.Fields(value) {
		if lipgloss.Width(line)+lipgloss.Width(word)+1 > width && line != "" {
			b.WriteString(line)
			b.WriteString("\n")
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	b.WriteString(line)
	return b.String()
}

// finishQuestion entrega la respuesta al agente como role=tool y continúa el
// turno exactamente donde quedó.
func (m Model) finishQuestion(answer agent.Answer) (Model, tea.Cmd) {
	qm := m.modal.question
	m.modal.kind = modalNone
	m.modal.question = questionModal{}

	// Pregunta del Orquestador Swarm: se resuelve en su canal y la misión
	// continúa; no toca el historial del agente nativo.
	if qm.swarm != nil {
		reply := askReply{cancelled: answer.Cancelled}
		if !answer.Cancelled {
			reply.answer = answer.Value()
		}
		select {
		case qm.swarm.reply <- reply:
		default:
		}
		if m.swarm.running {
			m.swarm.orchestratorStatus = "Reanudando la misión…"
		}
		m.refresh()
		return m, waitSwarmAsk(m.swarmAskRequests(), m.swarm.runGen)
	}

	content := agent.FormatAnswer(qm.question, answer)
	state := m.ensureAgentState()

	// El turno ya no es el último (el usuario envió otro mensaje o cambió de
	// sesión): la respuesta llega tarde y no debe tocar la conversación.
	stale := !m.turnIsCurrent() || !state.active

	decorated := chatMessage{
		Role:    ai.RoleUser,
		Content: content,
		Display: "↳ respuesta: " + oneLine(questionAnswerLabel(answer), 90),
	}
	if stale {
		m.notes = append(m.notes, note{Kind: noteError, Text: "La pregunta quedó obsoleta: el turno ya no está activo."})
		m.refresh()
		return m, m.textarea.Focus()
	}

	state.messages = append(state.messages, agent.ResultMessage(qm.callID, content))
	m.logTurnMessage(decorated)
	m.notes = append(m.notes, note{Kind: noteSuccess, Text: "✓ respuesta enviada: " + oneLine(questionAnswerLabel(answer), 60)})

	m.waiting = false
	m.streaming = true
	m.refresh()
	m.viewport.GotoBottom()
	return m.resolveNextProposal()
}

// questionAnswerLabel resume la respuesta para el historial o el aviso.
func questionAnswerLabel(answer agent.Answer) string {
	if answer.Cancelled {
		return "Pregunta cancelada"
	}
	if value := answer.Value(); value != "" {
		return value
	}
	return "Sin contenido"
}

// cancelPendingQuestion cierra la pregunta abierta y la reporta al modelo.
func (m Model) cancelPendingQuestion(reason string) (Model, tea.Cmd) {
	if m.modal.kind != modalQuestion {
		return m, nil
	}
	answer := agent.Answer{CallID: m.modal.question.callID, Cancelled: true}
	if reason != "" {
		m.notice = reason
	}
	return m.finishQuestion(answer)
}
