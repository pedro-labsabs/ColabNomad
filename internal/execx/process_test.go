package execx

import (
	"testing"
	"time"
)

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
