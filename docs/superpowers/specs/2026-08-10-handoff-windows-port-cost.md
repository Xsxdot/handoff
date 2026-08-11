# handoff 的 Windows 移植成本清单

**状态：已评估 · 暂不做**（2026-08-10）

这份文档不是实现 spec，是一份**决策记录 + 成本清单**。B37（prochost Windows 实现）
经过一次真机探路测试和一次全仓库静态扫描后决定暂不实施，本文档保存那两轮调查的
全部结论，以及已经推导完成的设计取舍——这样将来真有 Windows 用户出现时，B37 是一
条**已定型、可直接进 writing-plans** 的活，而不用重新发现一遍。

- **不做的理由**：项目作者只在 macOS 与 Linux 上使用 handoff，目前没有已知的
  Windows 用户；而下面第二节的成本清单是**扫出来的而不是估出来的**，因此可信。
  为零个用户支付这份成本不划算。
- **现状是诚实的终态**：`internal/prochost/platform_other.go` 的进程原语一律返回
  `errNotImplemented`（大声失败，而非静默半残），派发时 CLI 侧会看到
  `500 启动 executor 失败: prochost: 本平台的进程承载尚未实现`。不做 B37 不留隐患。
- **顺带落地的**：第六节那一处与 Windows 无关、在 macOS 上就有价值的小改动，已在
  本分支实现。

---

## 一、探路测试：真机实证结论（零代码改动）

平台：Windows（局域网 `192.168.0.84`），`handoff.exe` 由 `GOOS=windows GOARCH=amd64`
交叉编译自 `fea0fef5`，21.9MB。**能交叉编译本身要归功于 `modernc.org/sqlite` 是
纯 Go 驱动、不需要 cgo。**

方法上有一条纪律值得记下来：全程用「一次粘贴即可跑完并自动回传结果」的 PowerShell
脚本，而不是给操作者发多步指令；每一步的结论都由本机对照实验交叉验证，而不是采信
远端脚本的自述。

### 1.1 无需任何改动即可工作的部分

agentd 在原生 Windows 上**基本可用**，逐项实测：

- 配置：`C:\Users\<user>\.handoff` 自动创建，严格 YAML 解析正常
- SQLite 库创建、启动期恢复扫描、看门狗
- HTTP 服务 + Bearer 鉴权 + WebSocket 事件流
- 五个 executor 全部注册成功
- `handoff status --target`
- **`handoff dispatch --executor fake` 完整成功**：Windows 路径上的
  `git worktree add`、分支创建、任务目录、plan 落盘、入库、状态机流转，
  与 macOS 对照实例**逐字段一致**
- `handoff diff`
- `handoff stop`，含 `git worktree remove`（注意：这是在没有活的执行者进程时；
  见第二节 E2）
- `handoff attach` **可用**（B36 拆掉 tmux 后它已是纯 HTTP 客户端，不依赖 ssh，
  见 `cmd/attach.go:107`）。这一条对「Windows 机器上没开 sshd」的场景很关键。

### 1.2 三堵运行期的墙

1. **prochost 原语未实现** —— `500 启动 executor 失败: prochost: 本平台的进程承载尚未实现`。
   这是 B37 原定 scope 内的那一堵。
2. **`handoff run` 的 `sh -c`** —— `exec: "sh": executable file not found in %PATH%`
   （`internal/agentd/workspace.go:802`）。**不在** B37 原定 scope 里。
3. **`exec.LookPath` 只凭名字解析** —— 桌面 GUI 的 `OpenCode.exe` 被解析成
   `opencode` CLI。**不在** B37 原定 scope 里。根因不是大小写，而是「只凭名字
   无法区分 GUI 与 CLI」。

### 1.3 探路对 B37 优先级的两处订正

- **`LockFileEx` 与命名管道同等重要，不是次要项。** 存活锁是 `prochost.Alive()`
  的**唯一**判据（`internal/prochost/prochost.go:77`）；而 `platform_other.go` 的
  `flockExclusiveNB` 直接返回 nil、`lockSupported = false`，导致 Windows 上
  `Alive()` 恒为 false，**且同一 DataDir 的单实例保护完全不存在**——探路时不得不
  在每个脚本开头手工 `Stop-Process` 旧的 agentd，否则两个 agentd 会同时起来抢同
  一份状态。
- **`loginpath` 的 Windows 等价物有实证依据，不是锦上添花。**
  `internal/agentd/loginpath.go:47` 靠 `$SHELL -l -i -c` 取登录 PATH，Windows 上没
  有 `$SHELL`，整段降级为一条 Warn。**这条真机咬过一次**：探路中我据此误判「这台
  机器没装 git」，实际 git 2.55.0.windows.3 装着，只是不在 agentd 那个进程的 PATH
  上——从注册表刷新 PATH 后就找到了。后果不是「少个便利功能」，而是从图形界面或
  服务方式启动的 agentd 大概率找不到 git 和执行器 CLI。

### 1.4 探路当时未能覆盖的

那台 Windows 上**没有任何一个可用的 executor CLI**（opencode 只装了桌面 GUI）。
这直接决定了 B37 的验收门必须先在 Windows 上装 claude CLI 并配好凭据——否则无法做
端到端验收，而不端到端验收就违背本项目「每个 adapter 都真机端到端」的纪律（这也正
是 A 期把 B37 推后的原文理由）。**这道门本身就是 B37 成本的一部分。**

---

## 二、静态扫描：完整成本清单

对全仓库 non-test 代码做了一次 Windows 兼容性排查（硬编码 unix 路径、外部 unix 命令、
unix 专属文件系统语义、裸 syscall、信号、存活探测）。结果分三档。

### 2.1 挡住验收门（必须做，且都不在 B37 原定 scope 里）

**E1. claude 的裁决 socket 走 AF_UNIX，而且是 Start 的第一步。**

- `internal/executor/claudecode/adapter.go:192` 是 claude 启动的**步骤 1**，注释写明
  「裁决 socket 必须先于 claude 进程存在」，`err != nil` 直接 `return err`。
  socket 建不起来，claude executor 根本起不来。
- 服务端 `internal/executor/claudecode/perm.go:77` `net.Listen("unix", sockPath)`；
  客户端 `cmd/permission_mcp.go:203` `net.Dial("unix", ...)`。
- Go 自 1.12 起在 Windows 10 1803+ 支持 AF_UNIX，所以**可能**能跑，但未验证。
  更糟的是 `perm.go:16` 的文件头写着「socket 落在 0700 的任务目录内，**权限即边界**」
  ——Windows 上 `Chmod` 只能翻 read-only 位，这条安全不变量**静默失效**。
- 结论：Windows 上应换成带 DACL 的命名管道（正好复用输入通道那套管道代码）。
  另三个 executor 走 TCP loopback，那在 Windows 上是同机任意本地用户可连，不能当参照。

**E2. `git worktree remove` 删不掉执行者的 cwd。**

- `internal/agentd/workspace.go:450`；三个调用点
  `manager.go:869`（done）/ `:962`（stop）/ `:625`（派发失败补偿）都紧跟 kill 之后。
- Windows 上进程的当前目录本身就是一个不带 `FILE_SHARE_DELETE` 的目录句柄，而执行者
  的 cwd 正是这棵 worktree；`prochost.Kill` 又是异步的。
- 后果：**每个任务留一棵残留 worktree**，同名重试撞 `already exists`。三处都是
  「失败只打日志 + 发 progress 事件，不阻断」，所以不崩，但 e2e 的收尾必然脏。
- 修法：kill 后等进程真正退出再删。

### 2.2 与已知项捆绑的

- **`RunCmd` 在 Windows 上不建进程组也不按组回收**。
  `internal/agentd/workspace_procgroup_other.go` 是空实现（unix 侧
  `workspace_procgroup_unix.go:18,27` 用 `Setpgid` + `Kill(-pid)`）。10 分钟超时或
  HTTP 请求断开时，`CommandContext` 只杀顶层进程，**孙进程全变孤儿留在机器上**；
  `workspace.go:821-830` 那段「不重复补杀」的逻辑在 Windows 上等于空转。
  与 `sh -c` 是同一段代码，应一起做。
- **第五处「按名字解析可执行文件」**：`internal/agentd/approver.go:119` 的
  `argv[0]` 来自 `internal/executor/oneshot.go`，是裸名字 `"opencode"` / `"claude"` /
  `"grok"`。Windows 上这些 CLI 常是 npm 生成的 `.cmd`/`.ps1` shim，Go 的 `exec` 对
  批处理文件有额外的引号与 `PATHEXT` 约束。失败会走 escalate 分支
  （`approver.go:110-117`），即**每次自动审批都退化为唤醒人工审核者**。

### 2.3 被已定设计白捡修好的

`internal/executor/claudecode/proc.go:238` 是全仓库**唯一一处没做平台切分的裸
`syscall.O_NONBLOCK`**。它能编译（Windows 的 `syscall` 也定义了这个常量），但标志会
被 `os.OpenFile` 静默忽略，于是函数注释依赖的「shim 已死则打开立刻失败（ENXIO）」在
Windows 上不存在，`ErrTaskNotRunning` 的判据（`adapter.go:298`）失效。

第三节的「shim 当管道服务端」方案里，`Send` 变成「按名字连管道」——**连不上就等于
shim 已死**，正好是想要的语义，无需额外工作。

（对照：agentd 包为同一件事专门做了平台切分，见 `opennonblock_unix.go` /
`opennonblock_other.go`。claudecode 这一处是漏的。）

### 2.4 建议记为已知限制、即使做 B37 也不修

- **grok 在 Windows 上不可用**：`internal/executor/grok/taskenv.go:160` 用
  `os.Symlink` 把任务级 `auth.json` 指向真实 `~/.grok/auth.json`，失败即
  `return err`，executor 起不来。Windows 上 `os.Symlink` 需要
  `SeCreateSymbolicLinkPrivilege`（管理员或开启开发者模式）。相关还有
  `authsync.go:244,250` 与 `authsync.go:159,165`（junction 报 `ModeIrregular` 而非
  `ModeSymlink`，判定恒假 → 每轮都走完整收编分支）。
- **`handoff pull` 对 Windows 执行机不可用**：`cmd/pull.go:74` 拼 scp 风格远程地址，
  `task.RepoPath` 形如 `C:\Users\x\repo` 时 git 会把第二个冒号当分隔符解析。且那台
  机器本来就没开 sshd。审阅取证仍可走 `diff` / `fetch` / `attach`。
- **`killmode.go` 在 Windows 上是死代码**：`internal/agentd/killmode.go:28` 读
  `/proc/self/cgroup`，读失败 → `WarnIfKillModeUnsafe` 静默降级，systemd KillMode
  自检恒定无效。不崩，且 Windows 上本来没有 systemd。
- **macOS 专属的通知与自动弹终端**：`cmd/wait.go:159` / `cmd/dispatch.go:228` 已有
  `runtime.GOOS != "darwin"` 门禁，Windows 上退化为不通知、只打印一行提示。
- **权限位在 Windows 上普遍无效**：`0o700` 目录（DataDir、配置目录、cursor 目录、
  worktrees 根、taskDir、grok 任务级 HOME）与 `0o600` 文件（`spec.json` 含完整 env、
  `config.yaml` 含 token、各 executor 的 `auth.json` / `settings.json` / `mcp.json`）
  在 Windows 上都退化为继承父目录 ACL，同机其他本地用户可读。这是一整类问题，真要
  做的话应当在 DataDir 一层用一次显式 ACL 收口，而不是逐点修。

### 2.5 测试面（独立一档）

`internal/prochost/windows_build_test.go:26` 已有 `GOOS=windows go build ./...` 的
编译门禁，**所以整个模块在 Windows 上能编译**，上面所有问题都是运行期行为问题。

但测试**本身**大量 unix-only 且未加 build tag（`syscall.Kill`、
`syscall.SysProcAttr{Setpgid/Setsid}`、`syscall.Getpgid`、硬编码 `/bin/sh`、
`os.Symlink`、`/tmp` 字面量断言），涉及 `internal/executor/*/reap_test.go`、
`start_ordering_test.go`、`resume_test.go`、`internal/prochost/platform_test.go`、
`shim_test.go`、`internal/permgate/path_test.go`、`internal/agentd/workspace_test.go`、
`cmd/permission_mcp_test.go` 等十余个文件。已正确加 `//go:build unix` 的只有三个
（`approver_env_unix_test.go`、`workspace_fifo_unix_test.go`、
`workspace_procgroup_unix_test.go`）。

后果：`GOOS=windows go vet ./...` 会大面积失败。做 Windows CI 之前必须先补标签。

---

## 三、已经推导完成的设计取舍

这四项是本次 brainstorm 的实质产出，都基于对代码的实测核实而非印象。**将来重启
B37 时可直接采用，不必重新讨论。**

### 3.1 用 `golang.org/x/sys/windows`，不手写 DLL 绑定，也不引 go-winio

事实一：**stdlib `syscall` 在 Windows 上没有任何一个需要的 API**。逐个搜过
GOROOT：`CreateNamedPipe`、`ConnectNamedPipe`、`LockFileEx`、`WaitNamedPipe`、
`CreateJobObject`、`AssignProcessToJobObject`、`TerminateJobObject`、
`SetInformationJobObject` —— **一个都没有**。stdlib 只给到 `CreateFile`、
`CreatePipe`（匿名管道，不能跨进程按名字会合）和 `NewLazyDLL`。所以「只用 stdlib」
的实际含义是在本仓库里重写一份 x/sys/windows。

事实二：**`golang.org/x/sys v0.47.0` 已经在 `go.mod` 里了**（indirect，由
`modernc.org/sqlite` / `go-isatty` 拖入）。用 `x/sys/windows` **不新增任何模块**，
只是把一条既有的 indirect 提升为 direct：`go.sum` 不变，构建体积不变，供应链面不变。
且它确实够用——上面那一串 API 加 `JOBOBJECT_EXTENDED_LIMIT_INFORMATION` 结构体全都有
（唯一缺 `OpenJobObject`，而 3.3 的方案压根不需要它）。

**为什么 `platform_unix.go` 的「不引 x/sys」不能当先例**：那条注释是对的，但它成立
的原因是 unix 上 stdlib 就够了（`Flock` / `Mkfifo` / `Kill` 都在 stdlib 里），不引
x/sys 是零成本。Windows 上不引它的成本是手写十来个 DLL 绑定加手摆内存布局。同一条
原则（用最小够用的东西）在两个平台上导出相反的结论。

顺带订正 A 期骨架的一处注释：`platform_other.go:24` 写的
`createInputChannel → \\.\pipe\ 命名管道（go-winio）`——**go-winio 才是真正的新依赖**
（新模块 + 自己的依赖树，且 Job Object 部分仍要另找）。x/sys 不是。

### 3.2 输入通道：shim 当命名管道服务端，agentd 当客户端

**失配在哪。** unix 侧的输入通道是「文件系统对象 + 无状态」：shim 以 `O_RDWR` 持住
读端（`shim.go:86`，注释说明这是为了「FIFO 永不 EOF」），agentd 每次写入都重新
`O_WRONLY|O_NONBLOCK` 打开那个路径（`claudecode/proc.go:238`）。Windows 的命名管道住
在 `\\.\pipe\` 命名空间里、不在文件系统里，而且是有状态的句柄。

**「无状态 + 按路径寻址」不是实现细节，它就是 B36 的招牌属性**：agentd 重启后 shim
还活着、还持着读端，agentd 只要按路径重新打开就能继续写。所以「谁当服务端」直接决定
这条属性在 Windows 上还在不在：

- **agentd 当服务端**（贴合现有调用顺序：`createInputChannel` → 拉起 shim →
  `waitInputReader`）——agentd 一重启，服务端管道随进程消失，shim 的客户端句柄收到
  EOF，子进程 stdin 关闭，执行者大概率跟着死。**招牌属性没了。**
- **shim 当服务端**（采纳）——shim 是长命的那一侧，agentd 每次写入按管道名当客户端
  连一次。无状态、按名字寻址、活过 agentd 重启，与 unix 语义逐条对齐。

**代价与收益：**

- `createInputChannel` 在 Windows 上没东西可建（服务端要等 shim 起来才存在），退化成
  「推导并校验管道名」；`waitInputReader` 变成「等这个名字变得可连接」——这恰好就是
  它在 unix 上的原意（等读端出现），连它防的那个竞态都是同一个
  （`prochost.go:125` 记录的 8fca917 真机复现）。
- 白捡一个好处：子进程 stdin 可以是一条 `os.Pipe()` 匿名管道，shim 永久持着写端 →
  **天然永不 EOF**，比 unix 那个 `O_RDWR` 自己当写端的技巧干净。命名管道服务端只负责
  把客户端写来的字节泵进去，客户端断开就 `DisconnectNamedPipe` + 重新
  `ConnectNamedPipe` 等下一个写者。
- `nMaxInstances=1` 建管道时同名管道已被占用会失败——正好对应 unix 那条「路径存在但
  不是 FIFO 就显式失败」的检查。
- **`proc.json` 的 `InputCh` 字段保持文件系统路径不变**，管道名由该路径确定性推导
  （`handoff-<路径 SHA-256 前 16 字节>`，确定性 = agentd 重启后可重算 = 无需存状态）。
  所以三个 adapter 和 spec schema 一行都不用改，**平台缝隙完整留在 prochost 内部**。

（评估过的第三条路：改成文件队列，agentd 追加写 `in.log`、shim tail 它。能完全避开
Win32 管道代码且可在 macOS 上测试，但要自己处理偏移量持久化、部分写与消息分帧，
语义变动面更大。未采纳。）

### 3.3 `killGroup`：shim 自持 Job Object + `KILL_ON_JOB_CLOSE`

unix 上 `killGroup(pid)` 是 `kill(-pid)`，pid 本身就是进程组 id，无需任何句柄。
Windows 的 Job Object 是句柄制的，而 agentd 手里只有一个裸 pid——而且
**`OpenJobObject` 恰好是 x/sys/windows 唯一缺的那个函数**。

**结论是根本不需要回去。** Job Object 的
`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 限制位规定：最后一个指向该 job 的句柄关闭时，
job 内所有进程一律终止。而子进程虽然继承 job 成员身份，**却不继承 job 句柄**，所以
句柄只有 shim 一份。于是：

```
killGroup(pid) = OpenProcess(pid) + TerminateProcess    // 只杀 shim
                 → shim 的 job 句柄随进程关闭
                 → 最后一个句柄消失 → 整棵树被内核收掉
```

一个裸 pid 就够，不需要 `OpenJobObject`、不需要给 job 起全局名字、不需要手写任何
DLL 绑定。

**关键推论：job 必须由 shim 自己建、自己 assign。** 与 3.2 同一个陷阱——若由 agentd
在 `spawnDetached` 里建 job 并持句柄，agentd 一重启句柄就关，`KILL_ON_JOB_CLOSE`
当场把执行者收掉。长命的东西统一归 shim 持有：`spawnDetached` 在 Windows 上只做
`CreateProcess(DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP)`，job 在 shim 内部建。

**结构性影响：`shim.go` 目前是平台中立的**，这条路会给它加一个平台钩子（unix 空操作，
因为 `Setsid` 已在 `spawnDetached` 里做完；Windows 建 job + assign self）。
即**六原语变七原语**。

**一处刻意的行为差异，必须记住**：unix 上 shim 死了，执行者会被 init 收养继续跑，而
存活锁已释放 → `Alive()` 报 false —— handoff 认为它死了、实际它还在（现存的一处
wart）。Windows 上 `KILL_ON_JOB_CLOSE` 让 shim 一死整棵树跟着死，**现实与模型反而
对得上**。这是变好而不是变差，但语义确实不同。

### 3.4 验收门与范围

- **验收门**：先在 Windows 上装 claude CLI 并配凭据，做完整端到端
  （dispatch → permission/question → completed → diff → run → continue → done）。
  理由：不同 adapter 覆盖不同原语——opencode 走 HTTP serve，能验
  `spawnDetached` + Job Object + 存活锁，但**碰不到命名管道**；命名管道只在
  claude / grok / codex 的路径上。而 grok 在 Windows 上因 symlink 不可用（2.4），
  codex 凭据未就位，所以 claude 是唯一能覆盖全部七原语的选择。
- **范围**：七原语 + `LockFileEx` 存活锁（核心）、`handoff run` 的 `sh -c`、
  `loginpath` 的注册表等价物、四处 `LookPath` 解析后校验、E1、E2、`RunCmd` 进程组。
  **Windows CI 不在本次范围**（验收门本来就是真机 e2e，CI 属防回归的后续动作，且
  需先补 2.5 的测试标签）。

---

## 四、重启的触发条件

出现下列任一情形时，把 B37 从「暂不做」转回 todo，并直接以本文档第三节为设计输入
进 writing-plans：

1. 出现真实的 Windows 使用者（自己的或他人的）。
2. 需要把 handoff 作为产品分发，而 Windows 是必须覆盖的平台。

入手顺序建议：**先付验收门的前置代价**（在 Windows 上装好 claude CLI 与凭据），
再动代码。原因见 1.4——没有可端到端验收的执行器时，进程层写完也无法验收，这正是
A 期把 B37 推后的原文理由，重启时同样成立。

---

## 五、本次顺带落地的改动

扫描出的所有项里，只有一项**在 macOS / Linux 上就有独立价值**，已在本分支实现：

**四处 `exec.LookPath` 把解析出的绝对路径打进日志**
（`claudecode/proc.go`、`codex/proc.go`、`grok/proc.go`、`opencode/proc.go`）。

理由与 Windows 无关：PATH 上同时装着多份同名 CLI 是常态（nvm / homebrew /
npm global 各一份），此前日志里只有 `"claude"` 这个名字，版本行为不一致时完全
不可诊断。

`LookPath` 的另外半项（校验解析结果是不是真 CLI，用于区分桌面 GUI）是 Windows 特有
的，未做，留在第三节的范围里。

---

## 六、探路残留（需清理）

那台 Windows（`192.168.0.84`）上留有本次探路的残留物，Windows 这条线既已打住，应当
清理：

- agentd 仍在运行，且其临时 token 在协作过程中出现在聊天记录里——**应停掉 agentd，
  或轮换该 token**
- 三个 `failed` 任务（终态，无法用 `handoff stop` 回收）及其可能残留的 worktree
- 三个 `probe/*` 分支
- `%USERPROFILE%\handoff.exe`、`%USERPROFILE%\handoff-repo`、`%USERPROFILE%\.handoff`
- 一条为 agentd 端口放开的防火墙规则
- 审核者本机：`~/.handoff/config.yaml` 里的 `win` target

探路期间只修改过进程级 PATH，**未持久化任何系统或注册表环境变量**。
