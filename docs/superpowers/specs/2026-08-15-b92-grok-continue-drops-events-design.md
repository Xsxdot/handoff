# B92 修复：grok 回合失败后 continue，续接回合的事件被静默丢弃

**日期**：2026-08-15
**分支基线**：`main` @ `1a9bd…`（合入 B92 根因报告之后）
**根因报告**：[2026-08-15-b92-failed-not-transiting.md](../notes/2026-08-15-b92-failed-not-transiting.md)

---

## 1. 根因（报告已用证据钉死，此处只复述结论）

原假设「`failed` 事件落库但状态没迁移」**不成立**。日志显示 seq 660 落库时
`running → waiting_review` 迁移正确发生了。真实机制是三步：

1. grok 回合失败 → `emitFailed`（`adapter.go:350`）先 `emit` 结果事件，**再 `r.closeEvents()`
   把整条事件通道永久关闭**。
2. manager 的 `mediate` 循环从 `evCh` 读，通道一关就退出。
3. 协调者 `continue` → `Manager.Continue` 把状态搬回 `running` → `ad.Send`
   （`adapter.go:247`）在**同一个 runstate** 上 `CallAsync("session/prompt")` 开新回合，
   **既不重建通道也不重启 mediate**。新回合的一切事件在 `emit`（`adapter.go:333`）
   因 `evClosed` 短路被静默丢弃。任务于是停在 `running`，直到 2h 后看门狗落
   `stalled`——而 `stalled` 只唤醒、不修复。

**对照组是这份报告最硬的部分**：3 个 grok 任务（`398259b7` / `054ca06f` / `993e879d`）
failed 之后全部哑火；3 个 opencode 任务（`2abde49c` / `8bf4eee4` / `6864ab0e`）
failed 之后全部被 `continue` 救活并正常走到 `completed`。差异不在上报路径
（grok 的失败同样经 `evCh` → `handleEvent` → `handleResult`），而在**只有 grok 会因
回合失败关掉整条通道**——opencode / claudecode 只在订阅循环/流退出时关。

**这个 bug 是被协调者的正常操作触发的**：不 `continue` 就不会卡。而 `continue`
恰恰是「回合失败后接着干」的标准动作。

---

## 2. 设计：把「回合终结」和「执行终结」拆开

`closeEvents` 现在同时承担两件事，而它们的生命周期根本不同：

| 语义 | 范围 | 失败后还能不能续 |
|---|---|---|
| **回合终结** | 一次 `session/prompt` | **能**——`continue` 就是干这个的 |
| **执行终结** | 整个 grok serve 进程 / ACP 连接 | 不能，运行态已作废 |

现有代码把「回合失败」也当成「执行终结」处理，于是 `continue` 面对的是一具
已经被判死的运行态。修法是按语义拆成两个出口：

### 2.1 `emitTurnFailed`：回合失败，**不关通道**

用于两个 call site，两者的共同点是 **serve 进程还活着**（标本实测 `port 50007`
探活存活）：

- `adapter.go:443` `finishTurn` 的 `res.Err`（回合异常终止，含 ACP `-32603`）
- `adapter.go:451` `finishTurn` 的 `stopReason != end_turn`（拒答、达上限、被取消）

### 2.2 `emitFatal`：执行终结，emit 之后关通道（即现在的 `emitFailed`）

用于三个 call site，三者的共同点是**连接或进程已经没了**：

- `adapter.go:542` `onClosed` 权限应答通道中断
- `adapter.go:550` `onClosed` ACP 连接断开
- `resume.go:275` 看门狗判 serve 进程死亡

### 2.3 一次性语义怎么保住

`emitFailed` 的 doc 注释写明它承担去重：「断开处置、看门狗判死、回合异常三条
路径都可能同时到达，`closeEvents` 保证只有先到者生效」。拆开之后：

- **三条 fatal 路径之间**的去重仍由 `evClosed` 承担，一字不变。
- **回合路径与 fatal 路径之间**：回合失败在前、fatal 在后 → fatal 正常关闭，没问题；
  fatal 在前、回合失败在后 → 回合的 `emit` 被 `evClosed` 短路丢弃，也没问题。
- **回合路径自己**不需要跨回合去重：`finishTurn` 由 `awaitTurn` 一次 `CallAsync`
  调用一次，两个分支互斥。

所以拆开**不削弱**任何既有保证，只是不再把「这一回合结束了」误当成「这个
executor 完了」。

### 2.4 `Send` 的守卫（本设计最关键的一行）

`Send` 在 `lookup` 之后、`CallAsync` 之前加一道判断：**`r.evClosed` 为真时直接返回
包 `executor.ErrTaskNotRunning` 的错误**。

**为什么这一行比 §2.1 还重要**：即便将来又出现某条我们没想到的关通道路径，
这道守卫也会把「静默吞掉一整个回合」变成「`continue` 当场报错、manager 走四级
恢复阶梯」。B92 之所以要 2 小时 + 一次人工排查才被发现，全部代价来自「静默」——
一个明确的错误哪怕语义不完美，也比无声无息好一个数量级。

`ErrTaskNotRunning` 是既有的哨兵（`Send` 里 `CallAsync` 失败那条分支已经在用），
manager 的恢复阶梯以 `errors.Is(err, ErrTaskNotRunning)` 为触发条件，会尝试冷恢复
重建运行态——正是这种情况下该做的事。

---

## 3. 明确不做

- **manager 侧的不变量对账扫描**（报告的方案 B：watchdog 发现「最新事件是终态
  事件但状态不是终态」时告警/修复）。它值得做，且能兜住报告 §2.2 / §2.3 指出的
  两条**真实存在**的「先落事件后迁移、迁移失败」缺口（`Manager.Stop`、
  `reconcileExecutorGone`）。**但它要改 `watchdog.go`，而 B93 正在同一个文件里加
  `scanTaskProcs`**——同时改必然冲突。单开 **B97**，等 B93 落地后再做。
- **grok 凭据链路修复**。报告 §7 查实：`~/.handoff/tasks/<id>/grokhome/auth.json` 是
  指向 `~/.grok/auth.json` 的软链，而该权威文件**不存在**（只剩 `.lock`），于是
  悬空软链 → `-32000 Authentication required` → 所有 grok 新任务派不出去。这是与
  B92 **无关**的另一个真实缺陷（发生在 01:12，晚于两例卡死数小时），单开 **B98**。
  眼下的解法是人工在 mac-02 上 `grok login`。
- **`stalled` 的行为**。它只唤醒不修复是**刻意的**（executor 可能只是在长跑），
  本 spec 不改这条口径。
- **让 `Send` 重建通道并重启 mediate**。报告方案 A 提到过这条更激进的形态。不做：
  重建通道要让 manager 感知「通道换代」，引入旧 `close` 与新 `emit` 的竞态，
  测试面远大于收益——而 §2.1 拆语义之后通道**根本不需要重建**，因为它压根没被关。

---

## 4. 验收

1. **回合失败不关通道**：单测——构造一个 `stopReason=cancelled` 的回合终局，
   断言 ①`result{OK:false}` 事件被投出；②`evCh` **仍未关闭**；③随后一次 `Send`
   能成功发起新回合，且新回合产出的事件**能从 `evCh` 读到**。
   `res.Err` 那条分支同样来一遍。
2. **执行终结仍然关通道**：三条 fatal call site 各一条单测，断言 `evCh` 关闭。
3. **`Send` 守卫**：先走一条 fatal 路径关掉通道，再 `Send`，断言返回的 error
   满足 `errors.Is(err, executor.ErrTaskNotRunning)`，且**没有**发出 `session/prompt`。
4. **回归**：`go build ./... && go vet ./... && go test -count=1 ./...` 全过。
   既有的 `watchdog_internal_test`、`onclosed_drop_internal_test` 等必须原样通过——
   它们钉的是 fatal 路径的行为，本次不该动到。
5. **变异测试**：把 `emitTurnFailed` 改回调 `closeEvents()`，第 1 条必须 FAIL；
   摘掉 §2.4 的守卫，第 3 条必须 FAIL。两条各自单独红。
6. **真机复验**（`go test` 之外，必须做，但**不由 executor 执行**）：mac-02 上
   `grok login` 恢复凭据后，派一个 grok 任务，故意拒一次权限让回合以
   `stopReason=cancelled` 收尾，然后 `continue`，确认续接回合的事件能到达协调者、
   任务最终走到 `waiting_review`。**标本任务 `398259b7` 是现成的复验对象**——修好
   之后对它 `resume --force` 收口即可，不必再造一个。

---

## 5. 风险

**「回合失败后运行态还活着」是本次引入的新状态。** 以前回合一失败运行态就作废，
现在它会留在 `runs` 表里等 `continue`。如果 serve 其实已经半死（进程在、但不再
响应），这条运行态会一直占着，直到看门狗探活判死走 fatal 路径。**这正是看门狗
存在的理由**（`resume.go:275`），且 fatal 路径不变，所以兜底是现成的——但要意识到
从「立刻作废」变成了「等看门狗」，最坏情况下多等 `watchdogFailThreshold` 轮。

**`reconcileExecutorGone` 的触发时机变了。** 它由 mediate 循环退出触发（报告 §3 第 5 步：
「中介循环结束，开始对账」）。回合失败不再关通道 ⇒ 那一刻 mediate 不退出 ⇒ 对账
不再在此触发。对账在标本里走的是「任务无需状态对账，仅清扫残留」分支，也就是
**只清扫了残留进程**——而 **B93 §3.1 正在给 `handleResult` 加清扫**，恰好补上这个位置。
两条修复是互补的，实现时要在注释里点明这层关系。B93 若因故不落地，这里会留下一个
「回合失败后残留进程无人清扫」的窗口，届时须在本处补一次清扫。
