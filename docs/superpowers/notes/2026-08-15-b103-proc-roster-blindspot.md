# B103 排查：每任务进程点名漏掉了一整类进程

排查日期：2026-08-15。**只出报告，未改任何生产代码。**
现场机：mac-02（`sycmdeMacBook-Air.local`）。日志时区均为 +08:00。
本任务未再 fork 大批进程；未杀探针留下的 450 个 `sleep 900`；对 `~/.handoff` 只读、未写。

---

## 职责与边界

- **职责**：回答「08-15 真机探针为什么把 450 个 `sleep 900` 数漏、终态也清不掉」，并在 A / B / C 中给一个结论。
- **边界**：不改 `internal/`、`cmd/`、配置、agentd 二进制；不复现 fork 炸弹；不回收探针残留。
- **证据纪律**：每条结论只引用代码坐标或命令输出原文。推不出的写「判不出」，不拿推测填空。

---

## 0. 结论先行

**(B) 真实缺口。** 不是「B93 事故那种形态被设计漏掉了」——B93 事故里那 2100 个能被 `handoff status` 数到、也能被 `done` 的 `Kill(-pgid)` 整组收掉，落在 pgid 判据的覆盖面里。本次探针漏掉的是另一类：**executor 的 Bash 工具把每条命令 `setsid` 成新会话，命令里 `foo &` 之后工具壳立刻退出，子进程被 reparent 给 launchd，既不在 shim 的 pgid 里，也进不了（或留不住）出生名册。** B93 看门狗点名和 `Sweep` 用的是同一份 `Footprint`，所以数不到与清不掉是同一套归属判据；`done` 还另有一层：它根本不走 `Sweep`，只 `Kill` shim 进程组。

B72 写过「15s 窗口内出生并逃逸会漏记」，但那条局限的自我辩护是「真正堆积的是长命的编译/测试进程，它们活得远比 15s 长」（`shim.go:44-47`）。这次漏掉的 450 个活了整段 900 秒，漏的原因不是活得短，而是**中间父进程（工具壳）活得极短**。B93 自己写的真机复验配方恰好就是 `for i in $(seq 1 600); do sleep 300 & done`（`docs/superpowers/notes/2026-08-15-b93-ledger.md`），与本次探针同一形态——按现实现，这条配方触发不了 400 告警档。

---

## 1. `76332` 是谁；每条 bash 会不会落在新进程组

### 1.1 `classify` 的归属判据

`internal/prochost/footprint.go:115-117`：

```
if p.PGID != h.PID {
    continue
}
```

`h.PID` 是 shim pid，也是 shim 的 pgid（`spawnDetached` 在 `platform_unix.go:81` 设 `Setsid: true`）。本组探针：

| 对象 | pid / pgid | 出处 |
|---|---|---|
| 任务 shim | **76057 / 76057** | `proc.json`：`"pid":76057`；计划给定表 |
| 450 个 `sleep 900` | pgid 一律 **76332**，ppid 一律 **1** | 计划给定表；本任务 10:14 复读 `ps` 仍是这个数 |
| 进程 `76332` 本身 | **已不在** | `ps -p 76332` → `76332 gone` |

`classify` 因此把 450 个全部跳过。这与注释自己承认的内侧不封闭一致（`footprint.go:82-87`）。

### 1.2 现场还在的进程链（10:14，`76332` 已死）

```
$ hostname
sycmdeMacBook-Air.local
$ ps -ax -o pid,ppid,pgid,lstart,command | awk '/[s]leep 900/' | head -1
76336     1 76332 Sat Aug 15 10:08:57 2026     sleep 900
$ ps -ax -o pid=,command= | awk '/[s]leep 900/ {print $1}' | sort -n | awk 'NR==1{min=$1}{max=$1;n++} END{print "n="n,"min="min,"max="max}'
n=450 min=76336 max=76785
```

- 第一个 sleep 是 **76336**，组长 **76332** 已退，组还在（Unix 常态：组员不随组长退出而改 pgid）。
- `76333`–`76335` 三个 pid 在现场已经看不到。**判不出**它们当时分别是 `seq`、zsh 内部辅助还是别的短命子进程。

### 1.3 `76332` 的身份：grok Bash 工具为**这一条**命令拉起的 zsh，且自成会话

`76332` 本身已死，不能再 `ps` 它的 `command=`。同版本 grok（`grok 1.0.3 (1a29d5bc12d4)`，与探针 `serve.log` 横幅一致）在本排查任务里跑一条 bash 时，现场是：

```
$ ps -o pid,ppid,pgid,sess,stat,command -p $$
  PID  PPID  PGID   SESS STAT COMMAND
79769 77560 79769      0 Ss   /bin/zsh -c snap=$(command cat <&3); ... eval "$__grok_user_cmd" ...

$ ps -o pid,ppid,pgid,sess,stat,command -p 77560,77559
77560 77559 77559      0 S    /Users/sycm/.grok/bin/grok agent serve --bind 127.0.0.1:58415
77559 74513 77559      0 Ss   /Users/sycm/.local/bin/handoff _shim --spec .../67bb1343-.../spec.json
```

要点（命令输出原文，不是推断）：

- 工具壳是 **`/bin/zsh -c … eval "$__grok_user_cmd"`**，不是长期驻留的 helper。
- `pid == pgid`，`STAT=Ss`（session leader）⇒ 新会话 + 新进程组，不是留在 shim 组里。
- `ppid` 是 grok serve，不是 shim。
- 探针任务的 sleep 全部共享 **一个** pgid `76332`，而不是每个 sleep 自己一组。这与「非交互、无 job control 的 zsh 把 `sleep 900 &` 留在自己的进程组里，命令结束后自己退出」的形态一致。探针 `render.log` 也写了第 1 步输出是 `spawned=0`（`jobs -p` 在非交互壳里是空的）。

grok 二进制里能直接搜到它用的是 `process-wrap` 的 session/group 包装，不是 handoff 这边设的：

```
$ strings /Users/sycm/.grok/bin/grok | grep process_session
.../process-wrap-9.0.0/src/tokio/process_session.rs
```

handoff 的 grok adapter **没有** 对 bash 调 `Setsid`/`Setpgid`（全包只有测试里的 `Setpgid`）。新进程组是 **grok CLI 自己**给每条工具命令加的。

### 1.4 每条 bash 会不会落在一个**新的**进程组

对 **grok 1.0.3**：**会。** 两个独立样本（探针的 `76332`、本任务的 `79769`）都是「工具壳 pid == pgid == 会话组长，且 ≠ shim pgid」。这与 08-12 对 **opencode** Bash 工具的实证同构（`docs/superpowers/notes/2026-08-12-footprint-smoke.md` §4：每条命令 `setsid`，`pgid ==` 命令自己的 pid，`STAT` 带 `s`）。

**判不出**（本任务没取证）：claude / codex 的 Bash 工具是不是同样每条命令新进程组。

---

## 2. 出生登记谁写、为何这 450 个没进名册

### 2.1 写入点

只有一处生产写入：shim 里的 `snapshotRoster`。

| 谁 | 时机 | 坐标 |
|---|---|---|
| `RunShim` 拉起的 goroutine | executor `Start` 之后**立刻**采一次，然后每 `rosterInterval`（**15s**）再采 | `shim.go:143-158`、`shim.go:50` |
| 同上 | executor `Wait` 返回后 `close(stopRoster)`，**不再补最后一次快照** | `shim.go:173-176` |
| `snapshotRoster` | `descendantsOf(os.Getpid(), procs)` → `writeRoster` 整份覆盖 | `shim.go:276-288` |
| `descendantsOf` | 从 shim pid 沿 **ppid** BFS，不含 root | `roster.go:68` 起 |
| `writeRoster` | 临时文件 + rename，**不是**与旧名册做并集 | `roster.go:115-131` |

文件头把契约写死了（`roster.go:10-11`）：

> 不做增量维护：每次快照都是全量重算。最后一次快照 ≈ executor 死亡时刻的存活者，早退的短命进程自然不在里面。

`descendantsOf` 自己也写明（`roster.go:59-61`）：**只在树活着时有意义**；父进程一死、后代 reparent 给 launchd，ppid 链当场断。

没有任何 adapter、agentd、watchdog 往名册里写。adapter 只把 `roster_path` 记进 `proc.json`（探针：`.../35251ad9-.../roster.json`）。

### 2.2 这 450 个为什么不在名册里

探针任务目录只读结果：

```
$ cat .../35251ad9-.../roster.json
[{"pid":76058,"started_at":1786759671828494000},{"pid":76075,"started_at":1786759674611120000}]

$ cat .../35251ad9-.../child.pid
76058
```

- `76058` = grok serve（`shim.log`：`child_pid=76058`，`10:07:51`）。
- `76075` = shim 的另一个 ppid 后代（启动 `10:07:54`）。**判不出**它当时的 `command=`（进程已不在）。
- 名册里 **没有** `76332`，也没有任何 `76336`–`76785`。
- `roster.json` mtime = **10:11:51**（executor 死前最后一轮全量覆盖）。

时间轴（都是原文，不是估的）：

| 时刻 | 事实 |
|---|---|
| 10:07:51 | shim 76057 / grok 76058 起来；名册 goroutine 立刻采第一次 |
| 10:08:57 | 450 个 `sleep 900` 出生（`ps` 的 `lstart`） |
| ~1 秒内 | 工具壳结束（命令是 `...; echo spawned=...`，探针 `render.log` 已回报），sleep 的 ppid 变为 1 |
| 10:07:51 + 15s×N | 之后每一次 `snapshotRoster` 从 shim 沿 ppid 走，**走不到 ppid=1 的 sleep**，整份覆盖把它们排除 |
| 10:11:25 | `handleResult`：见 §5 |
| 10:11:51 | 名册最后一次落盘，仍是 2 条 |

所以 450 个没进**最后**名册，不是「文件丢了」或「读失败」：文件在、解析正常、内容就是 grok + 76075。

15s 窗口（`shim.go:44-45`、B72 spec §4.3）能解释「出生到父进程退出之间没撞上一次采样」。全量覆盖（`roster.go:10`）还能解释更狠的情况：即便某一次幸运采到了还挂在 zsh 下面的 sleep，**下一轮**父进程已死，名册会被写成「当前树上还看得到的那几个」，450 个照样被抹掉。本次时间轴（10:08:51 左右的 tick 在出生前、10:09:06 左右的 tick 在 reparent 后）连「幸运采到」都不必发生。

---

## 3. B93 那 2100 个为什么被正确归到任务名下

B93 事故任务是 **`d912b23a` / opencode / mac-02**（`docs/superpowers/specs/2026-08-15-b93-task-process-runaway-design.md` §1），不是 grok，也不是 `sleep &`。

### 3.1 事故侧硬事实

规格书记载：`handoff status` 先报该任务 **2100** 进程，40 秒后 **2140**；`done` 后整机从 **2411** 掉回 **290**。

agentd.log 原文（只读）：

```
2026-08-14T13:25:03  opencode 事件流意外中断，按失败结束回合  task=d912b23a
2026-08-14T13:25:06  执行者仍存活，拒绝清扫  pid=91331
2026-08-14T13:25:06  清扫时执行者仍存活，交由常规回收路径
2026-08-14T13:41:42  done 请求
2026-08-14T13:42:58  executor 无内存运行态，按恢复凭据兜底回收
2026-08-14T13:42:59  兜底回收 executor 资源  shim_pid=91331
2026-08-14T13:43:09  兜底回收完成
```

`done` 走的是 adapter `Reap` → `prochost.Kill` → `killGroup(shim pid)`（opencode 与 grok 的 reap 同构；grok 坐标 `internal/executor/grok/reap.go:35-40`，`prochost.Kill` 在 `prochost.go:181-190`，`killGroup` 在 `platform_unix.go:107-108`：`syscall.Kill(-pid, SIGKILL)`）。

`Manager.Done`（`manager.go:1075-1154`）**没有**调用 `SweepTaskProcs`。它只 `stopExecutor` / 无运行态则 Reap。

`Kill` / `killGroup` **只杀 pgid == shim pid 的那一组**。setsid 出去的进程组它够不着（`Kill` 自己的注释都写了「可能有逃逸出进程组的后代」，`prochost.go:201`）。

因此：B93 那 2100 个能在 `done` 时「瞬间掉回 290」，它们当时必须落在 **shim 的进程组**里，才会被 `Kill(-91331)` 带走。这与规格书里「2100 个 setsid 逃逸后代」的叙述**对不上**——那是作者按 opencode Bash 的已知行为做的定性，现场没有留下那 2100 个的 `ps` 行。

### 3.2 名册对不上「靠 roster 数到 2100」

`d912b23a` 盘上最后一份 `roster.json`（mtime 08-14 13:42，6 条）：`91332`（opencode serve）+ 另外 5 个 pid。**没有**两千条。

`Footprint` = `classify`（pgid==shim）∪ `rosterMembers`（`footprint.go:148-161`）。若 13:25 报 2100 是靠名册，13:42 这份被全量覆盖后的名册不该只剩 6 条——除非那 2100 个在 13:42 之前已经从 ppid 树上消失，而 `classify` 仍能按 pgid 数到它们。这与 §3.1 的 Kill 生效方式一致。

13:25 当时那份名册长什么样、那 2100 个各自的 `command=` / `pgid` 是什么：**判不出**（没有当时的 `ps`，名册已被覆盖）。规格书 §4 也明文把「查清 opencode 为什么 fork 出 2100 个」列为不做。

### 3.3 两次的差别（能钉死的部分）

| | B93 事故 `d912b23a`（opencode） | 本次探针 `35251ad9`（grok） |
|---|---|---|
| `handoff status` 任务行 | **2100 → 2140**（规格书） | **3 进程**（计划给定） |
| 进程与 shim 的 pgid | 能被 `Kill(-shim)` 收掉 ⇒ **当时在 shim 组内** | 一律 **76332 ≠ 76057** |
| 最后一份 `roster.json` | 6 条，无两千成员 | 2 条（76058、76075），无 450 个 sleep |
| 父进程是否很快退出 | 未知（无 `ps`） | 工具壳在命令返回后立刻退出，sleep 的 ppid=1 |
| `done` 清掉了吗 | 是（2411→290，`Kill` 组） | 否（done 前后 sleep 仍 450） |

所以 B93 事故形态**不落在本次盲区里**：它落在 pgid 判据（`classify` / `Kill`）盖得住的那一层。本次探针落在「Bash 工具 setsid + `cmd &` + 工具壳退出」那一层，pgid 与名册两段都够不着。

---

## 4. `handoff status` 的「N 进程」和看门狗是不是同一个数据源

**是同一个。** 都是 `prochost.Footprint`。

| 表面 | 调用链 | 坐标 |
|---|---|---|
| `handoff status` 任务行 `N 进程` | `Manager.Status` → `probeActive` → `prochost.Footprint` → `len(members)` | `internal/agentd/status.go:151-153`；渲染在 `cmd/status.go:140-141` |
| 看门狗 `scanTaskProcs` | `taskProcCountFn`（生产 = `Manager.TaskProcCount`）→ `prochost.Footprint` → `len(members)` | `cmd/agentd.go:170-172`；`reconcile.go:206-211`；`watchdog.go:302` |

`Footprint` 的成员是两段并集（`footprint.go:139-141, 148-161`）：

1. `classify`：`pgid == shim.PID` 且 `StartedAt >= shim.StartedAt`
2. `rosterMembers`：`roster.json` 里 pid 仍在、且 `StartedAt` 字节级相等

整机那一行 `835/2666` **不是**这个源。它是 `prochost.UIDUsage()`（`status.go` 里填 `resp.Proc`，`cmd/status.go:128-129` 打「本机 uid 已用/上限」）。450 个 sleep 进 uid 总量、不进任务名下，就是这两套口径各干各的：uid 计数不过问归属，任务点名只认 Footprint。

所以探针「3 进程」和「400 告警一次没响」不是两套账对不上，是**同一份漏账被两处各读了一次**。

---

## 5. 数不到与清不掉：同一套归属，两条回收路径还各加了一刀

`Sweep` 与 `Footprint` 共用 `classify` + 名册（`footprint.go:1-6, 222-225`）。名册里没有这 450 个、pgid 又不对，`Sweep` 就算跑了也是 0。

现场实际跑到的回收路径比「Sweep 漏看」还多两层，必须分开写，避免把不同原因捏成一个。

### 5.1 `handleResult` → `Sweep`：调用了，但被「执行者还活着」拒掉

`manager.go:2576` 在 Publish 之后 `m.sweep(taskID)`。探针 10:11:25 日志原文：

```
执行结果事件  task=35251ad9  ok=true
任务状态迁移  from=running to=waiting_review
执行者仍存活，拒绝清扫  pid=76057
清扫时执行者仍存活，交由常规回收路径
```

`Sweep` 在锁还在时直接返回 `ErrExecutorAlive`（`footprint.go:227-229`）。grok serve 回合结束后继续活着，这是设计，不是这次才坏。所以「终态即清扫」在 grok 回合成功这条路上**清的是 0，而且根本没枚举那 450 个**。

### 5.2 `handoff done`：不走 `Sweep`，只 `Kill` shim 组

`Manager.Done`（`manager.go:1127-1131`）只 `stopExecutor`。探针 10:12:05 日志是 `grok 停止任务` / `grok 任务已停止`，对应 `internal/executor/grok/adapter.go:286-309` → `r.proc.Kill()` → `prochost.Kill` → `killGroup(76057)`。

`Kill(-76057)` 带不走 pgid `76332`。`Done` 全文没有 `SweepTaskProcs`。所以即便名册里当时写满了 450 个 sleep，**这条 done 路径也不会去点名杀它们**。本次名册是空的，两层叠加：就算补上「done 之后再 Sweep」，按盘上这份名册也还是 0。

计划补测「done 前 450 / done 后 450」与上面两条路径一致。

---

## 6. 结论：(B) 真实缺口

### 6.1 为什么不是 (A)

B72 / `classify` 注释确实把「Bash 工具 setsid 出组」写成了已知定界，并声明逃逸层交给名册（`footprint.go:82-95`）。B72 spec §4.3 也写了 15s 漏记窗口。

但「已知盲区」盖不住这次，理由有三：

1. **名册的自我辩护与这次漏掉的对象矛盾。** `shim.go:46-47` 说真正堆积的是活得远超 15s 的编译/测试进程。这 450 个活了 900 秒，漏是因为**工具壳**只活了约 1 秒，全量覆盖名册把已经 reparent 的长命后代当成「早退的短命进程」扔掉（`roster.go:10-11`）。
2. **B93 事故形态不在这个盲区里**（见 §3），所以「事故当时 Footprint 是好的」不能证明「B93 给看门狗接上 Footprint 就能挡住 `sleep &`」。B93 ledger 自己写的复验命令与本次探针同构，按现实现过不了 400 档。
3. **`done` 根本不 Sweep。** 即使把名册修到能看见这 450 个，当前 `Done` 仍只 `Kill` shim 组。数与清在 Footprint/Sweep 上是孪生的，在 `done` 归档路径上不是。

### 6.2 缺口边界（什么样的任务会掉进去）

同时满足：

1. 进程是 executor **Bash 工具**拉起的（grok 1.0.3 与 opencode 都是每条命令新会话；claude/codex 本次未证）；
2. 工具命令很快返回，但用 `&` / 等价方式留下长命子进程（fire-and-forget）；
3. 子进程继承的是**工具壳的 pgid**，不是 shim 的 pgid；
4. 工具壳退出后子进程被 reparent 给 init/launchd，后续 15s 全量名册再也闭包不到它们。

典型命令就是本次探针和 B93 复验配方：`for i in $(seq 1 N); do sleep T & done`。

**不会**掉进去的（与 B93 事故同类）：一直留在 shim 进程组里的扇出（opencode 内部/子 agent/`go test` 若未 setsid）。那些 `classify` 能数到，`Kill(-shim)` 能收掉。

uid 围栏（B73）仍然把它们算进 835/2666，只是**不能按任务归因**，所以 400/1200 两档都不会响。

### 6.3 最小修法（只描述，本任务不动手）

要让「看门狗点名」和「终态清扫」都够得着这一类，至少两处，缺一不可：

1. **名册改为「仍存活的旧条目 ∪ 当前 ppid 闭包」，不要整份覆盖；并把采样间隔压到约 1s。**
   只改间隔不够：采到之后下一轮仍会抹掉。只改并集不够：1–2 秒的工具壳窗口打不中 15s tick。并集的删除条件必须是「pid 不在当前进程表」或「`StartedAt` 对不上」（沿用现有宁漏勿错），**不能**是「当前从 shim 沿 ppid 走不到」。可选加强：名册顺带记下见过的后代 pgid，`Footprint`/`Sweep` 把「活着且 pgid 命中、启动时刻 ≥ 该组长」的进程并进去——这样只要在工具壳活着时采到过组长 `76332`，壳退出后同组 sleep 仍认得出。

2. **`Manager.Done` 在 `stopExecutor`/`Reap`（`Kill` 组）之后调用一次 `SweepTaskProcs`。**
   否则名册修对了，归档路径还是只杀 shim 组。`handleResult` 里那次 Sweep 对 grok 成功回合会继续撞 `ErrExecutorAlive`，不能指望它收这类孤儿。

不建议用「ppid==1 且启动时刻 ≥ shim」去扫——那是 B47 误杀面，比诚实漏记更糟。

### 6.4 本任务明确没去取的证据

- `76332` 当时的 `command=`（进程已退）。
- `76075`、`d912b23a` 名册里那 5 个额外 pid 的 `command=`。
- B93 那 2100 个当时的 `ps` 行（规格书也没采）。
- claude / codex Bash 是否每条命令新进程组。

以上不影响 (B)：grok 工具壳的 setsid、名册终态只有 2 条、`Footprint` 共用、`done` 只 `Kill` 组，这四件都有原文。
