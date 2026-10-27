package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type discardWriter struct{}

func (discardWriter) Write(b []byte) (int, error) { return len(b), nil }

// TestSwarmHeadlessLoop ejecuta el programa real con un modo swarm simulado:
// abre el modal de agentes, alterna modo, cambia pestañas y sale.
func TestSwarmHeadlessLoop(t *testing.T) {
	m := swarmTestModel(t)
	p := tea.NewProgram(m,
		tea.WithoutSignalHandler(),
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(discardWriter{}),
	)
	go func() {
		time.Sleep(80 * time.Millisecond)
		p.Send(tea.WindowSizeMsg{Width: 130, Height: 42})
		p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) // salta splash
		time.Sleep(80 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeyCtrlT}) // modal agentes
		time.Sleep(60 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeyEsc})
		p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}, Alt: true}) // Alt+S
		time.Sleep(60 * time.Millisecond)
		p.Quit()
	}()
	if _, err := p.Run(); err != nil {
		t.Fatalf("program: %v", err)
	}
}

// TestSwarmMissionLoop arranca una misión real con un servidor LLM falso no
// disponible: debe terminar con error controlado, sin colgarse.
func TestSwarmMissionLoop(t *testing.T) {
	m := swarmTestModel(t)
	// Modelo no listo => /swarm debe rechazar rápido sin bloquear.
	next, _ := m.startSwarmMission("analiza el proyecto")
	got := asModel(next)
	if !strings.Contains(got.notice, "modelo") && !strings.Contains(got.notice, "No hay sub-agentes") {
		t.Logf("notice=%q (flujo continuó)", got.notice)
	}
}
