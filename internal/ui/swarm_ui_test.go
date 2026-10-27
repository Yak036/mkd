package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/config"
	"github.com/Yak036/mkd/internal/swarm"
)

// asModel fuerza la conversión tea.Model → Model en tests.
func asModel(v tea.Model) Model { return v.(Model) }

// swarmTestModel construye un Model con config lista y un servidor MCP nulo.
func swarmTestModel(t *testing.T) Model {
	t.Helper()
	cfg := config.Defaults()
	provider := cfg.Providers[cfg.ActiveProvider]
	provider.APIKey = "test"
	provider.ActiveModel = "test-model"
	cfg.Providers[cfg.ActiveProvider] = provider
	cfg.Normalize()

	client, _ := ai.New(cfg)
	m := mkTestModel(t, t.TempDir(), config.ModeDefault)
	m.cfg = cfg
	m.client = client
	m.state = stateWorkspace
	m.sidebarVisible = true
	m.width, m.height = 130, 42
	m.viewTab = 0
	m.layout()
	return m
}

func TestAgentModalRender(t *testing.T) {
	m := swarmTestModel(t)
	next, _ := m.openAgentModal()
	m = asModel(next)
	if m.modal.kind != modalAgents {
		t.Fatalf("modal=%v", m.modal.kind)
	}
	view := m.agentModalView()
	for _, needle := range []string{"Swarm Roster", "explorer", "coder", "qa", "docs", "Space"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("roster sin %q:\n%s", needle, view)
		}
	}
}

func TestAgentModalToggle(t *testing.T) {
	m := swarmTestModel(t)
	next, _ := m.openAgentModal()
	m = asModel(next)

	// Selecciona explorer y desactívalo con Space.
	am := &m.modal.agents
	for i, p := range am.profiles {
		if p.ID == "explorer" {
			am.selected = i
		}
	}
	wasEnabled := m.cfg.SubAgents["explorer"].Enabled
	next2, _ := m.toggleAgentProfile()
	m = asModel(next2)
	if m.cfg.SubAgents["explorer"].Enabled == wasEnabled {
		t.Fatal("Space no alternó el estado")
	}
}

func TestAgentModalEditorSave(t *testing.T) {
	m := swarmTestModel(t)
	next, _ := m.openAgentModal()
	m = asModel(next)
	am := &m.modal.agents
	for i, p := range am.profiles {
		if p.ID == "coder" {
			am.selected = i
		}
	}
	next2, _ := m.editAgentProfile()
	m = asModel(next2)
	if m.modal.agents.mode != agentModeEdit {
		t.Fatal("no abrió el editor")
	}
	m.modal.agents.name.SetValue("Backend Go")
	m.modal.agents.model.SetValue("my-model")
	next3, _ := m.saveAgentProfile()
	m = asModel(next3)
	if m.modal.agents.mode != agentModeRoster {
		t.Fatal("no volvió al roster")
	}
	if m.cfg.SubAgents["coder"].Model != "my-model" {
		t.Fatalf("modelo no guardado: %q", m.cfg.SubAgents["coder"].Model)
	}
}

func TestSwarmModeToggle(t *testing.T) {
	m := swarmTestModel(t)
	if m.swarmModeActive() {
		t.Fatal("debería iniciar en SINGLE")
	}
	next, _ := m.toggleSwarmMode()
	m = asModel(next)
	if !m.swarmModeActive() {
		t.Fatal("Alt+S no activó SWARM")
	}
	if !strings.Contains(m.swarmStatusPill(), "SWARM") {
		t.Fatalf("pill=%q", m.swarmStatusPill())
	}
}

func TestSwarmSlashRequiresInstruction(t *testing.T) {
	m := swarmTestModel(t)
	next, _ := m.handleSlash("/swarm")
	m = asModel(next)
	if !strings.Contains(m.notice, "Uso: /swarm") {
		t.Fatalf("notice=%q", m.notice)
	}
}

// TestSwarmEventUpdatesState simula eventos del bus y verifica el estado.
func TestSwarmEventUpdatesState(t *testing.T) {
	m := swarmTestModel(t)
	m.swarm.reset("mission-x")
	m.bus = &swarmBus{events: swarm.NewEventBus(), broker: swarm.NewHITLBroker()}
	gen := m.swarm.runGen

	explorer, _ := swarm.FindProfile(m.cfg.SubAgents, "explorer")
	msg := swarmEventMsg{gen: gen, ev: swarm.SwarmEvent{
		Kind: swarm.EventAgentStarted, AgentID: "explorer", Task: "mapear", Status: swarm.StatusRunning, Index: 1,
	}}
	next, _ := m.handleSwarmEvt(msg)
	m = asModel(next)

	live, ok := m.swarm.agents[explorer.ID]
	if !ok || live.Status != swarm.StatusRunning {
		t.Fatalf("estado=%#v", live)
	}

	// Tool call + result.
	next, _ = m.handleSwarmEvt(swarmEventMsg{gen: gen, ev: swarm.SwarmEvent{
		Kind: swarm.EventAgentToolCall, AgentID: "explorer", Tool: "search_code", Args: `{"query":"X"}`,
	}})
	m = asModel(next)
	next, _ = m.handleSwarmEvt(swarmEventMsg{gen: gen, ev: swarm.SwarmEvent{
		Kind: swarm.EventAgentToolResult, AgentID: "explorer", Tool: "search_code", Text: "3 hits",
	}})
	m = asModel(next)
	if len(live.Steps) != 1 || !live.Steps[0].Done {
		t.Fatalf("steps=%#v", live.Steps)
	}

	// Completed.
	next, _ = m.handleSwarmEvt(swarmEventMsg{gen: gen, ev: swarm.SwarmEvent{
		Kind: swarm.EventAgentCompleted, AgentID: "explorer", Status: swarm.StatusDone, Tokens: 1400,
	}})
	m = asModel(next)
	if live.Status != swarm.StatusDone {
		t.Fatalf("status=%v", live.Status)
	}
}

func TestSwarmTabSwitching(t *testing.T) {
	m := swarmTestModel(t)
	m.swarm.reset("mission-y")
	m.bus = &swarmBus{events: swarm.NewEventBus(), broker: swarm.NewHITLBroker()}
	gen := m.swarm.runGen

	// Arranca dos agentes con pestañas 1 y 2.
	first, _ := m.handleSwarmEvt(swarmEventMsg{gen: gen, ev: swarm.SwarmEvent{Kind: swarm.EventAgentStarted, AgentID: "explorer", Task: "a", Index: 1}})
	m = asModel(first)
	next, _ := m.handleSwarmEvt(swarmEventMsg{gen: gen, ev: swarm.SwarmEvent{Kind: swarm.EventAgentStarted, AgentID: "coder", Task: "b", Index: 2}})
	m = asModel(next)

	if got, _ := m.switchViewTab(2); asModel(got).viewTab != 2 {
		t.Fatalf("tab=%d", asModel(got).viewTab)
	}
	if got, _ := m.switchViewTab(0); asModel(got).viewTab != 0 {
		t.Fatalf("tab 0=%d", asModel(got).viewTab)
	}
	// Tab 5 no existe.
	got, _ := m.switchViewTab(5)
	if asModel(got).viewTab != 0 {
		t.Fatal("no debería cambiar a una pestaña vacía")
	}
}

func TestSwarmTabBarRenders(t *testing.T) {
	m := swarmTestModel(t)
	m.swarm.reset("mission-z")
	m.bus = &swarmBus{events: swarm.NewEventBus(), broker: swarm.NewHITLBroker()}
	gen := m.swarm.runGen
	next, _ := m.handleSwarmEvt(swarmEventMsg{gen: gen, ev: swarm.SwarmEvent{Kind: swarm.EventAgentStarted, AgentID: "explorer", Task: "a", Index: 1}})
	m = asModel(next)

	bar := m.swarmTabBar(m.innerWidth)
	if !strings.Contains(bar, "Orquestador") || !strings.Contains(bar, "explorer") {
		t.Fatalf("tab bar=%q", bar)
	}
	if view := m.View(); !strings.Contains(view, "Orquestador") {
		t.Fatal("View no incluye la barra de pestañas")
	}
}

func TestSwarmSidebarTree(t *testing.T) {
	m := swarmTestModel(t)
	m.swarm.reset("mission-w")
	m.bus = &swarmBus{events: swarm.NewEventBus(), broker: swarm.NewHITLBroker()}
	gen := m.swarm.runGen
	next, _ := m.handleSwarmEvt(swarmEventMsg{gen: gen, ev: swarm.SwarmEvent{Kind: swarm.EventAgentStarted, AgentID: "explorer", Task: "a", Index: 1}})
	m = asModel(next)
	next, _ = m.handleSwarmEvt(swarmEventMsg{gen: gen, ev: swarm.SwarmEvent{Kind: swarm.EventAgentToolCall, AgentID: "explorer", Tool: "search_code", Args: `{"query":"MCP"}`}})
	m = asModel(next)

	view := m.sidebarView(34, 80)
	for _, needle := range []string{"SWARM MISSION CONTROL", "Orquestador", "explorer", "search_code"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("sidebar sin %q:\n%s", needle, view)
		}
	}
}

func TestSwarmPermissionOpensDiffModal(t *testing.T) {
	m := swarmTestModel(t)
	// El permiso de patch_file necesita un archivo real para construir la Proposal.
	if err := os.WriteFile(filepath.Join(m.agentCwd(), "x.go"), []byte("package x\n\nconst A = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.swarm.reset("mission-p")
	broker := swarm.NewHITLBroker()
	m.bus = &swarmBus{events: swarm.NewEventBus(), broker: broker}
	gen := m.swarm.runGen
	started, _ := m.handleSwarmEvt(swarmEventMsg{gen: gen, ev: swarm.SwarmEvent{Kind: swarm.EventAgentStarted, AgentID: "coder", Task: "patch", Index: 1}})
	m = asModel(started)

	next, _ := m.handleSwarmPermission(swarmPermissionMsg{gen: gen, request: swarm.PermissionRequest{
		ID: "coder|patch_file#1", AgentID: "coder", Tool: "patch_file",
		Args: []byte(`{"path":"x.go","search":"const A = 1","replace":"const A = 2"}`),
	}})
	m = asModel(next)
	if m.modal.kind != modalDiff {
		t.Fatalf("modal=%v", m.modal.kind)
	}
	if m.modal.diff.swarm == nil || m.modal.diff.swarm.agentID != "coder" {
		t.Fatal("permiso no asociado al sub-agente")
	}
	if view := m.diffModalView(); !strings.Contains(view, "coder") {
		t.Fatalf("modal sin sub-agente:\n%s", view)
	}
}

// TestSwarmModeRoutesPlainMessages verifica que en SWARM un mensaje libre
// arranca una misión en lugar de un chat normal.
func TestSwarmModeRoutesPlainMessages(t *testing.T) {
	m := swarmTestModel(t)
	m.cfg.SwarmMode = config.SwarmModeSwarm
	m.cfg.Normalize()

	next, _ := m.startSwarmMission("analiza la arquitectura")
	m = asModel(next)
	if !m.swarm.active() {
		t.Fatalf("no se inició misión: notice=%q", m.notice)
	}
	if m.swarm.mission == "" {
		t.Fatal("misión sin id")
	}
}

// TestSwarmNoEnabledAgents bloquea el arranque cuando no hay equipo activo.
func TestSwarmNoEnabledAgents(t *testing.T) {
	m := swarmTestModel(t)
	for id, profile := range m.cfg.SubAgents {
		profile.Enabled = false
		m.cfg.SubAgents[id] = profile
	}
	next, _ := m.startSwarmMission("tarea")
	m = asModel(next)
	if !strings.Contains(m.notice, "sub-agentes") && !strings.Contains(m.notice, "Ctrl+T") {
		t.Fatalf("notice=%q", m.notice)
	}
	if m.swarm.active() {
		t.Fatal("no debería arrancar sin agentes")
	}
}

// TestSwarmEscCancels verifica que Esc cancela la misión en curso.
func TestSwarmEscCancels(t *testing.T) {
	m := swarmTestModel(t)
	m.swarm.reset("mission-esc")
	m.bus = &swarmBus{events: swarm.NewEventBus(), broker: swarm.NewHITLBroker()}
	m.swarm.running = true
	m.waiting = true

	next, _ := m.updateWorkspaceKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(next)
	if m.swarm.orchestratorStatus != "Cancelada por el usuario" {
		t.Fatalf("status=%q", m.swarm.orchestratorStatus)
	}
}

// TestAgentEditorArrowNavigation: en VS Code Tab cambia de panel, así que las
// flechas deben navegar entre campos del editor. En el prompt (textarea) las
// flechas mueven el cursor y PgUp/PgDn cambian de campo.
func TestAgentEditorArrowNavigation(t *testing.T) {
	m := swarmTestModel(t)
	next, _ := m.openAgentModal()
	m = asModel(next)
	am := &m.modal.agents
	for i, p := range am.profiles {
		if p.ID == "coder" {
			am.selected = i
		}
	}
	next, _ = m.editAgentProfile()
	m = asModel(next)
	am = &m.modal.agents

	if am.field != agentFieldName {
		t.Fatalf("campo inicial=%v", am.field)
	}
	// Down avanza de Nombre → Icono.
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyDown})
	m = asModel(next)
	if m.modal.agents.field != agentFieldIcon {
		t.Fatalf("down no avanzó: %v", m.modal.agents.field)
	}
	// Up retrocede.
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyUp})
	m = asModel(next)
	if m.modal.agents.field != agentFieldName {
		t.Fatalf("up no retrocedió: %v", m.modal.agents.field)
	}
	// Salta al prompt y comprueba que down NO cambia de campo ahí.
	m.modal.agents.field = agentFieldPrompt
	m.focusAgentField()
	before := m.modal.agents.field
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyDown})
	m = asModel(next)
	if m.modal.agents.field != before {
		t.Fatalf("down en prompt no debería cambiar de campo: %v", m.modal.agents.field)
	}
	// PgDn sí sale del prompt.
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyPgDown})
	m = asModel(next)
	if m.modal.agents.field == before {
		t.Fatal("pgdown debería cambiar de campo desde el prompt")
	}
}

// TestAgentEditorTabStillWorks: Tab sigue funcionando en terminales nativas.
func TestAgentEditorTabStillWorks(t *testing.T) {
	m := swarmTestModel(t)
	next, _ := m.openAgentModal()
	m = asModel(next)
	next, _ = m.editAgentProfile()
	m = asModel(next)
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyTab})
	m = asModel(next)
	if m.modal.agents.field != agentFieldIcon {
		t.Fatalf("tab no avanzó: %v", m.modal.agents.field)
	}
}
