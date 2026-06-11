package pool

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCredFile_MissingFile(t *testing.T) {
	dir := t.TempDir()
	credentialsFilePath = func() (string, error) {
		return filepath.Join(dir, ".claude", ".credentials.json"), nil
	}
	t.Cleanup(func() { credentialsFilePath = defaultCredentialsFilePath })

	got, err := readCredFile()
	if err != nil {
		t.Fatalf("readCredFile: %v", err)
	}
	if got != "" {
		t.Errorf("readCredFile = %q, want empty string", got)
	}
}

func TestCredFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	credentialsFilePath = func() (string, error) {
		return filepath.Join(dir, ".claude", ".credentials.json"), nil
	}
	t.Cleanup(func() { credentialsFilePath = defaultCredentialsFilePath })

	blob := `{"claudeAiOauth":{"accessToken":"tok"}}`
	if err := writeCredFile(blob); err != nil {
		t.Fatalf("writeCredFile: %v", err)
	}

	got, err := readCredFile()
	if err != nil {
		t.Fatalf("readCredFile: %v", err)
	}
	if got != blob {
		t.Errorf("readCredFile = %q, want %q", got, blob)
	}
}

func TestCredFile_CreatesDotClaudeDir(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, ".claude", ".credentials.json")
	credentialsFilePath = func() (string, error) { return credPath, nil }
	t.Cleanup(func() { credentialsFilePath = defaultCredentialsFilePath })

	if err := writeCredFile("{}"); err != nil {
		t.Fatalf("writeCredFile: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(credPath)); err != nil {
		t.Errorf(".claude dir not created: %v", err)
	}
}

func TestCredFile_Overwrite(t *testing.T) {
	dir := t.TempDir()
	credentialsFilePath = func() (string, error) {
		return filepath.Join(dir, ".claude", ".credentials.json"), nil
	}
	t.Cleanup(func() { credentialsFilePath = defaultCredentialsFilePath })

	if err := writeCredFile("first"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeCredFile("second"); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := readCredFile()
	if err != nil {
		t.Fatalf("readCredFile: %v", err)
	}
	if got != "second" {
		t.Errorf("readCredFile = %q, want %q", got, "second")
	}
}

func TestCredFile_Mode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode check not applicable on Windows")
	}
	dir := t.TempDir()
	credPath := filepath.Join(dir, ".claude", ".credentials.json")
	credentialsFilePath = func() (string, error) { return credPath, nil }
	t.Cleanup(func() { credentialsFilePath = defaultCredentialsFilePath })

	if err := writeCredFile("{}"); err != nil {
		t.Fatalf("writeCredFile: %v", err)
	}
	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 600", perm)
	}
}
