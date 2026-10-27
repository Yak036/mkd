package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	openai "github.com/sashabaranov/go-openai"

	"mkd/internal/config"
)

// Manager orquesta varios servidores MCP y traduce sus herramientas al
// formato de go-openai.
//
//	config.MCPServers ──► Manager.StartAll ──► []*Client
//	                                   │
//	                            Tools() → []openai.Tool {mcp__server__tool}
//	                                   │
//	                    Call(mcp__server__tool) → Client.CallTool
//
// Un fallo al arrancar un servidor no afecta a los demás: se guarda el error y
// el servidor aparece como desconectado en la TUI.
type Manager struct {
	mu      sync.RWMutex
	specs   map[string]Spec
	clients map[string]*Client
	errs    map[string]string
	enabled map[string]bool

	// onTool se invoca en cada tools/call para que la UI pinte el servidor.
	onTool func(server, tool string)
}

// NewManager construye el gestor a partir de la configuración.
func NewManager(servers map[string]config.MCPServerConfig) *Manager {
	m := &Manager{
		specs:   make(map[string]Spec, len(servers)),
		clients: make(map[string]*Client, len(servers)),
		errs:    make(map[string]string, len(servers)),
		enabled: make(map[string]bool, len(servers)),
	}
	for name, server := range servers {
		m.specs[name] = Spec{
			Name:    name,
			Command: server.Command,
			Args:    append([]string(nil), server.Args...),
			Env:     server.Env,
		}
		m.enabled[name] = server.Enabled
	}
	return m
}

// SetOnTool registra un observer para las llamadas a herramientas.
func (m *Manager) SetOnTool(fn func(server, tool string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onTool = fn
}

// StartAll lanza en paralelo todos los servidores habilitados y espera el
// handshake de cada uno. Los fallos se registran pero no abortan el arranque.
func (m *Manager) StartAll(ctx context.Context) {
	m.mu.RLock()
	names := make([]string, 0, len(m.specs))
	for name, enabled := range m.enabled {
		if enabled {
			names = append(names, name)
		}
	}
	m.mu.RUnlock()
	sort.Strings(names)

	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_ = m.connect(ctx, name)
		}(name)
	}
	wg.Wait()
}

// connect arranca (o reinicia) un servidor concreto.
func (m *Manager) connect(ctx context.Context, name string) error {
	m.mu.RLock()
	spec, ok := m.specs[name]
	observer := m.onTool
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("servidor MCP desconocido: %s", name)
	}

	m.disconnect(name)

	client := NewClient(spec)
	client.onTool = observer
	if err := client.Start(ctx); err != nil {
		m.mu.Lock()
		m.errs[name] = err.Error()
		m.mu.Unlock()
		return err
	}

	m.mu.Lock()
	m.clients[name] = client
	delete(m.errs, name)
	m.mu.Unlock()
	return nil
}

// disconnect cierra un servidor si estaba activo.
func (m *Manager) disconnect(name string) {
	m.mu.Lock()
	client := m.clients[name]
	delete(m.clients, name)
	m.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

// Start conecta un servidor concreto (usado al habilitarlo en vivo).
func (m *Manager) Start(ctx context.Context, name string) error {
	m.mu.Lock()
	m.enabled[name] = true
	m.mu.Unlock()
	return m.connect(ctx, name)
}

// Stop desconecta un servidor y lo marca deshabilitado.
func (m *Manager) Stop(name string) {
	m.mu.Lock()
	m.enabled[name] = false
	m.mu.Unlock()
	m.disconnect(name)
	m.mu.Lock()
	delete(m.errs, name)
	m.mu.Unlock()
}

// Reconnect reinicia todos los servidores habilitados.
func (m *Manager) Reconnect(ctx context.Context) {
	m.mu.RLock()
	names := make([]string, 0, len(m.clients)+len(m.errs))
	for name, enabled := range m.enabled {
		if enabled {
			names = append(names, name)
		}
	}
	m.mu.RUnlock()

	for _, name := range names {
		m.disconnect(name)
	}
	m.StartAll(ctx)
}

// AddServer registra un nuevo servidor en memoria y lo conecta.
func (m *Manager) AddServer(ctx context.Context, name string, server config.MCPServerConfig) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("nombre de servidor vacío")
	}
	m.mu.Lock()
	m.specs[name] = Spec{
		Name:    name,
		Command: server.Command,
		Args:    append([]string(nil), server.Args...),
		Env:     server.Env,
	}
	m.enabled[name] = server.Enabled
	m.mu.Unlock()

	if server.Enabled {
		return m.connect(ctx, name)
	}
	return nil
}

// UpdateSpecs reemplaza la lista de servidores (tras editar la config).
func (m *Manager) UpdateSpecs(servers map[string]config.MCPServerConfig) {
	m.mu.Lock()
	for name := range m.specs {
		if _, ok := servers[name]; !ok && m.enabled[name] {
			if client := m.clients[name]; client != nil {
				go client.Close()
			}
			delete(m.clients, name)
			delete(m.errs, name)
		}
	}
	for name, server := range servers {
		m.specs[name] = Spec{Name: name, Command: server.Command, Args: server.Args, Env: server.Env}
		m.enabled[name] = server.Enabled
	}
	m.mu.Unlock()
}

// Close cierra todos los subprocesos MCP de forma ordenada.
func (m *Manager) Close() {
	m.mu.Lock()
	clients := make([]*Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.clients = map[string]*Client{}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			_ = c.Close()
		}(client)
	}
	wg.Wait()
}

// AnyClients indica si hay al menos un servidor conectado.
func (m *Manager) AnyClients() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients) > 0
}

// Tools convierte todas las herramientas MCP en openai.Tool con el prefijo
// mcp__<server>__<tool>.
func (m *Manager) Tools() []openai.Tool {
	m.mu.RLock()
	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	clients := make(map[string]*Client, len(m.clients))
	for name, client := range m.clients {
		clients[name] = client
	}
	m.mu.RUnlock()
	sort.Strings(names)

	tools := make([]openai.Tool, 0, 16)
	for _, name := range names {
		client := clients[name]
		for _, tool := range client.Tools() {
			tools = append(tools, openai.Tool{
				Type: openai.ToolTypeFunction,
				Function: &openai.FunctionDefinition{
					Name:        QualifiedName(name, tool.Name),
					Description: describe(name, tool),
					Parameters:  sanitizeSchema(tool.InputSchema),
				},
			})
		}
	}
	return tools
}

// QualifiedName construye mcp__<server>__<tool>.
func QualifiedName(server, tool string) string {
	return ToolPrefix + server + "__" + tool
}

// ParsePrefixed separa mcp__<server>__<tool> en sus dos partes.
func ParsePrefixed(name string) (server, tool string, ok bool) {
	if !strings.HasPrefix(name, ToolPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(name, ToolPrefix)
	index := strings.Index(rest, "__")
	if index <= 0 || index+2 >= len(rest) {
		return "", "", false
	}
	return rest[:index], rest[index+2:], true
}

// IsMCPTool indica si el nombre de la herramienta proviene de MCP.
func IsMCPTool(name string) bool {
	_, _, ok := ParsePrefixed(name)
	return ok
}

// Call enruta una herramienta prefijada al servidor correspondiente.
func (m *Manager) Call(ctx context.Context, qualified string, arguments json.RawMessage) (CallResult, error) {
	server, tool, ok := ParsePrefixed(qualified)
	if !ok {
		return CallResult{}, fmt.Errorf("herramienta MCP inválida: %s", qualified)
	}

	m.mu.RLock()
	client := m.clients[server]
	observer := m.onTool
	m.mu.RUnlock()
	if client == nil {
		return CallResult{}, fmt.Errorf("servidor MCP no conectado: %s", server)
	}
	client.onTool = observer
	return client.CallTool(ctx, tool, arguments)
}

// Status es el estado de un servidor para la TUI.
type Status struct {
	Name    string
	Command string
	Args    []string
	Enabled bool
	Online  bool
	Tools   []string
	Err     string
}

// Statuses devuelve el estado de todos los servidores configurados.
func (m *Manager) Statuses() []Status {
	m.mu.RLock()
	names := make([]string, 0, len(m.specs))
	for name := range m.specs {
		names = append(names, name)
	}
	specs := make(map[string]Spec, len(m.specs))
	for name, spec := range m.specs {
		specs[name] = spec
	}
	clients := make(map[string]*Client, len(m.clients))
	for name, client := range m.clients {
		clients[name] = client
	}
	errs := make(map[string]string, len(m.errs))
	for name, err := range m.errs {
		errs[name] = err
	}
	enabled := make(map[string]bool, len(m.enabled))
	for name, value := range m.enabled {
		enabled[name] = value
	}
	m.mu.RUnlock()
	sort.Strings(names)

	out := make([]Status, 0, len(names))
	for _, name := range names {
		spec := specs[name]
		status := Status{
			Name:    name,
			Command: spec.Command,
			Args:    spec.Args,
			Enabled: enabled[name],
			Err:     errs[name],
		}
		if client := clients[name]; client != nil {
			status.Online = true
			for _, tool := range client.Tools() {
				status.Tools = append(status.Tools, tool.Name)
			}
		}
		out = append(out, status)
	}
	return out
}

// describe compone la descripción que ve el modelo para una tool MCP.
func describe(server string, tool Tool) string {
	description := strings.TrimSpace(tool.Description)
	if description == "" {
		description = "Herramienta MCP " + tool.Name
	}
	return fmt.Sprintf("[MCP %s] %s", server, description)
}

// sanitizeSchema garantiza un JSON Schema de objeto válido para el proveedor.
func sanitizeSchema(raw json.RawMessage) json.RawMessage {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	if _, ok := probe["type"]; !ok {
		probe["type"] = "object"
	}
	if _, ok := probe["properties"]; !ok {
		probe["properties"] = map[string]any{}
	}
	sanitized, err := json.Marshal(probe)
	if err != nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return json.RawMessage(sanitized)
}
