package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/config"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/tunnel"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/state"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/supervisor"
)

type runtimeFakeHandle struct{ done chan error }

func (h *runtimeFakeHandle) PID() int           { return 123 }
func (h *runtimeFakeHandle) Done() <-chan error { return h.done }
func (h *runtimeFakeHandle) Stop(time.Duration) error {
	select {
	case <-h.done:
	default:
		close(h.done)
	}
	return nil
}

type runtimeFakeRunner struct{ handle *runtimeFakeHandle }

func (r *runtimeFakeRunner) Start(execx.ManagedSpec) (execx.ProcessHandle, error) {
	r.handle = &runtimeFakeHandle{done: make(chan error)}
	return r.handle, nil
}

type runtimeFakeService struct{ stopped *bool }

func (s runtimeFakeService) Name() string                  { return "svc" }
func (s runtimeFakeService) Dependencies() []string        { return nil }
func (s runtimeFakeService) Prepare(context.Context) error { return nil }
func (s runtimeFakeService) Command() execx.ManagedSpec    { return execx.ManagedSpec{} }
func (s runtimeFakeService) Probe(context.Context) error   { return nil }
func (s runtimeFakeService) Cleanup(context.Context) error { *s.stopped = true; return nil }

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

func TestMaterializeLocalhostRunIdentityWritesPrivateKey0600(t *testing.T) {
	dir := t.TempDir()
	privateKey := "-----BEGIN OPENSSH PRIVATE KEY-----\ntest-key-material\n-----END OPENSSH PRIVATE KEY-----\n"
	path, err := materializeLocalhostRunIdentity(dir, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "ssh", "localhostrun_ed25519") {
		t.Fatalf("identity path = %q", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != privateKey {
		t.Fatalf("identity contents changed")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("identity permissions = %o", info.Mode().Perm())
	}
}

func TestMaterializeLocalhostRunIdentitySkipsMissingSecret(t *testing.T) {
	path, err := materializeLocalhostRunIdentity(t.TempDir(), "")
	if err != nil || path != "" {
		t.Fatalf("missing identity = %q, %v", path, err)
	}
}

func TestPrepareOpenCodeTunnelProviderUsesMaterializedLocalhostRunIdentity(t *testing.T) {
	dir := t.TempDir()
	provider, err := prepareOpenCodeTunnelProvider(config.TunnelLocalhostRun, "/usr/bin/ssh", dir, "private-key")
	if err != nil {
		t.Fatal(err)
	}
	command := provider.Command(4097, dir)
	joined := strings.Join(command.Args, " ")
	if !strings.Contains(joined, "IdentitiesOnly=yes") || !strings.Contains(joined, filepath.Join(dir, "ssh", "localhostrun_ed25519")) {
		t.Fatalf("prepared provider lacks stable identity: %#v", command.Args)
	}
	if strings.Contains(joined, "nokey@localhost.run") {
		t.Fatalf("prepared provider still uses anonymous localhost.run: %#v", command.Args)
	}
}

func TestUpAppliesBrowserCompatibleTunnelDefaults(t *testing.T) {
	r := &Runtime{Config: config.RuntimeConfig{StateDir: t.TempDir()}, Compose: func(context.Context, UpRequest) (*Composition, error) {
		return &Composition{Endpoints: map[string]string{"opencode": "https://o", "terminal": "https://t"}}, nil
	}}
	if _, err := r.Up(context.Background(), UpRequest{RepoURL: "https://example/repo"}); err != nil {
		t.Fatal(err)
	}
	if r.Config.OpenCodeTunnel != config.TunnelLocalhostRun || r.Config.TerminalTunnel != config.TunnelCloudflare || r.Config.OpenCodeGatewayPort != 4097 {
		t.Fatalf("effective defaults = %#v", r.Config)
	}
}

func TestUpRejectsGatewayPortCollision(t *testing.T) {
	r := &Runtime{Config: config.RuntimeConfig{StateDir: t.TempDir(), OpenCodePort: 4096, OpenCodeGatewayPort: 4096, TerminalPort: 7681}}
	if _, err := r.Up(context.Background(), UpRequest{RepoURL: "https://example/repo"}); err == nil || !strings.Contains(err.Error(), "service ports must be distinct") {
		t.Fatalf("gateway collision error = %v", err)
	}
}

func TestServicePortUsesGatewayPortForGatewayAndOpenCodeTunnel(t *testing.T) {
	c := config.RuntimeConfig{OpenCodePort: 4096, OpenCodeGatewayPort: 4097, TerminalPort: 7681}
	for _, name := range []string{"opencode-gateway", "tunnel:opencode"} {
		if got := servicePort(name, c); got != 4097 {
			t.Fatalf("%s port = %d", name, got)
		}
	}
}

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

func TestUpUsesManifestPathFromRequestAndRetainsIt(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "requested-versions.json")
	if err := os.WriteFile(manifestPath, []byte(`{"opencode":{"version":"requested"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var r *Runtime
	r = &Runtime{Config: config.RuntimeConfig{StateDir: dir, OpenCodePort: 4096, TerminalPort: 7681}, Compose: func(context.Context, UpRequest) (*Composition, error) {
		if got := r.Versions.OpenCode.Version; got != "requested" {
			return nil, errors.New("request manifest was not loaded")
		}
		return &Composition{Endpoints: map[string]string{}}, nil
	}}
	if _, err := r.Up(context.Background(), UpRequest{RepoURL: "https://example/repo", VersionsPath: manifestPath}); err != nil {
		t.Fatal(err)
	}
	if r.VersionsPath != manifestPath {
		t.Fatalf("versions path = %q, want %q", r.VersionsPath, manifestPath)
	}
}

func TestUpRollsBackStartedServicesWhenStateSaveFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	cleaned := false
	runner := &runtimeFakeRunner{}
	svc := runtimeFakeService{stopped: &cleaned}
	m := supervisor.NewManager([]supervisor.Service{svc}, runner, supervisor.RestartPolicy{MaxRestarts: 0})
	r := &Runtime{Config: config.RuntimeConfig{StateDir: dir, OpenCodePort: 4096, TerminalPort: 7681}, Compose: func(context.Context, UpRequest) (*Composition, error) {
		return &Composition{Services: []string{"svc"}, Endpoints: map[string]string{}, Manager: m}, nil
	}}
	_, err := r.Up(context.Background(), UpRequest{RepoURL: "https://example/repo"})
	if err == nil || !strings.Contains(err.Error(), "replace state.json") {
		t.Fatalf("Up error = %v", err)
	}
	if !cleaned || runner.handle == nil {
		t.Fatal("started service was not cleaned up after persistence failure")
	}
	select {
	case <-runner.handle.Done():
	default:
		t.Fatal("started process was not stopped after persistence failure")
	}
	if r.running || r.Manager != nil {
		t.Fatalf("runtime retained running state: running=%v manager=%v", r.running, r.Manager)
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

func TestSelectTunnelProvidersSupportsBrowserDefaultsAndLegacyServeo(t *testing.T) {
	open, err := selectOpenCodeTunnel(config.TunnelLocalhostRun, "/usr/bin/ssh")
	if err != nil || open.Name() != "localhostrun" {
		t.Fatalf("opencode provider = %v, %v", open, err)
	}
	if err := tunnel.Validate(open, tunnel.Requirements{SSE: true}); err != nil {
		t.Fatal(err)
	}
	legacyOpen, err := selectOpenCodeTunnel(config.TunnelServeo, "/usr/bin/ssh")
	if err != nil || legacyOpen.Name() != "serveo" {
		t.Fatalf("legacy opencode provider = %v, %v", legacyOpen, err)
	}
	term, err := selectTerminalTunnel(config.TunnelCloudflare, "/usr/bin/ssh", "/bin/cloudflared")
	if err != nil || term.Name() != "cloudflare" {
		t.Fatalf("terminal provider = %v, %v", term, err)
	}
	if _, err := selectOpenCodeTunnel(config.TunnelCloudflare, "/usr/bin/ssh"); err == nil {
		t.Fatal("cloudflare unexpectedly accepted for opencode")
	}
}

func TestNewOpenCodeGatewayServiceUsesDedicatedPortAndSecrets(t *testing.T) {
	c := config.RuntimeConfig{OpenCodePort: 4096, OpenCodeGatewayPort: 4097}
	cred := state.Credentials{OpenCodeUser: "opencode", OpenCodePassword: "secret"}
	g := newOpenCodeGatewayService("/bin/colabnomad", c, cred, "session-token")
	if g.BackendPort != 4096 || g.ListenPort != 4097 || g.Username != "opencode" || g.Password != "secret" || g.SessionToken != "session-token" || g.Binary != "/bin/colabnomad" {
		t.Fatalf("gateway = %#v", g)
	}
}

type namedGraphService struct{ name string }

func (s namedGraphService) Name() string                  { return s.name }
func (s namedGraphService) Dependencies() []string        { return nil }
func (s namedGraphService) Prepare(context.Context) error { return nil }
func (s namedGraphService) Command() execx.ManagedSpec    { return execx.ManagedSpec{} }
func (s namedGraphService) Probe(context.Context) error   { return nil }
func (s namedGraphService) Cleanup(context.Context) error { return nil }

func TestNewTunnelServicesTargetsGatewayAndTerminalSeparately(t *testing.T) {
	c := config.RuntimeConfig{OpenCodeGatewayPort: 4097, TerminalPort: 7681}
	cred := state.Credentials{OpenCodeUser: "opencode", OpenCodePassword: "open-secret", TerminalUser: "terminal", TerminalPassword: "term-secret"}
	openProvider := tunnel.NewLocalhostRun("ssh", "")
	termProvider := tunnel.NewCloudflare("cloudflared")
	gateway := namedGraphService{name: "opencode-gateway"}
	terminal := namedGraphService{name: "terminal"}

	to, tt := newTunnelServices(c, "/state", openProvider, termProvider, gateway, terminal, cred)
	if to.Name() != "tunnel:opencode" || to.LocalPort != 4097 || len(to.Dependencies()) != 1 || to.Dependencies()[0] != "opencode-gateway" || !to.Requirements.SSE {
		t.Fatalf("opencode tunnel = %#v deps=%#v", to, to.Dependencies())
	}
	if to.Auth == nil || to.Auth.Username != "opencode" || to.Auth.Password != "open-secret" {
		t.Fatalf("opencode tunnel auth = %#v", to.Auth)
	}
	if tt.Name() != "tunnel:terminal" || tt.LocalPort != 7681 || len(tt.Dependencies()) != 1 || tt.Dependencies()[0] != "terminal" || !tt.Requirements.WebSocket {
		t.Fatalf("terminal tunnel = %#v deps=%#v", tt, tt.Dependencies())
	}
	if tt.Auth == nil || tt.Auth.Username != "terminal" || tt.Auth.Password != "term-secret" {
		t.Fatalf("terminal tunnel auth = %#v", tt.Auth)
	}
}

type runtimeEndpointService struct {
	name       string
	urls       []string
	mu         sync.Mutex
	generation int
}

func (s *runtimeEndpointService) Name() string                  { return s.name }
func (s *runtimeEndpointService) Dependencies() []string        { return nil }
func (s *runtimeEndpointService) Command() execx.ManagedSpec    { return execx.ManagedSpec{} }
func (s *runtimeEndpointService) Probe(context.Context) error   { return nil }
func (s *runtimeEndpointService) Cleanup(context.Context) error { return nil }
func (s *runtimeEndpointService) Prepare(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation < len(s.urls)-1 {
		s.generation++
	}
	return nil
}
func (s *runtimeEndpointService) PublicURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.urls[s.generation]
}

func TestUpPersistsNewEndpointAfterTunnelRestart(t *testing.T) {
	dir := t.TempDir()
	endpoint := &runtimeEndpointService{name: "tunnel:opencode", urls: []string{"https://old.lhr.life", "https://new.lhr.life"}, generation: -1}
	runner := &runtimeFakeRunner{}
	m := supervisor.NewManager([]supervisor.Service{endpoint}, runner, supervisor.RestartPolicy{MaxRestarts: 1})
	r := &Runtime{Config: config.RuntimeConfig{StateDir: dir}, Compose: func(context.Context, UpRequest) (*Composition, error) {
		return &Composition{
			Services:  []string{"tunnel:opencode"},
			Endpoints: map[string]string{"opencode": "https://old.lhr.life"},
			Manager:   m,
			ServiceMap: map[string]supervisor.Service{
				"tunnel:opencode": endpoint,
			},
		}, nil
	}}
	if _, err := r.Up(context.Background(), UpRequest{RepoURL: "https://example/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Restart(context.Background(), "tunnel:opencode"); err != nil {
		t.Fatal(err)
	}

	if got := r.Status().Endpoints["opencode"]; got != "https://new.lhr.life" {
		t.Fatalf("runtime endpoint = %q, want new URL", got)
	}
	persisted, err := (state.Store{Dir: dir}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := persisted.Endpoints["opencode"]; got != "https://new.lhr.life" {
		t.Fatalf("persisted endpoint = %q, want new URL", got)
	}
	second, err := r.Up(context.Background(), UpRequest{RepoURL: "https://example/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if second.OpenCodeURL != "https://new.lhr.life" {
		t.Fatalf("idempotent up returned OpenCode URL %q, want new URL", second.OpenCodeURL)
	}
}

func TestDoctorReportsEndpointStateSyncFailure(t *testing.T) {
	r := &Runtime{}
	r.endpointSyncErr = errors.New("persist endpoint state")
	got := r.Doctor(context.Background())
	if got.Healthy || got.Components["state:endpoints"] != "unhealthy" {
		t.Fatalf("endpoint sync failure not surfaced: %#v", got)
	}
}
