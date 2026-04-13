# Sundial: Post-v1 Work

Collected from deferred decisions and future work in `engineering-design.md` (v1.3). Each item includes context on why it was deferred and what the v1 baseline is.

---

## Agent Integration

### Agent session resume
Resume a prior agent session instead of starting fresh. e.g., Codex `codex resume <session_id>`, Claude Code `--resume`.

**Why deferred:** The integration boundary is fragile — session ID format, failure classification, and portability contracts are not yet stable across agents. v1 is command-agnostic: it schedules shell commands without knowing whether they are agents. Resume can be layered on without changing the core scheduling model.

**v1 baseline:** Commands are fire-and-forget shell strings. No session tracking.

### Multi-agent support
Claude Code (`claude -p`), Hermes, and other agents beyond Codex.

**Why deferred:** Each agent has different invocation patterns, output formats, and failure modes. v1 targets Codex only to keep the scope tight.

**v1 baseline:** Any agent works if invokable as a shell command, but SKILL.md and examples target Codex.

---

## Scheduling & Execution

### Pause / unpause schedules
Allow disabling a schedule temporarily without removing it.

**Why deferred:** Requires a new `paused` status, CLI command, and daemon handling. Not needed for the core add/list/remove/show loop.

**v1 baseline:** Remove and re-add to achieve the same effect.

### `sundial run <id>` — manual one-off execution
Manually fire a schedule outside its trigger time. Useful for testing and debugging.

**Why deferred:** Nice-to-have, not critical for the agent workflow.

**v1 baseline:** Copy the command from `sundial show <id>` and run it manually.

### Additional dynamic triggers
Weather-based ("if rain forecast > 60%"), calendar-based ("30 min before next meeting"), market-based ("after market close").

**Why deferred:** Each trigger type needs its own data source, API key management, and failure handling. v1 covers the two most common patterns: static cron and dynamic solar.

**v1 baseline:** Cron and solar triggers only.

---

## Duplicate Detection

### Fuzzy duplicate detection for `sundial add`
Levenshtein distance, substring matching, or semantic similarity to catch near-duplicate schedule names or commands.

**Why deferred:** The matching algorithm and similarity threshold are undefined. A safety mechanism should not depend on an unspecified "smart" rule. Deterministic matching is implementable and predictable.

**v1 baseline:** Exact name match or exact command match blocks the add with an actionable hint. `--force` overrides.

---

## Observability

### `sundial logs <id>` — view run history from CLI
Surface fire/miss history, exit codes, and stdout previews without reading JSONL files directly.

**Why deferred:** The local log format is stable enough to read manually. A dedicated CLI command is a UX improvement, not a blocker.

**v1 baseline:** Logs at `~/.config/sundial/logs/<id>.jsonl`, readable by agents or users directly.

### Error notification via messaging
Relay errors (non-zero exits, missed fires) to Discord, Slack, email, or other channels so users are notified and can take action.

**Why deferred:** Requires integration with external messaging APIs, credential management, and per-schedule notification config. Out of scope for the core scheduling loop.

**v1 baseline:** Errors logged locally. `sundial show <id>` surfaces missed fires and last exit code.

### Run log export
Export local run logs to the data repo or external systems for audit/sharing.

**Why deferred:** Operational logs are deliberately local-only in v1 to keep git history clean. Export is a post-hoc convenience, not a design constraint.

**v1 baseline:** Logs stay in `~/.config/sundial/logs/`. Not git-tracked.

---

## Portability & Sync

### Periodic reconciliation / file-watch
Automatically detect data repo changes (from another machine or manual edit) without requiring `sundial reload`.

**Why deferred:** Adds complexity (polling interval, file-watch reliability, inotify/FSEvents) for minimal gain on single-machine v1. `sundial reload` provides an explicit, predictable refresh path.

**v1 baseline:** Reconciliation on daemon startup + explicit `sundial reload` RPC.

### Structured export/import for `recreation_command`
Persist a structured argv-style form instead of a shell string, enabling lossless round-trip for commands with nested quoting, shell metacharacters, or literal newlines.

**Why deferred:** The structured JSON desired state is already the canonical portability mechanism. `recreation_command` is a convenience hint. A proper `sundial export/import` format is a larger design task.

**v1 baseline:** `recreation_command` is a best-effort shell string. Portability relies on automatic reconciliation from structured JSON.

### Smarter git conflict resolution
Handle rebases, merge conflicts, or divergent histories in the data repo.

**Why deferred:** v1 does simple commit + best-effort push. Conflict resolution requires understanding the merge semantics of schedule JSON files.

**v1 baseline:** Best-effort push. If push fails, commit stays local and next push includes all pending commits.

---

## Shell Environment

### Interactive login shell investigation
Investigate switching from `/bin/zsh -l -c` (login shell, sources `~/.zprofile` only) to `/bin/zsh -l -i -c` (interactive login shell, sources both `~/.zprofile` and `~/.zshrc`).

**Why deferred:** Interactive shells have side effects (loading completions, printing motd, etc.) that may interfere with headless command execution. The tradeoffs need investigation. Many users configure PATH in `~/.zshrc`, so the current approach may miss tools.

**v1 baseline:** `/bin/zsh -l -c` — login shell only. Users must ensure PATH is configured in `~/.zprofile`. `sundial health` surfaces effective PATH.
