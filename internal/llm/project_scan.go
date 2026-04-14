package llm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ScanProject inspects the working directory and returns a formatted
// project context block suitable for prepending to the system prompt.
// Gives the local LLM baseline awareness so it doesn't have to call
// read_file/run_command just to figure out where it is and what's
// around. Mirrors what Claude Code does at session start.
//
// Includes:
//   - Working directory path
//   - git status --short (working tree state, if a git repo)
//   - git log --oneline -5 (recent commits, if a git repo)
//   - Top-level entries (visible files and dirs at the root)
//   - Contents of CLAUDE.md or AGENTS.md if present
//
// Returns an empty string if workDir is empty or doesn't exist —
// the caller should treat empty as "no priming, proceed without".
//
// All operations are read-only and best-effort: any individual probe
// failing (e.g. not a git repo) just omits that section. The whole
// scan should complete in well under 500ms in the common case.
func ScanProject(workDir string) string {
	if workDir == "" {
		return ""
	}
	info, err := os.Stat(workDir)
	if err != nil || !info.IsDir() {
		return ""
	}

	var b strings.Builder
	b.WriteString("<project_context>\n")
	fmt.Fprintf(&b, "cwd: %s\n", workDir)

	if status := scanGitStatus(workDir); status != "" {
		b.WriteString("\ngit status:\n")
		b.WriteString(indentScan(status, "  "))
		b.WriteString("\n")
	}

	if log := scanGitLog(workDir); log != "" {
		b.WriteString("\nrecent commits:\n")
		b.WriteString(indentScan(log, "  "))
		b.WriteString("\n")
	}

	if files := scanTopLevel(workDir); files != "" {
		b.WriteString("\ntop-level entries:\n")
		b.WriteString(indentScan(files, "  "))
		b.WriteString("\n")
	}

	if doc, name := scanProjectDoc(workDir); doc != "" {
		fmt.Fprintf(&b, "\n%s:\n", name)
		b.WriteString(indentScan(doc, "  "))
		b.WriteString("\n")
	}

	b.WriteString("</project_context>")

	// If we only emitted the cwd line and the wrapper, there's nothing
	// useful to prime with — return empty so the caller skips injection.
	if !strings.Contains(b.String(), "git status:") &&
		!strings.Contains(b.String(), "top-level entries:") &&
		!strings.Contains(b.String(), "recent commits:") {
		return ""
	}
	return b.String()
}

// scanGitStatus runs `git status --short` in workDir. Returns the
// trimmed output, or "" on failure (not a git repo, git not installed,
// etc.). "" with successful exit means the working tree is clean —
// in that case we return "(clean)" so the LLM sees an explicit signal.
func scanGitStatus(workDir string) string {
	cmd := exec.Command("git", "-C", workDir, "status", "--short")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "(clean)"
	}
	// Cap to ~30 lines so a huge dirty tree doesn't blow context.
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 30 {
		lines = append(lines[:30], fmt.Sprintf("... %d more changes", len(lines)-30))
	}
	return strings.Join(lines, "\n")
}

// scanGitLog runs `git log --oneline -5` in workDir.
func scanGitLog(workDir string) string {
	cmd := exec.Command("git", "-C", workDir, "log", "--oneline", "-5")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// scanTopLevel returns a sorted list of visible top-level entries in
// workDir. Hidden files (starting with .) are skipped except for
// .gitignore, .env.example, and similar conventionally-shown files.
// Caps at 40 entries to keep the prompt compact.
func scanTopLevel(workDir string) string {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			// Allow a few conventionally-visible dotfiles.
			switch name {
			case ".gitignore", ".env.example", ".dockerignore", ".editorconfig":
				// keep
			default:
				continue
			}
		}
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 40 {
		names = append(names[:40], fmt.Sprintf("... %d more", len(names)-40))
	}
	return strings.Join(names, "  ")
}

// scanProjectDoc looks for a project-level instruction file and
// returns its contents (truncated) and filename. Looks at common
// agent-instruction files in priority order. Returns empty strings
// if none found.
func scanProjectDoc(workDir string) (string, string) {
	candidates := []string{"CLAUDE.md", "AGENTS.md", "ag3nts.md"}
	for _, name := range candidates {
		path := filepath.Join(workDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		// Cap to first 3000 chars (~750 tokens) so a long doc
		// doesn't dominate the system prompt.
		if len(content) > 3000 {
			content = content[:3000] + "\n... [truncated, full doc at " + name + "]"
		}
		return strings.TrimSpace(content), name
	}
	return "", ""
}

// indentScan prepends prefix to each line of s. Local helper so we
// don't depend on the tui package's indent function.
func indentScan(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
