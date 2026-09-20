package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/config"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/secure"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/state"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/supervisor"
)

type UpRequest struct {
	RepoURL, RepoRef, GitHubToken, OpenCodeAPIKey string
	OpenCodeTunnel, TerminalTunnel                config.TunnelProviderName
}
type ConnectionSummary struct{ OpenCodeURL, TerminalURL, OpenCodeUser, OpenCodePassword, TerminalUser, TerminalPassword string }
type ProbeResult struct{ Err error }
type DoctorResult struct {
	Healthy    bool              `json:"healthy"`
	Components map[string]string `json:"components"`
}

type Runtime struct {
	Config      config.RuntimeConfig
	Store       state.Store
	Manager     *supervisor.Manager
	Probes      map[string]ProbeResult
	Credentials state.Credentials
	LogContents map[string]string
	Stop        func(context.Context) error
}

func (r *Runtime) Up(ctx context.Context, req UpRequest) (ConnectionSummary, error) {
	var out ConnectionSummary
	c := r.Config
	if c.OpenCodePort == 0 {
		c.OpenCodePort = 4096
	}
	if c.TerminalPort == 0 {
		c.TerminalPort = 7681
	}
	if err := validatePort(c.OpenCodePort); err != nil {
		return out, err
	}
	if err := validatePort(c.TerminalPort); err != nil {
		return out, err
	}
	if c.StateDir == "" {
		return out, fmt.Errorf("state directory is required")
	}
	if r.Store.Dir == "" {
		r.Store.Dir = c.StateDir
	}
	if err := os.MkdirAll(filepath.Join(c.StateDir, "logs"), 0700); err != nil {
		return out, err
	}
	cred := r.Credentials
	if cred.OpenCodeUser == "" {
		cred.OpenCodeUser = "opencode"
	}
	if cred.TerminalUser == "" {
		cred.TerminalUser = "terminal"
	}
	var err error
	if cred.OpenCodePassword == "" {
		cred.OpenCodePassword, err = secure.RandomPassword(32)
		if err != nil {
			return out, err
		}
	}
	if cred.TerminalPassword == "" {
		cred.TerminalPassword, err = secure.RandomPassword(32)
		if err != nil {
			return out, err
		}
	}
	// The state store contains no credentials; credentials are retained only in the daemon.
	r.Credentials = cred
	if r.Manager != nil {
		if err := r.Manager.StartAll(ctx); err != nil {
			return out, err
		}
	}
	if err := r.Store.Save(state.RuntimeState{SchemaVersion: 1, WorkspacePath: "", Services: map[string]state.ServiceState{}, Endpoints: map[string]string{}}); err != nil {
		return out, err
	}
	return ConnectionSummary{OpenCodeUser: cred.OpenCodeUser, OpenCodePassword: cred.OpenCodePassword, TerminalUser: cred.TerminalUser, TerminalPassword: cred.TerminalPassword}, nil
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %d", port)
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return fmt.Errorf("port %d already in use", port)
	}
	return l.Close()
}
func (r *Runtime) Status() state.RuntimeState { v, _ := r.Store.Load(); return v }
func (r *Runtime) Doctor(context.Context) DoctorResult {
	d := DoctorResult{Healthy: true, Components: map[string]string{}}
	for n, p := range r.Probes {
		if p.Err != nil {
			d.Components[n] = "unhealthy"
			d.Healthy = false
		} else {
			d.Components[n] = "healthy"
		}
	}
	if r.Config.StateDir != "" {
		if _, err := os.Stat(r.Config.StateDir); err != nil {
			d.Components["state_dir"] = "unhealthy"
			d.Healthy = false
		} else {
			d.Components["state_dir"] = "healthy"
		}
	}
	return d
}
func (r *Runtime) Logs(service string) string {
	return secure.Redact(r.LogContents[service], r.Credentials.OpenCodePassword, r.Credentials.TerminalPassword)
}
func (r *Runtime) Restart(ctx context.Context, service string) error {
	if r.Manager == nil {
		return fmt.Errorf("daemon is not running")
	}
	return r.Manager.Restart(ctx, service)
}
func (r *Runtime) Down(ctx context.Context) error {
	if r.Stop != nil {
		return r.Stop(ctx)
	}
	if r.Manager != nil {
		return r.Manager.StopAll(ctx)
	}
	return nil
}
func redacted(s string, secrets ...string) string {
	return strings.TrimSpace(secure.Redact(s, secrets...))
}
