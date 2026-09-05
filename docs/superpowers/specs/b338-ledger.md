# B338 台账

- 2026-09-05 真机：`card coordinate B337` 502 `远端载体 TUI 转发尚未接线：machine=mac-02`。Admit 选 leader/muse；workdir=`/Users/sycm/.handoff/repos/handoff`；名额归还。
- 根因：`openCoordinatorTUI` 只用 `IsLocalMachine`。hostname=`sycmdeMacBook-Air.local`，muse.machine=`mac-02`，listen=`100.73.238.21:7777` 与 targets.mac-02.addr 相同。
- 定级 L1。用户授权自主推进并批准。
- 修法：叠加已有 `Server.IsSelfTarget`。远端转发不在本卡。
- 基线红：`go test ./internal/agentd/ -run TestOpenCoordinatorTUITreatsSelfTargetAsLocal` → `远端载体 TUI 转发尚未接线：machine=mac-02`。
- 实现后：`go test ./internal/agentd/ -count=1 -run TestOpenCoordinatorTUI` ok 3.504s；`go build ./...` 绿。
- 变异：`selfTarget := s.IsSelfTarget(...)` 改成 `false`（可编译）后 `TestOpenCoordinatorTUITreatsSelfTargetAsLocal` 红回 502 文案；已还原。
