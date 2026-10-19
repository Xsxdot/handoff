# B446 排查与修法：协调者 launch 首轮自坐后收尾撞席

> 流：charter bug 流（implement 排查节 → review → finish）。级别：L1 判断待定——简报修为纯文本；
> 收尾修若改席位语义须人裁决（见「修法」第 2 条）。基线：origin/main 2e339348（B444 上线版）。

## 现象

`card coordinate`（含 B444 驻场者代叫）→ `launchCoordinatorRoundWithExpect`（internal/agentd/scheddrain.go）
跑完协调者首轮后 `BindSeatMode(coordinate, push)` 报「坐下被拒：席位非空…请使用 rebind」+ ERROR
「协调者回合已跑但席位未落」，调用方收 409、卡被标 needs_human；而席位其实已由同一会话坐上。

## 根因（证据：隔离靶子 linux-01 /root/b444-iso，卡 D5，协调者会话 ses_eac5ea14…）

1. 账本：D5 `driver_source=bind`、`driver_mode=pull`、无承载记录；`driver_seat_bound` seq114 10:10:46，
   收尾撞席 needs_human seq116 10:10:53。即席位不是 launch 落的，是协调者回合内 `card bind` 落的 pull 席位。
2. 协调者 transcript（opencode.db part 表）：首轮简报 `briefingFirstRound` 只说「席位尚未落定」，
   不说谁来落、何时落。模型反复推理「seat not settled… should I bind?」，参照前几张卡留下的
   「席位未落」needs_human 把它读成「该我去坐」，从 opencode 日志里翻出自己的会话 id，跑了
   `handoff card bind D5 --cli opencode --session ses_eac5ea14…` → `{"ok":true}`。
3. 收尾 `BindSeatMode` 只认空座 → CAS 冲突 → needs_human + 409。

结论：根因在首轮简报（说「未落定」却不说「agentd 会落、你别落」）。收尾侧「非空即失败」本身符合
设计；但同一会话自坐的结果是 **pull 席位、零承载**，与 launch 要落的 push 席位语义不同——
所以「同会话已坐 = 成功」不能原样放行（pull 席位不会被 push 唤醒，等于叫不醒）。

（D4 那次撞席是返工三之前驻场者自坐 D4，已被 B444 返工三的账本互斥与驻场简报修掉，不属本卡。）

## 修法

1. **简报（已做，纯文本，不改语义）**：`briefingFirstRound` 写明「席位在本轮结束后由 agentd 替你落定
   （机器人席位）」，并禁本轮 `handoff card bind` / `handoff card rebind`、禁去找自己的会话 id。
   测试 `TestB446LaunchBriefingForbidsSelfSeat`（经 `LaunchForCard` 生产入口）先红后绿。
2. **收尾兜底（人裁决 A，2026-10-19 已做）**：收尾遇席位 == 本回合会话身份（任意来源/模式）时，
   以 `RebindSeatMode(expect=该身份)` 原子改写为 coordinate/push + 承载，视为成功、不标 needs_human；
   席位是别的会话时维持现状（409 + needs_human）。

实现：`adoptSelfSeatedCoordinator`（internal/agentd/scheddrain.go），只在收尾坐下/换绑报 `ErrCASConflict`
时进入；重读卡，`DriverSession == 本回合身份` 才以自身为 expect 换绑（落一条 `driver_takeover` X→X），
否则原错误返回。覆盖 `card coordinate` 与 `rebind --launch` 两条 launch 收尾；重建回合（Wake rebuilt）不在范围。
测试 `TestB446LaunchFinalizeAdoptsSelfSeat`（经 HTTP launch/rebind 端点）先红（409）后绿。
