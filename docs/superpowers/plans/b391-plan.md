# B391 实现计划：IM 寻址 `@卡号` 前缀不被解析

读者：对代码库零上下文的执行者。级别 **L2**（collab 寻址面 + CLI 文案一致性，不新增跨域边、不重造契约）。
spec：`docs/superpowers/specs/b391.md`。契约锚：`docs/superpowers/specs/b358-contract.md` §3.6/§3.8/§4.3。
工作分支：`cards/B391-charter-2`（基线 HEAD `9ae1b5c349022195a68b509b7c33696015906486`，已 `git merge -X ours origin/cards/B233.1-charter-7`）。

---

## 0. 裁决：改实现还是改文档（先读契约再动手的第一步）

**裁决：改实现——在写入边界归一化 mention（剥可选前导 `@`），存储保持契约裸口径；判定侧（`ResolveDelivery` / `MessageWakeTargets` / `session wait`）零改动。**

### 证据（逐条带出处）

1. 契约 §3.6 规则 1（`b358-contract.md:260`）冻结：「**显式 `@`**：`mentions` 逐个解析。`mention` 是卡号 → `resolveSeat` 返回该卡当前席位；非卡号（外部会话身份）**原样使用**。」——mention 的**值**是卡号/身份本体，`@` 只是寻址类别的名字。
2. 契约 §4.3 条 20/23（`:385`、`:388`）：对显式 `@卡号` 解析为席位、对非卡号 mention **原样返回该身份**。
3. 契约 §3.8（`:286`）：「`member` 与寻址 target **逐字相等**」；§4.6 条 39（`:413`）同款。订阅方 `handoff session wait user:sy` 用的是**裸身份**。
4. 离线事实（本节点跑过，原始输出见台账）：
   - 探针 `Service.Send(Mentions: []string{"@B1"})` → 落账 `mentions=["@B1"]`，`MessageWakeTargets` 返回 `["@B1"]`（`GetCard("@B1")` 失败，不命中席位）。
   - 前端发送半边已在剥前缀：`web/src/app/rooms/SessionChat.tsx:162` `body.match(/@[^\s]+/g)?.map((token) => token.slice(1))`；`sessionModel.ts:53` 高亮也按 `token.slice(1)` 与 `mentions` 比。即**契约口径的 mention 值不带 `@`**。

### 为什么不能在判定侧容忍 `@`（排除方案）

若把 `@` 剥离放进 `room.ResolveDelivery`（`internal/collab/room/delivery.go:44`）或 `resolveMessageTargets`，则**存储里仍是 `@B1`**；订阅通道 `session wait <seat>` 对 target 逐字相等匹配（条 39），member 是裸席位串，永远匹配不上带 `@` 的 target——「@ 存原样、查不到、不唤醒」的静默失效原样保留，且订阅半边（R1 通道）被悄悄弄坏。故归一化必须在**写入边界**、在落账之前完成。

### 为什么不改文档

spec §2「做」与 §4 判据 1 明确要求「`@<卡号>` 寻址在修复前不命中、修复后命中该卡当前席位（变异复验：把 `@` 剥离去掉要重新变红）」——这是本卡可执行的验收判据。改文档（把帮助改成「不带 @」）无法满足判据 1，且与用户真机命令 `--mention @B382` 的意图相悖。只改实现、不改语义口径，**不是「两条都改」**：写入边界归一化后，存储/判定/订阅全部保持契约冻结口径不变。

### 归一化的确切位置

唯一写入门是 `internal/collab/service.go#Service.Send`（`:96`）：CLI `cmd/session.go:512`、旧 `cmd/room.go:159`、HTTP `internal/agentd/roomsapi.go:462` 三处**都经它**。在这一个门归一化 = 「同一规则所有入口共享同一道门」（缺陷族·门禁绕过）。旧房间在 `Service.Send:104-109` 已只读拒绝，不会到达归一化点，行为零变化。

---

## 1. 基线复核（动手前本节点已跑，原始输出见台账）

- `go test ./internal/collab/... -count=1` → `ok github.com/Xsxdot/handoff/internal/collab 7.501s`（room 子包 0.009s）。
- `go test ./cmd/... -count=1` → `ok github.com/Xsxdot/handoff/cmd 66.577s`。
- 现状签名/调用面（`codegraph sym`，本节点跑过）：
  - `Service.Send(roomID string, msg proto.RoomMessage, actor string) (int64, error)` — `internal/collab/service.go:96`。
  - `room.ResolveDelivery(msg proto.RoomMessage, replyAuthor string, resolveSeat func(mention string) (seat string, isCard bool)) []string` — `internal/collab/room/delivery.go:30`。
  - `Service.MessageWakeTargets(msg proto.RoomMessage) ([]string, error)` — `internal/collab/sessions.go:127`。
  - `Service.resolveMessageTargets`（`:132`）的 `resolveSeat`（`:133-139`）用 `s.lc.GetCard(mention)` 判卡。
- 帮助文案现状（`go run . session send --help` 原文，本节点跑过）：
  `--mention stringArray   @成员或卡号（可重复；寻址唤醒的来源）`（来源 `cmd/session.go:558`）。
- 既有守卫（不得触碰语义）：`internal/collab/room/delivery_gate_test.go`（ResolveDelivery 签名无成员集合、必引用 `Mentions`+`ReplyTo`）；`cmd/session_test.go:195 TestSessionWaitSourceGuard`（wait 通道 AST 级禁令）。
- 图基线：`codegraph validate` → 1 个既有 issue（`cards-B374-charter` 的 `k_collab_model` 重复容器），非本卡引入；`codegraph check` 未新增本卡视图。

### 图覆盖债（本节点记录，非阻断）

- `codegraph sym sessionSendCmd` / `sym n_cmd_sessionSendCmd_RunE` 未命中（图未覆盖该 CLI 符号）→ 已回落 grep，落点 `cmd/session.go:474/558/512`。
- `codegraph who-calls n_collab_Service_MessageWakeTargets` 输出仅焦点自身、零出边（图边缺失）；广播边明确可见（`internal/agentd/wakeconsumer.go:205`、`cmd/session.go:146`）→ 以 grep 为准。
- `codegraph flow n_collab_Service_Send` / `…MessageWakeTargets` 均 `degraded:true, steps:[]`（基线无 flows 段）。
- 新增未导出函数 `normalizeMentions` 将进图；implement 节点若发现 `codegraph validate` 新增 issue，按图刷新流程处理（无新跨域边、无新导出符号，不改 `target.json`/`best.json`）。

---

## 2. 测试范围声明（最小化）

- **Task 1**：`go test ./internal/collab/ -run 'TestSendNormalizesMentionAtPrefixToSeat|TestSendMentionNormalizationReverseCases|TestWakeTargetsAddressing|TestResolveDeliveryPurity|TestIsAddressed' -count=1`
- **Task 2**：`go test ./cmd/ -run 'TestSessionSendMentionAtPrefixWakesSeat|TestSessionSendMentionHelpMatchesAcceptedForms|TestSessionSendReplyToFlag|TestRoomSendCarriesRefAndMention|TestSessionSendNegativePaths' -count=1`
- **Task 3**：同 Task 2 命令。
- 每个 task 收尾：`go build ./...`。
- **全量测试不属于任何单个 task**（acceptance / 图对账）。禁止在某 task 内跑 `./...` 全量。

---

## Task 1：写入边界归一化 `@` 前缀（最薄路径条，红绿）

文件：
- `internal/collab/service.go`（实现）
- `internal/collab/sessions_test.go`（缝级测试）

Interfaces：
- Consumes：`proto.RoomMessage.Mentions []string`（`internal/proto/rooms.go:33`）、`Service.Send(roomID string, msg proto.RoomMessage, actor string) (int64, error)`（`service.go:96`）、`Service.MessageWakeTargets(msg proto.RoomMessage) ([]string, error)`（`sessions.go:127`）、既有测试夹具 `newSessionFixture`（`sessions_test.go:27`）、`sessionCard`（`:45`）。
- Produces：未导出 `normalizeMentions(mentions []string) []string`（`service.go`，包内）；`Service.Send` 在落账前把 `msg.Mentions` 归一化为契约裸口径。**无新导出符号、无签名变更、无新跨域边。**

### 步骤

1. **先写失败测试（红）**。在 `internal/collab/sessions_test.go` 追加（import 需补 `"slices"`）：

```go
// TestSendNormalizesMentionAtPrefixToSeat 锁 B391（spec §4.1）：写入边界把
// `@卡号` / `@身份` 归一化为契约裸口径，落账的 mentions 是 `B1`，判定命中
// 该卡当前席位。当前 bug：存储 @B1、GetCard 查不到、targets=["@B1"] 不命中。
// 变异复验：去掉 Send 的 normalizeMentions 调用，本测试重新变红。
func TestSendNormalizesMentionAtPrefixToSeat(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "B391 归一化卡")
	if err := st.BindSeat(card.ID, "cli:opencode#seat-1", proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("配人: %v", err)
	}
	session, err := svc.CreateSession("B391 归一化场", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	// 三种 @ 形态同发：卡号、user: 身份、agent: 身份。
	seq, err := svc.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "@" + card.ID + " @user:sy @agent:opencode 看这里",
		Mentions: []string{"@" + card.ID, "@user:sy", "@agent:opencode"},
	}, "user:sy")
	if err != nil {
		t.Fatalf("发言: %v", err)
	}

	// 穿真实序列化边界：从 SQLite 读回 payload 原文再解码，断言存储即契约裸口径。
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var (
		stored proto.RoomMessage
		found  bool
	)
	for _, ev := range events {
		if ev.Seq != seq || ev.Type != "room_message" {
			continue
		}
		found = true
		if err := json.Unmarshal(ev.Payload, &stored); err != nil {
			t.Fatalf("解码落账载荷: %v", err)
		}
	}
	if !found {
		t.Fatalf("未找到 seq=%d 的 room_message", seq)
	}
	// 存储边界断言：payload 里的 mentions 已是契约裸口径（body 仍保留 @ 前缀，
	// 不参与本断言——逐字相等只针对 Mentions 数组）。
	wantMentions := []string{card.ID, "user:sy", "agent:opencode"}
	if !slices.Equal(stored.Mentions, wantMentions) {
		t.Fatalf("落账 mentions 应为契约裸口径 %v，实得 %q", wantMentions, stored.Mentions)
	}

	// 判定命中该卡当前席位；两个外部身份原样。
	targets, err := svc.MessageWakeTargets(stored)
	if err != nil {
		t.Fatal(err)
	}
	wantTargets := []string{"cli:opencode#seat-1", "user:sy", "agent:opencode"}
	if !slices.Equal(targets, wantTargets) {
		t.Fatalf("寻址结果 %v want %v", targets, wantTargets)
	}
}

// TestSendMentionNormalizationReverseCases 两条反例不回归（spec §4.2/§4.3）：
// 无寻址不唤醒任何人；@空座卡不落空到别人；裸卡号既有行为不变。
func TestSendMentionNormalizationReverseCases(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	card := sessionCard(t, st, "B391 反例卡")
	if err := st.BindSeat(card.ID, "cli:opencode#seat-1", proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("配人: %v", err)
	}
	empty := sessionCard(t, st, "B391 空座卡")
	session, err := svc.CreateSession("B391 反例场", "user:sy", "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, card.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinCard(session.ID, empty.ID, "user:sy"); err != nil {
		t.Fatal(err)
	}
	noAddrSeq, err := svc.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "没人要办的话"}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	emptySeatSeq, err := svc.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "@空座", Mentions: []string{"@" + empty.ID}}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	bareSeq, err := svc.Send(session.ID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: "裸卡号", Mentions: []string{card.ID}}, "user:sy")
	if err != nil {
		t.Fatal(err)
	}
	events, err := st.EventsFromAsc([]string{}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	bySeq := map[int64]proto.RoomMessage{}
	for _, ev := range events {
		if ev.Type != "room_message" {
			continue
		}
		var m proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &m); err != nil {
			t.Fatal(err)
		}
		bySeq[ev.Seq] = m
	}
	for _, tc := range []struct {
		name      string
		seq       int64
		wantEmpty bool
	}{
		{"无寻址不唤醒", noAddrSeq, true},
		{"@空座卡不落空", emptySeatSeq, true},
		{"裸卡号既有行为", bareSeq, false},
	} {
		targets, err := svc.MessageWakeTargets(bySeq[tc.seq])
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tc.wantEmpty && len(targets) != 0 {
			t.Fatalf("%s: 应空集，实得 %v", tc.name, targets)
		}
		if !tc.wantEmpty && (len(targets) != 1 || targets[0] != "cli:opencode#seat-1") {
			t.Fatalf("%s: 应命中席位，实得 %v", tc.name, targets)
		}
	}
}
```

2. **跑红**：
   ```
   go test ./internal/collab/ -run 'TestSendNormalizesMentionAtPrefixToSeat|TestSendMentionNormalizationReverseCases' -count=1
   ```
   预期：`TestSendNormalizesMentionAtPrefixToSeat` FAIL（`落账 mentions 应为契约裸口径 [B1 user:sy agent:opencode]，实得 ["@B1" "@user:sy" "@agent:opencode"]`），反例测试 PASS。若红因是编译错误/typo，先修测试再继续。

3. **最小实现**。`internal/collab/service.go`：

   3a. import 块（`:13-26`）补 `"strings"`（当前未导入）。

   3b. 在 `Service.Send` 函数之前插入未导出 helper：

```go
// normalizeMentions 归一化消息的显式寻址：剥去每个 mention 的可选前导 '@'
// 与首尾空白，空项丢弃；无有效项返回 nil（保持 wire omitempty 语义）。
//
// 为什么在写入边界而不是判定侧（B391 裁决）：契约 §3.6/§3.8 冻结的寻址口径是
// 「mention 值即裸身份/卡号」（ResolveDelivery 规则 1、条 20/23），订阅通道
// `handoff session wait <member>` 又对 target 逐字相等匹配（条 39）——若让判定
// 侧容忍 '@'，存储里带前缀的值将永远匹配不上不带前缀的 member，把「@ 存原样、
// GetCard 查不到」的静默失效原样保留。写入侧归一化后，存储保持契约裸口径，
// ResolveDelivery/MessageWakeTargets/wakeconsumer/session wait 全部零改动。
//
// 只剥一个前导 '@'：`user:sy` 原样、`@user:sy`→`user:sy`、`@B1`→`B1`；
// `@@B1`→`@B1` 仍按外部身份落空（不静默错配成别的卡）。返回 nil 而非空切片，
// 保证 §7.3 最小金样本「mentions 缺省不出键」不被破坏。
func normalizeMentions(mentions []string) []string {
	var out []string
	for _, mention := range mentions {
		mention = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(mention), "@"))
		if mention == "" {
			continue
		}
		out = append(out, mention)
	}
	return out
}
```

   3c. `Service.Send` 末尾（`:110` 之前）落账前归一化：

```go
	// 写入边界归一化显式寻址：把 CLI/HTTP 的 `@成员` 形态剥成契约裸身份再落账
	// （存储即契约口径，判定与订阅零改动——理由见 normalizeMentions）。
	rawMentions := msg.Mentions
	msg.Mentions = normalizeMentions(rawMentions)
	if len(rawMentions) > 0 {
		log().Debug("会话寻址已归一化", "room", roomID,
			"raw", len(rawMentions), "normalized", len(msg.Mentions))
	}
	return s.sendToSession(r, msg, actor)
```

   3d. `sendToSession` 成功日志（`:175`）补一个寻址读数（成功路径不静默）：
   把 `log().Info("会话消息已落账", "room", r.ID, "kind", msg.Kind, "actor", actor, "seq", seq)`
   改为附带 `"mentions", len(msg.Mentions)`。

4. **跑绿**：
   ```
   go test ./internal/collab/ -run 'TestSendNormalizesMentionAtPrefixToSeat|TestSendMentionNormalizationReverseCases|TestWakeTargetsAddressing|TestResolveDeliveryPurity|TestIsAddressed' -count=1
   go build ./...
   ```

5. **变异复验（记台账原文）**：把 3c 的 `msg.Mentions = normalizeMentions(rawMentions)` 临时改成 `msg.Mentions = rawMentions`，重跑步骤 2 的测试 → 必须重新 FAIL（`@` 前缀仍存原样）；随后还原。变异只改一行，不进提交。

**注意（不得触碰）**：`room.ResolveDelivery`、`room.IsAddressed`、`Service.resolveMessageTargets`、`Service.MessageWakeTargets`、`agentd.roomMessageWakeEvents`、`cmd/session.go#runSessionWait` 一律零改动——它们是契约冻结的判定/订阅面。`delivery_gate_test.go` 与 `TestSessionWaitSourceGuard` 必须保持绿。

---

## Task 2：CLI 缝级回归（`session send --mention @卡号` 端到端）

文件：`cmd/session_test.go`（只加测试，不改实现）。

Interfaces：
- Consumes：`runLedgerCLI`（`cmd/ledgercli_test.go:33`）、`mustAddCard`（`cmd/room_test.go:27`）、`mustSendFixture`（`cmd/session_test.go:257`）、`decodeSessionWake`（`:56`）、`clearSeatSourceEnv`（`cmd/card_driver_test.go:18`）、`Facade.JoinCardToSession`、`Store.BindSeat`。
- Produces：无（纯测试；CLI 命令面签名不变）。

### 步骤

1. 在 `cmd/session_test.go` 追加：

```go
// TestSessionSendMentionAtPrefixWakesSeat 锁 B391 修复的用户缝（spec §4.1）：
// `handoff session send <会话> --mention @<卡号>` 必须剥前缀、落账裸口径、
// 并唤醒该卡当前席位（`session wait <席位>` 收到命中）。
// 修复前红（存储 @B1、席位订阅收不到）；Task 1 实现后绿。
func TestSessionSendMentionAtPrefixWakesSeat(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	cardID := mustAddCard(t, dir, "B391 @前缀卡")
	_, facade, st, sessionID, _ := mustSendFixture(t, dir, "B391 @前缀场")
	if err := facade.JoinCardToSession(sessionID, cardID, "user:tester"); err != nil {
		t.Fatalf("拉卡进群: %v", err)
	}
	seat := "cli:claude#b391-seat"
	if err := st.BindSeat(cardID, seat, proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("配人: %v", err)
	}
	body := "@" + cardID + " 看这里"
	out, _, err := runLedgerCLI(t, dir, "session", "send", sessionID, body, "--mention", "@"+cardID)
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("session send --mention @卡号: err=%v out=%q", err, out)
	}
	// 落账载荷为契约裸口径（True 序列化边界：CLI flag → payload → SQLite）。
	events, err := st.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Type != ledger.EvRoomMessage {
			continue
		}
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Body != body {
			continue
		}
		found = true
		if len(msg.Mentions) != 1 || msg.Mentions[0] != cardID {
			t.Fatalf("落账 mentions 应剥 @ 前缀为 %q，实得 %q", cardID, msg.Mentions)
		}
	}
	if !found {
		t.Fatal("消息未落账")
	}
	// 端到端：席位订阅收到命中（@卡号 → 当前席位）。
	out, _, err = runLedgerCLI(t, dir, "session", "wait", seat, "--since", "0")
	if err != nil {
		t.Fatalf("session wait 席位: %v", err)
	}
	wake := decodeSessionWake(t, out)
	if wake.Session != sessionID || wake.Hit.Body != body {
		t.Fatalf("@卡号 未唤醒该卡当前席位: %+v", wake)
	}
}
```

2. 跑（此时 Task 1 已落，预期绿）：
   ```
   go test ./cmd/ -run 'TestSessionSendMentionAtPrefixWakesSeat|TestSessionSendReplyToFlag|TestRoomSendCarriesRefAndMention|TestSessionSendNegativePaths' -count=1
   go build ./...
   ```
   若 Task 1 未落，本测试应红；红绿归属记台账。

**反例落点**：Task 1 的反例测试已锁「无寻址不唤醒」「@空座不落空」；本 task 不重复造反例，只做 CLI 端到端正例。

---

## Task 3：CLI 帮助文案与真实形态逐字对齐（spec §4.4）

文件：`cmd/session.go`（帮助字符串）、`cmd/session_test.go`（断言）。

### 步骤

1. `cmd/session.go:558` 把
   `sessionSendCmd.Flags().StringArrayVar(&sessionSendMention, "mention", nil, "@成员或卡号（可重复；寻址唤醒的来源）")`
   改为：
   `sessionSendCmd.Flags().StringArrayVar(&sessionSendMention, "mention", nil, "成员或卡号（@ 前缀可选；可重复；寻址唤醒的来源）")`
   —— 语义裁决已在 Task 1（改实现、存储裸口径），此处只是把**已接受的两形态**说清楚，不改任何判定语义。

2. 在 `cmd/session_test.go` 追加：

```go
// TestSessionSendMentionHelpMatchesAcceptedForms 锁 spec §4.4：--mention 帮助
// 措辞与真实接受的形态（可带 @、可裸写）一致。
func TestSessionSendMentionHelpMatchesAcceptedForms(t *testing.T) {
	flag := sessionSendCmd.Flags().Lookup("mention")
	if flag == nil {
		t.Fatal("找不到 --mention flag")
	}
	for _, want := range []string{"成员", "卡号", "@", "可选"} {
		if !strings.Contains(flag.Usage, want) {
			t.Fatalf("--mention 说明应含 %q，实得 %q", want, flag.Usage)
		}
	}
}
```

3. 跑：
   ```
   go test ./cmd/ -run 'TestSessionSendMentionHelpMatchesAcceptedForms|TestSessionSendMentionAtPrefixWakesSeat' -count=1
   go build ./...
   ```

---

## 3. 任务依赖 / DAG

```
Task 1（collab 归一化，红→绿，最薄路径条）
   └─> Task 2（CLI 缝端到端；依赖 Task 1 的实现才绿）
Task 3（帮助文案，独立，可与 Task 2 同批）
```

最薄路径条：本卡要锁的行为（`--mention @<卡号>` 命中当前席位）今天从声明缝 `Service.Send` 调用得不到预期结果（探针实测红），故 Task 1 必须排第一并点亮该行为。

---

## 4. 五项检查

### 4.1 缺陷族对抗审查

| 族 | 设问 | 结论 |
|---|---|---|
| 生命周期 / 状态机中断 | 归一化是纯函数、无资源、无跨进程状态，宿主重启无残留 | 无风险——`normalizeMentions` 不分配外部资源；进程重启后存储仍是裸口径 |
| 静默失败 / 误导报错 | 归一化会不会「报成功但没改」？`@@B1` 剥一个仍不命中会不会静默？ | `@@B1`→`@B1` 走外部分支零 Wake 是有意 fail-closed（不猜第二层前缀）；`Send` 成功日志新增 `mentions` 读数，归一化路径有 Debug 日志，可见 |
| 跨平台假设 | 平台无关（纯字符串） | 无风险，因为仅 `strings.TrimSpace`/`TrimPrefix` |
| 假红 / 假绿测试 | 判据是否中途副产物？有无反面断言？ | 正的缝级断言（Task 1）+ 两条反例（无寻址、@空座）+ 裸口径既有回归（`TestWakeTargetsAddressing`/`TestResolveDeliveryPurity`）都在场；Task 1 变异复验证明能变红 |
| 门禁绕过 | 新写路径过权限门吗？所有入口同一道门吗？ | 无新写路径；归一化在 `Service.Send` 单一门，CLI/HTTP 三入口共享；读写权限执法（`VerifyWriter`）顺序不变，仍在归一化前（`sendToSession` 内） |
| 序列化边界 | 新字段每处手写投影都列了吗？有穿真实边界的回归吗？ | 见 4.2；`mentions` 从 CLI/HTTP → `proto.RoomMessage` → JSON payload → SQLite → 解码 → `MessageWakeTargets`；Task 1 测试从 SQLite 读回 payload 原文断言无 `@`，Task 2 从 CLI 穿全链 |
| 枚举新值过白名单 | 新枚举？ | 无新枚举值 |
| 承重安全属性有测试锁住 | 「mention 与 member 逐字相等」是承重属性吗？有能变红的测试吗？ | 是（订阅通道命中判据）。`TestSendNormalizesMentionAtPrefixToSeat` 锁「存储恒裸口径」，使逐字相等成立；`TestSessionWaitMemberExactMatch` 锁订阅逐字相等不回归 |

### 4.2 序列化边界设问（逐处列全）

`mentions` 的产生→消费链与手写投影：
1. CLI `cmd/session.go:509` 把 `--mention` 值放进 `proto.RoomMessage.Mentions` → **断言在 Task 2**（落账 payload 裸口径 + std 形状）。
2. CLI `cmd/room.go:148` 同款（旧房间只读，不达落账）→ 零行为变化，无需新断言。
3. HTTP `internal/agentd/roomsapi.go:446/463` `mentions` 请求体 → `Send` → 归一化在 `Send` 内，HTTP 侧自动获得（无独立断言；web 前端 `SessionChat.tsx:162` 已剥前缀，不新增 web 改动）。
4. `internal/ledger/rooms.go#RecordRoomMessage` 把 `proto.RoomMessage` 编成 payload（`json.Marshal`），无字段解释 → 由 `Service.Send` 归一化保证入参裸口径。
5. 读侧 `room.UnmarshalMessage` → `MessageWakeTargets` → `GetCard` → 席位。
6. 订阅留痕 `Service.Mentions`/`Pending` 用 `room.MentionsMember`（`room.go:163`）逐字比较 → 归一化保证裸口径与 member 可比。
7. 收件箱 `internal/agentd/roomsapi.go:565` 消费 `Mentions` → 同口径。

**可空类型区分缺失/零值**：落账断言用 `slices.Equal(stored.Mentions, wantMentions)` 逐元素比对 Mentions 数组（`@B1` vs `B1` 在此逐字区分）；`omitempty` 语义（无有效 mention 出 nil 而非 `[]`）由 `normalizeMentions` 返回 nil 保证，既有 `internal/proto/rooms_fixture_test.go:55` 最小金样本回归覆盖。无有效 mention 时 `Mentions` 数组元素与 URL/token 边界由 `slices.Equal` 的可空比对兜住（nil 与 `[]string{}` 在 `slices.Equal` 下相等，故另由金样本锁 omitempty 键缺失）。

### 4.3 上下文预算检查

有界文件集：`internal/collab/service.go`、`internal/collab/sessions_test.go`、`cmd/session.go`、`cmd/session_test.go`（+ 本 plan）。可圈定，无需额外竖切卡。

### 4.4 类型标注（边界型子系统行为验收，真机清单）

collab 是子系统（非跨机边界），最小真机清单（implement 节点在实现环境跑，原始输出入台账）：
1. `handoff session send <会话> '<正文>' --mention @<卡号>` → stdout `{"ok":true,"seq":n}`。
2. `handoff session wait <该卡当前席位> --since 0` → 恰一行 `SessionWake` JSON，`hit.body` 与第 1 步正文一致、`session` 正确。
3. `handoff session send <会话> '<正文>'`（无 `--mention`）→ 席位订阅**收不到**（`--timeout` 到点 124）。
（`internal/collab` 内缝级测试已穿真 SQLite；真机项为 CLI 组装点 `openRoomService` 的有界补强。）

### 4.5 接缝覆盖（双向，对照 spec 测试决定的接缝清单）

spec 相关接缝 = 本卡触及的用户缝：**CLI `session send`** 与**collab 写入门 `Service.Send`**（`MessageWakeTargets` 为冻结判定缝，不改）。

**测试 → 缝**：
- `TestSendNormalizesMentionAtPrefixToSeat` 入口 = `Service.Send`（collab 声明缝），穿 `MessageWakeTargets`。
- `TestSendMentionNormalizationReverseCases` 入口 = `Service.Send`。
- `TestSessionSendMentionAtPrefixWakesSeat` 入口 = CLI `session send`（用户缝），穿 `Service.Send` 与 `session wait`。
- `TestSessionSendMentionHelpMatchesAcceptedForms` 入口 = `sessionSendCmd.Flags().Lookup("mention")`（CLI 接口面，spec §4.4）。

**缝 → 测试**：
- `Service.Send` 缝：Task 1 两支测试锁住。
- CLI `session send` 缝：Task 2 正例 + 既有 `TestSessionSendNegativePaths`/`TestSessionSendReplyToFlag`/`TestRoomSendCarriesRefAndMention` 回归。
- `MessageWakeTargets`/`ResolveDelivery` 判定缝：零改动的既有 `TestWakeTargetsAddressing`/`TestResolveDeliveryPurity` 回归在场（本卡不新增断言，因语义不动）。

**内部锁声明**：无。本卡无纯内部锁断言顶替缝级断言。

**退路同闸**：无改变入口符号的条件退路。

---

## 5. 占位符扫描

无 TBD、无「加适当错误处理」、无「同 Task N」指代；每个 Task 有精确文件路径、完整代码块、精确判据命令与预期。测试代码为完整可编译片段，复用既有夹具（`newSessionFixture`/`sessionCard`/`mustSendFixture`/`mustAddCard`/`runLedgerCLI`/`decodeSessionWake`）而非自造 harness。

## 6. 自审三查

- **spec 覆盖**：§4.1→Task 1+2；§4.2→Task 1 反例 + 既有回归；§4.3→Task 1 反例；§4.4→Task 3。§2「做」→Task 1（@卡号/@成员归一化）。§3 四项待查全部有结论（见 §0 与台账）。
- **占位符扫描**：见 §5，通过。
- **跨 task 类型/签名一致性**：`normalizeMentions(mentions []string) []string`（未导出，包内一致）；`Service.Send` 签名不变；测试引用符号与既有定义逐字一致。

## 7. 图/契约影响

- 无新导出符号、无签名变更、无新跨域边、不改 `target.json`/`best.json`。
- `codegraph validate` 必须保持 1 个既有 issue（`cards-B374-charter`），不得新增；implement 节点新增未导出 `normalizeMentions` 后按图刷新流程纳入 baseline。
- 契约零改动：本卡只让写入侧符合 §3.6/§3.8 既有裸口径，不改任何冻结条目。
