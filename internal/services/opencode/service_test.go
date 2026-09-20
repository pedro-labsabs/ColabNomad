package opencode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/health"
)

type fakeRunner struct {
	runs   []execx.Spec
	result execx.Result
	err    error
}

func (r *fakeRunner) LookPath(string) (string, error) { return "", errors.New("not implemented") }
func (r *fakeRunner) Run(_ context.Context, spec execx.Spec) (execx.Result, error) {
	r.runs = append(r.runs, spec)
	return r.result, r.err
}

func newTestService(t *testing.T) (*Service, *fakeRunner) {
	t.Helper()
	r := &fakeRunner{result: execx.Result{Stdout: "opencode v2.0.11\n"}}
	s := New(Config{Binary: "/verified/opencode", Workspace: "/content/workspaces/project", StateDir: t.TempDir(), Username: "opencode", Password: "password", APIKey: "api-key", Port: 4096}, health.Prober{})
	s.runner = r
	return s, r
}

func TestCommandUsesWorkspaceLocalhostAndBasicAuthEnv(t *testing.T) {
	svc, _ := newTestService(t)
	spec := svc.Command()
	if spec.Dir != "/content/workspaces/project" {
		t.Fatal(spec.Dir)
	}
	if got := strings.Join(spec.Args, " "); got != "serve --hostname 127.0.0.1 --port 4096" {
		t.Fatal(got)
	}
	if spec.Env["OPENCODE_SERVER_USERNAME"] != "opencode" || spec.Env["OPENCODE_SERVER_PASSWORD"] == "" {
		t.Fatal(spec.Env)
	}
	if spec.Env["OPENCODE_DISABLE_AUTOUPDATE"] != "1" {
		t.Fatal(spec.Env)
	}
	if strings.HasPrefix(spec.Env["OPENCODE_CONFIG"], spec.Dir) {
		t.Fatal("config must be outside workspace")
	}
	if spec.Env["OPENCODE_API_KEY"] != "api-key" {
		t.Fatal(spec.Env)
	}
}

func TestPrepareWritesExternalPrivateConfigAndChecksVersion(t *testing.T) {
	svc, runner := newTestService(t)
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(svc.cfg.StateDir, "opencode", "opencode.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{"$schema":"https://opencode.ai/config.json"}` {
		t.Fatalf("content = %q", content)
	}
	if len(runner.runs) != 1 || runner.runs[0].Path != "/verified/opencode" || strings.Join(runner.runs[0].Args, " ") != "--version" {
		t.Fatalf("runs = %#v", runner.runs)
	}
}

func TestPrepareRejectsWrongVersion(t *testing.T) {
	svc, runner := newTestService(t)
	runner.result.Stdout = "opencode v2.0.10\n"
	if err := svc.Prepare(context.Background()); err == nil {
		t.Fatal("wrong version accepted")
	}
}

func TestProbeRequiresAuthenticatedMatchingProject(t *testing.T) {
	var gotAuth, gotDirectory string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, _ := r.BasicAuth()
		gotAuth = user + ":" + password
		gotDirectory = r.Header.Get("x-opencode-directory")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"canonical":"/content/workspaces/project"}]`))
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	svc.probeURL = server.URL
	if err := svc.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "opencode:password" || gotDirectory != svc.cfg.Workspace {
		t.Fatalf("auth=%q directory=%q", gotAuth, gotDirectory)
	}
}

func TestProbeRejectsUnauthorizedMalformedAndMissingWorkspace(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		contentType string
	}{
		{"unauthorized", http.StatusUnauthorized, `{}`, "application/json"},
		{"malformed", http.StatusOK, `{`, "application/json"},
		{"missing workspace", http.StatusOK, `[{"canonical":"/other"}]`, "application/json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			svc, _ := newTestService(t)
			svc.probeURL = server.URL
			if err := svc.Probe(context.Background()); err == nil {
				t.Fatal("invalid readiness response accepted")
			}
		})
	}
}

func TestErrorsDoNotExposeSecrets(t *testing.T) {
	svc, runner := newTestService(t)
	runner.result.Stdout = svc.cfg.Password + " " + svc.cfg.APIKey
	err := svc.Prepare(context.Background())
	if err == nil || strings.Contains(err.Error(), svc.cfg.Password) || strings.Contains(err.Error(), svc.cfg.APIKey) {
		t.Fatalf("error = %v", err)
	}
	if got := svc.StatusError(errors.New(svc.cfg.Password + " " + svc.cfg.APIKey)); strings.Contains(got.Error(), svc.cfg.Password) || strings.Contains(got.Error(), svc.cfg.APIKey) {
		t.Fatalf("status error = %v", got)
	}
}
