package tui

// layoutDimensions calculates panel sizes based on terminal dimensions.
// Layout (top to bottom):
//   Row 0: Output (full width, takes remaining space)
//   Row 1: Input (full width, dynamic height)
//   Row 2: Status bar (1 line — agents, tasks, running count)
type layoutDimensions struct {
	// Total terminal size.
	width  int
	height int

	// Output viewport (full width).
	outputWidth  int
	outputHeight int

	// Input area.
	inputWidth  int
	inputHeight int

	// Status bar.
	statusWidth int
}

const (
	statusBarHeight = 1
	defaultInputH   = 3  // 1 line + border
	maxInputLines   = 10 // max lines before input scrolls
	borderSize      = 2  // top + bottom border
)

// calcLayout computes panel sizes. inputLines is the current number of
// visible lines in the textarea (1 when empty, grows as user types).
func calcLayout(width, height, inputLines int) layoutDimensions {
	d := layoutDimensions{
		width:  width,
		height: height,
	}

	// Everything spans full width.
	d.statusWidth = width
	d.outputWidth = width
	d.inputWidth = width

	// Input height = visible lines + border.
	d.inputHeight = inputLines + borderSize
	if d.inputHeight < defaultInputH {
		d.inputHeight = defaultInputH
	}

	// Output takes all remaining vertical space.
	d.outputHeight = height - d.inputHeight - statusBarHeight
	if d.outputHeight < 4 {
		d.outputHeight = 4
	}

	return d
}
