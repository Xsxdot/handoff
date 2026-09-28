# B383 plan：Wave 0（S3 被拒竖切）+ 批次执行清单

**依据**：specs/b383.md v5（已批准 2026-09-28）；级别 L3 重档，Wave 0 先行、再按故事拆批、最终集成。本文所有 `文件:行` 为 2026-09-28 读数。
**纪律**：每批独立提交、红→绿（先证红再证绿）、证据落 `docs/superpowers/ledgers/ledger-b383-spec.md`；未经独立 review 的批次不并入「通过」。

---

## 0. 执行序

| 批 | 故事 | 内容 | 缝 |
|---|---|---|---|
| **Wave 0** | S3 | 实时拒绝终局（竞态不变量全套）+ 对账哨兵归因 + manager 消费面回归 | 缝 3（+缝 2 夹具） |
| A | S1 | H1 子会话合成回放红→绿；H2 主会话序 B/C 合成回放红→绿（首个 part 事件即开段） | 缝 2 |
| B | S2a | rm 目标提取器 + scope 感知（**单独一轮**，纯字面形态） | 缝 1 |
| C | S2b+S4 | cd 解释基准（写删共用）+ heredoc 受限闭集；rg/grep 模板 + 维持人工清单显式 Escalate | 缝 1 |
| D | acceptance | 真机：哨兵版本确认、is_child 取证、S2 消费者环境证明 | 真机 |
| E | integrate | 合并功能线 | — |

## 1. Wave 0 实时腿：拒绝终局竞态设计

**目标语义**（spec WP3.1）：已成功送达 opencode 的拒绝确实终结当前父任务回合且无文本时，发 `result{OK:false, FailReason=被拒描述, VoidReason=新档}`，恰一次；API/idle 交错不改变归属；API 失败绝不登记为已拒；等待有界，超时/结果不明须显式诊断；标记不得泄漏到下一回合；父回合有文本则清标记按真实 trailer 分类。

### 机制（runState 新增，全部 turnMu 下）

1. `turnRejectPending map[string]string`——permID→被拒描述。`RespondPermission` 在**发 API 前**登记（decision=reject 时，携带当前回合纪元）；v1 语义「API 成功后才登记」正是竞态根源，作废。
2. `turnRejected []string`——语义收窄为「API 成功的**确认**拒绝」；API 成功时 pending→confirmed（纪元一致才转），失败删 pending（绝不转 confirmed）。
3. `turnEpoch uint64`——`clearTurn` 推进；pending/confirmed 携带纪元，结算时纪元不符即丢弃（治 `clearTurn`（adapter.go:2376-2380）不清 `turnRejected` 的泄漏面）。
4. **有界延迟分类**：`mapIdle` 空文本分支见 pending 非空（应答在飞）→ 不分类，武装延迟分类器（`time.AfterFunc(rejectSettleWait)`，gen= idleGen+armed 标记守卫，时长可配置、测试注入毫秒级）；结算路径更新状态后触发重判。
5. 延迟分类器判定序（turnMu 下，armed+gen 有效才执行）：confirmed 非空 → 被拒 result；pending 仍在（超时未决）→ 诊断 result{OK:false, FailReason=「权限拒绝应答界内未确认」+现场}（不假标已拒）；既无 confirmed 也无 pending（API 失败）→ B21 零文本形态 + FailReason 注明「拒绝应答失败、送达未确认」。
6. **恰一次**：分类消费即置纪元化 classified 标记，mapIdle 四出口（adapter.go:2204-2276）与延迟分类器互斥；重复 idle 走既有 idleGen 去抖（:2149-2176）。
7. 子会话语义：pending/confirmed 任务级；子 idle 不进 mapIdle（acceptForeign 丢弃）→ 子拒绝只登记，父回合继续（有文本/新活动）时 idleGen 自增使延迟候选失效，标记由 clearTurn 清——「父回合空文本终结才按被拒收口，有文本清标记」。
8. `VoidReason` 扩档：`VoidReasonPermissionDenied = "权限被拒，executor 仍在线"`（internal/executor/executor.go:105-110 同段新增）；零文本 B21 路径（adapter.go:2230-2234）顺带补 `VoidReasonTurnDiscipline`。

### 时序矩阵 → 测试（缝 3 全表，先红后绿）

| # | 场景 | 期望 |
|---|---|---|
| T1 | API 结算先于 idle | 被拒 result 恰一次 |
| T2 | idle 先到、界内 settle 成功 | 延迟后被拒 result 恰一次；重复 idle 不重发 |
| T3 | idle 先到、API 失败 | B21 形态 + 送达诊断；无任何被拒标记 |
| T4 | 界内未 settle（超时） | 诊断 result；迟到 settle 零泄漏（纪元守卫） |
| T5 | 父回合有文本（子拒绝后父继续） | 无被拒 result；trailer 正常分类；标记清 |
| T6 | 子拒绝 + 父回合空文本终结 | 被拒 result |
| T7 | clearTurn 三清（pending/confirmed/纪元） | 下一回合零泄漏 |
| T8 | 既有 `TestMapIdleRejectedTurnStillAsks`（adapter_test.go:1612-1628） | 改写为期望被拒 result |

## 2. Wave 0 对账腿：哨兵归因（受所测版本约束）

1. `SessionMessage` 扩 `ToolError string`：最后 terminal tool part 的 `state.error`（api.go:446 同点提取）——adapter 内部模型扩展。
2. `classifyReconciled` row4（reconcile.go:223-233）：`ToolError` **精确命中**哨兵 → 补 `result{OK:false}` + `VoidReasonPermissionDenied`；不命中（版本改文案/普通工具报错/文案缺失）→ 维持 question 现状。哨兵常量单点定义，注释注明「实证自 opencode 单一样本（session_rejectedend.json），版本漂移退化方向=安全侧回兜底 ask」。
3. 回归：`session_rejectedend.json` → result+新档；构造普通工具报错夹具 → question；改文案夹具 → question。
4. **新鲜抓包**：本机 opencode 1.18.32 在位——用既有 probe/抓包机制制造一次真实拒绝（触发 permission.asked → reject → 收 SSE），验证哨兵在 1.18.32 是否成立；结果（成立/失配）记台账；失配则该分支记「未达成」，不判通过。

## 3. manager 消费面（不改代码，断言回归）

被拒 result → `handleResult`（manager.go:3414-3530）：作废挂起工单（审计 VoidReason=新档）→ turn_failed 事件 → waiting_review → continue 可续。测试断言审计文案与状态迁移。

## 4. 后续批次要点（详见 spec §3）

- **A（S1）**：H1 子会话回放（修法：子会话等待显式归属——独立计时或单条 Info 不承载）；H2 序 B/C 回放（修法：首个 part 事件即开段、帧与 Detail 延后入参就绪，不造空段/错归属）。spike3/5 序 A 回归不变。
- **B（S2a，单独一轮）**：rm 独立提取器，纯字面形态；绝对目标解析后限 `Scope.TaskTmpDir`；`$TMPDIR` 仅认任务注入绑定（同名赋值/export/env/unset/命令替换/间接展开/子 shell/包装器一律升级）；相对删除走 cd 基准；逐目标全过；反例集照 spec。
- **C（S2b+S4）**：cd 解释基准（起点 Workdir，逐 cd 解析到允许基准，未知变更不沿用旧基准）；heredoc 受限闭集（引用定界符+无管道/替换/包装器/执行型后继；**消费者环境证明前置**——证明不了只认可信绝对路径形态，S2 不记通过）；rg/grep 纯搜索模板（rg 必带 `--no-config`）；维持人工清单（git checkout --/psql -c/内联 python/rg --pre）建**显式 Escalate** 判据——验收经真实 `handlePermission` 出口断言三态（无可复用→gate 工单；有复用→reuseDecision；均不经 Consult 直批，manager.go:2090-2137）。
- **D（acceptance，真机）**：哨兵版本确认落账、is_child 现场取证、消费者环境证明。
- **E（integrate）**：合并功能线 `cards/B233.1-charter-7`。

## 5. 边界

- 改动面：`internal/executor/opencode/{adapter,api,reconcile}.go`、`internal/executor/executor.go`、对应 _test；不新增跨层通路、零 schema、adapter 不 import permgate。
- 每批完成 → 台账记证据 → 独立 review 通过才计「通过」；任何不变量破坏即停批回 plan。
