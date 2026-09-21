package app

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/config"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/state"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/supervisor"
)

type doctorService struct {
	name string
	err  error
}

func (s doctorService) Name() string                  { return s.name }
func (s doctorService) Dependencies() []string        { return nil }
func (s doctorService) Prepare(context.Context) error { return nil }
func (s doctorService) Command() execx.ManagedSpec    { return execx.ManagedSpec{} }
func (s doctorService) Probe(context.Context) error   { return s.err }
func (s doctorService) Cleanup(context.Context) error { return nil }

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

func TestDoctorTreatsManagedListeningPortAsHealthy(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	r := &Runtime{Config: config.RuntimeConfig{StateDir: t.TempDir(), OpenCodePort: port}, Services: map[string]supervisor.Service{"opencode": doctorService{name: "opencode"}}}
	got := r.Doctor(context.Background())
	if got.Components["port:opencode"] != "healthy" {
		t.Fatalf("managed listener classified incorrectly: %#v", got)
	}
}

func TestDoctorProbesActualManagedServices(t *testing.T) {
	r := &Runtime{Services: map[string]supervisor.Service{
		"terminal": doctorService{name: "terminal"}, "opencode": doctorService{name: "opencode", err: context.Canceled},
		"tunnel:terminal": doctorService{name: "tunnel:terminal"}, "tunnel:opencode": doctorService{name: "tunnel:opencode"},
	}}
	got := r.Doctor(context.Background())
	if got.Components["opencode"] != "unhealthy" || got.Healthy {
		t.Fatalf("actual failing probe not surfaced: %#v", got)
	}
}

func TestUpIsIdempotentAndRejectsIncompatibleRunningConfig(t *testing.T) {
	calls := 0
	r := &Runtime{Config: config.RuntimeConfig{StateDir: t.TempDir(), OpenCodePort: 4096, TerminalPort: 7681}, Compose: func(context.Context, UpRequest) (*Composition, error) {
		calls++
		return &Composition{Services: []string{"terminal"}, Endpoints: map[string]string{"opencode": "https://o", "terminal": "https://t"}}, nil
	}}
	first, err := r.Up(context.Background(), UpRequest{RepoURL: "https://example/repo", RepoRef: "main", OpenCodeTunnel: config.TunnelServeo, TerminalTunnel: config.TunnelServeo})
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Up(context.Background(), UpRequest{RepoURL: "https://example/repo", RepoRef: "main", OpenCodeTunnel: config.TunnelServeo, TerminalTunnel: config.TunnelServeo})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || second != first {
		t.Fatalf("compatible Up was not idempotent: calls=%d first=%#v second=%#v", calls, first, second)
	}
	if _, err := r.Up(context.Background(), UpRequest{RepoURL: "https://other/repo", RepoRef: "main", OpenCodeTunnel: config.TunnelServeo, TerminalTunnel: config.TunnelServeo}); err == nil || !strings.Contains(err.Error(), "down first") {
		t.Fatalf("incompatible Up error: %v", err)
	}
	if r.Config.RepoURL != "https://example/repo" || r.Config.RepoRef != "main" {
		t.Fatalf("effective config not persisted: %#v", r.Config)
	}
}

func TestReplacementRuntimeMarksPersistedServicesStaleAndNeverAdoptsPID(t *testing.T) {
	dir := t.TempDir()
	store := state.Store{Dir: dir}
	if err := store.Save(state.RuntimeState{Services: map[string]state.ServiceState{"opencode": {PID: 12345, Status: string(supervisor.Healthy)}}}); err != nil {
		t.Fatal(err)
	}
	r := &Runtime{Config: config.RuntimeConfig{StateDir: dir}, Store: store}
	got := r.Status()
	if got.Services["opencode"].Status != "stale" || got.Services["opencode"].PID != 0 {
		t.Fatalf("replacement status adopted persisted process: %#v", got.Services["opencode"])
	}
	if err := r.Restart(context.Background(), "opencode"); err == nil || !strings.Contains(err.Error(), "run up or recover") {
		t.Fatalf("restart guidance: %v", err)
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
