# B329 台账

- 2026-09-04：B156.3 真机验收发现本场 grok 无 `HANDOFF_SESSION_*`，`card bind B321` 报未出示席位。建卡 B329。
- 2026-09-04：用户纠偏：cli+session id 的本意是关掉主会话后仍可手动出示、恢复席位，不是强制从环境变量读。B307 弃选的 `--to` 与「人出示自己的一对」不是同一件事。
- 2026-09-04：开卡移 spec。本场 env 核实：`GROK_SESSION_ID=01a06a5e-9608-7223-a8e3-0b4ca23c87e6`，`GROK_AGENT=1`，无 `HANDOFF_SESSION_*`。`handoff card bind --help` 无身份 flag。
- 2026-09-04：拍板出示来源：先认宿主当前会话，没有才手填 `--cli/--session`。出示函数仍唯一。
- 2026-09-04：拍板覆盖范围：不带参数能坐下的只有 grok，以及带着会话 id 的 claude。Pi / Codex Desktop / Kimi 手填。不猜没核过的宿主键。
- 2026-09-04：用户确认整段产品叙述可写进 spec。派生默认一并写入：手填挂在出示入口上、工作台不填一对、不改 grok 注入、不做本机身份文件、冲突 fail-closed、coordinate/rebind --launch 不得用这一对指定机器人。
- 2026-09-04：图 `codegraph sym currentSeatIdentity` → `n_cmd_currentSeatIdentity`，baseline 域 `d_coordination_cli`，`best.json` 容器 `k_cmd_fn` 归 `d_cli`。调用方四处均在 `cmd/`。定级 L2。
- 2026-09-04：现状测试 `cmd/card_driver_test.go#TestCurrentSeatIdentityRequiresInjectedPair` 锁的是「缺 HANDOFF_SESSION_ID 不得回退 USER」；本卡扩展来源后该负例仍须绿。
- 2026-09-04：独立审查 `docs/superpowers/reviews/b329-spec-review.md`。总判吸收后再批。维持 L2。C1 + I1–I5 写入 r1：完整注入对忽略宿主键；残缺注入整次失败；双宿主 id 失败；四入口真 flag；空座/user/裸 dispatch 带 flag 失败；出示测试必须清未声明宿主键。驳回「环境源互斥」：会打穿从 grok 拉起的机器人出示。
- 2026-09-04：bug-batch 吸收 r1 后即批准。
