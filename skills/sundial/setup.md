# Setup

One-time initialization for sundial. Both [scheduling.md](scheduling.md) and [integrating.md](integrating.md) assume the steps here are done. Re-run any step idempotently — nothing here is destructive.

## Verify the daemon is running

Sundial is a long-running macOS daemon. If `which sundial` fails or `sundial health` shows the daemon is not running, start it from the sundial repo:

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

## Scaffolding a new data repo

Run `sundial setup --data-repo <path>` to scaffold a new data repo. It writes `.agents/workspace.yaml` (stamping `tools.sundial.version`), creates `<data_repo>/sundial/config.yaml` from a template, and syncs this skill tree into `<data_repo>/.agents/skills/sundial/`. Idempotent — safe to re-run after upgrades.
