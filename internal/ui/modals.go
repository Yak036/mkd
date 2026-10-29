package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/claude"
	"github.com/Yak036/mkd/internal/codex"
	"github.com/Yak036/mkd/internal/config"
	"github.com/Yak036/mkd/internal/storage"
)

type modalKind int

const (
	modalNone modalKind = iota
	modalProvider
	modalModels
	modalSystem
	modalPalette
	modalSessions
	modalBlocks
	modalDiff
	modalCommit
	modalMCP
	modalAgents
	modalQuestion
)

type modalState struct {
	kind     modalKind
	provider providerModal
	models   modelsModal
	system   systemModal
	palette  paletteModal
	sessions sessionsModal
	blocks   blocksModal
	diff     diffModal
	commit   gitCommitModal
	mcp      mcpModal
	agents   agentModal
	question questionModal
}

// ---------------------------------------------------------------------------
// Provider / API keys
// ---------------------------------------------------------------------------

type providerModal struct {
	keys    []string
	index   int
	focus   int // 0 proveedor, 1 base URL, 2 API key
	baseURL textinput.Model
	apiKey  textinput.Model
	showKey bool
	err     string
	status  string // Mensaje informativo o de conexión exitosa
	testing bool   // Probando conexión contra el endpoint
}

func newProviderModal(cfg config.Config) providerModal {
	keys := cfg.ProviderKeys()
	index := indexOf(keys, cfg.ActiveProvider)
	if index < 0 {
		index = 0
	}

	baseURL := newInput("https://api.deepinfra.com/v1/openai")
	apiKey := newInput("sk-… (vacío para Ollama local)")
	apiKey.EchoMode = textinput.EchoPassword
	apiKey.EchoCharacter = '•'

	status := ""
	if len(keys) > 0 {
		p := cfg.Providers[keys[index]]
		baseURL.SetValue(p.BaseURL)
		apiKey.SetValue(p.APIKey)
		if p.Connected {
			status = fmt.Sprintf("● Conectado exitosamente (%d modelos)", len(p.Models))
		}
	}

	focus := 0
	if strings.TrimSpace(apiKey.Value()) == "" {
		focus = 2
	}

	return providerModal{
		keys:    keys,
		index:   index,
		focus:   focus,
		baseURL: baseURL,
		apiKey:  apiKey,
		status:  status,
	}
}

func (m Model) openProviderModal() (Model, tea.Cmd) {
	m.hideSlashMenu()
	m.modal.kind = modalProvider
	m.modal.provider = newProviderModal(m.cfg)
	m.textarea.Blur()
	m.resizeModals()
	return m, m.focusProviderField(m.modal.provider.focus)
}

func (m Model) openProviderCodexModal() (Model, tea.Cmd) {
	m.hideSlashMenu()
	m.modal.kind = modalProvider
	m.modal.provider = newProviderModal(m.cfg)
	if idx := indexOf(m.modal.provider.keys, "codex"); idx >= 0 {
		m.modal.provider.index = idx
		p := m.cfg.Providers["codex"]
		m.modal.provider.baseURL.SetValue(p.BaseURL)
		m.modal.provider.apiKey.SetValue(p.APIKey)
	}
	m.textarea.Blur()
	m.resizeModals()
	return m, m.focusProviderField(m.modal.provider.focus)
}

type codexAuthDoneMsg struct {
	bundle codex.TokenBundle
	err    error
}

type claudeAuthDoneMsg struct {
	bundle claude.TokenBundle
	err    error
}

func (m *Model) focusProviderField(index int) tea.Cmd {
	p := &m.modal.provider
	p.focus = index
	p.baseURL.Blur()
	p.apiKey.Blur()

	switch index {
	case 1:
		return p.baseURL.Focus()
	case 2:
		return p.apiKey.Focus()
	default:
		return nil
	}
}

func (m Model) updateProviderModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	p := &m.modal.provider

	switch {
	case key.Matches(msg, keyQuit):
		return m.quit()
	case key.Matches(msg, keyEsc):
		return m.closeModal()
	case msg.String() == "tab" || msg.String() == "down":
		return m, m.focusProviderField((p.focus + 1) % 3)
	case msg.String() == "shift+tab" || msg.String() == "up":
		return m, m.focusProviderField((p.focus + 2) % 3)
	case msg.String() == "ctrl+u":
		p.showKey = !p.showKey
		if p.showKey {
			p.apiKey.EchoMode = textinput.EchoNormal
			p.apiKey.EchoCharacter = 0
		} else {
			p.apiKey.EchoMode = textinput.EchoPassword
			p.apiKey.EchoCharacter = '•'
		}
		return m, nil
	case key.Matches(msg, keyEnter):
		return m.testAndConnectProvider()
	case msg.String() == "ctrl+i":
		key := ""
		if len(p.keys) > 0 {
			key = p.keys[p.index]
		}
		if key == "codex" {
			bundle, err := codex.LoadLocalAuth()
			if err != nil {
				p.err = "no se pudo leer ~/.codex/auth.json: " + err.Error()
				return m, nil
			}
			prov := m.cfg.Providers[key]
			prov.APIKey = bundle.AccessToken
			prov.RefreshToken = bundle.RefreshToken
			prov.AccountID = bundle.AccountID
			prov.TokenExpires = bundle.ExpiresAt
			m.cfg.Providers[key] = prov
			p.apiKey.SetValue(bundle.AccessToken)
			p.err = ""
			p.testing = true
			p.status = "Verificando conexión con Codex…"
			m.notice = "✓ Sesión de Codex importada de ~/.codex/auth.json"
			_ = m.persistConfig()
			return m, tea.Batch(testConnectionCmd(m.cfg, key), m.spinner.Tick)
		}
		if key == "claude" {
			bundle, err := claude.LoadLocalAuth()
			if err != nil {
				p.err = "no se pudieron leer credenciales de Claude: " + err.Error()
				return m, nil
			}
			prov := m.cfg.Providers[key]
			prov.APIKey = bundle.AccessToken
			prov.RefreshToken = bundle.RefreshToken
			prov.TokenExpires = bundle.ExpiresAt
			m.cfg.Providers[key] = prov
			p.apiKey.SetValue(bundle.AccessToken)
			p.err = ""
			p.testing = true
			p.status = "Verificando conexión con Claude…"
			m.notice = "✓ Credenciales de Claude importadas exitosamente"
			_ = m.persistConfig()
			return m, tea.Batch(testConnectionCmd(m.cfg, key), m.spinner.Tick)
		}
	case msg.String() == "ctrl+o":
		key := ""
		if len(p.keys) > 0 {
			key = p.keys[p.index]
		}
		if key == "codex" {
			p.err = "Iniciando autenticación en navegador (puerto 1455)…"
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				bundle, err := codex.LoginPKCE(ctx, nil)
				return codexAuthDoneMsg{bundle: bundle, err: err}
			}
		}
		if key == "claude" {
			p.err = "Iniciando autenticación con Claude en navegador (puerto 54321)…"
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				bundle, err := claude.LoginPKCE(ctx, nil)
				return claudeAuthDoneMsg{bundle: bundle, err: err}
			}
		}
	case p.focus == 0 && (msg.String() == "left" || msg.String() == "right"):
		delta := -1
		if msg.String() == "right" {
			delta = 1
		}
		return m.switchProviderModal(delta)
	}

	var cmd tea.Cmd
	switch p.focus {
	case 1:
		p.baseURL, cmd = p.baseURL.Update(msg)
	case 2:
		p.apiKey, cmd = p.apiKey.Update(msg)
	}
	return m, cmd
}

func (m Model) switchProviderModal(delta int) (Model, tea.Cmd) {
	p := &m.modal.provider
	if len(p.keys) == 0 {
		return m, nil
	}
	m.syncProviderForm()

	p.index = (p.index + delta + len(p.keys)) % len(p.keys)
	key := p.keys[p.index]
	provider := m.cfg.Providers[key]
	p.baseURL.SetValue(provider.BaseURL)
	p.apiKey.SetValue(provider.APIKey)
	p.showKey = false
	p.apiKey.EchoMode = textinput.EchoPassword
	p.apiKey.EchoCharacter = '•'
	p.err = ""
	p.testing = false
	if provider.Connected {
		p.status = fmt.Sprintf("● Conectado exitosamente (%d modelos)", len(provider.Models))
	} else {
		p.status = ""
	}
	return m, m.focusProviderField(p.focus)
}

func (m *Model) syncProviderForm() {
	p := &m.modal.provider
	if len(p.keys) == 0 {
		return
	}
	key := p.keys[p.index]
	provider := m.cfg.Providers[key]
	provider.BaseURL = strings.TrimSpace(p.baseURL.Value())
	provider.APIKey = strings.TrimSpace(p.apiKey.Value())
	m.cfg.Providers[key] = provider
}

// providerConnectionDoneMsg representa el resultado de probar la conexión en /connect.
type providerConnectionDoneMsg struct {
	providerKey string
	models      []string
	err         error
}

// testConnectionCmd ejecuta la verificación real de conexión contra el proveedor.
func testConnectionCmd(cfg config.Config, providerKey string) tea.Cmd {
	return func() tea.Msg {
		cfg.ActiveProvider = providerKey
		cfg.Normalize()
		client, err := ai.New(cfg)
		if err != nil {
			return providerConnectionDoneMsg{providerKey: providerKey, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		models, err := client.FetchAvailableModels(ctx)
		return providerConnectionDoneMsg{providerKey: providerKey, models: models, err: err}
	}
}

// testAndConnectProvider valida los datos del formulario e inicia la prueba de conexión.
func (m Model) testAndConnectProvider() (Model, tea.Cmd) {
	p := &m.modal.provider
	if p.testing {
		return m, nil
	}
	if len(p.keys) == 0 {
		p.err = "no hay proveedores configurados"
		return m, nil
	}
	m.syncProviderForm()
	key := p.keys[p.index]
	p.testing = true
	p.err = ""
	p.status = fmt.Sprintf("Probando conexión con %s…", config.DisplayName(key))
	_ = m.persistConfig()
	return m, tea.Batch(testConnectionCmd(m.cfg, key), m.spinner.Tick)
}

// handleProviderConnectionDone procesa la respuesta del test de conexión.
func (m Model) handleProviderConnectionDone(msg providerConnectionDoneMsg) (Model, tea.Cmd) {
	if m.modal.kind != modalProvider {
		return m, nil
	}
	p := &m.modal.provider
	p.testing = false

	prov := m.cfg.Providers[msg.providerKey]
	if msg.err != nil {
		p.err = "✗ Falló la conexión: " + msg.err.Error()
		p.status = ""
		prov.Connected = false
		m.cfg.Providers[msg.providerKey] = prov
		_ = m.persistConfig()
		// No se avanza a /model si la conexión no tuvo éxito
		return m, nil
	}

	prov.Connected = true
	if len(msg.models) > 0 {
		prov.Models = msg.models
		if prov.ActiveModel == "" || !containsString(prov.Models, prov.ActiveModel) {
			prov.ActiveModel = msg.models[0]
		}
		if cache, err := storage.SaveModelsCache(m.modelsCache, msg.providerKey, msg.models); err == nil {
			m.modelsCache = cache
		}
	}
	m.cfg.Providers[msg.providerKey] = prov
	m.cfg.ActiveProvider = msg.providerKey
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		p.err = err.Error()
		return m, nil
	}

	p.err = ""
	p.status = fmt.Sprintf("✓ ¡Conexión exitosa con %s! (%d modelos sincronizados)", prov.Name, len(msg.models))
	m.notice = "✓ Conectado exitosamente con " + prov.Name
	return m, nil
}

func (m Model) providerModalView() string {
	p := m.modal.provider
	key := ""
	provider := config.ProviderConfig{}
	if len(p.keys) > 0 {
		key = p.keys[p.index]
		provider = m.cfg.Providers[key]
	}
	if provider.Name == "" {
		provider.Name = config.DisplayName(key)
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("󰌆 Conectar Proveedor de IA (/connect)"))
	b.WriteString("\n\n")

	selectorLabel := labelStyle
	if p.focus == 0 {
		selectorLabel = labelFocusStyle
	}
	selector := fmt.Sprintf("◀ [ %s ] ▶", provider.Name)
	if p.focus == 0 {
		selector = titleCyanStyle.Render(selector)
	}
	b.WriteString(selectorLabel.Render("Proveedor:"))
	b.WriteString(" ")
	b.WriteString(selector)
	b.WriteString("  ")
	if provider.Connected {
		b.WriteString(successStyle.Render("● Conectado exitosamente"))
	} else {
		b.WriteString(mutedStyle.Render("○ No conectado"))
	}
	b.WriteString("\n\n")

	baseLabel := labelStyle
	if p.focus == 1 {
		baseLabel = labelFocusStyle
	}
	b.WriteString(baseLabel.Render("Base URL:"))
	b.WriteString(" ")
	b.WriteString(p.baseURL.View())
	b.WriteString("\n")

	keyLabel := labelStyle
	if p.focus == 2 {
		keyLabel = labelFocusStyle
	}
	b.WriteString(keyLabel.Render("API Key:"))
	b.WriteString(" ")
	b.WriteString(p.apiKey.View())
	b.WriteString("\n")

	if key == "codex" {
		b.WriteString("\n")
		if provider.APIKey != "" || provider.RefreshToken != "" {
			b.WriteString(successStyle.Render("✓ Suscripción activa configurada"))
			if provider.AccountID != "" {
				b.WriteString(mutedStyle.Render(fmt.Sprintf(" (Cuenta: %s)", provider.AccountID)))
			}
		} else if local, err := codex.LoadLocalAuth(); err == nil {
			b.WriteString(titleCyanStyle.Render("󰌆 Sesión detectada en ~/.codex/auth.json (pulsa Ctrl+I para importarla)"))
			if local.AccountID != "" {
				b.WriteString(mutedStyle.Render(fmt.Sprintf(" [%s]", local.AccountID)))
			}
		} else {
			b.WriteString(titleCyanStyle.Render("󰌆 Pulsa Ctrl+O para iniciar sesión con ChatGPT en el navegador"))
		}
		b.WriteString("\n")
	}

	if key == "claude" {
		b.WriteString("\n")
		if provider.APIKey != "" || provider.RefreshToken != "" {
			b.WriteString(successStyle.Render("✓ Suscripción / Clave de Claude configurada"))
		} else if _, err := claude.LoadLocalAuth(); err == nil {
			b.WriteString(titleCyanStyle.Render("󰌆 Credenciales de Claude detectadas (pulsa Ctrl+I para importarlas)"))
		} else {
			b.WriteString(titleCyanStyle.Render("󰌆 Pulsa Ctrl+O para iniciar sesión con Claude en el navegador"))
		}
		b.WriteString("\n")
	}

	if p.testing {
		b.WriteString("\n")
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(titleCyanStyle.Render(p.status))
		b.WriteString("\n")
	} else if p.status != "" {
		b.WriteString("\n")
		b.WriteString(successStyle.Render(p.status))
		b.WriteString("\n")
	}

	if p.err != "" {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(p.err))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if key == "codex" {
		b.WriteString(titleCyanStyle.Render("Ctrl+O login navegador · Ctrl+I importar ~/.codex/auth.json"))
		b.WriteString("\n")
	}
	if key == "claude" {
		b.WriteString(titleCyanStyle.Render("Ctrl+O login navegador · Ctrl+I importar sesión de Claude"))
		b.WriteString("\n")
	}
	b.WriteString(mutedStyle.Render("Enter probar y conectar · Tab/↑↓ campo · ◀/▶ proveedor · Ctrl+U ver/ocultar key"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Esc cerrar"))
	return m.modalBox(b.String())
}

// ---------------------------------------------------------------------------
// Gestor multi-modelo
// ---------------------------------------------------------------------------

type modelsFetchedMsg struct {
	providerKey string
	models      []string
	err         error
}

type modelsModal struct {
	keys     []string
	index    int
	filter   textinput.Model
	all      []string
	visible  []string
	selected int
	scroll   int
	loading  bool
	err      string
	status   string
}

func newModelsModal(cfg config.Config) modelsModal {
	keys := cfg.ProviderKeys()
	index := indexOf(keys, cfg.ActiveProvider)
	if index < 0 {
		index = 0
	}

	filter := newInput("Filtrar o escribir nuevo ID…")
	modal := modelsModal{
		keys:   keys,
		index:  index,
		filter: filter,
	}
	modal.loadProvider(cfg)
	return modal
}

func (mm *modelsModal) loadProvider(cfg config.Config) {
	mm.all = nil
	if len(mm.keys) > 0 {
		mm.all = append([]string(nil), cfg.Providers[mm.keys[mm.index]].Models...)
	}
	mm.filter.SetValue("")
	mm.applyFilter()
}

func (mm *modelsModal) applyFilter() {
	query := strings.ToLower(strings.TrimSpace(mm.filter.Value()))
	mm.visible = mm.visible[:0]
	for _, model := range mm.all {
		if query == "" || strings.Contains(strings.ToLower(model), query) {
			mm.visible = append(mm.visible, model)
		}
	}
	mm.selected = 0
	mm.scroll = 0
}

func (mm *modelsModal) selectedModel() string {
	if len(mm.visible) == 0 {
		return ""
	}
	if mm.selected < 0 {
		mm.selected = 0
	}
	if mm.selected >= len(mm.visible) {
		mm.selected = len(mm.visible) - 1
	}
	return mm.visible[mm.selected]
}

func (mm *modelsModal) moveSelection(delta, rows int) {
	if len(mm.visible) == 0 {
		return
	}
	mm.selected += delta
	if mm.selected < 0 {
		mm.selected = 0
	}
	if mm.selected >= len(mm.visible) {
		mm.selected = len(mm.visible) - 1
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

func (m Model) openModelsModal() (Model, tea.Cmd) {
	m.hideSlashMenu()
	// Caché local: los modelos se muestran al instante y solo se refrescan
	// desde la API cuando el usuario pulsa Ctrl+F.
	cache, err := storage.LoadModelsCache()
	if err == nil {
		m.modelsCache = cache
	}
	m.modal.kind = modalModels
	m.modal.models = newModelsModal(m.cfg)
	for _, key := range m.modal.models.keys {
		models := m.modelsCache.Models(key)
		if len(models) == 0 {
			continue
		}
		provider := m.cfg.Providers[key]
		for _, model := range models {
			if !containsString(provider.Models, model) {
				provider.Models = append(provider.Models, model)
			}
		}
		m.cfg.Providers[key] = provider
	}
	if len(m.modal.models.keys) > 0 {
		key := m.modal.models.keys[m.modal.models.index]
		if cached := m.modelsCache.Models(key); len(cached) > 0 {
			m.modal.models.status = fmt.Sprintf("%d modelos desde caché · Ctrl+F para sincronizar", len(cached))
		}
	}
	m.modal.models.loadProvider(m.cfg)

	// Sin caché para el proveedor activo (p. ej. API key recién configurada):
	// sincronizamos automáticamente para no obligar a recordar Ctrl+F.
	var autoSync tea.Cmd
	if len(m.modal.models.keys) > 0 {
		key := m.modal.models.keys[m.modal.models.index]
		provider := m.cfg.Providers[key]
		hasKey := strings.TrimSpace(provider.APIKey) != "" || config.IsLocal(provider.BaseURL)
		hasCache := len(m.modelsCache.Models(key)) > 0
		if hasKey && !hasCache {
			mm := m.modal.models
			mm.loading = true
			mm.status = "Sincronizando modelos con la API…"
			m.modal.models = mm
			autoSync = tea.Batch(fetchModelsCmd(m.cfg, key), m.spinner.Tick)
		}
	}

	m.textarea.Blur()
	m.resizeModals()
	return m, tea.Batch(m.modal.models.filter.Focus(), autoSync)
}

func (m Model) updateModelsModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	mm := &m.modal.models
	rows := m.modelRows()

	switch {
	case key.Matches(msg, keyQuit):
		return m.quit()
	case key.Matches(msg, keyEsc):
		return m.closeModal()
	case msg.String() == "ctrl+f":
		return m.startFetchModels()
	case msg.String() == "ctrl+a":
		return m.addModelFromFilter()
	case msg.String() == "ctrl+d":
		return m.deleteSelectedModel()
	case msg.String() == "ctrl+left" || msg.String() == "alt+left":
		return m.switchModelsProvider(-1)
	case msg.String() == "ctrl+right" || msg.String() == "alt+right":
		return m.switchModelsProvider(1)
	case msg.String() == "up":
		mm.moveSelection(-1, rows)
		return m, nil
	case msg.String() == "down":
		mm.moveSelection(1, rows)
		return m, nil
	case msg.String() == "pgup":
		mm.moveSelection(-rows, rows)
		return m, nil
	case msg.String() == "pgdown":
		mm.moveSelection(rows, rows)
		return m, nil
	case key.Matches(msg, keyEnter):
		return m.selectCurrentModel()
	}

	var cmd tea.Cmd
	mm.filter, cmd = mm.filter.Update(msg)
	mm.applyFilter()
	return m, cmd
}

func (m Model) switchModelsProvider(delta int) (Model, tea.Cmd) {
	mm := &m.modal.models
	if len(mm.keys) == 0 {
		return m, nil
	}
	mm.index = (mm.index + delta + len(mm.keys)) % len(mm.keys)
	m.cfg.ActiveProvider = mm.keys[mm.index]
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		mm.err = err.Error()
		return m, nil
	}
	mm.err = ""
	mm.status = ""
	mm.loadProvider(m.cfg)
	return m, nil
}

func (m Model) addModelFromFilter() (Model, tea.Cmd) {
	mm := &m.modal.models
	id := strings.TrimSpace(mm.filter.Value())
	if id == "" {
		mm.status = "Escribe un ID en el filtro antes de agregarlo."
		return m, nil
	}
	if len(mm.keys) == 0 {
		mm.err = "no hay proveedor activo"
		return m, nil
	}

	key := mm.keys[mm.index]
	provider := m.cfg.Providers[key]
	if !containsString(provider.Models, id) {
		provider.Models = append(provider.Models, id)
	}
	provider.ActiveModel = id
	m.cfg.Providers[key] = provider
	m.cfg.ActiveProvider = key
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		mm.err = err.Error()
		return m, nil
	}
	mm.err = ""
	mm.all = append([]string(nil), m.cfg.Providers[key].Models...)
	mm.filter.SetValue("")
	mm.applyFilter()
	mm.status = "Modelo agregado y activado: " + id
	return m, nil
}

func (m Model) deleteSelectedModel() (Model, tea.Cmd) {
	mm := &m.modal.models
	id := mm.selectedModel()
	if id == "" {
		mm.status = "No hay modelo seleccionado para eliminar."
		return m, nil
	}
	if len(mm.keys) == 0 {
		return m, nil
	}

	key := mm.keys[mm.index]
	provider := m.cfg.Providers[key]
	models := make([]string, 0, len(provider.Models))
	for _, model := range provider.Models {
		if model != id {
			models = append(models, model)
		}
	}
	provider.Models = models
	if provider.ActiveModel == id {
		provider.ActiveModel = ""
		if len(models) > 0 {
			provider.ActiveModel = models[0]
		}
	}
	m.cfg.Providers[key] = provider
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		mm.err = err.Error()
		return m, nil
	}
	mm.err = ""
	mm.all = append([]string(nil), m.cfg.Providers[key].Models...)
	mm.applyFilter()
	mm.status = "Modelo eliminado: " + id
	return m, nil
}

func (m Model) selectCurrentModel() (Model, tea.Cmd) {
	mm := &m.modal.models
	id := mm.selectedModel()
	if id == "" {
		mm.status = "Sin coincidencias. Ctrl+A para agregar el ID escrito."
		return m, nil
	}
	if len(mm.keys) == 0 {
		return m, nil
	}

	key := mm.keys[mm.index]
	provider := m.cfg.Providers[key]
	provider.ActiveModel = id
	if !containsString(provider.Models, id) {
		provider.Models = append(provider.Models, id)
	}
	m.cfg.Providers[key] = provider
	m.cfg.ActiveProvider = key
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		mm.err = err.Error()
		return m, nil
	}
	m.modal.kind = modalNone
	m.notice = "Modelo activo: " + id
	m.refresh()
	return m, m.textarea.Focus()
}

func (m Model) startFetchModels() (Model, tea.Cmd) {
	mm := &m.modal.models
	if mm.loading || len(mm.keys) == 0 {
		return m, nil
	}
	key := mm.keys[mm.index]
	mm.loading = true
	mm.err = ""
	mm.status = "Sincronizando con /v1/models…"
	return m, tea.Batch(fetchModelsCmd(m.cfg, key), m.spinner.Tick)
}

func fetchModelsCmd(cfg config.Config, providerKey string) tea.Cmd {
	return func() tea.Msg {
		cfg.ActiveProvider = providerKey
		cfg.Normalize()
		client, err := ai.New(cfg)
		if err != nil {
			return modelsFetchedMsg{providerKey: providerKey, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		models, err := client.FetchAvailableModels(ctx)
		return modelsFetchedMsg{providerKey: providerKey, models: models, err: err}
	}
}

func (m Model) handleModelsFetched(msg modelsFetchedMsg) (Model, tea.Cmd) {
	if m.modal.kind != modalModels || len(m.modal.models.keys) == 0 {
		return m, nil
	}
	mm := &m.modal.models
	current := mm.keys[mm.index]
	if msg.providerKey != current {
		return m, nil
	}

	mm.loading = false
	if msg.err != nil {
		mm.err = msg.err.Error()
		mm.status = ""
		return m, nil
	}
	if len(msg.models) == 0 {
		mm.status = "La API no devolvió modelos; se conservan los guardados."
		return m, nil
	}

	// Solo Ctrl+F reescribe el caché en disco.
	if cache, err := storage.SaveModelsCache(m.modelsCache, msg.providerKey, msg.models); err == nil {
		m.modelsCache = cache
	}

	provider := m.cfg.Providers[current]
	for _, model := range msg.models {
		if !containsString(provider.Models, model) {
			provider.Models = append(provider.Models, model)
		}
	}
	if provider.ActiveModel == "" {
		provider.ActiveModel = msg.models[0]
	}
	m.cfg.Providers[current] = provider
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		mm.err = err.Error()
		return m, nil
	}
	mm.err = ""
	mm.all = append([]string(nil), m.cfg.Providers[current].Models...)
	mm.applyFilter()
	mm.status = fmt.Sprintf("Sincronizados %d modelos desde la API.", len(msg.models))
	return m, nil
}

func (m Model) handleCodexAuthDone(msg codexAuthDoneMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		m.modal.provider.err = fmt.Sprintf("Error de autenticación: %v", msg.err)
		m.modal.provider.testing = false
		return m, nil
	}
	key := "codex"
	p := m.cfg.Providers[key]
	p.APIKey = msg.bundle.AccessToken
	p.RefreshToken = msg.bundle.RefreshToken
	p.AccountID = msg.bundle.AccountID
	p.TokenExpires = msg.bundle.ExpiresAt
	m.cfg.Providers[key] = p
	m.modal.provider.apiKey.SetValue(msg.bundle.AccessToken)
	m.modal.provider.err = ""
	m.modal.provider.testing = true
	m.modal.provider.status = "Verificando conexión con Codex…"
	_ = codex.SaveLocalAuth(msg.bundle)
	_ = m.persistConfig()
	m.notice = "✓ Autenticación de Codex completada exitosamente."
	return m, tea.Batch(testConnectionCmd(m.cfg, key), m.spinner.Tick)
}

func (m Model) handleClaudeAuthDone(msg claudeAuthDoneMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		m.modal.provider.err = fmt.Sprintf("Error de autenticación con Claude: %v", msg.err)
		m.modal.provider.testing = false
		return m, nil
	}
	key := "claude"
	p := m.cfg.Providers[key]
	p.APIKey = msg.bundle.AccessToken
	p.RefreshToken = msg.bundle.RefreshToken
	p.TokenExpires = msg.bundle.ExpiresAt
	m.cfg.Providers[key] = p
	m.modal.provider.apiKey.SetValue(msg.bundle.AccessToken)
	m.modal.provider.err = ""
	m.modal.provider.testing = true
	m.modal.provider.status = "Verificando conexión con Claude…"
	_ = claude.SaveLocalAuth(msg.bundle)
	_ = m.persistConfig()
	m.notice = "✓ Autenticación de Claude completada exitosamente."
	return m, tea.Batch(testConnectionCmd(m.cfg, key), m.spinner.Tick)
}

func (m Model) modelRows() int {
	return min(10, max(3, m.modalContentHeight()-14))
}

func (m Model) modelsModalView() string {
	mm := m.modal.models
	key := ""
	provider := config.ProviderConfig{}
	if len(mm.keys) > 0 {
		key = mm.keys[mm.index]
		provider = m.cfg.Providers[key]
	}
	if provider.Name == "" {
		provider.Name = config.DisplayName(key)
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("󰚩 Modelos de %s", provider.Name)))
	b.WriteString(mutedStyle.Render("  (misma API key / URL)"))
	b.WriteString("\n\n")

	b.WriteString(mutedStyle.Render("Proveedor:  "))
	b.WriteString(titleCyanStyle.Render("◀ " + provider.Name + " ▶"))
	b.WriteString(mutedStyle.Render("   Ctrl+←/→"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("🔍 Filtrar:  "))
	b.WriteString(mm.filter.View())
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", m.modalContentWidth())))
	b.WriteString("\n")

	switch {
	case mm.loading:
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(mutedStyle.Render("Sincronizando modelos…"))
		b.WriteString("\n")
	case len(mm.all) == 0:
		b.WriteString(warnStyle.Render("Ningún modelo disponible actualmente."))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("Escribe un ID y pulsa Ctrl+A, o Ctrl+F para sincronizar."))
		b.WriteString("\n")
	case len(mm.visible) == 0:
		b.WriteString(warnStyle.Render("Sin coincidencias para el filtro."))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("Borra el filtro o pulsa Ctrl+A para agregar el ID escrito."))
		b.WriteString("\n")
	default:
		rows := m.modelRows()
		if mm.scroll > len(mm.visible)-1 {
			mm.scroll = max(0, len(mm.visible)-rows)
		}
		end := min(len(mm.visible), mm.scroll+rows)
		for i := mm.scroll; i < end; i++ {
			model := mm.visible[i]
			marker := "○"
			markerStyle := offDotStyle
			if model == provider.ActiveModel {
				marker = "●"
				markerStyle = activeDotStyle
			}
			row := fmt.Sprintf("%s %s", markerStyle.Render(marker), model)
			if i == mm.selected {
				row = selectedRowStyle.Render(ansi.Truncate(" "+row, m.modalContentWidth(), "…"))
			} else if model == provider.ActiveModel {
				row = successStyle.Render(row)
			}
			b.WriteString(row)
			if model == provider.ActiveModel {
				b.WriteString(successStyle.Render("  [ACTIVO]"))
			}
			b.WriteString("\n")
		}
		b.WriteString(mutedStyle.Render(fmt.Sprintf("%d de %d modelos", len(mm.visible), len(mm.all))))
		b.WriteString("\n")
	}

	if mm.err != "" {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(mm.err))
		b.WriteString("\n")
	}
	if mm.status != "" {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(mm.status))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Enter seleccionar · Ctrl+A agregar ID · Ctrl+D eliminar"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Ctrl+F sincronizar API · Ctrl+←/→ proveedor · Esc cerrar"))
	return m.modalBox(b.String())
}

// ---------------------------------------------------------------------------
// System prompt
// ---------------------------------------------------------------------------

type systemModal struct {
	textarea textarea.Model
	err      string
	// pendingSkill, si no está vacío, convierte el editor en creador de skill:
	// Ctrl+S escribe <skills_dir>/<nombre>/SKILL.md en vez del system prompt.
	pendingSkill string
}

func newSystemModal(cfg config.Config) systemModal {
	ta := textarea.New()
	ta.Placeholder = "System prompt…"
	ta.Prompt = "› "
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.SetHeight(8)
	ta.SetWidth(60)
	ta.SetValue(cfg.SystemPrompt)
	ta.FocusedStyle.Base = lipgloss.NewStyle().Background(surface)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(surface)
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(purple).Background(surface)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(muted).Background(surface)
	ta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(text).Background(surface)
	ta.FocusedStyle.EndOfBuffer = lipgloss.NewStyle().Background(surface)
	ta.Focus()
	return systemModal{textarea: ta}
}

func (m Model) openSystemModal() (Model, tea.Cmd) {
	m.hideSlashMenu()
	m.modal.kind = modalSystem
	m.modal.system = newSystemModal(m.cfg)
	m.textarea.Blur()
	m.resizeModals()
	return m, m.modal.system.textarea.Focus()
}

func (m Model) updateSystemModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keyQuit):
		return m.quit()
	case key.Matches(msg, keyEsc):
		return m.closeModal()
	case msg.String() == "ctrl+s":
		return m.saveSystemModal()
	}

	var cmd tea.Cmd
	m.modal.system.textarea, cmd = m.modal.system.textarea.Update(msg)
	return m, cmd
}

func (m Model) saveSystemModal() (Model, tea.Cmd) {
	if name := m.modal.system.pendingSkill; name != "" {
		return m.saveSkillFromEditor(name, m.modal.system.textarea.Value())
	}
	m.cfg.SystemPrompt = strings.TrimSpace(m.modal.system.textarea.Value())
	if err := m.persistConfig(); err != nil {
		m.modal.system.err = err.Error()
		return m, nil
	}
	m.modal.kind = modalNone
	m.notice = "System prompt actualizado."
	m.refresh()
	return m, m.textarea.Focus()
}

func (m Model) systemModalView() string {
	var b strings.Builder
	if name := m.modal.system.pendingSkill; name != "" {
		b.WriteString(titleStyle.Render("✦ Nueva Skill: @" + name))
		b.WriteString(mutedStyle.Render("  · se guardará como SKILL.md"))
	} else {
		b.WriteString(titleStyle.Render("󰈙 Editar System Prompt"))
	}
	b.WriteString("\n\n")
	b.WriteString(m.modal.system.textarea.View())
	if m.modal.system.err != "" {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(m.modal.system.err))
	}
	b.WriteString("\n\n")
	if m.modal.system.pendingSkill != "" {
		b.WriteString(mutedStyle.Render("Ctrl+S crear skill · Esc cancelar"))
	} else {
		b.WriteString(mutedStyle.Render("Ctrl+S guardar · Esc cancelar"))
	}
	return m.modalBox(b.String())
}

// ---------------------------------------------------------------------------
// Paleta de comandos
// ---------------------------------------------------------------------------

type paletteAction struct {
	ID          string
	Name        string
	Description string
}

var paletteActions = []paletteAction{
	{ID: "model", Name: "Cambiar / agregar modelo", Description: "Ctrl+M · modelo · gestor multi-modelo"},
	{ID: "provider", Name: "Configurar proveedor / API key", Description: "Ctrl+K · keys · credenciales"},
	{ID: "codex", Name: "Autenticación Codex (ChatGPT)", Description: "OAuth · suscripción · ChatGPT Plus/Pro"},
	{ID: "fetch", Name: "Sincronizar modelos desde API", Description: "GET /v1/models · sincronizar"},
	{ID: "add", Name: "Adjuntar archivo", Description: "/add <ruta> · archivo · contexto"},
	{ID: "system", Name: "Editar system prompt", Description: "/system · sistema · prompt"},
	{ID: "compact", Name: "Compactar conversación", Description: "/compact · resumir · liberar tokens"},
	{ID: "sessions", Name: "Historial de sesiones", Description: "Ctrl+H · sessions · reanudar"},
	{ID: "new", Name: "Nueva sesión", Description: "/new · nueva · limpiar"},
	{ID: "clear", Name: "Limpiar historial", Description: "/clear · borrar · historial"},
	{ID: "sidebar", Name: "Alternar sidebar", Description: "Ctrl+B · panel lateral"},
	{ID: "agents", Name: "Equipo de sub-agentes", Description: "Ctrl+T · swarm · roster"},
	{ID: "swarmmode", Name: "Alternar modo Swarm", Description: "Alt+S · single/swarm"},
	{ID: "mode", Name: "Cambiar modo agente", Description: "plan/default/auto/chat"},
}

type paletteModal struct {
	filter   textinput.Model
	visible  []paletteAction
	selected int
	scroll   int
}

func newPaletteModal() paletteModal {
	filter := newInput("Buscar acción…")
	pm := paletteModal{filter: filter}
	pm.applyFilter()
	return pm
}

func (pm *paletteModal) applyFilter() {
	query := strings.ToLower(strings.TrimSpace(pm.filter.Value()))
	pm.visible = pm.visible[:0]
	for _, action := range paletteActions {
		haystack := strings.ToLower(action.Name + " " + action.Description)
		if query == "" || strings.Contains(haystack, query) {
			pm.visible = append(pm.visible, action)
		}
	}
	pm.selected = 0
	pm.scroll = 0
}

func (m Model) openPalette() (Model, tea.Cmd) {
	m.hideSlashMenu()
	m.modal.kind = modalPalette
	m.modal.palette = newPaletteModal()
	m.textarea.Blur()
	m.resizeModals()
	return m, m.modal.palette.filter.Focus()
}

func (m Model) updatePaletteModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	pm := &m.modal.palette
	rows := m.paletteRows()

	switch {
	case key.Matches(msg, keyQuit):
		return m.quit()
	case key.Matches(msg, keyEsc):
		return m.closeModal()
	case msg.String() == "up":
		if len(pm.visible) == 0 {
			return m, nil
		}
		pm.selected = max(0, pm.selected-1)
		pm.ensureVisible(rows)
		return m, nil
	case msg.String() == "down":
		if len(pm.visible) == 0 {
			return m, nil
		}
		pm.selected = min(len(pm.visible)-1, pm.selected+1)
		pm.ensureVisible(rows)
		return m, nil
	case key.Matches(msg, keyEnter):
		if len(pm.visible) == 0 {
			return m, nil
		}
		action := pm.visible[pm.selected]
		m.modal.kind = modalNone
		return m.runPaletteAction(action.ID)
	}

	var cmd tea.Cmd
	pm.filter, cmd = pm.filter.Update(msg)
	pm.applyFilter()
	return m, cmd
}

func (pm *paletteModal) ensureVisible(rows int) {
	if len(pm.visible) == 0 {
		return
	}
	if pm.selected < 0 {
		pm.selected = 0
	}
	if pm.selected >= len(pm.visible) {
		pm.selected = len(pm.visible) - 1
	}
	if pm.selected < pm.scroll {
		pm.scroll = pm.selected
	}
	if pm.selected >= pm.scroll+rows {
		pm.scroll = pm.selected - rows + 1
	}
}

func (m Model) paletteRows() int {
	return min(10, max(3, m.modalContentHeight()-9))
}

func (m Model) paletteModalView() string {
	pm := m.modal.palette
	var b strings.Builder
	b.WriteString(titleStyle.Render("󰘳 Paleta de Comandos"))
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render("Buscar: "))
	b.WriteString(pm.filter.View())
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", m.modalContentWidth())))
	b.WriteString("\n")

	if len(pm.visible) == 0 {
		b.WriteString(warnStyle.Render("Sin acciones que coincidan."))
		b.WriteString("\n")
	} else {
		rows := m.paletteRows()
		if pm.scroll > len(pm.visible)-1 {
			pm.scroll = max(0, len(pm.visible)-rows)
		}
		end := min(len(pm.visible), pm.scroll+rows)
		for i := pm.scroll; i < end; i++ {
			action := pm.visible[i]
			line := fmt.Sprintf("%s  %s", action.Name, mutedStyle.Render(action.Description))
			if i == pm.selected {
				line = selectedRowStyle.Render(ansi.Truncate(" "+action.Name+"  "+action.Description, m.modalContentWidth(), "…"))
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("↑/↓ elegir · Enter ejecutar · Esc cerrar"))
	return m.modalBox(b.String())
}

// ---------------------------------------------------------------------------
// Historial de sesiones
// ---------------------------------------------------------------------------

type sessionsLoadedMsg struct {
	sessions []storage.Session
	err      error
}

type sessionSavedMsg struct {
	note string
	err  error
}

type sessionsModal struct {
	sessions []storage.Session
	selected int
	scroll   int
	loading  bool
	status   string
	err      string
}

func (m Model) openSessionsModal() (Model, tea.Cmd) {
	m.hideSlashMenu()
	m.persistSession(false)
	m.modal.kind = modalSessions
	m.modal.sessions = sessionsModal{loading: true}
	m.textarea.Blur()
	m.resizeModals()
	return m, loadSessionsCmd()
}

func loadSessionsCmd() tea.Cmd {
	return func() tea.Msg {
		sessions, err := storage.ListSessions()
		return sessionsLoadedMsg{sessions: sessions, err: err}
	}
}

func (m Model) handleSessionsLoaded(msg sessionsLoadedMsg) (Model, tea.Cmd) {
	if m.modal.kind != modalSessions {
		return m, nil
	}
	sm := &m.modal.sessions
	sm.loading = false
	if msg.err != nil {
		sm.err = msg.err.Error()
		return m, nil
	}
	sm.sessions = msg.sessions
	sm.err = ""
	sm.selected = 0
	sm.scroll = 0
	return m, nil
}

func (m Model) handleSessionSaved(msg sessionSavedMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = "No se pudo guardar la sesión: " + msg.err.Error()
		return m, nil
	}
	m.notice = msg.note
	return m, nil
}

func (m *sessionsModal) moveSelection(delta, rows int) {
	if len(m.sessions) == 0 {
		return
	}
	m.selected += delta
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(m.sessions) {
		m.selected = len(m.sessions) - 1
	}
	if rows > 0 {
		if m.selected < m.scroll {
			m.scroll = m.selected
		}
		if m.selected >= m.scroll+rows {
			m.scroll = m.selected - rows + 1
		}
	}
}

func (m sessionsModal) selectedSession() (storage.Session, bool) {
	if len(m.sessions) == 0 {
		return storage.Session{}, false
	}
	index := m.selected
	if index < 0 {
		index = 0
	}
	if index >= len(m.sessions) {
		index = len(m.sessions) - 1
	}
	return m.sessions[index], true
}

func (m Model) sessionRows() int {
	return min(10, max(3, m.modalContentHeight()-12))
}

func (m Model) updateSessionsModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	sm := &m.modal.sessions
	rows := m.sessionRows()

	switch {
	case key.Matches(msg, keyQuit):
		return m.quit()
	case key.Matches(msg, keyEsc):
		return m.closeModal()
	case msg.String() == "ctrl+n":
		return m.newSession()
	case msg.String() == "up":
		sm.moveSelection(-1, rows)
		return m, nil
	case msg.String() == "down":
		sm.moveSelection(1, rows)
		return m, nil
	case msg.String() == "pgup":
		sm.moveSelection(-rows, rows)
		return m, nil
	case msg.String() == "pgdown":
		sm.moveSelection(rows, rows)
		return m, nil
	case msg.String() == "ctrl+d":
		return m.deleteSelectedSession()
	case msg.String() == "ctrl+r":
		return m, tea.Batch(loadSessionsCmd(), m.spinner.Tick)
	case key.Matches(msg, keyEnter):
		return m.resumeSelectedSession()
	}
	return m, nil
}

func (m Model) deleteSelectedSession() (Model, tea.Cmd) {
	sm := &m.modal.sessions
	session, ok := sm.selectedSession()
	if !ok {
		sm.status = "No hay sesión seleccionada para eliminar."
		return m, nil
	}
	if err := storage.DeleteSession(session.ID); err != nil {
		sm.err = err.Error()
		return m, nil
	}

	filtered := make([]storage.Session, 0, len(sm.sessions))
	for _, item := range sm.sessions {
		if item.ID != session.ID {
			filtered = append(filtered, item)
		}
	}
	sm.sessions = filtered
	sm.selected = min(sm.selected, max(0, len(filtered)-1))
	sm.err = ""
	sm.status = "Sesión eliminada: " + valueOr(session.Title, session.ID)

	if session.ID == m.session.ID {
		model, cmd := m.newSession()
		model.modal.kind = modalSessions
		model.modal.sessions = *sm
		return model, cmd
	}
	return m, nil
}

func (m Model) resumeSelectedSession() (Model, tea.Cmd) {
	session, ok := m.modal.sessions.selectedSession()
	if !ok {
		m.modal.sessions.status = "No hay sesión seleccionada."
		return m, nil
	}
	return m.loadSession(session.ID)
}

func (m Model) sessionsModalView() string {
	sm := m.modal.sessions

	var b strings.Builder
	b.WriteString(titleStyle.Render("󰋚 Historial de Sesiones"))
	b.WriteString(mutedStyle.Render("  (~/.local/share/mkd/sessions)"))
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", m.modalContentWidth())))
	b.WriteString("\n")

	switch {
	case sm.loading:
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(mutedStyle.Render("Cargando sesiones…"))
		b.WriteString("\n")
	case len(sm.sessions) == 0:
		b.WriteString(warnStyle.Render("Aún no hay sesiones guardadas."))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("Escribe un mensaje para crear la primera sesión."))
		b.WriteString("\n")
	default:
		rows := m.sessionRows()
		if sm.scroll > len(sm.sessions)-1 {
			sm.scroll = max(0, len(sm.sessions)-rows)
		}
		end := min(len(sm.sessions), sm.scroll+rows)
		for i := sm.scroll; i < end; i++ {
			session := sm.sessions[i]
			marker := "○"
			markerStyle := offDotStyle
			if session.ID == m.session.ID {
				marker = "●"
				markerStyle = activeDotStyle
			}
			title := oneLine(valueOr(session.Title, "Sesión sin título"), max(10, m.modalContentWidth()-26))
			meta := fmt.Sprintf("%s · %d msgs", shortDate(session.UpdatedAt), len(session.Messages))
			row := markerStyle.Render(marker) + " " + textStyle.Render(title) + "  " + mutedStyle.Render(meta)
			if i == sm.selected {
				row = selectedRowStyle.Render(ansi.Truncate(" "+title+"  "+meta, m.modalContentWidth(), "…"))
			}
			b.WriteString(row)
			b.WriteString("\n")
		}
		b.WriteString(mutedStyle.Render(fmt.Sprintf("%d sesiones · ordenadas por fecha", len(sm.sessions))))
		b.WriteString("\n")
	}

	if sm.err != "" {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(sm.err))
		b.WriteString("\n")
	}
	if sm.status != "" {
		b.WriteString("\n")
		b.WriteString(successStyle.Render(sm.status))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Enter reanudar · Ctrl+N nueva · Ctrl+D eliminar"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Ctrl+R recargar · ↑/↓ navegar · Esc cerrar"))
	return m.modalBox(b.String())
}

func shortDate(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	now := time.Now()
	if value.Year() == now.Year() && value.YearDay() == now.YearDay() {
		return value.Format("hoy 15:04")
	}
	if value.Year() == now.Year() {
		return value.Format("02 ene 15:04")
	}
	return value.Format("02 ene 2006")
}

// ---------------------------------------------------------------------------
// Router de modales + overlay
// ---------------------------------------------------------------------------

func (m Model) updateModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch m.modal.kind {
	case modalProvider:
		return m.updateProviderModal(msg)
	case modalModels:
		return m.updateModelsModal(msg)
	case modalSystem:
		return m.updateSystemModal(msg)
	case modalPalette:
		return m.updatePaletteModal(msg)
	case modalSessions:
		return m.updateSessionsModal(msg)
	case modalDiff:
		return m.updateAgentDiffKey(msg)
	case modalBlocks:
		return m.updateBlocksModal(msg)
	case modalCommit:
		return m.updateCommitModal(msg)
	case modalMCP:
		return m.updateMCPModal(msg)
	case modalAgents:
		return m.updateAgentModal(msg)
	case modalQuestion:
		return m.handleQuestionKey(msg)
	default:
		return m, nil
	}
}

func (m Model) updateModalMsg(msg tea.Msg) (Model, tea.Cmd) {
	switch m.modal.kind {
	case modalProvider:
		p := &m.modal.provider
		var cmd tea.Cmd
		switch p.focus {
		case 1:
			p.baseURL, cmd = p.baseURL.Update(msg)
		case 2:
			p.apiKey, cmd = p.apiKey.Update(msg)
		}
		return m, cmd
	case modalModels:
		var cmd tea.Cmd
		m.modal.models.filter, cmd = m.modal.models.filter.Update(msg)
		return m, cmd
	case modalSystem:
		var cmd tea.Cmd
		m.modal.system.textarea, cmd = m.modal.system.textarea.Update(msg)
		return m, cmd
	case modalPalette:
		var cmd tea.Cmd
		m.modal.palette.filter, cmd = m.modal.palette.filter.Update(msg)
		return m, cmd
	case modalCommit:
		if m.modal.commit.phase != commitReady {
			return m, nil
		}
		var cmd tea.Cmd
		m.modal.commit.message, cmd = m.modal.commit.message.Update(msg)
		return m, cmd
	case modalMCP:
		var cmd tea.Cmd
		m.modal.mcp, cmd = m.modal.mcp.updateInputs(msg)
		return m, cmd
	case modalAgents:
		return m.updateAgentModalMsg(msg)
	default:
		return m, nil
	}
}

func (m Model) closeModal() (Model, tea.Cmd) {
	m.modal.kind = modalNone
	m.refresh()
	return m, m.textarea.Focus()
}

func (m Model) modalView() string {
	switch m.modal.kind {
	case modalProvider:
		return m.providerModalView()
	case modalModels:
		return m.modelsModalView()
	case modalSystem:
		return m.systemModalView()
	case modalPalette:
		return m.paletteModalView()
	case modalSessions:
		return m.sessionsModalView()
	case modalBlocks:
		return m.blocksModalView()
	case modalDiff:
		return m.diffModalView()
	case modalCommit:
		return m.commitModalView()
	case modalMCP:
		return m.mcpModalView()
	case modalAgents:
		return m.agentModalView()
	case modalQuestion:
		return m.questionModalView()
	default:
		return ""
	}
}

func (m Model) modalOuterWidth() int {
	width := m.width - 8
	if width > 90 {
		width = 90
	}
	if width < 36 {
		width = max(24, m.width-2)
	}
	return width
}

func (m Model) modalContentWidth() int {
	return max(18, m.modalOuterWidth()-6)
}

// modalOuterHeight fija la altura total del modal para que no cambie al
// navegar entre proveedores o al filtrar modelos.
func (m Model) modalOuterHeight() int {
	height := m.height - 4
	if height > 30 {
		height = 30
	}
	if height < 18 {
		height = max(14, m.height-2)
	}
	return height
}

func (m Model) modalContentHeight() int {
	return max(8, m.modalOuterHeight()-4)
}

// modalBoxWithFooter garantiza que el footer (teclas de acción) quede visible:
// recorta el cuerpo al alto disponible en lugar de dejar que fitLines corte el
// final del contenido. Se usa en editores largos como el de sub-agentes.
func (m Model) modalBoxWithFooter(body, footer string) string {
	contentWidth := m.modalContentWidth()
	contentHeight := m.modalContentHeight()

	footerHeight := lipgloss.Height(footer)
	bodyHeight := max(1, contentHeight-footerHeight)
	body = fitLines(body, contentWidth, bodyHeight)
	footer = fitLines(footer, contentWidth, footerHeight)

	content := body
	if strings.TrimSpace(footer) != "" {
		content = body + "\n" + footer
	}
	// Recorte final garantizado: lipgloss.Render solo rellena, no corta, así que
	// el footer podría quedar fuera de pantalla en terminales pequeñas.
	//
	// modalStyle añade Padding(1,2) y borde doble: el alto total es
	// contentHeight + 2 (padding) + 2 (borde). Por eso el cuerpo se recorta
	// exactamente a contentHeight.
	content = fitLines(content, contentWidth, contentHeight)
	return modalStyle.
		Width(contentWidth + 4).
		Height(contentHeight).
		Render(content)
}

func (m Model) modalBox(content string) string {
	contentWidth := m.modalContentWidth()
	contentHeight := m.modalContentHeight()
	content = fitLines(content, contentWidth, contentHeight)
	return modalStyle.
		Width(contentWidth + 4).
		Height(contentHeight + 2).
		Render(content)
}

func (m *Model) resizeModals() {
	contentWidth := m.modalContentWidth()
	switch m.modal.kind {
	case modalProvider:
		inputWidth := max(18, contentWidth-16)
		m.modal.provider.baseURL.Width = inputWidth
		m.modal.provider.baseURL.CursorEnd()
		m.modal.provider.apiKey.Width = inputWidth
		m.modal.provider.apiKey.CursorEnd()
	case modalModels:
		m.modal.models.filter.Width = max(18, contentWidth-12)
		m.modal.models.filter.CursorEnd()
	case modalSystem:
		m.modal.system.textarea.SetWidth(max(20, contentWidth))
		m.modal.system.textarea.SetHeight(max(3, m.modalContentHeight()-5))
	case modalPalette:
		m.modal.palette.filter.Width = max(18, contentWidth-10)
		m.modal.palette.filter.CursorEnd()
	case modalCommit:
		m.resizeCommitModal()
	case modalMCP:
		m.modal.mcp.resize(contentWidth)
	case modalAgents:
		m.resizeAgentModal()
	}
}

func overlayModal(background, foreground string, width, height int) string {
	modalWidth := lipgloss.Width(foreground)
	modalHeight := lipgloss.Height(foreground)
	x := max(0, (width-modalWidth)/2)
	y := max(0, (height-modalHeight)/2)
	return overlayAt(background, foreground, x, y, width, height)
}

func overlayAt(background, foreground string, x, y, width, height int) string {
	if foreground == "" || width <= 0 || height <= 0 {
		return background
	}

	buf := cellbuf.NewBuffer(width, height)
	cellbuf.SetContent(buf, background)

	foregroundWidth := lipgloss.Width(foreground)
	foregroundHeight := lipgloss.Height(foreground)
	if foregroundWidth > width {
		foregroundWidth = width
	}
	if foregroundHeight > height {
		foregroundHeight = height
	}
	x = min(max(0, x), max(0, width-foregroundWidth))
	y = min(max(0, y), max(0, height-foregroundHeight))
	cellbuf.SetContentRect(buf, foreground, cellbuf.Rect(x, y, foregroundWidth, foregroundHeight))

	return strings.ReplaceAll(cellbuf.Render(buf), "\r\n", "\n")
}

func newInput(placeholder string) textinput.Model {
	input := textinput.New()
	input.Placeholder = placeholder
	input.Prompt = "› "
	input.CharLimit = 0
	input.Width = 32
	input.PromptStyle = lipgloss.NewStyle().Foreground(purple).Background(surface)
	input.TextStyle = lipgloss.NewStyle().Foreground(text).Background(surface)
	input.PlaceholderStyle = lipgloss.NewStyle().Foreground(muted).Background(surface)
	input.Cursor.Style = lipgloss.NewStyle().Foreground(cyan).Background(surface)
	return input
}

func indexOf(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
