//go:build !windows

package main

import "testing"

func TestShQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/x/.local/bin/claude-pool", "'/Users/x/.local/bin/claude-pool'"},
		{"/Users/J Doe/bin/claude-pool", "'/Users/J Doe/bin/claude-pool'"},
		{"/odd/it's/claude-pool", `'/odd/it'\''s/claude-pool'`},
	}
	for _, c := range cases {
		if got := shQuote(c.in); got != c.want {
			t.Errorf("shQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
