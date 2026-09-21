package tunnel

import (
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

var localhostRunURLPattern = regexp.MustCompile(`https://[A-Za-z0-9-]+\.lhr\.life(?:[^\s\x1b]*)?`)

type LocalhostRun struct {
	SSHPath        string
	KnownHostsPath string
	IdentityPath   string
}

func NewLocalhostRun(sshPath, knownHostsPath string) *LocalhostRun {
	return &LocalhostRun{SSHPath: sshPath, KnownHostsPath: knownHostsPath}
}

func (s *LocalhostRun) Name() string               { return "localhostrun" }
func (s *LocalhostRun) Capabilities() Capabilities { return Capabilities{SSE: true, WebSocket: true} }

func (s *LocalhostRun) Command(localPort int, stateDir string) execx.ManagedSpec {
	knownHosts := s.KnownHostsPath
	if knownHosts == "" {
		knownHosts = filepath.Join(stateDir, "localhostrun_known_hosts")
	}
	args := []string{
		"-T", "-o", "ExitOnForwardFailure=yes", "-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + knownHosts,
	}
	target := "nokey@localhost.run"
	if s.IdentityPath != "" {
		args = append(args, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-i", s.IdentityPath)
		target = "localhost.run"
	}
	args = append(args, "-R", fmt.Sprintf("80:127.0.0.1:%d", localPort), target)
	return execx.ManagedSpec{
		Spec:       execx.Spec{Path: s.SSHPath, Args: args},
		StdoutPath: filepath.Join(stateDir, fmt.Sprintf("localhostrun.%d.stdout.log", localPort)),
		StderrPath: filepath.Join(stateDir, fmt.Sprintf("localhostrun.%d.stderr.log", localPort)),
	}
}

func (s *LocalhostRun) DiscoverURL(output string) (string, error) {
	return discoverProviderURL(output, localhostRunURLPattern, "lhr.life")
}
