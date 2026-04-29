# claude-code-proxy

A local Go reverse proxy for [Claude Code](https://github.com/anthropics/claude-code). It forwards your cc OAuth subscription traffic to `api.anthropic.com` unchanged, and on hitting your 5-hour / 7-day Claude Pro/Max rate limits, automatically falls back to a pre-registered Anthropic API key — only for generation endpoints, keeping every other call (token refresh, profile, usage) on OAuth so cc keeps working normally.

## How it works

All Claude Code HTTP traffic flows through `http://127.0.0.1:8787` (the proxy) on its way to `https://api.anthropic.com`. The proxy rewrites the scheme and host on every request, then returns the upstream response verbatim — with one exception: when a rate limit is detected, it begins swapping the `Authorization: Bearer` header for `X-Api-Key` on eligible paths.

The header swap is path-gated. Only paths that begin with `/v1/` and do **not** begin with `/v1/oauth/` are eligible. In practice this means `/v1/messages`, `/v1/messages/count_tokens`, and similar generation endpoints get the fallback key while OAuth-managed paths stay untouched.

The proxy drives a two-state machine. In `active` mode every request passes through with the original OAuth token. When an upstream response comes back as HTTP 429 carrying a rate-limit signal — either an `Anthropic-Ratelimit-Unified-5h-Reset` or `Anthropic-Ratelimit-Unified-7d-Reset` header (or any of the standard per-resource reset headers), a `Retry-After` header, or a body with `error.type` of `rate_limit_error` / `usage_limit_*` — the proxy transitions to `throttled` mode and records the reset deadline. Once the deadline passes the proxy recovers lazily: the next incoming request checks the clock, finds the deadline expired, and flips back to `active` automatically.

```
active ──[429 + rate-limit signal]──► throttled(until=T)
                                              │
                                              └──[first request after T]──► active
```

## Quick start

```bash
# Build the binary
make build                               # produces ./bin/claudeproxy

# Set your fallback API key (get one at console.anthropic.com)
export ANTHROPIC_FALLBACK_API_KEY=sk-ant-...

# Start the proxy (binds to 127.0.0.1:8787)
./bin/claudeproxy &

# Point Claude Code at the proxy and use it normally
ANTHROPIC_BASE_URL=http://localhost:8787 claude
```

Before using the proxy, make sure you have logged in with Claude Code's own OAuth flow at least once:

```bash
claude login
```

Your OAuth credentials are stored by cc in `~/.claude/.credentials.json` and refreshed automatically. The proxy never touches them.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `PROXY_PORT` | `8787` | Listening port (bound to `127.0.0.1` only) |
| `ANTHROPIC_FALLBACK_API_KEY` | (empty) | Empty = fallback disabled; throttle just forwards 429 |
| `UPSTREAM_URL` | `https://api.anthropic.com` | For testing/debugging with a mock upstream |

`LOG_LEVEL` is reserved for future use and currently has no effect.

## Status endpoint

The proxy exposes a read-only status endpoint that never reaches upstream:

```bash
curl -s http://localhost:8787/status | jq
```

Normal operation:

```json
{
  "mode": "active",
  "request_count": 42,
  "fallback_count": 0
}
```

During throttle (fallback active):

```json
{
  "mode": "throttled",
  "throttled_until": "2026-04-29T22:00:00Z",
  "last_trigger_at": "2026-04-29T17:30:00Z",
  "last_trigger_code": 429,
  "last_trigger_src": "Anthropic-Ratelimit-Unified-5h-Reset",
  "request_count": 158,
  "fallback_count": 12
}
```

## Header swap rule

**Swap target** — when `mode == throttled` and `ANTHROPIC_FALLBACK_API_KEY` is set, the proxy removes the `Authorization: Bearer <token>` header and sets `X-Api-Key: <fallback-key>` on any path that satisfies both conditions:

1. Path starts with `/v1/`
2. Path does **not** start with `/v1/oauth/`

This covers `/v1/messages`, `/v1/messages/count_tokens`, and any future generation endpoints under `/v1/`.

**Pass-through (no header change)** even during throttle:

- `/v1/oauth/token` — cc's OAuth token refresh; must stay Bearer or the refresh fails
- `/api/oauth/profile`, `/api/oauth/usage` — cc UI's account info and usage display; these are OAuth-only and would return 401 with an API key
- `/status` — the proxy's own status endpoint; never reaches upstream at all

## What stays normal during fallback

- **cc UI's 5h/7d usage display** — cc reads it from `/api/oauth/usage`, which the proxy never swaps headers on. The bar and countdown keep reflecting your subscription usage accurately.
- **cc OAuth token refresh** — token refreshes go to `https://platform.claude.com/v1/oauth/token` (a different domain), so they never even traverse this proxy.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| 401 from cc during fallback | API key invalid or expired | Update `ANTHROPIC_FALLBACK_API_KEY` and restart the proxy |
| Port already in use | Another process is bound to 8787 | `PROXY_PORT=9999 ./bin/claudeproxy` |
| Streaming response cut off mid-stream | Anthropic returned 429 mid-SSE | Expected — the in-flight request is dropped; cc retries on the next request, which already uses the fallback key |
| OAuth 401 in `active` mode | cc OAuth token expired | Run `claude login` to refresh |

## Limitations / Non-goals

- Multi-account API key rotation (see [KarpelesLab/teamclaude](https://github.com/KarpelesLab/teamclaude))
- Account switcher for already-logged-in cc accounts (see [realiti4/claude-swap](https://github.com/realiti4/claude-swap))
- TUI or web dashboard
- In-stream fallback swap (the proxy does not replay an in-flight request that hit 429 mid-SSE)
- Pre-emptive switch on usage threshold — the proxy reacts only to actual 429 responses
- Self-managed OAuth login or token refresh — cc handles its own OAuth lifecycle entirely
- Use as a shared or team server — this is a localhost-only, single-user tool

## Credits

This project drew on two prior implementations for validation:

- **[KarpelesLab/teamclaude](https://github.com/KarpelesLab/teamclaude)** — validated the `Authorization` Bearer → `X-Api-Key` swap pattern, the raw pass-through exception for `/v1/oauth/token`, and the `Anthropic-Ratelimit-Unified-{5h,7d}-Reset` header names.
- **[realiti4/claude-swap](https://github.com/realiti4/claude-swap)** — confirmed the `~/.claude/.credentials.json` credential location and the `/api/oauth/usage` endpoint used by the cc UI.

## License

MIT
