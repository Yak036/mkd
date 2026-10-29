package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/agent"
	"github.com/Yak036/mkd/internal/config"
)

func askUserCall(id string) agent.Call {
	return agent.Call{
		ID:   id,
		Name: agent.ToolAskUser,
		Arguments: `{"question":"¿Qué caché usamos?","header":"Caché","options":[` +
			`{"label":"Memoria"},{"label":"Redis","description":"requiere servicio"}]}`,
	}
}

// ask_user abre el modal y no ejecuta ninguna herramienta ni pide permiso.
func TestResolveOpensQuestionModal(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.ensureAgentState()
	m.agent.startIndex = len(m.history)
	m.agent.active = true
	m.agent.pending = []agent.Call{askUserCall("q1")}

	next, cmd := m.resolveNextProposal()
	if cmd != nil {
		t.Fatal("la pregunta no debe lanzar comandos")
	}
	if next.modal.kind != modalQuestion {
		t.Fatalf("modal = %v, se esperaba modalQuestion", next.modal.kind)
	}
	if next.modal.question.callID != "q1" || len(next.modal.question.question.Options) != 2 {
		t.Fatalf("pregunta mal cargada: %#v", next.modal.question)
	}
	if next.waiting || next.streaming {
		t.Fatal("mientras se espera al usuario no debe haber spiner ni stream")
	}
}

// Elegir por número responde la opción y continúa el turno con role=tool.
func TestQuestionDigitAnswerContinuesTurn(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.ensureAgentState()
	m.agent.startIndex = len(m.history)
	m.agent.active = true
	m.agent.pending = []agent.Call{askUserCall("q1")}
	m, _ = m.resolveNextProposal()
	m.agent.messages = append(m.agent.messages, agent.ResultMessage("other", "previo"))

	next, _ := m.handleQuestionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})

	if next.modal.kind != modalNone {
		t.Fatal("el modal debía cerrarse")
	}
	var toolMsg string
	for _, msg := range next.agent.messages {
		if msg.Role == "tool" && msg.ToolCallID == "q1" {
			toolMsg = msg.Content
		}
	}
	if !strings.Contains(toolMsg, "Redis") {
		t.Fatalf("role=tool sin la opción elegida: %q", toolMsg)
	}
	// El turno arranca sin mensajes previos: la respuesta queda como único
	// mensaje visible del usuario.
	if len(next.history) != 1 {
		t.Fatalf("historial = %d, want 1 (respuesta): %#v", len(next.history), next.history)
	}
	if next.history[0].Role != "user" || !strings.Contains(next.history[0].Display, "Redis") {
		t.Fatalf("la respuesta debe verse como mensaje del usuario: %#v", next.history[0])
	}
}

// Tab lleva al texto libre; escribir y Enter devuelve una respuesta personalizada.
func TestQuestionCustomAnswer(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.ensureAgentState()
	m.agent.startIndex = len(m.history)
	m.agent.active = true
	m.agent.pending = []agent.Call{askUserCall("q1")}
	m, _ = m.resolveNextProposal()

	// Hay 2 opciones + fila de texto libre.
	next, _ := m.handleQuestionKey(tea.KeyMsg{Type: tea.KeyTab})
	if !next.modal.question.custom {
		t.Fatalf("Tab debía enfocar el texto libre: %#v", next.modal.question)
	}
	for _, r := range "SQLite" {
		next, _ = next.handleQuestionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	next, _ = next.handleQuestionKey(tea.KeyMsg{Type: tea.KeyEnter})

	var toolMsg string
	for _, msg := range next.agent.messages {
		if msg.Role == "tool" && msg.ToolCallID == "q1" {
			toolMsg = msg.Content
		}
	}
	if !strings.Contains(toolMsg, "SQLite") {
		t.Fatalf("role=tool sin la respuesta personalizada: %q", toolMsg)
	}
}

// Esc cancela la pregunta y el modelo recibe la cancelación.
func TestQuestionEscCancels(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.ensureAgentState()
	m.agent.startIndex = len(m.history)
	m.agent.active = true
	m.agent.pending = []agent.Call{askUserCall("q1")}
	m, _ = m.resolveNextProposal()

	next, _ := m.handleQuestionKey(tea.KeyMsg{Type: tea.KeyEsc})
	if next.modal.kind != modalNone {
		t.Fatal("Esc debía cerrar la pregunta")
	}
	var toolMsg string
	for _, msg := range next.agent.messages {
		if msg.Role == "tool" && msg.ToolCallID == "q1" {
			toolMsg = msg.Content
		}
	}
	if !strings.Contains(toolMsg, "cancelada") {
		t.Fatalf("role=tool sin cancelación: %q", toolMsg)
	}
}

// Una respuesta que llega después de cancelar el turno no contamina el chat.
func TestQuestionStaleAnswerIsDiscarded(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.ensureAgentState()
	m.agent.startIndex = len(m.history)
	m.agent.active = true
	m.agent.pending = []agent.Call{askUserCall("q1")}
	m, _ = m.resolveNextProposal()

	// El usuario ya empezó otro turno.
	m.history = append(m.history, chatMessage{Role: "user", Content: "otro tema"})

	next, _ := m.handleQuestionKey(tea.KeyMsg{Type: tea.KeyEnter})
	if next.modal.kind != modalNone {
		t.Fatal("la pregunta debía cerrarse")
	}
	for _, msg := range next.history {
		if strings.Contains(msg.Content, "Caché") || strings.Contains(msg.Content, "Memoria") {
			t.Fatalf("la respuesta obsoleta entró al historial: %#v", next.history)
		}
	}
	if len(next.agent.messages) != 0 {
		t.Fatalf("la respuesta obsoleta tocó la ventana del modelo: %#v", next.agent.messages)
	}
}

// Sin opciones, el modal arranca en el campo de texto libre.
func TestQuestionWithoutOptionsStartsCustom(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	q := agent.Question{Question: "¿Nombre del módulo?", AllowCustom: true}
	m, _ = m.openQuestionModal("q1", q)
	if !m.modal.question.custom {
		t.Fatalf("debería arrancar en texto libre: %#v", m.modal.question)
	}
	view := m.questionModalView()
	if !strings.Contains(view, "pregunta") {
		t.Fatalf("vista sin cabecera: %q", view)
	}
}

// Enviar mientras hay un turno abierto no inicia otro turno.
func TestSubmitBlockedWhileBusy(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.streaming = true
	m.textarea.SetValue("mensaje nuevo")

	raw, _ := m.submitInput()
	next := raw.(Model)
	if len(next.history) != 0 {
		t.Fatalf("se inició un turno durante otro: %#v", next.history)
	}
	if !strings.Contains(next.notice, "Espera") {
		t.Fatalf("falta el aviso: %q", next.notice)
	}
}

// Si el usuario cancela con un permiso o pregunta abiertos, el bucle no
// relanza otra ronda del agente.
func TestResolveStopsAfterCancel(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.ensureAgentState()
	m.agent.active = true
	m.agent.rounds = 2
	m.cancelled = true
	m.agent.pending = []agent.Call{
		{ID: "c1", Name: "read_file", Arguments: `{"path":"no-existe.txt"}`},
	}
	next, cmd := m.resolveNextProposal()
	if cmd != nil {
		t.Fatal("no debería lanzar otra ronda tras cancelar")
	}
	if next.agent.active || next.waiting || next.streaming {
		t.Fatalf("estado tras cancelar: active=%v waiting=%v streaming=%v", next.agent.active, next.waiting, next.streaming)
	}
}

// Cancelar el turno cierra también la pregunta abierta.
func TestCancelTurnClosesQuestion(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.ensureAgentState()
	m.agent.pending = []agent.Call{askUserCall("q1")}
	m.agent.startIndex = len(m.history)
	m.agent.active = true
	m, _ = m.resolveNextProposal()

	m.cancelTurn()
	if m.modal.kind != modalNone {
		t.Fatalf("el modal debía cerrarse: %v", m.modal.kind)
	}
	if m.agent.active || len(m.agent.pending) != 0 {
		t.Fatalf("estado del agente sin limpiar: %#v", m.agent)
	}
}

// El modal no debe desbordar el terminal ni perder las teclas de acción.
func TestQuestionModalFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{140, 45}, {100, 28}, {80, 22}} {
		m := mkTestModel(t, t.TempDir(), config.ModeDefault)
		m.width, m.height = size[0], size[1]
		m.layout()
		q := agent.Question{
			Question: "¿Qué estrategia prefieres para el resto del proyecto y por qué?",
			Header:   "Estrategia",
			Options: []agent.QuestionOption{
				{Label: "Opción A", Description: "descripción breve"},
				{Label: "Opción B"},
				{Label: "Opción C"},
				{Label: "Opción D"},
				{Label: "Opción E"},
				{Label: "Opción F"},
			},
			AllowCustom: true,
		}
		m, _ = m.openQuestionModal("q1", q)
		view := m.View()
		if !strings.Contains(view, "cancelar") {
			t.Fatalf("%dx%d: el footer de acciones quedó fuera de pantalla", size[0], size[1])
		}
		if lines := strings.Count(view, "\n") + 1; lines > size[1] {
			t.Fatalf("%dx%d: la vista ocupa %d líneas", size[0], size[1], lines)
		}
	}
}
