# ColabNomad v0.1

ColabNomad runs OpenCode and a browser terminal for a target Git repository in
a CPU-only Google Colab runtime. The notebook is a small bootstrap adapter;
the Go binary owns the long-lived services and their in-session state.

## Happy path

1. Open [`notebook/colabnomad.ipynb`](notebook/colabnomad.ipynb) in Google
   Colab and use a **CPU** runtime. GPU is not required.
2. In the first notebook cell, set `TARGET_REPO` to the repository URL. Set
   `TARGET_REF` too when a particular branch, tag, or ref is required.
3. Optionally add `GITHUB_TOKEN` and `OPENCODE_API_KEY` as Colab Secrets. The
   bootstrap reads only those two secret names; a token is useful for private
   GitHub repositories, and the API key is passed to OpenCode when supplied.
4. Run all notebook cells. The notebook fetches and checks out the configured
   `COLABNOMAD_REF`, then invokes the pinned bootstrap release configured by
   `COLABNOMAD_RELEASE`.
5. Copy the OpenCode URL and terminal URL printed by the `up` result. Copy the
   one-time OpenCode and terminal usernames/passwords as well. Treat both URLs
   and all credentials as sensitive; do not commit or publish them.

The bootstrap stores the binary under `/content/.colabnomad/bin`. In the web
terminal, make it available by name before using the CLI:

```bash
export PATH="/content/.colabnomad/bin:$PATH"
colabnomad status
```

The normal service controls are:

```bash
colabnomad status
colabnomad doctor
colabnomad logs terminal
colabnomad logs opencode
colabnomad restart opencode
colabnomad restart terminal
colabnomad down
```

`doctor` exits non-zero when its health result is unhealthy. `restart
opencode` restarts only OpenCode; `restart terminal` restarts only the ttyd
service. The terminal service attaches to the persistent tmux session named
`colabnomad`, so restarting the terminal service does not intentionally destroy
that shell. `down` stops managed processes, destroys that tmux session, and
closes the control daemon/socket.

## Boundaries and recovery

- Colab may terminate the VM at any time. `/content`, including the workspace
  and `/content/.colabnomad`, is ephemeral; treat the whole VM as disposable.
- Commit and push dirty target-repository work yourself to a user-owned remote.
  Git remotes are the durability boundary. ColabNomad does not automatically
  commit or push, and it preserves dirty target worktrees during normal
  recovery.
- v0.1 does not fake activity or bypass Colab idle policies and does not depend
  on an assumed session duration. Reconnect or restart from the notebook after
  a lost runtime.
- The notebook only selects refs/repos, clones or updates ColabNomad, and calls
  `colab/bootstrap.py`; it is not a general orchestration shell.
- Managed HTTP services bind to `127.0.0.1`; public access is provided only by
  authenticated tunnel routes. OpenCode's public route must support SSE.
- Do not execute arbitrary target-repository code merely because it was cloned.
  Runtime tools are pinned or compatibility-checked; installation does not use
  an unbounded `latest` channel.
- Normal status and log output redacts tokens, passwords, authorization
  headers, and credential-bearing URLs. Never paste credentials into logs or
  issue reports.

## Acceptance evidence

The deterministic repository gate and the environment-level fresh-Colab smoke
evidence are separate. See [`docs/colab-smoke-v0.1.md`](docs/colab-smoke-v0.1.md)
for the concrete checklist. An unchecked smoke checklist is not a claim that a
fresh Colab run succeeded.
