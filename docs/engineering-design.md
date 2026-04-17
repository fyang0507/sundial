# Sundial: Engineering Design Document

**Status:** Draft v1.9 (post-design-review round 9)
**Date:** 2026-04-13
**Author:** Fred Yang + Claude Code

---

## 1. Problem Statement

AI coding agents (Codex, Claude Code, Hermes, etc.) can operate autonomously in headless mode, but they have no native ability to schedule future invocations of themselves. A user who says *"check my trash bins every Monday and Tuesday, one hour before sunset"* needs a way to translate that into a persistent, recurring schedule that launches an agent at the right time -- even when that time shifts day-to-day based on solar events.

No existing scheduler solves this well:
- **cron** handles static schedules but cannot express dynamic ones ("1hr before sunset").
- **`heliocron`** computes solar delays but requires a separate cron job per schedule and wastes a sleeping process.
- **Home Assistant** has the right scheduling model but is a full smart-home platform, not an agent-friendly CLI.

Sundial fills this gap: a lightweight, agent-first CLI scheduler that supports both static (cron) and dynamic (solar/computed) schedules on macOS.

### Command-Agnostic Execution

Sundial schedules **arbitrary shell commands**, not agents specifically. A scheduled command can be a deterministic script (`python ~/scripts/cleanup.py`), a shell pipeline (`curl ... | jq ...`), or an agent in headless mode (`codex exec "..."`). Since agents are invoked as shell commands, they require no special treatment at the execution layer. Agent-specific features like **session resume** are deferred to post-v1 (see [Section 9](#9-resume-support--deferred-post-v1)).

## 2. Goals and Non-Goals

### v1 Goals
- Agent-first CLI with `add`, `list`, `remove` commands optimized for machine consumption
- Schedule any shell command (executed via login shell): scripts, pipelines, or agents in headless mode
- Static cron-style scheduling (e.g., "every weekday at 9am")
- Dynamic solar scheduling (e.g., "1 hour before sunset every Monday and Tuesday")
- macOS daemon managed via launchd (LaunchAgent — runs while macOS user is logged in)
- Agent-interpretable structured output (plain text with key: value pairs, `--json` flag)
- Per-schedule location for solar calculations (resolved via `sundial geocode`, passed as flags)
- Two-store state model: data repo as canonical desired state, local runtime state with reconciliation on startup
- Single-writer architecture: daemon owns all schedule state mutation and git operations; CLI sends RPCs via Unix socket (one documented exception: agent appends to `cli-feedback.jsonl`)
- Portable schedule records with `recreation_command` as a convenience/human-readable hint (not canonical reconstruction data)

### Non-Goals (v1)
- Multi-agent support beyond Codex (Claude Code, Hermes — deferred to v2)
- Linux / Windows compatibility (macOS only for v1)
- Complex git merge/conflict resolution (sundial does simple commit + best-effort push for schedule definitions only)
- Operational log persistence in git (run logs, fire history stay local)
- Web UI or GUI
- Distributed / multi-machine scheduling (v1 assumes a single active machine — the user is responsible for ensuring only one machine runs the daemon against a given data repo at a time; portability is for migration, not concurrent execution)
- Sub-minute scheduling precision

## 3. Architecture Overview

```
                  +-----------+
                  |   Agent   |  (Codex, running headless)
                  |           |  translates natural language
                  +-----+-----+  into CLI commands
                        |
                        | shell exec
                        v
                  +-----------+
                  | sundial   |  CLI binary (Go)
                  | (client)  |  sends JSON commands
                  +-----+-----+
                        |
                        | Unix domain socket
                        | ~/Library/Application Support/sundial/sundial.sock
                        v
                  +-----------+
                  | sundial   |  daemon (same binary, `sundial daemon` subcommand)
                  | (daemon)  |  managed by launchd
                  +-----+-----+
                        |
                        | at scheduled time
                        v
                  +-----------+
                  | /bin/zsh  |  spawned via login shell
                  | -l -c "." |  (scripts, pipelines, or agents)
                  +-----------+
```

### Key Design Decisions

**Single binary, dual mode.** The `sundial` binary serves as both CLI client and daemon. `sundial daemon` starts the long-running scheduler; all other subcommands are thin clients that connect to the daemon over a Unix domain socket.

**Daemon managed by launchd.** On `sundial install`, the tool reads `config.yaml` from the repo, validates the data repo, and writes a LaunchAgent plist to `~/Library/LaunchAgents/com.sundial.scheduler.plist`. The daemon runs whenever the macOS user is logged in and survives sleep/wake cycles. `sundial uninstall` removes it.

**Agent-first, human-readable.** The CLI follows [CLI-for-Agents](https://github.com/cursor/plugins/blob/main/cli-for-agent/skills/cli-for-agents/SKILL.md) design principles: non-interactive, discoverable via `--help`, fail-fast with actionable errors, and structured output.

**Single-writer architecture (with one documented exception).** The daemon is the sole authority for all **schedule state** mutation. CLI subcommands send JSON-RPC requests to the daemon, which writes desired-state files, updates local runtime state, and performs git commit+push. This eliminates races between the CLI and daemon on shared files and ensures a single code path for all schedule writes. The one exception is `cli-feedback.jsonl` — see [CLI Feedback](#cli-feedback) for why this is safe.

**Two-store state model.** Schedule definitions (desired state) live in the data repo for portability and audit. Runtime state (fire times, session IDs) lives locally. The daemon reconciles on startup — reading desired state from the data repo and creating missing local runtime entries.

## 4. Technology Choices

### Language: Go

| Factor | Go | Alternatives considered |
|---|---|---|
| **Single binary** | Native, ~10MB, zero runtime deps | Rust: equivalent. Node (bun compile): ~50MB. Python (PyInstaller): ~60-90MB, slow cold start. |
| **macOS daemon** | `kardianos/service` handles launchd plist generation, install/uninstall, signal handling | Rust: manual plist (trivial but no helper library). Node/Python: no ecosystem support. |
| **CLI framework** | `cobra` -- industry standard (docker, gh, kubectl) | Rust `clap`: equally good. Node `commander`: mature. Python `typer`: ergonomic. |
| **IPC** | Unix domain socket via stdlib `net` -- same pattern as Docker | All languages support UDS; Go's stdlib is simplest. |
| **Solar calculations** | `go-sunrise`, `suncalc-go`, or port NREL SPA (~200 LOC) | Python `astral` is best-in-class but Python's distribution story is a dealbreaker. |
| **Security** | Static binary, no eval, no dynamic loading. Commands run via `/bin/zsh -l -c` (login shell, same trust model as cron). | Rust is marginally stronger. Node/Python have larger attack surface. |
| **Dev velocity** | Fast compilation, simple deps, well-known patterns | Rust: 1.5-2x longer. Node/Python: faster to write, harder to ship. |

### Dependencies (expected)
- `spf13/cobra` -- CLI framework
- `kardianos/service` -- launchd daemon management
- `go-sunrise` or equivalent -- solar position calculations
- `robfig/cron` -- cron expression parsing (for static schedules)
- Standard library for everything else (JSON, Unix sockets, time, os/exec)

## 5. Schedule Model

### Desired State vs Runtime State

Every schedule is split across two stores:

1. **Desired state (data repo — canonical).** The complete schedule definition: trigger, command, location, metadata. Written by the daemon when processing an `add` RPC from the CLI. Portable across machines. This is the source of truth for *what should be scheduled*.
2. **Runtime state (local — machine-specific).** Derived scheduling data managed by the daemon: next fire time, execution history, session IDs. Lives in `~/.config/sundial/state/`. Recomputable from desired state.

The daemon reconciles these on startup: if a desired-state record exists in the data repo without a corresponding local runtime entry, the daemon creates one. See [Section 10](#10-data-repo-integration) for the full reconciliation model.

**Desired state** (written to `<data_repo>/sundial/schedules/sch_a1b2c3.json`):
```json
{
  "id": "sch_a1b2c3",
  "name": "Trash bin check",
  "created_at": "2026-04-13T22:00:00Z",
  "user_request": "Check trash bins every Mon/Tue, 1 hour before sunset",
  "trigger": {
    "type": "solar",
    "event": "sunset",
    "offset": "-PT1H",
    "days": ["monday", "tuesday"],
    "location": {
      "lat": 37.7749,
      "lon": -122.4194,
      "timezone": "America/Los_Angeles"
    }
  },
  "command": "cd ~/projects/trash-bin-detection && codex exec 'Check trash bins on sidewalk and report'",
  "status": "active",
  "recreation_command": "sundial add solar --event sunset --offset \"-1h\" --days mon,tue --lat 37.7749 --lon -122.4194 --timezone \"America/Los_Angeles\" --command \"cd ~/projects/trash-bin-detection && codex exec 'Check trash bins on sidewalk and report'\" --name \"Trash bin check\" --user-request \"Check trash bins every Mon/Tue, 1 hour before sunset\""
}
```

**Runtime state** (daemon-managed at `~/.config/sundial/state/sch_a1b2c3.json`):
```json
{
  "id": "sch_a1b2c3",
  "next_fire_at": "2026-04-14T18:42:00Z",
  "last_fired_at": "2026-04-08T18:38:00Z",
  "last_exit_code": 0,
  "fire_count": 12
}
```

### Trigger Interface

Both static and dynamic triggers implement the same interface:

```go
type Trigger interface {
    // NextFireTime returns the next fire time strictly after `after`.
    // Returns zero time if no future fire time exists.
    NextFireTime(after time.Time) time.Time

    // Validate checks the trigger configuration for errors.
    Validate() error

    // HumanDescription returns a human-readable summary.
    // e.g., "Every Mon, Tue at 1h before sunset (San Francisco)"
    HumanDescription() string
}
```

Implementations:
- **CronTrigger** -- wraps a cron expression. `NextFireTime` delegates to `robfig/cron` parser.
- **SolarTrigger** -- computes solar event time for the next matching day, applies offset. Uses NREL SPA algorithm or `go-sunrise` library.

### Recomputation Strategy: Next-Occurrence-Only

Following the Home Assistant pattern, `next_fire_at` is always computed on demand — never pre-cached across long windows:

1. **On daemon startup or crash recovery:** execute the full [Shutdown / Restart Behavior](#shutdown--restart-behavior) sequence — inspect persisted pre-restart `next_fire_at`, resolve fire-vs-miss, then advance all schedules forward.
2. **After each fire:** recompute `next_fire_at` for that schedule.
3. **On reload:** recompute `next_fire_at` for all schedules (no miss handling — daemon was running).
4. **On config change** (location update, schedule edit): recompute affected schedules.

This avoids stale pre-computed windows and keeps the data model simple. Solar calculations are ~microseconds, so on-demand computation has negligible cost.

### Missed Fire Policy: Grace Window, Then Skip

When the daemon wakes and a scheduled fire time has passed:

- **Within grace window (≤ 60 seconds late):** fire the command once. This covers normal system sleep/wake jitter and minor scheduling delays.
- **Beyond grace window:** skip the fire. Log one miss entry per skipped occurrence and compute the next valid fire time.
- **Backfill cap:** after extended downtime, log at most **10 missed occurrences** per schedule. Beyond that, log a single summary record (`"type": "miss_summary"`, with count and date range) to avoid unbounded backfill for high-frequency cron schedules.

Rationale: scheduled commands are time-sensitive by nature — running a sunset-triggered script minutes or hours late is worse than not running it at all. The 60-second grace window accommodates system sleep/wake without compromising this principle. If a future use case requires catch-up semantics, it should be designed with explicit miss metadata passed to the command, not as a blind re-invocation.

### Shutdown / Restart Behavior

When the Mac is powered off and restarted X days later:

1. **macOS login** triggers launchd, which starts the sundial daemon automatically (via `KeepAlive` and `RunAtLoad` in the plist).
2. **Reconcile desired state.** Read all schedule records from the data repo (`<data_repo>/sundial/schedules/`). Compare with local runtime state. Create local runtime entries for any new schedules; remove local entries for schedules marked `removed` in the data repo. This is how schedules from a previous machine (via data repo migration) get picked up.
3. **Inspect and resolve pre-restart fire times.** For each schedule, inspect the **persisted** `next_fire_at` from before the restart (not yet recomputed). This is the occurrence that may have been missed during downtime. For each schedule:
   - If `next_fire_at` is in the past **within the grace window** (≤ 60 seconds ago): **fire the command once**, then advance.
   - If `next_fire_at` is in the past **beyond the grace window**: **log as missed** per the [missed fire policy](#missed-fire-policy-grace-window-then-skip) (up to 10 per schedule, then a summary record).
   - If `next_fire_at` is in the future: no action needed.
4. **Advance schedules.** After resolving all pre-restart occurrences, recompute `next_fire_at` for every schedule using the trigger definition, advancing until `next_fire_at` is **strictly in the future**. This ordering — inspect old, decide, then advance — prevents both double-fires (recomputing before checking would lose the pre-restart occurrence) and silent drops (firing after recompute would use the wrong timestamp).
5. **Retry pending pushes.** If there are local commits ahead of the remote, attempt `git push`. This ensures schedule definitions committed during a network outage eventually propagate.
6. **Resume normal operation.** The daemon sleeps until the soonest `next_fire_at` across all schedules.

### Time Handling

- All internal timestamps in UTC. No ambiguity from DST transitions.
- Offsets (e.g., "-PT1H") are relative to the solar event, not to clock time. DST does not affect the rule.
- Display times in the schedule's configured timezone for human/agent readability.

## 6. CLI Design

### Command Structure

```
sundial                                 # prints status summary + help
sundial add cron|solar|poll [flags]     # create a new schedule (--dry-run to preview without creating)
sundial list [--json]                   # list all schedules
sundial remove <id>              # remove a schedule
sundial show <id> [--json]       # show details of one schedule
sundial reload                   # re-reconcile desired state from data repo (e.g., after repo sync)
sundial health                   # validate config, daemon status, show readiness
sundial geocode <address>        # resolve address to lat/lon/timezone (agent uses output as flags to `add`)
sundial daemon                   # start daemon (used by launchd, not user)
sundial install                  # validate config, install launchd agent
sundial uninstall                # uninstall launchd agent
```

### Agent-First Design Principles

Following [CLI-for-Agents](https://github.com/cursor/plugins/blob/main/cli-for-agent/skills/cli-for-agents/SKILL.md):

**Non-interactive.** Every input is a flag. No prompts, no menus.
```
# Agent resolves location first
sundial geocode "San Francisco, CA"
# → lat: 37.7749  lon: -122.4194  timezone: America/Los_Angeles

# Then passes everything to add
sundial add solar \
  --event sunset \
  --offset "-1h" \
  --days mon,tue \
  --lat 37.7749 \
  --lon -122.4194 \
  --timezone "America/Los_Angeles" \
  --command "cd ~/projects/trash-bin-detection && codex exec 'check trash bins'" \
  --name "Trash bin check" \
  --user-request "Check trash bins every Mon/Tue, 1 hour before sunset"
```

**Layered discoverability.** `sundial` shows top-level commands; `sundial add --help` shows full flag documentation with examples. No wall of text on every invocation.

**Structured output.** Default output is human-scannable plain text with key: value pairs. `--json` flag on every read command for machine parsing. Write commands (e.g., `add`) include the data repo path in success output for audit and debugging (the agent does **not** need to git sync — sundial handles commit and push automatically). Read commands surface missed fires so the agent can flag issues.
```
# Default (sundial add response)
id: sch_a1b2c3
name: Trash bin check
schedule: Every Mon, Tue at 1h before sunset (San Francisco)
next_fire: 2026-04-14 6:42pm PDT
status: active
saved_to: ~/data_repo/sundial/schedules/sch_a1b2c3.json
committed: sundial: add schedule sch_a1b2c3 "Trash bin check"

# sundial show <id> — surfaces missed fires
id: sch_a1b2c3
name: Trash bin check
schedule: Every Mon, Tue at 1h before sunset (San Francisco)
next_fire: 2026-04-14 6:42pm PDT
last_fire: 2026-04-08 6:38pm PDT (exit 0)
missed: 2 since last fire (daemon offline 2026-04-10 – 2026-04-12)
status: active

# --json
{"id":"sch_a1b2c3","name":"Trash bin check",...,"missed_count":2,"missed_since":"2026-04-08T18:38:00Z"}

# Degraded success (sundial add — committed but local state recovery was needed)
id: sch_a1b2c3
name: Trash bin check
schedule: Every Mon, Tue at 1h before sunset (San Francisco)
next_fire: 2026-04-14 6:42pm PDT
status: active
saved_to: ~/data_repo/sundial/schedules/sch_a1b2c3.json
committed: sundial: add schedule sch_a1b2c3 "Trash bin check"
recovery: local_state_reconciled
warning: local runtime state write failed — reconciliation recovered

# --json (degraded success)
{"id":"sch_a1b2c3","name":"Trash bin check",...,"status":"active","recovery":"local_state_reconciled","warning":"local runtime state write failed — reconciliation recovered"}
```

**Fail-fast with examples.** Missing required flags produce an error with a correct example invocation.
```
Error: --command is required

Example:
  sundial add cron --cron "0 9 * * 1-5" \
    --command "cd ~/projects/standup && codex exec 'daily standup'"
```

**Safe-by-default with deterministic duplicate detection.** `sundial add` is create-only. If the `--name` exactly matches an existing active schedule, or the `--command` exactly matches an existing active schedule, the daemon rejects the add and returns an actionable hint:
```
Error: duplicate schedule exists
  id: sch_a1b2c3
  name: Trash bin check
  match: exact name

To create anyway:    sundial add --force ...
To update existing:  sundial remove sch_a1b2c3 && sundial add ...
```
The agent gets enough context to decide — no silent upsert, no hard wall. `sundial remove` on a nonexistent ID succeeds silently. Fuzzy/similarity matching (Levenshtein, substring) is deferred to post-v1 — a weaker but deterministic rule is better than an undefined "smart" one in a safety-sensitive flow.

**Dry-run for verification.** `sundial add --dry-run` validates all flags and returns the computed `next_fire` time without creating the schedule. Useful for agent verification workflows.
```
# Preview without creating
sundial add solar --dry-run --event sunset --offset "-1h" --days mon,tue \
  --lat 37.7749 --lon -122.4194 --timezone "America/Los_Angeles" \
  --command "cd ~/projects/trash-bin-detection && codex exec 'check trash bins'"

# → schedule: Every Mon, Tue at 1h before sunset (37.7749, -122.4194)
#   next_fire: 2026-04-14 6:42pm PDT
#   (dry run — no schedule created)
```

**Shell execution model.** Commands are executed via `/bin/zsh -l -c "<command>"` (login shell — see [Environment Contract](#environment-contract)). This means shell syntax (pipes, `&&`, `cd`, variable expansion) works as expected, and the user's login profile (`~/.zprofile`) is sourced so tools on PATH are available. Note: `~/.zshrc` is **not** sourced (that requires an interactive shell) — tools must be on PATH via `~/.zprofile`. The command string is fully self-contained — use `cd ~/project && ...` if the command needs a specific working directory.

**Destructive guards.** `sundial remove --all` requires `--yes` flag. Individual removes do not require confirmation (agents retry freely).

## 7. Configuration

### No Config File in the Agent Workflow

The agent never edits a config file. All information required to create a schedule — including location for solar triggers — is resolved by the agent and passed as flags to `sundial add`. This keeps the agent workflow purely CLI-driven:

1. Agent resolves location via `sundial geocode "San Francisco, CA"` → gets lat/lon/timezone.
2. Agent passes `--lat`, `--lon`, `--timezone` to `sundial add`.
3. Schedule is self-contained — location is stored in the schedule's desired-state record.

There is no shared location config that schedules inherit from. Each schedule owns its full trigger definition.

### Daemon Config

A `config.yaml` in the sundial repo root is **mandatory** — the daemon will not start without it. The only required field is `data_repo`; all other fields have sensible defaults. The config is version-controlled alongside the CLI source.

**Bootstrap:** The user sets `data_repo` in `config.yaml` before running `sundial install`. The install command reads the config, validates that `data_repo` exists and is a git repository, then writes the launchd plist (which includes the path to the config) and starts the daemon.

**Daemon behavior when config is missing or invalid:**
- Config file missing → daemon refuses to start, logs actionable error: `config.yaml not found`
- `data_repo` missing from config → daemon refuses to start, logs: `data_repo is required in config.yaml`
- `data_repo` path does not exist or is not a git repo → daemon refuses to start, logs: `data_repo path invalid: <path>`
- `sundial health` surfaces the same errors when the daemon is unreachable.

```yaml
# All fields optional except data_repo — defaults shown
data_repo: "~/data_repo"             # REQUIRED — path to the data repo for schedule definitions

daemon:
  socket_path: "~/Library/Application Support/sundial/sundial.sock"
  log_level: info                 # debug | info | warn | error
  log_file: "~/Library/Logs/sundial/sundial.log"

state:
  path: "~/.config/sundial/state/"   # runtime state: next_fire_at, session_id, etc. (daemon-managed, not portable)
  logs_path: "~/.config/sundial/logs/"  # run logs: fire/miss records with bounded output (local only)
```

The agent does not interact with this file. `data_repo` is set once by the user before install. All other fields have sensible defaults.

Note: the `state.path` and `logs_path` directories hold **local data only** — runtime state and run logs respectively. These are not portable and not git-tracked. The canonical schedule definitions live in the data repo. See [Section 5](#desired-state-vs-runtime-state) for the split.

### Health Check

`sundial health` validates **infrastructure readiness** — it does not validate whether individual scheduled commands will succeed at runtime (those are arbitrary shell strings that may depend on working directory, network, or runtime state).

Checks:
- Daemon reachable via Unix domain socket
- Config file found and `data_repo` is valid
- Data repo path accessible, `sundial/schedules/` exists and is writable
- **Data repo git state is clean enough for writes:** not in detached HEAD, no rebase/merge in progress, no unmerged index entries. If any of these repo-level issues are detected, `health` reports the specific problem. Additionally, `health` lists any schedule files in `sundial/schedules/` that have local modifications (staged or unstaged) — these are informational warnings, not global write-blockers. The write path (`add`/`remove`) checks the same repo-level preconditions and also checks the **specific target file** for local modifications. Both checks must pass or the mutation fails entirely.
- All schedule definitions parseable (no corrupt JSON)
- Local state and log directories writable
- Effective `PATH` in the daemon's login-shell environment (informational — reports directories so the user can verify tools are reachable, but does not attempt to resolve individual commands)
- Any orphaned local schedules (desired state missing from data repo)
- Pending git pushes (local commits ahead of remote)

This is the agent's entry point for verifying the system is operational before calling `sundial add`.

## 8. Daemon Design

### Lifecycle

1. **Install:** `sundial install` reads `config.yaml` from the repo, validates the data repo, writes a launchd plist to `~/Library/LaunchAgents/`, and loads it. The daemon starts immediately and on every macOS login. **Note:** v1 uses a LaunchAgent (per-user), which means the daemon runs whenever the macOS user is logged in. It survives sleep/wake cycles (launchd restarts it), but does not run if the user is logged out of macOS entirely. This is the same scope as most Mac apps — for sundial's use case (agent tasks in a user context), this is the right fit.
2. **Startup:** Execute the full [Shutdown / Restart Behavior](#shutdown--restart-behavior) sequence: reconcile desired state, inspect pre-restart fire times, decide fire-vs-miss for each, then advance all schedules forward.
3. **Run loop:** Sleep until the next fire time. On wake, execute the command, recompute, re-sort, persist runtime state.
4. **IPC:** Listen on Unix domain socket. CLI commands arrive as JSON-RPC messages; daemon responds with JSON.
5. **Reload:** `sundial reload` sends an RPC that re-reads desired state from the data repo, creates/removes local runtime entries, recomputes `next_fire_at` for all schedules, and retries pending pushes. Unlike startup, reload does **not** perform pre-restart miss handling — it assumes the daemon has been running continuously, so there are no missed occurrences to inspect. This is the explicit refresh path after a repo sync or manual edit; no daemon restart needed.
6. **Signals:** SIGTERM (from launchd stop) triggers graceful shutdown — finish any in-progress fire, persist state, exit.
7. **Uninstall:** `sundial uninstall` stops the daemon and removes the plist.

### Schedule Execution

When a schedule fires:

1. Log the fire event (timestamp, schedule ID, command).
2. Spawn via `/bin/zsh -l -c "<command>"` (login shell). The shell sources login startup files (`~/.zprofile`), giving commands access to PATH entries configured there. See [Environment Contract](#environment-contract). Commands handle their own working directory (e.g., `cd ~/project && ...`).
3. Wait for process exit. Capture exit code, duration, and bounded stdout/stderr preview.
4. Recompute `next_fire_at`. Persist updated runtime state locally.
5. Append run log entry to local logs (`~/.config/sundial/logs/<schedule_id>.jsonl`).

### Post-Operation Git Hook

After **schedule definition changes** (`sundial add`, `sundial remove`), the daemon automatically:

0. **Check git preconditions.** Verify: (a) the data repo is not in detached HEAD, has no rebase/merge in progress, and has no unmerged index entries; (b) the target file (`sundial/schedules/<id>.json`) has no local modifications (staged or unstaged) that conflict with the daemon's expected state. If any precondition fails, **the entire mutation fails** — no local runtime state is created, no file is written. The daemon returns an actionable error to the CLI (e.g., `Error: data repo has rebase in progress — resolve and retry` or `Error: sundial/schedules/sch_a1b2c3.json has local modifications — discard or commit them first`). This ensures "success" always means the schedule is committed to the canonical data repo.
1. Write the schedule JSON file to `sundial/schedules/<id>.json`.
2. `git add -- sundial/schedules/<id>.json` (only the affected schedule file).
3. `git commit --only -- sundial/schedules/<id>.json -m "<descriptive message>"` (the `--only` flag creates a commit containing **only** the specified path, ignoring anything else in the staging area — this prevents accidentally sweeping in user-staged changes in a shared data repo).
4. Create the local runtime state entry (for `add`) or delete it (for `remove`). If this step fails (e.g., disk error writing to `~/.config/sundial/state/`), the daemon triggers an immediate in-process reconciliation — the same logic used on startup — to converge local state with the now-committed desired state. The CLI returns a degraded success indicating the schedule is committed but local state recovery was needed. This is a narrow edge case (local filesystem failure after a successful git commit) and the reconciliation path is already proven.
5. `git push` (best-effort — logged on failure, retried on next reconciliation point).

Commit messages follow a consistent format:
```
sundial: add schedule sch_a1b2c3 "Trash bin check"
sundial: remove schedule sch_a1b2c3
```

Only schedule mutations trigger git operations, and commits are path-scoped to the affected schedule file via `git commit --only`. This ensures sundial never commits unrelated changes the user may have staged in the data repo. **The git commit is the completion signal** — a successful `add` or `remove` always means the change is committed to the data repo. If git preconditions fail, the mutation fails entirely with an actionable error; there is no provisional local-only state. Operational data (run logs, fire history, missed fires) is stored locally and never committed. This keeps git history clean and eliminates the concurrency risk of background git operations during schedule fires.

If the push fails (no network, auth expired, remote conflict), the commit is still local. Push failures are logged but do not block the CLI. Pending pushes are retried at the next **reconciliation point** — daemon startup and `sundial reload`. This ensures local commits eventually propagate to the remote without requiring further mutations. If a retry also fails, it is logged and deferred to the next reconciliation point.

### Robustness

- **Wake from sleep:** The daemon process survives macOS sleep/wake — it does not exit and restart. On wake, the run loop detects elapsed time, applies the [missed fire policy](#missed-fire-policy-grace-window-then-skip), recomputes fire times, and resumes normal scheduling.
- **Crash recovery:** launchd `KeepAlive` restarts the daemon. On restart, the daemon executes the full [Shutdown / Restart Behavior](#shutdown--restart-behavior) sequence — including pre-restart miss handling. Desired state is in the data repo; runtime state is persisted locally. No state is lost.
- **Concurrent fires:** If two schedules fire at the same time, execute both concurrently (each in its own goroutine). A per-schedule mutex prevents overlapping runs of the same schedule. No serialization concern for git — only CLI operations (`add`, `remove`) trigger git, and they run synchronously.

### Environment Contract

Sundial has the same threat model as cron: it executes user-supplied commands in the user's own session with the user's own permissions. It does not elevate privileges, sandbox commands, or filter environment variables.

**v1 does not promise an interactive-shell environment.** LaunchAgents run with a more limited environment than an interactive terminal. Tools like `codex`, `uv`, or language runtimes may not be on `PATH` unless the user's shell profile sets them up.

To mitigate this, v1 launches commands via the user's login shell:
```
/bin/zsh -l -c "<command>"
```
The `-l` (login) flag makes zsh source login startup files (`/etc/zprofile`, `~/.zprofile`) but **not** `~/.zshrc` (which requires an interactive shell). This means PATH setup and tool managers (`nvm`, `pyenv`, `uv`) must be configured in `~/.zprofile` to be available to sundial commands. This is a known limitation — `sundial health` surfaces the effective PATH so users can verify their tools are reachable. A pragmatic tradeoff: slower than raw `/bin/sh -c` but gives commands access to login-shell environment.

**`sundial health` verifies infrastructure readiness** (see [Health Check](#health-check) for the full list). It does **not** validate individual scheduled commands — those are arbitrary shell strings that may use `cd`, pipes, `&&`, or runtime-dependent tools. The PATH report is informational, not a command-resolution check. `sundial add --dry-run` validates flags and computes the next fire time, but does not probe whether the command itself will succeed.

**Run log capture:** Stdout/stderr from fired commands are captured in local run logs with a bounded preview (first 10KB). Full output is not retained — commands that need output persistence should write to their own files.

No secrets are stored by sundial — API keys etc. are the user's responsibility via their environment or dotfiles.

## 9. Resume Support — Deferred (Post-v1)

Agent session resume (e.g., Codex `codex resume <session_id>`) is deferred to post-v1. The integration boundary is fragile and depends on contracts (session ID format, failure modes, portability) that are not yet stable. See [post-v1.md](post-v1.md) for full context.

For v1, sundial is **command-agnostic** — it schedules shell commands without knowing whether they are agents. Resume can be layered on without changing the core scheduling model.

## 10. Data Repo Integration

### Two-Store Model: Desired State + Runtime State

The data repo holds **desired state** — what should be scheduled. The local machine holds **runtime state** and **operational logs** — what is actually scheduled, its execution progress, and fire/miss history. The **daemon** owns all schedule I/O to both stores — the CLI never writes files directly. Only schedule definition changes trigger git commit + push (see [Post-Operation Git Hook](#post-operation-git-hook)). The one exception is `cli-feedback.jsonl`, which the agent writes directly (see [CLI Feedback](#cli-feedback)).

| Store | Location | Contents | Written by | In git? |
|---|---|---|---|---|
| Desired state (canonical) | `<data_repo>/sundial/schedules/` | Schedule definitions, trigger rules, metadata | Daemon (via `add`/`remove` RPC) | Yes (auto-commit) |
| Runtime state (local) | `~/.config/sundial/state/` | next_fire_at, fire history, exit codes | Daemon | No |
| Run logs (local) | `~/.config/sundial/logs/` | Fire and miss records, bounded stdout/stderr | Daemon | No |
| CLI feedback | `<data_repo>/sundial/cli-feedback.jsonl` | Agent-written friction/suggestions | Agent | Agent commits |

Only schedule definitions live in the data repo. All operational data (runtime state, run logs) stays local — this keeps git history clean and avoids background git operations during schedule fires. This is a deliberate departure from the [outreach CLI](https://github.com/fyang0507/fred-agent/blob/main/.agents/skills/outreach/SKILL.md) pattern where the agent owns all file I/O and git sync — here sundial handles schedule definition writes and git operations, while operational data is local-only.

### Portability via Reconciliation

The data repo is the **portable source of truth** for schedule configuration. **v1 assumes a single active machine** — the user is responsible for ensuring only one machine runs the daemon against a given data repo at a time. Portability is for **migration** (moving to a new machine), not concurrent execution. On the new machine, the daemon reads desired state from the data repo and creates local runtime entries for each active schedule — no manual replay needed. The user should stop the daemon on the old machine first to avoid duplicate execution. On an existing machine after a repo sync, run `sundial reload` to pick up changes without restarting the daemon.

**Reconciliation rules (applied on daemon startup and `sundial reload`):**

| Data repo | Local state | Action |
|---|---|---|
| `active` schedule | Missing | Create local runtime entry, compute `next_fire_at` |
| `active` schedule | Exists | Keep local state, recompute `next_fire_at` |
| `removed` schedule | Exists | Delete local runtime entry |
| Missing | Exists (orphan) | Disable locally, surface as `orphaned` in `health` and `show`. Data repo is canonical — if the definition is gone, the schedule should not run. |

The `recreation_command` in each schedule record is a **convenience hint** — a human-readable approximation of the CLI invocation that created the schedule. It is not a lossless representation: commands with nested quoting, shell metacharacters, or literal newlines may not round-trip safely. The canonical portability path is automatic reconciliation from the structured JSON desired state, not `recreation_command` replay.

### Directory Structure

```
<sundial-repo>/                     # this CLI repo (version-controlled)
  config.yaml          # mandatory — data_repo path + optional daemon overrides

<data-repo>/sundial/                # user's data repo (auto-committed by daemon)
  schedules/          # one JSON file per schedule — written by daemon on add/remove RPC
  cli-feedback.jsonl  # agent-written: friction, bugs, suggestions

~/.config/sundial/                  # local runtime data (not version-controlled)
  state/              # one JSON file per schedule — daemon-managed runtime state
  logs/               # one JSONL file per schedule — fire/miss records with bounded output
```

### What Sundial Writes (+ auto-commits)

| Event | Who | Data repo write | Local write | Git action |
|---|---|---|---|---|
| `sundial add` succeeds | Daemon (via CLI RPC) | `schedules/<id>.json` (create) | Runtime state | commit + push |
| `sundial remove` succeeds | Daemon (via CLI RPC) | `schedules/<id>.json` (status → `removed`) | Delete runtime state | commit + push |
| Schedule fires | Daemon | — | `logs/<id>.jsonl` (fire entry) + runtime state | — |
| Missed fire on restart | Daemon | — | `logs/<id>.jsonl` (miss entry) | — |

### What the Agent Writes

| Event | Where |
|---|---|
| CLI friction / suggestions | `cli-feedback.jsonl` (append — agent commits this itself) |

### Schedule Records (Desired State)

One JSON file per schedule in `sundial/schedules/`, written by the daemon when processing an `add` RPC. Contains the **full trigger definition** so the schedule can be reconstructed on any machine, plus the original `user_request` (passed via `--user-request` flag) for audit. See [Section 5](#desired-state-vs-runtime-state) for the full schema.

The `recreation_command` field stores a best-effort CLI invocation hint for this schedule. It is a **convenience string**, not a guaranteed lossless representation — complex commands with nested quoting or shell metacharacters may not round-trip safely. The primary portability mechanism is automatic reconciliation: the daemon reads the structured JSON desired state on startup. An agent migrating to a new machine should rely on reconciliation, not `recreation_command` replay.

### Run Log (Local)

One JSONL file per schedule in `~/.config/sundial/logs/`, named by schedule ID (e.g., `sch_a1b2c3.jsonl`). Strictly append-only. Written by the daemon, not the agent. **Not git-tracked** — operational logs stay local to avoid noisy git history and unbounded repo growth.

```jsonl
{"ts":"2026-04-14T18:42:00Z","type":"fire","schedule_id":"sch_a1b2c3","exit_code":0,"duration_s":34,"stdout_preview":"...first 10KB..."}
{"ts":"2026-04-15T18:38:00Z","type":"fire","schedule_id":"sch_a1b2c3","exit_code":0,"duration_s":29}
{"ts":"2026-04-21T18:35:00Z","type":"miss","schedule_id":"sch_a1b2c3","reason":"daemon_offline","scheduled_for":"2026-04-21T18:35:00Z"}
{"ts":"2026-04-28T18:30:00Z","type":"miss_summary","schedule_id":"sch_a1b2c3","reason":"daemon_offline","count":5,"from":"2026-04-22","to":"2026-04-28"}
```

Run log entries include a bounded stdout/stderr preview (first 10KB) for fire events. Commands that need full output persistence should write to their own files.

### CLI Feedback

**Intentional second writer.** The agent appends friction/bugs/suggestions directly to `sundial/cli-feedback.jsonl` and commits the file itself. This is a deliberate exception to the single-writer architecture. The concurrency risk is acceptable because: (1) the file is append-only JSONL — no read-modify-write cycle, so concurrent appends don't corrupt existing data; (2) it is a separate path from schedule definitions (`sundial/schedules/`), so it cannot interfere with the daemon's git operations; (3) the daemon never reads or depends on this file, so there is no coordination required.

```json
{"ts":"2026-04-14T19:00:00Z","category":"friction","command":"sundial add","description":"--offset flag doesn't accept '90m', only '-1h30m' or '-PT1H30M'","suggestion":"Accept minute-only offsets like '90m'"}
```

### Agent Workflow

The SKILL.md instructs the agent to:
1. Run `sundial health` → `sundial geocode` → `sundial add --user-request "..."` (CLI sends RPC to daemon, which writes schedule record, local runtime state, and auto-commits + pushes the data repo).
2. On encountering CLI friction: append to cli-feedback.jsonl at end of session.

The agent does **not** need to git sync schedule definitions — sundial handles that automatically via the [post-operation git hook](#post-operation-git-hook). Operational logs stay local and are not part of the agent's workflow.

## 11. Open Questions

| # | Question | Options | Status |
|---|----------|---------|--------|
| 1 | **Geocoding API choice** | OpenStreetMap Nominatim (free, no key) vs. Google/Mapbox (paid, more accurate) | **Decided:** Nominatim for v1. Free, no API key, sufficient for solar calculations. |
| 2 | **Cron expression flavor** | Standard 5-field cron vs. extended (seconds, years) | **Decided:** Standard 5-field. Matches `robfig/cron` default. |
| 3 | **Offset syntax** | ISO 8601 duration (`-PT1H`) vs. human shorthand (`-1h`, `+30m`) | **Decided:** Accept human shorthand in CLI flags, store as ISO 8601 internally. |
| 4 | **Duplicate detection algorithm** | Fuzzy (Levenshtein, substring) vs. deterministic (exact match) | **Decided:** Exact name match + exact command match for v1. Fuzzy matching deferred to post-v1. |
| 5 | **Reconciliation trigger** | Startup only vs. periodic (e.g., every 5 min) vs. file-watch on data repo | **Decided:** Startup + explicit `sundial reload` for v1. Periodic or file-watch deferred to post-v1. |
| 6 | ~~Session ID capture from Codex~~ | — | **Deferred** with resume support to post-v1. |

### Resolved from Design Review

These questions were raised by the design critique and resolved in this revision:

- **Single canonical schedule record?** → Two-store model: data repo = desired state, local = runtime state. Reconciled on startup.
- **Shell vs argv execution?** → Shell execution via `/bin/zsh -l -c` (login shell). Documented in Section 6.
- **What state is portable?** → Desired state (trigger, command, metadata) is portable. Runtime state (next_fire_at, fire history) is machine-local.
- **How are updates targeted?** → Deterministic exact match (name or command) on `add` with hints; `remove` + `add` for updates.
- **Missed-fire semantics?** → 60-second grace window, then skip. Backfill capped at 10 entries + summary.
- **`--dry-run`?** → Added to `sundial add`.
- **File format consistency?** → Standardized on JSON for all persisted data.
- **Write ownership?** → Single-writer architecture: daemon owns all schedule state mutation and git operations. CLI sends RPCs only, never writes files directly. One documented exception: agent appends to `cli-feedback.jsonl` directly — safe because it is append-only JSONL on a separate path that the daemon never reads.
- **Git automation scope?** → Only schedule definition changes (add/remove) trigger git commit+push. Operational logs stay local. Eliminates background git race conditions and noisy commit history.
- **Concurrent fire serialization?** → No git concern since fires don't trigger git ops. Per-schedule mutex for overlapping runs of the same schedule.
- **LaunchAgent environment?** → v1 launches via `/bin/zsh -l -c` (login shell — sources `~/.zprofile`, not `~/.zshrc`). Users must configure PATH in `~/.zprofile`. `sundial health` verifies effective PATH.
- **Run log size/redaction?** → Bounded 10KB stdout/stderr preview in local logs. Full output is the command's responsibility.
- **Orphaned schedule policy?** → Disable locally if desired state is missing from data repo. Surfaced in `health` and `show`.
- **`recreation_command` reliability?** → Downgraded to convenience hint only. Structured JSON desired state is the canonical portability mechanism via automatic reconciliation.
- **Cross-machine reconciliation lag?** → Added `sundial reload` RPC for v1. Explicit refresh path after repo sync — no daemon restart needed. Periodic/file-watch deferred to post-v1.
- **Duplicate detection algorithm?** → Deterministic exact-match (name or command) for v1. Fuzzy/similarity matching deferred to post-v1.
- **`data_repo` bootstrap?** → Mandatory config field in `config.yaml` (version-controlled in the sundial repo). User sets `data_repo` before running `sundial install`. Daemon refuses to start if missing or invalid, with actionable error messages.
- **Git push reliability?** → Pending pushes retried on daemon startup and `sundial reload`. Commits are path-scoped via `git commit --only` to avoid sweeping in user-staged changes.
- **`sundial health` scope?** → Infrastructure readiness only. Does not validate individual commands. PATH report is informational. `--dry-run` validates flags and fire times, not command executability.
- **Error notification?** → Deferred to post-v1. Future: relay errors via messaging (Discord, Slack).
- **Multi-machine duplicate execution?** → v1 assumes a single active machine. Portability is for migration only — the user stops the daemon on the old machine before starting on the new one. No machine ID or leader election needed.
- **Agent writes to data repo?** → Intentional exception to single-writer: agent appends to `cli-feedback.jsonl` directly. Safe because it is append-only JSONL on a separate path that the daemon never reads.
- **Git repo state preconditions?** → `health` checks for detached HEAD, in-progress rebase/merge, unmerged index entries, and local modifications to schedule files. If preconditions fail, the mutation fails entirely — no provisional local-only state. The git commit is the completion signal.
- **Path-level conflict on schedule files?** → If the target schedule file has local modifications (staged or unstaged), the mutation fails with an actionable conflict error. The daemon does not overwrite or merge user edits.
- **Restart grace-window ordering?** → Precise sequence: inspect persisted pre-restart `next_fire_at`, decide fire-vs-miss for that occurrence, then advance until `next_fire_at` is strictly in the future. Prevents double-fires and silent drops.
- **Startup vs reload semantics?** → Startup and crash recovery execute the full restart sequence (including pre-restart miss handling). Reload only reconciles and recomputes — no miss handling, since the daemon has been running continuously. Lifecycle and robustness sections reference the detailed algorithm instead of paraphrasing it.
- **Mutation success semantics?** → The git commit is the completion signal. A successful `add`/`remove` always means the change is committed to the data repo. If git preconditions fail, the entire mutation fails — no provisional local-only state.
- **Recomputation strategy vs startup algorithm?** → Section 5 recomputation strategy now distinguishes startup (inspect→decide→advance) from post-fire and reload (plain recompute). No stale summaries.
- **Health vs write-path scope for dirty files?** → Both use path-scoped checks. Health lists all dirty schedule files as informational warnings. The write path checks repo-level preconditions plus the specific target file only — an unrelated dirty schedule file does not block mutations.
- **Local-state failure after commit?** → If local runtime state creation/deletion fails after a successful commit, the daemon triggers immediate in-process reconciliation to converge. CLI returns degraded success. This is a narrow edge case and reuses the proven startup reconciliation path.
- **Stale git-sync hint in structured output?** → Removed "so the agent knows where to git sync" from the CLI output section. `saved_to` is retained for audit/debugging, not as a follow-up action. The agent does not git sync — sundial handles commit and push automatically.
- **Degraded-success output shape?** → Added plain-text and `--json` examples for the local-state recovery case. `recovery: local_state_reconciled` + `warning` field lets agents distinguish clean success from committed-with-recovery.

## 12. Future Work (Post-v1)

Full details, context, and v1 baselines for each item are in [post-v1.md](post-v1.md).

- **Agent session resume:** Codex `codex resume <session_id>`, Claude Code `--resume`, etc. Requires stable contracts for session ID production, failure classification, and portability. See [Section 9](#9-resume-support--deferred-post-v1).
- **Multi-agent support:** Claude Code (`claude -p`), Hermes, others.
- **Linux support:** Replace launchd with systemd user units. The daemon and CLI are already cross-platform; only the service management layer changes.
- **Additional dynamic triggers:** Weather-based ("if rain forecast > 60%"), calendar-based ("30 min before next meeting"), market-based ("after market close").
- **Pause / unpause** schedules without removing them.
- **`sundial run <id>`** for manual one-off execution (useful for testing).
- **`sundial logs <id>`** to view run history from the CLI.
- **Schedule groups** for bulk operations.
- **Error notification via messaging** (Discord, Slack, email) so users are notified of failures and can take action. v1 logs errors locally; future versions relay them to a configured channel.
- **Run log export** — export local run logs to data repo or external systems for audit/sharing.
- **Smarter git conflict resolution** — v1 does best-effort push; future versions could handle rebases or conflict resolution.
- **Fuzzy duplicate detection** — Levenshtein distance, substring matching, or semantic similarity for `sundial add` duplicate warnings. v1 uses deterministic exact-match only.
- **Periodic reconciliation** or file-watch on data repo for multi-device sync without daemon restart.
- **LaunchDaemon mode** for running schedules while logged out (requires elevated permissions).

## Appendix A: Solar Calculation Notes

The NREL Solar Position Algorithm (SPA) is the gold standard for computing solar zenith angle, from which sunrise/sunset times are derived. Key facts:

- Accuracy: +-0.0003 degrees in zenith angle over the period -2000 to 6000.
- Inputs: date, time, latitude, longitude, elevation, pressure, temperature.
- For sundial's purposes, elevation/pressure/temperature can use defaults with negligible impact on sunrise/sunset accuracy (< 1 minute error).
- Go libraries `go-sunrise` and `suncalc-go` implement variants of this. If precision is insufficient, porting the NREL SPA reference implementation is ~200 lines of Go.

Supported events for v1:
- `sunrise` / `sunset` -- when the sun's upper limb touches the horizon (standard definition, zenith angle 90.833 degrees).
- `civil_dawn` / `civil_dusk` -- sun is 6 degrees below horizon. Useful for "before it gets dark" scenarios.

## Appendix B: Example Agent Workflow

User says to Codex:
> "Set up a repeatable timer for detecting whether I've left trash bins on the sidewalk post garbage collection. Every Monday and Tuesday, 1 hour before sunset. The detection script is `python ~/project/trash-bin-detection.py` with a virtual env managed by uv."

Codex translates to:
```bash
# 1. Check system health
sundial health

# 2. Resolve location
sundial geocode "San Francisco, CA"
# → lat: 37.7749  lon: -122.4194  timezone: America/Los_Angeles

# 3. Preview the schedule (optional — verify before creating)
sundial add solar --dry-run \
  --event sunset \
  --offset "-1h" \
  --days mon,tue \
  --lat 37.7749 \
  --lon -122.4194 \
  --timezone "America/Los_Angeles" \
  --command "cd ~/projects/trash-bin-detection && uv run python trash-bin-detection.py"
# → schedule: Every Mon, Tue at 1h before sunset (37.7749, -122.4194)
#   next_fire: 2026-04-14 6:42pm PDT
#   (dry run — no schedule created)

# 4. Create the schedule (all info passed as flags — sundial writes data repo + local state, auto-commits)
sundial add solar \
  --event sunset \
  --offset "-1h" \
  --days mon,tue \
  --lat 37.7749 \
  --lon -122.4194 \
  --timezone "America/Los_Angeles" \
  --command "cd ~/projects/trash-bin-detection && uv run python trash-bin-detection.py" \
  --name "Trash bin detection" \
  --user-request "Set up a repeatable timer for trash bin detection. Every Monday and Tuesday, 1 hour before sunset."
```

Sundial responds (desired state written to data repo + auto-committed, runtime state created locally):
```
id: sch_7f8g9h
name: Trash bin detection
schedule: Every Mon, Tue at 1h before sunset (37.7749, -122.4194)
command: cd ~/projects/trash-bin-detection && uv run python trash-bin-detection.py
next_fire: 2026-04-14 6:42pm PDT
status: active
saved_to: ~/data_repo/sundial/schedules/sch_7f8g9h.json
committed: sundial: add schedule sch_7f8g9h "Trash bin detection"
```

No agent git sync needed — sundial handles the commit and push automatically.
