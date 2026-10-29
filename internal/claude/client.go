// Package claude implementa el cliente de comunicación con la Messages API de Anthropic Claude.
package claude

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
	"sort"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// claudeProxyFunc resuelve la URL del proxy para Claude, evitando loopback y detectando
// el túnel SOCKS5 local si está disponible.
func claudeProxyFunc(req *http.Request) (*url.URL, error) {
	host := req.URL.Hostname()
	if host == "localhost" || host == "127.0.0.1" || strings.HasPrefix(host, "127.") || host == "::1" {
		return nil, nil
	}

	if envProxy, err := http.ProxyFromEnvironment(req); err == nil && envProxy != nil {
		return envProxy, nil
	}

	proxyHost := os.Getenv("CLAUDE_PROXY_HOST")
	if proxyHost == "" {
		proxyHost = os.Getenv("CODEX_PROXY_HOST")
	}
	if proxyHost == "" {
		proxyHost = "127.0.0.1"
	}
	proxyPort := os.Getenv("CLAUDE_PROXY_PORT")
	if proxyPort == "" {
		proxyPort = os.Getenv("CODEX_PROXY_PORT")
	}
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

// claudeHTTPClient construye el cliente HTTP con soporte para proxy y timeout.
func claudeHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: claudeProxyFunc,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   90 * time.Second,
	}
}

// StreamEvent representa un fragmento de texto o llamada a herramienta en tiempo real.
type StreamEvent struct {
	DeltaContent string
	ToolCalls    []openai.ToolCall
	Err          error
}

// Request contiene todos los parámetros necesarios para una llamada a la API de Claude.
type Request struct {
	BaseURL      string
	AccessToken  string
	Model        string
	SystemPrompt string
	Messages     []openai.ChatCompletionMessage
	Tools        []openai.Tool
	MaxTokens    int
}

// ContentBlock representa un bloque de contenido en el protocolo Messages de Anthropic.
type ContentBlock struct {
	Type      string                 `json:"type"` // "text", "tool_use", "tool_result"
	Text      string                 `json:"text,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     map[string]interface{} `json:"input,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Content   string                 `json:"content,omitempty"`
}

// MessageParam representa un mensaje en la Messages API de Anthropic.
type MessageParam struct {
	Role    string         `json:"role"` // "user" o "assistant"
	Content []ContentBlock `json:"content"`
}

// ToolParam define el esquema de una herramienta en Anthropic.
type ToolParam struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// modelsListResponse representa la respuesta de GET /v1/models de Anthropic.
type modelsListResponse struct {
	Data []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Type        string `json:"type"`
	} `json:"data"`
}

// FetchModels consulta el catálogo de modelos disponibles en Anthropic Claude.
func FetchModels(ctx context.Context, baseURL, accessToken string) ([]string, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	reqURL := fmt.Sprintf("%s/models", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err == nil {
		setAuthHeaders(req, accessToken)
		client := claudeHTTPClient()
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			var mr modelsListResponse
			if err := json.Unmarshal(body, &mr); err == nil && len(mr.Data) > 0 {
				seen := make(map[string]bool)
				var models []string
				for _, m := range mr.Data {
					if m.ID != "" && !seen[m.ID] {
						seen[m.ID] = true
						models = append(models, m.ID)
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
	}

	// Modelos insignia estándar de Claude en caso de catálogo offline o endpoint restringido
	models := []string{
		"claude-3-7-sonnet-20250219",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
		"claude-3-opus-20240229",
	}
	sort.Strings(models)
	return models, nil
}

// setAuthHeaders añade las cabeceras requeridas según si el token es OAuth o API Key.
func setAuthHeaders(req *http.Request, token string) {
	token = strings.TrimSpace(token)
	req.Header.Set("anthropic-version", AnthropicVersion)
	if strings.HasPrefix(token, "sk-ant-oat") {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("anthropic-beta", AnthropicOAuthBetaHeader)
	} else {
		req.Header.Set("x-api-key", token)
	}
}

// ConvertOpenAIToAnthropic transforma el historial de go-openai al formato de Messages API de Anthropic,
// separando instrucciones de sistema y garantizando la alternancia estricta user/assistant.
func ConvertOpenAIToAnthropic(systemPrompt string, messages []openai.ChatCompletionMessage, tools []openai.Tool) (string, []MessageParam, []ToolParam) {
	var systemParts []string
	if strings.TrimSpace(systemPrompt) != "" {
		systemParts = append(systemParts, strings.TrimSpace(systemPrompt))
	}

	var rawParams []MessageParam

	for _, msg := range messages {
		switch msg.Role {
		case openai.ChatMessageRoleSystem:
			if strings.TrimSpace(msg.Content) != "" {
				systemParts = append(systemParts, strings.TrimSpace(msg.Content))
			}

		case openai.ChatMessageRoleAssistant:
			var blocks []ContentBlock
			if strings.TrimSpace(msg.Content) != "" {
				blocks = append(blocks, ContentBlock{
					Type: "text",
					Text: msg.Content,
				})
			}
			for _, tc := range msg.ToolCalls {
				inputMap := make(map[string]interface{})
				if tc.Function.Arguments != "" {
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &inputMap)
				}
				blocks = append(blocks, ContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: inputMap,
				})
			}
			if len(blocks) > 0 {
				rawParams = append(rawParams, MessageParam{
					Role:    "assistant",
					Content: blocks,
				})
			}

		case openai.ChatMessageRoleTool:
			// En Anthropic los resultados de herramientas van dentro de un mensaje con rol "user"
			rawParams = append(rawParams, MessageParam{
				Role: "user",
				Content: []ContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: msg.ToolCallID,
						Content:   msg.Content,
					},
				},
			})

		case openai.ChatMessageRoleUser:
			if strings.TrimSpace(msg.Content) != "" {
				rawParams = append(rawParams, MessageParam{
					Role: "user",
					Content: []ContentBlock{
						{
							Type: "text",
							Text: msg.Content,
						},
					},
				})
			}
		}
	}

	// Consolidar mensajes consecutivos con el mismo rol (Anthropic requiere alternancia estricta)
	var consolidated []MessageParam
	for _, p := range rawParams {
		if len(consolidated) > 0 && consolidated[len(consolidated)-1].Role == p.Role {
			consolidated[len(consolidated)-1].Content = append(consolidated[len(consolidated)-1].Content, p.Content...)
		} else {
			consolidated = append(consolidated, p)
		}
	}

	// Anthropic exige que el primer mensaje tenga rol "user"
	if len(consolidated) > 0 && consolidated[0].Role != "user" {
		consolidated = append([]MessageParam{
			{
				Role: "user",
				Content: []ContentBlock{
					{Type: "text", Text: "Continuar."},
				},
			},
		}, consolidated...)
	}

	// Mapear tools de OpenAI a Anthropic
	var anthropicTools []ToolParam
	for _, t := range tools {
		if t.Type != openai.ToolTypeFunction || t.Function == nil {
			continue
		}
		schemaMap := make(map[string]interface{})
		if t.Function.Parameters != nil {
			if b, err := json.Marshal(t.Function.Parameters); err == nil {
				_ = json.Unmarshal(b, &schemaMap)
			}
		}
		if len(schemaMap) == 0 {
			schemaMap = map[string]interface{}{
				"type": "object",
			}
		}
		anthropicTools = append(anthropicTools, ToolParam{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: schemaMap,
		})
	}

	return strings.Join(systemParts, "\n\n"), consolidated, anthropicTools
}

// anthropicRequestBody estructura el payload JSON enviado a /v1/messages.
type anthropicRequestBody struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system,omitempty"`
	Messages  []MessageParam `json:"messages"`
	Stream    bool           `json:"stream"`
	Tools     []ToolParam    `json:"tools,omitempty"`
}

// StreamMessages envía la conversación a Anthropic Messages API y procesa el streaming SSE.
func StreamMessages(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if strings.TrimSpace(req.AccessToken) == "" {
		return nil, errors.New("token de acceso de Claude no proporcionado")
	}

	baseURL := strings.TrimRight(req.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	system, messages, tools := ConvertOpenAIToAnthropic(req.SystemPrompt, req.Messages, req.Tools)

	payload := anthropicRequestBody{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  messages,
		Stream:    true,
		Tools:     tools,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("error al serializar payload de Claude: %w", err)
	}

	endpoint := baseURL + "/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	setAuthHeaders(httpReq, req.AccessToken)

	client := claudeHTTPClient()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("error al conectar con API de Claude: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("error de Claude (HTTP %d): %s", resp.StatusCode, string(body))
	}

	events := make(chan StreamEvent, 64)
	go processSSEStream(ctx, resp.Body, events)

	return events, nil
}

// processSSEStream analiza los eventos de SSE emitidos por Anthropic Messages API.
func processSSEStream(ctx context.Context, body io.ReadCloser, events chan<- StreamEvent) {
	defer body.Close()
	defer close(events)

	scanner := bufio.NewScanner(body)
	// Aumentar buffer para respuestas con tool definitions extensas
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	var currentEventType string

	// Estado para acumular llamadas a herramientas durante el streaming
	type toolInProgress struct {
		id   string
		name string
		json strings.Builder
	}
	toolsMap := make(map[int]*toolInProgress)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- StreamEvent{Err: ctx.Err()}
			return
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "event:") {
			currentEventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(data), &raw); err != nil {
			continue
		}

		evType, _ := raw["type"].(string)
		if evType == "" {
			evType = currentEventType
		}

		switch evType {
		case "error":
			errMsg := "error desconocido de Claude"
			if errObj, ok := raw["error"].(map[string]interface{}); ok {
				if msg, ok := errObj["message"].(string); ok {
					errMsg = msg
				}
			}
			events <- StreamEvent{Err: errors.New(errMsg)}
			return

		case "content_block_start":
			idx := 0
			if idxFloat, ok := raw["index"].(float64); ok {
				idx = int(idxFloat)
			}
			if cb, ok := raw["content_block"].(map[string]interface{}); ok {
				cbType, _ := cb["type"].(string)
				if cbType == "tool_use" {
					id, _ := cb["id"].(string)
					name, _ := cb["name"].(string)
					toolsMap[idx] = &toolInProgress{
						id:   id,
						name: name,
					}
				}
			}

		case "content_block_delta":
			idx := 0
			if idxFloat, ok := raw["index"].(float64); ok {
				idx = int(idxFloat)
			}
			if delta, ok := raw["delta"].(map[string]interface{}); ok {
				dType, _ := delta["type"].(string)
				switch dType {
				case "text_delta":
					if text, ok := delta["text"].(string); ok && text != "" {
						events <- StreamEvent{DeltaContent: text}
					}
				case "input_json_delta":
					if partial, ok := delta["partial_json"].(string); ok {
						if tool, ok := toolsMap[idx]; ok {
							tool.json.WriteString(partial)
						}
					}
				}
			}

		case "content_block_stop":
			idx := 0
			if idxFloat, ok := raw["index"].(float64); ok {
				idx = int(idxFloat)
			}
			if tool, ok := toolsMap[idx]; ok {
				// Emitir tool call completada
				tc := openai.ToolCall{
					ID:   tool.id,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tool.name,
						Arguments: tool.json.String(),
					},
				}
				events <- StreamEvent{ToolCalls: []openai.ToolCall{tc}}
				delete(toolsMap, idx)
			}

		case "message_stop":
			return
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		events <- StreamEvent{Err: err}
	}
}
