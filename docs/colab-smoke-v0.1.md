# Colab smoke checklist: v0.1

This checklist is for a real, fresh CPU Colab runtime. It is intentionally
unchecked until concrete external evidence is recorded. The deterministic
repository gate does not substitute for this environment-level test.

## Pre-release candidate procedure

Run the first fresh-Colab acceptance test against an exact pushed candidate
commit, before creating the `v0.1.0` tag or GitHub release:

1. push the reviewed candidate commit to the ColabNomad repository without
   creating a tag or release;
2. set `COLABNOMAD_REF` in the notebook to that exact commit SHA;
3. leave `COLABNOMAD_RELEASE = 'v0.1.0'`;
4. run the notebook normally.

Because `v0.1.0` does not exist yet, the release download returns 404 and the
bootstrap's tested fallback builds the already checked-out candidate source
using the pinned Go toolchain. This proves the exact candidate without
prematurely publishing the release. Only after every checklist row passes may
the same reviewed commit be tagged as `v0.1.0`, subject to explicit approval.

## Run context

- Date/time (UTC): ____________________
- Colab runtime type: CPU / ____________________
- ColabNomad ref selected in the notebook: ____________________
- Target repository/ref: ____________________
- Evidence location (redacted logs, screenshots, or notes): ____________________

## Required evidence

For each item, record a concise observation or artifact reference in the notes
column without recording tokens, passwords, authorization headers, or
credential-bearing URLs.

| Done | Check | Evidence / observation |
| --- | --- | --- |
| [ ] | fresh CPU runtime; no prior installs | |
| [ ] | notebook bootstraps from selected ColabNomad ref | |
| [ ] | target repository cloned and git remote contains no credential | |
| [ ] | OpenCode authenticated `/api/project` readiness succeeds for target workspace | |
| [ ] | public OpenCode login succeeds through the browser-auth gateway and `/api/event` delivers SSE through localhost.run | |
| [ ] | ttyd opens through public tunnel and attaches to persistent tmux | |
| [ ] | ttyd loads through Cloudflare Quick Tunnel after Basic Auth and upgrades `/ws` to WebSocket | |
| [ ] | closing/reopening browser preserves tmux shell | |
| [ ] | `restart opencode` does not recreate terminal/tmux | |
| [ ] | `restart terminal` does not destroy tmux session | |
| [ ] | `doctor` reports all required components healthy | |
| [ ] | dirty target file survives service restart | |
| [ ] | `down` stops managed processes and removes control socket | |

## Suggested evidence commands

Run these from the web terminal after copying the printed URLs and one-time
credentials. The first command is only a local inspection; do not expose the
remote URL or credentials in its output.

```bash
export PATH="/content/.colabnomad/bin:$PATH"
colabnomad status
colabnomad doctor
colabnomad logs opencode
colabnomad logs terminal
colabnomad restart opencode
colabnomad restart terminal
colabnomad status
colabnomad down
test ! -S /content/.colabnomad/control.sock
```

Use the browser to verify the OpenCode `/api/project` readiness, OpenCode SSE,
ttyd attachment, and browser-close/reopen behavior. To verify dirty-worktree
preservation, make a harmless uncommitted change in the target repository,
restart services, and inspect it again. Do not run `git reset --hard` or
`git clean` as recovery.

## Result

- Fresh-Colab smoke result: **BLOCKED / NOT RUN** (no fresh external Colab
  runtime was available for this documentation task).
- This document must remain unchecked until every row has concrete evidence.
