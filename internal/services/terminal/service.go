package terminal

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/deps"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/health"
)

const sessionName = "colabnomad"

type Service struct {
	Runner    execx.Runner
	System    *deps.System
	Workspace string
	Password  string
	Username  string
	Binary    string
	Port      int
	Prober    health.Prober
}

func (s *Service) Name() string           { return "terminal" }
func (s *Service) Dependencies() []string { return nil }

func (s *Service) Prepare(ctx context.Context) error {
	if s.Workspace == "" {
		return fmt.Errorf("terminal workspace is required")
	}
	if s.Password == "" {
		return fmt.Errorf("terminal password is required")
	}
	if s.System == nil {
		s.System = &deps.System{Runner: s.Runner}
	}
	tmux, err := s.System.Ensure(ctx, "tmux", "tmux")
	if err != nil {
		return err
	}
	r := s.Runner
	if r == nil {
		r = execx.OSRunner{}
	}
	result, err := r.Run(ctx, execx.Spec{Path: tmux, Args: []string{"has-session", "-t", sessionName}})
	if err == nil {
		return nil
	}
	if result.ExitCode != 1 {
		return fmt.Errorf("check tmux session: %w", err)
	}
	if _, err := r.Run(ctx, execx.Spec{Path: tmux, Args: []string{"new-session", "-d", "-s", sessionName, "-c", s.Workspace}}); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}
	return nil
}

func (s *Service) Command() execx.ManagedSpec {
	port := s.Port
	if port == 0 {
		port = 7681
	}
	binary := s.Binary
	if binary == "" {
		binary = "ttyd"
	}
	return execx.ManagedSpec{Spec: execx.Spec{Path: binary, Args: []string{"-i", "127.0.0.1", "-p", strconv.Itoa(port), "-c", s.username() + ":" + s.Password, "-W", "-w", s.Workspace, "tmux", "attach-session", "-t", sessionName}, Dir: s.Workspace}}
}

func (s *Service) username() string {
	if s.Username != "" {
		return s.Username
	}
	return "terminal"
}

func (s *Service) Probe(ctx context.Context) error {
	port := s.Port
	if port == 0 {
		port = 7681
	}
	_, err := s.Prober.Do(ctx, health.Request{URL: "http://127.0.0.1:" + strconv.Itoa(port) + "/", Auth: &health.BasicAuth{Username: s.username(), Password: s.Password}, WantStatus: http.StatusOK})
	return err
}

func (s *Service) Cleanup(context.Context) error { return nil }

func (s *Service) DestroySession(ctx context.Context) error {
	r := s.Runner
	if r == nil {
		r = execx.OSRunner{}
	}
	tmux := "tmux"
	if s.System != nil {
		path, err := s.System.Ensure(ctx, "tmux", "tmux")
		if err != nil {
			return fmt.Errorf("resolve tmux: %w", err)
		}
		tmux = path
	}
	result, err := r.Run(ctx, execx.Spec{Path: tmux, Args: []string{"kill-session", "-t", sessionName}})
	if err != nil && result.ExitCode != 1 {
		return fmt.Errorf("destroy tmux session: %w", err)
	}
	return nil
}

// DestroyExistingSession cleans up a previously-created session without
// installing tmux for a runtime that never reached Up.
func DestroyExistingSession(ctx context.Context) error {
	r := execx.OSRunner{}
	tmux, err := r.LookPath("tmux")
	if err != nil {
		return nil
	}
	result, err := r.Run(ctx, execx.Spec{Path: tmux, Args: []string{"kill-session", "-t", sessionName}})
	if err != nil && result.ExitCode != 1 {
		return fmt.Errorf("destroy tmux session: %w", err)
	}
	return nil
}
