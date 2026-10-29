package ui

import (
	"fmt"
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
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
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

// TestSwarmEscCancels verifica que Esc pide confirmación segura antes de cancelar la misión en curso.
func TestSwarmEscCancels(t *testing.T) {
	m := swarmTestModel(t)
	m.swarm.reset("mission-esc")
	m.bus = &swarmBus{events: swarm.NewEventBus(), broker: swarm.NewHITLBroker()}
	m.swarm.running = true
	m.waiting = true

	// 1. Primer Esc abre modal de confirmación sin abortar la misión
	next, _ := m.updateWorkspaceKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(next)
	if m.modal.kind != modalSwarmCancelConfirm {
		t.Fatalf("modal=%v, esperaba modalSwarmCancelConfirm tras Esc", m.modal.kind)
	}
	if !m.swarm.running {
		t.Fatal("el swarm no debía detenerse sin confirmación explícita")
	}

	// 2. Esc dentro del modal de confirmación cancela el aborto (el swarm sigue activo)
	next, _ = m.updateModal(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(next)
	if m.modal.kind != modalNone {
		t.Fatalf("modal=%v, esperaba modalNone tras cancelar con Esc", m.modal.kind)
	}
	if !m.swarm.running {
		t.Fatal("el swarm debía continuar en ejecución")
	}

	// 3. Volver a pulsar Esc y confirmar explícitamente con 'y'
	next, _ = m.updateWorkspaceKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(next)
	next, _ = m.updateModal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
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

// TestSwarmStreamingLiveCard verifica que la tarjeta en vivo de la misión se renderice
// dinámicamente con acciones legibles, agentes y estado durante la ejecución.
func TestSwarmStreamingLiveCard(t *testing.T) {
	m := swarmTestModel(t)
	m.conversationWidth = 90
	m.swarm.mission = "m-live"
	m.swarm.running = true
	m.swarm.orchestratorStatus = "Coordinando tareas en paralelo…"

	// Registrar sub-agente activo ejecutando patch_file
	p := config.SubAgentProfile{
		ID:    "frontend",
		Name:  "Frontend Specialist",
		Icon:  "󰜈",
		Color: "#3B82F6",
	}
	live := m.swarm.agent(p)
	live.Status = swarm.StatusRunning
	live.Task = "Mejoras UX/UI"
	live.Tool = "patch_file"
	live.Args = `{"path":"internal/ui/agentmodal.go"}`

	// Registrar sub-agente ejecutando MCP
	pQA := config.SubAgentProfile{
		ID:    "qa",
		Name:  "QA Specialist",
		Icon:  "󰒋",
		Color: "#F59E0B",
	}
	liveQA := m.swarm.agent(pQA)
	liveQA.Status = swarm.StatusRunning
	liveQA.Task = "Pruebas de regresión"
	liveQA.Tool = "mcp__playwright__browser_navigate"
	liveQA.Args = `{"url":"http://localhost:8080"}`

	card := m.streamingBlock()

	if !strings.Contains(card, "SWARM MISSION") {
		t.Errorf("se esperaba encabezado SWARM MISSION en la tarjeta: %s", card)
	}
	if !strings.Contains(card, "frontend") || !strings.Contains(card, "Editando archivo") {
		t.Errorf("se esperaba ver a frontend editando archivo: %s", card)
	}
	if !strings.Contains(card, "qa") || !strings.Contains(card, "MCP [playwright]") {
		t.Errorf("se esperaba ver a qa consultando MCP: %s", card)
	}
	if !strings.Contains(card, "Alt+1") {
		t.Errorf("se esperaban pistas de teclado Alt+1..N: %s", card)
	}
}

// TestSwarmPreservesConversationHistory comprueba que el orquestador incluya los turnos anteriores
// de la sesión al invocar una misión Swarm.
func TestSwarmPreservesConversationHistory(t *testing.T) {
	m := swarmTestModel(t)
	m.history = []chatMessage{
		{Role: ai.RoleUser, Content: "Plan previo de arquitectura"},
		{Role: ai.RoleAssistant, Content: "Aquí está el plan: Fase 1, Fase 2 y Fase 3."},
	}

	next, _ := m.startSwarmMission("vuelvelo a hacer")
	m = asModel(next)

	if len(m.history) != 3 {
		t.Fatalf("se esperaban 3 mensajes en history, got %d", len(m.history))
	}
	if m.history[0].Content != "Plan previo de arquitectura" {
		t.Errorf("historial turno 0 corrupto: %q", m.history[0].Content)
	}
	if m.history[1].Content != "Aquí está el plan: Fase 1, Fase 2 y Fase 3." {
		t.Errorf("historial turno 1 corrupto: %q", m.history[1].Content)
	}
	if m.history[2].Content != "vuelvelo a hacer" {
		t.Errorf("historial turno 2 corrupto: %q", m.history[2].Content)
	}
}

func TestSwarmKeyboardNavigationBeyond9(t *testing.T) {
	m := swarmTestModel(t)
	m.swarm.reset("mission-nav-12")
	m.bus = &swarmBus{events: swarm.NewEventBus(), broker: swarm.NewHITLBroker()}
	gen := m.swarm.runGen

	// Registramos 12 agentes
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("agent-%d", i)
		ev := swarm.SwarmEvent{Kind: swarm.EventAgentStarted, AgentID: id, Task: "test", Index: i}
		next, _ := m.handleSwarmEvt(swarmEventMsg{gen: gen, ev: ev})
		m = asModel(next)
	}

	if m.viewTab != 0 {
		t.Fatalf("se esperaba viewTab=0 inicialmente, got: %d", m.viewTab)
	}

	// Navegación con alt+right: de 0 pasa a 1
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	m = asModel(next)
	if m.viewTab != 1 {
		t.Fatalf("alt+right: esperado viewTab=1, got: %d", m.viewTab)
	}

	// Navegación con ctrl+tab: de 1 pasa a 2
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ctrl+tab")})
	m = asModel(next)
	if m.viewTab != 2 {
		t.Fatalf("ctrl+tab: esperado viewTab=2, got: %d", m.viewTab)
	}

	// Salto directo a pestaña 9 con Alt+9
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}, Alt: true})
	m = asModel(next)
	if m.viewTab != 9 {
		t.Fatalf("alt+9: esperado viewTab=9, got: %d", m.viewTab)
	}

	// alt+right desde 9 pasa a 10 (agente >9 accesible por teclado)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	m = asModel(next)
	if m.viewTab != 10 {
		t.Fatalf("alt+right desde 9: esperado viewTab=10, got: %d", m.viewTab)
	}

	// alt+right pasa a 11 y luego a 12
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	m = asModel(next)
	if m.viewTab != 11 {
		t.Fatalf("alt+right desde 10: esperado viewTab=11, got: %d", m.viewTab)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	m = asModel(next)
	if m.viewTab != 12 {
		t.Fatalf("alt+right desde 11: esperado viewTab=12, got: %d", m.viewTab)
	}

	// Ciclo circular: alt+right desde 12 vuelve a 0 (Orquestador)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	m = asModel(next)
	if m.viewTab != 0 {
		t.Fatalf("alt+right desde 12: esperado viewTab=0 (wrap), got: %d", m.viewTab)
	}

	// alt+left desde 0 va a 12 (wrap hacia atrás)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	m = asModel(next)
	if m.viewTab != 12 {
		t.Fatalf("alt+left desde 0: esperado viewTab=12, got: %d", m.viewTab)
	}

	// Flecha izquierda/derecha cuando se inspecciona un sub-agente y el textarea está vacío
	if m.textarea.Value() != "" {
		m.textarea.Reset()
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = asModel(next)
	if m.viewTab != 11 {
		t.Fatalf("left en inspector: esperado viewTab=11, got: %d", m.viewTab)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = asModel(next)
	if m.viewTab != 12 {
		t.Fatalf("right en inspector: esperado viewTab=12, got: %d", m.viewTab)
	}
}

func TestSwarmTabBarOverflowActiveVisible(t *testing.T) {
	m := swarmTestModel(t)
	m.swarm.reset("mission-overflow")
	m.bus = &swarmBus{events: swarm.NewEventBus(), broker: swarm.NewHITLBroker()}
	gen := m.swarm.runGen

	// Registramos 12 agentes
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("sub-%d", i)
		ev := swarm.SwarmEvent{Kind: swarm.EventAgentStarted, AgentID: id, Task: "run", Index: i}
		next, _ := m.handleSwarmEvt(swarmEventMsg{gen: gen, ev: ev})
		m = asModel(next)
	}

	// Caso 1: Tab activo 0 (Orquestador) con ancho ajustado
	m.viewTab = 0
	bar0 := m.swarmTabBar(70)
	if !strings.Contains(bar0, "Orquestador") {
		t.Fatalf("tab bar en tab 0 debe contener 'Orquestador': %q", bar0)
	}
	if !strings.Contains(bar0, "▶") {
		t.Fatalf("tab bar en tab 0 debe indicar desbordamiento hacia la derecha (▶): %q", bar0)
	}

	// Caso 2: Tab activo 12 (último agente >9) con ancho ajustado
	next, _ := m.switchViewTab(12)
	m = asModel(next)
	bar12 := m.swarmTabBar(70)
	if !strings.Contains(bar12, "12:") || !strings.Contains(bar12, "sub-12") {
		t.Fatalf("tab bar en tab 12 DEBE mostrar el agente 12 activo: %q", bar12)
	}
	if !strings.Contains(bar12, "◀") {
		t.Fatalf("tab bar en tab 12 debe indicar desbordamiento hacia la izquierda (◀): %q", bar12)
	}

	// Caso 3: Tab activo intermedio (tab 6)
	next, _ = m.switchViewTab(6)
	m = asModel(next)
	bar6 := m.swarmTabBar(70)
	if !strings.Contains(bar6, "6:") || !strings.Contains(bar6, "sub-6") {
		t.Fatalf("tab bar en tab 6 DEBE mostrar el agente 6 activo: %q", bar6)
	}
	if !strings.Contains(bar6, "◀") || !strings.Contains(bar6, "▶") {
		t.Fatalf("tab bar en tab 6 debe mostrar ambos indicadores (◀ y ▶): %q", bar6)
	}

	// Caso 4: Ancho estrecho extremo (25 columnas) no debe causar pánico y debe incluir tab activo
	barNarrow := m.swarmTabBar(25)
	if !strings.Contains(barNarrow, "6:") && !strings.Contains(barNarrow, "sub-6") {
		t.Fatalf("tab bar estrecho debe priorizar tab activo: %q", barNarrow)
	}
}
