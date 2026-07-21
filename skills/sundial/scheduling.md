# Scheduling with Sundial

This enriches `sundial <command> --help` — it does not repeat it. Every flag, the accepted formats, and a worked example for each trigger already live in `sundial add cron|solar|poll|at --help`. What follows is the behavior and contracts the flag reference can't convey: the poll trigger contract, the `--detach` + `--refresh` callback pattern, `--exec-timeout` and `--precondition` readiness gates, the daemon-wide active-hours firing window, duplicate detection, state inspection, the data-repo model, git sync, and diagnostics. For driving agent sessions specifically, see [agent-workflows.md](agent-workflows.md); for one-time setup, see [setup.md](setup.md).

You always talk to the daemon through the CLI — there is no Go library and no stable IPC surface for third parties (the Unix-socket JSON-RPC is an internal detail and may change). Every command accepts `--json` and is non-interactive: exit code `0` = success, `1` = error; errors go to stderr.

## Commands

| Action | Command |
|---|---|
| Create schedule | `sundial add cron\|solar\|poll\|at ...` |
| List schedules | `sundial list` |
| Show details | `sundial show <id>` |
| Remove schedule | `sundial remove <id>` (or `--all --yes`) |
| Pause / resume | `sundial pause <id>` / `sundial unpause <id>` |
| Health check | `sundial health` |
| Reload config | `sundial reload` |
| Scaffold data repo | `sundial setup [--data-repo <path>]` |
| Look up coordinates | `sundial geocode "<address>"` |

Run `sundial <command> --help` for the flags and an example. `sundial health --json` returns the resolved `data_repo`, `config` path, daemon pid, and `pending_pushes`; if the daemon isn't reachable, launchd hasn't started it — run `make start` from the sundial repo (see [setup.md](setup.md)).

## Schedule lifecycle

A schedule moves through a small set of states: **active** → **paused** (via `sundial pause`) → **active** (`unpause`); **active** → **completed** (a `--once` schedule after it fires, or an `at` after its single fire) → auto-reactivates on a matching `add` (by name or command); **active** → **removed** (`sundial remove`). Completion and removal are recorded as a `status` change in the data-repo file — not a delete — and paused schedules persist with their definition intact. This is the canonical reference for the transitions mentioned elsewhere.

## Trigger behavior beyond `--help`

**Solar** — resolve a street address into the `--lat`, `--lon`, and `--timezone` values you pass with `sundial geocode "<address>" --json`.

**At** — if the daemon is offline past the 60s grace window when an `at` schedule was due, the schedule completes with reason `missed` rather than firing late.

**Poll — the trigger contract.** Poll is the extension point most integrations use: it runs `--trigger` on the interval and fires `--command` only when the trigger exits `0`. When you write a trigger command:

1. Exit `0` when the condition holds, non-zero otherwise, and return **quickly** — poll checks block the scheduler tick, so keep them fast.
2. Sundial sets `SUNDIAL_SCHEDULE_ID` and `SUNDIAL_LAST_FIRED_AT` (ISO 8601) in the trigger's environment on every invocation, so the check can scope itself without sundial knowing your domain. Sundial only ever observes the exit code.
3. Minimum interval is 30s; timeouts are wall-clock (e.g. `72h`).

Without `--once` a poll runs until its timeout; with it the schedule completes after the first successful fire. A completed schedule auto-reactivates if `sundial add` is called again with a matching name or command (name is checked first) — so re-adding a one-off by its old name, even with different command text, revives the completed schedule rather than creating a new one. For a worked example that waits for a condition and then resumes a specific agent session, see [agent-workflows.md](agent-workflows.md).

## Duplicate detection

`sundial add` rejects a schedule that collides with an existing one — exact (same name or same command) or fuzzy (similar name via Levenshtein distance, or one command is a substring of another). Use `--force` to bypass both checks, or `--refresh` to update the existing schedule in place.

## Refreshing schedules

`--refresh` atomically updates an active schedule in place instead of removing and re-adding it — useful for resetting a poll timeout or changing trigger parameters while preserving the schedule ID.

- Matches on `--name`, so `--refresh` requires `--name`. It does not combine with `--force`, because `--force` bypasses the very duplicate check `--refresh` relies on to find the existing schedule.
- If an active schedule with that name exists → updates it (status `"refreshed"`, same ID); otherwise creates one (upsert semantics).
- Paused schedules are updated but stay paused.
- `CreatedAt` is reset, so poll timeouts restart from now.

## The callback pattern (`--detach` + `--refresh`)

If a scheduled command itself calls back into sundial — e.g. a poll callback that re-arms the watcher for another 72h — you will deadlock unless you use both:

- `--detach` on the **outer** add releases the per-schedule mutex as soon as the child is spawned. Without it the mutex is held for the full command duration and the nested `sundial add` is rejected with `schedule currently firing`. (`--detach` also means no `exit_code`/`duration_s` is captured; `sundial show` renders `last_fire: … (detached)`.)
- `--refresh` on the **nested** add updates the existing schedule in place instead of colliding with duplicate detection.

Use `--detach` only when the callback logs its outcome elsewhere or re-enters sundial. For any command whose exit code you want recorded, let it run attached.

## Command forms: shell line vs. argv array (paths with spaces)

`--command` (and `--trigger` and `--precondition`) is a **shell command LINE**: the daemon runs it verbatim through `zsh -l -c <line>`. That gives you pipes, redirection, globs, and `$VAR` expansion — but it also means a **bare filesystem path containing a space word-splits and dies with a silent `exit 127`** (e.g. `.../My Drive/.../script.zsh` becomes two tokens). This is the trap behind a script that "never runs" with no useful log.

For a path (or any argument) with spaces, use the **argv-array form** instead — a repeatable flag, one value per argument:

```bash
sundial add cron --cron "0 9 * * *" \
  --command-arg "/Users/you/My Drive/scripts/backup.zsh" \
  --command-arg "--target" --command-arg "value with space"
```

Each `--command-arg` value becomes a distinct argv word, so spaces never split and **no shell quoting is stored** in the schedule JSON (it persists as a `command_args` array). The array is executed as `zsh -l -c 'exec "$@"' zsh <args…>` — still a **login shell**, so `PATH` is rebuilt from your profile and user tools (`uv`, `codex`, homebrew, `~/.local/bin`) resolve exactly as with the string form. The array form does **not** interpret pipes/globs/redirection — it runs the program directly, so use the string form when you actually want shell features.

- Sibling flags for the other command fields: `--precondition-arg` (repeatable) and, for poll, `--trigger-arg` (repeatable).
- The string and array forms of a field are **mutually exclusive** — pass one or the other, not both.
- `sundial show` renders an array-form command as a bracketed, quoted list tagged `(argv array)`; JSON exposes `command_args` / `precondition_args` / `trigger_command_args`.
- Rule of thumb: **shell features → string form; a path/argument with spaces → array form.**

## Capping a command's runtime (`--exec-timeout`)

`--exec-timeout <duration>` (e.g. `30m`, `90s`) bounds the wall-clock runtime of an attached command. It is **opt-in**: with no flag the daemon waits indefinitely (the default). Use it for commands that can hang — a network fetch, an agent session, anything that might never return — so one stuck run can't hold the per-schedule mutex forever and stall the next fire.

- On expiry the command's entire process group is SIGKILLed (children die too, not just the shell). The run is recorded as a fire with `exit_code: -1`, `reason: "timeout"`, and a `[sundial: killed after exec_timeout …]` note in the stderr preview.
- `--exec-timeout` cannot be combined with `--detach`: a detached command's exit is never captured, so there is nothing to enforce a deadline against. `sundial add` rejects the combination at validation time.
- Do not confuse it with poll's `--timeout`, which bounds the *lifetime* of the whole poll schedule (how long to keep checking), not the runtime of a single command.

## Readiness gates (`--precondition`)

`--precondition <command>` is a **readiness gate layered on any trigger** (cron, solar, poll, or at). Before each scheduled fire the daemon runs the precondition: **exit 0 → proceed and fire; non-zero → defer** (the command does *not* run, it does *not* count as a fire) and retry later with bounded exponential backoff. With no `--precondition` the schedule fires unconditionally — the default.

**Precondition vs. the poll trigger.** They look similar but solve different problems:

- **poll** *is* the trigger: it has no other schedule and checks its `--trigger` command on a **fixed interval** until the condition holds (or its `--timeout` lifetime expires). Use it for "fire whenever this becomes true — could be minutes, could be days."
- **precondition** is a gate on a trigger that already has its own cadence. Use it for "fire on my normal schedule, but only once the world is ready." When the gate isn't ready it retries with **growing backoff** (not a fixed interval) and gives up by default at the next regular fire.

### Backoff and termination

- **Default backoff**: `1m, 5m, 30m, 1h, 2h`. The Nth deferral waits the Nth entry; the **last entry repeats as the cap**. Override the daemon-wide default via `daemon.precondition_backoff` in `<sundial-repo>/sundial.config.yaml`.
- **Default termination** is bounded by the **next regular fire**: the daemon keeps retrying until the precondition passes *or* the next backoff retry would land at/after the schedule's next regular fire. At that point it gives up, logs a miss (`reason: "precondition not met"`), resets the backoff, and advances to the next regular fire. For a one-off `at` (no next regular fire), the deadline is the first deferral plus a max-elapsed budget (default `2h`, configurable via `daemon.precondition_max_elapsed`); on expiry the `at` schedule completes with reason `missed`.
- **Per-schedule overrides**:
  - `--precondition-backoff "1m,5m,30m,1h,2h"` — comma-separated Go durations; replaces the schedule's backoff (each entry validated at add time).
  - `--precondition-max-elapsed <duration>` — when set, the daemon also terminates once `now ≥ first-deferral + this budget`. It takes **precedence** and applies *in addition to* the next-regular-fire bound: a deferral gives up as soon as *either* the next retry crosses the next regular fire *or* the elapsed budget is exhausted, whichever comes first.

Each deferral is recorded as a `deferred` run-log entry (with the attempt number and next retry delay); the eventual give-up is a `miss`. `sundial show` and the run log under `~/.config/sundial/logs/<id>.jsonl` reflect both.

### For complex or multiple conditions

The precondition is a single shell command, but a command can be a script. For several conditions (network *and* a file present *and* a service healthy), write a script that returns 0 only when all hold and pass its path: `--precondition "/path/to/ready.sh"`. The daemon exports `SUNDIAL_SCHEDULE_ID` and `SUNDIAL_LAST_FIRED_AT` into the precondition's environment, same as a poll trigger, so the script can scope itself.

### The network special case

The motivating case is "don't fire while the machine is offline." A connectivity check makes a good precondition — fast, unambiguous exit code:

```bash
sundial add cron --cron "0 9 * * *" \
  --command "your-network-dependent-command" \
  --precondition "curl -sf --max-time 5 -o /dev/null https://www.google.com/generate_204" \
  --exec-timeout 10m
```

`generate_204` returns HTTP 204 with an empty body and is widely used as a captive-portal/connectivity probe; `curl -sf --max-time 5` exits non-zero on any failure (DNS, timeout, non-2xx), so a flaky or absent network defers the fire. A pure-offline check that avoids any external endpoint is `route -n get default >/dev/null 2>&1` (a default route exists) or a DNS lookup like `dscacheutil -q host -a name www.apple.com >/dev/null 2>&1`. The default backoff (`1m, 5m, 30m, 1h, 2h`) is well suited to transient outages; widen it with `--precondition-backoff` if your network recovers slowly. Pair the network-dependent **command** with `--exec-timeout` (see "Capping a command's runtime" above) so a fetch that connects and then hangs can't wedge the schedule.

## Active-hours window (global; set via CLI; `--ignore-active-hours` to opt out)

Active hours model **your waking hours**, so they are a **daemon-wide setting**, not a per-schedule flag — and they are **runtime state you change with the CLI**, not a config-file value (so an agent can adjust them without editing a file or restarting the daemon):

```bash
sundial set-active-hours "08:00-22:00"                     # every schedule fires only in this window
sundial set-active-hours "22:00-02:00"                     # window may cross midnight
sundial set-active-hours "08:00-22:00" --tz America/Denver # pin to a fixed zone
sundial set-active-hours --clear                           # remove the window (fire at any hour)
```

The change is **applied to the running daemon immediately** — all existing schedules are re-clamped into the new window on the spot — and **persists across restarts** (stored in local state, not the data repo, since active hours are machine-local policy). `set-active-hours` reports how many schedules it re-clamped. **Every** schedule obeys the window (this mirrors `daemon.wake`: a single system-wide policy the schedules don't re-litigate).

A fire whose natural slot falls outside the window is **deferred to the next window opening** (never dropped), for **every** trigger type: a cron `0 3 * * *` (daily 3am) runs at 08:00 instead; a poll interval that comes due at 02:00 waits until 08:00 to run its check; an `at` set for a time outside the window fires once when the window next opens.

- **Follows your body clock by default.** With no `--tz`, the window is interpreted in the **machine's current local timezone and auto-updates when that changes**. Fly NYC → SFO and the daemon detects the new system zone (within ~a minute) and re-evaluates the window against it, so `08:00-22:00` keeps meaning *your local* 8am–10pm rather than staying on East-coast time. Pass `--tz` an IANA name (e.g. `America/Los_Angeles`) to **pin** the window to a fixed zone that does *not* travel. Windows are always evaluated in wall-clock terms in the effective zone, so they track DST either way.
- **Crossing midnight.** When start > end the window wraps past midnight: `"22:00-02:00"` is active from 22:00 through 02:00 the next day. The interval is half-open `[start, end)` — a fire exactly at the end time is outside.
- **Deferral, not catch-up-per-occurrence.** Many closed-period occurrences collapse into a **single** fire at the window opening — a `*/30` cron fires at 08:00, not once for every half-hour it was closed. `next_fire` (in `list`/`show`) always shows the next *eligible* time, already clamped into the window.
- **Per-schedule opt-out.** A schedule that must run off-hours (e.g. a 3am backup) exempts itself with `sundial add ... --ignore-active-hours`; it then fires at any hour regardless of the global window. There is no per-schedule *window* — the only per-schedule control is the on/off opt-out.
- **Interaction with `--precondition`.** Active hours is the **outer** gate: outside the window the fire is suppressed before the precondition is even checked. Inside the window, the precondition applies as usual.

When a fire is held back by the window, the daemon records a **`suppressed` run-log entry** (`reason: "outside active hours <window> (deferred to <time>)"`) — distinct from a command failure (a `fire` with a non-zero `exit_code`), a precondition deferral (`deferred`), and a poll-check false (no entry). While the current time is inside a closed period, `sundial show` renders an `active_hours: <window> (currently outside window — next fire deferred to window opening)` line (JSON: `active_hours`, `suppressed`) and `list` tags the schedule `[outside active hours]`. `sundial health` reports the current window. A schedule that opted out shows `active_hours: ignored` (JSON: `ignore_active_hours`). `sundial add` echoes an `active_hours` reminder so you know the new schedule is confined to the window (and how to override).

```bash
# Set your waking hours once; every schedule is then confined to them.
sundial set-active-hours "08:00-22:00"

# This X reply pipeline is automatically confined to waking review hours.
sundial add cron --cron "0 * * * *" \
  --command "reply-pipeline run" \
  --user-request "hourly X reply drafts to review while I'm awake"

# A backup that MUST run overnight opts out of the window:
sundial add cron --cron "0 3 * * *" --ignore-active-hours \
  --command "backup run" --user-request "nightly 3am backup"
```

## Recommended workflow

1. Geocode if needed — `sundial geocode "<address>" --json`
2. Dry-run — `sundial add ... --dry-run --json` (always dry-run first when building a schedule from natural language)
3. Create — `sundial add ... --json`
4. Confirm — `sundial show <id> --json`

Always pass `--user-request` with the original request that motivated the schedule.

## Inspecting state

**`sundial list` and `sundial show` are the source of truth.** They are the only views that combine the schedule *definition* with live *runtime* state — `next_fire`, `last_fired_at`, `fire_count`, `last_exit_code`. Always query them (with `--json` for machine parsing) rather than reading files:

```bash
sundial list --json
sundial show <id> --json
```

`sundial show` also surfaces the schedule's readiness configuration (`exec_timeout`, `precondition`, and any `precondition_backoff`/`precondition_max_elapsed` overrides) and — crucially — its **live precondition-backoff state**. When a fire is currently held back because the precondition exited non-zero, `show` renders a `deferred: precondition not met (attempt N, next retry <local time>)` line (in JSON: `precondition_deferred`, `precondition_attempts`, `precondition_retry_at`), and `list` tags that schedule `[deferred]`. This lets you distinguish a schedule *waiting on a precondition* (its `next_fire` is the retry instant, not the natural slot) from one that is genuinely idle until its next fire. It is the live complement to the `deferred` run-log entries that record past deferrals. It also surfaces the daemon-wide active-hours window the schedule obeys and, when the current time is inside a closed period, marks it suppressed (JSON: `active_hours`, `suppressed`; `list` tags it `[outside active hours]`) — the live complement to the `suppressed` run-log entries. A schedule that opted out via `--ignore-active-hours` shows `active_hours: ignored` instead.

The git-tracked files under `<data_repo>/sundial/schedules/` hold only the definition (trigger, command, status) — never the runtime fields — so a raw file read can't tell you when something next fires or whether its last run succeeded. Treat those files as **persistence and sync, not a query API**: read them only for git archaeology (what changed, when) or when debugging the daemon. For everything else, ask the CLI.

### Which data repo? (it's not your cwd)

There is exactly one data repo that schedules are written to: **the one the daemon was launched with**, fixed at `make start` time and baked into the launchd plist. The `sundial` CLI is directory-agnostic — `add`/`remove`/`pause` from *any* directory send an RPC to that one daemon, which performs the write into its own repo. The CLI never writes schedule files itself, and your cwd does not redirect where they land. So `cd`-ing into a different project's tree and running `sundial add` still writes to the daemon's repo, not the project you're standing in.

Confirm which repo the running daemon is attached to with `sundial health --json` (it reports the resolved `data_repo`). The one command that *does* write relative to your cwd is `sundial setup`, which scaffolds a repo directly rather than going through the daemon.

### Data repo layout

Sundial, your tool, and any other agent tooling in the same stack share one git repository:

```
<data_repo>/
  .agents/
    workspace.yaml        # shared across tools; sundial registers under tools.sundial
    skills/sundial/       # this skill tree (SKILL.md + child docs)
  sundial/
    schedules/            # one JSON per schedule — the definition only
```

The data repo holds **only data** — schedules, the workspace marker, and agent skill symlinks. Daemon options are not stored here; they live in `<sundial-repo>/sundial.config.yaml`. Runtime state (`~/.config/sundial/state/`) and run logs (`~/.config/sundial/logs/`) stay local to the machine — they are not part of the data repo. This split is deliberate: definitions are git-synced so they survive restarts and travel across machines; volatile runtime state stays local to keep git history clean.

Conventions to follow when adding your own tool to this shared layout:

- Register yourself under `tools.<your-tool>` in `.agents/workspace.yaml` with at least a `version` field. Mirror what sundial does.
- Keep operational logs **local** (e.g. `~/.config/<your-tool>/logs/`), not in the data repo. Sundial does this deliberately to keep git history clean; your tool should too.
- Ship a `SKILL.md` (and any child docs) under `.agents/skills/<your-tool>/`. Agents will discover it next to `sundial/SKILL.md`.
- Provide a `<your-tool> setup` command that writes your subtree idempotently, the way `sundial setup` does.

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

## Feedback and improvement

You are the primary user of this tool — your observations drive its improvement. When you encounter rough edges while working, surface them.

**What to report**: bugs, friction (too many steps, missing defaults), missing features, unclear behavior or error messages.

**How to report**: append to `sundial/cli-feedback.jsonl` in the data repo (one JSON object per line, append-only):

```json
{"ts":"2026-06-14T15:30:00Z","category":"friction","command":"sundial add","description":"No way to specify offset in minutes only — had to convert to hours","suggestion":"Accept bare minute values like --offset 30m"}
```

Fields: `ts` (ISO 8601), `category` (`bug` | `friction` | `missing_feature` | `unclear_behavior`), `command`, `description`, `suggestion` (optional).

**When to report**: after completing a task or at the end of a session. Don't interrupt your workflow — a one-liner is fine for minor issues.
