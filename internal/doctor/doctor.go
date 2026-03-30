package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/symlinks"
	"github.com/rohanrgit/ag3nts/internal/tools"
	"github.com/rohanrgit/ag3nts/internal/ui"
	"github.com/rohanrgit/ag3nts/internal/workflow"
)

// CheckResult holds the outcome of a single diagnostic check.
type CheckResult struct {
	Name   string
	OK     bool
	Detail string
	Fix    string
}

// RunAll executes all diagnostic checks and returns the results.
func RunAll(layout *paths.Layout, cfg *config.Config) []CheckResult {
	var results []CheckResult

	// 1. Project directory
	results = append(results, CheckResult{
		Name: "Project directory", OK: true, Detail: layout.Base,
	})

	// 2. Config file
	if _, err := os.Stat(layout.ConfigFile()); err == nil {
		results = append(results, CheckResult{
			Name: "Config file", OK: true, Detail: layout.ConfigFile(),
		})
	} else {
		results = append(results, CheckResult{
			Name: "Config file", OK: false, Detail: "not found",
			Fix: "ag3nts install",
		})
	}

	// 3. Tools installed
	for _, toolName := range cfg.AllowedTools() {
		tool := tools.Get(toolName)
		if tool == nil {
			continue
		}
		if tool.IsInstalled(layout) {
			ver, _ := tool.InstalledVersion(layout)
			results = append(results, CheckResult{
				Name: fmt.Sprintf("Tool: %s", toolName), OK: true, Detail: ver,
			})
		} else {
			results = append(results, CheckResult{
				Name: fmt.Sprintf("Tool: %s", toolName), OK: false, Detail: "not installed",
				Fix: "ag3nts install",
			})
		}
	}

	// 4. Node.js (if Gemini in tier)
	if cfg.ToolAllowed("gemini") {
		nodeBin := filepath.Join(layout.ToolDir("node"), "bin", "node")
		if _, err := os.Stat(nodeBin); err == nil {
			out, err := exec.Command(nodeBin, "--version").Output()
			if err == nil {
				results = append(results, CheckResult{
					Name: "Node.js", OK: true, Detail: string(out),
				})
			}
		} else {
			results = append(results, CheckResult{
				Name: "Node.js", OK: false, Detail: "not found",
				Fix: "ag3nts install",
			})
		}
	}

	// 5. Symlinks
	for _, spec := range symlinks.AllSpecs(layout) {
		if cfg.ToolAllowed(spec.Name) {
			ok, detail := symlinks.Validate(spec)
			fix := ""
			if !ok {
				fix = "ag3nts install"
			}
			results = append(results, CheckResult{
				Name: fmt.Sprintf("Symlink: %s", spec.Link), OK: ok, Detail: detail,
				Fix: fix,
			})
		}
	}

	// 6. Auth status
	for _, toolName := range cfg.AllowedTools() {
		tool := tools.Get(toolName)
		if tool == nil {
			continue
		}
		authed, _ := tool.AuthStatus(layout)
		if authed {
			results = append(results, CheckResult{
				Name: fmt.Sprintf("Auth: %s", toolName), OK: true, Detail: "authenticated",
			})
		} else {
			results = append(results, CheckResult{
				Name: fmt.Sprintf("Auth: %s", toolName), OK: false, Detail: "not authenticated",
				Fix: fmt.Sprintf("ag3nts run %s (then authenticate)", toolName),
			})
		}
	}

	// 7. Workflow
	if workflow.HasWorkflow(layout) {
		results = append(results, CheckResult{
			Name: "Workflow", OK: true, Detail: cfg.Workflows.Active,
		})
	} else {
		results = append(results, CheckResult{
			Name: "Workflow", OK: false, Detail: "none installed",
			Fix: "ag3nts workflow install <name> --repo <url>",
		})
	}

	// 8. MCP config
	settingsPath := filepath.Join(layout.ConfigDir("claude"), "settings.json")
	if data, err := os.ReadFile(settingsPath); err == nil {
		if strings.Contains(string(data), "ag3nts") {
			results = append(results, CheckResult{
				Name: "MCP server config", OK: true, Detail: "configured in Claude settings",
			})
		} else {
			results = append(results, CheckResult{
				Name: "MCP server config", OK: false, Detail: "not found in Claude settings",
				Fix: "ag3nts workflow install <name> --repo <url>",
			})
		}
	} else {
		results = append(results, CheckResult{
			Name: "MCP server config", OK: false, Detail: "Claude settings.json not found",
			Fix: "ag3nts workflow install <name> --repo <url>",
		})
	}

	// 9. Git (required for workflows)
	if _, err := exec.LookPath("git"); err == nil {
		results = append(results, CheckResult{
			Name: "Git", OK: true, Detail: "available",
		})
	} else {
		results = append(results, CheckResult{
			Name: "Git", OK: false, Detail: "not found",
			Fix: "xcode-select --install",
		})
	}

	return results
}

// Print displays check results in checklist format.
func Print(results []CheckResult) {
	ui.Header("ag3nts doctor")
	passed, failed := 0, 0
	for _, r := range results {
		if r.OK {
			if r.Detail != "" {
				ui.OK(fmt.Sprintf("%s — %s", r.Name, r.Detail))
			} else {
				ui.OK(r.Name)
			}
			passed++
		} else {
			detail := r.Detail
			if r.Fix != "" {
				detail += fmt.Sprintf(" → fix: %s", r.Fix)
			}
			ui.Fail(fmt.Sprintf("%s — %s", r.Name, detail))
			failed++
		}
	}
	fmt.Printf("\n%d passed, %d failed\n", passed, failed)
}

