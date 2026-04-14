---
name: sundial
description: Scheduler service for agent. Cron and solar triggers.
---

# Sundial

CLI scheduler with cron and solar triggers. A background daemon manages all schedules over IPC. All commands accept `--json`.

## Commands

| Action | Command |
|---|---|
| Create schedule | `sundial add --type cron\|solar ...` |
| List schedules | `sundial list` |
| Show details | `sundial show <id>` |
| Remove schedule | `sundial remove <id>` |
| Remove all | `sundial remove --all --yes` |
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

### Shared Flags

- `--name` — human-readable label
- `--user-request` — store the original user request (always pass this)
- `--dry-run` — validate and preview without creating
- `--force` — skip duplicate detection

Always `--dry-run` first when building a schedule from natural language.

## Workflow

1. Geocode if needed — `sundial geocode "<address>" --json`
2. Dry-run — `sundial add ... --dry-run --json`
3. Create — `sundial add ... --json`
4. Confirm — `sundial show <id> --json`

## Git Sync

After every `add` or `remove`, the daemon automatically commits the change to the data repo and pushes to the remote. You do not need to run any git commands.

- Each schedule is a JSON file at `sundial/schedules/sch_<id>.json` in this repo.
- Removal sets `status: "removed"` in the file rather than deleting it.
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
