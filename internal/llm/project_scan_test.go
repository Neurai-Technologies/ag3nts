package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanProject_EmptyWorkDir(t *testing.T) {
	if got := ScanProject(""); got != "" {
		t.Errorf("ScanProject(\"\") = %q, want empty", got)
	}
}

func TestScanProject_NonexistentDir(t *testing.T) {
	if got := ScanProject("/nonexistent/path/that/should/not/exist"); got != "" {
		t.Errorf("ScanProject(missing) = %q, want empty", got)
	}
}

func TestScanProject_DirNoUsefulContent(t *testing.T) {
	// An empty directory with no git, no files. ScanProject returns
	// "" because there's nothing useful to surface.
	dir := t.TempDir()
	got := ScanProject(dir)
	if got != "" {
		t.Errorf("ScanProject(empty dir) = %q, want empty", got)
	}
}

func TestScanProject_WithFiles(t *testing.T) {
	dir := t.TempDir()
	// Create a couple of files and a subdir.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}

	got := ScanProject(dir)
	if got == "" {
		t.Fatal("ScanProject returned empty for non-empty dir")
	}
	if !strings.Contains(got, "<project_context>") || !strings.Contains(got, "</project_context>") {
		t.Errorf("missing wrapper tags: %q", got)
	}
	if !strings.Contains(got, "cwd: "+dir) {
		t.Errorf("missing cwd line: %q", got)
	}
	if !strings.Contains(got, "top-level entries:") {
		t.Errorf("missing top-level entries section: %q", got)
	}
	if !strings.Contains(got, "main.go") {
		t.Errorf("missing main.go in top-level: %q", got)
	}
	if !strings.Contains(got, "go.mod") {
		t.Errorf("missing go.mod in top-level: %q", got)
	}
	if !strings.Contains(got, "src/") {
		t.Errorf("missing src/ subdir marker: %q", got)
	}
}

func TestScanProject_WithProjectDoc(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	doc := "# My Project\n\nThis is the agent instruction file.\n"
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}

	got := ScanProject(dir)
	if !strings.Contains(got, "CLAUDE.md:") {
		t.Errorf("missing CLAUDE.md section header: %q", got)
	}
	if !strings.Contains(got, "This is the agent instruction file") {
		t.Errorf("missing CLAUDE.md content: %q", got)
	}
}

func TestScanProject_ProjectDocPriorityOrder(t *testing.T) {
	// CLAUDE.md should be picked over AGENTS.md when both exist.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude doc"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("agents doc"), 0644); err != nil {
		t.Fatal(err)
	}
	got := ScanProject(dir)
	if !strings.Contains(got, "CLAUDE.md:") {
		t.Errorf("expected CLAUDE.md to win priority: %q", got)
	}
	if !strings.Contains(got, "claude doc") {
		t.Errorf("expected CLAUDE.md content: %q", got)
	}
	// AGENTS.md content should NOT appear.
	if strings.Contains(got, "agents doc") {
		t.Errorf("AGENTS.md should be skipped when CLAUDE.md exists: %q", got)
	}
}

func TestScanProject_HiddenFilesFiltered(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".secret"), []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("z"), 0644); err != nil {
		t.Fatal(err)
	}
	got := ScanProject(dir)
	if strings.Contains(got, ".secret") {
		t.Errorf(".secret should be filtered out of top-level: %q", got)
	}
	if !strings.Contains(got, ".gitignore") {
		t.Errorf(".gitignore should be visible in top-level: %q", got)
	}
}

func TestScanProject_LongDocTruncated(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create a 5000-byte doc; ScanProject should truncate at ~3000.
	long := strings.Repeat("a", 5000)
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(long), 0644); err != nil {
		t.Fatal(err)
	}
	got := ScanProject(dir)
	if !strings.Contains(got, "[truncated") {
		t.Errorf("expected truncation marker for 5000-char doc: %q", got)
	}
}
