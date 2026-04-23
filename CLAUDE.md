# Sundial

Agent-first CLI scheduler with cron, solar, poll, and at triggers. Go project targeting macOS.

## Build & Run

```bash
make build                    # build binary
make install                  # build and install to PATH
make start                    # build, install, scaffold the data repo, start the daemon,
                              # and register with launchd for auto-start on login
                              # (data repo comes from sundial.config.dev.yaml in this repo)
make stop                     # stop the daemon
make restart                  # stop + start
make test                     # run all tests
make vet                      # static analysis
make clean                    # remove local binary
```

### Setup

Sundial requires a running daemon. If `sundial health` shows the daemon is not running:

```bash
# From this repo's root:
make start
```

This builds the binary, installs it, resolves the data repo from `sundial.config.dev.yaml` (copy `sundial.config.dev.yaml.example` if you haven't already), scaffolds the data repo via `sundial setup` (workspace marker, `<data_repo>/sundial/config.yaml`, skills sync), starts the daemon, registers it with launchd, and runs a health check. The installed plist wraps the daemon with `caffeinate -i` so it holds a `PreventUserIdleSystemSleep` assertion for its lifetime — otherwise launchd suspends the job with the system and fires are missed. Explicit user-initiated sleep still works.

The data repo is resolved in this order: `SUNDIAL_DATA_REPO` env var → `sundial.config.dev.yaml` next to the binary → walk up from cwd for `.agents/workspace.yaml`.

CLI commands (`sundial add`, `sundial list`, etc.) connect to the daemon over a well-known socket path and do not need a config file.

## Architecture

Single binary, dual mode: CLI client + daemon (via `sundial daemon`).
Daemon managed by macOS launchd. CLI ↔ daemon IPC over Unix domain socket (JSON-RPC).

### Package Map

```
main.go              → cmd.Execute()
cmd/                 → cobra commands (thin wiring layer)
skills/              → top-level embedded SKILL.md tree (sundial/SKILL.md, scheduling.md, integrating.md). embed.go exposes skills.FS.
internal/
  model/             → all shared types, interfaces, errors (zero deps — everything imports this)
  trigger/           → CronTrigger + SolarTrigger + PollTrigger + AtTrigger implementing model.Trigger
  config/            → data-repo resolution (env / dev yaml / workspace walk-up), config loading, workspace.yaml stamping
  scaffold/          → config template + CopySkills orchestration used by `sundial setup` (reads skills.FS)
  version/           → single source of truth for the sundial version
  store/             → file I/O: desired state (data repo), runtime state, run logs (local)
  gitops/            → git precondition checks, commit --only, push
  geocode/           → Nominatim geocoding + timezone from coordinates
  ipc/               → JSON-RPC protocol, Unix socket client + server
  daemon/            → daemon lifecycle, scheduler run loop, reconciliation, execution, RPC handlers
  similarity/        → fuzzy string matching (Levenshtein, substring) for duplicate detection
  launchd/           → plist generation, launchd install/uninstall
  format/            → output formatting (plain text + JSON)
  integration/       → black-box integration tests that spin up a real daemon
```

### Key Design Decisions

- **model/ is the contract layer**: all shared types live here. Downstream packages import model, never each other's types.
- **Single-writer architecture**: daemon owns all schedule state mutation. CLI sends RPCs only.
- **Two-store state model**: desired state in data repo (git-tracked), runtime state local.
- **Four trigger types**: cron (static schedule), solar (sun-position-based), poll (condition-gated periodic check), at (one-off at an absolute timestamp; auto-completes after firing).
- **Schedule lifecycle**: active → paused (via `pause`) → active (via `unpause`); active → completed (via `--once`) or removed. Completed schedules auto-reactivate on matching `add`. Active schedules can be updated in place via `--refresh`.
- **Fuzzy duplicate detection**: `sundial add` checks exact name/command matches first, then fuzzy (Levenshtein for names, substring for commands). `--force` bypasses both.
- **Execution modes**: default waits for the command to exit (captures `exit_code` + `duration_s`); `--detach` spawns via `Start()` in a new session and returns immediately (per-schedule mutex released in milliseconds, no exit code captured). Use `--detach` for callbacks that re-enter sundial (nested `add --refresh`) or for long-running commands that log outcomes elsewhere.
- **Agent-first CLI**: non-interactive, --json flag, fail-fast with examples, --dry-run.

## Extending

- **New trigger type**: add a type implementing `model.Trigger` under `internal/trigger/`, add its `add` subcommand in `cmd/add_*.go`, wire it into reconciliation in `internal/daemon/`. Triggers stay pure (they compute next-fire times); side effects belong in the daemon.
- **New RPC**: extend the protocol in `internal/ipc/`, add a handler in `internal/daemon/`, add a thin CLI command in `cmd/`. The daemon remains the single writer — CLI never touches schedule files directly.
- **Skill/scaffold changes**: edit the embedded tree under `skills/sundial/` at the repo root (SKILL.md is the catalog; `scheduling.md` and `integrating.md` are child docs). The `skills` package exposes `skills.FS` via `go:embed`, and `internal/scaffold` walks it in `CopySkills`. `sundial setup` re-syncs the whole tree idempotently, so adding a new child doc is just adding the file — no code change needed.

## Doc Map

Two audiences. Do not mix them:

1. **Contributors improving sundial itself** — you. Start here (`CLAUDE.md`), then `docs/engineering-design.md` for the full design, `docs/post-v1.md` for the roadmap, `README.md` for the public-facing overview.
2. **Consumers of sundial** (agents scheduling events, engineers building tools on top) — the skill tree at `skills/sundial/`. `SKILL.md` is a catalog that signposts to `scheduling.md` (agent users) and `integrating.md` (agent-adjacent tool builders). Changes to what consumers see happen there, not in CLAUDE.md or README.md.
