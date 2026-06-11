//go:build windows

package main

import "errors"

func execClaude(args []string) error {
	return errors.New("exec claude: not implemented on windows yet")
}
