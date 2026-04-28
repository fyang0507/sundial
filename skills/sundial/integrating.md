# Integrating with Sundial

For engineers building a tool or agent runner that uses sundial as its scheduling primitive — e.g. `outreach-cli` shelling out to `sundial add poll`, or an agent wrapper that arms its own resume via `sundial add at`. If you just want to create schedules at runtime, read [scheduling.md](scheduling.md) instead. For one-time setup (daemon, data repo), see [setup.md](setup.md).

## Mental model

Sundial is a macOS daemon that runs shell commands on four trigger types (cron, solar, poll, at). It is deliberately **command-agnostic**: it does not know whether a command is a coding agent, a mailer, or `echo hi`. It just spawns `/bin/zsh -l -c "<your command>"` and records the exit code.

That minimalism is the integration surface. Anything you can write as a shell command can become a scheduled task; anything you can read from exit codes and the data repo is observable. You do not link against sundial as a library — you shell out to the `sundial` CLI, same as an agent would.

## The shared data repo

Sundial, your tool, and any other agent tooling in the same stack share one git repository. Sundial scaffolds it via `sundial setup`:

```
<data_repo>/
  .agents/
    workspace.yaml        # shared registry; sundial stamps tools.sundial
    skills/
      sundial/            # this skill tree, synced by `sundial setup`
      <your-tool>/        # ship your own skill alongside sundial's
  sundial/
    config.yaml
    schedules/            # one JSON per schedule (committed + pushed)
  <your-tool>/            # your tool's own subtree
```

Conventions to follow when adding a tool to this layout:

- Register yourself under `tools.<your-tool>` in `.agents/workspace.yaml` with at least a `version` field. Mirror what sundial does.
- Keep operational logs **local** (e.g. `~/.config/<your-tool>/logs/`), not in the data repo. Sundial does this deliberately to keep git history clean; your tool should too.
- Ship a `SKILL.md` (and any child docs) under `.agents/skills/<your-tool>/`. Agents will discover it next to `sundial/SKILL.md`.
- Provide a `<your-tool> setup` command that writes your subtree idempotently, the way `sundial setup` does.

## Calling sundial from another tool

You always talk to the daemon through the CLI. There is no Go library and no stable IPC surface for third parties — the Unix-socket JSON-RPC is an internal detail and may change.

```bash
sundial add at --at "..." --command "..." --json
sundial list --json
sundial show <id> --json
sundial remove <id> --json
```

Every command accepts `--json` and is non-interactive. Exit code `0` = success, `1` = error; errors go to stderr.

If you need to know whether sundial is installed and reachable before issuing a command:

```bash
sundial health --json
```

…which returns `data_repo`, `config`, daemon pid, and `pending_pushes`. Sundial will not be running if launchd has not started it yet; tell the user to run `make start` from the sundial repo.

## The poll trigger contract

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

## The callback pattern (`--detach` + `--refresh`)

If a scheduled command itself calls back into sundial — for instance, a poll callback that re-arms the watcher for another 72h — you will deadlock unless you use both:

- `--detach` on the **outer** add, so the per-schedule mutex releases as soon as the child is spawned. Without this, the mutex is held for the full command duration and the nested `sundial add` inside the callback is rejected with `schedule currently firing`.
- `--refresh` on the **nested** add, so the existing schedule is updated in place instead of collided with by duplicate detection. Upsert keyed on `--name`.

Semantically: `--detach` = "don't wait for exit, no exit code captured, `sundial show` prints `last_fire: … (detached)`." `--refresh` = "update if exists, create otherwise; requires `--name`; resets `CreatedAt` so poll timeouts restart."

Use `--detach` only when the callback logs its outcome elsewhere or re-enters sundial. For any command whose exit code you want recorded, let it run attached.

## Invoking agents as the scheduled command

Sundial does not know what a coding agent is, but the headless CLI of most agents is just a shell command, which means session **resume** works too.

- Codex: `codex exec --yolo --json "<prompt>"` for a new session; `codex exec resume <thread_id> --yolo --json "<prompt>"` to continue.
- Claude Code: `claude --dangerously-skip-permissions -p "<prompt>" --output-format json` for a new session; `claude --resume <session_id> --dangerously-skip-permissions -p "<prompt>" --output-format json` to continue.

### Obtaining the thread / session id

You do not get a session id from thin air — it is **emitted by a prior headless invocation of the agent itself**. Your tool typically runs an agent once in headless mode to kick off the workflow, captures the id from that run's structured output, and then embeds the id in the `--command` it later hands to `sundial add` so a future scheduled invocation can resume the same session.

Each agent emits the id in a slightly different shape. Parse accordingly:

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

Persist the id in your tool's own subtree (e.g. `<data_repo>/<your-tool>/sessions/<entity>.json`) so you can look it up next time you need to resume. Do **not** persist it in sundial's subtree — `sundial/schedules/` is owned by the daemon.

Then feed the id into the scheduled command:

```bash
sundial add at --at "2026-04-24T10:00:00" \
  --command "codex exec resume $thread_id --yolo 'continue where we left off'" \
  --name "resume-$thread_id"
```

A subtle point: the schedule file (and the git commit sundial pushes) now contains the session id as a substring of the command. Treat session ids as ordinary identifiers, not secrets — but if your tool's threads can be resumed by anyone with the id, factor that into whether the data repo should be private. See [`scheduling.md`](scheduling.md) for the full repertoire of scheduling patterns.
