package ui

// Swarm Mission Control: pestañas de inspección, tarjetas de delegación y
// árbol de progreso.
//
//	Header
//	╭──────────────────────────────────────────────────────────────╮
//	│ [0: 🟣 Orquestador] [1: 🟢 explorer ✓] [2: ⠋ coder (…) ] [3: ⏳ qa] │ ← tab bar
//	├──────────────────────────────────────────────────────────────┤
//	│  (contenido de la pestaña activa)                            │
//	╰──────────────────────────────────────────────────────────────╯
//
// Sidebar:
//	󰚩 SWARM MISSION CONTROL
//	├─ 🟣 Orquestador: Esperando sub-agentes...
//	├─ 🟢 [explorer] Completado (3.2s │ 1.4k tok)
//	│  └─ ✓ search_code("MCPServerConfig")
//	└─ ⠋ [coder] Ejecutando tool...
//	   └─ 🔧 patch_file(internal/mcp/client.go)
//
// Las pestañas se cambian con Alt+0..9; la pestaña 0 es el chat del Orquestador.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"mkd/internal/config"
	"mkd/internal/swarm"
)

// swarmTabBar renderiza la barra de pestañas de agentes.
func (m Model) swarmTabBar(width int) string {
	st := m.swarm
	if !st.active() {
		return ""
	}

	width = max(10, width)
	chips := make([]string, 0, len(st.order)+1)
	chips = append(chips, m.tabChip(0, "0", "Orquestador", "#A855F7", m.viewTab == 0, statusGlyph(st.running, true), ""))

	for _, live := range st.orderedAgents() {
		if live.Index <= 0 || live.Index > 9 {
			continue
		}
		glyph := statusGlyph(live.Status == swarm.StatusRunning || live.Status == swarm.StatusQueued || live.Status == swarm.StatusWaitingApproval, live.Status == swarm.StatusDone)
		chips = append(chips, m.tabChip(live.Index, itoa(live.Index), live.ID, live.Color, m.viewTab == live.Index, glyph, swarmTabState(live)))
	}

	line := strings.Join(chips, " ")
	line = ansi.Truncate(line, width, "…")
	return line
}

// tabChip pinta una pestaña individual.
func (m Model) tabChip(index int, label, name, color string, active bool, glyph, state string) string {
	base := lipglossStyleForeground(color)
	text := fmt.Sprintf("%s: %s %s", label, glyph, name)
	if state != "" {
		text += " " + state
	}

	if active {
		return lipgloss.NewStyle().
			Background(lipglossColor(color)).
			Foreground(lipgloss.Color("#FAFAFA")).
			Bold(true).
			Padding(0, 1).
			Render(text)
	}
	return base.Copy().Padding(0, 1).Render(text)
}

// swarmTabState devuelve la etiqueta corta de estado de un agente.
func swarmTabState(live *agentLive) string {
	switch live.Status {
	case swarm.StatusDone:
		return "✓"
	case swarm.StatusError:
		return "✗"
	case swarm.StatusWaitingApproval:
		return "⏸"
	case swarm.StatusRunning:
		return "(…)"
	default:
		return "⏳"
	}
}

// statusGlyph devuelve el glifo del árbol de progreso.
func statusGlyph(animating, done bool) string {
	switch {
	case done:
		return "🟢"
	case animating:
		return "⠋"
	default:
		return "⏳"
	}
}

// swarmAgentColor devuelve el color del agente (o cian por defecto).
func swarmAgentColor(live *agentLive) string {
	if live == nil {
		return "#06B6D4"
	}
	return config.NormalizeAgentColor(live.Color)
}

// ---------------------------------------------------------------------------
// Sidebar: árbol de progreso en vivo
// ---------------------------------------------------------------------------

// swarmSidebarLines devuelve las líneas del bloque SWARM MISSION CONTROL.
func (m Model) swarmSidebarLines(innerWidth int) []string {
	st := m.swarm
	if !st.active() {
		return nil
	}

	lines := []string{
		titleStyle.Render("󰚩 SWARM MISSION CONTROL"),
	}

	// Nodo orquestador.
	orchLine := "├─ " + lipglossStyleForeground("#A855F7").Render("🟣 Orquestador") + ": "
	status := st.orchestratorStatus
	if status == "" {
		status = "Planificando…"
	}
	if st.running && st.runningCount() > 0 {
		orchLine += mutedStyle.Render("Esperando sub-agentes…")
	} else if !st.running && st.finished.IsZero() == false {
		orchLine += successStyle.Render("Misión completada")
	} else {
		orchLine += mutedStyle.Render(oneLine(status, max(8, innerWidth-20)))
	}
	lines = append(lines, ansi.Truncate(orchLine, innerWidth, "…"))

	agents := st.orderedAgents()
	for i, live := range agents {
		last := i == len(agents)-1
		branch := "├─ "
		childBranch := "│  └─ "
		if last {
			branch = "└─ "
			childBranch = "   └─ "
		}

		color := swarmAgentColor(live)
		style := lipglossStyleForeground(color)
		icon := valueOr(live.Icon, "󰀐")
		head := branch + style.Render(icon+" ["+live.ID+"]")

		switch live.Status {
		case swarm.StatusDone:
			meta := formatSwarmDuration(live.Duration) + " │ " + formatTokens(live.Tokens) + " tok"
			head += successStyle.Render(" Completado") + mutedStyle.Render(" ("+meta+")")
		case swarm.StatusError:
			head += errorStyle.Render(" Error")
		case swarm.StatusWaitingApproval:
			head += warnStyle.Render(" Esperando permiso…")
		case swarm.StatusRunning:
			head += mutedStyle.Render(" Ejecutando…")
		default:
			head += mutedStyle.Render(" En cola: " + oneLine(live.Task, max(6, innerWidth-24)))
		}
		lines = append(lines, ansi.Truncate(head, innerWidth, "…"))

		// Última acción del agente.
		detail := ""
		switch {
		case live.Status == swarm.StatusRunning && live.Tool != "":
			detail = "🔧 " + live.Tool + "(" + oneLine(live.Args, max(6, innerWidth-22)) + ")"
		case live.Status == swarm.StatusDone && len(live.Steps) > 0:
			step := live.Steps[len(live.Steps)-1]
			mark := "✓"
			if step.Error {
				mark = "✗"
			}
			detail = mark + " " + step.Tool + "(" + oneLine(step.Args, max(6, innerWidth-22)) + ")"
		case live.Status == swarm.StatusWaitingApproval:
			detail = "🛡️ " + live.Tool + " (permiso)"
		case live.LastLine != "":
			detail = oneLine(live.LastLine, max(6, innerWidth-16))
		}
		if detail != "" {
			lines = append(lines, ansi.Truncate(mutedStyle.Render(childBranch+detail), innerWidth, "…"))
		}
	}

	return lines
}

// ---------------------------------------------------------------------------
// Vista central por pestaña
// ---------------------------------------------------------------------------

// swarmTabContent renderiza el contenido de la pestaña activa.
func (m Model) swarmTabContent(width, height int) string {
	st := m.swarm
	if !st.active() {
		return ""
	}

	// Pestaña 0 (o sin sub-agentes): chat del orquestador, que ya lo pinta el
	// viewport normal; aquí solo devolvemos vacío para no duplicarlo.
	if m.viewTab == 0 {
		return ""
	}

	live, ok := st.agentByTab(m.viewTab)
	if !ok {
		return mutedStyle.Render("(pestaña sin agente activo)")
	}

	return m.renderAgentInspector(live, width, height)
}

// renderAgentInspector pinta el stream en vivo de un sub-agente.
func (m Model) renderAgentInspector(live *agentLive, width, height int) string {
	var b strings.Builder
	color := swarmAgentColor(live)
	style := lipglossStyleForeground(color)

	header := style.Render(valueOr(live.Icon, "󰀐")+" ["+live.ID+"]") + " " +
		textStyle.Render(valueOr(live.Name, live.ID))
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Tarea: ") + textStyle.Render(oneLine(live.Task, max(10, width-8))))
	b.WriteString("\n")
	if live.Status == swarm.StatusDone {
		b.WriteString(successStyle.Render("● completado") +
			mutedStyle.Render(fmt.Sprintf(" · %s · %s tok", formatSwarmDuration(live.Duration), formatTokens(live.Tokens))))
	} else if live.Status == swarm.StatusError {
		b.WriteString(errorStyle.Render("✗ error: " + oneLine(live.Err, max(10, width-10))))
	} else {
		b.WriteString(mutedStyle.Render("● " + live.Status.String()))
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", max(10, width))))
	b.WriteString("\n")

	// Log de pasos.
	var body []string
	for _, step := range live.Steps {
		mark := "🔧"
		if step.Done {
			mark = "✓"
		}
		if step.Error {
			mark = "✗"
		}
		body = append(body, mutedStyle.Render(mark+" "+step.Tool+"("+oneLine(step.Args, max(8, width-10))+")"))
	}

	// Streaming interno.
	output := strings.TrimSpace(live.Output.String())
	if output != "" {
		if len(body) > 0 {
			body = append(body, "")
		}
		for _, line := range strings.Split(output, "\n") {
			body = append(body, m.renderMarkdown(line))
		}
	}
	if len(body) == 0 {
		body = append(body, mutedStyle.Render("(esperando salida del sub-agente…)"))
	}

	maxLines := max(3, height-6)
	if len(body) > maxLines {
		body = body[len(body)-maxLines:]
	}
	b.WriteString(strings.Join(body, "\n"))

	return assistantBoxStyle.Width(max(20, width-2)).Render(b.String())
}

// delegationCard pinta la tarjeta de una delegación en el chat del Orquestador.
func (m Model) delegationCard(ev delegationInfo) string {
	profile, _ := swarm.FindProfile(m.cfg.SubAgents, ev.agentID)
	name := valueOr(profile.Name, ev.agentID)
	color := config.NormalizeAgentColor(profile.Color)
	icon := valueOr(profile.Icon, "󰀐")

	width := max(20, m.conversationWidth-2)

	var b strings.Builder
	b.WriteString(lipglossStyleForeground(color).Render(icon+" "+ev.agentID) +
		mutedStyle.Render(" · "+name))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Tarea: ") + textStyle.Render(oneLine(ev.task, max(10, width-8))))

	if ev.tool != "" {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("🔧 ") + textStyle.Render(ev.tool+"("+oneLine(ev.args, max(8, width-12))+")"))
	}

	if summary := strings.TrimSpace(ev.summary); summary != "" {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(strings.Repeat("─", max(10, width-4))))
		b.WriteString("\n")
		detail := truncateMiddle(summary, 1400)
		b.WriteString(m.renderMarkdown(detail))
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipglossColor(color)).
		Padding(0, 1).
		Width(width)
	return box.Render(b.String())
}

// swarmStatusPill devuelve la píldora del modo para la barra de estado.
func (m Model) swarmStatusPill() string {
	if config.NormalizeSwarmMode(m.cfg.SwarmMode) == config.SwarmModeSwarm {
		return lipgloss.NewStyle().
			Background(lipgloss.Color("#A855F7")).
			Foreground(lipgloss.Color("#FAFAFA")).
			Bold(true).
			Padding(0, 1).
			Render("SWARM ORCHESTRATOR")
	}
	return mutedStyle.Render("SINGLE AGENT")
}

// formatSwarmDuration acorta una duración (3.2s, 1m04s).
func formatSwarmDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	seconds := d.Seconds()
	if seconds < 60 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	minutes := int(seconds) / 60
	rest := int(seconds) % 60
	return fmt.Sprintf("%dm%02ds", minutes, rest)
}

// delegationInfo alimenta las tarjetas de delegación del chat.
type delegationInfo struct {
	agentID string
	task    string
	tool    string
	args    string
	summary string
}
