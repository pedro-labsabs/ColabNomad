package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/app"
)

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

func TestLifecyclePayloadsCarryOnlyRequestedService(t *testing.T) {
	for _, command := range []string{"logs", "restart"} {
		o, err := parseArgs([]string{command, "terminal"})
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Service string `json:"service"`
		}
		if err := json.Unmarshal(requestPayload(o), &got); err != nil {
			t.Fatal(err)
		}
		if got.Service != "terminal" {
			t.Fatalf("%s payload: %#v", command, got)
		}
	}
}

func TestUpPayloadUsesSanitizedEnvironmentSecretsWithoutArgv(t *testing.T) {
	oldA, oldG := os.Getenv("OPENCODE_API_KEY"), os.Getenv("GITHUB_TOKEN")
	defer os.Setenv("OPENCODE_API_KEY", oldA)
	defer os.Setenv("GITHUB_TOKEN", oldG)
	_ = os.Setenv("OPENCODE_API_KEY", "key\n!@#")
	_ = os.Setenv("GITHUB_TOKEN", "gh:p@ss")
	o, _ := parseArgs([]string{"up", "--repo", "https://example/repo"})
	var got app.UpRequest
	if err := json.Unmarshal(requestPayload(o), &got); err != nil {
		t.Fatal(err)
	}
	if got.OpenCodeAPIKey == "" || got.GitHubToken == "" || strings.Contains(strings.Join([]string{o.Command, o.Repo}, " "), got.GitHubToken) {
		t.Fatalf("bad request boundary: %#v", got)
	}
}
