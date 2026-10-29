// Package codex proporciona el cliente de transporte para la API de suscripción de OpenAI Codex.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// StreamEvent representa un fragmento de texto o llamada a herramienta en tiempo real.
type StreamEvent struct {
	DeltaContent string
	ToolCalls    []openai.ToolCall
	Err          error
}

// Request agrupa los datos necesarios para una invocación a la API de Codex.
type Request struct {
	BaseURL      string
	AccessToken  string
	AccountID    string
	Model        string
	Instructions string
	Messages     []openai.ChatCompletionMessage
	Tools        []openai.Tool
}

// modelsResponse estructura la respuesta del endpoint GET /backend-api/codex/models.
type modelsResponse struct {
	Models []struct {
		Slug        string `json:"slug"`
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"models"`
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// FetchModels consulta los modelos disponibles para la suscripción activa.
func FetchModels(ctx context.Context, baseURL, accessToken, accountID string) ([]string, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	reqURL := fmt.Sprintf("%s/models?client_version=0.1.0", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	if strings.TrimSpace(accountID) != "" {
		req.Header.Set("ChatGPT-Account-Id", strings.TrimSpace(accountID))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error al consultar modelos de Codex: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error al listar modelos de Codex (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var mr modelsResponse
	if err := json.Unmarshal(body, &mr); err != nil {
		return nil, fmt.Errorf("respuesta inválida de catálogo de modelos: %w", err)
	}

	seen := make(map[string]bool)
	var models []string
	for _, m := range mr.Models {
		id := firstNonEmpty(m.Slug, m.ID)
		if id != "" && !seen[id] {
			seen[id] = true
			models = append(models, id)
		}
	}
	for _, d := range mr.Data {
		if d.ID != "" && !seen[d.ID] {
			seen[d.ID] = true
			models = append(models, d.ID)
		}
	}

	if len(models) == 0 {
		// Modelos base garantizados de Codex si el endpoint responde vacío
		models = []string{"gpt-5.3-codex", "gpt-4o", "o3-mini"}
	}

	sort.Strings(models)
	return models, nil
}

// StreamResponses envía la petición a /responses y devuelve un canal SSE.
func StreamResponses(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if strings.TrimSpace(req.AccessToken) == "" {
		return nil, errors.New("token de acceso no disponible para Codex")
	}

	baseURL := strings.TrimRight(firstNonEmpty(req.BaseURL, DefaultBaseURL), "/")
	endpoint := baseURL + "/responses"

	payload, err := buildResponsesPayload(req)
	if err != nil {
		return nil, fmt.Errorf("error al construir payload de Codex: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(req.AccessToken))
	if strings.TrimSpace(req.AccountID) != "" {
		httpReq.Header.Set("ChatGPT-Account-Id", strings.TrimSpace(req.AccountID))
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("error al conectar con backend de Codex: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API Codex error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	out := make(chan StreamEvent, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()

		reader := bufio.NewReader(resp.Body)
		var currentEvent string
		var accumulatedCalls []openai.ToolCall

		for {
			lineBytes, err := reader.ReadBytes('\n')
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
					sendEvent(ctx, out, StreamEvent{Err: err})
				}
				return
			}

			line := strings.TrimSpace(string(lineBytes))
			if line == "" {
				currentEvent = ""
				continue
			}

			if strings.HasPrefix(line, "event:") {
				currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}

			if strings.HasPrefix(line, "data:") {
				dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if dataStr == "[DONE]" {
					return
				}

				switch currentEvent {
				case "response.output_text.delta":
					var delta struct {
						Delta string `json:"delta"`
					}
					if err := json.Unmarshal([]byte(dataStr), &delta); err == nil && delta.Delta != "" {
						if !sendEvent(ctx, out, StreamEvent{DeltaContent: delta.Delta}) {
							return
						}
					}

				case "response.output_item.done":
					var itemWrap struct {
						Item struct {
							Type      string `json:"type"`
							CallID    string `json:"call_id"`
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"item"`
					}
					if err := json.Unmarshal([]byte(dataStr), &itemWrap); err == nil {
						if itemWrap.Item.Type == "function_call" {
							call := openai.ToolCall{
								ID:   itemWrap.Item.CallID,
								Type: openai.ToolTypeFunction,
								Function: openai.FunctionCall{
									Name:      itemWrap.Item.Name,
									Arguments: itemWrap.Item.Arguments,
								},
							}
							accumulatedCalls = append(accumulatedCalls, call)
						}
					}

				case "response.completed":
					if len(accumulatedCalls) > 0 {
						sendEvent(ctx, out, StreamEvent{ToolCalls: accumulatedCalls})
					}
					return

				case "error", "response.failed":
					var errWrap struct {
						Error struct {
							Message string `json:"message"`
						} `json:"error"`
					}
					msg := dataStr
					if err := json.Unmarshal([]byte(dataStr), &errWrap); err == nil && errWrap.Error.Message != "" {
						msg = errWrap.Error.Message
					}
					sendEvent(ctx, out, StreamEvent{Err: errors.New(msg)})
					return
				}
			}
		}
	}()

	return out, nil
}

func sendEvent(ctx context.Context, out chan<- StreamEvent, ev StreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// buildResponsesPayload traduce la conversación de MKD al esquema Responses API.
func buildResponsesPayload(req Request) ([]byte, error) {
	instructions := req.Instructions
	var inputItems []interface{}

	for _, msg := range req.Messages {
		switch msg.Role {
		case openai.ChatMessageRoleSystem:
			if instructions == "" {
				instructions = msg.Content
			} else {
				instructions += "\n" + msg.Content
			}

		case openai.ChatMessageRoleUser:
			inputItems = append(inputItems, map[string]interface{}{
				"type": "message",
				"role": "user",
				"content": []map[string]string{
					{
						"type": "input_text",
						"text": msg.Content,
					},
				},
			})

		case openai.ChatMessageRoleAssistant:
			if msg.Content != "" {
				inputItems = append(inputItems, map[string]interface{}{
					"type": "message",
					"role": "assistant",
					"content": []map[string]string{
						{
							"type": "output_text",
							"text": msg.Content,
						},
					},
				})
			}
			for _, tc := range msg.ToolCalls {
				inputItems = append(inputItems, map[string]interface{}{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				})
			}

		case openai.ChatMessageRoleTool:
			inputItems = append(inputItems, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Content,
			})
		}
	}

	var tools []interface{}
	for _, t := range req.Tools {
		if t.Type == openai.ToolTypeFunction && t.Function != nil {
			tools = append(tools, map[string]interface{}{
				"type":        "function",
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  t.Function.Parameters,
			})
		}
	}

	bodyMap := map[string]interface{}{
		"model":        req.Model,
		"instructions": instructions,
		"stream":       true,
		"store":        false,
		"input":        inputItems,
	}
	if len(tools) > 0 {
		bodyMap["tools"] = tools
	}

	return json.Marshal(bodyMap)
}
