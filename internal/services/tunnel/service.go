package tunnel

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/health"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/supervisor"
)

type Service struct {
	Provider     Provider
	Local        supervisor.Service
	Dependency   string
	Runner       execx.ProcessRunner
	StateDir     string
	LocalPort    int
	Requirements Requirements
	Auth         *health.BasicAuth
	Client       *http.Client
	FirstFrame   time.Duration
	EventPath    string

	mu        sync.RWMutex
	publicURL string
}

func NewService(provider Provider, local supervisor.Service, stateDir string, localPort int) *Service {
	return &Service{Provider: provider, Local: local, StateDir: stateDir, LocalPort: localPort}
}

func (s *Service) Name() string {
	dependency := s.Dependency
	if s.Local != nil {
		dependency = s.Local.Name()
	}
	if dependency == "" {
		return "tunnel"
	}
	return "tunnel:" + dependency
}
func (s *Service) Dependencies() []string {
	if s.Local != nil {
		return []string{s.Local.Name()}
	}
	if s.Dependency != "" {
		return []string{s.Dependency}
	}
	return nil
}

func (s *Service) Prepare(context.Context) error {
	if s.Local == nil && s.Dependency == "" {
		return fmt.Errorf("tunnel local dependency is required")
	}
	if err := Validate(s.Provider, s.Requirements); err != nil {
		return err
	}
	if s.LocalPort <= 0 {
		return fmt.Errorf("tunnel local port is required")
	}
	if s.StateDir == "" {
		return fmt.Errorf("tunnel state directory is required")
	}
	if err := os.MkdirAll(s.StateDir, 0700); err != nil {
		return err
	}
	command := s.Command()
	s.mu.Lock()
	s.publicURL = ""
	s.mu.Unlock()
	for _, path := range []string{command.StdoutPath, command.StderrPath} {
		if path == "" {
			continue
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return fmt.Errorf("reset tunnel output %q: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close tunnel output %q: %w", path, err)
		}
	}
	return nil
}

func (s *Service) Command() execx.ManagedSpec {
	if s.Provider == nil {
		return execx.ManagedSpec{}
	}
	return s.Provider.Command(s.LocalPort, s.StateDir)
}

func (s *Service) Probe(ctx context.Context) error {
	command := s.Command()
	stdout, stdoutErr := os.ReadFile(command.StdoutPath)
	stderr, stderrErr := os.ReadFile(command.StderrPath)
	if stdoutErr != nil && !os.IsNotExist(stdoutErr) {
		return fmt.Errorf("read tunnel output: %w", stdoutErr)
	}
	if stderrErr != nil && !os.IsNotExist(stderrErr) {
		return fmt.Errorf("read tunnel output: %w", stderrErr)
	}
	output := append(stdout, stderr...)
	s.mu.RLock()
	publicURL := s.publicURL
	s.mu.RUnlock()
	var err error
	if publicURL == "" {
		publicURL, err = s.Provider.DiscoverURL(string(output))
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.publicURL = publicURL
		s.mu.Unlock()
	}
	if s.Requirements.SSE {
		path := s.EventPath
		if path == "" {
			path = "/api/event"
		}
		headers := map[string]string(nil)
		if provider, ok := s.Provider.(interface{ ProbeHeaders() map[string]string }); ok {
			headers = provider.ProbeHeaders()
		}
		return ProbeSSEWithHeaders(ctx, s.Client, strings.TrimRight(publicURL, "/")+"/"+strings.TrimLeft(path, "/"), s.Auth, headers, s.FirstFrame)
	}
	headers := map[string]string(nil)
	if provider, ok := s.Provider.(interface{ ProbeHeaders() map[string]string }); ok {
		headers = provider.ProbeHeaders()
	}
	_, err = (health.Prober{Client: s.Client}).Do(ctx, health.Request{URL: publicURL, Auth: s.Auth, Headers: headers, WantStatus: http.StatusOK})
	return err
}

func (s *Service) PublicURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.publicURL
}

func (s *Service) Cleanup(context.Context) error { return nil }

var _ supervisor.Service = (*Service)(nil)
