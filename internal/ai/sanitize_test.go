package ai

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func assistantWithCalls(content string, ids ...string) openai.ChatCompletionMessage {
	msg := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: content}
	for _, id := range ids {
		msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
			ID:       id,
			Type:     openai.ToolTypeFunction,
			Function: openai.FunctionCall{Name: "read_file", Arguments: "{}"},
		})
	}
	return msg
}

func TestSanitizeMessagesDropsOrphanToolResults(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "sistema"},
		{Role: openai.ChatMessageRoleUser, Content: "hola"},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "huerfano", Content: "resultado"},
	}
	out := SanitizeMessages(messages)
	if len(out) != 2 {
		t.Fatalf("mensajes = %d, want 2: %#v", len(out), out)
	}
	for _, msg := range out {
		if msg.Role == openai.ChatMessageRoleTool {
			t.Fatalf("quedó un role=tool huérfano: %#v", msg)
		}
	}
}

func TestSanitizeMessagesDropsUnansweredCalls(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "lee dos archivos"},
		assistantWithCalls("voy a leer", "a", "b"),
		{Role: openai.ChatMessageRoleTool, ToolCallID: "a", Content: "contenido A"},
	}
	out := SanitizeMessages(messages)

	if len(out) != 3 {
		t.Fatalf("mensajes = %d, want 3: %#v", len(out), out)
	}
	if len(out[1].ToolCalls) != 1 || out[1].ToolCalls[0].ID != "a" {
		t.Fatalf("tool_calls = %#v, se esperaba solo 'a'", out[1].ToolCalls)
	}
	if out[2].ToolCallID != "a" {
		t.Fatalf("role=tool = %#v", out[2])
	}
}

func TestSanitizeMessagesKeepsTextWhenAllCallsDropped(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hazlo"},
		assistantWithCalls("mi plan es este", "a"),
		{Role: openai.ChatMessageRoleUser, Content: "sigue"},
	}
	out := SanitizeMessages(messages)

	if len(out) != 3 {
		t.Fatalf("mensajes = %d, want 3: %#v", len(out), out)
	}
	if len(out[1].ToolCalls) != 0 {
		t.Fatalf("quedaron tool_calls sin respuesta: %#v", out[1].ToolCalls)
	}
	if out[1].Content != "mi plan es este" || out[1].Role != openai.ChatMessageRoleAssistant {
		t.Fatalf("se perdió el texto del asistente: %#v", out[1])
	}
}

func TestSanitizeMessagesKeepsCompleteTurns(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "sistema"},
		{Role: openai.ChatMessageRoleUser, Content: "hola"},
		assistantWithCalls("", "a"),
		{Role: openai.ChatMessageRoleTool, ToolCallID: "a", Content: "ok"},
		{Role: openai.ChatMessageRoleAssistant, Content: "listo"},
	}
	out := SanitizeMessages(messages)
	if len(out) != len(messages) {
		t.Fatalf("mensajes = %d, want %d", len(out), len(messages))
	}
	if len(out[2].ToolCalls) != 1 {
		t.Fatalf("se perdió una llamada válida: %#v", out[2])
	}
}
