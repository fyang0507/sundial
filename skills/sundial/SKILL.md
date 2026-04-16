---
name: sundial
description: Scheduler service for agent. Cron, solar, and poll triggers.
---

# Sundial

CLI scheduler with cron, solar, and poll triggers. A background daemon manages all schedules over IPC. All commands accept `--json`.

## Setup

If `which sundial` fails or `sundial health` shows the daemon is not running, start it from the sundial repo:

```bash
cd <sundial-repo> && make start [launchd=1]
```

This builds, installs, and starts the daemon. Once running, all `sundial` commands work from any directory.

## Commands

| Action | Command |
|---|---|
| Create schedule | `sundial add --type cron\|solar\|poll ...` |
| List schedules | `sundial list` |
| Show details | `sundial show <id>` |
| Remove schedule | `sundial remove <id>` |
| Remove all | `sundial remove --all --yes` |
| Pause schedule | `sundial pause <id>` |
| Resume schedule | `sundial unpause <id>` |
| Health check | `sundial health` |
| Reload config | `sundial reload` |
| Look up coordinates | `sundial geocode "<address>"` |

## Creating Schedules

### Cron

```bash
sundial add --type cron \
  --cron "0 9 * * 1-5" \
  --command "cd ~/project && your-command-here" \
  --name "weekday 9am task"
```

### Solar

```bash
sundial add --type solar \
  --event sunset --offset "-1h" \
  --days mon,wed,fri \
  --lat 37.7749 --lon -122.4194 --timezone "America/Los_Angeles" \
  --command "cd ~/project && your-command-here" \
  --name "before-sunset task"
```

Required flags: `--event` (sunrise|sunset), `--days`, `--lat`, `--lon`, `--timezone`.

Optional `--offset`: human (`-1h`, `+30m`) or ISO 8601 (`-PT1H`, `PT30M`).

Use `sundial geocode "<address>" --json` to resolve an address into `lat`, `lon`, and `timezone`.

### Poll

Condition-gated periodic check. Runs a trigger command at a fixed interval; the main command fires only when the trigger exits 0.

```bash
sundial add --type poll \
  --trigger 'your-check-command --since "$SUNDIAL_LAST_FIRED_AT"' \
  --interval 2m --once \
  --command "cd ~/project && your-command-here" \
  --name "wait for condition"
```

Required flags: `--trigger` (condition command), `--interval` (check frequency, min 30s), `--timeout` (max lifetime, e.g. `72h`).

The trigger command receives `SUNDIAL_SCHEDULE_ID` and `SUNDIAL_LAST_FIRED_AT` env vars.

`--once` fires the command once then completes the schedule. Without it, the poll runs indefinitely. Completed schedules auto-reactivate if `sundial add` is called again with the same command.

### Shared Flags

- `--name` — human-readable label
- `--user-request` — store the original user request (always pass this)
- `--once` — fire once then complete the schedule
- `--dry-run` — validate and preview without creating
- `--force` — skip duplicate detection (exact and fuzzy)
- `--refresh` — update an existing schedule in place if name matches (requires `--name`; mutually exclusive with `--force`)
- `--detach` — fire-and-forget: spawn the command without waiting for exit. No `exit_code` or `duration_s` is captured; `sundial show` renders `last_fire: … (detached)`. Use this when the command is long-running and either logs its own outcome elsewhere or re-enters sundial (e.g. a callback that calls `sundial add --refresh`) — without `--detach` the per-schedule mutex is held for the full command duration and the nested refresh will be rejected as "schedule currently firing"

Duplicate detection catches both exact matches (same name or same command) and fuzzy matches (similar name via Levenshtein distance, or one command is a substring of another). Use `--force` to override.

### Refreshing Schedules

Use `--refresh` to atomically update an active schedule without removing it first. This is useful for resetting poll timeouts or changing trigger parameters while preserving the schedule ID.

```bash
# Original watcher with 72h timeout
sundial add --type poll --trigger "check-reply" --interval 2m --timeout 72h --once \
  --command "notify agent" --name "outreach-watch"

# Later: refresh with a new 72h countdown
sundial add --type poll --trigger "check-reply" --interval 2m --timeout 72h --once \
  --command "notify agent" --name "outreach-watch" --refresh
```

Behavior:
- If an active schedule with the same `--name` exists → updates it in place (status: `"refreshed"`, same ID).
- If no match → creates a new schedule (upsert semantics).
- Paused schedules are updated but stay paused.
- `CreatedAt` is reset, so poll timeouts restart from now.

Always `--dry-run` first when building a schedule from natural language.

## Workflow

1. Geocode if needed — `sundial geocode "<address>" --json`
2. Dry-run — `sundial add ... --dry-run --json`
3. Create — `sundial add ... --json`
4. Confirm — `sundial show <id> --json`

## Git Sync

After every `add` or `remove`, the daemon automatically commits the change to the data repo and pushes to the remote. You do not need to run any git commands.

- Each schedule is a JSON file at `sundial/schedules/sch_<id>.json` in this repo.
- Removal sets `status: "removed"` in the file rather than deleting it. `--once` schedules get `status: "completed"` after firing. Paused schedules get `status: "paused"`.
- Push is best-effort; `sundial health --json` reports `pending_pushes` if any failed.
- `sundial reload` retries pending pushes.

## Diagnosing Failures

If a scheduled command produces unexpected results, check the run logs:

- **Run logs**: `~/.config/sundial/logs/<id>.jsonl` — one JSONL entry per execution with stdout, stderr, exit code, and timestamps.
- **Daemon log**: `~/Library/Logs/sundial/sundial.log` — scheduler-level events, git errors, missed-fire warnings.

Read the run log first to see what the command actually produced, then the daemon log if the schedule itself misbehaved (didn't fire, git sync failed, etc.).

## Raw Data

Schedule definition files live in `sundial/schedules/` within this repo. Each is a JSON file you can read directly for schedule details, the stored user request, or the recreation command.

## Feedback and Improvement

You are the primary user of this tool — your observations drive its improvement. When you encounter rough edges while working, surface them.

**What to report**: bugs, friction (too many steps, missing defaults), missing features, unclear behavior or error messages.

**How to report**: append to `sundial/cli-feedback.jsonl` in this repo (one JSON object per line, append-only):

```json
{"ts":"2026-04-14T15:30:00Z","category":"friction","command":"sundial add","description":"No way to specify offset in minutes only — had to convert to hours","suggestion":"Accept bare minute values like --offset 30m"}
```

Fields: `ts` (ISO 8601), `category` (`bug` | `friction` | `missing_feature` | `unclear_behavior`), `command`, `description`, `suggestion` (optional).

**When to report**: after completing a task or at the end of a session. Don't interrupt your workflow — a one-liner is fine for minor issues.
