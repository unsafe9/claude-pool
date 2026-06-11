package pool

import (
	"os"
	"path/filepath"
	"strings"
)

func defaultCredentialsFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", ".credentials.json"), nil
}

// credentialsFilePath returns ~/.claude/.credentials.json — where Claude Code
// stores its credential on non-darwin platforms. Overridable for tests.
var credentialsFilePath = defaultCredentialsFilePath

func readCredFile() (string, error) {
	path, err := credentialsFilePath()
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
	return strings.TrimSpace(string(data)), nil
}

func writeCredFile(blob string) error {
	path, err := credentialsFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(blob), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
