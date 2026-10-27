package ui

import (
	"strings"
	"testing"

	mkdcontext "mkd/internal/context"
)

func TestMentionQueryAt(t *testing.T) {
	cases := []struct {
		value string
		want  string
		ok    bool
	}{
		{"@", "", true},
		{"hola @", "", true},
		{"hola @prov", "prov", true},
		{"ver @internal/co", "internal/co", true},
		{"sin mencion", "", false},
		{"email@dominio", "", false},
		{"linea1\n@git:di", "git:di", true},
		{"@foo bar", "", false},
	}
	for _, tc := range cases {
		got, ok := mentionQueryAt(tc.value)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("mentionQueryAt(%q) = %q,%v; want %q,%v", tc.value, got, ok, tc.want, tc.ok)
		}
	}
}

func TestMentionPopupFlow(t *testing.T) {
	m := New(Options{})
	m.state = stateWorkspace
	m.textarea.SetValue("@")
	m.updateMentions()
	if !m.MentionPopupVisible() {
		t.Fatal("popup debería estar visible tras @")
	}
	if len(m.mentions.popup.matches) == 0 {
		t.Fatal("sin matches para @")
	}

	// Especiales siempre primero con @ vacío.
	for i := 0; i < len(mkdcontext.SpecialProviders); i++ {
		if !m.mentions.popup.matches[i].Special {
			t.Fatalf("fila %d no es especial: %+v", i, m.mentions.popup.matches[i])
		}
	}

	// Espacio final tras completar oculta el popup (patrón suppressed).
	m.textarea.SetValue("@providers.go ")
	m.updateMentions()
	if m.MentionPopupVisible() {
		t.Fatal("el popup no debe mostrarse con token cerrado por espacio")
	}
}

func TestMentionsViewRenders(t *testing.T) {
	m := New(Options{})
	m.state = stateWorkspace
	m.width, m.height = 100, 30
	m.layout()
	m.textarea.SetValue("@")
	m.updateMentions()
	view, x, y := m.MentionsLayer()
	if view == "" {
		t.Fatal("MentionsLayer vacío con @")
	}
	if !strings.Contains(view, "@ contexto") {
		t.Fatalf("encabezado ausente: %q", view)
	}
	if x != 2 || y <= 0 {
		t.Fatalf("posición inesperada: x=%d y=%d", x, y)
	}
}

func TestMentionContextBlockStable(t *testing.T) {
	m := New(Options{})
	m.mentions.Mentions = map[string]string{
		"@tree":     "<project_tree>\nb\n</project_tree>",
		"@git:diff": "<git_diff>\na\n</git_diff>",
	}
	block := m.mentionsContextBlock()
	if !strings.Contains(block, "<git_diff>") || !strings.Contains(block, "<project_tree>") {
		t.Fatalf("bloque incompleto: %q", block)
	}
	if strings.Index(block, "<git_diff>") > strings.Index(block, "<project_tree>") {
		t.Fatalf("orden inestable: %q", block)
	}
}
