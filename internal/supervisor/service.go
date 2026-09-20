package supervisor

import (
	"context"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

type Service interface {
	Name() string
	Dependencies() []string
	Prepare(context.Context) error
	Command() execx.ManagedSpec
	Probe(context.Context) error
	Cleanup(context.Context) error
}

type RestartPolicy struct {
	MaxRestarts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type State string

const (
	Stopped   State = "stopped"
	Starting  State = "starting"
	Healthy   State = "healthy"
	Unhealthy State = "unhealthy"
)
