package swarm

// Bus de eventos y broker HITL.
//
//	sub-agente ──emit──► Bus ──chan SwarmEvent──► Bubbletea (tea.Msg)
//	   │
//	   │ necesita permiso
//	   ▼
//	Broker.Request ──(bloquea la goroutine)──► modal Diff (UI)
//	                     ◄── Decision ─────────┘
//
// El bus es no bloqueante: si nadie escucha, los eventos se descartan para no
// frenar a los sub-agentes. El broker serializa las peticiones de permiso con
// un mutex, de modo que solo un sub-agente muestra el modal a la vez.

import (
	"context"
	"sync"
	"time"
)

// EventKind clasifica un evento del swarm.
type EventKind int

const (
	// EventMissionStarted: comienza una misión orquestada.
	EventMissionStarted EventKind = iota
	// EventAgentQueued: un sub-agente quedó en cola (delegate_parallel).
	EventAgentQueued
	// EventAgentStarted: un sub-agente arrancó.
	EventAgentStarted
	// EventAgentStreamChunk: fragmento de razonamiento/texto del sub-agente.
	EventAgentStreamChunk
	// EventAgentToolCall: el sub-agente invocó una herramienta.
	EventAgentToolCall
	// EventAgentToolResult: resultado de una herramienta del sub-agente.
	EventAgentToolResult
	// EventAgentPermission: el sub-agente pide permiso HITL.
	EventAgentPermission
	// EventAgentCompleted: el sub-agente terminó correctamente.
	EventAgentCompleted
	// EventAgentError: el sub-agente falló.
	EventAgentError
	// EventMissionCompleted: la misión completa terminó.
	EventMissionCompleted
)

// String devuelve un nombre estable útil para depuración.
func (k EventKind) String() string {
	switch k {
	case EventMissionStarted:
		return "mission_started"
	case EventAgentQueued:
		return "agent_queued"
	case EventAgentStarted:
		return "agent_started"
	case EventAgentStreamChunk:
		return "agent_stream"
	case EventAgentToolCall:
		return "agent_tool_call"
	case EventAgentToolResult:
		return "agent_tool_result"
	case EventAgentPermission:
		return "agent_permission"
	case EventAgentCompleted:
		return "agent_completed"
	case EventAgentError:
		return "agent_error"
	case EventMissionCompleted:
		return "mission_completed"
	default:
		return "unknown"
	}
}

// AgentStatus es el estado visible de un sub-agente.
type AgentStatus int

const (
	// StatusQueued: esperando turno.
	StatusQueued AgentStatus = iota
	// StatusRunning: trabajando.
	StatusRunning
	// StatusWaitingApproval: bloqueado esperando permiso HITL.
	StatusWaitingApproval
	// StatusDone: completado.
	StatusDone
	// StatusError: falló.
	StatusError
)

// String devuelve una etiqueta corta para la TUI.
func (s AgentStatus) String() string {
	switch s {
	case StatusRunning:
		return "ejecutando"
	case StatusWaitingApproval:
		return "esperando permiso"
	case StatusDone:
		return "completado"
	case StatusError:
		return "error"
	default:
		return "en cola"
	}
}

// SwarmEvent es un evento emitido por el orquestador o un sub-agente.
type SwarmEvent struct {
	Kind EventKind
	// Mission identifica la misión (permite ignorar eventos de misiones viejas).
	Mission string
	// AgentID identifica al sub-agente emisor ("" = orquestador).
	AgentID string
	// Task es el título de la tarea asignada.
	Task string
	// Text transporta texto (chunk, mensaje de error o resultado).
	Text string
	// Tool es el nombre de la herramienta invocada.
	Tool string
	// Args son los argumentos crudos de la herramienta.
	Args string
	// Status es el estado resultante del agente, si aplica.
	Status AgentStatus
	// Duration y Tokens se llenan al completar.
	Duration time.Duration
	Tokens   int
	// Index es la posición del agente en la misión (para Alt+1..9).
	Index int
}

// EventBus distribuye eventos hacia la TUI sin bloquear a los emisores.
type EventBus struct {
	mu     sync.RWMutex
	ch     chan SwarmEvent
	closed bool
}

// NewEventBus crea un bus con buffer amplio para absorber ráfagas.
func NewEventBus() *EventBus {
	return &EventBus{ch: make(chan SwarmEvent, 512)}
}

// Events devuelve el canal de lectura que consume Bubbletea.
func (b *EventBus) Events() <-chan SwarmEvent {
	return b.ch
}

// Emit publica un evento. Nunca bloquea: si el buffer está lleno se descarta.
func (b *EventBus) Emit(event SwarmEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	select {
	case b.ch <- event:
	default:
		// Buffer lleno: preferimos perder un evento de progreso a frenar al
		// sub-agente. Los eventos terminales (completed/error) llegan por
		// canales separados en el orquestador.
	}
}

// Close cierra el canal de eventos una sola vez.
func (b *EventBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	close(b.ch)
}

// ---------------------------------------------------------------------------
// HITL broker
// ---------------------------------------------------------------------------

// PermissionRequest es una solicitud de permiso de un sub-agente.
type PermissionRequest struct {
	// ID es el identificador opaco con el que la UI responde (Resolve).
	ID        string
	Mission   string
	AgentID   string
	AgentName string
	Tool      string
	Summary   string
	// Args son los argumentos de la tool para construir la Proposal.
	Args []byte
}

// Decision es la respuesta del usuario a un permiso.
type Decision int

const (
	// DecisionReject rechaza la operación.
	DecisionReject Decision = iota
	// DecisionAllow permite solo esta vez.
	DecisionAllow
	// DecisionAlways permite y recuerda para el resto de la misión.
	DecisionAlways
)

// PermissionResult asocia una decisión a una solicitud.
type PermissionResult struct {
	Decision Decision
	// Auto indica que se auto-aprobó sin intervención del usuario.
	Auto bool
}

// HITLBroker serializa las solicitudes de permiso de los sub-agentes.
//
// Un sub-agente llama Request y se bloquea en el canal devuelto; la UI recibe
// la solicitud por Pending(), muestra el modal y responde con Resolve.
type HITLBroker struct {
	// Mu serializa el diálogo: solo una petición visible a la vez.
	Mu sync.Mutex

	requests chan PermissionRequest
	mu       sync.Mutex
	waiting  map[string]chan PermissionResult
	ids      int

	// autoApprove recuerda tool|agent aprobadas con "siempre" en la misión.
	autoApprove map[string]bool
}

// NewHITLBroker construye el broker.
func NewHITLBroker() *HITLBroker {
	return &HITLBroker{
		requests:    make(chan PermissionRequest, 8),
		waiting:     make(map[string]chan PermissionResult),
		autoApprove: make(map[string]bool),
	}
}

// Requests devuelve el canal que escucha la UI.
func (h *HITLBroker) Requests() <-chan PermissionRequest {
	return h.requests
}

// Request envía una solicitud y espera la decisión.
//
// Si la tool ya fue aprobada con "siempre" devuelve DecisionAlways de inmediato.
// El ctx permite cancelar la espera (al salir o cancelar la misión).
func (h *HITLBroker) Request(ctx context.Context, req PermissionRequest) PermissionResult {
	key := h.key(req.AgentID, req.Tool)
	h.mu.Lock()
	if h.autoApprove[key] {
		h.mu.Unlock()
		return PermissionResult{Decision: DecisionAlways, Auto: true}
	}
	h.ids++
	id := shardKey(key, h.ids)
	answer := make(chan PermissionResult, 1)
	h.waiting[id] = answer
	h.mu.Unlock()

	reqCopy := req
	reqCopy.ID = id // la UI responde por este id opaco

	// Serializa la PRESENTACIÓN del modal: solo una petición visible a la vez.
	// Se libera al resolverla, de modo que el siguiente sub-agente en espera
	// puede mostrarse en cuanto el usuario decide.
	h.Mu.Lock()
	defer h.Mu.Unlock()
	select {
	case h.requests <- reqCopy:
	case <-ctx.Done():
		h.forget(id)
		return PermissionResult{Decision: DecisionReject}
	}

	select {
	case result := <-answer:
		return result
	case <-ctx.Done():
		h.forget(id)
		return PermissionResult{Decision: DecisionReject}
	}
}

// Resolve entrega la decisión al sub-agente que espera por requestID.
func (h *HITLBroker) Resolve(requestID string, decision Decision) {
	h.mu.Lock()
	answer, ok := h.waiting[requestID]
	delete(h.waiting, requestID)
	h.mu.Unlock()
	if !ok {
		return
	}
	select {
	case answer <- PermissionResult{Decision: decision}:
	default:
	}
}

// ResolveAll rechaza cualquier petición pendiente (al cancelar la misión).
func (h *HITLBroker) ResolveAll() {
	h.mu.Lock()
	pending := h.waiting
	h.waiting = make(map[string]chan PermissionResult)
	h.mu.Unlock()
	for _, answer := range pending {
		select {
		case answer <- PermissionResult{Decision: DecisionReject}:
		default:
		}
	}
}

// RememberAlways marca una tool+agente como aprobada para la sesión del broker.
func (h *HITLBroker) RememberAlways(agentID, tool string) {
	h.mu.Lock()
	h.autoApprove[h.key(agentID, tool)] = true
	h.mu.Unlock()
}

// Reset limpia las aprobaciones "siempre" (al iniciar una misión nueva).
func (h *HITLBroker) Reset() {
	h.mu.Lock()
	h.autoApprove = make(map[string]bool)
	h.mu.Unlock()
	h.ResolveAll()
}

func (h *HITLBroker) forget(id string) {
	h.mu.Lock()
	delete(h.waiting, id)
	h.mu.Unlock()
}

func (h *HITLBroker) key(agentID, tool string) string {
	return agentID + "|" + tool
}

func shardKey(base string, id int) string {
	return base + "#" + itoa(id)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
