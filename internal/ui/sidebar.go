package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Yak036/mkd/internal/config"
)

// ContextFile representa un archivo anclado a la sesión con su hash SHA-256
// del último ensamblado (para detectar cambios en disco).
type ContextFile struct {
	Path string
	Size int64
	Hash string
	// Skill marca las entradas que provienen de `:@nombre` y no de un archivo.
	Skill bool
	// Label es el nombre de la skill para mostrarlo en el sidebar.
	Label string
}

func (m Model) sidebarView(width, height int) string {
	if width < 26 || height < 5 {
		return ""
	}

	innerWidth := max(10, width-4)
	provider, ok := m.cfg.Active()
	if !ok {
		provider = config.ProviderConfig{Name: "Sin configurar"}
	}

	lines := make([]string, 0, 40)
	add := func(line string) {
		lines = append(lines, ansi.Truncate(line, innerWidth, "…"))
	}
	separator := func() {
		add(mutedStyle.Render(strings.Repeat("─", innerWidth)))
	}

	add(sectionTitle("󰒓", "PROVEEDOR ACTIVO"))
	add(sidebarKV("Endpoint", valueOr(provider.Name, "sin configurar"), innerWidth))
	add(sidebarKV("Base URL", shortEndpoint(provider.BaseURL), innerWidth))
	add(sidebarKV("API Key", config.MaskKey(provider.APIKey), innerWidth))
	add(sidebarKV("Modelo", valueOr(provider.ActiveModel, "(sin modelo)"), innerWidth))
	add(sidebarKV("Guardados", fmt.Sprintf("%d modelos", len(provider.Models)), innerWidth))

	state := "○ sin configurar"
	stateStyle := warnStyle
	if provider.Configured() {
		state = "● operativo"
		stateStyle = successStyle
	}
	add(stateStyle.Render(state))

	separator()
	add(sectionTitle("󰈙", "ARCHIVOS EN CONTEXTO"))
	if len(m.contextFiles) == 0 {
		add(mutedStyle.Render("  (ninguno) · usa /add <ruta>"))
	} else {
		files := m.ctxMgr.Files()
		for i, file := range m.contextFiles {
			status := successStyle.Render("✓")
			label := formatBytes(file.Size)
			nameStyle := textStyle
			switch {
			case file.Skill:
				status = skillStyle.Render("✦")
				nameStyle = skillStyle
				label = "skill"
			case i < len(files) && files[i].Err != "":
				status = errorStyle.Render("!")
				label = "sin acceso"
			case i < len(files) && files[i].Hash != "" && file.Hash != "" && files[i].Hash != file.Hash:
				status = warnStyle.Render("↻")
				label = "cambiado"
			}
			add(fmt.Sprintf("  %s %s %s",
				status,
				nameStyle.Render(oneLine(displayFileName(file), max(8, innerWidth-14))),
				mutedStyle.Render("("+label+")"),
			))
		}
	}

	// Swarm Mission Control: árbol de progreso en vivo durante una misión.
	if m.swarm.active() {
		separator()
		for _, line := range m.swarmSidebarLines(innerWidth) {
			if strings.TrimSpace(ansi.Strip(line)) == "" {
				continue
			}
			add(line)
		}
	}

	if m.mcp != nil {
		servers := m.mcp.Statuses()
		if len(servers) > 0 {
			separator()
			add(sectionTitle("🔌", "SERVIDORES MCP"))
			for _, server := range servers {
				dot := offDotStyle.Render("○")
				detail := "Off"
				switch {
				case server.Online:
					dot = activeDotStyle.Render("●")
					detail = fmt.Sprintf("%d tools", len(server.Tools))
				case server.Err != "":
					dot = errorStyle.Render("!")
					detail = "error"
				}
				add(fmt.Sprintf("  %s %s %s",
					dot,
					textStyle.Render(oneLine(server.Name, max(8, innerWidth-16))),
					mutedStyle.Render("("+detail+")"),
				))
			}
		}
	}

	separator()
	add(sectionTitle("󰄨", "TELEMETRÍA"))
	add(sidebarKV("Msjs", fmt.Sprintf("%d", len(m.history)), innerWidth))
	m.addTokenBar(&add, innerWidth)
	add(sidebarKV("Sistema", formatTokens(m.usage.SystemTokens), innerWidth))
	add(sidebarKV("Archivos", formatTokens(m.usage.FileTokens), innerWidth))
	if m.summary != "" {
		add(sidebarKV("Resumen", formatTokens(m.usage.SummaryTokens), innerWidth))
	}
	add(sidebarKV("Historial", formatTokens(m.usage.HistoryTokens), innerWidth))

	separator()
	add(sectionTitle("󰋚", "SESIÓN"))
	add(sidebarKV("Título", valueOr(m.session.Title, "(sin título)"), innerWidth))
	add(sidebarKV("ID", valueOr(shortSessionID(m.session.ID), "—"), innerWidth))

	add("")
	add(mutedStyle.Render("Ctrl+B ocultar · Ctrl+H sesiones"))
	add(mutedStyle.Render("Ctrl+G commit · Ctrl+O MCP · Ctrl+T agents"))

	content := strings.Join(lines, "\n")
	return panelStyle.
		Width(max(10, width-2)).
		Height(max(3, height-2)).
		Render(content)
}

// addTokenBar dibuja una barra de uso del contexto frente al umbral.
func (m Model) addTokenBar(add *func(string), innerWidth int) {
	usage := m.usage
	threshold := m.cfg.CompactThreshold

	ratio := 0.0
	if threshold > 0 {
		ratio = float64(usage.TotalTokens) / float64(threshold)
	}

	barWidth := max(6, innerWidth-12)
	filled := int(ratio * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}

	fillStyle := successStyle
	if ratio >= 1 {
		fillStyle = errorStyle
	} else if ratio >= 0.75 {
		fillStyle = warnStyle
	}

	bar := fillStyle.Render(strings.Repeat("█", filled)) +
		mutedStyle.Render(strings.Repeat("░", barWidth-filled))
	percent := fmt.Sprintf("%3.0f%%", min(999, ratio*100))

	(*add)(mutedStyle.Render(fmt.Sprintf("%-9s", "Uso")) + " " + bar + " " + textStyle.Render(percent))
	(*add)(mutedStyle.Render(fmt.Sprintf("%-9s", "Tokens")) + " " +
		textStyle.Render(formatTokens(usage.TotalTokens)) +
		mutedStyle.Render(" / "+formatTokens(threshold)))
}

// tokenBarColor expone el color de la barra para reutilizarlo en el header.
func (m Model) tokenRatio() float64 {
	if m.cfg.CompactThreshold <= 0 {
		return 0
	}
	return float64(m.usage.TotalTokens) / float64(m.cfg.CompactThreshold)
}

func sidebarKV(label, value string, width int) string {
	labelWidth := 9
	labelText := mutedStyle.Render(fmt.Sprintf("%-*s", labelWidth, label))
	valueWidth := max(4, width-labelWidth-1)
	return labelText + " " + textStyle.Render(oneLine(value, valueWidth))
}

func shortEndpoint(baseURL string) string {
	if strings.TrimSpace(baseURL) == "" {
		return "(sin configurar)"
	}
	value := strings.TrimPrefix(baseURL, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimSuffix(value, "/")
	return value
}

// displayFileName usa el nombre de la skill cuando el contexto es una skill y
// el nombre base del archivo en el resto de casos.
func displayFileName(file ContextFile) string {
	if file.Skill && strings.TrimSpace(file.Label) != "" {
		return ":@" + file.Label
	}
	return fileName(file.Path)
}

func fileName(path string) string {
	if index := strings.LastIndexAny(path, `/\`); index >= 0 {
		return path[index+1:]
	}
	return path
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// shortSessionID recorta el ID de sesión para caber en el sidebar.
func shortSessionID(id string) string {
	if len(id) <= 18 {
		return id
	}
	return id[len(id)-18:]
}
