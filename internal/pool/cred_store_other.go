//go:build !darwin

package pool

// readCredential and writeCredential back the credential store on Linux and
// Windows using Claude Code's plaintext JSON file (~/.claude/.credentials.json).

func readCredential() (string, error) {
	return readCredFile()
}

func writeCredential(blob string) error {
	return writeCredFile(blob)
}
