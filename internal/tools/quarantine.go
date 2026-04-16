package tools

import "os/exec"

// stripQuarantine removes the macOS Gatekeeper quarantine attribute from a file.
func stripQuarantine(path string) {
	_ = exec.Command("xattr", "-d", "com.apple.quarantine", path).Run()
}
