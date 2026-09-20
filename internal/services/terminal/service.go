package terminal

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/deps"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/health"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/secure"
)

const sessionName = "colabnomad"

type Service struct {
	Runner    execx.Runner
	System    *deps.System
	Workspace string
	Password  string
	Username  string
	Port      int
	Prober    health.Prober
}

func (s *Service) Name() string           { return "terminal" }
func (s *Service) Dependencies() []string { return nil }

func (s *Service) Prepare(ctx context.Context) error {
	if s.Workspace == "" {
		return fmt.Errorf("terminal workspace is required")
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
	_, err = r.Run(ctx, execx.Spec{Path: tmux, Args: []string{"has-session", "-t", sessionName}})
	if err == nil {
		return nil
	}
	if _, err := r.Run(ctx, execx.Spec{Path: tmux, Args: []string{"new-session", "-d", "-s", sessionName, "-c", s.Workspace}}); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}
	return nil
}

func (s *Service) Command() execx.ManagedSpec {
	if s.Password == "" {
		s.Password, _ = secure.RandomPassword(24)
	}
	port := s.Port
	if port == 0 {
		port = 7681
	}
	return execx.ManagedSpec{Spec: execx.Spec{Path: "ttyd", Args: []string{"-i", "127.0.0.1", "-p", strconv.Itoa(port), "-c", s.username() + ":" + s.Password, "-W", "-w", s.Workspace, "tmux", "attach-session", "-t", sessionName}, Dir: s.Workspace}}
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
		if path, err := s.System.Ensure(ctx, "tmux", "tmux"); err == nil {
			tmux = path
		}
	}
	if _, err := r.Run(ctx, execx.Spec{Path: tmux, Args: []string{"kill-session", "-t", sessionName}}); err != nil {
		return fmt.Errorf("destroy tmux session: %w", err)
	}
	return nil
}
