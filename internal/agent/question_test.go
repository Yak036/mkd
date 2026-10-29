package agent

import (
	"strings"
	"testing"
)

func TestParseQuestionNormalizes(t *testing.T) {
	call := Call{
		ID:   "call-1",
		Name: ToolAskUser,
		Arguments: `{"question":"¿Qué enfoque?","header":"Enfoque","options":[` +
			`{"label":"A","description":"rápido"},{"label":"  "},{"label":"B"}]}`,
	}
	question, err := ParseQuestion(call)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if question.Question != "¿Qué enfoque?" {
		t.Fatalf("pregunta = %q", question.Question)
	}
	if question.Header != "Enfoque" {
		t.Fatalf("header = %q", question.Header)
	}
	if len(question.Options) != 2 || question.Options[1].Label != "B" {
		t.Fatalf("opciones = %#v", question.Options)
	}
	if !question.AllowCustom {
		t.Fatal("el texto libre debe estar siempre disponible")
	}
}

func TestParseQuestionRejectsEmpty(t *testing.T) {
	_, err := ParseQuestion(Call{ID: "x", Name: ToolAskUser, Arguments: `{"question":"   "}`})
	if err == nil {
		t.Fatal("se esperaba error con pregunta vacía")
	}
	if !IsArgumentError(err) {
		t.Fatalf("debería ser ArgumentError para autocorrección: %v", err)
	}
}

func TestParseQuestionWithoutOptionsForcesCustom(t *testing.T) {
	question, err := ParseQuestion(Call{ID: "x", Name: ToolAskUser, Arguments: `{"question":"¿Nombre?"}`})
	if err != nil {
		t.Fatal(err)
	}
	if !question.AllowCustom {
		t.Fatal("sin opciones la respuesta libre debe ser obligatoria")
	}
}

func TestParseQuestionCapsOptions(t *testing.T) {
	call := Call{ID: "x", Name: ToolAskUser, Arguments: `{"question":"?", "options":[` +
		`{"label":"1"},{"label":"2"},{"label":"3"},{"label":"4"},{"label":"5"},{"label":"6"},{"label":"7"},{"label":"8"}]}`}
	question, err := ParseQuestion(call)
	if err != nil {
		t.Fatal(err)
	}
	if len(question.Options) != MaxQuestionOptions {
		t.Fatalf("opciones = %d, want %d", len(question.Options), MaxQuestionOptions)
	}
}

func TestFormatAnswer(t *testing.T) {
	question := Question{Question: "¿A o B?"}

	option := FormatAnswer(question, Answer{Label: "B"})
	if !strings.Contains(option, "B") || !strings.Contains(option, "opción seleccionada") {
		t.Fatalf("formato de opción: %q", option)
	}

	custom := FormatAnswer(question, Answer{Text: "C"})
	if !strings.Contains(custom, ": C") {
		t.Fatalf("formato de texto libre: %q", custom)
	}

	cancelled := FormatAnswer(question, Answer{Cancelled: true})
	if !strings.Contains(cancelled, "cancelada") {
		t.Fatalf("formato de cancelación: %q", cancelled)
	}
}
