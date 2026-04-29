package proxy

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// LogEntry holds metadata for a single proxied request. It deliberately omits
// credentials, request bodies, and full headers to avoid leaking secrets.
type LogEntry struct {
	TS        time.Time `json:"ts"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	Mode      Mode      `json:"mode"`
	LatencyMS int64     `json:"latency_ms"`
}

// Logger writes one JSON object per line to w. All writes are serialized via
// mu so that concurrent calls never interleave partial output.
type Logger struct {
	w  io.Writer
	mu sync.Mutex
}

// NewLogger returns a Logger that writes JSON-Lines to w.
func NewLogger(w io.Writer) *Logger {
	return &Logger{w: w}
}

// LogRequest serializes e as a single JSON line to the underlying writer.
// If e.TS is zero it is set to time.Now().UTC() before marshalling.
func (l *Logger) LogRequest(e LogEntry) {
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}

	b, err := json.Marshal(e)
	if err != nil {
		// Should never happen with this struct; swallow silently.
		return
	}
	b = append(b, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(b)
}
