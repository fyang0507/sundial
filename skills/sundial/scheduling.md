# Scheduling with Sundial

This enriches `sundial <command> --help` — it does not repeat it. Every flag, the accepted formats, and a worked example for each trigger already live in `sundial add cron|solar|poll|at --help`. What follows is the behavior and contracts the flag reference can't convey: the poll trigger contract, the `--detach` + `--refresh` callback pattern, duplicate detection, state inspection, the data-repo model, git sync, and diagnostics. For driving agent sessions specifically, see [agent-workflows.md](agent-workflows.md); for one-time setup, see [setup.md](setup.md).

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

## Trigger behavior beyond `--help`

**Solar** — resolve a street address into the `--lat`, `--lon`, and `--timezone` values you pass with `sundial geocode "<address>" --json`.

**At** — if the daemon is offline past the 60s grace window when an `at` schedule was due, the schedule completes with reason `missed` rather than firing late.

**Poll — the trigger contract.** Poll is the extension point most integrations use: it runs `--trigger` on the interval and fires `--command` only when the trigger exits `0`. When you write a trigger command:

1. Exit `0` when the condition holds, non-zero otherwise, and return **quickly** — poll checks block the scheduler tick, so keep them fast.
2. Sundial sets `SUNDIAL_SCHEDULE_ID` and `SUNDIAL_LAST_FIRED_AT` (ISO 8601) in the trigger's environment on every invocation, so the check can scope itself without sundial knowing your domain. Sundial only ever observes the exit code.
3. Minimum interval is 30s; timeouts are wall-clock (e.g. `72h`).

Without `--once` a poll runs until its timeout; with it the schedule completes after the first successful fire. A completed schedule auto-reactivates if `sundial add` is called again with the same command. For a worked example that waits for a condition and then resumes a specific agent session, see [agent-workflows.md](agent-workflows.md).

## Duplicate detection

`sundial add` rejects a schedule that collides with an existing one — exact (same name or same command) or fuzzy (similar name via Levenshtein distance, or one command is a substring of another). Use `--force` to bypass both checks, or `--refresh` to update the existing schedule in place.

## Refreshing schedules

`--refresh` atomically updates an active schedule in place instead of removing and re-adding it — useful for resetting a poll timeout or changing trigger parameters while preserving the schedule ID.

- Matches on `--name`, so `--refresh` requires `--name` (and is mutually exclusive with `--force`).
- If an active schedule with that name exists → updates it (status `"refreshed"`, same ID); otherwise creates one (upsert semantics).
- Paused schedules are updated but stay paused.
- `CreatedAt` is reset, so poll timeouts restart from now.

## The callback pattern (`--detach` + `--refresh`)

If a scheduled command itself calls back into sundial — e.g. a poll callback that re-arms the watcher for another 72h — you will deadlock unless you use both:

- `--detach` on the **outer** add releases the per-schedule mutex as soon as the child is spawned. Without it the mutex is held for the full command duration and the nested `sundial add` is rejected with `schedule currently firing`. (`--detach` also means no `exit_code`/`duration_s` is captured; `sundial show` renders `last_fire: … (detached)`.)
- `--refresh` on the **nested** add updates the existing schedule in place instead of colliding with duplicate detection.

Use `--detach` only when the callback logs its outcome elsewhere or re-enters sundial. For any command whose exit code you want recorded, let it run attached.

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
    config.yaml           # daemon options (optional; defaults apply)
    schedules/            # one JSON per schedule — the definition only
```

Runtime state (`~/.config/sundial/state/`) and run logs (`~/.config/sundial/logs/`) stay local to the machine — they are not part of the data repo. This split is deliberate: definitions are git-synced so they survive restarts and travel across machines; volatile runtime state stays local to keep git history clean.

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
