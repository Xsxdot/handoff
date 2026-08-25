# 台账：B156.2.2（C2 d_ledger·事件写入点补肉）plan 轮（2026-08-26，边干边追加）

- 分支 cards/B156.2.2-charter，HEAD=b4231fb5（工作树 clean 起步）。
- [亲读] 契约 `docs/superpowers/specs/b156.2-contract.md` 含 §11 微修订：client.LedgerClient 8 方法（新增 ListAllCards）、cardWire 入参已改 ledger.CardView、GetCard 包空视图 Following 恒空（§11.4 冻结语义）。与 api.go:29-37/57-70/106-130 亲读一致。
- [亲读] 两处 Ticket 0 空壳确认：internal/ledger/rooms.go:33-38 RecordMessageConsumed（return nil，零行为）、internal/ledger/events.go:232-238 EnsureComment（return false,nil，零行为）。
- [亲读] ClearNeedsHumanFrom 同形先例=internal/ledger/events.go:312-340（mutate 事务内查后写、actor 列比对）；AnswerDecision 同事务查改写先例=decisions.go:66-89；mutate 单写者串行化依据=store.go:152-166（PG advisory lock + SQLite 单连接）。
- [亲读] card_events schema：payload SQLite TEXT / PG JSONB（store.go:225-229, 293-297）；EventsFromAsc 以 string 扫 payload 两方言通吃（events.go:88）→ 计划中的载荷查重走「SQL 粗筛 + Go 解载荷精比」，不用方言 JSON 函数。
- [亲测] `go build ./...` → BUILD_OK。
- [亲测] `go test ./internal/ledger/...` → ok internal/ledger 12.707s；api 包 [no test files]。基线绿。
- [亲测] `go run . graph check --repo . --view cards-B156.2-charter-4` → fails=0 warns=97，与协调者报文「fails=0 warns=97」逐字一致。
- [亲测] 裸 `graph check`（无视图）→ 「契约对照发现 4 处违规」exit 非零，dead-contract d_collab→d_ledger / d_collab→d_protocol / d_ledger→d_protocol 等——与协调者预告的 absorb 前固有形态一致，不修。
- [探针实测] 在 internal/ledger/api/ 写临时 zz_probe_test.go（计划 T2.1 测试代码原形），`go test ./internal/ledger/api/` 输出原文：
  - FAIL TestRecordMessageConsumedExactlyOnceThroughSeam「首次消费后应恰 1 条标记，实得 0」；
  - FAIL TestRecordMessageConsumedGroupMarkerIsCardless「群级消费标记应为无卡事件且恰一条: []」；
  - FAIL TestRecordMessageConsumedRejectsMissingCardAndEmptyConsumer「不存在的卡必须报 ErrNotFound，got <nil>」；
  - ok  TestRecordMessageConsumedUnknownSeqIsIdempotentNil（岔口六方案甲语义锁今天即绿，预期内）。
  → 证明：夹具可编译可跑；缝级断言今天写下去确实红（最薄路径条成立）；红转绿的唯一路径是给 RecordMessageConsumed 补肉。探针文件已删除，未入提交。
- [判断] RecordMessageConsumed 查重键=(msgSeq, consumer)，seq 全局唯一故 cardID 不参与查重；marker 的 actor 列存 consumer（粗筛列）+载荷两键（权威），与契约 §4「payload 含 message_seq 与 consumer 两键」一致且不新增冻结外形状。
- [判断] EnsureComment 查重范围=同卡内 EvComment 载荷 dedupe_key 相等；空 dedupeKey 报错（MarkNeedsHuman reason 守卫同形 events.go:281-283）；msgSeq 不做存在性校验是岔口六方案甲的选择，测试+注释锁死。
- [判断] 缝级断言落点：RecordMessageConsumed 断言从 api.Facade 进入（spec 测试接缝清单 #2 的真实实现侧，Facade.EventsFromAsc 可数标记，全断言缝可达）；EnsureComment 无声明缝承载（LedgerClient 方法集契约 §3.4 冻结不含它；RunOnce 接线归 C3）——内部锁理由按纪律块格式在 plan 文档声明。
- [判断] 与 C1（B156.2.1）文件交集：生产文件零交集；唯一潜在交集=codegraph/diffs/cards-B156.2-charter-4.json（仅当任一侧实现轮需要修正锚点时才动，本卡默认零视图改动）；rebase 规则见 plan 文档 §6。
- [判断] 与 C1（B156.2.1）文件交集：生产文件零交集；唯一潜在交集=codegraph/diffs/cards-B156.2-charter-4.json（仅当任一侧实现轮需要修正锚点时才动，本卡默认零视图改动）；rebase 规则见 plan 文档 §6。
- [探针实测] T2.2 测试块原形写入临时 internal/ledger/zz_probe2_test.go，`go test ./internal/ledger/ -run 'TestZZProbeEnsureComment' -v` 输出原文：--- FAIL TestZZProbeEnsureCommentWritesOncePerKey、--- FAIL TestZZProbeEnsureCommentEmptyKeyRejected（两支均编译通过、对 stub 跑红）。探针已删除。
- [复核] 探针删除后 `git status --short` 仅剩两个法定产出物（plan + 台账），工作树无残留。
- [产出物] docs/superpowers/plans/b156.2.2-plan.md（随本提交入库）。

# 实现轮台账（2026-08-26，边干边追加）

- 分支 cards/B156.2.2-charter-2，HEAD=0fe320df，工作树 clean 起步。跨卡审计六条裁决已并入执行（偏差逐条记于本节末尾汇总）。
- [亲测] 步骤1基线（裁决三改写后形态）：`go build ./...` → BUILD_OK；`go test ./internal/ledger/api/` → `[no test files]`（C1 尚未在本工作树落地）。真判据见下一条。
- [亲测] 红灯：`go test ./internal/ledger/api/ -run TestRecordMessageConsumed -v` → 恰四条 FAIL（裁决六补正断言后 UnknownSeq 也红）：ExactlyOnce「首次消费后应恰 1 条标记，实得 0」、GroupMarker「群级消费标记应恰一条，实得 0」、UnknownSeq「未知 seq 的消费也应真落恰好一条标记，实得 0」、Rejects「不存在的卡必须报 ErrNotFound，got <nil>」。
- [亲测] 红灯：`go test ./internal/ledger/ -run 'TestEnsureComment' -v` → 恰两条 FAIL：WritesOncePerKey「首次写入应返回 true,nil，得到 (false,<nil>)」、EmptyKeyRejected「空白 dedupeKey 必须报错」。失败原因均为功能缺失非 typo。
