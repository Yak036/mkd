package ui

// Flujo /review — Code Review local en streaming sobre el chat principal.
//
//	/review [rama-base]
//	     │
//	     ▼
//	git.DiffBetween ──► git.FilesFromDiff ──► git.ReviewPrompt
//	     │
//	     ▼
//	client.StreamMessages ──► chunkMsg (igual que un mensaje normal)
//	     │
//	     ▼
//	## 🔴 Bugs Potenciales / Condiciones de Carrera / Fugas de Recursos
//	## 🟡 Seguridad, Manejo de Errores y Casos Borde
//	## 🟢 Sugerencias de Refactorización Idiomática

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	openai "github.com/sashabaranov/go-openai"

	"mkd/internal/ai"
	"mkd/internal/git"
)

// reviewTimeout acota la revisión completa (diffs enormes, modelos lentos).
const reviewTimeout = 10 * time.Minute

// startReview recolecta el diff y lanza el streaming del code review.
//
// base vacía → revisa staged+unstaged (cambios actuales).
func (m Model) startReview(base string) (Model, tea.Cmd) {
	m.hideSlashMenu()
	if !m.configured() {
		m.notice = "Ningún modelo disponible actualmente."
		return m.openProviderModal()
	}
	if m.streaming || m.waiting {
		m.notice = "Espera a que termine la generación actual."
		return m, nil
	}

	cwd := m.agentCwd()
	if !git.IsRepo(cwd) {
		m.notice = "El directorio actual no es un repositorio git."
		return m, nil
	}

	diff, err := git.DiffBetween(cwd, base)
	if err != nil {
		m.notice = "git diff: " + err.Error()
		return m, nil
	}
	if strings.TrimSpace(diff) == "" {
		m.notice = "No hay cambios para revisar" + suffixIf(base, " contra "+base) + "."
		return m, nil
	}

	files := git.FilesFromDiff(diff)
	prompt := git.ReviewPrompt(diff, files, base)

	header := "Revisión de código (" + itoa(len(files)) + " archivo(s))"
	if base != "" {
		header += " contra " + base
	}
	display := "🔍 **" + header + "**\n\n_Revisando cambios con contexto -U5…_"
	message := chatMessage{Role: ai.RoleUser, Content: prompt, Display: display}
	m.history = append(m.history, message)
	m.blocks = append(m.blocks, m.renderMessage(message))

	m.notes = nil
	m.stream.Reset()
	m.waiting = true
	m.streaming = false
	m.cancelled = false
	m.reviewing = true
	m.notice = ""

	ctx, cancel := context.WithTimeout(context.Background(), reviewTimeout)
	m.reviewCancel = cancel
	m.cancel = cancel

	m.syncSession()
	m.refresh()
	m.viewport.GotoBottom()

	return m, tea.Batch(startReviewStream(ctx, m.client, prompt), m.spinner.Tick)
}

// startReviewStream abre el stream con el prompt del review como mensaje único.
func startReviewStream(ctx context.Context, client *ai.Client, prompt string) tea.Cmd {
	return func() tea.Msg {
		messages := []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "Eres un Senior Code Reviewer. Sigues la estructura pedida y citas archivo:línea.",
			},
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		}
		ch, err := client.StreamMessages(ctx, messages)
		return streamStartedMsg{ch: ch, err: err}
	}
}

// finishReview limpia las banderas de review y encadena la auto-compactación.
func (m *Model) finishReview() {
	m.reviewing = false
	if m.reviewCancel != nil {
		m.reviewCancel()
		m.reviewCancel = nil
	}
}

func suffixIf(value, suffix string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return suffix
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
