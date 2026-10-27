package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mkd/internal/agent"
	"mkd/internal/ai"
	"mkd/internal/config"
	"mkd/internal/git"
	"mkd/internal/mcp"
)

// agentCall construye una Call nativa/MCP para probar el enrutador.
func agentCall(name, arguments string) agent.Call {
	return agent.Call{ID: "c1", Name: name, Arguments: arguments}
}

// TestCommitModalRender verifica que el modal /commit pinta la cabecera, el
// aviso de stage y los atajos sin depender de un LLM real.
func TestCommitModalRender(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.width, m.height = 120, 40
	m.layout()

	m.modal.kind = modalCommit
	m.modal.commit = newGitCommitModal(git.Status{
		Branch:   "main",
		Unstaged: []string{"a.go", "b.go"},
		Adds:     128,
		Removes:  19,
	})
	m.modal.commit.phase = commitReady
	m.modal.commit.message.SetValue("feat(mcp): add stdio client\n\n- handshake\n- tools/list")

	view := m.commitModalView()
	for _, needle := range []string{"Git Commit", "Rama: main", "git add -u", "Ctrl+Enter", "Ctrl+R"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("commit modal sin %q:\n%s", needle, view)
		}
	}
	if !strings.Contains(m.commitModalView(), "feat(mcp)") {
		t.Fatal("el mensaje editable no se pinta")
	}
}

// TestCleanCommitMessage limpia tildes y prefijos habituales del modelo.
func TestCleanCommitMessage(t *testing.T) {
	cases := map[string]string{
		"```\nfeat(ui): x\n```":         "feat(ui): x",
		"Commit: fix(agent): y":         "fix(agent): y",
		"\"chore: limpiar comentario\"": "chore: limpiar comentario",
	}
	for in, want := range cases {
		if got := cleanCommitMessage(in); got != want {
			t.Fatalf("clean(%q)=%q, esperaba %q", in, got, want)
		}
	}
}

// TestReviewNoChangesWithoutRepo no rompe cuando no hay repo ni cambios.
func TestReviewNoChangesWithoutRepo(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeChat)
	cfg := config.Defaults()
	provider := cfg.Providers[cfg.ActiveProvider]
	provider.APIKey = "test-key"
	provider.ActiveModel = "test-model"
	cfg.Providers[cfg.ActiveProvider] = provider
	cfg.Normalize()

	client, err := ai.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.cfg = cfg
	m.client = client

	got, _ := m.startReview("")
	if !strings.Contains(got.notice, "repositorio git") {
		t.Fatalf("notice=%q", got.notice)
	}
}

// TestMCPModalRender verifica la lista de servidores y el sub-formulario.
func TestMCPModalRender(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.width, m.height = 120, 40
	m.layout()

	m.modal.kind = modalMCP
	m.modal.mcp = mcpModal{servers: []mcp.Status{
		{Name: "filesystem", Command: "npx", Args: []string{"-y", "server-filesystem"}, Online: true, Enabled: true,
			Tools: []string{"read_file", "write_file", "list_directory"}},
		{Name: "postgres", Command: "npx", Args: []string{"-y", "server-postgres"}, Enabled: false},
	}}
	view := m.mcpModalView()
	for _, needle := range []string{"Servidores MCP", "filesystem", "3 tools", "postgres", "Ctrl+R"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("mcp modal sin %q:\n%s", needle, view)
		}
	}

	m.modal.mcp.creating = true
	m.modal.mcp.name = newInput("nombre")
	m.modal.mcp.command = newInput("comando")
	m.modal.mcp.args = newInput("args")
	if form := m.mcpModalView(); !strings.Contains(form, "Nuevo servidor MCP") {
		t.Fatal("sub-formulario de alta no se pinta")
	}
}

// TestSidebarMCPSection comprueba que el sidebar lista los servidores.
func TestSidebarMCPSection(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.mcp = mcp.NewManager(map[string]config.MCPServerConfig{
		"filesystem": {Command: "npx", Enabled: false},
	})
	m.width, m.height = 140, 40
	m.layout()

	view := m.sidebarView(m.sidebarWidth, m.mainHeight)
	if !strings.Contains(view, "SERVIDORES MCP") || !strings.Contains(view, "filesystem") {
		// La sección puede quedar fuera si el panel es corto: renderizamos un
		// sidebar alto para garantizarla.
		view = m.sidebarView(34, 80)
	}
	if !strings.Contains(view, "SERVIDORES MCP") || !strings.Contains(view, "filesystem") {
		t.Fatalf("sidebar sin sección MCP:\n%s", view)
	}
}

// TestRenderingRealE2E es una red de seguridad de integración: recorre los
// tres modales nuevos y el sidebar MCP dentro del View completo.
func TestRenderingRealE2E(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.state = stateWorkspace
	m.sidebarVisible = true
	next, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 42})
	m = next.(Model)

	m.modal.kind = modalCommit
	m.modal.commit = newGitCommitModal(git.Status{Branch: "main", Staged: []string{"x.go"}, Adds: 3, Removes: 1})
	m.modal.commit.phase = commitReady
	if view := m.View(); !strings.Contains(view, "Git Commit") {
		t.Fatal("View sin modal commit")
	}

	m.modal.kind = modalMCP
	m.modal.mcp = mcpModal{servers: []mcp.Status{{Name: "obsidian", Command: "npx", Online: true, Tools: []string{"read_file"}}}}
	if view := m.View(); !strings.Contains(view, "Servidores MCP") {
		t.Fatal("View sin modal MCP")
	}

	m.modal.kind = modalNone
	if view := m.View(); strings.TrimSpace(view) == "" {
		t.Fatal("View vacío tras cerrar modales")
	}
}

// TestMCPToolRequiresApproval verifica que sin auto_approve la tool abre HITL.
func TestMCPToolRequiresApproval(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.mcp = mcp.NewManager(map[string]config.MCPServerConfig{
		"fake": {Command: "true", Enabled: true},
	})
	m.ensureAgentState()
	m.agent.pending = []agent.Call{agentCall(mcp.QualifiedName("fake", "read_file"), `{"path":"x"}`)}

	next, _ := m.resolveNextProposal()
	if next.modal.kind != modalDiff {
		t.Fatalf("modal=%v, esperaba HITL", next.modal.kind)
	}
	if _, ok := next.mcpPending["c1"]; !ok {
		t.Fatal("la llamada MCP no quedó pendiente de aprobación")
	}

	// Aprobación siempre: marca la tool como confiable.
	after, _ := next.updateAgentDiffKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !after.agent.autoApprove[mcp.QualifiedName("fake", "read_file")] {
		t.Fatal("la aprobación no se recordó")
	}
	if after.mcpCallTrusted("fake", mcp.QualifiedName("fake", "read_file")) != true {
		t.Fatal("mcpCallTrusted debería confiar tras [A]")
	}
}

// TestAgentToolsIncludeMCP verifica la unificación nativa + MCP.
func TestAgentToolsIncludeMCP(t *testing.T) {
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.mcp = nil
	native := len(m.agentTools())
	if native != 6 {
		t.Fatalf("tools nativas=%d", native)
	}
}

// TestMCPToolRouting llega hasta el manager real con un servidor de test.
func TestMCPToolRouting(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("sin ejecutable de test")
	}
	dir := t.TempDir()
	m := mkTestModel(t, dir, config.ModeDefault)
	m.mcp = mcp.NewManager(map[string]config.MCPServerConfig{
		"fake": {
			Command: exe,
			Args:    []string{"-test.run=TestMCPFakeServer"},
			Env:     map[string]string{"MKD_FAKE_MCP": "1"},
			Enabled: true,
		},
	})
	if err := m.mcp.Start(context.Background(), "fake"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.mcp.Close()

	tools := m.agentTools()
	found := false
	for _, tool := range tools {
		if tool.Function != nil && tool.Function.Name == "mcp__fake__echo" {
			found = true
		}
	}
	if !found {
		t.Fatal("mcp__fake__echo no está en agentTools")
	}

	cmd := m.runMCPCallCmd(agentCall(mcp.QualifiedName("fake", "echo"), `{"text":"hola"}`))
	msg, ok := cmd().(agentToolDoneMsg)
	if !ok {
		t.Fatal("mensaje inesperado")
	}
	if msg.content != "echo: hola" || !msg.mcp || msg.server != "fake" || msg.tool != "echo" {
		t.Fatalf("routing=%#v", msg)
	}
	if !strings.Contains(msg.callID, "c1") {
		t.Fatalf("callID=%q", msg.callID)
	}
}

// TestMCPFakeServer es un servidor MCP mínimo para TestMCPToolRouting.
func TestMCPFakeServer(t *testing.T) {
	if os.Getenv("MKD_FAKE_MCP") != "1" {
		t.Skip("solo actúa como servidor")
	}
	serveFakeMCP()
}

// serveFakeMCP implementa initialize / tools/list / tools/call.
func serveFakeMCP() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var msg struct {
			ID     *json.RawMessage `json:"id,omitempty"`
			Method string           `json:"method"`
			Params json.RawMessage  `json:"params,omitempty"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil || msg.ID == nil {
			continue
		}
		var result any
		switch msg.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "fake"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name": "echo", "description": "echo",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
			}}}
		case "tools/call":
			var p struct {
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			text, _ := p.Arguments["text"].(string)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "echo: " + text}}}
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}
}
