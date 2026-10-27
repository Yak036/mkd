package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Yak036/mkd/internal/agent"
	"github.com/Yak036/mkd/internal/swarm"
)

// Colores del diff: verde neón para añadidos, rojo para eliminados.
const (
	hexDiffAddBg    = "#052E16"
	hexDiffRemoveBg = "#450A0A"
)

var (
	diffAddStyle = lipgloss.NewStyle().
			Foreground(green).
			Background(lipgloss.Color(hexDiffAddBg))
	diffRemoveStyle = lipgloss.NewStyle().
			Foreground(danger).
			Background(lipgloss.Color(hexDiffRemoveBg))
	diffContextStyle = lipgloss.NewStyle().Foreground(muted)
	diffNoStyle      = lipgloss.NewStyle().Foreground(muted)
	diffHintStyle    = lipgloss.NewStyle().Foreground(cyan).Bold(true)
)

// approvalDecision es la respuesta del usuario al permiso de agente.
type approvalDecision int

const (
	// ApprovalApply aplica el cambio una vez.
	ApprovalApply approvalDecision = iota
	// ApprovalReject descarta el cambio.
	ApprovalReject
	// ApprovalAlways aplica y recuerda la herramienta para esta sesión.
	ApprovalAlways
)

// diffModal encapsula el estado del permiso de agente. autoApprove apunta al
// set de herramientas auto-aprobadas de la sesión; si es nil la preferencia
// "siempre" no persiste (modo vista previa).
type diffModal struct {
	visible     bool
	proposal    agent.Proposal
	autoApprove *bool
	status      string
	// swarm, si no es nil, indica que el permiso lo pide un sub-agente.
	swarm *swarmApproval
}

// swarmApproval conecta el modal HITL con el broker del swarm.
type swarmApproval struct {
	gen       int64
	requestID string
	agentID   string
	agentName string
	broker    *swarm.HITLBroker
}

// decisionForKey traduce una tecla en una decisión. Es puro: no toca estado,
// lo que permite testear el modal sin montar la TUI completa.
//
//	y / Y / enter        → ApprovalApply
//	n / N / esc          → ApprovalReject
//	a / A                → ApprovalAlways
func (dm diffModal) decisionForKey(msg tea.KeyMsg) (approvalDecision, bool) {
	switch msg.String() {
	case "y", "Y", "enter":
		return ApprovalApply, true
	case "n", "N", "esc":
		return ApprovalReject, true
	case "a", "A":
		return ApprovalAlways, true
	default:
		return ApprovalReject, false
	}
}

// diffTitle arma la cabecera según la acción propuesta.
func (dm diffModal) title() string {
	if dm.swarm != nil {
		return "🛡️ Sub-Agente [" + dm.swarm.agentID + "] solicita " + dm.proposal.Tool
	}
	return "🛡️ Permiso de Agente Requerido: " + dm.proposal.Tool
}

// diffTarget arma la línea de destino: archivo con contadores o comando.
func (dm diffModal) target() string {
	if dm.proposal.Action == agent.ActionRun {
		return "Comando: " + oneLineWarning(dm.proposal.Command)
	}
	return fmt.Sprintf("Archivo: %s (+%d / -%d líneas)",
		dm.proposal.Path, dm.proposal.Adds, dm.proposal.Removes)
}

// diffLinesView pinta una línea del diff con números viejo/nuevo y prefijo.
//
//	viejo  nuevo │  texto
//	  12      -  │ - línea eliminada
//	   -     13  │ + línea añadida
//	  14     14  │   contexto
func (dm diffModal) diffLinesView(maxRows, width int) string {
	lines := dm.proposal.Diff
	if len(lines) > maxRows {
		// Los diffs largos muestran el inicio y avisan del recorte.
		lines = lines[:maxRows]
	}

	var b strings.Builder
	for _, line := range lines {
		b.WriteString(renderDiffLine(line, width))
		b.WriteString("\n")
	}
	if len(dm.proposal.Diff) > maxRows {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("… %d líneas más", len(dm.proposal.Diff)-maxRows)))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderDiffLine pinta una sola línea del diff con colores y numeración.
func renderDiffLine(line agent.DiffLine, width int) string {
	oldNo := "     "
	if line.OldNo > 0 {
		oldNo = fmt.Sprintf("%5d", line.OldNo)
	}
	newNo := "     "
	if line.NewNo > 0 {
		newNo = fmt.Sprintf("%5d", line.NewNo)
	}

	body := " " + line.Kind.String() + " " + line.Text
	// Ancho disponible tras numeración (5+1+5+2 espacios = 13).
	bodyWidth := max(1, width-13)
	body = ansi.Truncate(body, bodyWidth, "…")

	switch line.Kind {
	case agent.DiffAdd:
		return diffNoStyle.Render(oldNo+" "+newNo) + " " + diffAddStyle.Render(fitWidth(body, bodyWidth))
	case agent.DiffRemove:
		return diffNoStyle.Render(oldNo+" "+newNo) + " " + diffRemoveStyle.Render(fitWidth(body, bodyWidth))
	default:
		return diffNoStyle.Render(oldNo+" "+newNo) + " " + diffContextStyle.Render(fitWidth(body, bodyWidth))
	}
}

// diffModalView renderiza el modal completo dentro de la caja estándar.
func (m Model) diffModalView() string {
	dm := m.modal.diff
	width := m.modalContentWidth()

	var b strings.Builder
	b.WriteString(titleStyle.Render(dm.title()))
	b.WriteString("\n")
	b.WriteString(textStyle.Render(dm.target()))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", width)))
	b.WriteString("\n")

	if dm.proposal.Action == agent.ActionRun {
		b.WriteString(mutedStyle.Render("Comando de shell ejecutado en el directorio del proyecto:"))
		b.WriteString("\n")
		b.WriteString(textStyle.Render("$ " + oneLineWarning(dm.proposal.Command)))
		b.WriteString("\n")
	} else {
		// Reservar espacio fijo para el footer (4 líneas) y la cabecera.
		footerLines := 4
		maxRows := max(3, m.modalContentHeight()-9-footerLines)
		b.WriteString(dm.diffLinesView(maxRows, width))
	}

	if dm.status != "" {
		b.WriteString("\n")
		b.WriteString(warnStyle.Render(dm.status))
	}
	b.WriteString("\n\n")

	b.WriteString(diffHintStyle.Render("[Y / Enter]"))
	b.WriteString(textStyle.Render(" Aplicar cambio   "))
	b.WriteString(diffHintStyle.Render("[N / Esc]"))
	b.WriteString(textStyle.Render(" Rechazar"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("[A] Permitir siempre en esta sesión"))

	return m.modalBox(b.String())
}

// fitWidth rellena el body para que el fondo de color cubra todo el ancho.
func fitWidth(value string, width int) string {
	if pad := width - lipgloss.Width(value); pad > 0 {
		return value + strings.Repeat(" ", pad)
	}
	return value
}

// oneLineWarning deja el comando en una línea para las cabeceras.
func oneLineWarning(command string) string {
	command = strings.ReplaceAll(command, "\n", " ")
	return strings.TrimSpace(command)
}

// diffModalKeys lista las teclas del modal para la barra de estado.
var diffModalKeys = []key.Binding{
	key.NewBinding(key.WithKeys("y", "enter"), key.WithHelp("Y/Enter", "aplicar")),
	key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("N/Esc", "rechazar")),
	key.NewBinding(key.WithKeys("a"), key.WithHelp("A", "permitir siempre")),
}
