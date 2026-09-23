# B380 implement 台账（S1 文档收口）

> 卡 B380 · implement 节点 · 分支 `cards/B380-charter-4`
> 起手 HEAD `e27b7ce0ef34cdbf483e6e15af909863b7fddaad`
> plan `docs/superpowers/plans/b380-plan.md` · 有界文件集：`skills/handoff/SKILL.md` + `README.md` + `docs/roadmap.md`
> 环境：go1.26.1 linux/amd64 · codegraph 在 PATH（`/usr/local/bin/codegraph`）· TMPDIR=`$TMPDIR`
> 边干边落：每个事实即刻追加，不攒回合末。

## 环境与分支（亲跑）

```
$ git branch --show-current && git rev-parse HEAD
cards/B380-charter-4
e27b7ce0ef34cdbf483e6e15af909863b7fddaad
$ which codegraph && echo TMPDIR=$TMPDIR
/usr/local/bin/codegraph
TMPDIR=/root/.handoff/tmp/805c3fc9
```

## T1 门禁（复核冻结实现在场，无改动）

### 1. go build

```
$ go build ./...
BUILD_EXIT=0
```

### 2. 三包子集测试

```
$ go test ./internal/ledger/ -run TestOpenTicketsTerminal -count=1
ok  	github.com/Xsxdot/handoff/internal/ledger	0.611s
LEDGER_EXIT=0

$ go test ./internal/orchestration/ -run TestB380 -count=1
ok  	github.com/Xsxdot/handoff/internal/orchestration	0.300s
ORCH_EXIT=0

$ go test ./internal/approval/ -run TestB380 -count=1
ok  	github.com/Xsxdot/handoff/internal/approval	0.157s
APPROVAL_EXIT=0
```

### 3. codegraph check

```
$ codegraph --repo . check
（fails: []；warns 若干既有 best-dangling / budget-raised / legacy / oversized-package / prefix-family）
CHECK_EXIT=0
```

### 4. 四处 grep（发布点/投影面在位）

```
$ grep -n "hub.Publish" internal/orchestration/facade.go
104:// 为什么 AppendEvent 成功后补 hub.Publish（B380 C-1）：ticket_answered 在客户端
133:		m.hub.Publish(evt)

$ grep -n "func (m \*Manager) approvePermission\|EventTypeTicketAnswered" internal/orchestration/manager.go
2554:func (m *Manager) approvePermission(...)
2577:	if evt, err := m.st.AppendEvent(taskID, proto.EventTypeTicketAnswered,

$ grep -n "ticketAnsweredPayload\|Hooks.Hub\|EventTypeTicketAnswered\|func (c \*Client) consult" internal/approval/client.go
314:func (c *Client) consult(...)
430:	if evt, err := c.hooks.Store.AppendEvent(c.taskID, proto.EventTypeTicketAnswered, ticketAnsweredPayload{
551:type ticketAnsweredPayload struct

$ grep -n "func (s \*Store) OpenTickets\|case evTicketsVoided" internal/ledger/taskstate.go
147:func (s *Store) OpenTickets() ([]OpenTicket, error) {
188:		case evTicketsVoided, "completed", "failed", "archived":
```

### 5. proto.go 注记（只读）

`sed -n '95,120p' internal/proto/proto.go` 读到 `EventTypeTicketAnswered` 注记已含「**会 Publish**（B380）……但在客户端不可交付」。

**T1 结论**：五类门禁全过，冻结实现在场，进入 T2。

## 文档基线 grep（改动前，亲跑）

```
$ grep -n '只入库' skills/handoff/SKILL.md
96:`wait`（只入库）。任务流的集合外全是可动作事件，...

$ grep -nE '逐 seq|watermark.*对账' docs/roadmap.md
（无命中，exit 1）
roadmap_grep_exit=1

$ grep -n 'published to the live' README.md
（无命中，exit 1）
readme_published_exit=1

$ grep -n 'ticket_answered' README.md
435:`ticket_answered`, `permission_auto_allow`, and `permission_reuse`. ...
```

## 图覆盖债（本节点 sym 实测）

```
$ codegraph --repo . sym AnswerTicket
命中 n_store_Store_AnswerTicket（internal/store/store.go）——未命中 Manager.AnswerTicket

$ codegraph --repo . sym OpenTickets
Error: 符号 "OpenTickets" 不在图中（近似候选: []）

$ codegraph --repo . sym EventTypeTicketAnswered
Error: 符号 "EventTypeTicketAnswered" 不在图中（近似候选: []）
```

与 plan §图覆盖债一致；上述符号只用普通路径引用，不带 `#Symbol` 锚。

## T3 断言脚本（预写，改动前应红）

脚本落 `$TMPDIR/b380_s1_assert.sh`（不入仓），内容照 plan §6 T3 逐字。

```
$ bash "$TMPDIR/b380_s1_assert.sh"
ASSERT_EXIT=1          # 首红（改动前）

# 逐条定位（红因 = 目标措辞缺失，非 typo）：
1a 六类事件...唤醒     count=0
1b ticket_answered同样  count=0
1c 会 Publish 进实时流  count=0
2  反面 只入库同行并列   (no hit, exit 1)  # 现状跨行，负断言本就不红；总脚本因 1a/b/c 红
3  README published...  count=0
4  roadmap 逐seq...     count=0
```

首红归因核实：`grep -n '七类\|ticket_answered' skills/handoff/SKILL.md` → `:95` 仍是七类并列；README/roadmap 均无目标措辞。**功能缺失，非 typo。**

## T2 实现（三处文档同步）

有界文件集恰好三份，逐字照 plan §6 改动一/二/三落地：

1. `skills/handoff/SKILL.md`：七类→六类 + `ticket_answered` 单列段（verbatim count=1 核过）。
2. `README.md`：末行后追加 published-to-live 澄清三行；前七行逐字未动（`git diff` 只见 append）。
3. `docs/roadmap.md`：文件末尾追加 `## 来自 B380 spec` 小节（count=1，无重复）。

```
$ git status --short
 M README.md
 M docs/roadmap.md
 M skills/handoff/SKILL.md
?? docs/superpowers/ledgers/2026-09-23-b380-implement-ledger.md
$ git diff --stat
 README.md               | 4 +++-
 docs/roadmap.md         | 5 +++++
 skills/handoff/SKILL.md | 6 ++++--
 3 files changed, 12 insertions(+), 3 deletions(-)
```

改动后断言：

```
$ bash "$TMPDIR/b380_s1_assert.sh"
B380_S1_ASSERT_OK
ASSERT_EXIT=0
```

逐字核验（python count）：`skill_verbatim=1`、`readme_verbatim=1`、README 前七行 old7 仍在、`VERBATIM_OK`。

## 变异复验（T3，手动，不留代码）

**变异前唯一性断言（python count==1）**：

```
skill_old_count("六类事件**不会**唤醒")=1
skill_mut_anchor_count("`ticket_answered` 同样**不会**唤醒")=1
road_b380_count("## 来自 B380 spec…")=1
road_seq_count("逐 seq 连续性对账")=1
UNIQUE_OK
```

**变异 1**：把 SKILL 改动一临时改回「七类……`ticket_answered`……（只入库）」（唯一命中后 replace）：

```
MUT1_APPLIED
$ bash "$TMPDIR/b380_s1_assert.sh"  → MUT1_ASSERT_EXIT=1
m1a=1  m1b=1  m1c=1   # 断言 1a/1b/1c 复红
m1neg=0               # 负断言跨行故不红；总脚本已红
```

还原（同锚 count==1）后：

```
MUT1_REVERTED
$ bash "$TMPDIR/b380_s1_assert.sh"  → RESTORE1_EXIT=0  B380_S1_ASSERT_OK
```

**变异 2**：删除 roadmap B380 小节（唯一命中后 replace）：

```
MUT2_APPLIED
$ bash "$TMPDIR/b380_s1_assert.sh"  → MUT2_ASSERT_EXIT=1
m2road=1   # 断言 4 复红
```

还原（append 回文件末尾，block 事前 `not in` 确认干净）后：

```
MUT2_REVERTED
$ bash "$TMPDIR/b380_s1_assert.sh"  → RESTORE2_EXIT=0  B380_S1_ASSERT_OK
```

**变异后工作树**：`git status` 只剩三份文档 M + 台账 ??，无变异残留；`grep -c '## 来自 B380 spec' docs/roadmap.md` → `1`。

## 代码回归（T3，确认 S1 不误伤）

```
$ go build ./...
BUILD_EXIT=0

$ go test ./internal/ledger/ -run TestOpenTicketsTerminal -count=1
ok  	github.com/Xsxdot/handoff/internal/ledger	0.400s
LEDGER_EXIT=0

$ go test ./internal/orchestration/ -run TestB380 -count=1
ok  	github.com/Xsxdot/handoff/internal/orchestration	0.239s
ORCH_EXIT=0

$ go test ./internal/approval/ -run TestB380 -count=1
ok  	github.com/Xsxdot/handoff/internal/approval	0.142s
APPROVAL_EXIT=0

$ codegraph --repo . check >/dev/null
CHECK_EXIT=0
```

测试范围照 plan：三份文档 grep 断言 + 三包子集回归；**未跑全量**（全量只属集成/收尾节点）。

## 收尾自审

- 每条错误分支有带上下文日志 / 成功路径出口日志：**不适用**——纯 Markdown，无 logger、无导出函数、无运行时行为（plan §12 已声明豁免）。
- 新文件有头注释 / 导出函数文档注释：**不适用**（同上）。
- 触及包测试绿、全量编译过：**是**（本节原文）。
- 与 plan Interfaces 签名一致：**是**——无 Go 符号；Produces 三份文档文本逐字如 plan §6。
- 有界文件集未扩围：**是**（`README.zh-CN.md` 等残余照 §7 不动）。
- 变异已全部还原：**是**（RESTORE1/2 exit 0 + git status）。

## 事实台账收口

- 本节点起手 HEAD `e27b7ce0`，分支 `cards/B380-charter-4`。
- 提交动作与提交后读数见本文件末「提交事实」节（收口时追加）。
- 未跑到结果的项：无（T1/T2/T3 全部亲跑）。跨机真机清单归协调者（plan §11），本节点不执行。

## 提交事实（历史读数）

```
$ git add README.md docs/roadmap.md skills/handoff/SKILL.md docs/superpowers/ledgers/2026-09-23-b380-implement-ledger.md
$ git commit -m "implement(B380): S1 文档同步——ticket_answered 交付性措辞 + roadmap 弃选项落账"
[cards/B380-charter-4 4a8b5998] implement(B380): S1 文档同步——ticket_answered 交付性措辞 + roadmap 弃选项落账
 4 files changed, 257 insertions(+), 3 deletions(-)
 create mode 100644 docs/superpowers/ledgers/2026-09-23-b380-implement-ledger.md

$ git rev-parse HEAD
4a8b5998a1857357cd89b98037aa03eb26231a6b

$ git status --short
（空）

$ git log --oneline -2
4a8b5998 implement(B380): S1 文档同步——ticket_answered 交付性措辞 + roadmap 弃选项落账
e27b7ce0 plan(B380): 关单镜像文档收口——实现计划 + 台账
```

（本段随 amend 一次收进同批提交；amend 后 HEAD 换 hash 是 git 事实，收口判据为工作树干净。）
