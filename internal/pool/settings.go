package pool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// settingsPath returns ~/.claude/settings.json with symlinks resolved, so the
// atomic rename below replaces the real file instead of clobbering a symlink
// (the user may keep settings in a dotfiles repo and symlink it into place).
// Overridable for tests.
var settingsPath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(home, ".claude", "settings.json")
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return p, nil
}

// SetAPIKeyHelper points the user-level apiKeyHelper setting at cmd, switching
// Claude Code's auth source to the helper: apiKeyHelper outranks the Keychain
// subscription OAuth credential in cc's documented precedence, and settings
// changes hot-reload into running sessions.
func SetAPIKeyHelper(cmd string) error {
	return editSettings(func(m map[string]any) bool {
		if cur, _ := m["apiKeyHelper"].(string); cur == cmd {
			return false
		}
		m["apiKeyHelper"] = cmd
		return true
	})
}

// RemoveAPIKeyHelper deletes the apiKeyHelper setting, handing auth back to
// the Keychain subscription credential.
func RemoveAPIKeyHelper() error {
	return editSettings(func(m map[string]any) bool {
		if _, ok := m["apiKeyHelper"]; !ok {
			return false
		}
		delete(m, "apiKeyHelper")
		return true
	})
}

// RestoreAPIKeyHelper puts back a foreign apiKeyHelper value we displaced, or
// removes the setting when there was none.
func RestoreAPIKeyHelper(prev string) error {
	if prev == "" {
		return RemoveAPIKeyHelper()
	}
	return SetAPIKeyHelper(prev)
}

// GetAPIKeyHelper returns the current apiKeyHelper setting, or "" if unset.
func GetAPIKeyHelper() (string, error) {
	path, err := settingsPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	v, _ := m["apiKeyHelper"].(string)
	return v, nil
}

// editSettings applies edit to the settings JSON object and writes it back
// atomically iff edit reports a change. NOTE: re-marshalling loses the user's
// key ordering; acceptable for a rare mode flip, and only when changed.
func editSettings(edit func(map[string]any) bool) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	m := map[string]any{}
	perm := os.FileMode(0o644)
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if info, err := os.Stat(path); err == nil {
			perm = info.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if !edit(m) {
		return nil
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
