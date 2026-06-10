# claude-pool

Credential pooler for Claude Code on macOS: pools subscription accounts (+ API
key fallback) and swaps cc's active credential on rate limits. Ships as a Claude
Code plugin. macOS-only, no cgo (Keychain access is via the `security` CLI).

- Source: `cmd/claude-pool` (CLI + hook entry points), `internal/pool` (store,
  oauth, keychain, usage).
- Build/test: `make build`, `make test` (or `go build ./...` / `go test ./...`).

## Releasing

Binaries are published by `.github/workflows/release.yml` on every `v*` tag,
cross-compiled for Apple Silicon (`darwin/arm64`).

1. Bump `version` in `.claude-plugin/plugin.json` (semver, **no** `v` prefix).
2. Commit, then tag and push the matching tag:
   ```bash
   git tag v0.1.1 && git push origin v0.1.1
   ```
3. The workflow builds `claude-pool-darwin-arm64` with
   `-ldflags "-X main.version=v0.1.1"` and attaches it to a GitHub Release.

The plugin version and the release tag **must match**: the session-start hook
(`hooks/scripts/claude-pool-run.sh`) reads the installed plugin's version and
downloads the same-tagged release binary when the local one is missing or
outdated. Local/source builds report version `dev` and are never auto-replaced,
so a working tree is safe to `make install` over.
