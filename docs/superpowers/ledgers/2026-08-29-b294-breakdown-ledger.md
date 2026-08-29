# B294 breakdown 轮台账（2026-08-29）

卡：B294（远端预览：执行机发布会话，左栏第四种任务行点开隔离 Chromium）
本节点：`charter:breakdown`；分支：`cards/B294-breakdown`。
本档按纪律边干边追加；每行记录一个已确立事实、亲跑命令、放弃尝试或判断。

## 开工与上游状态

- [fact] `git status --short --branch && git log -1 --oneline`：分支 `cards/B294-breakdown`，HEAD `1e45ff75c9b969db6ee3467779ad6261f8b3a029 contract(B294): freeze remote preview session wire`，工作树干净。
- [fact] `docs/superpowers/specs/b294.md:3` 头部为「状态：已批准」（2026-08-29，用户原话「批准」）。
- [fact] `docs/superpowers/specs/b294-contract.md:3-8` 头部声明上游已批准、冻结物已冻结、交棒 breakdown；内容将作为本轮逐条核对基准。
- [fact] 用户注入的有效基线分支为 `cards/B294-charter-2`，提交 `1e45ff75c9b969db6ee3467779ad6261f8b3a029`；当前执行分支已在该提交之上工作。

## 图与架构读数

- [cmd] `GOMODCACHE=/root/.handoff/tmp/8836bab5/gomodcache go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --help`：命令可运行，列出 `domains`、`resolve`、`check` 等查询子命令。
- [fact] `jq -r '.domains | to_entries[] | select(.value.parent == null) | ...' codegraph/best.json`：顶层域为 `d_orchestration`、`d_gateway`、`d_workspace`、`d_execution`、`d_sessions`、`d_transport`、`d_protocol`、`d_ledger`、`d_cli`、`d_web`、`d_policy`、`d_maintenance`；类型分别由 best.json 标为 logic/boundary（详见拆解稿 §1）。
- [cmd] `GOMODCACHE=/root/.handoff/tmp/8836bab5/gomodcache go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . domains`：实际输出 `view: baseline` 的旧版 20 领域树，其中 preview 相关的当前顶层平铺域不以 best.json 的同一结构呈现；以纪律指定的 `codegraph/best.json` 为准，不把该旧视图当本卡域清单。
- [cmd] `GOMODCACHE=/root/.handoff/tmp/8836bab5/gomodcache go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . check --view cards-B294-charter-2`：失败原文 `Error: 视图 cards-B294-charter-2 引用不完整: [新增节点 n_web_api_preview_closePreview 引用不存在的容器 k_web_api_preview 新增节点 n_web_api_preview_createPreview 引用不存在的容器 k_web_api_preview 新增节点 n_web_api_preview_fetchPreviews 引用不存在的容器 k_web_api_preview 新增节点 n_web_api_preview_openPreview 引用不存在的容器 k_web_api_preview]`，退出码 1。
- [judgment] `internal/agentd` 为 61 文件、无子包的平铺包，命中项目清单 §5.1 的「单包源文件数 ≥40」盲区；本卡可按 preview 专用文件、既有 Server/Store 接线、镜像与 web tree 文件圈出有界集合，因此不插竖切还债卡。

## 当前实现骨架查证

- [fact] `internal/agentd/server.go#Server.Handler` 已注册五条 preview HTTP/WS route；`internal/agentd/preview.go` 五个 handler 仍统一返回 503 `预览会话尚未接线`，是 Ticket 0 空壳。
- [fact] `internal/client/preview.go` 已有 REST 与 `StreamPreviewEventsOnce` 签名；REST 走 `Client.do`，WS 走 `wsDialOptions`，不带任务 cursor、不负责重连。
- [fact] `cmd/preview.go` 已有 `open/list/close` cobra 壳；open 透传 `Port/Path/Via`，成功文本已冻结，Ticket 0 下仍由 agentd 503 失败。
- [fact] `internal/proto/preview.go`、`web/src/api/types.ts`、`web/src/api/preview.ts`、`web/src/api/ws.ts` 与 Go/TS fixture 已存在；preview kind、owner 持久化、镜像、SOCKS/PAC/Chromium、树计数/搜索/未归属/点击等行为尚未接线。
- [fact] `internal/store.Open` 是现有 `handoff.db` 建表入口，`internal/store.Store` 是本机 SQLite 持有者；best.json 将其归入 `d_orchestration`，故 owner persistence 若复用它会新增实际触及域。
- [fact] `web/src/app/shell/Shell.tsx` 当前只装配 `useProjectTree`/`useTasks`，向 `ProjectTree` 传入任务、树与工作台回调；`ProjectTree.tsx#TaskRow` 当前 kind 闭集为 `tui|terminal|file`，任务行点击进入 `onOpenTask`，拖放写入既有任务 MIME。

## 契约边界澄清回写

- [judgment] 为诚实反映 best.json 的持久化归属，已在 `docs/superpowers/specs/b294-contract.md` 新增 §1.3：owner persistence 复用 `internal/store.Store` 时实际增加 `d_orchestration`，但不新增 wire 接缝。
- [pending] §1.3 同时记录 `is-open` 的分歧：若只是本页收到 `OpenPreview` 成功确认，可用 web 本地投影；若要求刷新/浏览器自行退出后的实时附着事实，冻结面缺查询/事件字段，必须退回 contract。

## 判断与放弃

- [judgment] B294 明确为 L3 轻档，单轮 implement；本稿的 U0–U5 是同一轮实现的序贯有序单元，不是并行派发卡，不调用 handoff、不起子任务。
- [judgment] owner persistence 复用 `internal/store.Store` 是当前实现提案；这会触及 `d_orchestration`，仅作本地 owner 数据存取，不改跨进程 DTO，也不形成 coordinator 中央 truth。
- [pending] 持久化表/Store 方法的命名与 TTL/idle 回收函数的具体组织仍列为待拍板实现分歧；未把任一命名写成冻结签名。
- [pending] `previewMirror` 是否独立于既有任务 `Mirror` 实现，及浏览器启动器按 OS 拆文件还是集中适配器，均列待拍板；语义只按冻结契约验收。
- [fact] `internal/targetclient.Pool` 当前对外 `For` 只返回 `*client.Client`；relay raw `DialContext` 存在于池内 dialer/relay，但未发现可供 agentd SOCKS 使用的 pool-scoped raw dial API。
- [judgment] 由此发现 P2 契约门：不能把 HTTP client transport 推测成任意 TCP upstream；本稿列“暴露受池生命周期管理的 raw DialContext”与其它方案，未新增冻结签名。
- [pending] P0 页面 `is-open` 若要求刷新/Chromium 自行退出后的权威附着事实，当前 `OpenPreviewResp.Opened` 与事件面不够；已回写 contract §1.3 并列为待拍板，不在拆解中偷加字段。
- [fact] 复核发现 contract §1 的历史开工记录仍写 `cards/B294-charter-2@5e8826f7`；已在 contract §1.3 追加任务指定有效基线 `cards/B294-charter-2@1e45ff75c9b969db6ee3467779ad6261f8b3a029` 的更正，不改变冻结 wire。

## 亲跑验证与产出

- [fact] 已用 apply_patch 写入本轮法定产出 `docs/superpowers/specs/b294-breakdown.md`，包含 §0 待拍板、域类型/资格、50 条契约逐条核对、五格闭环、U0–U5 四段式子卡、逐族缺陷审查、真机清单和图债。
- [cmd] `git diff --check`：退出码 0，无输出。
- [cmd] `GOMODCACHE=/root/.handoff/tmp/8836bab5/gomodcache go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b294-breakdown.md`：退出码 0；解析到 `internal/agentd/server.go#Server.Handler`，line 489，anchor `moved`，node `n_agentd_Server_Handler`。
- [fact] 最终检查前 `git status --short --branch`：`## cards/B294-breakdown`，修改 `docs/superpowers/specs/b294-contract.md`，新增台账与 breakdown；`git diff --stat` 仅显示已跟踪 contract 的 16 行，两个新文件尚未纳入 diff stat。
- [fact] `git add` 后 `git diff --cached --check`：退出码 0，无输出；staged stat 为 breakdown 349 行、台账 52 行、contract 16 行（追加基线更正后待重新计数）。
- [cmd] contract 基线更正与本条台账追加后重新执行：`git diff --cached --check` 退出码 0、无输出；`GOMODCACHE=/root/.handoff/tmp/8836bab5/gomodcache go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b294-breakdown.md` 退出码 0，仍解析 `internal/agentd/server.go#Server.Handler` 到 line 489、`n_agentd_Server_Handler`。
- [fact] 最终 staged stat 为 breakdown 349 行、台账 54 行、contract 17 行，共 420 行新增/修改；当前 staged 状态为 breakdown/台账新增、contract 修改。
- [cmd] `git commit -m "breakdown(B294): propose preview session implementation split"`：退出码 0；原始输出 `[cards/B294-breakdown 3b7c1728] breakdown(B294): propose preview session implementation split`，提交 3 个文件、422 行，新增 breakdown 与台账并修改 contract。
- [fact] `git commit --amend --no-edit`：退出码 0；原始输出 `[cards/B294-breakdown 930de221] breakdown(B294): propose preview session implementation split`，提交 3 个文件、423 行；本条台账已随 amend 纳入。后续最终 hash 以收尾 JSON 为准。
