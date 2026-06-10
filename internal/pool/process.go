package pool

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

type session struct {
	PID        int    `json:"pid"`
	Kind       string `json:"kind"`
	Entrypoint string `json:"entrypoint"`
}

// RunningSessions returns the PIDs of live Claude Code sessions, read from
// ~/.claude/sessions/{pid}.json (the same files Claude Code writes). Used to
// warn before a swap, since a refresh write-back from a running session can
// clobber the swapped credential.
func RunningSessions() []int {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(home, ".claude", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s session
		if json.Unmarshal(data, &s) != nil || s.PID <= 1 {
			continue
		}
		if pidAlive(s.PID) {
			pids = append(pids, s.PID)
		}
	}
	return pids
}

func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
