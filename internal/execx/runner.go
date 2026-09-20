package execx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

type Spec struct {
	Path string
	Args []string
	Dir  string
	Env  map[string]string
}
type Result struct {
	Stdout, Stderr string
	ExitCode       int
}
type Runner interface {
	Run(context.Context, Spec) (Result, error)
	LookPath(string) (string, error)
}
type OSRunner struct{}

func (OSRunner) LookPath(file string) (string, error) { return exec.LookPath(file) }

func (OSRunner) Run(ctx context.Context, spec Spec) (Result, error) {
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	if spec.Env != nil {
		cmd.Env = append([]string{}, os.Environ()...)
		for key, value := range spec.Env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return result, fmt.Errorf("command %q exited with code %d", spec.Path, result.ExitCode)
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, fmt.Errorf("run command %q: %w", spec.Path, err)
}
