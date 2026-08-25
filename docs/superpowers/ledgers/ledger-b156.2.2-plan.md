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
- [亲测] 绿灯：`go test ./internal/ledger/api/ -run TestRecordMessageConsumed -v` → 四支全 PASS（ExactlyOnce / GroupMarker / UnknownSeq / Rejects），`ok ... 0.357s`。
- [亲测] 绿灯：`go test ./internal/ledger/ -run 'TestEnsureComment' -v` → 两支全 PASS（WritesOncePerKey / EmptyKeyRejected）。
- [亲测] `go test ./internal/ledger/...` → ok internal/ledger 13.197s、ok internal/ledger/api 0.418s，无回归。
- [变异自验·三发全拦] 每发先 grep 断言命中唯一（count=1×3 处），再改语义不改「有没有用到」，编译过（MUT_BUILD_OK×3）后行为断言：
  1. rooms.go 查重条件取反（`marker.Consumer == consumer`→`!=`）→ ExactlyOnce 红「同参重试后仍应恰 1 条标记，实得 2」；
  2. events.go EnsureComment 查重条件取反（`==`→`!=`）→ WritesOncePerKey 红「同键二次调用应返回 false,nil，得到 (true,<nil>)」；
  3. events.go 整块删除空键守卫 → EmptyKeyRejected 红「空白 dedupeKey 必须报错」。
  三发均已 revert，`git status --short` 干净。
- [亲测] 全量触及包回归：`go build ./...`=BUILD_OK；`go test ./internal/ledger/... ./internal/collab/... ./internal/ledgerstep/...` → 全 ok（collab 0.854s、ledgerstep 6.237s，其余 cached）。
- [亲测] 图闸（裁决四：判据只认 fails）：`go run . graph check --repo . --view cards-B156.2-charter-4` → exit=0，解析 JSON 得 fails=0 warns=97。warns 数不作为判据。视图存在且引用完整，未触碰 target.json/best.json，未造新视图。
- [亲测] 占位残留扫描：`grep "Ticket 0 空壳" internal/ledger/{rooms,events}.go` 零命中；`grep "_ = cardID\|_ = msgSeq\|_ = consumer\|_ = dedupeKey" internal/ledger/*.go` 零命中。
- [亲测] 验收看板补行（裁决五）：`grep -rn "EvMessageConsumed" --include="*.go"` 非测试命中仅 types.go:74 常量定义与 rooms.go:75(查重 SELECT)/rooms.go:98(appendEvent)；appendEvent 发出点全仓恰一处＝internal/ledger/rooms.go:98，位于 Store.RecordMessageConsumed 内。给 C5 的提醒照裁决五转记：Service.Consume 现为 return nil 空壳，填肉时不得绕过账本方法直接 appendEvent。
- [亲测] 日志检查：`grep -n "Println\|fmt.Print" internal/ledger/rooms.go internal/ledger/events.go` 零命中；成功路径（已落账）与幂等跳过路径均有 slog 结构化 Info。
- [偏差台账·六条裁决逐条落实情况]
  1. 裁决一：测试文件落为 internal/ledger/api/api_rooms_test.go（未用 plan:58/:90/:95/:97 的 api_test.go，该名归 C1）；提交命令 git add 路径同步改。
  2. 裁决二：newFixture 返回 (client.LedgerClient, *ledger.Store)，四支测试的 RecordRoomMessage/RecordMessageConsumed 调用一律经接口变量 f；markersOf 改持 *ledger.Store 直查库（st.EventsFromAsc）。三方法均在契约 §3.4 八方法内。
  3. 裁决三：步骤1改为记录现状（api 包 no test files，C1 未在本工作树落地），真判据=四支新测试实现前跑红——实测恰四 FAIL（含裁决六正断言使 UnknownSeq 也红，plan:91 的「三红一绿预期」随之作废）。
  4. 裁决四：图闸只认 fails=0，warns=97 不进验收；实现轮未遇 registry.go/keystone.go（本工作树快照早于 B156.3 回灌），若后续 rebase 见到属正常。
  5. 裁决五：验收看板补行完成（上一条 grep 证据）；C5 提醒已转记。
  6. 裁决六：UnknownSeq 测试补正断言「调用后 markersOf 恰一条」，红灯原文在案。
- [提交切分] b0044ba1 台账开卷；a9813f8a T2.1（rooms.go + api_rooms_test.go）；e917933e T2.2（events.go + events_test.go）；本条随 docs 收尾 commit 入库。
- [自审] 错误分支均带上下文 %w 包裹；成功/幂等路径有结构化日志；新类型 consumedMarker 与导出符号 RecordMessageConsumed/EnsureComment 文档注释齐全；签名与冻结 stub 一字未变（RecordMessageConsumed(cardID string, msgSeq int64, consumer string) error / EnsureComment(cardID, dedupeKey, body, actor string) (bool, error)）。
