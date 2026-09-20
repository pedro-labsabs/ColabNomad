package deps

import (
	"context"
	"errors"
	"testing"

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
