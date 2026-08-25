# 台账：B156.2.7（C7 d_cli·房间命令族）plan 节点

> 本文件随 plan 节点边干边追加；与产出物 docs/superpowers/plans/b156.2.7-plan.md 同批提交。
> 分支：cards/B156.2.7-charter；HEAD=06647d44（C5 已合入功能线）。

## 2026-08-26 现状基线复核

- L1 `git status`：working tree clean；分支 cards/B156.2.7-charter；HEAD 06647d44（C5 merge）。
- L2 `go build ./...` → EXIT=0。
- L3 `go vet ./cmd/...` → EXIT=0。
- L4 `gofmt -l internal cmd` → 零输出（基线干净）。
- L5 `go run . graph check --repo . --view cards-B156.2-charter-4` → fails=[] EXIT=0。
- L6 `go run . graph validate --repo .` → EXIT=0。
- L7 `go run . graph who-calls n_collab_Service_Pointer --view cards-B156.2-charter-4` → 唯一上游 = `n_agentd_roomNarrator_Say`（internal/agentd/server.go:2341，d_keystone）。与「第一个上游消费方已定形」事实一致。

## 图数据事实（亲读 baseline/best/target）

- L8 baseline.json 节点 `n_collab_New` 不存在；`k_collab_fn` 容器为空（0 节点）。coordinator 声明的「collab.New 无节点」实测成立。
- L9 `n_ledger_api_Facade_New` 也不存在；Facade 的 LedgerClient 侧方法（GetCard/ListActiveCards/ListAllCards/RecordRoomMessage/RecordMessageConsumed/EventsFromAsc/BindDriver/DriverLease）全部无节点——只有 schedclient 侧（Get/Put/List/Delete/MarkNeedsHuman/EffectiveBaseBranch）有节点。这是 B156.3 门面归一留下的既存图债。
- L10 `n_ledger_Store_RebindDriver` 存在（C1 已建）。`n_ledger_Store_ClaimCardAs` 存在。
- L11 `k_client_Client` 现有 63 节点；命名 `n_client_Client_<Method>`，order=源文件行号。
- L12 cmd 节点容器：RunE 闭包在 `k_cmd_fn`，部分包级函数在 `c_cli`（历史混杂），两者都归 d_cli（best.json）。
- L13 容器 label（check 的 entries 匹配键）：k_collab_Service=「collab 入站门面」、k_collab_fn=「collab 包级函数」、k_ledger_Store=「ledger.Store」、k_client_Client=「client.Client」、k_ledger_api_Facade=「账本薄门面 Facade」。

## check 语义（亲读 charter/graph@v0.8.0 codegraph/check.go）

- L14 check.go:99-105：跨域边若 callee 容器 Label 在契约 entries 内 → 免费；否则计 LegacyHits，超 budget 即 fail。
- L15 check.go:176-181：声明方向无活跃边 → KindDeadContract fail（所以 d_cli→d_collab 声明必须同批带至少一条真实边）。
- L16 check.go:90-91：组装点文件里的边豁免 new-direction 但仍计活边。
- L17 子域归根：best.go SubsystemOf 沿 Parent 链上溯到顶层（d_transport_channel→d_transport）。所以 n_client_Client_Inbox 边归 d_cli→d_transport 契约（entries 含 "client.Client" label，免费）。
- L18 冻结条目文本「collab.Service 与入站门面实体」**不是任何容器的 label**——同批 d_keystone→d_collab 用的是实际 label「collab 入站门面」。判定：d_cli→d_collab 的 entries 必须写实际 label（["collab 入站门面","collab 包级函数"]），否则 check 报 over-budget（budget 0 下任意未入 entries 边即红）。这是机械对账，非语义变更。

## 命令族落点与先例对账

- L19 `roomCmd` 根挂 rootCmd（console/diff 同形，root.go AddCommand）；room 四子命令挂 roomCmd。
- L20 `room list/read/send` 直调 collab.Service（openRoomService 组装）；`room inbox` 走 client.Inbox（HTTP，岔口三）。`card rebind` 挂 cardCmd（card_driver.go 同族）。
- L21 card_dispatch.go 成功路径：RunE 内 ViaTemplate 成功后 roomPointer(...)（Service.Pointer，正文=卡号+模板名）。`runStepDispatch`（card_node.go）是 HTTP 受理、不持账本，指针不在本卡 CLI 进程落（agentd 侧 ledgerstep 是另一回事）。
- L22 actor 注入沿用 ledgercli.go:44-51 的 ledgerActor()（cli:<user>@<host>）。
- L23 RebindDriver 冲突错误文本「卡 %s 当前绑定 %q 非 %q」含「当前绑定」——CLI 测试 stderr CAS 语义断言锚点（binding.go:93）。

## 测试夹具

- L24 runLedgerCLI（ledgercli_test.go:24）进程内跑 rootCmd + --config 临时目录；card 命令首跑自动种 workflow/template。
- L25 setupDisciplineGateFixture（card_dispatch_test.go:119）种 discipline + 假目标机 status 位——dispatch 指针测试复用。
- L26 newCaptureTarget / newCardStepCLIEndpoint（httptest）——room inbox mock server 同形。
- L27 序列化边界：rebind 成功断言 EvDriverTakeover payload from/to（事件 wire 穿过真实 JSON 序列化）；room send 断言 payload 解回 RoomMessage 与 body/kind 一致。

## 决策记录

- D1 collab.New 图债处置选**方案①**（在本卡视图 diff 补节点）——本卡组装点真实调用它，且「包级构造器有户口」是本仓惯例（cursor.New 先例）；不选方案②债务，因为声明 d_cli→d_collab 需要至少一条活边且组装边是最自然的一条。ledgerapi.Facade.New 与 Facade 的 LedgerClient 侧方法记**图覆盖债**（选项②）——那是 B156.3 门面归一轮的既存面，非本卡造成的。
- D2 派发指针失败路径的测试注入用 **roomPointer 包级 seam**（swap 先例 dispatchTransport/startBackgroundCheck）——失败注入点在 CLI 自组装 Service 内部，spec 两条缝都构造不出「指针失败但派发不中断」的断言；merged 卡的自然 ErrReadOnly 曾考虑，但依赖「派发被允许发生在并入卡上」这一偶然行为，脆弱。seam 只包接缝#1（Service.Pointer）调用，不改变测试入口（card dispatch）。
- D3 指针正文=「已派发 <卡号> @ <模板名>」——裸派发的「节点名」取模板名（bare dispatch 唯一可得的派发形态标识）；--step 节点派发的指针在 agentd 侧 ledgerstep 落，不在本卡 CLI。
- D4 存在式断言（不计数）——keystone 叙事也在写同款指针行（roomNarrator.Say），计数式会偶发红。
- D5 契约 §8.0「新增账本能力调用一律经门面」对 rebind 的对账：该规则约束会话子系统出站需求面（collab→client.LedgerClient→Facade）；CLI 是既有 d_cli→d_ledger 直调契约面（entries=["ledger.Store"]，13+ cmd 文件先例，B229 既定形态），breakdown C7 ④入口指针明示 card_driver.go 同构——判定沿 breakdown 直调 `Store.RebindDriver`，留痕计划 §0。

## 未验证项

- 图闸「声明 d_cli→d_collab 后 check 绿」未在基线实跑（会改 target.json，plan 轮不动生产/图数据）；预期基于 L14-L16 的 check 源码语义推导。
- d_cli→d_ledger 契约的 budget 4 当前 LegacyHits 未实测（本卡新增的 rebind 边目标 label=ledger.Store 在 entries 内、免费，无需预算）。

## 计划落盘后补充

- P1 产出物写至 `docs/superpowers/plans/b156.2.7-plan.md`（法定路径）。
- P2 `inList` 是精确等值（charter/graph@v0.8.0 check.go:229-236 亲读）——d_cli→d_collab entries 写冻结文本「collab.Service 与入站门面实体」必 over-budget（budget 0 下任意未入 entries 边即红）的结论坐实；对账决议（实际 label）记入计划 §0/§1/L18。
- P3 变异④修正：删「去掉 --expect 传递」——该变异对已绑定卡仍是冲突（expect=""=要求无绑定，绑定存在即拒），不会红；改为「吞错返回 ok」。
- P4 判据基线红绿表（计划 §1）：五+一条命令与 dispatch 指针全部基线红（真红/编译红）；gofmt/vet 基线绿（防实现轮写脏）；无「今天绿但需要正控」的行为判据——所有行为判据红→绿，变异 ①-④ 补正控证明。