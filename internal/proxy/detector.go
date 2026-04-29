package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Decision is the result of classifying an HTTP response.
type Decision struct {
	Throttled bool
	Until     time.Time
	Reason    string // header name or error.type
}

// resetHeaders lists the header names that carry rate-limit reset times,
// in priority order (standard API-key headers first, then unified subscription headers).
var resetHeaders = []string{
	"Anthropic-Ratelimit-Requests-Reset",
	"Anthropic-Ratelimit-Tokens-Reset",
	"Anthropic-Ratelimit-Input-Tokens-Reset",
	"Anthropic-Ratelimit-Output-Tokens-Reset",
	"Anthropic-Ratelimit-Unified-5h-Reset",
	"Anthropic-Ratelimit-Unified-7d-Reset",
}

// parseResetValue tries to parse a header value first as RFC3339, then as Unix seconds.
// Returns the parsed time and true on success.
func parseResetValue(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(n, 0), true
	}
	return time.Time{}, false
}

// Classify inspects resp and returns a Decision describing whether the proxy
// should enter throttled mode and until when.
//
// Classification priority (only when status == 429):
//  1. Any reset header → throttled with parsed Until (earliest among all present)
//  2. Retry-After header → throttled with now+secs or HTTP-date
//  3. Body error.type == "rate_limit_error" or prefix "usage_limit_" → throttled with now+1h
//
// If the body is consumed, it is restored before returning.
func Classify(resp *http.Response) Decision {
	if resp.StatusCode != http.StatusTooManyRequests {
		return Decision{}
	}

	// --- 1. Reset headers ---
	var earliest time.Time
	var earliestHeader string
	for _, name := range resetHeaders {
		v := resp.Header.Get(name)
		if v == "" {
			continue
		}
		t, ok := parseResetValue(v)
		if !ok {
			continue
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
			earliestHeader = name
		}
	}
	if !earliest.IsZero() {
		return Decision{Throttled: true, Until: earliest, Reason: earliestHeader}
	}

	// --- 2. Retry-After header ---
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		ra = strings.TrimSpace(ra)
		// Try as integer seconds first.
		if secs, err := strconv.ParseInt(ra, 10, 64); err == nil {
			return Decision{
				Throttled: true,
				Until:     time.Now().Add(time.Duration(secs) * time.Second),
				Reason:    "Retry-After",
			}
		}
		// Try as HTTP-date.
		if t, err := http.ParseTime(ra); err == nil {
			return Decision{Throttled: true, Until: t, Reason: "Retry-After"}
		}
	}

	// --- 3. Body inspection ---
	if resp.Body == nil {
		return Decision{}
	}
	// Cap the read so a hostile/buggy upstream can't OOM the proxy via a giant 429 body.
	// 64 KiB is plenty for any plausible Anthropic error envelope.
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	// Always restore body regardless of read outcome.
	resp.Body = io.NopCloser(bytes.NewReader(buf))
	if err != nil {
		return Decision{}
	}

	var envelope struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if jsonErr := json.Unmarshal(buf, &envelope); jsonErr == nil {
		et := envelope.Error.Type
		if et == "rate_limit_error" || strings.HasPrefix(et, "usage_limit_") {
			return Decision{
				Throttled: true,
				Until:     time.Now().Add(1 * time.Hour),
				Reason:    et,
			}
		}
	}

	return Decision{}
}
