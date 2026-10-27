package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanFindsSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "go-reviewer", "---\nname: go-reviewer\ndescription: Revisa Go idiomático\n---\n\nRevisa el código.")
	writeSkill(t, dir, "sql-optimizer", "Optimiza consultas SQL.")
	// Carpeta sin SKILL.md: se ignora sin error.
	os.MkdirAll(filepath.Join(dir, "not-a-skill"), 0o755)

	found, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("skills = %d, want 2 (%v)", len(found), found)
	}
	if found[0].Name != "go-reviewer" || found[0].Description != "Revisa Go idiomático" {
		t.Fatalf("primera skill inesperada: %#v", found[0])
	}
	if found[1].Description != "Optimiza consultas SQL." {
		t.Fatalf("descripción sin frontmatter: %q", found[1].Description)
	}
}

func TestScanMissingDirIsEmpty(t *testing.T) {
	found, err := Scan(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("carpeta inexistente no debe fallar: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("se esperaban 0 skills, hay %d", len(found))
	}
}

func TestBlockWrapsContent(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "deploy", "---\ndescription: Despliega a k3s\n---\n\nPasos de deploy.")

	found, _ := Scan(dir)
	block, err := found[0].Block()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(block, `<skill name="deploy">`) || !strings.HasSuffix(block, "</skill>") {
		t.Fatalf("bloque mal formado: %q", block)
	}
	if !strings.Contains(block, "Pasos de deploy.") {
		t.Fatalf("contenido ausente: %q", block)
	}
}

func TestInstallAndOverwrite(t *testing.T) {
	dir := t.TempDir()

	if _, err := Install(dir, "reviewer", "# Reviewer\n\nRevisa."); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, "reviewer", "otra"); err == nil {
		t.Fatal("Install debe fallar si la skill existe")
	}
	if _, err := Overwrite(dir, "reviewer", "actualizada"); err != nil {
		t.Fatal(err)
	}

	found, _ := Scan(dir)
	if len(found) != 1 {
		t.Fatalf("skills = %d", len(found))
	}
	content, _ := found[0].Load()
	if !strings.Contains(content, "actualizada") {
		t.Fatalf("no se sobrescribió: %q", content)
	}
}

func TestInstallRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"", "..", "../escape", "con espacio", "slash/name"} {
		if _, err := Install(dir, name, "x"); err == nil {
			t.Fatalf("nombre %q debía rechazarse", name)
		}
	}
}

func TestRemoveSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "temp", "contenido")
	if err := Remove(dir, "temp"); err != nil {
		t.Fatal(err)
	}
	found, _ := Scan(dir)
	if len(found) != 0 {
		t.Fatalf("la skill no se eliminó: %v", found)
	}
}

func TestResolveDirExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := ResolveDir("~/mi-skills"); got != filepath.Join(home, "mi-skills") {
		t.Fatalf("ResolveDir = %q", got)
	}
	if got := ResolveDir(""); got != DefaultDir() {
		t.Fatalf("ResolveDir vacío = %q, want %q", got, DefaultDir())
	}
}
