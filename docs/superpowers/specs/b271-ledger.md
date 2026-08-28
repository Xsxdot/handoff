# B271 spec 台账

- 2026-08-28 用户「那就继续下一批」。原分组第四批 = B271 单卡（loopback 镜像 EOF），第五批 B234/B193 不动。
- 工作树 `/Users/sycm/.handoff/worktrees/b271-loopback`，分支 `fix/b271-local-dispatch`（从 `main` @ `d319f92d2`）。
- 卡 B271 待办→spec。源 note：B268 本机 grok；空 target 被拒；登记 `local=http://127.0.0.1:7777`；`machine=local` EOF；卡流停在 dispatched；权限门与 EOF 同一毫秒；自动批是 `approver_decision`；`resume --force` 不发 completed。
- `handoff template show charter-default`：`target` 空串，`discipline=charter-must-override`，`purpose=charter`。
- 本机 `config.yaml` `targets.local.addr=http://127.0.0.1:7777`。linux-01 在飞 B281 implement（`af294af9`，`cards/B281-charter-2`），本卡不碰。
- 代码：`ViaTemplate` 空 target 拒发（`internal/ledgerstep/dispatch.go:137-139`）。`targetClient("")` 拒（`cmd/card_dispatch.go:156-158`）。`resolveStepDiscipline` 空则跳过探活（`cardstep.go:136-141`）。
- 任务镜像：`machine=` 日志、`mirrorDiscoveryTick=30s`、`onEvent` → `hub.Publish`（`internal/agentd/mirror.go`）。账本镜像：`target=` 日志、空 target 因 `!registered[link.Target]` 被跳过（`ledgermirror/mirror.go:236-238`）。
- WS 掐线：`writeLiveBatch` 对 `seq <= lastWrittenSeq` 且大于重放面断开（`server.go:2090-2098`）。自订二次 Publish 会打中这条。`ReadTimeout=30s` 注释写明 Hijack 后对 WS 不生效，排除为 EOF 主因。
- `waitForTurnEnd` 走 `cl.WaitEvent`（`runner.go:417-419`），不是卡流。
- `LinkTask` 允许空 target（`cmd/status_test.go` 已用）。`WorkBranch` 跨机是字符串不等。
- 图：`codegraph context d_ledger` 有 ledger/ledgermirror/ledgerstep。`sym ViaTemplate` 命中。`who-calls waitForTurnEnd` 回到 `awaitNode` ← `Run`。任务镜像不在 d_ledger 包列表。
- 无前端页面形态，不走原型。
- 定级 L2：空 Target 字段语义收口，不新开 HTTP，不改状态机。
- 弃选闭区间 from_seq、魔法别名「本机」、钩子兼写账本、为自订删乱序断开、force 冒充 completed。
- 2026-08-28 独立审查 `01a046d9` 写入 `docs/superpowers/reviews/b271-spec-review.md`。总判修订后再批。Critical 2（WorkBranch 空串短路 / B192；本机源订 Hub 污染 Watchers）+ Important 5。
- 协调者吸收 r1：废止 B192 §2.1.3，去掉空串短路，历史缺字段视作本机、LocalBaseBranch 作后门；本机源不订 Hub（门铃+store），Watchers 语义不动；接缝覆盖 stepTransport/awaitNode/diffNode；Addr 去 scheme；For 之前分流。不抬 L3。不选 Hub 内部订。
- 用户 2026-08-28「老样子」授权批准 r1 并无人值守推进到合 main。
