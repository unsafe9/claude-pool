//go:build !darwin

package pool

import "errors"

func readCredential() (string, error) {
	return "", errors.New("credential store: not implemented on this platform yet")
}

func writeCredential(blob string) error {
	return errors.New("credential store: not implemented on this platform yet")
}
