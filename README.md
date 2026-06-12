# claude-pool

Credential pooler for [Claude Code](https://github.com/anthropics/claude-code). Pools multiple Claude subscription accounts — plus Anthropic API keys as a last resort — and automatically keeps Claude Code on whichever credential has the most rate-limit headroom. Works on macOS, Linux, WSL, and Windows (Git Bash, or plain PowerShell with a one-time manual binary install — see Caveats).

No proxy, no man-in-the-middle: Claude Code talks to `api.anthropic.com` directly. claude-pool only manages which credential it holds.

## Quick start

claude-pool ships as a Claude Code plugin — installing it is all the setup there is. In Claude Code:

```
/plugin marketplace add unsafe9/claude-pool
/plugin install claude-pool@claude-pool
```

From the next session start the plugin takes care of the rest:

- installs the `claude-pool` binary into `~/.local/bin` on first run (in the background, active the session after; on Windows this requires Git for Windows and puts the directory on your user `PATH` — restart the terminal once if hooks still report it missing), and keeps it in step with the plugin's version by self-updating whenever a plugin update outpaces it (locally built `dev` binaries are left alone);
- imports the account you are currently logged into as the pool's first account;
- from then on, hooks keep the pool balanced and swap credentials — no manual commands needed.

Three hooks do the work:

- **StopFailure / rate_limit** — reactive: the turn just died on a rate limit; swap immediately so the next attempt uses a fresh credential.
- **SessionStart** — proactive: start each session on the account with the most headroom.
- **UserPromptSubmit** — proactive, fire-and-forget: keeps the pool balanced mid-session without delaying the prompt.

`auto` is a silent no-op while the pool is empty, so the install order never matters.

To install the binary immediately instead of waiting a session (or to use the CLI without the plugin), run the installer one-liner — it targets `~/.local/bin`. On macOS/Linux that is normally already on your `PATH` (Claude Code lives there too); on Windows the installer adds it to your user `PATH` if missing (open a new terminal — and restart Claude Code — to pick it up):

```sh
# macOS / Linux / WSL
curl -fsSL https://raw.githubusercontent.com/unsafe9/claude-pool/main/install.sh | sh
```
```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/unsafe9/claude-pool/main/install.ps1 | iex
```

### Pool more accounts

One account is not much of a pool. `/login` with each additional account, then import it:

```bash
claude-pool import                 # auto-named after the account email (or a timestamp)
# /login with the next account, then:
claude-pool import --id work       # or name it yourself
```

If `claude-pool` is not on your `PATH`, it is at `~/.local/bin/claude-pool`.

Importing also makes that account the active one. Re-importing the same account (same `--id`, or auto-named by the same email) refreshes the stored credential without creating a duplicate.

### Register fallback API keys (optional)

```bash
claude-pool key add                            # auto-named key-YYYYMMDD-HHMMSS, key via stdin
claude-pool key add --id console2              # paste at the prompt (input hidden)
# or non-interactively:  pbpaste | claude-pool key add --id console2
```

## How it works

- **Account mode** (the default, preferred state): the chosen account's OAuth credential is written into Claude Code's own credential store — the macOS Keychain item `Claude Code-credentials`, or `~/.claude/.credentials.json` on Linux/WSL/Windows (the same plaintext file Claude Code itself uses there). Expiring tokens are refreshed before use.
- **Selection**: each account is scored by its *binding utilization* — `max(5-hour %, 7-day %)` from the subscription usage API (`/api/oauth/usage`). `auto` polls all accounts concurrently and activates the one with the lowest score.
- **API-key fallback**: accounts always win. Only when *every* successfully polled account sits at 100% does `auto` flip to API keys, by setting `apiKeyHelper` in `~/.claude/settings.json` — `apiKeyHelper` outranks the stored OAuth credential in Claude Code's documented [authentication precedence](https://code.claude.com/docs/en/authentication.md#authentication-precedence), and settings changes hot-reload into running sessions. The helper round-robins across registered keys on each invocation.
- **Recovery**: API-key time is billed time, so leaving it is aggressive. Three triggers race to get you back on subscription auth the moment any account resets below 100%:
  1. every `auto` run (hooks) re-polls all accounts while in API-key mode;
  2. the helper itself probes the accounts each time Claude Code asks it for a key — i.e. exactly when money is about to be spent — and switches back on the spot (the key it prints bridges only the in-flight request);
  3. on entering API-key mode, a detached one-shot is scheduled for the earliest known window reset (from the usage API's `resets_at`) and re-runs `auto` right after it.

```
account mode ──(every account at 100%)──▶ API-key mode
account mode ◀──(any account resets)───── API-key mode
```

- **Errors are not exhaustion**: an account whose usage poll fails is skipped, not treated as exhausted — and if every poll fails, `auto` stays on the current credential instead of dumping you onto API keys over a network blip.
- **Self-healing**: every run reconciles the store, settings, and credential store. Hand-deleting the `apiKeyHelper` is respected. A credential that Claude Code itself refreshed is harvested back into the pool (attributed to the right account by email via the profile API). A foreign `apiKeyHelper` you already had is preserved and restored when claude-pool leaves API-key mode.

State lives in `~/.config/claude-pool/pool.json` (mode 0600), lock-protected (flock; `LockFileEx` on Windows) against concurrent hook/helper runs. The file is encrypted at rest with machine-bound AES-256-GCM — see [Security](#security).

## Security

`pool.json` holds your pooled OAuth credentials and API keys, so it is encrypted at rest with **machine-bound AES-256-GCM** (stdlib crypto only; the key is derived via HKDF-SHA256 from the machine id and username). A pre-existing plaintext file is read transparently and re-written encrypted on the next save.

This is a deliberately narrow defense, and worth being honest about:

- **What it protects against:** automated credential scanners / info-stealers grepping known paths for `sk-ant-`, JWTs, or JSON key names, and accidental plaintext leaks (a stray git commit, screenshot, log, or backup). It also means one machine's `pool.json` is useless if copied to another machine.
- **What it does *not* protect against:** a targeted local attacker. The key derivation is open source and the tool must decrypt unattended (no passphrase, OS keychain, or biometric prompt), so anyone who can run code as you on your machine can recover the keys. Treat this as obfuscation against bulk/accidental exposure, not as strong encryption.

## CLI

The hooks drive everything through `claude-pool auto`; the same binary doubles as a CLI for inspecting and steering the pool by hand.

```bash
claude-pool list           # accounts with live 5h/7d usage, then keys
claude-pool switch work    # switch to a specific account
claude-pool rm console1    # remove an account or API key
claude-pool status         # active auth profile as JSON (no network)
claude-pool helper         # apiKeyHelper hook for cc (managed by auto, not for manual use)
claude-pool version        # build version
```

`status` is network-free, so a custom statusline script can call it on every render. It prints `{"mode","name"}` — `mode` is `account` or `apikey`, `name` is the active account or key id — and in API-key mode adds `"resets_at"` and `"reset_in_seconds"`: how long until an account is expected to free up and subscription auth resumes (read from the usage cache, omitted when unknown). For example, in a Claude Code [statusLine](https://code.claude.com/docs/en/statusline.md) script — a gray `[work]` on subscription, a red `[key:console2 40m]` billing warning while on an API key:

```sh
json=$(claude-pool status 2>/dev/null)
mode=$(printf '%s' "$json" | jq -r '.mode // empty')
name=$(printf '%s' "$json" | jq -r '.name // empty')
if [ "$mode" = "apikey" ]; then
  secs=$(printf '%s' "$json" | jq -r '.reset_in_seconds // empty')
  printf '\033[91m[key:%s%s]\033[0m' "$name" "${secs:+ $(( (secs + 59) / 60 ))m}"
elif [ -n "$name" ]; then
  printf '\033[90m[%s]\033[0m' "$name"
fi
```

Manual swapping, for use outside the hooks:

```bash
claude-pool auto                               # pick the least-used account / fall back / recover
claude-pool auto --if-needed --threshold 0.9   # cheap path: poll only the current account,
                                               # act only if it is past 90% (default 0.8)
claude-pool auto --launch -- --continue        # switch, then exec `claude --continue`
```

`--launch` always execs `claude` afterwards, even if the pool step failed — a pool error never blocks Claude Code from starting on whatever credential it already holds.

### Building from source

The install scripts above fetch a prebuilt binary; build from source if you prefer:

```bash
git clone https://github.com/unsafe9/claude-pool.git
cd claude-pool
make install   # builds into ~/.local/bin/claude-pool
```

Source builds report version `dev` and are never replaced by the plugin's self-update.

## Caveats

- One active credential per machine: all concurrent Claude Code sessions share the credential store. Mid-session pickup of a swap is not guaranteed; restart Claude Code to apply it instantly.
- On Windows, the first-run bootstrap runs through Git Bash (Claude Code resolves it from your Git for Windows install). Without Git for Windows there is no auto-install and the bootstrap hook reports a one-line error each session start — install the binary once with the PowerShell one-liner above; everything else works.
- On Linux/WSL/Windows, credentials are a plaintext `~/.claude/.credentials.json` — that is Claude Code's own storage on those platforms; claude-pool reads and writes the same file in the same format (no change to your security posture either way).
- On macOS, the first Keychain access may pop a permission prompt — choose **Always Allow** to avoid future prompts.
- Toggling API-key mode rewrites `~/.claude/settings.json`. Symlinks are resolved and preserved, but JSON key order is not.
- Running multiple consumer subscription accounts may sit against Anthropic's consumer terms of service. Use at your own risk.

## License

MIT
