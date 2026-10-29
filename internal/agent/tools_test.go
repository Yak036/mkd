package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// kinds resume el diff a una cadena tipo "+-  " para comparar fácil.
func kinds(lines []DiffLine) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line.Kind.String())
	}
	return b.String()
}

func TestUnifiedDiffAddRemoveContext(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\n"
	new := "a\nb\nc\nD\ne\nf\ng\nh\ni\n"

	diff := UnifiedDiff(old, new, "x.txt", 3)
	// a b c = contexto previo; d→D es el cambio; e f g h = contexto posterior
	// (max 3 antes y 3 después... aquí la "i" final también es cambio, así que
	// se conserva su ventana: una sola línea de contexto antes).
	got := kinds(diff)
	want := "   -+    +"
	if got != want {
		t.Fatalf("kinds = %q, want %q", got, want)
	}

	// La línea modificada debe aparecer como remove d y add D con números
	// correctos en ambos lados.
	var removed, added *DiffLine
	for i := range diff {
		switch {
		case diff[i].Kind == DiffRemove && diff[i].Text == "d":
			removed = &diff[i]
		case diff[i].Kind == DiffAdd && diff[i].Text == "D":
			added = &diff[i]
		}
	}
	if removed == nil || removed.OldNo != 4 || removed.NewNo != 0 {
		t.Fatalf("remove d = %+v, want OldNo=4 NewNo=0", removed)
	}
	if added == nil || added.NewNo != 4 || added.OldNo != 0 {
		t.Fatalf("add D = %+v, want NewNo=4 OldNo=0", added)
	}

	adds, removes := CountDiff(diff)
	if adds != 2 || removes != 1 {
		t.Fatalf("counts adds=%d removes=%d, want 2/1", adds, removes)
	}
}

func TestUnifiedDiffNewFile(t *testing.T) {
	diff := UnifiedDiff("", "uno\ndos\n", "nuevo.txt", 3)
	if kinds(diff) != "++" {
		t.Fatalf("kinds = %q, want ++", kinds(diff))
	}
	if diff[0].NewNo != 1 || diff[1].NewNo != 2 {
		t.Fatalf("numeración nueva incorrecta: %+v", diff)
	}
}

func TestUnifiedDiffIdentical(t *testing.T) {
	if diff := UnifiedDiff("a\nb\n", "a\nb\n", "x", 3); len(diff) != 0 {
		t.Fatalf("diff idéntico no debe tener líneas, got %v", kinds(diff))
	}
}

func TestApplyPatchUniqueAndAmbiguous(t *testing.T) {
	content := "línea uno\nobjetivo\nlínea tres\n"

	updated, err := applyPatch(content, "objetivo", "reemplazo")
	if err != nil {
		t.Fatalf("patch único falló: %v", err)
	}
	if !strings.Contains(updated, "reemplazo") || strings.Contains(updated, "objetivo") {
		t.Fatalf("resultado inesperado: %q", updated)
	}

	if _, err := applyPatch(content, "no-existe", "x"); err == nil {
		t.Fatal("esperaba error cuando search no aparece")
	}

	ambiguous := "x\nrep\nrep\n"
	if _, err := applyPatch(ambiguous, "rep", "y"); err == nil {
		t.Fatal("esperaba error cuando search es ambiguo")
	}
}

func TestSafePathRejectsEscapes(t *testing.T) {
	cwd := t.TempDir()

	for _, p := range []string{"../..", "../../etc/passwd", "a/../../b", "/etc/passwd"} {
		if _, _, err := SafePath(cwd, p); err == nil {
			t.Fatalf("SafePath(%q) debía rechazarse", p)
		}
	}

	abs, rel, err := SafePath(cwd, "sub/archivo.txt")
	if err != nil {
		t.Fatalf("ruta válida rechazada: %v", err)
	}
	if rel != "sub/archivo.txt" {
		t.Fatalf("rel = %q", rel)
	}
	if !strings.HasPrefix(abs, cwd) {
		t.Fatalf("abs %q fuera de cwd %q", abs, cwd)
	}

	// Una ruta absoluta dentro del cwd es válida.
	inside := filepath.Join(cwd, "ok.txt")
	if _, rel, err := SafePath(cwd, inside); err != nil || rel != "ok.txt" {
		t.Fatalf("ruta absoluta interna: rel=%q err=%v", rel, err)
	}
}

func TestBuildProposalAutoApproval(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		call Call
		auto bool
	}{
		{"read", Call{Name: ToolReadFile, Arguments: `{"path":"main.go"}`}, true},
		{"list", Call{Name: ToolListDir, Arguments: `{}`}, true},
		{"search", Call{Name: ToolSearchCode, Arguments: `{"query":"main"}`}, true},
		{"write", Call{Name: ToolWriteFile, Arguments: `{"path":"nuevo.txt","content":"hola\n"}`}, false},
		{"patch", Call{Name: ToolPatchFile, Arguments: `{"path":"main.go","search":"package main","replace":"package x"}`}, false},
		{"run", Call{Name: ToolRunCommand, Arguments: `{"command":"true"}`}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prop, err := BuildProposal(tc.call, cwd)
			if err != nil {
				t.Fatalf("BuildProposal: %v", err)
			}
			if prop.AutoApproved != tc.auto {
				t.Fatalf("AutoApproved = %v, want %v", prop.AutoApproved, tc.auto)
			}
		})
	}
}

func TestBuildProposalPatchNeedsUniqueSearch(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "dup.txt")
	if err := os.WriteFile(path, []byte("rep\nrep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call := Call{Name: ToolPatchFile, Arguments: `{"path":"dup.txt","search":"rep","replace":"x"}`}
	if _, err := BuildProposal(call, cwd); err == nil {
		t.Fatal("esperaba error de patch ambiguo")
	}
	// El archivo no debe tocarse durante BuildProposal.
	data, _ := os.ReadFile(path)
	if string(data) != "rep\nrep\n" {
		t.Fatalf("BuildProposal modificó el disco: %q", data)
	}
}

func TestExecuteWriteAndPatch(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "a.txt")

	write, err := BuildProposal(Call{Name: ToolWriteFile, Arguments: `{"path":"a.txt","content":"hola\n"}`}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(write, cwd); err != nil {
		t.Fatalf("Execute write: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "hola\n" {
		t.Fatalf("contenido = %q", data)
	}

	patch, err := BuildProposal(Call{Name: ToolPatchFile, Arguments: `{"path":"a.txt","search":"hola","replace":"adiós"}`}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(patch, cwd); err != nil {
		t.Fatalf("Execute patch: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "adiós\n" {
		t.Fatalf("contenido tras patch = %q", data)
	}
}

func TestExecuteRunCommand(t *testing.T) {
	cwd := t.TempDir()
	prop, err := BuildProposal(Call{Name: ToolRunCommand, Arguments: `{"command":"echo hola && exit 3"}`}, cwd)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Execute(prop, cwd)
	if err != nil {
		t.Fatalf("Execute run: %v", err)
	}
	if !strings.Contains(out, "exit=3") || !strings.Contains(out, "hola") {
		t.Fatalf("salida inesperada: %q", out)
	}
}

func TestParseCallsAndMessages(t *testing.T) {
	calls := ParseCalls([]openai.ToolCall{
		{ID: "1", Function: openai.FunctionCall{Name: ToolReadFile, Arguments: `{"path":"x"}`}},
	})
	if len(calls) != 1 || calls[0].ID != "1" || calls[0].Name != ToolReadFile {
		t.Fatalf("ParseCalls = %+v", calls)
	}

	msg := ResultMessage("1", "ok")
	if msg.Role != openai.ChatMessageRoleTool || msg.ToolCallID != "1" || msg.Content != "ok" {
		t.Fatalf("ResultMessage = %+v", msg)
	}
	rej := RejectedMessage("2")
	if rej.Content != RejectedContent || rej.ToolCallID != "2" {
		t.Fatalf("RejectedMessage = %+v", rej)
	}
}

func TestToolsCatalog(t *testing.T) {
	want := []string{ToolReadFile, ToolListDir, ToolSearchCode, ToolWriteFile, ToolPatchFile, ToolRunCommand}
	if len(Tools) != len(want) {
		t.Fatalf("Tools tiene %d entradas, want %d", len(Tools), len(want))
	}
	for i, tool := range Tools {
		if tool.Function == nil || tool.Function.Name != want[i] {
			t.Fatalf("tool %d = %+v, want %s", i, tool.Function, want[i])
		}
		if tool.Type != openai.ToolTypeFunction {
			t.Fatalf("tool %s no es de tipo function", want[i])
		}
	}
}

func TestReadInvertedAndOutOfBounds(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.txt")
	var content strings.Builder
	for i := 1; i <= 2000; i++ {
		content.WriteString("linea de prueba\n")
	}
	if err := os.WriteFile(filePath, []byte(content.String()), 0600); err != nil {
		t.Fatal(err)
	}

	// Caso 1: Rango invertido start=1559, end=1060 (el que causó el pánico exacto en la captura)
	prop, err := BuildProposal(Call{
		Name:      ToolReadFile,
		Arguments: `{"path":"sample.txt","start_line":1559,"end_line":1060}`,
	}, dir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Read(prop, dir)
	if err != nil {
		t.Fatalf("Read fallo con error: %v", err)
	}
	if !strings.Contains(out, "líneas 1060-1559") {
		t.Fatalf("se esperaba que invirtiera el rango ordenadamente, got: %s", out)
	}

	// Caso 2: start fuera del archivo
	propOut, _ := BuildProposal(Call{
		Name:      ToolReadFile,
		Arguments: `{"path":"sample.txt","start_line":9999}`,
	}, dir)
	outOut, err := Read(propOut, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outOut, "solo tiene 2000 líneas") {
		t.Fatalf("se esperaba mensaje de fuera de rango: %s", outOut)
	}
}
