package ui

// Modal Gestor de Equipo Swarm (Ctrl+T / /agents).
//
//	╭─ 🤖 Equipo de Sub-Agentes (Swarm Roster) ───────────────────╮
//	│ Agentes Disponibles:      │  Configuración de [coder]:       │
//	│ [x] 󰀐 explorer (Explorer)  │  Nombre: Senior Implementer      │
//	│ [x] 󰙨 coder    (Implementer)│  Modelo: (global)               │
//	│ [x] 󰒋 qa       (QA)       │  Tools:  read, search, patch, run │
//	│ [ ] 󰈙 docs     (Writer)   │  Prompt Específico:              │
//	│                           │  ┌────────────────────────────┐  │
//	│                           │  │ Eres un ingeniero…          │  │
//	│                           │  └────────────────────────────┘  │
//	│ [Space] Activar/Desactivar  [Enter] Editar  [Ctrl+N] Nuevo  │
//	╰──────────────────────────────────────────────────────────────╯
//
//	Editor: campos navegables (Nombre, Icono, Color, Modelo, Tools, Prompt).
//	Ctrl+S guarda en config.json; Esc vuelve al roster.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Yak036/mkd/internal/clipboard"
	"github.com/Yak036/mkd/internal/config"
	"github.com/Yak036/mkd/internal/swarm"
)

// agentModalMode alterna entre el roster y el editor de un perfil.
type agentModalMode int

const (
	agentModeRoster agentModalMode = iota
	agentModeEdit
)

// agentEditorField identifica el campo activo del editor.
type agentEditorField int

const (
	agentFieldName agentEditorField = iota
	agentFieldIcon
	agentFieldColor
	agentFieldProvider
	agentFieldModel
	agentFieldTools
	agentFieldPrompt
	agentFieldCount
)

// availableAgentTools define el catálogo estándar de herramientas seleccionables para sub-agentes.
var availableAgentTools = []string{
	"read_file",
	"list_dir",
	"search_code",
	"patch_file",
	"write_file",
	"run_command",
	"mcp:*",
}

// agentModal es el estado del gestor de equipo.
type agentModal struct {
	mode     agentModalMode
	profiles []config.SubAgentProfile
	selected int
	scroll   int
	status   string
	err      string

	// Editor.
	editing       config.SubAgentProfile
	field         agentEditorField
	name          textinput.Model
	icon          textinput.Model
	color         textinput.Model
	providerIndex int
	providers     []string
	model         textinput.Model
	toolsList     []string
	toolsSelected map[string]bool
	toolCursor    int
	prompt        textarea.Model

	// creating marca que el editor está creando un perfil nuevo.
	creating bool
}

// newAgentModal construye el modal desde la configuración.
func newAgentModal(cfg config.Config) agentModal {
	return agentModal{
		profiles: swarm.SortProfiles(cfg.SubAgents),
	}
}

// openAgentModal abre el gestor de equipo.
func (m Model) openAgentModal() (Model, tea.Cmd) {
	m.hideSlashMenu()
	m.modal.kind = modalAgents
	m.modal.agents = newAgentModal(m.cfg)
	m.textarea.Blur()
	m.resizeModals()
	return m, nil
}

// updateAgentModal procesa el roster o el editor.
func (m Model) updateAgentModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	am := &m.modal.agents
	if am.mode == agentModeEdit {
		return m.updateAgentEditor(msg)
	}

	rows := m.agentRows()
	switch {
	case key.Matches(msg, keyQuit):
		return m.quit()
	case key.Matches(msg, keyEsc):
		return m.closeModal()
	case msg.String() == "up":
		am.moveSelection(-1, rows)
		return m, nil
	case msg.String() == "down":
		am.moveSelection(1, rows)
		return m, nil
	case msg.String() == "pgup":
		am.moveSelection(-rows, rows)
		return m, nil
	case msg.String() == "pgdown":
		am.moveSelection(rows, rows)
		return m, nil
	case msg.String() == " " || msg.String() == "space":
		return m.toggleAgentProfile()
	case msg.String() == "ctrl+n":
		return m.startNewAgentProfile()
	case msg.String() == "ctrl+d":
		return m.deleteAgentProfile()
	case msg.String() == "ctrl+y", msg.String() == "ctrl+c", msg.String() == "c", msg.String() == "y":
		return m.copyAgentPrompt()
	case key.Matches(msg, keyEnter), msg.String() == "tab":
		return m.editAgentProfile()
	}
	return m, nil
}

// copyAgentPrompt copia el prompt específico (descripción) del sub-agente seleccionado al portapapeles.
func (m Model) copyAgentPrompt() (Model, tea.Cmd) {
	am := &m.modal.agents
	profile, ok := am.selectedProfile()
	if !ok {
		return m, nil
	}
	text := strings.TrimSpace(profile.RolePrompt)
	if text == "" {
		am.err = "El sub-agente seleccionado no tiene un prompt específico."
		return m, nil
	}
	if err := clipboard.Copy(text); err != nil {
		am.err = "Error al copiar: " + err.Error()
		return m, nil
	}
	am.err = ""
	am.status = fmt.Sprintf("✓ Prompt de [%s] copiado al portapapeles (%d caracteres)", profile.ID, len(text))
	m.notice = fmt.Sprintf("✓ Prompt de [%s] copiado al portapapeles", profile.ID)
	return m, nil
}

// moveSelection desplaza la selección con scroll visible.
func (am *agentModal) moveSelection(delta, rows int) {
	if len(am.profiles) == 0 {
		return
	}
	am.selected += delta
	if am.selected < 0 {
		am.selected = 0
	}
	if am.selected >= len(am.profiles) {
		am.selected = len(am.profiles) - 1
	}
	if rows > 0 {
		if am.selected < am.scroll {
			am.scroll = am.selected
		}
		if am.selected >= am.scroll+rows {
			am.scroll = am.selected - rows + 1
		}
	}
}

func (am agentModal) selectedProfile() (config.SubAgentProfile, bool) {
	if len(am.profiles) == 0 {
		return config.SubAgentProfile{}, false
	}
	index := min(max(0, am.selected), len(am.profiles)-1)
	return am.profiles[index], true
}

// toggleAgentProfile activa/desactiva el perfil seleccionado y lo persiste.
func (m Model) toggleAgentProfile() (Model, tea.Cmd) {
	am := &m.modal.agents
	profile, ok := am.selectedProfile()
	if !ok {
		return m, nil
	}
	agents, enabled := swarm.ToggleProfile(m.cfg.SubAgents, profile.ID)
	m.cfg.SubAgents = agents
	if err := m.persistConfig(); err != nil {
		am.err = "No se pudo guardar: " + err.Error()
		return m, nil
	}
	am.err = ""
	am.profiles = swarm.SortProfiles(m.cfg.SubAgents)
	state := "desactivado"
	if enabled {
		state = "activado"
	}
	am.status = fmt.Sprintf("✓ %s %s", profile.ID, state)
	return m, nil
}

// deleteAgentProfile elimina el perfil seleccionado.
func (m Model) deleteAgentProfile() (Model, tea.Cmd) {
	am := &m.modal.agents
	profile, ok := am.selectedProfile()
	if !ok {
		return m, nil
	}
	agents, removed := swarm.DeleteProfile(m.cfg.SubAgents, profile.ID)
	if !removed {
		am.err = "No se pudo eliminar " + profile.ID
		return m, nil
	}
	m.cfg.SubAgents = agents
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		am.err = "No se pudo guardar: " + err.Error()
		return m, nil
	}
	am.err = ""
	am.profiles = swarm.SortProfiles(m.cfg.SubAgents)
	am.selected = min(am.selected, max(0, len(am.profiles)-1))
	am.status = "✓ Sub-agente eliminado: " + profile.ID
	return m, nil
}

// editAgentProfile abre el editor del perfil seleccionado.
func (m Model) editAgentProfile() (Model, tea.Cmd) {
	am := &m.modal.agents
	profile, ok := am.selectedProfile()
	if !ok {
		return m, nil
	}
	am.editing = profile
	am.creating = false
	am.mode = agentModeEdit
	am.field = agentFieldName
	am.status = ""
	am.err = ""
	m.buildAgentEditor()
	m.resizeModals()
	return m, m.focusAgentField()
}

// startNewAgentProfile abre el editor en blanco para un perfil nuevo.
func (m Model) startNewAgentProfile() (Model, tea.Cmd) {
	am := &m.modal.agents
	am.editing = config.SubAgentProfile{
		ID:           "",
		Name:         "",
		Icon:         "󰀐",
		Color:        "#06B6D4",
		AllowedTools: []string{"read_file", "list_dir", "search_code"},
		Enabled:      true,
	}
	am.creating = true
	am.mode = agentModeEdit
	am.field = agentFieldName
	am.status = "Nuevo sub-agente: escribe un ID en el nombre (ej. reviewer)."
	am.err = ""
	m.buildAgentEditor()
	m.resizeModals()
	return m, m.focusAgentField()
}

// buildAgentEditor inicializa los inputs con los valores del perfil.
func (m *Model) buildAgentEditor() {
	am := &m.modal.agents
	profile := am.editing

	// Lista de proveedores: "" representa (global) y únicamente proveedores con conexión exitosa
	providers := []string{""}
	seen := map[string]bool{"": true}
	for _, key := range m.cfg.ConnectedProviderKeys() {
		if !seen[key] {
			providers = append(providers, key)
			seen[key] = true
		}
	}
	// Si el perfil editado ya tenía un proveedor específico previo, se conserva
	// para no romper configuraciones existentes
	if profile.Provider != "" && !seen[profile.Provider] {
		providers = append(providers, profile.Provider)
		seen[profile.Provider] = true
	}
	am.providers = providers
	am.providerIndex = 0
	for i, p := range providers {
		if p == profile.Provider {
			am.providerIndex = i
			break
		}
	}

	am.name = newInput("nombre visible (ej. Backend Go Specialist)")
	am.name.SetValue(profile.Name)
	am.icon = newInput("icono Nerd Font (ej. 󰙨)")
	am.icon.SetValue(profile.Icon)
	am.color = newInput("#RRGGBB")
	am.color.SetValue(profile.Color)
	am.model = newInput("modelo (vacío = global · Tab/◀▶ para autocompletar)")
	am.model.SetValue(profile.Model)

	// Selector interactivo de herramientas (Tools).
	am.toolsSelected = make(map[string]bool)
	am.toolsList = make([]string, len(availableAgentTools))
	copy(am.toolsList, availableAgentTools)

	// Conservar cualquier herramienta personalizada o específica del perfil existente.
	for _, t := range profile.AllowedTools {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if t == "*" {
			for _, def := range availableAgentTools {
				am.toolsSelected[def] = true
			}
			continue
		}
		found := false
		for _, existing := range am.toolsList {
			if existing == t {
				found = true
				break
			}
		}
		if !found {
			am.toolsList = append(am.toolsList, t)
		}
		am.toolsSelected[t] = true
	}

	// Si el perfil no tenía herramientas explícitas (nuevo o vacío), marcar lectura por defecto.
	if len(profile.AllowedTools) == 0 {
		am.toolsSelected["read_file"] = true
		am.toolsSelected["list_dir"] = true
		am.toolsSelected["search_code"] = true
	}
	am.toolCursor = 0

	ta := textarea.New()
	ta.Placeholder = "System prompt específico del sub-agente…"
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.SetHeight(6)
	ta.SetWidth(50)
	ta.SetValue(profile.RolePrompt)
	ta.FocusedStyle.Base = lipgloss.NewStyle().Background(surface)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(surface)
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(purple).Background(surface)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(muted).Background(surface)
	ta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(text).Background(surface)
	ta.FocusedStyle.EndOfBuffer = lipgloss.NewStyle().Background(surface)
	am.prompt = ta
}

// subAgentAvailableModels devuelve los modelos conocidos para el proveedor del subagente.
func (m Model) subAgentAvailableModels() []string {
	am := m.modal.agents
	provKey := ""
	if am.providerIndex > 0 && am.providerIndex < len(am.providers) {
		provKey = am.providers[am.providerIndex]
	}
	if provKey == "" {
		provKey = m.cfg.ActiveProvider
	}

	var list []string
	seen := map[string]bool{}

	if p, ok := m.cfg.Providers[provKey]; ok {
		for _, mdl := range p.Models {
			mdl = strings.TrimSpace(mdl)
			if mdl != "" && !seen[mdl] {
				list = append(list, mdl)
				seen[mdl] = true
			}
		}
	}

	for _, mdl := range m.modelsCache.Models(provKey) {
		mdl = strings.TrimSpace(mdl)
		if mdl != "" && !seen[mdl] {
			list = append(list, mdl)
			seen[mdl] = true
		}
	}

	return list
}

// focusAgentField enfoca el input del campo activo. Reajusta el tamaño del
// textarea para que el footer de acciones nunca quede empujado fuera del modal.
func (m *Model) focusAgentField() tea.Cmd {
	m.resizeAgentModal()
	am := &m.modal.agents
	am.name.Blur()
	am.icon.Blur()
	am.color.Blur()
	am.model.Blur()
	am.prompt.Blur()

	switch am.field {
	case agentFieldIcon:
		return am.icon.Focus()
	case agentFieldColor:
		return am.color.Focus()
	case agentFieldProvider, agentFieldTools:
		// Selectores interactivos (flechas/espacio), no requieren focus de textinput
		return nil
	case agentFieldModel:
		return am.model.Focus()
	case agentFieldPrompt:
		return am.prompt.Focus()
	default:
		return am.name.Focus()
	}
}

// updateAgentEditor procesa las teclas del editor.
func (m Model) updateAgentEditor(msg tea.KeyMsg) (Model, tea.Cmd) {
	am := &m.modal.agents

	switch {
	case key.Matches(msg, keyQuit):
		return m.quit()
	case key.Matches(msg, keyEsc):
		am.mode = agentModeRoster
		am.creating = false
		am.err = ""
		return m, nil
	// Tab funciona en terminales nativas, pero VS Code lo intercepta para
	// cambiar de panel. Por eso up/down y pgup/pgdown también navegan.
	case msg.String() == "tab":
		if am.field == agentFieldModel {
			// Autocompletado inteligente de modelos
			models := m.subAgentAvailableModels()
			if len(models) > 0 {
				val := strings.ToLower(strings.TrimSpace(am.model.Value()))
				var matches []string
				for _, mdl := range models {
					if val == "" || strings.Contains(strings.ToLower(mdl), val) {
						matches = append(matches, mdl)
					}
				}
				if len(matches) > 0 {
					current := am.model.Value()
					idx := -1
					for i, match := range matches {
						if match == current {
							idx = i
							break
						}
					}
					nextIdx := (idx + 1) % len(matches)
					am.model.SetValue(matches[nextIdx])
					am.model.CursorEnd()
					return m, nil
				}
			}
		}
		am.field = (am.field + 1) % agentFieldCount
		return m, m.focusAgentField()
	case msg.String() == "ctrl+down":
		am.field = (am.field + 1) % agentFieldCount
		return m, m.focusAgentField()
	case msg.String() == "shift+tab", msg.String() == "ctrl+up":
		am.field = (am.field + agentFieldCount - 1) % agentFieldCount
		return m, m.focusAgentField()
	case msg.String() == "down":
		if am.field == agentFieldPrompt {
			// En el prompt, la flecha hacia abajo mueve el cursor dentro del textarea.
			// Para salir del campo se usa Tab, PgDn o Ctrl+↓.
			var cmd tea.Cmd
			am.prompt, cmd = am.prompt.Update(msg)
			return m, cmd
		}
		am.field = (am.field + 1) % agentFieldCount
		return m, m.focusAgentField()
	case msg.String() == "up":
		if am.field == agentFieldPrompt {
			// En el prompt, la flecha hacia arriba mueve el cursor dentro del textarea.
			// Para salir del campo se usa Shift+Tab, PgUp o Ctrl+↑.
			var cmd tea.Cmd
			am.prompt, cmd = am.prompt.Update(msg)
			return m, cmd
		}
		am.field = (am.field + agentFieldCount - 1) % agentFieldCount
		return m, m.focusAgentField()
	case msg.String() == "pgdown":
		am.field = (am.field + 1) % agentFieldCount
		return m, m.focusAgentField()
	case msg.String() == "pgup":
		am.field = (am.field + agentFieldCount - 1) % agentFieldCount
		return m, m.focusAgentField()
	case msg.String() == "ctrl+s":
		return m.saveAgentProfile()
	case msg.String() == "ctrl+enter":
		return m.saveAgentProfile()
	case msg.String() == "ctrl+y":
		text := strings.TrimSpace(am.prompt.Value())
		if text == "" {
			am.err = "El prompt está vacío."
			return m, nil
		}
		if err := clipboard.Copy(text); err != nil {
			am.err = "Error al copiar: " + err.Error()
			return m, nil
		}
		am.err = ""
		am.status = fmt.Sprintf("✓ Prompt copiado al portapapeles (%d caracteres)", len(text))
		return m, nil
	}

	// Teclas específicas para selector de proveedor
	if am.field == agentFieldProvider {
		switch msg.String() {
		case "left", "h":
			if len(am.providers) > 0 {
				am.providerIndex = (am.providerIndex - 1 + len(am.providers)) % len(am.providers)
			}
			return m, nil
		case "right", "l", "space", "enter":
			if len(am.providers) > 0 {
				am.providerIndex = (am.providerIndex + 1) % len(am.providers)
			}
			return m, nil
		}
	}

	// Teclas específicas para selección rápida de modelo con flechas izquierda/derecha
	if am.field == agentFieldModel {
		models := m.subAgentAvailableModels()
		if len(models) > 0 {
			switch msg.String() {
			case "alt+left", "ctrl+left":
				current := am.model.Value()
				idx := indexOf(models, current)
				if idx < 0 {
					idx = len(models) - 1
				} else {
					idx = (idx - 1 + len(models)) % len(models)
				}
				am.model.SetValue(models[idx])
				am.model.CursorEnd()
				return m, nil
			case "alt+right", "ctrl+right":
				current := am.model.Value()
				idx := indexOf(models, current)
				if idx < 0 {
					idx = 0
				} else {
					idx = (idx + 1) % len(models)
				}
				am.model.SetValue(models[idx])
				am.model.CursorEnd()
				return m, nil
			case "enter":
				// Enter en modelo avanza a tools
				am.field = agentFieldTools
				return m, m.focusAgentField()
			}
		}
	}

	// Teclas específicas para selector interactivo de tools
	if am.field == agentFieldTools {
		switch msg.String() {
		case "left", "h":
			if len(am.toolsList) > 0 {
				am.toolCursor = (am.toolCursor - 1 + len(am.toolsList)) % len(am.toolsList)
			}
			return m, nil
		case "right", "l":
			if len(am.toolsList) > 0 {
				am.toolCursor = (am.toolCursor + 1) % len(am.toolsList)
			}
			return m, nil
		case " ", "space", "enter", "x":
			if len(am.toolsList) > 0 && am.toolCursor < len(am.toolsList) {
				t := am.toolsList[am.toolCursor]
				am.toolsSelected[t] = !am.toolsSelected[t]
			}
			return m, nil
		case "a":
			allSelected := true
			for _, t := range am.toolsList {
				if !am.toolsSelected[t] {
					allSelected = false
					break
				}
			}
			for _, t := range am.toolsList {
				am.toolsSelected[t] = !allSelected
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	switch am.field {
	case agentFieldName:
		am.name, cmd = am.name.Update(msg)
	case agentFieldIcon:
		am.icon, cmd = am.icon.Update(msg)
	case agentFieldColor:
		am.color, cmd = am.color.Update(msg)
	case agentFieldModel:
		am.model, cmd = am.model.Update(msg)
	case agentFieldPrompt:
		am.prompt, cmd = am.prompt.Update(msg)
	}
	return m, cmd
}

// saveAgentProfile valida y persiste el perfil en edición.
func (m Model) saveAgentProfile() (Model, tea.Cmd) {
	am := &m.modal.agents
	profile := am.editing
	profile.Name = strings.TrimSpace(am.name.Value())
	profile.Icon = strings.TrimSpace(am.icon.Value())
	profile.Color = strings.TrimSpace(am.color.Value())
	profile.Provider = ""
	if am.providerIndex > 0 && am.providerIndex < len(am.providers) {
		profile.Provider = am.providers[am.providerIndex]
	}
	profile.Model = strings.TrimSpace(am.model.Value())
	profile.RolePrompt = strings.TrimSpace(am.prompt.Value())

	var allowed []string
	for _, t := range am.toolsList {
		if am.toolsSelected[t] {
			allowed = append(allowed, t)
		}
	}
	if len(allowed) == 0 {
		allowed = []string{"read_file", "list_dir", "search_code"}
	}
	profile.AllowedTools = allowed

	// El ID se deriva del nombre al crear (o se reutiliza al editar).
	if profile.ID == "" {
		profile.ID = slugify(profile.Name)
	}
	if profile.ID == "" {
		am.err = "El sub-agente necesita un nombre/ID."
		return m, nil
	}
	if profile.Name == "" {
		profile.Name = profile.ID
	}
	if profile.Color == "" {
		profile.Color = "#06B6D4"
	}
	if len(profile.AllowedTools) == 0 {
		profile.AllowedTools = []string{"read_file", "list_dir", "search_code"}
	}

	agents, err := swarm.UpsertProfile(m.cfg.SubAgents, profile)
	if err != nil {
		am.err = err.Error()
		return m, nil
	}
	m.cfg.SubAgents = agents
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		am.err = "No se pudo guardar: " + err.Error()
		return m, nil
	}

	am.mode = agentModeRoster
	am.creating = false
	am.err = ""
	am.profiles = swarm.SortProfiles(m.cfg.SubAgents)
	am.status = "✓ Sub-agente guardado: " + profile.ID
	// Selecciona el perfil recién guardado.
	for i, candidate := range am.profiles {
		if candidate.ID == profile.ID {
			am.selected = i
			break
		}
	}
	return m, nil
}

// agentRows calcula las filas visibles del roster.
func (m Model) agentRows() int {
	return min(8, max(3, m.modalContentHeight()-8))
}

// agentModalView pinta el roster o el editor.
func (m Model) agentModalView() string {
	am := m.modal.agents
	if am.mode == agentModeEdit {
		return m.agentEditorView()
	}
	width := m.modalContentWidth()

	var b strings.Builder
	b.WriteString(titleStyle.Render("🤖 Equipo de Sub-Agentes (Swarm Roster)"))
	b.WriteString(mutedStyle.Render(fmt.Sprintf("  %d/%d activos", len(swarm.EnabledProfiles(m.cfg.SubAgents)), len(am.profiles))))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", width)))
	b.WriteString("\n")

	// Columna izquierda: lista de agentes.
	leftWidth := max(24, width/2-2)
	rows := m.agentRows()
	start := am.scroll
	if start > len(am.profiles)-1 {
		start = max(0, len(am.profiles)-rows)
	}
	end := min(len(am.profiles), start+rows)

	leftLines := make([]string, 0, rows)
	if len(am.profiles) == 0 {
		leftLines = append(leftLines, warnStyle.Render("Sin sub-agentes"))
	}
	for i := start; i < end; i++ {
		leftLines = append(leftLines, am.rosterRow(am.profiles[i], i == am.selected, leftWidth))
	}

	// Columna derecha: resumen del seleccionado.
	rightWidth := max(20, width-leftWidth-3)
	rightLines := am.detailLines(rightWidth)

	height := max(len(leftLines), len(rightLines))
	for len(leftLines) < height {
		leftLines = append(leftLines, "")
	}
	for len(rightLines) < height {
		rightLines = append(rightLines, "")
	}

	for i := 0; i < height; i++ {
		left := fitWidth(oneLineKeep(leftLines[i], leftWidth), leftWidth)
		right := oneLineKeep(rightLines[i], rightWidth)
		b.WriteString(left + mutedStyle.Render(" │ ") + right)
		b.WriteString("\n")
	}

	if am.err != "" {
		b.WriteString(errorStyle.Render(oneLine(am.err, width)))
		b.WriteString("\n")
	}
	if am.status != "" {
		b.WriteString(successStyle.Render(oneLine(am.status, width)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(diffHintStyle.Render("[Space]"))
	b.WriteString(textStyle.Render(" Activar/Desactivar  "))
	b.WriteString(diffHintStyle.Render("[Enter]"))
	b.WriteString(textStyle.Render(" Editar  "))
	b.WriteString(diffHintStyle.Render("[Ctrl+Y / c]"))
	b.WriteString(textStyle.Render(" Copiar Prompt"))
	b.WriteString("\n")
	b.WriteString(diffHintStyle.Render("[Ctrl+N]"))
	b.WriteString(textStyle.Render(" Nuevo  "))
	b.WriteString(diffHintStyle.Render("[Ctrl+D]"))
	b.WriteString(textStyle.Render(" Eliminar  "))
	b.WriteString(diffHintStyle.Render("[Esc]"))
	b.WriteString(textStyle.Render(" Cerrar"))
	return m.modalBox(b.String())
}

// rosterRow pinta una fila del roster: checkbox, icono, ID y nombre.
func (am agentModal) rosterRow(profile config.SubAgentProfile, selected bool, width int) string {
	check := mutedStyle.Render("[ ]")
	if profile.Enabled {
		check = successStyle.Render("[x]")
	}
	icon := ""
	if strings.TrimSpace(profile.Icon) != "" {
		icon = lipglossStyleForeground(profile.Color).Render(profile.Icon) + " "
	}
	name := textStyle.Render(profile.ID)
	detail := mutedStyle.Render("(" + oneLine(profile.Name, max(6, width-16)) + ")")
	if selected {
		box := "[ ]"
		if profile.Enabled {
			box = "[x]"
		}
		raw := "❯ " + box + " " + profile.Icon + " " + profile.ID + " (" + oneLine(profile.Name, max(6, width-18)) + ")"
		return selectedRowStyle.Render(ansi.Truncate(raw, width, "…"))
	}
	return "  " + check + " " + icon + name + " " + detail
}

// detailLines devuelve las líneas del panel de detalle.
func (am agentModal) detailLines(width int) []string {
	profile, ok := am.selectedProfile()
	if !ok {
		return []string{mutedStyle.Render("Selecciona un agente")}
	}
	lines := []string{
		titleCyanStyle.Render("Configuración de [" + profile.ID + "]"),
		"",
		mutedStyle.Render("Nombre: ") + textStyle.Render(oneLine(profile.Name, max(6, width-10))),
		mutedStyle.Render("Icono:  ") + textStyle.Render(profile.Icon) + "  " +
			mutedStyle.Render("Color: ") + lipglossStyleForeground(profile.Color).Render(profile.Color),
		mutedStyle.Render("Modelo: ") + textStyle.Render(modelLabel(profile)),
		mutedStyle.Render("Tools:  ") + textStyle.Render(oneLine(swarm.ToolsForPermissions(profile), max(6, width-10))),
		mutedStyle.Render("Estado: ") + enabledLabel(profile.Enabled),
		"",
		mutedStyle.Render("Prompt específico:"),
	}
	prompt := strings.TrimSpace(profile.RolePrompt)
	if prompt == "" {
		lines = append(lines, mutedStyle.Render("  (vacío · usa el prompt por defecto)"))
	} else {
		for _, line := range wrapText(prompt, max(12, width-2), 6) {
			lines = append(lines, textStyle.Render(line))
		}
	}
	return lines
}

// agentEditorView pinta el editor de un perfil.
func (m Model) agentEditorView() string {
	am := m.modal.agents
	width := m.modalContentWidth()

	var b strings.Builder
	title := "✎ Editar Sub-Agente"
	if am.creating {
		title = "✚ Nuevo Sub-Agente"
	}
	b.WriteString(titleStyle.Render(title))
	if !am.creating {
		b.WriteString(mutedStyle.Render("  [" + am.editing.ID + "]"))
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", width)))
	b.WriteString("\n")

	b.WriteString(agentFieldLabel("Nombre", am.field == agentFieldName))
	b.WriteString(am.name.View())
	b.WriteString("\n")
	b.WriteString(agentFieldLabel("Icono", am.field == agentFieldIcon))
	b.WriteString(am.icon.View())
	b.WriteString("\n")
	b.WriteString(agentFieldLabel("Color", am.field == agentFieldColor))
	b.WriteString(am.color.View())
	b.WriteString("\n")

	// Proveedor con selector interactivo
	provLabel := "(global)"
	if am.providerIndex > 0 && am.providerIndex < len(am.providers) {
		provKey := am.providers[am.providerIndex]
		provLabel = config.DisplayName(provKey)
	}
	provSelector := fmt.Sprintf("◀ [ %s ] ▶", provLabel)
	if am.field == agentFieldProvider {
		provSelector = titleCyanStyle.Render(provSelector) + " " + mutedStyle.Render("◀/▶ cambiar")
	}
	b.WriteString(agentFieldLabel("Proveedor", am.field == agentFieldProvider))
	b.WriteString(provSelector)
	b.WriteString("\n")

	// Modelo con autocompletado y catálogo dinámico
	b.WriteString(agentFieldLabel("Modelo", am.field == agentFieldModel))
	b.WriteString(am.model.View())
	b.WriteString("\n")

	// Modelos sugeridos o disponibles del proveedor
	models := m.subAgentAvailableModels()
	if len(models) > 0 {
		filter := strings.ToLower(strings.TrimSpace(am.model.Value()))
		var hints []string
		count := 0
		for _, mdl := range models {
			if filter == "" || strings.Contains(strings.ToLower(mdl), filter) {
				hints = append(hints, "["+mdl+"]")
				count++
				if count >= 3 {
					break
				}
			}
		}
		if len(hints) > 0 {
			hintLine := "  " + mutedStyle.Render("Disponibles: ") + titleCyanStyle.Render(strings.Join(hints, " "))
			if am.field == agentFieldModel {
				hintLine += " " + mutedStyle.Render("· Tab autocompleta · Alt+◀/▶ elige")
			}
			b.WriteString(hintLine + "\n")
		}
	} else {
		b.WriteString("  " + mutedStyle.Render("(vacío = modelo global)") + "\n")
	}

	b.WriteString(agentFieldLabel("Tools", am.field == agentFieldTools))
	b.WriteString(am.toolsSelectorView(am.field == agentFieldTools))
	b.WriteString("\n")
	b.WriteString(agentFieldLabel("Prompt", am.field == agentFieldPrompt))
	b.WriteString("\n")
	b.WriteString(m.promptViewForEditor())

	if am.err != "" {
		b.WriteString(errorStyle.Render(oneLine(am.err, width)))
		b.WriteString("\n")
	}
	if am.status != "" {
		b.WriteString(mutedStyle.Render(oneLine(am.status, width)))
		b.WriteString("\n")
	}

	// El footer de acciones debe verse siempre: se reserva su altura y, si el
	// contenido no cabe, se recorta el textarea del prompt (que es lo elástico)
	// en vez de cortar las teclas de abajo.
	footer := make([]string, 0, 2)
	footer = append(footer, "")
	footer = append(footer, diffHintStyle.Render("[↑/↓ campo]")+" "+
		diffHintStyle.Render("[Ctrl+Y]")+textStyle.Render(" Copiar prompt  ")+
		diffHintStyle.Render("[Ctrl+S]")+textStyle.Render(" Guardar  ")+
		diffHintStyle.Render("[Esc]")+textStyle.Render(" Volver"))
	return m.modalBoxWithFooter(b.String(), strings.Join(footer, "\n"))
}

// toolsSelectorView renderiza el selector interactivo de herramientas con badges [x]/[ ].
func (am agentModal) toolsSelectorView(focused bool) string {
	var row1, row2 []string
	for i, t := range am.toolsList {
		sel := am.toolsSelected[t]
		box := "[ ]"
		if sel {
			box = "[x]"
		}
		item := box + " " + t
		if focused && i == am.toolCursor {
			item = titleCyanStyle.Render("❯" + item)
		} else if sel {
			item = successStyle.Render(" " + item)
		} else {
			item = mutedStyle.Render(" " + item)
		}

		if i < 4 {
			row1 = append(row1, item)
		} else {
			row2 = append(row2, item)
		}
	}

	res := strings.Join(row1, "  ")
	if len(row2) > 0 {
		res += "\n" + strings.Repeat(" ", 11) + strings.Join(row2, "  ")
	}
	if focused {
		res += "\n" + strings.Repeat(" ", 11) + mutedStyle.Render("◀/▶ elegir · [Espacio/Enter] alternar · [a] todas")
	}
	return res
}

// promptViewForEditor devuelve el textarea del prompt con la altura que cabe
// realmente en el modal, de modo que el footer de acciones quede visible.
func (m Model) promptViewForEditor() string {
	chrome := m.editorChromeHeight()
	available := m.modalContentHeight() - chrome - 1
	if available < 3 {
		available = 3
	}
	m.modal.agents.prompt.SetHeight(available)
	return m.modal.agents.prompt.View()
}

// editorChromeHeight calcula las líneas que ocupa el editor sin el cuerpo del
// prompt: título, separador, los campos (etiqueta + input o selector), nota de tools,
// etiqueta Prompt y el footer con su línea en blanco.
//
// Se mide sobre el render real de los inputs para que funcione igual cuando un
// campo está enfocado (que añade el marcador ❯) o no.
func (m Model) editorChromeHeight() int {
	am := m.modal.agents
	height := 2 // título + separador
	height += lipgloss.Height(agentFieldLabel("Nombre", false) + am.name.View())
	height += lipgloss.Height(agentFieldLabel("Icono", false) + am.icon.View())
	height += lipgloss.Height(agentFieldLabel("Color", false) + am.color.View())
	height += lipgloss.Height(agentFieldLabel("Proveedor", false) + "◀ [ global ] ▶")
	height += lipgloss.Height(agentFieldLabel("Modelo", false) + am.model.View())
	height++ // hint de modelos disponibles
	height += lipgloss.Height(agentFieldLabel("Tools", am.field == agentFieldTools) + am.toolsSelectorView(am.field == agentFieldTools))
	height += lipgloss.Height(agentFieldLabel("Prompt", false))
	height += 2 // footer: línea en blanco + teclas
	if am.err != "" {
		height++
	}
	if am.status != "" {
		height++
	}
	return height
}

// resizeAgentModal ajusta inputs y textarea al ancho del modal.
func (m *Model) resizeAgentModal() {
	contentWidth := m.modalContentWidth()
	am := &m.modal.agents
	if am.mode != agentModeEdit {
		return
	}
	width := max(18, contentWidth-4)
	am.name.Width = width
	am.icon.Width = width
	am.color.Width = width
	am.model.Width = width
	am.prompt.SetWidth(width)

	chrome := m.editorChromeHeight()
	available := m.modalContentHeight() - chrome - 1
	if available < 3 {
		available = 3
	}
	am.prompt.SetHeight(available)
}

// updateAgentModalMsg reenvía mensajes no-tecla a los campos del editor.
func (m Model) updateAgentModalMsg(msg tea.Msg) (Model, tea.Cmd) {
	am := &m.modal.agents
	if am.mode != agentModeEdit {
		return m, nil
	}
	var cmd tea.Cmd
	switch am.field {
	case agentFieldName:
		am.name, cmd = am.name.Update(msg)
	case agentFieldIcon:
		am.icon, cmd = am.icon.Update(msg)
	case agentFieldColor:
		am.color, cmd = am.color.Update(msg)
	case agentFieldModel:
		am.model, cmd = am.model.Update(msg)
	case agentFieldPrompt:
		am.prompt, cmd = am.prompt.Update(msg)
	}
	return m, cmd
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// splitTools separa una lista de tools por comas o espacios.
func splitTools(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

// slugify deriva un ID a partir del nombre.
func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
			lastDash = r == '-'
		case r == ' ' || r == '.' || r == '/':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// agentFieldLabel pinta la etiqueta de un campo del editor.
func agentFieldLabel(label string, focused bool) string {
	marker := "  "
	if focused {
		return titleCyanStyle.Render("❯ "+label+":") + " "
	}
	return marker + labelStyle.Render(fmt.Sprintf("%-8s", label+":")) + " "
}

// modelLabel describe el modelo efectivo de un perfil.
func modelLabel(profile config.SubAgentProfile) string {
	if strings.TrimSpace(profile.Model) == "" {
		return "(global)"
	}
	if strings.TrimSpace(profile.Provider) != "" {
		return profile.Model + " (" + profile.Provider + ")"
	}
	return profile.Model
}

// enabledLabel pinta el estado habilitado/deshabilitado.
func enabledLabel(enabled bool) string {
	if enabled {
		return successStyle.Render("● activo")
	}
	return mutedStyle.Render("○ inactivo")
}

// wrapText divide un texto en líneas de ancho width (máximo maxLines).
func wrapText(value string, width, maxLines int) []string {
	if width < 4 {
		width = 4
	}
	words := strings.Fields(value)
	lines := make([]string, 0, maxLines)
	current := ""
	for _, word := range words {
		if current == "" {
			current = word
			continue
		}
		if len([]rune(current))+1+len([]rune(word)) <= width {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		current = word
		if len(lines) >= maxLines {
			break
		}
	}
	if current != "" && len(lines) < maxLines {
		lines = append(lines, current)
	}
	if len(lines) == maxLines && len(words) > 0 {
		lines[maxLines-1] = ansi.Truncate(lines[maxLines-1], max(4, width-1), "…")
	}
	return lines
}

// stripAnsi quita secuencias ANSI para poder aplicar un estilo de fila.
func stripAnsi(value string) string {
	return ansi.Strip(value)
}

// oneLineKeep es como oneLine pero no rompe estilos ya aplicados.
func oneLineKeep(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}
