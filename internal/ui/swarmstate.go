package ui

// Estado del modo Swarm en la TUI.
//
//	swarmState
//	├── mission     misión en curso ("" = ninguna)
//	├── agents      estado en vivo por sub-agente
//	├── tabs        orden de pestañas Alt+0..9
//	├── activeTab   0 = Orquestador, 1..9 = sub-agente
//	├── logs        streaming en vivo por agente
//	└── runGen      generación para descartar eventos viejos
//
//	Alt+0 ──► chat del Orquestador
//	Alt+1..9 ──► stream en vivo del sub-agente N

import (
	"strings"
	"sync/atomic"
	"time"

	"mkd/internal/config"
	"mkd/internal/swarm"
)

// swarmRunCounter genera generaciones monotónicas para descartar eventos de
// misiones canceladas o anteriores.
var swarmRunCounter atomic.Int64

// agentLive es el estado en vivo de un sub-agente.
type agentLive struct {
	ID     string
	Name   string
	Icon   string
	Color  string
	Task   string
	Status swarm.AgentStatus
	// Tool es la última herramienta invocada (para el árbol del sidebar).
	Tool string
	// Args son los argumentos resumidos de esa tool.
	Args string
	// LastLine es la última línea de progreso visible.
	LastLine string
	// Output acumula el streaming del sub-agente.
	Output strings.Builder
	// Tokens estimados y duración al completar.
	Tokens   int
	Duration time.Duration
	// Index es la pestaña asignada (1..9).
	Index int
	// Err guarda el mensaje de error si falló.
	Err string
	// Steps es el historial corto de herramientas ejecutadas.
	Steps []agentStep
}

// agentStep es una entrada del log de herramientas de un sub-agente.
type agentStep struct {
	Tool  string
	Args  string
	Done  bool
	Error bool
	Text  string
}

// swarmState agrupa todo el estado de la misión.
type swarmState struct {
	mission string
	gen     int64
	agents  map[string]*agentLive
	// order conserva las pestañas en orden de aparición (agentID).
	order []string
	// runGen identifica la generación activa.
	runGen int64
	// running indica que la misión sigue en curso.
	running bool
	// orchestratorStatus es el texto del nodo orquestador.
	orchestratorStatus string
	// delegated es el número de delegaciones resueltas.
	delegations int
	// started es el instante de arranque de la misión.
	started time.Time
	// finished es el instante de fin (zero si sigue).
	finished time.Time
}

// newSwarmState inicializa el estado vacío.
func newSwarmState() swarmState {
	return swarmState{agents: map[string]*agentLive{}}
}

// active indica que hay una misión visible.
func (s swarmState) active() bool { return s.mission != "" }

// agent devuelve (creando si hace falta) el estado de un agente.
func (s *swarmState) agent(profile config.SubAgentProfile) *agentLive {
	if s.agents == nil {
		s.agents = map[string]*agentLive{}
	}
	if live, ok := s.agents[profile.ID]; ok {
		return live
	}
	live := &agentLive{
		ID:    profile.ID,
		Name:  profile.Name,
		Icon:  valueOr(profile.Icon, "󰀐"),
		Color: config.NormalizeAgentColor(profile.Color),
		Index: len(s.order) + 1,
	}
	// Asigna la próxima pestaña disponible.
	if live.Index > 9 {
		live.Index = 9
	}
	s.agents[profile.ID] = live
	s.order = append(s.order, profile.ID)
	return live
}

// orderedAgents devuelve los agentes en orden de pestaña.
func (s swarmState) orderedAgents() []*agentLive {
	out := make([]*agentLive, 0, len(s.order))
	for _, id := range s.order {
		if live, ok := s.agents[id]; ok {
			out = append(out, live)
		}
	}
	return out
}

// agentByTab busca el agente cuyo índice de pestaña coincide.
func (s swarmState) agentByTab(tab int) (*agentLive, bool) {
	for _, live := range s.agents {
		if live.Index == tab {
			return live, true
		}
	}
	return nil, false
}

// runningCount cuenta agentes activos.
func (s swarmState) runningCount() int {
	count := 0
	for _, live := range s.agents {
		if live.Status == swarm.StatusRunning || live.Status == swarm.StatusWaitingApproval || live.Status == swarm.StatusQueued {
			count++
		}
	}
	return count
}

// reset limpia el estado para una misión nueva.
func (s *swarmState) reset(mission string) {
	*s = newSwarmState()
	s.mission = mission
	s.runGen = swarmRunCounter.Add(1)
	s.started = time.Now()
	s.running = true
	s.orchestratorStatus = "Planificando la misión…"
}
