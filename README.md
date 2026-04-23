# Sundial

[![License](https://img.shields.io/github/license/fyang0507/sundial)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/fyang0507/sundial?logo=go&logoColor=white)](go.mod)
[![Platform](https://img.shields.io/badge/platform-macOS-silver?logo=apple&logoColor=white)](#install)
[![Agent-native](https://img.shields.io/badge/design-agent--native-8A2BE2)](skills/sundial/SKILL.md)
[![Works with](https://img.shields.io/badge/works%20with-Codex%20%C2%B7%20Claude%20Code-black)](skills/sundial/scheduling.md)

**An agent-native, all-in-one scheduler for macOS.** Cron, solar, poll, and at triggers; a single Go binary; a data repo that travels with your other agent tools.

"Check my inbox every morning at 7am" is the cron a human writes. Sundial is for what comes next:

- *"Every weekday 30 minutes before sunset, check if I have pull my trash bins in from the curb."*
- *"When a reply arrives on this outreach thread — could be 2 minutes, could be 3 days — resume my session and continue the campaign."*
- *"Wake me up at 10am tomorrow, resume the same Claude session I'm in right now, and have it join the standup."*

Sundial schedules shell commands. Because modern coding agents (Codex, Claude Code) ship headless CLIs with `--resume`, those commands can be **future invocations of the agent itself** — fresh sessions *or* resumed ones. An agent using sundial is scheduling its own future self.

## Install

**Prerequisites:** Go 1.21+, macOS, a git repository you want sundial to use as its data repo.

```bash
git clone https://github.com/fyang0507/sundial && cd sundial
cp sundial.config.dev.yaml.example sundial.config.dev.yaml
vim sundial.config.dev.yaml   # set data_repo_path

make start
```

`make start` builds the binary, installs it to `PATH`, runs `sundial setup` against your data repo (writes `.agents/workspace.yaml`, scaffolds `<data_repo>/sundial/config.yaml`, syncs skills), starts the daemon, and registers it with launchd for auto-start on login. The launchd plist wraps the daemon with `caffeinate -i` so it holds a `PreventUserIdleSystemSleep` assertion — otherwise launchd suspends it with the system and fires are missed. Explicit user sleep (closing the lid) still works.

Other targets: `make stop`, `make restart`, `make test`, `make vet`.

## Your first schedule

```bash
# Daily 9am standup via a fresh Codex session
sundial add cron --cron "0 9 * * 1-5" \
  --command 'codex exec --yolo "join the daily standup and summarize it to ~/notes/standup.md"' \
  --name "daily-standup"

# Watch for a reply; fire once when it arrives (up to 72h)
sundial add poll \
  --trigger 'outreach reply-check --contact-id c_abc --since "$SUNDIAL_LAST_FIRED_AT"' \
  --interval 2m --timeout 72h --once --detach \
  --command 'codex exec resume <thread_id> --yolo "reply arrived; continue"' \
  --name "await-reply-c_abc"

sundial list
```

Every command accepts `--json`. Every command is non-interactive. `sundial <command> --help` has inline examples.

## Sundial vs. a heartbeat loop

The default way agents stay "live" today is a heartbeat: wake the model every N minutes, feed it the world state, let it decide whether anything needs doing. That is what OpenClaw runs. It works, but:

- **Tokens burn on no-ops.** Most heartbeats have nothing to do. You still pay for context and a decision on every tick.
- **Event precision is the heartbeat period.** A 5-minute heartbeat cannot fire "when the reply arrives" — it fires "within 5 minutes of whenever the reply arrives", plus another tick to act.
- **Context drifts by tick.** Long heartbeat chains need aggressive pruning or they become cost centers.

Sundial inverts this:

- **Wall-clock triggers** (`cron`, `at`, `solar`) fire at exactly the time you asked for. No intermediate wake-ups, zero token cost between fires.
- **Poll triggers** delegate the "is there anything to do?" question to a *cheap* shell command (a DB query, an HTTP check). The expensive agent only wakes when the check exits `0`, so event-to-agent latency is one poll interval — not a full tick with full context in between.
- **Each firing is a fresh or resumed headless session**, scoped to the actual event. You pay for one invocation per event, not one per tick.

Concretely: watching for an inbound SMS reply. A heartbeat loop wakes the agent every N minutes to ask "any replies yet?"; sundial runs a 30-line `outreach reply-check` script every 2 minutes (no LLM calls) and only invokes the agent when it actually has a reply to hand off.

## Documentation

Two audiences, two doc trees.

**If you use or integrate with sundial** — an agent scheduling events, or an engineer building a tool on top of sundial as infrastructure — the skill tree is your entry point. `sundial setup` syncs it into every data repo so agents can discover it next to the tools they already know.

- [`skills/sundial/SKILL.md`](skills/sundial/SKILL.md) — catalog. Picks your path.
- [`skills/sundial/scheduling.md`](skills/sundial/scheduling.md) — for agents creating schedules. Trigger types, commands, "invoke your future self" with `codex exec resume` / `claude --resume`, diagnostics.
- [`skills/sundial/integrating.md`](skills/sundial/integrating.md) — for engineers building tools on top of sundial: the data-repo contract, poll env vars, the `--detach` + `--refresh` callback pattern, how to ship your own skill alongside sundial's.

**If you work on sundial itself** — adding triggers, tightening the daemon, shipping a release — the contributor docs live in this repo:

- [`CLAUDE.md`](CLAUDE.md) — package map, conventions, extension recipes.
- [`docs/engineering-design.md`](docs/engineering-design.md) — the full design (daemon lifecycle, RPC protocol, file formats, reconciliation).
- [`docs/post-v1.md`](docs/post-v1.md) — roadmap.

## Architecture in one paragraph

Single Go binary, dual mode. Run as `sundial <subcommand>` and it's a CLI client that talks to a long-running daemon over a Unix-domain-socket JSON-RPC. Run as `sundial daemon` and it's the scheduler itself, managed by macOS launchd. **Desired state** (schedule definitions) lives in a shared git-tracked data repo, so schedules are portable and versioned alongside your other agent tooling. **Runtime state** (next fire times, run logs) stays on the local machine and is recomputed from desired state on startup. The daemon is the single writer; the CLI never edits schedule files directly. See [`docs/engineering-design.md`](docs/engineering-design.md) for the long version.

## Status

**v1** — macOS only. Cron, solar, poll, and at triggers. Multi-agent is implicit (any headless shell command works), but skills and examples were originally written with Codex in mind; Claude Code patterns are now documented in [SKILL.md](skills/sundial/SKILL.md). Linux support and first-class session tracking are post-v1 (see [`docs/post-v1.md`](docs/post-v1.md)).
