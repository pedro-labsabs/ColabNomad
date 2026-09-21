package app

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/artifact"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/config"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/deps"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/health"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/secure"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/opencode"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/terminal"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/tunnel"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/state"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/supervisor"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/workspace"
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
type Composition struct {
	WorkspacePath                  string
	Services                       []string
	Endpoints                      map[string]string
	Manager                        *supervisor.Manager
	TerminalTunnel, OpenCodeTunnel *tunnel.Service
	ServiceMap                     map[string]supervisor.Service
}
type ComposeFunc func(context.Context, UpRequest) (*Composition, error)

type loggedService struct {
	supervisor.Service
	dir string
}

func (s loggedService) Command() execx.ManagedSpec {
	spec := s.Service.Command()
	if spec.StdoutPath == "" {
		spec.StdoutPath = filepath.Join(s.dir, "logs", s.Name()+".stdout.log")
	}
	if spec.StderrPath == "" {
		spec.StderrPath = filepath.Join(s.dir, "logs", s.Name()+".stderr.log")
	}
	return spec
}

type Runtime struct {
	Config           config.RuntimeConfig
	Store            state.Store
	Manager          *supervisor.Manager
	Probes           map[string]ProbeResult
	Credentials      state.Credentials
	LogContents      map[string]string
	Stop             func(context.Context) error
	VersionsPath     string
	Versions         config.Versions
	Compose          ComposeFunc
	Services         map[string]supervisor.Service
	Terminal         *terminal.Service
	EndpointServices map[string]string
	WorkspacePath    string
	ToolChecks       map[string]error
	ProviderChecks   map[string]error
	ToolPaths        map[string]string
	RedactionSecrets []string
	running          bool
	downOnce         sync.Once
	downErr          error
}

func (r *Runtime) Up(ctx context.Context, req UpRequest) (ConnectionSummary, error) {
	var out ConnectionSummary
	c := r.Config
	if req.RepoURL != "" {
		c.RepoURL = req.RepoURL
	}
	if req.RepoRef != "" {
		c.RepoRef = req.RepoRef
	}
	if req.OpenCodeTunnel != "" {
		c.OpenCodeTunnel = req.OpenCodeTunnel
	}
	if req.TerminalTunnel != "" {
		c.TerminalTunnel = req.TerminalTunnel
	}
	if c.OpenCodeTunnel == "" {
		c.OpenCodeTunnel = config.TunnelServeo
	}
	if c.TerminalTunnel == "" {
		c.TerminalTunnel = config.TunnelServeo
	}
	if c.OpenCodePort == 0 {
		c.OpenCodePort = 4096
	}
	if c.TerminalPort == 0 {
		c.TerminalPort = 7681
	}
	if r.running {
		if c.RepoURL != r.Config.RepoURL || c.RepoRef != r.Config.RepoRef || c.OpenCodeTunnel != r.Config.OpenCodeTunnel || c.TerminalTunnel != r.Config.TerminalTunnel || c.OpenCodePort != r.Config.OpenCodePort || c.TerminalPort != r.Config.TerminalPort {
			return out, fmt.Errorf("runtime is already running with an incompatible configuration; down first")
		}
		return r.connectionSummary()
	}
	if c.OpenCodePort == c.TerminalPort {
		return out, fmt.Errorf("configured service ports must be distinct")
	}
	if c.OpenCodeTunnel != config.TunnelServeo {
		return out, fmt.Errorf("opencode tunnel provider %q lacks SSE capability", c.OpenCodeTunnel)
	}
	if c.TerminalTunnel != config.TunnelServeo && c.TerminalTunnel != config.TunnelCloudflare {
		return out, fmt.Errorf("unknown terminal tunnel provider %q", c.TerminalTunnel)
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
	manifestPath := r.VersionsPath
	if manifestPath == "" {
		manifestPath = os.Getenv("COLABNOMAD_VERSIONS_FILE")
	}
	if r.Versions.OpenCode.Version == "" {
		if manifestPath == "" {
			if r.Compose == nil {
				return out, fmt.Errorf("COLABNOMAD_VERSIONS_FILE is required; pinned versions manifest path is not configured")
			}
		} else {
			versions, err := config.LoadVersions(manifestPath)
			if err != nil {
				return out, fmt.Errorf("load pinned versions manifest %q: %w", manifestPath, err)
			}
			r.Versions = versions
		}
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
	if existing, loadErr := r.Store.LoadCredentials(); loadErr == nil {
		cred = existing
	} else if saveErr := r.Store.SaveCredentials(cred); saveErr != nil {
		return out, saveErr
	}
	// The runtime state contains no credentials; credentials remain in the protected credential file and response.
	r.Credentials = cred
	r.RedactionSecrets = []string{req.GitHubToken, req.OpenCodeAPIKey, cred.OpenCodePassword, cred.TerminalPassword}
	var comp *Composition
	if r.Compose != nil {
		comp, err = r.Compose(ctx, req)
	} else {
		comp, err = r.compose(ctx, c, req)
	}
	if err != nil {
		return out, err
	}
	if comp.Manager != nil {
		if err := comp.Manager.StartAll(ctx); err != nil {
			return out, err
		}
		r.Manager = comp.Manager
	}
	r.WorkspacePath = comp.WorkspacePath
	if comp.ServiceMap != nil {
		r.Services = comp.ServiceMap
	}
	if comp.TerminalTunnel != nil {
		comp.Endpoints["terminal"] = comp.TerminalTunnel.PublicURL()
	}
	if comp.OpenCodeTunnel != nil {
		comp.Endpoints["opencode"] = comp.OpenCodeTunnel.PublicURL()
	}
	r.EndpointServices = comp.Endpoints
	services := make(map[string]state.ServiceState)
	for _, name := range comp.Services {
		status := string(supervisor.Healthy)
		pid := 0
		if r.Manager != nil {
			status = string(r.Manager.State(name))
			pid = r.Manager.ServicePID(name)
		}
		services[name] = state.ServiceState{Status: status, Port: servicePort(name, c), PID: pid}
	}
	if err := r.Store.Save(state.RuntimeState{SchemaVersion: 1, DaemonPID: os.Getpid(), WorkspacePath: comp.WorkspacePath, Services: services, Endpoints: comp.Endpoints, UpdatedAt: time.Now().UTC()}); err != nil {
		return out, err
	}
	r.Config = c
	r.running = true
	return r.connectionSummary()
}

func (r *Runtime) connectionSummary() (ConnectionSummary, error) {
	return ConnectionSummary{OpenCodeURL: r.EndpointServices["opencode"], TerminalURL: r.EndpointServices["terminal"], OpenCodeUser: r.Credentials.OpenCodeUser, OpenCodePassword: r.Credentials.OpenCodePassword, TerminalUser: r.Credentials.TerminalUser, TerminalPassword: r.Credentials.TerminalPassword}, nil
}

func servicePort(name string, c config.RuntimeConfig) int {
	if strings.Contains(name, "opencode") {
		return c.OpenCodePort
	}
	return c.TerminalPort
}

func (r *Runtime) compose(ctx context.Context, c config.RuntimeConfig, req UpRequest) (*Composition, error) {
	if c.RepoURL == "" {
		return nil, fmt.Errorf("repository URL is required")
	}
	root := c.WorkspaceRoot
	if root == "" {
		root = filepath.Join(c.StateDir, "workspaces")
	}
	u, err := url.Parse(c.RepoURL)
	if err != nil {
		return nil, fmt.Errorf("parse repository URL: %w", err)
	}
	name := filepath.Base(strings.TrimSuffix(u.Path, "/"))
	if name == "." || name == "" {
		name = "repo"
	}
	wm := workspace.Manager{}
	wr, err := wm.Prepare(ctx, workspace.Request{RepoURL: c.RepoURL, Ref: c.RepoRef, Root: filepath.Join(root, name), GitHubToken: req.GitHubToken})
	if err != nil {
		return nil, err
	}
	key, err := config.PlatformKey(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	installer := artifact.Installer{}
	binDir := filepath.Join(c.StateDir, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		return nil, err
	}
	ensure := func(spec config.ToolSpec, path string) error {
		a, ok := spec.Artifacts[key]
		if !ok {
			return fmt.Errorf("missing pinned artifact for %s", key)
		}
		return installer.Ensure(ctx, a, path)
	}
	opencodeBin := filepath.Join(binDir, "opencode")
	ttydBin := filepath.Join(binDir, "ttyd")
	if err := ensure(r.Versions.OpenCode, opencodeBin); err != nil {
		return nil, fmt.Errorf("install opencode: %w", err)
	}
	if err := ensure(r.Versions.TTYD, ttydBin); err != nil {
		return nil, fmt.Errorf("install ttyd: %w", err)
	}
	sys := &deps.System{}
	ssh, err := sys.Ensure(ctx, "ssh", "openssh-client")
	if err != nil {
		return nil, err
	}
	var terminalTunnel tunnel.Provider = tunnel.NewServeo(ssh, "")
	if c.TerminalTunnel == config.TunnelCloudflare {
		cloud := filepath.Join(binDir, "cloudflared")
		if err := ensure(r.Versions.Cloudflared, cloud); err != nil {
			return nil, fmt.Errorf("install cloudflared: %w", err)
		}
		terminalTunnel = tunnel.NewCloudflare(cloud)
	}
	open := opencode.New(opencode.Config{Binary: opencodeBin, Workspace: wr.Path, StateDir: c.StateDir, Version: r.Versions.OpenCode.Version, APIKey: req.OpenCodeAPIKey, Username: r.Credentials.OpenCodeUser, Password: r.Credentials.OpenCodePassword, Port: c.OpenCodePort}, health.Prober{})
	term := &terminal.Service{Runner: execx.OSRunner{}, System: sys, Workspace: wr.Path, Username: r.Credentials.TerminalUser, Password: r.Credentials.TerminalPassword, Binary: ttydBin, Port: c.TerminalPort, Prober: health.Prober{}}
	r.Terminal = term
	r.ToolPaths = map[string]string{"tool:opencode": opencodeBin, "tool:ttyd": ttydBin, "tool:ssh": ssh}
	if c.TerminalTunnel == config.TunnelCloudflare {
		r.ToolPaths["tool:cloudflared"] = filepath.Join(binDir, "cloudflared")
	}
	to := tunnel.NewService(tunnel.NewServeo(ssh, ""), open, c.StateDir, c.OpenCodePort)
	to.Requirements = tunnel.Requirements{SSE: true}
	to.FirstFrame = 10 * time.Second
	tt := tunnel.NewService(terminalTunnel, term, c.StateDir, c.TerminalPort)
	tt.Requirements = tunnel.Requirements{WebSocket: true}
	tt.FirstFrame = 10 * time.Second
	termLogged := loggedService{Service: term, dir: c.StateDir}
	openLogged := loggedService{Service: open, dir: c.StateDir}
	tt.Local = termLogged
	to.Local = openLogged
	termService := supervisor.Service(termLogged)
	openService := supervisor.Service(openLogged)
	services := []supervisor.Service{termService, openService, tt, to}
	mgr := supervisor.NewManager(services, execx.OSProcessRunner{}, supervisor.RestartPolicy{})
	r.ProviderChecks = map[string]error{"provider:opencode": tunnel.Validate(tunnel.NewServeo(ssh, ""), tunnel.Requirements{SSE: true}), "provider:terminal": tunnel.Validate(terminalTunnel, tunnel.Requirements{WebSocket: true})}
	return &Composition{WorkspacePath: wr.Path, Services: []string{"terminal", "opencode", "tunnel:terminal", "tunnel:opencode"}, Endpoints: map[string]string{"terminal": tt.PublicURL(), "opencode": to.PublicURL()}, Manager: mgr, TerminalTunnel: tt, OpenCodeTunnel: to, ServiceMap: map[string]supervisor.Service{"terminal": termService, "opencode": openService, "tunnel:terminal": tt, "tunnel:opencode": to}}, nil
}

func preflightPortFree(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %d", port)
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return fmt.Errorf("port %d already in use", port)
	}
	return l.Close()
}
func validatePort(port int) error { return preflightPortFree(port) }
func portListening(port int) bool {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return true
	}
	_ = l.Close()
	return false
}
func (r *Runtime) Status() state.RuntimeState {
	store := r.Store
	if store.Dir == "" {
		store.Dir = r.Config.StateDir
	}
	v, _ := store.Load()
	if r.Manager != nil {
		for name, status := range r.Manager.Snapshot() {
			item := v.Services[name]
			item.Status = string(status)
			item.PID = r.Manager.ServicePID(name)
			v.Services[name] = item
		}
	}
	if len(r.EndpointServices) > 0 {
		v.Endpoints = make(map[string]string, len(r.EndpointServices))
		for k, value := range r.EndpointServices {
			v.Endpoints[k] = value
		}
	}
	return v
}
func (r *Runtime) Doctor(ctx context.Context) DoctorResult {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	d := DoctorResult{Healthy: true, Components: map[string]string{}}
	for n, p := range r.Probes {
		if p.Err != nil {
			d.Components[n] = "unhealthy"
			d.Healthy = false
		} else {
			d.Components[n] = "healthy"
		}
	}
	for n, service := range r.Services {
		if err := service.Probe(ctx); err != nil {
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
		if _, err := os.Stat(filepath.Join(r.Config.StateDir, "control.sock")); err != nil {
			d.Components["control_socket"] = "unhealthy"
			d.Healthy = false
		} else {
			d.Components["control_socket"] = "healthy"
		}
	}
	if r.WorkspacePath != "" {
		if _, err := os.Stat(r.WorkspacePath); err != nil {
			d.Components["workspace"] = "unhealthy"
			d.Healthy = false
		} else {
			d.Components["workspace"] = "healthy"
		}
	}
	for name, err := range r.ToolChecks {
		if err != nil {
			d.Components[name] = "unhealthy"
			d.Healthy = false
		} else {
			d.Components[name] = "healthy"
		}
	}
	for name, path := range r.ToolPaths {
		if _, err := os.Stat(path); err != nil {
			d.Components[name] = "unhealthy"
			d.Healthy = false
		} else {
			d.Components[name] = "healthy"
		}
	}
	for name, err := range r.ProviderChecks {
		if err != nil {
			d.Components[name] = "unhealthy"
			d.Healthy = false
		} else {
			d.Components[name] = "healthy"
		}
	}
	if r.Config.OpenCodePort != 0 {
		if portListening(r.Config.OpenCodePort) && componentHealthy(d, "opencode") {
			d.Components["port:opencode"] = "healthy"
		} else {
			d.Components["port:opencode"] = "unhealthy"
			d.Healthy = false
		}
	}
	if r.Config.TerminalPort != 0 {
		if portListening(r.Config.TerminalPort) && componentHealthy(d, "terminal") {
			d.Components["port:terminal"] = "healthy"
		} else {
			d.Components["port:terminal"] = "unhealthy"
			d.Healthy = false
		}
	}
	return d
}
func componentHealthy(d DoctorResult, name string) bool { return d.Components[name] == "healthy" }
func (r *Runtime) Logs(service string) string {
	var text string
	for _, suffix := range []string{".stdout.log", ".stderr.log"} {
		if b, err := os.ReadFile(filepath.Join(r.Config.StateDir, "logs", service+suffix)); err == nil {
			text += string(b)
		}
	}
	if text == "" {
		text = r.LogContents[service]
	}
	if len(text) > 64*1024 {
		text = text[len(text)-64*1024:]
	}
	return secure.Redact(text, append([]string{r.Credentials.OpenCodePassword, r.Credentials.TerminalPassword}, r.RedactionSecrets...)...)
}
func (r *Runtime) Restart(ctx context.Context, service string) error {
	if r.Manager == nil {
		return fmt.Errorf("daemon is not running")
	}
	return r.Manager.Restart(ctx, service)
}
func (r *Runtime) Down(ctx context.Context) error {
	r.downOnce.Do(func() {
		if r.Stop != nil {
			r.downErr = r.Stop(ctx)
		} else if r.Manager != nil {
			r.downErr = r.Manager.StopAll(ctx)
		}
		if r.Terminal != nil {
			if err := r.Terminal.DestroySession(ctx); r.downErr == nil {
				r.downErr = err
			}
		}
	})
	return r.downErr
}
func redacted(s string, secrets ...string) string {
	return strings.TrimSpace(secure.Redact(s, secrets...))
}
