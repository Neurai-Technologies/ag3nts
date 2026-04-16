package tui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// watchResize starts a goroutine that listens for SIGWINCH (terminal
// resize) signals and re-renders the in-place streaming region using
// the new terminal width. Without this, the region's cursor-math uses
// the terminal width captured on the previous chunk render — if the
// user resizes mid-stream, the next clear moves the cursor to the
// wrong place and the region renders corrupted.
//
// On resize: clear the old region (using old row count), then re-render
// the buffer at the current cursor position using the new terminal
// width. This eliminates the stale-frame artifact from the previous
// "reset to 0" approach.
//
// Returns immediately. The watcher exits cleanly when ctx is cancelled.
func (a *App) watchResize(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)

	go func() {
		defer signal.Stop(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				a.streamRegionMu.Lock()
				// Clear old region with stale row count.
				a.clearStreamRegionLocked()
				// Re-render with new terminal width.
				buf := a.streamRegionBuf
				if buf != "" {
					fmt.Fprint(os.Stderr, buf)
					a.streamRegionLines = countWrappedRows(buf, terminalCols())
					if a.streamRegionLines == 0 {
						a.streamRegionLines = 1
					}
				}
				a.streamRegionMu.Unlock()
			}
		}
	}()
}

// watchConfig polls the config file for modifications and auto-reloads
// hot-swappable settings (max_concurrency, routing, security, logging).
// Runs every 10 seconds. No external dependency (no fsnotify).
func (a *App) watchConfig(ctx context.Context) {
	if a.configPath == "" {
		return
	}

	var lastMod time.Time
	if info, err := os.Stat(a.configPath); err == nil {
		lastMod = info.ModTime()
	}

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				info, err := os.Stat(a.configPath)
				if err != nil {
					continue
				}
				if info.ModTime().Equal(lastMod) {
					continue
				}
				lastMod = info.ModTime()
				a.handleReload(ctx)
			}
		}
	}()
}
