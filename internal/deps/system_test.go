package deps

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

type fakeRunner struct {
	paths      map[string]string
	runs       []execx.Spec
	failUpdate bool
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if path := f.paths[name]; path != "" {
		return path, nil
	}
	return "", errors.New("not found")
}
func (f *fakeRunner) Run(_ context.Context, spec execx.Spec) (execx.Result, error) {
	f.runs = append(f.runs, spec)
	if len(spec.Args) >= 2 && spec.Args[1] == "update" && f.failUpdate {
		f.failUpdate = false
		return execx.Result{}, errors.New("update failed")
	}
	if len(spec.Args) >= 4 && spec.Args[1] == "install" {
		f.paths[spec.Args[3]] = "/usr/bin/" + spec.Args[3]
	}
	return execx.Result{}, nil
}

func TestEnsureRetriesUpdateAfterFailure(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{"apt-get": "/usr/bin/apt-get"}, failUpdate: true}
	s := System{Runner: r, GOOS: "linux", IsRoot: func() bool { return true }, Debian: true}
	if _, err := s.Ensure(context.Background(), "tmux", "tmux"); err == nil {
		t.Fatal("first update unexpectedly succeeded")
	}
	if _, err := s.Ensure(context.Background(), "tmux", "tmux"); err != nil {
		t.Fatal(err)
	}
	updates := 0
	for _, run := range r.runs {
		if len(run.Args) >= 2 && run.Args[1] == "update" {
			updates++
		}
	}
	if updates != 2 {
		t.Fatalf("update attempts = %d, runs = %#v", updates, r.runs)
	}
}

type concurrentRunner struct {
	mu             sync.Mutex
	installed      bool
	updates        int
	missingLookups int
	lookupsReady   chan struct{}
	readyOnce      sync.Once
	releaseUpdate  chan struct{}
}

func (r *concurrentRunner) LookPath(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "apt-get" {
		return "/usr/bin/apt-get", nil
	}
	if r.installed {
		return "/usr/bin/" + name, nil
	}
	if name == "tmux" {
		r.missingLookups++
		if r.missingLookups == 2 {
			r.readyOnce.Do(func() { close(r.lookupsReady) })
		}
	}
	return "", errors.New("not found")
}

func (r *concurrentRunner) Run(_ context.Context, spec execx.Spec) (execx.Result, error) {
	if len(spec.Args) >= 2 && spec.Args[1] == "update" {
		r.mu.Lock()
		r.updates++
		r.mu.Unlock()
		<-r.releaseUpdate
		return execx.Result{}, nil
	}
	if len(spec.Args) >= 2 && spec.Args[1] == "install" {
		r.mu.Lock()
		r.installed = true
		r.mu.Unlock()
	}
	return execx.Result{}, nil
}

func TestEnsureConcurrentCallsUpdateOnlyOnce(t *testing.T) {
	r := &concurrentRunner{lookupsReady: make(chan struct{}), releaseUpdate: make(chan struct{})}
	s := &System{Runner: r, GOOS: "linux", IsRoot: func() bool { return true }, Debian: true}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := s.Ensure(context.Background(), "tmux", "tmux"); results <- err }()
	}
	select {
	case <-r.lookupsReady:
	case <-time.After(time.Second):
		t.Fatal("concurrent calls did not both observe missing binary")
	}
	close(r.releaseUpdate)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	r.mu.Lock()
	updates := r.updates
	r.mu.Unlock()
	if updates != 1 {
		t.Fatalf("successful update calls = %d, want 1", updates)
	}
}

func TestEnsureInstallsMissingDebianBinaryOnce(t *testing.T) {
	r := &fakeRunner{paths: map[string]string{"apt-get": "/usr/bin/apt-get"}}
	s := System{Runner: r, GOOS: "linux", IsRoot: func() bool { return true }, Debian: true}
	if _, err := s.Ensure(context.Background(), "tmux", "tmux"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(context.Background(), "tmux", "tmux"); err != nil {
		t.Fatal(err)
	}
	if len(r.runs) != 2 || r.runs[0].Args[0] != "-qq" || r.runs[1].Args[2] != "-y" {
		t.Fatalf("runs = %#v", r.runs)
	}
}
