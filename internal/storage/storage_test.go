package storage

import (
	"os"
	"strings"
	"testing"
)

func withTempHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
}

func TestSessionRoundTrip(t *testing.T) {
	withTempHome(t)

	session := Session{
		ID:       NewSessionID(),
		Title:    DeriveTitle("Refactoriza el parser de contextos"),
		Cwd:      "/tmp",
		Provider: "deepinfra",
		Model:    "modelo",
		Files:    []string{"main.go"},
		Messages: []Message{{Role: "user", Content: "hola", Display: "hola"}},
	}
	if _, err := SaveSession(session); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != session.Title || len(loaded.Messages) != 1 {
		t.Fatalf("sesión mal leída: %#v", loaded)
	}
	if loaded.CreatedAt.IsZero() || loaded.UpdatedAt.IsZero() {
		t.Fatal("timestamps vacíos")
	}

	list, err := ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != session.ID {
		t.Fatalf("listado inesperado: %#v", list)
	}

	if err := DeleteSession(session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession(session.ID); !os.IsNotExist(err) && err == nil {
		t.Fatal("la sesión no se eliminó")
	}
}

func TestSessionSwarmPersistence(t *testing.T) {
	withTempHome(t)

	session := Session{
		ID:        NewSessionID(),
		Title:     "Misión Swarm Test",
		Cwd:       "/test/cwd",
		Provider:  "openai",
		Model:     "gpt-4o",
		SwarmTabs: []string{"explorer", "coder"},
		SwarmTab:  2,
		Swarm: &SwarmSessionState{
			Mission:            "mission-12345",
			OrchestratorStatus: "Misión completada",
			Delegations:        2,
			Order:              []string{"explorer", "coder"},
			SelectedTab:        2,
			Running:            false,
			Agents: map[string]SwarmTabSession{
				"explorer": {
					ID:           "explorer",
					Name:         "Explorer Agent",
					Icon:         "󰈞",
					Color:        "#10B981",
					Task:         "Investigar codebase",
					Instructions: "Buscar archivos relevantes",
					Status:       "done",
					LastLine:     "Completado con éxito",
					Output:       "Archivos encontrados: main.go, app.go",
					Tokens:       1500,
					DurationMs:   3200,
					Index:        1,
					Steps: []SwarmStepSession{
						{Tool: "search_code", Args: `{"query":"main"}`, Done: true, Text: "ok"},
					},
				},
				"coder": {
					ID:           "coder",
					Name:         "Coder Agent",
					Icon:         "󰘦",
					Color:        "#3B82F6",
					Task:         "Escribir tests",
					Instructions: "Implementar suite de pruebas",
					Status:       "done",
					LastLine:     "Tests escritos",
					Output:       "```go\nfunc TestX() {}\n```",
					Tokens:       2800,
					DurationMs:   5100,
					Index:        2,
					Steps: []SwarmStepSession{
						{Tool: "patch_file", Args: `{"path":"main_test.go"}`, Done: true, Text: "patched"},
					},
				},
			},
		},
	}

	path, err := SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession falló: %v", err)
	}
	if path == "" {
		t.Fatal("ruta vacía retornada")
	}

	loaded, err := LoadSession(session.ID)
	if err != nil {
		t.Fatalf("LoadSession falló: %v", err)
	}

	if loaded.Swarm == nil {
		t.Fatal("SwarmSessionState es nil")
	}
	if loaded.Swarm.Mission != "mission-12345" {
		t.Fatalf("Mission esperada mission-12345, obtenida %s", loaded.Swarm.Mission)
	}
	if loaded.Swarm.SelectedTab != 2 || loaded.SwarmTab != 2 {
		t.Fatalf("SelectedTab no coincide: Swarm.SelectedTab=%d, SwarmTab=%d", loaded.Swarm.SelectedTab, loaded.SwarmTab)
	}
	if len(loaded.Swarm.Order) != 2 || loaded.Swarm.Order[0] != "explorer" || loaded.Swarm.Order[1] != "coder" {
		t.Fatalf("Order no coincide: %v", loaded.Swarm.Order)
	}
	if len(loaded.Swarm.Agents) != 2 {
		t.Fatalf("se esperaban 2 agentes, se obtuvieron %d", len(loaded.Swarm.Agents))
	}
	coder := loaded.Swarm.Agents["coder"]
	if coder.ID != "coder" || coder.Tokens != 2800 || coder.DurationMs != 5100 {
		t.Fatalf("coder mal deserializado: %+v", coder)
	}
	if len(coder.Steps) != 1 || coder.Steps[0].Tool != "patch_file" {
		t.Fatalf("pasos de coder incorrectos: %+v", coder.Steps)
	}
	if !strings.Contains(coder.Output, "func TestX()") {
		t.Fatalf("output de coder no contiene código: %s", coder.Output)
	}
}

func TestDeriveTitle(t *testing.T) {
	cases := map[string]string{
		"hola mundo":        "hola mundo",
		"Arregla   el\tbug": "Arregla el bug",
		"<context_files>\n<file path=\"a\">x</file>\n</context_files>\npregunta real": "pregunta real",
		"": "Sesión sin título",
	}
	for in, want := range cases {
		if got := DeriveTitle(in); got != want {
			t.Fatalf("DeriveTitle(%q) = %q, want %q", in, got, want)
		}
	}
	if title := DeriveTitle(strings.Repeat("a", 80)); len([]rune(title)) > 52 {
		t.Fatalf("título demasiado largo: %q", title)
	}
}

func TestModelsCacheRoundTrip(t *testing.T) {
	withTempHome(t)

	cache, err := LoadModelsCache()
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.Models("deepinfra")) != 0 {
		t.Fatal("caché inicial no vacío")
	}

	cache, err = SaveModelsCache(cache, "deepinfra", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadModelsCache()
	if err != nil {
		t.Fatal(err)
	}
	models := reloaded.Models("deepinfra")
	if len(models) != 2 || models[0] != "a" {
		t.Fatalf("modelos mal cacheados: %v", models)
	}
}
