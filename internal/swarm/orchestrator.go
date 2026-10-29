package swarm

// Motor de orquestación jerárquica.
//
//	Mission (usuario /swarm)
//	        │
//	        ▼
//	┌──────────────────────────────────────────────────────────────┐
//	│ Orquestador (tool loop sobre delegate_task / _parallel)      │
//	│   system prompt + roster dinámico + tools nativas            │
//	└───────────────┬──────────────────────────────────────────────┘
//	                │ delegate_task(agent, título, instrucciones)
//	                │ delegate_parallel([]Task)
//	                ▼
//	  ┌────────────────────────────────────────────────────────┐
//	  │ Sub-agent runner (una goroutine por delegación)        │
//	  │  · client scoped (provider/model del perfil)           │
//	  │  · messages aislados (Sandboxed Context)               │
//	  │  · tools filtradas por AllowedTools                    │
//	  │  · HITL vía HITLBroker → modal de la TUI               │
//	  │  · progreso vía EventBus                               │
//	  └────────────────────────────────────────────────────────┘
//
// El orquestador NO ve el historial interno de los sub-agentes: solo recibe
// su reporte final como role=tool, lo que mantiene el contexto acotado.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"github.com/Yak036/mkd/internal/agent"
	"github.com/Yak036/mkd/internal/config"
)

// Nombres de las herramientas de delegación del orquestador.
const (
	ToolDelegateTask     = "delegate_task"
	ToolDelegateParallel = "delegate_parallel"
)

// Límites de ejecución.
const (
	// MaxRounds es el tope de rondas de tool calling por sub-agente.
	MaxRounds = 24
	// MaxParallel acota cuántos sub-agentes corren a la vez en delegate_parallel.
	MaxParallel = 4
	// OrchestratorTimeout acota la misión completa si se desea acotar externamente.
	OrchestratorTimeout = 30 * time.Minute
	// maxReportBytes recorta el reporte que vuelve al orquestador.
	maxReportBytes = 12 << 10
)

// Completer es la superficie mínima del cliente de IA que usa un sub-agente.
// Permite inyectar un cliente real (ai.Client) o un falso en tests.
type Completer interface {
	Ready() bool
	CompleteMessagesTools(ctx context.Context, messages []openai.ChatCompletionMessage, tools []openai.Tool) (openai.ChatCompletionMessage, error)
}

// ClientFactory construye un Completer para un proveedor/modelo concretos.
// Recibe el perfil (ya normalizado) y debe devolver un cliente listo.
type ClientFactory func(profile config.SubAgentProfile) (Completer, error)

// MCPCaller ejecuta una herramienta MCP (prefijo mcp__) y devuelve el texto.
// Lo inyecta la TUI con el manager MCP real.
type MCPCaller func(ctx context.Context, name, arguments string) (string, error)

// AskUser pregunta al usuario y espera su respuesta. La TUI lo implementa con
// el modal de preguntas; sin hook, el orquestador continúa con una suposición.
type AskUser func(ctx context.Context, question agent.Question) (answer string, cancelled bool)

// Task es una subtarea a delegar.
type Task struct {
	AgentID       string
	Title         string
	Instrucciones string
	ContextFiles  []string
}

// Report es el resultado de una delegación.
type Report struct {
	AgentID   string
	AgentName string
	Task      string
	Content   string
	Err       error
	Duration  time.Duration
	Tokens    int
	Rounds    int
}

// Orchestrator coordina la misión.
type Orchestrator struct {
	cfg      config.Config
	factory  ClientFactory
	cwd      string
	events   *EventBus
	broker   *HITLBroker
	mcpTools []openai.Tool
	mcpCall  MCPCaller
	askUser  AskUser

	// mission identifica la ejecución para descartar eventos viejos.
	mission string
	// index asigna tabs Alt+1..9 a los sub-agentes en orden de delegación.
	mu    sync.Mutex
	index map[string]int
	next  int

	// cancel cancela la misión completa.
	cancel context.CancelFunc
}

// Config agrupa las dependencias del orquestador.
type Config struct {
	Config   config.Config
	Factory  ClientFactory
	Cwd      string
	Events   *EventBus
	Broker   *HITLBroker
	MCPTools []openai.Tool
	MCPCall  MCPCaller
	// AskUser atiende ask_user del orquestador (nil = sin interacción).
	AskUser AskUser
	Mission string
}

// New construye un orquestador.
func New(cfg Config) *Orchestrator {
	mcpTools := append([]openai.Tool(nil), cfg.MCPTools...)
	return &Orchestrator{
		cfg:      cfg.Config,
		factory:  cfg.Factory,
		cwd:      cfg.Cwd,
		events:   cfg.Events,
		broker:   cfg.Broker,
		mcpTools: mcpTools,
		mcpCall:  cfg.MCPCall,
		askUser:  cfg.AskUser,
		mission:  cfg.Mission,
		index:    make(map[string]int),
	}
}

// Mission devuelve el identificador de la misión.
func (o *Orchestrator) Mission() string { return o.mission }

// Config devuelve la configuración con la que se construyó el orquestador.
func (o *Orchestrator) Config() config.Config { return o.cfg }

// SystemPrompt devuelve el prompt del orquestador (base + roster dinámico).
func (o *Orchestrator) SystemPrompt() string {
	return OrchestratorSystemPrompt(o.cfg.SystemPrompt, o.cfg.SubAgents)
}

// Tools devuelve las herramientas del orquestador: las nativas (para que pueda
// investigar lo justo) más delegate_task / delegate_parallel.
func (o *Orchestrator) Tools() []openai.Tool {
	tools := append([]openai.Tool(nil), agent.Tools...)
	tools = append(tools, agent.AskUserTool)
	tools = append(tools, o.mcpTools...)
	tools = append(tools, DelegateTools()...)
	return tools
}

// HandleToolCall ejecuta una llamada del orquestador y devuelve el texto para
// el role=tool. Delega en los sub-agentes cuando corresponde.
func (o *Orchestrator) HandleToolCall(ctx context.Context, call agent.Call) string {
	if IsDelegateTool(call.Name) {
		return o.handleDelegate(ctx, call)
	}
	if call.Name == agent.ToolAskUser {
		return o.handleAskUser(ctx, call)
	}
	if strings.HasPrefix(call.Name, "mcp__") {
		result, err := o.callMCP(ctx, call.Name, call.Arguments)
		if err != nil {
			return "error: " + err.Error()
		}
		return result
	}

	proposal, err := agent.BuildProposal(call, o.cwd)
	if err != nil {
		return "error: " + err.Error()
	}
	if !proposal.Action.RequiresApproval() {
		result, err := executeReadOnly(proposal, o.cwd)
		if err != nil {
			return "error: " + err.Error()
		}
		return result
	}
	return "error: el Orquestador no debe modificar el proyecto directamente; delega en un sub-agente."
}

// handleAskUser reenvía la pregunta a la TUI y devuelve la respuesta al modelo.
func (o *Orchestrator) handleAskUser(ctx context.Context, call agent.Call) string {
	question, err := agent.ParseQuestion(call)
	if err != nil {
		return "error: " + err.Error()
	}
	if o.askUser == nil {
		return "No hay interactividad disponible en esta misión: decide con la mejor información y registra la suposición."
	}
	answer, cancelled := o.askUser(ctx, question)
	if cancelled && strings.TrimSpace(answer) == "" {
		return agent.FormatAnswer(question, agent.Answer{CallID: call.ID, Cancelled: true})
	}
	return agent.FormatAnswer(question, agent.Answer{CallID: call.ID, Text: answer})
}

// handleDelegate procesa delegate_task / delegate_parallel.
func (o *Orchestrator) handleDelegate(ctx context.Context, call agent.Call) string {
	tasks, err := ParseDelegate(call.Name, call.Arguments)
	if err != nil {
		return "error: " + err.Error()
	}

	if call.Name == ToolDelegateTask {
		report := o.Delegate(ctx, tasks[0])
		return FormatReports([]Report{report})
	}

	reports := o.DelegateParallel(ctx, tasks)
	return FormatReports(reports)
}

// AttachCancel registra la función de cancelación de la misión.
func (o *Orchestrator) AttachCancel(cancel context.CancelFunc) {
	o.cancel = cancel
}

// Cancel detiene la misión y rechaza permisos pendientes.
func (o *Orchestrator) Cancel() {
	if o.cancel != nil {
		o.cancel()
	}
	if o.broker != nil {
		o.broker.ResolveAll()
	}
}

// emit publica un evento con la misión fijada.
func (o *Orchestrator) emit(event SwarmEvent) {
	if o.events == nil {
		return
	}
	event.Mission = o.mission
	o.events.Emit(event)
}

// tabIndex asigna (una vez) el índice de pestaña de un agente.
func (o *Orchestrator) tabIndex(agentID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	if index, ok := o.index[agentID]; ok {
		return index
	}
	o.next++
	index := o.next
	o.index[agentID] = index
	return index
}

// ---------------------------------------------------------------------------
// Roster y herramientas del orquestador
// ---------------------------------------------------------------------------

// Roster describe los sub-agentes habilitados para inyectarlos en el prompt.
func Roster(agents map[string]config.SubAgentProfile) string {
	enabled := EnabledProfiles(agents)
	if len(enabled) == 0 {
		return "(ningún sub-agente habilitado)"
	}
	var b strings.Builder
	for _, profile := range enabled {
		b.WriteString(fmt.Sprintf("- %s (%s) — %s\n", profile.ID, profile.Name, ToolsForPermissions(profile)))
		if prompt := oneLine(profile.RolePrompt, 160); prompt != "" {
			b.WriteString("    " + prompt + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// OrchestratorSystemPrompt construye el system prompt del orquestador.
func OrchestratorSystemPrompt(base string, agents map[string]config.SubAgentProfile) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(base))
	b.WriteString("\n\n")
	b.WriteString("Estás en MODO SWARM: eres el Orquestador Lead de un equipo de sub-agentes.\n")
	b.WriteString("Tu trabajo: descomponer la misión en fases lógicas, delegar en sub-agentes especializados y consolidar sus reportes.\n\n")
	b.WriteString("REGLAS FUNDAMENTALES DE ORQUESTACIÓN Y PIPELINE:\n")
	b.WriteString("1. SECUENCIA DESARROLLO → QA (ESTRICTA):\n")
	b.WriteString("   - NUNCA lances a un QA ('qa', 'code-tester', 'api-tester', 'ui-tester') en paralelo con los desarrolladores ('coder', 'frontend', 'backend') para revisar el mismo trabajo.\n")
	b.WriteString("   - QA NO puede auditar ni verificar código que aún no ha sido escrito o modificado.\n")
	b.WriteString("   - FASE 1 (IMPLEMENTACIÓN): Delega primero en 'coder' y/o 'frontend'/'backend'. Si son módulos independientes, usa delegate_parallel entre ellos.\n")
	b.WriteString("   - FASE 2 (VALIDACIÓN QA): SOLO después de que los implementadores terminen y entreguen su reporte de cambios, delega en 'qa' o 'code-tester' para que audite los cambios, ejecute tests y busque regresiones.\n")
	b.WriteString("2. DELEGACIÓN PARALELA vs INDIVIDUAL:\n")
	b.WriteString("   - Usa delegate_task para tareas secuenciales o dependientes de otra anterior.\n")
	b.WriteString("   - Usa delegate_parallel ÚNICAMENTE para tareas verdaderamente independientes que no esperan el resultado de otra en el mismo lote.\n")
	b.WriteString("3. CONTEXTO COMPLETO EN INSTRUCCIONES:\n")
	b.WriteString("   - Cada sub-agente tiene su propio contexto aislado. En 'instructions', dale órdenes claras, autónomas y específicas para su rol.\n")
	b.WriteString("   - Al delegar a QA en la Fase 2, incluye en 'instructions' el detalle exacto de los archivos y cambios que hicieron los desarrolladores.\n")
	b.WriteString("4. SÍNTESIS FINAL:\n")
	b.WriteString("   - Cierra siempre con una síntesis clara y completa del resultado consolidado para el usuario.\n\n")
	b.WriteString("Sub-agentes disponibles (usa exactamente estos IDs):\n")
	b.WriteString(Roster(agents))
	b.WriteString("\n")
	return b.String()
}

// DelegateTools devuelve las definiciones de las herramientas de delegación.
func DelegateTools() []openai.Tool {
	return []openai.Tool{
		agent.FunctionTool(ToolDelegateTask,
			"Lanza UN sub-agente con contexto aislado para resolver una subtarea. "+
				"Devuelve el reporte final del sub-agente. Usa el ID exacto del roster.",
			json.RawMessage(`{
  "type":"object",
  "properties":{
    "agent_id":{"type":"string","description":"ID del sub-agente del roster."},
    "task_title":{"type":"string","description":"Título corto de la tarea."},
    "instructions":{"type":"string","description":"Instrucciones completas y autónomas para el sub-agente."},
    "context_files":{"type":"array","items":{"type":"string"},"description":"Rutas que el sub-agente debe leer primero (opcional)."}
  },
  "required":["agent_id","task_title","instructions"]
}`)),
		agent.FunctionTool(ToolDelegateParallel,
			"Lanza VARIOS sub-agentes en paralelo (goroutines) y devuelve todos sus reportes. "+
				"Úsalo solo para subtareas independientes entre sí. NUNCA lances a un QA en paralelo con un implementador: QA va después de que el implementador termine.",
			json.RawMessage(`{
  "type":"object",
  "properties":{
    "tasks":{"type":"array","items":{
      "type":"object",
      "properties":{
        "agent_id":{"type":"string"},
        "task_title":{"type":"string"},
        "instructions":{"type":"string"},
        "context_files":{"type":"array","items":{"type":"string"}}
      },
      "required":["agent_id","task_title","instructions"]
    }}
  },
  "required":["tasks"]
}`)),
	}
}

// IsDelegateTool indica si el nombre corresponde a una tool de delegación.
func IsDelegateTool(name string) bool {
	return name == ToolDelegateTask || name == ToolDelegateParallel
}

// ParseDelegate decodifica los argumentos de una tool de delegación en tareas.
func ParseDelegate(name, arguments string) ([]Task, error) {
	switch name {
	case ToolDelegateTask:
		var raw struct {
			AgentID      string   `json:"agent_id"`
			TaskTitle    string   `json:"task_title"`
			Instructions string   `json:"instructions"`
			ContextFiles []string `json:"context_files"`
		}
		if err := json.Unmarshal([]byte(arguments), &raw); err != nil {
			return nil, fmt.Errorf("argumentos inválidos de %s: %w", name, err)
		}
		if strings.TrimSpace(raw.AgentID) == "" {
			return nil, errors.New("delegate_task requiere agent_id")
		}
		return []Task{{
			AgentID:       raw.AgentID,
			Title:         strings.TrimSpace(raw.TaskTitle),
			Instrucciones: raw.Instructions,
			ContextFiles:  cleanPaths(raw.ContextFiles),
		}}, nil
	case ToolDelegateParallel:
		var raw struct {
			Tasks []struct {
				AgentID      string   `json:"agent_id"`
				TaskTitle    string   `json:"task_title"`
				Instructions string   `json:"instructions"`
				ContextFiles []string `json:"context_files"`
			} `json:"tasks"`
		}
		if err := json.Unmarshal([]byte(arguments), &raw); err != nil {
			return nil, fmt.Errorf("argumentos inválidos de %s: %w", name, err)
		}
		if len(raw.Tasks) == 0 {
			return nil, errors.New("delegate_parallel requiere al menos una tarea")
		}
		tasks := make([]Task, 0, len(raw.Tasks))
		for _, item := range raw.Tasks {
			if strings.TrimSpace(item.AgentID) == "" {
				continue
			}
			tasks = append(tasks, Task{
				AgentID:       item.AgentID,
				Title:         strings.TrimSpace(item.TaskTitle),
				Instrucciones: item.Instructions,
				ContextFiles:  cleanPaths(item.ContextFiles),
			})
		}
		if len(tasks) == 0 {
			return nil, errors.New("ninguna tarea tiene agent_id válido")
		}
		return tasks, nil
	default:
		return nil, fmt.Errorf("herramienta de delegación desconocida: %q", name)
	}
}

func cleanPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" {
			out = append(out, path)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Ejecución
// ---------------------------------------------------------------------------

// Delegate ejecuta una task y devuelve su reporte. Propaga cancelación.
func (o *Orchestrator) Delegate(ctx context.Context, task Task) (rep Report) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("pánico interno en sub-agente %s: %v", task.AgentID, r)
			o.emit(SwarmEvent{
				Kind:         EventAgentError,
				AgentID:      task.AgentID,
				Task:         task.Title,
				Instructions: task.Instrucciones,
				Text:         err.Error(),
				Status:       StatusError,
			})
			rep = Report{AgentID: task.AgentID, Task: task.Title, Err: err}
		}
	}()

	profile, ok := FindProfile(o.cfg.SubAgents, task.AgentID)
	if !ok {
		err := fmt.Errorf("sub-agente %q no existe", task.AgentID)
		o.emit(SwarmEvent{Kind: EventAgentError, AgentID: task.AgentID, Task: task.Title, Instructions: task.Instrucciones, Text: err.Error(), Status: StatusError})
		return Report{AgentID: task.AgentID, Task: task.Title, Err: err}
	}
	if !profile.Enabled {
		err := fmt.Errorf("sub-agente %q está deshabilitado", profile.ID)
		return Report{AgentID: profile.ID, AgentName: profile.Name, Task: task.Title, Err: err}
	}

	tab := o.tabIndex(profile.ID)
	o.emit(SwarmEvent{
		Kind: EventAgentStarted, AgentID: profile.ID, Task: task.Title,
		Instructions: task.Instrucciones,
		Status:       StatusRunning, Index: tab,
	})

	start := time.Now()
	// No imponemos un timeout rígido por sub-agente para no truncar modelos con deep reasoning
	// ni abortar tareas mientras el usuario revisa y aprueba diffs en el modal HITL.
	// La cancelación está gobernada por el ctx general de la misión (manejado por el usuario con Esc/Ctrl+C).
	report := o.runSubAgent(ctx, profile, task, tab)
	report.Duration = time.Since(start)

	if report.Err != nil {
		o.emit(SwarmEvent{
			Kind: EventAgentError, AgentID: profile.ID, Task: task.Title,
			Instructions: task.Instrucciones,
			Text:         report.Err.Error(), Status: StatusError, Duration: report.Duration,
			Tokens: report.Tokens, Index: tab,
		})
		return report
	}

	o.emit(SwarmEvent{
		Kind: EventAgentCompleted, AgentID: profile.ID, Task: task.Title,
		Instructions: task.Instrucciones,
		Status:       StatusDone, Duration: report.Duration, Tokens: report.Tokens, Index: tab,
	})
	return report
}

// DelegateParallel ejecuta varias tasks en paralelo con un pool acotado.
func (o *Orchestrator) DelegateParallel(ctx context.Context, tasks []Task) []Report {
	if len(tasks) == 0 {
		return nil
	}

	// Reserva de índices de pestaña en orden de llegada (estable).
	for _, task := range tasks {
		if profile, ok := FindProfile(o.cfg.SubAgents, task.AgentID); ok {
			o.tabIndex(profile.ID)
		}
		o.emit(SwarmEvent{Kind: EventAgentQueued, AgentID: task.AgentID, Task: task.Title, Instructions: task.Instrucciones, Status: StatusQueued})
	}

	reports := make([]Report, len(tasks))
	sem := make(chan struct{}, MaxParallel)
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Add(1)
		go func(i int, task Task) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					reports[i] = Report{
						AgentID: task.AgentID,
						Task:    task.Title,
						Err:     fmt.Errorf("pánico atrapado en tarea paralela: %v", r),
					}
				}
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			reports[i] = o.Delegate(ctx, task)
		}(i, task)
	}
	wg.Wait()
	return reports
}

// runSubAgent ejecuta el tool loop aislado de un sub-agente.
func (o *Orchestrator) runSubAgent(ctx context.Context, profile config.SubAgentProfile, task Task, tab int) Report {
	report := Report{AgentID: profile.ID, AgentName: profile.Name, Task: task.Title}

	if o.factory == nil {
		report.Err = errors.New("factory de clientes no configurada")
		return report
	}
	client, err := o.factory(profile)
	if err != nil {
		report.Err = fmt.Errorf("cliente del sub-agente: %w", err)
		return report
	}
	if client == nil || !client.Ready() {
		report.Err = errors.New("el sub-agente no tiene modelo/proveedor configurado")
		return report
	}

	// Sandboxed Context: mensajes nuevos, sin historial del orquestador.
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: subAgentSystemPrompt(profile, o.cwd)},
		{Role: openai.ChatMessageRoleUser, Content: taskPrompt(task, o.cwd)},
	}
	tools := o.subAgentTools(profile)

	var streamed strings.Builder
	for round := 0; round < MaxRounds; round++ {
		if err := ctx.Err(); err != nil {
			report.Err = err
			return report
		}
		report.Rounds = round + 1

		assistant, err := client.CompleteMessagesTools(ctx, messages, tools)
		if err != nil {
			report.Err = err
			return report
		}
		messages = append(messages, assistant)

		if len(assistant.ToolCalls) == 0 {
			if text := strings.TrimSpace(assistant.Content); text != "" {
				streamed.WriteString(text)
				o.emit(SwarmEvent{Kind: EventAgentStreamChunk, AgentID: profile.ID, Task: task.Title, Text: text, Index: tab})
			}
			break
		}

		if content := strings.TrimSpace(assistant.Content); content != "" {
			streamed.WriteString(content + "\n\n")
			o.emit(SwarmEvent{Kind: EventAgentStreamChunk, AgentID: profile.ID, Task: task.Title, Text: content, Index: tab})
		}

		for _, call := range assistant.ToolCalls {
			if ctx.Err() != nil {
				report.Err = ctx.Err()
				return report
			}
			result := o.execSubAgentTool(ctx, profile, task, call, tab)
			messages = append(messages, agent.ResultMessage(call.ID, result))
			report.Tokens += approxTokens(result)
		}
	}

	report.Content = truncateReport(strings.TrimSpace(streamed.String()))
	if report.Content == "" {
		report.Content = "(el sub-agente no emitió un reporte en texto)"
	}
	return report
}

// execSubAgentTool aplica la política de AllowedTools y el HITL.
func (o *Orchestrator) execSubAgentTool(ctx context.Context, profile config.SubAgentProfile, task Task, call openai.ToolCall, tab int) string {
	name := call.Function.Name
	args := call.Function.Arguments

	// Preguntas: un sub-agente no habla con el usuario directamente; se le
	// indica que decida y deje constancia en su reporte.
	if name == agent.ToolAskUser {
		return "No hagas preguntas durante una delegación: decide con la mejor información disponible y registra la suposición en tu reporte final."
	}

	if !ToolAllowed(profile, name) {
		message := fmt.Sprintf("tool %q no permitida para el sub-agente %s (herramientas: %s)",
			name, profile.ID, ToolsForPermissions(profile))
		o.emit(SwarmEvent{Kind: EventAgentToolCall, AgentID: profile.ID, Task: task.Title, Tool: name, Args: argsString(args), Text: "bloqueada", Index: tab})
		return "error: " + message
	}

	o.emit(SwarmEvent{Kind: EventAgentToolCall, AgentID: profile.ID, Task: task.Title, Tool: name, Args: argsString(args), Index: tab})

	// Tools MCP: se delegan al handler inyectado por la UI (manager MCP).
	if strings.HasPrefix(name, "mcp__") {
		result, err := o.callMCP(ctx, name, args)
		if err != nil {
			return "error: " + err.Error()
		}
		o.emit(SwarmEvent{Kind: EventAgentToolResult, AgentID: profile.ID, Task: task.Title, Tool: name, Text: truncateReport(result), Index: tab})
		return result
	}

	proposal, err := agent.BuildProposal(agent.Call{ID: call.ID, Name: name, Arguments: args}, o.cwd)
	if err != nil {
		return "error: " + err.Error()
	}

	// Lectura pura: no requiere permiso.
	if !proposal.Action.RequiresApproval() {
		result, err := executeReadOnly(proposal, o.cwd)
		if err != nil {
			return "error: " + err.Error()
		}
		o.emit(SwarmEvent{Kind: EventAgentToolResult, AgentID: profile.ID, Task: task.Title, Tool: name, Text: firstLines(result, 3), Index: tab})
		return result
	}

	if o.broker != nil {
		o.emit(SwarmEvent{Kind: EventAgentPermission, AgentID: profile.ID, Task: task.Title, Tool: name, Args: argsString(args), Status: StatusWaitingApproval, Index: tab})
		result := o.broker.Request(ctx, PermissionRequest{
			Mission:   o.mission,
			AgentID:   profile.ID,
			AgentName: profile.Name,
			Tool:      name,
			Summary:   proposal.Summary,
			Args:      []byte(args),
		})
		switch result.Decision {
		case DecisionReject:
			return agent.RejectedContent
		case DecisionAlways:
			o.broker.RememberAlways(profile.ID, name)
		}
	}

	output, err := agent.Execute(proposal, o.cwd)
	if err != nil {
		o.emit(SwarmEvent{Kind: EventAgentToolResult, AgentID: profile.ID, Task: task.Title, Tool: name, Text: "error: " + err.Error(), Index: tab})
		return "error: " + err.Error()
	}
	o.emit(SwarmEvent{Kind: EventAgentToolResult, AgentID: profile.ID, Task: task.Title, Tool: name, Text: firstLines(output, 3), Index: tab})
	return output
}

// callMCP enruta una tool MCP a través del caller inyectado por la TUI.
func (o *Orchestrator) callMCP(ctx context.Context, name, args string) (string, error) {
	if o.mcpCall == nil {
		return "", fmt.Errorf("tool MCP %q no disponible en este orquestador", name)
	}
	return o.mcpCall(ctx, name, args)
}

// subAgentTools filtra el catálogo de tools según AllowedTools del perfil.
func (o *Orchestrator) subAgentTools(profile config.SubAgentProfile) []openai.Tool {
	tools := make([]openai.Tool, 0, len(agent.Tools)+len(o.mcpTools)+1)
	for _, tool := range append(append([]openai.Tool(nil), agent.Tools...), agent.AskUserTool) {
		if tool.Function == nil {
			continue
		}
		if ToolAllowed(profile, tool.Function.Name) {
			tools = append(tools, tool)
		}
	}
	for _, tool := range o.mcpTools {
		if tool.Function == nil {
			continue
		}
		if ToolAllowed(profile, tool.Function.Name) {
			tools = append(tools, tool)
		}
	}
	return tools
}

// subAgentSystemPrompt compone el prompt del sub-agente: rol + reglas de
// operación + cwd. Sin historial del orquestador.
func subAgentSystemPrompt(profile config.SubAgentProfile, cwd string) string {
	var b strings.Builder
	if strings.TrimSpace(profile.RolePrompt) != "" {
		b.WriteString(strings.TrimSpace(profile.RolePrompt))
	} else {
		b.WriteString("Eres un sub-agente del equipo MKD.")
	}
	b.WriteString("\n\n")
	b.WriteString("Trabajas de forma autónoma en una subtarea asignada por el Orquestador.\n")
	b.WriteString("Directorio de trabajo: " + valueOr(cwd, ".") + "\n")
	b.WriteString("Herramientas permitidas: " + ToolsForPermissions(profile) + ".\n")
	b.WriteString("Investiga lo necesario, actúa y TERMINA con un reporte conciso y accionable:\n")
	b.WriteString("- Qué encontraste o hiciste (rutas y símbolos).\n")
	b.WriteString("- Resultado/entregable y cualquier riesgo o pendiente.\n")
	b.WriteString("No pidas al usuario intervenir; si algo requiere permiso, la UI lo pedirá por ti.\n")
	return b.String()
}

// taskPrompt formatea la delegación para el sub-agente.
func taskPrompt(task Task, cwd string) string {
	var b strings.Builder
	title := valueOr(task.Title, "Subtarea delegada")
	b.WriteString("TAREA: " + title + "\n")
	if len(task.ContextFiles) > 0 {
		b.WriteString("\nArchivos sugeridos (léelos primero):\n")
		for _, path := range task.ContextFiles {
			b.WriteString("- " + path + "\n")
		}
	}
	b.WriteString("\nINSTRUCCIONES:\n")
	b.WriteString(strings.TrimSpace(task.Instrucciones))
	b.WriteString("\n")
	return b.String()
}

// executeReadOnly ejecuta read/list/search sin modificar nada.
func executeReadOnly(proposal agent.Proposal, cwd string) (string, error) {
	switch proposal.Action {
	case agent.ActionRead:
		return agent.Read(proposal, cwd)
	case agent.ActionList:
		return agent.List(proposal, cwd)
	case agent.ActionSearch:
		return agent.Search(proposal, cwd)
	default:
		return "", fmt.Errorf("acción no soportada en modo lectura: %s", proposal.Action)
	}
}

// FormatReports consolida los reportes de una delegación para el orquestador.
func FormatReports(reports []Report) string {
	if len(reports) == 1 {
		report := reports[0]
		if report.Err != nil {
			return fmt.Sprintf("Sub-agente %s falló: %v", report.AgentID, report.Err)
		}
		return report.Content
	}

	var b strings.Builder
	for _, report := range reports {
		name := valueOr(report.AgentName, report.AgentID)
		b.WriteString("## " + name + " — " + valueOr(report.Task, "tarea"))
		b.WriteString("\n")
		if report.Err != nil {
			b.WriteString("ERROR: " + report.Err.Error() + "\n\n")
			continue
		}
		b.WriteString(report.Content)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func truncateReport(value string) string {
	if len(value) <= maxReportBytes {
		return value
	}
	return value[:maxReportBytes] + "\n… [reporte truncado]"
}

func argsString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "{}"
	}
	return value
}

func firstLines(value string, n int) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func oneLine(value string, width int) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.Join(strings.Fields(value), " ")
	if width > 0 && len([]rune(value)) > width {
		runes := []rune(value)
		return string(runes[:width-1]) + "…"
	}
	return value
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func approxTokens(value string) int {
	runes := len([]rune(value))
	return (runes + 3) / 4
}

// MissionCounter genera IDs de misión estables.
var MissionCounter atomic.Int64

// NewMissionID devuelve un ID de misión incremental.
func NewMissionID() string {
	return fmt.Sprintf("mission-%d", MissionCounter.Add(1))
}
