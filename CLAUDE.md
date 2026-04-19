# Sundial

Agent-first CLI scheduler with cron, solar, poll, and at triggers. Go project targeting macOS.

## Build & Run

```bash
make build                    # build binary
make install                  # build and install to PATH
make start                    # build, install, and start daemon (uses config.yaml in repo root)
make start launchd=1          # same + register with launchd for auto-start on login
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

This builds the binary, installs it, starts the daemon using `config.yaml` in this repo, and runs a health check. The only required config field is `data_repo` (already set in `config.yaml`).

CLI commands (`sundial add`, `sundial list`, etc.) connect to the daemon over a well-known socket path and do not need a config file.

## Architecture

Single binary, dual mode: CLI client + daemon (via `sundial daemon`).
Daemon managed by macOS launchd. CLI ↔ daemon IPC over Unix domain socket (JSON-RPC).

### Package Map

```
main.go              → cmd.Execute()
cmd/                 → cobra commands (thin wiring layer)
internal/
  model/             → all shared types, interfaces, errors (zero deps — everything imports this)
  trigger/           → CronTrigger + SolarTrigger + PollTrigger + AtTrigger implementing model.Trigger
  config/            → config.yaml loading, validation, path expansion
  store/             → file I/O: desired state (data repo), runtime state, run logs (local)
  gitops/            → git precondition checks, commit --only, push
  geocode/           → Nominatim geocoding + timezone from coordinates
  ipc/               → JSON-RPC protocol, Unix socket client + server
  daemon/            → daemon lifecycle, scheduler run loop, reconciliation, execution, RPC handlers
  similarity/        → fuzzy string matching (Levenshtein, substring) for duplicate detection
  launchd/           → plist generation, launchd install/uninstall
  format/            → output formatting (plain text + JSON)
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

## Design Doc

Full engineering design: `docs/engineering-design.md`
