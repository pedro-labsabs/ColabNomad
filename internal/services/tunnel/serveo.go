package tunnel

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

var serveoURLPattern = regexp.MustCompile(`https://[A-Za-z0-9-]+\.serveo\.net(?:[^\s]*)?`)

type Serveo struct {
	SSHPath        string
	KnownHostsPath string
}

func NewServeo(sshPath, knownHostsPath string) *Serveo {
	return &Serveo{SSHPath: sshPath, KnownHostsPath: knownHostsPath}
}

func (s *Serveo) Name() string               { return "serveo" }
func (s *Serveo) Capabilities() Capabilities { return Capabilities{SSE: true, WebSocket: true} }

func (s *Serveo) Command(localPort int, stateDir string) execx.ManagedSpec {
	knownHosts := s.KnownHostsPath
	if knownHosts == "" {
		knownHosts = filepath.Join(stateDir, "serveo_known_hosts")
	}
	return execx.ManagedSpec{
		Spec: execx.Spec{Path: s.SSHPath, Args: []string{
			"-T", "-o", "ExitOnForwardFailure=yes", "-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3",
			"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + knownHosts,
			"-R", fmt.Sprintf("80:127.0.0.1:%d", localPort), "serveo.net",
		}},
		StdoutPath: filepath.Join(stateDir, fmt.Sprintf("serveo.%d.stdout.log", localPort)),
		StderrPath: filepath.Join(stateDir, fmt.Sprintf("serveo.%d.stderr.log", localPort)),
	}
}

func (s *Serveo) DiscoverURL(output string) (string, error) {
	return discoverProviderURL(output, serveoURLPattern, "serveo.net")
}

func discoverProviderURL(output string, pattern *regexp.Regexp, suffix string) (string, error) {
	match := pattern.FindString(output)
	if match == "" {
		return "", fmt.Errorf("no %s tunnel URL found", suffix)
	}
	u, err := url.Parse(match)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" {
		return "", fmt.Errorf("invalid %s tunnel URL", suffix)
	}
	if !strings.HasSuffix(strings.ToLower(u.Hostname()), "."+suffix) {
		return "", fmt.Errorf("invalid %s tunnel URL", suffix)
	}
	return strings.TrimRight(match, "/"), nil
}
