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
	"errors"
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
	case "config":
		err = cmdConfig(os.Args[2:])
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
  claude-pool config [SETTING [on|off]]
                                      Show settings, or set one:
        scoped-limits       Count model-scoped quotas (e.g. Fable's own weekly
                            window) toward exhaustion (default on)
  claude-pool hook <event>            plugin hook entry: session-start | background | stop-failure
  claude-pool helper                  apiKeyHelper hook for cc (managed by auto)
  claude-pool status                  Active auth profile as JSON (no network):
                                      {mode,name[,resets_at,reset_in_seconds]}
  claude-pool version                 Print the build version

Add accounts: log into cc with each account, then run `+"`import --id NAME`"+` each time.
`)
}

// ContinueOnError routes a bad flag through main's exit-1 path; ExitOnError
// would os.Exit(2), which cc reads as a blocking hook error.
func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

// ensureFresh returns a's OAuth data with a live access token, refreshing and
// persisting it when it is expiring. It updates a.Blob in place.
func ensureFresh(a *pool.Account) (pool.OAuthData, error) {
	if a.Revoked() {
		return pool.OAuthData{}, reloginError(a.ID, a.AuthError)
	}
	od, err := pool.ParseBlob(a.Blob)
	if err != nil {
		return pool.OAuthData{}, fmt.Errorf("account %q: %w", a.ID, err)
	}
	if !pool.IsExpired(od.ExpiresAt) {
		return od, nil
	}

	load := func() (string, error) { return storedBlob(a) }
	save := func(nb string) error { return persistRotation(a, nb) }
	blob, err := pool.RefreshCredential(a.ID, load, save)
	if err != nil {
		if errors.Is(err, errRelogin) {
			return pool.OAuthData{}, err
		}
		markAuthError(a, err)
		return pool.OAuthData{}, fmt.Errorf("account %q refresh: %w", a.ID, err)
	}
	a.Blob = blob
	return pool.ParseBlob(blob)
}

// storedBlob re-reads a's credential from disk, so a rotation another process
// stored while we waited for the lock wins over the snapshot in hand.
func storedBlob(a *pool.Account) (string, error) {
	cur, err := pool.Load()
	if err != nil {
		return a.Blob, nil
	}
	e := cur.Find(a.ID)
	if e == nil {
		return a.Blob, nil
	}
	if e.Revoked() {
		a.AuthError = e.AuthError
		return "", reloginError(a.ID, e.AuthError)
	}
	return e.Blob, nil
}

// persistRotation saves a rotated credential, and mirrors it to the Keychain
// when a is the live account.
func persistRotation(a *pool.Account, nb string) error {
	_, err := pool.LockedUpdate(func(st *pool.Store) error {
		if e := st.Find(a.ID); e != nil {
			e.Blob = nb
			e.AuthError = ""
		}
		// Guarding on the freshly loaded st.Current lets a swap another process
		// just made survive. The Keychain write is best-effort: the rotated
		// token must be saved even if it fails, or the next refresh reuses a
		// retired one — the next harvest reconciles the deferred write.
		if st.Mode != pool.ModeAPIKey && a.ID == st.Current {
			if werr := pool.WriteCredential(nb); werr != nil {
				fmt.Fprintln(os.Stderr, "claude-pool: credential store update deferred:", werr)
			}
		}
		return nil
	})
	return err
}

// errRelogin marks the errors that only a fresh login can clear, so callers can
// tell them apart from a refresh that merely failed.
var errRelogin = errors.New("needs re-login")

func reloginError(id, cause string) error {
	return fmt.Errorf("account %q %w: %s", id, errRelogin, cause)
}

// markAuthError records a permanently dead grant so the pool stops replaying a
// token the server will never accept again. Transient failures (network, 5xx, a
// client-level fault) are deliberately not recorded, so they keep retrying.
func markAuthError(a *pool.Account, err error) {
	var re *pool.RefreshError
	if !errors.As(err, &re) || !re.Revoked() {
		return
	}
	a.AuthError = re.Error()
	_, _ = pool.LockedUpdate(func(st *pool.Store) error {
		if e := st.Find(a.ID); e != nil {
			e.AuthError = a.AuthError
		}
		return nil
	})
}

// usageFor is the one token→usage chain shared by list and auto, and the single
// seam applying the pool's limit policy — so nothing downstream ever sees a
// model-scoped window the pool opted out of.
func usageFor(s *pool.Store, a *pool.Account) (pool.Usage, error) {
	od, err := ensureFresh(a)
	if err != nil {
		return pool.Usage{}, err
	}
	u, err := pool.FetchUsage(od.AccessToken)
	if err != nil {
		return pool.Usage{}, err
	}
	if s.IgnoreScopedLimits {
		u = u.WithoutScoped()
	}
	return u, nil
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

func demote(s *pool.Store) error {
	return s.Update(func(st *pool.Store) error {
		if st.Mode == pool.ModeAPIKey {
			st.Mode = pool.ModeAccount
		}
		st.SavedHelper = ""
		return nil
	})
}

// loadPool is how every command that touches credentials loads the store.
// Claude Code rotates the active credential in place, and that rotation retires
// the refresh token the pool holds — so folding it back before anything reads
// or overwrites a stored blob is what keeps accounts off dead grants. Doing it
// here means no caller has to remember to.
func loadPool() (*pool.Store, error) {
	s, err := pool.Load()
	if err != nil {
		return nil, err
	}
	harvest(s)
	return s, nil
}

// harvest folds a credential that cc itself refreshed (or a manual /login into
// a known account) back into the pool.
//
// Attribution is by account email via the profile API, which needs a live access
// token; a credential that expired in place is refreshed first. An
// unattributable one is left untouched rather than guessed at, since
// misattributing it would overwrite one account's credential with another's.
func harvest(s *pool.Store) {
	if len(s.Accounts) == 0 {
		return
	}
	kc, err := pool.ReadCredential()
	if err != nil || kc == "" {
		return
	}
	// Patch s in place rather than via Store.Update, which would swap in a fresh
	// disk load and detach every *Account the caller is holding.
	adopt := func(a *pool.Account) {
		_, _ = pool.LockedUpdate(func(st *pool.Store) error {
			if e := st.Find(a.ID); e != nil {
				e.Blob = kc
				e.AuthError = "" // a working credential supersedes a dead grant
			}
			if st.Mode != pool.ModeAPIKey {
				st.Current = a.ID
			}
			return nil
		})
		a.Blob, a.AuthError = kc, ""
		if s.Mode != pool.ModeAPIKey {
			s.Current = a.ID
		}
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
	if pool.IsExpired(od.ExpiresAt) {
		if kc, err = refreshCredentialStore(kc); err != nil {
			fmt.Fprintln(os.Stderr, "claude-pool: could not identify the active credential:", err)
			return
		}
		if od, err = pool.ParseBlob(kc); err != nil {
			return
		}
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

// refreshCredentialStore renews cc's own credential when it has expired in
// place, so harvest can identify it. The rotation goes straight back to the
// credential store: the refresh retired the token cc was holding, and leaving
// the old blob there would strand cc on a dead grant. Nothing can lock against
// cc itself, but this serializes claude-pool's own processes.
func refreshCredentialStore(blob string) (string, error) {
	return pool.RefreshCredential(pool.CredentialStoreLockID, func() (string, error) {
		if cur, err := pool.ReadCredential(); err == nil && cur != "" {
			return cur, nil
		}
		return blob, nil
	}, pool.WriteCredential)
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
	// The just-logged-in account is what the user wants active; this also leaves
	// apikey mode.
	if err := useAccount(s, s.Find(name)); err != nil {
		return err
	}
	fmt.Printf("imported current account as %q (%d total)\n", name, len(s.Accounts))
	return nil
}

// autoAccountID names an account imported without --id. The email local-part
// makes re-importing the same account refresh its existing entry; a local-part
// already claimed by a different account gets a timestamp suffix instead of
// silently overwriting it.
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
		fmt.Fprintln(os.Stderr, "claude-pool: left API key mode (last key removed); cc will use the stored account credential")
	}
	if wasActiveAccount {
		// The removed account still owns the live Keychain blob, so cc would go
		// on authenticating as it. pool.Load, not loadPool: that blob belongs to
		// the account just deleted, so there is nothing left to harvest it into.
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

	if *asJSON {
		// Deliberately pool.Load, not loadPool: --json is documented as
		// network-free, and harvesting reads the credential store.
		s, err := pool.Load()
		if err != nil {
			return err
		}
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

	s, err := loadPool()
	if err != nil {
		return err
	}
	if len(s.Accounts) == 0 && len(s.APIKeys) == 0 {
		fmt.Println("empty pool; run `claude-pool import --id NAME` and/or `claude-pool key add --id NAME`")
		return nil
	}
	now := time.Now()
	usages, errs := pollAccounts(s, nil)
	cacheUsages(s, usages, errs)
	var needLogin []string
	for i, a := range s.Accounts {
		marker := "  "
		if a.ID == s.Current && s.Mode != pool.ModeAPIKey {
			marker = "* "
		}
		switch {
		// A dead grant is a standing state, not a failed poll.
		case a.AuthError != "":
			needLogin = append(needLogin, a.ID)
			fmt.Printf("%s%-16s  re-login required (%s)\n", marker, a.ID, a.AuthError)
		case errs[i] != nil:
			fmt.Printf("%s%-16s  (error: %v)\n", marker, a.ID, errs[i])
		default:
			fmt.Printf("%s%-16s  %s\n", marker, a.ID, usages[i].FormatStatusline(now))
		}
	}
	for _, k := range s.APIKeys {
		marker := "  "
		if k.ID == s.CurrentKey && s.Mode == pool.ModeAPIKey {
			marker = "* "
		}
		fmt.Printf("%skey:%-12s  %s\n", marker, k.ID, maskKey(k.Key))
	}
	if len(needLogin) > 0 {
		fmt.Fprintf(os.Stderr, "\nTo repair: log into Claude Code as the account, then re-import it under the same name:\n")
		for _, id := range needLogin {
			fmt.Fprintf(os.Stderr, "  claude /login   # as %s\n  claude-pool import --id %s\n", id, id)
		}
	}
	return nil
}

func cmdSwitch(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: claude-pool switch <id>")
	}
	id := args[0]
	s, err := loadPool()
	if err != nil {
		return err
	}
	a := s.Find(id)
	if a == nil {
		return fmt.Errorf("no account %q (see `claude-pool list`)", id)
	}
	if _, err := ensureFresh(a); err != nil {
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

	s, err := loadPool()
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

	// Single account with no keys: nothing to switch between, though loadPool's
	// harvest still keeps the stored blob in sync with cc's.
	if len(s.Accounts) == 1 && len(s.APIKeys) == 0 {
		return finish(nil)
	}

	// --if-needed fast path (the per-prompt hook): poll only the current account
	// and stay put while it is under threshold. apikey mode gets no fast path —
	// returning to an account the moment one resets requires polling them all.
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

	// Idle accounts can't recover before a window resets, so a cache that still
	// proves exhaustion makes the poll pointless. Sound ONLY in apikey mode: in
	// account mode the current account is in use and its utilization rises,
	// which is why the fast path above keeps polling it.
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

	usages, errs := pollAccounts(s, curUsage)
	cacheUsages(s, usages, errs)
	best, bestScore, polled := bestAccount(s, usages, errs)

	switch {
	// A standing verdict, unlike an unreachable poll: the accounts will never
	// recover on their own, so keys are the only auth left.
	case allAuthFailed(s):
		fmt.Fprintln(os.Stderr, "claude-pool: every account needs re-login (see `claude-pool list`)")
		if len(s.APIKeys) > 0 && s.Mode != pool.ModeAPIKey {
			return finish(enterAPIKeyMode(s))
		}
		return finish(nil)

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

// allAuthFailed reports whether every account needs a fresh login. An empty
// pool is not "all revoked" — callers read this as a standing verdict.
func allAuthFailed(s *pool.Store) bool {
	if len(s.Accounts) == 0 {
		return false
	}
	for _, a := range s.Accounts {
		if !a.Revoked() {
			return false
		}
	}
	return true
}

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

// scheduleRecoveryWake re-runs `auto` just after the earliest exhausted account
// becomes usable, so leaving API-key mode does not wait for the next hook.
// Best-effort: skipped when no reset time is known, and capped so multi-day
// (7d-bound) resets fall to the hooks and the helper's recovery probe.
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
	// `__wake N` sleeps then re-runs auto, in-binary so this works on Windows
	// too.
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
// at now. A cache miss or an unknown reset yields false, so the caller falls
// back to polling rather than assuming.
func stillExhausted(a *pool.Account, now time.Time) bool {
	if a.Usage == nil {
		return false
	}
	t := usableAt(*a.Usage)
	return !t.IsZero() && now.Before(t)
}

// cacheUsages persists each successfully-polled usage, keyed by ID. usages/errs
// stay indexed parallel to s.Accounts, which is why this uses LockedUpdate and
// never s.Update: a fresh disk load could change the slice's length and leave
// the caller's print/scoring loop indexing out of range. Only future processes
// read the cache. Call on the main goroutine, after pollAccounts joins.
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
	for _, w := range u.Windows() {
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
// leaves apikey mode (restoring any displaced foreign apiKeyHelper). The store
// must already be harvested — see loadPool.
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
	// Serve the key first: cc waits for THIS process to exit, and a full poll
	// would exceed its helper timeout.
	fmt.Println(key)
	// cc calls the helper exactly when it is about to spend on a key, so this is
	// the moment to try recovering — detached, off cc's critical path.
	_ = spawnDetached("auto")
	return nil
}

func cmdConfig(args []string) error {
	if len(args) > 0 && args[0] != "scoped-limits" {
		return fmt.Errorf("unknown setting %q (known: scoped-limits)", args[0])
	}

	if len(args) < 2 {
		s, err := pool.Load()
		if err != nil {
			return err
		}
		fmt.Printf("scoped-limits  %s\n", onOff(!s.IgnoreScopedLimits))
		return nil
	}

	var ignore bool
	switch args[1] {
	case "on":
		ignore = false
	case "off":
		ignore = true
	default:
		return fmt.Errorf("scoped-limits takes on or off, not %q", args[1])
	}

	// Under the store lock, since a concurrent auto/helper may be writing the
	// usage cache in the same file.
	s, err := pool.LockedUpdate(func(st *pool.Store) error {
		st.IgnoreScopedLimits = ignore
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("scoped-limits  %s\n", onOff(!s.IgnoreScopedLimits))
	if ignore {
		fmt.Println("model-scoped quotas (e.g. Fable) no longer count toward exhaustion; only the 5-hour and 7-day windows do")
	}
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// cmdStatus prints the active auth profile as JSON from the store alone, no
// network — cheap enough for a statusline to call on every render. In API-key
// mode it adds the soonest cached window reset so a statusline can show "back
// to subscription in 40m"; a cold cache omits the reset fields.
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
