// Package mcp implementa un cliente nativo del Model Context Protocol sobre
// stdio y un gestor que integra sus herramientas en el bucle de tool calling
// de MKD.
//
//	┌──────────────┐  initialize   ┌──────────────────┐
//	│ mkd (parent) │──────────────►│ proceso MCP (hijo)│
//	│              │◄──────────────│  stdin/stdout JSON│
//	│  Client      │  tools/list   │  -RPC 2.0         │
//	│              │──────────────►│                  │
//	│              │  tools/call   │                  │
//	│              │──────────────►│                  │
//	└──────────────┘               └──────────────────┘
//
// Cada servidor se lanza como subproceso; las respuestas se leen línea por
// línea (JSON-RPC delimited por \n) y se correlacionan por id con un mapa de
// canales, lo que permite peticiones concurrentes sin bloquear la TUI.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProtocolVersion es la versión MCP que anunciamos en initialize.
const ProtocolVersion = "2024-11-05"

// ToolPrefix es el prefijo de las herramientas MCP expuestas al modelo.
// Formato: mcp__<server>__<tool>.
const ToolPrefix = "mcp__"

// Límites del cliente.
const (
	maxLineBytes    = 8 << 20 // payload máximo por línea (8 MiB)
	initTimeout     = 20 * time.Second
	callTimeout     = 120 * time.Second
	shutdownTimeout = 3 * time.Second
)

// JSON-RPC 2.0: códigos de error estándar.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInternalError  = -32603
)

// rpcMessage es el sobre JSON-RPC 2.0 (request, response o notification).
type rpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

// rpcError es el objeto de error JSON-RPC.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	if e.Data != nil && len(e.Data) > 0 {
		return fmt.Sprintf("mcp %d: %s (%s)", e.Code, e.Message, strings.TrimSpace(string(e.Data)))
	}
	return fmt.Sprintf("mcp %d: %s", e.Code, e.Message)
}

// Tool describe una herramienta expuesta por un servidor MCP.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// CallResult es el resultado de tools/call.
type CallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError"`
}

// ContentBlock es un bloque de contenido MCP (texto, imagen, recurso…).
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// Data/ MimeType para imágenes u otros recursos embebidos.
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
}

// Text devuelve el contenido concatenado como texto plano para el modelo.
func (r CallResult) Text() string {
	var b strings.Builder
	for _, block := range r.Content {
		switch block.Type {
		case "text":
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(block.Text)
		case "resource":
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(fmt.Sprintf("[resource %s]", block.URI))
		case "image":
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(fmt.Sprintf("[imagen %s omitida]", valueOr(block.MimeType, "binaria")))
		}
	}
	return b.String()
}

// Client es un servidor MCP conectado sobre stdio.
type Client struct {
	name    string
	command string
	args    []string
	env     map[string]string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan rpcMessage
	closed  bool
	closeCh chan struct{}

	tools []Tool

	// stderrTail guarda las últimas líneas de stderr para diagnósticos.
	stderrMu   sync.Mutex
	stderrTail []string

	onTool func(server, tool string)
}

// Spec agrupa los datos necesarios para lanzar un servidor MCP.
type Spec struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// NewClient construye un cliente sin lanzar el proceso.
func NewClient(spec Spec) *Client {
	return &Client{
		name:    spec.Name,
		command: spec.Command,
		args:    append([]string(nil), spec.Args...),
		env:     spec.Env,
		pending: make(map[int64]chan rpcMessage),
		closeCh: make(chan struct{}),
	}
}

// Name devuelve el nombre lógico del servidor.
func (c *Client) Name() string { return c.name }

// Tools devuelve una copia de las herramientas descubiertas.
func (c *Client) Tools() []Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Tool(nil), c.tools...)
}

// Start lanza el subproceso, hace el handshake y descubre herramientas.
func (c *Client) Start(ctx context.Context) error {
	if strings.TrimSpace(c.command) == "" {
		return fmt.Errorf("servidor MCP %q sin comando", c.name)
	}

	cmd := exec.Command(c.command, c.args...)
	cmd.Env = mergedEnv(c.env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp %s: stdin: %w", c.name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp %s: stdout: %w", c.name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("mcp %s: stderr: %w", c.name, err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mcp %s: no se pudo iniciar %q: %w", c.name, c.command, err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.stdout = stdout
	go c.readLoop(stdout)
	go c.drainStderr(stderr)

	initCtx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()

	if err := c.initialize(initCtx); err != nil {
		c.Close()
		return err
	}
	return c.listTools(initCtx)
}

// initialize ejecuta el handshake oficial MCP.
func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "mkd",
			"version": "1.0.0",
		},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return fmt.Errorf("mcp %s: initialize: %w", c.name, err)
	}
	// Notificación sin id: no espera respuesta.
	if err := c.notify("notifications/initialized", nil); err != nil {
		return fmt.Errorf("mcp %s: initialized: %w", c.name, err)
	}
	return nil
}

// listTools llama tools/list y cachea el resultado.
func (c *Client) listTools(ctx context.Context) error {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return fmt.Errorf("mcp %s: tools/list: %w", c.name, err)
	}
	var payload struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("mcp %s: tools/list inválido: %w", c.name, err)
	}
	c.mu.Lock()
	c.tools = payload.Tools
	c.mu.Unlock()
	return nil
}

// ReloadTools vuelve a consultar tools/list (servidores dinámicos).
func (c *Client) ReloadTools(ctx context.Context) error {
	return c.listTools(ctx)
}

// CallTool invoca una herramienta y devuelve el texto para el modelo.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (CallResult, error) {
	if len(strings.TrimSpace(string(arguments))) == 0 {
		arguments = json.RawMessage("{}")
	}
	params := map[string]any{
		"name":      name,
		"arguments": arguments,
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > callTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}

	raw, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return CallResult{}, fmt.Errorf("mcp %s/%s: %w", c.name, name, err)
	}
	var result CallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		// Algunos servidores devuelven el contenido como string plano.
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return CallResult{Content: []ContentBlock{{Type: "text", Text: text}}}, nil
		}
		return CallResult{}, fmt.Errorf("mcp %s/%s: respuesta inválida: %w", c.name, name, err)
	}
	if c.onTool != nil {
		c.onTool(c.name, name)
	}
	if result.IsError {
		text := strings.TrimSpace(result.Text())
		if text == "" {
			text = "error desconocido del servidor MCP"
		}
		return result, fmt.Errorf("%s", text)
	}
	return result, nil
}

// call envía una petición y espera su respuesta correlacionada por id.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	responseCh := make(chan rpcMessage, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("cliente MCP cerrado")
	}
	c.pending[id] = responseCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.writeRequest(id, method, params); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closeCh:
		return nil, errors.New("servidor MCP terminó inesperadamente")
	case msg := <-responseCh:
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	}
}

// notify envía una notificación JSON-RPC (sin id).
func (c *Client) notify(method string, params any) error {
	msg := rpcMessage{JSONRPC: "2.0", Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		msg.Params = raw
	}
	return c.write(msg)
}

// writeRequest serializa y envía una request con id.
func (c *Client) writeRequest(id int64, method string, params any) error {
	rawID, err := json.Marshal(id)
	if err != nil {
		return err
	}
	idJSON := json.RawMessage(rawID)
	msg := rpcMessage{JSONRPC: "2.0", ID: &idJSON, Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		msg.Params = raw
	}
	return c.write(msg)
}

// write envía un mensaje como una línea JSON.
func (c *Client) write(msg rpcMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.stdin == nil {
		return errors.New("stdin MCP no disponible")
	}
	_, err = c.stdin.Write(data)
	return err
}

// readLoop consume stdout línea por línea y enruta respuestas y notificaciones.
func (c *Client) readLoop(stdout io.ReadCloser) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			// Logs sueltos del servidor que no son JSON-RPC.
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		// Notificación o request del servidor: no esperamos respuesta nuestra.
		if msg.ID == nil {
			c.handleServerNotification(msg)
			continue
		}
		var id int64
		if err := json.Unmarshal(*msg.ID, &id); err != nil {
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		c.mu.Unlock()
		if ok {
			select {
			case ch <- msg:
			default:
			}
		}
	}
	c.signalClose()
}

// handleServerNotification responde a peticiones del servidor que exigimos
// mínimamente (ping) para evitar que se quede bloqueado.
func (c *Client) handleServerNotification(msg rpcMessage) {
	switch msg.Method {
	case "notifications/message", "notifications/progress", "":
		return
	}
}

// drainStderr guarda el stderr del servidor para diagnósticos y evita que el
// pipe se llene y bloquee al subproceso.
func (c *Client) drainStderr(stderr io.ReadCloser) {
	defer stderr.Close()
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 4<<10), 256<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		c.stderrMu.Lock()
		c.stderrTail = append(c.stderrTail, line)
		if len(c.stderrTail) > 20 {
			c.stderrTail = c.stderrTail[len(c.stderrTail)-20:]
		}
		c.stderrMu.Unlock()
	}
}

// Stderr devuelve las últimas líneas de error del servidor.
func (c *Client) Stderr() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return strings.Join(c.stderrTail, "\n")
}

// signalClose cierra todos los canales pendientes una sola vez.
func (c *Client) signalClose() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.closeCh)
	c.mu.Unlock()
}

// Close termina el subproceso con limpieza ordenada.
func (c *Client) Close() error {
	c.signalClose()

	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()

	select {
	case <-done:
	case <-time.After(shutdownTimeout):
		_ = c.cmd.Process.Kill()
		<-done
	}
	return nil
}

// mergedEnv combina el entorno del proceso con las variables del servidor.
func mergedEnv(extra map[string]string) []string {
	env := os.Environ()
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
