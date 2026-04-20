# Sundial

A lightweight, agent-first CLI scheduler with cron, solar, poll, and at triggers for macOS.

## What it does

Sundial lets you schedule shell commands using standard cron expressions, dynamic solar events (sunrise/sunset with offsets), condition-gated poll triggers, or one-off absolute timestamps. It is designed for AI coding agents that need to schedule future invocations of themselves -- for example, "check my trash bins every Monday and Tuesday, one hour before sunset", "notify me when a reply arrives", or "wake me up tomorrow morning at 10am for a client call" -- but works equally well for any human who wants solar-aware or event-driven scheduling.

## Quick start

**Prerequisites:** Go 1.21+, macOS, a git repository for schedule data.

```bash
# Copy the dev-local template and point it at your data repo:
cp sundial.config.dev.yaml.example sundial.config.dev.yaml
vim sundial.config.dev.yaml   # set data_repo_path

# Build, install, scaffold the data repo, and start the daemon
make start

# Create your first schedule
sundial add cron --cron "0 9 * * 1-5" \
  --command "cd ~/project && codex exec 'daily standup'" \
  --name "weekday standup"
```

`make start` builds the binary, installs it to PATH, runs `sundial setup` against the data repo (writes `.agents/workspace.yaml`, scaffolds `<data_repo>/sundial/config.yaml`, and syncs skills), starts the daemon, and runs a health check. To also register the daemon with launchd (auto-start on login):

```bash
make start launchd=1
```

Other targets: `make stop`, `make restart`.

## Commands

| Command     | Description                                      |
|-------------|--------------------------------------------------|
| `add cron\|solar\|poll\|at` | Create a new schedule of the given trigger type |
| `list`      | List all active schedules                        |
| `show <id>` | Show details of a specific schedule              |
| `remove <id>` | Remove a schedule (or `--all --yes` for all)  |
| `pause <id>` | Pause a schedule (stops firing, stays visible)   |
| `unpause <id>` | Resume a paused schedule                       |
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
sundial add cron --cron "0 9 * * 1-5" \
  --command "echo good morning"
```

### Solar

Trigger relative to sunrise or sunset, on specific days of the week. Requires location coordinates; timezone defaults to the machine's local zone.

```bash
# 1 hour before sunset on Mondays and Tuesdays
sundial add solar --event sunset --offset "-1h" --days mon,tue \
  --lat 37.7749 --lon -122.4194 --timezone "America/Los_Angeles" \
  --command "cd ~/project && codex exec 'check trash bins'"
```

Offset accepts human-friendly formats (`-1h`, `+30m`, `-1h30m`) or ISO 8601 durations (`-PT1H`, `PT30M`). Negative means before the event, positive means after.

Use `sundial geocode` to resolve a street address or city name into the `--lat`, `--lon`, and `--timezone` values:

```bash
sundial geocode "San Francisco, CA"
```

### Poll

Condition-gated periodic execution. The daemon runs a trigger command at a fixed interval; the main command fires only when the trigger exits 0. Useful for event-driven workflows where you're waiting for an external condition.

```bash
# Check every 2 minutes if a reply arrived, fire once when it does
sundial add poll \
  --trigger 'outreach reply-check --contact-id c_abc123 --channel sms' \
  --interval 2m --timeout 72h --once \
  --command "cd ~/project && codex exec 'Reply from Dr. Smith. Continue campaign.'" \
  --name "await reply from Dr. Smith"
```

The trigger command receives `SUNDIAL_SCHEDULE_ID` and `SUNDIAL_LAST_FIRED_AT` (ISO 8601 watermark) as environment variables so it can scope its check without sundial knowing the domain.

`--once` means "fire the command once, then mark the schedule as completed." Without it, the poll trigger keeps running indefinitely -- check, fire, check, fire. Completed schedules auto-reactivate if `sundial add` is called again with the same command.

To refresh an active poll watcher's timeout without removing it, use `--refresh`:

```bash
sundial add poll \
  --trigger 'outreach reply-check --contact-id c_abc123 --channel sms' \
  --interval 2m --timeout 72h --once \
  --command "cd ~/project && codex exec 'Continue campaign.'" \
  --name "await reply from Dr. Smith" --refresh
```

`--refresh` updates the existing schedule in place (same ID, fresh timeout countdown) if a schedule with the same `--name` already exists. If no match exists, it creates a new one.

### At

One-off scheduling at an absolute timestamp. The schedule fires exactly once at the given time, then auto-completes. Useful for "wake me at 10am tomorrow" or for agents scheduling a specific future session (e.g. rejoin a meeting at its start time).

```bash
# Naive local timestamp (defaults to machine's local timezone)
sundial add at --at "2026-04-20T10:00:00" \
  --command "say 'client call in five minutes'" \
  --name "client-call-reminder"

# Pin to a specific timezone
sundial add at --at "2026-04-20T10:00:00" --timezone "America/Los_Angeles" \
  --command "codex exec 'join the standup'"

# Zoned RFC3339 (--timezone is ignored; the offset wins)
sundial add at --at "2026-04-20T17:00:00Z" --command "echo hi"
```

Past timestamps are rejected at creation. If the daemon is offline when the fire time passes, the schedule is marked completed with reason `missed` on next startup (fires within a 60s grace window; misses beyond it). There is no `--once` flag — `at` is implicitly one-shot.

### Fire-and-forget with `--detach`

By default sundial waits for the command to exit so it can record the exit code. For long-running commands -- especially callbacks that re-enter sundial (e.g. a poll callback that calls `sundial add --refresh` to re-arm itself) -- the wait becomes a problem: the per-schedule mutex is held for the full command duration, and any nested refresh is rejected as "schedule currently firing."

```bash
sundial add poll \
  --trigger 'check-reply' --interval 2m --timeout 72h --once \
  --command 'long-running-callback ...' \
  --name 'outreach-watch' --detach
```

With `--detach`, sundial spawns the command via `exec.Start()` with its own session (so it survives daemon restarts) and returns immediately. Trade-offs: no `exit_code` or `duration_s` is captured, `sundial show` renders `last_fire: … (detached)`, and the child process is unsupervised once spawned. Use it only when the command logs its own outcome elsewhere or you don't need sundial-side visibility into the result.

## Configuration

Sundial stores schedules in a shared **data repo** (a git repo shared with any other agent tooling in the stack, e.g. outreach). The CLI resolves the data repo in this order:

1. `SUNDIAL_DATA_REPO` environment variable (explicit override)
2. `sundial.config.dev.yaml` next to the running binary (dev-local pointer, gitignored)
3. Walk up from cwd looking for `.agents/workspace.yaml`

Run `sundial setup --data-repo <path>` once per data repo to scaffold it: the command writes `.agents/workspace.yaml` (stamping `tools.sundial.version`), creates `<data_repo>/sundial/config.yaml` from a template, and syncs skills. It is idempotent.

Daemon options live in `<data_repo>/sundial/config.yaml`; all fields are optional and default to:

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
- Fuzzy duplicate detection catches near-duplicate names (Levenshtein) and commands (substring) with `--force` override
- `--refresh` for atomic in-place updates to active schedules (upsert by name)
- `--detach` for fire-and-forget commands that shouldn't block the firing window (required for callbacks that re-enter sundial)

Design follows the [CLI-for-Agents](https://github.com/cursor/plugins/blob/main/cli-for-agent/skills/cli-for-agents/SKILL.md) principles.

## Development

```bash
make build               # build binary
make install             # build and install to PATH
make start               # build, install, start daemon
make start launchd=1     # same + register with launchd
make stop                # stop the daemon
make restart             # stop + start
make test                # run all tests
make vet                 # static analysis
make clean               # remove local binary
```

### Project structure

```
cmd/                 -- cobra commands (CLI wiring)
internal/
  model/             -- shared types, interfaces, errors
  trigger/           -- CronTrigger + SolarTrigger + PollTrigger + AtTrigger implementations
  config/            -- data-repo resolution, config loading, workspace.yaml
  scaffold/          -- embedded skills + config template for `sundial setup`
  version/           -- single source of truth for the version string
  store/             -- file I/O for desired state and runtime state
  gitops/            -- git commit and push operations
  geocode/           -- Nominatim geocoding + timezone lookup
  ipc/               -- JSON-RPC protocol, Unix socket client/server
  daemon/            -- scheduler run loop, reconciliation, execution
  similarity/        -- fuzzy string matching for duplicate detection
  launchd/           -- plist generation, install/uninstall
  format/            -- output formatting (plain text + JSON)
```

See [CLAUDE.md](CLAUDE.md) for the full package map and agent-facing development guide.

## Status

**v1** -- macOS only, Codex-focused. Schedules arbitrary shell commands with cron, solar, poll, and at triggers.

See [docs/post-v1.md](docs/post-v1.md) for the roadmap, including multi-agent support, Linux compatibility, and session resume.
