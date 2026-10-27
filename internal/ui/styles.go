// Package ui implementa el workspace TUI de MKD.
//
// Identidad visual: cyberpunk retro-futurista oscuro con acentos púrpura
// neón, cian, verde matriz y gris carbón.
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	hexPurple  = "#A855F7"
	hexCyan    = "#06B6D4"
	hexGreen   = "#10B981"
	hexSurface = "#18181B"
	hexPanel   = "#1F1F23"
	hexBorder  = "#3F3F46"
	hexText    = "#E4E4E7"
	hexMuted   = "#71717A"
	hexError   = "#EF4444"
	hexWarn    = "#F59E0B"
	hexWhite   = "#FAFAFA"
)

var (
	purple  = lipgloss.Color(hexPurple)
	cyan    = lipgloss.Color(hexCyan)
	green   = lipgloss.Color(hexGreen)
	surface = lipgloss.Color(hexSurface)
	panel   = lipgloss.Color(hexPanel)
	border  = lipgloss.Color(hexBorder)
	text    = lipgloss.Color(hexText)
	muted   = lipgloss.Color(hexMuted)
	danger  = lipgloss.Color(hexError)
	warn    = lipgloss.Color(hexWarn)
	white   = lipgloss.Color(hexWhite)
)

var (
	brandStyle = lipgloss.NewStyle().
			Foreground(purple).
			Background(surface).
			Bold(true)

	titleStyle = lipgloss.NewStyle().
			Foreground(purple).
			Bold(true)

	titleCyanStyle = lipgloss.NewStyle().
			Foreground(cyan).
			Bold(true)

	mutedStyle = lipgloss.NewStyle().
			Foreground(muted)

	textStyle = lipgloss.NewStyle().
			Foreground(text)

	successStyle = lipgloss.NewStyle().
			Foreground(green).
			Bold(true)

	warnStyle = lipgloss.NewStyle().
			Foreground(warn).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(danger).
			Bold(true)

	labelStyle = lipgloss.NewStyle().
			Foreground(muted).
			Width(14)

	labelFocusStyle = lipgloss.NewStyle().
			Foreground(cyan).
			Bold(true).
			Width(14)

	frameStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Background(surface)

	headerStyle = lipgloss.NewStyle().
			Background(surface).
			Foreground(text).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
			Background(panel).
			Foreground(text).
			Padding(0, 1)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Background(surface).
			Padding(0, 1)

	userBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(purple).
			Padding(0, 1)

	assistantBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(cyan).
				Padding(0, 1)

	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(purple).
			Padding(0, 1)

	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(purple).
			Background(surface).
			Padding(1, 2)

	selectedRowStyle = lipgloss.NewStyle().
				Foreground(white).
				Background(lipgloss.Color("#3B0764"))

	activeDotStyle = lipgloss.NewStyle().Foreground(green).Bold(true)
	offDotStyle    = lipgloss.NewStyle().Foreground(muted)

	// skillStyle distingue las skills de los archivos anclados en el sidebar.
	skillStyle = lipgloss.NewStyle().Foreground(warn).Bold(true)

	// ruleStyle distingue las reglas en el popup `:@`.
	ruleStyle = lipgloss.NewStyle().Foreground(cyan).Bold(true)

	// addedStyle / removedStyle son el feedback de "selección confirmada".
	addedStyle = lipgloss.NewStyle().
			Foreground(green).
			Background(lipgloss.Color("#052E16")).
			Bold(true)
)

// fitLines recorta y rellena un bloque ANSI para que ocupe exactamente
// width x height celdas. Es la base del layout full-screen.
func fitLines(block string, width, height int) string {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}

	lines := strings.Split(block, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}

	for i, line := range lines {
		line = ansi.Truncate(line, width, "")
		if pad := width - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// oneLine recorta un texto a una sola línea de ancho máximo.
func oneLine(value string, width int) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.TrimSpace(value)
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}

// formatBytes devuelve un tamaño legible para el sidebar.
func formatBytes(size int64) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// approxTokens estima tokens con la heurística habitual de ~4 runas/token.
func approxTokens(value string) int {
	runes := len([]rune(value))
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

// formatTokens abrevia un número de tokens con separador de miles.
func formatTokens(value int) string {
	if value < 0 {
		value = 0
	}
	digits := fmt.Sprintf("%d", value)
	if len(digits) <= 3 {
		return digits
	}

	var b strings.Builder
	lead := len(digits) % 3
	if lead == 0 {
		lead = 3
	}
	b.WriteString(digits[:lead])
	for i := lead; i < len(digits); i += 3 {
		b.WriteString(",")
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// lipglossStyleForeground devuelve un estilo con el color hex indicado.
// Reutilizable por las vistas del Swarm que pintan por agente.
func lipglossStyleForeground(hex string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex))
}

// lipglossColor convierte un hex validado en un lipgloss.Color.
func lipglossColor(hex string) lipgloss.Color {
	return lipgloss.Color(hex)
}

// sectionTitle pinta un encabezado de sección del sidebar.
func sectionTitle(icon, label string) string {
	return titleStyle.Render(icon + " " + label)
}
