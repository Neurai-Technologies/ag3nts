package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// Bracketed paste escape sequences.
var (
	pasteStart = []byte("\033[200~")
	pasteEnd   = []byte("\033[201~")
)

// EnableBracketedPaste tells the terminal to wrap pastes in escape sequences.
func EnableBracketedPaste() {
	fmt.Fprintf(os.Stdout, "\033[?2004h")
}

// DisableBracketedPaste turns off bracketed paste mode.
func DisableBracketedPaste() {
	fmt.Fprintf(os.Stdout, "\033[?2004l")
}

// PasteReader wraps an io.Reader (stdin) and intercepts bracketed paste
// sequences. Newlines within a paste are replaced with spaces so readline
// treats the entire paste as a single line.
// LastTerminator records how the line ended:
//
//	13 = Enter (submit), 27 = Alt+Enter (continue multi-line)
type PasteReader struct {
	r              io.Reader
	buf            []byte // look-ahead buffer for detecting escape sequences
	pasting        bool
	sawESC         bool // true if last byte of previous chunk was ESC
	LastTerminator byte // 13 = Enter, 27 = Alt+Enter
}

// NewPasteReader creates a paste-aware stdin wrapper.
func NewPasteReader(r io.Reader) *PasteReader {
	return &PasteReader{r: r}
}

// Close implements io.ReadCloser.
func (p *PasteReader) Close() error {
	if c, ok := p.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (p *PasteReader) Read(out []byte) (int, error) {
	// Read from underlying reader.
	n, err := p.r.Read(out)
	if n == 0 {
		return n, err
	}

	data := out[:n]

	// Check for paste start/end sequences and process.
	result := p.process(data)
	copy(out, result)
	return len(result), err
}

func (p *PasteReader) process(data []byte) []byte {
	var result []byte

	for len(data) > 0 {
		if !p.pasting {
			// Look for paste start sequence.
			idx := bytes.Index(data, pasteStart)
			if idx >= 0 {
				// Pass through everything before the sequence.
				result = append(result, data[:idx]...)
				data = data[idx+len(pasteStart):]
				p.pasting = true
				continue
			}
			// No paste sequence — pass through normally.
			// Detect Alt+Enter (ESC+CR = bytes 27,13) for multi-line input.
			// Strip the ESC, pass CR to readline, record LastTerminator=27.
			var filtered []byte
			for i := 0; i < len(data); i++ {
				b := data[i]
				if p.sawESC {
					p.sawESC = false
					if b == 13 {
						// Alt+Enter: ESC was already dropped, pass CR through.
						p.LastTerminator = 27
						filtered = append(filtered, 13)
						continue
					}
					// Not Alt+Enter — ESC was start of another sequence, pass both.
					filtered = append(filtered, 27, b)
					continue
				}
				if b == 27 && !p.pasting {
					// Could be start of Alt+Enter or other escape sequence.
					// Check if next byte is CR (same chunk).
					if i+1 < len(data) && data[i+1] == 13 {
						// Alt+Enter: skip ESC, next iteration handles CR.
						p.LastTerminator = 27
						filtered = append(filtered, 13)
						i++ // skip the CR too, we already added it
						continue
					}
					// ESC at end of chunk — remember it for next Read.
					if i == len(data)-1 {
						p.sawESC = true
						continue
					}
					// ESC followed by something else — pass through.
					filtered = append(filtered, b)
					continue
				}
				if b == 13 {
					p.LastTerminator = 13
				}
				filtered = append(filtered, b)
			}
			result = append(result, filtered...)
			return result
		}

		// We're inside a paste. Look for paste end sequence.
		idx := bytes.Index(data, pasteEnd)
		if idx >= 0 {
			// Replace newlines with spaces in the pasted chunk.
			chunk := data[:idx]
			chunk = bytes.ReplaceAll(chunk, []byte("\r\n"), []byte(" "))
			chunk = bytes.ReplaceAll(chunk, []byte("\n"), []byte(" "))
			chunk = bytes.ReplaceAll(chunk, []byte("\r"), []byte(" "))
			result = append(result, chunk...)
			data = data[idx+len(pasteEnd):]
			p.pasting = false
			continue
		}

		// Still pasting, no end marker yet — replace newlines and continue.
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte(" "))
		data = bytes.ReplaceAll(data, []byte("\n"), []byte(" "))
		data = bytes.ReplaceAll(data, []byte("\r"), []byte(" "))
		result = append(result, data...)
		return result
	}

	return result
}
