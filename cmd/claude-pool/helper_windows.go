//go:build windows

package main

import (
	"errors"
	"strings"
)

func helperCommand() (string, error) {
	return "", errors.New("apiKeyHelper command: not implemented on windows yet")
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
