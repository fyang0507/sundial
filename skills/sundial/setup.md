# Setup

One-time initialization for sundial. [scheduling.md](scheduling.md) assumes the steps here are done. Re-run any step idempotently — nothing here is destructive.

## Verify the daemon is running

Sundial is a long-running macOS daemon. If `which sundial` fails or `sundial health` shows the daemon is not running, start it from the sundial repo:

```bash
cd <sundial-repo> && make start
```

This builds, installs, scaffolds the data repo (workspace marker and Sundial agent skill symlink), starts the daemon, and registers it with launchd (auto-start on login, wrapped with `caffeinate -i` so launchd doesn't suspend the daemon when the system idle-sleeps). The launchd plist runs `sundial daemon --config <abspath>`, where `<abspath>` is the absolute path of `sundial.config.yaml` in the sundial repo. Once running, all `sundial` commands work from any directory.

`sundial health --json` reports the resolved `data_repo`, the `config` path, the daemon pid, and any `pending_pushes`. Use it to confirm which data repo the running daemon is attached to.

## Config file and data repo

Sundial reads a single config file, `sundial.config.yaml`, that lives in the **sundial repo root** (not the data repo). It holds `data_repo_path` plus all `daemon:`/`state:` options. The daemon locates it in this order:

1. `--config <abspath>` flag (what the launchd plist uses)
2. `SUNDIAL_CONFIG` environment variable
3. `./sundial.config.yaml` in the current directory
4. `sundial.config.yaml` next to the running binary

`data_repo_path` from that file points at the shared **data repo** — the same git repo used by other agent tooling, holding only schedules, `.agents/workspace.yaml`, and agent skill symlinks. Relative values are resolved from the config file, so sibling checkouts can use `../fred-agent` and remain portable across Drive mount points. Override the path with the `SUNDIAL_DATA_REPO` environment variable or the `--data-repo` flag.

## Scaffolding a new data repo

Run `sundial setup --data-repo <path>` to scaffold a new data repo. It writes `.agents/workspace.yaml` (stamping `tools.sundial.version`) and installs `<data_repo>/.agents/skills/sundial/` as an agent skill symlink to this skill tree. No config is written into the data repo — daemon options live in `<sundial-repo>/sundial.config.yaml`. Idempotent — safe to re-run after upgrades.

## Waking the Mac for due schedules (optional)

The daemon is wrapped in `caffeinate -i`, which prevents *idle* sleep while it runs — but it cannot **wake** a Mac that has already slept (lid closed, scheduled sleep, etc.). A schedule due during sleep fires late, when the machine next wakes. If you need fires to happen on time even while the Mac is asleep, enable pmset wake management.

It is **opt-in and system-wide** (one global toggle, not per-schedule). When enabled, the daemon manages a single macOS `pmset` wake event for the soonest fire across all active schedules, scheduled `lead_time` (default `3m`) before the fire so the machine has resumed by the time it's due.

`pmset schedule` requires root, so the daemon runs it via `sudo -n` (non-interactive). Sundial **never edits sudoers for you** — you install a passwordless rule deliberately:

1. Run `sundial health`. When wake is enabled but the rule is missing, it prints the exact line under `wake` → `fix`, e.g.:

   ```
   <your-username> ALL=(root) NOPASSWD: /usr/bin/pmset
   ```

2. Install it. The clean way is a dedicated file in `/etc/sudoers.d/` (requires admin):

   ```bash
   sudo visudo -f /etc/sudoers.d/sundial-pmset
   # paste the line sundial printed, save
   ```

3. Enable wake in `<sundial-repo>/sundial.config.yaml` and restart the daemon:

   ```yaml
   daemon:
     wake:
       enabled: true
       lead_time: "3m"
   ```

4. Verify with `sundial health`. The `wake:` line reports one of four states, so don't assume anything but the first means failure:
   - `enabled (next wake ...)` — working; the time is local, matching `pmset -g sched`, which also lists the `wakeorpoweron` event.
   - `enabled (inactive: sudoers not configured)` — the NOPASSWD rule from step 1 is missing or wrong; health prints the exact `fix:` line to install. (This is what you'll see *before* step 2.)
   - `enabled (inactive: pmset unavailable)` — `pmset` couldn't be run on this host.
   - `enabled (no upcoming fire)` — wake is configured correctly but no active schedule is due yet, so there's nothing to wake for. Add a schedule and re-check.

Disabling / tearing down: the daemon deliberately leaves its wake event in place when it stops (so a normal sleep/launchd-suspend still wakes the machine and relaunches the daemon). If you permanently disable wake (`enabled: false` + restart) or uninstall sundial without a later restart to reconcile, one already-scheduled `wakeorpoweron` event can linger and wake/power-on the machine once at the old time. Check with `pmset -g sched` and clear a stray event with `sudo pmset schedule cancel wakeorpoweron "<MM/dd/yy HH:mm:ss>"`.

Security note: this grants passwordless `pmset` (which can change power settings broadly), affects the whole machine, and requires admin to install — which is exactly why it is off by default. If the rule is absent or pmset is unavailable, the daemon disables wake management gracefully and never breaks scheduling; `sundial health` surfaces the state.
