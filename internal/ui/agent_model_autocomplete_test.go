package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/clipboard"
)

// TestSubAgentProviderSelectionAndModelAutocomplete comprueba que en el editor
// de sub-agentes se puede seleccionar el proveedor con flechas y que el modelo
// se autocompleta o se cicla sin tener que escribirlo manualmente.
func TestSubAgentProviderSelectionAndModelAutocomplete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := swarmTestModel(t)

	// Configuramos modelos en el proveedor openrouter
	prov := m.cfg.Providers["openrouter"]
	prov.Models = []string{"anthropic/claude-3.5-sonnet", "google/gemini-2.5-pro", "deepseek/deepseek-chat"}
	prov.Connected = true
	m.cfg.Providers["openrouter"] = prov

	// Abrir gestor de sub-agentes e iniciar creación de nuevo perfil
	opened, _ := m.openAgentModal()
	m = opened
	created, _ := m.startNewAgentProfile()
	m = created

	am := &m.modal.agents
	if am.mode != agentModeEdit {
		t.Fatal("debía estar en modo edición")
	}

	// 1. Navegar hasta el campo Proveedor (agentFieldProvider)
	for am.field != agentFieldProvider {
		next, _ := m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyDown})
		m = asModel(next)
		am = &m.modal.agents
	}

	if am.field != agentFieldProvider {
		t.Fatalf("campo actual debería ser Proveedor, pero es %v", am.field)
	}

	// Cambiar proveedor con flecha derecha hasta llegar a "openrouter"
	for am.providers[am.providerIndex] != "openrouter" {
		next, _ := m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyRight})
		m = asModel(next)
		am = &m.modal.agents
	}

	if am.providers[am.providerIndex] != "openrouter" {
		t.Fatalf("se esperaba openrouter seleccionado, pero quedó: %s", am.providers[am.providerIndex])
	}

	// 2. Bajar al campo Modelo (agentFieldModel)
	next, _ := m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyDown})
	m = asModel(next)
	am = &m.modal.agents

	if am.field != agentFieldModel {
		t.Fatalf("campo actual debería ser Modelo, pero es %v", am.field)
	}

	// 3. Probar ciclado de modelos con Alt+Right sin escribir nada manualmente
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}, Alt: true})
	// Enviamos mensaje alt+right
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	m = asModel(next)
	am = &m.modal.agents

	if am.model.Value() != "anthropic/claude-3.5-sonnet" {
		t.Fatalf("alt+right debía seleccionar el primer modelo, got: %q", am.model.Value())
	}

	// Ciclar al siguiente
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	m = asModel(next)
	am = &m.modal.agents

	if am.model.Value() != "google/gemini-2.5-pro" {
		t.Fatalf("alt+right debía ciclar al segundo modelo, got: %q", am.model.Value())
	}

	// 4. Probar autocompletado con Tab escribiendo parte del nombre
	am.model.SetValue("deepseek")
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyTab})
	m = asModel(next)
	am = &m.modal.agents

	if am.model.Value() != "deepseek/deepseek-chat" {
		t.Fatalf("Tab debía autocompletar deepseek/deepseek-chat, got: %q", am.model.Value())
	}

	// 5. Guardar el perfil y verificar que Provider y Model quedan persistidos
	am.name.SetValue("OpenRouter Specialist")
	next, _ = m.saveAgentProfile()
	m = asModel(next)

	savedProfile, ok := m.cfg.SubAgents["openrouter-specialist"]
	if !ok {
		t.Fatal("no se encontró el subagente guardado")
	}
	if savedProfile.Provider != "openrouter" {
		t.Fatalf("proveedor no persistido en el perfil: %q", savedProfile.Provider)
	}
	if savedProfile.Model != "deepseek/deepseek-chat" {
		t.Fatalf("modelo no persistido en el perfil: %q", savedProfile.Model)
	}

	// Verificar que la vista de detalle en el roster muestre el modelo y proveedor
	view := m.agentModalView()
	if !strings.Contains(view, "deepseek/deepseek-chat (openrout") {
		t.Fatalf("la vista del roster no muestra el modelo y proveedor correctamente: %s", view)
	}
}

// TestSubAgentOnlyConnectedProviders verifica que en el editor de sub-agentes
// únicamente se muestren proveedores que tienen conexión exitosa comprobada.
func TestSubAgentOnlyConnectedProviders(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := swarmTestModel(t)

	// Marcamos solo "openrouter" como conectado, los demás desconectados
	for k, p := range m.cfg.Providers {
		p.Connected = (k == "openrouter")
		m.cfg.Providers[k] = p
	}

	opened, _ := m.openAgentModal()
	m = opened
	created, _ := m.startNewAgentProfile()
	m = created

	am := m.modal.agents
	for _, prov := range am.providers {
		if prov != "" && prov != "openrouter" {
			t.Fatalf("proveedor no conectado %q no debería aparecer en la lista de sub-agentes: %v", prov, am.providers)
		}
	}
}

// TestSubAgentCopyPrompt verifica que desde el roster o el editor se puede copiar
// el prompt/descripción del sub-agente seleccionado con notificación.
func TestSubAgentCopyPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := swarmTestModel(t)

	var lastCopied string
	oldCopy := clipboard.CopyFn
	clipboard.CopyFn = func(text string) error {
		lastCopied = text
		return nil
	}
	defer func() { clipboard.CopyFn = oldCopy }()

	opened, _ := m.openAgentModal()
	m = opened

	// En el roster, presionar 'c' copia el prompt del agente seleccionado
	next, _ := m.updateAgentModal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = asModel(next)

	if !strings.Contains(m.modal.agents.status, "copiado al portapapeles") {
		t.Fatalf("se esperaba estado de copiado en el roster, got: %q", m.modal.agents.status)
	}
	if lastCopied == "" {
		t.Fatal("el portapapeles debía recibir el texto del prompt")
	}

	// Abrir editor y presionar Ctrl+Y
	edit, _ := m.editAgentProfile()
	m = asModel(edit)
	m.modal.agents.prompt.SetValue("Prompt personalizado de prueba")

	next2, _ := m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyCtrlY})
	m = asModel(next2)

	if !strings.Contains(m.modal.agents.status, "Prompt copiado al portapapeles") {
		t.Fatalf("se esperaba confirmación al copiar desde el editor, got: %q", m.modal.agents.status)
	}
	if lastCopied != "Prompt personalizado de prueba" {
		t.Fatalf("se esperaba el texto editado en el portapapeles, got: %q", lastCopied)
	}
}

// TestSubAgentToolsInteractiveSelector verifica que el campo Tools es un selector interactivo
// navegable con flechas y alternable con Space sin tener que tipear nombres manuales.
func TestSubAgentToolsInteractiveSelector(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := swarmTestModel(t)

	opened, _ := m.openAgentModal()
	m = opened
	created, _ := m.startNewAgentProfile()
	m = created

	am := &m.modal.agents
	// Navegar hasta el campo Tools
	for am.field != agentFieldTools {
		next, _ := m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyDown})
		m = asModel(next)
		am = &m.modal.agents
	}

	if am.field != agentFieldTools {
		t.Fatalf("campo actual debía ser Tools, pero es %v", am.field)
	}

	// Por defecto para nuevo perfil: read_file, list_dir, search_code activos
	if !am.toolsSelected["read_file"] || !am.toolsSelected["list_dir"] || !am.toolsSelected["search_code"] {
		t.Fatalf("se esperaban tools de lectura por defecto activas: %v", am.toolsSelected)
	}
	if am.toolsSelected["run_command"] {
		t.Fatal("run_command no debería estar activo por defecto")
	}

	// Mover cursor a la derecha hasta run_command (índice 5)
	for i := 0; i < 5; i++ {
		next, _ := m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyRight})
		m = asModel(next)
		am = &m.modal.agents
	}

	if am.toolsList[am.toolCursor] != "run_command" {
		t.Fatalf("cursor en tool incorrecta: %q (esperado run_command)", am.toolsList[am.toolCursor])
	}

	// Activar run_command con Espacio
	next, _ := m.updateAgentEditor(tea.KeyMsg{Type: tea.KeySpace})
	m = asModel(next)
	am = &m.modal.agents

	if !am.toolsSelected["run_command"] {
		t.Fatal("run_command debió activarse con Espacio")
	}

	// Probar alternar todas con 'a'
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = asModel(next)
	am = &m.modal.agents

	// Como no todas estaban activas, 'a' debe marcar todas
	for _, tool := range am.toolsList {
		if !am.toolsSelected[tool] {
			t.Fatalf("tool %q debió quedar seleccionada al presionar 'a'", tool)
		}
	}

	// Deseleccionar una tool específica (ej. mcp:*)
	for am.toolsList[am.toolCursor] != "mcp:*" {
		next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyRight})
		m = asModel(next)
		am = &m.modal.agents
	}
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(next)
	am = &m.modal.agents

	if am.toolsSelected["mcp:*"] {
		t.Fatal("mcp:* debió deseleccionarse con Enter")
	}

	// Guardar perfil y verificar que las tools quedaron correctamente persistidas
	am.name.SetValue("Tool Master")
	saved, _ := m.saveAgentProfile()
	m = asModel(saved)

	prof, ok := m.cfg.SubAgents["tool-master"]
	if !ok {
		t.Fatal("no se encontró el subagente guardado tool-master")
	}

	// Debe contener run_command y NO debe contener mcp:*
	hasRun := false
	hasMCP := false
	for _, tool := range prof.AllowedTools {
		if tool == "run_command" {
			hasRun = true
		}
		if tool == "mcp:*" {
			hasMCP = true
		}
	}
	if !hasRun {
		t.Fatal("AllowedTools debió incluir run_command")
	}
	if hasMCP {
		t.Fatal("AllowedTools NO debió incluir mcp:*")
	}
}

// TestSubAgentPromptEditingNotFreezing verifica que al enfocar el campo Prompt:
// 1. El textarea recibe teclas y es completamente editable sin quedarse congelado ni bloquear el modal.
// 2. Las flechas arriba/abajo navegan el cursor en el textarea.
// 3. Tab o PgDown permiten salir fluidamente al siguiente campo.
func TestSubAgentPromptEditingNotFreezing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := swarmTestModel(t)

	opened, _ := m.openAgentModal()
	m = opened
	created, _ := m.startNewAgentProfile()
	m = created

	am := &m.modal.agents
	// Navegar hasta el campo Prompt
	for am.field != agentFieldPrompt {
		next, _ := m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyDown})
		m = asModel(next)
		am = &m.modal.agents
	}

	if am.field != agentFieldPrompt {
		t.Fatalf("campo actual debía ser Prompt, pero es %v", am.field)
	}

	// 1. Escribir texto caracter por caracter simulando tipeo de usuario
	for _, ch := range "Eres un experto en Go y TUI." {
		next, _ := m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = asModel(next)
		am = &m.modal.agents
	}

	if am.prompt.Value() != "Eres un experto en Go y TUI." {
		t.Fatalf("el texto del prompt no coincide: %q", am.prompt.Value())
	}

	// Insertar salto de línea con Enter
	next, _ := m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(next)
	am = &m.modal.agents

	for _, ch := range "Segunda linea de instrucciones." {
		next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = asModel(next)
		am = &m.modal.agents
	}

	expectedPrompt := "Eres un experto en Go y TUI.\nSegunda linea de instrucciones."
	if am.prompt.Value() != expectedPrompt {
		t.Fatalf("el texto multilínea del prompt falló: got %q", am.prompt.Value())
	}

	// 2. Probar que flechas arriba y abajo no congelan el modal
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyUp})
	m = asModel(next)
	am = &m.modal.agents
	if am.field != agentFieldPrompt {
		t.Fatalf("up en el textarea no debía salirse inesperadamente del prompt, campo: %v", am.field)
	}

	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyDown})
	m = asModel(next)
	am = &m.modal.agents
	if am.field != agentFieldPrompt {
		t.Fatalf("down en el textarea no debía salirse del prompt, campo: %v", am.field)
	}

	// 3. Salir del prompt con Tab
	next, _ = m.updateAgentEditor(tea.KeyMsg{Type: tea.KeyTab})
	m = asModel(next)
	am = &m.modal.agents

	if am.field == agentFieldPrompt {
		t.Fatal("Tab debía salir del campo Prompt hacia el siguiente campo")
	}

	// 4. Verificar que la vista del editor renderiza el texto sin errores
	view := m.agentEditorView()
	if !strings.Contains(view, "Eres un experto") {
		t.Fatalf("la vista del editor debía contener el texto del prompt, got: %s", view)
	}
}
