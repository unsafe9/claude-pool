// Command claude-pool pools multiple Claude subscription accounts and API
// keys for Claude Code. Accounts are always preferred: `auto` picks the one
// with the most rate-limit headroom and writes its credential into the macOS
// Keychain. Only when every account's binding window is exhausted does it fall
// back to registered API keys (via cc's apiKeyHelper setting, which outranks
// the Keychain credential); as soon as any account resets, it switches back.
//
// Wire `auto` into cc hooks (StopFailure/rate_limit, SessionStart,
// UserPromptSubmit) for automatic swapping.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unsafe9/claude-pool/internal/pool"
	"golang.org/x/term"
)

// version is the build version, injected via -ldflags "-X main.version=vX.Y.Z"
// by the release workflow. Local/source builds keep "dev", which the
// session-start hook treats as a developer build and never auto-replaces.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "import":
		err = cmdImport(os.Args[2:])
	case "key":
		err = cmdKey(os.Args[2:])
	case "rm":
		err = cmdRemove(os.Args[2:])
	case "list", "ls":
		err = cmdList(os.Args[2:])
	case "switch":
		err = cmdSwitch(os.Args[2:])
	case "auto":
		err = cmdAuto(os.Args[2:])
	case "hook":
		err = cmdHook(os.Args[2:])
	case "helper":
		err = cmdHelper()
	case "status":
		err = cmdStatus()
	case "__wake":
		err = cmdWake(os.Args[2:])
	case "__selfupdate":
		err = cmdSelfUpdate(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		// NOTE: never exit 2 — cc hooks treat exit 2 as a blocking error
		// (UserPromptSubmit would block and erase the user's prompt).
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `claude-pool — pool Claude subscription accounts (+ API key fallback) for Claude Code

Usage:
  claude-pool import [--id NAME]      Save the account currently logged into cc
                                      (default name: email user, else timestamp)
  claude-pool key add [--id NAME] [KEY]
                                      Register an API key (omit KEY to read
                                      stdin; default name: timestamp)
  claude-pool rm <id>                 Remove an account or API key
  claude-pool list [--json]           Show accounts (with 5h/7d usage) and keys;
                                      --json dumps pool state (no network/secrets)
  claude-pool switch <id>             Switch to a specific account
  claude-pool auto [flags] [-- cc-args]
                                      Pick the least-used account; if every
                                      account is exhausted, fall back to API
                                      keys until one resets
        --if-needed         Only act if the current account is over --threshold
        --threshold 0.0-1.0 Binding-utilization trigger for --if-needed (default 0.8)
        --launch            Exec `+"`claude`"+` after switching (pass cc args after --)
  claude-pool hook <event>            plugin hook entry: session-start | background | stop-failure
  claude-pool helper                  apiKeyHelper hook for cc (managed by auto)
  claude-pool status                  Active auth profile as JSON (no network):
                                      {mode,name[,resets_at,reset_in_seconds]}
  claude-pool version                 Print the build version

Add accounts: log into cc with each account, then run `+"`import --id NAME`"+` each time.
`)
}

// newFlagSet uses ContinueOnError so a bad flag exits 1 via main's error path
// — flag.ExitOnError would os.Exit(2), which cc hooks interpret as a blocking
// error (UserPromptSubmit would erase the prompt).
func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

// ensureFresh refreshes a's token if it is expiring, persisting the rotated
// credential to the store under lock and — when a is the live account — to the
// Keychain, so cc is never left holding a refresh token we just consumed.
func ensureFresh(s *pool.Store, a *pool.Account) (pool.OAuthData, error) {
	od, err := pool.ParseBlob(a.Blob)
	if err != nil {
		return pool.OAuthData{}, fmt.Errorf("account %q: %w", a.ID, err)
	}
	if !pool.IsExpired(od.ExpiresAt) {
		return od, nil
	}
	nb, changed, err := pool.Refresh(a.Blob)
	if err != nil {
		return od, fmt.Errorf("account %q refresh: %w", a.ID, err)
	}
	if !changed {
		return od, nil
	}
	a.Blob = nb
	if _, err := pool.LockedUpdate(func(st *pool.Store) error {
		if e := st.Find(a.ID); e != nil {
			e.Blob = nb
		}
		// Persist the rotated blob and update the Keychain under the same lock
		// so a concurrent auto/helper can't desync them. Guard on the freshly
		// loaded st.Current (not the stale snapshot) so we don't clobber a swap
		// another process just made. The Keychain write is best-effort: the
		// rotated refresh token MUST still be saved even if it fails, or the
		// next refresh would reuse a token the server may already have rotated
		// out — a deferred Keychain write is reconciled by the next auto/harvest.
		if st.Mode != pool.ModeAPIKey && a.ID == st.Current {
			if werr := pool.WriteCredential(nb); werr != nil {
				fmt.Fprintln(os.Stderr, "claude-pool: keychain update deferred:", werr)
			}
		}
		return nil
	}); err != nil {
		return od, err
	}
	od, _ = pool.ParseBlob(nb)
	return od, nil
}

// usageFor is the one token→usage chain shared by list and auto.
func usageFor(s *pool.Store, a *pool.Account) (pool.Usage, error) {
	od, err := ensureFresh(s, a)
	if err != nil {
		return pool.Usage{}, err
	}
	return pool.FetchUsage(od.AccessToken)
}

// isOurHelper reports whether an apiKeyHelper value is one we installed —
// matched loosely because the binary path can move between runs.
func isOurHelper(cmd string) bool {
	return strings.HasSuffix(cmd, " helper") && strings.Contains(cmd, "claude-pool")
}

// reconcile repairs the store/settings pair when they have desynced: keys
// removed out from under apikey mode, our helper hand-deleted or replaced by
// the user (their edit wins), or a helper left installed by a crashed
// transition.
func reconcile(s *pool.Store) error {
	helper, err := pool.GetAPIKeyHelper()
	if err != nil {
		return err
	}
	ours := isOurHelper(helper)
	switch {
	case s.Mode == pool.ModeAPIKey && len(s.APIKeys) == 0:
		if ours {
			if err := pool.RestoreAPIKeyHelper(s.SavedHelper); err != nil {
				return err
			}
		}
		return demote(s)
	case s.Mode == pool.ModeAPIKey && !ours:
		return demote(s)
	case s.Mode != pool.ModeAPIKey && ours:
		if err := pool.RestoreAPIKeyHelper(s.SavedHelper); err != nil {
			return err
		}
		return demote(s)
	}
	return nil
}

// demote drops the store back to account mode and clears the saved helper.
func demote(s *pool.Store) error {
	return s.Update(func(st *pool.Store) error {
		if st.Mode == pool.ModeAPIKey {
			st.Mode = pool.ModeAccount
		}
		st.SavedHelper = ""
		return nil
	})
}

// harvest folds a Keychain credential that cc itself refreshed (or a manual
// /login into a known account) back into the pool before any decision
// overwrites it with a stale stored blob. Attribution is by account email via
// the profile API; an unattributable credential is left untouched.
func harvest(s *pool.Store) {
	kc, err := pool.ReadCredential()
	if err != nil || kc == "" {
		return
	}
	adopt := func(a *pool.Account) {
		_ = s.Update(func(st *pool.Store) error {
			if e := st.Find(a.ID); e != nil {
				e.Blob = kc
			}
			if st.Mode != pool.ModeAPIKey {
				st.Current = a.ID
			}
			return nil
		})
	}
	for _, a := range s.Accounts {
		if a.Blob == kc {
			if s.Mode != pool.ModeAPIKey && s.Current != a.ID {
				adopt(a)
			}
			return
		}
	}
	od, err := pool.ParseBlob(kc)
	if err != nil {
		return
	}
	email, err := pool.FetchProfile(od.AccessToken)
	if err != nil || email == "" {
		return
	}
	for _, a := range s.Accounts {
		if a.Email != "" && a.Email == email {
			adopt(a)
			return
		}
	}
}

func cmdImport(args []string) error {
	fs := newFlagSet("import")
	id := fs.String("id", "", "name for this account (default: email user / timestamp)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	blob, err := pool.ReadCredential()
	if err != nil {
		return err
	}
	if blob == "" {
		return fmt.Errorf("no Claude Code credentials found; log in with `claude` first")
	}
	od, err := pool.ParseBlob(blob)
	if err != nil {
		return fmt.Errorf("current credential is not a valid OAuth blob: %w", err)
	}
	email, err := pool.FetchProfile(od.AccessToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not fetch account identity (%v); cc-side refreshes of this account won't be auto-harvested\n", err)
	}

	name := *id
	s, err := pool.LockedUpdate(func(st *pool.Store) error {
		if name == "" {
			name = autoAccountID(st, email)
		}
		st.Upsert(&pool.Account{ID: name, Email: email, Blob: blob})
		return nil
	})
	if err != nil {
		return err
	}
	// The just-logged-in account is what the user wants active — this also
	// leaves apikey mode if we were in it.
	if err := useAccount(s, s.Find(name)); err != nil {
		return err
	}
	fmt.Printf("imported current account as %q (%d total)\n", name, len(s.Accounts))
	return nil
}

// autoAccountID names an account imported without --id: the email local-part
// when available (so re-importing the same account refreshes the same entry),
// falling back to a timestamp. A local-part claimed by a DIFFERENT account
// gets a timestamp suffix instead of silently overwriting it.
func autoAccountID(st *pool.Store, email string) string {
	ts := time.Now().Format("20060102-150405")
	if email == "" {
		return "acc-" + ts
	}
	for _, a := range st.Accounts {
		if a.Email == email {
			return a.ID
		}
	}
	name := strings.SplitN(email, "@", 2)[0]
	if a := st.Find(name); a != nil && a.Email != email {
		name += "-" + ts
	}
	return name
}

func cmdKey(args []string) error {
	if len(args) < 1 || args[0] != "add" {
		return fmt.Errorf("usage: claude-pool key add --id NAME [KEY]")
	}
	fs := newFlagSet("key add")
	id := fs.String("id", "", "name for this API key (default: timestamp)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *id == "" {
		*id = "key-" + time.Now().Format("20060102-150405")
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("expected at most one KEY argument, got %d", fs.NArg())
	}

	key := strings.TrimSpace(fs.Arg(0))
	if key == "" {
		fmt.Fprint(os.Stderr, "paste API key: ")
		if term.IsTerminal(int(os.Stdin.Fd())) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr) // ReadPassword swallows the user's Enter
			if err != nil && len(b) == 0 {
				return fmt.Errorf("read key: %w", err)
			}
			key = strings.TrimSpace(string(b))
		} else {
			line, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil && line == "" {
				return fmt.Errorf("read key: %w", err)
			}
			key = strings.TrimSpace(line)
		}
	}
	if key == "" {
		return fmt.Errorf("empty API key")
	}

	s, err := pool.LockedUpdate(func(st *pool.Store) error {
		st.UpsertKey(&pool.APIKey{ID: *id, Key: key})
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("registered API key %q (%s, %d total)\n", *id, maskKey(key), len(s.APIKeys))
	return nil
}

func cmdRemove(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: claude-pool rm <id>")
	}
	leftAPIKeyMode := false
	var saved string
	wasActiveAccount := false
	if _, err := pool.LockedUpdate(func(st *pool.Store) error {
		// Record whether we are about to remove the live Keychain account before
		// Remove clears st.Current.
		wasActiveAccount = st.Mode != pool.ModeAPIKey && st.Current == args[0]
		if !st.Remove(args[0]) {
			return fmt.Errorf("no account or API key %q (see `claude-pool list`)", args[0])
		}
		// Removing the last key while it is the active auth source would leave
		// cc wired to a helper that can only fail.
		if st.Mode == pool.ModeAPIKey && len(st.APIKeys) == 0 {
			st.Mode = pool.ModeAccount
			leftAPIKeyMode, saved = true, st.SavedHelper
			st.SavedHelper = ""
		}
		return nil
	}); err != nil {
		return err
	}
	if leftAPIKeyMode {
		if err := pool.RestoreAPIKeyHelper(saved); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "claude-pool: left API key mode (last key removed); cc will use the Keychain account")
	}
	if wasActiveAccount {
		// The removed account still owns the live Keychain blob. Switch to a
		// remaining account so cc stops authenticating as the removed one. If
		// none remain, there is nothing to switch to.
		s, err := pool.Load()
		if err != nil {
			return err
		}
		if len(s.Accounts) > 0 {
			a := s.Accounts[0]
			if err := useAccount(s, a); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "claude-pool: removed the active account; switched to %q\n", a.ID)
		}
	}
	fmt.Printf("removed %q\n", args[0])
	return nil
}

func cmdList(args []string) error {
	fs := newFlagSet("list")
	asJSON := fs.Bool("json", false, "machine-readable pool state: no usage polling, no secrets")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := pool.Load()
	if err != nil {
		return err
	}

	if *asJSON {
		type entry struct {
			ID    string `json:"id"`
			Email string `json:"email,omitempty"`
		}
		out := struct {
			Mode       string  `json:"mode"`
			Current    string  `json:"current"`
			CurrentKey string  `json:"current_key,omitempty"`
			Accounts   []entry `json:"accounts"`
			APIKeys    []entry `json:"api_keys"`
		}{
			Mode:       s.Mode,
			Current:    s.Current,
			CurrentKey: s.CurrentKey,
			Accounts:   []entry{},
			APIKeys:    []entry{},
		}
		if out.Mode == "" {
			out.Mode = pool.ModeAccount
		}
		for _, a := range s.Accounts {
			out.Accounts = append(out.Accounts, entry{ID: a.ID, Email: a.Email})
		}
		for _, k := range s.APIKeys {
			out.APIKeys = append(out.APIKeys, entry{ID: k.ID})
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}

	if len(s.Accounts) == 0 && len(s.APIKeys) == 0 {
		fmt.Println("empty pool; run `claude-pool import --id NAME` and/or `claude-pool key add --id NAME`")
		return nil
	}
	now := time.Now()
	usages, errs := pollAccounts(s, nil)
	cacheUsages(s, usages, errs)
	for i, a := range s.Accounts {
		marker := "  "
		if a.ID == s.Current && s.Mode != pool.ModeAPIKey {
			marker = "* "
		}
		if errs[i] != nil {
			fmt.Printf("%s%-16s  (error: %v)\n", marker, a.ID, errs[i])
			continue
		}
		fmt.Printf("%s%-16s  %s\n", marker, a.ID, usages[i].FormatStatusline(now))
	}
	for _, k := range s.APIKeys {
		marker := "  "
		if k.ID == s.CurrentKey && s.Mode == pool.ModeAPIKey {
			marker = "* "
		}
		fmt.Printf("%skey:%-12s  %s\n", marker, k.ID, maskKey(k.Key))
	}
	return nil
}

func cmdSwitch(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: claude-pool switch <id>")
	}
	id := args[0]
	s, err := pool.Load()
	if err != nil {
		return err
	}
	a := s.Find(id)
	if a == nil {
		return fmt.Errorf("no account %q (see `claude-pool list`)", id)
	}
	if _, err := ensureFresh(s, a); err != nil {
		return err
	}
	if err := useAccount(s, a); err != nil {
		return err
	}
	fmt.Printf("switched to %q\n", id)
	warnRunning()
	return nil
}

func cmdAuto(args []string) error {
	fs := newFlagSet("auto")
	ifNeeded := fs.Bool("if-needed", false, "only act if the current account is over --threshold")
	threshold := fs.Float64("threshold", 0.8, "binding-utilization trigger for --if-needed (0.0-1.0)")
	launch := fs.Bool("launch", false, "exec `claude` after switching")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ccArgs := fs.Args()

	s, err := pool.Load()
	if err != nil {
		return err
	}
	now := time.Now()

	// --launch always execs claude: a pool failure must not block cc from
	// starting on whatever credential it already holds.
	finish := func(err error) error {
		if *launch {
			if err != nil {
				fmt.Fprintln(os.Stderr, "claude-pool:", err)
			}
			return execClaude(ccArgs)
		}
		return err
	}

	if err := reconcile(s); err != nil {
		fmt.Fprintln(os.Stderr, "claude-pool: reconcile:", err)
	}

	// Empty pool: nothing to manage — silent no-op keeps global hooks quiet.
	if len(s.Accounts) == 0 && len(s.APIKeys) == 0 {
		return finish(nil)
	}
	// Keys-only pool: nothing to prefer, go straight to the keys.
	if len(s.Accounts) == 0 {
		if s.Mode != pool.ModeAPIKey {
			return finish(enterAPIKeyMode(s))
		}
		return finish(nil)
	}

	harvest(s)

	// Single account with no keys: nothing to switch between (harvest above
	// still keeps the stored blob in sync with cc's).
	if len(s.Accounts) == 1 && len(s.APIKeys) == 0 {
		return finish(nil)
	}

	// --if-needed fast path (the per-prompt hook case): in account mode, if
	// the current account is still under the threshold, poll only it and stay
	// put. In apikey mode there is no fast path — returning to an account the
	// moment one resets requires polling them all.
	var curUsage *pool.Usage
	if *ifNeeded && s.Mode != pool.ModeAPIKey {
		if cur := s.Find(s.Current); cur != nil {
			if u, err := usageFor(s, cur); err == nil {
				if u.Score() < *threshold*100 {
					return finish(nil)
				}
				curUsage = &u
			}
		}
	}

	// While in API-key mode the accounts aren't being used, so their utilization
	// can't change until a window resets. If every account is provably still
	// exhausted (cached and not yet past its reset), there is nothing to recover
	// yet — skip the poll entirely. Re-poll only once some reset has passed.
	// This is sound ONLY in apikey mode; in account mode the current account IS
	// being used, so its utilization rises over time — hence the fast path above
	// keeps polling it.
	if s.Mode == pool.ModeAPIKey {
		allStillExhausted := true
		for _, a := range s.Accounts {
			if !stillExhausted(a, now) {
				allStillExhausted = false
				break
			}
		}
		if allStillExhausted {
			return finish(nil)
		}
	}

	// Full pass: poll every account concurrently, reusing the fast-path poll.
	usages, errs := pollAccounts(s, curUsage)
	cacheUsages(s, usages, errs)
	best, bestScore, polled := bestAccount(s, usages, errs)

	switch {
	// Zero information: a network blip must not be mistaken for exhaustion —
	// stay on whatever credential cc already has.
	case polled == 0:
		fmt.Fprintln(os.Stderr, "claude-pool: usage unreachable for every account; keeping current credential")
		return finish(nil)

	// Some account has headroom (utilization is 0..100; 100 = exhausted):
	// accounts always win over API keys.
	case bestScore < 100:
		prev, prevMode := s.Current, s.Mode
		if err := useAccount(s, best); err != nil {
			return finish(err)
		}
		if prevMode == pool.ModeAPIKey {
			fmt.Fprintf(os.Stderr, "claude-pool: account %q has headroom again; left API key mode (%.0f%% used)\n", best.ID, bestScore)
			warnRunning()
		} else if best.ID != prev {
			fmt.Fprintf(os.Stderr, "claude-pool: switched to account %q (%.0f%% used)\n", best.ID, bestScore)
			warnRunning()
		}
		return finish(nil)

	// Every polled account exhausted → API key fallback, if any are registered.
	case len(s.APIKeys) > 0:
		if s.Mode != pool.ModeAPIKey {
			fmt.Fprintln(os.Stderr, "claude-pool: every account is exhausted")
			if err := enterAPIKeyMode(s); err != nil {
				return finish(err)
			}
			// API-key time is billed time: arrange to leave it the moment the
			// earliest account window resets, not at the next hook firing.
			scheduleRecoveryWake(usages, errs)
		}
		return finish(nil)

	default:
		return finish(fmt.Errorf("every account is exhausted and no API keys are registered (`claude-pool key add`)"))
	}
}

// pollAccounts fetches every account's usage concurrently. cur, when non-nil,
// is reused as s.Current's result instead of re-polling it.
func pollAccounts(s *pool.Store, cur *pool.Usage) ([]pool.Usage, []error) {
	usages := make([]pool.Usage, len(s.Accounts))
	errs := make([]error, len(s.Accounts))
	var wg sync.WaitGroup
	for i, a := range s.Accounts {
		if cur != nil && a.ID == s.Current {
			usages[i] = *cur
			continue
		}
		wg.Add(1)
		go func(i int, a *pool.Account) {
			defer wg.Done()
			u, err := usageFor(s, a)
			if err != nil {
				errs[i] = err
				return
			}
			usages[i] = u
		}(i, a)
	}
	wg.Wait()
	return usages, errs
}

// bestAccount picks the successfully polled account with the most headroom.
func bestAccount(s *pool.Store, usages []pool.Usage, errs []error) (best *pool.Account, bestScore float64, polled int) {
	bestScore = math.Inf(1)
	for i, a := range s.Accounts {
		if errs[i] != nil {
			continue
		}
		polled++
		if sc := usages[i].Score(); sc < bestScore {
			best, bestScore = a, sc
		}
	}
	return best, bestScore, polled
}

// scheduleRecoveryWake spawns a detached one-shot that re-runs `auto` just
// after the earliest moment an exhausted account becomes usable again, so
// leaving API-key mode does not wait for the next hook firing. Best-effort:
// skipped when no reset time is known, capped so multi-day (7d-bound) resets
// are left to the hooks and the helper's recovery probe.
func scheduleRecoveryWake(usages []pool.Usage, errs []error) {
	now := time.Now()
	var soonest time.Time
	for i, u := range usages {
		if errs[i] != nil {
			continue
		}
		t := usableAt(u)
		if t.IsZero() {
			continue
		}
		if soonest.IsZero() || t.Before(soonest) {
			soonest = t
		}
	}
	if soonest.IsZero() {
		return
	}
	delay := soonest.Sub(now) + 30*time.Second
	if delay < time.Minute {
		delay = time.Minute
	}
	if delay > 6*time.Hour {
		return
	}
	target := now.Add(delay)
	// Dedup: repeated apikey entries would otherwise pile up overlapping wakers
	// that all fire together. Skip if one is already pending at-or-before us.
	if pool.PendingWakeBefore(target) {
		return
	}
	// Spawn a detached `__wake N` that sleeps then re-runs auto. This is the
	// portable replacement for the unix-only `/bin/sh -c "sleep N; exec <exe>
	// auto"`, working on Windows too.
	if err := spawnDetached("__wake", strconv.Itoa(int(delay.Seconds()))); err != nil {
		return
	}
	pool.RecordWake(target)
	fmt.Fprintf(os.Stderr, "claude-pool: will recheck accounts around %s\n",
		target.Format("15:04"))
}

// spawnDetached re-execs this binary with args in a new session, fully detached
// so it outlives the hook process that is about to be reaped. Best-effort: the
// caller ignores the error on a hot path.
func spawnDetached(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return pool.StartDetached(exec.Command(exe, args...))
}

// stillExhausted reports whether a's cached usage PROVES it is still exhausted
// at now — i.e. we have a cached usage, it pins down a reset time, and now has
// not reached it yet. A cache miss (nil Usage) or an unknown reset (usableAt
// returns zero) yields false, so the caller falls back to polling.
func stillExhausted(a *pool.Account, now time.Time) bool {
	if a.Usage == nil {
		return false
	}
	t := usableAt(*a.Usage)
	return !t.IsZero() && now.Before(t)
}

// cacheUsages persists each successfully-polled account's usage into the store,
// keyed by ID. usages/errs are indexed parallel to s.Accounts (pollAccounts'
// alignment). It persists via LockedUpdate WITHOUT writing back into the
// caller's s: s.Update would replace s.Accounts with a fresh disk load (a
// possibly different length if another process added/removed an account), which
// would leave the subsequent bestAccount / list print loop indexing usages/errs
// out of range. The current pass already holds the live results; only future
// processes read the cache. Call on the main goroutine, after pollAccounts joins.
func cacheUsages(s *pool.Store, usages []pool.Usage, errs []error) {
	now := time.Now()
	_, _ = pool.LockedUpdate(func(st *pool.Store) error {
		for i, a := range s.Accounts {
			if errs[i] != nil {
				continue
			}
			if e := st.Find(a.ID); e != nil {
				u := usages[i]
				e.Usage = &u
				e.UsageAt = now
			}
		}
		return nil
	})
}

// usableAt is the moment u's exhausted windows have all reset — zero when the
// account is not exhausted or a reset time is unknown.
func usableAt(u pool.Usage) time.Time {
	var t time.Time
	for _, w := range []pool.Window{u.FiveHour, u.SevenDay} {
		if w.Pct < 100 {
			continue
		}
		if w.ResetsAt.IsZero() {
			return time.Time{}
		}
		if w.ResetsAt.After(t) {
			t = w.ResetsAt
		}
	}
	return t
}

// useAccount makes a the active credential, reconciling against what the
// Keychain actually holds rather than trusting the stored Current pointer, and
// leaves apikey mode (restoring any displaced foreign apiKeyHelper).
func useAccount(s *pool.Store, a *pool.Account) error {
	kc, kcErr := pool.ReadCredential()
	if s.Mode == pool.ModeAPIKey {
		if err := pool.RestoreAPIKeyHelper(s.SavedHelper); err != nil {
			return err
		}
	}
	return s.Update(func(st *pool.Store) error {
		st.Mode = pool.ModeAccount
		st.Current = a.ID
		st.SavedHelper = ""
		if e := st.Find(a.ID); e != nil {
			e.Blob = a.Blob
		}
		// Keychain write + store update are one locked unit so a concurrent
		// auto/helper cannot desync the active credential from Current.
		if kcErr != nil || kc != a.Blob {
			return pool.WriteCredential(a.Blob)
		}
		return nil
	})
}

// enterAPIKeyMode flips auth to the registered API keys. The store is saved
// BEFORE the settings write, so cc can never observe our helper while the
// on-disk mode still says account (cmdHelper would refuse to serve). The key
// announced is the one the first helper call will actually serve.
func enterAPIKeyMode(s *pool.Store) error {
	next := s.PeekNextKey()
	if next == nil {
		return fmt.Errorf("no API keys registered")
	}
	prevHelper, err := pool.GetAPIKeyHelper()
	if err != nil {
		return err
	}
	cmd, err := helperCommand()
	if err != nil {
		return err
	}
	if err := s.Update(func(st *pool.Store) error {
		st.Mode = pool.ModeAPIKey
		st.CurrentKey = next.ID
		if prevHelper != "" && !isOurHelper(prevHelper) {
			st.SavedHelper = prevHelper // foreign helper: preserve for restore
		}
		return nil
	}); err != nil {
		return err
	}
	if err := pool.SetAPIKeyHelper(cmd); err != nil {
		// The store now says apikey but no helper is installed; cc would
		// silently fall back to the exhausted Keychain account while `current`
		// reports key mode. Roll the store back to account mode before returning.
		_ = s.Update(func(st *pool.Store) error {
			st.Mode = pool.ModeAccount
			st.CurrentKey = ""
			st.SavedHelper = ""
			return nil
		})
		return err
	}
	fmt.Fprintf(os.Stderr, "claude-pool: switching to API key %q\n", next.ID)
	warnRunning()
	return nil
}

// cmdHelper implements cc's apiKeyHelper contract: print an auth value to
// stdout. It rotates the round-robin cursor on every call, so cc's periodic
// helper refresh (TTL/401/startup) spreads load across the registered keys.
// The whole read-rotate-save runs under the store lock.
func cmdHelper() error {
	var key string
	if _, err := pool.LockedUpdate(func(st *pool.Store) error {
		if st.Mode != pool.ModeAPIKey {
			return fmt.Errorf("not in API key mode")
		}
		k := st.NextKey()
		if k == nil {
			return fmt.Errorf("no API keys registered")
		}
		key = k.Key
		return nil
	}); err != nil {
		return err
	}
	// Serve the key first: cc waits for THIS process to exit, so any recovery
	// work must not block here (a full poll can take ~16s and exceed cc's
	// helper timeout). stdout is unbuffered, so this flushes to cc now.
	fmt.Println(key)
	// Cost guard: cc calls the helper exactly when it is about to spend on an
	// API key. Hand the recovery decision to a detached `auto`, which already
	// leaves apikey mode the moment an account has headroom — without blocking
	// cc on the network.
	_ = spawnDetached("auto")
	return nil
}

// cmdStatus prints the active auth profile as JSON from the store alone, no
// network — cheap enough for a statusline script to call on every render. In
// API-key mode it adds the soonest moment an account is expected to free up
// (the soonest cached window reset), so a statusline can show "back to
// subscription in 40m". That reset is read from the usage cache `auto` persists;
// when no usable reset is known (cold cache) the reset fields are omitted.
func cmdStatus() error {
	s, err := pool.Load()
	if err != nil {
		return err
	}
	out := struct {
		Mode           string `json:"mode"`
		Name           string `json:"name"`
		ResetsAt       string `json:"resets_at,omitempty"`
		ResetInSeconds *int64 `json:"reset_in_seconds,omitempty"`
	}{Mode: pool.ModeAccount, Name: s.Current}

	if s.Mode == pool.ModeAPIKey {
		out.Mode = pool.ModeAPIKey
		out.Name = s.CurrentKey
		if t := soonestRecovery(s); !t.IsZero() {
			secs := int64(time.Until(t).Seconds())
			if secs < 0 {
				secs = 0
			}
			out.ResetsAt = t.Format(time.RFC3339)
			out.ResetInSeconds = &secs
		}
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

// soonestRecovery is the earliest moment any account is expected to leave
// exhaustion, from each account's cached usage — i.e. roughly when API-key mode
// can end. Zero when no account has a known usable-at time.
func soonestRecovery(s *pool.Store) time.Time {
	var soonest time.Time
	for _, a := range s.Accounts {
		if a.Usage == nil {
			continue
		}
		t := usableAt(*a.Usage)
		if t.IsZero() {
			continue
		}
		if soonest.IsZero() || t.Before(soonest) {
			soonest = t
		}
	}
	return soonest
}

func maskKey(key string) string {
	if len(key) <= 12 {
		return "…"
	}
	return key[:7] + "…" + key[len(key)-4:]
}

func warnRunning() {
	if pids := pool.RunningSessions(); len(pids) > 0 {
		fmt.Fprintf(os.Stderr,
			"note: %d Claude Code session(s) running; restart cc to apply the swap instantly "+
				"(mid-session pickup is not guaranteed)\n", len(pids))
	}
}

