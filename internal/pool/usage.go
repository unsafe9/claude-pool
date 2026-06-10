package pool

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"
)

// Window is one rate-limit window's utilization and reset time.
//
// NOTE the unit: the /api/oauth/usage body reports utilization as a percentage
// (0..100) and resets_at as RFC3339 — unlike the response *headers*, which use
// a 0..1 fraction and epoch seconds.
type Window struct {
	Pct      float64
	ResetsAt time.Time
}

// Usage is the 5-hour and 7-day rate-limit state for a subscription account.
type Usage struct {
	FiveHour Window
	SevenDay Window
}

// Score is the binding utilization (0..100): the more-constrained of the two
// windows, since either reaching 100 takes the account offline.
func (u Usage) Score() float64 {
	return math.Max(u.FiveHour.Pct, u.SevenDay.Pct)
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

	var raw struct {
		FiveHour *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Usage{}, err
	}

	// A 200 with neither window would otherwise decode to Score()=0 — ranking
	// an account with UNKNOWN usage as the freshest. Treat it as an error.
	if raw.FiveHour == nil && raw.SevenDay == nil {
		return Usage{}, fmt.Errorf("usage API returned no rate-limit windows")
	}

	var u Usage
	if raw.FiveHour != nil {
		u.FiveHour = mkWindow(raw.FiveHour.Utilization, raw.FiveHour.ResetsAt)
	}
	if raw.SevenDay != nil {
		u.SevenDay = mkWindow(raw.SevenDay.Utilization, raw.SevenDay.ResetsAt)
	}
	return u, nil
}

func mkWindow(util float64, resetsAt string) Window {
	w := Window{Pct: util}
	if t, err := time.Parse(time.RFC3339, resetsAt); err == nil {
		w.ResetsAt = t
	}
	return w
}

// FormatStatusline renders the compact form "4%/4h40m 2%/6d8h": for each window
// the floored utilization percent and the time until reset.
func (u Usage) FormatStatusline(now time.Time) string {
	return fmt.Sprintf("%d%%/%s %d%%/%s",
		int(math.Floor(u.FiveHour.Pct)), shortDur(u.FiveHour.ResetsAt.Sub(now), false),
		int(math.Floor(u.SevenDay.Pct)), shortDur(u.SevenDay.ResetsAt.Sub(now), true))
}

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
