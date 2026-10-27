package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	splashDuration = 1800 * time.Millisecond
	splashFrame    = 16 * time.Millisecond
)

type splashTickMsg time.Time

type splashModel struct {
	start time.Time
	frame int
}

func newSplash() splashModel {
	return splashModel{start: time.Now()}
}

func splashTick() tea.Cmd {
	return tea.Tick(splashFrame, func(t time.Time) tea.Msg {
		return splashTickMsg(t)
	})
}

func (s splashModel) done() bool {
	return time.Since(s.start) >= splashDuration
}

func (s splashModel) progress() float64 {
	elapsed := time.Since(s.start)
	if elapsed <= 0 {
		return 0
	}
	ratio := float64(elapsed) / float64(splashDuration)
	if ratio > 1 {
		return 1
	}
	return ratio
}

func (s splashModel) view(width, height int) string {
	if width < 34 || height < 12 {
		content := splashText("MKD", titleStyle, white) + "\n" + splashText("iniciando…", mutedStyle, muted)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content,
			lipgloss.WithWhitespaceChars(" "),
			lipgloss.WithWhitespaceBackground(surface),
		)
	}

	scale := 3
	if width < 76 {
		scale = 2
	}
	robotScale := 1
	if width >= 100 {
		robotScale = 2
	}

	art := buildSplashCanvas(scale, robotScale, s.frame).render()
	barWidth := min(48, max(18, width/2))
	content := strings.Join([]string{
		art,
		"",
		splashText("M K D   C L I", titleStyle, purple),
		splashText("CYBER TERMINAL WORKSPACE", mutedStyle, muted),
		"",
		splashProgressBar(barWidth, s.progress()),
		splashText("INICIANDO ENTORNO...", mutedStyle, muted),
		splashText("pulsa cualquier tecla para saltar", mutedStyle, muted),
	}, "\n")

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceBackground(surface),
	)
}

func splashText(value string, base lipgloss.Style, color lipgloss.TerminalColor) string {
	return base.Copy().Foreground(color).Background(surface).Render(value)
}

func splashProgressBar(width int, ratio float64) string {
	if width <= 0 {
		return ""
	}
	filled := int(math.Round(ratio * float64(width)))
	filled = min(max(filled, 0), width)

	filledStyle := lipgloss.NewStyle().Foreground(purple).Background(surface)
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#3F3F46")).Background(surface)
	return filledStyle.Render(strings.Repeat("█", filled)) +
		emptyStyle.Render(strings.Repeat("░", width-filled))
}

// pixelCanvas guarda colores hex por celda; "" significa transparente.
type pixelCanvas struct {
	width  int
	height int
	cells  []string
}

func newPixelCanvas(width, height int) *pixelCanvas {
	return &pixelCanvas{
		width:  width,
		height: height,
		cells:  make([]string, width*height),
	}
}

func (c *pixelCanvas) set(x, y int, color string) {
	if x < 0 || y < 0 || x >= c.width || y >= c.height || color == "" {
		return
	}
	c.cells[y*c.width+x] = color
}

func (c *pixelCanvas) color(x, y int) string {
	if x < 0 || y < 0 || x >= c.width || y >= c.height {
		return ""
	}
	return c.cells[y*c.width+x]
}

// render convierte la matriz de píxeles en medios bloques Unicode.
func (c *pixelCanvas) render() string {
	var b strings.Builder
	for y := 0; y < c.height; y += 2 {
		for x := 0; x < c.width; x++ {
			top := c.color(x, y)
			bottom := ""
			if y+1 < c.height {
				bottom = c.color(x, y+1)
			}

			switch {
			case top == "" && bottom == "":
				b.WriteString(lipgloss.NewStyle().Background(surface).Render(" "))
			case top != "" && bottom == "":
				b.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color(top)).
					Background(surface).
					Render("▀"))
			case top == "" && bottom != "":
				b.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color(bottom)).
					Background(surface).
					Render("▄"))
			default:
				b.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color(top)).
					Background(lipgloss.Color(bottom)).
					Render("▀"))
			}
		}
		if y+2 < c.height {
			b.WriteString("\n")
		}
	}
	return b.String()
}

var pixelFont = map[rune][]string{
	'M': {
		"#...#",
		"##.##",
		"#.#.#",
		"#...#",
		"#...#",
		"#...#",
		"#...#",
	},
	'K': {
		"#...#",
		"#..#.",
		"#.#..",
		"##...",
		"#.#..",
		"#..#.",
		"#...#",
	},
	'D': {
		"####.",
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		"#...#",
		"####.",
	},
}

var robotSprite = []string{
	"....aa....",
	"....aa....",
	"...####...",
	"..#oooo#..",
	"..#o##o#..",
	"..######..",
	".#.####.#.",
	"#..####..#",
	"...####...",
	"..##..##..",
	".##....##.",
}

func buildSplashCanvas(scale, robotScale, frame int) *pixelCanvas {
	lettersWidth := 3*5*scale + 2*scale
	lettersHeight := 7 * scale
	robotWidth := 10 * robotScale
	robotHeight := len(robotSprite) * robotScale

	width := 4 + lettersWidth + 4 + robotWidth + 4
	height := max(lettersHeight, robotHeight) + 4
	canvas := newPixelCanvas(width, height)

	drawPixelText(canvas, "MKD", 2, 2, scale, func(x, y int) string {
		return letterColor(x, y, frame, width)
	})

	robotX := 2 + lettersWidth + 4
	robotY := 2 + (height-4-robotHeight)/2
	drawRobot(canvas, robotX, robotY, robotScale, frame)
	return canvas
}

func drawPixelText(canvas *pixelCanvas, value string, x0, y0, scale int, colorAt func(x, y int) string) {
	cursor := x0
	for _, r := range value {
		glyph, ok := pixelFont[r]
		if !ok {
			cursor += 6 * scale
			continue
		}
		for row, line := range glyph {
			for col, char := range line {
				if char != '#' {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						x := cursor + col*scale + dx
						y := y0 + row*scale + dy
						canvas.set(x, y, colorAt(x, y))
					}
				}
			}
		}
		cursor += 6 * scale
	}
}

func drawRobot(canvas *pixelCanvas, x0, y0, scale, frame int) {
	pulse := 0.5 + 0.5*math.Sin(float64(frame)/18)
	body := lerpHex(hexCyan, hexGreen, pulse)
	glow := lerpHex(hexGreen, hexWhite, 0.55)
	eye := hexWhite
	antenna := hexPurple

	for row, line := range robotSprite {
		for col, char := range line {
			var color string
			switch char {
			case '#':
				color = body
			case 'o':
				color = eye
			case 'a':
				color = antenna
			case 'g':
				color = glow
			default:
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					canvas.set(x0+col*scale+dx, y0+row*scale+dy, color)
				}
			}
		}
	}
}

func letterColor(x, y, frame, canvasWidth int) string {
	t := float64(x) / float64(max(canvasWidth, 1))
	base := lerpHex(hexPurple, hexCyan, t)

	sweep := float64(frame%(canvasWidth+28)) - 14
	distance := math.Abs(float64(x) - sweep)
	switch {
	case distance < 2:
		return hexWhite
	case distance < 5:
		return hexGreen
	default:
		// Un leve degradado vertical hacia verde matriz.
		return lerpHex(base, hexGreen, float64(y%18)/90)
	}
}

func lerpHex(from, to string, t float64) string {
	t = math.Max(0, math.Min(1, t))
	r1, g1, b1 := parseHex(from)
	r2, g2, b2 := parseHex(to)
	return fmt.Sprintf("#%02X%02X%02X",
		int(math.Round(float64(r1)+(float64(r2)-float64(r1))*t)),
		int(math.Round(float64(g1)+(float64(g2)-float64(g1))*t)),
		int(math.Round(float64(b1)+(float64(b2)-float64(b1))*t)),
	)
}

func parseHex(value string) (int, int, int) {
	value = strings.TrimPrefix(value, "#")
	if len(value) != 6 {
		return 255, 255, 255
	}
	r, _ := strconv.ParseInt(value[0:2], 16, 32)
	g, _ := strconv.ParseInt(value[2:4], 16, 32)
	b, _ := strconv.ParseInt(value[4:6], 16, 32)
	return int(r), int(g), int(b)
}
