//go:build live

package smoke

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/health"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/browserauth"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/tunnel"
)

func TestLocalhostRunSSE(t *testing.T) {
	if os.Getenv("COLABNOMAD_LIVE_TUNNEL_TEST") != "1" {
		t.Skip("set COLABNOMAD_LIVE_TUNNEL_TEST=1 to run the live localhost.run test")
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/event" {
			http.NotFound(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != "test-password" {
			t.Fatalf("backend Basic Auth = %q/%q ok=%v", user, pass, ok)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: ready\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer backend.Close()
	gatewayHandler, err := browserauth.NewHandler(browserauth.Config{BackendURL: backend.URL, Username: "opencode", Password: "test-password", SessionToken: "test-session"})
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(gatewayHandler)
	defer local.Close()

	port := strings.TrimPrefix(local.URL, "http://")
	_, port, _ = strings.Cut(port, ":")
	state := t.TempDir()
	provider := tunnel.NewLocalhostRun("ssh", filepath.Join(state, "localhostrun_known_hosts"))
	process, err := (execx.OSProcessRunner{}).Start(provider.Command(mustPort(t, port), state))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Stop(time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var publicURL string
	for publicURL == "" {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		command := provider.Command(mustPort(t, port), state)
		stdout, _ := os.ReadFile(command.StdoutPath)
		stderr, _ := os.ReadFile(command.StderrPath)
		publicURL, err = provider.DiscoverURL(string(stdout) + string(stderr))
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err := tunnel.ProbeSSE(ctx, http.DefaultClient, publicURL+"/api/event", &health.BasicAuth{Username: "opencode", Password: "test-password"}, 20*time.Second); err != nil {
		t.Fatal(err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(publicURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("WWW-Authenticate") != "" || !strings.Contains(string(body), "ColabNomad") {
		t.Fatalf("public login page: status=%d auth=%q body=%q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"), string(body))
	}
	loginURL := publicURL + browserauth.LoginPath
	resp, err = client.PostForm(loginURL, url.Values{"username": {"opencode"}, "password": {"test-password"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("public login response: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	publicParsed, _ := url.Parse(publicURL)
	cookies := jar.Cookies(publicParsed)
	if len(cookies) == 0 {
		t.Fatal("public login did not establish session cookie")
	}
	if err := tunnel.ProbeSSE(ctx, client, publicURL+"/api/event", nil, 20*time.Second); err != nil {
		t.Fatalf("cookie-authenticated public SSE: %v", err)
	}
}

func mustPort(t *testing.T, value string) int {
	t.Helper()
	var port int
	if _, err := fmt.Sscanf(value, "%d", &port); err != nil {
		t.Fatal(err)
	}
	return port
}
