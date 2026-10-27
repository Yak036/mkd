package ui

// Ejecución del modo Swarm en la TUI.
//
//	/swarm <instrucción>
//	        │
//	        ▼
//	startSwarmMission
//	  ├─ crea EventBus + HITLBroker
//	  ├─ arranca el orquestador (goroutine, tea.Cmd)
//	  └─ lanza waitSwarmEvent (bucle de mensajes)
//
//	waitSwarmEvent ──► Update ──► swarmEventMsg / swarmDoneMsg / swarmPermissionMsg
//	                                    │
//	                        (a) actualizar estado por agente
//	                        (b) HITL → modal Diff (bloquea solo al sub-agente)
//	                        (c) tarjetas + refresco de sidebar/pestañas

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
	"github.com/Yak036/mkd/internal/swarm"
)

// swarmEventMsg transporta un evento del bus a Update.
type swarmEventMsg struct {
	gen int64
	ev  swarm.SwarmEvent
}

// swarmDoneMsg señala que el orquestador terminó.
type swarmDoneMsg struct {
	gen     int64
	content string
	err     error
}

// swarmPermissionMsg es una petición HITL de un sub-agente.
type swarmPermissionMsg struct {
	gen     int64
	request swarm.PermissionRequest
}

// swarmBus es el conjunto de canales vivos de la misión actual.
type swarmBus struct {
	events *swarm.EventBus
	broker *swarm.HITLBroker
}

// waitSwarmEvent drena un evento del bus y lo envía a Update.
func waitSwarmEvent(events <-chan swarm.SwarmEvent, gen int64) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return nil
		}
		return swarmEventMsg{gen: gen, ev: ev}
	}
}

// waitSwarmPermission espera la siguiente petición de permiso.
func waitSwarmPermission(requests <-chan swarm.PermissionRequest, gen int64) tea.Cmd {
	return func() tea.Msg {
		req, ok := <-requests
		if !ok {
			return nil
		}
		return swarmPermissionMsg{gen: gen, request: req}
	}
}

// startSwarmMission prepara el orquestador y lanza la misión.
func (m Model) startSwarmMission(instruction string) (tea.Model, tea.Cmd) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		m.notice = "Uso: /swarm <tarea>"
		return m, nil
	}
	if !m.configured() {
		m.notice = "Ningún modelo disponible actualmente."
		return m.openProviderModal()
	}
	if m.streaming || m.waiting {
		m.notice = "Espera a que termine la generación actual."
		return m, nil
	}

	enabled := swarm.EnabledProfiles(m.cfg.SubAgents)
	if len(enabled) == 0 {
		m.notice = "No hay sub-agentes activos. Configúralos con Ctrl+T (/agents)."
		return m, nil
	}

	m.hideSlashMenu()

	// Tarjeta del usuario con la instrucción de misión.
	display := "🌐 **Misión Swarm**\n\n" + instruction + "\n\n" +
		mutedStyle.Render(fmt.Sprintf("Equipo: %s", rosterNames(enabled)))
	message := chatMessage{Role: ai.RoleUser, Content: instruction, Display: display}
	m.history = append(m.history, message)
	m.blocks = append(m.blocks, m.renderMessage(message))

	if !m.sessionAutoSet {
		m.session.Title = storageDeriveTitle(instruction)
		m.sessionAutoSet = true
	}

	// Estado y canales de la misión.
	m.swarm.reset(swarm.NewMissionID())
	m.swarm.orchestratorStatus = "Planificando la misión…"
	gen := m.swarm.runGen
	events := swarm.NewEventBus()
	broker := swarm.NewHITLBroker()
	m.bus = &swarmBus{events: events, broker: broker}
	m.viewTab = 0

	// Contexto cancelable de la misión.
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	orchestrator := swarm.New(swarm.Config{
		Config:   m.cfg,
		Factory:  swarm.AIClientFactory(m.cfg),
		Cwd:      m.agentCwd(),
		Events:   events,
		Broker:   broker,
		MCPTools: m.mcpToolsForSwarm(),
		MCPCall:  m.mcpCallerForSwarm(),
		Mission:  m.swarm.mission,
	})
	orchestrator.AttachCancel(cancel)
	m.orchestrator = orchestrator
	m.orchestratorCmd = runOrchestratorCmd(ctx, orchestrator, instruction, gen)

	m.waiting = true
	m.streaming = false
	m.notice = ""
	m.reviewing = false
	m.syncSession()
	m.refreshUsage()
	m.refresh()
	m.viewport.GotoBottom()

	return m, tea.Batch(
		m.orchestratorCmd,
		waitSwarmEvent(events.Events(), gen),
		waitSwarmPermission(broker.Requests(), gen),
		m.spinner.Tick,
	)
}

// runOrchestratorCmd ejecuta el tool loop del orquestador en background.
func runOrchestratorCmd(ctx context.Context, o *swarm.Orchestrator, instruction string, gen int64) tea.Cmd {
	return func() tea.Msg {
		client, err := swarm.AIClientFactory(o.Config())(config.SubAgentProfile{})
		if err != nil {
			return swarmDoneMsg{gen: gen, err: err}
		}
		if client == nil || !client.Ready() {
			return swarmDoneMsg{gen: gen, err: ai.ErrNotConfigured}
		}

		messages := []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: o.SystemPrompt()},
			{Role: openai.ChatMessageRoleUser, Content: instruction},
		}
		tools := o.Tools()

		var final strings.Builder
		for round := 0; round < 40; round++ {
			if err := ctx.Err(); err != nil {
				return swarmDoneMsg{gen: gen, err: err}
			}
			assistant, err := client.CompleteMessagesTools(ctx, messages, tools)
			if err != nil {
				return swarmDoneMsg{gen: gen, err: err}
			}
			messages = append(messages, assistant)

			if text := strings.TrimSpace(assistant.Content); text != "" {
				final.WriteString(text)
				final.WriteString("\n\n")
			}

			if len(assistant.ToolCalls) == 0 {
				break
			}

			for _, call := range assistant.ToolCalls {
				result := o.HandleToolCall(ctx, agent.Call{
					ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments,
				})
				messages = append(messages, agent.ResultMessage(call.ID, result))
			}
		}

		return swarmDoneMsg{gen: gen, content: strings.TrimSpace(final.String())}
	}
}

// handleSwarmEvt aplica un evento del bus al estado.
func (m Model) handleSwarmEvt(msg swarmEventMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.swarm.runGen {
		// Evento de una misión vieja: se ignora y se sigue drenando.
		return m, waitSwarmEvent(m.bus.events.Events(), m.swarm.runGen)
	}
	ev := msg.ev
	profile, _ := swarm.FindProfile(m.cfg.SubAgents, ev.AgentID)

	switch ev.Kind {
	case swarm.EventMissionStarted:
		m.swarm.orchestratorStatus = "Delegando tareas…"

	case swarm.EventAgentQueued:
		live := m.swarm.agent(profile)
		live.Task = ev.Task
		if live.Status == 0 && len(live.Steps) == 0 {
			live.Status = swarm.StatusQueued
		}

	case swarm.EventAgentStarted:
		live := m.swarm.agent(profile)
		live.Task = ev.Task
		live.Status = swarm.StatusRunning
		if ev.Index > 0 {
			live.Index = ev.Index
		}
		m.swarm.orchestratorStatus = "Esperando sub-agentes…"

	case swarm.EventAgentStreamChunk:
		live := m.swarm.agent(profile)
		live.Status = swarm.StatusRunning
		if live.Output.Len() > 0 {
			live.Output.WriteString("\n")
		}
		live.Output.WriteString(ev.Text)
		live.LastLine = lastLine(ev.Text)

	case swarm.EventAgentToolCall:
		live := m.swarm.agent(profile)
		live.Status = swarm.StatusRunning
		live.Tool = ev.Tool
		live.Args = summarizeArgs(ev.Args)
		live.Steps = append(live.Steps, agentStep{Tool: ev.Tool, Args: live.Args})
		live.LastLine = ev.Tool + "(" + live.Args + ")"

	case swarm.EventAgentToolResult:
		live := m.swarm.agent(profile)
		for i := len(live.Steps) - 1; i >= 0; i-- {
			if live.Steps[i].Tool == ev.Tool && !live.Steps[i].Done {
				live.Steps[i].Done = true
				live.Steps[i].Text = ev.Text
				break
			}
		}
		live.Tool = ""
		live.Args = ""

	case swarm.EventAgentPermission:
		live := m.swarm.agent(profile)
		live.Status = swarm.StatusWaitingApproval
		live.Tool = ev.Tool
		live.Args = summarizeArgs(ev.Args)

	case swarm.EventAgentCompleted:
		live := m.swarm.agent(profile)
		live.Status = swarm.StatusDone
		live.Duration = ev.Duration
		live.Tokens = ev.Tokens
		live.Tool = ""
		live.Args = ""
		m.swarm.delegations++
		m.appendDelegationRecord(profile, live, "")

	case swarm.EventAgentError:
		live := m.swarm.agent(profile)
		live.Status = swarm.StatusError
		live.Err = ev.Text
		live.Tool = ""
		m.swarm.delegations++
		m.appendDelegationRecord(profile, live, ev.Text)

	case swarm.EventMissionCompleted:
		m.swarm.orchestratorStatus = "Misión completada"
	}

	m.refresh()
	return m, waitSwarmEvent(m.bus.events.Events(), m.swarm.runGen)
}

// appendDelegationRecord añade una tarjeta resumen al chat del orquestador.
func (m *Model) appendDelegationRecord(profile config.SubAgentProfile, live *agentLive, failure string) {
	summary := ""
	if failure != "" {
		summary = "**Error:** " + failure
	} else if output := strings.TrimSpace(live.Output.String()); output != "" {
		summary = truncateMiddle(output, 900)
	}
	card := m.delegationCard(delegationInfo{
		agentID: profile.ID,
		task:    live.Task,
		tool:    lastTool(live),
		summary: summary,
	})
	m.blocks = append(m.blocks, card)
}

// handleSwarmDone cierra la misión y publica el reporte consolidado.
func (m Model) handleSwarmDone(msg swarmDoneMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.swarm.runGen {
		return m, nil
	}

	m.swarm.running = false
	m.swarm.finished = time.Now()
	m.waiting = false
	m.streaming = false
	m.cancel = nil

	if msg.err != nil {
		m.swarm.orchestratorStatus = "Misión con error"
		m.notes = append(m.notes, note{Kind: noteError, Text: "Swarm: " + msg.err.Error()})
	} else {
		m.swarm.orchestratorStatus = "Misión completada"
		if strings.TrimSpace(msg.content) != "" {
			message := chatMessage{Role: ai.RoleAssistant, Content: msg.content, Display: msg.content}
			m.history = append(m.history, message)
			m.blocks = append(m.blocks, m.renderMessage(message))
		}
	}

	// Cierra los canales de la misión.
	if m.bus != nil {
		m.bus.events.Close()
		m.bus.broker.ResolveAll()
	}
	m.orchestrator = nil

	m.refreshUsage()
	m.syncSession()
	m.persistSession(false)
	m.refresh()
	m.viewport.GotoBottom()
	return m, m.maybeAutoCompact()
}

// handleSwarmPermission muestra el modal HITL para un sub-agente.
func (m Model) handleSwarmPermission(msg swarmPermissionMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.swarm.runGen || m.bus == nil {
		return m, waitSwarmPermission(m.swarmPermissionRequests(), m.swarm.runGen)
	}

	req := msg.request
	profile, _ := swarm.FindProfile(m.cfg.SubAgents, req.AgentID)

	// Construye una Proposal sintética para reutilizar el modal de Diff.
	proposal, err := agent.BuildProposal(agent.Call{
		ID: req.ID, Name: req.Tool, Arguments: string(req.Args),
	}, m.agentCwd())
	if err != nil {
		// Sin propuesta válida no hay nada que aprobar: se rechaza y sigue.
		m.bus.broker.Resolve(req.ID, swarm.DecisionReject)
		return m, waitSwarmPermission(m.swarmPermissionRequests(), m.swarm.runGen)
	}

	m.modal.kind = modalDiff
	m.modal.diff = diffModal{
		visible:  true,
		proposal: proposal,
		swarm: &swarmApproval{
			gen:       msg.gen,
			requestID: req.ID,
			agentID:   req.AgentID,
			agentName: valueOr(profile.Name, req.AgentID),
			broker:    m.bus.broker,
		},
	}
	m.textarea.Blur()
	m.resizeModals()
	return m, nil
}

// swarmPermissionRequests devuelve el canal de permisos actual (o nil).
func (m Model) swarmPermissionRequests() <-chan swarm.PermissionRequest {
	if m.bus == nil {
		return nil
	}
	return m.bus.broker.Requests()
}

// ---------------------------------------------------------------------------
// Helpers de modo
// ---------------------------------------------------------------------------

// mcpToolsForSwarm expone las tools MCP al orquestador y sub-agentes.
func (m Model) mcpToolsForSwarm() []openai.Tool {
	if m.mcp == nil {
		return nil
	}
	return m.mcp.Tools()
}

// mcpCallerForSwarm adapta el manager MCP al contrato del orquestador.
func (m Model) mcpCallerForSwarm() swarm.MCPCaller {
	mgr := m.mcp
	if mgr == nil {
		return nil
	}
	return func(ctx context.Context, name, arguments string) (string, error) {
		result, err := mgr.Call(ctx, name, json.RawMessage(arguments))
		text := strings.TrimSpace(result.Text())
		if err != nil {
			if text == "" {
				return "", err
			}
			return text, nil
		}
		return text, nil
	}
}

// toggleSwarmMode alterna SINGLE ↔ SWARM y persiste.
func (m Model) toggleSwarmMode() (tea.Model, tea.Cmd) {
	m.cfg.SwarmMode = config.NextSwarmMode(m.cfg.SwarmMode)
	m.cfg.Normalize()
	if err := m.persistConfig(); err != nil {
		m.notice = "No se pudo cambiar el modo: " + err.Error()
		return m, nil
	}
	if config.NormalizeSwarmMode(m.cfg.SwarmMode) == config.SwarmModeSwarm {
		m.notice = "Modo SWARM ORCHESTRATOR · usa /swarm <tarea>"
	} else {
		m.notice = "Modo SINGLE AGENT"
	}
	m.refresh()
	return m, nil
}

// swarmModeActive indica si el orquestador está activo.
func (m Model) swarmModeActive() bool {
	return config.NormalizeSwarmMode(m.cfg.SwarmMode) == config.SwarmModeSwarm
}

// switchViewTab cambia la pestaña visible (0..9).
func (m Model) switchViewTab(tab int) (tea.Model, tea.Cmd) {
	if tab < 0 || tab > 9 {
		return m, nil
	}
	if tab > 0 {
		if _, ok := m.swarm.agentByTab(tab); !ok {
			m.notice = "No hay sub-agente en la pestaña " + itoa(tab)
			return m, nil
		}
	}
	m.viewTab = tab
	m.refresh()
	return m, nil
}

// nextViewTab rota entre las pestañas existentes (Tab en el inspector).
func (m Model) nextViewTab(delta int) (tea.Model, tea.Cmd) {
	if !m.swarm.active() {
		return m, nil
	}
	tabs := []int{0}
	for _, live := range m.swarm.orderedAgents() {
		if live.Index > 0 && live.Index <= 9 {
			tabs = append(tabs, live.Index)
		}
	}
	if len(tabs) == 1 {
		return m, nil
	}
	current := 0
	for i, tab := range tabs {
		if tab == m.viewTab {
			current = i
			break
		}
	}
	next := (current + delta + len(tabs)) % len(tabs)
	return m.switchViewTab(tabs[next])
}

// rosterNames devuelve "explorer, coder, qa" para la tarjeta de misión.
func rosterNames(profiles []config.SubAgentProfile) string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.ID)
	}
	return strings.Join(names, ", ")
}

// lastTool devuelve la última herramienta ejecutada de un agente.
func lastTool(live *agentLive) string {
	if len(live.Steps) == 0 {
		return ""
	}
	return live.Steps[len(live.Steps)-1].Tool
}

// summarizeArgs acorta un JSON de argumentos para la UI.
func summarizeArgs(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return "{}"
	}
	// Intenta extraer el campo más representativo.
	var payload map[string]any
	if json.Unmarshal([]byte(args), &payload) == nil {
		for _, key := range []string{"path", "query", "command", "name", "text"} {
			if value, ok := payload[key].(string); ok && value != "" {
				return oneLine(value, 48)
			}
		}
	}
	return oneLine(args, 48)
}

// lastLine devuelve la última línea no vacía de un bloque de texto.
func lastLine(value string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return oneLine(line, 60)
		}
	}
	return ""
}

// storageDeriveTitle evita importar storage en varios sitios del swarm.
func storageDeriveTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len([]rune(value)) > 60 {
		return string([]rune(value)[:59]) + "…"
	}
	return value
}

var _ = fmt.Sprintf
var _ = tea.Batch
var _ = mcp.ParsePrefixed

// resolveSwarmApproval entrega la decisión al broker y cierra el modal.
func (m Model) resolveSwarmApproval(approval *swarmApproval, proposal agent.Proposal, decision approvalDecision) (Model, tea.Cmd) {
	m.modal.kind = modalNone
	m.modal.diff = diffModal{}

	swarmDecision := swarm.DecisionReject
	switch decision {
	case ApprovalApply:
		swarmDecision = swarm.DecisionAllow
	case ApprovalAlways:
		swarmDecision = swarm.DecisionAlways
	}

	if approval.broker != nil {
		approval.broker.Resolve(approval.requestID, swarmDecision)
	}

	// El sub-agente estaba bloqueado esperando; vuelve a "ejecutando".
	if live, ok := m.swarm.agents[approval.agentID]; ok {
		if swarmDecision == swarm.DecisionReject {
			live.Status = swarm.StatusRunning
		} else {
			live.Status = swarm.StatusRunning
		}
	}

	label := "✖ rechazado"
	switch swarmDecision {
	case swarm.DecisionAllow:
		label = "✔ aprobado"
	case swarm.DecisionAlways:
		label = "◈ aprobado (siempre en la misión)"
	}
	m.notes = append(m.notes, note{Kind: noteInfo, Text: label + ": " + approval.agentID + " · " + proposal.Tool})
	m.refresh()

	// Vuelve a escuchar peticiones de permiso de la misión.
	gen := m.swarm.runGen
	return m, waitSwarmPermission(m.swarmPermissionRequests(), gen)
}

// swarmTabKey traduce "alt+0".."alt+9" en un índice de pestaña.
func swarmTabKey(keyName string) (int, bool) {
	if !strings.HasPrefix(keyName, "alt+") {
		return 0, false
	}
	digit := keyName[len("alt+"):]
	if len(digit) != 1 || digit[0] < '0' || digit[0] > '9' {
		return 0, false
	}
	return int(digit[0] - '0'), true
}
