//go:build linux

package execx

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"
)

type OSProcessRunner struct{}

func mergedEnv(overrides map[string]string) []string {
	values := make(map[string]string)
	keys := make([]string, 0)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		if _, ok := values[key]; !ok {
			keys = append(keys, key)
		}
		values[key] = value
	}
	sort.Strings(keys)
	result := os.Environ()
	for i, entry := range result {
		key, _, _ := strings.Cut(entry, "=")
		result[i] = key + "=" + values[key]
	}
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func (OSProcessRunner) Start(spec ManagedSpec) (ProcessHandle, error) {
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	if spec.Env != nil {
		cmd.Env = mergedEnv(spec.Env)
	}
	var stdout, stderr *os.File
	var err error
	if spec.StdoutPath != "" {
		stdout, err = os.OpenFile(spec.StdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return nil, fmt.Errorf("open stdout: %w", err)
		}
	}
	if spec.StderrPath != "" {
		stderr, err = os.OpenFile(spec.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			if stdout != nil {
				stdout.Close()
			}
			return nil, fmt.Errorf("open stderr: %w", err)
		}
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		if stdout != nil {
			stdout.Close()
		}
		if stderr != nil {
			stderr.Close()
		}
		return nil, err
	}
	h := &osProcessHandle{cmd: cmd, done: make(chan error, 1), stdout: stdout, stderr: stderr}
	go func() {
		err := cmd.Wait()
		if stdout != nil {
			stdout.Close()
		}
		if stderr != nil {
			stderr.Close()
		}
		h.done <- err
		close(h.done)
	}()
	return h, nil
}

type osProcessHandle struct {
	cmd            *exec.Cmd
	done           chan error
	stdout, stderr *os.File
}

func (h *osProcessHandle) PID() int           { return h.cmd.Process.Pid }
func (h *osProcessHandle) Done() <-chan error { return h.done }
func (h *osProcessHandle) Stop(grace time.Duration) error {
	pid := h.PID()
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return err
	}
	if grace < 0 {
		grace = 0
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-h.done:
		return nil
	case <-timer.C:
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	<-h.done
	return nil
}
