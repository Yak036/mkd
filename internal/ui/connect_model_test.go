package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestModelWithoutConnectedProvider verifica que si ningún proveedor está
// conectado con éxito, /model no muestra listas de modelos ni permite seleccionar
// modelos ficticios, mostrando en su lugar una indicación para usar /connect.
func TestModelWithoutConnectedProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := swarmTestModel(t)

	// Nos aseguramos de que ningún proveedor esté conectado
	for k, p := range m.cfg.Providers {
		p.Connected = false
		m.cfg.Providers[k] = p
	}

	next, _ := m.openModelsModal()
	m = asModel(next)

	if len(m.modal.models.keys) != 0 {
		t.Fatalf("no debían salir proveedores en /model sin conexión exitosa: got %v", m.modal.models.keys)
	}

	view := m.modelsModalView()
	if !strings.Contains(view, "No hay ningún proveedor con conexión exitosa") {
		t.Fatalf("se esperaba mensaje de advertencia en /model, pero se obtuvo: %s", view)
	}
	if !strings.Contains(view, "/connect") {
		t.Fatalf("se esperaba recomendación de usar /connect en la vista, pero se obtuvo: %s", view)
	}
}

// TestConnectFailureStaysInConnectModal verifica que si la conexión falla en /connect,
// el modal permanece en /connect con el mensaje de error y NO abre /model ni marca el
// proveedor como conectado.
func TestConnectFailureStaysInConnectModal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized API Key", http.StatusUnauthorized)
	}))
	defer srv.Close()

	m := swarmTestModel(t)
	p := m.cfg.Providers[m.cfg.ActiveProvider]
	p.BaseURL = srv.URL
	p.APIKey = "sk-invalida"
	p.Connected = false
	m.cfg.Providers[m.cfg.ActiveProvider] = p

	// Abrir modal de conexión (/connect)
	next, _ := m.openProviderModal()
	m = asModel(next)

	// Simular presionar Enter para probar y conectar
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	next, cmd := m.updateProviderModal(enterMsg)
	m = asModel(next)

	if cmd == nil {
		t.Fatal("se esperaba un comando asíncrono para probar la conexión")
	}

	// Ejecutar comando de prueba de conexión
	done := make(chan tea.Msg, 1)
	go func() {
		// Ejecutar batches si aplica
		runCmd(cmd, done)
	}()

	select {
	case msg := <-done:
		if res, ok := msg.(providerConnectionDoneMsg); ok {
			next, _ = m.handleProviderConnectionDone(res)
			m = asModel(next)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout esperando el resultado de la conexión")
	}

	// Debe permanecer en modalProvider y mostrar error
	if m.modal.kind != modalProvider {
		t.Fatalf("el modal debió permanecer en /connect (modalProvider), pero quedó en %v", m.modal.kind)
	}
	if m.cfg.Providers[m.cfg.ActiveProvider].Connected {
		t.Fatal("el proveedor no debía quedar marcado como Connected tras un fallo")
	}
	if !strings.Contains(m.modal.provider.err, "Falló la conexión") {
		t.Fatalf("se esperaba mensaje de error de conexión, pero se obtuvo: %q", m.modal.provider.err)
	}
}

// TestConnectSuccessEnablesModelSelection verifica que tras una conexión exitosa en /connect,
// el proveedor queda marcado como Connected = true, sincroniza modelos reales y queda disponible
// para su selección en /model.
func TestConnectSuccessEnablesModelSelection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []any{
				map[string]any{"id": "modelo-real-verificado-1", "object": "model"},
				map[string]any{"id": "modelo-real-verificado-2", "object": "model"},
			},
		})
	}))
	defer srv.Close()

	m := swarmTestModel(t)
	p := m.cfg.Providers[m.cfg.ActiveProvider]
	p.BaseURL = srv.URL
	p.APIKey = "sk-valida"
	p.Connected = false
	p.Models = nil
	m.cfg.Providers[m.cfg.ActiveProvider] = p

	// Abrir modal de conexión (/connect)
	next, _ := m.openProviderModal()
	m = asModel(next)

	// Ejecutar prueba de conexión (Enter)
	next, cmd := m.updateProviderModal(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(next)

	done := make(chan tea.Msg, 1)
	go func() {
		runCmd(cmd, done)
	}()

	select {
	case msg := <-done:
		if res, ok := msg.(providerConnectionDoneMsg); ok {
			next, _ = m.handleProviderConnectionDone(res)
			m = asModel(next)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout esperando respuesta")
	}

	// Verificar éxito
	if !m.cfg.Providers[m.cfg.ActiveProvider].Connected {
		t.Fatal("el proveedor debía quedar marcado como Connected tras éxito")
	}
	if !strings.Contains(m.modal.provider.status, "Conexión exitosa") {
		t.Fatalf("se esperaba estado de conexión exitosa, pero se obtuvo: %q", m.modal.provider.status)
	}

	// Ahora cerramos el modal y abrimos /model
	m.modal.kind = modalNone
	next, _ = m.openModelsModal()
	m = asModel(next)

	if len(m.modal.models.keys) == 0 {
		t.Fatal("ahora /model debe contener el proveedor conectado")
	}
	if m.modal.models.keys[0] != m.cfg.ActiveProvider {
		t.Fatalf("proveedor inesperado en /model: %s", m.modal.models.keys[0])
	}
	if len(m.modal.models.all) != 2 || m.modal.models.all[0] != "modelo-real-verificado-1" {
		t.Fatalf("modelos inesperados tras conexión exitosa: %v", m.modal.models.all)
	}
}

// TestModelAddCommandRequiresConnection verifica que el slash command /model add <id>
// rechaza agregar modelos si el proveedor activo no tiene una conexión exitosa en /connect.
func TestModelAddCommandRequiresConnection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := swarmTestModel(t)

	// Desconectar proveedor activo
	p := m.cfg.Providers[m.cfg.ActiveProvider]
	p.Connected = false
	m.cfg.Providers[m.cfg.ActiveProvider] = p

	next, _ := m.handleSlash("/model add nuevo-modelo")
	m = asModel(next)

	if !strings.Contains(m.notice, "no tiene una conexión exitosa") {
		t.Fatalf("se esperaba aviso de proveedor no conectado, pero se obtuvo: %q", m.notice)
	}

	// Conectar proveedor activo
	p.Connected = true
	m.cfg.Providers[m.cfg.ActiveProvider] = p
	next, _ = m.handleSlash("/model add nuevo-modelo")
	m = toModel(next)

	if strings.Contains(m.notice, "no tiene una conexión exitosa") {
		t.Fatalf("no debía rechazar el comando tras tener conexión exitosa, notice: %q", m.notice)
	}
}

// toModel extrae Model ya sea que el retorno sea Model o *Model.
func toModel(v tea.Model) Model {
	if m, ok := v.(Model); ok {
		return m
	}
	if p, ok := v.(*Model); ok {
		return *p
	}
	panic("tipo inesperado en toModel")
}

// runCmd helper para aplanar llamadas asíncronas de bubbletea.
func runCmd(c tea.Cmd, done chan tea.Msg) {
	if c == nil {
		return
	}
	msg := c()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			runCmd(sub, done)
		}
		return
	}
	done <- msg
}
