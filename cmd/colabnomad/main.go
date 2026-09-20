package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/app"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/config"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/control"
)

type cliOptions struct{ Command, Service, Repo, Ref, OpenCodeTunnel, TerminalTunnel, StateDir string }

func parseArgs(args []string) (cliOptions, error) {
	if len(args) == 0 {
		return cliOptions{}, fmt.Errorf("usage: colabnomad <up|status|doctor|logs|restart|down>")
	}
	o := cliOptions{Command: args[0], OpenCodeTunnel: "serveo", TerminalTunnel: "serveo", StateDir: "/content/.colabnomad"}
	switch o.Command {
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
		f.StringVar(&o.OpenCodeTunnel, "opencode-tunnel", "serveo", "opencode tunnel")
		f.StringVar(&o.TerminalTunnel, "terminal-tunnel", "serveo", "terminal tunnel")
		if err := f.Parse(args[1:]); err != nil {
			return o, err
		}
		if o.Repo == "" {
			return o, fmt.Errorf("--repo is required")
		}
		if o.OpenCodeTunnel != "serveo" {
			return o, fmt.Errorf("opencode tunnel provider %q lacks required SSE capability", o.OpenCodeTunnel)
		}
		if o.TerminalTunnel != "serveo" && o.TerminalTunnel != "cloudflare" {
			return o, fmt.Errorf("unknown terminal tunnel provider %q", o.TerminalTunnel)
		}
	case "daemon":
		f := flag.NewFlagSet("daemon", flag.ContinueOnError)
		f.SetOutput(os.Stderr)
		f.StringVar(&o.StateDir, "state-dir", o.StateDir, "state directory")
		if err := f.Parse(args[1:]); err != nil {
			return o, err
		}
	default:
		return o, fmt.Errorf("unknown command %q; usage: colabnomad up|status|doctor|logs|restart|down", o.Command)
	}
	return o, nil
}

func main() {
	o, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if o.Command == "daemon" {
		if err := runDaemon(o.StateDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	r := &app.Runtime{Config: config.Default(o.Repo)}
	r.Config.StateDir = o.StateDir
	c := control.Client{Socket: filepath.Join(o.StateDir, "control.sock")}
	payload, _ := json.Marshal(o)
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
	_ = context.Background()
}

func runDaemon(stateDir string) error {
	r := &app.Runtime{Config: config.RuntimeConfig{StateDir: stateDir}, Probes: map[string]app.ProbeResult{}}
	s, err := control.NewServer(stateDir, func(req control.Request) control.Response {
		switch req.Command {
		case "up":
			var v cliOptions
			if err := json.Unmarshal(req.Payload, &v); err != nil {
				return control.Response{Error: err.Error()}
			}
			summary, err := r.Up(context.Background(), app.UpRequest{RepoURL: v.Repo, RepoRef: v.Ref, OpenCodeTunnel: config.TunnelProviderName(v.OpenCodeTunnel), TerminalTunnel: config.TunnelProviderName(v.TerminalTunnel)})
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
			return control.Response{OK: true}
		default:
			return control.Response{Error: "unsupported daemon command"}
		}
	})
	if err != nil {
		return err
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return s.Serve(ctx)
}
