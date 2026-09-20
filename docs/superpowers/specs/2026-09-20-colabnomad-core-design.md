# ColabNomad Core Design

**Date:** 2026-09-20
**Status:** Proposed for implementation
**Owner:** Pedro
**Repository:** `pedroteste00000008-stack/ColabNomad`

## 1. Purpose

ColabNomad turns a fresh Google Colab CPU runtime into a reproducible, disposable development workstation for short-term coding when the primary PC is unavailable.

The notebook is only a bootstrap surface. Operational behavior belongs in this repository and must be versioned, testable, restartable, and diagnosable without editing notebook cells.

Success means a user can open the notebook, provide a target Git repository and credentials through Colab Secrets, run the bootstrap, and receive working URLs for OpenCode Web and a browser terminal.

The design optimizes for fast reconstruction rather than assuming that a Colab VM will remain alive indefinitely.

## 2. Core constraints

- CPU runtime is the default; GPU is not required.
- The Colab VM is disposable and must never be treated as durable storage.
- Git remotes are the durable source of project state.
- The notebook must contain minimal orchestration logic.
- No component may rely on simulated activity or idle-timeout bypass behavior.
- Services exposed publicly must require authentication.
- Startup must be idempotent where practical.
- A failed service must be diagnosable without rerunning the entire notebook.
- The runtime must tolerate browser disconnects while the Colab VM itself remains alive.
- Version-sensitive external tools must be pinned or compatibility-checked.
- The v0.1 core uses Go; Zig is deferred until there is evidence that Go is a bottleneck.

## 3. Scope of v0.1

v0.1 provides:

1. a minimal Colab notebook;
2. a bootstrap entry point stored in this repository;
3. one Go CLI/runtime named `colabnomad`;
4. target workspace cloning and Git authentication support;
5. OpenCode installation, configuration, launch, health checking, and restart;
6. a web terminal backed by `ttyd` and `tmux`;
7. Cloudflare Quick Tunnel exposure;
8. process supervision with bounded restart/backoff;
9. `status`, `doctor`, `logs`, and `restart` commands;
10. structured runtime state and logs under an ephemeral Colab state directory.

Explicitly out of scope for v0.1:

- automated attempts to extend Colab session lifetime;
- GPU workload management;
- full browser IDEs such as code-server;
- automatic periodic Git commits or pushes;
- Cloudflare Named Tunnel provisioning;
- Google Drive persistence;
- multi-user hosting;
- container orchestration.

These can be added only after the core bootstrap and supervision path is proven stable.

## 4. Architecture

The runtime has five layers:

```text
notebook.ipynb
    |
    v
bootstrap
    |
    v
colabnomad (Go supervisor)
    |
    +-- workspace manager
    +-- dependency manager
    +-- service supervisor
    |      +-- OpenCode
    |      +-- ttyd -> tmux -> shell
    |      +-- cloudflared
    +-- health/diagnostics
    +-- runtime state/logs
```

The notebook does not own child processes after bootstrap handoff.

### 4.1 Notebook

The notebook exposes only user-facing bootstrap configuration such as:

- ColabNomad source/release channel;
- target repository URL;
- optional branch/ref;
- optional workspace name.

Secrets are read through Colab Secrets, not embedded in the notebook or committed files.

Its final action invokes the repository bootstrap and prints the resulting service summary.

### 4.2 Bootstrap

The bootstrap is intentionally small and uses tooling guaranteed to exist in a normal Colab image.

Responsibilities:

- detect OS and CPU architecture;
- create the ephemeral ColabNomad state directory;
- obtain a compatible ColabNomad binary;
- verify downloaded artifacts when checksums are available;
- execute `colabnomad up` with the requested target repository.

Distribution strategy:

1. prefer a pinned GitHub Release binary;
2. fall back to building the checked-out Go source when a release is unavailable;
3. fail with an actionable diagnostic instead of silently changing versions.

### 4.3 ColabNomad runtime

The Go runtime owns lifecycle and dependency ordering.

Initial command surface:

```text
colabnomad up
colabnomad status
colabnomad doctor
colabnomad logs [service]
colabnomad restart <service>
colabnomad down
```

The runtime writes machine-readable state to an ephemeral directory such as `/content/.colabnomad`.

State may contain service PIDs, ports, generated URLs, timestamps, resolved versions, and non-secret configuration.

Secrets must remain in process environment or protected temporary files and must never appear in normal logs or status output.

### 4.4 Workspace manager

The workspace manager clones the requested target repository into a deterministic path under `/content/workspaces`.

If the workspace already exists, ColabNomad must inspect it instead of deleting or recloning it.

Dirty worktrees are preserved. Destructive reset/clean operations are forbidden unless the user explicitly requests them.

GitHub authentication may use a token supplied through Colab Secrets or an already configured credential path.

### 4.5 Service supervisor

Each managed service implements a common lifecycle contract:

```text
Install/Resolve -> Configure -> Start -> Probe -> Running
                                  |        |
                                  |        +-> unhealthy -> restart/backoff
                                  +-> start failure -> diagnostic
```

A service definition must provide:

- executable/version resolution;
- process arguments and environment;
- readiness probe;
- log destination;
- restart policy;
- dependency list.

Restarts are bounded with exponential backoff so a persistent failure cannot create a hot restart loop.

## 5. Managed services

### 5.1 OpenCode

OpenCode runs against the target workspace, not the ColabNomad repository.

ColabNomad must pin or validate the OpenCode major/version expected by its adapter.

The adapter creates ephemeral OpenCode configuration outside the target repository unless the user explicitly requests project-local configuration.

The public OpenCode endpoint requires authentication.

Readiness is established using a real local API/health probe rather than only checking that a TCP port is open.

### 5.2 Terminal

The terminal path is:

```text
browser -> tunnel -> ttyd -> tmux session -> shell -> target workspace
```

`tmux` owns the shell session so a browser or tunnel reconnect does not destroy the shell while the Colab VM remains alive.

`ttyd` is bound to localhost and is exposed only through the configured tunnel.

The terminal endpoint requires authentication generated or supplied at startup.

### 5.3 Cloudflare tunnel

v0.1 uses Cloudflare Quick Tunnel as the zero-configuration transport. It runs one independent Quick Tunnel for OpenCode and one for the terminal, avoiding a reverse proxy dependency in the first release.

ColabNomad parses each assigned public URL, verifies that each tunnel process remains alive, and probes the exposed service before reporting readiness.

Tunnel loss must not terminate the underlying OpenCode or tmux sessions.

A stable Named Tunnel is a later feature and must fit behind the same tunnel interface.

## 6. Startup order

The required startup order is:

1. validate runtime and configuration;
2. resolve/install required dependencies;
3. prepare or inspect the target workspace;
4. start tmux session;
5. start ttyd and verify local readiness;
6. start OpenCode and verify local API readiness;
7. establish independent public tunnels for OpenCode and the terminal;
8. verify public reachability where technically possible;
9. persist runtime state;
10. print one concise connection summary.

If a later step fails, already-running local services should remain available for diagnostics unless continuing would create a security risk.

## 7. Error handling and diagnostics

Every user-visible failure must identify:

- failing component;
- attempted operation;
- relevant exit code or health result;
- log path;
- one concrete recovery action.

`colabnomad doctor` performs read-only checks for:

- filesystem/state directory;
- Git and target workspace;
- required binaries and versions;
- tmux session;
- ttyd process/readiness;
- OpenCode process/API readiness;
- cloudflared process/tunnel state;
- expected local ports.

Normal status output must redact secrets and credentials.

## 8. Security model

Services bind to localhost by default.

Public access is provided only through the tunnel layer and authenticated service endpoints.

Secrets are obtained from environment/Colab Secrets and are never committed into either ColabNomad or the target repository.

Generated passwords should use cryptographically secure randomness.

Logs must avoid printing tokens, Authorization headers, full credential URLs, or secret-bearing environment dumps.

The runtime must not execute arbitrary code from the target repository merely because the repository was cloned. Commands requiring target-project execution are explicit adapter actions.

## 9. Testing strategy

The Go core is designed around injectable process execution, filesystem paths, clocks, and HTTP probes so lifecycle behavior can be unit-tested without a real Colab session.

Required deterministic coverage includes:

- configuration validation;
- workspace preservation on dirty repositories;
- service dependency ordering;
- successful readiness transition;
- bounded restart/backoff;
- process exit before readiness;
- timeout during readiness;
- secret redaction;
- state serialization/deserialization;
- doctor classification of healthy and unhealthy services.

Integration tests run local fake HTTP services and disposable Git repositories.

A Colab smoke test remains the final environment-level validation and cannot be replaced by unit tests.

## 10. Repository boundaries

The intended source layout is:

```text
cmd/colabnomad/
internal/bootstrap/
internal/config/
internal/workspace/
internal/supervisor/
internal/health/
internal/services/opencode/
internal/services/terminal/
internal/services/tunnel/
internal/state/
colab/
notebook/
docs/
```

Files should be split by responsibility rather than accumulating orchestration into one large command file.

## 11. Design decisions

- **Go over Zig for v0.1:** prioritize mature process, HTTP, concurrency, and testing primitives over minimum possible binary size.
- **Repository-driven runtime:** notebook cells are not the source of operational truth.
- **tmux behind ttyd:** browser reconnects must not destroy the working shell.
- **Quick Tunnel first:** optimize initial usability while keeping the tunnel interface replaceable.
- **Git as durability boundary:** loss of the Colab VM must be treated as normal.
- **No automatic Git mutation in v0.1:** warnings and status are safer than implicit commits/pushes until recovery semantics are designed.
- **Explicit health probes:** process existence alone is insufficient evidence of readiness.

## 12. Definition of done for v0.1

v0.1 is complete only when a fresh CPU Colab runtime can use the repository notebook to:

1. bootstrap ColabNomad without manual shell preparation;
2. clone a selected target repository;
3. launch OpenCode and a tmux-backed browser terminal;
4. expose authenticated usable endpoints;
5. report health and logs through the CLI;
6. restart an individual failed managed service without recreating the whole runtime;
7. preserve a dirty target worktree during normal restart/recovery operations;
8. pass the repository's deterministic test suite.

The VM may still be terminated by Colab at any time; fast, reproducible reconstruction is the reliability target.
