# 拆除 tmux：跨平台进程承载层（prochost + shim）设计

> 日期：2026-08-10
> 状态：已与用户逐节确认，待实现
> 前置讨论结论：tmux 的两个存在理由——①执行者进程活过 agentd 重启 ②人可观测/介入——
> 前者有更便宜且跨平台的替法，后者已由桌面端统一 TUI 接管（复现原生 TUI 已实证不可行）。
> tmux 附带的代价（假存活判据、sh 脚本链、shellq 转义、argv 泄密约束、Windows 阻断）
> 不再值得支付。

## 1. 目标与范围决策

最终目的只有两个，全部保留、承载方式更换：

1. **agentd 重启/升级不影响执行者干活，重启后恢复连接**——由 shim + procinfo 接管；
2. **通过 handoff 看到执行者在干什么、能回复/停止**——观测走 agentd 事件面与新增
   render 流式 endpoint（桌面端与 CLI attach 共用），回复走既有 `reply`，停止走进程组 Kill。

已确认的范围决策（与用户逐项定案）：

- **不保留紧急逃生口**：executor 全是无头协议进程（HTTP/SSE、stream-json、ACP、WS），
  attach 从来只用于看，不用于在会话内敲键盘。兜底 = taskDir 落盘日志 + 手动 kill PID。
  不做 `debug_tmux` 之类的可选包装，tmux 依赖彻底删除。
- **进程层做到平台无关（两步走）**：
  - **A 期（本 spec）**：三个平台原语接口化 + build tag；Unix 实现完整落地并真机验收；
    Windows 侧只留骨架（`not implemented`）。不引 go-winio、不做 Job Object、不搭 Windows CI。
    验收门禁：`GOOS=windows go build ./...` 全绿。
  - **B 期（独立后续项目）**：A 期真机验证通过后，补 Windows 实现
    （命名管道 `\\.\pipe\`、`DETACHED_PROCESS` + Job Object），用户的 Windows 机器真机验收。
    背景：开源项目，Windows 用户基数大，此路不能堵死。
- **attach 语义重写为终端事件流**：从「execve 进 tmux」变成「流式消费 agentd 的
  render endpoint」。命令名与用户心智不变。
- **升级不做旧格式兼容**：尚未发版，挑无任务在跑的时间升级。Reap 只认新格式
  proc.json，读到不认识的内容按错误上报，不保留 tmux kill-session 遗留路径。
- **与桌面端并行开发**：本项目直接在 main 上做；桌面端 plan 01（控制面 + 工作台骨架，
  改动面与本项目几乎不相交）在其分支继续，**定期把 main 合入分支**（不攒到完工才合）。
  可预见冲突点仅 `proto.go` 与 `agentd/server.go` 路由注册。render 流式 endpoint 是
  main 送给桌面端的依赖：桌面 TaskTUI（plan 02+）直接消费，不另造。

## 2. 现状要拆的东西（被替换清单）

| 现状 | 位置 | 去向 |
|---|---|---|
| tmux new-session / has-session / kill-session / new-window | 四个 adapter 的 proc.go | prochost Start/Alive/Kill |
| `#!/bin/sh` 启动脚本（env 注入、tee 落盘、退出哨兵） | `write*Script` × 4 | shim（纯 Go）|
| `in.fifo` + `syscall.Mkfifo` + ENXIO 就绪探测 | claudecode/proc.go | prochost 输入通道原语（unix=FIFO，行为不变；win 骨架）|
| `shellq` 引号转义包 | internal/shellq | 删除（argv 直传，无 shell）|
| tmux 第二窗口 `tail -f render.log` | startRenderTailWindow × n | render 流式 endpoint |
| `handoff attach` 的 `syscall.Exec` + `ssh -t … tmux attach` | cmd/attach.go | 流式 HTTP 客户端 |
| 「tmux 会话在 ≠ 进程活」的假存活判据及各 adapter 的规避 | reap.go、grok probe 等 | 连根消失（无第二窗口吊会话）|
| secret 不能进 argv/tmux -e 的安全约束 | grok/opencode 注释 | 简化：env 由 Go 侧直设 cmd.Env，不落任何脚本文件 |

不变的东西：四个 adapter 的协议层（SSE / stream-json / ACP / WS JSON-RPC）、权限门
（unix socket + MCP）、turn/render.go 的 render.log 落盘、任务状态机、CLI 其余命令。

## 3. 进程承载层：internal/prochost

### 3.1 接口

```go
// Spec: 一次执行者进程的启动描述。argv 直传不经 shell；env 由 Go 侧合并完毕
//（继承 + env 文件 + handoff 注入，protectedEnvKeys 覆盖纪律不变）。
type Spec struct {
    Argv    []string // [0] 是 LookPath 解析后的绝对路径
    Dir     string   // 任务仓库工作目录
    Env     []string // 完整环境
    Stdout  string   // 追加落盘路径（out.jsonl / serve.log）
    Stderr  string   // 追加落盘路径（claude.log / serve 同文件亦可）
    InputCh string   // 可选：输入通道路径（仅 claude 用；unix=FIFO）
}

Start(spec) (Handle, error) // detached 拉起 shim，返回 {PID, StartTime}
Alive(h) bool               // PID 存在 且 启动时间吻合（防 PID 复用误判）
Kill(h) error               // 杀整个进程组（shim 为组长），带启动时间防误杀
CreateInputChannel(path) error // unix: Mkfifo（幂等）；win: not implemented
```

平台实现以 build tag 切分（`prochost_unix.go` / `prochost_windows.go`），
Windows 全部返回 `not implemented`（A 期）。

### 3.2 shim：handoff 二进制自我复用

`Start` 实际拉起 `handoff _shim --spec <taskDir>/spec.json`（隐藏子命令）。shim 职责：

1. `setsid` 成为新会话/进程组组长——脱离 agentd 的进程树与 cgroup
   （systemd `KillMode=control-group` 场景下 agentd 重启不再连坐执行者）；
2. 打开 Stdout/Stderr 落盘文件；InputCh 非空时以 `O_RDWR` 打开 FIFO
   ——复刻 sh 脚本 `exec 3<>` 的「自持读端」手法，agentd 侧 WriteInput 的
   O_WRONLY|O_NONBLOCK + ENXIO 等待逻辑原样保留；
3. spawn 真正的 executor，把 child_pid 补写进 proc.json；
4. `wait` 子进程，退出后把 `{"type":"handoff_exit","code":N}` 哨兵追加到 Stdout 文件。

**为什么必须有 shim（而不是裸 detach）**：退出哨兵现在由 sh 脚本（executor 的父进程）
写入；agentd 重启后，reparent 走的进程无法被 `waitpid`，若无常驻父进程，
「agentd 离线期间 executor 退出」的退出码永远拿不到——这会降级现有语义。
shim 是纯 Go，B 期天然成为 Windows Job Object 的持有者。

### 3.3 四个 adapter 的落点

- **opencode / grok / codex**：无 InputCh（各自有 HTTP/WS 探活面）。只换启动/探活/杀灭
  三件套；协议层探活判据不变（HTTP 端口应答 / WS 可连）。
- **claude**：InputCh = in.fifo，写入语义不变；进程层存活以 shim Handle 判，
  协议层仍以 out.jsonl 哨兵 + 流活性判（现状）。
- `write*Script` × 4 与 shellq 包删除。

## 4. 存活判定、恢复与 Reap

### 4.1 procinfo 统一为 proc.json

现在的 `claude.json` 泛化为四 adapter 统一的 `taskDir/proc.json`：

```json
{ "shim_pid": N, "shim_start": <unix纳秒>, "child_pid": M,
  "port": 0, "session_id": "...", "out_offset": 0 }
```

**写前置（write-ahead）时序**：agentd 先写 proc.json（shim 信息占位）再 `Start`，
shim 起来后补写 child_pid。保证「凡可能存在的进程，proc.json 一定先于它存在」，
Reap 永远有据可查。spawn 失败的残留占位由启动时间校验安全跳过。

### 4.2 两层存活判定

| 层 | 判据 | 消费者 |
|---|---|---|
| 进程层 | prochost.Alive（shim PID + 启动时间吻合）| 恢复时快速筛查、Reap |
| 协议层 | opencode/grok/codex: HTTP/WS 探活；claude: out.jsonl 哨兵 + 流活性 | 恢复后判「能否续用」|

tmux has-session 判据整体消失；「tail -f 吊着会话导致的假存活」问题连根拔掉。

### 4.3 Reap

读 proc.json → 校验 shim_start 与该 PID 当前实际启动时间吻合 → Kill（杀进程组）。
不吻合 = PID 已复用，视为已死直接成功（继承 workspace.go 防误杀纪律：
绝不向已回收的 PID 发 SIGKILL）。proc.json 缺失/无法解析 → 如实报错，不猜。

## 5. attach 流式接口

### 5.1 agentd endpoint

```
GET /tasks/{id}/render?offset=<字节偏移>&follow=1
```

- 从 offset 起吐 `taskDir/render.log` 字节流（chunked）；
- `follow=1`：到尾不关连接，轮询 stat（1s 间隔，不引 fsnotify）追增量；
- 周期心跳注释行防中间设备断连；响应头携带当前文件大小，断线凭已收字节数续传；
- 挂在现有 CLI REST 面（与桌面 `/v1` 控制面并列），鉴权同 token。

render.log 由 turn/render.go 统一落盘，四 adapter 天然全覆盖。

### 5.2 CLI attach 重写

`handoff attach <task>` = 上述 endpoint 的流式客户端：连上、打印 stdout、Ctrl+C 退。
默认从尾部回溯 4KB 开始（跟实况不刷屏），`--all` 从头放。
target 解析不变（显式 --target → 任务记录的 target → 本机）。

顺带消灭：`syscall.Exec`/execve 路径（Windows 审核者最后一个阻断消失）；
`ssh -t … tmux attach`（远程 attach 复用 agentd 连接；config `user` 字段只剩 pull 消费，
sshHostFromTarget 保留但仅 pull 使用）。dispatch 的 osascript 弹 Terminal 行为原样保留，
弹出的窗口跑的仍是 `handoff attach <id>`。

### 5.3 桌面端衔接

此 endpoint 即桌面 TaskTUI 的任务实况数据源。桌面端若需结构化事件而非渲染文本，
在其旁新增，不改此接口。

## 6. 测试与验收

- **单测缝平移**：tmuxKill/tmuxHasSession/startServe 等包级 var 缝 → prochost
  Start/Alive/Kill 三缝注入假承载；断言对象从会话名变为 Handle + 启动时间校验。
- **shim 用真进程测**（继承 start_ordering_test 纪律，不接受 mock）：
  ① setsid 后脱离父进程组；② 父进程（模拟 agentd）死后 shim + 子进程存活；
  ③ 子进程退出后哨兵落盘；④ Kill 连坐整组。
- **新增关键场景**：「agentd 离线期间 executor 退出」——起 shim → 杀模拟 agentd →
  子进程退出 → 断言哨兵已写。这是 shim 存在的根本理由，旧方案从未显式测过。
- **真机端到端**（四 adapter 各一遍，按 B2 七项清单纪律）：dispatch → 权限升级 →
  reply → continue → **任务中途重启 agentd → 恢复续接** → attach 流式实况
  （本机 + 远程）→ stop 验进程组全灭 → done 归档。重启续接是本次风险最高路径，
  一个 adapter 都不能免。
- **Windows 骨架门禁**：`GOOS=windows go build ./...` 全绿，进 CI；
  不做 Windows 运行时测试（B 期用真机做）。

## 7. 范围外 / 后续

- **B 期（Windows 实现）**：独立立项。命名管道、DETACHED_PROCESS + Job Object、
  Windows CI、四 executor CLI 在 Windows 的可用性验证、`loginpath`（$SHELL -l -i）
  的 Windows 等价物。前置条件：A 期四 adapter 真机验收通过。
- 桌面端后续 plan（TaskTUI 等）基于含本项目的 main 开分支，消费 render endpoint。
- `wait --notify`（Windows Toast）与 dispatch 弹终端（wt.exe）的 Windows 体验，
  属 B 期或更晚的体验项，不阻塞任何主流程（现有 GOOS 门禁已优雅降级）。
