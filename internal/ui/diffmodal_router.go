package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"mkd/internal/agent"
)

// openDiffModal abre el modal de permiso para una propuesta. autoApprove debe
// apuntar al set de herramientas auto-aprobadas de la sesión (puede ser nil).
func (m Model) openDiffModal(proposal agent.Proposal, autoApprove *bool) (Model, tea.Cmd) {
	m.hideSlashMenu()
	m.modal.kind = modalDiff
	m.modal.diff = diffModal{
		visible:     true,
		proposal:    proposal,
		autoApprove: autoApprove,
	}
	m.textarea.Blur()
	m.resizeModals()
	return m, nil
}

// closeDiffModal cierra el permiso sin tocar el estado del agente.
func (m Model) closeDiffModal() (Model, tea.Cmd) {
	m.modal.kind = modalNone
	m.modal.diff = diffModal{}
	m.refresh()
	return m, m.textarea.Focus()
}

// updateDiffModalKey procesa una tecla y devuelve la decisión cuando aplica.
// La decisión final (ejecutar + continuar el loop) la resuelve la capa de
// agente en workspace.go, que conoce la propuesta pendiente.
func (m Model) updateDiffModalKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	decision, ok := m.modal.diff.decisionForKey(msg)
	if !ok {
		return m, nil
	}
	switch decision {
	case ApprovalAlways:
		// Recordar la herramienta y aplicar en el mismo paso.
		if m.modal.diff.autoApprove != nil {
			*m.modal.diff.autoApprove = true
		}
		fallthrough
	case ApprovalApply:
		// El loop de agente consume m.modal.diff a través de esta nota.
		m.notice = "Permiso concedido: " + m.modal.diff.proposal.Tool
		return m.closeDiffModal()
	default:
		m.notice = "Permiso rechazado: " + m.modal.diff.proposal.Tool
		return m.closeDiffModal()
	}
}
