package supervisor

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

type testService struct {
	name     string
	deps     []string
	log      *[]string
	mu       *sync.Mutex
	spec     execx.ManagedSpec
	probeErr error
}

func (s *testService) Name() string                  { return s.name }
func (s *testService) Dependencies() []string        { return s.deps }
func (s *testService) Prepare(context.Context) error { s.add("prepare:" + s.name); return nil }
func (s *testService) Command() execx.ManagedSpec    { return s.spec }
func (s *testService) Probe(context.Context) error   { s.add("probe:" + s.name); return s.probeErr }
func (s *testService) Cleanup(context.Context) error { s.add("cleanup:" + s.name); return nil }
func (s *testService) add(v string)                  { s.mu.Lock(); defer s.mu.Unlock(); *s.log = append(*s.log, v) }

type fakeHandle struct {
	done chan error
	pid  int
}

func (h *fakeHandle) PID() int           { return h.pid }
func (h *fakeHandle) Done() <-chan error { return h.done }
func (h *fakeHandle) Stop(time.Duration) error {
	select {
	case <-h.done:
	default:
		close(h.done)
	}
	return nil
}

type fakeRunner struct {
	mu      sync.Mutex
	starts  []string
	nextErr error
	exit    bool
}

func (r *fakeRunner) Start(s execx.ManagedSpec) (execx.ProcessHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, s.Path)
	if r.nextErr != nil {
		return nil, r.nextErr
	}
	h := &fakeHandle{done: make(chan error), pid: len(r.starts)}
	if r.exit {
		close(h.done)
	}
	return h, nil
}

func TestManagerStartsDependenciesBeforeDependents(t *testing.T) {
	var log []string
	var mu sync.Mutex
	runner := &fakeRunner{}
	services := []Service{
		&testService{name: "tunnel", deps: []string{"opencode"}, log: &log, mu: &mu},
		&testService{name: "opencode", log: &log, mu: &mu},
	}
	m := NewManager(services, runner, RestartPolicy{MaxRestarts: 0})
	if err := m.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := log, []string{"prepare:opencode", "probe:opencode", "prepare:tunnel", "probe:tunnel"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestManagerStopsAfterRestartBudget(t *testing.T) {
	runner := &fakeRunner{exit: true}
	m := NewManager([]Service{&testService{name: "svc", log: &[]string{}, mu: &sync.Mutex{}}}, runner, RestartPolicy{MaxRestarts: 3, InitialBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond})
	if err := m.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for m.State("svc") != Unhealthy && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.State("svc") != Unhealthy {
		t.Fatal("service did not become unhealthy")
	}
	if len(runner.starts) != 4 {
		t.Fatalf("starts = %d, want 4", len(runner.starts))
	}
}

func TestManagerRejectsDependencyCycle(t *testing.T) {
	a := &testService{name: "a", deps: []string{"b"}, log: &[]string{}, mu: &sync.Mutex{}}
	b := &testService{name: "b", deps: []string{"a"}, log: &[]string{}, mu: &sync.Mutex{}}
	if err := NewManager([]Service{a, b}, &fakeRunner{}, RestartPolicy{}).StartAll(context.Background()); err == nil || !contains(err, "cycle") {
		t.Fatalf("error = %v", err)
	}
}

func TestManagerDetectsExitBeforeReadiness(t *testing.T) {
	runner := &fakeRunner{exit: true}
	svc := &testService{name: "svc", probeErr: fmt.Errorf("not ready"), log: &[]string{}, mu: &sync.Mutex{}}
	m := NewManager([]Service{svc}, runner, RestartPolicy{MaxRestarts: 0}, WithReadinessTimeout(time.Second))
	if err := m.StartAll(context.Background()); err == nil || !contains(err, "exited before readiness") {
		t.Fatalf("error = %v", err)
	}
}

func TestManagerCancelsRestartBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &fakeRunner{exit: true}
	m := NewManager([]Service{&testService{name: "svc", log: &[]string{}, mu: &sync.Mutex{}}}, runner, RestartPolicy{MaxRestarts: 3, InitialBackoff: time.Second})
	if err := m.StartAll(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for m.State("svc") != Unhealthy && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.State("svc") != Unhealthy {
		t.Fatal("cancellation did not stop backoff")
	}
	if runner.count() != 1 {
		t.Fatalf("starts = %d, want 1", runner.count())
	}
}

func (r *fakeRunner) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.starts) }
func contains(err error, text string) bool {
	return err != nil && fmt.Sprint(err) != "" && len(fmt.Sprint(err)) >= len(text) && (fmt.Sprint(err) == text || stringContains(fmt.Sprint(err), text))
}
func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
