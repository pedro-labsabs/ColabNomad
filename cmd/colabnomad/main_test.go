package main

import "testing"

func TestParseCLI(t *testing.T) {
	for _, args := range [][]string{{"status"}, {"doctor"}, {"logs", "terminal"}, {"restart", "opencode"}, {"down"}, {"up", "--repo", "https://example/repo", "--terminal-tunnel", "cloudflare"}} {
		if _, err := parseArgs(args); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, err := parseArgs([]string{"up", "--repo", "x", "--opencode-tunnel", "cloudflare"}); err == nil {
		t.Error("expected opencode capability rejection")
	}
	if _, err := parseArgs([]string{"wat"}); err == nil {
		t.Error("expected unknown command rejection")
	}
}
