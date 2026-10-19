# B446 台账

## 偏离与自主决定

1. 分支起点取 origin/main 2e339348（B444 上线版）而非本地 main d2327e33：本地 main 有 5 个未推送的 schema 版本闸提交、不含 B444；缺陷在 B444 代码上。
2. 卡上直接走 inline（本会话 bind 坐 B446），未派发节点：任务指定本会话处理且需真机隔离实例。

## 事实与命令

- 隔离靶子 linux-01 /root/b444-iso（agentd 7792 未运行；生产 agentd pid 2874754 未动）。
- D5 卡行：`('D5','cli:opencode#ses_eac5ea14dffe7ZkCFEgNWUVgbV','bind','pull','已完成')`；seat_bearings 无 D5 行。
- agentd.log 10:10:53：`坐下被拒：席位非空` source=coordinate → `协调者回合已跑但席位未落`。
- 协调者 transcript：bash `m.sh card bind D5 --cli opencode --session ses_eac5ea14dffe7ZkCFEgNWUVgbV` → `{"ok":true}`（会话 id 从 opencode 日志里读出）。
- 红：`go test ./internal/keystone/ -run 'TestB446|TestB435Briefing'` → FAIL（launch 首轮简报缺 "本轮结束后由 agentd"）。
- 绿：改 `briefingFirstRound` 后同命令 → `ok github.com/Xsxdot/handoff/internal/keystone 0.472s`。
