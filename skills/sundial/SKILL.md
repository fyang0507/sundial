---
name: sundial
description: Agent-native scheduler. Entry point — directs you to the right child doc depending on whether you are scheduling events or building a tool that uses sundial as infrastructure.
---

# Sundial

A macOS daemon that fires shell commands on four triggers (cron, solar, poll, at). You talk to it through the `sundial` CLI — every command accepts `--json` and none of them prompt.

This file is a catalog. Pick your path.

## Pick your path

### I want to schedule things with sundial

You're an agent (or a person) who needs a command to run on a cron, near sunset, when some external condition becomes true, or at a specific future time — including invoking a fresh or resumed session of yourself.

→ **[scheduling.md](scheduling.md)** — trigger types, commands, flags, workflow, the "invoke your future self" pattern, diagnostics, how to give feedback.

### I'm building a tool on top of sundial

You're writing a CLI or service that uses sundial as its scheduling primitive — e.g. an outreach tool that shells out to `sundial add poll`, or an agent runner that arms its own resume via `sundial add at`.

→ **[integrating.md](integrating.md)** — the shared-data-repo contract, the poll env-var contract, the `--detach` + `--refresh` callback pattern, how to ship your own skill alongside sundial's.

## Setup (both audiences)

If `which sundial` fails or `sundial health` shows the daemon is not running, start it from the sundial repo:

```bash
cd <sundial-repo> && make start
```

This builds, installs, scaffolds the data repo (workspace marker, sundial config, skills sync), starts the daemon, and registers it with launchd (auto-start on login, wrapped with `caffeinate -i` so launchd doesn't suspend the daemon when the system idle-sleeps). Once running, all `sundial` commands work from any directory.

`sundial health --json` reports the resolved `data_repo`, the `config` path, the daemon pid, and any `pending_pushes`. Use it to confirm which data repo the running daemon is attached to.

## Data repo resolution

Sundial stores schedules and its config inside a shared **data repo** — the same git repo used by other agent tooling. The CLI resolves the data repo in this order:

1. `SUNDIAL_DATA_REPO` environment variable (explicit override)
2. `sundial.config.dev.yaml` next to the running binary (dev-local pointer)
3. Walk up from cwd for `.agents/workspace.yaml`

Run `sundial setup --data-repo <path>` to scaffold a new data repo: it writes `.agents/workspace.yaml` (stamping `tools.sundial.version`), creates `<data_repo>/sundial/config.yaml` from a template, and syncs this skill tree (SKILL.md, scheduling.md, integrating.md) into `<data_repo>/.agents/skills/sundial/`. Idempotent.
