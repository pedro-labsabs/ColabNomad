package app

import (
	"context"
	"errors"
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
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/browserauth"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/opencode"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/terminal"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/tunnel"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/state"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/supervisor"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/workspace"
)

type UpRequest struct {
	RepoURL, RepoRef, GitHubToken, OpenCodeAPIKey, VersionsPath string
	OpenCodeTunnel, TerminalTunnel                              config.TunnelProviderName
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

func applyTunnelAuth(service *tunnel.Service, username, password string) {
	service.Auth = &health.BasicAuth{Username: username, Password: password}
}

func selectOpenCodeTunnel(name config.TunnelProviderName, ssh string) (tunnel.Provider, error) {
	switch name {
	case config.TunnelLocalhostRun:
		return tunnel.NewLocalhostRun(ssh, ""), nil
	case config.TunnelServeo:
		return tunnel.NewServeo(ssh, ""), nil
	default:
		return nil, fmt.Errorf("opencode tunnel provider %q lacks SSE capability", name)
	}
}

func selectTerminalTunnel(name config.TunnelProviderName, ssh, cloudflared string) (tunnel.Provider, error) {
	switch name {
	case config.TunnelCloudflare:
		return tunnel.NewCloudflare(cloudflared), nil
	case config.TunnelServeo:
		return tunnel.NewServeo(ssh, ""), nil
	default:
		return nil, fmt.Errorf("unknown terminal tunnel provider %q", name)
	}
}

func newOpenCodeGatewayService(binary string, c config.RuntimeConfig, cred state.Credentials, sessionToken string) browserauth.Service {
	return browserauth.Service{
		Binary: binary, BackendPort: c.OpenCodePort, ListenPort: c.OpenCodeGatewayPort,
		Username: cred.OpenCodeUser, Password: cred.OpenCodePassword, SessionToken: sessionToken, Prober: health.Prober{},
	}
}

func newTunnelServices(c config.RuntimeConfig, stateDir string, openProvider, terminalProvider tunnel.Provider, gateway, terminalLocal supervisor.Service, cred state.Credentials) (*tunnel.Service, *tunnel.Service) {
	to := tunnel.NewService(openProvider, gateway, stateDir, c.OpenCodeGatewayPort)
	to.Label = "opencode"
	to.Requirements = tunnel.Requirements{SSE: true}
	to.FirstFrame = 10 * time.Second
	applyTunnelAuth(to, cred.OpenCodeUser, cred.OpenCodePassword)

	tt := tunnel.NewService(terminalProvider, terminalLocal, stateDir, c.TerminalPort)
	tt.Requirements = tunnel.Requirements{WebSocket: true}
	tt.FirstFrame = 10 * time.Second
	applyTunnelAuth(tt, cred.TerminalUser, cred.TerminalPassword)
	return to, tt
}

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
	Config            config.RuntimeConfig
	Store             state.Store
	Manager           *supervisor.Manager
	Probes            map[string]ProbeResult
	Credentials       state.Credentials
	LogContents       map[string]string
	Stop              func(context.Context) error
	VersionsPath      string
	Versions          config.Versions
	Compose           ComposeFunc
	Services          map[string]supervisor.Service
	Terminal          *terminal.Service
	EndpointServices  map[string]string
	WorkspacePath     string
	ToolChecks        map[string]error
	ProviderChecks    map[string]error
	ToolPaths         map[string]string
	RedactionSecrets  []string
	SupervisorContext context.Context
	endpointMu        sync.RWMutex
	endpointSyncMu    sync.Mutex
	endpointSyncErr   error
	running           bool
	downOnce          sync.Once
	downErr           error
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
		c.OpenCodeTunnel = config.TunnelLocalhostRun
	}
	if c.TerminalTunnel == "" {
		c.TerminalTunnel = config.TunnelCloudflare
	}
	if c.OpenCodePort == 0 {
		c.OpenCodePort = 4096
	}
	if c.OpenCodeGatewayPort == 0 {
		c.OpenCodeGatewayPort = 4097
	}
	if c.TerminalPort == 0 {
		c.TerminalPort = 7681
	}
	if r.running {
		if c.RepoURL != r.Config.RepoURL || c.RepoRef != r.Config.RepoRef || c.OpenCodeTunnel != r.Config.OpenCodeTunnel || c.TerminalTunnel != r.Config.TerminalTunnel || c.OpenCodePort != r.Config.OpenCodePort || c.OpenCodeGatewayPort != r.Config.OpenCodeGatewayPort || c.TerminalPort != r.Config.TerminalPort {
			return out, fmt.Errorf("runtime is already running with an incompatible configuration; down first")
		}
		return r.connectionSummary()
	}
	if c.OpenCodePort == c.TerminalPort || c.OpenCodePort == c.OpenCodeGatewayPort || c.OpenCodeGatewayPort == c.TerminalPort {
		return out, fmt.Errorf("configured service ports must be distinct")
	}
	if c.OpenCodeTunnel != config.TunnelServeo && c.OpenCodeTunnel != config.TunnelLocalhostRun {
		return out, fmt.Errorf("opencode tunnel provider %q lacks SSE capability", c.OpenCodeTunnel)
	}
	if c.TerminalTunnel != config.TunnelServeo && c.TerminalTunnel != config.TunnelCloudflare {
		return out, fmt.Errorf("unknown terminal tunnel provider %q", c.TerminalTunnel)
	}
	if err := validatePort(c.OpenCodePort); err != nil {
		return out, err
	}
	if err := validatePort(c.OpenCodeGatewayPort); err != nil {
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
	manifestPath := req.VersionsPath
	if manifestPath == "" {
		manifestPath = r.VersionsPath
	}
	if manifestPath == "" {
		manifestPath = os.Getenv("COLABNOMAD_VERSIONS_FILE")
	}
	if manifestPath != "" {
		versions, err := config.LoadVersions(manifestPath)
		if err != nil {
			return out, fmt.Errorf("load pinned versions manifest %q: %w", manifestPath, err)
		}
		r.Versions = versions
		r.VersionsPath = manifestPath
	} else if r.Versions.OpenCode.Version == "" && r.Compose == nil {
		return out, fmt.Errorf("COLABNOMAD_VERSIONS_FILE is required; pinned versions manifest path is not configured")
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
	manager := comp.Manager
	if manager != nil {
		lifetime := r.SupervisorContext
		if lifetime == nil {
			lifetime = ctx
		}
		if err := manager.StartAllWithLifetime(ctx, lifetime); err != nil {
			return out, err
		}
	}
	if comp.TerminalTunnel != nil {
		comp.Endpoints["terminal"] = comp.TerminalTunnel.PublicURL()
	}
	if comp.OpenCodeTunnel != nil {
		comp.Endpoints["opencode"] = comp.OpenCodeTunnel.PublicURL()
	}
	services := make(map[string]state.ServiceState)
	for _, name := range comp.Services {
		status := string(supervisor.Healthy)
		pid := 0
		if manager != nil {
			status = string(manager.State(name))
			pid = manager.ServicePID(name)
		}
		services[name] = state.ServiceState{Status: status, Port: servicePort(name, c), PID: pid}
	}
	if err := r.Store.Save(state.RuntimeState{SchemaVersion: 1, DaemonPID: os.Getpid(), WorkspacePath: comp.WorkspacePath, Services: services, Endpoints: comp.Endpoints, UpdatedAt: time.Now().UTC()}); err != nil {
		if manager != nil {
			if cleanupErr := manager.StopAll(context.Background()); cleanupErr != nil {
				return out, errors.Join(err, fmt.Errorf("cleanup started services: %w", cleanupErr))
			}
		}
		return out, err
	}
	r.Manager = manager
	r.WorkspacePath = comp.WorkspacePath
	if comp.ServiceMap != nil {
		r.Services = comp.ServiceMap
	}
	r.endpointMu.Lock()
	r.EndpointServices = copyEndpoints(comp.Endpoints)
	r.endpointSyncErr = nil
	r.endpointMu.Unlock()
	r.Config = c
	r.running = true
	if manager != nil {
		manager.SetHealthyCallback(r.handleHealthyService)
	}
	return r.connectionSummary()
}

func (r *Runtime) connectionSummary() (ConnectionSummary, error) {
	r.endpointMu.RLock()
	openCodeURL := r.EndpointServices["opencode"]
	terminalURL := r.EndpointServices["terminal"]
	r.endpointMu.RUnlock()
	return ConnectionSummary{OpenCodeURL: openCodeURL, TerminalURL: terminalURL, OpenCodeUser: r.Credentials.OpenCodeUser, OpenCodePassword: r.Credentials.OpenCodePassword, TerminalUser: r.Credentials.TerminalUser, TerminalPassword: r.Credentials.TerminalPassword}, nil
}

func copyEndpoints(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func endpointKeyForService(name string) (string, bool) {
	switch name {
	case "tunnel:opencode":
		return "opencode", true
	case "tunnel:terminal":
		return "terminal", true
	default:
		return "", false
	}
}

func (r *Runtime) handleHealthyService(name string) {
	key, ok := endpointKeyForService(name)
	if !ok {
		return
	}
	service := r.Services[name]
	endpointService, ok := service.(interface{ PublicURL() string })
	if !ok {
		return
	}
	current := endpointService.PublicURL()
	if current == "" {
		return
	}

	r.endpointSyncMu.Lock()
	defer r.endpointSyncMu.Unlock()

	r.endpointMu.Lock()
	if r.EndpointServices == nil {
		r.EndpointServices = make(map[string]string)
	}
	changed := r.EndpointServices[key] != current
	if changed {
		r.EndpointServices[key] = current
	}
	retryPersistence := r.endpointSyncErr != nil
	endpoints := copyEndpoints(r.EndpointServices)
	r.endpointMu.Unlock()
	if !changed && !retryPersistence {
		return
	}

	persisted, err := r.Store.Load()
	if err == nil {
		persisted.Endpoints = endpoints
		persisted.UpdatedAt = time.Now().UTC()
		err = r.Store.Save(persisted)
	}
	r.endpointMu.Lock()
	r.endpointSyncErr = err
	r.endpointMu.Unlock()
}

func servicePort(name string, c config.RuntimeConfig) int {
	if name == "opencode-gateway" || name == "tunnel:opencode" {
		return c.OpenCodeGatewayPort
	}
	if strings.Contains(name, "opencode") {
		return c.OpenCodePort
	}
	return c.TerminalPort
}

func resolveWorkspacePath(root, repoURL string) (string, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", fmt.Errorf("invalid repository URL")
	}
	name := filepath.Base(strings.TrimSuffix(u.Path, "/"))
	if name == "." || name == "" {
		name = "repo"
	}
	if name == ".." {
		return "", fmt.Errorf("workspace path escapes root: invalid repository path")
	}

	root = filepath.Clean(root)
	path := filepath.Join(root, name)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace path escapes root: invalid repository path")
	}
	return path, nil
}

func (r *Runtime) compose(ctx context.Context, c config.RuntimeConfig, req UpRequest) (*Composition, error) {
	if c.RepoURL == "" {
		return nil, fmt.Errorf("repository URL is required")
	}
	root := c.WorkspaceRoot
	if root == "" {
		root = filepath.Join(c.StateDir, "workspaces")
	}
	workspacePath, err := resolveWorkspacePath(root, c.RepoURL)
	if err != nil {
		return nil, err
	}
	wm := workspace.Manager{}
	wr, err := wm.Prepare(ctx, workspace.Request{RepoURL: c.RepoURL, Ref: c.RepoRef, Root: workspacePath, GitHubToken: req.GitHubToken})
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
	cloudflared := ""
	if c.TerminalTunnel == config.TunnelCloudflare {
		cloudflared = filepath.Join(binDir, "cloudflared")
		if err := ensure(r.Versions.Cloudflared, cloudflared); err != nil {
			return nil, fmt.Errorf("install cloudflared: %w", err)
		}
	}
	openTunnel, err := selectOpenCodeTunnel(c.OpenCodeTunnel, ssh)
	if err != nil {
		return nil, err
	}
	terminalTunnel, err := selectTerminalTunnel(c.TerminalTunnel, ssh, cloudflared)
	if err != nil {
		return nil, err
	}
	selfBinary, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve colabnomad executable: %w", err)
	}
	sessionToken, err := secure.RandomPassword(32)
	if err != nil {
		return nil, fmt.Errorf("generate browser session token: %w", err)
	}
	r.RedactionSecrets = append(r.RedactionSecrets, sessionToken)

	open := opencode.New(opencode.Config{Binary: opencodeBin, Workspace: wr.Path, StateDir: c.StateDir, Version: r.Versions.OpenCode.Version, APIKey: req.OpenCodeAPIKey, Username: r.Credentials.OpenCodeUser, Password: r.Credentials.OpenCodePassword, Port: c.OpenCodePort}, health.Prober{})
	gateway := newOpenCodeGatewayService(selfBinary, c, r.Credentials, sessionToken)
	term := &terminal.Service{Runner: execx.OSRunner{}, System: sys, Workspace: wr.Path, Username: r.Credentials.TerminalUser, Password: r.Credentials.TerminalPassword, Binary: ttydBin, Port: c.TerminalPort, Prober: health.Prober{}}
	r.Terminal = term
	r.ToolPaths = map[string]string{"tool:opencode": opencodeBin, "tool:ttyd": ttydBin, "tool:ssh": ssh}
	if cloudflared != "" {
		r.ToolPaths["tool:cloudflared"] = cloudflared
	}

	termLogged := loggedService{Service: term, dir: c.StateDir}
	openLogged := loggedService{Service: open, dir: c.StateDir}
	gatewayLogged := loggedService{Service: gateway, dir: c.StateDir}

	to, tt := newTunnelServices(c, c.StateDir, openTunnel, terminalTunnel, gatewayLogged, termLogged, r.Credentials)

	termService := supervisor.Service(termLogged)
	openService := supervisor.Service(openLogged)
	gatewayService := supervisor.Service(gatewayLogged)
	services := []supervisor.Service{termService, openService, gatewayService, tt, to}
	mgr := supervisor.NewManager(services, execx.OSProcessRunner{}, supervisor.RestartPolicy{})
	r.ProviderChecks = map[string]error{
		"provider:opencode": tunnel.Validate(openTunnel, tunnel.Requirements{SSE: true}),
		"provider:terminal": tunnel.Validate(terminalTunnel, tunnel.Requirements{WebSocket: true}),
	}
	return &Composition{
		WorkspacePath:  wr.Path,
		Services:       []string{"terminal", "opencode", "opencode-gateway", "tunnel:terminal", "tunnel:opencode"},
		Endpoints:      map[string]string{"terminal": tt.PublicURL(), "opencode": to.PublicURL()},
		Manager:        mgr,
		TerminalTunnel: tt,
		OpenCodeTunnel: to,
		ServiceMap: map[string]supervisor.Service{
			"terminal": termService, "opencode": openService, "opencode-gateway": gatewayService,
			"tunnel:terminal": tt, "tunnel:opencode": to,
		},
	}, nil
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
	if r.Manager == nil {
		for name, item := range v.Services {
			item.Status = "stale"
			item.PID = 0
			v.Services[name] = item
		}
	}
	if r.Manager != nil {
		for name, status := range r.Manager.Snapshot() {
			item := v.Services[name]
			item.Status = string(status)
			item.PID = r.Manager.ServicePID(name)
			v.Services[name] = item
		}
	}
	r.endpointMu.RLock()
	if len(r.EndpointServices) > 0 {
		v.Endpoints = make(map[string]string, len(r.EndpointServices))
		for k, value := range r.EndpointServices {
			v.Endpoints[k] = value
		}
	}
	r.endpointMu.RUnlock()
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
	r.endpointMu.RLock()
	endpointSyncErr := r.endpointSyncErr
	r.endpointMu.RUnlock()
	if endpointSyncErr != nil {
		d.Components["state:endpoints"] = "unhealthy"
		d.Healthy = false
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
		return fmt.Errorf("runtime is not reconstructed; run up or recover before restart")
	}
	return r.Manager.Restart(ctx, service)
}
func (r *Runtime) Down(ctx context.Context) error {
	r.downOnce.Do(func() {
		prior := false
		store := r.Store
		if store.Dir == "" {
			store.Dir = r.Config.StateDir
		}
		if saved, err := store.Load(); err == nil && len(saved.Services) > 0 {
			prior = true
		}
		if r.Stop != nil {
			r.downErr = r.Stop(ctx)
		} else if r.Manager != nil {
			r.downErr = r.Manager.StopAll(ctx)
		}
		if r.Terminal != nil {
			if err := r.Terminal.DestroySession(ctx); r.downErr == nil {
				r.downErr = err
			}
		} else if prior {
			if err := terminal.DestroyExistingSession(ctx); r.downErr == nil {
				r.downErr = err
			}
		}
		if saved, err := store.Load(); err == nil && prior && r.downErr == nil {
			for name, item := range saved.Services {
				item.Status, item.PID = string(supervisor.Stopped), 0
				saved.Services[name] = item
			}
			saved.Endpoints = map[string]string{}
			r.downErr = store.Save(saved)
		}
	})
	return r.downErr
}
func redacted(s string, secrets ...string) string {
	return strings.TrimSpace(secure.Redact(s, secrets...))
}
