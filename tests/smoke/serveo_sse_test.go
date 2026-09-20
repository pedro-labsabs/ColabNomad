//go:build live

package smoke

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/tunnel"
)

func TestServeoSSE(t *testing.T) {
	if os.Getenv("COLABNOMAD_LIVE_TUNNEL_TEST") != "1" {
		t.Skip("set COLABNOMAD_LIVE_TUNNEL_TEST=1 to run the live Serveo test")
	}

	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/event" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: ready\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer local.Close()

	port := strings.TrimPrefix(local.URL, "http://")
	_, port, _ = strings.Cut(port, ":")
	state := t.TempDir()
	provider := tunnel.NewServeo("ssh", filepath.Join(state, "serveo_known_hosts"))
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
		stdout, _ := os.ReadFile(filepath.Join(state, "serveo.stdout.log"))
		stderr, _ := os.ReadFile(filepath.Join(state, "serveo.stderr.log"))
		publicURL, err = provider.DiscoverURL(string(stdout) + string(stderr))
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err := tunnel.ProbeSSE(ctx, http.DefaultClient, publicURL+"/api/event", nil, 20*time.Second); err != nil {
		t.Fatal(err)
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
