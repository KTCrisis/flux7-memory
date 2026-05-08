package transport

import (
	"context"
	"encoding/json"

	"github.com/KTCrisis/flux7-memory/internal/memory"
)

// Local is a Transport that invokes a Dispatcher directly in-process.
// It is the default transport when mem7 runs in stdio mode without a
// remote backend configured, and it is also the one HTTPServer wraps
// when mem7 runs as a backend.
type Local struct {
	dispatcher *memory.Dispatcher
}

// NewLocal wires a Local transport to a Dispatcher.
func NewLocal(d *memory.Dispatcher) *Local {
	return &Local{dispatcher: d}
}

// Call implements Transport by delegating to the underlying Dispatcher.
func (l *Local) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	return l.dispatcher.Call(ctx, method, params)
}
