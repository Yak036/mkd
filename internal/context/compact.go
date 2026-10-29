package context

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Yak036/mkd/internal/ai"
)

const (
	// DefaultCompactThreshold es el umbral por defecto de auto-compactación.
	DefaultCompactThreshold = 60000
	// CompactKeepRecent son los mensajes recientes que nunca se compactan.
	CompactKeepRecent = 4
	// compactRatio es la fracción más antigua del historial que se resume.
	compactRatio = 0.70
	// MaxFileBytes es el límite de tamaño por archivo anclado (1 MiB).
	MaxFileBytes = 1 << 20
)

const compactSystemPrompt = `Eres un compresor de contexto técnico. Resume la conversación en Markdown denso y factual.
Conserva: decisiones tomadas y su justificación, archivos y rutas modificadas, fragmentos de código clave,
comandos ejecutados, errores pendientes y acuerdos con el usuario. Elimina cortesías y repeticiones.
No inventes información que no aparezca en la conversación.`

// CompactResult describe el resultado de una compactación.
type CompactResult struct {
	Summary      string
	BeforeTokens int
	AfterTokens  int
	Removed      int
}

// Freed devuelve los tokens liberados.
func (r CompactResult) Freed() int {
	freed := r.BeforeTokens - r.AfterTokens
	if freed < 0 {
		return 0
	}
	return freed
}

// Compact resume el 70% más antiguo del historial y conserva intactos los
// últimos CompactKeepRecent mensajes.
//
//	antes:  [M1 M2 M3 M4 M5 M6 M7 M8 M9 M10]
//	después:                       [M7 M8 M9 M10]  + summary="…M1..M6…"
//
// El resumen no se inserta como mensaje del historial: Manager.Prepare lo
// inyecta como bloque <context_summary> una sola vez. Duplicarlo en ambos
// sitios gastaba ventana y desorientaba al modelo.
func (m *Manager) Compact(ctx context.Context, client *ai.Client, summary string, history []ai.Message) ([]ai.Message, string, CompactResult, error) {
	if client == nil || !client.Ready() {
		return history, summary, CompactResult{}, ai.ErrNotConfigured
	}

	split := compactSplit(len(history))
	if split <= 0 {
		return history, summary, CompactResult{
			BeforeTokens: historyTokens(history),
			AfterTokens:  historyTokens(history),
		}, fmt.Errorf("no hay suficientes mensajes para compactar")
	}

	older := history[:split]
	recent := history[split:]
	before := historyTokens(history[:])

	reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	input := make([]ai.Message, 0, len(older)+1)
	if strings.TrimSpace(summary) != "" {
		input = append(input, ai.Message{
			Role:    ai.RoleUser,
			Content: "Resumen previo acumulado:\n" + summary,
		})
	}
	input = append(input, older...)
	input = append(input, ai.Message{
		Role:    ai.RoleUser,
		Content: "Resume el bloque anterior en un contexto técnico denso y auto-suficiente.",
	})

	text, err := client.Complete(reqCtx, compactSystemPrompt, input)
	if err != nil {
		return history, summary, CompactResult{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return history, summary, CompactResult{}, fmt.Errorf("el modelo devolvió un resumen vacío")
	}

	compacted := append([]ai.Message(nil), recent...)

	after := historyTokens(compacted)
	return compacted, text, CompactResult{
		Summary:      text,
		BeforeTokens: before,
		AfterTokens:  after,
		Removed:      split,
	}, nil
}
