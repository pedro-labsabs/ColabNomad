package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

type Manager struct {
	services         map[string]Service
	runner           execx.ProcessRunner
	policy           RestartPolicy
	mu               sync.RWMutex
	lifecycleMu      sync.Mutex
	state            map[string]State
	handles          map[string]execx.ProcessHandle
	generation       map[string]uint64
	order            []string
	readinessTimeout time.Duration
	probeInterval    time.Duration
	clock            Clock
	lifetime         context.Context
}

// Timer and Clock isolate scheduling from wall-clock time. Production uses
// real timers; tests can advance a fake clock without sleeping.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type Clock interface{ NewTimer(time.Duration) Timer }

type realClock struct{}
type realTimer struct{ timer *time.Timer }

func (realClock) NewTimer(d time.Duration) Timer { return &realTimer{timer: time.NewTimer(d)} }
func (t *realTimer) C() <-chan time.Time         { return t.timer.C }
func (t *realTimer) Stop() bool                  { return t.timer.Stop() }

type ManagerOption func(*Manager)

func WithReadinessTimeout(timeout time.Duration) ManagerOption {
	return func(m *Manager) { m.readinessTimeout = timeout }
}
func WithProbeInterval(interval time.Duration) ManagerOption {
	return func(m *Manager) { m.probeInterval = interval }
}
func WithClock(clock Clock) ManagerOption {
	return func(m *Manager) {
		if clock != nil {
			m.clock = clock
		}
	}
}

func NewManager(services []Service, runner execx.ProcessRunner, policy RestartPolicy, options ...ManagerOption) *Manager {
	byName := make(map[string]Service, len(services))
	for _, service := range services {
		byName[service.Name()] = service
	}
	if policy.MaxRestarts == 0 {
		policy.MaxRestarts = 3
	}
	if policy.MaxRestarts < 0 {
		policy.MaxRestarts = 0
	}
	if policy.InitialBackoff <= 0 {
		policy.InitialBackoff = 500 * time.Millisecond
	}
	if policy.MaxBackoff <= 0 {
		policy.MaxBackoff = 8 * time.Second
	}
	m := &Manager{services: byName, runner: runner, policy: policy, state: make(map[string]State), handles: make(map[string]execx.ProcessHandle), generation: make(map[string]uint64), readinessTimeout: 30 * time.Second, probeInterval: 100 * time.Millisecond, clock: realClock{}}
	for _, option := range options {
		option(m)
	}
	return m
}

func (m *Manager) State(name string) State { m.mu.RLock(); defer m.mu.RUnlock(); return m.state[name] }

// Snapshot returns a copy of current service states for read-only status use.
func (m *Manager) Snapshot() map[string]State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]State, len(m.state))
	for k, v := range m.state {
		out[k] = v
	}
	return out
}
func (m *Manager) ServicePID(name string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if h := m.handles[name]; h != nil {
		return h.PID()
	}
	return 0
}

func (m *Manager) StartAll(ctx context.Context) error {
	return m.StartAllWithLifetime(ctx, ctx)
}

// StartAllWithLifetime uses startupCtx for preparation/readiness and lifetimeCtx
// for monitoring processes after startup has completed.
func (m *Manager) StartAllWithLifetime(startupCtx, lifetimeCtx context.Context) error {
	if lifetimeCtx == nil {
		lifetimeCtx = startupCtx
	}
	m.lifetime = lifetimeCtx
	order, err := m.topologicalOrder()
	if err != nil {
		return err
	}
	m.order = order
	for _, name := range order {
		if err := m.startReady(startupCtx, name); err != nil {
			_ = m.StopAll(context.Background())
			return err
		}
	}
	return nil
}

func (m *Manager) startReady(ctx context.Context, name string) error {
	return m.startReadyMode(ctx, name, true, nil)
}

var errStaleGeneration = errors.New("stale service generation")

func (m *Manager) startReadyMode(ctx context.Context, name string, monitor bool, expectedGeneration *uint64) error {
	service := m.services[name]
	if expectedGeneration != nil {
		m.lifecycleMu.Lock()
		defer m.lifecycleMu.Unlock()
		if !m.isGenerationCurrent(name, *expectedGeneration) {
			return errStaleGeneration
		}
	}
	for _, dep := range service.Dependencies() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if m.State(dep) != Healthy {
			return fmt.Errorf("dependency %q is not healthy for %q", dep, name)
		}
	}
	if err := service.Prepare(ctx); err != nil {
		return fmt.Errorf("prepare %q: %w", name, err)
	}
	m.setState(name, Starting)
	h, err := m.runner.Start(service.Command())
	if err != nil {
		m.setState(name, Unhealthy)
		return fmt.Errorf("start %q: %w", name, err)
	}
	m.mu.Lock()
	m.handles[name] = h
	m.mu.Unlock()
	if err := m.waitReady(ctx, name, service, h); err != nil {
		m.setState(name, Unhealthy)
		m.mu.Lock()
		if m.handles[name] == h {
			delete(m.handles, name)
		}
		m.mu.Unlock()
		_ = h.Stop(0)
		return err
	}
	m.setState(name, Healthy)
	if monitor {
		monitorCtx := m.lifetime
		if monitorCtx == nil {
			monitorCtx = ctx
		}
		go m.monitor(monitorCtx, name, service, h)
	}
	return nil
}

func (m *Manager) waitReady(ctx context.Context, name string, service Service, h execx.ProcessHandle) error {
	deadline := m.clock.NewTimer(m.readinessTimeout)
	defer deadline.Stop()
	for {
		probeCtx, cancelProbe := context.WithCancel(ctx)
		probeResult := make(chan error, 1)
		go func() { probeResult <- service.Probe(probeCtx) }()
		select {
		case err := <-probeResult:
			cancelProbe()
			if err == nil {
				return nil
			}
			interval := m.clock.NewTimer(m.probeInterval)
			select {
			case <-interval.C():
			case <-ctx.Done():
				interval.Stop()
				return ctx.Err()
			case <-h.Done():
				interval.Stop()
				return fmt.Errorf("process %q exited before readiness", name)
			case <-deadline.C():
				interval.Stop()
				return fmt.Errorf("readiness timeout for %q", name)
			}
		case <-ctx.Done():
			cancelProbe()
			return ctx.Err()
		case <-h.Done():
			cancelProbe()
			return fmt.Errorf("process %q exited before readiness", name)
		case <-deadline.C():
			cancelProbe()
			return fmt.Errorf("readiness timeout for %q", name)
		}
	}
}

func (m *Manager) monitor(ctx context.Context, name string, service Service, h execx.ProcessHandle) {
	<-h.Done()
	m.mu.Lock()
	current := m.handles[name] == h && m.state[name] == Healthy
	generation := m.generation[name]
	if current {
		delete(m.handles, name)
		m.state[name] = Unhealthy
	}
	m.mu.Unlock()
	if !current {
		return
	}
	for restart := 0; restart < m.policy.MaxRestarts; restart++ {
		backoff := m.policy.InitialBackoff << restart
		if backoff > m.policy.MaxBackoff {
			backoff = m.policy.MaxBackoff
		}
		if !m.isGenerationCurrent(name, generation) {
			return
		}
		timer := m.clock.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			m.setStateIfGenerationCurrent(name, generation, Unhealthy)
			return
		case <-timer.C():
		}
		if !m.isGenerationCurrent(name, generation) {
			return
		}
		if err := m.startReadyMode(ctx, name, false, &generation); err != nil {
			if errors.Is(err, errStaleGeneration) {
				return
			}
			continue
		}
		next, ok := m.currentHandleForGeneration(name, generation)
		if !ok {
			return
		}
		<-next.Done()
		m.mu.Lock()
		stillCurrent := m.generation[name] == generation && m.handles[name] == next && m.state[name] == Healthy
		if stillCurrent {
			delete(m.handles, name)
			m.state[name] = Unhealthy
		}
		m.mu.Unlock()
		if !stillCurrent {
			return
		}
	}
	m.setStateIfGenerationCurrent(name, generation, Unhealthy)
}

func (m *Manager) Restart(ctx context.Context, name string) error {
	service, ok := m.services[name]
	if !ok {
		return fmt.Errorf("unknown service %q", name)
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	h := m.handles[name]
	// Invalidate the monitored generation before stopping it. Otherwise its
	// Done channel can wake the monitor after this method has begun.
	delete(m.handles, name)
	m.generation[name]++
	m.state[name] = Stopped
	m.mu.Unlock()
	if h != nil {
		if err := h.Stop(0); err != nil {
			return err
		}
	}
	if err := service.Cleanup(ctx); err != nil {
		return err
	}
	return m.startReady(ctx, name)
}

func (m *Manager) StopAll(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if len(m.order) == 0 {
		var err error
		m.order, err = m.topologicalOrder()
		if err != nil {
			return err
		}
	}
	var first error
	for i := len(m.order) - 1; i >= 0; i-- {
		name := m.order[i]
		m.mu.Lock()
		h := m.handles[name]
		delete(m.handles, name)
		m.generation[name]++
		m.state[name] = Stopped
		m.mu.Unlock()
		if h != nil {
			if err := h.Stop(0); err != nil && first == nil {
				first = err
			}
		}
		if err := m.services[name].Cleanup(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) setState(name string, state State) {
	m.mu.Lock()
	m.state[name] = state
	m.mu.Unlock()
}

func (m *Manager) currentHandleForGeneration(name string, generation uint64) (execx.ProcessHandle, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h := m.handles[name]
	return h, h != nil && m.generation[name] == generation && m.state[name] == Healthy
}

func (m *Manager) setStateIfGenerationCurrent(name string, generation uint64, state State) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation[name] != generation {
		return false
	}
	m.state[name] = state
	return true
}

func (m *Manager) isGenerationCurrent(name string, generation uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.generation[name] == generation
}

func (m *Manager) topologicalOrder() ([]string, error) {
	const (
		unseen   = 0
		visiting = 1
		visited  = 2
	)
	marks := make(map[string]int)
	order := make([]string, 0, len(m.services))
	var visit func(string) error
	visit = func(name string) error {
		if _, ok := m.services[name]; !ok {
			return fmt.Errorf("unknown dependency %q", name)
		}
		if marks[name] == visiting {
			return fmt.Errorf("dependency cycle involving %q", name)
		}
		if marks[name] == visited {
			return nil
		}
		marks[name] = visiting
		for _, dep := range m.services[name].Dependencies() {
			if err := visit(dep); err != nil {
				return err
			}
		}
		marks[name] = visited
		order = append(order, name)
		return nil
	}
	for name := range m.services {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}
