package tui

// layoutDimensions calculates panel sizes based on terminal dimensions.
// Layout:
//   Row 0: Status bar (1 line)
//   Row 1: Output (left, ~70%) | Sidebar (right, ~30%)
//   Row 2: Input (bottom, 3 lines)
type layoutDimensions struct {
	// Total terminal size.
	width  int
	height int

	// Output viewport (main panel).
	outputWidth  int
	outputHeight int

	// Sidebar (agents + tasks).
	sidebarWidth  int
	sidebarHeight int
	agentHeight   int // top half of sidebar
	taskHeight    int // bottom half of sidebar

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
	minSidebarWidth = 24
	sidebarRatio    = 0.30 // 30% of width
	borderSize      = 2    // top + bottom border
)

// calcLayout computes panel sizes. inputLines is the current number of
// visible lines in the textarea (1 when empty, grows as user types).
func calcLayout(width, height, inputLines int) layoutDimensions {
	d := layoutDimensions{
		width:  width,
		height: height,
	}

	// Status bar spans full width.
	d.statusWidth = width

	// Sidebar width: 30% of terminal, minimum 24 chars.
	d.sidebarWidth = int(float64(width) * sidebarRatio)
	if d.sidebarWidth < minSidebarWidth {
		d.sidebarWidth = minSidebarWidth
	}
	if d.sidebarWidth > width/2 {
		d.sidebarWidth = width / 2
	}

	// Output takes remaining width.
	d.outputWidth = width - d.sidebarWidth

	// Input height = visible lines + border.
	d.inputHeight = inputLines + borderSize
	if d.inputHeight < defaultInputH {
		d.inputHeight = defaultInputH
	}

	// Vertical space for main content area (between status bar and input).
	contentHeight := height - statusBarHeight - d.inputHeight
	if contentHeight < 4 {
		contentHeight = 4
	}

	d.outputHeight = contentHeight
	d.sidebarHeight = contentHeight

	// Split sidebar: top half for agents, bottom half for tasks.
	d.agentHeight = d.sidebarHeight / 3
	if d.agentHeight < 3 {
		d.agentHeight = 3
	}
	d.taskHeight = d.sidebarHeight - d.agentHeight

	// Input spans full width.
	d.inputWidth = width

	return d
}
