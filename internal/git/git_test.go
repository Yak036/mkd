package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo crea un repositorio temporal con un commit inicial y devuelve su
// ruta. Si git no está instalado el test se salta.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git no disponible")
	}
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=MKD Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=MKD Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}

	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hola\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "chore: commit inicial")
	return dir
}

func TestStatusAndDiff(t *testing.T) {
	dir := initRepo(t)

	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := StatusPorcelain(dir)
	if err != nil {
		t.Fatal(err)
	}
	if status.HasStaged() {
		t.Fatal("no debería haber stage")
	}
	if len(status.Untracked) != 1 || status.Untracked[0] != "main.go" {
		t.Fatalf("untracked=%v", status.Untracked)
	}
	if status.Branch != "main" {
		t.Fatalf("branch=%q", status.Branch)
	}

	if err := AddAll(dir); err != nil {
		t.Fatal(err)
	}
	staged, err := StatusPorcelain(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !staged.HasStaged() {
		t.Fatal("debería haber stage tras add -A")
	}

	diff, err := StagedDiff(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "main.go") {
		t.Fatalf("diff sin main.go: %q", diff)
	}
}

func TestCommitPromptAndCommit(t *testing.T) {
	dir := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("uno\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AddAll(dir); err != nil {
		t.Fatal(err)
	}

	status, _ := StatusPorcelain(dir)
	log, err := RecentLog(dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 1 || !strings.Contains(log[0], "commit inicial") {
		t.Fatalf("log=%v", log)
	}

	prompt := CommitPrompt("diff --git a/x.txt b/x.txt\n+x", log, status)
	for _, needle := range []string{"Conventional Commits", "commit inicial", "Rama: main"} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("prompt sin %q", needle)
		}
	}

	result, err := Commit(dir, "feat(test): agregar x.txt")
	if err != nil {
		t.Fatal(err)
	}
	if result.Hash == "" {
		t.Fatal("commit sin hash")
	}
	if !strings.Contains(result.Subject, "feat(test)") {
		t.Fatalf("subject=%q", result.Subject)
	}
}

func TestFilesFromDiffAndTruncate(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-a\n+b\n"
	files := FilesFromDiff(diff)
	if len(files) != 1 || files[0] != "a.go" {
		t.Fatalf("files=%v", files)
	}

	small := TruncateDiff(diff)
	if !strings.Contains(small, "a.go") {
		t.Fatalf("truncado perdió a.go: %q", small)
	}

	// Diff enorme: fuerzo varios archivos de > MaxFileDiffBytes cada uno.
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString("diff --git a/file" + string(rune('a'+i)) + " b/file" + string(rune('a'+i)) + "\n")
		b.WriteString("@@ -1 +1 @@\n")
		b.WriteString(strings.Repeat("+linea de relleno\n", MaxFileDiffBytes/16))
	}
	out := TruncateDiff(b.String())
	if len(out) > MaxDiffBytes+1024 {
		t.Fatalf("diff no truncado: %d bytes", len(out))
	}
	if !strings.Contains(out, "truncado") {
		t.Fatalf("sin marca de truncado: %q", out[len(out)-120:])
	}
}

func TestReviewPromptStructure(t *testing.T) {
	prompt := ReviewPrompt("diff --git a/x b/x\n+a", []string{"x"}, "main")
	for _, needle := range []string{
		"Senior Code Reviewer",
		"🔴 Bugs Potenciales",
		"🟡 Seguridad",
		"🟢 Sugerencias",
		"main",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("prompt sin %q", needle)
		}
	}
}

func TestIsRepo(t *testing.T) {
	if !IsRepo(initRepo(t)) {
		t.Fatal("repo temporal no detectado")
	}
	if IsRepo(t.TempDir()) {
		t.Fatal("dir vacío detectado como repo")
	}
}
