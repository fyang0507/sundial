# Schedule a headless agent workflow

For agents scheduling their own future selves — waking up fresh, resuming *this* session later, or waiting for a condition and then resuming a specific session. This doc covers what's specific to driving an agent CLI; for the `sundial add` syntax that wraps these commands, see [scheduling.md](scheduling.md) and `sundial add <type> --help`. For prerequisites (daemon, data repo), see [setup.md](setup.md).

Because sundial just runs an arbitrary shell string, any headless agent CLI works, including session resume — the same `sundial add` invocation drives Codex or Claude Code. Pick whichever your workflow uses, then hand sundial the agent command as the `--command`.

## Run agents with full local access

Sundial fires commands **unattended** — no human is present to approve a permission prompt or unblock a sandbox. A scheduled agent that pauses for approval just hangs until its timeout and the fire is wasted. So launch headless agents in a full-access mode that can read and write the local workspace and run commands without prompting:

- **Codex** — `--yolo` (alias for `--dangerously-bypass-approvals-and-sandbox`: no approvals, no sandbox). Maximum capability — the agent can touch anything your user can, so use it only on a machine you trust. To stay inside one project, the contained alternative is `--sandbox workspace-write -a never` — writes confined to the workspace, still no prompts.
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

Drop either command into a `sundial add ... --command '<agent command>'` and you've scheduled your future self.

## Choose the model and reasoning level deliberately

A scheduled agent runs **unattended and often on a recurring cadence**, so its cost compounds silently with every fire — and there's no human watching to notice an overpowered model burning tokens on a trivial chore. Do not default to the most capable model at the highest reasoning level just because that's the interactive default. Instead, pick the **most economic model that can actually do the task**, and set its **reasoning/effort level explicitly** to match — never by omission. Both choices are part of writing the `--command`; reason about them the same way you'd reason about the prompt.

Two levers, set them both:

- **Model** — both CLIs take `--model`. Start from the cheap end and only move up if the task genuinely needs it: for Claude Code, `--model claude-haiku-4-5` (or `haiku`) for routine, well-specified chores; `claude-sonnet-4-6` (`sonnet`) for moderate work; reserve `claude-opus-4-8` (`opus`) for genuinely hard reasoning or long-horizon autonomy. Codex takes `--model` the same way.
- **Reasoning / effort** — match thinking depth to the task instead of inheriting the default (`high`). Claude Code: `--effort low|medium|high|xhigh|max` — use `low` or `medium` for mechanical or narrowly-scoped chores and raise it only when the work needs deeper reasoning. Codex: `-c model_reasoning_effort="low"` (`minimal`|`low`|`medium`|`high`).

When you resume a session, the model and effort are per-invocation flags on the resuming command, not inherited from the original session — restate them on every scheduled `--command`.

## Fresh vs. resume

- **Fresh session** — new context, cheap, but you must pass everything the future self needs in the prompt. Good for recurring chores ("triage my inbox") and standalone wake-ups.
- **Resume** — inherits everything from an existing session, so the future prompt can be a short "continue where we left off" instead of re-explaining the task. Good when the future call is a continuation of *this* conversation.

When you resume, name the schedule after yourself (`resume-self`, `resume-<task>`) so a later `--refresh` can find and re-arm it.

## Obtaining and persisting session ids for resume

To resume (`codex exec resume <id>` / `claude --resume <id>`) you need an id, and an id does not come from thin air — it is **emitted by a prior headless invocation of the agent itself**. You can't read your own id mid-session; it's handed to you at launch. A tool driving these sessions typically runs an agent once in headless mode, captures the id from that run's structured output, and embeds it in the `--command` it later hands to `sundial add`.

**Codex** — NDJSON on stdout; first line is `{"type":"thread.started","thread_id":"..."}`:
```bash
out=$(codex exec --yolo --json "<initial prompt>")
thread_id=$(printf '%s\n' "$out" | head -1 | jq -r '.thread_id')
```

**Claude Code** — a single JSON envelope on stdout (`--output-format json`), top-level field `session_id`:
```bash
out=$(claude --dangerously-skip-permissions -p "<initial prompt>" --output-format json)
session_id=$(printf '%s' "$out" | jq -r '.session_id')
```

You can also **pre-mint** a Claude Code id and reuse it: `claude --session-id <uuid> -p "..." --output-format json` starts the session under a `<uuid>` you chose, so you don't need to parse it back out.

Persist the id in your own subtree (e.g. `<data_repo>/<your-tool>/sessions/<entity>.json`) so you can look it up next time. Do **not** persist it in sundial's subtree — `sundial/schedules/` is owned by the daemon.

A subtle point: the schedule file (and the git commit sundial pushes) embeds the session id as a substring of the command. Treat session ids as ordinary identifiers, not secrets — but if your threads can be resumed by anyone with the id, factor that into whether the data repo should be private.

## Before you write the `--command`

- **Always quote the nested prompt.** The command runs under a login shell. Use single quotes on the outer string and escape inner quotes as needed.
- **Write outputs somewhere readable.** The future session has no conversational channel to reach the user. Log to a file, the data repo, or whatever external sink the user already watches.
- **The daemon can't tell "agent exited cleanly" from "agent fell over".** It only sees the shell exit code. If you care about outcome visibility, have the prompt instruct the agent to write a status file.
