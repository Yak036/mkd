package antigravity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func TestFormatPrompt(t *testing.T) {
	system := "Eres MKD con Antigravity."
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: "¿Cómo compilar Go?",
		},
		{
			Role:    openai.ChatMessageRoleAssistant,
			Content: "Usa `go build`.",
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: "¿Y para ejecutar tests?",
		},
	}

	prompt := FormatPrompt(system, messages)

	if !strings.Contains(prompt, "Eres MKD con Antigravity.") {
		t.Errorf("El prompt no incluye el system prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "Usuario: ¿Cómo compilar Go?") {
		t.Errorf("El prompt no incluye el turno del usuario: %s", prompt)
	}
	if !strings.Contains(prompt, "Asistente: Usa `go build`.") {
		t.Errorf("El prompt no incluye el turno del asistente: %s", prompt)
	}
	if !strings.Contains(prompt, "Usuario: ¿Y para ejecutar tests?") {
		t.Errorf("El prompt no incluye el segundo turno: %s", prompt)
	}
}

func TestNDJSONParsing(t *testing.T) {
	rawLine := `{"event":"step_update","step_update":{"conversation_id":"abc-123","step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"¡Hola Mundo!"}}`

	var update ndjsonStepUpdate
	if err := json.Unmarshal([]byte(rawLine), &update); err != nil {
		t.Fatalf("Error al deserializar NDJSON: %v", err)
	}

	if update.Event != "step_update" {
		t.Errorf("Evento incorrecto: %s", update.Event)
	}
	if update.StepUpdate == nil {
		t.Fatalf("StepUpdate es nil")
	}
	if update.StepUpdate.TextDelta != "¡Hola Mundo!" {
		t.Errorf("TextDelta incorrecto: %q", update.StepUpdate.TextDelta)
	}
}

func TestFetchModels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	models, err := FetchModels(ctx)
	if err != nil {
		t.Fatalf("FetchModels devolvió error: %v", err)
	}
	if len(models) == 0 {
		t.Fatalf("FetchModels devolvió lista vacía")
	}

	foundGemini := false
	for _, m := range models {
		if strings.HasPrefix(m, "gemini-") {
			foundGemini = true
			break
		}
	}
	if !foundGemini {
		t.Errorf("No se encontró ningún modelo gemini en la lista: %v", models)
	}
}

func TestLoadLocalAuth(t *testing.T) {
	info, err := LoadLocalAuth()
	if err != nil {
		t.Skipf("agy no disponible en este entorno de test: %v", err)
	}

	if !info.IsAvailable {
		t.Errorf("Se esperaba que IsAvailable fuera true")
	}
	if info.AgyPath == "" {
		t.Errorf("AgyPath no debe estar vacío")
	}
	if info.Account == "" {
		t.Errorf("Account no debe estar vacío")
	}
}
