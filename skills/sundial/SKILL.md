---
name: sundial
description: Agent-native scheduler. Entry point — directs you to the right child doc depending on whether you are scheduling events or building a tool that uses sundial as infrastructure.
---

# Sundial

A macOS daemon that fires shell commands on four triggers (cron, solar, poll, at). You talk to it through the `sundial` CLI — every command accepts `--json` and none of them prompt.

This file is a catalog. Pick your path.

## First time?

If `sundial` isn't on your PATH, the daemon isn't running, or you don't yet have a data repo, start at **[setup.md](setup.md)** — one-time initialization, shared by both paths below.

## Pick your path

### I want to schedule things with sundial

You're an agent (or a person) who needs a command to run on a cron, near sunset, when some external condition becomes true, or at a specific future time — including invoking a fresh or resumed session of yourself.

→ **[scheduling.md](scheduling.md)** — trigger types, commands, flags, workflow, the "invoke your future self" pattern, diagnostics, how to give feedback.

### I'm building a tool on top of sundial

You're writing a CLI or service that uses sundial as its scheduling primitive — e.g. an outreach tool that shells out to `sundial add poll`, or an agent runner that arms its own resume via `sundial add at`.

→ **[integrating.md](integrating.md)** — the shared-data-repo contract, the poll env-var contract, the `--detach` + `--refresh` callback pattern, how to ship your own skill alongside sundial's.
