package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/health"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/secure"
)

const requiredVersion = "opencode v2.0.11"

type Config struct {
	Binary, Workspace, StateDir, Version, Username, Password, APIKey string
	Port                                                             int
}

type Service struct {
	cfg    Config
	prober health.Prober
	runner execx.Runner

	// probeURL is only used to make package-level tests deterministic. In
	// normal operation it is always derived from the configured localhost port.
	probeURL string
}

func New(cfg Config, p health.Prober) *Service {
	if cfg.Username == "" {
		cfg.Username = "opencode"
	}
	return &Service{cfg: cfg, prober: p}
}

func (s *Service) Name() string           { return "opencode" }
func (s *Service) Dependencies() []string { return nil }

func (s *Service) configPath() string {
	return filepath.Join(s.cfg.StateDir, "opencode", "opencode.json")
}

func (s *Service) Prepare(ctx context.Context) error {
	if s.cfg.Binary == "" {
		return s.safeError("opencode binary is required")
	}
	if s.cfg.Workspace == "" {
		return s.safeError("opencode workspace is required")
	}
	if s.cfg.StateDir == "" {
		return s.safeError("opencode state directory is required")
	}
	if s.cfg.Password == "" {
		return s.safeError("opencode password is required")
	}
	path := s.configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return s.safeError("create opencode state: " + err.Error())
	}
	if err := os.WriteFile(path, []byte(`{"$schema":"https://opencode.ai/config.json"}`), 0600); err != nil {
		return s.safeError("write opencode config: " + err.Error())
	}
	r := s.runner
	if r == nil {
		r = execx.OSRunner{}
	}
	result, err := r.Run(ctx, execx.Spec{Path: s.cfg.Binary, Args: []string{"--version"}})
	if err != nil {
		return s.safeError("check opencode version: " + err.Error())
	}
	if strings.TrimSpace(result.Stdout) != requiredVersion {
		return s.safeError(fmt.Sprintf("unsupported opencode version %q, want %q", strings.TrimSpace(result.Stdout), requiredVersion))
	}
	return nil
}

func (s *Service) Command() execx.ManagedSpec {
	port := s.cfg.Port
	if port == 0 {
		port = 4096
	}
	env := map[string]string{
		"OPENCODE_CONFIG":             s.configPath(),
		"OPENCODE_DISABLE_AUTOUPDATE": "1",
		"OPENCODE_SERVER_USERNAME":    s.username(),
		"OPENCODE_SERVER_PASSWORD":    s.cfg.Password,
	}
	if s.cfg.APIKey != "" {
		env["OPENCODE_API_KEY"] = s.cfg.APIKey
	}
	return execx.ManagedSpec{Spec: execx.Spec{
		Path: s.cfg.Binary,
		Args: []string{"serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port)},
		Dir:  s.cfg.Workspace,
		Env:  env,
	}}
}

func (s *Service) Probe(ctx context.Context) error {
	port := s.cfg.Port
	if port == 0 {
		port = 4096
	}
	url := s.probeURL
	if url == "" {
		url = "http://127.0.0.1:" + strconv.Itoa(port) + "/api/project"
	}
	body, err := s.prober.Do(ctx, health.Request{
		URL: url, Auth: &health.BasicAuth{Username: s.username(), Password: s.cfg.Password},
		Headers:    map[string]string{"x-opencode-directory": s.cfg.Workspace},
		WantStatus: http.StatusOK, WantContentType: "application/json",
	})
	if err != nil {
		return s.safeError("probe opencode: " + err.Error())
	}
	var projects []struct {
		Canonical string `json:"canonical"`
	}
	if err := json.Unmarshal(body, &projects); err != nil {
		return s.safeError("decode opencode projects: " + err.Error())
	}
	for _, project := range projects {
		if project.Canonical == s.cfg.Workspace {
			return nil
		}
	}
	return s.safeError("opencode project readiness did not include workspace")
}

func (s *Service) Cleanup(context.Context) error { return nil }

func (s *Service) username() string {
	if s.cfg.Username != "" {
		return s.cfg.Username
	}
	return "opencode"
}

func (s *Service) safeError(message string) error {
	return errors.New(secure.Redact(message, s.cfg.Password, s.cfg.APIKey))
}

// StatusError provides the same redaction boundary for supervisor-facing
// status failures as the service's command and probe errors.
func (s *Service) StatusError(err error) error {
	if err == nil {
		return nil
	}
	return s.safeError(err.Error())
}

func (s *Service) String() string {
	return fmt.Sprintf("opencode service (binary=%q workspace=%q port=%d)", s.cfg.Binary, s.cfg.Workspace, s.cfg.Port)
}
