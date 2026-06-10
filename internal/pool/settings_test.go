package pool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func withSettingsAt(t *testing.T, path string) {
	t.Helper()
	old := settingsPath
	settingsPath = func() (string, error) {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved, nil
		}
		return path, nil
	}
	t.Cleanup(func() { settingsPath = old })
}

func TestSetRemoveAPIKeyHelper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte(`{"model":"opus","hooks":{"Stop":[]}}`), 0o644)
	withSettingsAt(t, path)

	if err := SetAPIKeyHelper("/bin/helper key"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var m map[string]any
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &m)
	if m["apiKeyHelper"] != "/bin/helper key" {
		t.Fatalf("apiKeyHelper = %v", m["apiKeyHelper"])
	}
	if m["model"] != "opus" {
		t.Errorf("existing keys lost: %v", m)
	}

	if err := RemoveAPIKeyHelper(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	data, _ = os.ReadFile(path)
	m = nil
	json.Unmarshal(data, &m)
	if _, ok := m["apiKeyHelper"]; ok {
		t.Errorf("apiKeyHelper not removed: %v", m)
	}
	if _, ok := m["hooks"]; !ok {
		t.Errorf("hooks lost on remove: %v", m)
	}
}

func TestEditSettingsPreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	link := filepath.Join(dir, "settings.json")
	os.WriteFile(real, []byte(`{}`), 0o644)
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	withSettingsAt(t, link)

	if err := SetAPIKeyHelper("x"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink replaced by regular file")
	}
	data, _ := os.ReadFile(real)
	if string(data) == "{}" {
		t.Errorf("real target not updated: %s", data)
	}
}

func TestEditSettingsNoChangeNoWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte(`{"apiKeyHelper":"x","custom":1}`), 0o644)
	withSettingsAt(t, path)

	if err := SetAPIKeyHelper("x"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != `{"apiKeyHelper":"x","custom":1}` {
		t.Errorf("file rewritten without a change: %s", data)
	}
}
