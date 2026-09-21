# ColabNomad Core Design

**Date:** 2026-09-20
**Status:** Proposed for implementation
**Owner:** Pedro
**Repository:** `pedro-labsabs/ColabNomad`

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
- The v0.1 language architecture is deliberately split: Python owns only Colab integration/bootstrap; Go owns the authoritative runtime and all long-lived child processes.
- Shell is an invocation surface, not an application layer: orchestration must not accumulate in large shell scripts.
- Rust or Zig may be introduced later only for an isolated low-level component with a demonstrated requirement that Go does not satisfy cleanly.

## 3. Scope of v0.1

v0.1 provides:

1. a minimal Colab notebook;
2. a minimal Python bootstrap entry point stored in this repository;
3. one Go CLI/runtime named `colabnomad`;
4. target workspace cloning and Git authentication support;
5. OpenCode installation, configuration, launch, health checking, and restart;
6. a web terminal backed by `ttyd` and `tmux`;
7. capability-aware public tunnel exposure, using localhost.run/OpenSSH for OpenCode and Cloudflare Quick Tunnel for ttyd by default;
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
bootstrap.py (Python / Colab adapter)
    |
    v
colabnomad (Go control plane + supervisor)
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

- ColabNomad repository/ref or release channel;
- target repository URL;
- optional target branch/ref;
- optional workspace name.

The notebook clones or updates the selected ColabNomad ref into a deterministic path, then invokes `colab/bootstrap.py` from that checkout. Cells must not duplicate dependency installation, service configuration, process supervision, health logic, or tunnel logic that belongs in the repository.

Secrets are read through Colab Secrets, not embedded in the notebook or committed files.

Its final action invokes the repository bootstrap and prints the resulting service summary.

### 4.2 Python bootstrap

The bootstrap is intentionally small Python code because Python and the `google.colab` integration are native to the Colab environment. It is an adapter into the Go runtime, not a second control plane.

Responsibilities:

- read notebook parameters and Colab Secrets;
- detect OS and CPU architecture;
- create the ephemeral ColabNomad state directory;
- resolve a pinned ColabNomad release;
- download and verify the matching binary and checksum;
- prepare a minimal, sanitized environment for the runtime;
- execute `colabnomad up` with the requested target repository.

The Python bootstrap must not supervise long-lived services, own restart loops, hold authoritative runtime state, or duplicate business logic from the Go core. Prefer the Python standard library plus `google.colab`; adding third-party Python dependencies requires explicit justification.

Distribution strategy:

1. prefer a pinned GitHub Release binary for the detected Linux architecture;
2. if the release artifact is unavailable, download a pinned Go toolchain into ephemeral state and build the already checked-out ColabNomad source commit rather than silently selecting a different version;
3. verify downloadable artifacts before execution whenever a published checksum exists;
4. fail with an actionable diagnostic instead of silently changing versions.

### 4.3 Language boundaries

The language split is part of the architecture:

- **Python:** Colab integration, parameter/secret acquisition, release bootstrap, then handoff.
- **Go:** authoritative control plane, process ownership, state, health checks, Git/workspace operations, service adapters, diagnostics, and CLI.
- **Shell:** short explicit commands invoked through argv only; no orchestration framework or large shell scripts.
- **Rust/Zig:** reserved for future isolated low-level components only when a measured requirement justifies another toolchain.

After the Python process hands off to `colabnomad`, normal runtime operation must not depend on Python.

### 4.4 ColabNomad runtime

The Go runtime owns lifecycle, dependency ordering, persistent in-session state, and all long-lived child processes.

`colabnomad up` must establish or reconnect to a long-lived Go supervisor that is independent of the Python bootstrap process. The bootstrap waits only for the bounded startup result/connection summary, then exits; killing or completing the notebook bootstrap cell must not make Python the lifetime owner of OpenCode, ttyd, tmux, or cloudflared.

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

A reachable control socket is reusable only when the daemon reports the same startup-captured binary identity as the current CLI. If the binary changed in place, the current CLI requests a normal `down`, waits for both the socket and owner identity to clear, and then starts the replacement daemon. Legacy daemons that do not implement the identity command are treated as stale; ColabNomad never signals a PID merely because it appears in saved state.

State may contain service PIDs, ports, generated URLs, timestamps, resolved versions, and non-secret configuration.

Secrets must remain in process environment or protected temporary files and must never appear in normal logs or status output.

### 4.5 Workspace manager

The workspace manager clones the requested target repository into a deterministic path under `/content/workspaces`.

If the workspace already exists, ColabNomad must inspect it instead of deleting or recloning it.

Dirty worktrees are preserved. Destructive reset/clean operations are forbidden unless the user explicitly requests them.

GitHub authentication may use a token supplied through Colab Secrets or an already configured credential path.

### 4.6 Service supervisor

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

ColabNomad must pin or validate the OpenCode major/version expected by its adapter. The initial v0.1 contract targets OpenCode v2 and runs its foreground server with `opencode serve` bound to `127.0.0.1`.

The adapter creates ephemeral OpenCode configuration outside the target repository unless the user explicitly requests project-local configuration. It uses an explicit `OPENCODE_CONFIG` path under ColabNomad ephemeral state and disables OpenCode auto-update so the pinned binary cannot silently change itself.

The public OpenCode endpoint requires authentication. For the pinned v2 contract, OpenCode itself still uses Basic Auth locally and readiness is proven with an authenticated JSON API request scoped to the target workspace, not by TCP-open checks or legacy v1 routes. Public browser access does not expose that Basic Auth challenge directly: a localhost-only ColabNomad browser-auth gateway validates the generated OpenCode credentials, issues a `Secure`, `HttpOnly`, `SameSite=Lax` session cookie, and proxies authenticated requests to OpenCode with the local Basic Auth header. This avoids repeated browser auth prompts on OpenCode's credential-sensitive static assets while keeping OpenCode bound to localhost.

Public streaming compatibility is proven against the v2 SSE endpoint `/api/event`; the first `server.connected` event or another valid SSE frame must arrive before the endpoint is considered ready.

### 5.2 Terminal

The terminal path is:

```text
browser -> tunnel -> ttyd -> tmux session -> shell -> target workspace
```

`tmux` owns the shell session so a browser or tunnel reconnect does not destroy the shell while the Colab VM remains alive.

`ttyd` is bound to localhost and is exposed only through the configured tunnel.

The terminal endpoint requires authentication generated or supplied at startup.

### 5.3 Tunnel providers

Tunnel transport is capability-driven. A provider declares at minimum whether it supports HTTP streaming/SSE and WebSocket traffic before it can be assigned to a managed service.

v0.1 ships three providers:

- **localhost.run/OpenSSH:** zero-config default for OpenCode. It is invoked through the system `ssh` client, supports the required SSE transport, and must pass a real SSE smoke test before release.
- **Cloudflare Quick Tunnel:** default for ttyd, where its WebSocket support is required. ColabNomad forces the connector transport to HTTP/2 because restricted Colab networks can delay QUIC-to-HTTP/2 fallback beyond the supervisor readiness window. A freshly-created `trycloudflare.com` hostname can briefly return NXDOMAIN through Colab's Docker-embedded resolver even after `cloudflared` registers the connector; only that exact DNS-not-found condition may use the provider's current-generation `Registered tunnel connection ... protocol=http2` evidence as readiness fallback. HTTP status, TLS, authentication, connection, and unrelated DNS failures remain hard probe failures. It remains rejected for OpenCode because the provider contract does not declare SSE.
- **Serveo/OpenSSH:** retained as an explicit legacy provider, but not selected by default for browser UIs because its free browser-warning interstitial is request-scoped and breaks multi-request UI flows.

The tunnel interface owns process startup, public URL discovery, readiness, capability reporting, and lifecycle observation. Tunnel loss must not terminate the underlying OpenCode or tmux sessions. When a managed tunnel restarts successfully and discovers a different public URL, the supervisor notifies the runtime after the new generation reaches `Healthy`; the runtime refreshes the corresponding in-memory endpoint and persists the full endpoint map back to `state.json`. `colabnomad status` and an idempotent `up` must therefore return the current URL after recovery. For zero-config `localhost.run` tunnels, an already-open browser tab cannot migrate to the replacement hostname automatically; the user reopens the current URL returned by ColabNomad.

Provider selection must fail closed: if a service requires a transport capability that the selected provider does not declare, startup stops with a diagnostic instead of launching a known-incompatible route.

Cloudflare Named Tunnel, ngrok, Tailscale Funnel, or other transports can be added later behind the same provider contract without changing OpenCode, terminal, or supervisor semantics.

## 6. Startup order

The required startup order is:

1. validate runtime and configuration;
2. resolve/install required dependencies;
3. prepare or inspect the target workspace;
4. start tmux session;
5. start ttyd and verify local readiness;
6. start OpenCode and verify local API readiness;
7. start the localhost-only OpenCode browser-auth gateway and verify its health endpoint;
8. select capability-compatible tunnel providers and establish public endpoints for the gateway and terminal;
9. verify public reachability where technically possible;
10. persist runtime state;
11. print one concise connection summary.

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
- configured tunnel-provider process/state and declared capabilities;
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

The Go core is designed around injectable process execution, filesystem paths, clocks, and HTTP probes so lifecycle behavior can be unit-tested without a real Colab session. The Python bootstrap is tested separately as a narrow adapter with mocked Colab Secrets, architecture detection, release resolution, checksum verification, and handoff.

Required deterministic coverage includes:

- Python bootstrap secret acquisition without secret leakage;
- Python architecture/release selection and checksum failure handling;
- Python-to-Go handoff without Python retaining runtime ownership;
- configuration validation;
- workspace preservation on dirty repositories;
- service dependency ordering;
- successful readiness transition;
- pinned OpenCode v2 CLI/API contract (`serve`, authenticated `/api/project`, SSE `/api/event`);
- tunnel capability rejection for incompatible service/provider pairs;
- SSE smoke-test success and streaming/buffering failure classification;
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
internal/config/
internal/workspace/
internal/supervisor/
internal/health/
internal/services/opencode/
internal/services/terminal/
internal/services/tunnel/
internal/state/
colab/bootstrap.py
colab/test_bootstrap.py
notebook/colabnomad.ipynb
docs/
```

Files should be split by responsibility rather than accumulating orchestration into one large command file.

## 11. Design decisions

- **Python + Go for v0.1:** Python is the smallest native bridge to Colab APIs; Go is the authoritative runtime because process control, HTTP, concurrency, static distribution, cross-compilation, and testing are all first-class without requiring a heavyweight runtime.
- **No gratuitous polyglot expansion:** Rust, Zig, C#, Dart, Nim, or other languages require a concrete subsystem-level benefit before entering the repository.
- **Repository-driven runtime:** notebook cells are not the source of operational truth.
- **tmux behind ttyd:** browser reconnects must not destroy the working shell.
- **Capability-driven tunnels:** localhost.run/OpenSSH is the zero-config OpenCode default because it carries SSE without a browser interstitial; Cloudflare Quick Tunnel is the ttyd default because it carries WebSocket. Serveo remains legacy-only. Providers must pass protocol-level smoke tests before release.
- **Git as durability boundary:** loss of the Colab VM must be treated as normal.
- **No automatic Git mutation in v0.1:** warnings and status are safer than implicit commits/pushes until recovery semantics are designed.
- **Explicit health probes:** process existence alone is insufficient evidence of readiness.

## 12. Definition of done for v0.1

v0.1 is complete only when a fresh CPU Colab runtime can use the repository notebook to:

1. use the Python Colab adapter to bootstrap a verified ColabNomad Go binary without manual shell preparation;
2. clone a selected target repository;
3. launch OpenCode and a tmux-backed browser terminal;
4. expose authenticated usable endpoints through capability-compatible tunnel providers, with OpenCode SSE verified end-to-end;
5. report health and logs through the CLI;
6. restart an individual failed managed service without recreating the whole runtime;
7. preserve a dirty target worktree during normal restart/recovery operations;
8. pass the repository's deterministic test suite.

The VM may still be terminated by Colab at any time; fast, reproducible reconstruction is the reliability target.
