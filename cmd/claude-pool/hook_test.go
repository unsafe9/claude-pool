package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWantSelfUpdate(t *testing.T) {
	cases := []struct {
		name       string
		have, want string
		update     bool
	}{
		{"dev build never updates", "dev", "0.2.0", false},
		{"equal versions skip", "0.2.0", "0.2.0", false},
		{"equal across v-prefix skip", "v0.2.0", "0.2.0", false},
		{"equal both v-prefixed skip", "v0.2.0", "v0.2.0", false},
		{"differing version updates", "0.1.0", "0.2.0", true},
		{"differing across v-prefix updates", "v0.1.0", "0.2.0", true},
		{"empty want skips", "0.1.0", "", false},
		{"dev skips even when want empty", "dev", "", false},
	}
	for _, c := range cases {
		if got := wantSelfUpdate(c.have, c.want); got != c.update {
			t.Errorf("%s: wantSelfUpdate(%q, %q) = %v, want %v", c.name, c.have, c.want, got, c.update)
		}
	}
}

func TestNormVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"v0.2.0", "0.2.0"},
		{"0.2.0", "0.2.0"},
		{"dev", "dev"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normVersion(c.in); got != c.want {
			t.Errorf("normVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPluginVersion(t *testing.T) {
	// Unset env → not ok.
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")
	if _, ok := pluginVersion(); ok {
		t.Error("unset CLAUDE_PLUGIN_ROOT should report not ok")
	}

	root := t.TempDir()
	t.Setenv("CLAUDE_PLUGIN_ROOT", root)

	// Missing file → not ok.
	if _, ok := pluginVersion(); ok {
		t.Error("missing plugin.json should report not ok")
	}

	dir := filepath.Join(root, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "plugin.json")

	// Valid version → parsed.
	if err := os.WriteFile(manifest, []byte(`{"name":"claude-pool","version":"0.3.1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, ok := pluginVersion(); !ok || v != "0.3.1" {
		t.Errorf("pluginVersion = (%q, %v), want (0.3.1, true)", v, ok)
	}

	// Empty version field → not ok.
	if err := os.WriteFile(manifest, []byte(`{"name":"claude-pool","version":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := pluginVersion(); ok {
		t.Error("empty version should report not ok")
	}

	// Malformed JSON → not ok.
	if err := os.WriteFile(manifest, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := pluginVersion(); ok {
		t.Error("malformed plugin.json should report not ok")
	}
}

// TestDownloadAndReplace exercises the happy path of the unix replaceExecutable:
// a served body lands atomically over a stand-in exe in a temp dir.
func TestDownloadAndReplace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows replaceExecutable renames a live exe; not exercised here")
	}
	const body = "#!/bin/sh\necho new-binary\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dir := t.TempDir()
	exe := filepath.Join(dir, "claude-pool")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := downloadAndReplace(srv.URL, exe); err != nil {
		t.Fatalf("downloadAndReplace: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("replaced exe = %q, want %q", got, body)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("replaced exe not executable: mode %v", info.Mode())
	}

	// No stray temp files left behind on success.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp dir has %d entries, want 1 (the exe)", len(entries))
	}
}

// TestDownloadAndReplaceNon200 confirms a non-200 leaves no temp file behind.
func TestDownloadAndReplaceNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	exe := filepath.Join(dir, "claude-pool")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := downloadAndReplace(srv.URL, exe); err == nil {
		t.Fatal("downloadAndReplace with 404 should error")
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old-binary" {
		t.Errorf("exe should be untouched on failure, got %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp dir has %d entries, want 1 (failed download must clean up)", len(entries))
	}
}
