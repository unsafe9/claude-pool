# claude-pool

Credential pooler for Claude Code: pools subscription accounts (+ API key
fallback) and swaps cc's active credential on rate limits. Ships as a Claude
Code plugin. Supports macOS (Keychain via `security` CLI), Linux, WSL, and
Windows (plaintext `~/.claude/.credentials.json`). No cgo.

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

First-time install is automatic: SessionStart bootstrap hooks
(`hooks/scripts/bootstrap.sh` / `bootstrap.ps1`, self-selecting by OS via
exec-form `sh`/`powershell.exe` PATH resolution) fetch the binary through the
bundled installers when it is missing. Manual/immediate install:
```sh
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/unsafe9/claude-pool/main/install.sh | sh
# Windows (PowerShell)
irm https://raw.githubusercontent.com/unsafe9/claude-pool/main/install.ps1 | iex
```
