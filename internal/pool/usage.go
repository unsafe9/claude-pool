package pool

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// Window is one rate-limit window's utilization and reset time. Label names a
// model-scoped window ("Fable"); it is empty for the account-wide windows.
//
// NOTE the unit: the /api/oauth/usage body reports utilization as a percentage
// (0..100) and resets_at as RFC3339 — unlike the response *headers*, which use
// a 0..1 fraction and epoch seconds.
type Window struct {
	Pct      float64   `json:"pct"`
	ResetsAt time.Time `json:"resets_at"`
	Label    string    `json:"label,omitempty"`
}

// Usage is the rate-limit state for a subscription account: the account-wide
// 5-hour and 7-day windows, plus any model-scoped windows (e.g. Fable's own
// weekly quota), which exhaust independently of the account-wide ones.
//
// FiveHour is nil when the plan has no 5-hour window (Ultra reports five_hour
// as null or leaves it out), so an absent window never reads as an empty one.
type Usage struct {
	FiveHour *Window  `json:"five_hour,omitempty"`
	SevenDay Window   `json:"seven_day"`
	Scoped   []Window `json:"scoped,omitempty"`
}

// Windows is every window that can take the account offline.
func (u Usage) Windows() []Window {
	ws := make([]Window, 0, 2+len(u.Scoped))
	if u.FiveHour != nil {
		ws = append(ws, *u.FiveHour)
	}
	return append(append(ws, u.SevenDay), u.Scoped...)
}

// WithoutScoped drops the model-scoped windows, leaving only the account-wide
// ones — what the pool sees when it has opted out of model-scoped quotas.
func (u Usage) WithoutScoped() Usage {
	u.Scoped = nil
	return u
}

// Score is the binding utilization (0..100): the most-constrained window, since
// any one of them reaching 100 takes the account offline. A model-scoped window
// counts the same as an account-wide one — an account whose Fable quota is spent
// is treated as exhausted even when its overall quota still has headroom.
func (u Usage) Score() float64 {
	score := 0.0
	for _, w := range u.Windows() {
		score = math.Max(score, w.Pct)
	}
	return score
}

// FetchUsage queries the subscription usage API with an OAuth access token.
func FetchUsage(accessToken string) (Usage, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return Usage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", oauthBeta)

	resp, err := httpClient.Do(req)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Usage{}, fmt.Errorf("usage API: HTTP %d", resp.StatusCode)
	}
	return parseUsage(resp.Body)
}

// parseUsage decodes a /api/oauth/usage body, which reports the same state two
// ways: account-wide windows in the top-level five_hour/seven_day objects, and
// model-scoped ones in the newer `limits` array under scope.model.display_name.
// The array's unscoped entries only restate the top-level windows, and the
// per-model top-level fields are no longer populated; both are skipped.
func parseUsage(r io.Reader) (Usage, error) {
	var raw struct {
		FiveHour *rawWindow `json:"five_hour"`
		SevenDay *rawWindow `json:"seven_day"`
		Limits   []rawLimit `json:"limits"`
	}
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return Usage{}, err
	}

	var u Usage
	if raw.FiveHour != nil {
		w := mkWindow(raw.FiveHour.Utilization, raw.FiveHour.ResetsAt)
		u.FiveHour = &w
	}
	if raw.SevenDay != nil {
		u.SevenDay = mkWindow(raw.SevenDay.Utilization, raw.SevenDay.ResetsAt)
	}
	for _, l := range raw.Limits {
		if l.Scope == nil {
			continue
		}
		w := mkWindow(l.Percent, l.ResetsAt)
		w.Label = l.label()
		u.Scoped = append(u.Scoped, w)
	}

	// A 200 with no windows at all would otherwise decode to Score()=0 — ranking
	// an account with UNKNOWN usage as the freshest. Treat it as an error.
	if raw.FiveHour == nil && raw.SevenDay == nil && len(u.Scoped) == 0 {
		return Usage{}, fmt.Errorf("usage API returned no rate-limit windows")
	}
	return u, nil
}

type rawWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type rawLimit struct {
	Kind     string  `json:"kind"`
	Percent  float64 `json:"percent"`
	ResetsAt string  `json:"resets_at"`
	Scope    *struct {
		Model   *rawScopeName `json:"model"`
		Surface *rawScopeName `json:"surface"`
	} `json:"scope"`
}

type rawScopeName struct {
	DisplayName string `json:"display_name"`
}

// label names the scoped window for humans, preferring the model it applies to.
func (l rawLimit) label() string {
	if l.Scope != nil {
		if m := l.Scope.Model; m != nil && m.DisplayName != "" {
			return m.DisplayName
		}
		if s := l.Scope.Surface; s != nil && s.DisplayName != "" {
			return s.DisplayName
		}
	}
	return l.Kind
}

func mkWindow(util float64, resetsAt string) Window {
	w := Window{Pct: util}
	if t, err := time.Parse(time.RFC3339, resetsAt); err == nil {
		w.ResetsAt = t
	}
	return w
}

// FormatStatusline renders the compact form "4%/4h40m 2%/6d8h": for each window
// the floored utilization percent and the time until reset. Model-scoped
// windows follow, tagged with their model — "4%/4h40m 2%/6d8h Fable:97%/1d23h".
// A plan without a 5-hour window keeps the column, as "-".
func (u Usage) FormatStatusline(now time.Time) string {
	fiveHour := "-"
	if w := u.FiveHour; w != nil {
		fiveHour = fmt.Sprintf("%d%%/%s", int(math.Floor(w.Pct)), shortDur(w.ResetsAt.Sub(now), false))
	}
	parts := []string{
		fiveHour,
		fmt.Sprintf("%d%%/%s", int(math.Floor(u.SevenDay.Pct)), shortDur(u.SevenDay.ResetsAt.Sub(now), true)),
	}
	for _, w := range u.Scoped {
		parts = append(parts, fmt.Sprintf("%s:%d%%/%s",
			w.Label, int(math.Floor(w.Pct)), shortDur(w.ResetsAt.Sub(now), true)))
	}
	return strings.Join(parts, " ")
}

// ShortDur formats a duration the way the usage line does — "6d8h", "4h40m",
// "40m" — for callers outside this file that render a countdown next to one.
func ShortDur(d time.Duration) string { return shortDur(d, true) }

// shortDur formats a duration as "6d8h" (days mode), "4h40m", or "40m".
func shortDur(d time.Duration, days bool) string {
	if d < 0 {
		d = 0
	}
	totalMin := int(d.Minutes())
	if days {
		if dd := totalMin / 1440; dd > 0 {
			return fmt.Sprintf("%dd%dh", dd, (totalMin%1440)/60)
		}
	}
	if h := totalMin / 60; h > 0 {
		return fmt.Sprintf("%dh%dm", h, totalMin%60)
	}
	return fmt.Sprintf("%dm", totalMin)
}
