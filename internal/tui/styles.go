package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Color palette — per-agent colors and UI chrome.
var (
	colorClaude  = lipgloss.Color("#B388FF") // purple
	colorGemini  = lipgloss.Color("#64FFDA") // teal
	colorCodex   = lipgloss.Color("#FFD54F") // amber
	colorOllama  = lipgloss.Color("#81C784") // green
	colorQwen    = lipgloss.Color("#FFA726") // orange — Qwen 3.5 head orchestrator
	colorGemma   = lipgloss.Color("#7E57C2") // deep purple — Gemma 4 reasoner
	colorLlama   = lipgloss.Color("#26C6DA") // cyan — Llama 4 Scout analyzer
	colorDefault = lipgloss.Color("#90A4AE") // grey
	colorError   = lipgloss.Color("#EF5350") // red
	colorSystem  = lipgloss.Color("#42A5F5") // blue
	colorBorder  = lipgloss.Color("#546E7A") // dark grey
	colorFocused = lipgloss.Color("#B0BEC5") // light grey
)

// agentColor returns the display color for a given agent name.
func agentColor(name string) color.Color {
	switch name {
	case "claude":
		return colorClaude
	case "gemini":
		return colorGemini
	case "codex":
		return colorCodex
	case "ollama":
		return colorOllama
	case "qwen3.5":
		return colorQwen
	case "gemma4":
		return colorGemma
	case "llama4-scout":
		return colorLlama
	case "ag3nts":
		return colorSystem
	case "error":
		return colorError
	case "system":
		return colorSystem
	default:
		return colorDefault
	}
}

// Panel border styles.
var (
	borderNormal = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder)

	borderFocused = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorFocused)
)

// Status bar style.
var statusBarStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#90A4AE")).
	Padding(0, 1)

// Agent status icons.
func statusIcon(status string) string {
	switch status {
	case "idle", "ready":
		return "●"
	case "running":
		return "◉"
	case "stopped":
		return "○"
	case "failed", "unavailable":
		return "✗"
	default:
		return "?"
	}
}

// Task status icons.
func taskIcon(status string) string {
	switch status {
	case "pending":
		return "○"
	case "queued":
		return "◎"
	case "running":
		return "→"
	case "completed":
		return "✓"
	case "failed":
		return "✗"
	default:
		return "?"
	}
}
