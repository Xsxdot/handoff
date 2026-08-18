# Windows 执行机补齐三个执行器（B128 / B123）设计

日期：2026-08-18
Backlog：B128（claude 与 grok 解禁）、B123（codex 真机补验）
前置：B37（Windows 原生执行机 A 期，已 done）

---

## 1. 目标与非目标

**目标**：让 Windows 执行机上 claude、grok、codex 三个执行器都真机可用，判据是各自跑通完整任务链路。

**非目标**：

- 不做 Windows 的 `procenum`（Toolhelp32 快照）——那是 B122，与本条无关
- 不改 claude / grok / codex 三个 adapter 的业务逻辑，只补平台缝
- 不引入新的 Go 模块

## 2. 现状：三个执行器各自卡在哪

`cmd/agentd.go:330` 有一段写死的拒绝：

```go
if goos == "windows" {
    logger.Warn("本平台不注册部分执行器",
        "skipped", []string{"claude", "grok"},
        "claude_reason", "输入通道（命名管道）与 AF_UNIX 裁决 socket 未实现（B37 第二批）",
        "grok_reason", "taskenv 用 os.Symlink，Windows 上需要特权")
    return ads
}
```

| 执行器 | 卡点 | 性质 |
|---|---|---|
| claude | 输入通道（`in.fifo`）与裁决 socket（`perm.sock`） | 真代码工作量，见 §4、§5 |
| grok | `os.Symlink`（`taskenv.go:160`、`authsync.go:244`） | **部署前置条件，非代码阻断**，见 §6 |
| codex | 无代码卡点，只是从未在 Windows 上跑过 | 纯验收，见 §7 |

## 3. 对 B37 成本清单的两处订正

本条的设计输入是 B37 的成本清单（`specs/2026-08-10-handoff-windows-port-cost.md`）与 B37 设计。两处结论在本轮被实测推翻，**不要沿用旧文档里的那两句**。

### 3.1 「AF_UNIX 裁决 socket 未实现」——已过时，AF_UNIX 在 Windows 上原生可用

成本清单把 AF_UNIX 裁决 socket 列为 claude 在 Windows 上的阻断项之一，也是当初把验收门从 claude 降到 opencode 的一半理由。**实测否掉。**

本地先看支持面：Go 1.26 的 `src/net/unixsock_posix.go` 构建约束是 `//go:build unix || js || wasip1 || windows`（**windows 在内**），`src/syscall/types_windows.go` 定义了 `AF_UNIX = 1`，`syscall_windows.go` 有 `RawSockaddrUnix` / `SockaddrUnix`。

再上真机跑跨进程探针（Windows Server 2025，`GOOS=windows GOARCH=amd64`，源码在会话 scratchpad，未入库）。探针刻意**用独立子进程当客户端**，不是同进程 goroutine——后者证明不了跨进程可用。实际输出：

```text
GOOS=windows GOARCH=amd64
SOCK_PATH C:\Users\administrator\AppData\Local\Temp\b128143107122\perm.sock len 65
LISTEN_OK C:\Users\administrator\AppData\Local\Temp\b128143107122\perm.sock
SOCK_FILE_EXISTS mode=Srw-rw-rw- size=0
CLIENT_OUT DIAL_OK ROUNDTRIP pong:ping
SERVER_GOT ping
SOCK_FILE_REMOVED_ON_CLOSE yes
```

三条可用结论：跨进程 `Listen`/`Dial`/双向收发全通；`Close()` 会自动删除 socket 文件（与 unix 一致，无需额外清理）；socket 文件的 mode 显示为 `Srw-rw-rw-`——**Windows 上没有 POSIX 权限位**，这条直接影响 §5.3 的安全论证。

### 3.2 「shim 建命名管道直接给子进程当 stdin」——不可行，会让 stdin 见 EOF

成本清单写的是「输入通道由 shim 当命名管道服务端」，方向对（服务端归属是承重的，见 §4.1），但**把管道句柄直接给子进程当 stdin 这一步走不通**。

unix 侧 shim 用 `O_RDWR` 打开 FIFO（`shim.go:118`），图的就是**永不 EOF**：agentd 每次投递以 `O_WRONLY|O_NONBLOCK` 打开、写完就关，子进程 stdin 不受影响。

Windows 命名管道没有这个性质：**客户端一断开，服务端侧即 broken pipe**。若子进程直接持有服务端句柄，它会在第一条指令投递完成的瞬间看到 stdin EOF，claude 的 stream-json 输入模式当场结束。症状极难排查——执行者起来了、第一条指令也执行了，然后再也不响应任何后续投递。

解法见 §4.2：匿名管道 + 中继。

## 4. 输入通道设计

### 4.1 服务端归属：必须是 shim

命名管道服务端由 shim 持有，agentd 当客户端。**反过来不行**：agentd 当服务端时，agentd 重启会关闭管道、杀死执行者的 stdin，而「执行者活过 agentd 重启」是 B36 的招牌属性、B37 已在 Windows 真机上验证过（`CREATE_BREAKAWAY_FROM_JOB` + `recovered=1`）。这条与成本清单的结论一致，本轮不变。

### 4.2 数据流

```text
agentd 侧                          shim 侧                      claude 进程
─────────                          ───────                      ───────────
WriteInputChannel(path, data)
  └─ CreateFile(\\.\pipe\...)  ──▶  命名管道服务端
     写一帧、关闭                     （FIRST_PIPE_INSTANCE）
                                        │ 中继 goroutine
                                        │  抄字节
                                        ▼
                                    匿名管道写端 ──────────▶ 匿名管道读端 = stdin
                                    （shim 全程攥着，
                                      故子进程永不见 EOF）
```

匿名管道（`os.Pipe()`）是 unix 侧 `O_RDWR` 的真正等价物：shim 在子进程整个生命周期内持有写端，读端交给子进程当 stdin，因此客户端来去不影响子进程。中继只做单向字节搬运——不解析、不按帧缓冲、不做背压之外的任何加工。claude 的 stream-json 是逐行 JSON，原样抄即可。

### 4.3 三个原语的平台映射

输入通道是**三个**原语，不是两个。第三个（写入）现在在 adapter 里，必须下沉。

| 原语 | unix | Windows |
|---|---|---|
| `CreateInputChannel(path)` | `mkfifo` 0600，并拒绝「已存在但不是命名管道」 | **no-op 返回 nil**。服务端必须由 shim 建（§4.1），agentd 侧无事可做；等待责任全部交给下一个原语 |
| `WaitInputReader(path, timeout)` | 轮询 `O_WRONLY\|O_NONBLOCK` 打开，`ENXIO` = 读端未就绪 | 轮询 `CreateFile` 管道名：`ERROR_FILE_NOT_FOUND` = 未就绪继续等；成功或 `ERROR_PIPE_BUSY` = 就绪。探测成功后立即关闭句柄——中继会把它看成一次「连上又断开的客户端」，因为中继是循环受理的，这一次空连接无害，读到 EOF 后回到下一轮 `ConnectNamedPipe` 即可 |
| `WriteInputChannel(path, data)`（新增） | `O_WRONLY\|O_NONBLOCK` 打开 + 写 + 关 | `CreateFile` + 写 + 关 |

### 4.4 为什么写入必须下沉进 prochost

`WriteInput` 现在在 `internal/executor/claudecode/proc.go:256`：

```go
f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
```

`syscall.O_NONBLOCK` 在 Windows 上**有定义**（`types_windows.go:50`，值 `0x00800`），所以这段**编译得过**——`GOOS=windows go build ./...` 与 `go vet` 的绿灯完全掩盖了它。它会在运行期失败：Windows 上 `in.fifo` 不是文件，`os.OpenFile` 直接 file-not-found。

若不下沉，平台缝就漏进 adapter，而且是编译期看不见的那一种。新增 `prochost.WriteInputChannel(path string, data []byte) error`，adapter 的 `WriteInput` 改为序列化后调它，平台差异继续全部关在 prochost 内。

现有注释里那条承重语义**天然保留**：unix 上「shim 已死时打开立刻失败 = 进程不在」，Windows 上 `CreateFile` 打不开管道名报 `ERROR_FILE_NOT_FOUND`，语义一致，调用方对 `executor.ErrTaskNotRunning` 的包装不用改。

### 4.5 shim 的第二个平台钩子

`shim.go:116` 那段 `spec.InputCh != ""` 的处理硬编码了 `os.OpenFile(spec.InputCh, os.O_RDWR, 0)`，Windows 上没有等价物。抽成平台钩子：

```go
// openInputChannel 准备子进程的 stdin。
// 返回：给 cmd.Stdin 用的读端；cleanup 在子进程退出后调用；error 非 nil 时 shim 放弃拉起。
func openInputChannel(path string) (r io.ReadCloser, cleanup func(), err error)
```

unix 实现返回 `O_RDWR` 打开的 FIFO；Windows 实现建匿名管道、起中继、返回读端。

B37 已经把 shim 从「平台中立」变成「需要一个平台钩子」（Job Object，六原语变七原语）。本轮是同一条路径上的第二个钩子，不是新开的口子。

### 4.6 管道命名与抢占防护

**命名**：由 `InputCh` 的绝对路径确定性推导，形如 `\\.\pipe\handoff-<h>`，其中 `h` 是 `sha256(abs(path))` 的**前 16 个十六进制字符**（即前 8 字节的 hex），全名长度约 24 字符，远低于 Windows 管道名 256 字符上限。确定性是硬要求——agentd 与 shim 必须在不共享额外状态的前提下算出同一个名字，`proc.json` 与三个 adapter 才能零改动。

**抢占防护**：创建时必须带 `FILE_FLAG_FIRST_PIPE_INSTANCE`，并显式设安全描述符只授权当前用户。

命名管道位于**全局命名空间**。不加 `FIRST_PIPE_INSTANCE` 时，任何本机进程都能抢先创建同名管道，之后 agentd 连上去的是它的实例——这条通道直接就是执行者的 stdin，被搭上去等于能给模型下任意指令。加上之后，抢占表现为 shim 创建失败，是可见故障而非静默劫持。

## 5. 裁决 socket 设计

### 5.1 结论：继续用 AF_UNIX，传输代码零改动

基于 §3.1 的实测，`internal/executor/claudecode/perm.go:77` 的 `net.Listen("unix", …)` 与 `cmd/permission_mcp.go:203` 的 `net.Dial("unix", …)` 在 Windows 上原样可用。不引 go-winio，不改用环回 TCP。

**明确否掉环回 TCP + token**：它是三个候选里唯一降低安全属性的——现有边界是「socket 文件在任务目录内，权限即边界」，换成 TCP 后同机任何进程都能连，只剩 token 挡着；而 token 还不能走 argv（B36 定的 argv 保密纪律，B37 真机验收专门查过「口令只在 Env 不在 argv」）。省下的仅仅是一个已在 `go.mod` 里的依赖，不值得。

### 5.2 socket 文件残留

探针实测 `Close()` 后文件已被删除，与 unix 行为一致。无需为 Windows 增加清理逻辑。

### 5.3 安全边界的论证依据要改（含一条待验前置）

`perm.go` 文件头现在写着：

> 为什么用 unix socket 而不是 agentd 的 HTTP 口：被监管的 executor 不该拿到 agentd token；socket 文件落在 0700 的任务目录内，**权限即边界**，且无需分配端口。

Windows 上没有 POSIX 权限位（探针实测 `mode=Srw-rw-rw-`），这句在 Windows 上是**假的**。边界实际由任务目录的 NTFS ACL 继承提供。注释必须改写成分平台表述——这是安全论证，写错的代价不是文档不准确。

**待验前置（本轮未验，不得假设）**：任务目录（`<DataDir>/tasks/<id>/`）的 NTFS ACL 是否真的只授权当前用户与 SYSTEM/Administrators。原计划在 Windows 机器上取 `Get-Acl` 实测，因该机被账户锁定反复打断（见 §9）未能取到。**实现阶段必须先验这一条**：若 ACL 不足以构成边界，需要在创建任务目录时显式设 ACL，那是本条范围内的追加工作。

### 5.4 路径长度

AF_UNIX 的 `sun_path` 上限 108 字节。探针路径 65 字节无问题；真实路径形如 `C:\Users\<user>\.handoff\tasks\<uuid>\perm.sock` 约 86 字节，够但不宽裕。`perm_test.go:171` 已有注释记载 unix 侧踩过这个坑（「长临时路径超 unix sun_path 上限」）。

要求：Windows 上若 DataDir 被配到深路径导致超限，必须给出一条明确指出「socket 路径过长」的错误，而不是把 `net.Listen` 的原始错误直接抛给用户。

## 6. grok：注册期能力探测

改法**不是**简单删掉写死的拒绝——那会把「拒绝」换成「注册了但运行期炸」，比现状更糟。

agentd 启动时在 DataDir 下试建一个符号链接再删掉：成功则注册 grok；失败则不注册，并给出可行动的理由（开启开发者模式，或让 agentd 以管理员身份运行）。

依据：`os.Symlink` 在 Windows 上需要 `SeCreateSymbolicLinkPrivilege`（管理员）或开发者模式。08-18 已在 agentd 真实的 schtasks 上下文（Administrator / S4U / RunLevel=Highest）实测可用——**所以这是部署前置条件，不是代码阻断**。

**明确否掉「把软链换成复制文件」**：软链的意义是 auth 文件只有一份权威副本。改成复制后，grok 在任务里刷新 token 写的是副本，用户那份与任务那份各自漂移，且这种不一致是静默的——正是 B26 那一整类问题。宁可诚实拒绝，不要静默降级。

## 7. codex：纯验收

零代码改动。B123 记的是「adapter 已注册但零真机证据」，凭据已于 08-18 由用户确认就位。

**本节与 §4–§6 互不阻塞**：万一凭据在真机上仍有问题，不得拖住 claude 与 grok 的落地。

## 8. 错误处理与降级语义

### 8.1 `CreateInputChannel` 的 no-op 是有风险的，风险由分工消解

Windows 上它返回 nil 但什么都没做——这类函数把「没问题」和「没检查」混成同一个返回值。缓解办法是把等待责任完全压到 `WaitInputReader`：管道服务端没建起来，它必然超时，而超时路径已有的处置（`StartProc` 自行 `Kill` 回收 shim，见 `proc.go:196-204`）保持不变。

注释里必须写明：**这里的 nil 表示「无事可做」，不表示「已验证」**。

### 8.2 `FIRST_PIPE_INSTANCE` 失败必须在 shim.log 里说清楚

它在 agentd 侧的表现是 `WaitInputReader` 超时，与「shim 起得慢」「claude 没装」外观完全相同。而管道名被抢占是**安全事件**，不能伪装成一次普通超时。

判据：`shim.log` 中有一条明确指出管道名已被占用的 Error，含管道名。

### 8.3 中继失败的取舍（明写，避免被后来人当 bug）

命名管道服务端失效后，任务收不到新指令，但 claude 进程可能正跑在回合中间。

取舍：中继失败先重建管道实例并重试；重试耗尽后打 Error 但**不杀子进程**（回合中间的产出不该被丢弃）。后续 `WriteInputChannel` 打不开管道，走既有的「进程可能已不在」路径。

这个映射并不精确（进程其实还在），但它给出的行动是对的——该任务已无法继续指挥。真实原因去 `shim.log` 找。**这是有意为之，不是缺陷。**

## 9. 测试策略

### 9.1 给 CI 的 windows-latest job 加 `go test`

`.github/workflows/ci.yml` 已有一个 `powershell` job 跑在 `windows-latest` 上（只跑 install.ps1 的 PowerShell 单测）。Go 侧在 Windows 上**一行测试都没跑过**。

命名管道中继、匿名管道不 EOF、`FIRST_PIPE_INSTANCE` 抢占、AF_UNIX 往返——全是运行期行为，在 macOS/Linux 上写多少单测都碰不到。

追加一步：`go test ./internal/prochost/... ./internal/executor/claudecode/...`。成本是已有 job 里的一个 step，收益是这些逻辑每个 PR 被真正执行一次，而不是等到有人去摸那台 Windows 机器。

### 9.2 交叉编译绿灯不是可用性证据

写进 spec 作为纪律：`GOOS=windows go build` 与 `GOOS=windows go vet` 全绿**不能**当成 Windows 可用的证据。本轮亲眼看到 `syscall.O_NONBLOCK` 在 Windows 上有定义、编译全绿、运行期必炸（§4.4）。这两道门只防编译期回归，运行期语义错配唯一的判据是真机跑。

## 10. 真机验收剧本

判据为全链路（用户 08-18 拍板）。

| # | 内容 | 判据 |
|---|---|---|
| 1 | 注册面 | `handoff status` 列出 claude / codex / fake / grok / opencode；agentd 日志中有 grok 的符号链接能力探测记录 |
| 2 | claude 全链路 | dispatch → 权限门拦截产工单 → `reply --approve` 放行 → `completed` |
| 3 | **多轮投递不 EOF** | `continue` 至少两次，每次都被响应 |
| 4 | `deny` 路径 | 拒绝后模型收到理由；目标文件未被改动 |
| 5 | 活过 agentd 重启 | 杀 agentd 并确认其 pid 消失后，shim 与 claude 的 pid 不变地存活；新 agentd 启动日志 `recovered=1` |
| 6 | `done` 零残留 | 进程、managed worktree、任务目录三样都清干净 |
| 7 | grok 全链路 | 同第 2 条，外加确认 auth 软链真的建成（`Get-Item` 看到 ReparsePoint） |
| 8 | codex 全链路（B123） | 五动作走完：dispatch → 权限门 → completed → continue 同会话续接 → done |

**第 3 条是刻意设计的**：单轮投递即使实现错了也可能碰巧通过（第一次客户端连接尚未断开），必须多轮才能证明子进程 stdin 没被 EOF 掉。这是 B37 §12.5 那条教训的同款——验「没坏」必须能把坏的情况真正区分出来；那次第 4 条验收连做三次才有效，前两次都是只断言「幸存者还在」的假 PASS。

**第 5 条同理**：必须先证明 agentd 的 pid 真的消失了，再断言 shim 与 claude 还活着。

## 11. 已知边界

1. **Windows 机器可用性是本条的外部依赖。** 该机（47.80.243.155）在设计期间因 Administrator 账户被公网爆破反复锁定（B127），可用窗口仅约一分钟，导致 §5.3 的 ACL 前置未能实测。实现与验收阶段必须先收安全组暴露面，否则真机验收无法进行。
2. **§5.3 的 NTFS ACL 未验**，是本条唯一带未知的前置，已在该节标注。
3. `procenum` 在 Windows 上仍未实现（B122），与本条无关但会影响 Windows 上的每任务进程计数告警档——不要在本条里顺手做。
4. 本条不覆盖 Linux：三个执行器在 Linux 上本就注册，无平台缝。
