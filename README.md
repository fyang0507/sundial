# Sundial

[![License](https://img.shields.io/github/license/fyang0507/sundial)](LICENSE) [![Go Version](https://img.shields.io/github/go-mod/go-version/fyang0507/sundial?logo=go&logoColor=white)](go.mod) [![Platform](https://img.shields.io/badge/platform-macOS-silver?logo=apple&logoColor=white)](#getting-started) [![Agent-native](https://img.shields.io/badge/design-agent--native-8A2BE2)](skills/sundial/SKILL.md) [![Works with](https://img.shields.io/badge/works%20with-Codex%20%C2%B7%20Claude%20Code-black)](skills/sundial/agent-workflows.md)

> **A scheduler for the moments an agent needs to show up.**

Sundial is an agent-native scheduler for macOS. It turns a time or a cheap external signal into an ordinary shell command: run a backup, send a reminder, start a fresh agent session, or resume the one that was waiting for a reply. It is a single Go binary with a long-running local daemon—not a cloud service, and not a loop that spends model tokens merely to notice that time has passed.

An agent can schedule its own future self. A person can still read, inspect, pause, and version every schedule.

## The moments it is for

| You need to act… | Use | What Sundial does |
| --- | --- | --- |
| on a reliable cadence | `cron` | Fires a command from a standard five-field cron expression. |
| relative to the real world | `solar` | Anchors a command to sunrise or sunset at a location. |
| when a condition becomes true | `poll` | Runs a small check on an interval; invokes the expensive command only when the check exits `0`. |
| once, at a particular instant | `at` | Fires at an absolute timestamp, then completes. |

That makes the boundary explicit: let a small program determine whether anything happened; wake the agent only when there is work worth doing.

```bash
# A scheduled agent session: weekdays at 09:00.
sundial add cron --cron "0 9 * * 1-5" \
  --name "daily-brief" \
  --command 'your-agent run "prepare the daily brief"'

# Wait for a reply without waking the agent every two minutes.
sundial add poll \
  --trigger 'check-for-reply --since "$SUNDIAL_LAST_FIRED_AT"' \
  --interval 2m --timeout 72h --once \
  --name "await-reply" \
  --command 'your-agent resume <session-id> "a reply arrived; continue now"'
```

The example commands are deliberately placeholders. Sundial executes the shell command you give it; see [scheduling a headless agent workflow](skills/sundial/agent-workflows.md) before handing an unattended session real authority.

## How it works

```text
you or an agent
      │  sundial add / list / show
      ▼
CLI client ── Unix-domain-socket JSON-RPC ──► Sundial daemon (launchd)
                                                  │
                         ┌────────────────────────┼────────────────────────┐
                         ▼                        ▼                        ▼
                  evaluates a trigger      runs your command         owns schedule state
                  cron · solar · poll · at  through a login shell     and reconciliation
                         │                        │                        │
                         └────────────────────────┴───────────┬────────────┘
                                                               ▼
                                      git-tracked data repo     local runtime state
                                      definitions + Git history next fires + run logs
```

The daemon is the single writer. Schedule-management commands can be run from any directory, but they always ask the running daemon to update the one configured data repo. Schedule definitions live under `sundial/schedules/`, are committed after changes, and are pushed best-effort. Volatile runtime state and logs stay on the machine, so a schedule can travel without pretending that one machine's run history should.

## Getting started

**Requirements:** macOS, Go 1.21+, and a git repository to use as Sundial's data repo.

```bash
git clone https://github.com/fyang0507/sundial && cd sundial
cp sundial.config.yaml.example sundial.config.yaml
$EDITOR sundial.config.yaml  # set data_repo_path to your git repository

make start
sundial health
```

`sundial.config.yaml` lives in the Sundial checkout, not in the data repo. `make start` builds and installs the binary, scaffolds the data repo with Sundial's agent skill, starts the daemon, and registers it with `launchd` for login-time startup. It is safe to rerun.

Start by previewing a schedule; then ask the daemon what it knows:

```bash
sundial add cron --cron "0 9 * * 1-5" \
  --name "daily-brief" \
  --command 'your-command-here' \
  --dry-run

sundial list
sundial show <schedule-id>
```

Every command is non-interactive and supports `--json`. Use `sundial <command> --help` for the exact flag contract.

## Designed for unattended work, with deliberate boundaries

- **Your command is the authority.** Sundial runs a supplied command through a login shell. Treat schedules and their data repo as code: review them, keep the host trusted, and do not put credentials in command strings that will be committed with the schedule definition.
- **A poll is a gate, not a tiny agent.** Keep `--trigger` checks fast, deterministic, and cheap. The main command runs only after an exit status of `0`; `--timeout` bounds how long the poll keeps looking.
- **Definitions travel; runtime stays local.** Schedules are versioned in the data repo. Runtime state and JSONL run logs are local, including failures and next-fire calculation.
- **The daemon protects ordinary idle time.** Its launchd service is wrapped with `caffeinate -i`, which prevents idle sleep while it runs. It cannot wake a Mac that is already asleep; optional `pmset` wake support is off by default and requires an explicit sudoers rule. See [setup](skills/sundial/setup.md#waking-the-mac-for-due-schedules-optional).
- **Time remains inspectable.** Use `list`, `show`, `pause`, `unpause`, and `health` instead of reaching into scheduler files. They combine the portable definition with live daemon state.

## Learn the right layer

**Using or integrating Sundial**

- [Agent skill catalog](skills/sundial/SKILL.md) — the command-agnostic model and trigger overview.
- [Setup](skills/sundial/setup.md) — daemon health, config resolution, data-repo scaffolding, and optional wake support.
- [Scheduling reference](skills/sundial/scheduling.md) — lifecycle, duplicate detection, `--dry-run`, readiness gates, active hours, state inspection, and git sync.
- [Agent workflows](skills/sundial/agent-workflows.md) — fresh versus resumed headless sessions and their operational tradeoffs.

**Contributing to Sundial**

- [Contributor guide](AGENTS.md) — package map, extension points, and repository conventions.
- [Engineering design](docs/engineering-design.md) — daemon lifecycle, RPC protocol, persistence model, and reconciliation.
- [Post-v1 roadmap](docs/post-v1.md) — deliberately deferred work.

```bash
make build   # compile the binary
make test    # run the test suite
make vet     # run static analysis
```

## Scope

Sundial v1 targets macOS and `launchd`. It deliberately schedules commands rather than becoming an agent framework: any headless CLI can be the command, and the scheduler stays responsible for time, state, and observability. It is released under the [MIT License](LICENSE).
