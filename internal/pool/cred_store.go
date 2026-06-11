package pool

// ReadCredential returns Claude Code's current credential blob, or "" if no
// credential exists.
func ReadCredential() (string, error) { return readCredential() }

// WriteCredential updates (or creates) Claude Code's credential with blob.
func WriteCredential(blob string) error { return writeCredential(blob) }
