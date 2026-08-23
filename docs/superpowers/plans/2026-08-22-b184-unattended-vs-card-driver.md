# B184 实现计划：无人值守标注要对账卡的驱动归属

> 2026-08-22。协调者写。卡：B184（中）。相邻发现另开卡 B189。

## 事实基线（协调者在 985f37135 上查证）

现象：B177 的 contract 轮挂 `waiting_answer` 数小时，期间每个新开/恢复的协调者
会话都按恢复纪律（扫 `watchers == 0` 的活跃任务）向用户播报同一个任务。

判定逻辑在 `cmd/status.go:308`：

```go
func unattended(a proto.ActiveTask) bool {
	if a.Watchers == nil || *a.Watchers > 0 {
		return false
	}
	switch proto.TaskState(a.State) {
	case proto.TaskStatePending, proto.TaskStateRunning, proto.TaskStateWaitingAnswer:
		return true
	...
```

它只看**订阅数**。而「有没有人在推这件事」的答案在**卡**上（`DriverSession`），
一个正在驱动的会话完全可能此刻没挂订阅（刚重启、正在别的事上、订阅断了还没补）。
于是「真孤儿」与「有人在推、只是没挂订阅」被压成同一格。

**一个决定性事实，直接否掉了「查心跳新鲜度」这个直觉修法**：
`internal/ledger/tasks.go:117` 的 `HeartbeatDriver` **全仓没有生产调用点**
（`grep -rn "HeartbeatDriver" --include="*.go" .` 只命中定义与测试），
而 `driverLeaseTTL = 5 * time.Minute`（tasks.go:15）。也就是说心跳只在认领那一刻
写一次，**5 分钟后必然过期**。拿「心跳新鲜」当降噪判据，只能降噪 5 分钟。
这个缺陷本身另开了卡 B189，**本卡不修它**。

反查挂账的现成能力：`internal/ledger/tasks.go` 已有 task → card 的反查
（`CardOfTask`，先跑 `grep -n "func (s \*Store) CardOfTask" internal/ledger/tasks.go`
确认精确签名，按实际的来）。卡在**协调者本机**的账本里，任务在（可能远端的）
agentd 上——所以这次对账天然发生在 CLI 侧，不需要动 agentd 协议。

## 设计决定

1. **不做二值判断，做归属展示**。三格取代原来的两格：

   | 情形 | 行末 |
   |---|---|
   | `watchers > 0` | 什么都不加（照旧） |
   | `watchers == 0` 且挂账卡有 `DriverSession` | `⚠ 无人订阅（卡 <id> 驱动 <session>，心跳 <时长>前）` |
   | `watchers == 0` 且无挂账卡 / 卡无驱动 | `⚠ 无人值守`（照旧，这是真孤儿） |

   why 不直接把有驱动的那格静音：驱动信息会腐坏（B189），静音等于用一个会
   过期的事实去掩盖一个真实的风险。把归属摆出来，判断交给看的人——这也正是
   播报纪律要收紧的那一格。
2. **账本不可用时静默退回旧行为**：账本是可选功能（`ledger.enabled`），打不开
   账本、卡查不到、反查报错，一律按原来的两格走，**不打警告**——status 是体检
   命令，不该因为一个可选子系统缺席就吵。
3. **纪律块同批改**：`skills/handoff/SKILL.md` 的会话恢复一节，把「给每个
   `watchers == 0` 的活跃任务补订阅并播报」收紧成「只对**真孤儿**播报；有驱动
   归属的补订阅但不打扰用户」。代码改了而纪律没改，下一个会话照样按旧纪律刷屏。

## Task 1：CLI 侧对账

`cmd/status.go`：

- `unattended(a)` 拆成两个函数：`attendance(a, lookup)` 返回一个小结构
  （`{Unattended bool, CardID, Driver string, HeartbeatAge time.Duration}`），
  原 `unattended` 的状态过滤逻辑原样保留在里面。
- 卡查询经**注入的函数**做（`lookup func(taskID string) (cardID, driver string,
  heartbeatAt time.Time, ok bool)`），不要在 `attendance` 里直接开账本——决策
  逻辑要能单测，副作用留在装配处。
- 渲染循环按上表三格拼行。心跳时长用整分钟粒度（`12m 前`），零值心跳打 `未知`。
- 装配处（`status` 的 RunE）用本机账本构造 lookup；`openLedger()` 失败时传 nil，
  `attendance` 对 nil lookup 走旧行为。

## Task 2：恢复纪律收紧

`skills/handoff/SKILL.md` 的「会话恢复：从零接管」一节：第 2 步补上——
补订阅照旧对所有 `watchers == 0` 的活跃任务做；**向用户播报**只对
「无挂账卡或卡无驱动」的那一类做，有驱动归属的在恢复报告里最多列一行事实
（「卡 B177 由 <session> 驱动，已补挂订阅」），不作为需要用户处置的事项。

## 测试映射

`cmd/status_test.go`（先 `grep -rn "unattended" cmd/*_test.go` 找既有用例，
它们是回归网，断言不许改）新增：

1. `TestAttendanceMarksTrueOrphan`：`watchers=0`、lookup 返回 `ok=false` →
   `Unattended` 为真（旧行为不变）。
2. `TestAttendanceReportsCardDriverInsteadOfOrphan`：`watchers=0`、lookup 返回
   驱动会话 + 12 分钟前的心跳 → `Unattended` 为假，且结构里带卡号、会话名、
   心跳时长；渲染出的行含 `无人订阅` 与卡号，**不含** `无人值守`。
3. `TestAttendanceIgnoresLedgerWhenLookupNil`：lookup 为 nil 时与用例 1 同结果
   （账本不可用的降级路径）。
4. `TestAttendanceKeepsWatchedTaskSilent`：`watchers=1` 时三格都不加（回归网）。

变异自检：把 lookup 的返回值忽略掉（永远当 ok=false），用例 2 必须转红。

## 测试范围

- `go test ./cmd/`
- `go build ./...`、`go vet ./...`、`gofmt -l .` 无输出

## 不属于本次

- **不修驱动心跳无人续期**（B189）。看见 `HeartbeatDriver` 没人调也不要顺手接上——
  它牵涉租约语义与抢占规则，值一次独立设计。
- 不改 agentd 的 status 协议、不给 `proto.ActiveTask` 加字段（对账在 CLI 侧做）。
- 不改看板/控制台的任何展示。
