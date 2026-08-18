# B142 + B122：Windows 平台层补完（service Manager + 每任务进程计数）

> 状态：设计定稿，待 writing-plans
> 来源：B142、B122（两条合并为一份 spec，理由见 §1.2）
> 前置：B37（Windows 原生执行机）已 done；B144–B148 papercuts 已修
> 前置探针：08-18 win-b37 真机 PASS（§3.7），§3 方案成立

---

## 一、背景与范围

### 1.1 两条 backlog 的实情

**B142（高优）**：`internal/service/service.go` 的 `New` 对 `windows` 直接返回错误，
理由写的是「进程承载层 Windows 实现尚未完成（backlog B37）」——**B37 早已 done**，
win-b37 此刻正拿这套 prochost 跑任务，理由是过期的。

后果不止文案错。没有托管管理器 → `handoff status` 恒报「agentd 非托管启动，换版会被
拒绝（`--force` 也不越过）」→ **Windows 执行机换版只能手工停进程覆盖文件**。
实测（win-b37）：`handoff service status` 与 `service install` 均退出 1。
证据：[Windows 全方位验收](../notes/2026-08-18-windows-executor-full-acceptance.md) §3 D1。

**同源的 D8**：手搓的 `schtasks` 托管里，`schtasks /end` 报 SUCCESS、任务回到 `Ready`，
但 agentd 进程原样活着——它只杀了外层 `cmd.exe`，孙进程没连坐。管理器视图与现实分叉。
本 spec 一并解决（见 §2.5）。

**B122（中优）**：Windows 上没有每任务进程计数能力，连带静默失效的是
`TaskBudget` 告警档（`RunWatchdog` → `taskProcCountFn` → `Footprint`）与
`handoff footprint` / `status` 的足迹显示。
`TaskHardLimit` 已由 B37 的 Job Object `ActiveProcessLimit` 接管**且更强**
（内核当场拒绝 fork，不用等下一次采样），不在本 spec 范围。

### 1.2 为什么合成一份 spec

两条同属 B37 留下的 Windows 平台层空缺，代码面相邻（`internal/service`、
`internal/prochost`），且**真机验收共享同一个窗口**——分开做要重复付两次
「开 RDP + 换版 + 真机验」的开销。

### 1.3 明确不做（写在这里免得后人当疏漏）

| 不做 | 为什么 |
|---|---|
| **整机进程余量档**（`procLimit` / `scanPressure`） | Windows 没有 `RLIMIT_NPROC` 式的每用户进程数上限（进程数受内存与句柄约束）。[B37 spec §11.6](2026-08-17-windows-native-executor-design.md) 已定案。`UIDUsage` 继续返回 `ErrNotSupported`，那条降级 Warn 保留 |
| **`enumProcs` 全系统枚举**（B122 原标题的 Toolhelp32 路线） | 理由见 §3.1。`procenum_other.go` 在 Windows 上继续返回 `ErrNotSupported`，**包括它现有那段解释性注释**——那段注释本身是资产，说明了「这个缺席在 Windows 上的含义与其它平台不同」 |
| **SCM Windows 服务** | 选型见 §2.1 |
| **B139**（grok 的 write 工具在 Windows 上不能写文件） | 根因未定位，不到写 spec 的阶段。它的取证是一步真机动作，挂在本 spec 落地后的那次验收窗口里顺带做 |

---

## 二、B142：Windows service Manager

### 2.1 选型：schtasks 计划任务，不用 SCM 服务

`launchd` 用 `gui/<uid>` 域、`systemd` 用 `--user`，两者都是「用户级、跑在用户会话里」。
Windows 没有直接对应物，两条候选：

**选 schtasks，三条理由**：

1. **它是已经在真机上跑通的形态**。win-b37 现在就是手搓的 S4U 计划任务，B37 那轮验收
   （四个执行器、并发、活过 agentd 重启）全部建立在这个形态上。换成 SCM 服务等于把
   已验证的运行前提换掉，B37 的验收结论要重新成立一遍。
2. **executor 的凭据落点**。grok 的 `~/.grok/auth.json`、opencode 的 auth、claude 的
   settings 全挂在用户 profile 下。SCM 服务默认跑在 Session 0 / SYSTEM，
   `%USERPROFILE%` 会变，这条链路有断的风险，而它是 handoff 的命脉。
3. **D8 那个「收不掉」不是 schtasks 的固有缺陷**，是手搓那份套了一层 `cmd.exe /c`
   的缺陷。Manager 自己装时直接把 `handoff.exe` 当 Action，不套 cmd（见 §2.2 第 4 条）。

**放弃 SCM 的代价**（如实记账）：SCM 的开机自启不需要用户登录。计划任务用
`<LogonTrigger>` 时需要用户登录过一次。这对一台长期在线的执行机不构成实际问题，
但如果将来出现「重启后无人登录」的部署形态，这一条要重新评估。

### 2.2 单元形态：走 XML，不走命令行参数

**关键收获：Windows 也是「生成单元文件 → 交给管理器加载」**，与 launchd 写 plist、
systemd 写 unit 完全同构。因为 `schtasks` 的命令行参数**表达不了多实例策略**——
`/Create` 没有 `/MULTIPLEINSTANCES` 这个开关，而我们恰恰需要 `IgnoreNew`。
要设它只能走 `schtasks /Create /XML <file>`。

于是 `internal/service/windows.go` 的结构与 `launchd.go` 逐行对应：

| launchd.go | windows.go |
|---|---|
| `plistBody(spec)` 渲染 plist | `taskXML(spec)` 渲染 Task Scheduler XML |
| `UnitPath()` → `~/Library/LaunchAgents/<label>.plist` | `UnitPath()` → `%LOCALAPPDATA%\handoff\<TaskName>.xml` |
| `launchctl bootstrap <domain> <path>` 加载 | `schtasks /Create /XML <path> /TN <TaskName> /F` |
| `launchctl bootout <target>` 卸载 | `schtasks /Delete /TN <TaskName> /F` |
| `launchctl print <target>` 复核 | `schtasks /Query /TN <TaskName> /XML` + 进程复核（§2.4） |
| `KeepAlive=true`（exit 0 也拉起） | `<Repetition>` 每 1 分钟 + `MultipleInstancesPolicy=IgnoreNew`（§2.3） |

常量：`WindowsTaskName = "handoff-agentd"`，与 `LaunchdLabel` / `SystemdUnit` 并列。

**XML 里四项承重配置，一条都不能少**：

1. **`<Repetition><Interval>PT1M</Interval><Duration>P365D</Duration>`**
   + **`<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`**
   这两条合起来才等价于 `KeepAlive`：活着时重复触发被忽略，死了下次触发拉起。
   **任一缺失都会失效**——缺前者 = 永不重启（换版后服务无声消失）；
   缺后者 = 每分钟起一个新实例，全被 DataDir 锁挡下，日志里刷满锁冲突。
2. **`<LogonTrigger>`**：登录时自启，对标 `RunAtLoad`。重复触发挂在这个触发器上。
3. **`<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>`**
   + **`<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>`**
   两者默认值都是 `true`，笔记本上会让任务**根本不启动且不报错**。
   这是 Task Scheduler 最经典的静默失效，必须显式关掉。
4. **`<Exec><Command>` 直接指向 `handoff.exe`，不套 `cmd.exe /c`**。
   D8 那个「`/end` 只杀外层」的根因就是手搓那份套了 cmd。
   `<Arguments>agentd --config <path></Arguments>`，与 plist 的 `ProgramArguments` 同构。
   日志重定向：Task Scheduler 不提供 `StandardOutPath` 式的能力，
   `Spec.LogPath` 由 agentd 自己写（`logx.Setup` 已经在做），XML 侧不管。

**另两项建议开启**（不承重，但省麻烦）：
`<StartWhenAvailable>true</StartWhenAvailable>`（错过触发时补跑）、
`<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>`（不限时长——默认 72 小时会把长跑的
agentd 掐掉，这是另一个静默失效面）。

### 2.3 换版链路：为什么必须是「exit 0 也拉起」

`shutdown.go` 的文件头写明：换版整条链
（下载 → 替换 → 退出 → 管理器拉起新版）**唯一的交接点就是退出码**。
systemd 的 `Restart=on-failure` 在 exit 0 时不会重启，所以部署模板改成了 `Restart=always`；
launchd 用 `KeepAlive=true`（`plistBody` 的注释明写「exit 0 也会被重新拉起，
这正是自更新换版所依赖的」）。

Windows 上 `schtasks` 自带的「失败时重启」（`RestartOnFailure`）**帮不上**——
它只在任务**非零退出**时生效，而换版走的是 exit 0。所以只能用 §2.2 第 1 条的
重复触发来模拟，而 `<Interval>` 的最小值是 **1 分钟**。

**由此产生一处必须同步改的冲突**：`upgradeWaitTimeoutPush`
（[cmd/upgrade.go:119](../../../cmd/upgrade.go)）是 **60 秒**，注释理由是
「二进制已经在对端、换版是秒级动作」。而 Windows 的最坏空窗接近 60 秒，
正好压在超时线上——那是最糟的失败形态：时好时坏，看起来像别的问题。

**改法：`upgradeWaitTimeoutPush` 60s → 120s，全平台生效**，并在注释里补上
Windows 这一档的理由。代价是一次真起不来的推送换版让操作者多等 60 秒，
可接受（那条注释原本的顾虑就是这个，不是别的）。

**不选的两条**（记账，免得后人重走）：

- *薄 supervisor 常驻*：空窗秒级，但新增一个常驻进程层，要与 B37 的
  `CREATE_BREAKAWAY_FROM_JOB` 重新对齐，且与 service 包「交给本平台的进程管理器托管」
  的职责边界冲突。
- *agentd 自己 spawn 继任者*：空窗秒级，但新旧两个 agentd 瞬时共存会撞 DataDir 锁
  （E3 已验证锁真的生效），新进程拿不到锁就退出且无人拉起 = **变砖**；
  要改 agentd 启动期的承重代码加等锁重试。风险与收益不成比例。

### 2.4 `Status()` 不能只信 schtasks

D8 实测过：任务状态报 `Ready` 而 agentd 进程原样活着——**管理器视图与现实会分叉**。

**分叉的根因值得说清，因为它决定要不要复核**：手搓那份套了 `cmd.exe /c`，
schtasks 跟踪的是外层 cmd，孙进程 agentd 不在它视野里。§2.2 第 4 条不套 cmd 之后，
schtasks 跟踪的直接就是 `handoff.exe`，这个分叉**理应消失**。

但「理应」不是「验过」，而 `Status` 说谎的代价是操作者据此做换版决策。所以仍然复核：

- `Installed`：`schtasks /Query /TN <TaskName>` 成功即为 true。查不到是**正常答案**
  不是错误（与 launchd `print` 退 113 同款纪律）。
- `Running`：读 `schtasks /Query /TN <TaskName> /V /FO CSV` 拿到状态**与 PID**，
  再用 `tasklist /FI "PID eq <pid>"` 复核那个 pid 真的在。
  **判据必须是 PID 不是镜像名**——`tasklist /FI "IMAGENAME eq handoff.exe"` 会把
  操作者正在敲的 `handoff` CLI 也数进去，那是个稳定的假阳性
  （协调者机器上尤其明显，CLI 几乎总在跑）。
- `Detail`：取 `schtasks /Query` 输出摘要，供排障。

**验收时要留意**：如果真机上 schtasks 状态与 pid 复核**从未分叉**，说明上面那条推理成立，
可以在后续版本里简化掉复核；如果仍然分叉，那就是一个新发现，要单独记账。
两种结果都要在验收记录里写明，不要因为「反正复核了」就不看那个信号。

`Installed=true, Running=false` 是一个真实且常见的状态（`Status` 结构的文档已经写明
这一点），Windows 上它还多一种成因：任务被电池策略挡下。

### 2.5 `Uninstall()` 不能依赖 `schtasks /End`

D8 的直接结论。`Uninstall` 的顺序：

1. `schtasks /Delete /TN <TaskName> /F` —— 先摘掉托管，避免删进程后又被重复触发拉起。
   任务本来就不在时报错属正常，只记 Debug（与 launchd `bootout` 同款）。
2. **自己按 pid 终止 agentd**，走 `prochost` 已验证的 job 连坐路径
   （D1 通过项实测：`stop` 后 6 个 pid 全部消失，含孙子辈的 `sleep.exe`）。
3. 删 XML 文件。不存在时返回 nil（幂等）。

### 2.6 Windows 专有的三个坑（不写进 spec 实现者必踩）

| 坑 | 表现 | 处置 |
|---|---|---|
| **XML 必须是 UTF-16 LE + BOM** | `schtasks /Create /XML` 喂 UTF-8 会报一个与编码毫无关系的错 | `writeFile` 缝里做编码转换，单测断言 BOM 与编码 |
| **`schtasks` 输出编码随系统码页** | 中文系统上 `run` 拿到的是 GBK 字节，塞进日志是乱码（同款坑已在读远端日志时踩过：`Get-Content` 默认按 ANSI 码页，中文日志全是乱码，看起来像日志写坏了，其实是读法错） | `Detail` 与错误报文里的原文按码页解码；拿不准就只取 ASCII 可读部分并标注，**不要静默丢弃非 ASCII 字节**——那会把真因抹掉 |
| **`/Create` 需要的权限** | S4U 任务通常要管理员或「作为服务登录」权限 | 失败时**原样带上 schtasks 的 stderr**，不自己编处置建议（B64 的教训：编出来的处置建议会把人引向错误方向） |

---

## 三、B122：Windows 每任务进程计数

### 3.1 为什么不用 Toolhelp32（推翻 B122 原标题）

`classify` 的三条规则全部以 **pgid** 为轴：规则一判组长复用、规则二靠 setsid 的
会话封闭性、规则三是时间下界。**Windows 上前两条根本没有对应物**——没有进程组、
没有会话封闭性，`PROCESSENTRY32` 只给 pid/ppid。照原标题实现 `enumProcs`，
`procEntry.PGID` 没有东西可填，`classify` 会整个失效，等于要为 Windows 重写一套成员判据。

而 `footprint.go` 已经论证过「事后不能用祖先链（ppid）补救」。
**Windows 上这条论证只会更成立**：Windows 的 pid 复用比 unix 激进，父进程死后子进程的
ppid 仍指向那个已死并可能已被复用的 pid——ppid 闭包在这里是个会说谎的判据。

**Windows 已经有一个内核维护的、精确的成员名册：B37 给每个任务建的 Job Object。**
`QueryInformationJobObject(JobObjectBasicProcessIdList)` 直接回答「这个任务当前有哪些
pid」，进程退出即从 job 移除；不需要 pgid、不需要时间下界、不需要采样补漏，
而且天然覆盖 unix 侧要靠三段判据才够得着的逃逸后代。

### 3.2 一个必须先讲的障碍

**光让 shim 往名册文件里写是不会生效的。** `Footprint` 第一行就是 `enumProcsFn()`，
Windows 上返回 `ErrNotSupported` 直接返回——它永远走不到名册那段。
所以 B122 必须在 `Footprint` 这一层开一条分支。

### 3.3 新平台缝

仿照 `attributes` / `MarkCapability` 已有的形态，加一个平台原语：

```go
// containerMembers 读进程容器（Windows Job Object）当前的成员表。
//
// 返回：
//   - pids: 容器内的成员 pid（含 shim 自己）
//   - sampledAt: 该份数据的采样时刻（unix 纳秒）
//   - supported: 本平台是否用容器作为成员来源。**false 时调用方必须落回
//     现有三段判据，而不是理解为「没有成员」**
func containerMembers(h Handle) (pids []int, sampledAt int64, supported bool)
```

- `containermembers_other.go`（含 darwin/linux）：`supported=false`
  ——含义是「本平台不用这条路，走现有三段判据」，不是「查不到」。
- `containermembers_windows.go`：读 shim 写的成员文件（§3.5）。

`Footprint` 顶部加一条前置分支：容器来源可用就直接返回，不可用才落到现有三段判据。

**unix 侧的执行路径必须逐字节不变**——B47 的误杀教训、B70 的口径一致、B72 的出生登记、
B119 的降级清扫全都长在那三段上，一行都不该碰。测试要有**反面断言**钉住这一点（§5）。

`Sweep` **不接这条缝**：Windows 上的回收由 job 的 `KILL_ON_JOB_CLOSE` 连坐承担
（B148 已经把那条误报的告警改掉了），Sweep 在 Windows 上继续走「本平台不做名册清扫，
回收由进程容器承担」的既有分支。**只读的 Footprint 接，动手的 Sweep 不接**——
两者风险模型不同，这条边界与 `footprint.go` 现有的纪律一致。

### 3.4 shim 侧：数据源从 procenum 换成 job

`runRosterSampling` 现在在 Windows 上第一次 `sample` 返回 false 就退出
（只打一条 Info）。改为：Windows 上数据源换成
`QueryInformationJobObject(jobHandle, JobObjectBasicProcessIdList)`。

- shim 自己持有 `jobHandle`（`platform_windows.go` 的包级 var，故意不 Close），
  **不需要 `OpenJobObject`**（x/sys/windows 里恰好缺这个函数），
  **不碰 B37 的承重代码**。
- 采样节奏、「内容未变则不写」的优化、退出时留最后一份快照——
  全部沿用 `rosterSampler` 现成的机制。

### 3.5 落一个独立文件，不复用 roster.json

job 成员表只给 pid 数组，**没有 StartedAt**。而 `roster.json` 的每一条都承诺带可信的
`StartedAt`——那是 `rosterKill` 的杀人判据（「pid 在表 + StartedAt 完全相等」才发信号）。
往里塞没有时刻的条目会污染一条承重判据的语义。

所以写独立文件 `members.json`（与 `roster.json` 同目录）：

```json
{"pids": [1234, 5678], "sampled_at": 1755500000000000000}
```

`sampled_at` **不是可选的**：Windows 上 `Footprint` 带采样延迟（agentd 拿不到 job 句柄，
只能读文件），输出必须能说明数据是什么时刻的。这是 B70「宣称什么就得是什么」的直接要求。

`Handle` 加一个 `MembersPath string \`json:"members_path,omitempty"\`` 字段，
与 `RosterPath` 同款纪律：omitempty + 零值语义（升级前写下的 proc.json 没有这个字段，
读出空串即 `supported=false` 落回三段判据，老任务不会因为升级就换判据）。

### 3.6 连带改动

| 改什么 | 为什么 |
|---|---|
| **删掉 `cmd/agentd.go:78` 那条启动 Warn**（「本平台不支持进程枚举，每任务进程预算告警档不生效」） | 它不再成立。**这是 B122 完成的可见判据之一** |
| `taskProcCountFn` 无需改动 | 它走的就是 `Footprint`，自动跟上，`TaskBudget` 告警档随之在 Windows 上生效 |
| `UIDUsage` / `scanPressure` 整机档**不动** | 保持 `ErrNotSupported` 与降级 Warn，理由见 §1.3 |
| `MarkCapability` 的那条 Warn **不动** | 「Windows 上这是预期形态：回收由 Job Object 进程容器承担」仍然正确 |

### 3.7 API 可用性：已由前置探针验证（08-18，win-b37 真机）

起草时这是本 spec 最大的未决项——探针不过则整个 §3 要回落到 Toolhelp32 + ppid 闭包
那条弱路。**08-18 已在 win-b37 上实跑验证，结论：方案成立。**

**静态结论**（x/sys v0.47.0）：

| 需要的东西 | 有没有 |
|---|---|
| `QueryInformationJobObject` 函数 | ✅ 有（`zsyscall_windows.go`） |
| `JobObjectBasicProcessIdList = 3` 常量 | ✅ 有（`types_windows.go:2602`） |
| `JOBOBJECT_BASIC_PROCESS_ID_LIST` 结构体 | ❌ **没有，必须手工声明** |

**手工声明（已验证可用，实现时直接照抄）**：

```go
// 尾部是变长数组：结构体只声明 1 个元素，实际长度由调用方分配的缓冲区决定。
// 两个 uint32 合计 8 字节，其后 ULONG_PTR 在 64 位上按 8 字节对齐、恰好落在偏移 8，
// 32 位上按 4 字节对齐同样落在偏移 8——两种位宽都没有隐式 padding，可以直接映射。
type jobBasicProcessIDList struct {
	NumberOfAssignedProcesses uint32
	NumberOfProcessIdsInList  uint32
	ProcessIdList             [1]uintptr
}
```

**取长度的手法：翻倍重试，不要「先查长度再分配」。** `ERROR_MORE_DATA` 时 Win32
**并不保证**把所需字节数写回 `retlen`（MSDN 对这个 information class 没有作出该承诺）。
依赖一个没被承诺的返回值会得到一个在小规模下好使、进程一多就随机失败的东西。
探针里用的是「估一个容量（64 槽），撞 `ERROR_MORE_DATA` 就翻倍，上限 65536」，已验证可用。
另需一道护栏：内核报的 `NumberOfProcessIdsInList` 若超过已分配槽位，**不要按那个数去读**
——翻倍重来，宁可多跑一轮也不越界读一段不属于我们的内存。

**真机验证输出**（win-b37，Windows Server 2025）：

```
self_pid=1696
step1_create_job=OK
step2_assign_self=OK
step3_query_before      pids=[1696]     assigned=1
step4_spawn_child       child_pid=444
step5_query_after_spawn pids=[1696 444] assigned=2
step6_query_after_kill  pids=[1696]     assigned=1
PROBE_RESULT=PASS
```

三条被这次实跑坐实的前提：

1. **step2 通过 = 外层 job 允许嵌套。** 这是最值得盯的失败形态——探针经 ssh 运行，
   而 Windows OpenSSH 会把整个会话放进一个 job（那正是 B37 里
   `CREATE_BREAKAWAY_FROM_JOB` 承重的由来）。嵌套若被拒，§3.4 的前提当场崩塌。
   实测可以嵌套。
2. **step5 = job 成员表真的反映执行者树的构成**，spawn 出来的子进程 500ms 内出现。
3. **step6 = 退出即从表中移除。** 这条是「Windows 侧不需要时间下界校验」的根据：
   unix 的 roster 要靠 `StartedAt` 相等来防 pid 易主，而 job 成员表由内核维护，
   进程一退出就不在表里，不存在「表里那个 pid 已经易主」的窗口。

---

## 四、接线与配置改动汇总

| 文件 | 改动 |
|---|---|
| `internal/service/service.go` | `New` 增加 `case "windows"`；包头「不支持 Windows」那句边界注释改写 |
| `internal/service/windows.go` | 新建：`windowsManager` + `taskXML` + 五个接口方法 |
| `internal/service/windows_test.go` | 新建：对标 `launchd_test.go` |
| `cmd/upgrade.go` | `upgradeWaitTimeoutPush` 60s → 120s，注释补 Windows 档理由 |
| `internal/prochost/footprint.go` | `Footprint` 顶部加容器前置分支；`Sweep` 不动 |
| `internal/prochost/containermembers.go` | 新建：平台无关契约 |
| `internal/prochost/containermembers_other.go` | 新建：`supported=false` |
| `internal/prochost/containermembers_windows.go` | 新建：读 `members.json` |
| `internal/prochost/prochost.go` | `Handle` 加 `MembersPath` |
| `internal/prochost/shim.go` | Windows 上采样源换成 job 成员表 |
| `internal/prochost/platform_windows.go` | 新增 `jobProcessIDs()` 原语（查 job 成员表） |
| `cmd/agentd.go` | 删掉 TaskBudget 不生效的启动 Warn |

**没有新的配置项。** `proc_fence.task_budget` / `task_hard_limit` 沿用现有语义。

---

## 五、测试策略

| 层 | 覆盖 | 在哪跑 |
|---|---|---|
| `taskXML(spec)` 渲染 | 纯函数。§2.2 四项承重配置**逐条断言**（`Repetition` 间隔、`IgnoreNew`、电池两项、`Command` 不套 cmd）+ UTF-16 BOM | 任何平台 |
| `Install`/`Uninstall`/`Status` | 注入 `run`/`writeFile`/`remove` 缝，断言命令拼装、失败回滚、幂等卸载。对标 `launchd_test.go`（155 行） | 任何平台 |
| `Status` 的进程复核 | 断言「schtasks 报 Ready 但进程在 → `Running=true`」这条分叉（D8 的判据） | 任何平台 |
| `Footprint` 容器前置分支 | 注入缝，断言「容器可用走容器、不可用落回三段」。**含反面断言：`supported=false` 时 unix 三段判据的调用序列不得改变** | 任何平台 |
| `containerMembers` Windows 实现 | `//go:build windows`：真的建 job、spawn 子进程、查成员表、验证 pid 出现与消失 | **只能在 Windows 上** |

**win-b37 上目前没有 Go**（08-18 实测：`'go' is not recognized as an internal or external
command`）。用户已确认可以装，**因此 windows-only 单测按「能真的跑」来设计，不做降级**。

装 Go 是验收的前置步骤，写进验收清单第一条（§6.3）。若届时因故没装成，
windows-only 单测降级为只做 `GOOS=windows go build` / `go vet` 交叉验证，
**此时必须在验收记录里如实写明这个降级，不得假装测过**。

顺带一提，探针本身**不需要**对端有 Go：交叉编译出 exe 传过去跑即可（08-18 就是这么做的）。
同样的手法可以用在「想在真机上跑一段一次性验证」的任何场合。

---

## 六、派发与验收归属

### 6.1 这份 plan 绝不能派给 win-b37

B128 之后 win-b37 上三个执行器可用，派过去看似合理——**但这份 plan 要做的正是装卸和
重启那台机器的 agentd 托管，而 executor 自己就跑在那个 agentd 底下**。
等于边开车边换轮子：plan 跑到一半就会把自己的宿主弄死，现场还没人能收。

**实现派 mac-02**（写代码 + 跑跨平台单测 + `GOOS=windows go build`/`go vet` 交叉验证），
**真机验收全部留本地由审核者手工做**。

### 6.2 全部真机判据都要驱动 handoff 自身，一条都不能写进派发的 plan

按「需驱动 agentd 的验收步骤必须显式归审核者」这条纪律，逐条点名：

**B142**：
1. `handoff service install` 成功，XML 落盘且编码正确
2. `handoff service status` 报托管（`Installed=true, Running=true`）
3. 手工 kill agentd → **1 分钟内被拉回**（这条验的是 §2.2 第 1 条那对配置）
4. **`handoff upgrade --target win-b37` 走通闸二**——这是 B142 的价值判据，不是可选项
5. `handoff service uninstall` 后任务与进程双清

**B122**：
1. 起一个任务 → `members.json` 有内容且 `sampled_at` 在推进
2. 把 `task_budget` 临时调到很小 → 收到 `task_proc_pressure` 事件
3. `handoff footprint` 显示成员数，且与手工点名（`tasklist` 逐个核对该任务的进程树）**对得上**
4. **启动日志里那条「预算告警档不生效」的 Warn 消失**

这些一条都不能写进派发的 plan——纪律块明令禁止 executor 调 handoff CLI，
写进去只会得到一句诚实的「未验证」，而那正是最承重的判据。

### 6.3 验收的操作风险与顺序

B142 的验收要在 win-b37 上装真正的托管，而那台机器现在跑着**手搓的 schtasks 任务**。
两者会打架：同名会互相覆盖，不同名会让两个 agentd 实例竞争 DataDir 锁。
且万一新的装不上，那台机器会变成「没有托管、原来的手搓也没了」。

**建议顺序**：

1. **先在 win-b37 上装 Go**（08-18 实测没装；windows-only 单测要它才能跑，见 §5）
2. `service install` 到一个**不同的任务名**上做冒烟，确认能起能停
3. 冒烟过了再拆手搓那份、改回正式名 `handoff-agentd`
4. 全程保留手搓那份的 XML 导出（`schtasks /Query /TN <旧名> /XML > backup.xml`）作回退

---

## 七、未决项与风险

| # | 项 | 处置 |
|---|---|---|
| ~~1~~ | ~~`JOBOBJECT_BASIC_PROCESS_ID_LIST` 是否可用~~ | ✅ **08-18 已由真机探针解决**（§3.7）：结构体要手工声明，声明已验证可用，成员表行为符合预期。**§3 方案成立，plan 不再需要探针 task** |
| ~~2~~ | ~~win-b37 上是否装有 Go~~ | ✅ **08-18 已确认：没装**。用户确认可装，列为验收前置步骤（§6.3 第 1 条） |
| 3 | `schtasks /Create` 所需权限 | 失败时原样带 stderr，不编处置建议（§2.6） |
| 4 | `<LogonTrigger>` 要求用户登录过一次 | 已知代价，记在 §2.1。出现「重启后无人登录」的部署形态时重新评估 SCM |
| 5 | Windows 换版空窗最长约 1 分钟 | 已知代价（macOS 约 10 秒）。已跑的任务不受影响（executor detached，「活过 agentd 重启」是招牌属性）。`upgradeWaitTimeoutPush` 放宽到 120s 留一倍余量 |
