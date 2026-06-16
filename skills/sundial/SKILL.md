---
name: sundial
description: Agent-native scheduler. Entry point — directs you to one-time setup and the full scheduling guide, including invoking your own future agent session and building tools on top of sundial.
---

# Sundial

A macOS daemon that fires shell commands on four triggers (cron, solar, poll, at). You talk to it through the `sundial` CLI — every command accepts `--json` and none of them prompt.

This file is a catalog. Pick your path.

## First time?

If `sundial` isn't on your PATH, the daemon isn't running, or you don't yet have a data repo, start at **[setup.md](setup.md)** — one-time initialization.

## Scheduling

Whether you're an agent (or a person) who needs a command to run on a cron, near sunset, when some external condition becomes true, or at a specific future time — including invoking a fresh or resumed session of yourself — or an engineer building a CLI/service on top of sundial:

→ **[scheduling.md](scheduling.md)** — trigger types, commands, flags, workflow, the "invoke your future self" pattern (run agents with full local access; resume your own session), inspecting state, diagnostics, how to give feedback, and a final section on building a tool on top of sundial (shared-data-repo contract, poll env-var contract, the `--detach` + `--refresh` callback pattern, shipping your own skill).
