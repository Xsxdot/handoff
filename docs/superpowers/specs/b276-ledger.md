# B276 spec 台账

- 2026-08-28 用户「继续做下一批」。沿用 2026-08-27 分批：第二批 = B245/B211/B256/B259/B258/B261 方向 1。
- 工作树 `/Users/sycm/.handoff/worktrees/batch-silent-wrong`，分支 `fix/silent-wrong` @ `8ddb060ae`（当前 origin/main）。
- 建卡 B276，源卡搁置。B261 `needs` 已清。
- B245 三处调用点核对：`cmd/dispatch.go:193-199`、`cmd/card_dispatch.go:207-221`、`internal/agentd/cardstep.go:145-166`。共同出口 `internal/discipline/dispatch.go:26` `ErrUnsupportedTarget`。
- B211：`internal/agentd/server.go:568` INFO；`webui.Embedded()` stub/embed 双文件；`cmd/status.go#renderStatusWithLookup` 无人读标记。
- B256：`cmd/service.go:342-369` `isEphemeralBin` 认 `go-build*` 分量 + `/tmp`。linux-01 全量在 `/tmp/hbfin`，`filepath.Abs("service.go")` 被当临时文件跳过。卡上「macOS 路径导致 Linux 不识别」不成立——本 spec 按此纠正。
- B259：`cmd/graph.go:19` Short 前缀 `[deprecated：请改用 codegraph 二进制]`。
- B258：`internal/agentd/server.go:993-1013` 三种成因同一 404「工单不存在」。skill 三处「404 即跳过」：`skills/handoff/SKILL.md` 178 / 254 / 596。
- B261：`internal/ledgerstep/dispatch.go:165` 瞬间冻结；`SetAcceptance` 只写「更新验收判据」。在飞判定用 `LatestTaskStates.LastType`，空=未知=在飞。
- 图：未跑 codegraph context（见 spec 备注）。
- 2026-08-28 独立审查 `docs/superpowers/reviews/b276-spec-review.md`：Critical 2 / Important 5 / Minor 2。
- 用户裁决 B259 改为删除 `handoff graph`，不是改 Short。spec r1 整段重写 B259：删 `cmd/graph.go`、permgate 白名单、平台不变量例外；替代入口锁定 `go run github.com/Xsxdot/charter/graph/cmd/codegraph`（charter 仓 `graph/cmd/codegraph/main.go` 存在；go.mod 钉 v0.9.0）。
- 吸收 Important：B211 JSON 名 `web_embedded` + false 必须出现在线上、填充 `handleStatus`；B256 候选先 `!isEphemeralBin` 否则 Fatal；B258 skill 锁 `--target`；B261 查询收口 `SetAcceptance`、接缝真打 CLI、一归档一在飞仍警告。CLI `cmd/card.go:179-182` 确认真写 `SetAcceptance`、不经 HTTP。
- 协调者意见：审查 Issue 1/2 必须改（否则按旧正文会做错题，删了又不给替代会比 deprecated 文案更坏）。Issue 3–7 写进 spec。Issue 8 活模板改入口纳入 B259 方案，不留 OOS。Issue 9 给实现注记，不单独立项。L2 保持。
- 用户 2026-08-28 授权吸收审查后无人值守推进到合 main 并 push。spec 头部回写已批准 r1。
