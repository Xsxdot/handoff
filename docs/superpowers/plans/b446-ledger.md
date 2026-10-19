# B446 台账

## 偏离与自主决定

1. 分支起点取 origin/main 2e339348（B444 上线版）而非本地 main d2327e33：本地 main 有 5 个未推送的 schema 版本闸提交、不含 B444；缺陷在 B444 代码上。
2. 收尾兜底按人裁决 A 实现（同会话自坐 → RebindSeatMode expect=自身 改写 coordinate/push + 承载）；不覆盖重建回合（Wake rebuilt 路径），理由：该路径席位已有旧会话，自坐需 rebind --self，简报修已禁。
3. 真机复用隔离靶子 /root/b444-iso（7792，非生产），换入本分支 linux 二进制（旧版留 bin/handoff.pre-b446），web 用 stub 构建。
4. 卡上直接走 inline（本会话 bind 坐 B446），未派发节点：任务指定本会话处理且需真机隔离实例。

## 事实与命令

- 隔离靶子 linux-01 /root/b444-iso（agentd 7792 未运行；生产 agentd pid 2874754 未动）。
- D5 卡行：`('D5','cli:opencode#ses_eac5ea14dffe7ZkCFEgNWUVgbV','bind','pull','已完成')`；seat_bearings 无 D5 行。
- agentd.log 10:10:53：`坐下被拒：席位非空` source=coordinate → `协调者回合已跑但席位未落`。
- 协调者 transcript：bash `m.sh card bind D5 --cli opencode --session ses_eac5ea14dffe7ZkCFEgNWUVgbV` → `{"ok":true}`（会话 id 从 opencode 日志里读出）。
- 红：`go test ./internal/keystone/ -run 'TestB446|TestB435Briefing'` → FAIL（launch 首轮简报缺 "本轮结束后由 agentd"）。
- 绿：改 `briefingFirstRound` 后同命令 → `ok github.com/Xsxdot/handoff/internal/keystone 0.472s`。
- 提交 8da3bd58（简报修）。
- 人裁决 A（经发起会话转达，2026-10-19）。
- 红：`go test ./internal/agentd/ -run TestB446` → 两子测 FAIL `状态=409 body={"error":"协调者已启动但席位 CAS 冲突；新会话未自动终止，请人工回收","session_id":"sess-self"}`。
- 绿：加 adoptSelfSeatedCoordinator 后 `go test ./internal/agentd/ -run 'TestB446|TestCoordLaunch|TestCoordRebind'` → `ok github.com/Xsxdot/handoff/internal/agentd 2.228s`。
- 全量 `go test ./...` → 仅 `cmd TestSessionSendActorResolution` 红（`--cli/--session 与当前环境会话不一致`，CLAUDE_CODE_SESSION_ID 环境敏感，既有已知项）；`env -u CLAUDE_CODE_SESSION_ID go test ./cmd/` → `ok github.com/Xsxdot/handoff/cmd 78.561s`；其余包全 ok。
- 真机（隔离靶子 linux-01 /root/b444-iso 127.0.0.1:7792，本分支 linux 二进制；生产 agentd pid 2874754 未动）：
  - D6（常规 coordinate）：`{"woke":true,"session_id":"ses_eac2648a0ffeyIlAxmmGhZrm4y"}`，卡行 coordinate/push，承载 free@linux-01，无 needs_human。
  - D7（卡标题诱导「先 card bind 自己坐下」）：协调者未自坐，`{"woke":true,…ses_eac24e778…}`，coordinate/push + 承载，无 needs_human（简报修生效）。
  - D8（回合中按 D5 原样手动 `card bind D8 --cli opencode --session <回合会话>` → `{"ok":true}`）：收尾日志 `坐下被拒：席位非空` → `协调者首轮已自己坐下，收尾改写为机器人席位` old_source=bind；事件 seq166 driver_seat_bound、seq184 driver_takeover X→X；卡行 coordinate/push，承载 free@linux-01；HTTP `{"woke":true,…}` exit=0，无 needs_human（兜底生效）。
  - 收尾：停隔离 agentd，换回原二进制（本分支版留 bin/handoff.b446）。
- review（独立 subagent，只读）：PASS。新鲜复跑 agentd/keystone/ledger 全 ok；变异（禁 adoptSelfSeatedCoordinator 调用）→ TestB446 两子测红 409，还原后绿。Minor 两条落 docs/roadmap.md「来自 B446 finish」。
- finish：未合并、未推送（合并归人）；分支 fix/b446-launch-self-seat。
