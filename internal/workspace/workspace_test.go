package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

type recordingRunner struct {
	specs []execx.Spec
	err   error
}

func (r *recordingRunner) LookPath(string) (string, error) { return "git", nil }

func (r *recordingRunner) Run(_ context.Context, spec execx.Spec) (execx.Result, error) {
	r.specs = append(r.specs, spec)
	return execx.Result{}, r.err
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestPreparePreservesDirtyExistingRepo(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	origin := filepath.Join(tmp, "origin.git")
	repo := filepath.Join(tmp, "repo")
	git(t, tmp, "init", "--bare", origin)
	seed := filepath.Join(tmp, "seed")
	os.Mkdir(seed, 0755)
	git(t, seed, "init")
	os.WriteFile(filepath.Join(seed, "tracked.txt"), []byte("original\n"), 0644)
	git(t, seed, "add", ".")
	git(t, seed, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
	git(t, seed, "branch", "-M", "main")
	git(t, seed, "remote", "add", "origin", origin)
	git(t, seed, "push", "origin", "main")
	m := Manager{}
	token := "tok:@,;\nsecret"
	if _, err := m.Prepare(context.Background(), Request{RepoURL: origin, Root: repo, Ref: "main", GitHubToken: token}); err != nil {
		t.Fatal(err)
	}
	if got := git(t, repo, "remote", "get-url", "origin"); strings.Contains(got, token) {
		t.Fatalf("token leaked into origin URL: %q", got)
	}
	os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("local change\n"), 0644)
	os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("keep me\n"), 0644)
	got, err := m.Prepare(context.Background(), Request{RepoURL: origin, Root: repo, Ref: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Dirty {
		t.Fatal("expected dirty result")
	}
	for name, want := range map[string]string{"tracked.txt": "local change\n", "untracked.txt": "keep me\n"} {
		b, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil || string(b) != want {
			t.Fatalf("%s changed: %q, %v", name, b, err)
		}
	}
}

func TestPrepareDoesNotExposeTokenInGitURLOrErrors(t *testing.T) {
	token := "tok:@,;\nsecret"
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{err: fmt.Errorf("git failed while using %s", token)}
	_, err := (Manager{Runner: runner}).Prepare(context.Background(), Request{RepoURL: "https://github.com/org/repo.git", Root: root, GitHubToken: token})
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("token leaked in error: %v", err)
	}
	for _, spec := range runner.specs {
		if strings.Contains(strings.Join(spec.Args, "\x00"), token) {
			t.Fatalf("token leaked in runner argv: %#v", spec.Args)
		}
	}
}

func TestPrepareFailsClosedWhenAskpassCannotBeCreated(t *testing.T) {
	original := createAskpass
	createAskpass = func() (*os.File, error) { return nil, errors.New("injected helper creation failure") }
	t.Cleanup(func() { createAskpass = original })
	runner := &recordingRunner{}
	_, err := (Manager{Runner: runner}).Prepare(context.Background(), Request{
		RepoURL:     "https://github.com/org/repo.git",
		Root:        filepath.Join(t.TempDir(), "repo"),
		GitHubToken: "tok:@,;\nsecret",
	})
	if err == nil || !strings.Contains(err.Error(), "create helper") {
		t.Fatalf("expected askpass creation failure, got %v", err)
	}
	if len(runner.specs) != 0 {
		t.Fatalf("git ran after askpass failure: %#v", runner.specs)
	}
}
