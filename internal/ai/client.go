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

	"github.com/Yak036/mkd/internal/antigravity"
	"github.com/Yak036/mkd/internal/claude"
	"github.com/Yak036/mkd/internal/codex"
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

// Client envuelve go-openai y el transporte para el proveedor y modelo activos.
type Client struct {
	api          *openai.Client
	ProviderKey  string
	ProviderName string
	Model        string
	BaseURL      string
	APIKey       string
	RefreshToken string
	AccountID    string
	TokenExpires int64
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
	refreshToken := provider.RefreshToken
	accountID := provider.AccountID
	tokenExpires := provider.TokenExpires

	// Si es proveedor Codex o apunta al backend de Codex
	isCodex := cfg.ActiveProvider == "codex" || config.IsCodex(provider.BaseURL)
	if isCodex {
		// Si no tiene credenciales en config, intenta importar desde ~/.codex/auth.json
		if apiKey == "" && refreshToken == "" {
			if local, err := codex.LoadLocalAuth(); err == nil {
				apiKey = local.AccessToken
				refreshToken = local.RefreshToken
				if accountID == "" {
					accountID = local.AccountID
				}
				if tokenExpires == 0 {
					tokenExpires = local.ExpiresAt
				}
				provider.APIKey = apiKey
				provider.RefreshToken = refreshToken
				provider.AccountID = accountID
				provider.TokenExpires = tokenExpires
				cfg.Providers[cfg.ActiveProvider] = provider
			}
		}
	}

	// Si es proveedor Antigravity (Google agy)
	isAntigravity := cfg.ActiveProvider == "antigravity" || cfg.ActiveProvider == "agy" || config.IsAntigravity(provider.BaseURL)
	if isAntigravity {
		if local, err := antigravity.LoadLocalAuth(); err == nil {
			if accountID == "" {
				accountID = local.Account
			}
			provider.AccountID = accountID
			if provider.BaseURL == "" {
				provider.BaseURL = "local://agy"
			}
			cfg.Providers[cfg.ActiveProvider] = provider
		}
	}
	isClaude := cfg.ActiveProvider == "claude" || config.IsClaude(provider.BaseURL)
	if isClaude {
		// Si no tiene credenciales en config, intenta importar desde archivos locales o variables
		if apiKey == "" && refreshToken == "" {
			if local, err := claude.LoadLocalAuth(); err == nil {
				apiKey = local.AccessToken
				refreshToken = local.RefreshToken
				if tokenExpires == 0 {
					tokenExpires = local.ExpiresAt
				}
				provider.APIKey = apiKey
				provider.RefreshToken = refreshToken
				provider.TokenExpires = tokenExpires
				cfg.Providers[cfg.ActiveProvider] = provider
			}
		}
	}

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
		RefreshToken: provider.RefreshToken,
		AccountID:    provider.AccountID,
		TokenExpires: provider.TokenExpires,
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

// isCodex indica si el cliente utiliza el protocolo de suscripción de Codex.
func (c *Client) isCodex() bool {
	return c.ProviderKey == "codex" || config.IsCodex(c.BaseURL)
}

// ensureCodexAuth comprueba la validez del token y lo refresca si está próximo a expirar.
func (c *Client) ensureCodexAuth(ctx context.Context) (accessToken, accountID string, err error) {
	if !c.isCodex() {
		return c.APIKey, "", nil
	}

	now := time.Now().Unix()
	if c.APIKey != "" && (c.TokenExpires == 0 || c.TokenExpires > now+60) {
		return c.APIKey, c.AccountID, nil
	}

	// Si tenemos refresh_token, intentamos refrescar contra OpenAI
	if c.RefreshToken != "" {
		newBundle, err := codex.RefreshAccessToken(ctx, c.RefreshToken)
		if err == nil && newBundle.AccessToken != "" {
			c.APIKey = newBundle.AccessToken
			c.RefreshToken = newBundle.RefreshToken
			if newBundle.AccountID != "" {
				c.AccountID = newBundle.AccountID
			}
			c.TokenExpires = newBundle.ExpiresAt

			// Guardar de forma asíncrona en config.json y ~/.codex/auth.json
			go func() {
				if cfg, err := config.LoadFile(); err == nil {
					if p, ok := cfg.Providers[c.ProviderKey]; ok {
						p.APIKey = newBundle.AccessToken
						p.RefreshToken = newBundle.RefreshToken
						p.AccountID = c.AccountID
						p.TokenExpires = newBundle.ExpiresAt
						cfg.Providers[c.ProviderKey] = p
						_, _ = config.Save(cfg)
					}
				}
				_ = codex.SaveLocalAuth(newBundle)
			}()

			return c.APIKey, c.AccountID, nil
		}
	}

	// Si no se pudo refrescar pero hay APIKey manual en memoria, se intenta
	if c.APIKey != "" {
		return c.APIKey, c.AccountID, nil
	}

	return "", "", errors.New("no hay sesión activa de Codex: inicia sesión con `mkd auth codex` o importa con Ctrl+I en Ctrl+K")
}

// isClaude indica si el cliente utiliza la API o suscripción de Claude (Anthropic).
func (c *Client) isClaude() bool {
	return c.ProviderKey == "claude" || config.IsClaude(c.BaseURL)
}

// ensureClaudeAuth comprueba la validez del token de Claude y lo refresca si está próximo a expirar.
func (c *Client) ensureClaudeAuth(ctx context.Context) (string, error) {
	if !c.isClaude() {
		return c.APIKey, nil
	}

	now := time.Now().Unix()
	if c.APIKey != "" && (c.TokenExpires == 0 || c.TokenExpires > now+60) {
		return c.APIKey, nil
	}

	// Si tenemos refresh_token, intentamos refrescar contra Anthropic
	if c.RefreshToken != "" {
		newBundle, err := claude.RefreshAccessToken(ctx, c.RefreshToken)
		if err == nil && newBundle.AccessToken != "" {
			c.APIKey = newBundle.AccessToken
			if newBundle.RefreshToken != "" {
				c.RefreshToken = newBundle.RefreshToken
			}
			c.TokenExpires = newBundle.ExpiresAt

			go func() {
				if cfg, err := config.LoadFile(); err == nil {
					if p, ok := cfg.Providers[c.ProviderKey]; ok {
						p.APIKey = newBundle.AccessToken
						p.RefreshToken = c.RefreshToken
						p.TokenExpires = newBundle.ExpiresAt
						cfg.Providers[c.ProviderKey] = p
						_, _ = config.Save(cfg)
					}
				}
				_ = claude.SaveLocalAuth(newBundle)
			}()

			return c.APIKey, nil
		}
	}

	if c.APIKey != "" {
		return c.APIKey, nil
	}

	return "", errors.New("no hay sesión activa de Claude: inicia sesión con `mkd auth claude` o importa con Ctrl+I en Ctrl+K")
}

// isAntigravity indica si el cliente utiliza el motor de Google Antigravity (agy).
func (c *Client) isAntigravity() bool {
	return c.ProviderKey == "antigravity" || c.ProviderKey == "agy" || config.IsAntigravity(c.BaseURL)
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

	if c.isCodex() {
		token, accountID, err := c.ensureCodexAuth(ctx)
		if err != nil {
			return nil, err
		}
		events, err := codex.StreamResponses(ctx, codex.Request{
			BaseURL:      c.BaseURL,
			AccessToken:  token,
			AccountID:    accountID,
			Model:        c.Model,
			Instructions: c.SystemPrompt,
			Messages:     messages,
		})
		if err != nil {
			return nil, err
		}
		out := make(chan Chunk, 64)
		go func() {
			defer close(out)
			for ev := range events {
				if ev.Err != nil {
					sendChunk(ctx, out, Chunk{Err: ev.Err})
					return
				}
				if ev.DeltaContent != "" {
					if !sendChunk(ctx, out, Chunk{Content: ev.DeltaContent}) {
						return
					}
				}
			}
		}()
		return out, nil
	}

	if c.isClaude() {
		token, err := c.ensureClaudeAuth(ctx)
		if err != nil {
			return nil, err
		}
		events, err := claude.StreamMessages(ctx, claude.Request{
			BaseURL:      c.BaseURL,
			AccessToken:  token,
			Model:        c.Model,
			SystemPrompt: c.SystemPrompt,
			Messages:     messages,
		})
		if err != nil {
			return nil, err
		}
		out := make(chan Chunk, 64)
		go func() {
			defer close(out)
			for ev := range events {
				if ev.Err != nil {
					sendChunk(ctx, out, Chunk{Err: ev.Err})
					return
				}
				if ev.DeltaContent != "" {
					if !sendChunk(ctx, out, Chunk{Content: ev.DeltaContent}) {
						return
					}
				}
			}
		}()
		return out, nil
	}

	if c.isAntigravity() {
		events, err := antigravity.StreamMessages(ctx, antigravity.Request{
			Model:        c.Model,
			SystemPrompt: c.SystemPrompt,
			Messages:     messages,
		})
		if err != nil {
			return nil, err
		}
		out := make(chan Chunk, 64)
		go func() {
			defer close(out)
			for ev := range events {
				if ev.Err != nil {
					sendChunk(ctx, out, Chunk{Err: ev.Err})
					return
				}
				if ev.DeltaContent != "" {
					if !sendChunk(ctx, out, Chunk{Content: ev.DeltaContent}) {
						return
					}
				}
			}
		}()
		return out, nil
	}

	stream, err := c.api.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:    c.Model,
		Messages: SanitizeMessages(messages),
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

	if c.isCodex() {
		token, accountID, err := c.ensureCodexAuth(ctx)
		if err != nil {
			return nil, err
		}
		events, err := codex.StreamResponses(ctx, codex.Request{
			BaseURL:      c.BaseURL,
			AccessToken:  token,
			AccountID:    accountID,
			Model:        c.Model,
			Instructions: c.SystemPrompt,
			Messages:     messages,
			Tools:        tools,
		})
		if err != nil {
			return nil, err
		}
		out := make(chan Chunk, 64)
		go func() {
			defer close(out)
			for ev := range events {
				if ev.Err != nil {
					sendChunk(ctx, out, Chunk{Err: ev.Err})
					return
				}
				if ev.DeltaContent != "" {
					if !sendChunk(ctx, out, Chunk{Content: ev.DeltaContent}) {
						return
					}
				}
			}
		}()
		return out, nil
	}

	if c.isClaude() {
		token, err := c.ensureClaudeAuth(ctx)
		if err != nil {
			return nil, err
		}
		events, err := claude.StreamMessages(ctx, claude.Request{
			BaseURL:      c.BaseURL,
			AccessToken:  token,
			Model:        c.Model,
			SystemPrompt: c.SystemPrompt,
			Messages:     messages,
			Tools:        tools,
		})
		if err != nil {
			return nil, err
		}
		out := make(chan Chunk, 64)
		go func() {
			defer close(out)
			for ev := range events {
				if ev.Err != nil {
					sendChunk(ctx, out, Chunk{Err: ev.Err})
					return
				}
				if ev.DeltaContent != "" {
					if !sendChunk(ctx, out, Chunk{Content: ev.DeltaContent}) {
						return
					}
				}
			}
		}()
		return out, nil
	}

	if c.isAntigravity() {
		events, err := antigravity.StreamMessages(ctx, antigravity.Request{
			Model:        c.Model,
			SystemPrompt: c.SystemPrompt,
			Messages:     messages,
		})
		if err != nil {
			return nil, err
		}
		out := make(chan Chunk, 64)
		go func() {
			defer close(out)
			for ev := range events {
				if ev.Err != nil {
					sendChunk(ctx, out, Chunk{Err: ev.Err})
					return
				}
				if ev.DeltaContent != "" {
					if !sendChunk(ctx, out, Chunk{Content: ev.DeltaContent}) {
						return
					}
				}
			}
		}()
		return out, nil
	}

	stream, err := c.api.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:    c.Model,
		Messages: SanitizeMessages(messages),
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

	if c.isCodex() {
		token, accountID, err := c.ensureCodexAuth(ctx)
		if err != nil {
			return openai.ChatCompletionMessage{}, err
		}
		events, err := codex.StreamResponses(ctx, codex.Request{
			BaseURL:      c.BaseURL,
			AccessToken:  token,
			AccountID:    accountID,
			Model:        c.Model,
			Instructions: c.SystemPrompt,
			Messages:     messages,
			Tools:        tools,
		})
		if err != nil {
			return openai.ChatCompletionMessage{}, err
		}
		var content strings.Builder
		var toolCalls []openai.ToolCall
		for ev := range events {
			if ev.Err != nil {
				return openai.ChatCompletionMessage{}, ev.Err
			}
			if ev.DeltaContent != "" {
				content.WriteString(ev.DeltaContent)
			}
			if len(ev.ToolCalls) > 0 {
				toolCalls = append(toolCalls, ev.ToolCalls...)
			}
		}
		return openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   content.String(),
			ToolCalls: toolCalls,
		}, nil
	}

	if c.isClaude() {
		token, err := c.ensureClaudeAuth(ctx)
		if err != nil {
			return openai.ChatCompletionMessage{}, err
		}
		events, err := claude.StreamMessages(ctx, claude.Request{
			BaseURL:      c.BaseURL,
			AccessToken:  token,
			Model:        c.Model,
			SystemPrompt: c.SystemPrompt,
			Messages:     messages,
			Tools:        tools,
		})
		if err != nil {
			return openai.ChatCompletionMessage{}, err
		}
		var content strings.Builder
		var toolCalls []openai.ToolCall
		for ev := range events {
			if ev.Err != nil {
				return openai.ChatCompletionMessage{}, ev.Err
			}
			if ev.DeltaContent != "" {
				content.WriteString(ev.DeltaContent)
			}
			if len(ev.ToolCalls) > 0 {
				toolCalls = append(toolCalls, ev.ToolCalls...)
			}
		}
		return openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   content.String(),
			ToolCalls: toolCalls,
		}, nil
	}

	if c.isAntigravity() {
		events, err := antigravity.StreamMessages(ctx, antigravity.Request{
			Model:        c.Model,
			SystemPrompt: c.SystemPrompt,
			Messages:     messages,
		})
		if err != nil {
			return openai.ChatCompletionMessage{}, err
		}
		var content strings.Builder
		for ev := range events {
			if ev.Err != nil {
				return openai.ChatCompletionMessage{}, ev.Err
			}
			if ev.DeltaContent != "" {
				content.WriteString(ev.DeltaContent)
			}
		}
		return openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleAssistant,
			Content: content.String(),
		}, nil
	}

	response, err := c.api.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    c.Model,
		Messages: SanitizeMessages(messages),
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

	if c.isCodex() || c.isClaude() || c.isAntigravity() {
		msg, err := c.CompleteMessagesTools(ctx, messages, nil)
		if err != nil {
			return "", err
		}
		return msg.Content, nil
	}

	response, err := c.api.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    c.Model,
		Messages: SanitizeMessages(messages),
	})
	if err != nil {
		return "", fmt.Errorf("API %s (%s): %w", c.Model, c.ProviderName, normalizeError(err))
	}
	if len(response.Choices) == 0 {
		return "", errors.New("el proveedor devolvió una respuesta vacía")
	}
	return response.Choices[0].Message.Content, nil
}

// SanitizeMessages arregla el historial antes de enviarlo al proveedor.
//
// Si un turno se cancela, se cambia de sesión o falla una ronda entre la
// petición de una herramienta y su resultado, quedan mensajes assistant con
// tool_calls sin su role=tool correspondiente (y viceversa). Los proveedores
// compatibles con OpenAI rechazan esa secuencia, así que:
//
//	assistant(tool_calls=[A,B]) ──► ¿llega role=tool A?  sí ──► se conserva
//	                                     │                  no ──► se descarta la llamada
//	role=tool huérfano (sin assistant) ──────────────────────────► se descarta
//
// Un assistant cuyos tool_calls se descartan todos se convierte en texto normal
// para no perder lo que ya había explicado el modelo.
func SanitizeMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return messages
	}

	out := make([]openai.ChatCompletionMessage, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == openai.ChatMessageRoleTool {
			// Huérfano: no hay assistant previo con esa llamada pendiente.
			continue
		}
		if msg.Role != openai.ChatMessageRoleAssistant || len(msg.ToolCalls) == 0 {
			out = append(out, msg)
			continue
		}

		// Recoge los resultados que sí siguieron a esta ronda.
		answered := make(map[string]bool, len(msg.ToolCalls))
		end := i + 1
		for end < len(messages) && messages[end].Role == openai.ChatMessageRoleTool {
			answered[messages[end].ToolCallID] = true
			end++
		}

		kept := make([]openai.ToolCall, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			if answered[call.ID] {
				kept = append(kept, call)
			}
		}
		if len(kept) == 0 {
			if strings.TrimSpace(msg.Content) != "" {
				out = append(out, openai.ChatCompletionMessage{
					Role:    openai.ChatMessageRoleAssistant,
					Content: msg.Content,
				})
			}
			i = end - 1
			continue
		}

		msg.ToolCalls = kept
		out = append(out, msg)
		for j := i + 1; j < end; j++ {
			for _, call := range kept {
				if messages[j].ToolCallID == call.ID {
					out = append(out, messages[j])
					break
				}
			}
		}
		i = end - 1
	}
	return out
}

// FetchAvailableModels consulta GET /v1/models y devuelve IDs ordenados.
//
// Algunos proveedores (DeepInfra) exponen cientos de modelos mezclando imagen,
// embeddings y versiones deprecadas. Si detectamos su catálogo público en
// /models/list lo usamos para quedarnos solo con los de texto activos; si no,
// devolvemos la lista del endpoint OpenAI tal cual.
func (c *Client) FetchAvailableModels(ctx context.Context) ([]string, error) {
	if c.isCodex() {
		token, accountID, err := c.ensureCodexAuth(ctx)
		if err != nil {
			return nil, err
		}
		return codex.FetchModels(ctx, c.BaseURL, token, accountID)
	}

	if c.isClaude() {
		token, err := c.ensureClaudeAuth(ctx)
		if err != nil {
			return nil, err
		}
		return claude.FetchModels(ctx, c.BaseURL, token)
	}

	if c.isAntigravity() {
		return antigravity.FetchModels(ctx)
	}

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
