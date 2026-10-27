// Package agent define las herramientas (tool calling) que el modelo puede
// invocar sobre el proyecto y el ciclo humano-en-el-medio (HITL).
//
// El paquete es deliberadamente independiente de la TUI: construye propuestas
// a partir de una llamada del modelo, calcula diffs y solo ejecuta cuando la
// capa de UI lo autoriza.
//
//	Ciclo de una herramienta (agent loop):
//
//	     modelo ──tool_call──► ParseCalls ──► BuildProposal ──► Proposal
//	                                                              │
//	                  ┌───────────────────────────────────────────┤
//	                  │ auto-aprobada (read/list/search)          │ requiere permiso
//	                  ▼                                           ▼
//	              Execute ◄── autorización (Y/A) ── diff modal ──► rechazo
//	                  │                                           │
//	                  ▼                                           ▼
//	          ResultMessage(callID, out)               RejectedMessage(callID)
//	                  │                                           │
//	                  └────────────► mensajes role=tool ◄─────────┘
//	                                        │
//	                                  nueva petición al modelo
//
// Las rutas se resuelven siempre dentro del cwd con SafePath: ninguna
// herramienta puede leer, escribir ni ejecutar fuera del proyecto.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// RejectedContent es el texto que ve el modelo tras un rechazo humano.
const RejectedContent = "Operación rechazada por el usuario"

// Call es una invocación de herramienta ya normalizada (sin punteros del SDK).
type Call struct {
	ID        string
	Name      string
	Arguments string
}

// ParseCalls convierte la lista cruda del SDK en llamadas propias.
func ParseCalls(toolCalls []openai.ToolCall) []Call {
	if len(toolCalls) == 0 {
		return nil
	}
	calls := make([]Call, 0, len(toolCalls))
	for _, tc := range toolCalls {
		calls = append(calls, Call{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return calls
}

// ResultMessage construye el mensaje role=tool con la salida de una herramienta.
func ResultMessage(callID, content string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role:       openai.ChatMessageRoleTool,
		Content:    content,
		ToolCallID: callID,
	}
}

// RejectedMessage construye el mensaje role=tool para una operación rechazada
// por el usuario. Se devuelve al modelo para que sepa que no hubo cambios.
func RejectedMessage(callID string) openai.ChatCompletionMessage {
	return ResultMessage(callID, RejectedContent)
}

// ArgumentError envuelve un fallo de argumentos para que la UI lo devuelva al
// modelo en lugar de abortar el turno: el modelo puede corregir y reintentar.
type ArgumentError struct {
	Tool string
	Err  error
}

func (e ArgumentError) Error() string {
	return fmt.Sprintf("argumentos inválidos para %s: %v", e.Tool, e.Err)
}

func (e ArgumentError) Unwrap() error { return e.Err }

// IsArgumentError indica si un error vino de parsear los argumentos.
func IsArgumentError(err error) bool {
	var target ArgumentError
	return errors.As(err, &target)
}

// DecodeArguments decodifica los argumentos de una llamada en el destino
// dado. Devuelve ArgumentError para que el llamador pueda reenviarlo al modelo.
func DecodeArguments(call Call, dst any) error {
	if strings.TrimSpace(call.Arguments) == "" {
		return ArgumentError{Tool: call.Name, Err: errors.New("faltan argumentos JSON")}
	}
	if err := json.Unmarshal([]byte(call.Arguments), dst); err != nil {
		return ArgumentError{Tool: call.Name, Err: err}
	}
	return nil
}
