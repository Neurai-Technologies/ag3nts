package context

import (
	goctx "context"
	"sync"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
)

// Recorder subscribes to the event bus and auto-captures agent events
// as chunks in a RollingStore. Low-signal events (progress, init) are
// filtered out to keep storage volume manageable.
type Recorder struct {
	store  *RollingStore
	bus    *bus.Bus
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	closed sync.Once
}

// NewRecorder creates a Recorder that will auto-append bus events to store.
func NewRecorder(rs *RollingStore, b *bus.Bus) *Recorder {
	return &Recorder{
		store: rs,
		bus:   b,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// Start spawns a goroutine that drains the bus and appends to the store.
// Returns immediately; the goroutine runs until Stop() or ctx is done.
func (rec *Recorder) Start(ctx goctx.Context) {
	rec.once.Do(func() {
		go rec.run(ctx)
	})
}

// Stop signals the goroutine to exit and waits for it to finish.
func (rec *Recorder) Stop() {
	rec.closed.Do(func() {
		close(rec.stop)
	})
	<-rec.done
}

func (rec *Recorder) run(ctx goctx.Context) {
	defer close(rec.done)

	// Subscribe to the global system topic. Wildcard would be fine too
	// but "system" already gets a copy of every published agent event
	// from orchestrator.publish().
	ch := rec.bus.SubscribeWithReplay(1024, 0, "system")

	for {
		select {
		case <-rec.stop:
			return
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			rec.handleEvent(ev)
		}
	}
}

// handleEvent converts a bus event to a Chunk and appends it, filtering
// out low-signal events to control volume.
func (rec *Recorder) handleEvent(ev bus.Event) {
	ae, ok := ev.Payload.(agent.AgentEvent)
	if !ok {
		// Non-agent payload (e.g., status string) — skip.
		return
	}

	// Filter: skip high-frequency low-signal events.
	if isLowSignal(ae.Kind) {
		return
	}

	// Skip empty content unless it's a completion/error marker.
	if ae.Content == "" && !isMarkerEvent(ae.Kind) {
		return
	}

	chunk := &Chunk{
		TaskID:    ae.TaskID,
		Agent:     ae.Agent,
		Kind:      "event_" + ae.Kind.String(),
		Content:   ae.Content,
		CreatedAt: ae.Timestamp,
	}
	_ = rec.store.Append(chunk)
}

// isLowSignal returns true for events that would flood the log without
// adding context value.
func isLowSignal(kind agent.EventKind) bool {
	switch kind {
	case agent.EventProgress, agent.EventInit:
		return true
	default:
		return false
	}
}

// isMarkerEvent returns true for events that are meaningful even without
// content (e.g., completion boundary).
func isMarkerEvent(kind agent.EventKind) bool {
	switch kind {
	case agent.EventComplete, agent.EventError:
		return true
	default:
		return false
	}
}
