# Sundial

Agent-first CLI scheduler with cron, solar, and poll triggers. Go project targeting macOS.

## Build & Run

```bash
make build                    # build binary
make install                  # build and install to /usr/local/bin
make test                     # run all tests
make vet                      # static analysis
make clean                    # remove local binary
```

## Architecture

Single binary, dual mode: CLI client + daemon (via `sundial daemon`).
Daemon managed by macOS launchd. CLI ↔ daemon IPC over Unix domain socket (JSON-RPC).

### Package Map

```
main.go              → cmd.Execute()
cmd/                 → cobra commands (thin wiring layer)
internal/
  model/             → all shared types, interfaces, errors (zero deps — everything imports this)
  trigger/           → CronTrigger + SolarTrigger + PollTrigger implementing model.Trigger
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
- **Three trigger types**: cron (static schedule), solar (sun-position-based), poll (condition-gated periodic check).
- **Schedule lifecycle**: active → paused (via `pause`) → active (via `unpause`); active → completed (via `--once`) or removed. Completed schedules auto-reactivate on matching `add`.
- **Fuzzy duplicate detection**: `sundial add` checks exact name/command matches first, then fuzzy (Levenshtein for names, substring for commands). `--force` bypasses both.
- **Agent-first CLI**: non-interactive, --json flag, fail-fast with examples, --dry-run.

## Design Doc

Full engineering design: `docs/engineering-design.md`
