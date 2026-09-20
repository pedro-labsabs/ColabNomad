package tunnel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
