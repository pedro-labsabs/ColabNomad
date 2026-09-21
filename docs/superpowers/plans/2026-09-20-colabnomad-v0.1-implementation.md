# ColabNomad v0.1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a reproducible Colab development runtime that bootstraps from a minimal notebook, runs an independent Go supervisor, exposes authenticated OpenCode v2 and a tmux-backed web terminal, and can reconstruct safely after a Colab VM disappears.

**Architecture:** Python is only the Colab adapter and hands off to a detached Go control plane over a Unix-domain socket. The Go daemon owns workspace preparation, verified tool installation, service supervision, health checks, credentials, tunnel selection, diagnostics, and lifecycle commands. Tunnel providers declare protocol capabilities; OpenCode may only use a provider that supports SSE.

**Tech Stack:** Go 1.27.1, Python 3.12 standard library + `google.colab`, Linux amd64/arm64, OpenCode v2 2.0.11, tmux, ttyd 1.7.7, OpenSSH/Serveo, cloudflared 2026.9.1, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-20-colabnomad-core-design.md`

## Global Constraints

- CPU Colab is the default; no GPU requirement.
- Treat `/content` and the whole VM as disposable; Git remotes are the durability boundary.
- Notebook cells only select refs/repos, clone/update ColabNomad, and invoke `colab/bootstrap.py`.
- Never simulate activity, defeat idle policies, or depend on an assumed Colab session duration.
- Bind managed HTTP services to `127.0.0.1`; public exposure happens only through authenticated tunnel routes.
- Python must not own long-lived services; Go owns every persistent child process and in-session state.
- Shell commands are explicit argv invocations, not an orchestration layer.
- Preserve dirty target worktrees; normal recovery must never run destructive reset/clean operations.
- Do not execute arbitrary target-repository code merely because the repository was cloned.
- External tools are pinned or compatibility-checked; runtime installation never uses an unbounded `latest` channel.
- Tunnel selection fails closed when the provider lacks a required capability.
- Normal status/log output must redact tokens, passwords, Authorization headers, and credential-bearing URLs.
- No automatic Git commits or pushes in v0.1.

## Review Focus

1. **Credentials with punctuation/newlines:** Git/OpenCode secrets must stay out of argv, remote URLs, state JSON, and logs. Task 3 and Task 9 add leakage tests.
2. **Stale daemon state:** a dead Unix socket or reused PID must be recovered without signaling an unrelated process. Task 8 adds stale-state tests.
3. **Occupied service port:** `up` must report the owning component/port and must never kill an unknown listener. Task 8 adds a collision test.
4. **Tunnel buffers SSE:** a provider that returns HTTP 200 but does not deliver an SSE frame within the deadline must fail OpenCode readiness. Task 7 adds streaming/buffering tests.
5. **Existing dirty workspace:** reconnect/restart must leave modified and untracked files untouched. Task 3 adds an integration test with a disposable Git repository.

---

## File Map

- `go.mod` — module identity and Go/toolchain floor.
- `config/versions.json` — pinned external artifacts for Linux amd64/arm64.
- `internal/config/` — runtime configuration, version manifest parsing, validation.
- `internal/execx/` — one-shot commands plus Linux managed process groups.
- `internal/artifact/` — verified downloads and safe tar extraction.
- `internal/secure/` — random credentials and redaction helpers.
- `internal/deps/` — system binary compatibility checks and minimal apt fallback.
- `internal/health/` — authenticated localhost/public HTTP health probing.
- `internal/state/` — atomic runtime state and protected credential storage.
- `internal/workspace/` — clone/open target repositories without destructive mutation.
- `internal/supervisor/` — dependency ordering, readiness, restart/backoff, stop semantics.
- `internal/services/terminal/` — tmux session + ttyd adapter.
- `internal/services/opencode/` — pinned OpenCode v2 web adapter.
- `internal/services/tunnel/` — provider capabilities, Serveo, Cloudflare Quick, SSE probing.
- `internal/control/` — Unix-socket request/response protocol.
- `internal/app/` — composition root for `up/status/doctor/logs/restart/down`.
- `cmd/colabnomad/` — CLI and hidden daemon entry point.
- `colab/bootstrap.py` — minimal Colab adapter; `colab/test_bootstrap.py` tests it.
- `notebook/colabnomad.ipynb` — two-cell bootstrap notebook; `notebook/test_notebook.py` enforces minimality.
- `tests/smoke/` — real public-tunnel protocol smoke tests.
- `.github/workflows/ci.yml` — deterministic tests/builds.
- `.github/workflows/release.yml` — live tunnel gate plus Linux release artifacts.
- `README.md` — user workflow, secrets, recovery, diagnostics.

---

### Task 1: Runtime configuration and pinned version manifest

**Files:**
- Create: `go.mod`
- Create: `config/versions.json`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/config/versions.go`
- Create: `internal/config/versions_test.go`**Interfaces:**
- Produces: `config.RuntimeConfig`, `config.TunnelProviderName`, `config.Artifact`, `config.ToolSpec`, `config.Versions`, `config.LoadVersions(path)`, `config.PlatformKey(goos, goarch)`.
- Consumes: nothing; this is the root contract for later tasks.

- [ ] **Step 1: Write failing configuration tests**

```go
func TestDefaultConfigUsesServeoAndLocalhost(t *testing.T) {
    got := Default("https://github.com/example/project.git")
    if got.OpenCodeTunnel != TunnelServeo || got.TerminalTunnel != TunnelServeo { t.Fatal(got) }
    if got.OpenCodePort != 4096 || got.TerminalPort != 7681 { t.Fatal(got) }
}

func TestPlatformKey(t *testing.T) {
    got, err := PlatformKey("linux", "amd64")
    if err != nil || got != "linux-amd64" { t.Fatalf("%q %v", got, err) }
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/config -v`
Expected: FAIL because the package/types do not exist.

- [ ] **Step 3: Add the module and minimal config contracts**

```go
type TunnelProviderName string
const ( TunnelServeo TunnelProviderName = "serveo"; TunnelCloudflare TunnelProviderName = "cloudflare" )type RuntimeConfig struct {
    StateDir, WorkspaceRoot, RepoURL, RepoRef string
    OpenCodePort, TerminalPort int
    OpenCodeTunnel, TerminalTunnel TunnelProviderName
}

type Artifact struct { URL, Integrity, Member string }
type ToolSpec struct { Version string; Artifacts map[string]Artifact }
type Versions struct { BootstrapGo, OpenCode, TTYD, Cloudflared ToolSpec }
```

`go.mod` must use module `github.com/pedroteste00000008-stack/ColabNomad`, `go 1.27.0`, and `toolchain go1.27.1`.

- [ ] **Step 4: Add exact initial pins to `config/versions.json`**

Use these values, preserving both architectures:

```json
{
  "bootstrap_go": {"version":"1.27.1","artifacts":{
    "linux-amd64":{"url":"https://go.dev/dl/go1.27.1.linux-amd64.tar.gz","integrity":"sha256:63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445"},
    "linux-arm64":{"url":"https://go.dev/dl/go1.27.1.linux-arm64.tar.gz","integrity":"sha256:3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec"}}},
  "opencode": {"version":"2.0.11","artifacts":{
    "linux-amd64":{"url":"https://registry.npmjs.org/@opencode/cli-linux-x64/-/cli-linux-x64-2.0.11.tgz","integrity":"sha512-L+OgUSSTSu6chQrqL19boZ+xXxOzmETXwoKFXi/Pzpu79MRSuZkfPsunIBrf68qAIkgDaTC8HUXNvLZ80ZBLvQ==","member":"package/bin/opencode"},
    "linux-arm64":{"url":"https://registry.npmjs.org/@opencode/cli-linux-arm64/-/cli-linux-arm64-2.0.11.tgz","integrity":"sha512-2dwuF+X1ONjWy6jvJocevd+GFeMz5e1j7Mz6N38Ao0vwBaAuqrCN7SrmYKn57EUilcPXcIi4xrxG/dP+rR94gA==","member":"package/bin/opencode"}}},
  "ttyd": {"version":"1.7.7","artifacts":{
    "linux-amd64":{"url":"https://github.com/tsl0922/ttyd/releases/download/1.7.7/ttyd.x86_64","integrity":"sha256:8a217c968aba172e0dbf3f34447218dc015bc4d5e59bf51db2f2cd12b7be4f55"},
    "linux-arm64":{"url":"https://github.com/tsl0922/ttyd/releases/download/1.7.7/ttyd.aarch64","integrity":"sha256:b38acadd89d1d396a0f5649aa52c539edbad07f4bc7348b27b4f4b7219dd4165"}}},
  "cloudflared": {"version":"2026.9.1","artifacts":{
    "linux-amd64":{"url":"https://github.com/cloudflare/cloudflared/releases/download/2026.9.1/cloudflared-linux-amd64","integrity":"sha256:03f1f25d1cc93b9ad6c60569d44060bc4f17ed97075760ed8cfca4b12dcd68cc"},
    "linux-arm64":{"url":"https://github.com/cloudflare/cloudflared/releases/download/2026.9.1/cloudflared-linux-arm64","integrity":"sha256:3d97437c71848bd8df68041e12436b484a661d95073ea1937f01a845ce88faa3"}}}
}
```

- [ ] **Step 5: Make manifest/config tests green**

Run: `go test ./internal/config -v && go vet ./internal/config`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod config/versions.json internal/config
git commit -m "chore: establish runtime configuration and version pins"
```

### Task 2: Verified artifact, command, and secret primitives

**Files:**
- Create: `internal/execx/runner.go`
- Create: `internal/execx/runner_test.go`
- Create: `internal/artifact/install.go`
- Create: `internal/artifact/install_test.go`
- Create: `internal/secure/secure.go`
- Create: `internal/secure/secure_test.go`

**Interfaces:**
- Consumes: `config.Artifact`.
- Produces: `execx.Runner`, `execx.Spec`, `artifact.Installer.Ensure(ctx, artifact, dst)`, `secure.RandomPassword(n)`, `secure.Redact(text, secrets...)`.

- [ ] **Step 1: Write RED tests for integrity and redaction**

```go
func TestEnsureRejectsBadDigestWithoutLeavingBinary(t *testing.T) {
    // httptest.Server serves known bytes; spec carries a different sha256.
    // Assert Ensure returns an integrity error and dst does not exist.
}

func TestRedactRemovesEverySecret(t *testing.T) {
    got := secure.Redact("token=abc password=p@ss", "abc", "p@ss")
    if strings.Contains(got, "abc") || strings.Contains(got, "p@ss") { t.Fatal(got) }
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/artifact ./internal/execx ./internal/secure -v`
Expected: FAIL because packages are missing.

- [ ] **Step 3: Implement one-shot command execution and verified installation**

```go
type Spec struct { Path string; Args []string; Dir string; Env map[string]string }
type Result struct { Stdout, Stderr string; ExitCode int }
type Runner interface { Run(context.Context, Spec) (Result, error); LookPath(string) (string, error) }

type Installer struct { Client *http.Client }
func (i Installer) Ensure(ctx context.Context, a config.Artifact, dst string) error
```

`Ensure` must stream into a temporary file, accept only `sha256:<hex>` or `sha512-<base64>`, verify before rename, extract only the exact configured tar member when `Member` is non-empty, reject traversal, chmod `0755`, then atomically rename.

- [ ] **Step 4: Implement cryptographic credentials and redaction**

```go
func RandomPassword(n int) (string, error) // crypto/rand; URL-safe alphabet
func Redact(text string, secrets ...string) string // replace non-empty secrets with "[REDACTED]"
```

- [ ] **Step 5: Add archive traversal and SHA-512 tests, then run GREEN**

Run: `go test ./internal/artifact ./internal/execx ./internal/secure -v`
Expected: PASS, including bad digest, missing tar member, traversal, and redaction cases.

- [ ] **Step 6: Commit**

```bash
git add internal/artifact internal/execx internal/secure
git commit -m "feat: add verified artifact and execution primitives"
```

### Task 3: Atomic runtime state and non-destructive workspace preparation

**Files:**
- Create: `internal/state/types.go`
- Create: `internal/state/store.go`
- Create: `internal/state/store_test.go`
- Create: `internal/workspace/workspace.go`
- Create: `internal/workspace/workspace_test.go`

**Interfaces:**
- Consumes: `execx.Runner`, `secure.Redact`.
- Produces: `state.Store`, `state.RuntimeState`, `state.Credentials`, `workspace.Manager.Prepare(ctx, Request) (Result, error)`.

- [ ] **Step 1: Write failing state/workspace tests**

```go
func TestStoreWritesStateAndCredentials0600(t *testing.T) {
    // Save state + credentials, stat files, require mode 0600, load round-trip.
}

func TestPreparePreservesDirtyExistingRepo(t *testing.T) {
    // Create local bare origin, clone, modify tracked file + add untracked file.
    // Prepare again and assert both local changes are byte-for-byte unchanged.
}
```

- [ ] **Step 2: Add the state model and atomic store**

```go
type RuntimeState struct {
    SchemaVersion int `json:"schema_version"`
    DaemonPID int `json:"daemon_pid"`
    WorkspacePath string `json:"workspace_path"`
    Services map[string]ServiceState `json:"services"`
    Endpoints map[string]string `json:"endpoints"`
    UpdatedAt time.Time `json:"updated_at"`
}
type Credentials struct { OpenCodeUser, OpenCodePassword, TerminalUser, TerminalPassword string }
type Store struct { Dir string }
func (s Store) Load() (RuntimeState, error)
func (s Store) Save(RuntimeState) error
func (s Store) LoadCredentials() (Credentials, error)
func (s Store) SaveCredentials(Credentials) error
```

Use temp-file + `fsync` + rename; state and credentials stay separate. No secret field exists in `RuntimeState`.

- [ ] **Step 3: Implement workspace preparation without embedding tokens in URLs**

```go
type Request struct { RepoURL, Ref, Root, GitHubToken string }
type Result struct { Path, Commit string; Dirty bool }
func (m Manager) Prepare(ctx context.Context, req Request) (Result, error)
```When cloning GitHub with a token, create a `0700` askpass helper that reads `COLABNOMAD_GITHUB_TOKEN` from the process environment; use `GIT_ASKPASS` and `GIT_TERMINAL_PROMPT=0`. Never rewrite `origin` to a credential-bearing URL. Existing repositories are fetched only when explicitly safe; dirty files are never reset or cleaned.

- [ ] **Step 4: Add leakage and dirty-tree review-focus tests**

Assert that runner argv, saved state, `git remote get-url origin`, and captured logs never contain the test token, including a token containing `:`, `@`, and newline-like punctuation.

- [ ] **Step 5: Run GREEN**

Run: `go test ./internal/state ./internal/workspace -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/state internal/workspace
git commit -m "feat: add durable runtime state and workspace preparation"
```

### Task 4: Linux managed processes and restart-bounded supervisor

**Files:**
- Create: `internal/execx/process.go`
- Create: `internal/execx/process_linux.go`
- Create: `internal/execx/process_test.go`
- Create: `internal/supervisor/service.go`
- Create: `internal/supervisor/manager.go`
- Create: `internal/supervisor/manager_test.go`**Interfaces:**
- Consumes: `execx.Spec`.
- Produces: `execx.ProcessRunner`, `execx.ProcessHandle`, `supervisor.Service`, `supervisor.Manager`, `supervisor.RestartPolicy`.

- [ ] **Step 1: Write RED tests for ordering, process groups, and bounded restart**

```go
func TestManagerStartsDependenciesBeforeDependents(t *testing.T) {
    // fake services: tunnel depends on opencode; assert prepare/start/probe order.
}

func TestManagerStopsAfterRestartBudget(t *testing.T) {
    // fake process exits immediately; fake clock records 500ms,1s,2s backoff.
    // require exactly 4 starts for MaxRestarts=3 and final Unhealthy state.
}
```

- [ ] **Step 2: Add managed process contracts**

```go
type ManagedSpec struct { Spec; StdoutPath, StderrPath string }
type ProcessHandle interface { PID() int; Done() <-chan error; Stop(grace time.Duration) error }
type ProcessRunner interface { Start(ManagedSpec) (ProcessHandle, error) }
```

Linux `Start` sets `SysProcAttr.Setpgid=true`; `Stop` signals the negative PGID with SIGTERM, waits for the grace period, then SIGKILLs the group. Never signal a PID loaded only from stale JSON.

- [ ] **Step 3: Add supervisor contracts**```go
type Service interface {
    Name() string
    Dependencies() []string
    Prepare(context.Context) error
    Command() execx.ManagedSpec
    Probe(context.Context) error
    Cleanup(context.Context) error
}
type RestartPolicy struct { MaxRestarts int; InitialBackoff, MaxBackoff time.Duration }
```

`Manager.StartAll` performs topological ordering, starts one service only after dependencies are healthy, and waits for readiness. Unexpected exits use exponential backoff capped at 8s; default policy is 3 restarts with initial 500ms. `Restart(name)` stops/restarts only that service after dependency checks. `StopAll` always stops in reverse dependency order so public tunnels disappear before their local services.

- [ ] **Step 4: Add deterministic fake-clock/fake-process tests**

Cover dependency cycles, readiness timeout, process exit before readiness, stop escalation, and cancellation during backoff.

- [ ] **Step 5: Run GREEN and race detector for the supervisor**

Run: `go test -race ./internal/execx ./internal/supervisor -v`
Expected: PASS with no race reports.

- [ ] **Step 6: Commit**

```bash
git add internal/execx internal/supervisor
git commit -m "feat: add supervised process lifecycle"
```

### Task 5: tmux-backed authenticated web terminal

**Files:**
- Create: `internal/deps/system.go`
- Create: `internal/deps/system_test.go`
- Create: `internal/health/http.go`
- Create: `internal/health/http_test.go`
- Create: `internal/services/terminal/service.go`
- Create: `internal/services/terminal/service_test.go`

**Interfaces:**
- Consumes: `execx.Runner`, `execx.ManagedSpec`, `supervisor.Service`, protected terminal credentials.
- Produces: `deps.System.Ensure(ctx, binary, aptPackage)`, `health.Prober`, `terminal.Service`, `terminal.Service.DestroySession(ctx)`.

- [ ] **Step 1: Write RED tests for tmux reuse and ttyd command construction**

```go
func TestPrepareReusesExistingTmuxSession(t *testing.T) {
    // Fake `tmux has-session` success; assert no `new-session` command is issued.
}

func TestCommandBindsLocalhostAndRequiresWritableAuth(t *testing.T) {
    spec := svc.Command()
    joined := strings.Join(spec.Args, " ")
    for _, want := range []string{"-i 127.0.0.1", "-p 7681", "-c terminal:", "-W", "tmux attach-session -t colabnomad"} {
        if !strings.Contains(joined, want) { t.Fatalf("missing %q: %s", want, joined) }
    }
}
```

- [ ] **Step 2: Implement system dependency resolution and HTTP probing**

`deps.System.Ensure` first uses `LookPath`. Only when absent and running as root on Debian/Ubuntu may it run `apt-get -qq update` once per process and `apt-get -qq install -y <package>`, then re-run `LookPath`. It is used for `tmux` and `ssh`; it must return an actionable error on unsupported systems.

```go
type BasicAuth struct { Username, Password string }
type Request struct { URL string; Auth *BasicAuth; Headers map[string]string; WantStatus int; WantContentType string }
type Prober struct { Client *http.Client }
func (p Prober) Do(ctx context.Context, req Request) ([]byte, error)
```

- [ ] **Step 3: Implement terminal service**

`Prepare` must ensure `tmux`, run `tmux has-session -t colabnomad`, and only on absence run `tmux new-session -d -s colabnomad -c <workspace>`. `Command` runs:

```text
ttyd -i 127.0.0.1 -p 7681 -c terminal:<generated-password> -W -w <workspace> tmux attach-session -t colabnomad
```

`Probe` performs authenticated HTTP GET `/` through `health.Prober.Do` and requires 200. `Cleanup` is a no-op so restarting ttyd preserves the tmux shell. `DestroySession` is called only by explicit full `down`.

- [ ] **Step 4: Add tests for auth and session-destruction semantics**

Use `httptest.Server` to require Basic Auth; assert wrong/missing credentials fail. Assert service restart never calls `tmux kill-session`, while `DestroySession` does exactly once.

- [ ] **Step 5: Run GREEN**Run: `go test ./internal/deps ./internal/health ./internal/services/terminal -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/deps internal/health internal/services/terminal
git commit -m "feat: add tmux-backed web terminal service"
```

### Task 6: Pinned OpenCode v2 web service

**Files:**
- Create: `internal/services/opencode/service.go`
- Create: `internal/services/opencode/service_test.go`

**Interfaces:**
- Consumes: verified OpenCode binary path, workspace path, `health.Prober`, credentials, `supervisor.Service`.
- Produces: `opencode.Service` with localhost `serve` command, external ephemeral config, and authenticated `/api/project` readiness.

- [ ] **Step 1: Write RED tests for command/environment and health**

```go
func TestCommandUsesWorkspaceLocalhostAndBasicAuthEnv(t *testing.T) {
    spec := svc.Command()
    if spec.Dir != "/content/workspaces/project" { t.Fatal(spec.Dir) }
    if got := strings.Join(spec.Args, " "); got != "serve --hostname 127.0.0.1 --port 4096" { t.Fatal(got) }
    if spec.Env["OPENCODE_SERVER_USERNAME"] != "opencode" || spec.Env["OPENCODE_SERVER_PASSWORD"] == "" { t.Fatal(spec.Env) }
    if spec.Env["OPENCODE_DISABLE_AUTOUPDATE"] != "1" { t.Fatal(spec.Env) }
    if strings.HasPrefix(spec.Env["OPENCODE_CONFIG"], spec.Dir) { t.Fatal("config must be outside workspace") }
}
```

- [ ] **Step 2: Implement the adapter**

```go
type Config struct {
    Binary, Workspace, StateDir, Version, Username, Password, APIKey string
    Port int
}
type Service struct { cfg Config; prober health.Prober }
func New(cfg Config, p health.Prober) *Service
```

`Prepare` writes `<stateDir>/opencode/opencode.json` outside the target repository with mode `0600` and exact initial content `{"$schema":"https://opencode.ai/config.json"}`, sets `OPENCODE_CONFIG` to that file, sets `OPENCODE_DISABLE_AUTOUPDATE=1`, and runs `<binary> --version`, requiring exactly `opencode v2.0.11`. `Command` runs the pinned v2 binary as `opencode serve --hostname 127.0.0.1 --port <port>`. It inherits only the sanitized runtime environment plus explicit OpenCode/provider secrets; it never binds `0.0.0.0`.

`Probe` performs Basic-Auth GET `http://127.0.0.1:<port>/api/project` with header `x-opencode-directory: <workspace>`, requires status 200 + `application/json`, decodes the project list, and requires an entry whose `canonical` path equals the workspace. Do not probe legacy `/global/*` routes or the currently absent `/api/health` route.

- [ ] **Step 3: Add readiness mismatch and secret-output tests**

Use `httptest.Server` for 200 JSON with matching workspace, 401, malformed JSON, and a project list that omits the workspace. Add a fake runner test for wrong `--version`. Verify formatting an error or service status cannot include the password or API key.

- [ ] **Step 4: Run GREEN**

Run: `go test ./internal/services/opencode -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/services/opencode
git commit -m "feat: add OpenCode v2 service adapter"
```

### Task 7: Capability-aware Serveo and Cloudflare tunnel providers

**Files:**
- Create: `internal/services/tunnel/provider.go`
- Create: `internal/services/tunnel/service.go`
- Create: `internal/services/tunnel/serveo.go`
- Create: `internal/services/tunnel/cloudflare.go`
- Create: `internal/services/tunnel/sse.go`
- Create: `internal/services/tunnel/tunnel_test.go`
- Create: `tests/smoke/serveo_sse_test.go`

**Interfaces:**
- Consumes: `execx.ManagedSpec`, `health.BasicAuth`, state directory, selected provider name.
- Produces: `tunnel.Capabilities`, `tunnel.Requirements`, `tunnel.Provider`, `tunnel.Service`, `tunnel.ProbeSSE`.

- [ ] **Step 1: Write RED capability tests**

```go
type Capabilities struct { SSE, WebSocket bool }
type Requirements struct { SSE, WebSocket bool }

func TestCloudflareQuickRejectedForOpenCode(t *testing.T) {
    err := Validate(NewCloudflare("/bin/cloudflared"), Requirements{SSE:true})
    if err == nil { t.Fatal("expected SSE capability rejection") }
}

func TestServeoSatisfiesOpenCodeRequirements(t *testing.T) {
    if err := Validate(NewServeo("/usr/bin/ssh", "/state/known_hosts"), Requirements{SSE:true}); err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: Implement provider commands and URL discovery**

Serveo capabilities are `{SSE:true, WebSocket:true}` and command argv is:

```text
ssh -T -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=<state>/serveo_known_hosts -R 80:127.0.0.1:<port> serveo.net
```

Parse only trusted `https://*.serveo.net` or `https://*.serveousercontent.com` URLs from provider output. The second host family was confirmed by the required live Serveo smoke during implementation. Cloudflare Quick capabilities are `{SSE:false, WebSocket:true}` and command argv is:

```text
cloudflared tunnel --no-autoupdate --url http://127.0.0.1:<port>
```

Parse only `https://*.trycloudflare.com`. Never accept an arbitrary URL printed by a child process.

- [ ] **Step 3: Implement tunnel service and fail-closed selection**

```go
type Provider interface {
    Name() string
    Capabilities() Capabilities
    Command(localPort int, stateDir string) execx.ManagedSpec
    DiscoverURL(output string) (string, error)
}
func Validate(Provider, Requirements) error
```

`tunnel.Service` depends on its local service (`opencode` or `terminal`), launches the provider, discovers the public URL from its log, then probes that exact endpoint before becoming healthy. It exposes `func (s *Service) PublicURL() string` after discovery; `Runtime.Up` reads only this method when building the connection summary.

- [ ] **Step 4: Implement SSE streaming probe and buffering regression test**

```go
func ProbeSSE(ctx context.Context, client *http.Client, url string, auth *health.BasicAuth, firstFrame time.Duration) error
```

For OpenCode, call this against `<publicURL>/api/event`. Send `Accept: text/event-stream`, require 2xx and `Content-Type: text/event-stream`, then require a non-comment SSE field (`data:` or `event:`) before `firstFrame`. A test server that sends headers immediately but delays the first frame past the deadline must return a distinct streaming-timeout error.

- [ ] **Step 5: Add real Serveo smoke test behind build tag**

`tests/smoke/serveo_sse_test.go` uses `//go:build live`. It starts a local SSE server that emits `data: ready\n\n`, creates an actual Serveo tunnel through the provider, discovers the public URL, and requires `ProbeSSE` to receive the frame. Skip only when `COLABNOMAD_LIVE_TUNNEL_TEST` is not `1`; when enabled, any Serveo failure is a test failure.

- [ ] **Step 6: Run deterministic GREEN**

Run: `go test ./internal/services/tunnel -v`
Expected: PASS without external network dependency.

- [ ] **Step 7: Commit**

```bash
git add internal/services/tunnel tests/smoke
git commit -m "feat: add capability-aware tunnel providers"
```

### Task 8: Detached Go daemon, Unix control plane, and CLI lifecycle

**Files:**
- Create: `internal/control/protocol.go`
- Create: `internal/control/server.go`
- Create: `internal/control/client.go`
- Create: `internal/control/control_test.go`
- Create: `internal/app/runtime.go`
- Create: `internal/app/runtime_test.go`
- Create: `internal/app/doctor.go`
- Create: `cmd/colabnomad/main.go`
- Create: `cmd/colabnomad/daemon_linux.go`
- Create: `cmd/colabnomad/main_test.go`

**Interfaces:**
- Consumes: all prior managers/services.
- Produces: local control protocol and commands `up`, `status`, `doctor`, `logs`, `restart`, `down`; hidden `daemon` subcommand.

- [ ] **Step 1: Write RED protocol and stale-state tests**

```go
type Request struct { Command string `json:"command"`; Payload json.RawMessage `json:"payload,omitempty"` }
type Response struct { OK bool `json:"ok"`; Error string `json:"error,omitempty"`; Payload json.RawMessage `json:"payload,omitempty"` }

func TestEnsureDaemonRemovesUndialableSocketWithoutKillingStoredPID(t *testing.T) {
    // Write stale socket/state with PID equal to a harmless live helper.
    // ensureDaemon must replace the socket and the helper must still be alive.
}
```

- [ ] **Step 2: Implement Unix-socket server/client**

Socket path is `<stateDir>/control.sock`, mode `0600`. Encode exactly one JSON request and one JSON response per connection with bounded message size. The server never logs raw payloads because `up` may carry credentials.

- [ ] **Step 3: Implement runtime composition and `up`**

```go
type UpRequest struct { RepoURL, RepoRef, GitHubToken, OpenCodeAPIKey string; OpenCodeTunnel, TerminalTunnel config.TunnelProviderName }
type ConnectionSummary struct { OpenCodeURL, TerminalURL, OpenCodeUser, OpenCodePassword, TerminalUser, TerminalPassword string }
func (r *Runtime) Up(context.Context, UpRequest) (ConnectionSummary, error)
```

`Up` validates ports/config, resolves platform + pinned artifacts, ensures `tmux` and `ssh` as needed, prepares the workspace, loads or generates credentials, constructs `terminal`, `opencode`, `tunnel:terminal`, and `tunnel:opencode`, then starts the supervisor. Persist only non-secret status/endpoints; return credentials only in the `up` response.

- [ ] **Step 4: Implement detached daemon startup**

If the socket dials successfully, reuse the daemon. Otherwise remove only the stale socket file and start the current executable as `daemon --state-dir <dir>` with `SysProcAttr.Setsid=true`, stdout/stderr to `<stateDir>/logs/daemon.log`, and `Process.Release()`. Wait up to 5s for the socket; never signal a PID merely because it appears in saved state.

- [ ] **Step 5: Implement lifecycle commands and doctor**

`status` returns redacted service states/endpoints; `doctor` checks state dir, workspace, tool paths/versions, socket, configured ports, service probes, and provider capabilities; `logs <service>` returns a redacted tail; `restart <service>` uses supervisor restart; `down` stops managed services, destroys the tmux session, closes/removes the socket, and exits the daemon.

- [ ] **Step 6: Add occupied-port review-focus test**

Bind an unrelated listener on 4096, call `Up`, require an actionable `port 4096 already in use` error, then prove the unrelated listener is still accepting connections. Add tests that `status`/`logs` never include generated credentials.

Also add `TestDoctorClassifiesHealthyAndUnhealthyServices`: feed one healthy and one failed fake probe, require per-component classifications plus a non-zero overall unhealthy result without mutating either service.

- [ ] **Step 7: Add CLI parsing tests**

Cover:

```text
colabnomad up --repo <url> [--ref <ref>] [--opencode-tunnel serveo] [--terminal-tunnel serveo|cloudflare]
colabnomad status
colabnomad doctor
colabnomad logs <service>
colabnomad restart <service>
colabnomad down
```

Unknown providers/commands exit non-zero with usage; `cloudflare` for `--opencode-tunnel` is rejected before daemon mutation.

- [ ] **Step 8: Run GREEN with race detector**

Run: `go test -race ./internal/control ./internal/app ./cmd/colabnomad -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/control internal/app cmd/colabnomad
git commit -m "feat: add daemon control plane and CLI lifecycle"
```

### Task 9: Minimal Python Colab bootstrap and Go handoff

**Files:**
- Create: `colab/bootstrap.py`
- Create: `colab/test_bootstrap.py`

**Interfaces:**
- Consumes: checked-out repo, `config/versions.json`, optional Colab Secrets.
- Produces: a verified `colabnomad` binary and one bounded `colabnomad up` invocation; Python exits after that command returns.

- [ ] **Step 1: Write RED bootstrap tests with `unittest.mock`**

```python
def test_checksum_mismatch_never_executes_binary(self):
    # Serve/download bytes whose hash differs from the manifest.
    # Assert BootstrapError and subprocess.run was never called for colabnomad.

def test_secret_is_environment_only_not_argv(self):
    token = 'ghp:a@b!punctuation'
    # Mock userdata.get -> token; run handoff builder.
    # Assert token not in argv/repr(argv) and env['GITHUB_TOKEN'] == token.
```

- [ ] **Step 2: Implement bootstrap configuration and platform mapping**

```python
@dataclass(frozen=True)
class BootstrapConfig:
    target_repo: str
    target_ref: str = ''
    release: str = ''
    state_dir: Path = Path('/content/.colabnomad')
```The CLI accepts `--target-repo`, `--target-ref`, `--release`, and `--state-dir`. Expose pure functions `platform_key()`, `load_versions(repo_root)`, `download_verified(url, integrity, dst)`, `try_release(config, repo_root)`, `build_checked_out(config, repo_root)`, `collect_colab_secrets()`, and `run_up(binary, config, secrets)` so tests can isolate them.

- [ ] **Step 3: Implement release-first, source-build fallback**

For `--release v0.1.0`, try:

```text
https://github.com/pedroteste00000008-stack/ColabNomad/releases/download/v0.1.0/colabnomad-linux-<amd64|arm64>
https://github.com/pedroteste00000008-stack/ColabNomad/releases/download/v0.1.0/SHA256SUMS
```

Require the named binary to be present in `SHA256SUMS` and verify SHA-256 before chmod/rename. On release absence, read `bootstrap_go` from `config/versions.json`, verify/extract the pinned Go archive into `<stateDir>/toolchains/go-1.27.1`, then run from the checked-out repository:

```text
CGO_ENABLED=0 GOOS=linux GOARCH=<arch> <go>/bin/go build -trimpath -ldflags "-s -w" -o <stateDir>/bin/colabnomad ./cmd/colabnomad
```

Never fetch a different ColabNomad ref/version as fallback.

- [ ] **Step 4: Implement Colab Secret collection and sanitized handoff**

Read `GITHUB_TOKEN` and `OPENCODE_API_KEY` through `google.colab.userdata.get` when available; missing secrets are allowed. Start from an allowlist of `HOME`, `PATH`, `LANG`, `LC_ALL`, `SHELL`, `TERM`, `TMPDIR`, then add found secrets. Invoke:

```text
<binary> up --repo <target_repo> [--ref <target_ref>]
```The Python process must not daemonize or supervise anything itself.

- [ ] **Step 5: Add fallback-build and Python-lifetime tests**

Mock release 404 and assert the exact pinned Go archive is selected. Mock successful `colabnomad up` and assert bootstrap returns immediately after that subprocess exits; no background Python thread/process is created.

- [ ] **Step 6: Run GREEN**

Run: `python3 -m unittest colab.test_bootstrap -v`
Expected: PASS using only the standard library.

- [ ] **Step 7: Commit**

```bash
git add colab/bootstrap.py colab/test_bootstrap.py
git commit -m "feat: add Colab bootstrap adapter"
```

### Task 10: Minimal reproducible Colab notebook

**Files:**
- Create: `notebook/colabnomad.ipynb`
- Create: `notebook/test_notebook.py`

**Interfaces:**
- Consumes: public ColabNomad repo/ref and `colab/bootstrap.py`.
- Produces: a notebook that only configures, checks out, and hands off.

- [ ] **Step 1: Write RED notebook-structure tests**

```python
def test_notebook_has_only_two_code_cells():    nb = json.load(open('notebook/colabnomad.ipynb'))
    code = [c for c in nb['cells'] if c['cell_type'] == 'code']
    assert len(code) == 2

def test_notebook_contains_no_runtime_orchestration():
    text = Path('notebook/colabnomad.ipynb').read_text()
    for banned in ('cloudflared tunnel', 'ttyd ', 'opencode web', 'opencode serve', 'apt-get', 'nohup'):
        assert banned not in text
```

- [ ] **Step 2: Create configuration cell**

Cell 1 contains only editable values:

```python
COLABNOMAD_REPO = 'https://github.com/pedroteste00000008-stack/ColabNomad.git'
COLABNOMAD_REF = 'v0.1.0'
COLABNOMAD_RELEASE = 'v0.1.0'
TARGET_REPO = ''  # user must set this
TARGET_REF = ''
```

- [ ] **Step 3: Create checkout/handoff cell**

Cell 2 validates `TARGET_REPO`, clones the kit if absent, otherwise fetches without merging, then executes:

```text
git -C /content/colabnomad-kit fetch --depth=1 origin v0.1.0
git -C /content/colabnomad-kit checkout --detach FETCH_HEADpython /content/colabnomad-kit/colab/bootstrap.py --target-repo <TARGET_REPO> --release v0.1.0 [--target-ref <TARGET_REF>]
```

Use `subprocess.run([...], check=True)` with argv arrays; do not use notebook shell magics for runtime logic.

- [ ] **Step 4: Run GREEN and JSON validation**

Run: `python3 -m unittest notebook.test_notebook -v && python3 -m json.tool notebook/colabnomad.ipynb >/dev/null`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add notebook
git commit -m "feat: add minimal Colab notebook"
```

### Task 11: CI, live tunnel release gate, and Linux release artifacts

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: all repository tests and `tests/smoke/serveo_sse_test.go`.
- Produces: deterministic CI and tagged `linux-amd64`/`linux-arm64` release assets with `SHA256SUMS`.

- [ ] **Step 1: Add CI workflow**

On pushes and pull requests, use `actions/checkout`, `actions/setup-go` with `1.27.1`, and `actions/setup-python` with `3.12`, then run:```text
go test ./...
go test -race ./...
go vet ./...
python3 -m unittest colab.test_bootstrap notebook.test_notebook -v
go build -trimpath ./cmd/colabnomad
git diff --check
```

- [ ] **Step 2: Add release live-protocol gate**

`release.yml` triggers on tags matching `v*`. Before building/uploading artifacts, run on Ubuntu:

```text
COLABNOMAD_LIVE_TUNNEL_TEST=1 go test -tags=live ./tests/smoke -run TestServeoSSE -v
```

This job is not allowed to continue on error. A provider outage or SSE buffering failure blocks the release because Serveo is the default OpenCode transport.

- [ ] **Step 3: Build reproducible release binaries**

Build with:

```text
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/colabnomad-linux-amd64 ./cmd/colabnomad
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/colabnomad-linux-arm64 ./cmd/colabnomad
(cd dist && sha256sum colabnomad-linux-* > SHA256SUMS)
```Use GitHub's `gh release create "$GITHUB_REF_NAME" dist/* --generate-notes` with `contents: write`; the release job depends on CI-equivalent verification and the live Serveo SSE gate.

- [ ] **Step 4: Validate workflow syntax locally where possible**

At minimum parse both YAML files with Python if PyYAML is available; regardless, inspect `git diff --check` and verify all referenced commands/files exist. CI itself is the authoritative workflow execution check after push.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows
git commit -m "ci: add verification and release pipelines"
```

### Task 12: User documentation and v0.1 acceptance validation

**Files:**
- Create: `README.md`
- Create: `docs/colab-smoke-v0.1.md`

**Interfaces:**
- Consumes: final CLI/notebook behavior.
- Produces: user workflow and evidence checklist for the environment-level acceptance gate.

- [ ] **Step 1: Document the exact happy path**

`README.md` must cover: open `notebook/colabnomad.ipynb` in CPU Colab; set `TARGET_REPO`; add optional `GITHUB_TOKEN`/`OPENCODE_API_KEY` as Colab Secrets; Run All; copy OpenCode/terminal URLs and one-time credentials; use `colabnomad status`, `doctor`, `logs`, `restart`, and `down` from the web terminal.

- [ ] **Step 2: Document recovery and boundaries**

State explicitly that Colab may terminate the VM at any time, `/content` is ephemeral, dirty work must be committed/pushed by the user, v0.1 does not fake activity or bypass idle limits, and automatic Git mutation is intentionally absent.

- [ ] **Step 3: Add fresh-Colab smoke checklist**

`docs/colab-smoke-v0.1.md` requires evidence for all of:

```text
[ ] fresh CPU runtime; no prior installs
[ ] notebook bootstraps from selected ColabNomad ref
[ ] target repository cloned and git remote contains no credential
[ ] OpenCode authenticated /api/project readiness succeeds for target workspace
[ ] public OpenCode /api/event delivers SSE through Serveo
[ ] ttyd opens through public tunnel and attaches to persistent tmux
[ ] closing/reopening browser preserves tmux shell
[ ] restart opencode does not recreate terminal/tmux
[ ] restart terminal does not destroy tmux session
[ ] doctor reports all required components healthy
[ ] dirty target file survives service restart
[ ] down stops managed processes and removes control socket
```

- [ ] **Step 4: Run full deterministic verification**

Run:

```bash
go test ./...
go test -race ./...
go vet ./...
python3 -m unittest colab.test_bootstrap notebook.test_notebook -v
go build -trimpath -o /tmp/colabnomad ./cmd/colabnomad
/tmp/colabnomad --help
git diff --check
```

Expected: all commands exit 0. This does not substitute for the fresh-Colab checklist.

- [ ] **Step 5: Perform the environment-level Colab smoke test**

Run the committed notebook in a fresh CPU Colab and record concrete output/observations against `docs/colab-smoke-v0.1.md`. If a fresh Colab runtime is unavailable, v0.1 is not considered fully verified; report the run as blocked rather than claiming completion.

- [ ] **Step 6: Commit documentation**

```bash
git add README.md docs/colab-smoke-v0.1.md
git commit -m "docs: document ColabNomad v0.1 workflow"
```

## Final Integration Gate

After all 12 task commits, re-read the spec and run the full deterministic verification again. Then review the branch for: accidental secret persistence, any service binding to non-loopback interfaces, unpinned network downloads, shell orchestration, destructive Git commands, provider-capability bypasses, and notebook/runtime logic duplication.

Do not create the `v0.1.0` tag or publish a GitHub release until the fresh-Colab smoke checklist passes and the user explicitly authorizes the external write.

## Expected Commit Sequence

1. `chore: establish runtime configuration and version pins`
2. `feat: add verified artifact and execution primitives`
3. `feat: add durable runtime state and workspace preparation`
4. `feat: add supervised process lifecycle`
5. `feat: add tmux-backed web terminal service`
6. `feat: add OpenCode v2 service adapter`
7. `feat: add capability-aware tunnel providers`
8. `feat: add daemon control plane and CLI lifecycle`
9. `feat: add Colab bootstrap adapter`
10. `feat: add minimal Colab notebook`
11. `ci: add verification and release pipelines`
12. `docs: document ColabNomad v0.1 workflow`

Each task must leave the working tree clean and its listed tests green before the next task starts.
