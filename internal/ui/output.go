package ui

import (
	"fmt"
	"os"
)

const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

// Step prints a step indicator: "→ msg"
func Step(msg string) {
	fmt.Fprintf(os.Stderr, "%s→%s %s\n", colorCyan, colorReset, msg)
}

// OK prints a success indicator: "✓ msg"
func OK(msg string) {
	fmt.Fprintf(os.Stderr, "%s✓%s %s\n", colorGreen, colorReset, msg)
}

// Skip prints a skip indicator: "⊘ msg"
func Skip(msg string) {
	fmt.Fprintf(os.Stderr, "%s⊘%s %s\n", colorYellow, colorReset, msg)
}

// Fail prints a failure indicator: "✗ msg"
func Fail(msg string) {
	fmt.Fprintf(os.Stderr, "%s✗%s %s\n", colorRed, colorReset, msg)
}

// Bold prints bold text.
func Bold(msg string) string {
	return colorBold + msg + colorReset
}

// Header prints a section header.
func Header(msg string) {
	fmt.Fprintf(os.Stderr, "\n%s%s%s\n", colorBold, msg, colorReset)
}
