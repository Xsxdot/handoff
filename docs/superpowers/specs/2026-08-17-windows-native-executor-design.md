# Windows 原生执行机（B37 重启）设计

**状态：已定案 · 待实现**（2026-08-17）

这份文档是 B37 的实现 spec。它的设计输入是
[2026-08-10 的成本清单](2026-08-10-handoff-windows-port-cost.md)——那份文档是决策
记录与成本清单，本文档是在它基础上做完范围裁决后的落地方案。

**重启触发条件已满足**：成本清单第四节写的两条触发条件（出现真实 Windows 使用者 /
需按产品分发且 Windows 必须覆盖）中，本次两条同时成立，但**本轮只兑现第一条**：
先把「一台 Windows 机器真能接活」打通，分发就绪的部分整体推到第二批（见第十节）。

---

## 一、本轮目标与非目标

**目标**：一台干净的 Windows 机器能作为 handoff 执行机，被 dispatch 派活、真实跑完
一个 plan、被审阅、被收口，且进程治理语义与 unix 执行机对等。

**非目标**（全部推到第二批，理由见第十节）：命名管道与 claude 支持、AF_UNIX 裁决
socket、ConPTY 与工作台终端、Windows Service 托管、`pathenv` 的注册表等价物、
`handoff pull` 的路径冒号、权限位 ACL 收口。grok 沿用成本清单 2.4 的判断，
保持「已知限制、不修」。

## 二、已定决策

这六条在 brainstorm 中逐条裁决，实现时不得自行改动；要改先回来改这份 spec。

| # | 决策 | 理由摘要 |
|---|------|---------|
| D1 | 动因是「两者，但先打通自用」 | 终点是分发，本轮只做单机端到端跑通 |
| D2 | 真机验收门用 **opencode** | 它与 codex 都是零 unix-ism，而 opencode 是日常派 plan 的主力，跑通它才等于「这台机器真能接活」。选它使命名管道与 AF_UNIX 两块整个出本轮 |
| D3 | 验收机器是**新开一台干净云 Windows**，纯执行机 | 不背 08-10 探路残留；接入手续按 `windows-coordinator-box-setup` 的记录重走 |
| D4 | 本轮**不写托管代码** | 验收用 `schtasks` 手工配一条开机启动，git 与 opencode 直接装进机器级 PATH。这使 `pathenv` 的注册表解析整个不需要做——它本质是「补救用户没把工具装进 PATH」的便利功能。`service.Manager` 继续大声拒绝 Windows，不造半残 |
| D5 | `handoff run` 走 **Git for Windows 自带的 `sh`** | 现有 plan 里的 unix 风格验证命令一行不用改。代价是执行机必须装完整 Git for Windows（MinGit 不带 `sh`） |
| D6 | 测试 build tag **补齐全仓**，CI 的 `GOOS=windows` 门从 build 升级为 `go vet ./...` | 真机 e2e 不可能每个 PR 跑，编译/vet 门是 B37 落地后唯一能守住它的自动化机制（B36 当时就靠 `TestWindowsCrossCompiles` 守住） |

## 三、对 08-10 成本清单的五处订正

清单是可信的，但代码在这一周里走远了，五处必须订正。**实现时以本文档为准。**

第五处（E2 的根因归因过时，`prochost.Kill` 在 B47 之后已是同步复核）单独放在 6.1，
因为它同时改变了修法与落点。

### 3.1 清单低估：prochost 自己多了三样非 unix 平台没有的东西

B73 与 B72/B103 落地后新增：

- `fence.go`（`RLIMIT_NPROC` 进程围栏）→ `fence_other.go` 返回 `errFenceNotSupported`
- `procenum.go`（后代名册与足迹）→ `procenum_other.go` 返回 `errNotSupported`
- `killProc`（roster 第二段清扫）→ 未实现

### 3.2 清单高估：Job Object 让上面两样一个白捡、一个变多余

我们本来就要为 `killGroup` 建 Job Object。job 一旦建起来：

- **围栏是白捡的**：`ActiveProcessLimit` 与 `KILL_ON_JOB_CLOSE` 在同一个
  `JOBOBJECT_EXTENDED_LIMIT_INFORMATION` 结构体里，同一次 `SetInformationJobObject`
  调用设完。而且语义比 unix 侧**更准**——`RLIMIT_NPROC` 限的是「每用户进程数」，是拿
  用户级限制近似「围住这棵执行者树」；job 限的正好就是这棵树。
- **roster 的回收职责变多余**：roster 存在的唯一理由是 unix 上 `kill(-pgid)` 收不到
  换了进程组的后代（grok 把每条命令 `setsid` 成新会话那次，B103）。Windows 的 job 是
  **内核强制的包含关系**，子进程无法自行逃逸。所以 `procenum` 在 Windows 上只剩
  「足迹观测/归因」一个用途，缺席不构成安全缺口。

**结论**：围栏做（白捡），`procenum` 不做（但注释必须改写，见 7.4）。

### 3.3 清单过时：「验收门必须是 claude」不再成立

清单 3.4 的推理是「不同 adapter 覆盖不同原语，只有 claude 覆盖全部七原语」。这个推理
本身没错，但它把「覆盖全原语」当成了验收门的必要条件。重新扫过四个 adapter 的平台
原语依赖：

| adapter | 传输 | unix-ism |
|---|---|---|
| **opencode** | `opencode serve` HTTP | **零** |
| **codex** | `codex app-server --listen ws://` | **零** |
| grok | — | `os.Symlink`（Windows 阻断，不修） |
| claude | FIFO stdin | 命名管道 + AF_UNIX 裁决 socket + `syscall.O_NONBLOCK` |

若本轮目标是「一台机器端到端跑通」而非「全原语覆盖」，选 opencode 可把命名管道
（清单 3.2 整节）与 AF_UNIX 裁决 socket（清单 E1）两块最贵的骨头整个推到第二批。

清单里「codex 凭据未就位」也已不成立（mac-02 在用），但 codex 不是日常派 plan 的
主力，跑通它证明不了「这台机器能接日常的活」，故仍选 opencode。

### 3.4 清单说的「六原语变七原语」不再需要

清单 3.3 说「`shim.go` 目前是平台中立的，这条路会给它加一个平台钩子」。B73 之后
`shim.go:100` 已经有了这个钩子——spawn 之前调 `setNprocLimit(spec.NprocLimit)` 装
围栏。所以正确做法不是**新增**钩子，而是把这个已存在的调用点**泛化**成「安装进程
容器」。**零新增原语。**

### 3.5 清单的 `LookPath` 校验一项应当降级

清单把「校验 `LookPath` 解析结果是不是真 CLI」列为必做（桌面 GUI `OpenCode.exe` 被
解析成 `opencode`）。但 opencode adapter 本来就有 10 秒的 serve 就绪探测
（`internal/executor/opencode/proc.go:175`），GUI 起来不会 listen 那个端口，**功能上
已经被挡住了**；缺的只是错误信息里没带解析到的绝对路径，于是人看到「serve 就绪超时」
而不是「你解析到的是 GUI」。

故降级为：**在就绪超时的错误里带上 `bin`**（一行）。真正的 GUI/CLI 判别逻辑留给第二批
——那时才有必要替陌生用户兜底。

---

## 四、架构：进程容器

平台缝隙绝大部分留在 `internal/prochost` 内部（新增 `platform_windows.go`），
**`prochost.Spec` / `proc.json` / 四个 adapter 一行不改**。prochost 之外只有第六节
的三处。

### 4.1 抽象

把 `shim.go:100` 的围栏安装点泛化为「安装进程容器」：

```
安装进程容器(spec.NprocLimit)
  ├─ unix    → setNprocLimit(RLIMIT_NPROC)                        ← 现状，不动
  └─ windows → CreateJobObject
             + SetInformationJobObject(KILL_ON_JOB_CLOSE | ACTIVE_PROCESS)
             + AssignProcessToJobObject(self)
```

Windows 那一侧一次调用同时拿到三样：连坐回收、围栏、以及 job 作为内核强制的树包含
关系（承担 roster 的回收职责）。

### 4.2 所有权铁律：job 必须归 shim

job 由 shim 自己建、自己 assign、自己持句柄。**若由 agentd 在 `spawnDetached` 里建并
持句柄，agentd 一重启句柄就关，`KILL_ON_JOB_CLOSE` 当场把执行者收掉**——B36 的招牌
属性「执行者活过 agentd 重启」当场失效。这与清单 3.2 里输入通道「谁当服务端」是同一
个陷阱：长命的东西统一归 shim 持有。

子进程继承 job 成员身份，但**不继承 job 句柄**（`CreateJobObject` 返回的句柄默认不可
继承），所以句柄全程只有 shim 一份。

### 4.3 `killGroup` 退化为两步

```
killGroup(pid) = OpenProcess(PROCESS_TERMINATE, pid) + TerminateProcess   // 只杀 shim
                 → shim 的 job 句柄随进程关闭
                 → 最后一个句柄消失 → 整棵树被内核收掉
```

一个裸 pid 就够，不需要 `OpenJobObject`——它恰好是 `x/sys/windows@v0.47.0` 唯一缺的
那个函数（已核实：`CreateJobObject` / `AssignProcessToJobObject` /
`SetInformationJobObject` / `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` /
`ActiveProcessLimit` / `LockFileEx` / `CreateToolhelp32Snapshot` 全部就位，
`OpenJobObject` 缺席）。

**依赖**：把 `golang.org/x/sys` 从 indirect 提升为 direct。零新增模块、`go.sum` 不变、
构建体积不变（理由见清单 3.1）。不引 go-winio。

### 4.4 三处这个改法自己冒出来、清单里没有的细节

1. **`shim.go:100` 的 `if spec.NprocLimit > 0` 闸门在 Windows 上是错的。** 围栏被策略
   关掉时 `NprocLimit == 0`，这个闸会把整个 job 创建跳过——而 job 是 `killGroup` 的
   **唯一**回收手段。**Windows 上 job 必须无条件建，`ActiveProcessLimit` 只在 `> 0`
   时才设。**
2. **同一调用点两个平台的失败语义相反。** unix 上围栏装不上只 `Warn`
   （`shim.go:102`，没围栏照样能跑）；Windows 上 job 建不起来意味着没有任何回收能力，
   必须硬失败退出。
3. **围栏值有个 off-by-one。** `RLIMIT_NPROC` 装在 shim 上由执行者全树继承；job 的
   `ActiveProcessLimit` 计的是 job 内进程数，而 **shim 自己也在 job 里**。`fence.go`
   算出的值语义是「执行者树的进程数」，Windows 上要 `+1` 才等价。

### 4.5 `CREATE_BREAKAWAY_FROM_JOB`：招牌属性在 Windows 上的承重点

D4 选了用 `schtasks` 常驻 agentd，而 **Task Scheduler 会把任务进程放进它自己的 job
里**以支持「结束任务」。Windows 8+ 虽支持嵌套 job，但若外层 job 带了
`KILL_ON_JOB_CLOSE`，agentd 一停 shim 会被外层 job 连坐杀掉，「执行者活过 agentd
重启」在 Windows 上就没了。

因此 `spawnDetached` 必须先尝试带 `CREATE_BREAKAWAY_FROM_JOB` 脱离父 job；被拒时回落
到不带该标志，并打一条明确 Warn（本机上执行者不保证活过 agentd 重启）。

**这一条不接受推理结论，必须进真机验收剧本第 4 条实测。**

### 4.6 一处刻意的行为差异

unix 上 shim 死了，执行者会被 init 收养继续跑，而存活锁已释放 → `Alive()` 报 false
——handoff 认为它死了、实际它还在（现存的一处 wart）。Windows 上
`KILL_ON_JOB_CLOSE` 让 shim 一死整棵树跟着死，**现实与模型反而对得上**。这是变好而
不是变差，但语义确实不同，实现时要在注释里写明，避免后人当 bug「修」回去。

---

## 五、存活锁：`LockFileEx`

三个锁原语落地：

- `flockExclusiveNB` → `LockFileEx(LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY)`
- `isLockContended` → 判 `ERROR_LOCK_VIOLATION`
- `lockSupported` → `true`

**语义等价性**：Windows 字节区间锁随句柄关闭而释放，进程终止时句柄由系统关闭，因此
「内核在进程死亡时无条件释放」这条不变量成立。`internal/agentd/lock.go` 那套
「不写 PID、不做进程探活、不提供 `--force` 逃生口」的设计前提**不需要动**。

`lockSupported` 从 `false` 翻成 `true` 会同时点亮两个现在瞎着的东西：

1. **`prochost.Alive()`**。它现在在 Windows 上恒为 `false`（试锁恒成功 = 锁没人持 =
   shim 死了）。这不只是少个功能：`Alive()` 是恢复扫描、看门狗、`Kill` 复核的共同判据，
   恒 false 会让 agentd 认为所有任务都已死。
2. **DataDir 单实例保护**（`internal/agentd/lock.go:71` 那条 Warn 消失）。探路时正因它
   缺席，每个 PowerShell 脚本开头都得手工 `Stop-Process` 旧 agentd，否则两个 agentd
   抢同一份 SQLite 与同一批 worktree。

---

## 六、prochost 之外的三处改动

| 位置 | 改什么 |
|---|---|
| `internal/initflow` 的 `RoleOptions` / `form.go:212` | 解禁 Windows 的执行机角色，并改写 roleNotice——它现在写着「Windows 上 handoff 只能当协调者」，落地后就成了假话 |
| `internal/agentd/workspace.go:1477` | `sh -c` 在 Windows 上按序定位 Git for Windows 的 `sh.exe`：PATH → `%ProgramFiles%\Git\bin\sh.exe` → `%ProgramFiles(x86)%\Git\bin\sh.exe` → `%LOCALAPPDATA%\Programs\Git\bin\sh.exe`；全落空则返回明说「请装完整 Git for Windows，MinGit 不带 sh」的错误。与 `pathenv` 的「已知安装目录兜底」同一模式，仓库已有先例 |
| `internal/agentd/workspace.go` 的 `RemoveManagedWorktree`（`:728`） | 清单 E2 的修正版，见 6.1 |

另有一行改动：opencode serve 就绪超时的错误里带上解析到的 `bin`（见 3.5）。

### 6.1 E2 的根因订正：不是「Kill 是异步的」

**成本清单 E2 的归因已经过时。** 它写的是「`prochost.Kill` 又是异步的」，据此提出
「kill 后等进程真正退出再删」。但 B47 之后 `Kill` 已经是同步复核的——它在确认死亡前
不返回，`prochost.go:180` 的注释甚至明确写着「调用方紧随其后的资源清理（如
`RemoveManagedWorktree`）天然排在进程真死之后，不需要额外同步」。按清单原文去
「加等待」会加在一个已经等过的地方。

**真正的 Windows 竞态在别处**：`Kill` 的复核判据是 **shim 的存活锁**，而执行者子进程
是被 `KILL_ON_JOB_CLOSE` 连坐杀掉的。shim 进程拆解时，「锁文件句柄释放」与「job 最后
一个句柄关闭触发连坐」是两个并列的后果，**内核不保证前者晚于后者完成**。于是存在一个
窗口：`Alive()` 已转 false、`Kill` 已返回，而执行者子进程仍在活着——它的 cwd 是那棵
worktree，在 Windows 上等于一个不带 `FILE_SHARE_DELETE` 的目录句柄，`git worktree
remove` 必然失败。后果与清单描述的一致（每任务残留一棵 worktree、同名重试撞
`already exists`），但成因不同，修法也不同。

**修法**：给 `RemoveManagedWorktree` 内部加带退避的重试，而不是在调用方加等待。

- **为什么改函数内部而不是调用点**：实际有**四个**调用点（`workspace.go:477` 派发失败
  补偿、`manager.go:855`、`:1187`、`:1288`），不是清单说的三个。改函数一处覆盖全部四处，
  且不会漏掉将来新增的调用点。
- **为什么不去等子进程**：`child.pid` 虽然有，但用 pid 等存活会重新引入 pid 复用误判
  ——那正是整个 `prochost` 用文件锁而非 pid 判存活的原因（`prochost.go` 包注释记着
  workspace.go「300 条成功命令误杀 114 次」的教训）。为一个短暂窗口引入已被消除的
  风险不划算。
- **语义不变**：重试耗尽后仍按现状只 Warn 并继续（「失败不阻断」），不新增阻断点。
- **unix 上同样启用**：重试在 unix 上第一次就成功（unix 允许删除作为他人 cwd 的目录），
  零代价，且避免出现一条只在 Windows 上走过的路径。

---

## 七、失败语义与降级纪律

沿用 `platform_other.go` 文件头已确立的原则——**大声失败优于静默半残**，但按「缺席
意味着什么」分四档，不能一刀切。

### 7.1 硬失败

- **Job Object 建不起来**：没有 job = 没有任何回收手段 = `Kill` 永远杀不干净。绝不能
  沿用 unix 侧围栏那条 `Warn` 就往下走。
- **`LockFileEx` 返回非撞锁的真错误**：存活语义的地基塌了，继续跑等于所有 `Alive()`
  判据都不可信。必须与 `ERROR_LOCK_VIOLATION`（正常撞锁 → `ErrLockHeld`）严格区分。
- **找不到 `sh.exe`**：`handoff run` 返回可行动文案，不静默降级去试 cmd 或 PowerShell
  ——那会让协调者写的 unix 风格命令以难以理解的方式半跑。

### 7.2 降级并大声说

- **`CREATE_BREAKAWAY_FROM_JOB` 被父 job 拒绝**：回落到不带该标志，Warn 明说本机上
  执行者不保证活过 agentd 重启。这是招牌属性的降级，必须留痕。
- **`ActiveProcessLimit` 设不上**：沿用 unix 侧现状（`shim.go:102` 的 Warn）。

### 7.3 诚实拒绝（新增，落在注册层）

Windows 上 `cmd/agentd.go:285` 的 `defaultAdapters` **不注册 claude 与 grok**。

这比在 `Start` 里报错更早也更诚实：`handoff status` 会如实显示这台机器支持哪些执行器，
协调者在派发前就看得见，而不是任务跑到一半转 failed。

**codex 照常注册，但记为「未验」而非「支持」。** 它与 opencode 同为零 unix-ism（3.3），
没有已知阻断，所以不注册它是对它的诬告；但本轮验收门只跑 opencode，codex 在 Windows
上一次都没跑过。落地记账时必须写「codex 未验」，不得因为「原理上应该能跑」就记成已支持
——这是 B84 那条「不把模拟写成真机」纪律的同一条。

对 claude 尤其重要——它的 `Start` 第一步是建 AF_UNIX 裁决 socket，而 Go 在
Windows 10 1803+ 支持 AF_UNIX，**socket 可能真的建得起来**，然后走到
`CreateInputChannel` 才炸。那是一个半启动状态：socket 建了、进程没起、清理路径没人走
过。与其让它走到那里，不如在门口就不放行。

### 7.4 保持未实现，但必须改注释

- `createInputChannel` / `waitInputReader`：继续返回 `errNotImplemented`。它们只在
  claude 路径上，而 claude 在 Windows 上已不注册，故这条错误本轮实际**不可达**——注释
  要写清这个「不可达」是被注册层挡住的，不是碰巧。
- `procenum`：继续返回 `errNotSupported`，但注释必须改写成「**回收职责已由 Job Object
  承担，此处缺席只损失足迹观测**」。现在那句「调用方必须据此降级为未知」在 Windows 上
  会被读成「进程可能逃逸没人管」——那是假的，而且会误导人做出错误决策。

### 7.5 两处 build tag 的机械调整

`platform_other.go` 的文件头解释过为什么不用 `_windows` 后缀（会成为隐式 GOOS 约束，
plan9/js 就没实现了）。现在我们要的**正是** windows-only 实现，所以
`platform_windows.go` 用文件名后缀是对的，而：

- `platform_other.go`：`//go:build !unix` → `//go:build !unix && !windows`
- `fence_other.go`：`//go:build !darwin && !linux` → `+ && !windows`

这是那条注释预留的演进路径，不是推翻它。

### 7.6 日志

按全局规范与 `instrumenting-code`：job 创建/assign、breakaway 成功与回落、围栏设定
（含 `+1` 后的实际值）、锁获取与撞锁、`sh.exe` 定位结果（打绝对路径）、E2 的等待时长
与超时，全部是关键节点，必打；错误分支一律带上下文。

---

## 八、测试

### 8.1 能在 macOS 上钉住的（纯逻辑，与平台原语无关）

- `sh.exe` 候选路径的探测顺序（注入 fs stat）
- `RoleOptions("windows")` 的输出与 roleNotice 文案
- `RemoveManagedWorktree` 的重试退避（注入假 remove）：首次成功即返回、耗尽后只 Warn
  并继续，不阻断
- 围栏值 `+1` 换算（纯函数）
- `isLockContended` 对错误码的分类

### 8.2 只能真机验的

Job Object 的连坐行为、`CREATE_BREAKAWAY_FROM_JOB` 在 Task Scheduler 的 job 下会不会
被拒、`LockFileEx` 的死亡释放。这三样没有本地替代，推理再充分也不算数。

### 8.3 build tag 债与 CI 门（D6）

补齐成本清单 2.5 列出的十余个 unix-only 测试文件的 `//go:build unix`：涉及
`internal/executor/*/reap_test.go`、`start_ordering_test.go`、`resume_test.go`、
`internal/prochost/platform_test.go`、`shim_test.go`、`internal/permgate/path_test.go`、
`internal/agentd/workspace_test.go`、`cmd/permission_mcp_test.go` 等。

**逐个确认 unix-ism 再加 tag，不得盲加**——盲加会把本可在 Windows 上跑的用例一起排除，
那等于用一个假的绿色换真的覆盖。

CI 的 `GOOS=windows` 门从 `go build ./...` 升级为 `go vet ./...`。

---

## 九、真机验收剧本

由审核者独立执行，**不采信执行者自述**（沿用 B29/B36 的纪律）。

1. 干净机器接入：完整 Git for Windows（非 MinGit）+ opencode CLI + 两者进机器级 PATH
   + `schtasks` 常驻 agentd。接入手续按 `windows-coordinator-box-setup` 的三条记录走
2. `handoff init` 能选出执行机角色，roleNotice 不再说假话
3. dispatch → opencode 真跑完一个小 plan → `completed`
4. **重启 agentd，executor 存活**——招牌属性，同时暴露 breakaway 到底被拒没被拒（4.5）
5. `handoff stop` 只杀 shim，用 `tasklist` 复核整棵树被 job 连坐收掉
6. 围栏：设一个小的 `ActiveProcessLimit`，验证扇出被挡住，且 `+1` 换算没差一位
7. 起第二个 agentd，验证 DataDir 单实例拒绝且文案可行动
8. `handoff run` 跑一条含管道的 unix 风格命令，验证走的是 Git 的 `sh`
9. `done` 后 worktree 删干净、同名重试不撞 `already exists`（E2）
10. `handoff status` 里没有 claude 与 grok，派发它们被干净拒绝

---

## 十、第二批：分发就绪（另立 backlog 条目）

本轮明确不做，但落地后应当立刻登记，避免「自用能跑」被误当成「可分发」：

| 项 | 为什么推后 |
|---|---|
| 命名管道 + claude 支持 | 最贵的一块（清单 3.2 整节 + E1 的 DACL 裁决管道），而 opencode 已足够证明执行机可用 |
| ConPTY 与工作台终端 | W4/W5 之后新长出的缺口，与进程承载层正交 |
| Windows Service 托管 + `pathenv` 注册表解析 | D4 已裁决：干净机器上直接配对机器级 PATH 即可，托管是分发场景才需要的 |
| `LookPath` 的 GUI/CLI 判别 | 见 3.5，自用场景下就绪探测已挡住 |
| `handoff pull` 的路径冒号 | 审阅取证可走 `diff`/`fetch`/`attach` |
| 权限位 ACL 收口 | 一整类问题，应在 DataDir 一层用一次显式 ACL 解决，不逐点修 |
| `procenum` 的 Toolhelp32 实现（足迹观测） | 回收职责已由 job 承担（3.2），缺席只损失足迹展示；`CreateToolhelp32Snapshot` 符号就位，真需要看足迹时再做 |
| codex 在 Windows 上的真机验收 | 本轮注册但未验（7.3），补一次 e2e 即可转「已验」 |
| grok on Windows | 沿用清单 2.4：`os.Symlink` 需特权，保持已知限制、不修 |
