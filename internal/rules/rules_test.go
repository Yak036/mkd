package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFrontmatter(t *testing.T) {
	meta, body := parseFrontmatter(`---
description: Convenciones Go
globs: internal/**/*.go, cmd/*.go
alwaysApply: true
priority: 7
---

# Regla
Usa gofmt.`)

	if meta["description"] != "Convenciones Go" {
		t.Fatalf("description=%q", meta["description"])
	}
	if !parseBool(meta["alwaysapply"]) {
		t.Fatalf("alwaysApply no parseado: %q", meta["alwaysapply"])
	}
	if parseInt(meta["priority"]) != 7 {
		t.Fatalf("priority=%q", meta["priority"])
	}
	globs := splitList(meta["globs"])
	if len(globs) != 2 || globs[0] != "internal/**/*.go" {
		t.Fatalf("globs=%v", globs)
	}
	if !strings.Contains(body, "Usa gofmt.") {
		t.Fatalf("body=%q", body)
	}
}

func TestParseFrontmatterBlockList(t *testing.T) {
	meta, _ := parseFrontmatter(`---
globs:
  - "*.md"
  - internal/*.go
---
contenido`)
	globs := splitList(meta["globs"])
	if len(globs) != 2 || globs[0] != "*.md" || globs[1] != "internal/*.go" {
		t.Fatalf("globs lista YAML=%v", globs)
	}
}

func TestLoadCascadeAndLegacy(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module x\n")
	writeFile(t, filepath.Join(root, ".mkd", "rules", "go.md"), "---\ndescription: base\n---\nBase.")
	writeFile(t, filepath.Join(root, "internal", ".mkd", "rules", "deep.md"), "---\ndescription: profunda\n---\nProfunda.")
	writeFile(t, filepath.Join(root, "MKD.md"), "Contexto legacy.")
	writeFile(t, filepath.Join(root, "internal", "MKD.md"), "Contexto legacy interno.")

	work := filepath.Join(root, "internal")
	list, err := Load(t.TempDir(), work)
	if err != nil {
		t.Fatal(err)
	}

	names := Names(list)
	want := map[string]bool{"go": true, "deep": true, "mkd-internal": true}
	for _, name := range names {
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("faltan reglas %v (hay %v)", want, names)
	}

	for _, rule := range list {
		if rule.Name == "mkd-internal" && !rule.AlwaysApply {
			t.Fatal("MKD.md debe ser alwaysApply")
		}
	}
}

func TestLoadNoProjectRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".mkd", "rules", "only.md"), "solo proyecto")
	list, err := Load(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "only" {
		t.Fatalf("reglas=%v", Names(list))
	}
}

func TestSelectAlwaysAndGlobs(t *testing.T) {
	list := []Rule{
		{Name: "always", AlwaysApply: true, Body: "a", Path: "/a"},
		{Name: "go", Globs: []string{"internal/**/*.go"}, Body: "g", Path: "/g"},
		{Name: "docs", Globs: []string{"*.md"}, Body: "d", Path: "/d"},
		{Name: "manual", Body: "m", Path: "/m"},
	}

	sel := Select(list, []string{"internal/ai/client.go"}, nil)
	names := ruleNames(sel.Rules)
	if !has(names, "always") || !has(names, "go") {
		t.Fatalf("selección=%v", names)
	}
	if has(names, "docs") || has(names, "manual") {
		t.Fatalf("no debía incluir docs/manual: %v", names)
	}

	// Manual forzada.
	sel = Select(list, nil, []string{"manual"})
	if !has(ruleNames(sel.Rules), "manual") {
		t.Fatalf("manual no incluida: %v", ruleNames(sel.Rules))
	}
}

func TestSelectPriorityOrder(t *testing.T) {
	list := []Rule{
		{Name: "low", AlwaysApply: true, Priority: 1, Body: "l", Path: "/l"},
		{Name: "high", AlwaysApply: true, Priority: 50, Body: "h", Path: "/h"},
	}
	sel := Select(list, nil, nil)
	if len(sel.Rules) != 2 || sel.Rules[0].Name != "high" {
		t.Fatalf("orden=%v", ruleNames(sel.Rules))
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		scope   string
		pattern string
		target  string
		want    bool
	}{
		{"", "*.go", "internal/ai/client.go", true},
		{"", "*.go", "README.md", false},
		{"", "internal/**/*.go", "internal/ai/deep/x.go", true},
		{"", "internal/**/*.go", "cmd/main.go", false},
		{"", "internal/*.go", "internal/ai/client.go", false},
		{"", "internal/*.go", "internal/tools.go", true},
		{"", "**/*.md", "a/b/c.md", true},
		{"", "cmd/?.go", "cmd/a.go", true},
		{"", "cmd/?.go", "cmd/ab.go", false},
	}
	for _, tc := range cases {
		if got := matchGlob(tc.scope, tc.pattern, tc.target); got != tc.want {
			t.Fatalf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.target, got, tc.want)
		}
	}
}

func TestBlockRender(t *testing.T) {
	block := Block([]Rule{
		{Name: "go", Source: "proyecto", Globs: []string{"*.go"}, Body: "Usa gofmt."},
	})
	if !strings.Contains(block, `<rule name="go" source="proyecto" globs="*.go">`) {
		t.Fatalf("bloque=%q", block)
	}
	if !strings.Contains(block, "Usa gofmt.") || !strings.HasSuffix(block, "</project_rules>") {
		t.Fatalf("bloque=%q", block)
	}
	if Block(nil) != "" {
		t.Fatal("sin reglas debe devolver vacío")
	}
}

func TestFind(t *testing.T) {
	list := []Rule{{Name: "Go-Style"}, {Name: "docs"}}
	if rule, ok := Find(list, "go-style"); !ok || rule.Name != "Go-Style" {
		t.Fatal("Find case-insensitive falló")
	}
	if _, ok := Find(list, "nope"); ok {
		t.Fatal("Find encontró algo inexistente")
	}
}

func ruleNames(list []Rule) []string {
	out := make([]string, 0, len(list))
	for _, rule := range list {
		out = append(out, rule.Name)
	}
	return out
}

func has(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}
