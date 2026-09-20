package config

import "testing"

func TestDefaultConfigUsesServeoAndLocalhost(t *testing.T) {
	got := Default("https://github.com/example/project.git")
	if got.OpenCodeTunnel != TunnelServeo || got.TerminalTunnel != TunnelServeo {
		t.Fatal(got)
	}
	if got.OpenCodePort != 4096 || got.TerminalPort != 7681 {
		t.Fatal(got)
	}
}

func TestPlatformKey(t *testing.T) {
	got, err := PlatformKey("linux", "amd64")
	if err != nil || got != "linux-amd64" {
		t.Fatalf("%q %v", got, err)
	}
}
