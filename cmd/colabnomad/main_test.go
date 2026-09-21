package main

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/app"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/control"
)

func TestDaemonRuntimeUsesOperationalDefaults(t *testing.T) {
	r := newDaemonRuntime("/tmp/task8-state")
	if r.Config.StateDir != "/tmp/task8-state" || r.Config.WorkspaceRoot != "/content/workspaces" || r.Config.OpenCodePort != 4096 || r.Config.TerminalPort != 7681 || r.Config.OpenCodeTunnel != "serveo" || r.Config.TerminalTunnel != "serveo" {
		t.Fatalf("daemon config: %#v", r.Config)
	}
}

func TestControlTimeoutsMatchCommandWorkloads(t *testing.T) {
	if controlTimeout("up") < 3*time.Minute {
		t.Fatalf("up timeout too short: %s", controlTimeout("up"))
	}
	if controlTimeout("doctor") <= controlTimeout("status") {
		t.Fatalf("doctor timeout must exceed status: doctor=%s status=%s", controlTimeout("doctor"), controlTimeout("status"))
	}
	for _, command := range []string{"status", "logs", "restart", "down"} {
		if controlTimeout(command) <= 0 || controlTimeout(command) >= time.Minute {
			t.Fatalf("short command timeout out of bounds: %s=%s", command, controlTimeout(command))
		}
	}
}

func TestSerializedHandlerDoesNotOverlapLifecycleRequests(t *testing.T) {
	var mu sync.Mutex
	active, maxActive := 0, 0
	h := serializedHandler(func(control.Request) control.Response {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return control.Response{OK: true}
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = h(control.Request{Command: "up"}) }()
	}
	wg.Wait()
	if maxActive != 1 {
		t.Fatalf("serialized handler overlapped %d requests", maxActive)
	}
}

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
