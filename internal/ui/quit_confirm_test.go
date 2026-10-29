package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yak036/mkd/internal/config"
)

func newTestWorkspaceModel(t *testing.T) Model {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("XDG_DATA_HOME", tmp)
	cfg := config.Defaults()
	m := New(Options{Client: nil, Config: cfg})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(Model)
	m.state = stateWorkspace
	m.layout()
	return m
}

// TestQuitConfirmOnCtrlC verifica que presionar Ctrl+C una vez no cierra MKD,
// sino que pide confirmación. Presionar Ctrl+C una segunda vez confirma la salida.
func TestQuitConfirmOnCtrlC(t *testing.T) {
	m := newTestWorkspaceModel(t)

	// 1. Primera pulsación de Ctrl+C: debe abrir el modal de confirmación y NO salir.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)

	if m.modal.kind != modalQuitConfirm {
		t.Fatalf("se esperaba modalQuitConfirm tras primer Ctrl+C, pero quedó: %v", m.modal.kind)
	}
	if cmd != nil {
		// tea.Quit no debe ejecutarse en el primer Ctrl+C
		msg := cmd()
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			t.Fatal("el primer Ctrl+C no debía emitir tea.Quit")
		}
	}

	// Comprobar que la vista del modal muestra la confirmación
	view := m.modalView()
	if !strings.Contains(view, "¿Deseas salir de MKD?") {
		t.Fatalf("la vista del modal no contiene la pregunta esperada: %s", view)
	}

	// 2. Segunda pulsación de Ctrl+C: confirma la salida y devuelve tea.Quit.
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)

	if cmd == nil {
		t.Fatal("el segundo Ctrl+C debía devolver un tea.Cmd")
	}
	msg := cmd()
	if _, isQuit := msg.(tea.QuitMsg); !isQuit {
		t.Fatalf("el segundo Ctrl+C debía emitir tea.Quit, pero emitió: %T", msg)
	}
}

// TestQuitConfirmOnEsc verifica que presionar Esc en reposo no cierra MKD de golpe,
// sino que pide confirmación; y presionar Esc de nuevo en el modal cancela y vuelve al workspace.
func TestQuitConfirmOnEsc(t *testing.T) {
	m := newTestWorkspaceModel(t)

	// 1. Presionar Esc en reposo abre el diálogo de confirmación.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.modal.kind != modalQuitConfirm {
		t.Fatalf("se esperaba modalQuitConfirm tras presionar Esc, pero quedó: %v", m.modal.kind)
	}
	if cmd != nil {
		msg := cmd()
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			t.Fatal("Esc no debía emitir tea.Quit")
		}
	}

	// 2. Presionar Esc dentro del modal de confirmación CANCELA y vuelve al chat.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.modal.kind != modalNone {
		t.Fatalf("presionar Esc en el modal de confirmación debía cancelar a modalNone, pero quedó: %v", m.modal.kind)
	}
}

// TestQuitConfirmCancelWithN verifica que presionar 'n' cancela la salida.
func TestQuitConfirmCancelWithN(t *testing.T) {
	m := newTestWorkspaceModel(t)

	// Abrir modal con Ctrl+C
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)

	// Cancelar con 'n'
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = next.(Model)

	if m.modal.kind != modalNone {
		t.Fatalf("presionar 'n' debía cerrar el modal, pero quedó: %v", m.modal.kind)
	}
}

// TestQuitConfirmConfirmWithS verifica que presionar 's' confirma la salida.
func TestQuitConfirmConfirmWithS(t *testing.T) {
	m := newTestWorkspaceModel(t)

	// Abrir modal con Ctrl+C
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)

	// Confirmar con 's'
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("presionar 's' debía devolver cmd tea.Quit")
	}
	msg := cmd()
	if _, isQuit := msg.(tea.QuitMsg); !isQuit {
		t.Fatalf("se esperaba tea.QuitMsg, got: %T", msg)
	}
}

// TestQuitConfirmConfirmWithEnter verifica que presionar Enter en el modal confirma la salida.
func TestQuitConfirmConfirmWithEnter(t *testing.T) {
	m := newTestWorkspaceModel(t)

	// Abrir modal con Ctrl+C
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)

	// Confirmar con Enter
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("presionar Enter debía devolver cmd tea.Quit")
	}
	msg := cmd()
	if _, isQuit := msg.(tea.QuitMsg); !isQuit {
		t.Fatalf("se esperaba tea.QuitMsg, got: %T", msg)
	}
}

// TestSlashExitAndQuitCommands verifica que /exit y /quit abren la confirmación de salida.
func TestSlashExitAndQuitCommands(t *testing.T) {
	m := newTestWorkspaceModel(t)

	// Probar /exit
	m.textarea.SetValue("/exit")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if m.modal.kind != modalQuitConfirm {
		t.Fatalf("/exit debía abrir modalQuitConfirm, got: %v", m.modal.kind)
	}

	// Cancelar
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	// Probar /quit
	m.textarea.SetValue("/quit")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if m.modal.kind != modalQuitConfirm {
		t.Fatalf("/quit debía abrir modalQuitConfirm, got: %v", m.modal.kind)
	}
}

// TestQuitConfirmFromInsideModal verifica que si el usuario está dentro de un modal
// (ej. configuración de proveedores) y presiona Ctrl+C, no se sale de golpe sino que pide confirmación.
func TestQuitConfirmFromInsideModal(t *testing.T) {
	m := newTestWorkspaceModel(t)

	// Abrir modal de proveedores
	next, _ := m.openProviderModal()
	m = next

	if m.modal.kind != modalProvider {
		t.Fatalf("se esperaba modalProvider, got: %v", m.modal.kind)
	}

	// Presionar Ctrl+C dentro del modal
	next2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next2.(Model)

	if m.modal.kind != modalQuitConfirm {
		t.Fatalf("Ctrl+C dentro del modal debía abrir modalQuitConfirm, got: %v", m.modal.kind)
	}
	if cmd != nil {
		msg := cmd()
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			t.Fatal("Ctrl+C dentro de un modal no debía cerrar de golpe sin confirmar")
		}
	}
}
