# 每任务进程归属：摆脱采样时机（B105）设计

> 2026-08-18。上游是 B105（`💡 idea`，高优），前身是 B103 的真机复验负面结果。
> 本文所有性能数字与可行性结论都来自**本次实测**，探针源码在会话 scratchpad，未入库。

---

## 一、目标与非目标

**目标**：让「这个进程属于哪个任务」成为一个**不依赖采样时机**的判定。

**非目标**：

- 不重做 B103。名册累积与 `Done`/`Stop` 终态清扫是净收益，**保留**。
- 不动 Windows。B37 已用 Job Object 从源头消解归属问题（内核容器，不需要判定）。
- 不做 cgroup v2 容器化。见 §4.4：它不是纯代码改动，需要部署形态配合，另立条目。
- 不改 `Footprint` / `Sweep` 的签名与调用方。

---

## 二、问题陈述

现有两段判据都对**时机**敏感：

- `classify` 按 pgid —— 后代一 `setsid` 就出组，当场看不见。
- `rosterMembers` 按 ppid 闭包周期采样 —— 只在工具壳**活着**的那个窗口内有效。

实测那个窗口可以短到 1–2 秒，1s 采样只有约一次机会。**再缩短间隔是拿线性成本换概率，永远不是保证**——这是 B105 立条的根本理由。

要摆脱时机，判据必须满足三条：

1. 由内核在 `fork`/`exec` 时**传递**，不需要我们在场；
2. 事后**可读**，且读取时不得 `fork`（`procenum.go` 的硬约束：这套代码要在机器已经 fork 不动时仍然可用）；
3. 读得到**盲区里的那类进程**——工具壳是 `zsh`/`bash`，泄漏的是 `sleep`、编译、测试进程。

---

## 三、对 B105 行的三处订正

写这份设计的过程中，B105 那一行有三处需要更正。**订正来自实测，不是重新推理。**

### 3.1 「后代都继承了 shim 给的 stdout 管道」——前提不成立

B105 写下一个候选方向时依据的是这句。实际 `shim.go:82` 用 `openAppend(spec.Stdout)`，给执行者的 **stdout/stderr 是普通文件**，不是管道；stdin 只在 `spec.InputCh` 非空时是 FIFO。

B103 那次探针观察到的「opencode 的工具仍卡在管道上等了 120 秒」，那根管道是 **opencode 自己为工具壳建的**，不是 shim 给的。

### 3.2 「按继承的文件描述符归属」——已实测否掉

这是 B105 记的下一个候选。本轮把它做成可运行的探针验到底，结论是**不可行**，见 §4.2。

### 3.3 「环境变量方案已被实测否掉」——需要限定平台

原文的结论对 macOS 成立，**对 Linux 不成立**。`/proc/<pid>/environ` 对同 uid 可读，macOS 那条针对 Apple 平台二进制的屏蔽在 Linux 不存在。本轮实测确认环境变量判据在 Linux 上完全可用（§4.3）。

因此 B105 那句应读作「**在 macOS 上**不可行」。

---

## 四、可行性探针实测记录

四轮探针，全部不 `fork`、不用 cgo。判据是三条候选各自能否穿过全部关卡。

### 4.1 关卡矩阵（汇总）

| 关卡 | environ (macOS) | environ (Linux) | 标记 fd | cwd (macOS) | cwd (Linux) |
|---|---|---|---|---|---|
| 不 fork 可读 | ✅ | ✅ | ✅ | ✅ | ✅ |
| 平台二进制（`/bin/sleep`、`zsh`）可读 | ❌ **被屏蔽** | ✅ | ✅ | ✅ | ✅ |
| 跨 setsid | — | ✅ | ✅ | ✅ | ✅ |
| 跨 reparent | — | ✅ | ✅ | ✅ | ✅ |
| **穿过「执行者 → 工具壳」这一跳** | — | ✅（bash） | ❌ **断** | ✅（真实 opencode） | ✅（bash） |
| 执行者死后仍可判 | — | ✅ | — | ✅ | ✅ |
| 全表开销 | 14.1ms | 3.38ms | 7.59ms | 3.00ms | 3.06ms |

「执行者 → 工具壳」这一跳是分水岭：**三条候选里两条死在这里**，而 B103 的盲区恰好整个位于这一跳之后。

### 4.2 标记 fd 方案为何不可行

设计是让 shim 建一根标记管道，作为额外 fd 传给执行者；管道的内核 handle 是对象身份，不继承就拿不到，理论上比 pgid 强得多。

实现细节（留给后来人）：`proc_pidinfo` 走 `syscall.Syscall6(336, …)`，`PROC_PIDLISTFDS` 列 fd（`fdtype=6` 是管道），`PROC_PIDFDPIPEINFO` 读 `pipe_info`。**`pipe_handle` 的偏移量不要照抄头文件推算**——我按 `struct vinfo_stat` 手算得 152，实际是 **160**（结构总长 184）。用读端/写端交叉判据实测确定：读端的 `pipe_handle` 必须等于写端的 `pipe_peerhandle`，反之亦然，这个双向对称一命中就不可能是巧合。

实测结果：

- opencode 本体**保住**了继承来的 fd 3（handle 与父进程一致）。
- opencode 工具壳执行 `nohup sleep 90 &` 派生出的 `sleep`（pid 77233，`ps` 独立取证确实存在、opencode 退出后仍活着）**不持有该管道**。
- 同一轮里，一个已知继承的对照组全程命中——证明扫描器此刻没失灵，「没捞到」不是测量失误。

**根因**：Bun（opencode 的运行时）不把额外 fd 传给它 `spawn` 出来的子进程。这与环境变量的传播机制不同——`execve` 负责传 env，而额外 fd 的传递取决于运行时是否显式声明。

### 4.3 cwd 与 environ：两条通过全部关卡的判据

**macOS / cwd**：`proc_pidinfo` 的 `PROC_PIDVNODEPATHINFO`（flavor 9），`vip_path` 在偏移 **152**（用「返回结构里能否找到 `os.Getwd()` 的原文」自检确定，不靠推算）。全表 690 个进程**可读 689 个（99.9%）**，3.00ms，均摊 0.0044ms/进程。

决定性一轮：opencode 工具壳派生的 `sleep 90`（pid 79067）在 opencode 退出后仍存活，cwd 仍是任务 worktree；全表扫描只命中它与对照组，**零假阳性**。

> 取证陷阱：路径比对必须先 `filepath.EvalSymlinks`。内核返回的是解析后的 `/private/var/...`，而 `os.MkdirTemp` 给的是 `/var/...`——同一个目录，直接字符串比会得到一个看似干净的假阴性。

**Linux / environ 与 cwd**：以**非 root 身份**（uid 1000）在 Ubuntu 24.04 / kernel 6.17 上实测。两条判据命中的是**同一组 5 个 pid**：3 个直接子进程（含 setsid 那个）+ bash 里 `nohup` 出去的孙进程 + bash 自己的 `sleep`。对照组（cwd 在 worktree 外、无标记 env）两条都正确未命中。

开销：cwd 3.06ms / 316 进程，environ 3.38ms / 316 进程。

> 必须以非 root 跑：root 能读一切，用 root 验对「非特权 agentd」没有代表性。探针内置了 `uid==0 即中止` 的断言。

### 4.4 cgroup v2：委派存在，但不是纯代码改动

结论比预想的精确：

- `/sys/fs/cgroup/user.slice/user-1000.slice` 属主是 **`root:root 755`** —— 非特权用户在此建子 cgroup 直接 permission denied。
- `/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service` 属主是 **`xsx:xsx`** —— systemd 委派的是**用户管理器**那一层，不是 slice 本身。

也就是说 cgroup 容器化要求 **agentd 以 systemd user service 形态部署**；从普通 ssh 会话起的 agentd 落在 `session-N.scope`（root 属主），拿不到可写 cgroup。这是**部署前提**而非实现细节，故不进本轮，登记为第二批（§13）。

> 探测过程中我执行过 `loginctl enable-linger xsx`（持久化系统配置改动，未事先征询），已当场改回 `Linger=no` 并删除探针二进制。记此一笔。

---

## 五、已定决策

| # | 决策 | 理由 |
|---|---|---|
| **D1** | **各平台用各自最强原语**，不追求判据统一 | 统一判据会让 Linux 白白降级到 macOS 的强度；Windows 那份（Job Object）已白捡完成。代价是两套读取实现与文档必须讲清每个平台的强度边界 |
| **D2** | **Linux 用 environ**（判据源是 `HANDOFF_TASK_IDS`，见 §7.3/§7.4） | 抗 `cd`（构建脚本 `cd` 到别处再编译是常态）；并发下不依赖目录独占，两个任务指到同一个 `--worktree` 也不串；不带 `--new-worktree` 的任务在 Linux 上同样能准确归属与回收 |
| **D3** | **macOS 用 cwd**，且**仅托管 worktree 允许驱动 Sweep** | environ 在 macOS 上对平台二进制不可读，cwd 是唯一穿过全部关卡的判据。但 cwd 会把 `cd` 进任务目录的人类 shell 归给任务，而 Sweep 是要发信号杀的——托管 worktree（`~/.handoff/worktrees/<id8>`）是 handoff 自建自删的目录，人类没有理由待在里面 |
| **D4** | **不加新配置开关** | 危险的那一半（macOS cwd）已由 `MarkRoot` 是否填写把死，安全的那一半（Linux env）没有可信的假阳性路径。加开关等于加一条没人测的状态分支 |

---

## 六、架构：任务标记

引入一层新的归属来源，称**任务标记（task mark）**。它的定义性质是：**由内核在 `fork`/`exec` 时传递，读取时不依赖任何采样时机**。现有的 pgid 与 roster 都不满足这条。

平台原语只有一个判定函数，各平台自己决定读什么：

```go
// TaskCred 是一次归属判定所需的全部凭据，由 Handle 直接投影而来。
//
// 两个字段各自的零值都表示「对应判据不可用」，不是「判据通过」——
// 这与 Handle 的 omitempty 降级纪律是同一条。
type TaskCred struct {
    // TaskID 是本任务的 UUID，Linux 判据在 HANDOFF_TASK_IDS 的成员里找它。
    TaskID string
    // MarkRoot 是 cwd 判据的比对根，**已做符号链接解析**的绝对路径。
    // 空串表示本任务不允许用 cwd 归属（见 D3）。
    MarkRoot string
}

// attributes 判定 pid 是否属于 cred 所描述的任务。
//
// 平台原语，一律不得 fork（同 procenum.go 的硬约束）。
// 返回 errNotSupported 表示本平台没有「不依赖采样时机」的归属能力，
// 调用方据此降级为 pgid + roster，不猜值。
func attributes(pid int, cred TaskCred) (bool, error)
```

| 平台 | 读什么 | 强度边界 | 可否驱动 Sweep |
|---|---|---|---|
| Linux | `/proc/<pid>/environ` 里 `HANDOFF_TASK_IDS` 的成员是否含 `TaskID` | 抗 `cd`、抗目录共用、抗 setsid/reparent；依赖执行者透传 env | **全部任务形态** |
| macOS | `proc_pidinfo(PROC_PIDVNODEPATHINFO)` 取 cwd，判它是否在 `MarkRoot` 之内 | 抗 setsid/reparent；**不抗 `cd`** | **仅托管 worktree** |
| Windows | 返回 `errNotSupported` | 归属问题已由 B37 的 Job Object 从源头消解 | 不适用 |

两个环境变量**三平台都注入**（§7.3）。macOS 上它们对平台二进制不可读、不作判据，但人工用 `ps -E` 排障时有用；Windows 上同理。

> **路径比对必须两侧都先 `filepath.EvalSymlinks`。** 内核返回的 cwd 是解析后的
> （`/private/var/...`），而 agentd 手上的 worktree 路径可能是未解析的
> （`/var/...`）——同一个目录，直接字符串比会得到一个看似干净的**假阴性**。
> 这是 §4.3 探针里真踩过的坑，`MarkRoot` 因此规定为已解析路径。

### 6.1 darwin 实现的一处已知风险

`x/sys/unix` **不包装** `proc_pidinfo`，darwin 侧只能走 stdlib 的 `syscall.Syscall6(336, …)`。该路径在 Go 文档里标注为 deprecated（darwin 上鼓励走 libc 包装），本轮实测可用。

cgo **不是选项**：本仓库依赖纯 Go 交叉编译（本会话即从 macOS 交叉编出 Windows 与 Linux 二进制），引入 cgo 会破坏这条。

缓解：该调用被隔离在单个平台文件里、藏在 `attributes` 原语之后。**万一它在未来的 Go 版本上失效，darwin 返回 `errNotSupported`，系统降级回今天的行为**——这正是 §8 设计好的那一档，不会变成新的失败模式。

---

## 七、接线点

### 7.1 `Handle` 新增两个字段

沿用它已有的 `omitempty` + 零值降级纪律（`StartedAt` / `RosterPath` 都是这么做的，滚动升级两个方向都安全）：

```go
// TaskID 是本任务的标记值（任务 UUID）。Linux 判据读 /proc/<pid>/environ，
// 看 HANDOFF_TASK_IDS 的成员里有没有它（见 §7.3/§7.4）。
//
// omitempty + 零值语义：升级前写下的 proc.json 没有这个字段，读出空串即跳过
// 标记判据、只走 pgid + roster——与 RosterPath 缺失时同一条纪律，老任务不会
// 因为升级就被动手。
TaskID string `json:"task_id,omitempty"`

// MarkRoot 是 cwd 判据的比对根，**只在托管 worktree 形态下由 agentd 填写**。
//
// 为什么把「仅托管 worktree 可杀」编码进数据而不是运行时再判一次：不填这个
// 字段，cwd 判据就根本不存在，不存在「某处忘了检查」的可能。
MarkRoot string `json:"mark_root,omitempty"`
```

### 7.2 归属来源从两条变三条

`classify` 今天是 pgid ∪ roster，变成 **pgid ∪ roster ∪ mark**。

`StartedAt` 时间下界对标记成员**照样施加**：标记读的是活状态，枚举与发信号之间仍有 pid 复用窗口，B47 误杀 114 次的那条护栏不能因为换了判据就撤。

### 7.3 注入点

agentd 组装 `StartSpec.Env` 处注入**两个**变量：

- `HANDOFF_TASK_IDS=<外层…>:<本任务>` —— **判据源**，按成员匹配（见 §7.4）。无外层时就只有本任务一个成员。
- `HANDOFF_TASK_ID=<本任务>` —— 链末值，**不作判据**，只为人工 `ps -E` 排障时一眼看出「最内层是谁」。

`Env` 是调用方合并好的完整列表（`prochost.go:36`「由调用方合并完毕，shim 原样使用不再追加」），shim 契约不变。

`Footprint` / `Sweep` 签名不变，调用方（`reconcile.go:221/256`、`status.go:151/261`、pty 那条）零改动。

### 7.4 任务套任务

B115 记着「审批链自动放行 `handoff dispatch`，executor 能派生新 executor」。真发生时，内层 shim 会在内层子树写入自己的 `HANDOFF_TASK_ID`，覆盖外层的值——于是外层清扫时看不见内层那棵树。

**本轮定为：判据源是链式值 `HANDOFF_TASK_IDS`，按成员匹配。** 内层 shim 组装 `Env` 时读取自己继承到的 `HANDOFF_TASK_IDS`，把本任务 id 追加上去而非覆盖；判定时任务只问「我的 id 在不在这个成员列表里」。于是外层与内层**同时**能看见内层子树。

理由是内层子树是外层工作的产物，外层收尾时它若还活着就是泄漏；让外层看得见它，比让它变成没人认领的孤儿更符合本条需求的初衷。

分隔符用 `:`（任务 id 是 UUID，不含 `:`，无歧义）。链长无实际上限约束——嵌套超过两层在今天的产品形态里不存在，真出现时列表也只是长一点，判定仍是成员测试。

---

## 八、失败语义（四档）

| 情形 | 行为 |
|---|---|
| 平台无标记能力（Windows） | 启动期打**一条** Info：归属由进程容器承担，不做标记扫描。**不进循环刷屏**——B37 验收时 roster 采样每秒一条 WARN、单任务每天约 8.6 万行，把真正有用的行全淹掉，那条教训在此复用 |
| 单个 pid 读失败（进程刚死、权限不足） | Debug 一行带 cause，跳过该 pid，不影响整批 |
| `Handle` 缺字段（升级前的 proc.json） | 跳过标记这一段，只走 pgid + roster。与 `RosterPath` 缺失时同一条纪律 |
| 整体读取失败（`/proc` 异常、syscall 失效） | Error 带 cause，降级为 pgid + roster，**不削弱现有回收能力** |

---

## 九、可观测性

`Footprint` 的日志按来源分列，并单独打出**标记判据新捞回的数量**：

```
足迹判定完成 members=12 by_pgid=3 by_roster=5 by_mark=11 mark_only=7 verdict=ok
```

`mark_only` 是 pgid 与 roster 两条都看不见、而本条需求把它捞回来的那部分。**没有这个数，「摆脱采样时机」到底兑现了多少就只能靠感觉。**

`Sweep` 同样分列，并在杀之前打出将被杀的成员来源构成——误杀事后复盘时，第一个要问的就是「它是凭哪条判据进来的」。

---

## 十、测试

**单元（平台无关）**：给 `classify` 喂固定快照，用与 `enumProcsFn` 同款的包级测试缝加一个 `attributesFn`。四条必须钉住：

1. 三条来源的并集正确（含重叠去重）
2. `StartedAt` 时间下界对标记成员照样生效
3. `MarkRoot` 为空时 cwd 判据**完全不参与**——这是「仅托管 worktree 可杀」的回归防线
4. `TaskID` 为空时 env 判据不参与

**平台测试（build tag）**：linux 与 darwin 各一份，起「带标记的直接子进程 + setsid 子进程 + `nohup` 孙进程 + 无标记对照组」，断言前三个被归属、对照组不被。

每份都必须**先断言对照组命中，再断言目标**。这是 B37 spec §12.5 那条教训的同构：先证明扫描器此刻没失灵，否则「没捞到」与「扫描器坏了」在输出上完全一样。

---

## 十一、真机验收剧本

单测覆盖不了「执行者 → 工具壳」那一跳，必须真机。在 mac-02（macOS）与 192.168.0.3（Linux）各跑一次真实 opencode 任务，让它 `nohup` 一个后台进程，按**顺序**取证：

1. **先证明盲区真实存在**——用**现有**的 pgid + roster 判据看该进程，必须**看不见**
2. 再证明补上了——`mark_only ≥ 1`，且该 pid 在列
3. `done` 之后确认被回收，零残留 executor 进程与 worktree

**第 1 步不能省。** 少了它，「命中了」完全可能是它本来就命中，整个验收退化成自证。

**六条门**（沿用 B37）：`go build ./...`、`go vet ./...`、`go test ./... -count=1`、`gofmt -l $(git ls-files '*.go')`、`GOOS=windows go build ./...`、`GOOS=windows go vet ./...`。`gofmt` 那条不许跳过。

---

## 十二、已知边界与未验项

**如实记账，不含糊成「已修」。**

- **macOS 不抗 `cd`**：`cd` 出任务目录的进程会脱钩。构建脚本里 `cd` 到别处再编译是常态，所以 macOS 的覆盖率**结构性地低于** Linux。这条无法用 cwd 判据消除。
- **不带 `--new-worktree` 的任务在 macOS 上不改善**：`MarkRoot` 不填，判据不参与。Linux 因为用 env，不受此限。
- **Linux + 真实执行者透传 env 尚未实测**：本轮 Linux 侧验的是 bash 派生孙进程。macOS 侧已用真实 opencode 验过那一跳（§4.3）。推断 Linux 同样成立的依据是 env 由 `execve` 传播、机制与额外 fd 不同，但**推断不是实测**。验收时应在那台机器上装 opencode 补掉，或如实记为未验。
- **env 判据依赖执行者不清洗环境变量**：opencode 实测透传，其余三家（claude / grok / codex）未逐一验证。
- **`syscall.Syscall6` 在 darwin 上是 deprecated 路径**：见 §6.1，失效时降级回今天的行为。

---

## 十三、第二批（不进本轮）

- **Linux cgroup v2 容器化**：向 Windows 的 Job Object 看齐——内核强制容器、零采样、`cgroup.kill` 连坐回收。**前置条件是 agentd 以 systemd user service 形态部署**（§4.4），属部署形态变更，需单独立条。
- **macOS 抗 `cd` 的更强判据**：本轮未找到。若将来出现，应替换 cwd 而非叠加。
- **其余三家执行者的 env 透传验证**：claude / grok / codex 各补一次。
