package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"
)

// readSingleChar reads one character from stdin in raw terminal mode
// (no Enter required). Used by the permission prompt so pressing 1/2/3
// immediately selects the option. Falls back to fmt.Scanln if raw mode
// fails (e.g. stdin is a pipe).
func readSingleChar() string {
	fd := os.Stdin.Fd()
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// Fallback: readline-style input requiring Enter.
		var s string
		fmt.Scanln(&s)
		return strings.TrimSpace(s)
	}
	defer term.Restore(fd, oldState)

	var buf [1]byte
	_, _ = os.Stdin.Read(buf[:])
	fmt.Println() // newline since raw mode doesn't echo
	return string(buf[0])
}

// fallbackCols is used when the terminal width can't be determined.
// 80 is the historical default and conservative enough that wrap
// calculations don't badly under-estimate row counts.
const fallbackCols = 80

// terminalCols probes the terminal width from stderr (where streaming
// output goes), falling back to the COLUMNS env var, then to 80. The
// value is used by the in-place streaming region to compute how many
// terminal rows a wrapped buffer occupies.
func terminalCols() int {
	if w, _, err := term.GetSize(os.Stderr.Fd()); err == nil && w > 0 {
		return w
	}
	if env := os.Getenv("COLUMNS"); env != "" {
		if n, err := strconv.Atoi(env); err == nil && n > 0 {
			return n
		}
	}
	return fallbackCols
}

// countWrappedRows returns how many terminal rows the given text
// occupies when wrapped at cols characters per row. Newlines force
// new rows. Empty strings count as zero rows. Tabs are treated as
// 8-character soft-tabs (matches most terminals' default).
func countWrappedRows(text string, cols int) int {
	if text == "" {
		return 0
	}
	if cols <= 0 {
		cols = fallbackCols
	}
	rows := 0
	for _, line := range strings.Split(text, "\n") {
		w := visibleWidth(line)
		if w == 0 {
			rows++
			continue
		}
		// Ceiling division: a w-char line wraps to ceil(w/cols) rows.
		rows += (w + cols - 1) / cols
	}
	return rows
}

// visibleWidth approximates the visible column count of a line by
// counting runes (so non-ASCII chars count as 1 column each). This
// is correct for ASCII and most BMP characters; CJK wide chars and
// combining marks are slightly off but close enough for region
// row-counting. Tabs expand to the next 8-column boundary.
func visibleWidth(s string) int {
	w := 0
	for _, r := range s {
		switch r {
		case '\t':
			w += 8 - (w % 8)
		default:
			w++
		}
	}
	// Use rune count as a sanity check upper bound.
	if rc := utf8.RuneCountInString(s); rc > w {
		w = rc
	}
	return w
}

// clearStreamRegion erases the current in-place streaming region by
// moving the cursor up to the start of the region and clearing to
// end of screen. Caller must hold streamRegionMu.
//
// Cursor position assumption: after the previous renderStreamRegion,
// the cursor is at the END of the last row of the region (i.e. at
// the rightmost rendered character). To get back to the start of
// row 0 of the region, move up (rows-1) lines, then \r to col 0,
// then \033[J to clear from cursor to end of screen.
func (a *App) clearStreamRegionLocked() {
	if a.streamRegionLines == 0 {
		return
	}
	if a.streamRegionLines == 1 {
		fmt.Fprint(os.Stderr, "\r\033[K")
	} else {
		fmt.Fprintf(os.Stderr, "\033[%dA\r\033[J", a.streamRegionLines-1)
	}
	a.streamRegionLines = 0
}

// renderStreamRegion writes the partial-line buffer to stderr in
// place. It first clears the prior region, then writes the new
// content as raw text (no glamour), then updates the row count.
//
// This is called for every chunk arrival when there's a partial
// line in the buffer (i.e. content past the last \n). The user
// sees text appear word-by-word as the model generates it.
func (a *App) renderStreamRegion(buf string) {
	a.streamRegionMu.Lock()
	defer a.streamRegionMu.Unlock()
	a.clearStreamRegionLocked()
	if buf == "" {
		return
	}
	fmt.Fprint(os.Stderr, buf)
	a.streamRegionLines = countWrappedRows(buf, terminalCols())
	if a.streamRegionLines == 0 {
		a.streamRegionLines = 1
	}
}

// commitStreamRegion erases the current in-place region without
// writing anything. Used before printing committed lines through
// the normal channels (printlns, glamour-rendered lines), so the
// raw streaming text doesn't leak into the committed output.
func (a *App) commitStreamRegion() {
	a.streamRegionMu.Lock()
	defer a.streamRegionMu.Unlock()
	a.clearStreamRegionLocked()
}
