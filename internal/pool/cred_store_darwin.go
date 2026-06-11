//go:build darwin

package pool

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
)

// keychainService and keychainAccount identify the macOS Keychain item Claude
// Code reads its credentials from: service "Claude Code-credentials",
// account $USER.
const keychainService = "Claude Code-credentials"

func keychainAccount() string {
	// cc keys the item by the OS username; $USER can lie under sudo/launchd.
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "user"
}

// readCredential returns Claude Code's current credential blob, or "" if no item
// exists. It shells out to the macOS `security` CLI and may prompt for Keychain
// access on first use (choose "Always Allow" to avoid future prompts).
func readCredential() (string, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-a", keychainAccount(), "-s", keychainService, "-w").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 44 {
			return "", nil // 44 = item not found
		}
		return "", fmt.Errorf("security find-generic-password: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// writeCredential updates (or creates) Claude Code's credential item with blob.
//
// NOTE: the secret is passed via -w, briefly exposing it in this process's argv.
// That is acceptable for a single-user local tool and mirrors how claude-swap
// and Claude Code itself write the item.
func writeCredential(blob string) error {
	out, err := exec.Command("security", "add-generic-password",
		"-U", "-s", keychainService, "-a", keychainAccount(), "-w", blob).CombinedOutput()
	if err != nil {
		return fmt.Errorf("security add-generic-password: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
