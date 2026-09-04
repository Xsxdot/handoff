# B330 spec 审查

审查对象：`docs/superpowers/specs/b330.md`（状态：待用户批准；bug-batch：独立审查吸收后即批准；头部自称 **L1**）
对照台账：`docs/superpowers/specs/b330-ledger.md`
对照定义文件：`docs/superpowers/specs/b330-charter.json`（声称相对 charter v10 只给七个 dispatch 节点加 `override.squad=runner`）
对照代码：工作树 `/Users/sycm/.handoff/worktrees/b330-charter`，分支 `cards/B330-charter`，基线 `origin/main` @ `4e633e9a6`（与 `handoff version` 的 revision 同戳）。本审查只写本文件，不改 spec/代码/json、不 commit、不 `workflow put`。
审查者：独立 spec 审查人（charter 流；与作者无会话史，一切以亲手读活代码和活账本为准）
日期：2026-09-04

行号按当前工作树，会漂。`codegraph sym ResolveNode` 命中 `n_ledgerstep_ResolveNode`（`k_ledgerstep_fn` / `d_ledger`）；`startCardStep` 命中 baseline 节点 `n_agentd_Server_startCardStep`，签名已过期（图里仍是 `(cardID, node, actor string)`，活代码是 `CardStepReq`），`who-calls ResolveNode` 边为空并警告 5 个未扫描入口。最优图 `k_agentd_Server → d_gateway`。小队分支以源码为准，记图覆盖债。

## 1. 总判

**可以批准。** 符合头部「bug-batch：独立审查吸收后即批准」——设计不用改；文末 Minor 是建议吸收的句子，不挡批准。

方向对，一条不变式成立：编制域只在 `node.Override.Squad != ""` 时 `Admit`；现网 charter v10 七个派发节点 Squad 全空，所以真卡点火走模板默认、排队横带是哑的。本卡只把已有小队 `runner` 写进那七个节点，不改 Go、不改 HTTP、不迁在飞卡。弃选站得住：空 squad 默认进 runner 会推翻 B156.3「空 = 存量直绑」；只绑 implement 只修一半；出厂种子写死本机小队名会把编制泄漏进仓库；全量 migrate 会把钉旧版的在飞轮次拽去 muse/opencode。

亲手 diff **成立**：`b330-charter.json` 相对活账本 `handoff workflow show charter` 的 `Def`，在剥掉七处 `"squad":"runner"` 之后 **canonical JSON 全等**。`states` / `gates` / `board` / 节点序 / `template` / `discipline` / `purpose` / `produces` / `max_rounds` / `omit_acceptance` / `next` / `on_fail` / 各节点 `gate` **无静默改动**。L1 独立验证成立，不抬 L2/L3（见 §3）。

不能当成「JSON 对了故事就验完」的原因收在 Minor：空位时的主可见变化是执行者从模板默认换成 muse/opencode，不是 `GET /api/queue` 立刻非空；本卡钉 v10，用 B330 自己 `--step` 当试纸必假红。正文里测试决定 4 已经把真机派发放进 OOS，读得够清，不构成承重歧义。

## 2. Findings

### Critical

无。不定级错、不改 Go/HTTP、定义文件相对 v10 没有夹带 states/gates/board/produces/max_rounds/purpose 的静默改动。会让实现走错题的读法，正文里都有挡板（只 put、不 migrate、不新增 Go 测试、不在本卡打 `--step`）。

### Important

无。

### Minor

#### M1. 弃选 migrate 写成「linux-01/codex 直绑」；活模板不是这样。空位时的主可见变化是换执行者，不是队列变非空

- **位置**：问题陈述 `b330.md:13,21`、弃选 `b330.md:35`、故事 1 `b330.md:40`、测试 4 `b330.md:60`
- **事实**：活模板 `handoff template show charter-default` → v5，`executor=codex`，`target=""`。空 target 在 B271 之后是本机，不是 linux-01。v10 七个派发节点自身也没有 `override.target` / `override.executor`。接线后 `Admit` 走 `runner` 唯一成员 muse（mac-02 / opencode / 5），`bindingFor` 用载体三元组接管（`internal/scheduling/scheduling.go` `bindingFor`）。满员才 `ErrNoSlot` → `Enqueue`；有空位则当场派到 muse/opencode，`GET /api/queue` 可以仍空。
- **建议**：弃选改成「钉旧版、正在走模板默认（本机 + charter-default 的 codex）的轮次会被改去 muse/opencode」。故事 1 拆两句：空位 → 走 runner 解析到 muse/opencode，不再用模板默认；满员 → 进 `ignition_queue`。验收不要把 put 后 queue 仍空当成失败。

#### M2. 「钉 v10 的在飞卡」写窄了；本卡自己也是 v10，不能当故事 1 的试纸

- **位置**：故事 2 `b330.md:41`、测试 3–4 `b330.md:59-60`、L1 plan `b330.md:67`
- **事实**：`handoff card show`：B330 待办 charter **v10**；B329 spec v10；B320 implement v10。另有未迁的旧钉：B307/B313 implement **v9**、B316 review v9、C2 待办 **v7**。`ResolveNode` 读的是卡钉版本不是最新版（`internal/ledgerstep/runner.go#ResolveNode`）。对本卡 `card dispatch --step plan` 在 put 之后 **仍空 squad**，会继续走模板默认——看起来像 put 没生效。
- **建议**：测试 3 改成「put 前已钉的卡（抽查 v7/v9/v10 各一张）`workflow_version` 不变」。验收句补「禁止用 B330 自己 `--step` 验证故事 1」。测试 4 已有「不在本卡打 `--step`」，把对象写成「新开卡」即可。

#### M3. 声明缝是 `ResolveNode`，本卡实际锁的是 `workflow show`

- **位置**：测试决定缝 `b330.md:55-57`
- **事实**：`ResolveNode` 导出、生产调用方是 `startCardStep` / `handleCardStep` / `StepRunner.nodeFor`，**不是假缝**。但本卡「不新增 Go 测试」，四条验收没有一条调用 `ResolveNode`。新卡能吃到 runner，靠的是 `CreateCard` → `prepareCard` → `GetWorkflow(name, 0)` 钉最新版（`internal/ledger/cards.go#prepareCard`）；账本此刻只有一条工作流 `charter`，缺省建卡必钉最新。
- **建议**：缝改写成 `Store.PutWorkflow` / `GetWorkflow`（调用方 `cmd/workflow.go` 的 put/show），并加一句「新卡钉最新版，故 show 最新版的 squad 即 `ResolveNode` 对新卡将读到的值」。或保留 `ResolveNode` 为行为缝，但写明本卡的缝级断言就是 show 原文。

#### M4. 「与 v10 逐字节同」过满

- **位置**：方案 `b330.md:26`、实现决定 `b330.md:48`
- **事实**：剥掉七处 squad 后 put 文件与 live `Def` 的 canonical JSON 全等，**语义**成立。`workflow show` 外包 `Name/Version/CreatedAt`、键序、空白与 put 文件不是逐字节。`PutWorkflow` 还会 `withStatesFromNodes()` 用 `node.gate` 覆盖顶层 `states/gates`（本文件两边已一致，结果不变）。
- **建议**：改成「除七处 `override.squad` 外字段与 v10 Def 语义相同（states/gates/board/节点能力开关/产出/路由）」。L1 plan 第 1 步用字段 diff，不要 `cmp` show 原文和 put 文件。

#### M5. OOS「后续要做」两条未落 `docs/roadmap.md`

- **位置**：OOS `b330.md:75`；对照 `docs/roadmap.md` 无 B330
- **事实**：spec 收尾自检要求「本期不做、后续要做」逐条进 roadmap。两条是：下一张新 charter 卡走满「满员入队 → 出队」；需要时给 runner 加成员。
- **建议**：吸收时把这两行抄进 roadmap，来源指向 `docs/superpowers/specs/b330.md` OOS。永不做三条（空 squad 默认 runner / 协调者改走 runner / 出厂种子写死小队名）不要落队列。

## 3. 定级意见

独立结论：**L1 成立。** 不要抬 L2，不要抬 L3。

定级两问套到定稿范围（只 put 账本 charter 新版本，不改 Go、不改 HTTP、不 migrate）：

1. **跨几个子系统的契约面？** 本卡增量只写账本工作流定义（`d_ledger` / `Store.PutWorkflow`）。`cardstep` 小队分支、Admit/Enqueue、CLI `--step` 都是 B156.3 已上线的消费方，本卡不改它们。单子系统。
2. **动不动契约层？** 填的是已冻结字段 `NodeOverride.Squad`（`internal/ledger/types.go`、`internal/proto/ledger.go` 均已有；`ledgerNodeWire` 已投影）。不新增 wire、不改 HTTP 动词、不改 Admit 语义。不动契约层。

L1 两判据：

1. **plan 增量为零**：实现就是 put 指定文件 + show 对账七行 + 不 migrate。写成 plan 只会复述 spec 末尾四步。同一文件挂 `spec:` 与 `plan:` 过 implement 门，正当。
2. **验收一眼可核**：`workflow show` 最新版七个派发节点 `squad=runner`、五个人工列空、版本 +1、抽查旧卡钉住。不需要单独 acceptance 节点。真机排队狗粮已 OOS，不把 L1 抬成 L2。

不是 L2：没有「改哪一层收口 / 谁依赖谁」需要 plan 展开的分叉。不是 L3：不跨子系统、不对接新的跨进程契约。

## 4. 接缝

声明缝：**账本工作流定义 → `ledgerstep.ResolveNode` 读到的 `node.Override.Squad`（调用方 `internal/agentd/cardstep.go` 的小队分支）**。

合法性：

1. **符号 + 调用方**：`ResolveNode` 导出（`internal/ledgerstep/runner.go`）。活调用方：`startCardStep`（`cardstep.go`，小队分支在 `node.Override.Squad != ""` 时 `admitSquadStep`）、`handleCardStep` 预检、`StepRunner.nodeFor`。`codegraph who-calls` 无边，以 grep 为准。
2. **不是假缝**：有生产调用方，不是为走满分支抽的纯函数。
3. **本卡锁点偏低一档**：不新增 Go 测试时，真正被断言的是 `PutWorkflow` → `GetWorkflow`（`cmd/workflow.go` put/show）。行为缝仍是 `ResolveNode`，但要对新卡成立，还依赖「建卡钉最新版」这条已有不变式（活账本只有 `charter` 一条流，缺省建卡成立）。

生产点火链（本卡不改，只接线）：

`handoff card dispatch --step` → `runStepDispatch`（`cmd/card_node.go`，本机 `LocalEndpoint` + `client.CardStep`）→ `POST /api/cards/{id}/step` → `handleCardStep` → `startCardStep` → `ResolveNode`（**卡钉版本**）→ squad 非空则 `admitSquadStep`（`Admit`；满员/`ErrRetryExhausted` 转 `Enqueue`，返回 queued，**不起 goroutine、无 task、卡事件流零写入**，HTTP 仍 202）→ Binding 三元组接管 `StepRunner` 的 target/executor/model。

协调者 `card coordinate` 走 `LaunchAdmit` + `leader`（`scheddrain.go` / `coordapi.go`），不读这些节点的 squad。人工列 `Dispatch=false`，`StepRunner.Run` 在派发前拒绝（`node.go`），不占 runner——前提是人工列继续空 squad（put 文件如此）。

建议（不挡批准）：见 M3。不要为了对齐声明缝去加 Go 测试，那会把 L1 做成 L2。

## 5. 二解测试

| 陈述 | 读法 A | 读法 B | 承重？ | 正文是否钉死 |
|---|---|---|---|---|
| 只 put 新版本 | 写最新版，旧钉不变 | put 完把 B330 migrate 到新版好让本卡 implement 走 runner | 会 | 钉死不 migrate（含 B329）；本卡由协调者本机 put |
| 七个派发节点 | 全部 `dispatch:true`：contract/breakdown/plan/implement/review/integrate/图对账 | 只绑会「跑很久」的 implement | 会 | 钉死七个；弃选「只绑 implement」 |
| 新开卡走 runner | `card add` 之后钉最新版，`--step` 才 Admit | 对本卡 B330 `--step` 就该进队 | 会（假红） | 故事 1 写「新开的」；测试 4 禁止本卡 `--step`。建议 M2 再钉验收句 |
| 满员进点火队列 | 有空位：muse/opencode 当场派发；满员才入队 | put 成功 ⇒ `GET /api/queue` 立刻非空 | 弱 | 故事 1 两件事写在一句里。建议 M1 拆开；不挡，因验收是 show 不是 queue |
| `--target/--executor` 仍可用 | 仍经 `effectiveCovers` 进 Admit，**不**退回空 squad 直绑；占 runner 名额 | 有 `--target` 就跳过小队，回到模板默认 | 会 | 「保持现网 Admit；节点现在多了 squad 解析层」足够偏向 A |
| runner 不健康 | `ErrNoHealthy` / 小队不存在 → `startCardStep` 同步错误 → HTTP 400，不回落模板 | 回落 charter-default/codex | 会 | 「不再静默用模板默认执行者」锁 A。`PutWorkflow` **不**校验小队存在（与纪律块相同，派发时才报） |
| 人工列空 squad | 不派发、不 Admit | Web 文案「空值表示不派发」——误以为空 squad 的 dispatch 节点也不派发 | 不（本卡不改 Web） | 人工列本来 `dispatch` 假；空 squad 的派发节点仍走模板。本卡不碰 Web |
| 出厂种子 | 本卡 put 用 spec 旁 json | 有人日后用 `deploy/workflows/charter-v4.json` 再 put，会丢掉 squad，并静默改 implement 的 gate（出厂是 `require_attachment: plan`，v10 是 `require_attachment_any: [plan,breakdown]`） | 后续卡 | 永不做「出厂写死小队名」；未写「禁止用未含 squad 的出厂种子覆盖」。建议吸收时 OOS 加半句 |

结论：承重岔口在正文里都有挡板。剩下的是验收试纸和可见变化的主语（M1/M2），零上下文实现者按 L1 plan 只会 put+show，走不错题。

## 6. 批准前最小补丁

无必须补丁。设计、定级、定义文件都可以原样进 implement。

若按 bug-batch「吸收后即批准」顺手改 spec 句子（仍不改 json、不改代码）：

1. 弃选 migrate 的理由改成模板默认（本机 + codex），不要写 linux-01/codex 直绑。
2. 故事 1 拆空位 / 满员；验收明确 queue 空不是失败。
3. 测试 3 覆盖 put 前已钉的 v7/v9/v10；验收禁止用 B330 `--step` 当试纸。
4. 缝的符号与 show 锁点写到同一句（M3）。
5. 「逐字节」改成「Def 语义相同」。
6. OOS 后续要做两条吸收进 `docs/roadmap.md`。

以上全部是句子和 roadmap 一行，不是代码，也不是对 `b330-charter.json` 的改动——该文件相对 v10 已经干净。

## 7. 跑过的命令与读过但未成 finding 的地方

### 命令

- `git rev-parse --abbrev-ref HEAD` → `cards/B330-charter`；`HEAD` / `origin/main` = `4e633e9a6`；工作树未跟踪三份 spec 文件。
- `handoff version` → revision `4e633e9a674a+dirty4`（与基线同戳）。
- `handoff workflow show charter` → **Name=charter Version=10** CreatedAt=`2026-09-03T20:46:13+08:00`。七个 `dispatch:true` 节点 override **无** squad；人工列 override `{}`。
- `handoff workflow list` → 仅 `charter v10`。
- `handoff squad list` → 载体 grok(mac-02, grok, 3)、muse(mac-02, opencode, 5)；小队 `leader`(coordinator, muse/5)、`runner`(executor, muse/5)。与 spec 现状读数一致。
- `handoff template show charter-default` → v5，`executor=codex`，`target=""`，`purpose=charter`。
- `handoff card show B330` → charter **v10** 待办。B329 spec v10；B320 implement v10；B307/B313 implement v9；B316 review v9；C2 待办 v7。
- Python canonical diff：`b330-charter.json` vs live `Def`，剥 squad 后 **全等**。squad 只加在七个 dispatch 节点，值均为 `"runner"`。
- `codegraph sym ResolveNode` / `sym startCardStep` / `who-calls ResolveNode`（见文首图覆盖债）。

未跑：`handoff workflow put`（本审查禁止）；未对本卡或任何卡 `--step`；未打 `GET /api/queue`（台账记空，本审查不把它当代码事实）。

### 对码（spec 现状读数）

| spec 引用 | 实际 | 结论 |
|---|---|---|
| `cardstep.go`：`Override.Squad != ""` 才 `admitSquadStep` | `cardstep.go:64-75` | 成立 |
| 满员 `ErrNoSlot` 转 `Enqueue`；queued 无 task、卡事件零写入、返回 nil | `scheddispatch.go:75-91`；`cardstep.go:71-74` 释放槽位 `return nil`；`handleCardStep` `err==nil` → **202** | 成立。CLI 随后短等 dispatched/失败 comment，queued 会 20s 后打「已受理，首态未到」（`card_node.go:188-193`）。B156.3 既有形态，本卡 OOS 真机 |
| `--target/--executor/--model` 经 `effectiveCovers` 交 Admit（请求 > 节点） | `scheddispatch.go:57-58,108-123`；`bindingFor` 再覆盖载体缺省 | 成立 |
| 协调者不经本文件，走 `leader` + `LaunchAdmit` | `scheddispatch.go:12` 边界；`coordapi.go` / `scheddrain.go:249,328` | 成立 |
| put 只产生新版本；钉住的卡不吃新定义 | `cmd/workflow.go#wfPutCmd` → `PutWorkflow` MAX+1；`ResolveNode` 用 `card.WorkflowVersion`；`CreateCard` 钉最新 | 成立 |
| `NodeOverride.Squad` 零值 = 存量直绑 | `types.go:183-186` 注释与 `json:"squad,omitempty"` | 成立 |
| 不改 Go / 不改出厂种子 | 工作树相对 main 无 `.go` diff；`deploy/workflows/charter-v4.json` 仍无 squad | 成立（本审查未 put） |

### 读过但未成 finding

- `PutWorkflow.validateNodes`：校验模板存在、不校验小队名（与纪律块同一纪律：Store 看不见编制域）。`charter-default` v5 在，put 不会因模板拒。
- `withStatesFromNodes` 覆盖顶层 gates：put 文件 `node.gate` 与顶层 `gates` 已对齐，不会悄悄改门。
- 看板列名「代办」（对「待办」）及 `state_to_column` 里「已出 spec / 待合并 / 终止」等非节点键：v10 原样，put 未「修好」。若改掉反而是静默 diff。
- `图对账` `max_rounds=2`、仅 review 有 `purpose=review`、implement 无 `produces`：均与 v10 同。
- Web `FlowsPage` 已有执行者小队下拉，`ledgerNodeWire` 已投影 `Squad`。本卡走 CLI 全文 put，避免只存 nodes+board 的另一条写路径在审查范围外被误用；不把「其实可以点 UI」写成缺陷。
- Web 文案「空值表示不派发」与代码「空 squad = 模板直绑仍派发」不一致，是 B156.3 既有 UI 债，本卡不改 Web。
- `leader` 与 `runner` 共享载体 muse、上限 5：接线后 charter 派发和协调者拉起争同一载体计数。编制是用户的，OOS「加成员」已覆盖，不当本卡缺陷。
- `deploy/workflows/charter-v4.json` 相对 v10 还有 implement gate 从 `require_attachment: plan` 到 `require_attachment_any` 等历史差。spec 不用它当底是对的。
- B156.3 已有缝级测试 `TestEffectiveCoversParityMatrix`、`scheddispatch_test.go` 小队夹具；本卡不重复做 Go 测试正当。
- 卡列表 JSON 不含 `workflow_version`，测试 3 必须 `card show`，不是 `card list --json`。
