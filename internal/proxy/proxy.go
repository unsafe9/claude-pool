package proxy

import (
	"context"
	"net/http"
	"net/http/httputil"
	"time"
)

// ctxKey namespaces context values stored by this package.
type ctxKey int

const modeCtxKey ctxKey = iota

// captureMode stores ptr in ctx so the Director can record the Mode it observed
// at request-decision time. The wrapping handler reads the same ptr after
// ServeHTTP returns, so the log entry reflects the routing decision instead of
// a separate (potentially racy) read of state.
func captureMode(ctx context.Context, ptr *Mode) context.Context {
	return context.WithValue(ctx, modeCtxKey, ptr)
}

// recordMode writes mode to the slot installed by captureMode. No-op if the
// context has no slot (e.g. Director invoked directly in unit tests).
func recordMode(ctx context.Context, mode Mode) {
	if ptr, ok := ctx.Value(modeCtxKey).(*Mode); ok {
		*ptr = mode
	}
}

// statusRecorder wraps http.ResponseWriter to capture the written status code.
// status stays 0 until WriteHeader is called, so the log can distinguish
// "request completed normally" from "writer was never touched" (e.g. ReverseProxy
// failed before sending headers).
// It also implements http.Flusher so SSE streaming passes through.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(c int) {
	r.status = c
	r.ResponseWriter.WriteHeader(c)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// NewHandler wires together the reverse proxy, status endpoint, and per-request
// logging into a single http.Handler.
//
// Routes:
//   - GET /status → NewStatusHandler (bypasses the reverse proxy)
//   - everything else → httputil.ReverseProxy with timing + logging
func NewHandler(upstreamURL, fallbackAPIKey string, state *State, logger *Logger) http.Handler {
	rp := &httputil.ReverseProxy{
		Director:       NewDirector(upstreamURL, fallbackAPIKey, state),
		ModifyResponse: NewModifier(state),
		Transport:      http.DefaultTransport,
	}

	mux := http.NewServeMux()

	// /status bypasses the reverse proxy entirely.
	mux.Handle("/status", NewStatusHandler(state))

	// Everything else goes through the reverse proxy, wrapped for timing and logging.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var mode Mode
		r = r.WithContext(captureMode(r.Context(), &mode))
		rec := &statusRecorder{ResponseWriter: w}
		rp.ServeHTTP(rec, r)
		logger.LogRequest(LogEntry{
			Method:    r.Method,
			Path:      r.URL.Path,
			Status:    rec.status,
			Mode:      mode,
			LatencyMS: time.Since(start).Milliseconds(),
		})
	})

	return mux
}
