package proxy

import (
	"encoding/json"
	"net/http"
)

// NewStatusHandler returns an http.Handler that writes the current State snapshot as JSON.
func NewStatusHandler(state *State) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(state.Snapshot())
	})
}
