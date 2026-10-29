package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/storage"
)

// TestModelsAutoSyncWithoutCache: al abrir el modal de modelos con API key pero
// sin caché, se dispara automáticamente la sincronización con /v1/models.
func TestModelsAutoSyncWithoutCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" && r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []any{
				map[string]any{"id": "deepseek-ai/DeepSeek-V4.1-Flash-1017", "object": "model"},
				map[string]any{"id": "Qwen/Qwen2.5-Coder-32B-Instruct", "object": "model"},
			},
		})
	}))
	defer srv.Close()

	m := swarmTestModel(t)
	provider := m.cfg.Providers[m.cfg.ActiveProvider]
	provider.BaseURL = srv.URL
	provider.APIKey = "sk-test"
	provider.Connected = true
	m.cfg.Providers[m.cfg.ActiveProvider] = provider
	m.cfg.Normalize()
	m.modelsCache = storage.ModelsCache{} // sin caché

	next, cmd := m.openModelsModal()
	m = asModel(next)
	if !m.modal.models.loading {
		t.Fatalf("no se disparó la sincronización automática: status=%q", m.modal.models.status)
	}

	// Ejecuta el batch (aplanando batches anidados como hace Bubbletea) y
	// aplica el modelsFetchedMsg resultante.
	runCmd := func(c tea.Cmd) { _ = c }
	runCmd = func(c tea.Cmd) {
		if c == nil {
			return
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case msg := <-done:
			switch v := msg.(type) {
			case tea.BatchMsg:
				for _, sub := range v {
					runCmd(sub)
				}
			case modelsFetchedMsg:
				next, _ := m.handleModelsFetched(v)
				m = asModel(next)
			}
		case <-time.After(5 * time.Second):
		}
	}
	runCmd(cmd)

	models := m.cfg.Providers[m.cfg.ActiveProvider].Models
	found := false
	for _, model := range models {
		if model == "deepseek-ai/DeepSeek-V4.1-Flash-1017" {
			found = true
		}
	}
	if !found {
		t.Fatalf("los modelos de la API no se incorporaron: %v", models)
	}
}

// TestModelsNoAutoSyncWithCache: si ya hay caché, abrir el modal no golpea la
// API (se respeta el diseño de sincronización explícita con Ctrl+F).
func TestModelsNoAutoSyncWithCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := swarmTestModel(t)
	provider := m.cfg.Providers[m.cfg.ActiveProvider]
	provider.APIKey = "sk-test"
	provider.Connected = true
	m.cfg.Providers[m.cfg.ActiveProvider] = provider
	m.cfg.Normalize()

	cache, err := storage.SaveModelsCache(storage.ModelsCache{}, m.cfg.ActiveProvider,
		[]string{"modelo-cacheado-1", "modelo-cacheado-2"})
	if err != nil {
		t.Skipf("no se pudo preparar caché: %v", err)
	}
	m.modelsCache = cache

	next, _ := m.openModelsModal()
	m = asModel(next)
	if m.modal.models.loading {
		t.Fatal("no debería sincronizar si ya hay caché")
	}
}
