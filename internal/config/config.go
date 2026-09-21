package config

import (
	"fmt"
	"os"
)

type TunnelProviderName string

const (
	TunnelServeo     TunnelProviderName = "serveo"
	TunnelCloudflare TunnelProviderName = "cloudflare"
)

type RuntimeConfig struct {
	StateDir, WorkspaceRoot, RepoURL, RepoRef string
	OpenCodePort, TerminalPort                int
	OpenCodeTunnel, TerminalTunnel            TunnelProviderName
}

func Default(repoURL string) RuntimeConfig {
	stateDir := os.Getenv("COLABNOMAD_STATE_DIR")
	if stateDir == "" {
		stateDir = "/content/.colabnomad"
	}
	return RuntimeConfig{
		StateDir:       stateDir,
		WorkspaceRoot:  "/content/workspaces",
		RepoURL:        repoURL,
		RepoRef:        "",
		OpenCodePort:   4096,
		TerminalPort:   7681,
		OpenCodeTunnel: TunnelServeo,
		TerminalTunnel: TunnelServeo,
	}
}

func PlatformKey(goos, goarch string) (string, error) {
	if goos == "" || goarch == "" {
		return "", fmt.Errorf("platform must include both GOOS and GOARCH")
	}
	if goos != "linux" || (goarch != "amd64" && goarch != "arm64") {
		return "", fmt.Errorf("unsupported platform %q-%q", goos, goarch)
	}
	return goos + "-" + goarch, nil
}
