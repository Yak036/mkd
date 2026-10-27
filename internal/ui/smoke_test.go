package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mkd/internal/ai"
	"mkd/internal/config"
)

// TestSmokeRenderWorkspace arranca el Model real, salta el splash y renderiza
// los tres modos de agente sin pánicos ni cadenas vacías.
func TestSmokeRenderWorkspace(t *testing.T) {
	cfg := config.Defaults()
	client, err := ai.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m := New(Options{Client: client, Config: cfg})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = next.(Model)
	m.state = stateWorkspace
	m.refreshUsage()

	view := m.View()
	if strings.TrimSpace(view) == "" {
		t.Fatal("vista vacía")
	}
	for _, needle := range []string{"MKD", "TELEMETRÍA", "Tokens"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("sidebar sin %q", needle)
		}
	}

	// Ciclo de modos en la barra de estado.
	for _, mode := range []string{config.ModeDefault, config.ModeAuto, config.ModePlan, config.ModeChat} {
		m.cfg.AgentMode = mode
		m.cfg.Normalize()
		if s := m.View(); !strings.Contains(s, config.ModeLabel(mode)) {
			t.Fatalf("status sin modo %s", mode)
		}
	}
}

// TestSmokeModalsRenders garantiza que los modales nuevos renderizan.
func TestSmokeModalsRenders(t *testing.T) {
	cfg := config.Defaults()
	client, _ := ai.New(cfg)
	m := New(Options{Client: client, Config: cfg})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = next.(Model)
	m.state = stateWorkspace
	m.layout()

	blocks, _ := m.openBlocksModal()
	if strings.TrimSpace(blocks.View()) == "" {
		t.Fatal("modal de bloques vacío")
	}

	m.history = []chatMessage{{Role: ai.RoleAssistant, Content: "```go\nfmt.Println(1)\n```", Display: "x"}}
	withBlocks, _ := m.openBlocksModal()
	if view := withBlocks.blocksModalView(); !strings.Contains(view, "Bloques") {
		t.Fatalf("modal de bloques sin título: %q", view)
	}
}
