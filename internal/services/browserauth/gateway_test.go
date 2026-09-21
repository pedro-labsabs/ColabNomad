package browserauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGatewayRejectsNonLoopbackBackend(t *testing.T) {
	for _, backend := range []string{"http://0.0.0.0:4096", "http://192.0.2.1:4096", "https://example.com"} {
		if _, err := NewHandler(Config{BackendURL: backend, Username: "opencode", Password: "secret", SessionToken: "session"}); err == nil {
			t.Fatalf("accepted non-loopback backend %q", backend)
		}
	}
}

func TestGatewayListenAddressMustBeLoopback(t *testing.T) {
	for _, address := range []string{"0.0.0.0:4097", ":4097", "192.0.2.1:4097"} {
		if err := validateLoopbackListenAddress(address); err == nil {
			t.Fatalf("accepted non-loopback listen address %q", address)
		}
	}
	for _, address := range []string{"127.0.0.1:4097", "[::1]:4097", "localhost:4097"} {
		if err := validateLoopbackListenAddress(address); err != nil {
			t.Fatalf("rejected loopback listen address %q: %v", address, err)
		}
	}
}

func TestGatewayLoginCreatesSecureSessionAndProxiesWithBackendAuth(t *testing.T) {
	seenAuth := make(chan [2]string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			t.Error("backend request missing Basic Auth")
		}
		if cookie, err := r.Cookie(SessionCookieName); err == nil {
			t.Errorf("gateway session cookie leaked to backend: %#v", cookie)
		}
		seenAuth <- [2]string{user, pass}
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.WriteString(w, "console.log('ok')")
	}))
	defer backend.Close()

	h, err := NewHandler(Config{BackendURL: backend.URL, Username: "opencode", Password: "secret", SessionToken: "opaque-session"})
	if err != nil {
		t.Fatal(err)
	}

	loginPage := httptest.NewRecorder()
	h.ServeHTTP(loginPage, httptest.NewRequest(http.MethodGet, "/", nil))
	if loginPage.Code != http.StatusOK || !strings.Contains(loginPage.Body.String(), "ColabNomad") {
		t.Fatalf("login page: code=%d body=%q", loginPage.Code, loginPage.Body.String())
	}
	if got := loginPage.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("login page triggered browser Basic Auth: %q", got)
	}

	form := url.Values{"username": {"opencode"}, "password": {"secret"}}
	login := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(login, req)
	if login.Code != http.StatusSeeOther || login.Header().Get("Location") != "/" {
		t.Fatalf("login response: code=%d location=%q", login.Code, login.Header().Get("Location"))
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %#v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != SessionCookieName || cookie.Value != "opaque-session" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("session cookie = %#v", cookie)
	}

	proxied := httptest.NewRecorder()
	assetReq := httptest.NewRequest(http.MethodGet, "/_assets/app.js", nil)
	assetReq.AddCookie(cookie)
	h.ServeHTTP(proxied, assetReq)
	if proxied.Code != http.StatusOK || proxied.Body.String() != "console.log('ok')" {
		t.Fatalf("proxied response: code=%d body=%q", proxied.Code, proxied.Body.String())
	}
	if got := proxied.Header().Get("Content-Security-Policy"); got != "" {
		t.Fatalf("login CSP leaked into proxied OpenCode response: %q", got)
	}
	if got := <-seenAuth; got != [2]string{"opencode", "secret"} {
		t.Fatalf("backend auth = %#v", got)
	}
}

func TestGatewayAllowsExplicitBasicAuthForAutomatedProbe(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != "secret" {
			t.Fatalf("backend auth = %q/%q ok=%v", user, pass, ok)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ready\n\n")
	}))
	defer backend.Close()
	h, err := NewHandler(Config{BackendURL: backend.URL, Username: "opencode", Password: "secret", SessionToken: "session"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/event", nil)
	req.SetBasicAuth("opencode", "secret")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "data: ready") {
		t.Fatalf("automated probe = %d %q", rr.Code, rr.Body.String())
	}
}

func TestGatewayReturnsPlain401ForUnauthenticatedAssetRequests(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthenticated asset reached backend")
	}))
	defer backend.Close()
	h, err := NewHandler(Config{BackendURL: backend.URL, Username: "opencode", Password: "secret", SessionToken: "session"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_assets/app.js", nil)
	req.Header.Set("Accept", "*/*")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized || strings.Contains(rr.Body.String(), "<html") {
		t.Fatalf("asset response = %d %q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("asset triggered Basic Auth challenge %q", got)
	}
}

func TestGatewayRejectsWrongLoginWithoutBasicAuthChallenge(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unauthenticated request reached backend")
	}))
	defer backend.Close()
	h, err := NewHandler(Config{BackendURL: backend.URL, Username: "opencode", Password: "secret", SessionToken: "session"})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"username": {"opencode"}, "password": {"wrong"}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := rr.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("unexpected Basic Auth challenge %q", got)
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Fatalf("wrong login set cookies: %#v", rr.Result().Cookies())
	}
}

func TestGatewayHealthIsUnauthenticated(t *testing.T) {
	backend := httptest.NewServer(http.NotFoundHandler())
	defer backend.Close()
	h, err := NewHandler(Config{BackendURL: backend.URL, Username: "opencode", Password: "secret", SessionToken: "session"})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, HealthPath, nil))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "ok" {
		t.Fatalf("health = %d %q", rr.Code, rr.Body.String())
	}
}

func TestServiceKeepsSecretsOutOfArgvAndDependsOnOpenCode(t *testing.T) {
	s := Service{Binary: "/bin/colabnomad", BackendPort: 4096, ListenPort: 4097, Username: "opencode", Password: "top-secret", SessionToken: "session-secret"}
	if got := s.Dependencies(); len(got) != 1 || got[0] != "opencode" {
		t.Fatalf("dependencies = %#v", got)
	}
	spec := s.Command()
	joined := strings.Join(append([]string{spec.Path}, spec.Args...), " ")
	if strings.Contains(joined, "top-secret") || strings.Contains(joined, "session-secret") || strings.Contains(joined, "opencode") {
		t.Fatalf("secret or username leaked to argv: %q", joined)
	}
	for _, key := range []string{EnvBackendURL, EnvListenAddress, EnvUsername, EnvPassword, EnvSessionToken} {
		if spec.Env[key] == "" {
			t.Fatalf("missing %s in env: %#v", key, spec.Env)
		}
	}
	if spec.Args[0] != "gateway" {
		t.Fatalf("gateway argv = %#v", spec.Args)
	}
}

func TestServiceProbeUsesHealthEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != HealthPath {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port := u.Port()
	s := Service{ListenPort: mustPort(t, port)}
	if err := s.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func mustPort(t *testing.T, raw string) int {
	t.Helper()
	var p int
	if _, err := fmt.Sscanf(raw, "%d", &p); err != nil {
		t.Fatal(err)
	}
	return p
}
