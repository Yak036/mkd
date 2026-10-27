package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFuzzyScore(t *testing.T) {
	cases := []struct {
		pattern string
		target  string
		want    bool
	}{
		{"mgr", "internal/context/manager.go", true},
		{"aam", "internal/context/manager.go", false},
		{"mg", "internal/context/manager.go", true},
		{"ctxmgr", "internal/context/manager.go", true},
		{"nope", "internal/context/manager.go", false},
		{"XYZ", "internal/context/manager.go", false},
		{"mgrx", "internal/context/manager.go", false},
		{"", "anything", true},
		{"providers", "internal/context/providers.go", true},
	}
	for _, tc := range cases {
		_, ok := fuzzyScore(tc.pattern, tc.target)
		if ok != tc.want {
			t.Fatalf("fuzzyScore(%q, %q) ok=%v, want %v", tc.pattern, tc.target, ok, tc.want)
		}
	}

	// Prefijo de segmento y coincidencia contigua deben puntuar más alto.
	prefix, _ := fuzzyScore("providers", "providers.go")
	mid, _ := fuzzyScore("providers", "internal/context/providers.go")
	if prefix <= mid {
		t.Fatalf("el prefijo debe puntuar más: prefix=%d mid=%d", prefix, mid)
	}
}

func TestFileIndexRespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n!keep.log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		path := filepath.Join(dir, rel)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("src/a.go", "package src")
	write("debug.log", "log ignorado")
	write("keep.log", "log negativo")

	index := NewFileIndex(dir)
	if err := index.Build(); err != nil {
		t.Fatal(err)
	}
	files := index.Files()

	has := func(rel string) bool {
		for _, file := range files {
			if file == rel {
				return true
			}
		}
		return false
	}
	if !has("src/a.go") {
		t.Fatalf("falta src/a.go: %v", files)
	}
	if has("debug.log") {
		t.Fatalf("*.log no fue excluido: %v", files)
	}
	if !has("keep.log") {
		t.Fatalf("la negación !keep.log no se respetó: %v", files)
	}
	if has(".gitignore") == false {
		t.Fatalf("el .gitignore en sí debería estar indexado: %v", files)
	}

	matches := index.Match("ago", 5)
	if len(matches) == 0 || matches[0] != "src/a.go" {
		t.Fatalf("Match(ago) = %v", matches)
	}
}

func TestResolveTreeMaxDepth(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "l1", "l2", "l3", "l4"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"root.txt", "l1/a.go", "l1/l2/b.go", "l1/l2/l3/c.go", "l1/l2/l3/l4/d.go"} {
		path := filepath.Join(dir, rel)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out, err := resolveTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<project_tree>") || !strings.Contains(out, "</project_tree>") {
		t.Fatalf("bloque mal envuelto: %q", out)
	}
	if !strings.Contains(out, "l1/") || !strings.Contains(out, "a.go") {
		t.Fatalf("nivel 1 ausente: %q", out)
	}
	if !strings.Contains(out, "l2/") || !strings.Contains(out, "b.go") {
		t.Fatalf("nivel 2 ausente: %q", out)
	}
	if !strings.Contains(out, "l3/") {
		t.Fatalf("nivel 3 (directorio) ausente: %q", out)
	}
	if strings.Contains(out, "c.go") {
		t.Fatalf("c.go está en el 4º nivel y no debe aparecer: %q", out)
	}
	if strings.Contains(out, "l4/") || strings.Contains(out, "d.go") {
		t.Fatalf("nivel 4 no debe aparecer: %q", out)
	}
}

func TestResolveMentionFileBlock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hola mundo"), 0o600); err != nil {
		t.Fatal(err)
	}
	block, err := ResolveMention("@hello.txt", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block, `<file path="hello.txt">`) || !strings.Contains(block, "hola mundo") {
		t.Fatalf("bloque de archivo inesperado: %q", block)
	}
	if _, err := ResolveMention("@noexiste.txt", dir); err == nil {
		t.Fatal("se esperaba error para archivo inexistente")
	}
}

func TestParseIgnorePattern(t *testing.T) {
	cases := []struct {
		line     string
		ok       bool
		negate   bool
		dirOnly  bool
		anchored bool
	}{
		{"", false, false, false, false},
		{"# comentario", false, false, false, false},
		{"*.log", true, false, false, false},
		{"!keep.log", true, true, false, false},
		{"/build", true, false, false, true},
		{"src/tmp/", true, false, true, true},
		{"**/gen", true, false, false, true},
	}
	for _, tc := range cases {
		p, ok := parseIgnorePattern(tc.line)
		if ok != tc.ok {
			t.Fatalf("%q ok=%v want %v", tc.line, ok, tc.ok)
		}
		if !ok {
			continue
		}
		if p.negate != tc.negate || p.dirOnly != tc.dirOnly || p.anchored != tc.anchored {
			t.Fatalf("%q => %+v", tc.line, p)
		}
	}
}

func TestIgnoreMatcherGlobstarAndDefaults(t *testing.T) {
	m := newIgnoreMatcher()
	m.rules = append(m.rules, ignorePattern{segs: []string{"**", "gen"}})

	if !m.ignored("gen", true) {
		t.Fatal("**/gen debe casar en la raíz")
	}
	if !m.ignored("a/b/gen", true) {
		t.Fatal("**/gen debe casar en profundidad")
	}
	if !m.ignored("node_modules/x.js", false) {
		t.Fatal("node_modules debe ignorarse por defecto")
	}
	if m.ignored("src/main.go", false) {
		t.Fatal("src/main.go no debe ignorarse")
	}
}
