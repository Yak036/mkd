package claude

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func TestConvertOpenAIToAnthropic(t *testing.T) {
	systemPrompt := "Eres un asistente experto."
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: "Regla adicional: responde breve.",
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: "Hola, ¿cómo estás?",
		},
		{
			Role:    openai.ChatMessageRoleAssistant,
			Content: "Muy bien, ¿en qué te ayudo?",
			ToolCalls: []openai.ToolCall{
				{
					ID:   "call_123",
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      "read_file",
						Arguments: `{"path":"main.go"}`,
					},
				},
			},
		},
		{
			Role:       openai.ChatMessageRoleTool,
			ToolCallID: "call_123",
			Content:    "package main\n\nfunc main() {}",
		},
	}

	tools := []openai.Tool{
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "read_file",
				Description: "Lee un archivo del disco",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{"type": "string"},
					},
					"required": []string{"path"},
				},
			},
		},
	}

	sys, anthropicMsgs, anthropicTools := ConvertOpenAIToAnthropic(systemPrompt, messages, tools)

	// Verificar system prompt combinado
	if !strings.Contains(sys, "Eres un asistente experto.") || !strings.Contains(sys, "Regla adicional") {
		t.Errorf("System prompt incompleto: %q", sys)
	}

	// Verificar alternancia estricta y cantidad de mensajes
	if len(anthropicMsgs) < 2 {
		t.Fatalf("Se esperaban al menos 2 mensajes, se obtuvieron %d", len(anthropicMsgs))
	}

	if anthropicMsgs[0].Role != "user" {
		t.Errorf("El primer mensaje debe tener rol 'user', tiene %s", anthropicMsgs[0].Role)
	}

	// Verificar que tool_use se haya generado en el mensaje del asistente
	assistantFound := false
	for _, m := range anthropicMsgs {
		if m.Role == "assistant" {
			assistantFound = true
			hasToolUse := false
			for _, b := range m.Content {
				if b.Type == "tool_use" && b.ID == "call_123" && b.Name == "read_file" {
					hasToolUse = true
				}
			}
			if !hasToolUse {
				t.Errorf("No se encontró el bloque tool_use en el mensaje assistant")
			}
		}
	}
	if !assistantFound {
		t.Errorf("No se encontró mensaje con rol assistant")
	}

	// Verificar herramienta convertida
	if len(anthropicTools) != 1 {
		t.Fatalf("Se esperaba 1 herramienta, se encontraron %d", len(anthropicTools))
	}
	if anthropicTools[0].Name != "read_file" {
		t.Errorf("Nombre de tool incorrecto: %s", anthropicTools[0].Name)
	}
}

func TestProcessSSEStream(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","model":"claude-3-7-sonnet-20250219"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"¡Hola"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" Mundo!"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_abc","name":"bash"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_stop
data: {"type":"message_stop"}
`

	reader := io.NopCloser(strings.NewReader(sseData))
	events := make(chan StreamEvent, 10)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go processSSEStream(ctx, reader, events)

	var textContent strings.Builder
	var toolCalls []openai.ToolCall

	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("Error inesperado en SSE: %v", ev.Err)
		}
		if ev.DeltaContent != "" {
			textContent.WriteString(ev.DeltaContent)
		}
		if len(ev.ToolCalls) > 0 {
			toolCalls = append(toolCalls, ev.ToolCalls...)
		}
	}

	if textContent.String() != "¡Hola Mundo!" {
		t.Errorf("Texto reconstruido incorrecto: %q", textContent.String())
	}

	if len(toolCalls) != 1 {
		t.Fatalf("Se esperaba 1 tool call, se obtuvieron %d", len(toolCalls))
	}
	if toolCalls[0].ID != "call_abc" || toolCalls[0].Function.Name != "bash" {
		t.Errorf("Tool call incorrecta: %+v", toolCalls[0])
	}
	if toolCalls[0].Function.Arguments != `{"command":"ls"}` {
		t.Errorf("Argumentos incorrectos: %s", toolCalls[0].Function.Arguments)
	}
}

func TestFetchModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"claude-3-7-sonnet-20250219","display_name":"Claude 3.7"},{"id":"claude-3-5-haiku-20241022","display_name":"Claude 3.5 Haiku"}]}`)
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	models, err := FetchModels(ctx, ts.URL, "sk-ant-test-token")
	if err != nil {
		t.Fatalf("FetchModels falló: %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("Se esperaban 2 modelos, se obtuvieron %d: %v", len(models), models)
	}
	if models[0] != "claude-3-5-haiku-20241022" || models[1] != "claude-3-7-sonnet-20250219" {
		t.Errorf("Modelos ordenados incorrectamente: %v", models)
	}
}

func TestSaveAndLoadLocalAuth(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	bundle := TokenBundle{
		AccessToken:  "sk-ant-oat01-test-token",
		RefreshToken: "refresh-test-token",
		ExpiresAt:    time.Now().Unix() + 3600,
	}

	if err := SaveLocalAuth(bundle); err != nil {
		t.Fatalf("SaveLocalAuth falló: %v", err)
	}

	loaded, err := LoadLocalAuth()
	if err != nil {
		t.Fatalf("LoadLocalAuth falló: %v", err)
	}

	if loaded.AccessToken != bundle.AccessToken {
		t.Errorf("AccessToken no coincide: %s vs %s", loaded.AccessToken, bundle.AccessToken)
	}
	if loaded.RefreshToken != bundle.RefreshToken {
		t.Errorf("RefreshToken no coincide: %s vs %s", loaded.RefreshToken, bundle.RefreshToken)
	}

	// Verificar permisos 0600
	info, err := os.Stat(filepath.Join(tmpDir, ".mkd", "claude_auth.json"))
	if err != nil {
		t.Fatalf("no se pudo consultar archivo guardado: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("permisos incorrectos: %o (se esperaba 0600)", perm)
	}
}
