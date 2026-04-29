package proxy

import "net/http"

// NewModifier returns an httputil.ReverseProxy-compatible ModifyResponse function
// that classifies the upstream response and transitions state into throttled mode
// when a rate limit is detected.
func NewModifier(state *State) func(*http.Response) error {
	return func(resp *http.Response) error {
		if d := Classify(resp); d.Throttled {
			state.EnterThrottled(d.Until, d.Reason, resp.StatusCode)
		}
		return nil
	}
}
