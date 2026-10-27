package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/agent"
	"github.com/Yak036/mkd/internal/config"
	mkdcontext "github.com/Yak036/mkd/internal/context"
)

// mkTestModel construye un Model mínimo para probar flujos sin TTY.
func mkTestModel(t *testing.T, dir string, mode string) Model {
	t.Helper()
	cfg := config.Defaults()
	cfg.AgentMode = mode
	cfg.Normalize()
	return Model{
		cfg:      cfg,
		state:    stateWorkspace,
		textarea: textarea.New(),
		ctxMgr:   mkdcontext.NewManager(cfg.SystemPrompt, dir, cfg.CompactThreshold),
		width:    120,
		height:   40,
	}
}

// El modo plan bloquea write_file y devuelve role=tool al modelo.
func TestAgentPlanBlocksWrite(t *testing.T) {
	dir := t.TempDir()
	m := mkTestModel(t, dir, config.ModePlan)
	m.ensureAgentState()
	m.agent.pending = []agent.Call{{ID: "c1", Name: "write_file", Arguments: `{"path":"x.txt","content":"y"}`}}

	next, cmd := m.resolveNextProposal()
	if cmd != nil {
		t.Fatal("plan no debe ejecutar")
	}
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); err == nil {
		t.Fatal("plan escribió el archivo")
	}
	found := false
	for _, msg := range next.agent.messages {
		if msg.Role == "tool" && msg.ToolCallID == "c1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sin role=tool de bloqueo: %#v", next.agent.messages)
	}
}

// El modal HITL rechaza con N y reporta el rechazo al modelo.
func TestAgentRejectReportsToModel(t *testing.T) {
	dir := t.TempDir()
	m := mkTestModel(t, dir, config.ModeDefault)
	m.ensureAgentState()
	m.agent.pending = []agent.Call{{ID: "c1", Name: "write_file", Arguments: `{"path":"x.txt","content":"y"}`}}

	next, _ := m.resolveNextProposal()
	got := next
	if got.modal.kind != modalDiff {
		t.Fatalf("no abrió HITL: %v", got.modal.kind)
	}
	after, _ := got.updateAgentDiffKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	res := after
	rejected := false
	for _, msg := range res.agent.messages {
		if msg.Role == "tool" && msg.ToolCallID == "c1" && msg.Content == agent.RejectedContent {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("sin rechazo al modelo: %#v", res.agent.messages)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); err == nil {
		t.Fatal("el rechazo escribió el archivo")
	}
}

// [A] activa autoApprove de sesión para la herramienta.
func TestAgentAlwaysApprovesForSession(t *testing.T) {
	dir := t.TempDir()
	m := mkTestModel(t, dir, config.ModeDefault)
	m.ensureAgentState()
	m.agent.pending = []agent.Call{{ID: "c2", Name: "write_file", Arguments: `{"path":"a.txt","content":"b"}`}}

	next, _ := m.resolveNextProposal()
	got := next
	if got.modal.kind != modalDiff {
		t.Fatalf("sin modal: %v", got.modal.kind)
	}
	after, _ := got.updateAgentDiffKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	res := after
	if !res.agent.autoApprove["write_file"] {
		t.Fatal("[A] no activó autoApprove")
	}
}

// Enter sobre un archivo en el popup @ lo registra en el sidebar.
func TestMentionEnterAddsToSidebar(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "target.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := mkTestModel(t, dir, config.ModeChat)
	m.textarea.SetValue("@target")
	m.updateMentions()
	if !m.MentionPopupVisible() {
		t.Fatal("popup no visible")
	}
	next, _ := m.applySelectedMention()
	got := next.(*Model)
	if len(got.contextFiles) != 1 || got.contextFiles[0].Size == 0 {
		t.Fatalf("sidebar sin archivo: %#v", got.contextFiles)
	}
}

// El ciclo Ctrl+T rota default → auto → plan → default; chat solo por /mode.
func TestAgentModeCycle(t *testing.T) {
	dir := t.TempDir()
	m := mkTestModel(t, dir, config.ModeDefault)
	var order []string
	for i := 0; i < 3; i++ {
		m.cfg.AgentMode = config.NextMode(m.cfg.AgentMode)
		order = append(order, config.ModeLabel(m.cfg.AgentMode))
	}
	want := []string{"AUTO", "PLAN", "DEFAULT"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ciclo = %v, want %v", order, want)
		}
	}
	if config.ModeLabel(config.NormalizeMode("chat")) != "CHAT" {
		t.Fatal("chat no normaliza")
	}
}

// navModel construye un árbol interno/ai + internal/agent para probar rutas.
func navModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "internal", "ai"), 0o755))
	must(os.MkdirAll(filepath.Join(dir, "internal", "agent"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "internal", "ai", "client.go"), []byte("x"), 0o600))
	must(os.WriteFile(filepath.Join(dir, "internal", "agent", "tools.go"), []byte("y"), 0o600))
	return mkTestModel(t, dir, config.ModeChat)
}

func mentionTokens(m Model) []string {
	out := make([]string, 0, len(m.mentions.popup.matches))
	for _, it := range m.mentions.popup.matches {
		out = append(out, it.Token)
	}
	return out
}

// El autocompletado debe comportarse como una shell: "dir/" lista hijos,
// "dir/pref" resuelve rutas completas y los directorios van primero.
func TestMentionShellLikeNavigation(t *testing.T) {
	m := navModel(t)

	m.textarea.SetValue("@internal/")
	m.updateMentions()
	got := mentionTokens(m)
	if len(got) != 2 || got[0] != "@internal/agent/" || got[1] != "@internal/ai/" {
		t.Fatalf("@internal/ -> %v", got)
	}

	m.textarea.SetValue("@internal/ai/")
	m.updateMentions()
	got = mentionTokens(m)
	if len(got) != 1 || got[0] != "@internal/ai/client.go" {
		t.Fatalf("@internal/ai/ -> %v", got)
	}

	m.textarea.SetValue("@internal/ai/cl")
	m.updateMentions()
	got = mentionTokens(m)
	if len(got) != 1 || got[0] != "@internal/ai/client.go" {
		t.Fatalf("@internal/ai/cl -> %v", got)
	}
}

// Tab sobre un directorio completa con "/" y mantiene el popup navegable.
func TestMentionTabDescends(t *testing.T) {
	m := navModel(t)
	m.textarea.SetValue("@internal/")
	m.updateMentions()
	for i, it := range m.mentions.popup.matches {
		if it.Token == "@internal/agent/" {
			m.mentions.popup.selected = i
		}
	}
	next, _ := m.completeMention()
	got := next
	if v := got.textarea.Value(); v != "@internal/agent/" {
		t.Fatalf("Tab no descendió: %q", v)
	}
	if !got.MentionPopupVisible() {
		t.Fatal("Tab sobre dir debe mantener el popup")
	}
}

// Enter sobre un directorio navega dentro sin anclar nada ni abrir modales.
func TestMentionEnterOnDirNavigates(t *testing.T) {
	m := navModel(t)
	m.textarea.SetValue("@internal/")
	m.updateMentions()
	for i, it := range m.mentions.popup.matches {
		if it.Token == "@internal/ai/" {
			m.mentions.popup.selected = i
		}
	}
	next, _ := m.applySelectedMention()
	got := *(next.(*Model))
	if v := got.textarea.Value(); v != "@internal/ai/" {
		t.Fatalf("Enter sobre dir no completó: %q", v)
	}
	if got.modal.kind != modalNone {
		t.Fatalf("Enter sobre dir abrió un modal inesperado: %v", got.modal.kind)
	}
	if len(got.contextFiles) != 0 {
		t.Fatal("Enter sobre dir no debe anclar archivos")
	}
}

// Al terminar el splash sin modelo activo se elige el primero en vez de abrir
// el gestor de modelos (regresión del modal intrusivo al arrancar).
func TestFinishSplashDoesNotOpenModelsModal(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	provider := cfg.Providers[cfg.ActiveProvider]
	provider.ActiveModel = ""
	provider.APIKey = "sk-test-123456"
	provider.Models = []string{"modelo-a", "modelo-b"}
	cfg.Providers[cfg.ActiveProvider] = provider
	cfg.Normalize()
	cfg.Providers[cfg.ActiveProvider] = provider

	m := mkTestModel(t, dir, config.ModeChat)
	m.cfg = cfg
	m.width, m.height = 120, 40

	next, _ := m.finishSplash()
	got := next.(Model)
	if got.modal.kind == modalModels {
		t.Fatal("finishSplash no debe abrir el modal de modelos")
	}
	if active, ok := got.cfg.Active(); !ok || active.ActiveModel == "" {
		t.Fatal("debía elegir un modelo por defecto")
	}
}

// skillModel crea un Model con una carpeta de skills temporal.
func skillModel(t *testing.T) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "go-reviewer"), 0o755); err != nil {
		t.Fatal(err)
	}
	skillDoc := "---\ndescription: Revisa Go\n---\n\nRevisa el código."
	if err := os.WriteFile(filepath.Join(dir, "go-reviewer", "SKILL.md"), []byte(skillDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy", "SKILL.md"), []byte("Pasos de deploy."), 0o644); err != nil {
		t.Fatal(err)
	}

	m := mkTestModel(t, t.TempDir(), config.ModeChat)
	m.cfg.SkillsDir = dir
	return m, dir
}

// `:@` abre el popup de skills y `Enter` activa la elegida.
func TestSkillMentionFlow(t *testing.T) {
	m, _ := skillModel(t)
	m.textarea.SetValue(":@")
	m.updateMentions()
	if !m.MentionPopupVisible() || !m.mentions.popup.skillsMode {
		t.Fatal(":@ no abrió el modo skills")
	}
	if len(m.mentions.popup.matches) != 2 {
		t.Fatalf("skills=%d", len(m.mentions.popup.matches))
	}

	m.textarea.SetValue(":@go")
	m.updateMentions()
	next, _ := m.applySelectedMention()
	got := *(next.(*Model))
	if len(got.contextFiles) != 1 || !got.contextFiles[0].Skill || got.contextFiles[0].Label != "go-reviewer" {
		t.Fatalf("sidebar sin skill: %#v", got.contextFiles)
	}
	if block := got.mentionsContextBlock(); !strings.Contains(block, `<skill name="go-reviewer"`) {
		t.Fatalf("skill no inyectada: %q", block)
	}
	if banner := got.confirmationBanner(); !strings.Contains(banner, ":@go-reviewer") {
		t.Fatalf("sin banner de confirmación: %q", banner)
	}
}

// El trigger de archivos sigue funcionando tras añadir el de skills.
func TestMentionFileTriggerUnaffected(t *testing.T) {
	m, _ := skillModel(t)
	m.textarea.SetValue("@")
	m.updateMentions()
	if m.mentions.popup.skillsMode {
		t.Fatal("@ no debe entrar en modo skills")
	}
	if !m.MentionPopupVisible() {
		t.Fatal("@ debía abrir el popup de archivos")
	}
	for _, it := range m.mentions.popup.matches {
		if it.Skill {
			t.Fatalf("skill en popup de archivos: %#v", it)
		}
	}
}

// rulesModel crea un proyecto temporal con reglas y un Model apuntando a él.
func rulesModel(t *testing.T) (Model, string) {
	t.Helper()
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644))
	must(os.MkdirAll(filepath.Join(root, ".mkd", "rules"), 0o755))
	must(os.WriteFile(filepath.Join(root, ".mkd", "rules", "go-style.md"),
		[]byte("---\ndescription: Estilo Go\nglobs: internal/**/*.go\n---\n\nUsa gofmt."), 0o644))
	must(os.WriteFile(filepath.Join(root, ".mkd", "rules", "manual.md"),
		[]byte("---\ndescription: Regla manual\n---\n\nSolo cuando la piden."), 0o644))
	must(os.WriteFile(filepath.Join(root, ".mkd", "rules", "always.md"),
		[]byte("---\nalwaysApply: true\n---\n\nSiempre presente."), 0o644))

	m := mkTestModel(t, root, config.ModeChat)
	m.cfg.RulesDir = filepath.Join(t.TempDir(), "global-rules")
	m.applyRulesConfig()
	return m, root
}

// El popup `:@` incluye reglas además de skills.
func TestMentionRuleMatchesInPopup(t *testing.T) {
	m, _ := rulesModel(t)
	m.textarea.SetValue(":@")
	m.updateMentions()
	if !m.MentionPopupVisible() {
		t.Fatal(":@ no abrió el popup")
	}
	found := map[string]bool{}
	for _, it := range m.mentions.popup.matches {
		if it.Rule {
			found[it.Token] = true
		}
	}
	for _, want := range []string{":@go-style", ":@manual", ":@always"} {
		if !found[want] {
			t.Fatalf("falta %s en el popup: %v", want, found)
		}
	}
}

// Forzar una regla la marca como activa y la pasa al ContextManager.
func TestApplyRuleForcesIt(t *testing.T) {
	m, _ := rulesModel(t)
	m.textarea.SetValue(":@manual")
	m.updateMentions()
	next, _ := m.applySelectedMention()
	got := *(next.(*Model))

	if !got.ruleActive("manual") {
		t.Fatalf("regla no forzada: %v", got.agent.manualRules)
	}
	if len(got.agent.manualRules) != 1 || got.agent.manualRules[0] != "manual" {
		t.Fatalf("manualRules=%v", got.agent.manualRules)
	}
	if banner := got.confirmationBanner(); !strings.Contains(banner, ":@manual") {
		t.Fatalf("sin confirmación: %q", banner)
	}
}

// alwaysApply entra sin forzar; globs entra solo si toca el archivo anclado.
func TestRulesInjectedBySelection(t *testing.T) {
	m, root := rulesModel(t)

	// Sin archivos: solo la regla alwaysApply.
	m.refreshUsage()
	rules := m.ctxMgr.Rules()
	if len(rules) != 3 {
		t.Fatalf("reglas descubiertas=%d", len(rules))
	}
	sel := m.ctxMgr.RuleSelection()
	if sel.Always != 1 || sel.Matched != 0 {
		t.Fatalf("selección sin archivos: %+v", sel)
	}

	// Con un .go dentro de internal/ se activa también la de globs.
	target := filepath.Join(root, "internal", "ai", "client.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package ai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ctxMgr.AddFile(target); err != nil {
		t.Fatal(err)
	}
	m.refreshUsage()
	sel = m.ctxMgr.RuleSelection()
	if sel.Always != 1 || sel.Matched != 1 {
		t.Fatalf("selección con archivo: %+v", sel)
	}

	prepared, _ := m.ctxMgr.Prepare("", nil)
	if !strings.Contains(prepared.System, "<project_rules>") ||
		!strings.Contains(prepared.System, "Siempre presente.") ||
		!strings.Contains(prepared.System, "Usa gofmt.") {
		t.Fatalf("system sin reglas: %q", prepared.System)
	}
	if strings.Contains(prepared.System, "Solo cuando la piden.") {
		t.Fatal("la regla manual no debía inyectarse")
	}
}

// /rule rm libera una regla forzada.
func TestRemoveForcedRule(t *testing.T) {
	m, _ := rulesModel(t)
	m.agent.manualRules = []string{"manual"}
	next, _ := m.removeRuleForced("manual")
	got := next.(Model)
	if got.ruleActive("manual") {
		t.Fatalf("sigue forzada: %v", got.agent.manualRules)
	}
}

// El footer de acciones del editor de sub-agentes debe verse siempre, incluso
// con el prompt enfocado en terminales de 24 filas (regresión reportada:
// "no me deja bajar para ir a la opción de abajo").
func TestAgentEditorFooterAlwaysVisible(t *testing.T) {
	for _, size := range []struct{ w, h int }{{140, 45}, {120, 30}, {100, 24}} {
		cfg := config.Defaults()
		m := New(Options{Client: nil, Config: cfg})
		next, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = next.(Model)
		m.state = stateWorkspace
		m.layout()

		opened, _ := m.openAgentModal()
		m = opened
		created, _ := m.startNewAgentProfile()
		m = created

		if view := m.agentEditorView(); !strings.Contains(view, "Guardar") {
			t.Fatalf("%dx%d: footer ausente al abrir", size.w, size.h)
		}

		// Bajar hasta el prompt (5 campos) y comprobar que sigue visible.
		for i := 0; i < 5; i++ {
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
			m = next.(Model)
		}
		view := m.agentEditorView()
		if !strings.Contains(view, "Guardar") || !strings.Contains(view, "Volver") {
			t.Fatalf("%dx%d: footer ausente en Prompt", size.w, size.h)
		}
		if lines := len(strings.Split(view, "\n")); lines > m.modalOuterHeight()+2 {
			t.Fatalf("%dx%d: el modal (%d líneas) excede el alto disponible (%d)",
				size.w, size.h, lines, m.modalOuterHeight()+2)
		}

		// Tab sigue permitiendo cambiar de campo desde el prompt.
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = next.(Model)
		if m.modal.agents.field == agentFieldPrompt {
			t.Fatalf("%dx%d: Tab no sale del prompt", size.w, size.h)
		}
	}
}
