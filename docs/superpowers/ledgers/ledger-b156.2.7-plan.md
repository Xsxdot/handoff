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
## 2026-08-26 实现轮（cards/B156.2.7-charter-3，HEAD=5f4793ff）

- R1 基线复核：`go build ./...` EXIT=0；`go test ./cmd/ -run 'TestCardDispatchClaimAndSnapshot|TestCardDriverCommandsTakeoverAndRelease'` → ok 0.742s。
- R2 首红（T7.1）：`go test ./cmd/ -run 'TestRoom|TestCardDispatchWritesPointer|TestCardDispatchPointerFailure|TestCardRebind'` → build failed：`cmd/card_dispatch_test.go:993:20: undefined: swapRoomPointer`。编译红（新缝符号 roomPointer/swapRoomPointer 尚未落地，非拼写错）。落地空壳后须再见断言红。
- R3 断言红证据（纪律「空壳落地后须再见断言红」）：把 roomPointer 实现临时换成 no-op（可编译，`_ = proto.RoomMessage{}` 保住 import）→ `TestCardDispatchWritesPointerLine` **断言红**「账本里没有 kind=pointer ∧ by_system=true 的派发指针行」（card_dispatch_test.go:965）→ 还原后复绿。断言有牙。
- R4 计划测试修正①（rebind 成功用例）：计划 §T7.1 的 TestCardRebindSuccessAndTakeoverEvent 先 `ClaimCard(c.ID,"cli:old@h")` 后不带 `--expect` 直接 rebind——与 C1 已冻结的 `RebindDriver` 语义冲突（binding.go:72 `expect=""=要求当前无绑定`），首跑红 `卡 D1 当前绑定 "cli:old@h" 非 ""`。修正：加 `--expect cli:old@h`（CAS 前值=当前绑定），断言意图（session 覆写 + EvDriverTakeover from/to）不变。
- R5 计划测试修正② + harness 根因修复（resetAllFlags 切片 flag）：计划 §T7.1 的 TestRoomSendCarriesRefAndMention 断言 `len(msg.Refs)==2`，首跑红 `refs 载荷漂移: [[] [] ... docs/x.md B156]`。根因=harness resetAllFlags 对切片 flag 用 `Value.Set(DefValue)` 复位，pflag 的 Set 是 append 语义且空默认 DefValue="[]" → 每次 Execute 往共享的 roomSendRefs 追加垃圾元素（decision.go --option 是既有同型 flag，只因无长度断言才未被咬）。修复：resetAllFlags 对 `pflag.SliceValue` 且 DefValue∈{"","[]"} 走 `Replace(nil)` 分支（ledgercli_test.go）。
- R6 harness 根因修复②（runLedgerCLI 执行后复位）：room inbox 测试（--target mac-02）是 room_test.go 最后一个测试，脏 targetName 经 root/sessions 测试的 resetFlags save-restore 传播，导致 status 族 6 测试全红（「target mac-02 未在配置中定义」）。修复：runLedgerCLI Execute() 后再 resetAllFlags 一次并还原 configPath=cfgPath（workflow_test.go 执行后直接 openLedger() 依赖 configPath 保持）。全仓 `go test ./cmd/` 复绿（含全部既有 status/decision/workflow 测试）。
- R7 T7.3 收口读数：`go build ./...` EXIT=0；`go vet ./cmd/...` EXIT=0；`gofmt -l internal cmd` 零输出（room.go/room_test.go 曾被列出，gofmt -w 后干净）；`grep -rn "fmt.Print" cmd/room.go cmd/card_driver.go cmd/card_dispatch.go` 零命中（机器输出走 OutOrStdout 的 fmt.Fprintln，非日志）。触及测试 10 支全 PASS；`go test ./cmd/` 全量 + `internal/client`/`internal/ledger/...`/`internal/collab/...` 全绿。**时点限定（审阅退回补记）**：这后半句「全量全绿」在 **T7.3 收口时、图提交 ad02282f 之前**为真——全量里的 TestRepoContractGate 在裸门形态下对最终提交不成立（图提交把 d_cli→d_collab 声明带进来后裸门必红，见 R14）。
- R8 T7.4 图数据落盘：target.json contracts 尾部追加 d_cli→d_collab（entries=["collab 入站门面","collab 包级函数"]，逐字节仅追加不改动既有条目）；视图 diff nodesAdded +8、edgesAdded +12（既有 29 边 + 12 = 41）。
- R9 手写 JSON 双判据：`python3` 解析 edgesAdded len==41（声明 12 条全在）、nodesAdded keys==25（17 既有 + 8 新，无重复键被静默归零）；target.json `d_cli→d_collab` 计数==1。grep -c 每个新节点 id == 1 定义 + N 处边引用（无重复定义键）。
- R10 图闸读数：`go run . graph validate --repo .` EXIT=0；`go run . graph check --repo . --view cards-B156.2-charter-4` fails=[] warns=96（协调者基线 97，-1 由 k_collab_fn 从空容器补上 n_collab_New 消掉 best-dangling 所致——正向变化，已核对）。
- R11 逐容器点数（读源码侧）：k_collab_fn 视图 +1、`grep -c '^func New(' internal/collab/service.go`==1；k_cmd_fn 视图 +6、room RunE 4 + rebind 1 + openRoomService 1；k_client_Client 视图 +1、`func (c *Client) Inbox`==1；k_collab_Service 零新增。
- R12 变异复验（四发全红后还原，均在 cmd/card_dispatch.go / cmd/card_driver.go / codegraph/diffs 施加与执行同路径）：
  - 变异① 去掉指针调用（`if false` 保 import 可编译）→ TestCardDispatchWritesPointerLine 红「账本里没有 kind=pointer…」→ 还原复绿；
  - 变异② 指针失败改 `return perr` → TestCardDispatchPointerFailureDoesNotInterrupt 红「Pointer 失败不应打断派发主流程: 指针写失败」→ 还原复绿；
  - 变异③ 删 d_cli→d_collab 六条活边 → `graph check --view` EXIT=1、fails=1 dead-contract d_cli→d_collab（与协调者对照实验一致）→ 备份还原后 EXIT=0 fails=0；
  - 变异④ rebind 吞错返回 ok → TestCardRebindConflictNonZeroExitAndCAS 红「CAS 冲突必须失败（退出码非零）」→ 还原复绿。
  - 还原后全量复验：`go test ./cmd/ -run 'TestRoom|TestCardDispatchWritesPointer|TestCardDispatchPointerFailure|TestCardRebind'` ok；`graph check --view` EXIT=0 fails=0 warns=96。
- R13 协调者显式请求落地（与计划 §T7.2 内联 RunE 冲突，以协调者补充为准）：派发指针调用从 RunE 匿名闭包提取为具名函数 `writeDispatchPointer(st, id, nodeLabel)`（card_dispatch.go）——C6 指针引用白名单守卫按「所在函数」归属，落在闭包里归属不到真正调用点。`writeDispatchPointer` 内部仍是 `roomPointer(collab.New(ledgerapi.New(st)), ...)` 同一 seam；判据一/二测试不变。图边保持计划声明形状 `n_cmd_cardDispatchCmd_RunE → n_collab_Service_Pointer`（调用边门控只校验文件 import，不校验调用行位置）；`graph check --view` 复跑 EXIT=0 fails=0。

## 2026-08-26 审阅退回单点修复轮（cards/B156.2.7-charter-4，HEAD=34e87919）

**本轮只改了什么**：① `cmd/room.go` 抽 `roomServiceFor` 单绑定点（openRoomService 改调它、注释同步）；② `cmd/card_dispatch.go` writeDispatchPointer 改调 `roomServiceFor`、移除随之不再使用的 ledgerapi import；③ 本台账（R7 时点限定 + R14-R16）。**未动**：target.json、视图 diff、任何测试断言、`internal/collab`/`internal/ledger`/`internal/proto` 生产代码、cmd/ledgercli_test.go 夹具。

- R14 **交付态裸门 TestRepoContractGate 有且仅有两条 fail（absorb 前固有形态，非本卡引入）**。协调者实测：`5582a6ce` 上 `go test ./cmd/ -run TestRepoContractGate` → ok；`34e87919`（图提交 ad02282f 之后）→ FAIL 恰好两条。本节点在 34e87919 改动前复跑确认，原文：
  ```
  --- FAIL: TestRepoContractGate (0.03s)
      graph_gate_test.go:38: 契约违规 [dead-contract] 契约 d_cli→d_collab 声明的方向没有活跃 call、implements 或组装点豁免边（期望在该方向看到至少一条跨子系统边）
      graph_gate_test.go:38: 契约违规 [dead-entry] 契约 d_cli→d_collab 声明的入口 "collab 包级函数" 在 d_collab 中不存在（无同 Label 容器或其非 deleted 节点均不属 d_collab；期望在 d_collab 找到）
  ```
  **成因**：裸门是 `codegraph.Check(tg, best, codegraph.Merge(g, nil), decls)`（cmd/graph_gate_test.go:36）——`nil` 就是 diffs 位，它只读 `baseline.json`，不看任何视图 diff。dead-contract：d_cli→d_collab 的全部活边都声明在本卡视图 diff（cards-B156.2-charter-4）里，baseline 里没有 → 裸门无活边。dead-entry 的具体成因：**`k_collab_fn` 在已 absorb 的 baseline 里是空容器**，`n_collab_New` 只存在于本卡未 absorb 的视图 diff 里——`--view` 下有节点所以不报，裸门下没有所以 dead-entry。absorb 之后两条一起消。**处置**：不改 target.json、不改视图 diff、不改测试、不删声明。下游预期：review / 图对账 / finish 在裸门上见到这两条即本卡正常交付态，finish absorb 后自消。
- R15 **组装点收敛（架构法第八条，审阅 minor 修正）**。改动前 `grep -rn 'collab\.New(' cmd/ | grep -v '_test\.go' | grep -v ':[0-9]*:[[:space:]]*//'`（注释行过滤判据，git grep 34e87919）恰两行：`cmd/room.go:38`（openRoomService）与 `cmd/card_dispatch.go:330`（writeDispatchPointer）；room.go:32 那行注释被判据过滤。改动后同判据恰一行：`cmd/room.go:34`（roomServiceFor 体内）。writeDispatchPointer 已持有 `*ledger.Store`，不能改调 openRoomService()（那会再开一个账本句柄）——维持 `roomPointer(roomServiceFor(st), ...)`；ledgerapi import 随最后一次使用移除。**图边核验**：`go run . graph check --repo . --view cards-B156.2-charter-4` 改后 EXIT=0 fails=0，warns=96 按 kind 与协调者 34e87919 基线逐类一致（container-misplaced 51 / legacy 34 / prefix-family 5 / anchor-off-domain 2 / best-dangling 2 / oversized-package 2）——新增 roomServiceFor 是 cmd 包内部绑定间接层，视图 diff 里 `n_cmd_openRoomService → n_collab_New` 与 `n_cmd_cardDispatchCmd_RunE → n_collab_New` 两条边声明维持不变（调用边门控只校验文件 import，R13 writeDispatchPointer 先例同款），无需调整边声明。
- R16 **夹具改动越界记账（审阅 minor，本轮不动夹具）**。`cmd/ledgercli_test.go` 两处改动越过计划 §2 有界文件集：resetAllFlags 的 SliceValue.Replace 分支、runLedgerCLI 执行后复位（resetAllFlags + configPath 还原）。根因（R5/R6）：slice flag 的 Value.Set 是 append 语义、空默认 DefValue="[]"，每次 Execute 往共享切片追加垃圾元素（room send --ref/--mention 首跑撞上）；--target 脏值经既有 harness 的 save-restore 传播染 status 族。审阅认定属实现轮根因修复、仅测试夹具、已留根因，记账不阻塞。**断言削弱检查（判据=夹具改回原样、确认原来该红的仍然红）**：临时把两处夹具改回原样实测——`TestRoomSendCarriesRefAndMention` 红 `refs 载荷漂移: [[] [] [] [] docs/x.md B156]`（R5 原红复现）；room inbox 跑后 status 族 6 支全红 `target "mac-02" 未在配置 ... 中定义`（R6 原红复现）。夹具改动**不削弱任何既有断言**；还原后触及测试复绿。断言行一字未改，改动只消除 harness 状态污染，不影响被测的产物行为。

## 2026-08-26 复审退回视图 diff 修复轮（cards/B156.2.7-charter-5，HEAD=4e6c9a25）

**本轮只改了什么**：`codegraph/diffs/cards-B156.2-charter-4.json`（图数据，视图 diff）。**未动**：`cmd/` 下任何源码（修复本体 roomServiceFor 收敛判合格，见 R15）、target.json、测试、`internal/` 生产代码。复审裁决一条 major：视图 diff 里 `n_cmd_openRoomService → n_collab_New` 与 `n_cmd_cardDispatchCmd_RunE → n_collab_New` 两条边不再对应任何直接调用（收敛后全仓唯一调 `collab.New` 的是 roomServiceFor）——`graph check` 四入参全读 JSON、零源码解析，绿不代表边对。按**甲**案修复。

- R17 **本轮改动内容**：① nodesAdded 新增 `n_cmd_roomServiceFor`（k_cmd_fn，cmd/room.go:33）；② edgesAdded 删两条旧边（`n_cmd_openRoomService → n_collab_New`、`n_cmd_cardDispatchCmd_RunE → n_collab_New`），改加三条新边（`n_cmd_roomServiceFor → n_collab_New`、`n_cmd_openRoomService → n_cmd_roomServiceFor`、`n_cmd_cardDispatchCmd_RunE → n_cmd_roomServiceFor`）；③ 刷新 cmd/room.go 六个节点行号（roomServiceFor=33 新增、openRoomService 33→39、roomListCmd 47→53、roomReadCmd 76→82、roomSendCmd 114→120、roomInboxCmd 139→145，各 +6）。nodesAdded 26（原 25 + 1）、edgesAdded 42（原 41 − 2 + 3）。
- R18 **复审给的行号三个有误，下游不可引用**：复审裁决写 `openRoomService 39 / roomListCmd_RunE 53 / roomReadCmd_RunE 83 / roomSendCmd_RunE 121 / roomInboxCmd_RunE 146`，后三个各高一行。**正确锚点是 `RunE:` 那一行，不是函数体第一行**：roomReadCmd RunE=82、roomSendCmd RunE=120、roomInboxCmd RunE=145（函数体第一行是 83/121/146）。两条独立算法互相印证：(a) 直接读 `RunE:` 行 → 53/82/120/145；(b) 既有 diff 旧值（47/76/114/139）各 +6 → 53/82/120/145，一致。func 声明类两个（roomServiceFor=33、openRoomService=39）复审是对的。
- R19 **六个行号逐行 sed 自验读数（判据：不靠任何人给的数字）**——每条确认锚点：
  ```
  == line 33 ==  func roomServiceFor(st *ledger.Store) *collab.Service {          （func 类 ✓）
  == line 39 ==  func openRoomService() (*collab.Service, *ledger.Store, error) { （func 类 ✓）
  == line 53 ==  RunE: func(cmd *cobra.Command, _ []string) error {              （RunE 类，roomListCmd ✓）
  == line 82 ==  RunE: func(cmd *cobra.Command, args []string) error {           （RunE 类，roomReadCmd ✓）
  == line 120 == RunE: func(cmd *cobra.Command, args []string) error {           （RunE 类，roomSendCmd ✓）
  == line 145 == RunE: func(cmd *cobra.Command, _ []string) error {              （RunE 类，roomInboxCmd ✓）
  ```
  六条读数全部对上，无一迁就。
- R20 **手写 JSON 双判据**：`python3` `object_pairs_hook` 查重复键零命中（顶层）；解析后 `nodesAdded` len==26（声明 +1 达成）、`edgesAdded` len==42（声明 −2+3 达成）；`n_cmd_roomServiceFor` 定义键恰 1 处、在 edges 中引用恰 3 条；两条旧边 `n_cmd_openRoomService → n_collab_New` / `n_cmd_cardDispatchCmd_RunE → n_collab_New` 均确认不存在，指向 `n_collab_New` 的边恰剩 1 条（`n_cmd_roomServiceFor → n_collab_New`，与「全仓唯一调 collab.New 的是 roomServiceFor」一致）。
- R21 **图闸读数**：`go run . graph validate --repo .` EXIT=0（containers 263 / nodes 3718 / edges 4746 / 零 issue）；`go run . graph check --repo . --view cards-B156.2-charter-4` EXIT=0 fails=[] warns=96，按 kind 与基线逐类一致：`container-misplaced 51 / legacy 34 / prefix-family 5 / best-dangling 2 / anchor-off-domain 2 / oversized-package 2`——六类无一变化（本轮只改 cmd 内部绑定间接层与行号，不新增跨域边，不改变任何容器/领域归属）。
- R22 **`go test ./cmd/` 唯一红仍应是 TestRepoContractGate 两条（dead-contract + dead-entry），不多不少**。实测：`grep -c "^--- FAIL"` == 1（唯一 TestRepoContractGate）；其内 fail 恰两条原文 `契约违规 [dead-contract]` 与 `契约违规 [dead-entry]`（R14 交付态裸门固有形态，absorb 前不消）。本轮零源码改动，该红纹丝不动。
- R23 **`git diff --stat` 只含两个文件**：`codegraph/diffs/cards-B156.2-charter-4.json` 与本台账。本轮没有触碰任何其他文件。
