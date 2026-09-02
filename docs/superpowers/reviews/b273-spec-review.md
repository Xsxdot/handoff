# B273 spec 审查（B241 / B242 / B243 / B244）

审查对象：`docs/superpowers/specs/b273.md`（待用户批准稿）  
对照代码：工作树 `fix/batch2-bugs` @ `967e3a589`（与 main 同提交，spec-only）  
源卡：B241 / B242 / B243 / B244 并入 B273

## Summary

四条里 B241、B244 的改法对准了各自的真实根因（HTTP 投影丢字段；协议层与角色层互斥指令）。B243 / B242 明确不改生产侧，是消费侧止损：对已记录的复现（双 `completed` 抢跑、notes 裸引号打碎 JSON）可以把可见白跑收住，但生产侧半成品 `completed` 和裸 JSON notes 都还在。主导残留风险是 B243：宽限写了「到期仍收口」，却没规定如何打断正在阻塞的 `Client.WaitEvent`。按现状 `awaitNode` 的接法实现，「只有一条无 `final_text` 的 completed」要么挂死，要么把 `DeadlineExceeded` 误报成「未取到裁决报文」——这是老执行器和 opencode 对账补发路径的活路径，不是文风问题。

## Per-bug

### B243 — 止损（文档复现可收；生产侧双发仍在；宽限打断未写死）

根因核对成立。发射点只有 `internal/agentd/manager.go:3184` 的 `AppendEvent(EventTypeCompleted)`；守卫在 `manager.go:3145-3158` 只丢 `waiting_review` 之后的**失败**结果，迟到成功故意放行。空 `FinalText` 在 `3180-3183` 被收成缺字段（指针 nil，`omitempty`）。opencode 实时 `mapIdle` 会带 `FinalText`（`internal/executor/opencode/adapter.go:2029-2032`），但对账补发的 finish **故意不带**（`internal/executor/opencode/reconcile.go:239-242` 只抄 branch/commit/summary）。65–214ms 双 `completed` 与「先残缺再补全文」对得上。

消费侧现状：`waitForTurnEnd`（`internal/ledgerstep/wire.go:34-36`）见 `completed` 立刻返回，不看 payload；`awaitNode`（`internal/ledgerstep/runner.go:417-419`）随后 `WaitEvent` 已把 cursor 推过该 seq，再 `Attach` → `finalMessageFromEvents`。`finalMessageFromEvents` 第一条循环从后往前认**最后一条** `completed`（`wire.go:63-83`）：窗口里只有残缺那条时回落 `summary`，围栏丢失，与源卡 B229.1 / B156.3 一致。

spec 推荐「等带非空 `final_text` 的 completed + 秒级宽限 + 取报文时有 text 的优先」能关掉**已记录的双发窗口**（65ms / 214ms ≪ 秒级）。生产侧双发不改，这是止损，spec 自己也写了 Out of Scope，可接受——前提是宽限真能在「没有第二条事件」时返回。这一点 spec 没写，见 Issue 1。

`turn_failed` 立刻终态：对「从未出现 completed 的真失败」是对的（`TestWaitForTurnEndAcceptsTurnFailed`，`wire_test.go:137-146`）。B180 族 trailing `turn_failed` 在现网会被 `manager.go:3155` 丢掉，送不进 `WaitEvent`。`TestFinalMessagePrefersCompletedOverTrailingTurnFailed`（`wire_test.go:85-98`）锁的是 Attach 快照侧 completed 优先，不是 wait 侧。宽限期内若仍按字面把 `turn_failed` 当立刻终态，会在「残缺 completed → turn_failed → 带 text 的第二条 completed」上重开 TOCTOU；现网守卫挡住了这条，spec 却没把这个依赖写进去（Issue 3）。

### B242 — 止损（对准 notes 截断；抢救规则未钉死，会假绿 / 静默丢 findings）

根因核对成立。`ParseVerdict`（`internal/ledgerstep/verdict.go:32-45`）只对最后一个围栏做整段 `json.Unmarshal`；失败则 `node.go:196-199` `haltForHuman("裁决解析失败")`。调用方没有第二条解析路径。源卡 `"enabled":true` 这类 notes 裸引号会整块失败，pass 也转等人。

「先严格、失败后只在同一围栏里抢救」能关掉这条复现，且与 B243 用不同夹具，方向对。生产侧格式不改，是止损，Out of Scope 已记。

未钉死的三点会在实现时走样：

1. 「能读到 `verdict` ∈ {pass,fail}」没说取围栏里第一条还是最后一条。围栏级已经 last-block-wins（`verdict.go:37`）。若抢救再 `FindAll` last-wins，fail 的 notes 里引用 `"verdict":"pass"` 会假绿。
2. findings 解不出变空切片：路由只看 `verdict.Pass`（`node.go:267-269`），fail 仍走 `OnFail`，不会因此改列。但 `RecordReviewVerdict` 只存 `verdict.Raw`（`node.go:204`）。spec 写「调用方打进 timeline 的原文仍是整段围栏」——那是**解析失败**的 `haltForHuman` 评论（`node.go:199`）。抢救成功走的是落账路径，不会自动写那条评论。
3. `NotesDiscarded` 没有调用方契约：成功路径今天不读这个标志。

见 Issue 4。

### B244 — 治本（矛盾在 prompt 层；机械上仍拦不住 review 去 commit）

根因核对成立。`ProtocolRules` 第 2 条（`internal/executor/turn/protocol.go:43-45`）无条件「必须 git add 并 commit」，经 `RenderPrompt` 灌进所有 executor；codex 常驻指令复用同一常量。角色层：`charter-review` v2 红线是「审查是只读回合：不 commit、不改文件、不建分支内容」；`charter-recon` v1 则是双分支——无 `codegraph/` 时「不 commit、不建目录」，有视图时「只动 `codegraph/diffs/`，一次 commit」（`handoff discipline get` 实读）。`charter-v4.json` review 节点 `purpose=review` + `charter-review`（约 94-100 行），图对账节点只有 `charter-recon`、没有 purpose 覆盖（约 129-134 行）。purpose 与只读不是一一对应，spec 拒绝按 purpose 分叉铁律，这点是对的。

trailer 解析不校验「是不是新提交」：`ParseTrailer` 只认字段；`kind=="finish"` 时 grok/codex 用 `firstNonEmpty(tr.Commit, git HEAD)`（`internal/executor/grok/adapter.go:643-647`，codex 对称）。`GitTurnStatus.hasNew`（`internal/executor/turn/gitprobe.go:42`）只在 trailer 缺失的兜底路径用。finish + `commit=当前 HEAD` 今天已经能 completed。铁律改成「听角色纪律、无新提交则填 HEAD、commit 不许空」后，review / recon 无图 与 recon 有 diff 的矛盾从协议层消失，改由角色正文自己的 if/then 说话。这是治本。

机械上：本卡不阻止 review 执行者真的去 commit。可接受——缺陷是互斥指令，不是缺 git hook；在协议层禁 commit 会误伤 recon 的「必须交视图」。残留是模型仍可能习惯性交一份台账，那是角色纪律 / 后续卡的事，不是本卡铁律该加闸的。

### B241 — 治本（丢字段只在 GET 投影；派发不走这条路径）

根因核对成立。账本 `internal/ledger/types.go:163-177` 有 `Purpose`；`internal/proto/ledger.go:59-64` 没有。唯一投影 `ledgerNodeWire`（`internal/agentd/ledgerapi.go:124-126`）只抄四个字段。`handleFlowGet`（597-612）走投影；`handleFlowPut`（622-624）直接解码 `[]ledger.NodeDef`，客户端送来就留。派发：`runner.go:346` `PurposeOverride: node.Override.Purpose`；`cardstep.go:52` `ResolveNode` 直读账本。B183 不会沿 CLI/看板派发复活。

控制台读-改-存：`web/src/app/flows/FlowsPage.tsx:71-73` 编辑时 `fetchFlow`（GET `/api/flows/{name}`），保存 `putFlow(name, nodes)`（107）。`NodeEditor` 已有 `purpose` 控件（`web/src/app/flows/NodeEditor.tsx:211-216`），类型在 `web/src/api/ledger.ts:96-103`。GET 不回 purpose 时控件是空，保存就把账本里的 `purpose=review` 抹掉。补 proto + 投影是治本。

已存在的不一致：`handleFlows` 列表（575-577）把 `workflow.Def` 原样进 JSON，**已经含 purpose**；详情 GET 丢掉。编辑器走 GET 不走列表的 nodes，所以今天只有详情路径在害人。修 GET 之后两边应对齐。接缝 5 只 marshal 投影函数，锁不住 HTTP 读-改-存（Issue 5）。

## Issues

### Issue 1 -- Severity: Critical

- File: `docs/superpowers/specs/b273.md:50-55`（方案 §B243 第 3 点）；活路径 `internal/ledgerstep/runner.go:417-419`、`internal/client/client.go:1412-1430`、`internal/ledgerstep/wire.go:25-38`
- Description: 用户故事 2 要求「只有一条没有 `final_text` 的 completed：宽限到期后仍能收口，取摘要，不挂死」。生产接法是阻塞的 `Client.WaitEvent`：返回一条可动作事件就把 cursor 写到该 seq（`client.go:1420-1424`），下一次 `WaitEvent` 从下一条开始阻塞，没有自己的超时（超时只来自传入的 `ctx`）。第一条残缺 `completed` 交付后 cursor 已越过它；若第二条永远不来（老执行器、或对账补发的 finish 根本不带 `FinalText`，见 `reconcile.go:239-242`），下一次 `WaitEvent` 会一直堵到外层 ctx 或 stalled。spec 写了「宽限时钟必须可注入」「到期仍没有才返回」，但没写：
  1. 宽限必须做进**后续那次** `wait(ctx)` 的 child deadline（`context.WithTimeout(parent, grace)`），而不是 `Sleep` 后再 Attach、也不是另起一个不取消的 `WaitEvent` goroutine；
  2. child `DeadlineExceeded` 且 **parent 仍活着** 时，`waitForTurnEnd` 返回 **nil**（成功），然后 `clientFinalMessage`/`Attach` 回落 summary——`RecentEvents` 窗口 100 条（`internal/agentd/server.go:56-57,898`）仍看得到第一条；
  3. parent 取消（停环节、agentd 退出）仍要原样报错，不得被宽限吞掉。
  按字面把「等到第二条」做成再调一次无 deadline 的 `wait(ctx)`，老执行器挂死。做成 `WithTimeout` 却把 `DeadlineExceeded` 从 `awaitNode` 包成 `等回合终态: context deadline exceeded`（`runner.go:420`），则故事 2 变成 `node.go:192-194` 的「未取到裁决报文」等人——今天缺 `final_text` 仍能靠 summary 收口（`wire_test.go:47-52`），这是回归。单测注入的 fake `wait` 若不阻塞在 `ctx.Done()` 上，接缝 1 绿不了这个活路径。spec 还写「不为宽限单独导出计时器类型当接缝」，更必须把「后续 wait 的 ctx 带 deadline」写成判据。
- Suggestion: 在 B243 方案第 3 点和实现决定里补上上面 1–3；接缝 1 加一条：残缺 completed 之后打进 `wait` 的 ctx 必须有 deadline，测试侧的 fake wait 要在 `ctx.Done()` 上返回，到期断言 `waitForTurnEnd` 返回 nil 而非 error。禁止泄漏未取消的 `WaitEvent` goroutine（cursor 会在 `Done` 之后仍被推进）。
- Status: open

### Issue 2 -- Severity: Important

- File: `docs/superpowers/specs/b273.md:119-123`；存量 `internal/ledgerstep/wire_test.go:114-133`；`internal/ledgerstep/wire.go:34-36`
- Description: 接缝 1 要求「先无 `final_text` 再有 → 等到第二条」。今天 `waitForTurnEnd` 只看 `event.Type`，存量 `TestWaitForTurnEndSkipsNonTerminalEvents` 用**无 payload** 的 `completed` 当终态。新判据下无 payload = 没有非空 `final_text` = 进入宽限，这条测试要么再调 wait 越界，要么被改成「立刻到期的宽限」而继续把「等到第一条 completed」当绿——正好是 spec 写的反面。接缝清单没有点名这条存量测试必须改夹具（completed 带非空 `final_text` 才返回），也没要求 wait 侧解析 `Event.Payload`（与 `finalMessageFromEvents` 同一套指针/缺字段语义）。
- Suggestion: 写明 `waitForTurnEnd` 必须反序列化 completed payload；非空 `*final_text` 才终态；缺字段进入宽限；显式 `final_text:""` 不算「有」。点名改 `TestWaitForTurnEndSkipsNonTerminalEvents`：最后一条事件要带非空 `final_text`。接缝 1 的双发用例必须在 fake 事件里带 JSON payload，不能只填 Type。
- Status: open

### Issue 3 -- Severity: Important

- File: `docs/superpowers/specs/b273.md:51`（`turn_failed` / `failed` 立刻终态）；`internal/agentd/manager.go:3145-3158`；`internal/ledgerstep/wire_test.go:85-98`
- Description: 宽限开始后，字面规则会让下一条 `turn_failed` 立刻结束等待，随后 `finalMessageFromEvents` 若快照里还只有残缺 completed，就回落 summary，第二条带围栏的 completed 作废。现网 `handleResult` 在 `waiting_review` 后丢弃 !OK，B180 的 trailing EOF 通常进不了 `WaitEvent`，所以**不会重开已记录的 B243 双 completed 复现**。但 spec 没声明「立刻终态」依赖这道守卫；对账/流中断若再写出一条已落库的 `turn_failed`，宽限就被切断。`TestFinalMessagePrefersCompletedOverTrailingTurnFailed` 只覆盖 Attach，不覆盖 wait 在宽限期内收到 `turn_failed`。
- Suggestion: 二选一写进正文：（a）宽限期内 `turn_failed`/`failed` 不打断等第二条带 `final_text` 的 completed，到期再交给现有 `finalMessageFromEvents`（completed 优先于 trailing turn_failed）；（b）明确依赖 `manager.go:3155`，并加接缝：宽限期内收到 `turn_failed` 时的预期（立刻返回 vs 继续等）。未出现过任何 completed 的真失败仍必须一次返回，避免失败回合挂死。
- Status: open

### Issue 4 -- Severity: Important

- File: `docs/superpowers/specs/b273.md:63-68`；`internal/ledgerstep/verdict.go:37-45`；`internal/ledgerstep/node.go:196-204`；存量夹具 `internal/ledgerstep/verdict_test.go:20-27`
- Description: 抢救规则有三个洞，实现按字面做会出假绿或静默丢证据。
  1. 没说围栏内多个 `"verdict":"pass|fail"` 取哪一个。抄 last-block-wins 就会 last-match。fail 块 notes 里写「不要把 `"verdict":"pass"` 当成结论」时，整段 JSON 已碎，抢救会路由成 pass，卡进下一列。源卡现场是 `"enabled":true`，这条是相邻但真实的误扫。
  2. spec 说 notes 丢弃时「调用方打进 timeline 的原文仍是整段围栏」。解析失败才走 `haltForHuman` 把 `message` 写成评论（`node.go:199`）。抢救成功走 `RecordReviewVerdict(..., verdict.Raw)`（`node.go:204`）。若 `Raw` 被改成重建的 `{"verdict":"pass"}`，findings/notes 从事件流蒸发，看板上看起来像干净 pass。
  3. findings 解不出变空：fail 仍 `OnFail`（`node.go:267`），列不会走错；但 `Outcome.Verdict.Findings` 变空，日志 `findings=0`，自动续跑的人只能去啃 raw。不算改路由，算静默降质。
- Suggestion: 钉死：只扫**已经切出的最后一块围栏**；`verdict` 取该正文里**第一个** `"verdict":"pass"|"fail"`（与对象里字段顺序一致，notes 在后）；`Verdict.Raw` 抢救后仍是围栏原文；`NotesDiscarded` / findings 清空时 `node.go` 必须再写一条普通评论（或等价 timeline 事件），不能只靠 Raw。接缝 3 加两条夹具：（1）源卡形态 `notes` 含 `"enabled":true` 且 `verdict=pass` → Pass=true，Raw 含损坏 notes；（2）`verdict=fail` 且 notes 里出现 `"verdict":"pass"` → Pass=false。禁止在整份回合文本上扫 `"verdict"`（spec 已有，保持）。
- Status: open

### Issue 5 -- Severity: Important

- File: `docs/superpowers/specs/b273.md:144-145`；活路径 GET `internal/agentd/ledgerapi.go:597-612`、PUT `620-624`、列表 `575-577`；控制台 `web/src/app/flows/FlowsPage.tsx:71-73,107`；投影 `ledgerapi.go:124-126`；`internal/proto/ledger.go:59-64`
- Description: 「PUT 本来就会留下 purpose」在客户端**真的送了**该键时成立。抹掉发生在 GET 投影丢掉 → 编辑器 `node.override.purpose` 为空 → PUT 的 `[]ledger.NodeDef` 没有该键。接缝 5 只 `json.Marshal(ledgerNodeWire(...))`，与已有 `TestLedgerNodeWirePreservesProduces`（`internal/agentd/ledgerapi_test.go:1113-1134`）同级，**不经过** `handleFlowGet` / `handleFlowPut`，也锁不住「GET JSON → 原样 PUT → 再 GET」。列表接口已经把 `workflow.Def` 全量序列化（含 purpose），详情走投影；只测投影函数绿、HTTP 详情仍丢，读-改-存照旧坏。`TestContractFixtures` 里 `FlowDetail` 样本 override 为空（`internal/proto/contract_fixture_test.go:132-136`），加 `omitempty` 字段不会令现有 fixture 变红，也就锁不住新键。
- Suggestion: 接缝 5 改为真实 HTTP：账本节点 `override.purpose=review` → GET `/api/flows/{name}` 的 JSON 含 `"purpose":"review"`；零值不出现；把这次 GET 的 `nodes` 原样 PUT 回去，再 GET 仍在。投影单元测试可留作辅。契约 fixture 加一个带 `purpose` 的 NodeDef/FlowDetail 样本（`-update` 显式刷新）。
- Status: open

### Issue 6 -- Severity: Minor

- File: `docs/superpowers/specs/b273.md:50,111`；`internal/ledgerstep/wire.go:74-78`；`internal/ledgerstep/wire_test.go:54-58`；生产 `internal/agentd/manager.go:3180-3183`
- Description: 生产侧空字符串不会出现在 payload 里（空则指针 nil，键省略）。存量测试把显式 `"final_text":""` 定为报错、禁止回落 summary。spec「非空 `final_text`」与此相容，但没写多条 completed 时遇到空串要跳过、去找带非空 text 的那条；只剩空串时仍应报错而不是 summary。漏写的话接缝 2 可能改掉这条存量语义。
- Suggestion: 接缝 2 保留「显式空串报错」；多条时跳过空串，优先非空 `final_text`，都没有再回落缺字段那条的 summary。
- Status: open

### Issue 7 -- Severity: Minor

- File: `docs/superpowers/specs/b273.md:136-140`；`internal/executor/turn/protocol.go:40-46`；recon 纪律实读（`charter-recon` v1「提交：…一次 commit」与「无图项目…不 commit」）
- Description: 接缝 4 禁止无条件「必须 git add 并 commit」作为**唯一**收尾指令，并要求出现「听角色纪律」「无新提交则填 HEAD」。若实现把第 2 条收成「只读则永不 commit」，recon 有视图时不再被协议层要求提交。spec 故事 7 有「产出型角色仍须 commit」，接缝没钉这半句。
- Suggestion: 接缝 4 同时断言渲染结果含「角色要求提交则必须 commit」（或等价），且 trailer 三字段名不变。不在协议层点名 review/recon。
- Status: open

## Defect-family answers

- **生命周期 / 状态机中断**：宽限在 `awaitNode` 持运行锁的阻塞段里（`runner.go:405-430`）。agentd 重启会丢掉进程内在飞环节（`cardstep.go:11-12` 已声明不恢复），宽限中途重启与今天中途 Ctrl-C 同类，卡上没有终态——可接受，但 Issue 1 的挂死比「重启丢在飞」更糟，因为它发生在**不重启**的老执行器路径。`WaitEvent` 断线会重连并从 cursor 续拉（`client.go:1390-1395`）；cursor 已过残缺 completed 时，重连仍能接到第二条。宽限必须是 parent ctx 的 child：停环节要能取消，不能只靠可注入时钟的 `Sleep`。
- **静默失败 / 误导报错**：B242 抢救成功但丢掉 findings/notes，若不把 Raw 钉成围栏原文、也不写评论，就是「报 pass/fail 但没把证据留下」——算静默降质，不是「报成功但没做完列转移」。Issue 4。B243 若把宽限超时上抛成「未取到裁决报文」，是误导报错（报文在第一条 completed 的 summary 里）。Issue 1。
- **假红 / 假绿测试**：接缝 1 的 fake wait 不阻塞 ctx 则挂死测不出（Issue 1）。存量无 payload 的 completed 测试可能继续当绿（Issue 2）。接缝 5 不经 HTTP 读-改-存（Issue 5）。B243/B242 分夹具这条写对了，必须保住。`TestFinalMessagePrefersCompletedOverTrailingTurnFailed` 不能冒充「宽限期内收到 turn_failed」的接缝（Issue 3）。
- **序列化边界**：B241 Purpose 账本 → proto → JSON → 前端 → PUT：前端类型和控件已在；缺的是 GET 投影和一条穿透 HTTP 的往返断言（Issue 5）。列表 GET 已穿透（`handleFlows` 直接 `workflow.Def`），详情 GET 没有。B243 `final_text` 指针 vs 缺字段：生产 `omitempty` + nil 指针；消费 `*string`；显式空串报错。spec 应用「非空」对齐，别把空串当缺字段（Issue 6）。
- **门禁绕过**：B244 只改 prompt，review 执行者仍可 `git commit`。可接受，因为本缺陷是互斥指令而不是安全闸；机械禁 commit 会破坏 recon 必交视图。trailer schema 不动、空 commit 仍禁止，completed 链上的 hash 不会因本卡变空。无 trailer 且无新提交仍走 question/失败兜底（`GitTurnStatus.hasNew`），故事 6 依赖模型**仍然输出 trailer**。

## Spec approval recommendation

**修订后再批。**

四条方向都对，B241/B244 可以按当前正文落地；B243/B242 的止损也够关掉源卡现场。不能批的原因是 Issue 1：宽限与阻塞 `WaitEvent` 的接法没写死，按字面实现会在故事 2 上挂死或误报。以下是批准前必须补进 spec 正文的最小改动（不是代码）：

1. **B243 宽限 vs WaitEvent**（Issue 1）：残缺 completed 之后，后续 `wait(ctx)` 使用 `WithTimeout(parent, grace)`；到期且 parent 未取消 → `waitForTurnEnd` 成功返回，再 Attach 回落 summary；禁止无 deadline 的二次 WaitEvent，禁止泄漏 WaitEvent goroutine。接缝 1 的 fake wait 必须听 `ctx.Done()`。
2. **B243 wait 解析 payload + 改存量测试**（Issue 2）：`waitForTurnEnd` 认非空 `final_text`；点名改 `TestWaitForTurnEndSkipsNonTerminalEvents` 夹具。
3. **B243 宽限期内 turn_failed**（Issue 3）：写清（a）宽限内不打断 或（b）依赖 `manager.go:3155` 并加接缝。
4. **B242 抢救规则**（Issue 4）：第一个 `verdict`、Raw 保持围栏原文、丢弃可见、两条夹具（`"enabled":true` pass；fail notes 内嵌 `"verdict":"pass"`）。
5. **B241 HTTP 往返**（Issue 5）：GET `/api/flows/{name}` → 原样 PUT → 再 GET，锁住 `purpose=review`。

Issue 6、7 可在实现计划里消化，不挡批准，但写进 spec 成本低，建议一并带上。
