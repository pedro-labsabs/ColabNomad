package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/app"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/config"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/control"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/services/browserauth"
)

type cliOptions struct{ Command, Service, Repo, Ref, OpenCodeTunnel, TerminalTunnel, StateDir string }

const startupTimeout = 10 * time.Minute

func boundedStartupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, startupTimeout)
}

func runDaemonUp(parent context.Context, r *app.Runtime, request app.UpRequest) (app.ConnectionSummary, error) {
	startupCtx, cancelStartup := boundedStartupContext(parent)
	defer cancelStartup()
	return r.Up(startupCtx, request)
}

func cliUsage() string {
	return "usage: colabnomad <up|status|doctor|logs|restart|down>"
}

func controlTimeout(command string) time.Duration {
	switch command {
	case "up":
		return 10 * time.Minute
	case "doctor":
		return 45 * time.Second
	default:
		return 5 * time.Second
	}
}

func requestPayload(o cliOptions) []byte {
	if o.Command == "up" {
		b, _ := json.Marshal(app.UpRequest{RepoURL: o.Repo, RepoRef: o.Ref, GitHubToken: os.Getenv("GITHUB_TOKEN"), OpenCodeAPIKey: os.Getenv("OPENCODE_API_KEY"), LocalhostRunSSHPrivateKey: os.Getenv("LOCALHOST_RUN_SSH_PRIVATE_KEY"), VersionsPath: os.Getenv("COLABNOMAD_VERSIONS_FILE"), OpenCodeTunnel: config.TunnelProviderName(o.OpenCodeTunnel), TerminalTunnel: config.TunnelProviderName(o.TerminalTunnel)})
		return b
	}
	b, _ := json.Marshal(o)
	return b
}

func serializedHandler(handler control.Handler) control.Handler {
	var mu sync.Mutex
	return func(req control.Request) control.Response { mu.Lock(); defer mu.Unlock(); return handler(req) }
}

func parseArgs(args []string) (cliOptions, error) {
	if len(args) == 0 {
		return cliOptions{}, fmt.Errorf("%s", cliUsage())
	}
	stateDir := os.Getenv("COLABNOMAD_STATE_DIR")
	if stateDir == "" {
		stateDir = "/content/.colabnomad"
	}
	o := cliOptions{Command: args[0], OpenCodeTunnel: "localhostrun", TerminalTunnel: "cloudflare", StateDir: stateDir}
	switch o.Command {
	case "--help", "-h", "help":
		if len(args) != 1 {
			return o, fmt.Errorf("usage: %s", o.Command)
		}
		o.Command = "help"
	case "status", "doctor", "down":
		if len(args) != 1 {
			return o, fmt.Errorf("usage: %s", o.Command)
		}
	case "logs", "restart":
		if len(args) != 2 {
			return o, fmt.Errorf("usage: %s <service>", o.Command)
		}
		o.Service = args[1]
	case "up":
		f := flag.NewFlagSet("up", flag.ContinueOnError)
		f.SetOutput(os.Stderr)
		f.StringVar(&o.Repo, "repo", "", "repository URL")
		f.StringVar(&o.Ref, "ref", "", "repository ref")
		f.StringVar(&o.OpenCodeTunnel, "opencode-tunnel", "localhostrun", "opencode tunnel")
		f.StringVar(&o.TerminalTunnel, "terminal-tunnel", "cloudflare", "terminal tunnel")
		f.StringVar(&o.StateDir, "state-dir", o.StateDir, "state directory")
		if err := f.Parse(args[1:]); err != nil {
			return o, err
		}
		if o.Repo == "" {
			return o, fmt.Errorf("--repo is required")
		}
		if o.OpenCodeTunnel != "serveo" && o.OpenCodeTunnel != "localhostrun" {
			return o, fmt.Errorf("opencode tunnel provider %q lacks required SSE capability", o.OpenCodeTunnel)
		}
		if o.TerminalTunnel != "serveo" && o.TerminalTunnel != "cloudflare" {
			return o, fmt.Errorf("unknown terminal tunnel provider %q", o.TerminalTunnel)
		}
	case "gateway":
		if len(args) != 1 {
			return o, fmt.Errorf("usage: gateway")
		}
	case "daemon":
		f := flag.NewFlagSet("daemon", flag.ContinueOnError)
		f.SetOutput(os.Stderr)
		f.StringVar(&o.StateDir, "state-dir", o.StateDir, "state directory")
		if err := f.Parse(args[1:]); err != nil {
			return o, err
		}
	default:
		return o, fmt.Errorf("unknown command %q; %s", o.Command, cliUsage())
	}
	return o, nil
}

func main() {
	o, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if o.Command == "help" {
		fmt.Println(cliUsage())
		return
	}
	if o.Command == "gateway" {
		if err := browserauth.RunFromEnvironment(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if o.Command == "daemon" {
		if err := runDaemon(o.StateDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	c := control.Client{Socket: filepath.Join(o.StateDir, "control.sock"), Timeout: controlTimeout(o.Command)}
	payload := requestPayload(o)
	if err := control.EnsureDaemon(o.StateDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	resp, err := c.Do(control.Request{Command: o.Command, Payload: payload})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(resp.Payload) > 0 {
		fmt.Println(string(resp.Payload))
	}
	if o.Command == "doctor" {
		var diagnosis struct {
			Healthy bool `json:"healthy"`
		}
		if json.Unmarshal(resp.Payload, &diagnosis) == nil && !diagnosis.Healthy {
			os.Exit(1)
		}
	}
}

func newDaemonRuntime(stateDir string) *app.Runtime {
	c := config.Default("")
	c.StateDir = stateDir
	return &app.Runtime{Config: c, Probes: map[string]app.ProbeResult{}}
}

func runDaemon(stateDir string) error {
	r := newDaemonRuntime(stateDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.SupervisorContext = ctx
	s, err := control.NewServer(stateDir, serializedHandler(func(req control.Request) control.Response {
		switch req.Command {
		case "identity":
			binaryIdentity, err := control.CurrentBinaryIdentity()
			if err != nil {
				return control.Response{Error: err.Error()}
			}
			b, _ := json.Marshal(struct {
				Binary string `json:"binary"`
			}{Binary: binaryIdentity})
			return control.Response{OK: true, Payload: b}
		case "up":
			var v app.UpRequest
			if err := json.Unmarshal(req.Payload, &v); err != nil {
				return control.Response{Error: err.Error()}
			}
			summary, err := runDaemonUp(context.Background(), r, v)
			if err != nil {
				return control.Response{Error: err.Error()}
			}
			b, _ := json.Marshal(summary)
			return control.Response{OK: true, Payload: b}
		case "status":
			b, _ := json.Marshal(r.Status())
			return control.Response{OK: true, Payload: b}
		case "doctor":
			b, _ := json.Marshal(r.Doctor(context.Background()))
			return control.Response{OK: true, Payload: b}
		case "logs":
			var v struct {
				Service string `json:"service"`
			}
			_ = json.Unmarshal(req.Payload, &v)
			return control.Response{OK: true, Payload: json.RawMessage(strconv.Quote(r.Logs(v.Service)))}
		case "restart":
			var v struct {
				Service string `json:"service"`
			}
			_ = json.Unmarshal(req.Payload, &v)
			if err := r.Restart(context.Background(), v.Service); err != nil {
				return control.Response{Error: err.Error()}
			}
			return control.Response{OK: true}
		case "down":
			if err := r.Down(context.Background()); err != nil {
				return control.Response{Error: err.Error()}
			}
			cancel()
			return control.Response{OK: true}
		default:
			return control.Response{Error: "unsupported daemon command"}
		}
	}))
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Serve(ctx)
}
