package agent

// Preguntas interactivas (ask_user).
//
// El agente puede detener su pensamiento para pedir una decisión al usuario y
// continuar el mismo turno con la respuesta:
//
//	modelo ──ask_user──► ParseQuestion ──► UI (modal con opciones + texto libre)
//	                                              │
//	                                              ▼
//	                            Answer.Label / Answer.Text / Answer.Cancelled
//	                                              │
//	                            FormatAnswer ──► role=tool ──► nueva ronda
//
// El paquete solo valida y formatea: mostrar el modal es responsabilidad de la
// capa de UI, igual que el HITL de escritura.

import (
	"fmt"
	"strings"
)

// ToolAskUser es el nombre canónico de la herramienta de preguntas.
const ToolAskUser = "ask_user"

// MaxQuestionOptions acota las alternativas para que el modal quepa en
// cualquier terminal y el modelo no invente menús inmanejables.
const MaxQuestionOptions = 6

// QuestionOption es una alternativa que el usuario puede elegir.
type QuestionOption struct {
	// Label es el texto visible y el valor devuelto al modelo.
	Label string `json:"label"`
	// Description explica cuándo elegirla (opcional).
	Description string `json:"description,omitempty"`
}

// Question es una pregunta que el modelo hace durante el turno.
type Question struct {
	// Header es una etiqueta corta (máx. ~24 caracteres) para el modal.
	Header string `json:"header,omitempty"`
	// Question es el texto de la pregunta.
	Question string `json:"question"`
	// Options son las alternativas sugeridas (puede estar vacío).
	Options []QuestionOption `json:"options,omitempty"`
	// AllowCustom permite escribir una respuesta libre además de las opciones.
	// Si no hay opciones, la respuesta libre es obligatoria.
	AllowCustom bool `json:"allow_custom,omitempty"`
}

// Answer es la respuesta del usuario a una pregunta.
type Answer struct {
	// CallID identifica la llamada que se está resolviendo.
	CallID string
	// Label es la opción elegida (vacío si la respuesta fue libre).
	Label string
	// Text es la respuesta libre escrita por el usuario.
	Text string
	// Cancelled indica que el usuario descartó la pregunta.
	Cancelled bool
}

// Value devuelve la respuesta efectiva (opción o texto libre).
func (a Answer) Value() string {
	if strings.TrimSpace(a.Text) != "" {
		return strings.TrimSpace(a.Text)
	}
	return strings.TrimSpace(a.Label)
}

// ParseQuestion valida los argumentos de ask_user. Un JSON inválido devuelve
// ArgumentError para que el modelo se autocorrija, como el resto de tools.
func ParseQuestion(call Call) (Question, error) {
	var question Question
	if err := DecodeArguments(call, &question); err != nil {
		return Question{}, err
	}
	question.Question = strings.TrimSpace(question.Question)
	if question.Question == "" {
		return Question{}, ArgumentError{Tool: call.Name, Err: fmt.Errorf("la pregunta no puede estar vacía")}
	}

	cleaned := make([]QuestionOption, 0, len(question.Options))
	for _, option := range question.Options {
		label := strings.TrimSpace(option.Label)
		if label == "" {
			continue
		}
		cleaned = append(cleaned, QuestionOption{
			Label:       label,
			Description: strings.TrimSpace(option.Description),
		})
	}
	if len(cleaned) > MaxQuestionOptions {
		cleaned = cleaned[:MaxQuestionOptions]
	}
	question.Options = cleaned

	// El texto libre siempre está disponible: obligatorio sin alternativas y
	// opcional junto a ellas (el usuario puede pensar otra respuesta).
	question.AllowCustom = true
	question.Header = oneLineLabel(question.Header, 24)
	return question, nil
}

// FormatAnswer construye el texto que el modelo recibe como role=tool.
func FormatAnswer(question Question, answer Answer) string {
	if answer.Cancelled {
		return "Pregunta cancelada por el usuario: no hubo respuesta. Continúa con una suposición razonable y dilo explícitamente."
	}

	value := answer.Value()
	if value == "" {
		return "El usuario respondió sin texto. Continúa con una suposición razonable y dilo explícitamente."
	}

	if answer.Label != "" && strings.TrimSpace(answer.Text) == "" {
		return fmt.Sprintf("Respuesta del usuario a %q: %s", question.Question, answer.Label) +
			" (opción seleccionada)."
	}
	return fmt.Sprintf("Respuesta del usuario a %q: %s", question.Question, value)
}

// Summary describe la pregunta para el bloque del chat.
func (q Question) Summary() string {
	var b strings.Builder
	if q.Header != "" {
		b.WriteString("[" + q.Header + "] ")
	}
	b.WriteString(q.Question)
	for _, option := range q.Options {
		b.WriteString("\n  • " + option.Label)
		if option.Description != "" {
			b.WriteString(" — " + option.Description)
		}
	}
	return b.String()
}

// oneLineLabel recorta una etiqueta a n runas, sin saltos de línea.
func oneLineLabel(value string, n int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > n {
		return strings.TrimSpace(string(runes[:n-1])) + "…"
	}
	return value
}
