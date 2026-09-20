package app

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/config"
)

func TestUpRejectsOccupiedPortWithoutTouchingListener(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	r := &Runtime{Config: config.RuntimeConfig{StateDir: t.TempDir(), OpenCodePort: port, TerminalPort: port + 1}}
	_, err = r.Up(context.Background(), UpRequest{})
	if err == nil || !strings.Contains(err.Error(), "port "+itoa(port)+" already in use") {
		t.Fatalf("got %v", err)
	}
	probe, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("listener was disturbed: %v", err)
	}
	probe.Close()
}

func TestDoctorClassifiesHealthyAndUnhealthyServices(t *testing.T) {
	r := &Runtime{Probes: map[string]ProbeResult{"healthy": {Err: nil}, "failed": {Err: context.Canceled}}}
	got := r.Doctor(context.Background())
	if got.Healthy || got.Components["healthy"] != "healthy" || got.Components["failed"] != "unhealthy" {
		t.Fatalf("unexpected doctor result: %#v", got)
	}
}

func TestDoctorClassifiesMissingWorkspaceToolAndProvider(t *testing.T) {
	r := &Runtime{Config: config.RuntimeConfig{StateDir: t.TempDir()}, WorkspacePath: t.TempDir() + "/missing", ToolChecks: map[string]error{"tool:opencode": context.Canceled}, ProviderChecks: map[string]error{"provider:opencode": context.Canceled}}
	got := r.Doctor(context.Background())
	for _, name := range []string{"workspace", "tool:opencode", "provider:opencode"} {
		if got.Components[name] != "unhealthy" {
			t.Fatalf("%s: %#v", name, got)
		}
	}
	if got.Healthy {
		t.Fatal("doctor reported unhealthy components as healthy")
	}
}

func itoa(n int) string {
	if n < 0 {
		return "-"
	}
	if n == 0 {
		return "0"
	}
	b := make([]byte, 0, 8)
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
