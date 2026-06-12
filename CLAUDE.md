# claude-pool

Credential pooler for Claude Code: pools subscription accounts (+ API key
fallback) and swaps cc's active credential on rate limits. Ships as a Claude
Code plugin. Supports macOS (Keychain via `security` CLI), Linux, WSL, and
Windows (plaintext `~/.claude/.credentials.json`). No cgo.

The pool file `~/.config/claude-pool/pool.json` is encrypted at rest with
machine-bound AES-256-GCM (`internal/pool/crypto.go`; key via HKDF-SHA256 over
machine-id + username, stdlib only). This is a deliberately narrow defense:
it keeps plaintext credential patterns (`sk-ant-`, JWTs, JSON key names) off
disk against scanners/info-stealers and accidental leaks (git, screenshots,
backups), and makes one machine's ciphertext non-portable. It is **not**
protection against a targeted local attacker — the key derivation is open
source and the tool decrypts unattended (no passphrase/keychain/biometrics).
Legacy plaintext files load transparently and are re-written encrypted on the
next save.

- Source: `cmd/claude-pool` (CLI + hook entry points), `internal/pool` (store,
  oauth, keychain, usage).
- Build/test: `make build`, `make test` (or `go build ./...` / `go test ./...`).

## Releasing

Binaries are published by `.github/workflows/release.yml` (via goreleaser,
config `.goreleaser.yaml`) on every `v*` tag, for all combinations of
darwin/linux/windows × amd64/arm64. Assets are named
`claude-pool-<os>-<arch>` (`.exe` on Windows).

1. Bump `version` in `.claude-plugin/plugin.json` (semver, **no** `v` prefix).
2. Commit, then tag and push the matching tag:
   ```bash
   git tag v0.1.1 && git push origin v0.1.1
   ```

The plugin version and the release tag **must match**: the session-start hook
(`claude-pool hook session-start`) self-updates the binary when it detects a
version drift between the installed plugin and the running binary. Local/source
builds report version `dev` and are never auto-replaced, so a working tree is
safe to `make install` over.

First-time install is automatic: a string-form SessionStart bootstrap hook runs
`hooks/scripts/bootstrap.sh` (Claude Code executes string hooks with `/bin/sh`
on unix and with Git Bash on Windows, resolved from the Git install itself),
which fetches the binary through the bundled installers when it is missing —
`install.sh` on unix, `install.ps1` via `powershell.exe` on Windows. Windows
without Git Bash has no auto-bootstrap (the hook errors); install manually:
```sh
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/unsafe9/claude-pool/main/install.sh | sh
# Windows (PowerShell)
irm https://raw.githubusercontent.com/unsafe9/claude-pool/main/install.ps1 | iex
```
