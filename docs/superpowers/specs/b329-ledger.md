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
- 2026-09-04：计划基线验收 `go test ./cmd/ -count=1 -run 'TestCurrentSeatIdentityRequiresInjectedPair|TestCardBindUsesCurrentSeat|TestCardRebindSelfUsesLocalLedger|TestCardRebindSelfAndTakeoverEvent|TestCardRebindRequiresExplicitModeAndRejectsLegacyFlags|TestCardRebindHelpUsesExplicitModes|TestCardDispatchStep(UsesActorIdentity|SubmitsToLocalAgentd)|TestRoomSendLandsRoomMessageWithUserKind' -v`；原始结果末尾为 `PASS` / `ok github.com/Xsxdot/handoff/cmd 1.173s`。随后 `go build ./...` 成功（退出 0，无输出）。
- 2026-09-04：当前执行分支为 `cards/B329-charter`，基线 HEAD 为 `0cf036a240acc0a185b4b7145058714c92031fe4`；计划工作树当前只有本节点台账改动。
- 2026-09-04：codegraph 实跑 `context d_cli` 命中最佳领域 `d_cli`，结果 `outputNodes=46 outputEdges=30 truncated=false`；`sym currentSeatIdentity` 命中 `cmd/card_seat.go:18` 的 `func currentSeatIdentity() (string, error)`；`who-calls currentSeatIdentity` 仅返回 `cardBindCmd.RunE → currentSeatIdentity`，并警告 `基线仍有 5 个未扫描入口`。`sym n_cmdrunStepDispatch` 返回过期图签名，`sym n_cmd_roomSendCmd_RunE` 命中 `cmd/room.go:120`；`flow n_cmd_cardBindCmd_RunE` 真实返回 `degraded=true`、`steps=[]`。其余三入口按源码列为图覆盖债。
- 2026-09-04：基线 `go run . card bind --help`、`card dispatch --help`、`room send --help` 均未列 `--cli`/`--session`；`go run . card rebind --help` 仅列 `--self`/`--launch`；`go run . card coordinate B329 --cli grok --session s` 真实返回 `Error: unknown flag: --cli` 和退出码 1。
- 2026-09-04：依赖 API 现场核对：`go doc github.com/spf13/pflag.Flag.Changed` 返回 `Changed bool // If the user set the value (or if left to default)`；`go doc github.com/spf13/cobra.Command.Flags` 返回 `Flags returns the complete FlagSet that applies to this command (local and persistent declared here and by all parents)`。计划用命令本地 flag 注册，并用 `Changed` 锁住显式传入但值为空的禁用分支。
- 2026-09-04：计划 `docs/superpowers/plans/b329-plan.md` 已落盘；覆盖唯一出示函数、四个 CLI 接缝、B312/skill 回写、测试隔离、序列化边界、缺陷族对抗与用户故事 1–11。`git diff --check` 退出 0；占位符扫描仅命中 `go test ./...`/`go build ./...` 包模式和 `room send` 的 variadic `<text...>` 语法，未发现待填写占位符。
- 2026-09-04：提交计划与台账的命令为 `git add docs/superpowers/plans/b329-plan.md docs/superpowers/specs/b329-ledger.md`、`git commit -m "docs(B329): add implementation plan"`；原始输出为 `[cards/B329-charter 9309ed7e] docs(B329): add implementation plan`、`2 files changed, 700 insertions(+)`、`create mode 100644 docs/superpowers/plans/b329-plan.md`。
