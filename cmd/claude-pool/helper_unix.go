//go:build !windows

package main

import (
	"os"
	"strings"
)

// helperCommand returns the apiKeyHelper command line for this binary. cc runs
// the value with /bin/sh, so the path must be shell-quoted.
func helperCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return shQuote(exe) + " helper", nil
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
