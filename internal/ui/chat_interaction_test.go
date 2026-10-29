package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/clipboard"
	"github.com/Yak036/mkd/internal/config"
	"github.com/Yak036/mkd/internal/swarm"
)

// setupTestWorkspace crea un workspace configurado en estado de trabajo listo para pruebas.
func setupTestWorkspace(t *testing.T) Model {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	cfg := config.Defaults()
	client, err := ai.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m := New(Options{Client: client, Config: cfg})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 35})
	m = next.(Model)
	m.state = stateWorkspace
	m.layout()
	return m
}

// TestCtrlBackspaceDeletesWordInTextarea verifica que Ctrl+Backspace (y su equivalente terminal Ctrl+H)
// borre la palabra anterior en el textarea sin abrir ningún modal de sesiones.
func TestCtrlBackspaceDeletesWordInTextarea(t *testing.T) {
	m := setupTestWorkspace(t)

	// Escribimos texto de prueba en el textarea
	m.textarea.SetValue("hola mundo cruel")
	m.textarea.CursorEnd()

	// 1. Simulación de Ctrl+W (borrado base)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = next.(Model)
	if !strings.HasPrefix(m.textarea.Value(), "hola mundo") {
		t.Fatalf("se esperaba que ctrl+w borrara 'cruel', obtenido: %q", m.textarea.Value())
	}

	// Restablecemos el texto
	m.textarea.SetValue("hola mundo cruel")
	m.textarea.CursorEnd()

	// 2. Simulación de Ctrl+Backspace enviada como byte 0x08 / ctrl+h (terminales estándar Linux / xterm)
	msgCtrlH := tea.KeyMsg{Type: tea.KeyCtrlH}
	next, _ = m.Update(msgCtrlH)
	m = next.(Model)

	// Debe haber borrado "cruel", quedando "hola mundo " o "hola mundo"
	val := m.textarea.Value()
	if val == "hola mundo cruel" {
		t.Fatalf("Ctrl+H no borró la palabra anterior: %q", val)
	}
	if strings.Contains(val, "cruel") {
		t.Fatalf("la palabra 'cruel' no debió quedar tras Ctrl+H: %q", val)
	}

	// Comprobamos críticamente que NO se haya abierto el modal de sesiones
	if m.modal.kind != modalNone {
		t.Fatalf("Ctrl+H abrió indebidamente un modal: tipo=%v", m.modal.kind)
	}

	// 3. Simulación de tecla explícita "ctrl+backspace" en updateWorkspaceKey
	m.textarea.SetValue("palabra uno dos")
	m.textarea.CursorEnd()
	nextModel, _ := m.updateWorkspaceKey(tea.KeyMsg(tea.Key{Type: tea.KeyCtrlH}))
	m = nextModel.(Model)
	if m.modal.kind != modalNone {
		t.Fatalf("ctrl+backspace abrió el modal de sesiones: %v", m.modal.kind)
	}
	if strings.HasSuffix(m.textarea.Value(), "dos") {
		t.Fatalf("se esperaba que se borrara 'dos', obtenido: %q", m.textarea.Value())
	}
}

// TestCtrlSOpensSessionsModal verifica que el atajo de sesiones se haya reubicado a Ctrl+S
// y no colisione con Ctrl+Backspace.
func TestCtrlSOpensSessionsModal(t *testing.T) {
	m := setupTestWorkspace(t)

	// Al pulsar Ctrl+S se debe abrir el modal de sesiones
	msgCtrlS := tea.KeyMsg{Type: tea.KeyCtrlS}
	next, _ := m.Update(msgCtrlS)
	m = next.(Model)

	if m.modal.kind != modalSessions {
		t.Fatalf("Ctrl+S debía abrir modalSessions, pero kind es: %v", m.modal.kind)
	}

	// Cerramos el modal
	m.modal.kind = modalNone

	// Al pulsar Ctrl+H NO debe abrirse modalSessions
	msgCtrlH := tea.KeyMsg{Type: tea.KeyCtrlH}
	next, _ = m.Update(msgCtrlH)
	m = next.(Model)

	if m.modal.kind == modalSessions {
		t.Fatalf("Ctrl+H no debe abrir modalSessions")
	}
}

// TestAgentTabScrollingWheelAndKeyboard valida el scroll completo del visor de agente
// tanto por rueda del ratón como por teclado (PgUp, PgDown, Up, Down, Home, End).
func TestAgentTabScrollingWheelAndKeyboard(t *testing.T) {
	m := setupTestWorkspace(t)

	// Configuramos una misión swarm activa y seleccionamos la pestaña 1
	m.swarm = newSwarmState()
	m.swarm.running = true
	m.swarm.mission = "misión de prueba"
	profile := config.SubAgentProfile{ID: "coder", Name: "Programador", Icon: "💻"}
	m.cfg.SubAgents = map[string]config.SubAgentProfile{"coder": profile}
	live := m.swarm.agent(profile)
	live.Index = 1
	live.Status = swarm.StatusRunning
	live.Task = "Implementar interacción TUI"
	live.Instructions = "Instrucciones detalladas del orquestador para el programador"

	// Agregamos varios pasos y líneas de salida largas
	for i := 1; i <= 30; i++ {
		live.Steps = append(live.Steps, agentStep{
			Tool: "patch_file",
			Args: `{"path": "workspace.go"}`,
			Done: true,
		})
		live.Output.WriteString(strings.Repeat("Salida del agente paso ", 2) + "\n")
	}

	// Cambiamos a la pestaña 1
	next, _ := m.switchViewTab(1)
	m = next.(Model)

	if m.viewTab != 1 {
		t.Fatalf("se esperaba viewTab=1, obtenido %d", m.viewTab)
	}
	if m.agentScroll != 0 {
		t.Fatalf("agentScroll inicial debe ser 0 (cola en vivo), obtenido %d", m.agentScroll)
	}

	// 1. Rueda del ratón hacia arriba (WheelUp): debe incrementar el scroll
	mouseWheelUp := tea.MouseMsg{
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	}
	next, _ = m.Update(mouseWheelUp)
	m = next.(Model)
	if m.agentScroll != 3 {
		t.Fatalf("WheelUp debía incrementar agentScroll a 3, obtenido %d", m.agentScroll)
	}

	// 2. Rueda del ratón hacia abajo (WheelDown): debe decrementar el scroll
	mouseWheelDown := tea.MouseMsg{
		Button: tea.MouseButtonWheelDown,
		Action: tea.MouseActionPress,
	}
	next, _ = m.Update(mouseWheelDown)
	m = next.(Model)
	if m.agentScroll != 0 {
		t.Fatalf("WheelDown debía decrementar agentScroll a 0, obtenido %d", m.agentScroll)
	}

	// 3. WheelDown por debajo de cero debe acotarse en 0
	next, _ = m.Update(mouseWheelDown)
	m = next.(Model)
	if m.agentScroll < 0 {
		t.Fatalf("agentScroll no debe ser negativo, obtenido %d", m.agentScroll)
	}

	// 4. Teclado: PgUp / PgDown
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(Model)
	expectedPage := m.agentPageDelta()
	if m.agentScroll != expectedPage {
		t.Fatalf("PgUp debía incrementar agentScroll a %d, obtenido %d", expectedPage, m.agentScroll)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(Model)
	if m.agentScroll != 0 {
		t.Fatalf("PgDown debía volver agentScroll a 0, obtenido %d", m.agentScroll)
	}

	// 5. Teclado: Flechas Up / Down con textarea vacío
	m.textarea.Reset()
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if m.agentScroll != 1 {
		t.Fatalf("Flecha arriba con input vacío debía poner agentScroll=1, obtenido %d", m.agentScroll)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if m.agentScroll != 0 {
		t.Fatalf("Flecha abajo debía decrementar agentScroll a 0, obtenido %d", m.agentScroll)
	}

	// 6. Home / End
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = next.(Model)
	if m.agentScroll <= 0 {
		t.Fatalf("Home debía saltar hacia arriba, obtenido %d", m.agentScroll)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(Model)
	if m.agentScroll != 0 {
		t.Fatalf("End debía saltar al final (0), obtenido %d", m.agentScroll)
	}

	// 7. Renderizado: comprobar que la vista contiene el indicador de scroll
	m.agentScroll = 5
	view := m.renderAgentInspector(live, 80, 20)
	if !strings.Contains(view, "scroll activo") {
		t.Fatalf("la vista del inspector con scroll=5 debía mostrar 'scroll activo', vista:\n%s", view)
	}

	// 8. Cambiar de pestaña debe reiniciar el scroll a 0
	next, _ = m.switchViewTab(0)
	m = next.(Model)
	if m.agentScroll != 0 {
		t.Fatalf("cambiar de pestaña debía reiniciar agentScroll a 0, obtenido %d", m.agentScroll)
	}
}

// TestMouseToggle verifica la alternancia entre modo capturado y modo de selección nativa.
func TestMouseToggle(t *testing.T) {
	m := setupTestWorkspace(t)

	if !m.mouseCapture {
		t.Fatal("mouseCapture debe arrancar en true por defecto")
	}

	// Alternar con /mouse
	next, cmd := m.handleSlash("/mouse")
	m = next.(Model)

	if m.mouseCapture {
		t.Fatal("primer /mouse debía desactivar mouseCapture para selección nativa")
	}
	if cmd == nil {
		t.Fatal("primer /mouse debía devolver tea.DisableMouse cmd")
	}
	if !strings.Contains(m.notice, "selección nativa") {
		t.Fatalf("aviso no menciona selección nativa: %q", m.notice)
	}

	// Alternar de vuelta
	next, cmd = m.handleSlash("/mouse")
	m = next.(Model)

	if !m.mouseCapture {
		t.Fatal("segundo /mouse debía reactivar mouseCapture")
	}
	if cmd == nil {
		t.Fatal("segundo /mouse debía devolver tea.EnableMouseCellMotion cmd")
	}
	if !strings.Contains(m.notice, "capturado") {
		t.Fatalf("aviso no menciona modo capturado: %q", m.notice)
	}
}

// TestCopyPromptAndResponse verifica la copia del último prompt y de la última respuesta al portapapeles.
func TestCopyPromptAndResponse(t *testing.T) {
	m := setupTestWorkspace(t)

	var lastCopied string
	oldCopy := clipboard.CopyFn
	clipboard.CopyFn = func(text string) error {
		lastCopied = text
		return nil
	}
	defer func() { clipboard.CopyFn = oldCopy }()

	// Agregamos mensajes de historial
	m.history = []chatMessage{
		{Role: ai.RoleUser, Content: "Mi primer prompt de prueba"},
		{Role: ai.RoleAssistant, Content: "Esta es una respuesta en texto plano sin bloques de código."},
	}

	// 1. /copy prompt
	next, _ := m.handleSlash("/copy prompt")
	m = next.(Model)
	if lastCopied != "Mi primer prompt de prueba" {
		t.Fatalf("/copy prompt copió %q, esperado %q", lastCopied, "Mi primer prompt de prueba")
	}
	if !strings.Contains(m.notice, "Prompt copiado") {
		t.Fatalf("aviso incorrecto: %q", m.notice)
	}

	// 2. /copy text
	lastCopied = ""
	next, _ = m.handleSlash("/copy text")
	m = next.(Model)
	if lastCopied != "Esta es una respuesta en texto plano sin bloques de código." {
		t.Fatalf("/copy text copió %q", lastCopied)
	}
	if !strings.Contains(m.notice, "Respuesta copiada") {
		t.Fatalf("aviso incorrecto: %q", m.notice)
	}

	// 3. /copy cuando no hay bloques de código debe hacer fallback a la respuesta
	lastCopied = ""
	next, _ = m.copyLastBlock()
	m = next.(Model)
	if lastCopied != "Esta es una respuesta en texto plano sin bloques de código." {
		t.Fatalf("/copy fallback copió %q", lastCopied)
	}
}
