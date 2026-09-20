package tunnel

import (
	"fmt"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

type Capabilities struct{ SSE, WebSocket bool }
type Requirements struct{ SSE, WebSocket bool }

type Provider interface {
	Name() string
	Capabilities() Capabilities
	Command(localPort int, stateDir string) execx.ManagedSpec
	DiscoverURL(output string) (string, error)
}

func Validate(provider Provider, requirements Requirements) error {
	if provider == nil {
		return fmt.Errorf("tunnel provider is required")
	}
	capabilities := provider.Capabilities()
	if requirements.SSE && !capabilities.SSE {
		return fmt.Errorf("tunnel provider %q does not support SSE", provider.Name())
	}
	if requirements.WebSocket && !capabilities.WebSocket {
		return fmt.Errorf("tunnel provider %q does not support WebSocket", provider.Name())
	}
	return nil
}
