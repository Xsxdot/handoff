# B273 acceptance 台账

- 2026-08-27 卡进 acceptance。审查 pass，对象 `b07d76e32`，工作树 `cards/B273-charter-2`。
- 复跑（本机，本轮）：`go build ./...` 退出 0。`go test ./internal/ledgerstep -count=1` ok 1.199s。`go test ./internal/executor/turn -count=1` ok 0.787s。`go test ./internal/executor/codex -count=1` ok 6.990s。`go test ./internal/agentd -run 'TestFlowNodePurposeSurvivesHTTPGetPutGet|TestLedgerNodeWireOmitsZeroPurpose|TestFlow|TestLedgerNodeWire' -count=1` ok 1.819s。
- 变异 M1（B243，锚点唯一）：`waitForTurnEnd` 在「收到带 final_text 的 completed」处改成 `true || (payload.FinalText…)`。`go build ./internal/ledgerstep/` 过。`TestWaitForTurnEndWaitsForCompletedFinalText` FAIL：`wait calls = 1, want 2`。还原后绿。
- 变异 M2（B242 last-wins）：`FindStringSubmatch`→`FindAll` 取最后一次。编译过，但 `TestParseVerdictUsesFirstVerdictNotNotesMention` **仍绿**。原因：夹具 notes 是 `bad \"verdict\":\"pass\"`，正则 `"verdict"\s*:\s*"(pass|fail)"` 在 `:` 与 `"pass"` 之间吃不下反斜杠，第二次命中不存在。实现仍是第一次匹配。记夹具牙口缺口，不挡本卡。
- 变异 M2b（B242 恒返回 pass）：`firstVerdictValue` 改为 `return "pass", true`。编译过。同测试 FAIL：`verdict 被 notes 引用覆盖: {Pass:true ... Raw:{"verdict":"fail",...}}`。还原后绿。证明该测试至少锁住「抢救结果必须是 fail」。
- 变异 M3（B244）：`是否提交听角色纪律` 改回无条件 commit 句。编译过。`TestProtocolRulesMakeCommitConditionalOnRole` FAIL：缺少该短语 +「仍保留无条件 commit 铁律」。还原后绿。
- 变异 M4（B241）：`Purpose: node.Override.Purpose` → `Purpose: ""`。编译过。`TestFlowNodePurposeSurvivesHTTPGetPutGet` FAIL：`first purpose = ""`。还原后绿。
- 真机 B244：本卡 review 任务 `85f54cc7` completed.commit = `b07d76e32227269ece1991350b632653f193d243`，与 implement 提交同一。只读审查轮未另建提交。
- 真机 B241：现役 agentd `69f25dfd`（不含本修）。`handoff workflow show charter` 账本 review.override.purpose=`review`。`GET /api/flows/charter`（Bearer token）review.override 只有 `discipline`，无 `purpose`。与立项一致，修要等换二进制。分支内 httptest 已锁修复。
- 真机 B243/B242 双发窗口 / 破碎 JSON 整轮：本轮未再造现场。消费侧由单测+变异锁住。下次 charter 裁决轮观察是否还转「等人·没有围栏」。
- 无 codegraph/diffs 本卡视图，跳过图对账。
