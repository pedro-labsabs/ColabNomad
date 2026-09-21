package supervisor

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
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
	handles []*fakeHandle
	nextErr error
	exit    bool
}

type fakeTimer struct {
	clock   *fakeClock
	due     time.Duration
	ch      chan time.Time
	stopped bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }
func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	return true
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeTimer
}

func (c *fakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, due: c.now + d, ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	if t.due <= c.now {
		t.stopped = true
		t.ch <- time.Unix(0, int64(c.now))
	}
	return t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now += d
	now := c.now
	for _, t := range c.timers {
		if !t.stopped && t.due <= now {
			t.stopped = true
			t.ch <- time.Unix(0, int64(now))
		}
	}
	c.mu.Unlock()
}
func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.stopped {
			n++
		}
	}
	return n
}
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	for i := 0; i < 1000000 && !condition(); i++ {
		runtime.Gosched()
	}
	if !condition() {
		t.Fatal("condition did not become true")
	}
}

func (r *fakeRunner) Start(s execx.ManagedSpec) (execx.ProcessHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, s.Path)
	if r.nextErr != nil {
		return nil, r.nextErr
	}
	h := &fakeHandle{done: make(chan error), pid: len(r.starts)}
	r.handles = append(r.handles, h)
	if r.exit {
		close(h.done)
	}
	return h, nil
}

func (r *fakeRunner) closeLatest() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.handles) == 0 {
		return
	}
	select {
	case <-r.handles[len(r.handles)-1].done:
	default:
		close(r.handles[len(r.handles)-1].done)
	}
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
	runner := &fakeRunner{}
	clock := &fakeClock{}
	m := NewManager([]Service{&testService{name: "svc", log: &[]string{}, mu: &sync.Mutex{}}}, runner, RestartPolicy{MaxRestarts: 3}, WithClock(clock))
	if err := m.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.closeLatest()
	waitFor(t, func() bool { return clock.pending() > 0 })
	clock.Advance(500 * time.Millisecond)
	waitFor(t, func() bool { return runner.count() == 2 })
	waitFor(t, func() bool { return m.State("svc") == Healthy })
	runner.closeLatest()
	waitFor(t, func() bool { return clock.pending() > 0 })
	clock.Advance(time.Second)
	for i := 0; i < 1000000 && runner.count() != 3; i++ {
		runtime.Gosched()
	}
	if runner.count() != 3 {
		t.Fatalf("after second advance starts=%d pending=%d state=%s", runner.count(), clock.pending(), m.State("svc"))
	}
	waitFor(t, func() bool { return m.State("svc") == Healthy })
	runner.closeLatest()
	waitFor(t, func() bool { return clock.pending() > 0 })
	clock.Advance(2 * time.Second)
	waitFor(t, func() bool { return runner.count() == 4 })
	waitFor(t, func() bool { return m.State("svc") == Healthy })
	runner.closeLatest()
	waitFor(t, func() bool { return m.State("svc") == Unhealthy })
	if m.State("svc") != Unhealthy {
		t.Fatal("service did not become unhealthy")
	}
	if runner.count() != 4 {
		t.Fatalf("starts = %d, want 4", runner.count())
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
	runner := &fakeRunner{}
	clock := &fakeClock{}
	m := NewManager([]Service{&testService{name: "svc", log: &[]string{}, mu: &sync.Mutex{}}}, runner, RestartPolicy{MaxRestarts: 3, InitialBackoff: time.Second}, WithClock(clock))
	if err := m.StartAll(ctx); err != nil {
		t.Fatal(err)
	}
	runner.closeLatest()
	cancel()
	waitFor(t, func() bool { return m.State("svc") == Unhealthy })
	if m.State("svc") != Unhealthy {
		t.Fatal("cancellation did not stop backoff")
	}
	if runner.count() != 1 {
		t.Fatalf("starts = %d, want 1", runner.count())
	}
}

func TestManagerReadinessTimeout(t *testing.T) {
	clock := &fakeClock{}
	svc := &testService{name: "svc", probeErr: fmt.Errorf("not ready"), log: &[]string{}, mu: &sync.Mutex{}}
	m := NewManager([]Service{svc}, &fakeRunner{}, RestartPolicy{MaxRestarts: 3}, WithClock(clock), WithReadinessTimeout(2*time.Second), WithProbeInterval(time.Second))
	result := make(chan error, 1)
	go func() { result <- m.StartAll(context.Background()) }()
	waitFor(t, func() bool { return clock.pending() >= 1 })
	clock.Advance(2 * time.Second)
	select {
	case err := <-result:
		if err == nil || !contains(err, "readiness timeout") {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("readiness did not time out")
	}
}

func TestManagerReadinessTimeoutCancelsBlockingProbe(t *testing.T) {
	clock := &fakeClock{}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	svc := &blockingProbeService{name: "blocking", started: started, cancelled: cancelled}
	m := NewManager([]Service{svc}, &fakeRunner{}, RestartPolicy{MaxRestarts: 0}, WithClock(clock), WithReadinessTimeout(2*time.Second))
	result := make(chan error, 1)
	go func() { result <- m.StartAll(context.Background()) }()
	waitFor(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	})
	waitFor(t, func() bool { return clock.pending() >= 1 })
	clock.Advance(2 * time.Second)
	select {
	case err := <-result:
		if err == nil || !contains(err, "readiness timeout") {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocking probe did not time out")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("probe context was not cancelled")
	}
}

func TestManagerReadinessTimeoutReturnsBeforeProbeAcknowledgesCancellation(t *testing.T) {
	clock := &fakeClock{}
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	svc := &uncooperativeProbeService{name: "uncooperative", started: started, cancelled: cancelled, release: release}
	m := NewManager([]Service{svc}, &fakeRunner{}, RestartPolicy{MaxRestarts: 0}, WithClock(clock), WithReadinessTimeout(2*time.Second))
	result := make(chan error, 1)
	go func() { result <- m.StartAll(context.Background()) }()
	waitFor(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	})
	waitFor(t, func() bool { return clock.pending() >= 1 })
	clock.Advance(2 * time.Second)
	select {
	case err := <-result:
		if err == nil || !contains(err, "readiness timeout") {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("readiness timeout waited for an uncooperative probe")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("probe context was not cancelled")
	}
	close(release)
}

type blockingProbeService struct {
	name               string
	started, cancelled chan struct{}
}

func (s *blockingProbeService) Name() string                  { return s.name }
func (s *blockingProbeService) Dependencies() []string        { return nil }
func (s *blockingProbeService) Prepare(context.Context) error { return nil }
func (s *blockingProbeService) Command() execx.ManagedSpec    { return execx.ManagedSpec{} }
func (s *blockingProbeService) Probe(ctx context.Context) error {
	close(s.started)
	<-ctx.Done()
	close(s.cancelled)
	return ctx.Err()
}
func (s *blockingProbeService) Cleanup(context.Context) error { return nil }

type uncooperativeProbeService struct {
	name                        string
	started, cancelled, release chan struct{}
}

func (s *uncooperativeProbeService) Name() string                  { return s.name }
func (s *uncooperativeProbeService) Dependencies() []string        { return nil }
func (s *uncooperativeProbeService) Prepare(context.Context) error { return nil }
func (s *uncooperativeProbeService) Command() execx.ManagedSpec    { return execx.ManagedSpec{} }
func (s *uncooperativeProbeService) Probe(ctx context.Context) error {
	close(s.started)
	<-ctx.Done()
	close(s.cancelled)
	<-s.release
	return ctx.Err()
}
func (s *uncooperativeProbeService) Cleanup(context.Context) error { return nil }

func TestManagerExplicitRestartDoesNotTriggerAutomaticRestart(t *testing.T) {
	runner := &fakeRunner{}
	svc := &testService{name: "svc", log: &[]string{}, mu: &sync.Mutex{}}
	m := NewManager([]Service{svc}, runner, RestartPolicy{MaxRestarts: 3})
	if err := m.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Restart(context.Background(), "svc"); err != nil {
		t.Fatal(err)
	}
	if runner.count() != 2 {
		t.Fatalf("starts = %d, want exactly 2", runner.count())
	}
}

func TestManagerStopsInReverseDependencyOrder(t *testing.T) {
	var log []string
	mu := &sync.Mutex{}
	leaf := &testService{name: "leaf", log: &log, mu: mu}
	middle := &testService{name: "middle", deps: []string{"leaf"}, log: &log, mu: mu}
	root := &testService{name: "root", deps: []string{"middle"}, log: &log, mu: mu}
	m := NewManager([]Service{root, middle, leaf}, &fakeRunner{}, RestartPolicy{MaxRestarts: 3})
	if err := m.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := log[len(log)-3:]; !reflect.DeepEqual(got, []string{"cleanup:root", "cleanup:middle", "cleanup:leaf"}) {
		t.Fatalf("cleanup order = %v", got)
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
