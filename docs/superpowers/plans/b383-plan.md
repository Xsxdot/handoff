# B383 plan：Wave 0（S3）+ 批次清单（对齐 v6 复杂度门）

**依据**：specs/b383.md v6（已批准 2026-09-28）。**复杂度门合规声明**：本 plan 不新增独立定时器（只复用 `idleTimer`/`idleGen` 重新武装）、不做双份拒绝表（单表 + 在飞计数器）、不写通用 shell 解析器、零 schema；「等待有界」由 30 秒 API 截止时间给出，不造等待机制。

## 0. 执行序

| 批 | 故事 | 内容 | 缝 |
|---|---|---|---|
| **Wave 0** | S3 | 实时拒绝终局（竞态不变量）+ 对账哨兵归因 + manager 消费面回归 | 缝 3（+缝 2 夹具） |
| A | S1 | H1 子会话 / H2 主会话序 B/C 合成回放红→绿；spike3/5 回归 | 缝 2 |
| B | S2a | rm 删除目标提取 + scope 放行（单独一轮，小型正例闭集） | 缝 1 |
| C | S2b+S4 | cd 后相对写按实际目录判 + heredoc 受限闭集；grep/rg 纯搜索参数例外 | 缝 1 |
| D | acceptance | 真机：哨兵版本确认、is_child 取证、裸 cat 消费者环境证明 | 真机 |
| E | integrate | 合并功能线 `cards/B233.1-charter-7` | — |

## 1. Wave 0 实时腿（单表 + 计数器 + 复用 idle 去抖 + 30s API 截止）

现状：`RespondPermission`（adapter.go:702-775）API 成功后才 `noteRejected`（:771-789），SSE idle 可在另一 goroutine 抢先消费空回合（:2199-2239），`clearTurn`（:2376-2380）不清标记。

1. **登记前移（单表）**：runState 的拒绝标记改 `map[string]string`（permID→描述，取自 `permText`，拼接保持确定性）；`RespondPermission` 在**发 API 前** turnMu 下登记，并递增新计数器 `rejectInFlight`。API **成功**→标记保留（=确认已拒）；**失败**（含超时）→按 permID 摘除、绝不留作已拒；两种结算都递减计数器（下限 0）。现 :771-773「成功后才登记」路径删除。
2. **30 秒截止**：`r.api.RespondPermission` 的 ctx 包 `context.WithTimeout(30s)`（常量 `respondPermissionTimeout`）；超时=失败，走既有 manager `NoteDeliveryFailed`/重投分支（manager.go:2872-2877）。
3. **在飞延迟分类（复用 idle 去抖）**：`mapIdle` 空文本且 `rejectInFlight>0` → 不分类，用**现有** `idleTimer`/`idleGen` 重新武装一次 idle 分类，延迟 = 新常量 `rejectSettleGrace`（默认 31s > 30s 截止；测试注入毫秒级，模式照抄 `idleGrace` :74/:134/:193）。到期重跑 `mapIdle` 按当时状态判：标记在 → 被拒 result；标记已摘 → B21 零文本路径。重武装至多一次（截止保证第二次可判）；防御分支仍不可判 → B21 + 诊断文案。不新建 goroutine/channel/定时器类型。
4. **被拒终局改语义**：空文本 + 标记在且不在飞 → 发 `result{OK:false, FailReason=「回合因权限被拒终止：」+被拒描述, VoidReason=VoidReasonPermissionDenied}`（复用 `rejectedTurnQuestion` 的描述组装）；**不再发合成 question**。advanceWatermark/clearTurn/takeAskedViaTool/captureStartCommit 照旧。
5. **clearTurn 补清**：拒绝标记表置空、计数器归零——修迟到登记泄漏；父回合有文本走 trailer 分类后 clearTurn 清标记（「父回合有文本则清」）。
6. **VoidReason**：`internal/executor/executor.go:105-110` 增 `VoidReasonPermissionDenied = "权限被拒，executor 仍在线"`；零文本 B21 result（adapter.go:2230-2234）补 `VoidReasonTurnDiscipline`（消 executor.go:99-104 记载的审计矛盾——普通零文本失败保留自己的理由，不冒充权限被拒）。
7. **子会话**：子 idle 照旧被 acceptForeign 丢弃；标记任务级——父回合有文本清标记，空文本终结按被拒收口。「子拒绝后父回合是否正常终结」是 Wave 0 要验证的承重未知：若父回合始终无终局，停批回 spec（v6 §3 Wave 0 停线条件）。

## 2. Wave 0 对账腿（哨兵归因，受所测版本约束）

1. `SessionMessage`（api.go:325-339）扩 `ToolError string`：:445-446 同点从最后 terminal tool part 的 state 提取 error 文案（State 结构补 Error 字段，json tag `error`）。
2. `classifyReconciled` row4（reconcile.go:223-233）：`ToolError` **精确命中**哨兵常量（`"The user rejected permission to use this specific tool call."`，单点定义，注释注明「实证自单一样本 session_rejectedend.json，未记录版本，漂移退化方向=安全侧回兜底 ask」）→ 补 `result{OK:false}` + `VoidReasonPermissionDenied`；不命中 → 维持 question 现状。
3. 回归：`session_rejectedend.json` → result+新档；普通工具报错/改文案夹具 → question。
4. **新鲜抓包（时间盒 30 分钟）**：本机 opencode 1.18.32 在位——用既有 probe/回放基建尝试一次真实拒绝抓包验证哨兵；做不成如实记「留 acceptance」，不假报。

## 3. manager 消费面（不改代码，断言回归）

被拒 result → `handleResult`（manager.go:3414-3530）：作废挂起工单（审计 VoidReason=新档）→ turn_failed 事件 → waiting_review → continue 可续。

## 4. 测试矩阵（红→绿；旧 `TestMapIdleRejectedTurnStillAsks` adapter_test.go:1612-1628 改写为期望被拒 result）

| # | 场景 | 期望 |
|---|---|---|
| T1 | API 结算先于 idle | 被拒 result 恰一次 |
| T2 | idle 先到、界内 settle 成功 | 延迟后被拒 result 恰一次；重复 idle 不重发 |
| T3 | idle 先到、API 失败 | B21 零文本形态；无被拒标记 |
| T4 | API 超时（httptest 挂起） | 失败路径；迟到结算零泄漏 |
| T5 | 父回合有文本（标记在场） | 正常 trailer 分类；标记清；下一回合零泄漏 |
| T6 | 子会话拒绝 + 父回合空文本终结 | 被拒 result |
| T7 | clearTurn 三清 | 下一回合零泄漏 |
| T8 | 对账哨兵命中/失配/普通报错 | result+新档 / question / question |

## 5. 后续批次要点

- **A（S1）**：H1 子会话回放（修法：等待计入正确工具段或单条 Info 声明不承载）；H2 序 B/C 回放（修法：首个 part 事件即开段、帧与 Detail 延后，不造空段/错归属）。等待归属要断言计时数据，不只数日志。
- **B（S2a，单独一轮）**：rm 独立提取；仅 TaskTmpDir 直接目标或可证明 cd 已进入该目录的相对目标；`$TMPDIR` 须仍是任务注入值（重绑定/展开/包装器不放宽）；逐目标全过；反例集照 spec §S2。
- **C（S2b+S4）**：cd 后相对写按实际目录判（redirect.go:8-17 的误放行残余一并修）；heredoc 同构闭集（引用字面定界符、单 cat 写 scope 内文件、无管道/执行正文后继）；`grep` 纯搜索参数与显式 `rg --no-config` 纯搜索参数排除 handoff 字样。人工清单（git checkout --/psql -c/内联 python/rg --pre/越界 Paths 模块缓存读）保持 Escalate 语义：验收经 `judgeBash`→`handlePermission` 三出口断言——无可复用 allow 建 gate 工单、有复用走 reuseDecision、均不经 Consult 模型直批（manager.go:2090-2137）。
- **D（acceptance，真机）**：哨兵版本确认、is_child 取证、裸 cat 消费者环境证明（证明不了则 S2 收窄到可信绝对路径，原始案例不算通过）。
- **E（integrate）**：合并功能线。

## 6. 边界

改动面：`internal/executor/opencode/{adapter,api,reconcile}.go`、`internal/executor/executor.go`、对应 _test；不动 web/ 与 permgate（Wave 0 不含判据改动）；不新增跨层通路、零 schema、adapter 不 import permgate。每批红→绿证据落 `docs/superpowers/ledgers/ledger-b383-spec.md`，独立 review 通过才计「通过」。
