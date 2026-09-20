package app

import "context"

// CheckDoctor is the injectable doctor entry point used by the daemon and CLI.
func (r *Runtime) CheckDoctor(ctx context.Context) DoctorResult { return r.Doctor(ctx) }
