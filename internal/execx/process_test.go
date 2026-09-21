package execx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestManagedChildSysProcAttrProtectsAgainstParentDeath(t *testing.T) {
	attr := managedChildSysProcAttr()
	if !attr.Setpgid || attr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("child policy = %#v", attr)
	}
}

func TestManagedChildSurvivesCreatorThreadExit(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	type startResult struct {
		process ProcessHandle
		err     error
	}
	started := make(chan startResult, 1)
	stdoutPath := filepath.Join(t.TempDir(), "stdout")
	go func() {
		runtime.LockOSThread()
		process, err := (OSProcessRunner{}).Start(ManagedSpec{
			Spec:       Spec{Path: "sh", Args: []string{"-c", "printf ready; sleep 30"}},
			StdoutPath: stdoutPath,
		})
		if err == nil {
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				data, readErr := os.ReadFile(stdoutPath)
				if readErr == nil && string(data) == "ready" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if data, readErr := os.ReadFile(stdoutPath); readErr != nil || string(data) != "ready" {
				err = fmt.Errorf("child did not become ready: %v, output %q", readErr, data)
			}
		}
		started <- startResult{process: process, err: err}
		runtime.Goexit()
	}()

	result := <-started
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.process == nil {
		t.Fatal("process handle is nil")
	}
	time.Sleep(100 * time.Millisecond)

	select {
	case err := <-result.process.Done():
		t.Fatalf("managed child exited after creator thread terminated: %v", err)
	case <-time.After(5 * time.Second):
	}

	if err := result.process.Stop(100 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestProcessStopsItsProcessGroup(t *testing.T) {
	p, err := (OSProcessRunner{}).Start(ManagedSpec{Spec: Spec{Path: "sh", Args: []string{"-c", "trap '' TERM; sleep 10"}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.PID() <= 0 {
		t.Fatalf("invalid pid %d", p.PID())
	}
	if err := p.Stop(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(time.Second):
		t.Fatal("process did not stop")
	}
}

func TestProcessStopEscalatesAfterGrace(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "stderr")
	p, err := (OSProcessRunner{}).Start(ManagedSpec{Spec: Spec{Path: "sh", Args: []string{"-c", "trap 'echo term >&2; sleep 10' TERM; while :; do sleep 1; done"}}, StderrPath: output})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := p.Stop(100 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "term") {
		t.Fatalf("TERM handler was not observed: %q", data)
	}
}
