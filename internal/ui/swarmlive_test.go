package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/config"
)

// fakeLLM simula un proveedor OpenAI-compatible con tool calling.
// - 1ª llamada: pide delegate_task(coder)
// - llamadas de sub-agente: responden texto
// - última: responde síntesis final
type fakeLLM struct {
	calls atomic.Int64
}

func (f *fakeLLM) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := f.calls.Add(1)
		var body struct {
			Messages []map[string]any `json:"messages"`
			Tools    []any            `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		// ¿Es un sub-agente? (mensaje de usuario con "TAREA:")
		isSub := false
		for _, msg := range body.Messages {
			if content, ok := msg["content"].(string); ok && strings.Contains(content, "TAREA:") {
				isSub = true
			}
		}

		var message map[string]any
		switch {
		case isSub:
			message = map[string]any{"role": "assistant", "content": "Sub-agente completó la tarea: todo OK."}
		case len(body.Tools) > 0 && n == 1:
			message = map[string]any{
				"role":    "assistant",
				"content": "Delegando al coder...",
				"tool_calls": []any{map[string]any{
					"id": "call_1", "type": "function",
					"function": map[string]any{
						"name":      "delegate_task",
						"arguments": `{"agent_id":"coder","task_title":"implementar","instructions":"haz el trabajo"}`,
					},
				}},
			}
		default:
			message = map[string]any{"role": "assistant", "content": "## Misión completada\nEl coder entregó su reporte."}
		}

		resp := map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// TestFullSwarmMissionAgainstFakeLLM valida el ciclo completo: orquestador →
// delegate_task → sub-agente con su propio cliente → reporte → síntesis.
func TestFullSwarmMissionAgainstFakeLLM(t *testing.T) {
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

	client, err := ai.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.cfg = cfg
	m.client = client
	m.state = stateWorkspace
	m.width, m.height = 130, 42
	m.layout()

	next, cmd := m.startSwarmMission("implementa la feature X")
	m = asModel(next)
	if m.swarm.mission == "" {
		t.Fatalf("misión no iniciada: notice=%q", m.notice)
	}

	// tea.Batch devuelve tea.BatchMsg ([]tea.Cmd): ejecutamos cada comando
	// hasta encontrar el swarmDoneMsg real del orquestador.
	done, ok := runBatchForDone(cmd)
	if !ok {
		t.Fatalf("no se obtuvo swarmDoneMsg del batch")
	}
	next2, _ := m.handleSwarmDone(done)
	m = asModel(next2)

	if done.err != nil {
		t.Fatalf("misión falló: %v", done.err)
	}
	if !strings.Contains(done.content, "Misión completada") {
		t.Fatalf("síntesis inesperada: %q", done.content)
	}
	if llm.calls.Load() < 3 {
		t.Fatalf("llamadas=%d, esperaba orquestador+sub-agente+síntesis", llm.calls.Load())
	}
	// En el programa real el bus lo drena handleSwarmEvt; aquí validamos que el
	// orquestador delegó de verdad comprobando el nº de llamadas al LLM.
	// 1=orquestador pide delegate_task, 2=sub-agente reporta, 3=síntesis final.
	if llm.calls.Load() < 3 {
		t.Fatalf("el orquestador no delegó (llamadas=%d)", llm.calls.Load())
	}
	if !strings.Contains(done.content, "coder") && !strings.Contains(done.content, "Misión") {
		t.Fatalf("síntesis sin rastro de la delegación: %q", done.content)
	}
}

// runBatchForDone ejecuta todos los comandos de un tea.BatchMsg y devuelve el
// primer swarmDoneMsg encontrado (los demás pueden bloquearse esperando eventos,
// por eso se ejecutan en goroutines con timeout).
func runBatchForDone(cmd tea.Cmd) (swarmDoneMsg, bool) {
	msg := cmd()
	if done, ok := msg.(swarmDoneMsg); ok {
		return done, true
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return swarmDoneMsg{}, false
	}

	results := make(chan swarmDoneMsg, len(batch))
	for _, sub := range batch {
		if sub == nil {
			continue
		}
		go func(sub tea.Cmd) {
			defer func() { _ = recover() }()
			if m, ok := sub().(swarmDoneMsg); ok {
				results <- m
			}
		}(sub)
	}
	select {
	case done := <-results:
		return done, true
	case <-time.After(5 * time.Second):
		return swarmDoneMsg{}, false
	}
}
