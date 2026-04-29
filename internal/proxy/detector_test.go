package proxy

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func makeResp(status int, headers map[string]string, body string) *http.Response {
	h := make(http.Header)
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestClassify_NotThrottled_When200(t *testing.T) {
	resp := makeResp(200, nil, `{"type":"message"}`)
	d := Classify(resp)
	if d.Throttled {
		t.Errorf("expected not throttled on 200, got %+v", d)
	}
}

func TestClassify_NotThrottled_When429WithoutLimitSignals(t *testing.T) {
	// 429 with overloaded_error body — not a quota/rate-limit signal
	resp := makeResp(429, nil, `{"error":{"type":"overloaded_error"}}`)
	d := Classify(resp)
	if d.Throttled {
		t.Errorf("expected not throttled for overloaded_error, got %+v", d)
	}
}

func TestClassify_Throttled_OnRateLimitHeader_RFC3339(t *testing.T) {
	future := time.Now().Add(2 * time.Hour).UTC().Round(time.Second)
	resp := makeResp(429, map[string]string{
		"Anthropic-Ratelimit-Requests-Reset": future.Format(time.RFC3339),
	}, `{}`)
	d := Classify(resp)
	if !d.Throttled {
		t.Fatalf("expected throttled, got %+v", d)
	}
	diff := d.Until.Sub(future)
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("Until %v not close to expected %v (diff %v)", d.Until, future, diff)
	}
}

func TestClassify_Throttled_OnUnifiedReset_UnixSeconds(t *testing.T) {
	future := time.Now().Add(1 * time.Hour).UTC().Round(time.Second)
	resp := makeResp(429, map[string]string{
		"Anthropic-Ratelimit-Unified-5h-Reset": fmt.Sprintf("%d", future.Unix()),
	}, `{}`)
	d := Classify(resp)
	if !d.Throttled {
		t.Fatalf("expected throttled, got %+v", d)
	}
	diff := d.Until.Sub(future)
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("Until %v not close to expected %v (diff %v)", d.Until, future, diff)
	}
}

func TestClassify_Throttled_PicksEarliestAmongMultiple(t *testing.T) {
	now := time.Now().UTC().Round(time.Second)
	earlier := now.Add(1 * time.Hour)
	later := now.Add(2 * time.Hour)
	resp := makeResp(429, map[string]string{
		"Anthropic-Ratelimit-Requests-Reset": later.Format(time.RFC3339),
		"Anthropic-Ratelimit-Tokens-Reset":   earlier.Format(time.RFC3339),
	}, `{}`)
	d := Classify(resp)
	if !d.Throttled {
		t.Fatalf("expected throttled, got %+v", d)
	}
	diff := d.Until.Sub(earlier)
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("Until %v not close to earliest %v (diff %v)", d.Until, earlier, diff)
	}
}

func TestClassify_Throttled_OnRetryAfterSeconds(t *testing.T) {
	before := time.Now()
	resp := makeResp(429, map[string]string{
		"Retry-After": "3600",
	}, `{}`)
	d := Classify(resp)
	after := time.Now()
	if !d.Throttled {
		t.Fatalf("expected throttled, got %+v", d)
	}
	lo := before.Add(3600 * time.Second)
	hi := after.Add(3600 * time.Second)
	if d.Until.Before(lo) || d.Until.After(hi) {
		t.Errorf("Until %v not in [%v, %v]", d.Until, lo, hi)
	}
}

func TestClassify_Throttled_OnRetryAfterHTTPDate(t *testing.T) {
	future := time.Now().Add(30 * time.Minute).UTC().Round(time.Second)
	httpDate := future.Format(http.TimeFormat)
	resp := makeResp(429, map[string]string{
		"Retry-After": httpDate,
	}, `{}`)
	d := Classify(resp)
	if !d.Throttled {
		t.Fatalf("expected throttled, got %+v", d)
	}
	diff := d.Until.Sub(future)
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("Until %v not close to expected %v (diff %v)", d.Until, future, diff)
	}
}

func TestClassify_Throttled_OnUsageLimitErrorBody(t *testing.T) {
	before := time.Now()
	resp := makeResp(429, nil, `{"error":{"type":"rate_limit_error"}}`)
	d := Classify(resp)
	after := time.Now()
	if !d.Throttled {
		t.Fatalf("expected throttled, got %+v", d)
	}
	lo := before.Add(1 * time.Hour)
	hi := after.Add(1 * time.Hour)
	if d.Until.Before(lo) || d.Until.After(hi) {
		t.Errorf("Until %v not in [%v, %v]", d.Until, lo, hi)
	}
}

func TestClassify_BodyRestored_AfterRead(t *testing.T) {
	body := `{"error":{"type":"rate_limit_error"}}`
	resp := makeResp(429, nil, body)
	_ = Classify(resp)
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll after Classify: %v", err)
	}
	if string(got) != body {
		t.Errorf("body not restored: got %q, want %q", string(got), body)
	}
}
