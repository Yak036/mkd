package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Yak036/mkd/internal/agent"
	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/config"
	mkdcontext "github.com/Yak036/mkd/internal/context"
	"github.com/Yak036/mkd/internal/mcp"
	"github.com/Yak036/mkd/internal/storage"
	"github.com/Yak036/mkd/internal/swarm"
	"github.com/Yak036/mkd/internal/update"
)

// getwd aisla os.Getwd para tests del bucle de agente.
func getwd() (string, error) { return os.Getwd() }

const maxContextFileBytes = mkdcontext.MaxFileBytes

var (
	keyQuit     = key.NewBinding(key.WithKeys("ctrl+c"))
	keyEsc      = key.NewBinding(key.WithKeys("esc"))
	keyEnter    = key.NewBinding(key.WithKeys("enter"))
	keyPalette  = key.NewBinding(key.WithKeys("ctrl+p"))
	keyModels   = key.NewBinding(key.WithKeys("ctrl+m", "f2", "ctrl+o"))
	keyKeys     = key.NewBinding(key.WithKeys("ctrl+k"))
	keySidebar  = key.NewBinding(key.WithKeys("ctrl+b"))
	keyClear    = key.NewBinding(key.WithKeys("ctrl+l"))
	keySessions = key.NewBinding(key.WithKeys("ctrl+h"))
	keyCopy     = key.NewBinding(key.WithKeys("ctrl+y"))
	keyBlocks   = key.NewBinding(key.WithKeys("alt+c"))
	keyAgents   = key.NewBinding(key.WithKeys("ctrl+t"))
	keyCommit   = key.NewBinding(key.WithKeys("ctrl+g"))
	keyMCP      = key.NewBinding(key.WithKeys("ctrl+o"))
	keySwarmMod = key.NewBinding(key.WithKeys("alt+s"))
	keyPageUp   = key.NewBinding(key.WithKeys("pgup"))
	keyPageDown = key.NewBinding(key.WithKeys("pgdown"))
)

type state int

const (
	stateSplash state = iota
	stateWorkspace
)

type chatMessage struct {
	Role    string
	Content string
	Display string
}

type noteKind int

const (
	noteInfo noteKind = iota
	noteError
	noteSuccess
)

type note struct {
	Kind noteKind
	Text string
}

type slashCommand struct {
	Name        string
	Description string
}

var slashCommands = []slashCommand{
	{Name: "/connect", Description: "Conectar y verificar proveedor de IA"},
	{Name: "/model", Description: "Seleccionar modelo de proveedores conectados"},
	{Name: "/model add", Description: "Agregar modelo al proveedor conectado: /model add <id>"},
	{Name: "/keys", Description: "Configurar credenciales de proveedores"},
	{Name: "/add", Description: "Adjuntar archivo al contexto: /add <ruta>"},
	{Name: "/remove", Description: "Quitar archivo del contexto: /remove <ruta>"},
	{Name: "/system", Description: "Editar el system prompt activo"},
	{Name: "/compact", Description: "Resumir la conversación para liberar contexto"},
	{Name: "/sessions", Description: "Historial de sesiones guardadas"},
	{Name: "/blocks", Description: "Bloques de código detectados (Alt+C)"},
	{Name: "/copy", Description: "Copiar el último bloque de código (Ctrl+Y)"},
	{Name: "/mode", Description: "Modo agente: /mode plan|default|auto|chat"},
	{Name: "/commit", Description: "Generar y confirmar un commit git (Ctrl+G)"},
	{Name: "/review", Description: "Code review del diff actual: /review [rama-base]"},
	{Name: "/mcp", Description: "Gestor de servidores MCP (Ctrl+O)"},
	{Name: "/agents", Description: "Equipo de sub-agentes Swarm (Ctrl+T)"},
	{Name: "/swarm", Description: "Misión multi-agente: /swarm <tarea>"},
	{Name: "/skills", Description: "Listar skills instaladas (:@nombre)"},
	{Name: "/skill add", Description: "Crear una skill: /skill add <nombre>"},
	{Name: "/skills-dir", Description: "Cambiar carpeta de skills: /skills-dir <ruta>"},
	{Name: "/rules", Description: "Listar reglas del proyecto (:@nombre)"},
	{Name: "/rule rm", Description: "Liberar una regla forzada: /rule rm <nombre>"},
	{Name: "/rules-dir", Description: "Cambiar carpeta global de reglas: /rules-dir <ruta>"},
	{Name: "/new", Description: "Crear una sesión limpia"},
	{Name: "/title", Description: "Renombrar la sesión: /title <texto>"},
	{Name: "/clear", Description: "Limpiar el historial de la conversación"},
	{Name: "/help", Description: "Mostrar la ayuda de comandos"},
}

const slashMenuRows = 7

// Options agrupa las dependencias que recibe el workspace.
type Options struct {
	Client *ai.Client
	Config config.Config
	Files  []string
	// MCP es el gestor de servidores MCP (opcional; nil = sin MCP).
	MCP *mcp.Manager
	// UpdateCheck entrega el resultado de la comprobación de versión (opcional).
	// La TUI lo escucha sin bloquear el arranque.
	UpdateCheck <-chan update.Check
}

// Model es el estado completo del workspace full-screen.
type Model struct {
	client *ai.Client
	cfg    config.Config
	ctxMgr *mkdcontext.Manager
	mcp    *mcp.Manager

	state  state
	splash splashModel

	// updateCh entrega el resultado de la comprobación de versión.
	updateCh <-chan update.Check
	// updateNotice se muestra en la barra de estado hasta que el usuario
	// actualice (ej. "↑ v0.7.0 disponible · mkd upgrade").
	updateNotice string

	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	history []chatMessage
	blocks  []string
	stream  strings.Builder
	summary string

	streaming  bool
	waiting    bool
	cancelled  bool
	streamCh   <-chan ai.Chunk
	cancel     context.CancelFunc
	compacting bool

	// mcpPending guarda las llamadas MCP esperando decisión HITL por callID.
	mcpPending map[string]agent.Call

	// swarm contiene el estado de la misión multi-agente en curso.
	swarm swarmState
	// bus son los canales vivos de la misión actual.
	bus *swarmBus
	// orchestrator es el orquestador de la misión en curso.
	orchestrator *swarm.Orchestrator
	// orchestratorCmd es el comando que ejecuta la misión (útil para tests).
	orchestratorCmd tea.Cmd
	// viewTab es la pestaña visible (0 = Orquestador, 1..9 = sub-agente).
	viewTab int

	// reviewCancel cancela el stream de /review en curso.
	reviewCancel context.CancelFunc
	// reviewing marca que el stream actual es un code review.
	reviewing bool

	usage       mkdcontext.Usage
	modelsCache storage.ModelsCache

	session        storage.Session
	sessionDirty   bool
	sessionAutoSet bool

	notes  []note
	notice string

	slashMenuVisible    bool
	slashMenuIndex      int
	slashMenuScroll     int
	slashMenuSuppressed string
	slashMatches        []slashCommand

	contextFiles []ContextFile
	modal        modalState
	mentions     MentionsState
	agent        agentState

	sidebarVisible bool

	width             int
	height            int
	innerWidth        int
	innerHeight       int
	mainHeight        int
	conversationWidth int
	sidebarWidth      int
	effectiveSidebar  bool

	renderer *glamour.TermRenderer
}

// New construye el workspace. Arranca en el splash pixel art.
func New(opts Options) Model {
	ta := textarea.New()
	ta.Placeholder = "Escribe un mensaje…  / comandos · Ctrl+M/F2 modelo · Ctrl+K keys"
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.SetHeight(2)
	ta.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "❯ "
		}
		return "  "
	})
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "shift+enter", "ctrl+j"),
		key.WithHelp("alt+enter", "nueva línea"),
	)
	ta.FocusedStyle.Base = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(purple).
		Background(surface)
	ta.BlurredStyle.Base = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Background(surface)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(surface)
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(purple).Background(surface)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(muted).Background(surface)
	ta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(text).Background(surface)
	ta.FocusedStyle.EndOfBuffer = lipgloss.NewStyle().Background(surface)
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle().Background(surface)
	ta.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(muted).Background(surface)
	ta.BlurredStyle.Placeholder = lipgloss.NewStyle().Foreground(muted).Background(surface)
	ta.BlurredStyle.Text = lipgloss.NewStyle().Foreground(text).Background(surface)
	ta.BlurredStyle.EndOfBuffer = lipgloss.NewStyle().Background(surface)
	ta.Focus()

	sp := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(cyan)),
	)

	vp := viewport.New(0, 0)
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 3

	m := Model{
		client:         opts.Client,
		cfg:            opts.Config,
		mcp:            opts.MCP,
		state:          stateSplash,
		splash:         newSplash(),
		viewport:       vp,
		textarea:       ta,
		spinner:        sp,
		sidebarVisible: true,

		swarm:  newSwarmState(),
		width:  100,
		height: 30,
	}
	m.ctxMgr = mkdcontext.NewManager(opts.Config.SystemPrompt, "", mkdcontext.DefaultCompactThreshold)
	m.applyRulesConfig()
	m.updateCh = opts.UpdateCheck
	m.modelsCache, _ = storage.LoadModelsCache()
	m.startSession()
	m.seedContextFiles(opts.Files)
	m.layout()
	return m
}

// startSession crea una sesión nueva en memoria con el cwd actual.
func (m *Model) startSession() {
	cwd, _ := os.Getwd()
	provider, _ := m.cfg.Active()
	m.session = storage.Session{
		ID:        storage.NewSessionID(),
		Title:     "",
		Cwd:       cwd,
		Provider:  m.cfg.ActiveProvider,
		Model:     provider.ActiveModel,
		CreatedAt: time.Now(),
	}
	m.sessionDirty = false
	m.sessionAutoSet = false
}

// syncSession refresca proveedor/modelo/archivos y marca la sesión para guardar.
func (m *Model) syncSession() {
	provider, _ := m.cfg.Active()
	m.session.Provider = m.cfg.ActiveProvider
	m.session.Model = provider.ActiveModel
	m.session.Files = m.contextFilePaths()
	m.sessionDirty = true
}

func (m Model) contextFilePaths() []string {
	paths := make([]string, 0, len(m.contextFiles))
	for _, file := range m.contextFiles {
		paths = append(paths, file.Path)
	}
	return paths
}

// persistSession guarda la sesión si cambió y tiene contenido.
func (m *Model) persistSession(force bool) {
	if !force && (!m.sessionDirty || len(m.history) == 0) {
		return
	}
	if m.session.ID == "" {
		m.startSession()
	}

	messages := make([]storage.Message, 0, len(m.history))
	for _, msg := range m.history {
		messages = append(messages, storage.Message{Role: msg.Role, Content: msg.Content, Display: msg.Display})
	}
	m.session.Messages = messages
	m.session.Summary = m.summary
	if strings.TrimSpace(m.session.Title) == "" {
		m.session.Title = firstPromptTitle(m.history)
	}
	if _, err := storage.SaveSession(m.session); err != nil {
		m.notice = "No se pudo guardar la sesión: " + err.Error()
		return
	}
	m.sessionDirty = false
}

// firstPromptTitle deriva el título del primer mensaje del usuario.
func firstPromptTitle(history []chatMessage) string {
	for _, msg := range history {
		if msg.Role != ai.RoleUser {
			continue
		}
		title := storage.DeriveTitle(msg.Display)
		if title != "" && title != "Sesión sin título" {
			return title
		}
		title = storage.DeriveTitle(msg.Content)
		if title != "" {
			return title
		}
	}
	return "Sesión sin título"
}

// loadSession reemplaza la conversación activa por una sesión de disco.
func (m Model) loadSession(id string) (Model, tea.Cmd) {
	session, err := storage.LoadSession(id)
	if err != nil {
		m.notice = "No se pudo abrir la sesión: " + err.Error()
		return m, nil
	}

	m.cancelTurn()
	m.streamCh = nil
	m.cancelled = false
	m.compacting = false
	m.stream.Reset()
	m.notes = nil
	m.hideSlashMenu()

	m.session = session
	m.sessionDirty = false
	m.sessionAutoSet = strings.TrimSpace(session.Title) != "" && session.Title != "Sesión sin título"
	m.summary = session.Summary

	m.history = make([]chatMessage, 0, len(session.Messages))
	for _, msg := range session.Messages {
		display := msg.Display
		if display == "" {
			display = msg.Content
		}
		m.history = append(m.history, chatMessage{Role: msg.Role, Content: msg.Content, Display: display})
	}

	m.contextFiles = nil
	m.ClearMentions()
	m.ctxMgr = mkdcontext.NewManager(m.cfg.SystemPrompt, session.Cwd, mkdcontext.DefaultCompactThreshold)
	m.applyRulesConfig()
	if m.ctxMgr.WorkDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			m.ctxMgr.WorkDir = cwd
		}
	}
	for _, path := range session.Files {
		if _, err := m.ctxMgr.AddFile(path); err != nil {
			m.notice = "Archivo no disponible: " + path
		}
	}

	m.modal.kind = modalNone
	m.rebuildHistory()
	m.refreshUsage()
	m.rebuildHistory()
	m.refresh()
	m.viewport.GotoBottom()
	m.notice = "Sesión cargada: " + valueOr(session.Title, session.ID)
	return m, m.textarea.Focus()
}

func (m Model) newSession() (Model, tea.Cmd) {
	m.persistSession(false)
	m.cancelTurn()
	m.streamCh = nil
	m.cancelled = false
	m.compacting = false
	m.stream.Reset()
	m.hideSlashMenu()

	m.history = nil
	m.blocks = nil
	m.notes = nil
	m.summary = ""
	m.contextFiles = nil

	cwd, _ := os.Getwd()
	if m.ctxMgr != nil && m.ctxMgr.WorkDir != "" {
		cwd = m.ctxMgr.WorkDir
	}
	m.ClearMentions()
	m.ctxMgr = mkdcontext.NewManager(m.cfg.SystemPrompt, cwd, mkdcontext.DefaultCompactThreshold)
	m.applyRulesConfig()
	m.startSession()
	m.modal.kind = modalNone

	m.refreshUsage()
	m.refresh()
	m.viewport.GotoTop()
	m.notice = "Nueva sesión creada."
	return m, m.textarea.Focus()
}

// Init arranca el splash y conecta los servidores MCP habilitados.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{splashTick()}
	if m.mcp != nil {
		cmds = append(cmds, mcpInitCmd(m.mcp))
	}
	if m.updateCh != nil {
		cmds = append(cmds, waitUpdateCheck(m.updateCh))
	}
	return tea.Batch(cmds...)
}

// updateCheckMsg llega cuando finaliza la comprobación de versión.
type updateCheckMsg update.Check

// waitUpdateCheck espera el resultado sin bloquear el bucle de la TUI.
func waitUpdateCheck(ch <-chan update.Check) tea.Cmd {
	return func() tea.Msg {
		check, ok := <-ch
		if !ok {
			return nil
		}
		return updateCheckMsg(check)
	}
}

// mcpInitCmd lanza los servidores MCP en segundo plano.
func mcpInitCmd(mgr *mcp.Manager) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		mgr.StartAll(ctx)
		return mcpStatusMsg{servers: mgr.Statuses()}
	}
}

// Update procesa el splash, los modales, el streaming y el workspace.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		m.layout()
		return m, nil

	case splashTickMsg:
		if m.state != stateSplash {
			return m, nil
		}
		m.splash.frame++
		if m.splash.done() {
			return m.finishSplash()
		}
		return m, splashTick()

	case modelsFetchedMsg:
		return m.handleModelsFetched(msg)

	case providerConnectionDoneMsg:
		return m.handleProviderConnectionDone(msg)

	case codexAuthDoneMsg:
		return m.handleCodexAuthDone(msg)

	case claudeAuthDoneMsg:
		return m.handleClaudeAuthDone(msg)

	case compactDoneMsg:
		return m.handleCompactDone(msg)

	case sessionsLoadedMsg:
		return m.handleSessionsLoaded(msg)

	case sessionSavedMsg:
		return m.handleSessionSaved(msg)

	case updateCheckMsg:
		if notice := update.Check(msg).Notice(); notice != "" {
			m.updateNotice = notice
		}
		return m, nil

	case agentToolCallsMsg:
		return m.handleAgentToolCalls(msg)

	case agentToolDoneMsg:
		if msg.mcp {
			return m.handleMCPToolDone(msg)
		}
		return m.handleAgentToolDone(msg)

	case agentRoundDoneMsg:
		if m.modal.kind == modalQuestion {
			return m, nil
		}
		return m.handleAgentRoundDone(msg)

	case commitContextMsg:
		return m.handleCommitContext(msg)

	case commitGeneratedMsg:
		return m.handleCommitGenerated(msg)

	case commitDoneMsg:
		return m.handleCommitDone(msg)

	case mcpStatusMsg:
		return m.handleMCPStatus(msg)

	case swarmEventMsg:
		return m.handleSwarmEvt(msg)

	case swarmDoneMsg:
		return m.handleSwarmDone(msg)

	case swarmPermissionMsg:
		return m.handleSwarmPermission(msg)

	case swarmAskMsg:
		return m.handleSwarmAsk(msg)

	case spinner.TickMsg:
		if !m.waiting && !m.modal.models.loading && !m.modal.provider.testing && !m.agent.requested && !m.swarm.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case streamStartedMsg:
		return m.handleStreamStarted(msg)

	case chunkMsg:
		return m.handleChunk(msg)
	}

	if m.state == stateSplash {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			if key.Matches(keyMsg, keyQuit) {
				return m.quit()
			}
			return m.finishSplash()
		}
		return m, nil
	}

	if m.modal.kind != modalNone {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			return m.updateModal(msg)
		case tea.MouseMsg:
			return m, nil
		default:
			return m.updateModalMsg(msg)
		}
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.slashMenuVisible {
			switch msg.String() {
			case "esc":
				m.hideSlashMenu()
				return m, nil
			case "up":
				m.moveSlashSelection(-1)
				return m, nil
			case "down":
				m.moveSlashSelection(1)
				return m, nil
			case "tab":
				return m.completeSlashSelection()
			case "enter":
				return m.executeSlashSelection()
			}
		}
		return m.updateWorkspaceKey(msg)
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	default:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		m.updateSlashMenu()
		m.mentionTextareaChanged()
		return m, cmd
	}
}

// View compone splash o workspace y superpone el modal activo.
func (m Model) View() string {
	if m.state == stateSplash {
		return m.splash.view(m.width, m.height)
	}

	base := m.workspaceView()
	if m.modal.kind != modalNone {
		base = overlayModal(base, m.modalView(), m.width, m.height)
	} else {
		if m.slashMenuVisible {
			menu := m.slashMenuView()
			menuHeight := lipgloss.Height(menu)
			y := m.height - 7 - menuHeight
			base = overlayAt(base, menu, 2, y, m.width, m.height)
		}
		if view, x, y := m.MentionsLayer(); view != "" {
			base = overlayAt(base, view, x, y, m.width, m.height)
		}
	}
	return paintOpaqueBackground(base, m.width, surface)
}

func (m Model) finishSplash() (tea.Model, tea.Cmd) {
	m.state = stateWorkspace
	m.layout()
	m.rebuildHistory()
	m.refreshUsage()

	if m.needsOnboarding() {
		return m.openProviderModal()
	}
	// Si el modelo activo está vacío pero hay proveedor válido, elegimos el
	// primero disponible en vez de abrir un modal que interrumpe al arrancar.
	if provider, ok := m.cfg.Active(); ok && strings.TrimSpace(provider.ActiveModel) == "" {
		if len(provider.Models) > 0 {
			provider.ActiveModel = provider.Models[0]
			m.cfg.Providers[m.cfg.ActiveProvider] = provider
			m.cfg.Normalize()
			_ = m.persistConfig()
		}
	}
	return m, m.textarea.Focus()
}

func (m *Model) seedContextFiles(files []string) {
	for _, path := range files {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			m.notice = "No se pudo adjuntar " + path + ": " + err.Error()
			continue
		}
		if info.IsDir() || info.Size() > maxContextFileBytes {
			m.notice = "Archivo omitido (directorio o >1 MiB): " + path
			continue
		}
		file, err := m.ctxMgr.AddFile(path)
		if err != nil {
			m.notice = "No se pudo adjuntar " + path + ": " + err.Error()
			continue
		}
		m.contextFiles = append(m.contextFiles, ContextFile{Path: file.Path, Size: file.Size, Hash: file.Hash})
	}
	if len(files) > 0 {
		m.syncSession()
	}
}

func (m Model) needsOnboarding() bool {
	provider, ok := m.cfg.Active()
	if !ok {
		return true
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		return true
	}
	return strings.TrimSpace(provider.APIKey) == "" && !config.IsLocal(provider.BaseURL)
}

func (m Model) quit() (Model, tea.Cmd) {
	// Una pregunta o un turno abiertos al salir quedan cancelados: el usuario
	// volverá a la sesión sin acciones a medio resolver.
	if m.modal.kind == modalQuestion {
		m.answerPendingQuestionBeforeQuit()
	}
	if m.cancel != nil {
		m.cancel()
	}
	if m.reviewCancel != nil {
		m.reviewCancel()
	}
	m.persistSession(false)
	if m.mcp != nil {
		m.mcp.Close()
	}
	return m, tea.Quit
}

func (m Model) updateWorkspaceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.notice = ""

	// El popup de menciones tiene prioridad absoluta sobre el input: Enter/Tab/
	// flechas deben resolver la mención antes de enviar el mensaje o abrir
	// cualquier otro modal.
	if m.MentionPopupVisible() {
		switch msg.String() {
		case "esc":
			m.hideMentions()
			return m, nil
		case "up":
			m.moveMentionSelection(-1)
			return m, nil
		case "down":
			m.moveMentionSelection(1)
			return m, nil
		case "pgup":
			m.moveMentionSelection(-mentionPopupRows)
			return m, nil
		case "pgdown":
			m.moveMentionSelection(mentionPopupRows)
			return m, nil
		case "tab":
			return m.completeMention()
		case "enter":
			return m.applySelectedMention()
		}
	}

	// Alt+0..9 cambia la pestaña del inspector Swarm.
	if tab, ok := swarmTabKey(msg.String()); ok {
		return m.switchViewTab(tab)
	}

	switch {
	case key.Matches(msg, keyQuit):
		return m.quit()

	case key.Matches(msg, keyPalette):
		return m.openPalette()

	case key.Matches(msg, keyModels):
		return m.openModelsModal()

	case key.Matches(msg, keyKeys):
		return m.openProviderModal()

	case key.Matches(msg, keySessions):
		return m.openSessionsModal()

	case key.Matches(msg, keyCopy):
		return m.copyLastBlock()

	case key.Matches(msg, keyBlocks):
		return m.openBlocksModal()

	case key.Matches(msg, keyAgents):
		return m.openAgentModal()

	case key.Matches(msg, keySwarmMod):
		return m.toggleSwarmMode()

	case key.Matches(msg, keyCommit):
		return m.openCommitModal()

	case key.Matches(msg, keyMCP):
		return m.openMCPModal()

	case key.Matches(msg, keySidebar):
		m.sidebarVisible = !m.sidebarVisible
		m.layout()
		return m, nil

	case key.Matches(msg, keyClear):
		m.clearConversation()
		return m, nil

	case key.Matches(msg, keyEsc):
		if m.swarm.running {
			if m.orchestrator != nil {
				m.orchestrator.Cancel()
			}
			if m.cancel != nil {
				m.cancel()
			}
			m.swarm.orchestratorStatus = "Cancelada por el usuario"
			if m.modal.kind == modalQuestion {
				m.modal.kind = modalNone
				m.modal.question = questionModal{}
			}
			m.refresh()
			return m, m.textarea.Focus()
		}
		if m.streaming || m.waiting || m.modal.kind == modalQuestion {
			m.cancelled = true
			m.cancelTurn()
			m.notes = append(m.notes, note{Kind: noteInfo, Text: "— turno cancelado —"})
			m.refresh()
			return m, m.textarea.Focus()
		}
		return m.quit()

	case key.Matches(msg, keyEnter):
		return m.submitInput()

	case key.Matches(msg, keyPageUp), key.Matches(msg, keyPageDown):
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.updateSlashMenu()
	m.mentionTextareaChanged()
	return m, cmd
}

func (m Model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.textarea.Value())
	if text == "" {
		return m, nil
	}
	if m.compacting {
		m.notice = "Espera a que termine la compactación."
		return m, nil
	}
	// Un solo turno a la vez: mezclar un envío nuevo con un stream, una ronda
	// de herramientas o un permiso pendiente rompe la secuencia
	// assistant(tool_calls) → role=tool que exige el proveedor.
	if m.busy() {
		m.notice = "Espera a que termine el turno actual (Esc para cancelar)."
		return m, nil
	}
	m.textarea.Reset()
	m.hideSlashMenu()

	if strings.HasPrefix(text, "/") {
		return m.handleSlash(text)
	}
	if !m.configured() {
		m.notice = "Ningún modelo disponible actualmente."
		return m.openProviderModal()
	}
	// En modo SWARM el agente principal actúa como Orquestador: cada mensaje
	// libre se convierte en una misión multi-agente.
	if m.swarmModeActive() {
		return m.startSwarmMission(text)
	}
	return m.send(text)
}

func (m Model) handleSlash(text string) (tea.Model, tea.Cmd) {
	m.hideSlashMenu()

	switch {
	case text == "/model":
		return m.openModelsModal()

	case text == "/model add":
		m.notice = "Uso: /model add <id>"
		return m, nil

	case strings.HasPrefix(text, "/model add "):
		id := strings.TrimSpace(strings.TrimPrefix(text, "/model add"))
		if active, ok := m.cfg.Active(); !ok || !active.Connected {
			m.notice = "El proveedor activo no tiene una conexión exitosa. Conéctate primero con /connect"
			return m, nil
		}
		return m.addModelAndActivate(id)

	case text == "/connect" || text == "/keys":
		return m.openProviderModal()

	case text == "/add":
		m.notice = "Uso: /add <ruta>"
		return m, nil

	case strings.HasPrefix(text, "/add "):
		path := strings.TrimSpace(strings.TrimPrefix(text, "/add"))
		return m.addContextFile(path)

	case text == "/remove":
		m.notice = "Uso: /remove <ruta>"
		return m, nil

	case strings.HasPrefix(text, "/remove "):
		path := strings.TrimSpace(strings.TrimPrefix(text, "/remove"))
		return m.removeContextFile(path)

	case text == "/system":
		return m.openSystemModal()

	case text == "/compact":
		return m.startCompact()

	case text == "/copy":
		return m.copyLastBlock()

	case text == "/skills":
		return m.listSkills()

	case text == "/rules":
		return m.listRules()

	case text == "/rules-dir":
		m.notice = "Uso: /rules-dir <ruta> (actual: " + m.cfg.RulesDir + ")"
		return m, nil

	case strings.HasPrefix(text, "/rules-dir "):
		return m.setRulesDir(strings.TrimSpace(strings.TrimPrefix(text, "/rules-dir")))

	case text == "/rule rm":
		m.notice = "Uso: /rule rm <nombre>"
		return m, nil

	case strings.HasPrefix(text, "/rule rm "):
		return m.removeRuleForced(strings.TrimPrefix(text, "/rule rm"))

	case text == "/skills-dir":
		m.notice = "Uso: /skills-dir <ruta> (actual: " + m.skillsDir() + ")"
		return m, nil

	case strings.HasPrefix(text, "/skills-dir "):
		return m.setSkillsDir(strings.TrimSpace(strings.TrimPrefix(text, "/skills-dir")))

	case text == "/skill add":
		m.notice = "Uso: /skill add <nombre>"
		return m, nil

	case strings.HasPrefix(text, "/skill add "):
		name := strings.TrimSpace(strings.TrimPrefix(text, "/skill add"))
		return m.createSkillPrompt(name)

	case text == "/skill rm":
		m.notice = "Uso: /skill rm <nombre>"
		return m, nil

	case strings.HasPrefix(text, "/skill rm "):
		name := strings.TrimSpace(strings.TrimPrefix(text, "/skill rm"))
		return m.removeSkill(name)

	case text == "/blocks":
		return m.openBlocksModal()

	case text == "/mode":
		m.notice = "Uso: /mode plan|default|auto|chat"
		return m, nil

	case strings.HasPrefix(text, "/mode "):
		mode := strings.TrimSpace(strings.TrimPrefix(text, "/mode"))
		if !m.setAgentMode(mode) {
			m.notice = "Modo inválido. Usa: plan, default, auto o chat."
			return m, nil
		}
		return m, nil

	case text == "/commit":
		return m.openCommitModal()

	case text == "/review":
		return m.startReview("")

	case strings.HasPrefix(text, "/review "):
		base := strings.TrimSpace(strings.TrimPrefix(text, "/review"))
		return m.startReview(base)

	case text == "/mcp":
		return m.openMCPModal()

	case text == "/agents":
		return m.openAgentModal()

	case text == "/swarm":
		m.notice = "Uso: /swarm <tarea>"
		return m, nil

	case strings.HasPrefix(text, "/swarm "):
		instruction := strings.TrimSpace(strings.TrimPrefix(text, "/swarm"))
		return m.startSwarmMission(instruction)

	case text == "/sessions":
		return m.openSessionsModal()

	case text == "/new":
		return m.newSession()

	case text == "/title":
		m.notice = "Uso: /title <texto>"
		return m, nil

	case strings.HasPrefix(text, "/title "):
		title := strings.TrimSpace(strings.TrimPrefix(text, "/title"))
		if title == "" {
			m.notice = "Uso: /title <texto>"
			return m, nil
		}
		m.session.Title = storage.DeriveTitle(title)
		m.sessionAutoSet = true
		m.persistSession(true)
		m.notice = "Título de sesión: " + m.session.Title
		return m, nil

	case text == "/clear":
		m.clearConversation()
		return m, nil

	case text == "/help":
		m.notice = "Comandos: /model, /keys, /add, /system, /compact, /sessions, /mode, /commit, /review, /mcp, /agents, /swarm, /clear"
		return m, nil

	default:
		m.notice = "Comando no reconocido: " + strings.Fields(text)[0]
		return m, nil
	}
}

func (m *Model) hideSlashMenu() {
	m.slashMenuVisible = false
	m.slashMenuSuppressed = m.textarea.Value()
	m.slashMatches = nil
	m.slashMenuIndex = 0
	m.slashMenuScroll = 0
}

func (m *Model) updateSlashMenu() {
	value := m.textarea.Value()
	if value == m.slashMenuSuppressed {
		m.slashMenuVisible = false
		return
	}
	m.slashMenuSuppressed = ""

	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\n\t") || strings.HasSuffix(value, " ") {
		m.hideSlashMenu()
		return
	}

	query := strings.ToLower(value)
	matches := make([]slashCommand, 0, len(slashCommands))
	for _, command := range slashCommands {
		if strings.HasPrefix(strings.ToLower(command.Name), query) {
			matches = append(matches, command)
		}
	}
	if len(matches) == 0 {
		m.hideSlashMenu()
		return
	}

	selected := ""
	if m.slashMenuVisible && m.slashMenuIndex >= 0 && m.slashMenuIndex < len(m.slashMatches) {
		selected = m.slashMatches[m.slashMenuIndex].Name
	}
	m.slashMatches = matches
	m.slashMenuVisible = true
	m.slashMenuIndex = 0
	if selected != "" {
		for i, command := range matches {
			if command.Name == selected {
				m.slashMenuIndex = i
				break
			}
		}
	}
	m.ensureSlashVisible()
}

func (m *Model) ensureSlashVisible() {
	if len(m.slashMatches) == 0 {
		m.slashMenuScroll = 0
		return
	}
	if m.slashMenuIndex < m.slashMenuScroll {
		m.slashMenuScroll = m.slashMenuIndex
	}
	if m.slashMenuIndex >= m.slashMenuScroll+slashMenuRows {
		m.slashMenuScroll = m.slashMenuIndex - slashMenuRows + 1
	}
	maxScroll := max(0, len(m.slashMatches)-slashMenuRows)
	if m.slashMenuScroll > maxScroll {
		m.slashMenuScroll = maxScroll
	}
}

func (m *Model) moveSlashSelection(delta int) {
	if len(m.slashMatches) == 0 {
		return
	}
	m.slashMenuIndex = min(max(0, m.slashMenuIndex+delta), len(m.slashMatches)-1)
	m.ensureSlashVisible()
}

func (m Model) completeSlashSelection() (tea.Model, tea.Cmd) {
	if !m.slashMenuVisible || len(m.slashMatches) == 0 {
		return m, nil
	}
	if m.slashMenuIndex < 0 || m.slashMenuIndex >= len(m.slashMatches) {
		return m, nil
	}
	command := m.slashMatches[m.slashMenuIndex]
	m.textarea.SetValue(command.Name + " ")
	m.textarea.CursorEnd()
	m.hideSlashMenu()
	return m, m.textarea.Focus()
}

func (m Model) executeSlashSelection() (tea.Model, tea.Cmd) {
	if !m.slashMenuVisible || len(m.slashMatches) == 0 {
		return m, nil
	}
	if m.slashMenuIndex < 0 || m.slashMenuIndex >= len(m.slashMatches) {
		return m, nil
	}
	command := m.slashMatches[m.slashMenuIndex]
	m.textarea.Reset()
	m.hideSlashMenu()
	return m.handleSlash(command.Name)
}

func (m Model) slashMenuView() string {
	maxWidth := m.innerWidth - 6
	if m.effectiveSidebar {
		maxWidth = m.conversationWidth - 2
	}
	outerWidth := min(84, max(50, maxWidth))
	contentWidth := max(20, outerWidth-4)
	rows := slashMenuRows

	lines := make([]string, 0, rows+1)
	title := titleStyle.Render("Comandos") + mutedStyle.Render("  Tab completar · Enter ejecutar")
	lines = append(lines, ansi.Truncate(title, contentWidth, "…"))

	start := m.slashMenuScroll
	if start > len(m.slashMatches)-1 {
		start = max(0, len(m.slashMatches)-rows)
	}
	end := min(len(m.slashMatches), start+rows)
	for i := start; i < end; i++ {
		command := m.slashMatches[i]
		if i == m.slashMenuIndex {
			line := fmt.Sprintf(" %-14s %s", command.Name, command.Description)
			lines = append(lines, selectedRowStyle.Render(ansi.Truncate(line, contentWidth, "…")))
			continue
		}
		padding := max(1, 15-lipgloss.Width(command.Name))
		description := ansi.Truncate(command.Description, max(4, contentWidth-15), "…")
		lines = append(lines, titleStyle.Render(command.Name)+strings.Repeat(" ", padding)+mutedStyle.Render(description))
	}
	for len(lines) < rows+1 {
		lines = append(lines, "")
	}

	content := strings.Join(lines, "\n")
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(purple).
		Background(surface).
		Padding(0, 1).
		Width(outerWidth - 2)
	return box.Render(content)
}

func (m Model) send(text string) (tea.Model, tea.Cmd) {
	if !m.configured() {
		m.notice = "Ningún modelo disponible actualmente."
		return m.openProviderModal()
	}

	m.hideSlashMenu()
	message := chatMessage{
		Role:    ai.RoleUser,
		Content: text,
		Display: text,
	}
	m.history = append(m.history, message)
	m.blocks = append(m.blocks, m.renderMessage(message))

	if !m.sessionAutoSet {
		m.session.Title = storage.DeriveTitle(text)
		m.sessionAutoSet = true
	}
	m.syncSession()

	m.notes = nil
	m.stream.Reset()
	m.waiting = true
	m.streaming = false
	m.cancelled = false
	m.notice = ""

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	m.notifyFileChanges()
	m.refreshUsage()
	m.refresh()
	m.viewport.GotoBottom()

	// Modo agente: el turno se resuelve con herramientas (tool calling).
	if m.cfg.AgentMode != config.ModeChat {
		return m.startAgent()
	}

	prepared, _ := m.ctxMgr.Prepare(m.summary, m.apiHistory())
	if extra := m.mentionsContextBlock(); extra != "" {
		prepared.Files = strings.TrimSpace(prepared.Files + "\n\n" + extra)
	}
	return m, tea.Batch(
		startStream(ctx, m.client, prepared),
		m.spinner.Tick,
	)
}

func startStream(ctx context.Context, client *ai.Client, prepared mkdcontext.Prepared) tea.Cmd {
	return func() tea.Msg {
		ch, err := client.StreamMessages(ctx, prepared.Messages())
		return streamStartedMsg{ch: ch, err: err}
	}
}

func waitForChunk(ch <-chan ai.Chunk) tea.Cmd {
	return func() tea.Msg {
		chunk, ok := <-ch
		if !ok {
			return chunkMsg{done: true}
		}
		return chunkMsg{content: chunk.Content, err: chunk.Err}
	}
}

type streamStartedMsg struct {
	ch  <-chan ai.Chunk
	err error
}

type chunkMsg struct {
	content string
	err     error
	done    bool
}

func (m Model) handleStreamStarted(msg streamStartedMsg) (tea.Model, tea.Cmd) {
	if m.cancelled {
		m.finishStream()
		m.refresh()
		return m, m.maybeAutoCompact()
	}
	if msg.err != nil {
		m.failStream(msg.err)
		return m, nil
	}
	m.streamCh = msg.ch
	return m, waitForChunk(msg.ch)
}

func (m Model) handleChunk(msg chunkMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if m.cancelled {
			m.finishStream()
			m.refresh()
			return m, m.maybeAutoCompact()
		}
		m.failStream(msg.err)
		return m, nil
	}
	if msg.content != "" {
		m.waiting = false
		m.streaming = true
		m.stream.WriteString(msg.content)
		m.refresh()
	}
	if msg.done {
		m.finishStream()
		m.refresh()
		return m, m.maybeAutoCompact()
	}
	return m, waitForChunk(m.streamCh)
}

func (m *Model) finishStream() {
	content := m.stream.String()
	m.stream.Reset()

	m.waiting = false
	m.streaming = false
	m.streamCh = nil

	if strings.TrimSpace(content) != "" {
		message := chatMessage{Role: ai.RoleAssistant, Content: content, Display: content}
		m.history = append(m.history, message)
		m.blocks = append(m.blocks, m.renderMessage(message))
	}
	if m.cancelled {
		m.notes = append(m.notes, note{Kind: noteInfo, Text: "— generación cancelada —"})
	}

	m.cancelled = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.reviewing {
		m.finishReview()
	}

	m.refreshUsage()
	m.syncSession()
	m.persistSession(false)
}

func (m *Model) failStream(err error) {
	content := m.stream.String()
	m.stream.Reset()

	m.waiting = false
	m.streaming = false
	m.streamCh = nil
	m.cancelled = false

	if strings.TrimSpace(content) != "" {
		message := chatMessage{Role: ai.RoleAssistant, Content: content, Display: content}
		m.history = append(m.history, message)
		m.blocks = append(m.blocks, m.renderMessage(message))
	}
	m.notes = append(m.notes, note{Kind: noteError, Text: "Error: " + err.Error()})

	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.reviewing {
		m.finishReview()
	}
	m.refreshUsage()
	m.syncSession()
	m.persistSession(false)
	m.refresh()
}

func (m *Model) clearConversation() {
	m.cancelTurn()
	m.streamCh = nil
	m.cancelled = false
	m.compacting = false
	m.hideSlashMenu()

	m.history = nil
	m.blocks = nil
	m.notes = nil
	m.stream.Reset()
	m.summary = ""
	m.session.Title = ""
	m.sessionAutoSet = false
	m.syncSession()
	m.persistSession(false)
	m.refreshUsage()
	m.notice = "Historial limpiado."
	m.refresh()
	m.viewport.GotoTop()
}

func (m *Model) addContextFile(path string) (tea.Model, tea.Cmd) {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "" {
		m.notice = "Uso: /add <ruta>"
		return m, nil
	}

	existing := false
	for i := range m.contextFiles {
		if m.contextFiles[i].Path == path {
			existing = true
			break
		}
	}

	file, err := m.ctxMgr.AddFile(path)
	if err != nil {
		m.notice = "No se pudo adjuntar: " + err.Error()
		return m, nil
	}

	if existing {
		for i := range m.contextFiles {
			if m.contextFiles[i].Path == file.Path {
				m.contextFiles[i] = ContextFile{Path: file.Path, Size: file.Size, Hash: file.Hash}
			}
		}
		m.notice = "Archivo actualizado: " + file.Path
	} else {
		m.contextFiles = append(m.contextFiles, ContextFile{Path: file.Path, Size: file.Size, Hash: file.Hash})
		m.notice = "Archivo adjuntado: " + file.Path
	}
	m.syncSession()
	m.refreshUsage()
	return m, nil
}

func (m *Model) removeContextFile(path string) (tea.Model, tea.Cmd) {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "" {
		m.notice = "Uso: /remove <ruta>"
		return m, nil
	}

	found := false
	filtered := m.contextFiles[:0]
	for _, file := range m.contextFiles {
		if file.Path == path {
			found = true
			continue
		}
		filtered = append(filtered, file)
	}
	if !found {
		m.notice = "El archivo no está en el contexto: " + path
		return m, nil
	}
	m.contextFiles = filtered
	m.ctxMgr.RemoveFile(path)
	m.syncSession()
	m.refreshUsage()
	m.notice = "Archivo quitado: " + path
	return m, nil
}

// notifyFileChanges informa en la nota inferior si algún archivo anclado
// cambió en disco desde el último ensamblado.
func (m *Model) notifyFileChanges() {
	changed := m.ctxMgr.RefreshFiles()
	if len(changed) == 0 {
		return
	}
	for i := range m.contextFiles {
		for _, path := range changed {
			if m.contextFiles[i].Path == path {
				m.contextFiles[i].Size = 0
			}
		}
	}
	m.syncContextFileSizes()
	m.notes = append(m.notes, note{
		Kind: noteInfo,
		Text: fmt.Sprintf("↻ %d archivo(s) recargado(s) desde disco", len(changed)),
	})
}

func (m *Model) syncContextFileSizes() {
	files := m.ctxMgr.Files()
	for i := range m.contextFiles {
		for _, file := range files {
			if file.Path == m.contextFiles[i].Path {
				m.contextFiles[i].Size = file.Size
				m.contextFiles[i].Hash = file.Hash
			}
		}
	}
}

func (m *Model) addModelAndActivate(id string) (tea.Model, tea.Cmd) {
	id = strings.TrimSpace(id)
	if id == "" {
		m.notice = "Uso: /model add <id>"
		return m, nil
	}
	provider, ok := m.cfg.Active()
	if !ok {
		m.notice = "No hay proveedor activo."
		return m, nil
	}
	if !containsString(provider.Models, id) {
		provider.Models = append(provider.Models, id)
	}
	provider.ActiveModel = id
	m.cfg.Providers[m.cfg.ActiveProvider] = provider
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		m.notice = "Error al guardar: " + err.Error()
		return m, nil
	}
	m.notice = "Modelo activo: " + id
	m.refresh()
	return m, nil
}

func (m *Model) persistConfig() error {
	if err := m.cfg.Validate(); err != nil {
		return err
	}
	m.cfg.Normalize()
	if _, err := config.Save(m.cfg); err != nil {
		return err
	}
	client, err := ai.New(m.cfg)
	if err != nil {
		return err
	}
	m.client = client
	m.refresh()
	return nil
}

func (m Model) runPaletteAction(id string) (Model, tea.Cmd) {
	switch id {
	case "connect", "provider":
		return m.openProviderModal()
	case "model":
		return m.openModelsModal()
	case "codex":
		return m.openProviderCodexModal()
	case "fetch":
		var cmd tea.Cmd
		m, cmd = m.openModelsModal()
		var fetchCmd tea.Cmd
		m, fetchCmd = m.startFetchModels()
		return m, tea.Batch(cmd, fetchCmd)
	case "add":
		m.modal.kind = modalNone
		m.textarea.SetValue("/add ")
		m.textarea.CursorEnd()
		return m, m.textarea.Focus()
	case "system":
		return m.openSystemModal()
	case "compact":
		return m.startCompact()
	case "sessions":
		return m.openSessionsModal()
	case "new":
		return m.newSession()
	case "clear":
		m.clearConversation()
		return m, nil
	case "sidebar":
		m.sidebarVisible = !m.sidebarVisible
		m.layout()
		return m, nil
	case "agents":
		return m.openAgentModal()
	case "swarmmode":
		next, cmd := m.toggleSwarmMode()
		return next.(Model), cmd
	case "mode":
		next, cmd := m.cycleAgentMode()
		return next.(Model), cmd
	default:
		return m, nil
	}
}

type compactDoneMsg struct {
	history []ai.Message
	summary string
	result  mkdcontext.CompactResult
	err     error
}

func (m Model) startCompact() (Model, tea.Cmd) {
	if !m.configured() {
		m.notice = "Ningún modelo disponible actualmente."
		return m, nil
	}
	if m.streaming || m.waiting {
		m.notice = "Espera a que termine la generación actual."
		return m, nil
	}
	if len(m.history) <= mkdcontext.CompactKeepRecent {
		m.notice = "Historial demasiado corto para compactar."
		return m, nil
	}
	if m.compacting {
		return m, nil
	}

	m.compacting = true
	m.notice = "Compactando conversación…"
	return m, compactCmd(m.ctxMgr, m.client, m.summary, m.apiHistory())
}

// shouldAutoCompact indica si el uso superó el umbral configurado.
func (m Model) shouldAutoCompact() bool {
	if m.ctxMgr == nil || m.cfg.CompactThreshold <= 0 {
		return false
	}
	return m.usage.TotalTokens >= m.cfg.CompactThreshold
}

// maybeAutoCompact lanza la compactación automática tras terminar un stream.
func (m Model) maybeAutoCompact() tea.Cmd {
	_, cmd := m.startAutoCompact()
	return cmd
}

// startAutoCompact compara contra el estado y devuelve el comando.
func (m Model) startAutoCompact() (Model, tea.Cmd) {
	if m.compacting || m.streaming || m.waiting || !m.configured() {
		return m, nil
	}
	if !m.shouldAutoCompact() {
		return m, nil
	}
	if len(m.history) <= mkdcontext.CompactKeepRecent {
		return m, nil
	}
	m.compacting = true
	m.notice = "Compactación automática por umbral de contexto…"
	return m, compactCmd(m.ctxMgr, m.client, m.summary, m.apiHistory())
}

func compactCmd(mgr *mkdcontext.Manager, client *ai.Client, summary string, history []ai.Message) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		compacted, text, result, err := mgr.Compact(ctx, client, summary, history)
		return compactDoneMsg{history: compacted, summary: text, result: result, err: err}
	}
}

func (m Model) handleCompactDone(msg compactDoneMsg) (Model, tea.Cmd) {
	m.compacting = false
	if msg.err != nil {
		m.notes = append(m.notes, note{Kind: noteError, Text: "Error al compactar: " + msg.err.Error()})
		m.refresh()
		return m, nil
	}

	m.summary = strings.TrimSpace(msg.summary)
	m.history = make([]chatMessage, 0, len(msg.history))
	for _, item := range msg.history {
		display := item.Content
		if strings.HasPrefix(strings.TrimSpace(item.Content), "<context_summary>") {
			display = mutedStyle.Render("— contexto previo comprimido —")
		}
		m.history = append(m.history, chatMessage{Role: item.Role, Content: item.Content, Display: display})
	}

	m.rebuildHistory()
	m.refreshUsage()
	m.syncSession()
	m.persistSession(true)

	freed := msg.result.Freed()
	m.notes = append(m.notes, note{
		Kind: noteSuccess,
		Text: fmt.Sprintf("✓ Contexto compactado: %s -> %s tokens (−%s, %d msgs)",
			formatTokens(msg.result.BeforeTokens),
			formatTokens(msg.result.AfterTokens),
			formatTokens(freed),
			msg.result.Removed,
		),
	})
	m.refresh()
	return m, nil
}

func (m Model) apiHistory() []ai.Message {
	messages := make([]ai.Message, 0, len(m.history))
	for _, msg := range m.history {
		messages = append(messages, ai.Message{Role: msg.Role, Content: msg.Content})
	}
	return messages
}

// refreshUsage recalcula el desglose de tokens que consume el sidebar.
func (m *Model) refreshUsage() {
	if m.ctxMgr == nil {
		return
	}
	m.ctxMgr.SetSystemPrompt(m.cfg.SystemPrompt)
	m.ctxMgr.SetMaxTokens(m.cfg.CompactThreshold)
	_, usage := m.ctxMgr.Prepare(m.summary, m.apiHistory())
	m.usage = usage
}

// answerPendingQuestionBeforeQuit registra la cancelación en el historial
// antes de persistir la sesión, sin relanzar el turno.
func (m *Model) answerPendingQuestionBeforeQuit() {
	qm := m.modal.question
	m.modal.kind = modalNone
	m.modal.question = questionModal{}
	content := agent.FormatAnswer(qm.question, agent.Answer{CallID: qm.callID, Cancelled: true})
	m.history = append(m.history, chatMessage{
		Role:    ai.RoleUser,
		Content: content,
		Display: "↳ pregunta cancelada al salir",
	})
	m.agent.active = false
	m.agent.messages = nil
	m.agent.pending = nil
	m.agent.requested = false
	m.sessionDirty = true
}

// busy indica que hay trabajo del modelo en curso cuyo turno no debe
// entrelazarse con un mensaje nuevo.
func (m Model) busy() bool {
	return m.streaming || m.waiting || m.swarm.running || m.modal.kind == modalDiff || m.modal.kind == modalQuestion
}

func (m Model) configured() bool {
	return m.client != nil && m.client.Ready() && m.cfg.Configured()
}

func (m *Model) layout() {
	width := m.width
	height := m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}

	m.innerWidth = max(20, width-2)
	m.innerHeight = max(10, height-2)

	const headerHeight = 2
	const inputHeight = 4
	const statusHeight = 1

	tabBarHeight := 0
	if m.swarm.active() {
		tabBarHeight = 1
	}
	m.mainHeight = m.innerHeight - headerHeight - inputHeight - statusHeight - tabBarHeight
	if m.mainHeight < 3 {
		m.mainHeight = 3
	}

	m.effectiveSidebar = m.sidebarVisible && m.innerWidth >= 84
	m.sidebarWidth = 0
	if m.effectiveSidebar {
		m.sidebarWidth = 34
	}

	conversationWidth := m.innerWidth
	if m.effectiveSidebar {
		conversationWidth = m.innerWidth - m.sidebarWidth - 1
	}
	if conversationWidth < 24 {
		m.effectiveSidebar = false
		m.sidebarWidth = 0
		conversationWidth = m.innerWidth
	}
	m.conversationWidth = conversationWidth

	m.viewport.Width = m.conversationWidth
	m.viewport.Height = m.mainHeight
	m.textarea.SetWidth(m.innerWidth)
	m.textarea.SetHeight(2)
	m.renderer = newRenderer(max(20, m.conversationWidth-4))

	m.syncContextFileSizes()
	m.rebuildHistory()
	m.refresh()
	m.resizeModals()
}

func (m *Model) rebuildHistory() {
	m.blocks = make([]string, 0, len(m.history))
	for _, message := range m.history {
		m.blocks = append(m.blocks, m.renderMessage(message))
	}
}

func (m *Model) refresh() {
	atBottom := m.viewport.AtBottom()
	m.viewport.SetContent(m.content())
	if atBottom {
		m.viewport.GotoBottom()
	}
}

func (m Model) content() string {
	parts := append([]string(nil), m.blocks...)
	if m.streaming || m.waiting {
		parts = append(parts, m.streamingBlock())
	}
	if len(m.notes) > 0 {
		parts = append(parts, m.notesView())
	}
	if len(parts) == 0 {
		return m.welcome()
	}
	return strings.Join(parts, "\n\n")
}

func (m Model) welcome() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("MKD"))
	b.WriteString("\n\n")

	if !m.configured() {
		b.WriteString(warnStyle.Render("Ningún modelo disponible actualmente."))
		b.WriteString("\n\n")
		b.WriteString(mutedStyle.Render("Ctrl+K configura proveedor y API key · Ctrl+M/F2 elige un modelo"))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("También puedes escribir /keys o /model"))
		return b.String()
	}

	provider, _ := m.cfg.Active()
	b.WriteString(mutedStyle.Render(fmt.Sprintf("%s · %s", valueOr(provider.Name, "Proveedor"), valueOr(provider.ActiveModel, "(sin modelo)"))))
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render("Escribe un mensaje, usa / para comandos o Ctrl+P para la paleta."))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Ctrl+M/F2 modelos · Ctrl+K keys · Ctrl+B sidebar · Ctrl+L limpiar"))
	return b.String()
}

func (m Model) renderMessage(message chatMessage) string {
	width := m.conversationWidth
	if width <= 0 {
		width = 80
	}
	contentWidth := max(20, width-4)

	if message.Role == ai.RoleUser {
		content := lipgloss.NewStyle().Width(contentWidth).Render(message.Display)
		box := userBoxStyle.Width(max(20, width-2))
		return box.Render(userHeader() + "\n" + content)
	}

	body := m.renderMarkdown(message.Display)
	box := assistantBoxStyle.Width(max(20, width-2))
	return box.Render(assistantHeader() + "\n" + body)
}

func (m Model) streamingBlock() string {
	width := m.conversationWidth
	if width <= 0 {
		width = 80
	}

	header := assistantHeader()
	switch {
	case m.waiting:
		header += " " + m.spinner.View() + " " + mutedStyle.Render("pensando…")
	case m.streaming:
		header += " " + mutedStyle.Render("▍ generando")
	}

	body := mutedStyle.Render("esperando respuesta…")
	if m.stream.Len() > 0 {
		body = m.renderMarkdown(m.stream.String() + "▍")
	}

	box := assistantBoxStyle.Width(max(20, width-2))
	return box.Render(header + "\n" + body)
}

func (m Model) notesView() string {
	lines := make([]string, 0, len(m.notes))
	for _, item := range m.notes {
		switch item.Kind {
		case noteError:
			lines = append(lines, errorStyle.Render(item.Text))
		case noteSuccess:
			lines = append(lines, successStyle.Render(item.Text))
		default:
			lines = append(lines, mutedStyle.Render(item.Text))
		}
	}
	return strings.Join(lines, "\n")
}

func userHeader() string {
	return titleStyle.Render("󰀄 Tú")
}

func assistantHeader() string {
	return titleCyanStyle.Render("󰚩 MKD")
}

func (m Model) renderMarkdown(value string) string {
	if m.renderer == nil || strings.TrimSpace(value) == "" {
		return value
	}
	out, err := m.renderer.Render(value)
	if err != nil {
		return value
	}
	return strings.Trim(out, "\n")
}

func newRenderer(width int) *glamour.TermRenderer {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(max(20, width)),
	)
	if err != nil {
		return nil
	}
	return renderer
}

func (m Model) workspaceView() string {
	width := m.width
	height := m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}

	// El aviso de confirmación (`✓ seleccionado: …`) se muestra justo encima
	// del input, en verde, durante unos segundos tras anclar algo.
	parts := []string{m.headerView(m.innerWidth)}
	if m.swarm.active() {
		parts = append(parts, m.tabBarView(m.innerWidth))
	}
	parts = append(parts, m.mainView())
	if banner := m.confirmationBanner(); banner != "" {
		parts = append(parts, banner)
	}
	parts = append(parts, m.textarea.View(), m.statusView(m.innerWidth))
	content := strings.Join(parts, "\n")

	content = fitLines(content, m.innerWidth, m.innerHeight)
	return frameStyle.Render(content)
}

func (m Model) headerView(width int) string {
	contentWidth := max(1, width-2)
	left := brandStyle.Render("▓▒░ MKD ░▒▓")
	title := oneLine(valueOr(m.session.Title, "Sesión Activa"), 40)
	center := mutedStyle.Render("󰚩 " + title)

	provider, ok := m.cfg.Active()
	providerName := "Sin configurar"
	model := "(sin modelo)"
	if ok {
		providerName = valueOr(provider.Name, "Proveedor")
		model = valueOr(provider.ActiveModel, "(sin modelo)")
	}

	dot := warnStyle.Render("○")
	if m.configured() {
		dot = successStyle.Render("●")
	}
	right := dot + " " + textStyle.Render(providerName) + " " +
		mutedStyle.Render("│") + " " + textStyle.Render(model)

	line := left + "  " + center
	if lipgloss.Width(line)+lipgloss.Width(right)+2 <= contentWidth {
		gap := contentWidth - lipgloss.Width(line) - lipgloss.Width(right)
		line += strings.Repeat(" ", gap) + right
	} else {
		line = left + "  " + right
	}
	line = ansi.Truncate(line, contentWidth, "…")
	if pad := contentWidth - lipgloss.Width(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}

	separator := mutedStyle.Render(strings.Repeat("─", width))
	return headerStyle.Width(width).Render(line) + "\n" + separator
}

func (m Model) mainView() string {
	conversation := fitLines(m.viewport.View(), m.conversationWidth, m.mainHeight)
	// Pestaña de un sub-agente: el panel central muestra su stream en vivo.
	if m.swarm.active() && m.viewTab > 0 {
		if content := m.swarmTabContent(m.conversationWidth, m.mainHeight); content != "" {
			conversation = fitLines(content, m.conversationWidth, m.mainHeight)
		}
	}
	if !m.effectiveSidebar {
		return conversation
	}

	side := m.sidebarView(m.sidebarWidth, m.mainHeight)
	main := lipgloss.JoinHorizontal(lipgloss.Top, conversation, " ", side)
	return fitLines(main, m.innerWidth, m.mainHeight)
}

// tabBarView pinta la barra de pestañas del inspector bajo el header.
func (m Model) tabBarView(width int) string {
	bar := m.swarmTabBar(width)
	if bar == "" {
		return ""
	}
	return headerStyle.Width(width).Render(bar)
}

func (m Model) statusView(width int) string {
	contentWidth := max(1, width-2)
	mode := "NORMAL"
	hints := "Ctrl+P Paleta │ Ctrl+T Agents │ Alt+S Swarm │ Ctrl+G Commit │ Ctrl+O MCP │ Esc Salir"

	switch {
	case m.modal.kind == modalQuestion:
		mode = "PREGUNTA"
		hints = "↑/↓ elegir · 1-6 opción · Enter responder · Esc cancelar"
	case m.modal.kind != modalNone:
		mode = "MODAL"
		hints = "Esc cerrar · atajos dentro del modal"
	case m.slashMenuVisible:
		mode = "SLASH"
		hints = "↑/↓ elegir · Tab completar · Enter ejecutar · Esc cerrar"
	case m.MentionPopupVisible():
		mode = "MENCIÓN"
		hints = "↑/↓ elegir · Tab entrar/completar · Enter anclar · Esc cerrar"
	case m.compacting:
		mode = "COMPACT"
		hints = "resumiendo conversación…"
	case m.swarm.running:
		mode = "SWARM"
		hints = "sub-agentes en paralelo · Alt+0..9 inspector · Esc cancelar"
	case m.streaming || m.waiting:
		if m.agent.requested {
			mode = "AGENTE"
			hints = "resolviendo herramientas · Esc cancelar"
		} else {
			mode = "STREAM"
			hints = "Esc cancelar generación · Ctrl+C salir"
		}
	}

	if mode == "NORMAL" {
		if contentWidth < 50 {
			hints = "Ctrl+P │ Ctrl+T Agents │ Alt+S Swarm │ Esc"
		} else if contentWidth < 84 {
			hints = "Ctrl+P Paleta │ Ctrl+T Agents │ Alt+S Swarm │ Esc"
		}
	}

	badge := lipgloss.NewStyle().
		Background(purple).
		Foreground(white).
		Bold(true).
		Padding(0, 1).
		Render(mode)

	agentBadge := mutedStyle.Render("⟨"+config.ModeLabel(m.cfg.AgentMode)+"⟩ ") + m.swarmStatusPill() + " "
	line := agentBadge + badge + " " + mutedStyle.Render(hints)
	if m.notice != "" {
		avail := max(4, contentWidth-lipgloss.Width(agentBadge)-lipgloss.Width(badge)-1)
		line = agentBadge + badge + " " + warnStyle.Render(oneLine(m.notice, avail))
	}

	// Aviso de actualización alineado a la derecha. Tiene prioridad sobre las
	// pistas: si no cabe se recortan las pistas, nunca el aviso (para que el
	// usuario se entere de que hay versión nueva).
	if m.updateNotice != "" {
		notice := successStyle.Render(m.updateNotice)
		noticeWidth := lipgloss.Width(notice)

		if noticeWidth+3 >= contentWidth {
			line = notice
		} else {
			// Deja hueco al aviso recortando la parte izquierda de la barra.
			prefix := ""
			if m.notice != "" {
				prefix = warnStyle.Render(oneLine(m.notice, max(4, contentWidth/3))) + " "
			} else {
				prefix = agentBadge + badge + " "
			}
			avail := contentWidth - lipgloss.Width(prefix) - noticeWidth - 1
			if avail > 8 {
				line = prefix + mutedStyle.Render(oneLine(hints, avail))
			} else {
				line = prefix
			}
			pad := contentWidth - lipgloss.Width(line) - noticeWidth
			if pad > 0 {
				line += strings.Repeat(" ", pad)
			}
			line += notice
		}
	}
	line = ansi.Truncate(line, contentWidth, "…")
	if pad := contentWidth - lipgloss.Width(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return statusBarStyle.Width(width).Render(line)
}
