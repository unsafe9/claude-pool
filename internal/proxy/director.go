package proxy

import (
	"net/http"
	"net/url"
	"strings"
)

// ShouldSwap reports whether path is eligible for OAuth→API-key header swap.
// Only paths under /v1/ (excluding /v1/oauth/) are swappable generation endpoints.
func ShouldSwap(path string) bool {
	return strings.HasPrefix(path, "/v1/") && !strings.HasPrefix(path, "/v1/oauth/")
}

// NewDirector returns an httputil.ReverseProxy Director function that rewrites
// each request's scheme/host to upstreamURL and, when the state is throttled
// and a fallbackAPIKey is configured, swaps the Authorization header for an
// X-Api-Key header on whitelisted paths.
//
// The upstreamURL is parsed once at construction time; NewDirector panics if
// parsing fails.
func NewDirector(upstreamURL, fallbackAPIKey string, state *State) func(*http.Request) {
	parsed, err := url.Parse(upstreamURL)
	if err != nil {
		panic("proxy: NewDirector: invalid upstreamURL: " + err.Error())
	}

	return func(req *http.Request) {
		// 1. Always rewrite scheme, host, and HTTP Host header.
		req.URL.Scheme = parsed.Scheme
		req.URL.Host = parsed.Host
		req.Host = parsed.Host

		// 2. Always count the request.
		state.IncRequest()

		// 3. Read mode once and publish it for the request log so the routing
		//    decision and the log line agree, even if state flips mid-flight.
		mode := state.CurrentMode()
		recordMode(req.Context(), mode)

		// 4. Conditionally swap headers.
		if mode == ModeThrottled && fallbackAPIKey != "" && ShouldSwap(req.URL.Path) {
			req.Header.Del("Authorization")
			req.Header.Set("X-Api-Key", fallbackAPIKey)
			state.IncFallback()
		}
	}
}
