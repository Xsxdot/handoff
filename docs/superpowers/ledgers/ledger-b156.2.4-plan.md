# ledger-b156.2.4-plan.md

plan 节点台账（B156.2.4 C4·Send 白名单执法全矩阵 + Pointer 实现）。边干边落：
每条确立的事实一行，含命令与原始输出或结论出处。本台账与产出物
`docs/superpowers/plans/b156.2.4-plan.md` 同批提交。

## 基线事实（2026-08-26，本工作树 cards/B156.2.4-charter @ c2e91ca0）

- L1: 分支 cards/B156.2.4-charter，HEAD=c2e91ca0a5e4cb37124934e1ec1e143f06dd7fd8，工作树干净。
- L2: `go test ./internal/collab/...` → `ok github.com/Xsxdot/handoff/internal/collab 0.799s`（基线全绿）。
- L3: `go run . graph check --repo . --view cards-B156.2-charter-4` → `"fails": []`、EXIT=0（2026-08-26 实测两遍，第二遍 grep 到 fails 空原文）。
- L4: 基线 collab 图节点：m_collab_Service + 九个 Service 方法 + m_collab_client_LedgerClient（baseline.json 亲读）；resolveRoom/kindAllowed/isGroupRoom/sameRoom/mentionsMember/unmarshalRoomMessage/protoRoomEventType 均不在图（未导出函数不被追踪）。
- L5: 契约 §11.4「Card.Following 仅在列表方法上有值；GetCard 恒空」亲读；GetCard→裸 Card 无 Following 投影（internal/ledger/cards.go:410-416 返回 scanCard 裸 Card）。
- L6: C1 已落地：ClaimCardAs/RebindDriver/DriverLeaseOf 等真实实现（internal/ledger/binding.go 亲读），service_test 夹具可经 Store 直接绑定。

## 判据先在基线跑（探针，zz_plan_probe_test.go，已删未提交）

- P1: 十支测试探针在基线对 stub 全红（P1-P10 FAIL），唯一绿的是群房间空 actor 回归钉（P11 PASS）。红文摘录：
  - P4 user actor==绑定值 `got <nil>`（现状放行的洞）
  - P5 并入房间 `got <nil>`（现状无并入只读）
  - P6 换绑剥权 `got collab: 消息形态不在白名单`（占位）
  - P7 Pointer `Pointer seq 必须为正，got 0`（空壳）
  - P9 终态 Pointer `got <nil>`（空壳）
  - 完整红文在 b156.2.4-plan.md §3 T4.1 步骤 3（探针已删，红文原文已抄入 plan 台账段）。
- P2: 探针验证夹具机制可行：CreateCard 父子链（B1→B1.1→B1.1.1）、ClaimCardAs、MergeCards、CloseCard、RebindDriver 全部在真 SQLite 上跑通。

## 实现代码块全绿验证（临时装回，验证后已还原）

- P3: room 子包 + service.go 改写 + 测试文件按 plan 代码块全文临时装回 → `go test ./internal/collab/...` → `ok`（EXIT=0，1.826s）。
- P4: `go vet ./internal/collab/...` → EXIT=0；`go build ./...` → EXIT=0。
- P5: 变异靶①（Pointer 函数体改回 `return 0, nil`）→ `go test ./internal/collab/ -run TestPointer` 四支全红（WritesPointerMessage「seq 必须为正，got 0」/Overrides「<nil> seq=0」/ReadOnlyRoom「got <nil>」/UnknownRoom「got <nil>」）。已还原。
- P6: 变异靶②（VerifyWriter default 分支绑定比对改恒假）→ TestSendCoordinatorKindsRequireBinding 与 TestSendRebindRevokesOldSession 红（「当前绑定者可发，got err=collab: 书写者与房间身份不符」）。已还原。

## 视图增量形状验证（临时文件，验证后已删除/还原）

- P7: 临时视图 codegraph/diffs/plan-probe-tmp.json（cards-B156.2-charter-4 复制 + containersAdded k_collab_room_fn/k_collab_room_model + nodesAdded 八符号）→ `go run . graph check --repo . --view plan-probe-tmp` → `"fails": []` EXIT=0；`graph validate --repo .` EXIT=0。临时视图已删。
- P8: best.json 临时加 k_collab_room_fn/k_collab_room_model→d_collab 后 `check --view cards-B156.2-charter-4` 仍 fails=[] EXIT=0；已还原（best.json.bak 比对零改动）。
- P9: room.go 符号行号（按最终代码块排版）：Room:36、Resolve:55、VerifyWriter:102、KindAllowed:149、IsTerminalStatus:161、SameRoom:188、MentionsMember:200、UnmarshalMessage:214（视图 diff 用）。

## 决策记录

- D1: 子包命名 `internal/collab/room`（执法内核）；C5 游标子包预留 `internal/collab/cursor`（C5 建包）。沉放清单见 plan §7。
- D2: 哨兵下沉 room 子包、根包再导出（根包依赖子包，不可反向，哨兵只能单点定义在子包）；collab.ErrXxx 契约符号不变。
- D3: room.Resolve 取数用一次 ListAllCards("") 全量投影（GetCard 无 Following，契约 §11.4 唯一枚举源）；返回哨兵直接不包裹（既有测试用 == 比较）。
- D4: 岔口十最窄读法实现：user 类相关卡={该卡, 直接父}（卡有父时额外拒直接父绑定值），群房间仅要求非空。实现按此写测试，协调者否了就放宽（plan §4 测试范围声明已注明）。
- D5: Pointer 落账 actor 固定 `system:pointer`（契约 §3.3 Pointer 无 actor 参数；与既有 TestSendRejectsPointerViaSend 的 actor 用词一致；C7 断言不查 actor）。
- D6: Pointer 不校验 Body 非空（契约无此条；Send 亦不校验 body，保持一致）。

## 自审三查

- Q1: 没亲自跑到结果的命令是否写成结论？无——graph check、go test、探针红绿、变异红、vet/build 全部本机实跑，原文入台账。
- Q2: 本轮碰过 handoff CLI / 起过新 executor？无——仅 `go run . graph check/validate`（只读图子命令）与 go 测试进程。
## 实现轮台账（2026-08-26，本工作树 cards/B156.2.4-charter-2）

- R1: 基线复核：`go test ./internal/collab/...` → ok（EXIT=0）；`go run . graph check --repo . --view cards-B156.2-charter-4` → `"fails": []` EXIT=0（grep 原文在案）。
- R2: T4.1 立红：按 plan 代码块整文件替换 service_test.go 后 `go test ./internal/collab/ -run 'TestSend|TestPointer' -v` → **恰 11 支 FAIL**、其余（竖切/群级/未知kind/pointer拒收/未知房间/空actor/终态只读/群房间非空）保持绿。红文原文：
  - TestSendCoordinatorKindsRequireBinding `kind escalation 非绑定者必须 ErrNotWriter，got collab: 消息形态不在白名单`
  - TestSendRelayAllowsDirectParentWriter `直接父绑定者 relay 必须可发，got collab: 消息形态不在白名单`
  - TestSendRelayRejectsGrandparentAndUnrelated `relay actor=cli:g@h 必须 ErrNotWriter，got collab: 消息形态不在白名单`
  - TestSendUserRejectsCardBinding `user actor==绑定值必须 ErrNotWriter，got <nil>`
  - TestSendUserRejectsParentBinding `user actor==直接父绑定值必须 ErrNotWriter，got <nil>`
  - TestSendRejectsMergedCardRoom `并入房间必须 ErrReadOnly，got <nil>`
  - TestSendRebindRevokesOldSession `换绑前旧会话可发，got collab: 消息形态不在白名单`
  - TestPointerWritesPointerMessage `Pointer seq 必须为正，got 0`
  - TestPointerOverridesCallerKindAndBySystem `Pointer: <nil> seq=0`
  - TestPointerRejectsReadOnlyRoom `终态房间 Pointer 必须 ErrReadOnly，got <nil>`
  - TestPointerRejectsUnknownRoom `未知房间 Pointer 必须 ErrNoRoom，got <nil>`
  - 编译通过（断言红而非编译红）：失败全是断言失败。
- R3: T4.2 建 room 子包：`go build ./internal/collab/room/` EXIT=0。
- R4: T4.3 改写 service.go + pin 改引用（room.RoomEventType / 新增 TestRoomStatusLiteralMatchesLedger）→ `go build ./internal/collab/...` EXIT=0；`grep -n "Println\|fmt.Print" internal/collab/service.go internal/collab/room/*.go` 零命中（exit=1）。
- R5: T4.4 跑绿：`go test ./internal/collab/ -run 'TestSend|TestPointer' -v` 19 支全 PASS；`go test ./internal/collab/...` ok EXIT=0；`go build ./...` EXIT=0；`go vet ./internal/collab/...` EXIT=0。
- R6: 占位扫描：`grep -rn "竖切阶段不伪造放行\|Ticket 0 空壳" internal/collab/` ——「竖切阶段不伪造放行」零命中；「Ticket 0 空壳」仅剩 Consume/MarkRead/Unread（C5 半成品，plan §3 T4.4 允许保留）。
