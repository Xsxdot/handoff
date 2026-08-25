# ledger-b156.2.5-plan.md

plan 节点台账（B156.2.5 C5·消费恰好一次与注意力读模型）。边干边落：每条确立
的事实一行，含命令与原始输出或结论出处。本台账与产出物
`docs/superpowers/plans/b156.2.5-plan.md` 同批提交。

## 基线事实（2026-08-26，本工作树 cards/B156.2.5-charter @ 1a150604）

- L1: 分支 cards/B156.2.5-charter，HEAD=1a1506040066c30afe1e548df4d00e972e58801d（C4 合入功能线），工作树干净。
- L2: `go test ./internal/collab/... ./internal/ledger/...` → 全 ok（collab 0.805s / ledger 3.534s / ledger/api 1.033s，基线全绿）。
- L3: `go run . graph check --repo . --view cards-B156.2-charter-4` → `"fails": []`、EXIT=0（warn 97 条不作判据）。
- L4: 现状 service.go 读侧五法全是空壳：Pending/Consume/ListRooms/MarkRead/Unread 占位（service.go:128-186 亲读）；Mentions 已实现但**无未消费过滤**（契约 §3.3「未消费提及」缺口，C4 只锁了成员过滤）。
- L5: ledger.Store.now 未导出（store.go:38），collab 测试（package collab）**无法**设置 Store 注入时钟；跨包同步两侧时钟需给 ledger 加导出测试钩子——超出 d_collab 卡边界。裁决：活性翻转测试走假 client（缝#2），expiresAt 与 collab 注入时钟（nowFn 包内变量）取同一可拨源；真实集成用确定性真实时钟场景（+5m 缓冲 / 负 TTL 已过期）。此裁决是 breakdown「夹具把两侧时钟拨到同一可拨源」的合规替代——同一可拨源保持、零 d_ledger 表面。
- L6: 岔口六方案甲已在 ledger 侧锁定（rooms.go:48-54「不校验 msgSeq 是否指向存在的 room_message」）——collab.Consume 对无效 seq 幂等 nil 不落标记。
- L7: UnmergeCard 存在（merge.go:94-110）——「拆回解冻」测试可真跑。
- L8: `proto.RoomSummary` wire 形状冻结（proto/rooms.go:55-68），C5 不改一字；金样本（rooms_fixture_test.go）不含 RoomSummary，C5 零触碰。
- L9: 图容器现状：k_collab_Service 持 m_collab_Service + 9 方法（order 10-19）；k_collab_room_fn 7 函数 + k_collab_room_model 1；客户端接口是单一 model 节点 m_collab_client_LedgerClient，无 per-方法节点。常量（RoomEventType/Kind*）不入图（C4 先例）。

## 判据先在基线跑（探针 zz_plan_probe_test.go，已删未提交）

- P1: 探针 14 支测试（C5 读模型全量，含临时 nowFn 声明）在基线对 stub 跑：
  - **14 支 FAIL**：PendingGroupMention「Pending 应只含…: []」、PendingCardRoom「Pending 应只含…: []」、ConsumeIdempotent「不得产生第二条标记: 0」、ConsumeSecondConsumer「A 应恰一条: 0」、ConsumePayloadTwoKeys「没有消费标记事件」、MentionsExcludes「消费后 Mentions 应清空:[…]」、ListRoomsSorts「列表条目数不符: []」、TerminalSinks「条目数: []」、ReadOnlyFlags「并入卡应只读」、LiveFlip（panic index out of range，stub 空列表）、LiveRealStore、Unmerge「并入卡房间应只读」、MarkReadUnread「未读应 3: 0」、MarkReadPerRoom「卡B 未读应 1: 0」；
  - **1 支 PASS（回归锁）**：ConsumeInvalidSeq——stub 本就无副作用，绿在基线上；红只能在变异上（实现写标记/报错即红）。计划标注为回归锁非红绿锁。
- P2: 探针夹具机制验证：fakeLC 实现 client.LedgerClient 全部 9 方法在 package collab 测试中编译通过；fakeRoomMsg 群房间无卡事件构造正确。

## 实现代码块全绿验证（临时装回，验证后已还原）

- P3: room.go 追加 4 函数 + cursor/cursor.go 新建 + service.go 整文件替换 + readmodel_test.go/cursorfile_test.go 新建 → `go test ./internal/collab/...` → `ok github.com/Xsxdot/handoff/internal/collab 1.062s`。新测试 18 支全 PASS（-v 逐支核过）。
- P4: `go vet ./internal/collab/...` → EXIT=0；`go build ./...` → EXIT=0。
- P5: 变异①（ListRooms 判据 `nowFn()` → `time.Now()`）→ TestListRoomsLiveFlip 红「租约未过期应 live=true … Live:false」；TestListRoomsLiveRealStore 仍绿（真实时钟场景本就该绿，符合预期）。已还原。
- P6: 变异②a（Pending 去掉 consumed 过滤）→ TestPendingGroupMentionAndConsume 红「消费后 Pending 应清空:[…]」；变异②b（Mentions 去掉 consumed 过滤）→ TestMentionsExcludesConsumed 红「消费后 Mentions 应清空:[…]」。已还原。
- P7: 变异③（Consume 跳过 RecordMessageConsumed）→ TestConsumeIdempotentSameArgs 红「不得产生第二条标记: 0」+ TestConsumePayloadTwoKeys 红「没有消费标记事件」。已还原。
- P8: **gofmt 教训复现**：计划代码块初稿有 import 顺序/缩进问题，`gofmt -l internal/collab/` 列出 4 文件（cursor.go/service.go/readmodel_test.go/cursorfile_test.go）——与协调者警告一致，代码块已按 gofmt -w 后的干净形态定稿，收尾清单含 `gofmt -l` 零输出步骤。
- P8b: **计划代码块回灌验证**：从 plan 文档提取全部 5 个 go 代码块按原样装回 → `gofmt -l internal/collab/` 零输出、`go test ./internal/collab/ -count=1` ok、`go vet` EXIT=0（证明 plan 文档的代码块是逐字可执行的 gofmt 干净形态；room.go 追加块以文件末尾直接追加、保留块首空行）。计划新增日志步骤（见下）后同法复验仍全绿。
- P8c: 按纪律「加关键节点日志」对 Pending/Consume/Mentions/ListRooms/MarkRead/Unread 六方法补入口/错误分支/成功路径 slog（保持 Send/Pointer 既有形态），回灌复验 gofmt+test+vet 全绿；变异① 复跑仍红（「租约未过期应 live=true … Live:false」）——日志纯增量不改判据牙。
- P8d: 计划 graph JSON 块回灌验证：best.json 追加块 A + 视图块 B 按原样合并 → `graph check --view cards-B156.2-charter-4` fails=[]（warn 97 与基线一致）。

## 视图增量形状验证（临时应用，验证后已还原）

- P9: best.json containers += k_collab_cursor_fn/k_collab_cursor_Store→d_collab；cards-B156.2-charter-4.json 追加 containersAdded 2 + nodesAdded 9 + edgesAdded 18 → `go run . graph check --repo . --view cards-B156.2-charter-4` → `"fails": []`（warn 97 与基线一致）、`graph validate` EXIT=0。
- P10: 逐容器点数自检：k_collab_room_fn=11（导出 11 函数）、k_collab_room_model=1、k_collab_cursor_fn=1（New）、k_collab_cursor_Store=3（Store+MarkRead+Cursor）、k_collab_Service=11（model+10 方法）——与源码导出符号数逐一相等；bestCoverage assignedContainers 255→257。
- P11: 图增量恢复后基线复验：`graph check --view cards-B156.2-charter-4` fails=[]、`go test ./internal/collab/...` ok（工作树干净）。

## 决策记录

- D1: 未读游标介质=A.1 岔口四方案甲（datadir JSON 文件，tmp+rename 原子写），包 `internal/collab/cursor`；Service.New 冻结签名不动，默认纯内存（拍板 5.4 游标降级缓存），新增 `Service.SetCursorStore(st *cursor.Store)` 供组装点接线（A.1 是移交 plan 附区，介质与并发模型归本卡决策）。C6 组装点需在 server.go 调 SetCursorStore（filepath.Join(cfg.DataDir, "room-cursors.json")）——跨卡注记。
- D2: 活性时钟 = collab 包内变量 `nowFn`（breakdown「包内变量注入，不改构造器」原文）；live 翻转测试经假 client（缝#2）+ 同一可拨源，变异靶①（改读 time.Now()）确定性翻红。真实集成测试用确定性真实时钟场景，不依赖时钟同步。
- D3: Unread 语义 = 该房间 room_message 中 seq>游标的条数（水位语义）；「打开房间即置已读」（MarkRead 到当前最大 seq→0）。游标单调只进不退。
- D4: ListRooms 条目=全部卡房间（含全部终态卡，沉底）+ 各项目 project:<name> + global 恒在；project 群从卡的 Project 集合派生（事件里的群房间若不在卡集合也补入）；LastActivity=最新 room_message 时刻，卡房间无消息回退 UpdatedAt；群标题=项目名、global 标题=「全员」。
- D5: Pending「所绑卡房间」= ListAllCards 中 DriverSession==consumer 的全部卡（含终态/并入卡——只读冻结防新写，不抹旧留言，杜绝无人消费黑洞）；卡级只收 kind==user；群级只要 @ 定向。Coordinator 协调者类消息（escalation 等）不进 Pending。
- D6: Mentions 补未消费过滤（契约 §3.3「未消费提及」）+ 全流分页（ReadAllEvents），与 Pending 共用 ConsumedSeqs。
- D7: Consume 定位消息所在卡（全流按 seq 找，取其 CardID 传给 RecordMessageConsumed）；群级消息传空串（项目级标记）；无效 seq/非 room_message → 幂等 nil 不落标记（岔口六方案甲，C2 已锁账本侧）。

## 自审三查

- 判据先在基线跑：P1 探针 13 红 1 回归绿；P5-P7 变异三靶全红（防假绿）。
- 占位符扫描：计划无 TBD/占位；全部代码块为 P3 验证过的 gofmt 干净形态。
- 跨 task 签名一致性：本卡 Produces（无新契约签名，仅 SetCursorStore 属 A.1 机制）+ Consumes（RoomSummary/RoomMessage/LedgerEvent/ListAllCards/RecordMessageConsumed/DriverLease 等 client 方法逐字对照契约 §3.4）；C6 消费 ListRooms/History/Mentions/MarkRead/Unread/Consume 签名不变。