package execx

import "time"

// ManagedSpec describes a long-lived command and its dedicated output files.
type ManagedSpec struct {
	Spec
	StdoutPath string
	StderrPath string
}

type ProcessHandle interface {
	PID() int
	Done() <-chan error
	Stop(grace time.Duration) error
}

type ProcessRunner interface {
	Start(ManagedSpec) (ProcessHandle, error)
}
