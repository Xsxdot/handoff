<p align="center">
  <img src="docs/assets/handoff-mark.svg" width="128" alt="handoff logo">
</p>

<h1 align="center">handoff</h1>

<p align="center">
  <a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="Apache 2.0 license"></a>
</p>

<p align="center"><strong>Hand an implementation plan to another AI. You just review.</strong></p>

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

On Windows, handoff can act as either a coordinator or an executor machine. The available
executors and the grok symlink capability check are summarized in the Executor Notes below.
`wait --notify` desktop notifications exist only on macOS; on Windows the wake-up channel is
`wait`'s stdout.

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
gets pulled right back up): to make a config change take effect, use `handoff service
restart`; to stop it for a while, use `handoff service stop` (it stays down until
`handoff service start` — a reboot won't bring it back); to remove the management entirely,
use `handoff service uninstall`.

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

### 浏览器控制台

```bash
handoff console                 # 打开系统浏览器（自动换一次性 ticket）
handoff console --print-url     # 只打印兑换 URL，不打开浏览器
handoff sessions                # 列出已建立的浏览器会话
handoff sessions revoke <id>    # 吊销一个会话（手机丢失时用它）
```

**机制**：`console` 用主令牌向 agentd 换一张 **60 秒、一次性**的 ticket，
浏览器打开该 URL 后 agentd 原子消费它，下发一个 httpOnly cookie 会话（默认 30 天，
滑动续期），此后 `/api` 与 `/ws` 全部路由都用这个 cookie。

**长期凭据永远不进 URL**——URL 里只有那张一次性 ticket。

**Host 白名单**：agentd 只接受 Host 为 `127.0.0.1` / `localhost` / `::1` /
配置的 `listen` 地址的请求。放到域名后面时必须配：

```yaml
web:
  allowed_hosts:
    - handoff.example.com
```

不配的表现是**全部请求 403**，agentd 日志里有 `Host 不在白名单`。

**桌面壳接线契约**（壳内零凭据逻辑）：

1. 探测本机 agentd 是否在监听；
2. 执行 `handoff console --print-url`，**stdout 恰好一行，就是 URL**；
3. `loadURL(那一行)`。

壳不读 `config.yaml`、不碰主令牌、不实现任何鉴权代码。会话过期时页面返回 401，
壳重跑第 2、3 步即可，用户无感。

## 配置（~/.handoff/config.yaml）

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

### 节点化工作流

工作流是一串**节点**，每个节点既是看板的一列，也是「卡走到这列时怎么办」的
配置。系统**不预设任何节点类型**——「审阅」「合并」这些语义由下面几个能力
开关组合出来：

| 开关 | 含义 |
|------|------|
| `dispatch` | 进入这一列时派发一个任务 |
| `verdict` | 等回合终态、解析 `handoff-verdict` 块并按结果路由（蕴含 `dispatch`） |
| `carry_card_context` | 把卡上下文（卡号/标题/有效基线/验收判据/附件）拼进 prompt |
| `max_rounds` | 裁决的轮次封顶，到顶转「需要你」 |
| `next` / `on_fail` | 通过/未过分别移到哪一列，**按节点名指向** |
| `human_bases` | 卡的有效基线落在其中时不自动执行，直接转「需要你」 |
| `gate` | 进入这一列的门槛（要求某类附件 / 要求验收判据非空） |

节点用 `template` 引一份派发模板（执行者、目标机、模型、纪律块、prompt 正文），
再用 `override` 覆盖其中单个字段——想让审阅这一列换个执行者，只改这一个节点。

**节点配的是规矩，不是具体要干什么。** 「合并到哪条分支」这种每张卡都不同的
值来自卡本身的**有效基线分支**（子卡自动继承父卡的），由 `carry_card_context`
带进 prompt；节点上的纪律块只规定「合并目标以卡的基线为准，不要越过它碰别的
分支」。

工作流不可变版本化：每次保存都是发布一个新版本，卡钉着建卡时的版本，
老卡完全不受影响。

### Waiting for another task to be archived

When a second session's work depends on a task you are still reviewing, that session needs
one thing only: to know when the task was really archived. `--until-done` is that latch.

```bash
handoff wait <task> --until-done --timeout 3h
```

While it waits it prints nothing — `question`, `permission_request` and `completed` all
pass by silently — and it never advances the coordinator's cursor, so the session actually
reviewing the task still receives every event. Only once you run `handoff done` does the
latch print a single line, the raw `archived` event, and exit 0.

| Exit code | Meaning |
|---:|---|
| `0` | Archived. stdout is the raw `archived` JSON; `payload.note` carries the note from `done` |
| `124` | The total wait budget elapsed and the task is still not archived |
| `1` | The dependency failed, or auth / task id / protocol error |

`--timeout` here is a **total** budget, not an idle one: intermediate frames deliberately
do not extend it, otherwise a task nobody ever archives could keep the latch alive forever.

The latch only wakes you up — it never dispatches the follow-up work. The original task
still needs its own coordinator to answer tickets, review `completed` and archive it
explicitly; do not reach for this instead of a `wait --follow` review subscription.

## Connecting a Remote Executor Machine

Coordinator machine and executor machine are joined by one direct WebSocket connection
(the coordinator dials out). The only requirement: **the coordinator machine can reach the
executor machine's agentd port**. Pick a connectivity option by environment:

- **Same LAN / intranet**: connect directly; put the intranet IP in `targets`.
- **Across networks**: use Tailscale, WireGuard, or a similar overlay to pull both
  machines into one virtual network; put the virtual interface IP in `targets`.
- **Cloud relay**: available when the two machines cannot share a network. The executor
  dials the relay and the coordinator opens HTTP/WS streams through it; the relay forwards
  control metadata and opaque E2E ciphertext, but does not receive the handoff token or
  persist tunnel payloads. Use `wss://` in production.

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
segment) — or use the E2E-encrypted cloud relay described below.

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

完整的 `~/.handoff/config.yaml` 样例：

```yaml
approver:                     # 分级审批链的廉价模型审批者
  executor: opencode          # 空=不启用审批链（权限请求直接升级人工审核者）
  model: cheap/model          # 审批者模型；空=用执行者自身默认
  timeout: 60s                # 单次裁决超时，超时按 escalate（fail-closed）
  blacklist:                  # 自定义黑名单正则；命中即跳过审批者直接升级
    - "kubectl .*delete"
executor:                     # dispatch 未显式指定执行者时的缺省
  default: opencode
  model: ""                   # 缺省模型（dispatch --model 可逐任务覆盖）
terminal:                     # dispatch 成功后的终端弹窗（默认不弹）
  auto: false                 # 置 true 则 darwin 下 osascript 弹 Terminal.app 进实况
sync:                         # 任务终结（failed）或回合失败（turn_failed）后自动同步远程任务分支到本地
  auto: true                  # 关闭后仍可用 handoff pull 手动同步
env:                          # agent 启动时注入的环境变量文件（放 ~/.handoff/env/ 下）
  opencode: dev.env           # 值是纯文件名；未配置的 agent 不注入
  claude: work.env            # 对 claude 执行者同样生效（鉴权/代理等走同一套注入）
repo_root: ""                 # 项目落点根目录；留空则取 <datadir>/repos（首次生成配置时写入本文件）
path_dirs: ["/opt/tools/bin"] # 额外的可执行文件搜索目录；按需才加，不需要就别写这个键
web:                          # 浏览器控制台 Host 白名单
  allowed_hosts:              # 放行域名（回环地址恒在白名单，无需配置）
    - handoff.example.com
```

### Cloud relay configuration

Use a relay target when the coordinator cannot reach the executor's agentd port directly.
The executor's `relay` block uses its register credential; the coordinator's target uses a
separate connect credential. Both sides use the executor token as the E2E key source, so the
relay only sees the control credential and encrypted tunnel traffic.

On the executor:

```yaml
relay:
  url: "wss://relay.example.com/relay"
  credential: "<register credential>"
  node: "devbox"
```

On the coordinator:

```yaml
targets:
  devbox:
    relay: "wss://relay.example.com/relay"
    credential: "<connect credential>"
    node: "devbox"
    token: "<executor token>"
```

`relay` and `addr` are mutually exclusive. Relay mode requires a high-entropy token (the
normal `handoff init` token qualifies), and `handoff pull` uses the Bundle HTTP endpoint
through the tunnel instead of git-over-SSH. `ws://` is supported only for a local relay test.

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

`pull` fetches a git bundle over agentd's HTTP API and fetches it into your local repo —
**it needs neither ssh on the executor machine nor a POSIX login shell on the remote**. If
the remote agentd is too old (no `GET /api/tasks/{id}/bundle`; it returns 404), `pull`
automatically falls back to the old ssh path — that path still needs the executor machine
to be ssh-able with a POSIX login shell (Windows' cmd.exe does not qualify). The client
log states which path this run used.

## Command Reference

| Command | Purpose | Key flags |
|------|------|----------|
| `handoff init` | Detect executors, generate/update config interactively (idempotent) | — |
| `handoff service install\|uninstall\|status` | Put agentd under launchd / systemd management | — |
| `handoff service start\|stop\|restart` | Start / stop / restart the managed agentd (`stop` stays down until `start`) | — |
| `handoff agentd` | Run agentd in the foreground (development/debugging; day-to-day use goes through service) | `--executor=opencode\|claude\|grok\|codex\|fake` (default opencode) |
| `handoff dispatch [plan.md]` | Dispatch a task (project identified by current directory) | `--prompt "<instruction>"` (at least one of this and a plan file); `--target <machine>`; `--executor`/`--model`/`--name`; `--branch\|--new-branch <b>`; `--base <t>`; `--worktree <path>\|--new-worktree`; `--allow-dirty`; `--no-sync-check`; `--no-terminal` |
| `handoff wait <task>` | Block until the next event that needs you (`--until-done` waits silently for the archive instead) | `--follow` (keep subscribing until the task ends); `--until-done` (dependency latch, prints only the `archived` event; mutually exclusive with `--follow`); `--notify`; `--timeout <duration>` (one-shot = total budget, `--follow` = idle budget, `--until-done` = total budget); `--no-sync` |
| `handoff reply <task>` | Answer a ticket | `--ticket <id>` plus exactly one of `--approve` / `--deny [--reason]` / `--answer "text"` |
| `handoff diff <task>` | git diff + commit list (defaults to the task's own base commit, falling back to the repo's default branch) | `--base <rev>` |
| `handoff fetch <task> <file>` | Read a single file from the task repo | — |
| `handoff run <task> <command...>` | Run a review command inside the task repo (sh -c, 10min timeout; one argument is a shell command, multiple arguments are shell-quoted) | handoff's own flags must come **before** the task id |
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
`waiting_review` → archived (`completed`). **A turn that ends in failure (the
`turn_failed` event) also goes to `waiting_review`** — the task is still alive and the
executor session keeps its full context, so you can retry with `continue`. Only `stop`,
a watchdog kill, or the executor no longer being present lands the task in `failed`
(terminal; re-dispatch to continue). **Both `continue` and `done` require
`waiting_review`**; any other state returns 409. When in doubt, `handoff show` first.

`wait` is woken by these events:

| Event | Meaning | Action |
|------|------|------|
| `permission_request` | executor asks for authorization | `reply --approve` / `--deny --reason` |
| `question` | executor has a requirements question | `reply --answer` |
| `completed` | a turn finished | goes to review: `diff` for evidence, then `continue` or `done` |
| `turn_failed` | a turn failed, but the task is still alive in `waiting_review` | goes to review: `diff` for evidence, then `continue` to retry |
| `failed` | terminal: the task ended (`stop`, watchdog kill, executor gone) | the task is over — re-dispatch to continue; `reclaim` cleans up the worktree |
| `archived` | task archived by done (payload carries the note) | the task is truly over |
| `delivery_failed` | a reply was persisted but never reached the executor | `handoff resume <task>` to redeliver |
| `stalled` | watchdog: no output for a long time | `attach`/`show` to judge long-running vs stuck |

`progress` and approval-chain audit events are stored without waking anyone; they show up
in `show`'s event history.

## Configuration Reference (~/.handoff/config.yaml)

Generated on first run (with a random token). Every section except `listen` / `token` may
be omitted:

```yaml
listen: "127.0.0.1:7777"      # three settings: local only (default) / single interface IP (auto-adds a loopback aux listener) / "0.0.0.0:7777" all interfaces (see "Connecting a Remote Executor Machine")
token: "<auto-generated on first run>"   # access token; copy into the coordinator machine's targets section when pairing

executor:                     # default executor when dispatch doesn't specify one
  default: opencode
  model: ""                   # default model (--model overrides per task)

approver:                     # tiered approval chain: a cheap model screens permission requests first
  executor: opencode          # empty = disabled (permission requests escalate straight to a human)
  model: cheap/model
  timeout: 60s                # a ruling timeout is treated as escalation (fail-closed)
  blacklist:                  # a hit skips the approver and escalates straight to a human
    - "kubectl .*delete"

env:                          # env-var files injected when the executor starts (put them in ~/.handoff/env/)
  opencode: dev.env           # value is a bare filename; executors not listed get nothing injected
  codex: codex.env

terminal:
  auto: false                 # true = on macOS, pop a Terminal with the live view after a successful dispatch (--no-terminal per dispatch)

sync:
  auto: true                  # sync the remote task branch back to the local repo when the task ends

repo_root: ""                 # root directory where auto-registered projects land; empty = <datadir>/repos
path_dirs: ["/opt/tools/bin"] # extra executable directories; agentd already merges the login shell PATH and common install dirs — add here only when that's not enough
proxy: ""                     # outbound proxy for handoff itself; empty = honor HTTPS_PROXY etc.
                              # supports http:// https:// socks5:// socks5h://

proc_fence:                   # executor process fence (RLIMIT_NPROC), keeps a runaway fork from taking down the machine
  disabled: false             # escape hatch; leave it alone normally
  reserve_ratio: 0.1          # emergency lane reserved for agentd/sshd
```

**The `proxy` section**: an outbound proxy for **handoff itself**. Its scope is exactly
two things — the update path (release lookup, asset download) and agentd's `git clone` /
`git fetch`. What makes it more practical than setting the `HTTPS_PROXY` environment
variable: agentd is launched by launchd / systemd and **cannot see your terminal's shell
env**, while this machine already has a `config.yaml` anyway.

Three boundaries worth remembering:

- **It does not apply to the coordinator ↔ agentd link.** That's a LAN / overlay /
  loopback address; putting a proxy on it only invents new failure modes.
- **It does not apply to executors.** Executor egress goes through the `env` section
  (next), so the failure domains don't overlap — a dead proxy breaks upgrades, not
  running tasks.
- **git remotes over the SSH protocol don't see it.** git's `http.proxy` only applies to
  `http(s)://` remotes. If the repo that auto-registration needs to clone is
  `git@github.com:...`, switch to the HTTPS address
  (`git config --global url."https://github.com/".insteadOf git@github.com:`).

A bad value (unsupported scheme, missing host) makes agentd **fail at startup with the
reason** — deliberately: the background update check fails silently, so a broken proxy
not caught at startup looks like "nothing ever happens" and can go unnoticed for months.

**The `env` section**: hands executors their proxy, private registry, and other
environment variables. In the config, write a **bare filename** per executor name (as
above, `opencode: dev.env`; executors not listed get nothing injected). The file itself
lives on the **executor machine** under `~/.handoff/env/`, in dotenv format:

```sh
# ~/.handoff/env/dev.env — a leading # is a comment; no inline comments (# is legal inside URLs)
export HTTPS_PROXY=http://127.0.0.1:7890   # the export prefix is optional
GOPROXY=https://goproxy.cn,direct
PATH=${PATH}:/usr/local/go/bin             # ${VAR} expands one level; no expansion inside single quotes
```

The same env file is also injected into the approver (`approver`) — otherwise the proxy
covers only half the picture, and an approver that can't reach the network silently
escalates everything to a human. A missing file or a syntax error **rejects the dispatch**
with the full path and line number; nothing starts half-broken.

**Permission tiers**: permission requests flow through three tiers — static rules (edits
pass, dangerous commands always ask) → the cheap-model approver (when `approver` is
configured; blacklist hits and failed rulings always escalate) → the human coordinator's
`reply`. Three consecutive approver failures disable it automatically; everything then
escalates straight to the human. Within one task, a byte-identical permission request that
a human already approved is reused automatically (recorded as a `permission_reuse` event)
instead of asking again.

Write targets outside the task's workspace — including shell redirect targets such as
`echo x > ~/.zshrc` — always escalate to the human, never to the approver.

## Executor Notes

`--executor` accepts `opencode` (default) / `claude` / `grok` / `codex` / `agy` / `fake` (a
dependency-free scripted demo). Readiness checks and caveats:

Windows 执行机上执行器的现状：

| 执行器 | 状态 | 说明 |
|---|---|---|
| opencode | 可用 | B37 真机验收通过 |
| codex | 可用 | B123 真机验收通过 |
| claude | 可用 | 输入通道走命名管道 + 中继，裁决 socket 走 AF_UNIX（Windows 原生支持） |
| agy | 可用 | 输入通道走命名管道 + 中继，裁决 socket 走 AF_UNIX（PreToolUse hook 动态裁决） |
| grok | 取决于部署形态 | 需要创建符号链接的权限：agentd 以管理员身份运行，或开启开发者模式。agentd 启动时会探测并在日志里说明 |

- **opencode**: install [opencode](https://opencode.ai/go?ref=3AMC8DKNGP) on the executor
  machine with model credentials configured (this is a referral link: sign up through it
  and we each get $5 of credit).
- **claude** (Claude Code): the executor machine is logged in (`claude -p "hi"` produces
  output). The per-task permission policy is a pure policy file with no credentials;
  claude reads credentials from its own user config.
- **grok**: the executor machine is logged in (`grok -p "hi"` produces output). Note that
  grok reads the machine's Claude Code personal config (the allow rules in
  `~/.claude/settings.local.json`). handoff's per-task ask overrides most of it, but an
  operation that is personally allowed and not enumerated by handoff will pass. On
  disconnect, pending permission requests are not re-sent and the task ends `failed`
  (reopen with `continue`).
- **codex**: the executor machine has run `codex login`. Three things to know:
  - Consider cleaning up `~/.codex/AGENTS.md`, `~/.codex/hooks.json`, and the
    `[mcp_servers]` section of `config.toml` — they change executor behavior (agentd
    WARNs at startup). `model` / `sandbox_mode` etc. in `config.toml` need **no**
    cleanup; handoff pins them at the protocol level and wins.
  - The permission model differs: operations inside the workspace (including `rm -rf`)
    are auto-approved by the OS sandbox and never reach the approval chain, and network
    operations pass per its config **without asking anyone** — the same commands on
    claude/grok would go through the approval chain.
  - If the executor machine needs a proxy for egress, codex **must** get its proxy file
    through the `env` section — agentd starts from a non-interactive context and inherits
    none of your shell's proxy variables. The symptom of forgetting is confusing: the
    session comes up, the task shows running, but the model produces not a single token,
    and the only trace is `failed to refresh available models` scrolling in the task
    directory's `serve.log`.
- **agy** (Antigravity CLI): the executor machine has installed and logged into `agy` (`agy -p "hi"` produces output).
  - Permission model: dynamically routes `PreToolUse` hooks in `.agents/hooks.json` to the task's `perm.sock`, ensuring all sensitive tool calls (such as `run_command`) are intercepted and evaluated by Handoff's permission pipeline.
  - Task artifacts: task directory contains `in.fifo` (input channel), `out.jsonl` (event stream), `agy.log` (stderr log), `perm.sock` (permission socket), and `proc.json` (recovery credentials).

## Upgrading

Upgrades are triggered by you; there is no scheduled auto-update. By default **each
executor machine downloads from GitHub itself**: the coordinator looks up the version
once, downloads a few hundred bytes of `checksums.txt`, and sends down the tag and
sha256. A multi-machine upgrade thus moves tens of bytes between coordinator and executor
machines instead of a 20MB binary per machine — over a cloud relay that difference is
decisive.

Integrity is enforced by the **coordinator-supplied** sha256: the executor machine
compares it after downloading, so a poisoned local proxy or mirror is caught on the spot
(the checksum and the asset travel two separate trust paths).

When an executor machine genuinely has no egress, fall back to download-then-push with
`--push`. If the remote agentd is too old to understand self-pull, the fallback to push
is **automatic** — nothing for you to manage.

```bash
handoff upgrade                       # survey: list every machine's version and verdict
handoff upgrade --now                 # upgrade every machine that's behind (remotes first, this machine last)
handoff upgrade --now --target devbox # upgrade one machine
handoff upgrade --rollback            # roll this machine back to <binary>.prev
```

Two safety gates: a machine with active tasks (`running` / `waiting_answer`) refuses to
upgrade by default (`--force` overrides); a machine whose agentd is unmanaged refuses,
and `--force` does **not** override — run `handoff service install` on that machine
first. Upgrades never roll back automatically; the old binary stays at `<path>.prev`.

All three platforms self-update, Windows included — replacement is "rename the old
binary to `.prev`, move the new one in", which is exactly the operation Windows permits
on a running exe.

The CLI checks for a new version in the background at most once a day, prints one line
on stderr if there is one, and never replaces itself.

## Session Recovery

The coordinator holds no state (it all lives in the executor machine's agentd), so a
crashed session, a network drop, and a takeover from another machine are all the same
two moves:

```bash
handoff tasks                      # list all tasks and their states
handoff show <task>                # snapshot: state + unhandled tickets + event history
```

Clear the `pending_tickets`, then act by state: `running` keeps waiting for events,
`waiting_review` goes into review.

### 进程归属的平台差异

handoff 判断「这个进程属于哪个任务」有三条来源：进程组（pgid）、后代名册
（采样得来）、任务标记。前两条对采样时机敏感——工具壳只活一两秒时会漏；
任务标记不依赖时机，但各平台强度不同：

| 平台 | 标记判据 | 边界 |
|---|---|---|
| Linux | 注入的 `HANDOFF_TASK_ID` 环境变量 | 全部任务形态可用；依赖执行者透传环境变量 |
| macOS | 进程的工作目录是否在任务 worktree 内 | **仅 `--new-worktree` 的托管任务**启用；进程 `cd` 出任务目录后脱钩 |
| Windows | 不适用 | 回收由 Job Object 进程容器承担，内核连坐，不需要事后判定 |

macOS 上不带 `--new-worktree` 的任务不启用该判据：那时任务跑在共享主仓里，
用户自己的编辑器与 shell 也在那个目录下，按工作目录归属会把它们一起清掉。

## Troubleshooting

Logs live on the executor machine: the agentd main log is `~/.handoff/agentd.log`
(`HANDOFF_LOG_LEVEL=debug` lowers the level); the task directory
`~/.handoff/tasks/<task-id>/` holds `render.log` (the live model view — what `attach`
reads) and `serve.log` (executor output; the authority for after-the-fact evidence),
among others.

| Symptom | Cause and action |
|------|-----------|
| A command returns 404 "task not found" | You passed a short id. Every command takes the full UUID; get it from `handoff tasks` |
| `continue` / `done` returns 409 | The task is not in pending review. `handoff show` for the real state |
| `reply` returns 502 / you receive `delivery_failed` | The ruling was persisted but the executor never got it. `handoff resume <task>` redelivers idempotently |
| Task frozen at `running` but `attach` shows the model finished long ago | The turn ending was lost in a disconnect window. `handoff resume <task>` reconciles it back; if it can't decide, add `--force` to close out (keeps the session — unlike `stop`, which kills it) |
| `wait` never returns | Usually there is simply no event yet (normal); reconnect logs on stderr are normal too. Unattended, add `--timeout` |
| `wait` errors out immediately | 401 = tokens differ between the two machines; 1008 = the task id doesn't exist. Fix it first — retrying won't heal it |
| Commands for a remote task report "task not found" | You forgot `--target`, so the command hit the local agentd. Check `addr=` on stderr |
| `dispatch` reports a dirty work tree | The task repo has uncommitted changes; commit or stash (dirty changes would contaminate the task branch) |
| `dispatch` returns 400 "baseline commit does not exist in the task repo" | Your local commit isn't pushed. `git push` and retry |
| Dispatch says the executor is `not found in $PATH`, but it runs fine in your terminal | agentd's PATH differs from your terminal's. At startup it auto-completes the PATH (login shell + common install dirs) — search the log for the PATH-completion line; if that still doesn't cover it, add the directory to `path_dirs` and restart agentd |
| The executor reports `resource temporarily unavailable` | You hit the process fence (the anti-runaway-fork guard), not a code bug. Reduce parallelism; details are in the task directory's `shim.log` |
| agentd won't start, data directory locked | An agentd is already running. Reuse it; to upgrade, stop the old one before starting the new |
| `upgrade --now` fails or hangs | The default is executor-machine self-pull, so the cause is on the remote side. `handoff status --target <name>` and read `update.pull_state`: `stage` is how far it got, `error` is the verbatim reason (proxy unreachable / sha256 mismatch / self-check failed). If the machine has no egress, use `handoff upgrade --now --push` |

**macOS: after downloading from the Releases page, "cannot be opened because the developer cannot be verified"**

The published darwin assets are Developer ID-signed and notarized, but a bare
command-line tool cannot carry an embedded notarization ticket (Apple's stapler only
supports .app / .dmg / .pkg), so the first run needs the network to let the system check
with Apple. Offline, it gets blocked. Fix: retry online, or strip the quarantine flag by
hand:

```bash
xattr -d com.apple.quarantine ~/.local/bin/handoff
```

Installing via `curl | bash` never hits this — curl doesn't set the quarantine flag.

**Windows: "Windows protected your PC"**

The Windows binaries carry no Authenticode signature (that requires purchasing an OV/EV
certificate), so SmartScreen flags an unknown publisher. Fix: click "More info" → "Run
anyway". You can verify the download's sha256 against `checksums.txt` first.

## Uninstall

```bash
handoff service uninstall
rm ~/.local/bin/handoff
rm -rf ~/.handoff        # includes config, task data and logs — delete only once you're sure
```

## Roadmap

- **Desktop app**: view and operate tasks in a GUI, not just the CLI.

## Documentation

- Design doc — architecture, protocol, error handling (Chinese):
  [docs/superpowers/specs/2026-08-07-handoff-design.md](docs/superpowers/specs/2026-08-07-handoff-design.md)
- The handoff skill for AI coordinators (Chinese):
  [skills/handoff/SKILL.md](skills/handoff/SKILL.md) — the skill is embedded in the
  binary and synced to every local agent on install and upgrade, so versions never drift
- systemd manual deployment template:
  [deploy/handoff-agentd.service](deploy/handoff-agentd.service) (note that
  `KillMode=process` in the template is a hard requirement: it guarantees restarting
  agentd doesn't kill running executors)
- Contributing — how to run locally, which gates to pass before submitting (Chinese):
  [CONTRIBUTING.md](CONTRIBUTING.md)
- Security policy and threat model (Chinese; **report vulnerabilities through the private
  channel, not a public issue**): [SECURITY.md](SECURITY.md)

## Community

Scan the QR code to join the **Handoff Coding** WeChat group:

<p align="center">
  <img src="docs/assets/wechat-group.jpg" width="280" alt="Handoff Coding WeChat group QR code">
</p>

WeChat group QR codes expire after 7 days. If it no longer scans, open an
[issue](https://github.com/Xsxdot/handoff/issues) and we'll refresh it.

## Links

- [Linux Do](https://linux.do) — a sincere, friendly, united, professional developer
  community
- [opencode](https://opencode.ai/go?ref=3AMC8DKNGP) — handoff's default executor; this is
  a referral link, sign up through it and we each get $5 of credit

## License

[Apache License 2.0](LICENSE)
