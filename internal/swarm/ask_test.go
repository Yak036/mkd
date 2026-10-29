package swarm

import (
	"context"
	"strings"
	"testing"

	"github.com/Yak036/mkd/internal/agent"
)

// El orquestador reenvía ask_user al hook de la UI y devuelve su respuesta.
func TestHandleAskUserUsesHook(t *testing.T) {
	var got agent.Question
	o := New(Config{
		AskUser: func(_ context.Context, question agent.Question) (string, bool) {
			got = question
			return "Redis", false
		},
	})

	result := o.HandleToolCall(context.Background(), agent.Call{
		ID:        "call-1",
		Name:      agent.ToolAskUser,
		Arguments: `{"question":"¿Qué caché?"}`,
	})

	if got.Question != "¿Qué caché?" {
		t.Fatalf("pregunta = %q", got.Question)
	}
	if !strings.Contains(result, "Redis") {
		t.Fatalf("resultado = %q", result)
	}
}

func TestHandleAskUserWithoutHook(t *testing.T) {
	o := New(Config{})
	result := o.HandleToolCall(context.Background(), agent.Call{
		ID:        "call-1",
		Name:      agent.ToolAskUser,
		Arguments: `{"question":"¿Qué caché?"}`,
	})
	if !strings.Contains(result, "No hay interactividad") {
		t.Fatalf("resultado = %q", result)
	}
}

func TestHandleAskUserInvalidArguments(t *testing.T) {
	o := New(Config{})
	result := o.HandleToolCall(context.Background(), agent.Call{
		ID:        "call-1",
		Name:      agent.ToolAskUser,
		Arguments: `{"question":""}`,
	})
	if !strings.HasPrefix(result, "error:") {
		t.Fatalf("resultado = %q", result)
	}
}
