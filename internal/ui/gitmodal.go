package ui

// Modal interactivo de /commit — flujo inteligente con Conventional Commits.
//
//	/commit o Ctrl+G
//	      │
//	      ▼
//	git.StatusPorcelain ──► sin cambios ──► nota "nada que commitear"
//	      │
//	      ▼
//	  LLM (git.CommitPrompt) ──► mensaje conventional (editable)
//	      │
//	      ▼
//	┌─ 󰊢 Generar y Confirmar Git Commit ───────────────────────┐
//	│ Rama: main │ Archivos modificados: 4 (+128 / -19)        │
//	│ ┌──────────────────────────────────────────────────────┐ │
//	│ │ feat(mcp): add stdio JSON-RPC client for MCP tools   │ │
//	│ └──────────────────────────────────────────────────────┘ │
//	│ [Ctrl+Enter] Commit  [Ctrl+R] Regenerar                  │
//	│ [Ctrl+A] Stage All   [Esc] Cancelar                      │
//	└──────────────────────────────────────────────────────────┘
//	      │ Ctrl+Enter
//	      ▼
//	  git.Commit ──► [main abc1234] … ──► chat + status bar

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	openai "github.com/sashabaranov/go-openai"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/git"
)

// commitPhase describe en qué paso del flujo está el modal.
type commitPhase int

const (
	// commitLoading: el LLM está generando el mensaje.
	commitLoading commitPhase = iota
	// commitReady: el mensaje está listo para editar/confirmar.
	commitReady
	// commitRunning: se está ejecutando git commit.
	commitRunning
)

// gitCommitModal es el estado del modal /commit.
type gitCommitModal struct {
	phase   commitPhase
	status  git.Status
	diff    string
	message textarea.Model
	err     string
	status_ string
}

// generationTimeout acota la llamada al LLM para el mensaje de commit.
const commitGenerationTimeout = 60 * time.Second

// commitGeneratedMsg llega cuando el LLM terminó de proponer el mensaje.
type commitGeneratedMsg struct {
	message string
	err     error
}

// commitDoneMsg llega cuando git commit terminó.
type commitDoneMsg struct {
	result git.CommitResult
	err    error
}

// commitContextMsg transporta el diff/estado recolectado antes del LLM.
type commitContextMsg struct {
	status git.Status
	diff   string
	prompt string
	err    error
}

// newGitCommitModal construye el modal en fase de carga.
func newGitCommitModal(status git.Status) gitCommitModal {
	ta := textarea.New()
	ta.Placeholder = "feat(scope): descripción…"
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.SetHeight(5)
	ta.SetWidth(60)
	ta.FocusedStyle.Base = lipgloss.NewStyle().Background(surface)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(surface)
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(purple).Background(surface)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(muted).Background(surface)
	ta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(text).Background(surface)
	ta.FocusedStyle.EndOfBuffer = lipgloss.NewStyle().Background(surface)
	return gitCommitModal{phase: commitLoading, status: status, message: ta}
}

// openCommitModal arranca el flujo /commit recolectando el diff y llamando al
// LLM en background.
func (m Model) openCommitModal() (Model, tea.Cmd) {
	m.hideSlashMenu()
	if m.streaming || m.waiting {
		m.notice = "Espera a que termine la generación actual."
		return m, nil
	}

	cwd := m.agentCwd()
	if !git.IsRepo(cwd) {
		m.notice = "El directorio actual no es un repositorio git."
		return m, nil
	}
	if !m.configured() {
		m.notice = "Ningún modelo disponible para generar el mensaje."
		return m.openProviderModal()
	}

	status, err := git.StatusPorcelain(cwd)
	if err != nil {
		m.notice = "git status: " + err.Error()
		return m, nil
	}
	if !status.HasChanges() {
		m.notice = "No hay cambios para commitear."
		return m, nil
	}

	m.modal.kind = modalCommit
	m.modal.commit = newGitCommitModal(status)
	m.textarea.Blur()
	m.resizeModals()
	return m, tea.Batch(gatherCommitContextCmd(cwd), m.spinner.Tick)
}

// gatherCommitContextCmd recolecta diff, log y construye el prompt sin bloquear.
func gatherCommitContextCmd(cwd string) tea.Cmd {
	return func() tea.Msg {
		status, err := git.StatusPorcelain(cwd)
		if err != nil {
			return commitContextMsg{err: err}
		}

		var diff string
		if status.HasStaged() {
			diff, err = git.StagedDiff(cwd)
		} else {
			diff, err = git.UnstagedDiff(cwd)
		}
		if err != nil {
			return commitContextMsg{err: err}
		}
		if strings.TrimSpace(diff) == "" {
			// Solo untracked: mostramos los nombres para que el LLM tenga algo.
			diff = "(archivos nuevos sin stage)\n" + strings.Join(append(status.Staged, status.Untracked...), "\n")
		}

		log, _ := git.RecentLog(cwd, 3)
		return commitContextMsg{
			status: status,
			diff:   diff,
			prompt: git.CommitPrompt(diff, log, status),
		}
	}
}

// handleCommitContext guarda el contexto y lanza la generación con el LLM.
func (m Model) handleCommitContext(msg commitContextMsg) (Model, tea.Cmd) {
	if m.modal.kind != modalCommit {
		return m, nil
	}
	c := &m.modal.commit
	if msg.err != nil {
		c.phase = commitReady
		c.err = msg.err.Error()
		return m, nil
	}
	c.status = msg.status
	c.diff = msg.diff
	c.err = ""

	client := m.client
	prompt := msg.prompt
	return m, tea.Batch(generateCommitCmd(client, prompt), m.spinner.Tick)
}

// generateCommitCmd pide al modelo el mensaje de commit y lo limpia.
func generateCommitCmd(client *ai.Client, prompt string) tea.Cmd {
	return func() tea.Msg {
		if client == nil || !client.Ready() {
			return commitGeneratedMsg{err: ai.ErrNotConfigured}
		}
		ctx, cancel := context.WithTimeout(context.Background(), commitGenerationTimeout)
		defer cancel()

		messages := []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "Generas mensajes de commit profesionales en formato Conventional Commits. Respondes solo con el mensaje.",
			},
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		}
		out, err := client.CompleteMessages(ctx, messages)
		return commitGeneratedMsg{message: out, err: err}
	}
}

// updateCommitModal procesa las teclas del modal.
func (m Model) updateCommitModal(msg tea.KeyMsg) (Model, tea.Cmd) {
	c := &m.modal.commit

	switch {
	case key.Matches(msg, keyQuit):
		return m.openQuitConfirmModal()
	case key.Matches(msg, keyEsc):
		m.modal.kind = modalNone
		m.refresh()
		return m, m.textarea.Focus()
	case msg.String() == "ctrl+r":
		return m.regenerateCommit()
	case msg.String() == "ctrl+a":
		return m.stageAllCommit()
	case msg.String() == "ctrl+enter", msg.String() == "ctrl+s":
		return m.executeCommit()
	}

	if c.phase == commitReady {
		var cmd tea.Cmd
		c.message, cmd = c.message.Update(msg)
		return m, cmd
	}
	return m, nil
}

// regenerateCommit vuelve a pedir el mensaje al LLM con el diff actual.
func (m Model) regenerateCommit() (Model, tea.Cmd) {
	c := &m.modal.commit
	if !m.configured() {
		c.err = "Ningún modelo disponible."
		return m, nil
	}
	c.phase = commitLoading
	c.err = ""
	c.status_ = "Regenerando mensaje…"

	client := m.client
	prompt := git.CommitPrompt(c.diff, nil, c.status)
	return m, tea.Batch(generateCommitCmd(client, prompt), m.spinner.Tick)
}

// stageAllCommit ejecuta git add -A y refresca el estado.
func (m Model) stageAllCommit() (Model, tea.Cmd) {
	c := &m.modal.commit
	cwd := m.agentCwd()
	if err := git.AddAll(cwd); err != nil {
		c.err = err.Error()
		return m, nil
	}
	status, err := git.StatusPorcelain(cwd)
	if err != nil {
		c.err = err.Error()
		return m, nil
	}
	c.status = status
	c.status_ = "✓ git add -A ejecutado"
	return m, nil
}

// executeCommit valida y lanza git commit.
func (m Model) executeCommit() (Model, tea.Cmd) {
	c := &m.modal.commit
	if c.phase != commitReady {
		return m, nil
	}
	message := strings.TrimSpace(c.message.Value())
	if message == "" {
		c.err = "El mensaje de commit está vacío."
		return m, nil
	}
	if strings.HasPrefix(message, "```") {
		message = stripFences(message)
	}

	cwd := m.agentCwd()
	// Si no hay nada en stage, hacemos git add -u automáticamente (como avisó
	// el modal).
	if !c.status.HasStaged() {
		if err := git.AddTracked(cwd); err != nil {
			c.err = err.Error()
			return m, nil
		}
		// Los archivos nuevos requieren add -A; lo intentamos sin fallar.
		_ = git.AddAll(cwd)
	}

	c.phase = commitRunning
	c.err = ""
	c.status_ = "Ejecutando git commit…"
	return m, tea.Batch(commitCmd(cwd, message), m.spinner.Tick)
}

// commitCmd ejecuta git commit en background.
func commitCmd(cwd, message string) tea.Cmd {
	return func() tea.Msg {
		result, err := git.Commit(cwd, message)
		return commitDoneMsg{result: result, err: err}
	}
}

// handleCommitGenerated inyecta el mensaje propuesto en el textarea.
func (m Model) handleCommitGenerated(msg commitGeneratedMsg) (Model, tea.Cmd) {
	if m.modal.kind != modalCommit {
		return m, nil
	}
	c := &m.modal.commit
	c.phase = commitReady
	c.status_ = ""
	if msg.err != nil {
		c.err = "No se pudo generar el mensaje: " + msg.err.Error()
		c.message.SetValue("")
		return m, c.message.Focus()
	}

	c.err = ""
	c.message.SetValue(cleanCommitMessage(msg.message))
	c.message.CursorEnd()
	m.resizeModals()
	return m, c.message.Focus()
}

// handleCommitDone cierra el modal, deja traza en el chat y avisa en la barra.
func (m Model) handleCommitDone(msg commitDoneMsg) (Model, tea.Cmd) {
	if m.modal.kind != modalCommit {
		return m, nil
	}
	c := &m.modal.commit
	if msg.err != nil {
		c.phase = commitReady
		c.err = msg.err.Error()
		c.status_ = ""
		return m, nil
	}

	m.modal.kind = modalNone
	hash := valueOr(msg.result.Hash, "commit creado")
	subject := valueOr(msg.result.Subject, hash)
	m.notice = "✓ Commit " + hash + " · " + oneLine(subject, 48)

	summary := "✓ **Commit creado**: `" + hash + "`\n\n```\n" + subject + "\n```"
	if msg.result.Output != "" {
		summary += "\n" + mutedStyle.Render(oneLine(firstOutputLine(msg.result.Output), 100))
	}
	m.blocks = append(m.blocks, assistantBoxStyle.Width(max(20, m.conversationWidth-2)).Render(
		titleCyanStyle.Render("󰊢 Git")+"\n"+m.renderMarkdown(summary)))
	m.refresh()
	m.viewport.GotoBottom()
	return m, m.textarea.Focus()
}

// commitModalView pinta el modal.
func (m Model) commitModalView() string {
	c := m.modal.commit
	width := m.modalContentWidth()

	var b strings.Builder
	b.WriteString(titleStyle.Render("󰊢 Generar y Confirmar Git Commit"))
	b.WriteString("\n")
	b.WriteString(textStyle.Render(fmt.Sprintf("Rama: %s", valueOr(c.status.Branch, "—"))))
	b.WriteString(mutedStyle.Render(fmt.Sprintf("  │ Archivos modificados: %d (+%d / -%d)",
		c.status.ChangedFiles(), c.status.Adds, c.status.Removes)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", width)))
	b.WriteString("\n")

	if !c.status.HasStaged() {
		b.WriteString(warnStyle.Render("⚠ No hay archivos en stage. Se hará git add -u automáticamente al confirmar."))
		b.WriteString("\n")
	}

	b.WriteString(mutedStyle.Render("Mensaje propuesto (Editable):"))
	b.WriteString("\n")
	switch c.phase {
	case commitLoading, commitRunning:
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(mutedStyle.Render(valueOr(c.status_, "Generando mensaje…")))
		b.WriteString("\n")
	default:
		b.WriteString(c.message.View())
		b.WriteString("\n")
	}

	if c.err != "" {
		b.WriteString(errorStyle.Render(oneLine(c.err, width)))
		b.WriteString("\n")
	} else if c.status_ != "" {
		b.WriteString(successStyle.Render(c.status_))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(diffHintStyle.Render("[Ctrl+Enter]"))
	b.WriteString(textStyle.Render(" Ejecutar commit   "))
	b.WriteString(diffHintStyle.Render("[Ctrl+R]"))
	b.WriteString(textStyle.Render(" Regenerar"))
	b.WriteString("\n")
	b.WriteString(diffHintStyle.Render("[Ctrl+A]"))
	b.WriteString(textStyle.Render(" Stage All (git add -A)   "))
	b.WriteString(diffHintStyle.Render("[Esc]"))
	b.WriteString(textStyle.Render(" Cancelar"))
	return m.modalBox(b.String())
}

// resizeCommitModal ajusta el textarea al ancho disponible.
func (m *Model) resizeCommitModal() {
	contentWidth := m.modalContentWidth()
	m.modal.commit.message.SetWidth(max(20, contentWidth-2))
	m.modal.commit.message.SetHeight(max(4, min(10, m.modalContentHeight()-10)))
}

// cleanCommitMessage quita tildes de código, prefijos de lista y comillas.
func cleanCommitMessage(value string) string {
	value = strings.TrimSpace(value)
	value = stripFences(value)
	value = strings.TrimSpace(value)

	// Algunos modelos envuelven el mensaje en comillas o con "Commit:".
	for _, prefix := range []string{"commit:", "mensaje:", "message:"} {
		if strings.HasPrefix(strings.ToLower(value), prefix) {
			value = strings.TrimSpace(value[len(prefix):])
		}
	}
	value = strings.Trim(value, "\"'`")
	return strings.TrimSpace(value)
}

// stripFences elimina un bloque ```…``` envolvente si lo hubiera.
func stripFences(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			lines[i] = ""
			break
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			lines[i] = ""
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func firstOutputLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
