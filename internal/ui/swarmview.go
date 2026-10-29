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

	"github.com/Yak036/mkd/internal/config"
	"github.com/Yak036/mkd/internal/swarm"
)

// swarmTabBar renderiza la barra de pestañas de agentes.
// Garantiza que la pestaña activa permanezca visible y proporciona indicadores de
// desbordamiento (◀ / ▶) cuando la cantidad de agentes excede el ancho de la terminal.
func (m Model) swarmTabBar(width int) string {
	st := m.swarm
	if !st.active() {
		return ""
	}

	width = max(10, width)
	agents := st.orderedAgents()
	chips := make([]string, 0, len(agents)+1)

	// Pestaña 0: Orquestador
	chips = append(chips, m.tabChip(0, "0", "Orquestador", "#A855F7", m.viewTab == 0, statusGlyph(st.running, true), ""))
	activeIdx := 0

	for _, live := range agents {
		if live == nil || live.Index <= 0 {
			continue
		}
		if m.viewTab == live.Index {
			activeIdx = len(chips)
		}
		glyph := statusGlyph(live.Status == swarm.StatusRunning || live.Status == swarm.StatusQueued || live.Status == swarm.StatusWaitingApproval, live.Status == swarm.StatusDone)
		chips = append(chips, m.tabChip(live.Index, itoa(live.Index), live.ID, live.Color, m.viewTab == live.Index, glyph, swarmTabState(live)))
	}

	return renderTabBarWithActiveVisible(chips, activeIdx, width)
}

// renderTabBarWithActiveVisible ajusta las pestañas al ancho disponible asegurando
// que la pestaña activa (activeIdx) sea siempre visible e incluyendo glifos de navegación
// por desbordamiento (◀ / ▶) cuando existan pestañas adicionales ocultas.
func renderTabBarWithActiveVisible(chips []string, activeIdx, width int) string {
	if len(chips) == 0 {
		return ""
	}
	if activeIdx < 0 || activeIdx >= len(chips) {
		activeIdx = 0
	}

	chipWidths := make([]int, len(chips))
	totalWidth := 0
	for i, c := range chips {
		w := ansi.StringWidth(c)
		chipWidths[i] = w
		totalWidth += w
		if i > 0 {
			totalWidth++ // espacio entre chips
		}
	}

	// Si todas las pestañas caben completas, mostrarlas sin truncamiento
	if totalWidth <= width {
		return strings.Join(chips, " ")
	}

	// Helper para calcular ancho total con indicadores de desbordamiento
	computeRange := func(s, e int) (int, string, string) {
		leftStr := ""
		leftW := 0
		if s > 0 {
			leftStr = fmt.Sprintf("◀ +%d ", s)
			leftW = ansi.StringWidth(leftStr)
		}
		rightStr := ""
		rightW := 0
		if e < len(chips)-1 {
			rightStr = fmt.Sprintf(" +%d ▶", len(chips)-1-e)
			rightW = ansi.StringWidth(rightStr)
		}
		contentW := 0
		for i := s; i <= e; i++ {
			contentW += chipWidths[i]
			if i > s {
				contentW++
			}
		}
		total := leftW + contentW + rightW

		// Si con indicadores detallados excede el ancho, intentar con indicadores compactos
		if total > width && (s > 0 || e < len(chips)-1) {
			cLeftStr := ""
			cLeftW := 0
			if s > 0 {
				cLeftStr = "◀ "
				cLeftW = 2
			}
			cRightStr := ""
			cRightW := 0
			if e < len(chips)-1 {
				cRightStr = " ▶"
				cRightW = 2
			}
			cTotal := cLeftW + contentW + cRightW
			if cTotal <= width {
				return cTotal, cLeftStr, cRightStr
			}
		}
		return total, leftStr, rightStr
	}

	start := activeIdx
	end := activeIdx

	// Si ni siquiera el chip activo cabe en el ancho, truncarlo directamente
	if chipWidths[activeIdx] >= width {
		return ansi.Truncate(chips[activeIdx], width, "…")
	}

	w, leftStr, rightStr := computeRange(start, end)
	if w > width {
		// No caben los indicadores junto al chip activo; mostrar solo el chip activo
		return ansi.Truncate(chips[activeIdx], width, "…")
	}

	// Expandir iterativamente hacia ambos lados manteniendo el foco en el tab activo
	for {
		canExpandLeft := start > 0
		canExpandRight := end < len(chips)-1
		if !canExpandLeft && !canExpandRight {
			break
		}

		expandRight := false
		expandLeft := false
		if canExpandLeft && canExpandRight {
			distLeft := activeIdx - start
			distRight := end - activeIdx
			if distRight <= distLeft {
				expandRight = true
			} else {
				expandLeft = true
			}
		} else if canExpandRight {
			expandRight = true
		} else {
			expandLeft = true
		}

		expanded := false
		if expandRight {
			newW, nLeft, nRight := computeRange(start, end+1)
			if newW <= width {
				end++
				w, leftStr, rightStr = newW, nLeft, nRight
				expanded = true
			} else if canExpandLeft {
				newW2, nLeft2, nRight2 := computeRange(start-1, end)
				if newW2 <= width {
					start--
					w, leftStr, rightStr = newW2, nLeft2, nRight2
					expanded = true
				}
			}
		} else if expandLeft {
			newW, nLeft, nRight := computeRange(start-1, end)
			if newW <= width {
				start--
				w, leftStr, rightStr = newW, nLeft, nRight
				expanded = true
			} else if canExpandRight {
				newW2, nLeft2, nRight2 := computeRange(start, end+1)
				if newW2 <= width {
					end++
					w, leftStr, rightStr = newW2, nLeft2, nRight2
					expanded = true
				}
			}
		}

		if !expanded {
			break
		}
	}

	overflowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF")).Bold(true)
	var b strings.Builder
	if leftStr != "" {
		b.WriteString(overflowStyle.Render(leftStr))
	}
	for i := start; i <= end; i++ {
		if i > start {
			b.WriteString(" ")
		}
		b.WriteString(chips[i])
	}
	if rightStr != "" {
		b.WriteString(overflowStyle.Render(rightStr))
	}

	return ansi.Truncate(b.String(), width, "")
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

// renderAgentInspector pinta el stream en vivo de un sub-agente con soporte de scroll.
func (m Model) renderAgentInspector(live *agentLive, width, height int) string {
	var b strings.Builder
	boxWidth := max(20, width-2)
	color := swarmAgentColor(live)
	style := lipglossStyleForeground(color)

	tabBadge := mutedStyle.Render(fmt.Sprintf(" (Pestaña %d · Alt+←/→)", live.Index))
	header := style.Render(valueOr(live.Icon, "󰀐")+" ["+live.ID+"]") + " " +
		textStyle.Render(valueOr(live.Name, live.ID)) + " " + tabBadge
	taskLine := mutedStyle.Render("Tarea: ") + textStyle.Render(oneLine(live.Task, max(10, boxWidth-8)))

	var statusLine string
	if live.Status == swarm.StatusDone {
		statusLine = successStyle.Render("● completado") +
			mutedStyle.Render(fmt.Sprintf(" · %s · %s tok", formatSwarmDuration(live.Duration), formatTokens(live.Tokens)))
	} else if live.Status == swarm.StatusError {
		statusLine = errorStyle.Render("✗ error: " + oneLine(live.Err, max(10, boxWidth-10)))
	} else {
		statusLine = mutedStyle.Render("● " + live.Status.String())
	}

	// Cuerpo con instrucciones, pasos y salida en vivo.
	var body []string
	if strings.TrimSpace(live.Instructions) != "" {
		body = append(body, titleCyanStyle.Render("📋 Orden del Orquestador:"))
		rendered := m.renderMarkdown(strings.TrimSpace(live.Instructions))
		for _, line := range strings.Split(rendered, "\n") {
			body = append(body, line)
		}
		body = append(body, mutedStyle.Render(strings.Repeat("─", max(10, boxWidth-4))))
	}

	// Log de pasos ejecutados por el sub-agente.
	for _, step := range live.Steps {
		mark := "🔧"
		if step.Done {
			mark = "✓"
		}
		if step.Error {
			mark = "✗"
		}
		body = append(body, mutedStyle.Render(mark+" "+formatActionDescription(step.Tool, step.Args)))
	}

	// Acción actual en caliente o estado de espera.
	if live.Status == swarm.StatusRunning {
		if live.Tool != "" {
			body = append(body, m.spinner.View()+" "+textStyle.Render(formatActionDescription(live.Tool, live.Args)))
		} else {
			body = append(body, m.spinner.View()+" "+mutedStyle.Render("Pensando y generando respuesta…"))
		}
	} else if live.Status == swarm.StatusWaitingApproval {
		body = append(body, warnStyle.Render("⏸ Esperando aprobación HITL para: "+formatActionDescription(live.Tool, live.Args)))
	}

	// Streaming interno y salida acumulada del sub-agente.
	output := strings.TrimSpace(live.Output.String())
	if output != "" {
		if len(body) > 0 {
			body = append(body, "")
		}
		renderedOut := m.renderMarkdown(output)
		for _, line := range strings.Split(renderedOut, "\n") {
			body = append(body, line)
		}
	}
	if len(body) == 0 {
		body = append(body, mutedStyle.Render("(esperando salida del sub-agente…)"))
	}

	// Cálculo del área visible y recorte según el scroll activo.
	availLines := max(1, height-6)
	totalLines := len(body)
	visibleBody := body

	var sepText string
	if totalLines > availLines {
		maxScroll := totalLines - availLines
		scroll := m.agentScroll
		if scroll < 0 {
			scroll = 0
		}
		if scroll > maxScroll {
			scroll = maxScroll
		}
		start := totalLines - availLines - scroll
		end := start + availLines
		if start < 0 {
			start = 0
		}
		if end > totalLines {
			end = totalLines
		}
		visibleBody = body[start:end]

		if scroll > 0 {
			sepText = fmt.Sprintf("─── [▲ +%d arriba · scroll activo · End abajo] ───", scroll)
		} else {
			sepText = fmt.Sprintf("─── [▲ %d líneas · PgUp/Rueda scroll] ───", totalLines)
		}
	} else {
		sepText = strings.Repeat("─", max(10, boxWidth-4))
	}

	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(taskLine)
	b.WriteString("\n")
	b.WriteString(statusLine)
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(sepText))
	b.WriteString("\n")
	b.WriteString(strings.Join(visibleBody, "\n"))

	return assistantBoxStyle.Width(boxWidth).Render(b.String())
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

// formatActionDescription describe de forma legible y humana la acción de una herramienta.
func formatActionDescription(tool, args string) string {
	tool = strings.TrimSpace(tool)
	args = strings.TrimSpace(args)
	cleanArgs := args
	if strings.HasPrefix(args, "{") {
		cleanArgs = summarizeArgs(args)
	}
	cleanArgs = oneLine(cleanArgs, 60)

	switch {
	case tool == "patch_file":
		return fmt.Sprintf("🔧 Editando archivo: %s", cleanArgs)
	case tool == "write_file":
		return fmt.Sprintf("📝 Escribiendo archivo: %s", cleanArgs)
	case tool == "read_file":
		return fmt.Sprintf("📖 Leyendo archivo: %s", cleanArgs)
	case tool == "search_code":
		return fmt.Sprintf("🔍 Buscando en código: %s", cleanArgs)
	case tool == "list_dir":
		return fmt.Sprintf("📂 Listando directorio: %s", cleanArgs)
	case tool == "run_command":
		return fmt.Sprintf("⚡ Ejecutando comando: %s", cleanArgs)
	case strings.HasPrefix(tool, "mcp__"):
		trimmed := strings.TrimPrefix(tool, "mcp__")
		parts := strings.SplitN(trimmed, "__", 2)
		server := "mcp"
		toolName := trimmed
		if len(parts) == 2 {
			server = parts[0]
			toolName = parts[1]
		}
		if cleanArgs != "" && cleanArgs != "{}" {
			return fmt.Sprintf("🔌 MCP [%s]: %s(%s)", server, toolName, cleanArgs)
		}
		return fmt.Sprintf("🔌 MCP [%s]: %s", server, toolName)
	case tool != "":
		if cleanArgs != "" && cleanArgs != "{}" {
			return fmt.Sprintf("⚙️ %s(%s)", tool, cleanArgs)
		}
		return fmt.Sprintf("⚙️ %s", tool)
	default:
		return "⠋ Pensando y diseñando solución…"
	}
}

// swarmStreamingBlock construye el panel en vivo de la misión para la vista de conversación principal.
func (m Model) swarmStreamingBlock(width int) string {
	boxWidth := max(24, width-2)
	st := m.swarm

	var b strings.Builder
	// Header de la tarjeta con spinner animado
	orchColor := "#A855F7"
	statusText := valueOr(st.orchestratorStatus, "Planificando la misión…")
	header := lipglossStyleForeground(orchColor).Render("󰚩 MKD SWARM MISSION") + " " +
		m.spinner.View() + " " + mutedStyle.Render(statusText)
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", boxWidth-2)))
	b.WriteString("\n")

	agents := st.orderedAgents()
	if len(agents) == 0 {
		b.WriteString(mutedStyle.Render("  ⠋ El orquestador está analizando la instrucción y organizando al equipo…\n"))
	} else {
		for _, live := range agents {
			color := swarmAgentColor(live)
			style := lipglossStyleForeground(color)
			icon := valueOr(live.Icon, "󰀐")
			agentTitle := style.Render(fmt.Sprintf("%s [%s]", icon, live.ID)) + " " + textStyle.Render(valueOr(live.Name, live.ID))

			// Indicador según estado
			switch live.Status {
			case swarm.StatusRunning:
				action := formatActionDescription(live.Tool, live.Args)
				b.WriteString(fmt.Sprintf("  ● %s\n", agentTitle))
				b.WriteString(fmt.Sprintf("    %s %s\n", m.spinner.View(), textStyle.Render(action)))
				if live.Task != "" {
					b.WriteString(fmt.Sprintf("    %s\n", mutedStyle.Render("↳ Tarea: "+oneLine(live.Task, boxWidth-12))))
				}
			case swarm.StatusWaitingApproval:
				b.WriteString(fmt.Sprintf("  ⏸ %s %s\n", agentTitle, warnStyle.Render("Esperando tu aprobación")))
				b.WriteString(fmt.Sprintf("    🛡️ %s\n", warnStyle.Render(formatActionDescription(live.Tool, live.Args))))
			case swarm.StatusDone:
				meta := fmt.Sprintf("%s · %s tok", formatSwarmDuration(live.Duration), formatTokens(live.Tokens))
				b.WriteString(fmt.Sprintf("  ✓ %s %s\n", agentTitle, successStyle.Render("Completado ("+meta+")")))
				if len(live.Steps) > 0 {
					last := live.Steps[len(live.Steps)-1]
					b.WriteString(fmt.Sprintf("    %s\n", mutedStyle.Render("↳ Último paso: "+formatActionDescription(last.Tool, last.Args))))
				}
			case swarm.StatusError:
				b.WriteString(fmt.Sprintf("  ✗ %s %s\n", agentTitle, errorStyle.Render("Falló")))
				b.WriteString(fmt.Sprintf("    %s\n", errorStyle.Render("↳ Error: "+oneLine(live.Err, boxWidth-12))))
			default:
				b.WriteString(fmt.Sprintf("  ⏳ %s %s\n", agentTitle, mutedStyle.Render("En cola de ejecución")))
				if live.Task != "" {
					b.WriteString(fmt.Sprintf("    %s\n", mutedStyle.Render("↳ Tarea: "+oneLine(live.Task, boxWidth-12))))
				}
			}
			b.WriteString("\n")
		}
	}

	// Pistas de atajos de teclado en el pie
	b.WriteString(mutedStyle.Render(strings.Repeat("─", boxWidth-2)))
	b.WriteString("\n")
	hints := "Alt+0 Orquestador │ Alt+1..9 Inspector de agente │ Esc Cancelar misión"
	if len(agents) > 9 {
		hints = "Alt+0 Orquestador │ Alt+1..9 Inspector │ Alt+←/→ Más agentes (10+) │ Esc Cancelar"
	} else if len(agents) > 0 {
		hints = fmt.Sprintf("Alt+0 Orquestador │ Alt+1..%d Ver sub-agente │ Alt+←/→ Navegar │ Esc Cancelar misión", len(agents))
	}
	b.WriteString(mutedStyle.Render("💡 " + hints))

	box := assistantBoxStyle.Width(boxWidth)
	return box.Render(b.String())
}
