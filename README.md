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

## Quick Start

## Connecting a Remote Executor Machine

## Remote Executor Machine

## Command Reference

## Task States and Events

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
