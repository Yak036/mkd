package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"mkd/internal/agent"
)

// updateAgentDiffKey traduce Y/N/A del modal HITL en acciones del bucle de
// agente. Es el único punto donde se aplica o rechaza una propuesta pendiente.
//
//	Y / Enter  → ejecuta la propuesta y continúa el loop
//	N / Esc    → devuelve "Operación rechazada por el usuario" al modelo
//	A          → aprueba la herramienta para toda la sesión y ejecuta
func (m Model) updateAgentDiffKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	decision, ok := m.modal.diff.decisionForKey(msg)
	proposal := m.modal.diff.proposal
	if !ok || proposal.Tool == "" {
		return m, nil
	}

	// Permiso solicitado por un sub-agente del Swarm: se resuelve en el broker
	// y se sigue escuchando peticiones; no toca el estado del agente nativo.
	if approval := m.modal.diff.swarm; approval != nil {
		return m.resolveSwarmApproval(approval, proposal, decision)
	}

	state := m.ensureAgentState()
	callID := ""
	for id, candidate := range state.proposals {
		if candidate.Tool == proposal.Tool &&
			candidate.Path == proposal.Path &&
			candidate.Command == proposal.Command {
			callID = id
			break
		}
	}

	m.modal.kind = modalNone
	m.modal.diff = diffModal{}
	m.notice = ""
	m.waiting = false
	m.streaming = true

	// Las llamadas MCP usan el mismo modal pero se enrutan al servidor.
	if mcpCall, ok := m.mcpPending[callID]; ok {
		switch decision {
		case ApprovalReject:
			state.messages = append(state.messages, agent.RejectedMessage(callID))
			delete(state.proposals, callID)
			delete(m.mcpPending, callID)
			m.notes = append(m.notes, note{Kind: noteInfo, Text: "✖ rechazado: " + proposal.Tool})
			m.refresh()
			return m.resolveNextProposal()
		case ApprovalAlways:
			state.autoApprove[proposal.Tool] = true
		}
		delete(state.proposals, callID)
		m.notes = append(m.notes, note{Kind: noteInfo, Text: "🔌 MCP ejecutando: " + proposal.Tool})
		m.refresh()
		return m, m.runMCPCallCmd(mcpCall)
	}

	switch decision {
	case ApprovalReject:
		if callID != "" {
			state.messages = append(state.messages, agent.RejectedMessage(callID))
			delete(state.proposals, callID)
		}
		m.notes = append(m.notes, note{Kind: noteInfo, Text: "✖ rechazado: " + proposal.Tool})
		m.refresh()
		return m.resolveNextProposal()

	case ApprovalAlways:
		state.autoApprove[proposal.Tool] = true
		state.decision[callID] = ApprovalAlways
		m.notes = append(m.notes, note{Kind: noteInfo, Text: "◈ " + proposal.Tool + " aprobado para esta sesión"})
		fallthrough

	default:
		if callID != "" {
			delete(state.proposals, callID)
		}
		m.notes = append(m.notes, note{Kind: noteInfo, Text: "✔ aplicando: " + autoSummary(proposal)})
		m.refresh()
		return m, m.runProposalCmd(proposal, callID)
	}
}
