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
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// codexProxyFunc devuelve la URL del proxy a utilizar, evitando enviar tráfico loopback
// (como tests con httptest.Server) al proxy externo y detectando automáticamente
// si el túnel SOCKS5 local de Codex (127.0.0.1:10808) está activo.
func codexProxyFunc(req *http.Request) (*url.URL, error) {
	host := req.URL.Hostname()
	if host == "localhost" || host == "127.0.0.1" || strings.HasPrefix(host, "127.") || host == "::1" {
		return nil, nil
	}

	if envProxy, err := http.ProxyFromEnvironment(req); err == nil && envProxy != nil {
		return envProxy, nil
	}

	proxyHost := os.Getenv("CODEX_PROXY_HOST")
	if proxyHost == "" {
		proxyHost = "127.0.0.1"
	}
	proxyPort := os.Getenv("CODEX_PROXY_PORT")
	if proxyPort == "" {
		proxyPort = "10808"
	}
	addr := net.JoinHostPort(proxyHost, proxyPort)
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err == nil {
		conn.Close()
		return url.Parse("socks5://" + addr)
	}
	return nil, nil
}

// codexHTTPClient construye un cliente HTTP con soporte para proxy y timeout.
func codexHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: codexProxyFunc,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}
}

// LoadLocalModelsCache lee los modelos sincronizados en ~/.codex/models_cache.json si existe.
func LoadLocalModelsCache() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".codex", "models_cache.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var mr modelsResponse
	if err := json.Unmarshal(data, &mr); err != nil {
		return nil, err
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
		return nil, errors.New("no se encontraron modelos en models_cache.json")
	}
	sort.Strings(models)
	return models, nil
}

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

	reqURL := fmt.Sprintf("%s/models?client_version=0.165.0", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("User-Agent", "codex-cli/0.165.0")
	if strings.TrimSpace(accountID) != "" {
		req.Header.Set("ChatGPT-Account-Id", strings.TrimSpace(accountID))
	}

	client := codexHTTPClient()
	resp, err := client.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var mr modelsResponse
		if err := json.Unmarshal(body, &mr); err == nil {
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
			if len(models) > 0 {
				sort.Strings(models)
				return models, nil
			}
		}
	}
	if resp != nil {
		resp.Body.Close()
	}

	// Fallback 1: Si falló la consulta remota, leer caché local sincronizado en ~/.codex/models_cache.json
	if cached, err := LoadLocalModelsCache(); err == nil && len(cached) > 0 {
		return cached, nil
	}

	// Fallback 2: Modelos base garantizados de Codex
	models := []string{
		"gpt-6.1-sol",
		"gpt-6.1-terra",
		"gpt-6-astra",
		"gpt-6-sol",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-5.3-codex",
		"gpt-4o",
		"o3-mini",
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
	httpReq.Header.Set("User-Agent", "codex-cli/0.165.0")
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(req.AccessToken))
	if strings.TrimSpace(req.AccountID) != "" {
		httpReq.Header.Set("ChatGPT-Account-Id", strings.TrimSpace(req.AccountID))
	}

	client := codexHTTPClient()
	resp, err := client.Do(httpReq)
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
