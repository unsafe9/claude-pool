package proxy

import (
	"net/http"
	"net/http/httputil"
	"time"
)

// statusRecorder wraps http.ResponseWriter to capture the written status code.
// It defaults to 200 in case WriteHeader is never called explicitly.
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
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		rp.ServeHTTP(rec, r)
		logger.LogRequest(LogEntry{
			Method:    r.Method,
			Path:      r.URL.Path,
			Status:    rec.status,
			Mode:      string(state.CurrentMode()),
			LatencyMS: time.Since(start).Milliseconds(),
		})
	})

	return mux
}
