# handoff

[English](README.md) | [简体中文](README.zh-CN.md)

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

**Hand an implementation plan to another AI. You just review.**

handoff is a CLI-only, two-role collaboration tool. You — or any coding-agent session: Claude Code, opencode, grok, and codex all work — play the **coordinator**: write the plan, dispatch the task, rule on permissions, review the changes. The **executor** (opencode / Claude Code / grok / codex) does the actual work in its own independent session — on the same machine, or on any dev box you can reach over the network (see "Connecting a Remote Executor Machine").

```
write plan → handoff dispatch → executor works independently
                  ↑                        │
     reply: approve/answer ←── permission gate / question wakes you
                  │                        │
      handoff diff to review ←──────── turn finished
                  │
  not satisfied: continue / satisfied: done → archive
```

**Why not just open a terminal and let the AI run?**

- **Execution is separated from review**: the executor works in its own session, on its own branch (optionally its own worktree). Dangerous operations are stopped at the permission gate and put to you — it moves one step per approval you give.
- **Nothing is lost when you disconnect**: all state and events are persisted in the executor machine's agentd (SQLite). Your session crashes, you close the laptop lid, you switch to another computer — two commands take over the full live state.
- **Remote compute**: write the plan on your laptop, dispatch to an always-on workstation. Code travels through git; changes come back via `handoff pull`.
- **No central anything**: no central server, no hooks, no MCP configuration. Two machines and one direct WebSocket connection.

This workflow is battle-tested on itself: **everything in this project beyond the first milestone was built by Claude Code acting as coordinator, dispatching to opencode through handoff** — the code you are reading is its own output.

## Installation

macOS / Linux (amd64 / arm64):

```bash
curl -fsSL https://handoff.gosuper.dev/install | bash
```

Windows (amd64 / arm64, PowerShell):

```powershell
irm https://handoff.gosuper.dev/install.ps1 | iex
```

**On Windows, handoff can only act as coordinator** — dispatching, reviewing, ruling on
permissions, and `upgrade`-ing remote machines all work, but the machine itself cannot be
an executor machine: the process-hosting layer agentd depends on is not yet implemented on
non-unix platforms (backlog B37). The dispatch target must be a macOS or Linux executor
machine. If you want a Windows box to execute, install WSL2 and set up handoff plus an
executor inside it following the Linux steps (for the coordinator machine to reach WSL2,
forward the agentd port in from the Windows host, or put WSL2 directly on a virtual
network like Tailscale). Also note that `wait --notify` desktop notifications exist only
on macOS; on Windows the wake-up channel is `wait`'s stdout.

The script installs the binary to `~/.local/bin/handoff` (on Windows,
`%LOCALAPPDATA%\Programs\handoff\handoff.exe`) — no sudo, no administrator rights.
`HANDOFF_INSTALL_DIR` changes the directory. Nothing is written until the sha256 checks
out, and the script is safe to re-run. Confirm the install:

```bash
handoff version
```

A first line like `v0.1.0` means a release build; `unknown` means a local `go build`
artifact (`handoff upgrade` will not treat it as a release for comparison).

Building from source requires Go 1.26+ (older toolchains can auto-download it with
`GOTOOLCHAIN=auto`):

```bash
go build -o handoff . && sudo mv handoff /usr/local/bin/
```

## Quick Start (local machine, 5 minutes)

**1. Initialize the configuration.** `handoff init` detects which executors are installed
on this machine and generates `~/.handoff/config.yaml` through a short Q&A (including a
random token). Re-run it any time to change the configuration — each question defaults to
the current value, so pressing Enter all the way through keeps everything as is.

```bash
handoff init
```

**2. Put agentd under service management.** agentd is the resident service (task state
machine + executor lifecycle management). Hand it to launchd / systemd so it restarts on
crash and starts on boot:

```bash
handoff service install
handoff service status
```

If you picked the executor-machine role in `init`, it offers to install the service right
there — answer y and it's done. **An unmanaged agentd does not come back after a reboot**,
and its PATH depends on whichever shell started it — "first dispatch after reboot says the
executor is not installed" is usually exactly this. Once managed, Ctrl-C won't kill it (it
gets pulled right back up); to stop it, use `handoff service uninstall`.

**A machine that only coordinates does not need a local agentd**: dispatch, `wait`,
`reply`, `diff`, and `attach` all talk directly to the target machine's agentd. The first
time you dispatch a new project, the CLI also registers the project locally (for the local
project tree shown by `handoff project ls`); if there is no local agentd, that hop is
skipped automatically with a notice — the dispatch itself is unaffected.

**3. Dispatch your first task.** From your project directory (the work tree must be clean):

```bash
handoff dispatch --prompt "Change the install command in the README to brew"   # small task, no plan file
handoff dispatch --new-worktree plan.md                                        # real plan, executed in its own worktree
```

The first line on stdout is the task JSON; its `.id` is the `<task>` every later command
takes (the full UUID — short ids are not supported).

**4. Wait for events, make the calls.**

```bash
handoff wait <task> --notify              # block until the next event that needs you (desktop notification on macOS)
handoff reply <task> --ticket <id> --approve                                      # grant permission
handoff reply <task> --ticket <id> --deny --reason "no global package installs"    # deny (always give a reason)
handoff reply <task> --ticket <id> --answer "use pgx, not gorm"                    # answer a question
```

**5. Review and wrap up.** After `completed`, the task enters pending review:

```bash
handoff diff <task>                       # git diff + commit list
handoff run <task> go test ./...          # run verification commands inside the task repo
handoff continue <task> "make it 3 retries"   # not satisfied: follow-up in the same session, context preserved
handoff done <task> --note "accepted"         # satisfied: archive and reclaim the executor
```

To watch the executor live at any moment: `handoff attach <task>`.

> When the coordinator is an AI session, none of this needs memorizing: installation
> already set up the handoff skill for all four agents (Claude Code / opencode / grok /
> codex), and the AI drives the whole loop by the discipline written in the skill. One
> capability difference: Claude Code and grok have background-task wake-up, so they can
> keep a long `wait --follow` subscription; opencode and codex don't, and the skill steers
> them to foreground blocking `wait` calls, one turn at a time.

## Connecting a Remote Executor Machine

Coordinator machine and executor machine are joined by one direct WebSocket connection
(the coordinator dials out). The only requirement: **the coordinator machine can reach the
executor machine's agentd port**. Pick a connectivity option by environment:

- **Same LAN / intranet**: connect directly; put the intranet IP in `targets`.
- **Across networks**: use Tailscale, WireGuard, or a similar overlay to pull both
  machines into one virtual network; put the virtual interface IP in `targets`.
- **Cloud relay**: coming soon — two machines that can't share a network will connect
  through a relay.

The executor machine's `listen` has three settings:

- **`127.0.0.1:7777` (default)**: local machine only. Keep the default for local-only use.
- **A single interface IP (e.g. Tailscale's `100.x.y.z:7777`)**: exposes agentd on that
  one interface only — a smaller attack surface than `0.0.0.0`. agentd automatically adds
  an auxiliary `127.0.0.1:<same port>` listener, so local commands always go over
  loopback and don't wobble with the interface's state. Known limitation: while that IP is
  absent (a restart while the overlay tool is down, or booting before it), agentd fails to
  start; under service management, launchd/systemd keeps re-launching it until the IP
  returns and it comes up on its own.
- **`0.0.0.0:7777`**: all interfaces; accepts remote dispatch from any direction.

**Security red line: before exposing agentd on an interface (the latter two settings),
confirm the machine is not directly exposed to the public internet.** agentd is plaintext
HTTP/WS with Bearer-token auth and no TLS: on the public internet the token can be
intercepted in transit, and holding the token equals dispatching arbitrary code execution
on the executor machine. Home/office networks (behind NAT) and virtual overlay networks
are the intended places to run `0.0.0.0`; a cloud host with a public IP should not be an
executor machine at this stage (or firewall the port down to the intranet/overlay
segment) — wait for the cloud relay.

## Remote Executor Machine

Three steps to send work to another machine:

**1. Executor machine**: install handoff, install an executor (e.g. opencode) with its
model credentials configured, then `handoff init` + `handoff service install`.

**2. Pair from your machine**: copy the token from the executor machine's
`~/.handoff/config.yaml` into the `targets` section of the same file on your machine:

```yaml
targets:
  devbox:
    addr: "192.168.x.x:7777"
    token: "<the executor machine's token>"
    user: "<remote ssh username>"    # omit if same as your local username; pull uses it over ssh
```

**3. Dispatch**:

```bash
git push                                  # required before remote dispatch — handoff never ships code; code travels through git
handoff dispatch --target devbox --new-worktree plan.md
```

The first dispatch to a machine registers the project automatically (cloning if needed)
under the executor machine's configured `repo_root` — you never tell handoff where the
code lives. Dispatch branches off your local HEAD as the baseline: an unpushed commit is
rejected with a 400, and uncommitted local changes are stopped with a prompt (pass
`--allow-dirty` if you've confirmed they're unrelated).

When the task ends, the remote task branch syncs back to your local repo automatically
(`sync.auto`; or manually with `handoff pull <task>`) — **fetch only, no merge**. Merging
into the mainline is your review decision.

## Command Reference

| Command | Purpose | Key flags |
|------|------|----------|
| `handoff init` | Detect executors, generate/update config interactively (idempotent) | — |
| `handoff service install\|uninstall\|status` | Put agentd under launchd / systemd management | — |
| `handoff agentd` | Run agentd in the foreground (development/debugging; day-to-day use goes through service) | `--executor=opencode\|claude\|grok\|codex\|fake` (default opencode) |
| `handoff dispatch [plan.md]` | Dispatch a task (project identified by current directory) | `--prompt "<instruction>"` (at least one of this and a plan file); `--target <machine>`; `--executor`/`--model`/`--name`; `--branch\|--new-branch <b>`; `--base <t>`; `--worktree <path>\|--new-worktree`; `--allow-dirty`; `--no-sync-check`; `--no-terminal` |
| `handoff wait <task>` | Block until the next event that needs you | `--follow` (keep subscribing until the task ends); `--notify`; `--timeout <duration>`; `--no-sync` |
| `handoff reply <task>` | Answer a ticket | `--ticket <id>` plus exactly one of `--approve` / `--deny [--reason]` / `--answer "text"` |
| `handoff diff <task>` | git diff + commit list (review material) | `--base <branch>` |
| `handoff fetch <task> <file>` | Read a single file from the task repo | — |
| `handoff run <task> <command...>` | Run a review command inside the task repo (sh -c, 10min timeout) | handoff's own flags must come **before** the task id; everything after it is passed through verbatim |
| `handoff continue <task> "<instruction>"` | Send a follow-up change instruction (requires pending-review state; continues the same session) | — |
| `handoff done <task>` | Archive the task and reclaim the executor (requires pending-review state) | `--note "<note>"` |
| `handoff stop <task>` | Abort (stop the executor, void tickets, task ends failed) | — |
| `handoff tasks` | List all tasks (one JSON per line) | — |
| `handoff show <task>` | Task snapshot (task + pending tickets + recent events) | — |
| `handoff attach [task]` | Follow the task live in the terminal (no argument shows a picker) | `--all` (replay from the start); `--no-follow` |
| `handoff resume <task>` | Recover a stuck task: redeliver undelivered replies / reconcile a lost turn ending | `--force` (force-close to pending review when reconciliation can't decide) |
| `handoff pull <task>` | Sync a remote task branch to the local repo (fetch only, no checkout) | — |
| `handoff project add\|ls\|rm` | Manage project registrations | `--target <machine>`; add takes `--path <existing path>` |
| `handoff reclaim` | Clean up managed worktrees left by terminal-state tasks (branches kept) | — |
| `handoff footprint` | Per-task process usage and this machine's process headroom | — |
| `handoff status` | Is agentd usable, version, active tasks, executor liveness | `--target <name>`; `--json`; exit code 0=usable 1=unreachable |
| `handoff upgrade` | Survey/upgrade this machine and all targets | `--now`; `--target <name>`; `--force`; `--rollback` |
| `handoff version` | Print the version (first line is the bare version, for scripts to compare) | — |
| `handoff skill [install]` | Show/reinstall the embedded AI skill (synced automatically on install and upgrade; normally hands-off) | — |

Global flags: `--agentd http://127.0.0.1:7777` (agentd address), `--target <name>`
(resolve address and token from config), `--config <path>` (default
`~/.handoff/config.yaml`).

## Task States and Events

Task state machine: `pending` → `running` → (`waiting_answer` ⇄ `running`) →
`waiting_review` → archived (`completed`). **A turn that ends in failure also goes to
`waiting_review`** — the executor session is still alive with full context, so you can
retry with `continue`. Only `stop`, or an executor failing to start, lands the task in
`failed` (terminal; re-dispatch to continue). **Both `continue` and `done` require
`waiting_review`**; any other state returns 409. When in doubt, `handoff show` first.

`wait` is woken by these events:

| Event | Meaning | Action |
|------|------|------|
| `permission_request` | executor asks for authorization | `reply --approve` / `--deny --reason` |
| `question` | executor has a requirements question | `reply --answer` |
| `completed` / `failed` | a turn finished / a turn ended in failure | both go to review: `diff` for evidence, then `continue` or `done` |
| `archived` | task archived by done (payload carries the note) | the task is truly over |
| `delivery_failed` | a reply was persisted but never reached the executor | `handoff resume <task>` to redeliver |
| `stalled` | watchdog: no output for a long time | `attach`/`show` to judge long-running vs stuck |

`progress` and approval-chain audit events are stored without waking anyone; they show up
in `show`'s event history.

## Configuration Reference

## Executor Notes

## Upgrading

## Session Recovery

## Troubleshooting

## Uninstall

## Coming Soon

## Documentation

## Links

## License
