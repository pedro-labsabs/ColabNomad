package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWorkspacePathPreservesRepositoryBasename(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")

	got, err := resolveWorkspacePath(root, "https://example.com/owner/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "repo.git"); got != want {
		t.Fatalf("workspace path = %q, want %q", got, want)
	}
}

func TestResolveWorkspacePathRejectsTraversalBeforeWorkspaceOperations(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	parent := filepath.Dir(root)

	for _, repoURL := range []string{
		"file:///tmp/..",
		"https://example.com/..",
		"https://example.com/%2e%2e",
	} {
		t.Run(repoURL, func(t *testing.T) {
			got, err := resolveWorkspacePath(root, repoURL)
			if err == nil {
				t.Fatalf("resolveWorkspacePath(%q) returned %q without an error", repoURL, got)
			}
			if got == parent || got == root || !strings.Contains(err.Error(), "workspace path escapes root") {
				t.Fatalf("path/error = %q/%v, want an escape error and no outside path", got, err)
			}
		})
	}
}

func TestResolveWorkspacePathRejectsMalformedURLWithoutLeakingUserinfo(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	const username, password = "alice", "super-secret"

	_, err := resolveWorkspacePath(root, "https://"+username+":"+password+"%zz@example.com/repo")
	if err == nil {
		t.Fatal("malformed repository URL unexpectedly resolved")
	}
	if strings.Contains(err.Error(), username) || strings.Contains(err.Error(), password) || strings.Contains(err.Error(), "@") {
		t.Fatalf("error leaks URL userinfo: %v", err)
	}
}
