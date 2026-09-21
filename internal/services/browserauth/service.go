package browserauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/health"
)

const (
	EnvBackendURL    = "COLABNOMAD_GATEWAY_BACKEND_URL"
	EnvListenAddress = "COLABNOMAD_GATEWAY_LISTEN"
	EnvUsername      = "COLABNOMAD_GATEWAY_USERNAME"
	EnvPassword      = "COLABNOMAD_GATEWAY_PASSWORD"
	EnvSessionToken  = "COLABNOMAD_GATEWAY_SESSION_TOKEN"
)

type Service struct {
	Binary       string
	BackendPort  int
	ListenPort   int
	Username     string
	Password     string
	SessionToken string
	Prober       health.Prober
}

func (s Service) Name() string           { return "opencode-gateway" }
func (s Service) Dependencies() []string { return []string{"opencode"} }

func (s Service) Prepare(context.Context) error {
	if s.Binary == "" || s.BackendPort <= 0 || s.ListenPort <= 0 || s.Username == "" || s.Password == "" || s.SessionToken == "" {
		return fmt.Errorf("browser auth gateway configuration is incomplete")
	}
	return nil
}

func (s Service) Command() execx.ManagedSpec {
	return execx.ManagedSpec{Spec: execx.Spec{
		Path: s.Binary,
		Args: []string{"gateway"},
		Env: map[string]string{
			EnvBackendURL:    "http://127.0.0.1:" + strconv.Itoa(s.BackendPort),
			EnvListenAddress: "127.0.0.1:" + strconv.Itoa(s.ListenPort),
			EnvUsername:      s.Username,
			EnvPassword:      s.Password,
			EnvSessionToken:  s.SessionToken,
		},
	}}
}

func (s Service) Probe(ctx context.Context) error {
	_, err := s.Prober.Do(ctx, health.Request{URL: "http://127.0.0.1:" + strconv.Itoa(s.ListenPort) + HealthPath, WantStatus: http.StatusOK})
	return err
}

func (s Service) Cleanup(context.Context) error { return nil }

func RunFromEnvironment() error {
	h, err := NewHandler(Config{
		BackendURL:   os.Getenv(EnvBackendURL),
		Username:     os.Getenv(EnvUsername),
		Password:     os.Getenv(EnvPassword),
		SessionToken: os.Getenv(EnvSessionToken),
	})
	if err != nil {
		return err
	}
	listen := os.Getenv(EnvListenAddress)
	if err := validateLoopbackListenAddress(listen); err != nil {
		return err
	}
	return http.ListenAndServe(listen, h)
}

func validateLoopbackListenAddress(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil || !isLoopbackHost(host) {
		return fmt.Errorf("gateway listen address must be explicit loopback host:port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("gateway listen address has invalid port")
	}
	return nil
}
