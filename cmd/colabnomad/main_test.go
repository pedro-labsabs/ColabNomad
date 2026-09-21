package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/app"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/config"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/control"
)

func TestStartupContextIsBoundedAndCancellable(t *testing.T) {
	ctx, cancel := boundedStartupContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > startupTimeout {
		t.Fatalf("startup context deadline = %v, ok=%v", deadline, ok)
	}
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("startup context did not cancel")
	}
}

func TestDaemonUpPassesBoundedContextToRuntime(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	observed := make(chan context.Context, 1)
	r := &app.Runtime{Config: config.RuntimeConfig{StateDir: t.TempDir()}, Compose: func(ctx context.Context, _ app.UpRequest) (*app.Composition, error) {
		observed <- ctx
		return nil, ctx.Err()
	}}
	cancelParent()
	_, _ = runDaemonUp(parent, r, app.UpRequest{})
	select {
	case ctx := <-observed:
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("daemon Up context has no deadline")
		}
		if ctx.Err() == nil {
			t.Fatal("daemon Up context did not receive parent cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not receive startup context")
	}
}

func TestDaemonRuntimeUsesOperationalDefaults(t *testing.T) {
	r := newDaemonRuntime("/tmp/task8-state")
	if r.Config.StateDir != "/tmp/task8-state" || r.Config.WorkspaceRoot != "/content/workspaces" || r.Config.OpenCodePort != 4096 || r.Config.OpenCodeGatewayPort != 4097 || r.Config.TerminalPort != 7681 || r.Config.OpenCodeTunnel != "localhostrun" || r.Config.TerminalTunnel != "cloudflare" {
		t.Fatalf("daemon config: %#v", r.Config)
	}
}

func TestControlTimeoutsMatchCommandWorkloads(t *testing.T) {
	if controlTimeout("up") < 3*time.Minute {
		t.Fatalf("up timeout too short: %s", controlTimeout("up"))
	}
	if controlTimeout("doctor") <= 30*time.Second || controlTimeout("doctor") <= controlTimeout("status") {
		t.Fatalf("doctor timeout lacks transport margin: doctor=%s status=%s", controlTimeout("doctor"), controlTimeout("status"))
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
	for _, args := range [][]string{{"status"}, {"doctor"}, {"logs", "terminal"}, {"restart", "opencode"}, {"down"}, {"gateway"}, {"up", "--repo", "https://example/repo"}, {"up", "--repo", "https://example/repo", "--opencode-tunnel", "serveo", "--terminal-tunnel", "serveo"}} {
		if _, err := parseArgs(args); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	o, err := parseArgs([]string{"up", "--repo", "https://example/repo"})
	if err != nil || o.OpenCodeTunnel != "localhostrun" || o.TerminalTunnel != "cloudflare" {
		t.Fatalf("default tunnels = %q/%q err=%v", o.OpenCodeTunnel, o.TerminalTunnel, err)
	}
	if _, err := parseArgs([]string{"up", "--repo", "x", "--opencode-tunnel", "cloudflare"}); err == nil {
		t.Error("expected opencode capability rejection")
	}
	if _, err := parseArgs([]string{"wat"}); err == nil {
		t.Error("expected unknown command rejection")
	}
}

func TestParseCLIUsesEnvironmentStateDirAndAllowsUpOverride(t *testing.T) {
	t.Setenv("COLABNOMAD_STATE_DIR", "/custom/state")
	o, err := parseArgs([]string{"status"})
	if err != nil || o.StateDir != "/custom/state" {
		t.Fatalf("status state dir = %q, %v", o.StateDir, err)
	}
	o, err = parseArgs([]string{"up", "--repo", "x", "--state-dir", "/explicit"})
	if err != nil || o.StateDir != "/explicit" {
		t.Fatalf("up state dir = %q, %v", o.StateDir, err)
	}
}

func TestParseCLIHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-h", "help"} {
		o, err := parseArgs([]string{arg})
		if err != nil || o.Command != "help" {
			t.Fatalf("%q: options=%#v err=%v", arg, o, err)
		}
	}
	usage := cliUsage()
	for _, command := range []string{"up", "status", "doctor", "logs", "restart", "down"} {
		if !strings.Contains(usage, command) {
			t.Errorf("help output omits %q: %q", command, usage)
		}
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

func TestUpPayloadCarriesPinnedManifestPath(t *testing.T) {
	t.Setenv("COLABNOMAD_VERSIONS_FILE", "/tmp/pinned-versions.json")
	o, err := parseArgs([]string{"up", "--repo", "https://example/repo"})
	if err != nil {
		t.Fatal(err)
	}
	var got app.UpRequest
	if err := json.Unmarshal(requestPayload(o), &got); err != nil {
		t.Fatal(err)
	}
	if got.VersionsPath != "/tmp/pinned-versions.json" {
		t.Fatalf("versions path = %q", got.VersionsPath)
	}
}
