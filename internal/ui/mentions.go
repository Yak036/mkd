package ui

// Menciones @ con fuzzy finder: popup flotante sobre el input, completado con
// Tab y adjunto de archivos/especiales con Enter.
//
//	@go…                ┌─ @ contexto ────────────────┐
//	                    │ 󰊢 @git:diff  cambios locales │
//	                    │ 󰈔 providers.go  internal/…   │
//	                    └──────────────────────────────┘
//	   Textarea sigue con foco; la tecla no se reenvía al textarea.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	mkdcontext "mkd/internal/context"
)

// mentionPopupRows es el máximo de filas visibles del popup.
const mentionPopupRows = 8

// mentionQueryLimit acota la longitud del token @ activo.
const mentionQueryLimit = 80

type mentionItem struct {
	Token       string
	Label       string
	Description string
	Special     bool
	// Dir marca directorios: al elegirlos se navega dentro en vez de anclarlos.
	Dir bool
	// Skill marca skills instaladas, invocables con `:@nombre`.
	Skill bool
	// Rule marca reglas del proyecto/globales, invocables con `:@nombre`.
	Rule bool
}

type mentionPopup struct {
	visible    bool
	query      string
	matches    []mentionItem
	files      []mentionItem
	selected   int
	scroll     int
	suppressed string
	// skillsMode indica que el token es `:@…` y solo se listan skills.
	skillsMode bool
	// confirmed guarda el token recién anclado y confirmedAt la hora del
	// anclaje, para mostrar un aviso verde durante una ventana breve.
	confirmed   string
	confirmedAt time.Time
}

// mentionIndexCache comparte un FileIndex por cwd entre modelos (la vista y el
// update son por valor, así que el índice se guarda en un puntero apto para
// copias). La caché de disco vive en context.FileIndex.
type mentionIndexCache struct {
	mu    sync.Mutex
	cwd   string
	index *mkdcontext.FileIndex
}

func (c *mentionIndexCache) get(cwd string) *mkdcontext.FileIndex {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.index != nil && c.cwd == cwd {
		if err := c.index.Build(); err == nil {
			return c.index
		}
	}
	index := mkdcontext.NewFileIndex(cwd)
	if err := index.Build(); err != nil {
		return nil
	}
	c.cwd = cwd
	c.index = index
	return index
}

// mentionsCwd es el directorio base de las menciones. Se usa el WorkDir del
// Manager cuando está disponible para que coincida con los archivos anclados.
func (m Model) mentionsCwd() string {
	if m.ctxMgr != nil && strings.TrimSpace(m.ctxMgr.WorkDir) != "" {
		return m.ctxMgr.WorkDir
	}
	cwd, _ := os.Getwd()
	return cwd
}

func (m *Model) mentionCache() *mentionIndexCache {
	if m.mentions.index == nil {
		m.mentions.index = &mentionIndexCache{}
	}
	return m.mentions.index
}

// updateMentions analiza el valor del textarea y refresca el popup. Se llama
// tras cada actualización del textarea (y al ganar foco).
func (m *Model) updateMentions() {
	if !m.mentionPopupAllowed() {
		m.hideMentions()
		return
	}

	value := m.textarea.Value()
	if value == m.mentions.popup.suppressed {
		m.mentions.popup.visible = false
		return
	}
	m.mentions.popup.suppressed = ""

	query, ok := mentionQueryAt(value)
	if !ok {
		m.hideMentions()
		return
	}

	previous := m.selectedMentionToken()
	popup := &m.mentions.popup
	popup.query = query
	popup.skillsMode = m.mentionSkillsTrigger()
	popup.confirmed = ""

	if popup.skillsMode {
		popup.files = nil
		// `:@` lista skills y reglas juntas: ambos son contexto inyectable.
		popup.matches = append(m.mentionSkillMatches(query), m.mentionRuleMatches(query)...)
	} else {
		popup.files = m.mentionFileMatches(query)
		popup.matches = m.composeMentionMatches(query, popup.files)
	}
	if len(popup.matches) == 0 {
		m.hideMentions()
		return
	}

	popup.visible = true
	popup.selected = 0
	if previous != "" {
		for i, item := range popup.matches {
			if item.Token == previous {
				popup.selected = i
				break
			}
		}
	}
	m.ensureMentionVisible()
}

// mentionPopupAllowed evita el popup durante modales, streaming o la paleta de
// slash (que comparten el mismo input).
func (m Model) mentionPopupAllowed() bool {
	return m.state == stateWorkspace &&
		m.modal.kind == modalNone &&
		!m.streaming && !m.waiting &&
		!m.slashMenuVisible
}

// mentionQueryAt extrae el token @ activo situado al final del texto (donde
// está el cursor tras teclear), sin espacios intermedios y con límite de 80
// caracteres. Solo considera la última línea (las menciones no cruzan saltos).
func mentionQueryAt(value string) (string, bool) {
	line := value
	if idx := strings.LastIndexByte(line, '\n'); idx >= 0 {
		line = line[idx+1:]
	}
	start := strings.LastIndexByte(line, ' ')
	if start < 0 {
		start = 0
	} else {
		start++
	}
	token := line[start:]
	// `:@nombre` es el trigger de skills; `@ruta` el de contexto. En ambos
	// casos la consulta es lo que va tras el `@`.
	var query string
	switch {
	case strings.HasPrefix(token, ":@"):
		query = token[2:]
	case strings.HasPrefix(token, "@"):
		query = token[1:]
	default:
		return "", false
	}
	if len([]rune(query)) > mentionQueryLimit {
		return "", false
	}
	for _, r := range query {
		if r == '\t' || r == '\r' {
			return "", false
		}
	}
	return query, true
}

// composeMentionMatches pone los especiales primero y luego los archivos.
func (m Model) composeMentionMatches(query string, files []mentionItem) []mentionItem {
	lower := strings.ToLower(query)
	matches := make([]mentionItem, 0, len(mkdcontext.SpecialProviders)+len(files))
	for _, provider := range mkdcontext.SpecialProviders {
		if !mentionSpecialMatches(lower, provider.Name) {
			continue
		}
		matches = append(matches, mentionItem{
			Token:       provider.Name,
			Label:       provider.Name,
			Description: provider.Description,
			Special:     true,
		})
	}
	matches = append(matches, files...)
	return matches
}

// mentionSpecialMatches filtra tokens especiales por substring o subsecuencia
// ("git", "diff", "gd" casan con "@git:diff").
func mentionSpecialMatches(query, name string) bool {
	if query == "" {
		return true
	}
	lower := strings.ToLower(strings.TrimPrefix(name, "@"))
	if strings.Contains(lower, query) {
		return true
	}
	return isSubsequence(query, lower)
}

// isSubsequence indica si pattern es subsecuencia case-insensitive de target.
func isSubsequence(pattern, target string) bool {
	pat := []rune(pattern)
	pi := 0
	for _, r := range target {
		if pi < len(pat) && r == pat[pi] {
			pi++
		}
	}
	return pi == len(pat)
}

// mentionFileMatches indexa el cwd y devuelve los archivos que coinciden.
// markConfirmed registra el token anclado para el flash de confirmación.
func (m *Model) markConfirmed(token string) {
	m.mentions.popup.confirmed = token
	m.mentions.popup.confirmedAt = time.Now()
}

// confirmationWindow es cuánto tiempo se mantiene el aviso de confirmación.
const confirmationWindow = 3 * time.Second

// confirmationBanner devuelve la línea verde de confirmación si está vigente.
func (m Model) confirmationBanner() string {
	if m.mentions.popup.confirmed == "" || m.mentions.popup.confirmedAt.IsZero() {
		return ""
	}
	if time.Since(m.mentions.popup.confirmedAt) > confirmationWindow {
		return ""
	}
	return successStyle.Render("✓ seleccionado: ") +
		skillOrFileStyle(m.mentions.popup.confirmed).Render(m.mentions.popup.confirmed)
}

// skillOrFileStyle pinta las skills en ámbar y los archivos en verde.
func skillOrFileStyle(token string) lipgloss.Style {
	if strings.HasPrefix(token, ":@") {
		return skillStyle
	}
	return successStyle
}

// mentionFileMatches alimenta el popup con el autocompletado tipo shell:
// prefijo de ruta primero, fuzzy después, y directorios navegables.
func (m *Model) mentionFileMatches(query string) []mentionItem {
	cwd := m.mentionsCwd()
	index := m.mentionCache().get(cwd)
	if index == nil {
		return nil
	}
	paths := index.PathMatches(strings.TrimPrefix(query, "./"), mentionPopupRows)
	items := make([]mentionItem, 0, len(paths))
	for _, path := range paths {
		isDir := strings.HasSuffix(path, "/")
		clean := strings.TrimSuffix(path, "/")
		label := filepath.Base(clean)
		description := ""
		if isDir {
			label += "/"
			description = "directorio"
		} else {
			description = filepath.Dir(clean)
			if description == "." {
				description = "./"
			}
		}
		items = append(items, mentionItem{
			Token:       "@" + path,
			Label:       label,
			Description: description,
			Dir:         isDir,
		})
	}
	return items
}

func (m *Model) hideMentions() {
	popup := &m.mentions.popup
	popup.visible = false
	popup.suppressed = m.textarea.Value()
	popup.matches = nil
	popup.selected = 0
	popup.scroll = 0
}

func (m *Model) ensureMentionVisible() {
	popup := &m.mentions.popup
	if len(popup.matches) == 0 {
		popup.scroll = 0
		return
	}
	if popup.selected < popup.scroll {
		popup.scroll = popup.selected
	}
	if popup.selected >= popup.scroll+mentionPopupRows {
		popup.scroll = popup.selected - mentionPopupRows + 1
	}
	maxScroll := max(0, len(popup.matches)-mentionPopupRows)
	if popup.scroll > maxScroll {
		popup.scroll = maxScroll
	}
}

// moveMentionSelection mueve la selección del popup.
func (m *Model) moveMentionSelection(delta int) {
	popup := &m.mentions.popup
	if !popup.visible || len(popup.matches) == 0 {
		return
	}
	popup.selected = min(max(0, popup.selected+delta), len(popup.matches)-1)
	m.ensureMentionVisible()
}

// MentionPopupVisible indica si el popup debe capturar ↑/↓/Tab/Enter.
func (m Model) MentionPopupVisible() bool {
	return m.mentions.popup.visible
}

func (m Model) selectedMention() (mentionItem, bool) {
	popup := m.mentions.popup
	if !popup.visible || len(popup.matches) == 0 {
		return mentionItem{}, false
	}
	index := min(max(0, popup.selected), len(popup.matches)-1)
	return popup.matches[index], true
}

func (m Model) selectedMentionToken() string {
	item, ok := m.selectedMention()
	if !ok {
		return ""
	}
	return item.Token
}

// completeMention (Tab) sustituye el token @ parcial por la selección. Los
// especiales dejan el popup abierto para poder seguir escribiendo; los
// archivos lo cierran.
func (m Model) completeMention() (Model, tea.Cmd) {
	item, ok := m.selectedMention()
	if !ok {
		return m, nil
	}
	// Directorio: completar con "/" (sin espacio) y seguir navegando.
	if item.Dir {
		m.replaceMentionToken(strings.TrimSuffix(item.Token, "/") + "/")
		m.mentions.popup.suppressed = ""
		m.updateMentions()
		return m, m.textarea.Focus()
	}

	m.replaceMentionToken(item.Token + " ")
	// Especiales dejan el popup abierto para seguir escribiendo la mención.
	if item.Special {
		m.mentions.popup.suppressed = ""
		m.updateMentions()
	} else {
		m.hideMentions()
	}
	return m, m.textarea.Focus()
}

// applySelectedMention (Enter) aplica la selección actual del popup.
func (m *Model) applySelectedMention() (tea.Model, tea.Cmd) {
	return m.applyMentionItem()
}

// applyMention aplica un item: archivos → archivos activos; especiales →
// contenido resuelto en el estado de menciones.
func (m *Model) applyMention(item mentionItem) (tea.Model, tea.Cmd) {
	token := strings.TrimPrefix(item.Token, "@")
	cwd := m.mentionsCwd()
	var notice string

	// Directorio: completar la ruta y dejar el popup abierto con sus hijos.
	if item.Dir {
		m.replaceMentionToken(item.Token)
		m.mentions.popup.suppressed = ""
		m.updateMentions()
		return m, m.textarea.Focus()
	}

	// Regla (`:@nombre`): forzarla para el turno.
	if item.Rule {
		name := strings.TrimPrefix(item.Token, ":@")
		var ok bool
		notice, ok = m.applyRule(name)
		m.replaceMentionToken(strings.TrimPrefix(item.Token, ":") + " ")
		m.markConfirmed(item.Token)
		m.mentions.popup.suppressed = ""
		m.hideMentions()
		m.notice = notice
		_ = ok
		return m, m.textarea.Focus()
	}

	// Skill (`:@nombre`): inyectar su SKILL.md y marcarla en el sidebar.
	if item.Skill {
		name := strings.TrimPrefix(item.Token, ":@")
		notice, _ = m.applySkill(name)
		m.replaceMentionToken(strings.TrimPrefix(item.Token, ":") + " ")
		m.markConfirmed(item.Token)
		m.mentions.popup.suppressed = ""
		m.hideMentions()
		m.notice = notice
		return m, m.textarea.Focus()
	}

	if item.Special {
		content, err := mkdcontext.ResolveMention(item.Token, cwd)
		if err != nil {
			notice = "No se pudo resolver " + item.Token + ": " + err.Error()
		} else {
			if m.mentions.Mentions == nil {
				m.mentions.Mentions = map[string]string{}
			}
			m.mentions.Mentions[item.Token] = content
			m.markConfirmed(item.Token)
			notice = "Contexto agregado: " + item.Token
		}
	} else {
		path := token
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		file, err := m.ctxMgr.AddFile(path)
		if err != nil {
			notice = "No se pudo adjuntar: " + err.Error()
		} else {
			m.upsertContextFile(file)
			m.syncSession()
			m.refreshUsage()
			notice = "Archivo adjuntado: " + file.Path
		}
	}

	m.replaceMentionToken(item.Token + " ")
	m.mentions.popup.suppressed = ""
	m.hideMentions()
	m.notice = notice
	return m, m.textarea.Focus()
}

func (m *Model) applyMentionItem() (tea.Model, tea.Cmd) {
	item, ok := m.selectedMention()
	if !ok {
		return m, nil
	}
	return m.applyMention(item)
}

// upsertContextFile refleja el archivo anclado en el sidebar sin duplicar.
func (m *Model) upsertContextFile(file mkdcontext.PinnedFile) {
	for i := range m.contextFiles {
		if m.contextFiles[i].Path == file.Path {
			m.contextFiles[i] = ContextFile{Path: file.Path, Size: file.Size, Hash: file.Hash}
			return
		}
	}
	m.contextFiles = append(m.contextFiles, ContextFile{Path: file.Path, Size: file.Size, Hash: file.Hash})
}

// replaceMentionToken sustituye el token @ del final del texto por replacement.
// Se usa el final porque el popup solo se muestra al teclear al final; es
// equivalente al cursor en el caso normal de escritura.
func (m *Model) replaceMentionToken(replacement string) {
	value := m.textarea.Value()
	start := len(value)
	for start > 0 {
		ch := value[start-1]
		if ch == '@' || ch == ' ' || ch == '\n' || ch == '\t' {
			break
		}
		start--
	}
	if start > 0 && value[start-1] == '@' {
		start--
		// Trigger de skills `:@`: incluir también los dos puntos.
		if start > 0 && value[start-1] == ':' {
			start--
		}
	}
	m.textarea.SetValue(value[:start] + replacement)
	m.textarea.CursorEnd()
	m.mentions.popup.query = ""
	m.mentions.popup.files = nil
}

// MentionQuery expone la consulta activa (útil para la barra de estado).
func (m Model) MentionQuery() string {
	return m.mentions.popup.query
}

// MentionsContextBlock devuelve los bloques de menciones especiales resueltas,
// en orden estable, para inyectarlos en el contexto al enviar.
func (m Model) mentionsContextBlock() string {
	if len(m.mentions.Mentions) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m.mentions.Mentions))
	for key := range m.mentions.Mentions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		block := strings.TrimSpace(m.mentions.Mentions[key])
		if block != "" {
			parts = append(parts, block)
		}
	}
	return strings.Join(parts, "\n\n")
}

// ClearMentions limpia el estado de menciones (p.ej. en nueva sesión).
func (m *Model) ClearMentions() {
	m.mentions.popup = mentionPopup{}
	m.mentions.Mentions = nil
}

// mentionsView dibuja el popup flotante. Debe superponerse con overlayAt sobre
// workspaceView, dejando libre el input.
func (m Model) mentionsView() string {
	popup := m.mentions.popup
	if !popup.visible || len(popup.matches) == 0 {
		return ""
	}

	maxWidth := m.innerWidth - 6
	if m.effectiveSidebar {
		maxWidth = m.conversationWidth - 2
	}
	outerWidth := min(84, max(50, maxWidth))
	contentWidth := max(24, outerWidth-4)

	lines := make([]string, 0, mentionPopupRows+1)
	headerText := "@ contexto"
	prefix := "@"
	if popup.skillsMode {
		headerText = "✦ skills"
		prefix = ":@"
	}
	header := titleStyle.Render(headerText)
	if popup.query != "" {
		header += mutedStyle.Render("  " + ansi.Truncate(prefix+popup.query, max(8, contentWidth-14), "…"))
	}
	if popup.skillsMode {
		header += mutedStyle.Render("  Enter activar")
	} else {
		header += mutedStyle.Render("  Tab/Enter: entrar o anclar")
	}
	lines = append(lines, ansi.Truncate(header, contentWidth, "…"))

	start := popup.scroll
	if start > len(popup.matches)-1 {
		start = max(0, len(popup.matches)-mentionPopupRows)
	}
	end := min(len(popup.matches), start+mentionPopupRows)
	for i := start; i < end; i++ {
		item := popup.matches[i]
		icon := "󰈔"
		iconStyle := textStyle
		labelStyle := titleStyle
		switch {
		case item.Special:
			icon = "󰊢"
			iconStyle = titleCyanStyle
		case item.Skill:
			icon = "✦"
			iconStyle = skillStyle
			labelStyle = skillStyle
		case item.Rule:
			icon = "⚖"
			iconStyle = ruleStyle
			labelStyle = ruleStyle
		}

		active := (item.Skill && m.skillActive(strings.TrimPrefix(item.Token, ":@"))) ||
			(item.Rule && m.ruleActive(strings.TrimPrefix(item.Token, ":@")))

		if i == popup.selected {
			line := icon + " " + item.Label + "  " + item.Description
			lines = append(lines, selectedRowStyle.Render(ansi.Truncate(" "+line, contentWidth, "…")))
			continue
		}
		if active {
			// Feedback: lo ya seleccionado/activo se pinta en verde neón.
			labelStyle = successStyle
			iconStyle = successStyle
		}

		labelWidth := max(8, contentWidth/2)
		label := ansi.Truncate(item.Label, labelWidth, "…")
		description := ansi.Truncate(item.Description, max(4, contentWidth-labelWidth-4), "…")
		lines = append(lines, iconStyle.Render(icon)+" "+
			labelStyle.Render(padToWidth(label, labelWidth))+" "+mutedStyle.Render(description))
	}
	for len(lines) < mentionPopupRows+1 {
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

// MentionsLayer devuelve la capa flotante y su posición (x, y) dentro del frame para
// superponerla con overlayAt sobre workspaceView. (x, y) = (0, 0) y view=""
// cuando el popup no está visible.
func (m Model) MentionsLayer() (view string, x, y int) {
	view = m.mentionsView()
	if view == "" {
		return "", 0, 0
	}
	const headerHeight = 2
	const statusHeight = 1
	boxHeight := max(1, lipgloss.Height(view))
	y = m.height - 1 - headerHeight - statusHeight - boxHeight
	y = max(2, y)
	return view, 2, y
}

// padToWidth rellena con espacios hasta width celdas visibles.
func padToWidth(value string, width int) string {
	if pad := width - lipgloss.Width(value); pad > 0 {
		return value + strings.Repeat(" ", pad)
	}
	return value
}

// mentionTextareaChanged se llama cuando el textarea procesa una tecla: limpia
// la supresión y recalcula el popup.
func (m *Model) mentionTextareaChanged() {
	m.mentions.popup.suppressed = ""
	m.updateMentions()
}
