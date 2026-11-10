package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/unsafe9/claude-pool/internal/pool"
)

func TestUsableAt(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	in1h, in2h := now.Add(time.Hour), now.Add(2*time.Hour)

	// Not exhausted → zero.
	if got := usableAt(pool.Usage{
		FiveHour: &pool.Window{Pct: 80, ResetsAt: in1h},
		SevenDay: pool.Window{Pct: 50, ResetsAt: in2h},
	}); !got.IsZero() {
		t.Errorf("non-exhausted usableAt = %v, want zero", got)
	}
	// Only 5h exhausted → its reset.
	if got := usableAt(pool.Usage{
		FiveHour: &pool.Window{Pct: 100, ResetsAt: in1h},
		SevenDay: pool.Window{Pct: 50, ResetsAt: in2h},
	}); !got.Equal(in1h) {
		t.Errorf("5h-bound usableAt = %v, want %v", got, in1h)
	}
	// Both exhausted → the later reset.
	if got := usableAt(pool.Usage{
		FiveHour: &pool.Window{Pct: 100, ResetsAt: in1h},
		SevenDay: pool.Window{Pct: 100, ResetsAt: in2h},
	}); !got.Equal(in2h) {
		t.Errorf("both-bound usableAt = %v, want %v", got, in2h)
	}
	// Exhausted window with unknown reset → zero (can't schedule).
	if got := usableAt(pool.Usage{
		FiveHour: &pool.Window{Pct: 100},
		SevenDay: pool.Window{Pct: 50, ResetsAt: in2h},
	}); !got.IsZero() {
		t.Errorf("unknown-reset usableAt = %v, want zero", got)
	}
	// Only a model-scoped window exhausted → its reset.
	if got := usableAt(pool.Usage{
		FiveHour: &pool.Window{Pct: 20, ResetsAt: in1h},
		SevenDay: pool.Window{Pct: 50, ResetsAt: in1h},
		Scoped:   []pool.Window{{Pct: 100, ResetsAt: in2h, Label: "Fable"}},
	}); !got.Equal(in2h) {
		t.Errorf("scoped-bound usableAt = %v, want %v", got, in2h)
	}
}

// An Ultra account has no 5-hour window. It must rank by its 7-day and scoped
// windows alongside accounts that still have one, which keep being held out
// while their 5-hour window is spent.
func TestBestAccount_UltraWithoutFiveHour(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	week := func(resetIn time.Duration, pct float64) pool.Window {
		return pool.Window{Pct: pct, ResetsAt: now.Add(resetIn)}
	}
	fiveHourSpent := &pool.Window{Pct: 100, ResetsAt: now.Add(time.Hour)}
	fiveHourFree := &pool.Window{Pct: 10, ResetsAt: now.Add(time.Hour)}

	for _, tc := range []struct {
		name   string
		usages []pool.Usage // accounts "a" (Ultra) and "b" (with a 5h window)
		want   string
		score  float64
	}{
		{"ultra resets sooner", []pool.Usage{
			{SevenDay: week(day, 60)},
			{FiveHour: fiveHourFree, SevenDay: week(3*day, 20)},
		}, "a", 60},
		{"other resets sooner", []pool.Usage{
			{SevenDay: week(3*day, 0)},
			{FiveHour: fiveHourFree, SevenDay: week(day, 20)},
		}, "b", 20},
		{"other's 5h spent", []pool.Usage{
			{SevenDay: week(3*day, 60)},
			{FiveHour: fiveHourSpent, SevenDay: week(day, 20)},
		}, "a", 60},
		{"ultra's week spent", []pool.Usage{
			{SevenDay: week(day, 100)},
			{FiveHour: fiveHourFree, SevenDay: week(3*day, 20)},
		}, "b", 20},
		{"ultra's scoped window spent", []pool.Usage{
			{SevenDay: week(day, 10), Scoped: []pool.Window{{Pct: 100, ResetsAt: now.Add(day), Label: "Fable"}}},
			{FiveHour: fiveHourFree, SevenDay: week(3*day, 20)},
		}, "b", 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &pool.Store{Accounts: []*pool.Account{{ID: "a"}, {ID: "b"}}}
			got, score, polled := bestAccount(s, tc.usages, make([]error, 2), 100)
			if got == nil || got.ID != tc.want || score != tc.score || polled != 2 {
				t.Errorf("bestAccount = %v (score %v, polled %d), want %s (score %v, polled 2)",
					got, score, polled, tc.want, tc.score)
			}
		})
	}

	// An exhausted Ultra account is usable again when its week resets.
	if got := usableAt(pool.Usage{SevenDay: week(day, 100)}); !got.Equal(now.Add(day)) {
		t.Errorf("ultra usableAt = %v, want %v", got, now.Add(day))
	}
}

func TestSoonestRecovery(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	in1h, in3h := now.Add(time.Hour), now.Add(3*time.Hour)

	// Two exhausted accounts → the earlier reset wins; an account with no
	// cached usage is skipped, not treated as usable now.
	s := &pool.Store{Accounts: []*pool.Account{
		{ID: "a", Usage: &pool.Usage{FiveHour: &pool.Window{Pct: 100, ResetsAt: in3h}}},
		{ID: "b"},
		{ID: "c", Usage: &pool.Usage{FiveHour: &pool.Window{Pct: 100, ResetsAt: in1h}}},
	}}
	if got := soonestRecovery(s); !got.Equal(in1h) {
		t.Errorf("soonestRecovery = %v, want %v", got, in1h)
	}

	// No account has a known usable-at time → zero (reset fields omitted).
	s = &pool.Store{Accounts: []*pool.Account{
		{ID: "a", Usage: &pool.Usage{FiveHour: &pool.Window{Pct: 80, ResetsAt: in1h}}},
		{ID: "b"},
	}}
	if got := soonestRecovery(s); !got.IsZero() {
		t.Errorf("soonestRecovery with no exhausted account = %v, want zero", got)
	}
}

func TestGrantNote(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *pool.Account {
		return &pool.Account{Blob: fmt.Sprintf(
			`{"claudeAiOauth":{"accessToken":"a","refreshTokenExpiresAt":%d}}`, now.Add(d).UnixMilli())}
	}

	// Far out: no note, or the warning becomes furniture nobody reads.
	if got := grantNote(at(30*24*time.Hour), now); got != "" {
		t.Errorf("30d out = %q, want no note", got)
	}
	if got := grantNote(at(51*time.Hour), now); got != "  login expires in 2d3h" {
		t.Errorf("51h out = %q", got)
	}
	if got := grantNote(at(-time.Hour), now); got != "  login expired — re-login" {
		t.Errorf("lapsed = %q", got)
	}
	// Credentials from before the field, and blobs that do not parse, say nothing
	// rather than claiming an expiry of 1970.
	if got := grantNote(&pool.Account{Blob: `{"claudeAiOauth":{"accessToken":"a"}}`}, now); got != "" {
		t.Errorf("no deadline in blob = %q, want no note", got)
	}
	if got := grantNote(&pool.Account{Blob: "not json"}, now); got != "" {
		t.Errorf("unparseable blob = %q, want no note", got)
	}
}

func TestIsOurHelper(t *testing.T) {
	if !isOurHelper("'/Users/x/.local/bin/claude-pool' helper") {
		t.Error("quoted claude-pool helper should match")
	}
	if !isOurHelper("/Users/x/.local/bin/claude-pool helper") {
		t.Error("unquoted claude-pool helper should match")
	}
	if isOurHelper("/corp/auth.sh") {
		t.Error("foreign helper must not match")
	}
	if isOurHelper("") {
		t.Error("empty value must not match")
	}
	// Windows cmd.exe format: double-quoted path.
	if !isOurHelper(`"C:\Users\u\.local\bin\claude-pool.exe" helper`) {
		t.Error("windows double-quoted claude-pool.exe helper should match")
	}
}

func TestBestAccount(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	use := func(pct float64, weekResetIn time.Duration) pool.Usage {
		u := pool.Usage{FiveHour: &pool.Window{Pct: pct, ResetsAt: now.Add(time.Hour)}}
		if weekResetIn > 0 {
			u.SevenDay = pool.Window{Pct: pct, ResetsAt: now.Add(weekResetIn)}
		}
		return u
	}
	day := 24 * time.Hour

	for _, tc := range []struct {
		name   string
		usages []pool.Usage
		want   string
	}{
		// The account about to forfeit its quota goes first, used or not.
		{"soonest reset under ceiling", []pool.Usage{use(10, 6*day), use(60, day), use(30, 3*day)}, "b"},
		// Past the ceiling it is not a candidate, however soon it resets.
		{"over ceiling skipped", []pool.Usage{use(10, 6*day), use(85, day), use(30, 3*day)}, "c"},
		// A week that has not started has nothing at risk.
		{"unknown reset last", []pool.Usage{use(10, 0), use(60, 5*day), use(30, 0)}, "b"},
		{"tie to lower use", []pool.Usage{use(50, day), use(20, day), use(70, day)}, "b"},
		// Nothing under the ceiling: least used, so API-key fallback still reads
		// "is anything usable at all" off the score.
		{"all over ceiling", []pool.Usage{use(95, day), use(85, 6*day), use(99, 2*day)}, "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &pool.Store{Accounts: []*pool.Account{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
			best, _, polled := bestAccount(s, tc.usages, make([]error, 3), 80)
			if best == nil || best.ID != tc.want || polled != 3 {
				t.Errorf("best = %v (polled %d), want %s", best, polled, tc.want)
			}
		})
	}
}

// The fast path must hand over to a full poll exactly when bestAccount would
// pick another account, judging from the cache alone.
func TestSoonerCandidate(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	week := func(resetIn time.Duration, pct float64) *pool.Usage {
		return &pool.Usage{SevenDay: pool.Window{Pct: pct, ResetsAt: now.Add(resetIn)}}
	}
	cur := &pool.Account{ID: "cur"}
	curUsage := *week(3*day, 10)

	for _, tc := range []struct {
		name  string
		other *pool.Account
		want  bool
	}{
		{"resets sooner", &pool.Account{Usage: week(day, 50)}, true},
		{"resets later", &pool.Account{Usage: week(5*day, 0)}, false},
		{"sooner but exhausted", &pool.Account{Usage: week(day, 100)}, false},
		{"sooner, 5h spent until later", &pool.Account{Usage: &pool.Usage{
			FiveHour: &pool.Window{Pct: 100, ResetsAt: now.Add(time.Hour)},
			SevenDay: pool.Window{Pct: 50, ResetsAt: now.Add(day)},
		}}, false},
		{"sooner, 5h spent but since reset", &pool.Account{Usage: &pool.Usage{
			FiveHour: &pool.Window{Pct: 100, ResetsAt: now.Add(-time.Minute)},
			SevenDay: pool.Window{Pct: 50, ResetsAt: now.Add(day)},
		}}, true},
		// Its week rolled over since the poll; the new one has not started.
		{"cached week already reset", &pool.Account{Usage: week(-time.Hour, 50)}, false},
		{"never polled", &pool.Account{}, true},
		{"disabled", &pool.Account{Usage: week(day, 0), Disabled: true}, false},
		{"revoked", &pool.Account{Usage: week(day, 0), AuthError: "invalid_grant"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.other.ID = "other"
			s := &pool.Store{Accounts: []*pool.Account{cur, tc.other}}
			if got := soonerCandidate(s, cur, curUsage, now, 100); got != tc.want {
				t.Errorf("soonerCandidate = %v, want %v", got, tc.want)
			}
		})
	}
}

// The usage API throttles polls hard, and an account whose poll failed drops
// out of every pick. A throttled account must therefore still score from its
// cache, or auto swaps off it, and a swap back to it finds nothing usable.
// None of these cases may reach the network.
func TestUsageForThrottled(t *testing.T) {
	now := time.Now()
	cached := &pool.Usage{
		FiveHour: &pool.Window{Pct: 100, ResetsAt: now.Add(-time.Minute)}, // refilled since
		SevenDay: pool.Window{Pct: 40, ResetsAt: now.Add(48 * time.Hour)},
	}
	for _, tc := range []struct {
		name      string
		a         pool.Account
		wantScore float64
		wantErr   bool
	}{
		{"throttled with cache serves it as of now",
			pool.Account{ID: "a", Usage: cached, UsageAt: now.Add(-time.Hour), UsageRetryAt: now.Add(time.Minute)}, 40, false},
		{"recent poll is reused",
			pool.Account{ID: "a", Usage: cached, UsageAt: now.Add(-time.Second)}, 40, false},
		{"throttled without cache is unknown",
			pool.Account{ID: "a", UsageRetryAt: now.Add(time.Minute)}, 0, true},
		{"dead grant never scores from cache",
			pool.Account{ID: "a", Usage: cached, UsageAt: now, AuthError: "invalid_grant"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := usageFor(&pool.Store{}, &tc.a)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && u.Score() != tc.wantScore {
				t.Errorf("score = %v, want %v", u.Score(), tc.wantScore)
			}
		})
	}
}
