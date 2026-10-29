package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/config"
	"github.com/Yak036/mkd/internal/mcp"
	"github.com/Yak036/mkd/internal/storage"
	"github.com/Yak036/mkd/internal/swarm"
)

// TestSessionDeleteSafetyConfirmation verifica que la eliminación de sesiones (Ctrl+D)
// requiera confirmación explícita con 'y', y que por defecto seguro Enter y Esc cancelen el borrado.
func TestSessionDeleteSafetyConfirmation(t *testing.T) {
	m := newTestWorkspaceModel(t)
	s1 := storage.Session{ID: "sess-1", Title: "Sesión Importante", UpdatedAt: time.Now()}
	s2 := storage.Session{ID: "sess-2", Title: "Segunda Sesión", UpdatedAt: time.Now()}

	m.modal.kind = modalSessions
	m.modal.sessions = sessionsModal{
		sessions: []storage.Session{s1, s2},
		selected: 0,
	}

	// 1. Presionar Ctrl+D debe activar el estado de confirmación pendiente sin borrar la sesión.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)

	if !m.modal.sessions.deletePending {
		t.Fatal("Ctrl+D debía activar deletePending en modalSessions")
	}
	view := m.modalView()
	if !strings.Contains(view, "¿Eliminar sesión") {
		t.Fatalf("la vista debía advertir sobre eliminación de sesión:\n%s", view)
	}

	// 2. Presionar Enter debe CANCELAR el borrado por seguridad por defecto.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if m.modal.sessions.deletePending {
		t.Fatal("Enter debía cancelar deletePending en modalSessions")
	}
	if len(m.modal.sessions.sessions) != 2 {
		t.Fatalf("Enter no debía eliminar la sesión, quedan %d", len(m.modal.sessions.sessions))
	}

	// 3. Presionar Ctrl+D y luego Esc debe cancelar.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)
	if !m.modal.sessions.deletePending {
		t.Fatal("Ctrl+D debía activar deletePending de nuevo")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.modal.sessions.deletePending {
		t.Fatal("Esc debía cancelar deletePending")
	}
	if len(m.modal.sessions.sessions) != 2 {
		t.Fatalf("Esc no debía eliminar la sesión, quedan %d", len(m.modal.sessions.sessions))
	}

	// 4. Presionar Ctrl+D y luego confirmar explícitamente con 'y' realiza la eliminación.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(Model)

	if m.modal.sessions.deletePending {
		t.Fatal("deletePending debía quedar en false tras confirmar")
	}
	if len(m.modal.sessions.sessions) != 1 || m.modal.sessions.sessions[0].ID != "sess-2" {
		t.Fatalf("la sesión sess-1 debía haberse eliminado, quedan: %v", m.modal.sessions.sessions)
	}
}

// TestClearConversationSafetyConfirmation verifica que limpiar la conversación (Ctrl+L o /clear)
// no borre el chat directamente si contiene mensajes, sino que pida confirmación segura.
func TestClearConversationSafetyConfirmation(t *testing.T) {
	m := newTestWorkspaceModel(t)
	// Poblamos historial y bloques en la conversación activa
	m.history = []chatMessage{
		{Role: "user", Content: "Hola MKD", Display: "Hola MKD"},
	}
	m.blocks = []string{"Hola MKD"}

	// 1. Presionar Ctrl+L cuando hay contenido abre modalClearConfirm sin borrar nada aún.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = asModel(next)

	if m.modal.kind != modalClearConfirm {
		t.Fatalf("se esperaba modalClearConfirm tras Ctrl+L con historial, got: %v", m.modal.kind)
	}
	if len(m.history) == 0 {
		t.Fatal("el historial no debió borrarse al solo abrir el modal")
	}

	view := m.modalView()
	if !strings.Contains(view, "¿Deseas limpiar la conversación actual?") {
		t.Fatalf("la vista del modal no contiene la confirmación esperada:\n%s", view)
	}

	// 2. Presionar Enter cancela la limpieza por seguridad por defecto.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(next)

	if m.modal.kind != modalNone {
		t.Fatalf("Enter debía cerrar el modal a modalNone, got: %v", m.modal.kind)
	}
	if len(m.history) == 0 {
		t.Fatal("el historial no debió borrarse tras cancelar con Enter")
	}

	// 3. Probar que /clear también solicita confirmación.
	m.textarea.SetValue("/clear")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(next)

	if m.modal.kind != modalClearConfirm {
		t.Fatalf("/clear debía abrir modalClearConfirm, got: %v", m.modal.kind)
	}

	// 4. Presionar Esc cancela la limpieza.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(next)

	if m.modal.kind != modalNone || len(m.history) == 0 {
		t.Fatal("Esc debía cerrar el modal preservando el historial")
	}

	// 5. Presionar Ctrl+L y confirmar con 'y' limpia definitivamente la conversación.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = asModel(next)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = asModel(next)

	if m.modal.kind != modalNone {
		t.Fatalf("tras confirmar debía volver a modalNone, got: %v", m.modal.kind)
	}
	if len(m.history) != 0 || len(m.blocks) != 0 {
		t.Fatalf("la conversación debía haberse vaciado tras confirmar con 'y', history=%d blocks=%d",
			len(m.history), len(m.blocks))
	}

	// 6. Si la conversación ya está vacía, /clear no requiere confirmación molesta.
	m.textarea.SetValue("/clear")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(next)
	if m.modal.kind != modalNone {
		t.Fatalf("con conversación ya vacía, /clear no debía abrir modal de confirmación, got: %v", m.modal.kind)
	}
}

// TestSwarmEscCancelSafetyConfirmation verifica que presionar Esc durante una misión
// swarm activa muestre la confirmación modal y no aborte de golpe, permitiendo reanudar o cancelar.
func TestSwarmEscCancelSafetyConfirmation(t *testing.T) {
	m := newTestWorkspaceModel(t)
	m.swarm.reset("mision-test")
	m.bus = &swarmBus{events: swarm.NewEventBus(), broker: swarm.NewHITLBroker()}
	m.swarm.running = true
	m.waiting = true

	// 1. Presionar Esc durante la ejecución del Swarm debe abrir modalSwarmCancelConfirm.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.modal.kind != modalSwarmCancelConfirm {
		t.Fatalf("se esperaba modalSwarmCancelConfirm tras Esc con Swarm activo, got: %v", m.modal.kind)
	}
	if !m.swarm.running {
		t.Fatal("el swarm no debió cancelarse inmediatamente con Esc")
	}

	view := m.modalView()
	if !strings.Contains(view, "¿Cancelar la misión Swarm en curso?") {
		t.Fatalf("vista sin pregunta de confirmación:\n%s", view)
	}

	// 2. Presionar Enter cancela el intento de aborto; la misión sigue corriendo.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if m.modal.kind != modalNone {
		t.Fatalf("Enter debía cerrar el modal, got: %v", m.modal.kind)
	}
	if !m.swarm.running {
		t.Fatal("el swarm debía continuar en ejecución tras pulsar Enter")
	}

	// 3. Presionar Esc abre de nuevo el modal; presionar Esc lo cancela y mantiene la misión.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.modal.kind != modalNone || !m.swarm.running {
		t.Fatal("Esc en el modal debía mantener la misión viva")
	}

	// 4. Presionar Esc y confirmar con 'y' aborta la misión de forma controlada.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(Model)

	if m.swarm.orchestratorStatus != "Cancelada por el usuario" {
		t.Fatalf("status=%q, se esperaba 'Cancelada por el usuario'", m.swarm.orchestratorStatus)
	}
}

// TestMCPNameOverwriteSafetyConfirmation verifica que agregar un servidor MCP con un nombre
// ya existente en la configuración pida confirmación antes de sobrescribir.
func TestMCPNameOverwriteSafetyConfirmation(t *testing.T) {
	m := newTestWorkspaceModel(t)
	m.cfg.MCPServers = map[string]config.MCPServerConfig{
		"postgres": {Command: "original-cmd", Args: []string{"original-arg"}, Enabled: true},
	}
	m.mcp = mcp.NewManager(m.cfg.MCPServers)

	// Abrir modal MCP y pasar al modo creación
	next, _ := m.openMCPModal()
	m = next
	next2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	m = asModel(next2)

	// Escribir el mismo nombre ya existente pero con comando nuevo
	m.modal.mcp.name.SetValue("postgres")
	m.modal.mcp.command.SetValue("nuevo-comando")
	m.modal.mcp.args.SetValue("--nuevo")

	// 1. Guardar con Ctrl+S debe detectar que ya existe y marcar overwritePending.
	next3, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = asModel(next3)

	if !m.modal.mcp.overwritePending {
		t.Fatal("guardar un nombre MCP existente debía activar overwritePending")
	}
	// El comando original en config aún NO debe haber cambiado
	if m.cfg.MCPServers["postgres"].Command != "original-cmd" {
		t.Fatalf("la config se sobrescribió prematuramente: %v", m.cfg.MCPServers["postgres"])
	}

	view := m.modalView()
	if !strings.Contains(view, "¿Sobrescribir?") {
		t.Fatalf("la vista debía avisar sobre sobrescritura:\n%s", view)
	}

	// 2. Presionar Enter cancela la sobrescritura por defecto seguro.
	next4, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(next4)

	if m.modal.mcp.overwritePending {
		t.Fatal("Enter debía cancelar overwritePending")
	}
	if m.cfg.MCPServers["postgres"].Command != "original-cmd" {
		t.Fatal("la config cambió tras cancelar con Enter")
	}

	// 3. Volver a intentar y cancelar con Esc.
	next5, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = asModel(next5)
	if !m.modal.mcp.overwritePending {
		t.Fatal("Ctrl+S debía reactivar overwritePending")
	}
	next6, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(next6)
	if m.modal.mcp.overwritePending {
		t.Fatal("Esc debía cancelar overwritePending")
	}

	// 4. Intentar y confirmar explícitamente con 'y': la sobrescritura se aplica.
	next7, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = asModel(next7)
	next8, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = asModel(next8)

	if m.modal.mcp.creating {
		t.Fatal("tras confirmar sobrescritura debía salir del modo creating")
	}
	if m.cfg.MCPServers["postgres"].Command != "nuevo-comando" {
		t.Fatalf("la sobrescritura no se aplicó: %v", m.cfg.MCPServers["postgres"])
	}
}

// TestModelDeleteSafetyConfirmation verifica que borrar un modelo (Ctrl+D) en el gestor
// de modelos requiera confirmación con 'y' y que Enter / Esc cancelen la eliminación.
func TestModelDeleteSafetyConfirmation(t *testing.T) {
	m := newTestWorkspaceModel(t)
	pKey := m.cfg.ActiveProvider
	provider := m.cfg.Providers[pKey]
	provider.Connected = true
	provider.Models = []string{"model-a", "model-b"}
	provider.ActiveModel = "model-a"
	m.cfg.Providers[pKey] = provider

	next, _ := m.openModelsModal()
	m = next

	initialCount := len(m.cfg.Providers[pKey].Models)

	// 1. Presionar Ctrl+D activa deletePending sin borrar el modelo.
	next2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = asModel(next2)

	if !m.modal.models.deletePending {
		t.Fatal("Ctrl+D debía activar deletePending en modalModels")
	}
	view := m.modalView()
	if !strings.Contains(view, "¿Eliminar modelo") {
		t.Fatalf("la vista no contiene la confirmación de eliminación de modelo:\n%s", view)
	}

	// 2. Presionar Enter cancela por seguridad.
	next3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(next3)

	if m.modal.models.deletePending {
		t.Fatal("Enter debía cancelar deletePending")
	}
	if len(m.cfg.Providers[pKey].Models) != initialCount {
		t.Fatalf("el modelo fue alterado tras Enter: inicial=%d, actual=%d", initialCount, len(m.cfg.Providers[pKey].Models))
	}

	// 3. Presionar Ctrl+D y luego Esc cancela.
	next4, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = asModel(next4)
	next5, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(next5)
	if m.modal.models.deletePending {
		t.Fatal("Esc debía cancelar deletePending")
	}

	// 4. Presionar Ctrl+D y confirmar con 'y' elimina el modelo.
	next6, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = asModel(next6)
	next7, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = asModel(next7)

	if m.modal.models.deletePending {
		t.Fatal("deletePending debía volver a false")
	}
	if len(m.cfg.Providers[pKey].Models) != initialCount-1 {
		t.Fatalf("se esperaba que un modelo fuese eliminado: inicial=%d, actual=%d", initialCount, len(m.cfg.Providers[pKey].Models))
	}
}

// TestAgentEditorDiscardSafetyConfirmation verifica que si hay cambios sin guardar
// en el editor de un sub-agente, presionar Esc pida confirmación antes de descartar.
func TestAgentEditorDiscardSafetyConfirmation(t *testing.T) {
	m := newTestWorkspaceModel(t)
	next, _ := m.openAgentModal()
	m = next

	// Abrir el editor para el primer agente
	next2, _ := m.editAgentProfile()
	m = next2

	// Modificar un campo para que haya cambios (dirty)
	m.modal.agents.name.SetValue("Nombre Modificado Inédito")

	// 1. Presionar Esc con cambios sin guardar debe activar discardPending.
	next3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(next3)

	if !m.modal.agents.discardPending {
		t.Fatal("Esc con cambios debía activar discardPending en el editor de agentes")
	}
	if m.modal.agents.mode != agentModeEdit {
		t.Fatal("no debía salir del editor de agentes de golpe")
	}

	view := m.modalView()
	if !strings.Contains(view, "¿Descartar?") {
		t.Fatalf("la vista del editor no muestra el aviso de descarte:\n%s", view)
	}

	// 2. Presionar Enter cancela el descarte y mantiene al usuario editando.
	next4, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(next4)

	if m.modal.agents.discardPending {
		t.Fatal("Enter debía cancelar discardPending")
	}
	if m.modal.agents.mode != agentModeEdit {
		t.Fatal("el usuario debía permanecer en el editor tras Enter")
	}

	// 3. Presionar Esc de nuevo y cancelar con 'n'.
	next5, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(next5)
	next6, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = asModel(next6)

	if m.modal.agents.discardPending || m.modal.agents.mode != agentModeEdit {
		t.Fatal("'n' debía cancelar discardPending manteniéndose en edición")
	}

	// 4. Presionar Esc y confirmar con 'y' descarta los cambios y vuelve al roster.
	next7, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(next7)
	next8, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = asModel(next8)

	if m.modal.agents.mode != agentModeRoster {
		t.Fatalf("confirmar con 'y' debía volver a agentModeRoster, got: %v", m.modal.agents.mode)
	}
}

// TestAgentDeleteSafetyConfirmationDefaultSafe verifica que en el roster de agentes,
// presionar Ctrl+D pida confirmación y que Enter CANCELA en lugar de borrar accidentalmente.
func TestAgentDeleteSafetyConfirmationDefaultSafe(t *testing.T) {
	m := newTestWorkspaceModel(t)
	// Asegurar al menos 3 perfiles para poder borrar uno
	m.cfg.SubAgents = map[string]config.SubAgentProfile{
		"ag-1": {ID: "ag-1", Name: "Agente Uno", Enabled: true},
		"ag-2": {ID: "ag-2", Name: "Agente Dos", Enabled: true},
		"ag-3": {ID: "ag-3", Name: "Agente Tres", Enabled: true},
	}
	next, _ := m.openAgentModal()
	m = next

	initialCount := len(m.modal.agents.profiles)

	// 1. Presionar Ctrl+D activa deletePending.
	next2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = asModel(next2)

	if !m.modal.agents.deletePending {
		t.Fatal("Ctrl+D debía activar deletePending en roster de agentes")
	}

	// 2. Presionar Enter debe CANCELAR por defecto seguro (brecha cerrada).
	next3, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(next3)

	if m.modal.agents.deletePending {
		t.Fatal("Enter debía cancelar deletePending")
	}
	if len(m.modal.agents.profiles) != initialCount {
		t.Fatalf("Enter no debía borrar el agente; inicial=%d, actual=%d", initialCount, len(m.modal.agents.profiles))
	}

	// 3. Confirmar explícitamente con 'y' borra el sub-agente.
	next4, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = asModel(next4)
	next5, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = asModel(next5)

	if len(m.modal.agents.profiles) != initialCount-1 {
		t.Fatalf("confirmar con 'y' debía eliminar el perfil; inicial=%d, actual=%d", initialCount, len(m.modal.agents.profiles))
	}
}
