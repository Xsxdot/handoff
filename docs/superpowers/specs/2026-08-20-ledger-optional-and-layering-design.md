# 账本可选化与命令分层（B156.1 收尾修订）

> 状态：spec（2026-08-20 讨论定案）。属 B156.1，是一期落地后、真用起来之前的
> 收尾修订。上游：[一期 spec](2026-08-18-workbench-phase1-design.md)、
> [交接文档](../ledgers/2026-08-19-b156.1-handoff.md)。

## 1. 背景：三个问题

一期工具已全部落地并真机验过，但「让它被用起来」之前暴露三个问题：

1. **可选化不存在**。账本被实现成了主入口：dock「工作项」图标与 `/cards`
   `/flows` 路由无条件注册，任务看板降级为「只看未挂账」的兜底；CLI
   `openLedger()` 与 agentd 在 dsn 为空时**静默自建**本机 `ledger.db`——
   系统里根本没有「账本未启用」这个状态。用户裁决：**不能要求所有用户都用
   这个功能**，可选边界 = skill + 前端入口。
2. **两条回路在 skill 里打架**。任务回路（dispatch→wait→分诊）与账本回路
   （card dispatch→card wait→节点）看似两条主循环，spec §7 改写会与现有
   handoff skill 四节冲突。讨论定案：**分层，不是动词融合**——账本回路是
   把任务回路**包了一层**，外层管卡的调度，内层处置具体 task 事件时用的
   还是原动词（reply/approve/continue），分诊表、审批纪律、排障全部共享。
   曾提议 `dispatch --card` 动词收编与 status 账本面板，**均撤销**：那是拿
   改 CLI 去解决文档问题，且污染执行域动词的纯净性。
3. **回合末四分法缺两个写入口**。spec §7 要求完成项→验收事件、阻断→等人
   标记，但 `RecordAcceptance` 与 `MarkNeedsHuman` 都只有 store 方法没有
   CLI——「已验」永远变不成 true，主会话也没法手工标「阻断需人工」。

## 2. 裁决原则（先于一切细节）

- **执行域动词一字不动、零 card 感知**。`dispatch / wait / reply /
  continue / approve / done` 保持今天的样子，非账本用户的世界零变化。
  现有 `wait --card` 分支塞在核心 `cmd/wait.go` 里（`runCardWait` 住在
  那个文件，5 处 ledger 引用），是这条原则的反例，本次搬走。
- **账本域自成 `card` / `workflow` / `decision` 三族**，内部调用执行域。
  「挂卡」不是独立动作，是 `card dispatch` 自动做的回链。
- **账本状态永不自发流转**，每次转移必须有 actor 落事件（取证）。一期的
  发动机是主会话（按 skill 回路推），三期换成规则引擎，人只在门上出现。
- **不做单独的 ledger skill**。原因：AI 按 description 触发 skill，用户说
  「派发这个 plan」触发的是 handoff skill，单独的账本 skill 根本不会被
  加载；且分层后两条回路没有冲突，一节「账本模式」放进同一份 skill 即可，
  未启用账本的用户读到的是一节明确标注前置条件的惰性内容。
  （早先「单独 skill + 手动安装」的提议由此被取代；现有 skill 安装机制
  也是单 skill 的，不为此改造。）

## 3. `ledger.enabled` 开关

`LedgerConfig` 加 `Enabled bool`（`yaml:"enabled,omitempty"`），**默认
false**。dsn 语义不变：enabled 且 dsn 空 = 本机 SQLite 回退；enabled 且
dsn 非空 = 中心库。开关是**机器级**的（协调机的账本），一个信号喂三面：

| 面 | enabled=false 时 |
|----|------------------|
| agentd | 不开账本库、不起事件镜像子系统、`SetLedger` 不调用。`GET /api/ledger/health` **挪出 `withLedger`**，恒 200 返回 `{"enabled":false}`（其余 `/api/cards*` 等仍走 withLedger 503——健康探针是给前端做门控的，不能用 503 当信号） |
| web | 见 §5 |
| CLI | `openLedger()` 单点拦截，card/workflow/decision 三族全部报：`账本未启用：在 config.yaml 设 ledger.enabled: true（可选 ledger.dsn 连中心库，缺省本机 SQLite）` |

边界：dsn 非空但 enabled=false → **不启用**，agentd 启动时 Warn 一行
「ledger.dsn 已配置但 enabled=false，账本未启用」，防静默困惑。
`decodeStrict` 已知键清单更新为 `ledger{enabled,dsn}`。

无兼容包袱：card 命令族与账本 API 从未发版（main 上没有，本机二进制没有），
现有唯一受影响方是本分支的验收实例，改法是给它们的 config 补一行。

## 4. CLI 三处

### 4.1 `card wait` 搬家

`cmd/wait.go` 删掉 `--card` / `--subtree` 两个 flag、互斥校验与
`runCardWait`，回到纯执行域。新增 `card wait <id> [--subtree]
[--timeout]`，实现原样搬运（账本单流多路 wait 语义不变），既有
`wait_card_test.go` 的用例随迁改名。

### 4.2 `card accept`——验收写入口

`card accept <id> --evidence <文本>`：调 `RecordAcceptance(id, true,
evidence, actor)`，**verified=true 时 evidence 必填**（空报错——已验必须
带证据，这是本项目取证文化）。`--unverified` 落 verified=false（对应
backlog 的 done(未验)，evidence 可空）。actor 用 `ledgerActor()`。
判据文本仍由 `card update --accept` 设，两者分工：update 写「怎么才算
验过」，accept 写「验的结果」。

落不落验收记录永远自愿；只有工作流配了 `RequireAcceptance` gate（如
出厂 feature 流的「待合并」）它才成为那条流的门。

### 4.3 `card needs`——等人标记写入口

`card needs <id> <reason...>` 调 `MarkNeedsHuman`（reason 必填，store
已强制）；`card needs <id> --clear` 调 `ClearNeedsHuman`。回合末四分法
的「阻断需人工」一格由此有门。节点执行器自动打的标记与手工打的同源同显。

## 5. Web 门控

Shell 启动时查一次 `/api/ledger/health`：

- `enabled=false`：dock 不渲染「工作项」图标（`ProjectTree.tsx:716`）、
  `/cards` `/flows` 路由不注册（直达 URL 走既有未匹配路径行为，不做
  专门提示页）、dock 角标不计卡数（`Shell.tsx:83`）、任务看板**回到主
  入口形态**——不默认「只看未挂账」筛选（`BoardPage.tsx:47`），
  「未挂账」概念整体不出现；`columns.ts:157` 等「工作项看板已是主入口」
  的注释同步修正为条件性表述。
- `enabled=true`：现状不变。
- health 请求失败（agentd 版本旧/网络错）：按 false 处理，不亮账本入口
  ——宁可少显示，不显示一个点进去 503 的入口。

## 6. skill「账本模式」节（spec §7 的落点）

改 `skills/handoff/SKILL.md`（随二进制 embed 分发，安装机制不动），新增
一节「账本模式：把任务回路包在卡回路里」，前置条件明确标注「本项目
`ledger.enabled: true` 时适用；否则本节全部忽略」。内容大纲：

1. **唤醒先查账不信记忆**：`card list --needs` + `decision list` +
   在飞卡的 `card show`，从账本与事件流重建现场。
2. **派发前查账防重复开工**：`card list --project <p> --status 进行中`，
   同一张卡已被认领则干净失败（CAS 语义，第二个会话得到明确报错）。
3. **外层换 card 族**：`card dispatch <id>`（即认领）→ `card wait <id>
   [--subtree]`；**内层与任务回路完全相同**——醒来处理 permission_request
   / question / turn_failed 用原动词（reply/approve/continue），事件
   分诊表、审批硬纪律、审阅取证、排障各节原样适用，不重复不改写。
4. **task 完成后的推进**：`card move <id> 待审阅` → `card dispatch <id>
   --node review`（fail 自动 continue 带发现项，3 轮封顶超限转等人；
   `card note <id> --reset-node review` 人工重置）→ pass 后
   `card accept <id> --evidence ...` → `card move` 下一态 →
   `card dispatch <id> --node merge`（基线=main 的卡自动推「待合并」
   等用户，绝不自动合主线）。
5. **回合末四分法落账**（聊天 prose 照旧，账本是结构化副本）：
   完成项→`card move`/`card accept`；更正→`card note --correction`；
   请示→`decision open`；阻断→`card needs <id> <reason>`。
6. **验收后发现 bug：开新卡挂关联，不 reopen**（账本历史不改写）。

现有各节的改动仅限：在「主循环」节头部加一行分流指引（「本项目启用账本
时，外层循环见『账本模式』节，本节仍适用于内层 task 处置」），不改动
任何既有内容的语义。

## 7. 明确不做（本轮）

- web 的验收开关 UI、「按节点派发」按钮、子任务树 rollup——留 backlog
  按痛感排（交接文档 A 组）。
- §8 存量切换与 backlog.md 冻结——用户定案先试用两天再切。
- 本分支合回 main——用户决定「用一段时间再合」，main 已反向合入
  （`884f09a6d`）。
- 合并节点 origin 依赖等 D 组三条观察——切换前另行定夺，不混进本轮。
- status 命令加账本面板——撤销（见 §2）。

## 8. 验收判据

① 新 config（无 ledger 段）起 agentd：DataDir 下**不生成** `ledger.db`、
   `/api/ledger/health` 返回 `{"enabled":false}`、`card add` 报未启用文案。
② `ledger.enabled: true` 且 dsn 空：SQLite 回退照旧，card 全族可用。
③ web：off 时 dock 无「工作项」图标、`/cards` 直达不渲染看板、任务看板
   无「只看未挂账」默认筛选；on 时三者恢复现状。
④ 搬家后 `cmd/wait.go` 零 ledger import；`card wait --subtree` 行为与原
   `wait --card --subtree` 等价（原测试用例迁移后全绿）。
⑤ `card accept B --evidence "..."` 后 `card list` 验收列由「待真机验」
   变「已验」；`--evidence` 缺失时报错不落事件。
⑥ `card needs B "等授权"` 后 `card list --needs` 可见该卡；`--clear` 后
   消失。
⑦ skill 新节中出现的每条命令与 `handoff card --help` 实际命令面一致
   （无凭空 flag）。
⑧ 全量门：gofmt 无输出、`go build/vet/test ./...` 全绿、web `tsc` 0 错
   + vitest 全绿。
