package tunnel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

type namedLocalService struct{ name string }

type generationProvider struct {
	stdoutPath string
	stderrPath string
}

func (p generationProvider) Name() string               { return "generation-test" }
func (p generationProvider) Capabilities() Capabilities { return Capabilities{} }
func (p generationProvider) Command(int, string) execx.ManagedSpec {
	return execx.ManagedSpec{
		StdoutPath: p.stdoutPath,
		StderrPath: p.stderrPath,
	}
}
func (p generationProvider) DiscoverURL(output string) (string, error) {
	for _, token := range strings.Fields(output) {
		if strings.HasPrefix(token, "http://") {
			return token, nil
		}
	}
	return "", fmt.Errorf("no generation test URL found")
}

func (s namedLocalService) Name() string                  { return s.name }
func (s namedLocalService) Dependencies() []string        { return nil }
func (s namedLocalService) Prepare(context.Context) error { return nil }
func (s namedLocalService) Command() execx.ManagedSpec    { return execx.ManagedSpec{} }
func (s namedLocalService) Probe(context.Context) error   { return nil }
func (s namedLocalService) Cleanup(context.Context) error { return nil }

func TestCloudflareQuickRejectedForOpenCode(t *testing.T) {
	err := Validate(NewCloudflare("/bin/cloudflared"), Requirements{SSE: true})
	if err == nil {
		t.Fatal("expected SSE capability rejection")
	}
}

func TestServeoSatisfiesOpenCodeRequirements(t *testing.T) {
	if err := Validate(NewServeo("/usr/bin/ssh", "/state/known_hosts"), Requirements{SSE: true}); err != nil {
		t.Fatal(err)
	}
}

func TestTunnelServicesHaveDistinctDependencyNames(t *testing.T) {
	terminal := NewService(NewServeo("ssh", ""), namedLocalService{name: "terminal"}, "/state", 7681)
	opencode := NewService(NewServeo("ssh", ""), namedLocalService{name: "opencode"}, "/state", 4096)
	if terminal.Name() != "tunnel:terminal" || opencode.Name() != "tunnel:opencode" {
		t.Fatalf("names = %q, %q", terminal.Name(), opencode.Name())
	}
	if !reflect.DeepEqual(terminal.Dependencies(), []string{"terminal"}) || !reflect.DeepEqual(opencode.Dependencies(), []string{"opencode"}) {
		t.Fatalf("dependencies = %#v, %#v", terminal.Dependencies(), opencode.Dependencies())
	}
}

func TestTunnelPrepareRequiresDependency(t *testing.T) {
	s := NewService(NewServeo("ssh", ""), nil, t.TempDir(), 7681)
	if err := s.Prepare(context.Background()); err == nil {
		t.Fatal("tunnel without a local dependency was accepted")
	}
}

func TestTunnelPrepareStartsFirstProviderGeneration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	stateDir := t.TempDir()
	provider := generationProvider{
		stdoutPath: filepath.Join(stateDir, "provider.stdout.log"),
		stderrPath: filepath.Join(stateDir, "provider.stderr.log"),
	}
	s := NewService(provider, namedLocalService{name: "local"}, stateDir, 7681)

	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provider.stdoutPath, []byte(server.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.PublicURL(); got != server.URL {
		t.Fatalf("PublicURL = %q, want %q", got, server.URL)
	}
}

func TestTunnelPrepareStartsFreshProviderGeneration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	stateDir := t.TempDir()
	provider := generationProvider{
		stdoutPath: filepath.Join(stateDir, "provider.stdout.log"),
		stderrPath: filepath.Join(stateDir, "provider.stderr.log"),
	}
	s := NewService(provider, namedLocalService{name: "local"}, stateDir, 7681)

	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provider.stdoutPath, []byte(server.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.PublicURL(); got != "" {
		t.Fatalf("PublicURL after new generation = %q, want empty", got)
	}
	for _, path := range []string{provider.stdoutPath, provider.stderrPath} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(contents) != 0 {
			t.Fatalf("%s contains stale generation output %q", path, contents)
		}
	}

	secondURL := server.URL + "/second"
	if err := os.WriteFile(provider.stdoutPath, []byte(secondURL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.PublicURL(); got != secondURL {
		t.Fatalf("PublicURL = %q, want %q", got, secondURL)
	}
}

func TestTunnelPrepareFailsWhenProviderOutputCannotBeReset(t *testing.T) {
	stateDir := t.TempDir()
	blockedPath := filepath.Join(stateDir, "provider.stdout.log")
	if err := os.Mkdir(blockedPath, 0700); err != nil {
		t.Fatal(err)
	}
	provider := generationProvider{
		stdoutPath: blockedPath,
		stderrPath: filepath.Join(stateDir, "provider.stderr.log"),
	}
	s := NewService(provider, namedLocalService{name: "local"}, stateDir, 7681)

	if err := s.Prepare(context.Background()); err == nil {
		t.Fatal("Prepare succeeded despite an unusable provider output path")
	}
}

func TestServeoCommandsUseDisjointOutputPaths(t *testing.T) {
	first := NewServeo("/usr/bin/ssh", "/state/known_hosts").Command(4096, "/state")
	second := NewServeo("/usr/bin/ssh", "/state/known_hosts").Command(7681, "/state")
	if first.StdoutPath == second.StdoutPath || first.StderrPath == second.StderrPath {
		t.Fatalf("Serveo output paths overlap: %#v and %#v", first, second)
	}
}

func TestProviderCommandsAndURLDiscoveryAreRestricted(t *testing.T) {
	serveo := NewServeo("/usr/bin/ssh", "/state/known_hosts")
	command := serveo.Command(7681, "/state")
	if command.Path != "/usr/bin/ssh" || !strings.Contains(strings.Join(command.Args, " "), "-R 80:127.0.0.1:7681") {
		t.Fatalf("unexpected Serveo command: %#v", command)
	}
	if got, err := serveo.DiscoverURL("Forwarding https://demo.serveo.net\n"); err != nil || got != "https://demo.serveo.net" {
		t.Fatalf("Serveo URL = %q, %v", got, err)
	}
	if _, err := serveo.DiscoverURL("https://evil.example/"); err == nil {
		t.Fatal("accepted arbitrary Serveo URL")
	}
	for _, raw := range []string{"https://demo.serveousercontent.com/path", "https://demo.foo.serveo.net/path"} {
		if got, err := serveo.DiscoverURL(raw); err != nil || got != strings.TrimRight(raw, "/") {
			t.Fatalf("Serveo URL %q = %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"https://serveo.net", "https://serveousercontent.com", "https://demo.serveo.net.evil", "https://user:pass@demo.serveo.net"} {
		if _, err := serveo.DiscoverURL(raw); err == nil {
			t.Fatalf("accepted untrusted Serveo URL %q", raw)
		}
	}
	if got := serveo.ProbeHeaders(); got["serveo-skip-browser-warning"] != "true" {
		t.Fatalf("Serveo probe headers = %#v", got)
	}

	cloudflare := NewCloudflare("/bin/cloudflared")
	command = cloudflare.Command(7681, "/state")
	if got, want := strings.Join(command.Args, " "), "tunnel --no-autoupdate --url http://127.0.0.1:7681"; got != want {
		t.Fatalf("Cloudflare args = %q, want %q", got, want)
	}
	if got, err := cloudflare.DiscoverURL("INF https://demo.trycloudflare.com\n"); err != nil || got != "https://demo.trycloudflare.com" {
		t.Fatalf("Cloudflare URL = %q, %v", got, err)
	}
}

func TestProbeSSEWaitsForFirstFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	err := ProbeSSE(context.Background(), server.Client(), server.URL, nil, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "streaming timeout") {
		t.Fatalf("ProbeSSE error = %v, want streaming timeout", err)
	}
}

func TestProbeSSEWithHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Probe") != "yes" {
			t.Errorf("missing probe header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: ready\n\n"))
	}))
	defer server.Close()
	if err := ProbeSSEWithHeaders(context.Background(), server.Client(), server.URL, nil, map[string]string{"X-Probe": "yes"}, time.Second); err != nil {
		t.Fatal(err)
	}
}
