package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Yak036/mkd/internal/ai"
	"github.com/Yak036/mkd/internal/config"
	"github.com/Yak036/mkd/internal/storage"
	"github.com/Yak036/mkd/internal/swarm"
)

// TestSwarmTabPersistenceRoundTrip valida la persistencia y restauración completa de
// pestañas de sub-agentes, incluyendo output, historial de pasos, orden y pestaña seleccionada.
func TestSwarmTabPersistenceRoundTrip(t *testing.T) {
	m := swarmTestModel(t)

	// Configuración de misión Swarm con 3 agentes
	m.swarm.reset("mission-persist-roundtrip")
	m.swarm.orchestratorStatus = "Misión completada"
	m.swarm.running = false
	m.swarm.delegations = 3

	pExplorer := m.cfg.SubAgents["explorer"]
	pCoder := m.cfg.SubAgents["coder"]
	pQA := m.cfg.SubAgents["qa"]

	liveExplorer := m.swarm.agent(pExplorer)
	liveExplorer.Status = swarm.StatusDone
	liveExplorer.Task = "Investigar código existente"
	liveExplorer.Instructions = "Buscar dependencias en storage y ui"
	liveExplorer.Tokens = 1200
	liveExplorer.Duration = 2200 * time.Millisecond
	liveExplorer.Output.WriteString("Análisis completado: encontrados 12 archivos.")
	liveExplorer.Steps = []agentStep{
		{Tool: "search_code", Args: `{"query":"sessions"}`, Done: true, Text: "12 matches"},
	}

	liveCoder := m.swarm.agent(pCoder)
	liveCoder.Status = swarm.StatusDone
	liveCoder.Task = "Implementar persistencia de swarm"
	liveCoder.Instructions = "Extender storage.Session y swarmState"
	liveCoder.Tokens = 3400
	liveCoder.Duration = 4500 * time.Millisecond
	liveCoder.Output.WriteString("```go\nfunc TestPersistence() {\n    // Implementado\n}\n```")
	liveCoder.Steps = []agentStep{
		{Tool: "read_file", Args: `{"path":"internal/ui/swarmstate.go"}`, Done: true, Text: "read"},
		{Tool: "patch_file", Args: `{"path":"internal/ui/workspace.go"}`, Done: true, Text: "patched"},
	}

	liveQA := m.swarm.agent(pQA)
	liveQA.Status = swarm.StatusDone
	liveQA.Task = "Verificar suite de tests"
	liveQA.Instructions = "Ejecutar pruebas unitarias de regresión"
	liveQA.Tokens = 850
	liveQA.Duration = 1100 * time.Millisecond
	liveQA.Output.WriteString("PASS: todos los tests unitarios pasaron exitosamente.")
	liveQA.Steps = []agentStep{
		{Tool: "run_command", Args: `{"cmd":"go test ./..."}`, Done: true, Text: "ok"},
	}

	// Historial del orquestador en el chat
	m.history = []chatMessage{
		{Role: ai.RoleUser, Content: "Tarea Swarm: persistir pestañas", Display: "🌐 Misión Swarm"},
		{Role: ai.RoleAssistant, Content: "Misión Swarm ejecutada y concluida exitosamente."},
	}
	m.rebuildHistory()

	// Seleccionamos la pestaña 2 (coder)
	next, _ := m.switchViewTab(2)
	m = asModel(next)
	if m.viewTab != 2 {
		t.Fatalf("se esperaba viewTab=2, se obtuvo %d", m.viewTab)
	}

	// Persistir la sesión en disco
	m.persistSession(true)
	sessionID := m.session.ID
	if sessionID == "" {
		t.Fatal("sessionID vacío tras persistSession")
	}

	// Cargar la sesión en un nuevo modelo con estado limpio
	mLoaded := testLoadSession(t, m, sessionID)

	// Verificación de estado Swarm global
	if !mLoaded.swarm.active() {
		t.Fatal("swarmState no quedó activo tras loadSession")
	}
	if mLoaded.swarm.running {
		t.Fatal("la misión no debe restaurarse como corriendo (running debe ser false)")
	}
	if mLoaded.waiting {
		t.Fatal("waiting debe ser false al cargar la sesión")
	}
	if mLoaded.viewTab != 2 {
		t.Fatalf("viewTab seleccionado no se preservó: esperado 2, obtenido %d", mLoaded.viewTab)
	}

	// Verificación de orden de pestañas
	expectedOrder := []string{"explorer", "coder", "qa"}
	if len(mLoaded.swarm.order) != 3 {
		t.Fatalf("orden de tamaño incorrecto: %v", mLoaded.swarm.order)
	}
	for i, id := range expectedOrder {
		if mLoaded.swarm.order[i] != id {
			t.Errorf("orden en posición %d: esperado %s, obtenido %s", i, id, mLoaded.swarm.order[i])
		}
	}

	// Verificación de detalles por sub-agente
	coder, ok := mLoaded.swarm.agents["coder"]
	if !ok {
		t.Fatal("sub-agente coder no encontrado en el estado restaurado")
	}
	if coder.Status != swarm.StatusDone {
		t.Fatalf("estado de coder: esperado StatusDone, obtenido %v", coder.Status)
	}
	if coder.Tokens != 3400 {
		t.Fatalf("tokens de coder: esperados 3400, obtenidos %d", coder.Tokens)
	}
	if coder.Duration != 4500*time.Millisecond {
		t.Fatalf("duración de coder: esperada 4.5s, obtenida %v", coder.Duration)
	}
	if coder.Instructions != "Extender storage.Session y swarmState" {
		t.Fatalf("instrucciones de coder incorrectas: %q", coder.Instructions)
	}
	if !strings.Contains(coder.Output.String(), "func TestPersistence()") {
		t.Fatalf("output de coder no contiene el bloque esperado: %q", coder.Output.String())
	}
	if len(coder.Steps) != 2 {
		t.Fatalf("pasos de coder: esperados 2, obtenidos %d", len(coder.Steps))
	}
	if coder.Steps[1].Tool != "patch_file" || !coder.Steps[1].Done {
		t.Fatalf("segundo paso de coder incorrecto: %+v", coder.Steps[1])
	}

	// Verificación de renderizado de la barra de pestañas
	bar := mLoaded.swarmTabBar(mLoaded.innerWidth)
	for _, needle := range []string{"0:", "Orquestador", "1:", "explorer", "2:", "coder", "3:", "qa"} {
		if !strings.Contains(bar, needle) {
			t.Errorf("swarmTabBar no contiene %q:\n%s", needle, bar)
		}
	}

	// Verificación de renderizado del inspector de la pestaña activa (coder, tab 2)
	tabContent := mLoaded.swarmTabContent(mLoaded.conversationWidth, mLoaded.mainHeight)
	if !strings.Contains(tabContent, "TestPersistence") {
		t.Fatalf("swarmTabContent no contiene la salida de coder:\n%s", tabContent)
	}
	if !strings.Contains(tabContent, "Editando archivo") {
		t.Fatalf("swarmTabContent no contiene la descripción del paso:\n%s", tabContent)
	}

	// Cambiar a la pestaña 1 (explorer) y verificar su inspector
	nextTab, _ := mLoaded.switchViewTab(1)
	mTab1 := asModel(nextTab)
	if mTab1.viewTab != 1 {
		t.Fatalf("viewTab esperado 1, obtenido %d", mTab1.viewTab)
	}
	tabContent1 := mTab1.swarmTabContent(mTab1.conversationWidth, mTab1.mainHeight)
	if !strings.Contains(tabContent1, "Análisis completado") {
		t.Fatalf("swarmTabContent de explorer no contiene su salida:\n%s", tabContent1)
	}

	// Cambiar a la pestaña 0 (orquestador)
	nextTab0, _ := mLoaded.switchViewTab(0)
	mTab0 := asModel(nextTab0)
	if mTab0.viewTab != 0 {
		t.Fatalf("viewTab esperado 0, obtenido %d", mTab0.viewTab)
	}
	if mTab0.swarmTabContent(mTab0.conversationWidth, mTab0.mainHeight) != "" {
		t.Fatal("la pestaña 0 debe retornar contenido vacío para no duplicar el viewport")
	}
}

// TestSwarmPersistenceNoLiveJobsOnRestore valida estrictamente el requisito:
// "Ensure no running job restored as live."
// Ningún sub-agente ni misión que se encontrara en ejecución, en cola o esperando
// aprobación debe quedar como live tras restaurar la sesión.
func TestSwarmPersistenceNoLiveJobsOnRestore(t *testing.T) {
	m := swarmTestModel(t)

	// Simulamos una misión que fue interrumpida abruptamente (ej. crash o cierre)
	// mientras estaba en plena ejecución.
	m.swarm.reset("mission-interrupted")
	m.swarm.running = true
	m.swarm.orchestratorStatus = "Esperando sub-agentes…"

	pExplorer := m.cfg.SubAgents["explorer"]
	pCoder := m.cfg.SubAgents["coder"]
	pQA := m.cfg.SubAgents["qa"]
	pDocs := m.cfg.SubAgents["docs"]

	// explorer ya había terminado
	liveExplorer := m.swarm.agent(pExplorer)
	liveExplorer.Status = swarm.StatusDone
	liveExplorer.Output.WriteString("Investigación terminada.")

	// coder estaba en ejecución activa con una tool en proceso
	liveCoder := m.swarm.agent(pCoder)
	liveCoder.Status = swarm.StatusRunning
	liveCoder.Tool = "patch_file"
	liveCoder.Args = `{"path":"app.go"}`
	liveCoder.Steps = []agentStep{
		{Tool: "read_file", Args: "app.go", Done: true, Text: "read"},
		{Tool: "patch_file", Args: "app.go", Done: false}, // En curso
	}

	// qa estaba en cola esperando turno
	liveQA := m.swarm.agent(pQA)
	liveQA.Status = swarm.StatusQueued

	// docs estaba esperando aprobación HITL
	liveDocs := m.swarm.agent(pDocs)
	liveDocs.Status = swarm.StatusWaitingApproval
	liveDocs.Tool = "write_file"

	m.history = []chatMessage{
		{Role: ai.RoleUser, Content: "Misión incompleta", Display: "🌐 Misión Swarm"},
	}

	// Persistir la sesión en estado intermedio
	m.persistSession(true)
	sessionID := m.session.ID

	// Restaurar la sesión con estado limpio
	mLoaded := testLoadSession(t, m, sessionID)

	// 1. La misión general NO debe estar corriendo
	if mLoaded.swarm.running {
		t.Fatal("FAIL: m.swarm.running es true tras restaurar")
	}
	if mLoaded.swarm.runningCount() != 0 {
		t.Fatalf("FAIL: runningCount() = %d, se esperaba 0", mLoaded.swarm.runningCount())
	}
	if mLoaded.busy() {
		t.Fatal("FAIL: busy() es true tras restaurar")
	}

	// 2. El status del orquestador debe ser terminal, no transitorio
	if strings.Contains(mLoaded.swarm.orchestratorStatus, "Esperando") ||
		strings.Contains(mLoaded.swarm.orchestratorStatus, "Planificando") {
		t.Fatalf("FAIL: orchestratorStatus sigue transitorio: %q", mLoaded.swarm.orchestratorStatus)
	}

	// 3. explorer debe conservar su StatusDone
	resExplorer := mLoaded.swarm.agents["explorer"]
	if resExplorer.Status != swarm.StatusDone {
		t.Fatalf("explorer debió quedar en StatusDone, obtenido %v", resExplorer.Status)
	}

	// 4. coder (estaba Running) debe haber pasado a StatusError con mensaje de interrupción
	resCoder := mLoaded.swarm.agents["coder"]
	if resCoder.Status == swarm.StatusRunning || resCoder.Status == swarm.StatusQueued || resCoder.Status == swarm.StatusWaitingApproval {
		t.Fatalf("coder se restauró en estado live: %v", resCoder.Status)
	}
	if resCoder.Status != swarm.StatusError {
		t.Fatalf("coder debía estar en StatusError, obtenido: %v", resCoder.Status)
	}
	if resCoder.Err == "" {
		t.Fatal("coder no tiene mensaje de error explicativo")
	}
	if resCoder.Tool != "" || resCoder.Args != "" {
		t.Fatalf("coder no debe conservar tools pendientes activas: tool=%q, args=%q", resCoder.Tool, resCoder.Args)
	}
	// Su paso en curso debió ser cerrado con Error: true
	if len(resCoder.Steps) != 2 || !resCoder.Steps[1].Done || !resCoder.Steps[1].Error {
		t.Fatalf("el paso incompleto de coder no fue cerrado con error: %+v", resCoder.Steps[1])
	}

	// 5. qa (estaba Queued) debe haber pasado a StatusError
	resQA := mLoaded.swarm.agents["qa"]
	if resQA.Status == swarm.StatusQueued || resQA.Status == swarm.StatusRunning {
		t.Fatalf("qa se restauró en estado live: %v", resQA.Status)
	}
	if resQA.Status != swarm.StatusError {
		t.Fatalf("qa debía estar en StatusError, obtenido: %v", resQA.Status)
	}

	// 6. docs (estaba WaitingApproval) debe haber pasado a StatusError
	resDocs := mLoaded.swarm.agents["docs"]
	if resDocs.Status == swarm.StatusWaitingApproval || resDocs.Status == swarm.StatusRunning {
		t.Fatalf("docs se restauró en estado live: %v", resDocs.Status)
	}
	if resDocs.Status != swarm.StatusError {
		t.Fatalf("docs debía estar en StatusError, obtenido: %v", resDocs.Status)
	}

	// 7. La barra de pestañas no debe contener glifos de animación "⠋"
	bar := mLoaded.swarmTabBar(mLoaded.innerWidth)
	if strings.Contains(bar, "⠋") {
		t.Fatalf("la barra de pestañas contiene glifo de animación activa '⠋':\n%s", bar)
	}
}

// TestLegacySwarmTabsMigration valida la compatibilidad con sesiones previas
// que solo contenían los campos planos SwarmTabs ([]string) y SwarmTab (int).
func TestLegacySwarmTabsMigration(t *testing.T) {
	m := swarmTestModel(t)

	legacySession := storage.Session{
		ID:        storage.NewSessionID(),
		Title:     "Sesión Legacy Swarm",
		SwarmTabs: []string{"explorer", "coder"},
		SwarmTab:  2,
		Messages: []storage.Message{
			{Role: "user", Content: "ejecuta tarea", Display: "ejecuta tarea"},
		},
	}
	if _, err := storage.SaveSession(legacySession); err != nil {
		t.Fatalf("falló SaveSession: %v", err)
	}

	loaded, _ := m.loadSession(legacySession.ID)
	mLoaded := asModel(loaded)

	if !mLoaded.swarm.active() {
		t.Fatal("la sesión legacy no activó swarm")
	}
	if mLoaded.viewTab != 2 {
		t.Fatalf("viewTab esperado 2, obtenido %d", mLoaded.viewTab)
	}
	if len(mLoaded.swarm.order) != 2 || mLoaded.swarm.order[0] != "explorer" || mLoaded.swarm.order[1] != "coder" {
		t.Fatalf("orden de pestañas legacy incorrecto: %v", mLoaded.swarm.order)
	}
	if mLoaded.swarm.running {
		t.Fatal("sesión legacy no debe correr como live")
	}
}

// TestSwarmTabSwitchingPersistsSelectedTab valida que al alternar entre pestañas con Alt+1..9
// se marque la sesión dirty y al guardar se persista la pestaña seleccionada.
func TestSwarmTabSwitchingPersistsSelectedTab(t *testing.T) {
	m := swarmTestModel(t)
	m.swarm.reset("mission-switch-tab")

	pExplorer := m.cfg.SubAgents["explorer"]
	pCoder := m.cfg.SubAgents["coder"]

	live1 := m.swarm.agent(pExplorer)
	live1.Status = swarm.StatusDone
	live2 := m.swarm.agent(pCoder)
	live2.Status = swarm.StatusDone

	m.history = []chatMessage{
		{Role: ai.RoleUser, Content: "misión", Display: "misión"},
	}

	// Cambiar a la pestaña 2
	next, _ := m.switchViewTab(2)
	m = asModel(next)
	if m.viewTab != 2 {
		t.Fatalf("viewTab esperado 2, obtenido %d", m.viewTab)
	}
	if !m.sessionDirty {
		t.Fatal("switchViewTab debió marcar la sesión como dirty")
	}

	// Guardar
	m.persistSession(false)
	sessionID := m.session.ID

	// Recargar con estado limpio
	mLoaded := testLoadSession(t, m, sessionID)

	if mLoaded.viewTab != 2 {
		t.Fatalf("SelectedTab no persistió: esperado 2, obtenido %d", mLoaded.viewTab)
	}
}

// testLoadSession limpia el estado conversacional y del swarm de un modelo
// y recarga la sesión desde disco utilizando su mismo almacenamiento.
func testLoadSession(t *testing.T, m Model, sessionID string) Model {
	t.Helper()
	mClean := m
	mClean.swarm = newSwarmState()
	mClean.history = nil
	mClean.blocks = nil
	mClean.viewTab = 0
	loaded, _ := mClean.loadSession(sessionID)
	return asModel(loaded)
}

// TestSwarmTabPersistenceMoreThan9AgentsRoundTrip valida que más de 9 sub-agentes
// reciban índices secuenciales únicos (>9 sin clampear a 9), persistan íntegramente
// en disco y se restauren manteniendo sus índices únicos y la pestaña activa seleccionada (ej: 11).
func TestSwarmTabPersistenceMoreThan9AgentsRoundTrip(t *testing.T) {
	m := swarmTestModel(t)
	m.swarm.reset("mission-12-agents")
	m.swarm.orchestratorStatus = "Misión de 12 agentes finalizada"
	m.swarm.running = false

	const numAgents = 12
	agentIDs := make([]string, numAgents)
	for i := 1; i <= numAgents; i++ {
		agentID := fmt.Sprintf("agent-%02d", i)
		agentIDs[i-1] = agentID
		p := config.SubAgentProfile{
			ID:    agentID,
			Name:  fmt.Sprintf("Especialista %d", i),
			Icon:  "󰀐",
			Color: "#06B6D4",
		}
		live := m.swarm.agent(p)
		live.Status = swarm.StatusDone
		live.Task = fmt.Sprintf("Tarea específica del agente %d", i)
		live.Instructions = fmt.Sprintf("Instrucción detallada para agente %d", i)
		live.Tokens = 100 * i
		live.Duration = time.Duration(i*500) * time.Millisecond
		live.Output.WriteString(fmt.Sprintf("Salida generada por %s", agentID))

		// Verificar que el índice asignado por agent() sea exactamente i (1..12), sin clampear a 9
		if live.Index != i {
			t.Fatalf("agente %s: se esperaba Index=%d, se obtuvo %d (¿clampeado a 9?)", agentID, i, live.Index)
		}
	}

	// Verificar determinismo de agentByTab:
	// Para cada tab 1..12, debe devolver el agente correcto
	for i := 1; i <= numAgents; i++ {
		live, ok := m.swarm.agentByTab(i)
		if !ok {
			t.Fatalf("agentByTab(%d) no encontró el agente", i)
		}
		expectedID := fmt.Sprintf("agent-%02d", i)
		if live.ID != expectedID {
			t.Fatalf("agentByTab(%d) devolvió %s, esperado %s", i, live.ID, expectedID)
		}
	}

	// Seleccionamos la pestaña 11 (un agente > 9)
	next, _ := m.switchViewTab(11)
	m = asModel(next)
	if m.viewTab != 11 {
		t.Fatalf("switchViewTab(11): se esperaba viewTab=11, se obtuvo %d", m.viewTab)
	}

	m.history = []chatMessage{
		{Role: ai.RoleUser, Content: "misión con 12 agentes", Display: "misión"},
		{Role: ai.RoleAssistant, Content: "completada"},
	}

	// Persistir la sesión en disco
	m.persistSession(true)
	sessionID := m.session.ID
	if sessionID == "" {
		t.Fatal("sessionID vacío tras persistSession")
	}

	// Recargar la sesión desde disco en un modelo limpio
	mLoaded := testLoadSession(t, m, sessionID)

	// Validar que la pestaña seleccionada 11 se haya conservado intacta
	if mLoaded.viewTab != 11 {
		t.Fatalf("viewTab restaurado: se esperaba 11, se obtuvo %d", mLoaded.viewTab)
	}

	// Validar que se hayan restaurado los 12 agentes con sus índices originales únicos
	if len(mLoaded.swarm.agents) != numAgents {
		t.Fatalf("cantidad de agentes restaurados: esperado %d, obtenido %d", numAgents, len(mLoaded.swarm.agents))
	}
	if len(mLoaded.swarm.order) != numAgents {
		t.Fatalf("orden de agentes restaurados: esperado %d, obtenido %d", numAgents, len(mLoaded.swarm.order))
	}

	for i := 1; i <= numAgents; i++ {
		expectedID := fmt.Sprintf("agent-%02d", i)
		live, ok := mLoaded.swarm.agentByTab(i)
		if !ok {
			t.Fatalf("sesión restaurada: agentByTab(%d) no encontró el agente", i)
		}
		if live.ID != expectedID {
			t.Fatalf("sesión restaurada: agentByTab(%d) devolvió %s, esperado %s", i, live.ID, expectedID)
		}
		if live.Index != i {
			t.Fatalf("sesión restaurada: agente %s tiene Index=%d, esperado %d", live.ID, live.Index, i)
		}
	}
}

// TestSwarmRestoreLegacyCollisionSelfHeal valida que si una sesión antigua guardó
// múltiples sub-agentes con Index=9 (debido al bug de clamping a 9), restoreSwarmState
// detecte la colisión de índices y reasigne índices únicos secuenciales sin pérdida ni bloqueo.
func TestSwarmRestoreLegacyCollisionSelfHeal(t *testing.T) {
	stSaved := storage.SwarmSessionState{
		Mission: "legacy-clamped-mission",
		Order:   []string{"ag1", "ag2", "ag3", "ag4", "ag5", "ag6", "ag7", "ag8", "ag9", "ag10", "ag11"},
		Agents: map[string]storage.SwarmTabSession{
			"ag1":  {ID: "ag1", Index: 1, Status: "done"},
			"ag2":  {ID: "ag2", Index: 2, Status: "done"},
			"ag3":  {ID: "ag3", Index: 3, Status: "done"},
			"ag4":  {ID: "ag4", Index: 4, Status: "done"},
			"ag5":  {ID: "ag5", Index: 5, Status: "done"},
			"ag6":  {ID: "ag6", Index: 6, Status: "done"},
			"ag7":  {ID: "ag7", Index: 7, Status: "done"},
			"ag8":  {ID: "ag8", Index: 8, Status: "done"},
			"ag9":  {ID: "ag9", Index: 9, Status: "done"},
			"ag10": {ID: "ag10", Index: 9, Status: "done"}, // Clampeado con colisión
			"ag11": {ID: "ag11", Index: 9, Status: "done"}, // Clampeado con colisión
		},
	}

	st := restoreSwarmState(stSaved)
	// Verificar que no existan índices duplicados
	seen := make(map[int]string)
	for _, id := range st.order {
		live := st.agents[id]
		if prev, exists := seen[live.Index]; exists {
			t.Fatalf("colisión de índice detectada: %s y %s tienen Index=%d", prev, live.ID, live.Index)
		}
		seen[live.Index] = live.ID
	}

	// ag10 y ag11 deben haber recibido índices 10 y 11
	if st.agents["ag10"].Index != 10 {
		t.Fatalf("ag10 Index esperado 10, obtenido %d", st.agents["ag10"].Index)
	}
	if st.agents["ag11"].Index != 11 {
		t.Fatalf("ag11 Index esperado 11, obtenido %d", st.agents["ag11"].Index)
	}
}
