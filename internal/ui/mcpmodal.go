package ui

// Modal gestor de servidores MCP (/mcp o Ctrl+O).
//
//	┌─ 🔌 Servidores MCP (Model Context Protocol) ──────────────┐
//	│  ● filesystem  │ npx -y @modelcontextprotocol/... (5 tools)│
//	│  ○ postgres    │ npx -y @modelcontextprotocol/... (Off)    │
//	│  ──────────────────────────────────────────────────────   │
//	│  Herramientas expuestas por [filesystem]:                 │
//	│  • read_file, write_file, list_directory, search_files    │
//	│  [Space/Enter] Activar/Desactivar  [Ctrl+N] Nuevo Server  │
//	│  [Ctrl+R] Reconectar Todos         [Esc] Cerrar           │
//	└───────────────────────────────────────────────────────────┘
//
//	Ctrl+N abre un sub-formulario Nombre / Comando / Argumentos que se guarda
//	en ~/.config/mkd/config.json (con Enabled=true) y se conecta en vivo.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Yak036/mkd/internal/config"
	"github.com/Yak036/mkd/internal/mcp"
)

// mcpConnectTimeout acota el arranque bajo demanda de un servidor MCP.
const mcpConnectTimeout = 25 * time.Second

// mcpFormField identifica el campo activo del sub-formulario de alta.
type mcpFormField int

const (
	mcpFieldName mcpFormField = iota
	mcpFieldCommand
	mcpFieldArgs
)

// mcpModal es el estado del gestor de servidores MCP.
type mcpModal struct {
	servers  []mcp.Status
	selected int
	scroll   int
	status   string
	err      string

	// Modo alta de servidor.
	creating         bool
	overwritePending bool
	field            mcpFormField
	name             textinput.Model
	command          textinput.Model
	args             textinput.Model

	loading bool
}

// mcpStatusMsg entrega el estado de los servidores tras una reconexión.
type mcpStatusMsg struct {
	servers []mcp.Status
	note    string
	err     error
}

// mcpServersLoadedMsg llega tras activar/desactivar o reconectar.
type mcpServersLoadedMsg struct {
	note string
}

// newMCPModal construye el modal desde el manager.
func newMCPModal(mgr *mcp.Manager) mcpModal {
	modal := mcpModal{}
	if mgr != nil {
		modal.servers = mgr.Statuses()
	}
	return modal
}

// openMCPModal abre el gestor.
func (m Model) openMCPModal() (Model, tea.Cmd) {
	m.hideSlashMenu()
	m.modal.kind = modalMCP
	m.modal.mcp = newMCPModal(m.mcp)
	m.textarea.Blur()
	m.resizeModals()
	if len(m.modal.mcp.servers) == 0 {
		m.modal.mcp.status = "No hay servidores MCP configurados. Ctrl+N para agregar uno."
	}
	return m, nil
}

// updateMCPModal procesa las teclas del gestor.
func (m Model) updateMCPModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	mm := &m.modal.mcp

	if mm.creating {
		return m.updateMCPForm(msg)
	}

	switch {
	case key.Matches(msg, keyQuit):
		return m.openQuitConfirmModal()
	case key.Matches(msg, keyEsc):
		return m.closeModal()
	case msg.String() == "ctrl+n":
		mm.creating = true
		mm.field = mcpFieldName
		mm.name = newInput("nombre (ej. filesystem)")
		mm.command = newInput("comando (ej. npx)")
		mm.args = newInput("argumentos separados por espacio")
		mm.err = ""
		mm.status = ""
		m.resizeModals()
		return m, mm.name.Focus()
	case msg.String() == "ctrl+r":
		return m.reconnectMCP()
	case msg.String() == "up":
		mm.moveSelection(-1, m.mcpRows())
		return m, nil
	case msg.String() == "down":
		mm.moveSelection(1, m.mcpRows())
		return m, nil
	case msg.String() == "pgup":
		mm.moveSelection(-m.mcpRows(), m.mcpRows())
		return m, nil
	case msg.String() == "pgdown":
		mm.moveSelection(m.mcpRows(), m.mcpRows())
		return m, nil
	case msg.String() == " " || key.Matches(msg, keyEnter):
		return m.toggleSelectedMCPServer()
	}
	return m, nil
}

// updateMCPForm gestiona el alta de un servidor nuevo.
func (m Model) updateMCPForm(msg tea.KeyMsg) (Model, tea.Cmd) {
	mm := &m.modal.mcp

	// Si hay una sobrescritura pendiente, esperamos confirmación explícita con 'y'.
	// Por defecto seguro, Enter o Esc cancelan la sobrescritura.
	if mm.overwritePending {
		switch {
		case msg.String() == "y" || msg.String() == "Y":
			return m.commitSaveMCPServer()
		case key.Matches(msg, keyEsc) || key.Matches(msg, keyEnter) || msg.String() == "n" || msg.String() == "N":
			mm.overwritePending = false
			mm.err = "Sobrescritura cancelada."
			return m, nil
		default:
			mm.overwritePending = false
			return m, nil
		}
	}

	switch {
	case key.Matches(msg, keyQuit):
		return m.openQuitConfirmModal()
	case key.Matches(msg, keyEsc):
		mm.creating = false
		mm.overwritePending = false
		mm.err = ""
		return m, nil
	case msg.String() == "tab" || msg.String() == "down":
		return m, m.nextFormField(1)
	case msg.String() == "shift+tab" || msg.String() == "up":
		return m, m.nextFormField(-1)
	case msg.String() == "ctrl+s", msg.String() == "ctrl+enter":
		return m.saveNewMCPServer()
	}

	var cmd tea.Cmd
	switch mm.field {
	case mcpFieldName:
		mm.name, cmd = mm.name.Update(msg)
	case mcpFieldCommand:
		mm.command, cmd = mm.command.Update(msg)
	case mcpFieldArgs:
		mm.args, cmd = mm.args.Update(msg)
	}
	return m, cmd
}

// nextFormField rota el foco del sub-formulario.
func (m *Model) nextFormField(delta int) tea.Cmd {
	mm := &m.modal.mcp
	mm.name.Blur()
	mm.command.Blur()
	mm.args.Blur()

	mm.field = mcpFormField((int(mm.field) + delta + 3) % 3)
	switch mm.field {
	case mcpFieldCommand:
		return mm.command.Focus()
	case mcpFieldArgs:
		return mm.args.Focus()
	default:
		return mm.name.Focus()
	}
}

// saveNewMCPServer valida, comprueba colisiones de nombres y solicita confirmación si ya existe.
func (m Model) saveNewMCPServer() (Model, tea.Cmd) {
	mm := &m.modal.mcp
	name := strings.TrimSpace(mm.name.Value())
	command := strings.TrimSpace(mm.command.Value())

	if name == "" || command == "" {
		mm.err = "Nombre y comando son obligatorios."
		return m, nil
	}
	if m.mcp == nil {
		mm.err = "Gestor MCP no disponible."
		return m, nil
	}

	// Comprobar si ya existe un servidor con ese nombre antes de sobrescribir
	if _, exists := m.cfg.MCPServers[name]; exists && !mm.overwritePending {
		mm.overwritePending = true
		mm.err = fmt.Sprintf("El servidor MCP '%s' ya existe.", name)
		return m, nil
	}

	return m.commitSaveMCPServer()
}

// commitSaveMCPServer persiste y conecta definitivamente el servidor nuevo o sobrescrito.
func (m Model) commitSaveMCPServer() (Model, tea.Cmd) {
	mm := &m.modal.mcp
	name := strings.TrimSpace(mm.name.Value())
	command := strings.TrimSpace(mm.command.Value())
	args := strings.Fields(mm.args.Value())

	server := config.MCPServerConfig{Command: command, Args: args, Enabled: true}
	if m.cfg.MCPServers == nil {
		m.cfg.MCPServers = map[string]config.MCPServerConfig{}
	}
	m.cfg.MCPServers[name] = server
	if err := m.persistConfig(); err != nil {
		mm.err = "No se pudo guardar la config: " + err.Error()
		return m, nil
	}
	_ = m.mcp.AddServer(context.Background(), name, server)
	m.mcp.UpdateSpecs(m.cfg.MCPServers)

	mm.creating = false
	mm.overwritePending = false
	mm.err = ""
	mm.status = "✓ Servidor agregado: " + name
	mm.servers = m.mcp.Statuses()
	return m, tea.Batch(mcpReloadCmd(m.mcp), m.spinner.Tick)
}

// toggleSelectedMCPServer habilita/deshabilita el servidor seleccionado.
func (m Model) toggleSelectedMCPServer() (Model, tea.Cmd) {
	mm := &m.modal.mcp
	server, ok := mm.selectedServer()
	if !ok || m.mcp == nil {
		return m, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), mcpConnectTimeout)
	defer cancel()

	cfg := m.cfg.MCPServers[server.Name]
	if server.Online || server.Enabled {
		m.mcp.Stop(server.Name)
		cfg.Enabled = false
		mm.status = "○ Servidor desactivado: " + server.Name
	} else {
		cfg.Enabled = true
		if err := m.mcp.Start(ctx, server.Name); err != nil {
			mm.err = err.Error()
			mm.status = ""
			cfg.Enabled = false
		} else {
			mm.err = ""
			mm.status = "● Servidor activado: " + server.Name
		}
	}
	m.cfg.MCPServers[server.Name] = cfg
	if err := m.persistConfig(); err != nil {
		mm.err = "No se pudo guardar la config: " + err.Error()
	}
	mm.servers = m.mcp.Statuses()
	m.refresh()
	return m, m.spinner.Tick
}

// reconnectMCP reinicia todos los servidores habilitados.
func (m Model) reconnectMCP() (Model, tea.Cmd) {
	mm := &m.modal.mcp
	if m.mcp == nil {
		return m, nil
	}
	mm.loading = true
	mm.status = "Reconectando servidores MCP…"
	mm.err = ""
	return m, tea.Batch(mcpReconnectCmd(m.mcp), m.spinner.Tick)
}

// mcpReconnectCmd reconecta en background para no bloquear el event loop.
func mcpReconnectCmd(mgr *mcp.Manager) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), mcpConnectTimeout)
		defer cancel()
		mgr.Reconnect(ctx)
		return mcpStatusMsg{servers: mgr.Statuses(), note: "✓ Servidores MCP reconectados"}
	}
}

// mcpReloadCmd relee el estado tras un alta.
func mcpReloadCmd(mgr *mcp.Manager) tea.Cmd {
	return func() tea.Msg {
		return mcpStatusMsg{servers: mgr.Statuses()}
	}
}

// handleMCPStatus refresca la lista del modal.
func (m Model) handleMCPStatus(msg mcpStatusMsg) (Model, tea.Cmd) {
	if m.modal.kind != modalMCP {
		return m, nil
	}
	mm := &m.modal.mcp
	mm.loading = false
	mm.servers = msg.servers
	if msg.err != nil {
		mm.err = msg.err.Error()
		return m, nil
	}
	if msg.note != "" {
		mm.status = msg.note
	}
	return m, nil
}

// moveSelection desplaza la selección con scroll visible.
func (mm *mcpModal) moveSelection(delta, rows int) {
	if len(mm.servers) == 0 {
		return
	}
	mm.selected += delta
	if mm.selected < 0 {
		mm.selected = 0
	}
	if mm.selected >= len(mm.servers) {
		mm.selected = len(mm.servers) - 1
	}
	if rows > 0 {
		if mm.selected < mm.scroll {
			mm.scroll = mm.selected
		}
		if mm.selected >= mm.scroll+rows {
			mm.scroll = mm.selected - rows + 1
		}
	}
}

func (mm mcpModal) selectedServer() (mcp.Status, bool) {
	if len(mm.servers) == 0 {
		return mcp.Status{}, false
	}
	index := min(max(0, mm.selected), len(mm.servers)-1)
	return mm.servers[index], true
}

// updateInputs reenvía mensajes no-tecla a los inputs del sub-formulario.
func (mm mcpModal) updateInputs(msg tea.Msg) (mcpModal, tea.Cmd) {
	if !mm.creating {
		return mm, nil
	}
	var cmd tea.Cmd
	switch mm.field {
	case mcpFieldName:
		mm.name, cmd = mm.name.Update(msg)
	case mcpFieldCommand:
		mm.command, cmd = mm.command.Update(msg)
	case mcpFieldArgs:
		mm.args, cmd = mm.args.Update(msg)
	}
	return mm, cmd
}

// resize ajusta el ancho de los inputs del formulario.
func (mm *mcpModal) resize(contentWidth int) {
	width := max(18, contentWidth-14)
	if mm.creating {
		mm.name.Width = width
		mm.command.Width = width
		mm.args.Width = width
	}
}

// mcpRows calcula las filas visibles de la lista de servidores.
func (m Model) mcpRows() int {
	return min(6, max(2, m.modalContentHeight()-13))
}

// mcpModalView pinta el gestor.
func (m Model) mcpModalView() string {
	mm := m.modal.mcp
	width := m.modalContentWidth()

	var b strings.Builder
	b.WriteString(titleStyle.Render("🔌 Servidores MCP (Model Context Protocol)"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", width)))
	b.WriteString("\n")

	if mm.creating {
		b.WriteString(mutedStyle.Render("Nuevo servidor MCP:"))
		b.WriteString("\n")
		b.WriteString(fieldLabel("Nombre", mm.field == mcpFieldName))
		b.WriteString(mm.name.View())
		b.WriteString("\n")
		b.WriteString(fieldLabel("Comando", mm.field == mcpFieldCommand))
		b.WriteString(mm.command.View())
		b.WriteString("\n")
		b.WriteString(fieldLabel("Args", mm.field == mcpFieldArgs))
		b.WriteString(mm.args.View())
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("Los argumentos se separan por espacios."))
		b.WriteString("\n")
	} else {
		switch {
		case mm.loading:
			b.WriteString(m.spinner.View())
			b.WriteString(" ")
			b.WriteString(mutedStyle.Render("Actualizando servidores…"))
			b.WriteString("\n")
		case len(mm.servers) == 0:
			b.WriteString(warnStyle.Render("No hay servidores MCP configurados."))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render("Ctrl+N agrega uno (ej. npx @modelcontextprotocol/server-filesystem)."))
			b.WriteString("\n")
		default:
			rows := m.mcpRows()
			start := mm.scroll
			if start > len(mm.servers)-1 {
				start = max(0, len(mm.servers)-rows)
			}
			end := min(len(mm.servers), start+rows)
			for i := start; i < end; i++ {
				b.WriteString(mm.serverRow(mm.servers[i], i == mm.selected, width))
				b.WriteString("\n")
			}
			b.WriteString("\n")
			if server, ok := mm.selectedServer(); ok {
				b.WriteString(mutedStyle.Render("Herramientas expuestas por [" + server.Name + "]:"))
				b.WriteString("\n")
				if len(server.Tools) == 0 {
					b.WriteString(mutedStyle.Render("  (sin herramientas o servidor desconectado)"))
				} else {
					b.WriteString("  " + textStyle.Render("• "+strings.Join(server.Tools, ", ")))
				}
				b.WriteString("\n")
			}
		}
	}

	if mm.err != "" {
		b.WriteString(errorStyle.Render(oneLine(mm.err, width)))
		b.WriteString("\n")
	}
	if mm.status != "" {
		b.WriteString(successStyle.Render(oneLine(mm.status, width)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if mm.creating {
		if mm.overwritePending {
			b.WriteString(warnStyle.Render(fmt.Sprintf("⚠ El servidor [%s] ya existe. ¿Sobrescribir? ", strings.TrimSpace(mm.name.Value()))))
			b.WriteString(diffHintStyle.Render("[y]"))
			b.WriteString(textStyle.Render(" Confirmar   "))
			b.WriteString(diffHintStyle.Render("[Esc / Enter]"))
			b.WriteString(textStyle.Render(" Cancelar"))
		} else {
			b.WriteString(diffHintStyle.Render("[Tab]"))
			b.WriteString(textStyle.Render(" siguiente campo   "))
			b.WriteString(diffHintStyle.Render("[Ctrl+S]"))
			b.WriteString(textStyle.Render(" Guardar y conectar   "))
			b.WriteString(diffHintStyle.Render("[Esc]"))
			b.WriteString(textStyle.Render(" Cancelar"))
		}
	} else {
		b.WriteString(diffHintStyle.Render("[Space/Enter]"))
		b.WriteString(textStyle.Render(" Activar/Desactivar   "))
		b.WriteString(diffHintStyle.Render("[Ctrl+N]"))
		b.WriteString(textStyle.Render(" Nuevo Server"))
		b.WriteString("\n")
		b.WriteString(diffHintStyle.Render("[Ctrl+R]"))
		b.WriteString(textStyle.Render(" Reconectar Todos   "))
		b.WriteString(diffHintStyle.Render("[Esc]"))
		b.WriteString(textStyle.Render(" Cerrar"))
	}
	return m.modalBox(b.String())
}

// serverRow pinta una fila de servidor con su indicador y nº de herramientas.
func (mm mcpModal) serverRow(server mcp.Status, selected bool, width int) string {
	dot := offDotStyle.Render("○")
	if server.Online {
		dot = activeDotStyle.Render("●")
	}

	command := strings.TrimSpace(server.Command + " " + strings.Join(server.Args, " "))
	var detail string
	switch {
	case server.Online:
		detail = fmt.Sprintf("(%d tools)", len(server.Tools))
	case server.Err != "":
		detail = "(error)"
	default:
		detail = "(Off)"
	}

	nameWidth := max(8, width/3)
	commandWidth := max(10, width-nameWidth-len(detail)-8)
	line := fmt.Sprintf("%s %s │ %s %s",
		dot,
		textStyle.Render(padTo(server.Name, nameWidth)),
		oneLine(command, commandWidth),
		mutedStyle.Render(detail),
	)
	if selected {
		return selectedRowStyle.Render(ansi.Truncate(" "+line, max(10, width+2), "…"))
	}
	return line
}

func fieldLabel(label string, focused bool) string {
	if focused {
		return labelFocusStyle.Render(label+":") + " "
	}
	return labelStyle.Render(label+":") + " "
}

// padTo rellena con espacios hasta width visible.
func padTo(value string, width int) string {
	if pad := width - len([]rune(value)); pad > 0 {
		return value + strings.Repeat(" ", pad)
	}
	return value
}
