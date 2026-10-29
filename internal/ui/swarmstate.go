package ui

// Estado del modo Swarm en la TUI.
//
//	swarmState
//	├── mission     misión en curso ("" = ninguna)
//	├── agents      estado en vivo por sub-agente
//	├── tabs        orden de pestañas (Alt+0..9 / Alt+←/→)
//	├── activeTab   0 = Orquestador, 1..N = sub-agente
//	├── logs        streaming en vivo por agente
//	└── runGen      generación para descartar eventos viejos
//
//	Alt+0 ──► chat del Orquestador
//	Alt+1..9 ──► stream en vivo del sub-agente 1..9
//	Alt+←/→, Ctrl+Tab ──► navegación secuencial entre todas las pestañas (soporta >9)

import (
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Yak036/mkd/internal/config"
	"github.com/Yak036/mkd/internal/storage"
	"github.com/Yak036/mkd/internal/swarm"
)

// swarmRunCounter genera generaciones monotónicas para descartar eventos de
// misiones canceladas o anteriores.
var swarmRunCounter atomic.Int64

// agentLive es el estado en vivo de un sub-agente.
type agentLive struct {
	ID           string
	Name         string
	Icon         string
	Color        string
	Task         string
	Instructions string
	Status       swarm.AgentStatus
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
	// Index es la pestaña asignada (1..N). No se clampea a 9 para evitar colisiones
	// silenciosas entre sub-agentes; agentes >9 son navegables con Alt+←/→ o Ctrl+Tab.
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
	// Asigna la próxima pestaña disponible de forma estrictamente secuencial y única.
	// No se limita a 9 para soportar enjambres extensos sin colisión silenciosa.
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
// Itera de forma determinista sobre s.order para evitar la aleatoriedad de los maps de Go.
func (s swarmState) agentByTab(tab int) (*agentLive, bool) {
	for _, id := range s.order {
		if live, ok := s.agents[id]; ok && live.Index == tab {
			return live, true
		}
	}
	// Respaldo determinista ordenando claves si existiera algún agente registrado fuera de s.order:
	for _, live := range s.agents {
		if live != nil && live.Index == tab {
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

// snapshot serializa el estado completo del swarm para su persistencia en sesión.
func (s swarmState) snapshot(selectedTab int) *storage.SwarmSessionState {
	if !s.active() {
		return nil
	}
	agents := make(map[string]storage.SwarmTabSession, len(s.agents))
	for id, live := range s.agents {
		if live == nil {
			continue
		}
		steps := make([]storage.SwarmStepSession, 0, len(live.Steps))
		for _, step := range live.Steps {
			steps = append(steps, storage.SwarmStepSession{
				Tool:  step.Tool,
				Args:  step.Args,
				Done:  step.Done,
				Error: step.Error,
				Text:  step.Text,
			})
		}
		agents[id] = storage.SwarmTabSession{
			ID:           live.ID,
			Name:         live.Name,
			Icon:         live.Icon,
			Color:        live.Color,
			Task:         live.Task,
			Instructions: live.Instructions,
			Status:       agentStatusToString(live.Status),
			Tool:         live.Tool,
			Args:         live.Args,
			LastLine:     live.LastLine,
			Output:       live.Output.String(),
			Tokens:       live.Tokens,
			DurationMs:   live.Duration.Milliseconds(),
			Index:        live.Index,
			Err:          live.Err,
			Steps:        steps,
		}
	}

	order := make([]string, len(s.order))
	copy(order, s.order)

	return &storage.SwarmSessionState{
		Mission:            s.mission,
		OrchestratorStatus: s.orchestratorStatus,
		Delegations:        s.delegations,
		Order:              order,
		SelectedTab:        selectedTab,
		Running:            s.running,
		Agents:             agents,
	}
}

// restoreSwarmState reconstruye el estado del swarm desde el almacenamiento persistido,
// asegurando de forma estricta que NINGÚN trabajo en curso sea restaurado como activo/live.
func restoreSwarmState(saved storage.SwarmSessionState) swarmState {
	st := newSwarmState()
	st.mission = saved.Mission
	st.runGen = swarmRunCounter.Add(1)
	st.running = false // Garantía: jamás restaurar la misión como corriendo en vivo
	st.delegations = saved.Delegations

	// Estado del orquestador: si estaba transitorio/en ejecución al guardar, marcar como finalizado/interrumpido.
	if saved.Running || isTransientOrchestratorStatus(saved.OrchestratorStatus) {
		st.orchestratorStatus = "Misión finalizada (restaurada)"
	} else if saved.OrchestratorStatus != "" {
		st.orchestratorStatus = saved.OrchestratorStatus
	} else {
		st.orchestratorStatus = "Misión finalizada"
	}

	st.order = make([]string, 0, len(saved.Order))
	for _, id := range saved.Order {
		id = strings.TrimSpace(id)
		if id != "" {
			st.order = append(st.order, id)
		}
	}

	st.agents = make(map[string]*agentLive, len(saved.Agents))
	for id, ag := range saved.Agents {
		live := &agentLive{
			ID:           ag.ID,
			Name:         ag.Name,
			Icon:         ag.Icon,
			Color:        config.NormalizeAgentColor(ag.Color),
			Task:         ag.Task,
			Instructions: ag.Instructions,
			Tool:         ag.Tool,
			Args:         ag.Args,
			LastLine:     ag.LastLine,
			Tokens:       ag.Tokens,
			Duration:     time.Duration(ag.DurationMs) * time.Millisecond,
			Index:        ag.Index,
			Err:          ag.Err,
		}
		if live.ID == "" {
			live.ID = id
		}
		if live.Name == "" {
			live.Name = live.ID
		}
		if live.Icon == "" {
			live.Icon = "󰀐"
		}
		live.Output.WriteString(ag.Output)

		// Restaurar el historial de pasos ejecutados por el sub-agente
		if len(ag.Steps) > 0 {
			live.Steps = make([]agentStep, 0, len(ag.Steps))
			for _, step := range ag.Steps {
				live.Steps = append(live.Steps, agentStep{
					Tool:  step.Tool,
					Args:  step.Args,
					Done:  step.Done,
					Error: step.Error,
					Text:  step.Text,
				})
			}
		}

		// Requisito crítico: Asegurar que ningún trabajo quede restaurado como live.
		// Todo sub-agente que haya quedado en cola, ejecutando o esperando aprobación
		// (o en estado no terminal si la misión se interrumpió) pasa a StatusError
		// ("Interrumpido al cerrar sesión") para evitar spinners infinitos o estados fantasma.
		status := parseAgentStatus(ag.Status)
		if status == swarm.StatusRunning || status == swarm.StatusQueued || status == swarm.StatusWaitingApproval || (saved.Running && status != swarm.StatusDone && status != swarm.StatusError) {
			status = swarm.StatusError
			if live.Err == "" {
				live.Err = "Interrumpido al cerrar sesión"
			}
			// Finalizar cualquier paso de herramienta que haya quedado abierto
			for i := range live.Steps {
				if !live.Steps[i].Done {
					live.Steps[i].Done = true
					live.Steps[i].Error = true
				}
			}
			live.Tool = ""
			live.Args = ""
		}
		live.Status = status

		st.agents[live.ID] = live
	}

	// Si el orden persistido estaba vacío pero hay agentes, ordenar según Index
	if len(st.order) == 0 && len(st.agents) > 0 {
		type indexedAgent struct {
			id    string
			index int
		}
		var list []indexedAgent
		for id, live := range st.agents {
			list = append(list, indexedAgent{id: id, index: live.Index})
		}
		sort.SliceStable(list, func(i, j int) bool {
			return list[i].index < list[j].index
		})
		for _, item := range list {
			st.order = append(st.order, item.id)
		}
	}

	// Garantizar que cada sub-agente tenga un índice de pestaña válido y único (1..N).
	// Si existiesen índices duplicados (por ejemplo, sesiones antiguas que clampearon a 9)
	// o índices inválidos (<= 0), se reasignan secuencialmente para garantizar unicidad.
	seenIndices := make(map[int]bool, len(st.order))
	for i, id := range st.order {
		if live, ok := st.agents[id]; ok {
			if live.Index <= 0 || seenIndices[live.Index] {
				live.Index = i + 1
			}
			seenIndices[live.Index] = true
		}
	}

	return st
}

// restoreLegacySwarmTabs gestiona la compatibilidad con sesiones anteriores que solo
// almacenaban SwarmTabs ([]string) y SwarmTab (int).
func restoreLegacySwarmTabs(tabs []string, selectedTab int, cfg config.Config) (swarmState, int) {
	st := newSwarmState()
	st.mission = "restored-mission"
	st.orchestratorStatus = "Misión finalizada (restaurada)"
	st.running = false
	st.runGen = swarmRunCounter.Add(1)

	for i, id := range tabs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		profile, found := swarm.FindProfile(cfg.SubAgents, id)
		if !found {
			profile = config.SubAgentProfile{
				ID:    id,
				Name:  id,
				Icon:  "󰀐",
				Color: "#06B6D4",
			}
		}
		live := st.agent(profile)
		live.Status = swarm.StatusDone
		live.Index = i + 1
	}

	viewTab := 0
	if selectedTab > 0 && selectedTab <= len(st.order) {
		viewTab = selectedTab
	}
	return st, viewTab
}

// agentStatusToString mapea el estado interno a una cadena legible para JSON.
func agentStatusToString(s swarm.AgentStatus) string {
	switch s {
	case swarm.StatusQueued:
		return "queued"
	case swarm.StatusRunning:
		return "running"
	case swarm.StatusWaitingApproval:
		return "waiting_approval"
	case swarm.StatusDone:
		return "done"
	case swarm.StatusError:
		return "error"
	default:
		return "queued"
	}
}

// parseAgentStatus parsea el estado serializado soportando nombres y números.
func parseAgentStatus(s string) swarm.AgentStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "running":
		return swarm.StatusRunning
	case "2", "waiting_approval", "waiting":
		return swarm.StatusWaitingApproval
	case "3", "done", "completed":
		return swarm.StatusDone
	case "4", "error", "failed":
		return swarm.StatusError
	default:
		return swarm.StatusQueued
	}
}

// isTransientOrchestratorStatus detecta si el estado del orquestador era efímero/en vivo.
func isTransientOrchestratorStatus(status string) bool {
	status = strings.TrimSpace(strings.ToLower(status))
	if status == "" {
		return true
	}
	transient := []string{
		"planificando",
		"delegando",
		"esperando",
		"coordinando",
		"reanudando",
		"ejecutando",
	}
	for _, term := range transient {
		if strings.Contains(status, term) {
			return true
		}
	}
	return false
}
