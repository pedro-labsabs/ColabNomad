package execx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
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
		env := make(map[string]string)
		inheritedOrder := make([]string, 0)
		inherited := make(map[string]bool)
		for _, entry := range os.Environ() {
			key, value, ok := strings.Cut(entry, "=")
			if !ok {
				continue
			}
			if !inherited[key] {
				inheritedOrder = append(inheritedOrder, key)
				inherited[key] = true
			}
			env[key] = value
		}
		for key, value := range spec.Env {
			env[key] = value
		}
		newKeys := make([]string, 0)
		for key := range spec.Env {
			if !inherited[key] {
				newKeys = append(newKeys, key)
			}
		}
		sort.Strings(newKeys)
		cmd.Env = make([]string, 0, len(inheritedOrder)+len(newKeys))
		for _, key := range inheritedOrder {
			cmd.Env = append(cmd.Env, key+"="+env[key])
		}
		for _, key := range newKeys {
			cmd.Env = append(cmd.Env, key+"="+env[key])
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
