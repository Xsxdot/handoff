# B273 spec 台账

- 2026-08-27 用户要求：子 agent 清 B246/B224；第一批四张卡聚合成一张做。
- 建卡 `handoff card add "批 2：环节白跑止损（B241/B242/B243/B244）" --project handoff --priority 高` → B273。
- 工作树 `/Users/sycm/.handoff/worktrees/batch2-bugs` 分支 `fix/batch2-bugs` @ `967e3a589`。
- B241 派发路径实读：`internal/ledgerstep/runner.go` `PurposeOverride: node.Override.Purpose`；`internal/agentd/cardstep.go` `ResolveNode` 后同样持账本 `ledger.NodeDef`。HTTP GET `handleFlowGet` 才走 `ledgerNodeWire`。PUT `handleFlowPut` 直接解码 `[]ledger.NodeDef`。前端 `web/src/api/ledger.ts` `NodeOverride.purpose?` 已在。
- B243 等终态实读：`waitForTurnEnd` 对 `EventTypeCompleted` 立即返回；`awaitNode` 用 `cl.WaitEvent`；随后 `clientFinalMessage` → `finalMessageFromEvents` 倒序，无 `final_text` 回落 `summary`。源卡观测间隔 65ms / 214ms。
- B242 实读：`ParseVerdict` 围栏内整段 `json.Unmarshal`。
- B244 实读：`ProtocolRules` 第 2 条无条件 commit；`RenderPrompt` 无 purpose 参数；grok/codex `kind=="finish"` 接受 trailer.commit=HEAD。
- 定级 L2：四条局部修，不付 L3 契约冻结成本。B241 加 omitempty 字段；B244 不改 trailer schema。
- 弃选记录见 spec 各节，不在此重复。
- 2026-08-27 独立审查（`docs/superpowers/reviews/b273-spec-review.md`）：修订后再批。Critical = B243 宽限未规定如何打断阻塞 `WaitEvent`（`runner.go:417-419` / `client.go:1412-1430`）。Important = wait 必须解析 payload 并改存量无 payload 测试；宽限期内 turn_failed；B242 抢救取第一个 verdict + Raw 原文 + timeline；B241 必须 HTTP 往返。Minor = 显式空串、产出型角色仍须 commit。
- 审查选 a：宽限期内 turn_failed 不打断。不选 b（依赖 `manager.go:3155` 丢弃守卫），因对账/流中断仍可能写出已落库的 turn_failed。
- 用户 2026-08-27「OK，改吧」→ spec r1 回写上述五条 + 两条 Minor。
- 用户 2026-08-27 批准 r1，交棒 plan：派 linux-01 / codex。
