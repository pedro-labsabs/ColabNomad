package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadVersions(t *testing.T) {
	manifest := filepath.Join("..", "..", "config", "versions.json")
	got, err := LoadVersions(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got.BootstrapGo.Version != "1.27.1" || got.OpenCode.Version != "2.0.11" || got.TTYD.Version != "1.7.7" || got.Cloudflared.Version != "2026.9.1" {
		t.Fatalf("unexpected versions: %+v", got)
	}
	for _, tool := range []ToolSpec{got.BootstrapGo, got.OpenCode, got.TTYD, got.Cloudflared} {
		if len(tool.Artifacts) != 2 {
			t.Fatalf("expected both Linux architectures: %+v", tool)
		}
	}
}

func TestLoadVersionsMissingFile(t *testing.T) {
	if _, err := LoadVersions(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected missing manifest error")
	}
}

func TestPlatformKeyMatchesRuntime(t *testing.T) {
	got, err := PlatformKey(runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64") && (err != nil || got != runtime.GOOS+"-"+runtime.GOARCH) {
		t.Fatalf("%q %v", got, err)
	}
}
