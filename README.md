# claude-pool

Credential pooler for [Claude Code](https://github.com/anthropics/claude-code) on macOS. Pools multiple Claude subscription accounts — plus Anthropic API keys as a last resort — and automatically keeps Claude Code on whichever credential has the most rate-limit headroom.

No proxy, no man-in-the-middle: Claude Code talks to `api.anthropic.com` directly. claude-pool only manages which credential it holds.

## How it works

- **Account mode** (the default, preferred state): the chosen account's OAuth credential is written into the macOS Keychain item Claude Code reads (`Claude Code-credentials`). Expiring tokens are refreshed before use.
- **Selection**: each account is scored by its *binding utilization* — `max(5-hour %, 7-day %)` from the subscription usage API (`/api/oauth/usage`). `auto` polls all accounts concurrently and activates the one with the lowest score.
- **API-key fallback**: accounts always win. Only when *every* successfully polled account sits at 100% does `auto` flip to API keys, by setting `apiKeyHelper` in `~/.claude/settings.json` — `apiKeyHelper` outranks the Keychain OAuth credential in Claude Code's documented [authentication precedence](https://code.claude.com/docs/en/authentication.md#authentication-precedence), and settings changes hot-reload into running sessions. The helper round-robins across registered keys on each invocation.
- **Recovery**: API-key time is billed time, so leaving it is aggressive. Three triggers race to get you back on subscription auth the moment any account resets below 100%:
  1. every `auto` run (hooks) re-polls all accounts while in API-key mode;
  2. the helper itself probes the accounts each time Claude Code asks it for a key — i.e. exactly when money is about to be spent — and switches back on the spot (the key it prints bridges only the in-flight request);
  3. on entering API-key mode, a detached one-shot is scheduled for the earliest known window reset (from the usage API's `resets_at`) and re-runs `auto` right after it.

```
account mode ──(every account at 100%)──▶ API-key mode
account mode ◀──(any account resets)───── API-key mode
```

- **Errors are not exhaustion**: an account whose usage poll fails is skipped, not treated as exhausted — and if every poll fails, `auto` stays on the current credential instead of dumping you onto API keys over a network blip.
- **Self-healing**: every run reconciles the store, settings, and Keychain. Hand-deleting the `apiKeyHelper` is respected. A credential that Claude Code itself refreshed in the Keychain is harvested back into the pool (attributed to the right account by email via the profile API). A foreign `apiKeyHelper` you already had is preserved and restored when claude-pool leaves API-key mode.

State lives in `~/.config/claude-pool/pool.json` (mode 0600), flock-protected against concurrent hook/helper runs.

## Installation

```bash
go install github.com/unsafe9/claude-pool/cmd/claude-pool@latest
```

Or from source:

```bash
git clone https://github.com/unsafe9/claude-pool.git
cd claude-pool
make install   # = go install ./cmd/claude-pool
```

`go install` places the binary in `$GOBIN` (default `~/go/bin`). Installing the binary by hand is optional — the plugin below bootstraps it automatically on first session start.

## Usage

### Add accounts

Log into Claude Code with each account, importing after each login:

```bash
claude-pool import                 # auto-named after the account email (or a timestamp)
# /login with the next account, then:
claude-pool import --id work       # or name it yourself
```

Importing also makes that account the active one. Re-importing the same account (same `--id`, or auto-named by the same email) refreshes the stored credential without creating a duplicate.

### Register fallback API keys (optional)

```bash
claude-pool key add                            # auto-named key-YYYYMMDD-HHMMSS, key via stdin
claude-pool key add --id console2              # paste at the prompt (input hidden)
# or non-interactively:  pbpaste | claude-pool key add --id console2
```

### Automatic switching

```bash
claude-pool auto                               # pick the least-used account / fall back / recover
claude-pool auto --if-needed --threshold 0.9   # cheap path: poll only the current account,
                                               # act only if it is past 90% (default 0.8)
claude-pool auto --launch -- --continue        # switch, then exec `claude --continue`
```

`--launch` always execs `claude` afterwards, even if the pool step failed — a pool error never blocks Claude Code from starting on whatever credential it already holds.

### Other commands

```bash
claude-pool list           # accounts with live 5h/7d usage, then keys
claude-pool switch work    # switch to a specific account
claude-pool rm console1    # remove an account or API key
claude-pool current        # active auth profile: "work", or "key:console1" in API-key mode
claude-pool helper         # apiKeyHelper hook for cc (managed by auto, not for manual use)
```

`current` is network-free, so a custom statusline script can call it on every render to show which auth profile is active — usage and reset times for the current credential are already exposed to statusline scripts by Claude Code itself (`.rate_limits`).

## Hook-driven swapping

This repo doubles as a Claude Code plugin that ships the swap hooks preconfigured. In Claude Code:

```
/plugin marketplace add unsafe9/claude-pool
/plugin install claude-pool@claude-pool
```

On the first session start the plugin bootstraps the binary with `go install` automatically (requires a Go toolchain); everything else works out of the box. The plugin wires three hooks:

- **StopFailure / rate_limit** — reactive: the turn just died on a rate limit; swap immediately so the next attempt uses a fresh credential.
- **SessionStart** — proactive: start each session on the account with the most headroom. When the pool has no accounts yet, the logged-in account is imported automatically first (the hook checks `list --json` and calls `import`).
- **UserPromptSubmit** — proactive, fire-and-forget: keeps the pool balanced mid-session without delaying the prompt.

`auto` is a silent no-op while the pool is empty, so the plugin is safe to install before importing any accounts.

## Caveats

- macOS only — credentials move through the `security` CLI and the Keychain.
- One active credential per machine: all concurrent Claude Code sessions share whatever is in the Keychain. Mid-session pickup of a swap is not guaranteed; restart Claude Code to apply it instantly.
- The first Keychain access may pop a permission prompt — choose **Always Allow** to avoid future prompts.
- Toggling API-key mode rewrites `~/.claude/settings.json`. Symlinks are resolved and preserved, but JSON key order is not.
- Running multiple consumer subscription accounts may sit against Anthropic's consumer terms of service. Use at your own risk.

## License

MIT
