package execx

import (
	"os"
	"path/filepath"
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
