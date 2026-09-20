package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/config"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/state"
)

func TestUpComposesExactServicesAndPersistsNonSecretState(t *testing.T) {
	dir := t.TempDir()
	r := &Runtime{Config: config.RuntimeConfig{StateDir: dir, WorkspaceRoot: filepath.Join(dir, "work"), OpenCodePort: 4096, TerminalPort: 7681}, VersionsPath: filepath.Join(dir, "versions.json"), Compose: func(context.Context, UpRequest) (*Composition, error) {
		return &Composition{WorkspacePath: filepath.Join(dir, "work", "repo"), Services: []string{"terminal", "opencode", "tunnel:terminal", "tunnel:opencode"}, Endpoints: map[string]string{"terminal": "https://terminal.test", "opencode": "https://opencode.test"}}, nil
	}}
	manifest := config.Versions{OpenCode: config.ToolSpec{Version: "2"}}
	b, _ := json.Marshal(manifest)
	if err := os.WriteFile(r.VersionsPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	summary, err := r.Up(context.Background(), UpRequest{RepoURL: "https://example/repo", GitHubToken: "gh:secret", OpenCodeAPIKey: "key\nsecret"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.OpenCodeURL != "https://opencode.test" || summary.TerminalURL != "https://terminal.test" {
		t.Fatalf("summary: %#v", summary)
	}
	st, err := r.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("secret persisted in runtime state")
	}
	if _, err := r.Store.LoadCredentials(); err != nil {
		t.Fatal("credentials were not protected/saved: ", err)
	}
	r.LogContents = map[string]string{"terminal": "gh:secret key\nsecret"}
	if got := r.Logs("terminal"); strings.Contains(got, "gh:secret") || strings.Contains(got, "key\nsecret") {
		t.Fatalf("secret leaked from logs: %q", got)
	}
}

func TestLogsReadBoundedFilesAndRedactCredentials(t *testing.T) {
	dir := t.TempDir()
	r := &Runtime{Config: config.RuntimeConfig{StateDir: dir}, Credentials: state.Credentials{OpenCodePassword: "p@ss\nword"}}
	path := filepath.Join(dir, "logs", "terminal.stdout.log")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	if err := os.WriteFile(path, []byte("before\np@ss\nword\nafter"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := r.Logs("terminal"); strings.Contains(got, "p@ss") || !strings.Contains(got, "after") {
		t.Fatalf("bad redaction/tail: %q", got)
	}
}
