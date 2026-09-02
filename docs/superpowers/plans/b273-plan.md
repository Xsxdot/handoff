# B273 批 2：环节白跑止损实施计划

## 0. 范围与基线

本计划以已批准的 `docs/superpowers/specs/b273.md` r1 为唯一行为来源，覆盖 B243、B242、B244、B241 四条独立改法。四条改法禁止抽公共“环节白跑”框架；每个 task 只改自己的文件集。事实台账是 `docs/superpowers/ledgers/2026-08-27-b273-plan-ledger.md`，实施中逐事实追加。

动手前已在当前基线取得以下结果：

| task | 基线命令 | 实际结果 |
|---|---|---|
| B243、B242 | `go test ./internal/ledgerstep -count=1` | 退出 0：`ok  github.com/Xsxdot/handoff/internal/ledgerstep 6.273s` |
| B244 | `go test ./internal/executor/turn ./internal/executor/codex -count=1` | 退出 0：`ok  github.com/Xsxdot/handoff/internal/executor/turn 0.165s`；无独立 codex 输出 |
| B241 | `GOMODCACHE=/root/.handoff/tmp/27c30ff0/b273-gomodcache GOSUMDB=off go test ./internal/agentd -run 'TestFlow|TestLedgerNodeWire' -count=1` | 退出 0：`ok  github.com/Xsxdot/handoff/internal/agentd 0.841s` |

B241 默认缓存的真实失败输出为 `go: writing go.mod cache: open /root/go/pkg/mod/cache/download/github.com/!xsxdot/charter/graph/@v/v0.9.0.mod24106577.tmp: read-only file system`、`internal/agentd/codegraph.go:22:2: open /root/go/pkg/mod/cache/download/github.com/!xsxdot/charter/graph/@v/v0.9.0.lock: read-only file system`、`FAIL github.com/Xsxdot/handoff/internal/agentd [setup failed]`；该环境事实已在台账记录。

## 1. 接口与图覆盖债

已按 best 领域词表查询仓内 `codegraph/best.json` 的 `d_ledger`、`d_protocol`、`d_gateway`。`d_ledger` 实际显示账本保存与事件镜像；`d_gateway` 显示 `truncated.atDepth=0 droppedNodes=24`、`fociTruncated.total=77 shown=5`；`d_ledger` 显示 `fociTruncated.total=57 shown=5`。结果有截断，不能视作完整调用图。

`codegraph sym` 未覆盖 `internal/ledgerstep/wire.go#waitForTurnEnd`、`internal/ledgerstep/verdict.go#ParseVerdict`、`internal/executor/turn/protocol.go#ProtocolRules`、`internal/agentd/ledgerapi.go#ledgerNodeWire`，四次退出状态均为 1；`who-calls`/`chain --with-source` 对 `waitForTurnEnd`、`ParseVerdict`、`StepRunner.awaitNode`、`Server.handleFlowGet` 也实际退出状态 1。原始错误全文已落台账；实施者按源码和 grep 复核这些调用面，不能用图缺失证明调用者不存在。

### 1.1 Task 接缝契约

B243 Consumes：

~~~go
func waitForTurnEnd(ctx context.Context, wait func(context.Context) (*proto.Event, error)) error
func finalMessageFromEvents(events []*proto.Event) (string, error)
func (r *StepRunner) awaitNode() func(context.Context, string, string) (string, error)
func (c *Client) WaitEvent(ctx context.Context, taskID string, all bool) (*proto.Event, error)
~~~

Produces：残缺 completed 后的每次后续 wait 收到 parent child deadline；child 到期且 parent 活着返回 nil；parent 取消和其它 wait 错误原样返回；正文选择非空 `final_text`，缺字段才可回落 summary，显式空值报错。

B242 Consumes：

~~~go
func ParseVerdict(message string) (Verdict, error)
func (n *NodeStep) RunOnce(ctx context.Context, cardID string) (Outcome, error)
~~~

Produces：严格失败后只从最后围栏正文抢救首个 `verdict`，按 pass/fail 返回且 Raw 保留围栏正文；丢弃 notes/findings 时 RunOnce 写可见普通评论。

B244 Consumes/Produces：

~~~go
const ProtocolRules = `1. 提问纪律：任何需要人决策的问题，输出单行 JSON {"ask":"<问题>"}
   然后结束本回合。协调者的回答会作为下一条消息发给你。
   禁止自行假设，禁止用其它格式提问。
2. 收尾纪律：是否提交听角色纪律。
   角色禁止修改工作树时，不创建新提交，commit 填当前 HEAD；无新提交时 commit 仍填当前 HEAD，不能为空。
   角色要求提交时，必须 git add 并 commit（不要 push）。
   无论是否创建新提交，都必须输出单行 JSON：{"branch":"<分支>","commit":"<hash>","summary":"<50字内摘要>"}
   作为本回合最后一行。
3. 只在当前分支工作，不切分支、不改 git 配置。`
func RenderPrompt(taskID, planContent, disciplineBlock string) (string, error)
~~~

Produces：RenderPrompt 仍嵌入同一 ProtocolRules；trailer 字段仍为 branch、commit、summary。

B241 Consumes/Produces：

~~~go
type NodeOverride struct {
    Executor string `json:"executor,omitempty"`
    Discipline string `json:"discipline,omitempty"`
    Target string `json:"target,omitempty"`
    Model string `json:"model,omitempty"`
    Purpose string `json:"purpose,omitempty"`
}
func (s *Server) handleFlowGet(w http.ResponseWriter, r *http.Request)
func (s *Server) handleFlowPut(w http.ResponseWriter, r *http.Request)
~~~

Produces：详情 GET 的 override 含 purpose；GET 返回的 nodes 原样 PUT 后再 GET，purpose 仍在；空 purpose 因 omitempty 不输出。

## 2. Task B243：终态等待与 Attach 正文选择

### 2.1 文件范围、最薄路径、基线后红测

只改 `internal/ledgerstep/wire.go`、`internal/ledgerstep/wire_test.go`；不改 runner、client 或生产事件发出端。声明缝是 `waitForTurnEnd ← StepRunner.awaitNode ← Client.WaitEvent` 与 `finalMessageFromEvents ← clientFinalMessage`。测试必须从这些符号进入，不能以内部 JSON helper 顶替。

在基线之后先更新现有 `TestWaitForTurnEndSkipsNonTerminalEvents` 的最终 completed，使其 Data 含非空 final_text，再新增以下完整断言。Go 测试使用 JSON 字符串中的 `\u0060` 表达围栏，避免无 payload 假绿：

~~~go
func TestWaitForTurnEndWaitsForCompletedFinalText(t *testing.T) {
    events := []*proto.Event{
        {Type: proto.EventTypeCompleted, Data: json.RawMessage("{\"summary\":\"早到摘要\"}")},
        {Type: proto.EventTypeCompleted, Data: json.RawMessage("{\"summary\":\"最终摘要\",\"final_text\":\"\\u0060\\u0060\\u0060handoff-verdict\\n{\\\"verdict\\\":\\\"pass\\\"}\\n\\u0060\\u0060\\u0060\"}")},
    }
    calls := 0
    err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
        event := events[calls]
        calls++
        return event, nil
    })
    if err != nil { t.Fatalf("waitForTurnEnd() error = %v", err) }
    if calls != 2 { t.Fatalf("wait calls = %d, want 2", calls) }
}

func TestWaitForTurnEndGraceDeadlineReturnsSuccess(t *testing.T) {
    first := &proto.Event{Type: proto.EventTypeCompleted, Data: json.RawMessage("{\"summary\":\"唯一摘要\"}")}
    oldGrace := turnEndGrace
    turnEndGrace = time.Nanosecond
    defer func() { turnEndGrace = oldGrace }()
    calls := 0
    deadlineSeen := make(chan bool, 1)
    err := waitForTurnEnd(context.Background(), func(ctx context.Context) (*proto.Event, error) {
        calls++
        if calls == 1 { return first, nil }
        _, ok := ctx.Deadline()
        deadlineSeen <- ok
        <-ctx.Done()
        return nil, ctx.Err()
    })
    if err != nil { t.Fatalf("deadline grace error = %v", err) }
    if !<-deadlineSeen { t.Fatal("grace wait context has no deadline") }
}

func TestWaitForTurnEndDoesNotSwallowParentCancellation(t *testing.T) {
    parent, cancel := context.WithCancel(context.Background())
    defer cancel()
    first := &proto.Event{Type: proto.EventTypeCompleted, Data: json.RawMessage("{\"summary\":\"摘要\"}")}
    calls := 0
    err := waitForTurnEnd(parent, func(ctx context.Context) (*proto.Event, error) {
        calls++
        if calls == 1 { return first, nil }
        cancel()
        <-ctx.Done()
        return nil, ctx.Err()
    })
    if !errors.Is(err, context.Canceled) { t.Fatalf("error = %v, want context.Canceled", err) }
}

func TestWaitForTurnEndIgnoresFailureDuringGrace(t *testing.T) {
    first := &proto.Event{Type: proto.EventTypeCompleted, Data: json.RawMessage("{\"summary\":\"摘要\"}")}
    failed := &proto.Event{Type: proto.EventTypeFailed, Data: json.RawMessage("{\"error\":\"late failure\"}")}
    turnFailed := &proto.Event{Type: proto.EventTypeTurnFailed, Data: json.RawMessage("{\"error\":\"late turn failure\"}")}
    second := &proto.Event{Type: proto.EventTypeCompleted, Data: json.RawMessage("{\"final_text\":\"\\u0060\\u0060\\u0060handoff-verdict\\n{\\\"verdict\\\":\\\"pass\\\"}\\n\\u0060\\u0060\\u0060\"}")}
    events := []*proto.Event{first, failed, turnFailed, second}
    calls := 0
    err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
        event := events[calls]
        calls++
        return event, nil
    })
    if err != nil { t.Fatalf("grace failure error = %v", err) }
    if calls != 4 { t.Fatalf("wait calls = %d, want 4", calls) }
}

func TestWaitForTurnEndReturnsFailureWithoutCompleted(t *testing.T) {
    for _, eventType := range []proto.EventType{proto.EventTypeFailed, proto.EventTypeTurnFailed} {
        t.Run(eventType, func(t *testing.T) {
            event := &proto.Event{Type: eventType, Data: json.RawMessage("{\"error\":\"failed\"}")}
            if err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
                return event, nil
            }); err != nil {
                t.Fatalf("waitForTurnEnd() error = %v", err)
            }
        })
    }
}

func TestFinalMessageUsesNonEmptyFinalTextAcrossCompletedEvents(t *testing.T) {
    events := []*proto.Event{
        {Type: proto.EventTypeCompleted, Data: json.RawMessage("{\"summary\":\"first summary\"}")},
        {Type: proto.EventTypeCompleted, Data: json.RawMessage("{\"summary\":\"second summary\",\"final_text\":\"\\u0060\\u0060\\u0060handoff-verdict\\n{\\\"verdict\\\":\\\"pass\\\"}\\n\\u0060\\u0060\\u0060\"}")},
    }
    got, err := finalMessageFromEvents(events)
    if err != nil { t.Fatalf("finalMessageFromEvents() error = %v", err) }
    if !strings.Contains(got, "handoff-verdict") { t.Fatalf("message = %q, want final_text", got) }
}
~~~

补齐测试 import `encoding/json`、`errors`、`strings`、`time`；已有 summary fallback、显式空串错误、completed 优先于 trailing turn_failed 测试必须保留。先运行：

~~~text
go test ./internal/ledgerstep -run 'TestWaitForTurnEnd|TestFinalMessage' -count=1
~~~

新断言应使基线至少一条失败；实施者记录实际输出。即使意外先绿，也不删除 seam 测试、不改成直喂 helper。

### 2.2 最小实现

`internal/ledgerstep/wire.go` 文件头注释写明“消费 WaitEvent 终态并从 Attach 快照选择回合正文；不负责生产事件或裁决路由”。增加 imports `errors`、`log/slog`、`time`。以下代码是新增类型及函数的完整内容：

~~~go
var turnEndGrace = time.Second

type completedPayload struct {
    Summary string `json:"summary"`
    FinalText *string `json:"final_text"`
}

func decodeCompletedPayload(event *proto.Event) (completedPayload, error) {
    var payload completedPayload
    if err := json.Unmarshal(event.Data, &payload); err != nil {
        return completedPayload{}, fmt.Errorf("解析 completed payload 失败: %w", err)
    }
    return payload, nil
}

func waitForTurnEnd(ctx context.Context, wait func(context.Context) (*proto.Event, error)) error {
    sawCompleted := false
    waitCtx := ctx
    var cancelGrace context.CancelFunc
    defer func() { if cancelGrace != nil { cancelGrace() } }()
    for {
        event, err := wait(waitCtx)
        if err != nil {
            if cancelGrace != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
                slog.InfoContext(ctx, "completed 宽限到期，继续收取 Attach 摘要")
                return nil
            }
            slog.ErrorContext(ctx, "等待回合终态失败", "saw_completed", sawCompleted, "error", err)
            return err
        }
        if event == nil {
            slog.WarnContext(ctx, "等待回合终态收到空事件")
            continue
        }
        switch event.Type {
        case proto.EventTypeCompleted:
            payload, decodeErr := decodeCompletedPayload(event)
            if decodeErr != nil {
                slog.WarnContext(ctx, "completed payload 不可解析，进入宽限", "error", decodeErr)
            } else if payload.FinalText != nil && *payload.FinalText != "" {
                slog.InfoContext(ctx, "收到带 final_text 的 completed")
                return nil
            } else {
                slog.InfoContext(ctx, "收到残缺 completed，进入宽限", "final_text_present", payload.FinalText != nil)
            }
            sawCompleted = true
            if cancelGrace == nil { waitCtx, cancelGrace = context.WithTimeout(ctx, turnEndGrace) }
        case proto.EventTypeTurnFailed, proto.EventTypeFailed:
            if !sawCompleted {
                slog.InfoContext(ctx, "未见 completed，失败事件收口", "event_type", event.Type)
                return nil
            }
            slog.InfoContext(ctx, "宽限内忽略失败事件，继续等待 completed", "event_type", event.Type)
        }
    }
}

func finalMessageFromEvents(events []*proto.Event) (string, error) {
    var summary string
    summaryAvailable := false
    explicitEmpty := false
    for i := len(events) - 1; i >= 0; i-- {
        event := events[i]
        if event == nil || event.Type != proto.EventTypeCompleted { continue }
        payload, err := decodeCompletedPayload(event)
        if err != nil {
            slog.Warn("跳过不可解析的 completed payload", "index", i, "error", err)
            continue
        }
        if payload.FinalText != nil {
            if *payload.FinalText != "" { return *payload.FinalText, nil }
            explicitEmpty = true
            continue
        }
        if !summaryAvailable && payload.Summary != "" { summary, summaryAvailable = payload.Summary, true }
    }
    if summaryAvailable && !explicitEmpty { return summary, nil }
    if summaryAvailable && explicitEmpty { return "", errors.New("completed 的 final_text 为空") }
    return "", errors.New("事件中没有可用的 completed 正文")
}
~~~

`context.WithTimeout(ctx, turnEndGrace)` 只能在首次残缺 completed 后创建，且 child 必须传给每一次后续 wait。禁止第二个无 deadline 的 WaitEvent、`time.Sleep` 或未取消 goroutine。child 到期只有 parent `ctx.Err() == nil` 时转 `nil`；parent 取消与其它错误原样返回。日志覆盖残缺、正文、空事件、宽限成功、parent/其它错误和 Attach 解码错误。

实现后只运行：

~~~text
go test ./internal/ledgerstep -run 'TestWaitForTurnEnd|TestFinalMessage' -count=1
go test ./internal/ledgerstep -count=1
~~~

两条命令取得退出 0 原始输出后才可判绿。验收必须覆盖：残缺后正文、deadline 到期成功、无 completed 的 failed 与 turn_failed 即刻成功、宽限内 failed 与 turn_failed 不切断、多个 completed 选非空正文、缺字段回落 summary、显式空串错误。B243 测试不调用 ParseVerdict；B242 另锁裁决解析。

## 3. Task B242：损坏 notes 时抢救首个 verdict

### 3.1 文件范围、接口与基线后红测

只改 `internal/ledgerstep/verdict.go`、`internal/ledgerstep/node.go`、`internal/ledgerstep/node_test.go`、`internal/ledgerstep/verdict_test.go`。B242 不改 B243 的事件等待，不把 final_text 夹具用于裁决 parser。声明缝为 `ParseVerdict ← NodeStep.RunOnce ← routing`。

基线后先写下列 parser 测试，然后跑：

~~~text
go test ./internal/ledgerstep -run 'TestParseVerdict|TestNodeStep.*Verdict' -count=1
~~~

~~~go
func TestParseVerdictSalvagesFirstVerdictWhenNotesIsBroken(t *testing.T) {
    fence := strings.Repeat(string(rune(96)), 3)
    message := "正文\n" + fence + "handoff-verdict\n" +
        "{\"verdict\":\"pass\",\"findings\":[{\"severity\":\"minor\",\"summary\":\"保留\"}],\"notes\":\"enabled\":true}\n" +
        fence + "\n"
    got, err := ParseVerdict(message)
    if err != nil { t.Fatalf("ParseVerdict() error = %v", err) }
    if !got.Pass || len(got.Findings) != 1 || got.Findings[0].Summary != "保留" {
        t.Fatalf("抢救结果 = %+v", got)
    }
    if got.Notes != "" { t.Fatalf("Notes = %q, want empty", got.Notes) }
    if !strings.Contains(got.Raw, "\"notes\":\"enabled\":true") {
        t.Fatalf("Raw 丢失损坏 notes: %q", got.Raw)
    }
    if !got.salvaged || !got.notesDropped || got.findingsDropped {
        t.Fatalf("抢救标记 = %+v", got)
    }
}

func TestParseVerdictUsesFirstVerdictNotNotesMention(t *testing.T) {
    fence := strings.Repeat(string(rune(96)), 3)
    message := fence + "handoff-verdict\n" +
        "{\"verdict\":\"fail\",\"findings\":[],\"notes\":\"bad \\\"verdict\\\":\\\"pass\\\"\":true}\n" +
        fence + "\n"
    got, err := ParseVerdict(message)
    if err != nil { t.Fatalf("ParseVerdict() error = %v", err) }
    if got.Pass { t.Fatalf("verdict 被 notes 引用覆盖: %+v", got) }
}

func TestParseVerdictStillRejectsMissingOrUnknownVerdict(t *testing.T) {
    fence := strings.Repeat(string(rune(96)), 3)
    for _, message := range []string{
        "没有裁决围栏",
        fence + "handoff-verdict\n{\"verdict\":\"maybe\"}\n" + fence + "\n",
    } {
        if _, err := ParseVerdict(message); err == nil {
            t.Fatalf("ParseVerdict(%q) unexpectedly succeeded", message)
        }
    }
}

func TestNodeStepCommentsWhenSalvageDropsNotes(t *testing.T) {
    st, card := nodeLedger(t)
    fence := strings.Repeat(string(rune(96)), 3)
    message := fence + "handoff-verdict\n" +
        "{\"verdict\":\"pass\",\"findings\":[],\"notes\":\"enabled\":true}\n" +
        fence + "\n"
    step := newNodeStep(t, st, ledger.NodeDef{
        Name: "review", Dispatch: true, Verdict: true, Template: "review-generic",
    }, message, nil)
    out, err := step.RunOnce(context.Background(), card.ID)
    if err != nil { t.Fatalf("RunOnce() error = %v", err) }
    if out.Action != ActionPass || !out.Verdict.Pass { t.Fatalf("outcome = %+v", out) }
    events, err := st.EventsFromAsc([]string{card.ID}, 0, 1000)
    if err != nil { t.Fatalf("读取事件: %v", err) }
    found := false
    for _, event := range events {
        if event.Type == ledger.EvComment && strings.Contains(string(event.Payload), "notes") &&
            strings.Contains(string(event.Payload), "抢救") {
            found = true
        }
    }
    if !found { t.Fatal("抢救丢弃 notes 没有普通评论留痕") }
}
~~~

补齐测试 import `strings` 与既有 `context`、`testing`、`ledger`。测试逐条锁住：合法严格 JSON 继续成功；notes 含未转义引号且 verdict=pass 仍 pass、Raw 仍是围栏正文；fail 且 notes 提及 pass 仍 fail；无围栏和 maybe 仍失败；成功抢救后 timeline 有 notes 丢弃评论。先红后实现，不能把 parser 直接喂已清洗的短 JSON。

### 3.2 最小实现代码

`verdict.go` 文件头注释补充“解析最后 handoff-verdict 围栏；抢救只针对围栏正文，不扫描整回合文本”。保留 Finding 字段，将 Verdict 扩展为：

~~~go
type Verdict struct {
    Pass            bool      `json:"pass"`
    Findings        []Finding `json:"findings"`
    Notes           string    `json:"notes,omitempty"`
    Raw             string    `json:"-"`
    salvaged        bool
    notesDropped    bool
    findingsDropped bool
}
~~~

用以下完整 helper 从字段冒号后独立解码 JSON 值；`InputOffset` 后的边界检查防止 malformed notes 的前缀字符串被误认成完整 notes：

~~~go
func decodeVerdictField(raw, key string, dst any) bool {
    pat := regexp.MustCompile("(?s)\\\"" + regexp.QuoteMeta(key) + "\\\"\\s*:\\s*")
    loc := pat.FindStringIndex(raw)
    if loc == nil { return false }
    dec := json.NewDecoder(strings.NewReader(raw[loc[1]:]))
    if err := dec.Decode(dst); err != nil { return false }
    tail := strings.TrimSpace(raw[loc[1]+int(dec.InputOffset()):])
    return tail == "" || tail[0] == ',' || tail[0] == '}'
}

func firstVerdictValue(raw string) (string, bool) {
    pat := regexp.MustCompile("(?s)\\\"verdict\\\"\\s*:\\s*\\\"(pass|fail)\\\"")
    match := pat.FindStringSubmatch(raw)
    if len(match) != 2 { return "", false }
    return match[1], true
}
~~~

`ParseVerdict` 保持先取最后围栏、先整体 `json.Unmarshal` 的严格路径；整体失败时只对切出的 `raw` 做抢救。完整控制流程如下：

~~~go
func ParseVerdict(message string) (Verdict, error) {
    blocks := verdictBlockPat.FindAllStringSubmatch(message, -1)
    if len(blocks) == 0 { return Verdict{}, fmt.Errorf("报文中没有 handoff-verdict block") }
    raw := strings.TrimSpace(blocks[len(blocks)-1][1])
    var wire struct {
        Verdict string    `json:"verdict"`
        Findings []Finding `json:"findings"`
        Notes string       `json:"notes"`
    }
    if err := json.Unmarshal([]byte(raw), &wire); err == nil {
        switch wire.Verdict {
        case "pass":
            return Verdict{Pass: true, Findings: wire.Findings, Notes: wire.Notes, Raw: raw}, nil
        case "fail":
            return Verdict{Findings: wire.Findings, Notes: wire.Notes, Raw: raw}, nil
        default:
            return Verdict{}, fmt.Errorf("verdict 值 %q 不在 {pass,fail}", wire.Verdict)
        }
    }
    verdictValue, ok := firstVerdictValue(raw)
    if !ok {
        return Verdict{}, fmt.Errorf("裁决 JSON 解析失败，且无法从围栏正文抢救 verdict")
    }
    var findings []Finding
    findingsPresent := strings.Contains(raw, "\"findings\"")
    findingsOK := !findingsPresent || decodeVerdictField(raw, "findings", &findings)
    if !findingsOK { findings = nil }
    var notes string
    notesPresent := strings.Contains(raw, "\"notes\"")
    notesOK := !notesPresent || decodeVerdictField(raw, "notes", &notes)
    if !notesOK { notes = "" }
    result := Verdict{
        Pass: verdictValue == "pass", Findings: findings, Notes: notes, Raw: raw,
        salvaged: true, notesDropped: notesPresent && !notesOK,
        findingsDropped: findingsPresent && !findingsOK,
    }
    slog.Warn("裁决围栏已抢救", "verdict", verdictValue,
        "notes_dropped", result.notesDropped, "findings_dropped", result.findingsDropped)
    return result, nil
}
~~~

增加 `log/slog` import。严格路径仍只接受 pass/fail；抢救路径取正则首次命中，不取最后命中；findings/notes 逐字段独立解码；Raw 永远是最后围栏去首尾空白后的原文，不重建 JSON。抢救缺失 verdict 或 maybe 仍报错。`decodeVerdictField` 是内部实现，不另立内部锁。

在 `NodeStep.RunOnce` 的 ParseVerdict 成功之后、`gatedWrite("裁决落账")` 之前加入以下完整副作用步骤：

~~~go
if verdict.salvaged && (verdict.notesDropped || verdict.findingsDropped) {
    dropped := make([]string, 0, 2)
    if verdict.notesDropped { dropped = append(dropped, "notes") }
    if verdict.findingsDropped { dropped = append(dropped, "findings") }
    body := fmt.Sprintf("裁决 JSON 已抢救，仍按 %t 路由；以下字段因 JSON 损坏被丢弃：%s。Raw 保留在裁决事件中。",
        verdict.Pass, strings.Join(dropped, "、"))
    if err := n.gatedWrite("裁决抢救留痕"); err != nil { return Outcome{}, err }
    if _, err := n.St.AddComment(cardID, body, "普通", n.actor()); err != nil {
        return Outcome{}, err
    }
    logger.Warn("裁决抢救字段已丢弃并留普通评论",
        "card", cardID, "node", n.Node.Name, "dropped", dropped)
}
~~~

`node.go` 顶部职责注释补充“成功抢救也须保留丢弃字段的普通评论”；`RunOnce` 的 exported 注释补充抢救留痕与返回错误语义。成功日志包含 pass/findings，抢救日志包含 card/node/dropped；AddComment 失败原样返回，不能静默。

### 3.3 绿测与范围

只跑：

~~~text
go test ./internal/ledgerstep -run 'TestParseVerdict|TestNodeStep.*Verdict' -count=1
go test ./internal/ledgerstep -count=1
~~~

两条取得退出 0 后才判绿。测试入口穿过 parser 与 RunOnce，后者再穿过真实账本 comment/RecordReviewVerdict 序列化边界；B242 不依赖 B243 的 WaitEvent。

## 4. Task B244：角色条件化收尾协议

### 4.1 文件范围、基线后红测与接口

只改 `internal/executor/turn/protocol.go`、`internal/executor/turn/protocol_test.go`。`internal/executor/codex/developer_instructions_test.go` 只复用 ProtocolRules，不改文件。声明缝为 `ProtocolRules ← RenderPrompt`。

基线后先在 `protocol_test.go` 增加以下完整 seam 断言，再运行：

~~~go
func TestProtocolRulesMakeCommitConditionalOnRole(t *testing.T) {
    out, err := turn.RenderPrompt("T1", "plan", "")
    if err != nil { t.Fatal(err) }
    for _, want := range []string{
        "是否提交听角色纪律",
        "角色禁止修改工作树",
        "commit 填当前 HEAD",
        "角色要求提交时，必须 git add 并 commit",
        "\"branch\"",
        "\"commit\"",
        "\"summary\"",
    } {
        if !strings.Contains(out, want) { t.Errorf("prompt 缺少 %q", want) }
    }
    if strings.Contains(out, "收尾纪律：全部完成后必须 git add 并 commit（不要 push）") {
        t.Error("仍保留无条件 commit 铁律")
    }
    lower := strings.ToLower(turn.ProtocolRules)
    if strings.Contains(lower, "review") || strings.Contains(lower, "recon") {
        t.Error("ProtocolRules 不应点名具体角色")
    }
}
~~~

先只跑：

~~~text
go test ./internal/executor/turn ./internal/executor/codex -run 'TestRenderPrompt|TestProtocolRules' -count=1
~~~

现行无条件文案应使新增断言失败；记录实际红测输出，不能把断言改成只测常量而不穿过 RenderPrompt。

### 4.2 最小实现与注释

`protocol.go` 文件头职责注释补充“ProtocolRules 是启动 prompt 与 codex 常驻指令共同消费的提示词契约；本 task 不改变 trailer 解析”。将常量全文替换为：

~~~go
const ProtocolRules = `1. 提问纪律：任何需要人决策的问题，输出单行 JSON {"ask":"<问题>"}
   然后结束本回合。协调者的回答会作为下一条消息发给你。
   禁止自行假设，禁止用其它格式提问。
2. 收尾纪律：是否提交听角色纪律。
   角色禁止修改工作树时，不创建新提交，commit 填当前 HEAD；无新提交时 commit 仍填当前 HEAD，不能为空。
   角色要求提交时，必须 git add 并 commit（不要 push）。
   无论是否创建新提交，都必须输出单行 JSON：{"branch":"<分支>","commit":"<hash>","summary":"<50字内摘要>"}
   作为本回合最后一行。
3. 只在当前分支工作，不切分支、不改 git 配置。`
~~~

文本必须同时说明禁止修改工作树的角色不 commit 并填当前 HEAD，以及要求提交的角色仍必须 commit；不能点名 review/recon。trailer schema 与 `ParseTrailer` 不改，字段仍只有 branch、commit、summary；不允许空 commit。实现中的成功路径仍由现有 RenderPrompt 返回，错误仍返回原错误。

### 4.3 绿测与范围

只跑：

~~~text
go test ./internal/executor/turn ./internal/executor/codex -run 'TestRenderPrompt|TestProtocolRules' -count=1
go test ./internal/executor/turn ./internal/executor/codex -count=1
~~~

两条均取得退出 0 才判绿。验收清单：RenderPrompt 含条件提交、只读填 HEAD、产出型仍提交、三 trailer 字段不变、协议不点名具体角色；codex 复用同常量的既有测试仍绿。

## 5. Task B241：HTTP 工作流投影保留 Purpose

### 5.1 文件范围、现状边界与基线后红测

只改 `internal/proto/ledger.go`、`internal/agentd/ledgerapi.go`、`internal/agentd/ledgerapi_test.go`。只读核对 `web/src/api/ledger.ts` 的 `NodeOverride.purpose?: string` 已存在，不改 web 文件。`handleFlows` 列表原样序列化账本 Def，非本 task；派发路径直读账本，非本 task。声明缝为 `handleFlowGet / handleFlowPut ← console read-modify-write`。

### 5.2 基线后测试与测试范围

复用 `ledgerapi_test.go` 已有真实 SQLite + httptest harness：`newLedgerEnv`、`seedAgentdLedger`、`ledgerGet`、`ledgerPut`，只新增以下 seam 断言，不另造数据库或服务器夹具。测试 import 复用现有 bytes、encoding/json、net/http：

~~~go
func TestFlowNodePurposeSurvivesHTTPGetPutGet(t *testing.T) {
    env := newLedgerEnv(t)
    seedAgentdLedger(t, env.ledger)
    if _, err := env.ledger.PutWorkflow("charter", ledger.WorkflowDef{
        Nodes: []ledger.NodeDef{{Name: "review"}, {Name: "done"}},
    }); err != nil {
        t.Fatalf("seed charter workflow: %v", err)
    }
    initial := []byte("{\"nodes\":[{\"name\":\"review\",\"override\":{\"purpose\":\"review\"}},{\"name\":\"done\"}]}")
    code, body := ledgerPut(t, env.testAgentdEnv, "/api/flows/charter", string(initial))
    if code != http.StatusOK { t.Fatalf("initial put code = %d, body = %s", code, body) }
    code, body = ledgerGet(t, env.testAgentdEnv, "/api/flows/charter")
    if code != http.StatusOK { t.Fatalf("first get code = %d, body = %s", code, body) }
    var first struct {
        Nodes []json.RawMessage `json:"nodes"`
    }
    if err := json.Unmarshal([]byte(body), &first); err != nil { t.Fatalf("decode first get: %v", err) }
    if len(first.Nodes) != 2 { t.Fatalf("first nodes = %d, want 2", len(first.Nodes)) }
    var review struct {
        Override struct {
            Purpose string `json:"purpose"`
        } `json:"override"`
    }
    if err := json.Unmarshal(first.Nodes[0], &review); err != nil { t.Fatalf("decode review: %v", err) }
    if review.Override.Purpose != "review" { t.Fatalf("first purpose = %q", review.Override.Purpose) }
    putBody := "{\"nodes\":[" + string(first.Nodes[0]) + "," + string(first.Nodes[1]) + "]}"
    code, body = ledgerPut(t, env.testAgentdEnv, "/api/flows/charter", putBody)
    if code != http.StatusOK { t.Fatalf("read-modify-write code = %d, body = %s", code, body) }
    code, body = ledgerGet(t, env.testAgentdEnv, "/api/flows/charter")
    if code != http.StatusOK { t.Fatalf("second get code = %d, body = %s", code, body) }
    var second struct {
        Nodes []struct {
            Override struct { Purpose string `json:"purpose"` } `json:"override"`
        } `json:"nodes"`
    }
    if err := json.Unmarshal([]byte(body), &second); err != nil { t.Fatalf("decode second get: %v", err) }
    if len(second.Nodes) != 2 || second.Nodes[0].Override.Purpose != "review" {
        t.Fatalf("purpose lost after GET PUT GET: %+v", second.Nodes)
    }
}

func TestLedgerNodeWireOmitsZeroPurpose(t *testing.T) {
    raw, err := json.Marshal(ledgerNodeWire(ledger.NodeDef{Name: "legacy"}))
    if err != nil { t.Fatal(err) }
    if bytes.Contains(raw, []byte("\"purpose\"")) {
        t.Fatalf("zero purpose must be omitted: %s", raw)
    }
}
~~~

先写红测并运行：

~~~text
GOMODCACHE=/root/.handoff/tmp/27c30ff0/b273-gomodcache GOSUMDB=off go test ./internal/agentd -run 'TestFlowNodePurposeSurvivesHTTPGetPutGet|TestLedgerNodeWireOmitsZeroPurpose' -count=1
~~~

缺字段基线应使新增断言失败；记录实际输出。此处必须经过真实 HTTP GET → JSON RawMessage → PUT → HTTP GET，投影函数单测不能替代 seam。

### 5.3 最小实现与序列化边界

在 `internal/proto/ledger.go` 中把 NodeOverride 完整改为：

~~~go
type NodeOverride struct {
    Executor   string `json:"executor,omitempty"`
    Discipline string `json:"discipline,omitempty"`
    Target     string `json:"target,omitempty"`
    Model      string `json:"model,omitempty"`
    Purpose    string `json:"purpose,omitempty"`
}
~~~

在 `internal/agentd/ledgerapi.go` 将现有 `ledgerNodeWire` 完整改为以下函数；Produces 指针投影必须同时保留：

~~~go
func ledgerNodeWire(node ledger.NodeDef) proto.NodeDef {
    // 显式投影指针，保留旧节点字段缺失与新节点显式对象之间的区别。
    var produces *proto.NodeOutput
    if node.Produces != nil {
        produces = &proto.NodeOutput{
            Kind: node.Produces.Kind,
            Path: node.Produces.Path,
        }
    }
    return proto.NodeDef{
        Name: node.Name, Template: node.Template,
        Override: proto.NodeOverride{
            Executor: node.Override.Executor, Discipline: node.Override.Discipline,
            Target: node.Override.Target, Model: node.Override.Model,
            Purpose: node.Override.Purpose,
        },
        Dispatch: node.Dispatch, Verdict: node.Verdict, CarryCardContext: node.CarryCardContext,
        MaxRounds: node.MaxRounds, OmitAcceptance: node.OmitAcceptance, Next: node.Next, OnFail: node.OnFail,
        Gate: proto.Gate{
            RequireAttachment: node.Gate.RequireAttachment,
            RequireAttachmentAny: node.Gate.RequireAttachmentAny,
            RequireAcceptance: node.Gate.RequireAcceptance,
            RequireChildrenDone: node.Gate.RequireChildrenDone,
        },
        HumanBases: node.HumanBases,
        Produces: produces,
    }
}
~~~

保留该函数现有 Produces 指针投影及注释；只新增 Purpose。函数注释补充“详情 GET 的账本到 proto 投影必须保留 Purpose”。proto DTO 注释补充“兼容 wire DTO，旧客户端忽略未知键”。不改工作流定义、不改派发路径、不改其它字段。

手写序列化边界逐处是：ledger NodeOverride → proto NodeOverride → encoding/json HTTP GET → 测试 RawMessage → PUT 解码为 ledger NodeDef → 再次 ledgerNodeWire → HTTP GET。roundtrip 测试锁非零 review；purpose omitempty 锁零值 absent。现有 produces roundtrip 测试继续保留。

### 5.4 绿测与范围

只跑：

~~~text
GOMODCACHE=/root/.handoff/tmp/27c30ff0/b273-gomodcache GOSUMDB=off go test ./internal/agentd -run 'TestFlowNodePurposeSurvivesHTTPGetPutGet|TestLedgerNodeWireOmitsZeroPurpose' -count=1
GOMODCACHE=/root/.handoff/tmp/27c30ff0/b273-gomodcache GOSUMDB=off go test ./internal/agentd -run 'TestFlow|TestLedgerNodeWire' -count=1
~~~

两条取得退出 0 才判绿。验收：详情 GET review purpose、零值 absent、GET nodes 原样 PUT 后第二次 GET 仍 review；旧 nodes 与其它 override 字段不变。

## 6. 跨 task 自审与收口

### 6.1 缺陷族对抗审查

| 缺陷族 | 反问 | 锁点 |
|---|---|---|
| 时序/取消 | child 到期、parent 取消、宽限内失败会不会混淆？ | B243 fake wait 检查后续 ctx deadline；deadline 且 parent alive 返回 nil；parent cancellation 原样错误；宽限失败后继续第二 completed |
| 状态/重复 | 第一条 completed 是否再次吞正文，失败是否挂死？ | B243 wait 次数、非空 final_text 终止、无 completed failed 即刻收口、Attach 多 completed 选择 |
| JSON/缺省 | 缺失、显式零、损坏是否混淆？ | B243 *string；B242 独立字段解码与 Raw；B241 omitempty 与 HTTP roundtrip |
| 误判/路由 | notes 的 pass 会否覆盖 fail，抢救是否有证据？ | B242 首个 verdict、RunOnce 评论与 RecordReviewVerdict |
| 协议兼容 | 条件 commit 是否误伤要求提交角色，字段是否漂移？ | B244 RenderPrompt 同时断言两种角色和三 trailer 字段 |
| 投影/兼容 | GET→PUT 是否抹字段，零值是否污染响应？ | B241 真实 GET→PUT→GET 与 zero-purpose absent |
| 观测/证据 | 成功/抢救是否静默，错误是否有上下文？ | B243 slog；B242 slog + 普通评论；B241 沿现有 handler 日志 |

### 6.2 接缝双向与序列化审计

新增手写投影链完整清单：

1. B243 Event.Data → completedPayload → wait 终止；Event.Data → completedPayload → final message。
2. B242 围栏正文 → strict/salvage → Verdict.Raw/Findings/Notes → RecordReviewVerdict；丢弃元数据 → AddComment body。
3. B244 ProtocolRules → RenderPrompt；trailer 三字段不变。
4. B241 ledger NodeOverride → proto NodeOverride → JSON GET → RawMessage PUT → ledger NodeOverride → JSON GET。

五条 spec seam 逐条有缝级入口：

| seam | 测试入口 |
|---|---|
| waitForTurnEnd ← awaitNode / WaitEvent | TestWaitForTurnEndWaitsForCompletedFinalText、TestWaitForTurnEndGraceDeadlineReturnsSuccess、TestWaitForTurnEndDoesNotSwallowParentCancellation、TestWaitForTurnEndIgnoresFailureDuringGrace、TestWaitForTurnEndReturnsFailureWithoutCompleted |
| finalMessageFromEvents ← clientFinalMessage | TestFinalMessageUsesNonEmptyFinalTextAcrossCompletedEvents，加存量 summary/empty/trailing failure |
| ParseVerdict ← RunOnce / routing | TestParseVerdictSalvagesFirstVerdictWhenNotesIsBroken、TestParseVerdictUsesFirstVerdictNotNotesMention、TestParseVerdictStillRejectsMissingOrUnknownVerdict、TestNodeStepCommentsWhenSalvageDropsNotes |
| ProtocolRules ← RenderPrompt | TestProtocolRulesMakeCommitConditionalOnRole 与既有 RenderPrompt/constant reuse |
| handleFlowGet/Put ← read-modify-write | TestFlowNodePurposeSurvivesHTTPGetPutGet、TestLedgerNodeWireOmitsZeroPurpose |

测试→缝与缝→测试均满足。B243 deadline 是后续 wait ctx 的真实 seam，不是新导出的计时器假缝；内部 helper 不能顶替缝级断言。

### 6.3 用户故事归属

1. 两条 completed 取带围栏后者：B243 2.1/2.2。
2. 单残缺 completed 宽限后摘要收口：B243 deadline 与 final message fallback。
3. 无 completed 的失败即时收口、宽限内失败不切断：B243 wait tests。
4. 损坏 notes 的 pass 按 pass 路由且 Raw 保留：B242 parser/RunOnce。
5. 无围栏/maybe 仍失败、fail 不被 notes 的 pass 覆盖：B242 parser。
6. 只读角色不建新提交且 trailer.commit 为 HEAD：B244 RenderPrompt 条件文案，真机执行阶段验证。
7. 要求提交角色仍提交、协议不点名角色：B244 same seam。
8. review purpose GET→PUT→GET 保持：B241 HTTP roundtrip。

### 6.4 预算、类型标注与真机验收

文件集有界：B243 2 个文件、B242 4 个文件、B244 2 个文件、B241 3 个文件；web 只读核对，无跨包竖切缺口。

实施后必须逐项报告真实结果：

- B243：真实 StepRunner awaitNode 使用 WaitEvent；残缺后 child deadline；deadline 且 parent alive 返回 nil；parent cancel 原样错误；Attach 取得 summary/final_text。
- B242：真实 NodeStep.RunOnce 将 salvaged pass/fail 写入 verdict，Raw 含围栏；丢弃字段有普通 comment；无围栏/maybe 进入 needs-human。
- B244：RenderPrompt 输出条件 commit；turn/codex 共用常量测试；只读角色 trailer.commit 为当前 HEAD、要求提交角色创建新 commit。
- B241：真实 agentd GET→PUT→GET 返回 review purpose；零值 JSON 无 purpose；其它列表/派发路径未改。

本计划节点不把未执行真机结果写成 pass。

### 6.5 占位符扫描与协调者收口

计划不含未定项、模糊错误处理或只用任务编号代替内容的占位符。测试代码完整给出；复用的已有 harness 明确为 `wire_test.go`、`node_test.go`、`ledgerapi_test.go` 中的真实构造，断言逐条列全。图查询原始错误全文在台账中，计划不把图缺失写成实现结论。

四 task 各自先红测、最小实现、task 绿测；不以全量测试替代 task 判据。四 task 完成后由协调者执行跨 task 最终门禁（本步骤由协调者执行，不派发）：当前分支运行 `git diff --check`、四组最小命令及所需全量回归；只在亲自取得退出 0 原始输出后写 pass。协调者复核无公共框架、文件未越界、台账含红绿输出，再 git add/commit；不 push、不切分支、不改 git 配置。
