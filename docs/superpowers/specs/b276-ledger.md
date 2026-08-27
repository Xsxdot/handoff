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
