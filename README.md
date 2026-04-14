# Sundial

A lightweight, agent-first CLI scheduler with cron and solar triggers for macOS.

## What it does

Sundial lets you schedule recurring shell commands using standard cron expressions or dynamic solar events (sunrise/sunset with offsets). It is designed for AI coding agents that need to schedule future invocations of themselves -- for example, "check my trash bins every Monday and Tuesday, one hour before sunset" -- but works equally well for any human who wants solar-aware scheduling without gluing together cron and heliocron.

## Quick start

**Prerequisites:** Go 1.21+, macOS, a git repository for schedule data.

```bash
# Build and install to PATH
go build -o sundial .
sudo mv sundial /usr/local/bin/

# Create config (data_repo is the only required field)
cp config.yaml.example config.yaml
# Edit config.yaml: set data_repo to the path of your git data repo

# Install the daemon as a launchd service
sundial install

# Verify the daemon is running
sundial health

# Create your first schedule
sundial add --type cron --cron "0 9 * * 1-5" \
  --command "cd ~/project && codex exec 'daily standup'" \
  --name "weekday standup"
```

## Commands

| Command     | Description                                      |
|-------------|--------------------------------------------------|
| `add`       | Create a new cron or solar schedule              |
| `list`      | List all active schedules                        |
| `show <id>` | Show details of a specific schedule              |
| `remove <id>` | Remove a schedule (or `--all --yes` for all)  |
| `health`    | Check daemon, config, and data repo health       |
| `geocode <address>` | Look up lat/lon/timezone for an address  |
| `reload`    | Reload daemon config and reconcile schedules     |
| `install`   | Install the daemon as a launchd service          |
| `uninstall` | Remove the daemon from launchd                   |
| `daemon`    | Run the daemon process (called by launchd)       |

All commands support `--json` for machine-parseable output. Run `sundial <command> --help` for full flag details.

## Schedule types

### Cron

Standard five-field cron expressions for static, time-based schedules.

```bash
# Every weekday at 9am
sundial add --type cron --cron "0 9 * * 1-5" \
  --command "echo good morning"
```

### Solar

Trigger relative to sunrise or sunset, on specific days of the week. Requires location coordinates and timezone.

```bash
# 1 hour before sunset on Mondays and Tuesdays
sundial add --type solar --event sunset --offset "-1h" --days mon,tue \
  --lat 37.7749 --lon -122.4194 --timezone "America/Los_Angeles" \
  --command "cd ~/project && codex exec 'check trash bins'"
```

Offset accepts human-friendly formats (`-1h`, `+30m`, `-1h30m`) or ISO 8601 durations (`-PT1H`, `PT30M`). Negative means before the event, positive means after.

Use `sundial geocode` to resolve a street address or city name into the `--lat`, `--lon`, and `--timezone` values:

```bash
sundial geocode "San Francisco, CA"
```

## Configuration

Place `config.yaml` in the project root alongside the sundial binary (or set `SUNDIAL_CONFIG` / pass `--config`). The only required field is `data_repo` -- everything else has sensible defaults.

```yaml
data_repo: "~/data_repo"   # REQUIRED -- path to git repo for schedule definitions
```

All other fields are optional and default to:

```yaml
daemon:
  socket_path: "~/Library/Application Support/sundial/sundial.sock"
  log_level: info                      # debug | info | warn | error
  log_file: "~/Library/Logs/sundial/sundial.log"

state:
  path: "~/.config/sundial/state/"     # runtime state (local, not portable)
  logs_path: "~/.config/sundial/logs/" # run logs (local only)
```

## Architecture

Sundial is a **single Go binary** that operates in two modes:

- **CLI client** -- all subcommands except `daemon`. Connects to the running daemon over a Unix domain socket and sends JSON-RPC requests.
- **Daemon** -- long-running scheduler process started by `sundial daemon`. Managed by macOS launchd so it starts on login and survives sleep/wake cycles.

**Two-store state model:** Schedule definitions (desired state) live in the data repo and are git-tracked for portability. Runtime state (next fire times, execution history) lives locally and is recomputed from desired state on daemon startup.

**Single-writer architecture:** The daemon owns all schedule state mutation. The CLI never writes schedule files directly -- it sends RPCs to the daemon, which handles file I/O and git operations.

See [docs/engineering-design.md](docs/engineering-design.md) for the full design document.

## How it works with agents

Sundial is designed so an AI coding agent can discover and use it without human intervention. The typical agent workflow:

1. **Health check** -- `sundial health` to confirm the daemon is running
2. **Geocode** -- `sundial geocode "City, State"` to resolve location (for solar schedules)
3. **Dry run** -- `sundial add --dry-run ...` to validate and preview without creating
4. **Create** -- `sundial add ...` to create the schedule
5. **Verify** -- `sundial show <id>` or `sundial list` to confirm

Key agent-friendly features:

- `--json` flag on all commands for structured machine parsing
- `--dry-run` on `add` to validate without side effects
- `--help` with inline examples on every command
- Non-interactive -- no prompts, fail-fast with actionable error messages
- Consistent exit codes (0 = success, 1 = error)

Design follows the [CLI-for-Agents](https://github.com/cursor/plugins/blob/main/cli-for-agent/skills/cli-for-agents/SKILL.md) principles.

## Development

```bash
go build -o sundial .    # build
go test ./...            # run all tests
go vet ./...             # static analysis
```

### Project structure

```
cmd/                 -- cobra commands (CLI wiring)
internal/
  model/             -- shared types, interfaces, errors
  trigger/           -- CronTrigger + SolarTrigger implementations
  config/            -- config.yaml loading and validation
  store/             -- file I/O for desired state and runtime state
  gitops/            -- git commit and push operations
  geocode/           -- Nominatim geocoding + timezone lookup
  ipc/               -- JSON-RPC protocol, Unix socket client/server
  daemon/            -- scheduler run loop, reconciliation, execution
  launchd/           -- plist generation, install/uninstall
  format/            -- output formatting (plain text + JSON)
```

See [CLAUDE.md](CLAUDE.md) for the full package map and agent-facing development guide.

### Running the daemon locally

```bash
# Start the daemon in the foreground for testing
sundial daemon --config config.yaml

# In another terminal, interact via the CLI
sundial health
sundial add --type cron --cron "* * * * *" --command "echo tick" --name "test"
sundial list
```

## Status

**v1** -- macOS only, Codex-focused. Schedules arbitrary shell commands with cron and solar triggers.

See [docs/post-v1.md](docs/post-v1.md) for the roadmap, including multi-agent support, Linux compatibility, pause/unpause, and session resume.
