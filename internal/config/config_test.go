package config

import "testing"

func TestDefaultConfigUsesBrowserCompatibleTunnels(t *testing.T) {
	got := Default("https://github.com/example/project.git")
	if got.OpenCodeTunnel != TunnelLocalhostRun || got.TerminalTunnel != TunnelCloudflare {
		t.Fatal(got)
	}
	if got.OpenCodePort != 4096 || got.OpenCodeGatewayPort != 4097 || got.TerminalPort != 7681 {
		t.Fatal(got)
	}
	if got.StateDir != "/content/.colabnomad" || got.WorkspaceRoot != "/content/workspaces" {
		t.Fatalf("unexpected paths: %+v", got)
	}
	if got.RepoRef != "" {
		t.Fatalf("expected optional repo ref to be empty, got %q", got.RepoRef)
	}
}

func TestPlatformKey(t *testing.T) {
	got, err := PlatformKey("linux", "amd64")
	if err != nil || got != "linux-amd64" {
		t.Fatalf("%q %v", got, err)
	}
}
