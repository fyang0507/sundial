# Scheduling with Sundial

For agents (and humans) who want sundial to run a command at some future point. If you're building a tool that uses sundial as infrastructure, read [integrating.md](integrating.md) instead. For setup and data-repo resolution, see [SKILL.md](SKILL.md).

The schedule you most likely care about is **yourself**: use sundial to invoke a future coding-agent session — fresh or resumed — at an absolute time, on a recurring cadence, or when an external condition becomes true.

## Invoke your future self

Sundial does not know or care what a command is; it just runs a shell string. That means any headless agent CLI works, including session resume — and because sundial is agent-agnostic, the same sundial invocation works equally well whether you drive Codex or Claude Code. Pick whichever your workflow uses.

**Codex** (NDJSON; session id arrives on the first `thread.started` line):
```bash
codex exec --yolo --json "<prompt>"                      # new session
codex exec resume <thread_id> --yolo --json "<prompt>"   # resume
```

**Claude Code** (single JSON; `session_id` field):
```bash
claude --dangerously-skip-permissions -p "<prompt>" --output-format json               # new
claude --resume <session_id> --dangerously-skip-permissions -p "<prompt>" --output-format json   # resume
```

### Wake up as a fresh session tomorrow at 10am

```bash
# Codex
sundial add at --at "2026-04-24T10:00:00" \
  --command 'codex exec --yolo "join the 10am standup, summarize the doc in ~/work/notes.md"' \
  --name "standup-wakeup" \
  --user-request "join standup tomorrow at 10am"

# Claude Code
sundial add at --at "2026-04-24T10:00:00" \
  --command 'claude --dangerously-skip-permissions -p "join the 10am standup, summarize the doc in ~/work/notes.md" --output-format json' \
  --name "standup-wakeup" \
  --user-request "join standup tomorrow at 10am"
```

### Continue THIS conversation later (resume the current session)

```bash
# Codex — thread id captured from this session's `thread.started` NDJSON line
sundial add at --at "2026-04-24T10:00:00" \
  --command 'codex exec resume abc123 --yolo "continue where we left off"' \
  --name "resume-session"

# Claude Code — session_id captured from this session's JSON envelope
sundial add at --at "2026-04-24T10:00:00" \
  --command 'claude --resume abc123 --dangerously-skip-permissions -p "continue where we left off" --output-format json' \
  --name "resume-session"
```

### Recurring: every weekday at 7am, as a fresh session

```bash
# Codex
sundial add cron --cron "0 7 * * 1-5" \
  --command 'codex exec --yolo "triage my inbox"' \
  --name "daily-triage"

# Claude Code
sundial add cron --cron "0 7 * * 1-5" \
  --command 'claude --dangerously-skip-permissions -p "triage my inbox" --output-format json' \
  --name "daily-triage"
```

### Wait for an external condition, then resume a specific session to act on it

```bash
# Codex
sundial add poll \
  --trigger 'outreach reply-check --contact-id c_abc --since "$SUNDIAL_LAST_FIRED_AT"' \
  --interval 2m --timeout 72h --once --detach \
  --command 'codex exec resume abc123 --yolo "a reply arrived — continue the campaign"' \
  --name "await-reply-c_abc"

# Claude Code
sundial add poll \
  --trigger 'outreach reply-check --contact-id c_abc --since "$SUNDIAL_LAST_FIRED_AT"' \
  --interval 2m --timeout 72h --once --detach \
  --command 'claude --resume abc123 --dangerously-skip-permissions -p "a reply arrived — continue the campaign" --output-format json' \
  --name "await-reply-c_abc"
```

A few things to think about before you write the `--command`:

- **Pick fresh vs. resume.** New session = new context, cheap but you must pass everything the future self needs via the prompt. Resume = inherits your current context, useful when the future call is a continuation of *this* conversation.
- **Always quote the nested prompt.** The command runs under a login shell (`/bin/zsh -l -c`). Use single quotes on the outer string and escape inner quotes as needed.
- **Write outputs somewhere readable.** The future session has no conversational channel to reach the user. Log to a file, the data repo, or whatever external sink the user already watches.
- **The daemon doesn't know "agent exited cleanly" from "agent fell over".** It only sees the shell exit code. If you care about outcome visibility, have the prompt instruct the agent to write a status file.

## Commands

| Action | Command |
|---|---|
| Create schedule | `sundial add cron\|solar\|poll\|at ...` |
| List schedules | `sundial list` |
| Show details | `sundial show <id>` |
| Remove schedule | `sundial remove <id>` |
| Remove all | `sundial remove --all --yes` |
| Pause schedule | `sundial pause <id>` |
| Resume schedule | `sundial unpause <id>` |
| Health check | `sundial health` |
| Reload config | `sundial reload` |
| Scaffold data repo | `sundial setup [--data-repo <path>]` |
| Look up coordinates | `sundial geocode "<address>"` |

## Creating Schedules

### Cron

```bash
sundial add cron \
  --cron "0 9 * * 1-5" \
  --command "cd ~/project && your-command-here" \
  --name "weekday 9am task"
```

Required flags: `--cron`.

### Solar

```bash
sundial add solar \
  --event sunset --offset "-1h" \
  --days mon,wed,fri \
  --lat 37.7749 --lon -122.4194 --timezone "America/Los_Angeles" \
  --command "cd ~/project && your-command-here" \
  --name "before-sunset task"
```

Required flags: `--event` (sunrise|sunset), `--days`, `--lat`, `--lon`.

Optional flags:
- `--offset` — human (`-1h`, `+30m`) or ISO 8601 (`-PT1H`, `PT30M`).
- `--timezone` — IANA timezone (e.g. `America/Los_Angeles`); defaults to the machine's local timezone.
- `--once` — fire once then complete.

Use `sundial geocode "<address>" --json` to resolve an address into `lat`, `lon`, and `timezone`.

### Poll

Condition-gated periodic check. Runs a trigger command at a fixed interval; the main command fires only when the trigger exits 0.

```bash
sundial add poll \
  --trigger 'your-check-command --since "$SUNDIAL_LAST_FIRED_AT"' \
  --interval 2m --timeout 72h --once \
  --command "cd ~/project && your-command-here" \
  --name "wait for condition"
```

Required flags: `--trigger` (condition command), `--interval` (check frequency, min 30s), `--timeout` (max lifetime, e.g. `72h`).

Optional flags:
- `--once` — fire once then complete the schedule. Without it, the poll runs indefinitely. Completed schedules auto-reactivate if `sundial add` is called again with the same command.

The trigger command receives `SUNDIAL_SCHEDULE_ID` and `SUNDIAL_LAST_FIRED_AT` env vars.

### At

One-off fire at an absolute timestamp. Fires exactly once, then auto-completes. Use for "wake me up at 10am tomorrow" or for agents scheduling a future session at a known time (e.g. rejoin a meeting).

```bash
sundial add at \
  --at "2026-04-20T10:00:00" \
  --command "codex exec 'join the standup'" \
  --name "standup reminder"
```

Required flags: `--at` (ISO timestamp).

`--at` formats:
- Naive local time — `2026-04-20T10:00:00` — interpreted in `--timezone` (defaults to machine's local zone).
- Zoned RFC3339 — `2026-04-20T10:00:00-07:00` or `2026-04-20T17:00:00Z` — `--timezone` is ignored.

Optional flags:
- `--timezone` — IANA timezone for naive timestamps. Ignored when `--at` includes an explicit offset.

Past timestamps are rejected at creation. There is no `--once` flag — `at` is implicitly one-shot. If the daemon is offline past the 60s grace window, the schedule completes with reason `missed`.

### Shared flags (all subcommands)

- `--command` — shell command to execute (required)
- `--name` — human-readable label
- `--user-request` — store the original user request (always pass this)
- `--dry-run` — validate and preview without creating
- `--force` — skip duplicate detection (exact and fuzzy)
- `--refresh` — update an existing schedule in place if name matches (requires `--name`; mutually exclusive with `--force`)
- `--detach` — fire-and-forget: spawn the command without waiting for exit. No `exit_code` or `duration_s` is captured; `sundial show` renders `last_fire: … (detached)`. Use this when the command is long-running and either logs its own outcome elsewhere or re-enters sundial (e.g. a callback that calls `sundial add --refresh`) — without `--detach` the per-schedule mutex is held for the full command duration and the nested refresh will be rejected as "schedule currently firing"

Duplicate detection catches both exact matches (same name or same command) and fuzzy matches (similar name via Levenshtein distance, or one command is a substring of another). Use `--force` to override.

### Refreshing schedules

Use `--refresh` to atomically update an active schedule without removing it first. This is useful for resetting poll timeouts or changing trigger parameters while preserving the schedule ID.

```bash
# Original watcher with 72h timeout
sundial add poll --trigger "check-reply" --interval 2m --timeout 72h --once \
  --command "notify agent" --name "outreach-watch"

# Later: refresh with a new 72h countdown
sundial add poll --trigger "check-reply" --interval 2m --timeout 72h --once \
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

## Data repo layout

Schedules are stored inside the shared data repo (the same git repo used by any other agent tooling):

```
<data_repo>/
  .agents/
    workspace.yaml        # shared across tools; sundial registers under tools.sundial
    skills/sundial/       # this skill tree (SKILL.md + child docs)
  sundial/
    config.yaml           # daemon options (optional; defaults apply)
    schedules/            # one JSON per schedule
```

Runtime state (`~/.config/sundial/state/`) and run logs (`~/.config/sundial/logs/`) stay local to the machine — they are not part of the data repo.

## Git sync

After every `add` or `remove`, the daemon automatically commits the change to the data repo and pushes to the remote. You do not need to run any git commands.

- Each schedule is a JSON file at `sundial/schedules/sch_<id>.json` in the data repo.
- Removal sets `status: "removed"` in the file rather than deleting it. `--once` schedules get `status: "completed"` after firing. Paused schedules get `status: "paused"`.
- Push is best-effort; `sundial health --json` reports `pending_pushes` if any failed.
- `sundial reload` retries pending pushes.

## Diagnosing failures

If a scheduled command produces unexpected results, check the run logs:

- **Run logs**: `~/.config/sundial/logs/<id>.jsonl` — one JSONL entry per execution with stdout, stderr, exit code, and timestamps.
- **Daemon log**: `~/Library/Logs/sundial/sundial.log` — scheduler-level events, git errors, missed-fire warnings.

Read the run log first to see what the command actually produced, then the daemon log if the schedule itself misbehaved (didn't fire, git sync failed, etc.).

## Raw data

Schedule definition files live in `sundial/schedules/` within the data repo. Each is a JSON file you can read directly for schedule details, the stored user request, or the recreation command.

## Feedback and improvement

You are the primary user of this tool — your observations drive its improvement. When you encounter rough edges while working, surface them.

**What to report**: bugs, friction (too many steps, missing defaults), missing features, unclear behavior or error messages.

**How to report**: append to `sundial/cli-feedback.jsonl` in the data repo (one JSON object per line, append-only):

```json
{"ts":"2026-04-14T15:30:00Z","category":"friction","command":"sundial add","description":"No way to specify offset in minutes only — had to convert to hours","suggestion":"Accept bare minute values like --offset 30m"}
```

Fields: `ts` (ISO 8601), `category` (`bug` | `friction` | `missing_feature` | `unclear_behavior`), `command`, `description`, `suggestion` (optional).

**When to report**: after completing a task or at the end of a session. Don't interrupt your workflow — a one-liner is fine for minor issues.
