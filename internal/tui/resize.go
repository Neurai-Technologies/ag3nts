package tui

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// watchResize starts a goroutine that listens for SIGWINCH (terminal
// resize) signals and force-clears the in-place streaming region on
// each one. Without this, the region's cursor-math uses the terminal
// width captured on the previous chunk render — if the user resizes
// mid-stream, the next clear moves the cursor to the wrong place and
// the region renders corrupted for one frame.
//
// The resize handler resets streamRegionLines to 0 without emitting
// any ANSI (no cursor-up, no erase). The next chunk arrives, the
// renderer sees streamRegionLines == 0 (no prior region to clear),
// and writes the buffer fresh at the current cursor position. This
// means a single frame of the prior region is left on screen after
// the resize — minor visual artifact, no corruption, self-heals on
// the next chunk.
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
				a.streamRegionLines = 0
				a.streamRegionMu.Unlock()
			}
		}
	}()
}
