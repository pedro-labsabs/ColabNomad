# Daemon owner identity repair report

Implemented the scoped `internal/control` repair:

- `NewServer` writes an atomically replaced, mode `0600` owner identity containing PID and Linux `/proc` start time.
- `EnsureDaemon` verifies owner identity under the election flock before handling an undialable socket, refuses replacement when the owner is alive, and fails closed when a Unix socket has no verifiable owner.
- Stale or mismatched identities may be removed for replacement; no identity PID is signaled.
- `Server.Close` removes only the owner identity still matching that server.
- Added the four required RED/GREEN regression tests.

Gates passed:

- `gofmt -w internal/control`
- `go test -count=1 -race ./internal/control -v`
- `go test -count=1 -race ./...`
- `go vet ./...`
- `git diff --check`
