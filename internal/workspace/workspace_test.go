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

type freshCloneRunner struct {
	specs []execx.Spec
}

func (r *freshCloneRunner) LookPath(string) (string, error) { return "git", nil }

func (r *freshCloneRunner) Run(_ context.Context, spec execx.Spec) (execx.Result, error) {
	r.specs = append(r.specs, spec)
	switch spec.Args[0] {
	case "clone":
		if err := os.MkdirAll(filepath.Join(spec.Args[len(spec.Args)-1], ".git"), 0755); err != nil {
			return execx.Result{}, err
		}
	case "remote":
		return execx.Result{Stdout: "https://github.com/org/repo.git\n"}, nil
	case "rev-parse":
		return execx.Result{Stdout: "commit\n"}, nil
	}
	return execx.Result{}, nil
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

func existingRepo(t *testing.T, origin string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
	git(t, dir, "remote", "add", "origin", origin)
	return dir
}

func TestPrepareRejectsExistingWorkspaceWithDifferentOrigin(t *testing.T) {
	t.Parallel()
	repo := existingRepo(t, "https://github.com/other/repo.git")

	_, err := (Manager{}).Prepare(context.Background(), Request{
		RepoURL: "https://github.com/requested/repo.git",
		Root:    repo,
	})
	if err == nil || !strings.Contains(err.Error(), "workspace origin mismatch") {
		t.Fatalf("expected workspace origin mismatch, got %v", err)
	}
	if got := string(mustReadFile(t, filepath.Join(repo, "tracked.txt"))); got != "content\n" {
		t.Fatalf("workspace changed after mismatch: %q", got)
	}
}

func TestPrepareReusesSameRepositoryWithEquivalentOrigin(t *testing.T) {
	t.Parallel()
	repo := existingRepo(t, "HTTPS://github.com/org/repo.git/")

	result, err := (Manager{}).Prepare(context.Background(), Request{
		RepoURL: "https://github.com/org/repo",
		Root:    repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Commit == "" {
		t.Fatal("expected existing repository commit")
	}
}

func TestPreparePreservesDirtySameRepository(t *testing.T) {
	t.Parallel()
	repo := existingRepo(t, "https://github.com/org/repo.git")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := (Manager{}).Prepare(context.Background(), Request{
		RepoURL: "https://github.com/org/repo/",
		Root:    repo,
		Ref:     "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Dirty {
		t.Fatal("expected dirty result")
	}
	if got := string(mustReadFile(t, filepath.Join(repo, "tracked.txt"))); got != "dirty\n" {
		t.Fatalf("dirty workspace changed: %q", got)
	}
}

func TestPrepareCredentialBearingURLUsesSanitizedIdentity(t *testing.T) {
	t.Parallel()
	repo := existingRepo(t, "https://github.com/org/repo.git")
	token := "secret-token"

	_, err := (Manager{}).Prepare(context.Background(), Request{
		RepoURL:     "https://user:" + token + "@GITHUB.com/org/repo.git",
		Root:        repo,
		GitHubToken: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	wanted, ok := repositoryIdentity("https://user:" + token + "@GITHUB.com/org/repo.git")
	if !ok || strings.Contains(wanted, token) || wanted != "remote:github.com/org/repo" {
		t.Fatalf("credential was not sanitized from identity: %q", wanted)
	}
}

func TestPrepareFreshCloneStripsCredentialsFromCloneSource(t *testing.T) {
	t.Parallel()
	runner := &freshCloneRunner{}
	root := filepath.Join(t.TempDir(), "repo")
	secret := "secret-token"

	_, err := (Manager{Runner: runner}).Prepare(context.Background(), Request{
		RepoURL:     "https://user:" + secret + "@github.com/org/repo.git",
		Root:        root,
		GitHubToken: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.specs) == 0 {
		t.Fatal("expected clone command")
	}
	clone := runner.specs[0].Args
	if strings.Contains(strings.Join(clone, "\x00"), secret) || strings.Contains(clone[len(clone)-2], "user@") {
		t.Fatalf("credentials leaked into clone source: %#v", clone)
	}
	if got := clone[len(clone)-2]; got != "https://github.com/org/repo.git" {
		t.Fatalf("unexpected credential-free clone source: %q", got)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
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
