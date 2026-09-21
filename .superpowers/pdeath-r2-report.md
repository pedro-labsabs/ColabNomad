# PDEATHSIG dedicated creator-thread repair — GREEN report

Implemented the minimal Linux production repair in `internal/execx/process_linux.go`:

- managed `cmd.Start` calls are serialized through one process-wide launcher;
- the launcher locks its OS thread once and remains alive for the process lifetime;
- each launch error is returned to its caller;
- existing process-group, PDEATHSIG, log lifecycle, wait goroutine, and handle behavior are unchanged.

## Verification

- `go test ./internal/execx -run TestManagedChildSurvivesCreatorThreadExit -count=5 -v` — PASS (5/5)
- `go test -count=1 -race ./internal/execx -v` — PASS
- `go test -count=1 -race ./...` — PASS
- `go vet ./...` — PASS
- `git diff --check` — PASS

No push, merge, tag, or release performed.
