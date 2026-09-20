package state

import "time"

type ServiceState struct {
	PID    int    `json:"pid"`
	Port   int    `json:"port,omitempty"`
	Status string `json:"status,omitempty"`
}

type RuntimeState struct {
	SchemaVersion int                     `json:"schema_version"`
	DaemonPID     int                     `json:"daemon_pid"`
	WorkspacePath string                  `json:"workspace_path"`
	Services      map[string]ServiceState `json:"services"`
	Endpoints     map[string]string       `json:"endpoints"`
	UpdatedAt     time.Time               `json:"updated_at"`
}

type Credentials struct {
	OpenCodeUser     string `json:"opencode_user"`
	OpenCodePassword string `json:"opencode_password"`
	TerminalUser     string `json:"terminal_user"`
	TerminalPassword string `json:"terminal_password"`
}
