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
