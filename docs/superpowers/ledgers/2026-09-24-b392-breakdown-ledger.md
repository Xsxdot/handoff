# B392 breakdown 节点台账（2026-09-24）

卡：B392「mobile bind：生产接线 SessionCookie/SwitchMachine（去掉 notWiredSessions）」。
节点：breakdown（charter，L3 轻档复核）。
分支：`cards/B392-charter-3`（不切换、不改 git 配置）。
产出：`docs/superpowers/specs/b392-breakdown.md`（本批提交）。
角色：handoff 派发形态（executor 出稿，本地协调者拍板）。不派发、不调用 handoff CLI、不起新 executor。

## 事实流水（命令与原始输出）

1. **基线/HEAD**：`git branch --show-current` → `cards/B392-charter-3`；`git rev-parse HEAD` → `6efc39541294bc989863515231faead944f2181d`；`git status` → `nothing to commit, working tree clean`。
2. **HEAD 父提交**：`git log -1 --format='%H %P' HEAD` → `6efc3954… 400ee3c4927650b902c50a054ee9943085388e5e`（contract 冻结提交的父 = spec 冻结提交，与 contract 头部基线链一致）。
3. **有效基线分支**：`git rev-parse origin/cards/B392-spec` → `400ee3c4927650b902c50a054ee9943085388e5e`；`git merge-base --is-ancestor 400ee3c4 HEAD` → YES（spec 在本分支祖先链内）。`origin/cards/B392-charter` → `d2cd3246…`（contract 头部所指第 2 轮续接点）。
4. **上游状态位实读**：`docs/superpowers/specs/b392.md:3` → 「状态：已批准（用户 2026-09-24 批准；D1 锚点与 S1–S3 建议已吸收）」；`b392-contract.md:5` → 「冻结状态：本提交随 Ticket 0 骨架、能变红的适配器契约测试与本台账冻结」。
5. **生产装配在位（Ticket 0）**：`mobile/bind/bind.go:40` `liveCore = mobilecore.New(nil, log)`、`:42` `core coreAPI = liveCore`；`mobile/bind/session.go:20` `sessions sessionAPI = newCoreSessions(liveCore)`；`mobile/bind/adapter.go` `sessionCoreAPI` + `coreSessions` + `newCoreSessions` + `SessionCookie`/`SwitchMachine`。
6. **占位零装配**：`grep -rn 'notWiredSessions\|errSessionsNotWired' --include=*.go .` → 仅 `./mobile/bind/session.go:5` 一行**注释**（无装配、无类型定义）。→ contract §5.1-2「源码零命中」措辞与现状不符（见 P4）。
7. **契约测试在位**：`mobile/bind/adapter_test.go` 5 个测试函数（`TestCoreSessionsSwitchMachineSequence`、`TestCoreSessionsSwitchMachineFailsClosed`（3 子例）、`TestCoreSessionsSessionCookieRejectsWrongMachine`、`TestCoreSessionsLockSerializesSwitchAndRead`、`TestDefaultRuntimeSharesOneCore`）。**无** `notWiredSessions` 源码零命中断言。
8. **既有测试过期语义**：`mobile/bind/bind_test.go:90 TestBindSessionFailIsClosed` 直接调默认 `sessions`（非 swap），注释写「S2 未接线时」，实际靠包级 `liveCore` 无任何机器才通过——与 spec §8.3 真实 Core 生产守卫存在共享全局态次序耦合（P3）。
9. **适配器语义核对**：`adapter.go:42-51` `SessionCookie` 锁内先 `ActiveMachine()==machine`、再 `Session()`、再判 `Value==""`；`adapter.go:62-71` `SwitchMachine` `Activate(context.Background())` → 判空 → `Origin`，任一步失败 `("",err)`、失败后不调 `Origin`。与 contract §4.2 逐字一致。
10. **模块工具链现状**：`grep gobind` + `go test ./bind/ -run TestGomobileSurfaceHasNoSkips -v` → `未找到 gobind；… go install golang.org/x/mobile/cmd/gobind@v0.0.0-20260908204917-8b95e45f8d3e` → `--- SKIP` `ok`（**SKIP ≠ 绿**）。
11. **图门禁**：`codegraph --repo . check` → exit 0，`fails=[]`；`warns` 41 条（`best-dangling` 含 `k_mobilecore_Core`/`k_mobilecore_fn`/`k_mobilecore_model`/`k_proto_PairBundle`/`k_proto_PairMachine`，及 `budget-raised`/`legacy`/`oversized-package`/`prefix-family`）——**基线既存**（本卡不改 best.json）。
12. **图完整性**：`codegraph --repo . validate` → exit 1，`issues` = `[cards-B272-charter] 新增容器 k_dropdir_fn 引用不存在的基线领域 d_coordination_api`、`[cards-B374-charter] 新增容器 k_collab_model 已存在于基线，containersAdded 只接受新容器`——与 contract §7.4 一致，非本卡引入。
13. **图覆盖债（sym 亲跑）**：`codegraph --repo . sym 'Core.Activate'`/`'Core.Session'`/`'Core.ActiveMachine'`/`'Core.Pair'`/`'coreSessions'`/`'newCoreSessions'` → 均 `Error: 符号 … 不在图中（图未覆盖或名字有误）；近似候选: []`——与 contract §3/§7.4 一致。
14. **域归属**：`codegraph/best.json` 顶层域 15 个（`parent` 缺省）；`scripts/codegraph-rescan/main.go:2140` `internal/mobilecore → d_transport_channel`，`:2105` `internal/agentd → d_gateway`，`:2139` `internal/client → d_transport_channel`。`mobile/` 被扫描配方排除（图外嵌套 module）。
15. **编译/测试（本轮新鲜，`mobile/`）**：`go version` → `go1.26.1 linux/amd64`；`go build ./...` → OK；`go test ./... -count=1` → `ok mobile 0.383s` + `ok mobile/bind 0.005s`；`go test ./bind/ -race -count=1` → `ok mobile/bind 1.017s`；`go vet ./...` → 无输出；`gofmt -l .` → 无输出。
16. **根模块（本轮新鲜）**：`go build ./...` → OK；`go list ./... | grep -c handoff/mobile` → `0`；`go list -deps ./... | grep -c handoff/mobile` → `0`。
17. **`codegraph resolve --doc`（收口前亲跑）**：`b392-contract.md` → exit 0，两锚 `n_client_Client_IssueAuthTicket`、`n_agentd_sessionCookie` 均 `ok`；`b392-breakdown.md` 初跑报 `export_surface_test.go#wantBindSurface (file_missing)`（图外文件误用 `#Symbol` 锚），改为普通路径后重跑 → exit 0，两锚均 `ok`（坏锚已修）。
18. **竖切夹具可行性**：`internal/mobilecore/core.go:107` `func New(dial DialFunc, log *slog.Logger) *Core`（`:35` 导出 `DialFunc`）、`internal/client/client.go:195` `func New(addr, token string) *Client`、`internal/proto/pairing.go:111` `func EncodePairBundle(...)`——故 `mobile/bind` 的 `_test.go` 可自建真实 Core + httptest agentd 夹具，无需新接缝。
19. **import 禁令只扫生产文件**：`mobile/bind/export_surface_test.go:84-89` AST 过滤 `!strings.HasSuffix(fi.Name(), "_test.go")`；故测试文件可 import `internal/client`/`internal/proto`（边界澄清 §2.4-1）。

## 未验证 / 欠跑（不得写成结论）

- **变异红**（contract §7.3 三条 double 层）：contract 自述，本节点**未复跑**（避免扰动工作树）。
- **gobind 真产物**：本工作树无 gobind，`TestGomobileSurfaceHasNoSkips` SKIP。
- **真实 Core 竖切 + 生产守卫**：spec §8.2/§8.3 欠账，归 I1（机内可跑，本节点未做）。
- **根模块全量测试**：本节点只跑根 build/list，未跑根全量测试。
- **真机**：AAR/XCFramework、平台 cookie jar 行为，归协调者（§6）。

## 提交事实（历史读数）

- 本批：`git add docs/superpowers/specs/b392-breakdown.md docs/superpowers/ledgers/2026-09-24-b392-breakdown-ledger.md && git commit -m "breakdown(B392): …"`。
  提交当时的原始输出：`[cards/B392-charter-3 306b3714] breakdown(B392): mobile bind 生产接线轻档复核提案——0 子卡单实现轮 + P1-P5 待拍板` / `2 files changed, 281 insertions(+)`。随后 `git status --short` → 空。
  本条为 amend 收进同批（amend 换 hash 是 git 事实，收口判据是工作树干净，不是文件里的 hash 等于 HEAD）。

## 2026-09-24 协调者拍板与契约修订

- P1 甲：0 子卡，单实现轮；P2 甲：保留 `context.Background()`；P3 甲：默认生产无 swap 真实 Core 竖切（隔离子进程）+ 独立 Core swap 补充 + 默认 `liveCore` 身份守卫，旧空态测试改显式替身；P4 甲：回写 contract；P5 甲：`-race` + `TryLock` 双闸。
- P4 修订：`b392-contract.md` 冻结项 2 改为“生产默认装配不包含占位”，不再声称源码文本零命中；补充 `_test.go` 可为夹具 import `internal/client` / `internal/proto`、生产文件仍禁止的边界澄清，并记录于 contract §11。
- breakdown 状态改为“已拍板（2026-09-24）”，P1–P5 裁决与理由写入 breakdown §8；canonical contract 修订与本拆解裁决同批提交后才进入 plan。
- 2026-09-24 plan review 对齐：P3 的默认生产无 swap 子进程竖切为 §8.3 必做承重项；独立 Core + swap 仅补充行为覆盖；TryLock 是去互斥变异唯一确定性红证据，真实 gate/-race 为补充。
