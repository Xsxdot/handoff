# B229 拆解稿落地台账

- 2026-08-25：开工核对 `git status --short --branch && git log --oneline -4`；原始输出：`## cards/B229-charter-2`（工作树干净）、HEAD 链为 `f848f0e6 chore(ledger): record b229 contract freeze` ← `97dcaf96 contract(b229): freeze discipline layers, store and dispatch-time delivery` ← `10906bb1 docs(spec): B229 …（已批准，L3 重档）`；判断：spec 已批准、契约已随 97dcaf96 冻结，本节点在 `cards/B229-charter-2` 上出稿。
- 2026-08-25：读 spec 头部第 3 行原文「> 状态：**已批准（用户，2026-08-24）** · 级别 **L3** · 档位 **重档**」；spec 状态位在文件头部，无需补写。
- 2026-08-25：契约头部核对——b229-contract.md 头部只有「**冻结物**：本文档 + …」，无字面「已冻结」状态行；冻结事实由提交 97dcaf96 提交说明承载。判断：状态位失真（流程状态托付给了 git log），本轮在契约头部补一行状态元数据并记入修订记录，不动任何冻结正文。
- 2026-08-25：`git show 97dcaf96 --stat` 原始输出确认冻结物含 Ticket 0 骨架：internal/discipline/dispatch.go(+98)、internal/ledger/disciplines.go(+108)、client/server/manager/events/proto/TS fixtures 等 24 文件 1163 行。
- 2026-08-25：实读骨架 internal/discipline/dispatch.go 全文：ResolveDispatch 四参签名、ErrUnsupportedTarget、DisciplineRef/ResolvedDiscipline 与契约 §2.2 逐字一致。
- 2026-08-25：实读骨架 internal/ledger/disciplines.go 全文：PutDiscipline/GetDiscipline/ListDisciplineNames、64KiB 上限、名字校验含 filepath.Separator 与 '/' 双查，与契约 §2.1 一致。
- 2026-08-25：`go build ./...` 原始输出仅 `BUILD_OK`（退出 0）；`go test ./internal/discipline ./internal/ledger ./internal/ledgerstep ./internal/proto ./internal/client` 五包全 ok（discipline 0.007s / ledger 12.869s… 原始行见本台账上方命令输出）。
- 2026-08-25：`go test ./internal/discipline -run TestResolveDispatch -v` 原始输出：TestResolveDispatchTriState 三子态（nil/false/true）、Assembly、Refusal 全 PASS；Refusal 内含 charter-must-override 未知名拒发断言（dispatch_test.go:111）。
- 2026-08-25：grep 核对欠账现状：manager.go resolveDisciplineFor 仍在 :359，三调用点 :760/:1271/:3380；cmd 无 discipline 命令族（grep 零命中）；ledgerapi.go:658 下拉仍 discipline.List(discipline.Dir(...))；Ledger.Enabled 三消费点 cmd/agentd.go:248、cmd/ledgercli.go:30、cmd/status.go:86 俱在；internal/agentd 全包 grep DisciplinesSupported 零命中（上报点未接线）；builtin/*.md 六份俱在。
- 2026-08-25：codegraph/target.json contracts 实读：d_cli→d_policy legacyBudget 32 entries 含「discipline（包级函数）」、d_gateway→d_policy legacyBudget 38 同 entry；与契约 §6 一致。
- 2026-08-25：新发现（契约 §2.7 退役清单未覆盖）：internal/agentd/discipline.go 另有 GET/PUT /api/discipline/file 端点（:110 handleDisciplineFileRead、:141 handleDisciplineFileWrite），且 files.go 存在 discipline.Write——目录退役后 PUT 仍可写死目录=编辑「成功」但永不生效的静默失败通道 + 漂移载体复活口。web/src/api/client.ts:337-355 有 fetchDisciplineFile/saveDisciplineFile 调用面。此缺口列入待拍板 P4 并回写契约修订记录。
- 2026-08-25：config 加载入口定位 internal/config/config.go#Load(:349)、decodeStrict(:550)——T5 的退休 Warn 落点。
- 2026-08-25：旧事件反序列化判据（契约 §5 快照回归「老事件无该键得 0 不报错」）grep internal/ledger/*_test.go 零命中——尚未有测试锁，列入 T3 验收。
- 2026-08-25：`command -v codegraph` 无输出、`handoff graph --help` 有输出（resolve 子命令存在）——符号锚自检用 handoff graph resolve 执行（平台不变量允许只读 graph 子命令）。
- 2026-08-25：`handoff graph resolve --doc docs/superpowers/specs/b229-breakdown.md` 原始输出：7 锚（resolveDisciplineFor ok / Dispatcher.ViaTemplate moved / Server.stepTransport ok / newTargetClientNamed ok / openLedger ok / config#Load ok / config#LedgerConfig ok），无 bad，EXIT=0。
- 2026-08-25：契约头部补状态行「> 状态：**已冻结（2026-08-25，提交 97dcaf96）**…」并追加 §8 拆解期修订记录四条；diff 范围核对仅头部一行与文末追加，冻结正文 §1–§7 零改动。
- 2026-08-25：拆解稿落盘 docs/superpowers/specs/b229-breakdown.md（法定路径）；产出物为提案非裁决，P1–P5 待拍板集中在 §0。
- 2026-08-25：收尾自查——① 未把没跑到的命令写成结论（web tsc/vitest 明记未验证；graph check 双读数留给 T7 实跑）；② 台账边干边追加属实；③ 本轮零次 handoff 写操作、零新 executor 进程（只用了只读 handoff graph resolve 与 go build/go test/grep/git show）。
