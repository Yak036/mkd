package ui

// Bucle de agente (Tool Calling + HITL).
//
//	mensaje del usuario
//	        │
//	        ▼
//	┌──────────────────────┐
//	│ CompleteMessagesTools│◄──────────────┐
//	└──────────┬───────────┘               │
//	           │ tool_calls?               │
//	   ┌───────┴────────┐                  │
//	   │ no             │ sí               │
//	   ▼                ▼                  │
//	imprime texto   BuildProposal          │
//	                ┌────┴─────┐           │
//	         auto   │          │  requiere │
//	                ▼          ▼  permiso  │
//	             Execute   modal diff ─────┤
//	                │      (Y/N/A)          │
//	                └──────► role=tool ─────┘
//
// El tope de rondas evita bucles infinitos de auto-corrección.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	openai "github.com/sashabaranov/go-openai"

	"github.com/Yak036/mkd/internal/agent"
	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/config"
	"github.com/Yak036/mkd/internal/mcp"
)

// maxAgentRounds limita cuántas veces el modelo puede pedir herramientas en
// un mismo turno antes de cortar (50 ≈ 25 llamadas agrupadas).
const maxAgentRounds = 50

// agentToolCallsMsg llega cuando el modelo pidió herramientas.
type agentToolCallsMsg struct {
	assistant openai.ChatCompletionMessage
	calls     []agent.Call
	err       error
}

// agentToolDoneMsg transporta el resultado de una herramienta ejecutada.
type agentToolDoneMsg struct {
	callID  string
	name    string
	content string
	err     error
	// mcp/ server/ tool identifican las llamadas a servidores MCP.
	mcp    bool
	server string
	tool   string
}

// agentRoundDoneMsg cierra el turno con el texto final del asistente.
type agentRoundDoneMsg struct {
	content string
	err     error
}

// agentState agrupa el estado del bucle para no ensuciar Model más de la cuenta.
type agentState struct {
	// messages es la ventana que se envía al modelo (incluye role=tool).
	messages []openai.ChatCompletionMessage

	// pending contiene las llamadas aún sin resolver de la ronda actual.
	pending []agent.Call

	// proposals cachea las propuestas por ID de llamada para el modal HITL.
	proposals map[string]agent.Proposal

	// autoApprove recuerda las herramientas aprobadas con [A] en la sesión.
	autoApprove map[string]bool

	// decision guarda la resolución tomada en el modal (apply/reject/always).
	decision map[string]approvalDecision

	// flags mantiene punteros estables para el modal HITL ([A] = siempre).
	flags map[string]*bool

	// results indexa los role=tool recuperables por ID tras el modal.
	results map[string]string

	rounds int
	active bool

	// requested indica que el turno en curso debe usar herramientas.
	requested bool

	// manualRules son las reglas forzadas con `:@rule-nombre` en la sesión.
	manualRules []string
}

// ensureAgentState inicializa el estado del bucle la primera vez.
func (m *Model) ensureAgentState() *agentState {
	if m.agent.proposals == nil {
		m.agent.proposals = map[string]agent.Proposal{}
	}
	if m.agent.autoApprove == nil {
		m.agent.autoApprove = map[string]bool{}
	}
	if m.agent.decision == nil {
		m.agent.decision = map[string]approvalDecision{}
	}
	if m.agent.results == nil {
		m.agent.results = map[string]string{}
	}
	if m.mcpPending == nil {
		m.mcpPending = map[string]agent.Call{}
	}
	return &m.agent
}

// agentEnabled indica si el modo actual permite herramientas.
func (m Model) agentEnabled() bool {
	return m.agent.requested
}

// agentSystemPrompt añade instrucciones de herramienta al system prompt.
func (m Model) agentSystemPrompt() string {
	mode := config.NormalizeMode(m.cfg.AgentMode)
	var b strings.Builder
	b.WriteString("Eres MKD en modo agente. Tienes herramientas para explorar y modificar el proyecto.\n")
	b.WriteString("- Usa read_file/list_dir/search_code para investigar antes de proponer cambios.\n")
	b.WriteString("- Usa patch_file para cambios quirúrgicos y write_file solo para archivos nuevos o reescrituras completas.\n")
	b.WriteString("- Tras escribir código, usa run_command para compilar/probar y corrige los errores en la siguiente ronda.\n")
	b.WriteString("- Explica brevemente qué vas a hacer antes de cada herramienta.\n")
	b.WriteString("- Las herramientas con prefijo mcp__<servidor>__<tool> provienen de servidores MCP externos; úsalas cuando aporten datos del entorno (notas, base de datos, etc.).\n")
	switch mode {
	case config.ModePlan:
		b.WriteString("- MODO PLAN: solo lectura. No escribas ni ejecutes; describe el plan y los cambios propuestos en texto.\n")
	case config.ModeAuto:
		b.WriteString("- MODO AUTO: los cambios dentro del proyecto se aprueban automáticamente. Avanza sin pedir confirmación.\n")
	default:
		b.WriteString("- MODO DEFAULT: cada escritura o comando requiere confirmación del usuario; espera su aprobación.\n")
	}
	return b.String()
}

// startAgentRound lanza una petición con herramientas. El contenido previo del
// modelo (si lo hay) ya se ha volcado en el historial de la TUI.
func (m Model) startAgentRound() (Model, tea.Cmd) {
	state := m.ensureAgentState()
	if !m.configured() {
		m.notes = append(m.notes, note{Kind: noteError, Text: "Ningún modelo disponible actualmente."})
		m.refresh()
		return m, nil
	}

	state.rounds++
	if state.rounds > maxAgentRounds {
		state.active = false
		m.waiting = false
		m.streaming = false
		m.notes = append(m.notes, note{Kind: noteError, Text: "Límite de rondas de agente alcanzado."})
		m.refresh()
		return m, nil
	}

	m.waiting = true
	m.streaming = false
	m.notice = ""

	client := m.client
	messages := state.messages
	tools := m.agentTools()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	return m, tea.Batch(
		agentRoundCmd(ctx, client, messages, tools),
		m.spinner.Tick,
	)
}

func agentRoundCmd(ctx context.Context, client *ai.Client, messages []openai.ChatCompletionMessage, tools []openai.Tool) tea.Cmd {
	return func() tea.Msg {
		assistant, err := client.CompleteMessagesTools(ctx, messages, tools)
		if err != nil {
			return agentToolCallsMsg{err: err}
		}
		calls := agent.ParseCalls(assistant.ToolCalls)
		if len(calls) == 0 {
			return agentRoundDoneMsg{content: assistant.Content}
		}
		return agentToolCallsMsg{assistant: assistant, calls: calls}
	}
}

// handleAgentToolCalls procesa la ronda: imprime texto parcial, resuelve las
// herramientas auto-aprobadas y abre el modal para las que requieren permiso.
func (m Model) handleAgentToolCalls(msg agentToolCallsMsg) (tea.Model, tea.Cmd) {
	state := m.ensureAgentState()
	m.waiting = false

	if msg.err != nil {
		state.active = false
		m.failStream(msg.err)
		return m, nil
	}

	if text := strings.TrimSpace(msg.assistant.Content); text != "" {
		message := chatMessage{Role: ai.RoleAssistant, Content: msg.assistant.Content, Display: msg.assistant.Content}
		m.history = append(m.history, message)
		m.blocks = append(m.blocks, m.renderMessage(message))
		m.refresh()
		m.viewport.GotoBottom()
	}

	// La ventana enviada al modelo debe incluir el mensaje del asistente con
	// sus tool_calls para que los role=tool posteriores tengan sentido.
	state.messages = append(state.messages, msg.assistant)
	state.pending = msg.calls

	m.streaming = true
	return m.resolveNextProposal()
}

// resolveNextProposal toma la siguiente llamada pendiente. Auto-aprobadas se
// ejecutan; el resto abre el modal de diff y se resuelve al elegir Y/N/A.
func (m Model) resolveNextProposal() (Model, tea.Cmd) {
	state := m.ensureAgentState()

	for len(state.pending) > 0 {
		call := state.pending[0]
		state.pending = state.pending[1:]

		// Herramientas MCP: se enrutan al servidor. Solo se ejecutan sin HITL
		// si el servidor tiene auto_approve o el usuario ya aprobó esa tool en
		// la sesión; si no, se muestra el mismo modal de permiso que las tools
		// nativas de escritura.
		if server, tool, ok := mcp.ParsePrefixed(call.Name); ok {
			if m.mcpCallTrusted(server, call.Name) {
				m.streaming = true
				return m, m.runMCPCallCmd(call)
			}
			proposal := mcpProposal(server, tool, call)
			state.proposals[call.ID] = proposal
			state.decision[call.ID] = ApprovalReject
			m.mcpPending[call.ID] = call
			open, cmd := m.openDiffModal(proposal, m.autoApproveFlag(call.Name))
			return open, cmd
		}

		cwd := m.agentCwd()
		proposal, err := agent.BuildProposal(call, cwd)
		if err != nil {
			// Error de argumentos: devolvemos el error al modelo para que se
			// autocorrija en lugar de abortar el turno.
			content := "error: " + err.Error()
			if agent.IsArgumentError(err) {
				content = "error de argumentos: " + err.Error()
			}
			state.messages = append(state.messages, agent.ResultMessage(call.ID, content))
			continue
		}

		// Modo plan: las acciones de escritura/ejecución se rechazan sin abrir modal.
		if config.NormalizeMode(m.cfg.AgentMode) == config.ModePlan && proposal.Action.RequiresApproval() {
			content := "Bloqueado por el modo PLAN: solo lectura y búsqueda están permitidos. Describe el cambio propuesto en tu respuesta."
			state.messages = append(state.messages, agent.ResultMessage(call.ID, content))
			state.decision[call.ID] = ApprovalReject
			m.notes = append(m.notes, note{Kind: noteInfo, Text: "PLAN: se bloqueó " + proposal.Tool})
			continue
		}

		mode := config.NormalizeMode(m.cfg.AgentMode)
		auto := proposal.AutoApproved || mode == config.ModeAuto || state.autoApprove[proposal.Tool]

		// En modo auto no se abre el modal: se aplica y se avisa en el chat.
		if auto {
			state.proposals[call.ID] = proposal
			state.decision[call.ID] = ApprovalApply
			if !proposal.AutoApproved {
				m.notes = append(m.notes, note{Kind: noteInfo, Text: "◈ auto: " + autoSummary(proposal)})
			}
			return m, m.runProposalCmd(proposal, call.ID)
		}

		state.proposals[call.ID] = proposal
		state.decision[call.ID] = ApprovalReject
		open, cmd := m.openDiffModal(proposal, m.autoApproveFlag(proposal.Tool))
		return open, cmd
	}

	// Sin más llamadas pendientes: pedimos al modelo que continúe con los
	// resultados acumulados en la ventana.
	m.streaming = false
	return m.startAgentRound()
}

// autoApproveFlag devuelve el puntero al flag del set de la sesión.
func (m *Model) autoApproveFlag(tool string) *bool {
	state := m.ensureAgentState()
	value := state.autoApprove[tool]
	state.autoApprove[tool] = value
	// No podemos devolver &state.autoApprove[tool] (map), así que usamos un
	// mapa auxiliar de punteros estables.
	if state.flags == nil {
		state.flags = map[string]*bool{}
	}
	ptr, ok := state.flags[tool]
	if !ok {
		flag := state.autoApprove[tool]
		ptr = &flag
		state.flags[tool] = ptr
	}
	return ptr
}

func (m Model) agentCwd() string {
	if m.ctxMgr != nil && m.ctxMgr.WorkDir != "" {
		return m.ctxMgr.WorkDir
	}
	cwd, _ := getwd()
	return cwd
}

// appendToolResult añade un role=tool a la ventana del modelo.
func (m *Model) appendToolResult(callID, content string) {
	if callID == "" {
		return
	}
	state := m.ensureAgentState()
	state.messages = append(state.messages, agent.ResultMessage(callID, content))
}

// completePendingToolCall cierra la llamada en curso con el contenido dado,
// cierra el modal y continúa el bucle del agente.
func (m Model) completePendingToolCall(content string) (tea.Model, tea.Cmd) {
	state := m.ensureAgentState()
	proposal := m.modal.diff.proposal

	var callID string
	for id, candidate := range state.proposals {
		if candidate.Tool == proposal.Tool && candidate.Command == proposal.Command && candidate.Path == proposal.Path {
			callID = id
			break
		}
	}

	m.modal.kind = modalNone
	m.modal.diff = diffModal{}
	m.waiting = false
	m.streaming = true

	if callID != "" {
		state.messages = append(state.messages, agent.ResultMessage(callID, content))
		delete(state.proposals, callID)
		delete(state.decision, callID)
	}
	m.refresh()
	return m.resolveNextProposal()
}

// rejectPendingToolCall descarta la propuesta abierta y sigue el bucle.
func (m Model) rejectPendingToolCall() (tea.Model, tea.Cmd) {
	return m.completePendingToolCall(agent.RejectedContent)
}

// runProposalCmd ejecuta una propuesta aprobada y devuelve el resultado.
func (m Model) runProposalCmd(proposal agent.Proposal, callID string) tea.Cmd {
	cwd := m.agentCwd()
	client := m.client
	messages := append([]openai.ChatCompletionMessage(nil), m.agent.messages...)
	return func() tea.Msg {
		output, err := agent.Execute(proposal, cwd)
		if err != nil {
			output = "error: " + err.Error()
		}
		_ = callID
		_ = client
		_ = messages
		return agentToolDoneMsg{
			callID:  callID,
			name:    proposal.Tool,
			content: output,
			err:     err,
		}
	}
}

// handleMCPToolDone registra el resultado de una tool MCP y continúa el bucle.
func (m Model) handleMCPToolDone(msg agentToolDoneMsg) (tea.Model, tea.Cmd) {
	state := m.ensureAgentState()
	state.messages = append(state.messages, agent.ResultMessage(msg.callID, msg.content))
	delete(m.mcpPending, msg.callID)

	header := fmt.Sprintf("🔌 MCP [%s] -> %s", msg.server, msg.tool)
	if msg.err != nil {
		header += " · error"
	}
	m.blocks = append(m.blocks, mutedStyle.Render(header)+"\n"+m.renderToolDetail(msg.content))
	m.streaming = true
	m.refresh()
	m.viewport.GotoBottom()
	return m.resolveNextProposal()
}

// handleAgentToolDone registra el resultado en el chat y en la ventana del
// modelo, y continúa con la siguiente llamada pendiente.
func (m Model) handleAgentToolDone(msg agentToolDoneMsg) (tea.Model, tea.Cmd) {
	state := m.ensureAgentState()

	state.messages = append(state.messages, agent.ResultMessage(msg.callID, msg.content))

	header := "› " + msg.name + " · resultado de herramienta"
	switch {
	case msg.mcp && msg.err != nil:
		header = fmt.Sprintf("🔌 MCP [%s] -> %s · error", msg.server, msg.tool)
	case msg.mcp:
		header = fmt.Sprintf("🔌 MCP [%s] -> %s", msg.server, msg.tool)
	case msg.err != nil:
		header = "› " + msg.name + " · error"
	}
	m.blocks = append(m.blocks, mutedStyle.Render(header)+"\n"+m.renderToolDetail(msg.content))
	m.refresh()
	m.viewport.GotoBottom()

	return m.resolveNextProposal()
}

// handleAgentRoundDone cierra el turno con la respuesta final del modelo.
func (m Model) handleAgentRoundDone(msg agentRoundDoneMsg) (tea.Model, tea.Cmd) {
	state := m.ensureAgentState()
	m.waiting = false
	m.streaming = false
	m.cancel = nil

	if msg.err != nil {
		state.active = false
		m.failStream(msg.err)
		return m, nil
	}

	content := strings.TrimSpace(msg.content)
	if content != "" {
		message := chatMessage{Role: ai.RoleAssistant, Content: content, Display: content}
		m.history = append(m.history, message)
		m.blocks = append(m.blocks, m.renderMessage(message))
	}

	state.active = false
	state.messages = nil
	state.pending = nil
	state.proposals = map[string]agent.Proposal{}
	state.decision = map[string]approvalDecision{}
	state.flags = map[string]*bool{}
	state.results = map[string]string{}
	state.rounds = 0
	m.agent.requested = false

	m.refreshUsage()
	m.syncSession()
	m.persistSession(false)
	m.refresh()
	m.viewport.GotoBottom()
	return m, m.maybeAutoCompact()
}

// mcpCallTrusted decide si una tool MCP puede ejecutarse sin permisos.
func (m Model) mcpCallTrusted(server, qualified string) bool {
	state := m.ensureAgentState()
	if state.autoApprove[qualified] {
		return true
	}
	cfg, ok := m.cfg.MCPServers[server]
	return ok && cfg.AutoApprove
}

// mcpProposal construye una Proposal sintética para el modal HITL de una tool
// MCP. ActionRun fuerza RequiresApproval y hace que el modal muestre el
// comando equivalente en lugar de un diff.
func mcpProposal(server, tool string, call agent.Call) agent.Proposal {
	return agent.Proposal{
		Action:  agent.ActionRun,
		Tool:    call.Name,
		Command: fmt.Sprintf("MCP [%s] -> %s %s", server, tool, oneLine(string(call.Arguments), 80)),
		Summary: "ejecutar tool MCP " + tool + " en " + server,
	}
}

// agentTools combina las 4 herramientas nativas de MKD con las expuestas por
// los servidores MCP conectados (prefijo mcp__<server>__<tool>).
func (m Model) agentTools() []openai.Tool {
	tools := append([]openai.Tool(nil), agent.Tools...)
	if m.mcp != nil {
		tools = append(tools, m.mcp.Tools()...)
	}
	return tools
}

// runMCPCallCmd ejecuta una herramienta MCP en background.
func (m Model) runMCPCallCmd(call agent.Call) tea.Cmd {
	mgr := m.mcp
	name := call.Name
	arguments := call.Arguments
	callID := call.ID
	server, tool, _ := mcp.ParsePrefixed(name)

	return func() tea.Msg {
		if mgr == nil {
			return agentToolDoneMsg{callID: callID, name: name, content: "error: gestor MCP no disponible", err: nil}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		result, err := mgr.Call(ctx, name, json.RawMessage(arguments))
		content := strings.TrimSpace(result.Text())
		if err != nil {
			if content == "" {
				content = "error: " + err.Error()
			}
			return agentToolDoneMsg{callID: callID, name: name, content: content, err: err, mcp: true, server: server, tool: tool}
		}
		if content == "" {
			content = "(sin contenido de texto)"
		}
		return agentToolDoneMsg{callID: callID, name: name, content: content, mcp: true, server: server, tool: tool}
	}
}

// startAgent arranca el bucle para el prompt recién añadido al historial.
func (m Model) startAgent() (Model, tea.Cmd) {
	state := m.ensureAgentState()
	state.messages = nil
	state.pending = nil
	state.proposals = map[string]agent.Proposal{}
	state.decision = map[string]approvalDecision{}
	state.flags = map[string]*bool{}
	state.results = map[string]string{}
	state.rounds = 0
	state.active = true
	m.agent.requested = true

	prepared, _ := m.ctxMgr.Prepare(m.summary, m.apiHistory())
	base := prepared.Messages()
	if extra := m.mentionsContextBlock(); extra != "" {
		base = append([]openai.ChatCompletionMessage{{
			Role:    openai.ChatMessageRoleSystem,
			Content: extra,
		}}, base...)
	}
	base = append(base, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: m.agentSystemPrompt(),
	})
	state.messages = base

	m.notes = append(m.notes, note{Kind: noteInfo, Text: "◈ agente activo · modo " + config.ModeLabel(m.cfg.AgentMode)})
	m.stream.Reset()
	m.cancelled = false
	return m.startAgentRound()
}

// renderToolDetail pinta el resultado de una herramienta con estilo de bloque
// de código y límite de líneas para no inundar la conversación.
func (m Model) renderToolDetail(content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	const maxLines = 14
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], mutedStyle.Render(fmt.Sprintf("… (%d líneas más)", len(lines)-maxLines)))
	}
	body := strings.Join(lines, "\n")
	return mutedStyle.Render(body)
}

// truncateMiddle recorta por el centro preservando el final (errores).
func truncateMiddle(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	half := limit / 2
	return string(runes[:half]) + "\n…\n" + string(runes[len(runes)-half:])
}

// autoSummary describe una acción auto-aprobada para el chat.
func autoSummary(proposal agent.Proposal) string {
	switch proposal.Action {
	case agent.ActionWrite, agent.ActionPatch:
		return fmt.Sprintf("%s %s (+%d / -%d)", proposal.Tool, proposal.Path, proposal.Adds, proposal.Removes)
	case agent.ActionRun:
		return proposal.Tool + ": " + proposal.Command
	default:
		return proposal.Tool
	}
}
