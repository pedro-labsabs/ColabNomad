package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Artifact struct {
	URL       string `json:"url"`
	Integrity string `json:"integrity"`
	Member    string `json:"member,omitempty"`
}

type ToolSpec struct {
	Version   string              `json:"version"`
	Artifacts map[string]Artifact `json:"artifacts"`
}

type Versions struct {
	BootstrapGo ToolSpec `json:"bootstrap_go"`
	OpenCode    ToolSpec `json:"opencode"`
	TTYD        ToolSpec `json:"ttyd"`
	Cloudflared ToolSpec `json:"cloudflared"`
}

func LoadVersions(path string) (Versions, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Versions{}, fmt.Errorf("read versions manifest: %w", err)
	}
	var versions Versions
	if err := json.Unmarshal(data, &versions); err != nil {
		return Versions{}, fmt.Errorf("decode versions manifest: %w", err)
	}
	return versions, nil
}
