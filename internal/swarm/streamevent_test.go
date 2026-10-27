package swarm

import (
	"context"
	"testing"

	openai "github.com/sashabaranov/go-openai"

	"mkd/internal/config"
)

// TestStreamEventEmitted comprueba que el texto final del sub-agente se emite
// como EventAgentStreamChunk (lo que alimenta el inspector en la TUI).
func TestStreamEventEmitted(t *testing.T) {
	cfg := readyConfig(t)
	fake := &fakeClient{responses: []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleAssistant, Content: "reporte final del coder"},
	}}
	events := NewEventBus()

	o := New(Config{
		Config: cfg, Cwd: t.TempDir(), Events: events, Broker: NewHITLBroker(), Mission: NewMissionID(),
		Factory: func(config.SubAgentProfile) (Completer, error) { return fake, nil },
	})
	report := o.Delegate(context.Background(), Task{AgentID: "coder", Title: "t", Instrucciones: "x"})
	if report.Err != nil {
		t.Fatal(report.Err)
	}

	// El bus es síncrono con buffer: extraemos lo emitido sin cerrar el canal.
	var kinds []EventKind
	for {
		select {
		case ev := <-events.Events():
			kinds = append(kinds, ev.Kind)
		default:
			events.Close()
			sawStream := false
			for _, k := range kinds {
				if k == EventAgentStreamChunk {
					sawStream = true
				}
			}
			if !sawStream {
				t.Fatalf("sin EventAgentStreamChunk: %v", kinds)
			}
			return
		}
	}
}
