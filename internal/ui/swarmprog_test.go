package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mkd/internal/ai"
	"mkd/internal/config"
	"mkd/internal/swarm"
)

// pumpSwarm ejecuta el orquestador y entrega los eventos del bus al Update
// real, reproduciendo fielmente lo que hace Bubbletea con waitSwarmEvent.
func pumpSwarm(t *testing.T, m *Model, instruction string, timeout time.Duration) {
	t.Helper()
	next, cmd := m.startSwarmMission(instruction)
	*m = asModel(next)
	if m.bus == nil {
		t.Fatalf("misión no iniciada: %q", m.notice)
	}

	// startSwarmMission devuelve un tea.Batch; ejecutamos SÓLO el comando del
	// orquestador (m.orchestratorCmd) y dejamos que el bucle de bombeo de abajo
	// sea el único consumidor de los canales (igual que hace Bubbletea).
	doneCh := make(chan swarmDoneMsg, 1)
	eventsCh := m.bus.events.Events()
	permCh := m.bus.broker.Requests()

	if orchCmd := m.orchestratorCmd; orchCmd != nil {
		go func() {
			defer func() { _ = recover() }()
			if done, ok := orchCmd().(swarmDoneMsg); ok {
				doneCh <- done
			}
		}()
	}
	_ = cmd

	// Bombea eventos hasta que el orquestador termine.
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-eventsCh:
			if !ok {
				eventsCh = nil
				continue
			}
			next, _ := m.handleSwarmEvt(swarmEventMsg{gen: m.swarm.runGen, ev: ev})
			*m = asModel(next)
		case req := <-permCh:
			// Auto-aprueba permisos en el test.
			m.bus.broker.Resolve(req.ID, swarm.DecisionAllow)
		case done := <-doneCh:
			// Drena los eventos que ya estén en el buffer antes de cerrar, para
			// que el estado del sub-agente refleje su streaming (como haría
			// Bubbletea, que procesa los mensajes en orden de llegada).
			drainEvents(t, m)
			next, _ := m.handleSwarmDone(done)
			*m = asModel(next)
			return
		case <-deadline:
			t.Fatalf("misión no terminó en %s", timeout)
		}
	}
}

// TestSwarmProgramLiveMission valida el ciclo completo con el Update real:
// delegación + eventos + estado por agente + reporte final.
func TestSwarmProgramLiveMission(t *testing.T) {
	llm := &fakeLLM{}
	server := httptest.NewServer(llm.handler())
	defer server.Close()

	cfg := config.Defaults()
	provider := cfg.Providers[cfg.ActiveProvider]
	provider.BaseURL = server.URL
	provider.APIKey = "test-key"
	provider.ActiveModel = "fake-model"
	cfg.Providers[cfg.ActiveProvider] = provider
	cfg.Normalize()

	client, _ := ai.New(cfg)
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.cfg = cfg
	m.client = client
	m.state = stateWorkspace
	m.width, m.height = 130, 42
	m.layout()

	pumpSwarm(t, &m, "implementa la feature X", 8*time.Second)

	if llm.calls.Load() < 3 {
		t.Fatalf("llamadas=%d (esperaba orquestador+sub-agente+síntesis)", llm.calls.Load())
	}
	if !m.swarm.finished.IsZero() == false {
		t.Logf("swarm no marcado como terminado")
	}
	live, ok := m.swarm.agents["coder"]
	if !ok {
		t.Fatalf("coder no registrado en el estado: %+v", m.swarm.agents)
	}
	if live.Status != swarm.StatusDone {
		t.Fatalf("coder status=%v", live.Status)
	}
	if len(live.Steps) == 0 && strings.TrimSpace(live.Output.String()) == "" {
		t.Fatalf("coder sin actividad registrada: %#v out=%q", live, live.Output.String())
	}
	// La síntesis debe estar en el historial.
	found := false
	for _, msg := range m.history {
		if strings.Contains(msg.Content, "Misión completada") {
			found = true
		}
	}
	if !found {
		t.Fatal("la síntesis final no llegó al historial")
	}
	// El sidebar debe pintar el árbol.
	if view := m.sidebarView(34, 80); !strings.Contains(view, "SWARM MISSION CONTROL") {
		t.Fatalf("sidebar sin mission control")
	}
}

// drainEvents consume y aplica los eventos ya encolados sin bloquear.
func drainEvents(t *testing.T, m *Model) {
	t.Helper()
	for {
		select {
		case ev, ok := <-m.bus.events.Events():
			if !ok {
				return
			}
			next, _ := m.handleSwarmEvt(swarmEventMsg{gen: m.swarm.runGen, ev: ev})
			*m = asModel(next)
		default:
			return
		}
	}
}
