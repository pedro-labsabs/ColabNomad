# Runtime lifecycle repair report

## Result

Implemented the three repairs from `runtime-lifecycle-brief.md` using strict
RED–GREEN TDD:

- Added `Manager.StartAllWithLifetime` so startup/readiness cancellation does
  not cancel daemon-lifetime restart monitoring; `StartAll` remains compatible.
- Added `UpRequest.VersionsPath`, included the client manifest path in daemon
  payloads, and retained the validated request manifest on `Runtime`.
- Delayed durable runtime assignments until state persistence succeeds and
  stopped all started services on persistence failure, preserving the original
  error and joining cleanup errors when present.

## Verification

All required gates passed:

- `gofmt`
- focused supervisor/app/cmd tests with `-race`
- `go test -count=1 -race ./...`
- `go vet ./...`
- `python3 -m pytest notebook/test_notebook.py colab/test_bootstrap.py` (17 passed)
- `git diff --check`

The focused RED tests failed before the production changes for the missing
lifetime API, request manifest field, and rollback behavior; they pass after
the implementation.
