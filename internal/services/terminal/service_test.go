package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/deps"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

type runner struct {
	paths map[string]string
	runs  []execx.Spec
}

func (r *runner) LookPath(s string) (string, error) {
	if p := r.paths[s]; p != "" {
		return p, nil
	}
	return "", errors.New("missing")
}
func (r *runner) Run(_ context.Context, s execx.Spec) (execx.Result, error) {
	r.runs = append(r.runs, s)
	if len(s.Args) > 0 && s.Args[0] == "has-session" {
		return execx.Result{}, nil
	}
	return execx.Result{}, nil
}

func TestPrepareReusesExistingTmuxSession(t *testing.T) {
	r := &runner{paths: map[string]string{"tmux": "/usr/bin/tmux"}}
	s := &Service{Runner: r, System: &deps.System{Runner: r}, Workspace: "/content/workspaces/repo"}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 1 {
		t.Fatalf("unexpected commands: %#v", r.runs)
	}
}

func TestCommandBindsLocalhostAndRequiresWritableAuth(t *testing.T) {
	s := &Service{Workspace: "/workspace", Password: "secret"}
	spec := s.Command()
	joined := strings.Join(spec.Args, " ")
	for _, want := range []string{"-i 127.0.0.1", "-p 7681", "-c terminal:secret", "-W", "tmux attach-session -t colabnomad"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q: %s", want, joined)
		}
	}
}

func TestDestroySessionIsExplicitAndCleanupDoesNotKill(t *testing.T) {
	r := &runner{paths: map[string]string{"tmux": "/usr/bin/tmux"}}
	s := &Service{Runner: r, System: &deps.System{Runner: r}, Workspace: "/workspace"}
	if err := s.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 0 {
		t.Fatal("cleanup killed session")
	}
	if err := s.DestroySession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 1 || !strings.Contains(strings.Join(r.runs[0].Args, " "), "kill-session") {
		t.Fatalf("runs=%#v", r.runs)
	}
}
