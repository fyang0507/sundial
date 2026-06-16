# Scheduling with Sundial

For anyone who wants sundial to run a command at some future point — agents scheduling their own future selves or setting reminders, and engineers building a tool that uses sundial as its scheduling primitive. The first half is the scheduling repertoire; [Building a tool on top of sundial](#building-a-tool-on-top-of-sundial) at the end is for integrators. For one-time setup (daemon, data repo), see [setup.md](setup.md).

The schedule you most likely care about is **yourself**: use sundial to invoke a future coding-agent session — fresh or resumed — at an absolute time, on a recurring cadence, or when an external condition becomes true.

## Invoke your future self

Sundial does not know or care what a command is; it just runs a shell string under `/bin/zsh -l -c`. That means any headless agent CLI works, including session resume — and because sundial is agent-agnostic, the same sundial invocation works whether you drive Codex or Claude Code. Pick whichever your workflow uses.

### Run agents with full local access

Sundial fires commands **unattended** — no human is present to approve a permission prompt or unblock a sandbox. A scheduled agent that pauses for approval just hangs until its timeout and the fire is wasted. So launch headless agents in a full-access mode that can read and write the local workspace and run commands without prompting. This is the **preferred** invocation for scheduled work:

- **Codex** — `--yolo` (alias for `--dangerously-bypass-approvals-and-sandbox`: no approvals, no sandbox). Maximum capability — the agent can touch anything your user can, so use it only on a machine you trust. For work that can stay inside one project, the contained alternative is `--sandbox workspace-write -a never` — writes are confined to the workspace, still no prompts.
- **Claude Code** — `--dangerously-skip-permissions` (equivalently `--permission-mode bypassPermissions`). On macOS, Claude Code's bash sandbox is **off by default**, so this single flag is enough for full local read/write. Don't enable the sandbox in settings for scheduled runs.

These flags are intentional and load-bearing here, not a careless shortcut: the interactive safety rails exist to keep a human in the loop, and a scheduler has no human in the loop. Keep the data repo and the host machine trusted accordingly.

The headless invocations, with the preferred flags:

**Codex** (NDJSON; session id arrives on the first `thread.started` line):
```bash
codex exec --yolo --json "<prompt>"                      # new session
codex exec resume <thread_id> --yolo --json "<prompt>"   # resume
```

**Claude Code** (single JSON envelope; `session_id` field):
```bash
claude --dangerously-skip-permissions -p "<prompt>" --output-format json                          # new
claude --resume <session_id> --dangerously-skip-permissions -p "<prompt>" --output-format json    # resume
```

### Wake up as a fresh session tomorrow at 10am

```bash
# Codex
sundial add at --at "2026-06-16T10:00:00" \
  --command 'codex exec --yolo "join the 10am standup, summarize the doc in ~/work/notes.md"' \
  --name "standup-wakeup" \
  --user-request "join standup tomorrow at 10am"

# Claude Code
sundial add at --at "2026-06-16T10:00:00" \
  --command 'claude --dangerously-skip-permissions -p "join the 10am standup, summarize the doc in ~/work/notes.md" --output-format json' \
  --name "standup-wakeup" \
  --user-request "join standup tomorrow at 10am"
```

### Continue *this* session later (resume yourself)

If you already know your own session id, you can schedule your future self to resume *this exact conversation* — same context, same working state — at a later time or when a condition flips. This is different from a fresh session: resume inherits everything you have now, so the future prompt can be a short "continue" instead of re-explaining the task.

How you come to know your own id — you can't read it out of thin air mid-session; it is handed to you at launch:

- **Claude Code** — the clean way is to be launched with a **pre-minted id**: whoever started you ran `claude --session-id <uuid> ...` and passed that `<uuid>` into your prompt or environment, so you already hold it. (Otherwise the id is the top-level `session_id` in the JSON envelope your caller captured when it started you.)
- **Codex** — your `thread_id` was emitted on the `thread.started` line when your session began, or it is the id you were resumed with. Your caller captures it and hands it to you.

Then schedule the resume of that same id:

```bash
# Claude Code — resume THIS session tomorrow at 10am (550e8400-… is the id you were launched with)
sundial add at --at "2026-06-16T10:00:00" \
  --command 'claude --resume 550e8400-e29b-41d4-a716-446655440000 --dangerously-skip-permissions -p "continue where we left off; write status to ~/work/status.md" --output-format json' \
  --name "resume-self" \
  --user-request "pick this back up tomorrow morning"

# Codex — resume THIS thread later
sundial add at --at "2026-06-16T10:00:00" \
  --command 'codex exec resume 01J8XABCDEF --yolo "continue where we left off; write status to ~/work/status.md"' \
  --name "resume-self"
```

Because the resumed command embeds your own id, name the schedule after yourself (`resume-self`, `resume-<task>`) so a later `--refresh` can find and re-arm it.

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
  --command 'codex exec resume 01J8XABCDEF --yolo "a reply arrived — continue the campaign"' \
  --name "await-reply-c_abc"

# Claude Code
sundial add poll \
  --trigger 'outreach reply-check --contact-id c_abc --since "$SUNDIAL_LAST_FIRED_AT"' \
  --interval 2m --timeout 72h --once --detach \
  --command 'claude --resume 550e8400-e29b-41d4-a716-446655440000 --dangerously-skip-permissions -p "a reply arrived — continue the campaign" --output-format json' \
  --name "await-reply-c_abc"
```

A few things to think about before you write the `--command`:

- **Pick fresh vs. resume.** New session = new context, cheap, but you must pass everything the future self needs via the prompt. Resume = inherits your current context, useful when the future call is a continuation of *this* conversation.
- **Always quote the nested prompt.** The command runs under a login shell (`/bin/zsh -l -c`). Use single quotes on the outer string and escape inner quotes as needed.
- **Write outputs somewhere readable.** The future session has no conversational channel to reach the user. Log to a file, the data repo, or whatever external sink the user already watches.
- **The daemon can't tell "agent exited cleanly" from "agent fell over".** It only sees the shell exit code. If you care about outcome visibility, have the prompt instruct the agent to write a status file.

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

The trigger command receives `SUNDIAL_SCHEDULE_ID` and `SUNDIAL_LAST_FIRED_AT` env vars. The full contract is under [The poll trigger contract](#the-poll-trigger-contract).

### At

One-off fire at an absolute timestamp. Fires exactly once, then auto-completes. Use for "wake me up at 10am tomorrow" or for agents scheduling a future session at a known time (e.g. rejoin a meeting).

```bash
sundial add at \
  --at "2026-06-20T10:00:00" \
  --command "codex exec --yolo 'join the standup'" \
  --name "standup reminder"
```

Required flags: `--at` (ISO timestamp).

`--at` formats:
- Naive local time — `2026-06-20T10:00:00` — interpreted in `--timezone` (defaults to machine's local zone).
- Zoned RFC3339 — `2026-06-20T10:00:00-07:00` or `2026-06-20T17:00:00Z` — `--timezone` is ignored.

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
- `--detach` — fire-and-forget: spawn the command without waiting for exit. No `exit_code` or `duration_s` is captured; `sundial show` renders `last_fire: … (detached)`. Required when the command re-enters sundial (e.g. a callback that calls `sundial add --refresh`) — see [The callback pattern](#the-callback-pattern---detach----refresh).

Duplicate detection catches both exact matches (same name or same command) and fuzzy matches (similar name via Levenshtein distance, or one command is a substring of another). Use `--force` to override.

### Refreshing schedules

Use `--refresh` to atomically update an active schedule without removing it first. Useful for resetting poll timeouts or changing trigger parameters while preserving the schedule ID.

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

## Inspecting state

**`sundial list` and `sundial show` are the source of truth.** They are the only views that combine the schedule *definition* with live *runtime* state — `next_fire`, `last_fired_at`, `fire_count`, `last_exit_code`. Always query them (with `--json` for machine parsing) rather than reading files:

```bash
sundial list --json
sundial show <id> --json
```

The git-tracked files under `<data_repo>/sundial/schedules/` hold only the definition (trigger, command, status) — never the runtime fields — so a raw file read can't tell you when something next fires or whether its last run succeeded. Treat those files as **persistence and sync, not a query API**: read them only for git archaeology (what changed, when) or when debugging the daemon. For everything else, ask the CLI.

### Which data repo? (it's not your cwd)

There is exactly one data repo that schedules are written to: **the one the daemon was launched with**, fixed at `make start` time and baked into the launchd plist. The `sundial` CLI is directory-agnostic — `add`/`remove`/`pause` from *any* directory send an RPC to that one daemon, which performs the write into its own repo. The CLI never writes schedule files itself, and your cwd does not redirect where they land. So `cd`-ing into a different project's tree and running `sundial add` still writes to the daemon's repo, not the project you're standing in.

Confirm which repo the running daemon is attached to with `sundial health --json` (it reports the resolved `data_repo`). The one command that *does* write relative to your cwd is `sundial setup`, which scaffolds a repo directly rather than going through the daemon.

### Data repo layout

Schedules are stored inside that data repo (the same git repo used by any other agent tooling):

```
<data_repo>/
  .agents/
    workspace.yaml        # shared across tools; sundial registers under tools.sundial
    skills/sundial/       # this skill tree (SKILL.md + child docs)
  sundial/
    config.yaml           # daemon options (optional; defaults apply)
    schedules/            # one JSON per schedule — the definition only
```

Runtime state (`~/.config/sundial/state/`) and run logs (`~/.config/sundial/logs/`) stay local to the machine — they are not part of the data repo. This split is deliberate: definitions are git-synced so they survive restarts and travel across machines; volatile runtime state stays local to keep git history clean.

### Git sync

After every `add` or `remove`, the daemon automatically commits the change to the data repo and pushes to the remote. You do not need to run any git commands.

- Each schedule is a JSON file at `sundial/schedules/sch_<id>.json` in the data repo.
- Removal sets `status: "removed"` in the file rather than deleting it. `--once` schedules get `status: "completed"` after firing. Paused schedules get `status: "paused"`.
- Push is best-effort; `sundial health --json` reports `pending_pushes` if any failed.
- `sundial reload` retries pending pushes and re-reads the schedule files from disk.

### Diagnosing failures

If a scheduled command produces unexpected results, check the run logs:

- **Run logs**: `~/.config/sundial/logs/<id>.jsonl` — one JSONL entry per execution with stdout, stderr, exit code, and timestamps.
- **Daemon log**: `~/Library/Logs/sundial/sundial.log` — scheduler-level events, git errors, missed-fire warnings.

Read the run log first to see what the command actually produced, then the daemon log if the schedule itself misbehaved (didn't fire, git sync failed, etc.).

## Building a tool on top of sundial

This section is for engineers building a tool or agent runner that uses sundial as its scheduling primitive — e.g. an `outreach-cli` shelling out to `sundial add poll`, or an agent wrapper that arms its own resume via `sundial add at`. If you just want to create schedules at runtime, everything you need is above.

### Mental model

Sundial is deliberately **command-agnostic**: it does not know whether a command is a coding agent, a mailer, or `echo hi`. It just spawns `/bin/zsh -l -c "<your command>"` and records the exit code. That minimalism is the integration surface — anything you can write as a shell command can become a scheduled task; anything you can read from exit codes and the data repo is observable. You do not link against sundial as a library; you shell out to the `sundial` CLI, same as an agent would.

### The shared data repo contract

Sundial, your tool, and any other agent tooling in the same stack share one git repository (layout under [Data repo layout](#data-repo-layout)). Conventions to follow when adding a tool to this layout:

- Register yourself under `tools.<your-tool>` in `.agents/workspace.yaml` with at least a `version` field. Mirror what sundial does.
- Keep operational logs **local** (e.g. `~/.config/<your-tool>/logs/`), not in the data repo. Sundial does this deliberately to keep git history clean; your tool should too.
- Ship a `SKILL.md` (and any child docs) under `.agents/skills/<your-tool>/`. Agents will discover it next to `sundial/SKILL.md`.
- Provide a `<your-tool> setup` command that writes your subtree idempotently, the way `sundial setup` does.

### Calling sundial from another tool

You always talk to the daemon through the CLI. There is no Go library and no stable IPC surface for third parties — the Unix-socket JSON-RPC is an internal detail and may change.

```bash
sundial add at --at "..." --command "..." --json
sundial list --json
sundial show <id> --json
sundial remove <id> --json
```

Every command accepts `--json` and is non-interactive. Exit code `0` = success, `1` = error; errors go to stderr.

Before issuing a command, check that sundial is installed and reachable:

```bash
sundial health --json
```

…which returns `data_repo`, `config`, daemon pid, and `pending_pushes`. Sundial will not be running if launchd has not started it yet; tell the user to run `make start` from the sundial repo.

### The poll trigger contract

`poll` is the extension point most integrations will use. When your tool wants sundial to wait for a condition and then fire a callback, you:

1. Ship a check command that exits `0` when the condition holds, non-zero otherwise, and **quickly** (poll checks block the scheduler tick).
2. Accept `SUNDIAL_SCHEDULE_ID` and `SUNDIAL_LAST_FIRED_AT` (ISO 8601) as environment variables so the check can scope itself without sundial knowing your domain. Sundial sets both on every trigger invocation.
3. Tell callers what interval and timeout to pass. Minimum interval is 30s; timeouts are wall-clock (e.g. `72h`).

Example (outreach watches for replies; sundial does not need to understand email):

```bash
sundial add poll \
  --trigger 'outreach reply-check --contact-id c_abc --since "$SUNDIAL_LAST_FIRED_AT"' \
  --interval 2m --timeout 72h --once --detach \
  --command 'codex exec resume <thread> --yolo "a reply arrived; continue the campaign"' \
  --name "await-reply-c_abc"
```

### The callback pattern (`--detach` + `--refresh`)

If a scheduled command itself calls back into sundial — for instance, a poll callback that re-arms the watcher for another 72h — you will deadlock unless you use both:

- `--detach` on the **outer** add, so the per-schedule mutex releases as soon as the child is spawned. Without this, the mutex is held for the full command duration and the nested `sundial add` inside the callback is rejected with `schedule currently firing`.
- `--refresh` on the **nested** add, so the existing schedule is updated in place instead of collided with by duplicate detection. Upsert keyed on `--name`.

Semantically: `--detach` = "don't wait for exit, no exit code captured, `sundial show` prints `last_fire: … (detached)`." `--refresh` = "update if exists, create otherwise; requires `--name`; resets `CreatedAt` so poll timeouts restart."

Use `--detach` only when the callback logs its outcome elsewhere or re-enters sundial. For any command whose exit code you want recorded, let it run attached.

### Obtaining and persisting session ids for resume

To resume an agent session (`codex exec resume <id>` / `claude --resume <id>`), you need an id, and an id does not come from thin air — it is **emitted by a prior headless invocation of the agent itself**. Your tool typically runs an agent once in headless mode to kick off the workflow, captures the id from that run's structured output, and embeds it in the `--command` it later hands to `sundial add`.

Each agent emits the id in a different shape:

**Codex** — NDJSON on stdout. First line is `{"type":"thread.started","thread_id":"..."}`:

```bash
out=$(codex exec --yolo --json "<initial prompt>")
thread_id=$(printf '%s\n' "$out" | head -1 | jq -r '.thread_id')
# or streaming: codex exec --yolo --json "..." | tee run.log | head -1 | jq -r '.thread_id'
```

**Claude Code** — a single JSON envelope on stdout (`--output-format json`). Top-level field `session_id`:

```bash
out=$(claude --dangerously-skip-permissions -p "<initial prompt>" --output-format json)
session_id=$(printf '%s' "$out" | jq -r '.session_id')
```

You can also **pre-mint** a Claude Code id and reuse it: `claude --session-id <uuid> -p "..." --output-format json` starts the session under a `<uuid>` you chose, so you don't need to parse it back out.

Persist the id in your tool's **own** subtree (e.g. `<data_repo>/<your-tool>/sessions/<entity>.json`) so you can look it up next time you need to resume. Do **not** persist it in sundial's subtree — `sundial/schedules/` is owned by the daemon.

Then feed the id into the scheduled command:

```bash
sundial add at --at "2026-06-20T10:00:00" \
  --command "codex exec resume $thread_id --yolo 'continue where we left off'" \
  --name "resume-$thread_id"
```

A subtle point: the schedule file (and the git commit sundial pushes) now contains the session id as a substring of the command. Treat session ids as ordinary identifiers, not secrets — but if your tool's threads can be resumed by anyone with the id, factor that into whether the data repo should be private.

## Feedback and improvement

You are the primary user of this tool — your observations drive its improvement. When you encounter rough edges while working, surface them.

**What to report**: bugs, friction (too many steps, missing defaults), missing features, unclear behavior or error messages.

**How to report**: append to `sundial/cli-feedback.jsonl` in the data repo (one JSON object per line, append-only):

```json
{"ts":"2026-06-14T15:30:00Z","category":"friction","command":"sundial add","description":"No way to specify offset in minutes only — had to convert to hours","suggestion":"Accept bare minute values like --offset 30m"}
```

Fields: `ts` (ISO 8601), `category` (`bug` | `friction` | `missing_feature` | `unclear_behavior`), `command`, `description`, `suggestion` (optional).

**When to report**: after completing a task or at the end of a session. Don't interrupt your workflow — a one-liner is fine for minor issues.
