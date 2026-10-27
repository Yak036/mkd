package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/Yak036/mkd/internal/config"
)

// notReadyClient simula un perfil sin modelo/credenciales.
type notReadyClient struct{}

func (notReadyClient) Ready() bool { return false }

func (notReadyClient) CompleteMessagesTools(context.Context, []openai.ChatCompletionMessage, []openai.Tool) (openai.ChatCompletionMessage, error) {
	return openai.ChatCompletionMessage{}, nil
}

func baseConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	provider := cfg.Providers[cfg.ActiveProvider]
	provider.APIKey = "test"
	provider.ActiveModel = "test-model"
	cfg.Providers[cfg.ActiveProvider] = provider
	cfg.Normalize()
	return cfg
}

func TestProfileDefaults(t *testing.T) {
	cfg := config.Defaults()
	cfg.Normalize()
	for _, id := range []string{"explorer", "coder", "qa", "docs"} {
		if _, ok := cfg.SubAgents[id]; !ok {
			t.Fatalf("falta perfil %s", id)
		}
	}
	if !cfg.SubAgents["explorer"].Enabled || cfg.SubAgents["docs"].Enabled {
		t.Fatal("enabled por defecto incorrecto")
	}
	if got := ToolsForPermissions(cfg.SubAgents["explorer"]); !strings.Contains(got, "search") {
		t.Fatalf("tools explorer=%q", got)
	}
}

func TestToolAllowed(t *testing.T) {
	cfg := config.Defaults()
	cfg.Normalize()
	explorer := cfg.SubAgents["explorer"]

	cases := []struct {
		tool string
		want bool
	}{
		{"read_file", true},
		{"search_code", true},
		{"write_file", false},
		{"patch_file", false},
		{"mcp__filesystem__read_file", false},
	}
	for _, tc := range cases {
		if got := ToolAllowed(explorer, tc.tool); got != tc.want {
			t.Fatalf("ToolAllowed(explorer,%s)=%v", tc.tool, got)
		}
	}

	coder := cfg.SubAgents["coder"]
	if !ToolAllowed(coder, "patch_file") || !ToolAllowed(coder, "run_command") {
		t.Fatal("coder debería tener patch/run")
	}

	// mcp:* habilita cualquier tool MCP.
	coder.AllowedTools = append(coder.AllowedTools, MCPWildcard)
	if !ToolAllowed(coder, "mcp__fs__read_file") {
		t.Fatal("mcp:* no habilitó tool MCP")
	}
}

func TestParseDelegate(t *testing.T) {
	tasks, err := ParseDelegate(ToolDelegateTask, `{"agent_id":"coder","task_title":"x","instructions":"hazlo"}`)
	if err != nil || len(tasks) != 1 || tasks[0].AgentID != "coder" {
		t.Fatalf("parse task: %v %#v", err, tasks)
	}
	if _, err := ParseDelegate(ToolDelegateTask, `{}`); err == nil {
		t.Fatal("esperaba error sin agent_id")
	}

	raw, _ := json.Marshal(map[string]any{"tasks": []map[string]any{
		{"agent_id": "explorer", "task_title": "a", "instructions": "i"},
		{"agent_id": "qa", "task_title": "b", "instructions": "j"},
	}})
	tasks, err = ParseDelegate(ToolDelegateParallel, string(raw))
	if err != nil || len(tasks) != 2 {
		t.Fatalf("parse parallel: %v %#v", err, tasks)
	}
	if _, err := ParseDelegate(ToolDelegateParallel, `{"tasks":[]}`); err == nil {
		t.Fatal("esperaba error sin tareas")
	}
}

func TestRosterAndSystemPrompt(t *testing.T) {
	cfg := baseConfig(t)
	roster := Roster(cfg.SubAgents)
	for _, id := range []string{"explorer", "coder", "qa"} {
		if !strings.Contains(roster, id) {
			t.Fatalf("roster sin %s", id)
		}
	}
	if strings.Contains(roster, "docs") {
		t.Fatal("roster no debe incluir docs (deshabilitado)")
	}
	prompt := OrchestratorSystemPrompt("BASE", cfg.SubAgents)
	if !strings.Contains(prompt, "MODO SWARM") || !strings.Contains(prompt, "BASE") {
		t.Fatalf("prompt orquestador incompleto")
	}
}

func TestFormatReports(t *testing.T) {
	single := FormatReports([]Report{{AgentID: "coder", Content: "hecho"}})
	if single != "hecho" {
		t.Fatalf("single=%q", single)
	}
	multi := FormatReports([]Report{
		{AgentID: "explorer", AgentName: "Code Explorer", Task: "map", Content: "mapa"},
		{AgentID: "qa", Task: "tests", Err: errors.New("falló")},
	})
	if !strings.Contains(multi, "Code Explorer") || !strings.Contains(multi, "ERROR: falló") {
		t.Fatalf("multi=%q", multi)
	}
}

// TestDelegateParallelRunsConcurrently verifica goroutines + WaitGroup con un
// factory que no requiere red (cliente no Ready => error controlado).
func TestDelegateParallelRunsConcurrently(t *testing.T) {
	cfg := baseConfig(t)
	events := NewEventBus()
	defer events.Close()

	o := New(Config{
		Config:  cfg,
		Cwd:     t.TempDir(),
		Events:  events,
		Broker:  NewHITLBroker(),
		Mission: NewMissionID(),
		Factory: func(profile config.SubAgentProfile) (Completer, error) {
			return &notReadyClient{}, nil // no Ready => error controlado
		},
	})

	reports := o.DelegateParallel(context.Background(), []Task{
		{AgentID: "explorer", Title: "a", Instrucciones: "x"},
		{AgentID: "coder", Title: "b", Instrucciones: "y"},
		{AgentID: "qa", Title: "c", Instrucciones: "z"},
	})
	if len(reports) != 3 {
		t.Fatalf("reports=%d", len(reports))
	}
	for _, report := range reports {
		if report.Err == nil {
			t.Fatalf("%s debería fallar sin cliente Ready", report.AgentID)
		}
	}
}

func TestDelegateRejectsDisabledAgent(t *testing.T) {
	cfg := baseConfig(t)
	o := New(Config{Config: cfg, Cwd: t.TempDir(), Events: NewEventBus(), Broker: NewHITLBroker(), Mission: NewMissionID()})
	report := o.Delegate(context.Background(), Task{AgentID: "docs", Title: "doc"})
	if report.Err == nil || !strings.Contains(report.Err.Error(), "deshabilitado") {
		t.Fatalf("err=%v", report.Err)
	}
}

func TestHITLBrokerAlwaysRemember(t *testing.T) {
	broker := NewHITLBroker()
	broker.RememberAlways("coder", "patch_file")

	result := broker.Request(context.Background(), PermissionRequest{AgentID: "coder", Tool: "patch_file"})
	if !result.Auto || result.Decision != DecisionAlways {
		t.Fatalf("result=%#v", result)
	}
}

func TestHITLBrokerResolve(t *testing.T) {
	broker := NewHITLBroker()
	done := make(chan PermissionResult, 1)
	go func() {
		done <- broker.Request(context.Background(), PermissionRequest{AgentID: "qa", Tool: "run_command"})
	}()

	req := <-broker.Requests()
	if req.AgentID != "qa" || req.ID == "" {
		t.Fatalf("request=%#v", req)
	}
	broker.Resolve(req.ID, DecisionAllow)

	if result := <-done; result.Decision != DecisionAllow {
		t.Fatalf("result=%#v", result)
	}
}

func TestEventBusNonBlocking(t *testing.T) {
	bus := NewEventBus()
	for i := 0; i < 2000; i++ {
		bus.Emit(SwarmEvent{Kind: EventAgentStreamChunk, Text: "x"})
	}
	bus.Close()
}

// TestHITLBrokerSerializesParallelRequests verifica que dos sub-agentes en
// paralelo no reciben permiso a la vez: el broker entrega una solicitud, espera
// resolución y solo después entrega la siguiente.
func TestHITLBrokerSerializesParallelRequests(t *testing.T) {
	broker := NewHITLBroker()
	results := make(chan string, 2)

	go func() {
		res := broker.Request(context.Background(), PermissionRequest{AgentID: "coder", Tool: "patch_file"})
		results <- "coder:" + decisionName(res.Decision)
	}()
	go func() {
		res := broker.Request(context.Background(), PermissionRequest{AgentID: "qa", Tool: "run_command"})
		results <- "qa:" + decisionName(res.Decision)
	}()

	// Debe llegar exactamente una petición a la vez.
	first := <-broker.Requests()
	// No debe haber otra petición disponible antes de resolver la primera.
	select {
	case second := <-broker.Requests():
		t.Fatalf("dos peticiones simultáneas: %s y %s", first.AgentID, second.AgentID)
	case <-time.After(150 * time.Millisecond):
	}
	broker.Resolve(first.ID, DecisionAllow)

	second := <-broker.Requests()
	if second.ID == first.ID {
		t.Fatal("misma petición reentregada")
	}
	broker.Resolve(second.ID, DecisionAllow)

	for i := 0; i < 2; i++ {
		if got := <-results; got == "" {
			t.Fatal("resultado vacío")
		}
	}
}

func decisionName(d Decision) string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionAlways:
		return "always"
	default:
		return "reject"
	}
}
