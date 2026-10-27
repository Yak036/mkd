// Package ai encapsula el cliente OpenAI-compatible de MKD.
//
// Un Client representa al proveedor activo y su modelo activo. El streaming se
// expone por canales para que Bubbletea lo consuma con tea.Cmd sin bloquear el
// event loop.
//
//	cmd/root ──► Stream ──► goroutine SSE ──► chan Chunk
//	                                              │
//	                         pipe ◄───────────────┤
//	                         TUI  ◄───────────────┘
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/Yak036/mkd/internal/config"
)

// ErrNotConfigured se devuelve cuando falta proveedor, credenciales o modelo.
var ErrNotConfigured = errors.New("Ningún modelo disponible actualmente: configura un proveedor y una API key con Ctrl+K o elige un modelo con Ctrl+M")

// Roles disponibles en la API de chat.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message es un mensaje del historial.
type Message struct {
	Role    string
	Content string
}

// Chunk es un fragmento de la respuesta en streaming.
type Chunk struct {
	Content string
	Err     error
}

// Client envuelve go-openai para el proveedor y modelo activos.
type Client struct {
	api          *openai.Client
	ProviderKey  string
	ProviderName string
	Model        string
	BaseURL      string
	APIKey       string
	SystemPrompt string
	configured   bool
}

// New construye el cliente sin exigir credenciales. La TUI debe abrirse
// incluso sin configuración para que el usuario la complete desde dentro.
func New(cfg config.Config) (*Client, error) {
	provider, ok := cfg.Active()
	if !ok {
		provider = config.ProviderConfig{}
	}

	apiKey := provider.APIKey
	if apiKey == "" {
		// Ollama y servidores locales ignoran la clave, pero go-openai
		// necesita un valor no vacío para la cabecera Authorization.
		apiKey = "local"
	}

	apiCfg := openai.DefaultConfig(apiKey)
	if provider.BaseURL != "" {
		apiCfg.BaseURL = strings.TrimRight(provider.BaseURL, "/")
	}

	return &Client{
		api:          openai.NewClientWithConfig(apiCfg),
		ProviderKey:  cfg.ActiveProvider,
		ProviderName: provider.Name,
		Model:        provider.ActiveModel,
		BaseURL:      provider.BaseURL,
		APIKey:       provider.APIKey,
		SystemPrompt: cfg.SystemPrompt,
		configured:   provider.Configured(),
	}, nil
}

// NewForProfile construye un cliente para un sub-agente: hereda el proveedor y
// modelo activos salvo que el perfil sobreescriba provider y/o model.
//
// Un Model explícito se aplica sobre el proveedor elegido (el mismo endpoint y
// credenciales), lo que permite, por ejemplo, asignar un coder a un modelo más
// potente sin duplicar la API key.
func NewForProfile(cfg config.Config, providerKey, model string) (*Client, error) {
	providerKey = strings.TrimSpace(providerKey)
	if providerKey != "" {
		if _, ok := cfg.Providers[providerKey]; ok {
			cfg.ActiveProvider = providerKey
		}
	}
	cfg.Normalize()

	provider, _ := cfg.Active()
	if strings.TrimSpace(model) != "" {
		provider.ActiveModel = strings.TrimSpace(model)
		cfg.Providers[cfg.ActiveProvider] = provider
	}
	return New(cfg)
}

// Ready indica si hay endpoint, modelo y credenciales para operar.
func (c *Client) Ready() bool {
	return c.configured
}

// Stream lanza la petición y devuelve un canal con la respuesta incremental.
func (c *Client) Stream(ctx context.Context, system string, history []Message) (<-chan Chunk, error) {
	return c.StreamMessages(ctx, c.messages(system, history))
}

// StreamMessages lanza el streaming con un slice de mensajes ya ensamblado
// (system, archivos anclados, resumen e historial en orden estable).
func (c *Client) StreamMessages(ctx context.Context, messages []openai.ChatCompletionMessage) (<-chan Chunk, error) {
	if !c.Ready() {
		return nil, ErrNotConfigured
	}

	stream, err := c.api.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:    c.Model,
		Messages: messages,
		Stream:   true,
	})
	if err != nil {
		return nil, fmt.Errorf("API %s (%s): %w", c.Model, c.ProviderName, normalizeError(err))
	}

	out := make(chan Chunk, 64)
	go func() {
		defer close(out)
		defer stream.Close()

		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				sendChunk(ctx, out, Chunk{Err: normalizeError(err)})
				return
			}

			for _, choice := range response.Choices {
				if choice.Delta.Content == "" {
					continue
				}
				if !sendChunk(ctx, out, Chunk{Content: choice.Delta.Content}) {
					return
				}
			}
		}
	}()

	return out, nil
}

// StreamWithTools lanza el streaming habilitando tool calling. Devuelve el
// canal normal de contenido, pero debe usarse junto con CompleteMessagesTools
// cuando el modelo responde con tool_calls: el streaming no expone los
// argumentos fragmentados de forma cómoda para la UI.
func (c *Client) StreamWithTools(ctx context.Context, messages []openai.ChatCompletionMessage, tools []openai.Tool) (<-chan Chunk, error) {
	if !c.Ready() {
		return nil, ErrNotConfigured
	}

	stream, err := c.api.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:    c.Model,
		Messages: messages,
		Stream:   true,
		Tools:    tools,
	})
	if err != nil {
		return nil, fmt.Errorf("API %s (%s): %w", c.Model, c.ProviderName, normalizeError(err))
	}

	out := make(chan Chunk, 64)
	go func() {
		defer close(out)
		defer stream.Close()

		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				sendChunk(ctx, out, Chunk{Err: normalizeError(err)})
				return
			}
			for _, choice := range response.Choices {
				if choice.Delta.Content == "" {
					continue
				}
				if !sendChunk(ctx, out, Chunk{Content: choice.Delta.Content}) {
					return
				}
			}
		}
	}()

	return out, nil
}

// ToolResponse es el resultado de una ronda con herramientas.
type ToolResponse struct {
	// Content es el texto que el modelo ya emitía antes de pedir herramientas.
	Content string
	// Calls contiene las llamadas solicitadas (vacío si terminó el turno).
	Calls []openai.ToolCall
}

// CompleteMessagesTools hace una petición no streaming con herramientas y
// devuelve el mensaje completo del asistente para inspeccionar ToolCalls.
func (c *Client) CompleteMessagesTools(ctx context.Context, messages []openai.ChatCompletionMessage, tools []openai.Tool) (openai.ChatCompletionMessage, error) {
	if !c.Ready() {
		return openai.ChatCompletionMessage{}, ErrNotConfigured
	}
	response, err := c.api.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    c.Model,
		Messages: messages,
		Tools:    tools,
	})
	if err != nil {
		return openai.ChatCompletionMessage{}, fmt.Errorf("API %s (%s): %w", c.Model, c.ProviderName, normalizeError(err))
	}
	if len(response.Choices) == 0 {
		return openai.ChatCompletionMessage{}, errors.New("el proveedor devolvió una respuesta vacía")
	}
	return response.Choices[0].Message, nil
}

// Complete hace una petición no streaming (usada por /compact).
func (c *Client) Complete(ctx context.Context, system string, history []Message) (string, error) {
	return c.CompleteMessages(ctx, c.messages(system, history))
}

// CompleteMessages hace una petición no streaming con mensajes ya ensamblados.
func (c *Client) CompleteMessages(ctx context.Context, messages []openai.ChatCompletionMessage) (string, error) {
	if !c.Ready() {
		return "", ErrNotConfigured
	}

	response, err := c.api.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    c.Model,
		Messages: messages,
	})
	if err != nil {
		return "", fmt.Errorf("API %s (%s): %w", c.Model, c.ProviderName, normalizeError(err))
	}
	if len(response.Choices) == 0 {
		return "", errors.New("el proveedor devolvió una respuesta vacía")
	}
	return response.Choices[0].Message.Content, nil
}

// FetchAvailableModels consulta GET /v1/models y devuelve IDs ordenados.
//
// Algunos proveedores (DeepInfra) exponen cientos de modelos mezclando imagen,
// embeddings y versiones deprecadas. Si detectamos su catálogo público en
// /models/list lo usamos para quedarnos solo con los de texto activos; si no,
// devolvemos la lista del endpoint OpenAI tal cual.
func (c *Client) FetchAvailableModels(ctx context.Context) ([]string, error) {
	if c.BaseURL == "" || (c.APIKey == "" && !config.IsLocal(c.BaseURL)) {
		return nil, ErrNotConfigured
	}

	if models, ok := c.fetchCuratedModels(ctx); ok {
		return models, nil
	}

	response, err := c.api.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("sincronizar modelos: %w", normalizeError(err))
	}

	models := make([]string, 0, len(response.Models))
	for _, model := range response.Models {
		if strings.TrimSpace(model.ID) != "" {
			models = append(models, model.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}

// curatedModel es una entrada del catálogo público /models/list.
type curatedModel struct {
	Name       string `json:"model_name"`
	Type       string `json:"type"`
	Deprecated int64  `json:"deprecated"`
	Private    int    `json:"private"`
}

// fetchCuratedModels intenta leer /models/list y filtra los modelos de texto
// activos. Devuelve ok=false si el proveedor no expone ese catálogo.
func (c *Client) fetchCuratedModels(ctx context.Context) ([]string, bool) {
	origin := strings.TrimSuffix(c.BaseURL, "/")
	for _, suffix := range []string{"/v1/openai", "/v1", "/openai"} {
		origin = strings.TrimSuffix(origin, suffix)
	}
	if origin == "" {
		return nil, false
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/models/list", nil)
	if err != nil {
		return nil, false
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, false
	}

	var catalog []curatedModel
	if err := json.NewDecoder(response.Body).Decode(&catalog); err != nil || len(catalog) == 0 {
		return nil, false
	}

	now := time.Now().Unix()
	models := make([]string, 0, len(catalog))
	for _, entry := range catalog {
		if strings.TrimSpace(entry.Name) == "" || entry.Private != 0 {
			continue
		}
		if entry.Type != "text-generation" {
			continue
		}
		if entry.Deprecated > 0 && entry.Deprecated <= now {
			continue
		}
		models = append(models, entry.Name)
	}
	if len(models) == 0 {
		return nil, false
	}
	sort.Strings(models)
	return models, true
}

func (c *Client) messages(system string, history []Message) []openai.ChatCompletionMessage {
	system = strings.TrimSpace(system)
	if system == "" {
		system = c.SystemPrompt
	}

	messages := make([]openai.ChatCompletionMessage, 0, len(history)+1)
	if system != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: system,
		})
	}
	for _, msg := range history {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return messages
}

// sendChunk evita bloquear la goroutine si la UI ya se cerró.
func sendChunk(ctx context.Context, out chan<- Chunk, chunk Chunk) bool {
	select {
	case out <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

// normalizeError traduce los errores estructurados del proveedor.
func normalizeError(err error) error {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) && apiErr.Message != "" {
		return fmt.Errorf("%s (HTTP %d)", apiErr.Message, apiErr.HTTPStatusCode)
	}
	return err
}
