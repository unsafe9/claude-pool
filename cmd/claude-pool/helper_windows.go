//go:build windows

package main

import (
	"os"
	"strings"
)

// helperCommand returns the apiKeyHelper command line for this binary. On
// Windows cc runs the value with cmd.exe, where single quotes are literal —
// the path is double-quoted instead.
func helperCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return `"` + exe + `" helper`, nil
}

// shQuote is referenced from OS-neutral code and will be removed once the
// recovery waker stops shelling out.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
