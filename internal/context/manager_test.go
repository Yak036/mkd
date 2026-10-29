package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yak036/mkd/internal/ai"
)

func TestCompactSplitKeepsRecent(t *testing.T) {
	cases := []struct {
		total int
		want  int
	}{
		{0, 0},
		{4, 0},
		{5, 1},
		{10, 6},
		{20, 14},
		{3, 0},
	}
	for _, tc := range cases {
		if got := compactSplit(tc.total); got != tc.want {
			t.Fatalf("compactSplit(%d) = %d, want %d", tc.total, got, tc.want)
		}
	}
}

func TestPrepareOrderAndUsage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ContextDocName), []byte("reglas del proyecto"), 0o600); err != nil {
		t.Fatal(err)
	}
	pinned := filepath.Join(dir, "main.go")
	if err := os.WriteFile(pinned, []byte("package main"), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager("system base", dir, 1000)
	if _, err := mgr.AddFile(pinned); err != nil {
		t.Fatal(err)
	}

	prepared, usage := mgr.Prepare("resumen previo", []ai.Message{
		{Role: ai.RoleUser, Content: "hola"},
		{Role: ai.RoleAssistant, Content: "qué tal"},
	})

	if !strings.Contains(prepared.System, "system base") {
		t.Fatalf("system sin prompt base: %q", prepared.System)
	}
	if !strings.Contains(prepared.System, "reglas del proyecto") {
		t.Fatalf("system sin MKD.md: %q", prepared.System)
	}
	if !strings.Contains(prepared.Files, "<context_files>") {
		t.Fatalf("bloque de archivos ausente: %q", prepared.Files)
	}

	messages := prepared.Messages()
	if len(messages) != 5 {
		t.Fatalf("mensajes = %d, want 5", len(messages))
	}
	if !strings.Contains(messages[2].Content, "context_summary") {
		t.Fatalf("resumen fuera de orden: %q", messages[2].Content)
	}
	if messages[3].Content != "hola" || messages[4].Content != "qué tal" {
		t.Fatalf("historial mal ordenado: %#v", messages[3:])
	}
	if usage.TotalTokens == 0 || usage.SystemTokens == 0 || usage.FileTokens == 0 {
		t.Fatalf("uso incompleto: %#v", usage)
	}
}

func TestRefreshFilesDetectsChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("uno"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager("", dir, 1000)
	if _, err := mgr.AddFile(path); err != nil {
		t.Fatal(err)
	}
	if changed := mgr.RefreshFiles(); len(changed) != 0 {
		t.Fatalf("cambio falso: %v", changed)
	}
	if err := os.WriteFile(path, []byte("dos"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed := mgr.RefreshFiles(); len(changed) != 1 || changed[0] != path {
		t.Fatalf("no detectó el cambio: %v", changed)
	}
}

// El resumen compactado se envía una sola vez: como <context_summary>, nunca
// duplicado como mensaje del historial.
func TestPrepareDoesNotDuplicateSummary(t *testing.T) {
	mgr := NewManager("", t.TempDir(), 1000)
	history := []ai.Message{
		{Role: ai.RoleUser, Content: "<context_summary>\nviejo\n</context_summary>"},
		{Role: ai.RoleUser, Content: "nuevo"},
	}

	prepared, _ := mgr.Prepare("resumen vivo", history)
	messages := prepared.Messages()

	summaryCount := 0
	for _, msg := range messages {
		if strings.Contains(msg.Content, "<context_summary>") {
			summaryCount++
		}
	}
	if summaryCount != 1 {
		t.Fatalf("bloques de resumen = %d, want 1: %#v", summaryCount, messages)
	}
	if len(prepared.History) != 1 || prepared.History[0].Content != "nuevo" {
		t.Fatalf("historial mal filtrado: %#v", prepared.History)
	}
}

// Compact no inyecta el resumen como mensaje: solo devuelve los recientes.
func TestCompactLeavesSummaryOutOfHistory(t *testing.T) {
	if got := withoutSummaryMessages([]ai.Message{
		{Role: ai.RoleUser, Content: "hola"},
		{Role: ai.RoleUser, Content: "  <context_summary>x</context_summary>"},
	}); len(got) != 1 {
		t.Fatalf("withoutSummaryMessages = %#v", got)
	}
}
