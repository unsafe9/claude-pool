package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestLogger_WritesValidJSONLine(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf)

	entry := LogEntry{
		TS:        time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Method:    "POST",
		Path:      "/v1/messages",
		Status:    200,
		Mode:      ModeActive,
		LatencyMS: 123,
	}
	l.LogRequest(entry)

	output := buf.String()
	// Must end with exactly one newline
	if len(output) == 0 || output[len(output)-1] != '\n' {
		t.Fatalf("output does not end with newline: %q", output)
	}
	// Must be a single line (only one newline at the end)
	if bytes.Count([]byte(output), []byte("\n")) != 1 {
		t.Fatalf("expected exactly one newline, got: %q", output)
	}

	var got LogEntry
	if err := json.Unmarshal([]byte(output[:len(output)-1]), &got); err != nil {
		t.Fatalf("failed to parse JSON: %v\noutput: %q", err, output)
	}
	if got.Path != "/v1/messages" {
		t.Errorf("Path: got %q, want %q", got.Path, "/v1/messages")
	}
	if got.Status != 200 {
		t.Errorf("Status: got %d, want 200", got.Status)
	}
}

func TestLogger_AutoTimestamp(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf)

	before := time.Now().UTC()
	l.LogRequest(LogEntry{
		Method: "GET",
		Path:   "/health",
		Status: 200,
		Mode:   ModeThrottled,
	})
	after := time.Now().UTC()

	output := buf.String()
	var got LogEntry
	if err := json.Unmarshal([]byte(output[:len(output)-1]), &got); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if got.TS.IsZero() {
		t.Fatal("TS must not be zero when auto-set")
	}
	if got.TS.Before(before.Add(-time.Second)) || got.TS.After(after.Add(5*time.Second)) {
		t.Errorf("TS %v not within expected range [%v, %v]", got.TS, before, after.Add(5*time.Second))
	}
}

func TestLogger_ConcurrentSafe(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf)

	const goroutines = 50
	const perGoroutine = 10
	const total = goroutines * perGoroutine

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				l.LogRequest(LogEntry{
					Method:    "POST",
					Path:      "/v1/messages",
					Status:    200,
					Mode:      ModeActive,
					LatencyMS: int64(id*perGoroutine + j),
				})
			}
		}(i)
	}
	wg.Wait()

	scanner := bufio.NewScanner(&buf)
	lineCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		lineCount++
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("line %d is not valid JSON: %v\nline: %q", lineCount, err, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	if lineCount != total {
		t.Errorf("expected %d lines, got %d", total, lineCount)
	}
}
