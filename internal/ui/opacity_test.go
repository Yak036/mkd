package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"strings"
	"testing"
)

func TestViewIsAlwaysOpaque(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := swarmTestModel(t)
	m.width = 120
	m.height = 35
	m.layout()

	// 1. Vista normal
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) == 0 {
		t.Fatal("vista vacía")
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "\x1b[48;2;24;24;27m") {
			t.Fatalf("línea %d no empieza con el fondo opaco surface: %q", i, line)
		}
		if !strings.HasSuffix(line, "\x1b[0m") {
			t.Fatalf("línea %d no termina cerrando estilo: %q", i, line)
		}
	}

	// 2. Vista con modal abierto
	m.modal.kind = modalProvider
	m.modal.provider = newProviderModal(m.cfg)
	viewModal := m.View()
	linesModal := strings.Split(viewModal, "\n")
	for i, line := range linesModal {
		if !strings.HasPrefix(line, "\x1b[48;2;24;24;27m") {
			t.Fatalf("modal línea %d no empieza con fondo opaco: %q", i, line)
		}
	}
}
