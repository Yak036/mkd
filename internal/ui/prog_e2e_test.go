package ui

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/config"
)

type captureWriter struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (c *captureWriter) Write(b []byte) (int, error) {
	c.mu.Lock()
	c.buf.Write(b)
	c.mu.Unlock()
	return len(b), nil
}
func (c *captureWriter) String() string { c.mu.Lock(); defer c.mu.Unlock(); return c.buf.String() }

// TestRealProgramFullMission lanza un tea.Program real y verifica que la misión
// se ejecuta y la UI refleja los sub-agentes en la salida renderizada.
func TestRealProgramFullMission(t *testing.T) {
	llm := &fakeLLM{}
	srv := httptest.NewServer(llm.handler())
	defer srv.Close()

	cfg := config.Defaults()
	p := cfg.Providers[cfg.ActiveProvider]
	p.BaseURL = srv.URL
	p.APIKey = "k"
	p.ActiveModel = "m"
	cfg.Providers[cfg.ActiveProvider] = p
	cfg.SwarmMode = config.SwarmModeSwarm
	cfg.Normalize()

	client, _ := ai.New(cfg)
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.cfg = cfg
	m.client = client
	m.state = stateWorkspace
	m.width, m.height = 140, 45
	m.layout()
	m.textarea.Focus()

	out := &captureWriter{}
	prog := tea.NewProgram(m,
		tea.WithoutSignalHandler(),
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
	)
	go func() {
		time.Sleep(100 * time.Millisecond)
		prog.Send(tea.WindowSizeMsg{Width: 140, Height: 45})
		time.Sleep(120 * time.Millisecond)
		// En modo swarm, un mensaje libre arranca la misión: escribe y envía.
		prog.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("implementa X")})
		prog.Send(tea.KeyMsg{Type: tea.KeyEnter})
		// Espera a que la misión complete (3 llamadas al LLM).
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) && llm.calls.Load() < 3 {
			time.Sleep(50 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
		prog.Quit()
	}()

	if _, err := prog.Run(); err != nil {
		t.Fatalf("program: %v", err)
	}
	if llm.calls.Load() < 3 {
		t.Fatalf("misión incompleta: llamadas=%d", llm.calls.Load())
	}
	rendered := stripANSI(out.String())
	if !strings.Contains(rendered, "SWARM") && !strings.Contains(rendered, "Orquestador") {
		t.Fatalf("la UI no reflejó el modo swarm")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !(s[j] >= '@' && s[j] <= '~') {
					j++
				}
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
