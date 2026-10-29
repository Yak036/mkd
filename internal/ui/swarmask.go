package ui

// Preguntas del Orquestador Swarm.
//
//	runOrchestratorCmd (goroutine)
//	        │ ask_user
//	        ▼
//	askUserBridge.Request ──chan──► TUI abre modalQuestion
//	        │                              │
//	   (bloqueado) ◄──Resolve──────── respuesta o cancelación

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/agent"
)

// askRequest es una pregunta del orquestador en espera de la TUI.
type askRequest struct {
	question agent.Question
	reply    chan askReply
}

type askReply struct {
	answer    string
	cancelled bool
}

// askUserBridge serializa las preguntas del orquestador hacia la TUI. Igual
// que el HITLBroker, solo una pregunta está visible a la vez.
type askUserBridge struct {
	mu       sync.Mutex
	requests chan askRequest
}

// newAskUserBridge crea el puente con un canal acotado.
func newAskUserBridge() *askUserBridge {
	return &askUserBridge{requests: make(chan askRequest, 4)}
}

// Requests expone el canal que consume la TUI.
func (b *askUserBridge) Requests() <-chan askRequest {
	return b.requests
}

// Ask bloquea la goroutine del orquestador hasta que la TUI responde o la
// misión se cancela.
func (b *askUserBridge) Ask(ctx context.Context, question agent.Question) (string, bool) {
	if b == nil {
		return "", true
	}
	reply := make(chan askReply, 1)
	select {
	case b.requests <- askRequest{question: question, reply: reply}:
	case <-ctx.Done():
		return "", true
	}
	select {
	case value := <-reply:
		return value.answer, value.cancelled
	case <-ctx.Done():
		return "", true
	}
}

// askUserFromSwarm adapta el puente al contrato del orquestador.
func askUserFromSwarm(bridge *askUserBridge) func(context.Context, agent.Question) (string, bool) {
	if bridge == nil {
		return nil
	}
	return bridge.Ask
}

// waitSwarmAsk devuelve un tea.Cmd que espera la siguiente pregunta.
func waitSwarmAsk(requests <-chan askRequest, gen int64) tea.Cmd {
	return func() tea.Msg {
		request, ok := <-requests
		if !ok {
			return nil
		}
		return swarmAskMsg{gen: gen, request: request}
	}
}

// swarmAskMsg llega cuando el orquestador hace una pregunta.
type swarmAskMsg struct {
	gen     int64
	request askRequest
}

// handleSwarmAsk abre el modal de preguntas para el orquestador.
func (m Model) handleSwarmAsk(msg swarmAskMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.swarm.runGen || m.bus == nil {
		// Pregunta de una misión vieja: se descarta y se sigue escuchando.
		select {
		case msg.request.reply <- askReply{cancelled: true}:
		default:
		}
		return m, waitSwarmAsk(m.swarmAskRequests(), m.swarm.runGen)
	}

	m.swarm.orchestratorStatus = "Esperando respuesta del usuario…"
	m.modal.kind = modalQuestion
	qm := questionModal{
		question: msg.request.question,
		swarm:    &msg.request,
		// Sin callID: la respuesta va al canal del orquestador.
	}
	if qm.question.AllowCustom && len(qm.question.Options) == 0 {
		qm.custom = true
		qm.focusCustom = true
	}
	m.modal.question = qm
	m.textarea.Blur()
	m.resizeModals()
	m.refresh()
	return m, nil
}

// swarmAskRequests devuelve el canal de preguntas de la misión actual.
func (m Model) swarmAskRequests() <-chan askRequest {
	if m.bus == nil {
		return nil
	}
	return m.bus.ask.Requests()
}
