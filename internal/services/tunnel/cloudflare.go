package tunnel

import (
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

var cloudflareURLPattern = regexp.MustCompile(`https://[A-Za-z0-9-]+\.trycloudflare\.com(?:[^\s]*)?`)

type Cloudflare struct{ Binary string }

func NewCloudflare(binary string) *Cloudflare { return &Cloudflare{Binary: binary} }

func (c *Cloudflare) Name() string               { return "cloudflare" }
func (c *Cloudflare) Capabilities() Capabilities { return Capabilities{WebSocket: true} }

func (c *Cloudflare) Command(localPort int, stateDir string) execx.ManagedSpec {
	return execx.ManagedSpec{
		Spec:       execx.Spec{Path: c.Binary, Args: []string{"tunnel", "--no-autoupdate", "--url", fmt.Sprintf("http://127.0.0.1:%d", localPort)}},
		StdoutPath: filepath.Join(stateDir, fmt.Sprintf("cloudflare.%d.stdout.log", localPort)),
		StderrPath: filepath.Join(stateDir, fmt.Sprintf("cloudflare.%d.stderr.log", localPort)),
	}
}

func (c *Cloudflare) DiscoverURL(output string) (string, error) {
	return discoverProviderURL(output, cloudflareURLPattern, "trycloudflare.com")
}
