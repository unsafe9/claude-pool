package pool

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// usageBody is a real /api/oauth/usage response: the per-model top-level fields
// are dead, and Fable's weekly quota shows up only as a scoped `limits` entry.
const usageBody = `{
  "five_hour": {"utilization": 18.0, "resets_at": "2026-08-16T13:09:59.977288+00:00"},
  "seven_day": {"utilization": 54.0, "resets_at": "2026-08-18T12:59:59.977310+00:00"},
  "seven_day_opus": null,
  "seven_day_sonnet": null,
  "limits": [
    {"kind": "session", "group": "session", "percent": 18, "severity": "normal",
     "resets_at": "2026-08-16T13:09:59.977288+00:00", "scope": null, "is_active": false},
    {"kind": "weekly_all", "group": "weekly", "percent": 54, "severity": "normal",
     "resets_at": "2026-08-18T12:59:59.977310+00:00", "scope": null, "is_active": false},
    {"kind": "weekly_scoped", "group": "weekly", "percent": 97, "severity": "critical",
     "resets_at": "2026-08-18T12:59:59.977511+00:00",
     "scope": {"model": {"id": null, "display_name": "Fable"}, "surface": null}, "is_active": true}
  ]
}`

func TestParseUsage_ScopedModelWindowBinds(t *testing.T) {
	u, err := parseUsage(strings.NewReader(usageBody))
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	if u.FiveHour.Pct != 18 || u.SevenDay.Pct != 54 {
		t.Errorf("account-wide windows = %v/%v, want 18/54", u.FiveHour.Pct, u.SevenDay.Pct)
	}
	// The two unscoped limits restate five_hour/seven_day and must not be kept.
	if len(u.Scoped) != 1 {
		t.Fatalf("Scoped = %+v, want exactly the Fable window", u.Scoped)
	}
	if u.Scoped[0].Label != "Fable" || u.Scoped[0].Pct != 97 {
		t.Errorf("scoped window = %+v, want Fable at 97", u.Scoped[0])
	}
	if u.Scoped[0].ResetsAt.IsZero() {
		t.Error("scoped window has no reset time")
	}
	if got := u.Score(); got != 97 {
		t.Errorf("Score = %v, want 97 (Fable's quota is the binding one)", got)
	}
}

func TestParseUsage_RejectsWindowlessResponse(t *testing.T) {
	if _, err := parseUsage(strings.NewReader(`{"five_hour": null, "seven_day": null, "limits": []}`)); err == nil {
		t.Error("parseUsage accepted a response with no windows; want an error so the account is not ranked as fresh")
	}
}

// Ultra has no 5-hour window: the API sends five_hour as null or leaves it out.
// Either way it must stay absent all the way through, not read as an empty
// window that renders "0%/0m".
func TestParseUsage_NoFiveHourWindow(t *testing.T) {
	const rest = `"seven_day": {"utilization": 54.0, "resets_at": "2026-08-18T12:59:59Z"},
	  "limits": [{"kind": "weekly_scoped", "percent": 97, "resets_at": "2026-08-18T12:59:59Z",
	    "scope": {"model": {"display_name": "Fable"}}}]}`
	now := time.Date(2026, 8, 16, 12, 59, 59, 0, time.UTC)
	for name, body := range map[string]string{
		"null":    `{"five_hour": null, ` + rest,
		"missing": `{` + rest,
	} {
		t.Run(name, func(t *testing.T) {
			u, err := parseUsage(strings.NewReader(body))
			if err != nil {
				t.Fatalf("parseUsage: %v", err)
			}
			if u.FiveHour != nil {
				t.Errorf("FiveHour = %+v, want nil", *u.FiveHour)
			}
			if n := len(u.Windows()); n != 2 {
				t.Errorf("Windows() has %d entries, want 2 (7-day and Fable)", n)
			}
			if got := u.Score(); got != 97 {
				t.Errorf("Score = %v, want 97", got)
			}
			if got, want := u.FormatStatusline(now), "- 54%/2d0h Fable:97%/2d0h"; got != want {
				t.Errorf("FormatStatusline = %q, want %q", got, want)
			}
			// The usage cache in pool.json must keep the window absent too.
			b, err := json.Marshal(u)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "five_hour") {
				t.Errorf("cached usage %s carries five_hour", b)
			}
			var back Usage
			if err := json.Unmarshal(b, &back); err != nil || back.FiveHour != nil {
				t.Errorf("cache round-trip FiveHour = %+v (err %v), want nil", back.FiveHour, err)
			}
		})
	}
}

func TestParseUsage_AcceptsLimitsOnlyResponse(t *testing.T) {
	body := `{"limits": [{"kind": "weekly_scoped", "percent": 100,
	  "resets_at": "2026-08-18T12:59:59Z", "scope": {"model": {"display_name": "Fable"}}}]}`
	u, err := parseUsage(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parseUsage: %v", err)
	}
	if u.Score() != 100 {
		t.Errorf("Score = %v, want 100", u.Score())
	}
}

func TestFormatStatusline_MatchesCompactForm(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	u := Usage{
		FiveHour: &Window{Pct: 4.9, ResetsAt: now.Add(4*time.Hour + 40*time.Minute)},
		SevenDay: Window{Pct: 2.1, ResetsAt: now.Add(6*24*time.Hour + 8*time.Hour)},
	}
	got := u.FormatStatusline(now)
	want := "4%/4h40m 2%/6d8h"
	if got != want {
		t.Errorf("FormatStatusline = %q, want %q", got, want)
	}
}

func TestFormatStatusline_TagsScopedWindows(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	u := Usage{
		FiveHour: &Window{Pct: 4.9, ResetsAt: now.Add(4*time.Hour + 40*time.Minute)},
		SevenDay: Window{Pct: 2.1, ResetsAt: now.Add(6*24*time.Hour + 8*time.Hour)},
		Scoped:   []Window{{Pct: 97, ResetsAt: now.Add(6*24*time.Hour + 8*time.Hour), Label: "Fable"}},
	}
	got := u.FormatStatusline(now)
	want := "4%/4h40m 2%/6d8h Fable:97%/6d8h"
	if got != want {
		t.Errorf("FormatStatusline = %q, want %q", got, want)
	}
}

func TestShortDur(t *testing.T) {
	cases := []struct {
		d    time.Duration
		days bool
		want string
	}{
		{4*time.Hour + 40*time.Minute, false, "4h40m"},
		{6*24*time.Hour + 8*time.Hour, true, "6d8h"},
		{40 * time.Minute, false, "40m"},
		{-time.Hour, false, "0m"},
		{20 * time.Hour, true, "20h0m"}, // <1 day in days mode falls back to h/m
		{2*time.Hour + 5*time.Minute, false, "2h5m"},
	}
	for _, c := range cases {
		if got := shortDur(c.d, c.days); got != c.want {
			t.Errorf("shortDur(%v, days=%v) = %q, want %q", c.d, c.days, got, c.want)
		}
	}
}

func TestScore_PicksBindingWindow(t *testing.T) {
	u := Usage{
		FiveHour: &Window{Pct: 30},
		SevenDay: Window{Pct: 70},
	}
	if got := u.Score(); got != 70 {
		t.Errorf("Score = %v, want 70 (the more-constrained window)", got)
	}
}
