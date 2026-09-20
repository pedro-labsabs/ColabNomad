package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	if _, err := m.Prepare(context.Background(), Request{RepoURL: origin, Root: repo, Ref: "main"}); err != nil {
		t.Fatal(err)
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
	token := "tok:@\nsecret"
	root := filepath.Join(t.TempDir(), "repo")
	_, err := (Manager{}).Prepare(context.Background(), Request{RepoURL: "https://example.invalid/repo.git", Root: root, GitHubToken: token})
	if err != nil && strings.Contains(err.Error(), token) {
		t.Fatalf("token leaked in error: %v", err)
	}
}
