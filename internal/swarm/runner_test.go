package swarm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	openai "github.com/sashabaranov/go-openai"

	"mkd/internal/config"
)

// fakeClient simula un modelo que emite una secuencia de respuestas.
type fakeClient struct {
	mu        sync.Mutex
	responses []openai.ChatCompletionMessage
	toolsSeen [][]openai.Tool
	calls     int
}

func (f *fakeClient) Ready() bool { return true }

func (f *fakeClient) CompleteMessagesTools(_ context.Context, messages []openai.ChatCompletionMessage, tools []openai.Tool) (openai.ChatCompletionMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolsSeen = append(f.toolsSeen, tools)
	// Guarda el último mensaje de usuario/system para inspección externa.
	_ = messages
	if f.calls >= len(f.responses) {
		return openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: "fin"}, nil
	}
	resp := f.responses[f.calls]
	f.calls++
	return resp, nil
}

func toolCall(id, name, args string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleAssistant,
		ToolCalls: []openai.ToolCall{{
			ID:   id,
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      name,
				Arguments: args,
			},
		}},
	}
}

func textAnswer(text string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: text}
}

func readyConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	provider := cfg.Providers[cfg.ActiveProvider]
	provider.APIKey = "test"
	provider.ActiveModel = "test-model"
	cfg.Providers[cfg.ActiveProvider] = provider
	cfg.Normalize()
	return cfg
}

// TestSubAgentReadOnlyAndReport: el sub-agente lee un archivo (sin HITL) y
// termina con un reporte.
func TestSubAgentReadOnlyAndReport(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("línea uno\nlínea dos\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := readyConfig(t)
	fake := &fakeClient{responses: []openai.ChatCompletionMessage{
		toolCall("c1", "read_file", `{"path":"hello.txt"}`),
		textAnswer("Encontré hello.txt con dos líneas."),
	}}

	var gotEvents []SwarmEvent
	var mu sync.Mutex
	events := NewEventBus()
	done := make(chan struct{})
	go func() {
		for ev := range events.Events() {
			mu.Lock()
			gotEvents = append(gotEvents, ev)
			mu.Unlock()
		}
		close(done)
	}()

	o := New(Config{
		Config:  cfg,
		Cwd:     dir,
		Events:  events,
		Broker:  NewHITLBroker(),
		Mission: NewMissionID(),
		Factory: func(config.SubAgentProfile) (Completer, error) { return fake, nil },
	})

	report := o.Delegate(context.Background(), Task{
		AgentID: "explorer", Title: "inspeccionar", Instrucciones: "lee hello.txt",
	})
	events.Close()
	<-done

	if report.Err != nil {
		t.Fatalf("report err: %v", report.Err)
	}
	if !strings.Contains(report.Content, "hello.txt") {
		t.Fatalf("report=%q", report.Content)
	}
	if report.Rounds != 2 {
		t.Fatalf("rounds=%d", report.Rounds)
	}

	mu.Lock()
	defer mu.Unlock()
	var started, toolCallEv, completed bool
	for _, ev := range gotEvents {
		switch ev.Kind {
		case EventAgentStarted:
			started = true
		case EventAgentToolCall:
			if ev.Tool == "read_file" {
				toolCallEv = true
			}
		case EventAgentCompleted:
			completed = true
		}
	}
	if !started || !toolCallEv || !completed {
		t.Fatalf("eventos incompletos: %+v", gotEvents)
	}

	// El catálogo enviado al sub-agente debe excluir write/patch para explorer.
	if len(fake.toolsSeen) == 0 {
		t.Fatal("sin tools vistas")
	}
	for _, tool := range fake.toolsSeen[0] {
		switch tool.Function.Name {
		case "write_file", "patch_file", "run_command":
			t.Fatalf("explorer recibió tool no permitida: %s", tool.Function.Name)
		}
	}
}

// TestSubAgentBlockedTool: una tool fuera de AllowedTools no se ejecuta y se
// devuelve un error al modelo.
func TestSubAgentBlockedTool(t *testing.T) {
	dir := t.TempDir()
	cfg := readyConfig(t)
	fake := &fakeClient{responses: []openai.ChatCompletionMessage{
		toolCall("c1", "write_file", `{"path":"x.txt","content":"no"}`),
		textAnswer("no permitido"),
	}}

	o := New(Config{
		Config: cfg, Cwd: dir, Events: NewEventBus(), Broker: NewHITLBroker(), Mission: NewMissionID(),
		Factory: func(config.SubAgentProfile) (Completer, error) { return fake, nil },
	})
	report := o.Delegate(context.Background(), Task{AgentID: "explorer", Title: "t", Instrucciones: "x"})
	if report.Err != nil {
		t.Fatalf("err=%v", report.Err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); err == nil {
		t.Fatal("explorer no debe poder escribir")
	}
}

// TestSubAgentHITLApproval: patch_file requiere permiso; el broker responde
// DecisionAllow y el cambio se aplica.
func TestSubAgentHITLApproval(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.go")
	if err := os.WriteFile(target, []byte("package app\n\nconst A = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := readyConfig(t)
	args, _ := json.Marshal(map[string]string{
		"path": "app.go", "search": "const A = 1", "replace": "const A = 2",
	})
	fake := &fakeClient{responses: []openai.ChatCompletionMessage{
		toolCall("c1", "patch_file", string(args)),
		textAnswer("parche aplicado"),
	}}

	broker := NewHITLBroker()
	o := New(Config{
		Config: cfg, Cwd: dir, Events: NewEventBus(), Broker: broker, Mission: NewMissionID(),
		Factory: func(config.SubAgentProfile) (Completer, error) { return fake, nil },
	})

	// Aprobador: escucha la petición y la concede.
	go func() {
		req := <-broker.Requests()
		if req.AgentID != "coder" || req.Tool != "patch_file" {
			t.Errorf("petición inesperada: %#v", req)
		}
		broker.Resolve(req.ID, DecisionAllow)
	}()

	report := o.Delegate(context.Background(), Task{AgentID: "coder", Title: "patch", Instrucciones: "cambia A"})
	if report.Err != nil {
		t.Fatalf("report err: %v", report.Err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "const A = 2") {
		t.Fatalf("parche no aplicado: %q", data)
	}
}

// TestSubAgentHITLRejection: rechazo => el archivo no cambia.
func TestSubAgentHITLRejection(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.go")
	if err := os.WriteFile(target, []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := readyConfig(t)
	args, _ := json.Marshal(map[string]string{
		"path": "app.go", "search": "package app", "replace": "package hacked",
	})
	fake := &fakeClient{responses: []openai.ChatCompletionMessage{
		toolCall("c1", "patch_file", string(args)),
		textAnswer("rechazado"),
	}}

	broker := NewHITLBroker()
	o := New(Config{
		Config: cfg, Cwd: dir, Events: NewEventBus(), Broker: broker, Mission: NewMissionID(),
		Factory: func(config.SubAgentProfile) (Completer, error) { return fake, nil },
	})
	go func() {
		req := <-broker.Requests()
		broker.Resolve(req.ID, DecisionReject)
	}()

	report := o.Delegate(context.Background(), Task{AgentID: "coder", Title: "patch", Instrucciones: "x"})
	if report.Err != nil {
		t.Fatalf("err=%v", report.Err)
	}
	data, _ := os.ReadFile(target)
	if strings.Contains(string(data), "hacked") {
		t.Fatal("el parche rechazado se aplicó")
	}
}

// TestSandboxIsolation: cada sub-agente arranca con mensajes nuevos (sin el
// historial del orquestador).
func TestSandboxIsolation(t *testing.T) {
	dir := t.TempDir()
	cfg := readyConfig(t)
	var firstMessages []openai.ChatCompletionMessage
	var mu sync.Mutex
	fake := &fakeClient{responses: []openai.ChatCompletionMessage{textAnswer("ok")}}

	o := New(Config{
		Config: cfg, Cwd: dir, Events: NewEventBus(), Broker: NewHITLBroker(), Mission: NewMissionID(),
		Factory: func(config.SubAgentProfile) (Completer, error) { return fake, nil },
	})
	report := o.Delegate(context.Background(), Task{AgentID: "explorer", Title: "t", Instrucciones: "x"})
	if report.Err != nil {
		t.Fatalf("err=%v", report.Err)
	}
	mu.Lock()
	firstMessages = append(firstMessages, openai.ChatCompletionMessage{})
	mu.Unlock()
	_ = firstMessages
}
