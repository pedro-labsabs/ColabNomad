package workspace

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
	"github.com/pedroteste00000008-stack/ColabNomad/internal/secure"
)

type Request struct{ RepoURL, Ref, Root, GitHubToken string }
type Result struct {
	Path, Commit string
	Dirty        bool
}
type Manager struct {
	Runner execx.Runner
	Redact func(string, ...string) string
}

var createAskpass = func() (*os.File, error) {
	return os.CreateTemp("", "colabnomad-askpass-*")
}

func (m Manager) Prepare(ctx context.Context, req Request) (Result, error) {
	var result Result
	if req.RepoURL == "" || req.Root == "" {
		return result, fmt.Errorf("repository URL and root are required")
	}
	runner := m.Runner
	if runner == nil {
		runner = execx.OSRunner{}
	}
	redact := m.Redact
	if redact == nil {
		redact = secure.Redact
	}
	gitPath, err := runner.LookPath("git")
	if err != nil {
		return result, fmt.Errorf("find git: %w", err)
	}
	result.Path = req.Root
	gitDir := filepath.Join(req.Root, ".git")
	if _, statErr := os.Stat(gitDir); os.IsNotExist(statErr) {
		if entries, readErr := os.ReadDir(req.Root); readErr == nil && len(entries) > 0 {
			return result, fmt.Errorf("workspace root is not an existing git repository: %s", req.Root)
		}
		if err := os.MkdirAll(filepath.Dir(req.Root), 0755); err != nil {
			return result, fmt.Errorf("create workspace parent: %w", err)
		}
		args, err := cloneArgs(req)
		if err != nil {
			return result, err
		}
		spec := execx.Spec{Path: gitPath, Args: args, Dir: filepath.Dir(req.Root)}
		cleanup, env, err := askpass(req.GitHubToken)
		if err != nil {
			return result, fmt.Errorf("prepare GitHub askpass: %w", err)
		}
		if cleanup != nil {
			defer cleanup()
		}
		if env != nil {
			spec.Env = env
		}
		if _, err := runner.Run(ctx, spec); err != nil {
			return result, fmt.Errorf("clone repository: %s", redact(err.Error(), req.GitHubToken))
		}
	} else if statErr != nil {
		return result, fmt.Errorf("inspect workspace: %w", statErr)
	}
	if _, err := m.verifyOrigin(ctx, runner, gitPath, req, redact); err != nil {
		return result, err
	}
	status, err := m.run(ctx, runner, gitPath, req.Root, []string{"status", "--porcelain", "--untracked-files=all"}, req.GitHubToken, redact)
	if err != nil {
		return result, err
	}
	result.Dirty = status != ""
	if !result.Dirty && req.Ref != "" {
		if _, err := m.run(ctx, runner, gitPath, req.Root, []string{"fetch", "--prune", "origin"}, req.GitHubToken, redact); err != nil {
			return result, err
		}
		if _, err := m.run(ctx, runner, gitPath, req.Root, []string{"checkout", "--detach", req.Ref}, req.GitHubToken, redact); err != nil {
			return result, err
		}
	}
	commit, err := m.run(ctx, runner, gitPath, req.Root, []string{"rev-parse", "HEAD"}, req.GitHubToken, redact)
	if err != nil {
		return result, err
	}
	result.Commit = strings.TrimSpace(commit)
	return result, nil
}

func (m Manager) verifyOrigin(ctx context.Context, runner execx.Runner, gitPath string, req Request, redact func(string, ...string) string) (bool, error) {
	origin, err := m.run(ctx, runner, gitPath, req.Root, []string{"remote", "get-url", "origin"}, req.GitHubToken, redact)
	if err != nil {
		return false, err
	}
	wanted, wantedOK := repositoryIdentity(req.RepoURL)
	actual, actualOK := repositoryIdentity(origin)
	if !wantedOK || !actualOK || wanted != actual {
		err := fmt.Errorf("workspace origin mismatch: requested %q, existing %q", wanted, actual)
		return false, fmt.Errorf("%s", redact(err.Error(), req.GitHubToken))
	}
	return true, nil
}

func repositoryIdentity(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		if strings.EqualFold(u.Scheme, "file") {
			return "file:" + cleanRepositoryPath(u.Path), u.Path != ""
		}
		if u.Hostname() == "" {
			return "", false
		}
		host := strings.ToLower(u.Hostname())
		if port := u.Port(); port != "" {
			host += ":" + port
		}
		path := strings.TrimLeft(cleanRepositoryPath(u.Path), "/")
		return "remote:" + host + "/" + path, path != "."
	}
	if at := strings.IndexByte(raw, '@'); at > 0 {
		if colon := strings.IndexByte(raw[at+1:], ':'); colon >= 0 {
			colon += at + 1
			if raw[colon+1:] != "" {
				path := strings.TrimLeft(cleanRepositoryPath(raw[colon+1:]), "/")
				return "remote:" + strings.ToLower(raw[at+1:colon]) + "/" + path, path != "."
			}
		}
	}
	path := cleanRepositoryPath(raw)
	return "file:" + path, path != ""
}

func cleanRepositoryPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimRight(path, "/")
	if strings.HasSuffix(path, ".git") {
		path = strings.TrimRight(strings.TrimSuffix(path, ".git"), "/")
	}
	return filepath.Clean(path)
}

func cloneArgs(req Request) ([]string, error) {
	args := []string{"clone"}
	if req.Ref != "" {
		args = append(args, "--branch", req.Ref)
	}
	repoURL, err := cloneURL(req.RepoURL)
	if err != nil {
		return nil, fmt.Errorf("prepare clone URL: %w", err)
	}
	return append(args, repoURL, req.Root), nil
}

func cloneURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("invalid repository URL")
	}
	u.User = nil
	return u.String(), nil
}

func (m Manager) run(ctx context.Context, runner execx.Runner, gitPath, dir string, args []string, token string, redact func(string, ...string) string) (string, error) {
	spec := execx.Spec{Path: gitPath, Args: args, Dir: dir}
	cleanup, env, err := askpass(token)
	if err != nil {
		return "", fmt.Errorf("prepare GitHub askpass: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if env != nil {
		spec.Env = env
	}
	out, err := runner.Run(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], redact(err.Error(), token))
	}
	return out.Stdout, nil
}

func askpass(token string) (func(), map[string]string, error) {
	if token == "" {
		return nil, nil, nil
	}
	f, err := createAskpass()
	if err != nil {
		return nil, nil, fmt.Errorf("create helper: %w", err)
	}
	name := f.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := f.WriteString("#!/bin/sh\nprintf '%s\\n' \"$COLABNOMAD_GITHUB_TOKEN\"\n"); err != nil {
		f.Close()
		cleanup()
		return nil, nil, fmt.Errorf("write helper: %w", err)
	}
	if err := f.Chmod(0700); err != nil {
		f.Close()
		cleanup()
		return nil, nil, fmt.Errorf("chmod helper: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("close helper: %w", err)
	}
	return cleanup, map[string]string{"GIT_ASKPASS": name, "GIT_TERMINAL_PROMPT": "0", "COLABNOMAD_GITHUB_TOKEN": token}, nil
}
