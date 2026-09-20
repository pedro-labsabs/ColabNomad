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
	paths     map[string]string
	runs      []execx.Spec
	hasResult execx.Result
	hasErr    error
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
		return r.hasResult, r.hasErr
	}
	return execx.Result{}, nil
}

func TestPrepareReusesExistingTmuxSession(t *testing.T) {
	r := &runner{paths: map[string]string{"tmux": "/usr/bin/tmux"}}
	s := &Service{Runner: r, System: &deps.System{Runner: r}, Workspace: "/content/workspaces/repo", Password: "secret"}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 1 {
		t.Fatalf("unexpected commands: %#v", r.runs)
	}
}

func TestPrepareCreatesSessionWhenTmuxReportsExitOne(t *testing.T) {
	r := &runner{paths: map[string]string{"tmux": "/verified/tmux"}, hasResult: execx.Result{ExitCode: 1}, hasErr: errors.New("no session")}
	s := &Service{Runner: r, System: &deps.System{Runner: r}, Workspace: "/workspace", Password: "secret"}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 2 || r.runs[1].Args[0] != "new-session" {
		t.Fatalf("runs = %#v", r.runs)
	}
}

func TestPrepareSurfacesNonOneTmuxFailure(t *testing.T) {
	r := &runner{paths: map[string]string{"tmux": "/verified/tmux"}, hasResult: execx.Result{ExitCode: 2}, hasErr: errors.New("tmux unavailable")}
	s := &Service{Runner: r, System: &deps.System{Runner: r}, Workspace: "/workspace", Password: "secret"}
	if err := s.Prepare(context.Background()); err == nil {
		t.Fatal("non-1 tmux failure was ignored")
	}
	if len(r.runs) != 1 {
		t.Fatalf("unexpected runs = %#v", r.runs)
	}
}

func TestCommandBindsLocalhostAndRequiresWritableAuth(t *testing.T) {
	s := &Service{Workspace: "/workspace", Password: "secret", Binary: "/verified/ttyd"}
	spec := s.Command()
	if spec.Path != "/verified/ttyd" {
		t.Fatalf("path = %q", spec.Path)
	}
	joined := strings.Join(spec.Args, " ")
	for _, want := range []string{"-i 127.0.0.1", "-p 7681", "-c terminal:secret", "-W", "tmux attach-session -t colabnomad"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q: %s", want, joined)
		}
	}
}

func TestPrepareRejectsEmptyPassword(t *testing.T) {
	r := &runner{paths: map[string]string{"tmux": "/verified/tmux"}}
	s := &Service{Runner: r, System: &deps.System{Runner: r}, Workspace: "/workspace"}
	if err := s.Prepare(context.Background()); err == nil {
		t.Fatal("empty password was accepted")
	}
	if len(r.runs) != 0 {
		t.Fatalf("dependency commands ran before credential validation: %#v", r.runs)
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

func TestDestroySessionPropagatesConfiguredDependencyFailure(t *testing.T) {
	r := &runner{}
	s := &Service{Runner: r, System: &deps.System{Runner: r}, Workspace: "/workspace"}
	if err := s.DestroySession(context.Background()); err == nil {
		t.Fatal("dependency failure was swallowed")
	}
	if len(r.runs) != 0 {
		t.Fatalf("kill-session ran after dependency failure: %#v", r.runs)
	}
}
