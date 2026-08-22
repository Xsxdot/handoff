# B153：backlog 总账从 markdown 迁入卡账本

> 2026-08-22 定稿。charter 套件与工作流整合的第一步（三个子问题：backlog 迁卡、
> 日常路径卡驱动、纪律块与 charter 同源——本 spec 只做第一个，后两个见 Out of Scope）。

## 问题陈述

`docs/superpowers/backlog.md` 是本地 markdown 总账，取号靠「grep 算 max」——它只有
局部视图却给出看起来正确的答案，撞号事故已实测四轮（记忆 `backlog-diverged-across-branches`：
八条分支八种答案；三路并集跑了仍会因汇流点并行推进而过期）。B153 立项时用户已裁决：
不再打补丁，把总账管理做成 handoff 自己的能力。

卡账本一侧的基础设施已基本齐备（现状读数，contract 节点对本轮工作树复核）：

- 卡 ID 即 B 号，中心账本事务内分配，永不复用：`internal/ledger/cards.go#Store.CreateCard`
  （cards.go:118-160）、`nextTopID`（cards.go:57-90）、子卡点号 `nextChildID`（cards.go:92-116）。
- 防撞历史号的水位已内建：`Store.EnsureMinB`（cards.go:33-55，注释明写「迁移前防与
  markdown 总账撞号」），CLI 出口 `cmd/card_minb.go`。
- 搁置语义已内建：`card close --reason 搁置` + `card revive`（搁置→待办）。
- 证据门已内建：`card accept`（缺省已验需 `--evidence`，`--unverified` 落未验）——与
  product-backlog 的 evidence gate 同构。
- 逃生门已内建：`card export` 导出最薄 markdown 只读快照。
- `triage` 纯人工定性流（待办→定性中→已定性）出厂 seed 已落地（B167）；跨流迁移五条
  语义已真机验收（B167）。

**缺的只有一条**：显式指定 B 号的导入通道——`CreateCard` 只会自动取号，存量行迁入
要保原号。这是本 spec 唯一的新代码。

## 级别与档位

**L3 轻档**（contract → breakdown → 单轮 implement → review → acceptance → finish）。

定级两问：①触及账本域与 CLI 域两个逻辑域（实例化清单 §2）；②账本域契约面新增导入
入口（域间契约增量）→ L3。选档：单域工作量远低于 70 分钟固定成本 → 轻档。
轻档不跳契约冻结——导入通道的签名与撞号语义正是最该冻结的那部分。

## 方案（含弃选与理由）

### 存量处置：迁活跃行 + 冻结 markdown（已拍板）

- **迁入**：活跃行导入卡账本，保原 B 号——💡 idea 13 行、📋 specced 5 行、🔨 doing 7 行、
  📦 epic 1 行（含其子行）。
- **不迁**：🗄️ shelved 3 行留在冻结 md 里（用户维持原判：休眠条目不占卡账本，复活时
  走显式 ID 导入通道补建卡）；✅ done 约 170 行留 md 作纯历史。
- **冻结**：md 顶部加冻结标注（「本文件已冻结为历史归档，新条目一律建卡；shelved
  条目复活走 `card import`」）；`card minb` 垫高水位到全局 max（当前 169，操作时重算）。
- 冻结标注要同时落到 `main` 与 `handoff/web-console` 两条血脉（汇流点并行推进是撞号
  根因之一，冻结不落全就没冻住）。

弃选：

- **全量导入删 md**：170 行 done 的 timeline 只会有一条导入事件，审计价值有限；每行
  几百字的富文本案情备注塞进卡字段会失真。
- **只设 min_b 不迁存量**：双账并存期无界，领活要看两处，skill 正文得同时描述两套操作。

### 状态映射：全落 triage（已拍板）

| markdown 状态 | 卡落点 |
|---|---|
| 💡 idea | triage「待办」 |
| 📋 specced | triage「已定性」+ spec 挂附件（kind=spec，仓内相对路径） |
| 🔨 doing | triage「已定性」+ `card note` 记「存量迁入时已在进行中」+ 原领取日期 |
| 📦 epic | 父卡（子行用显式 ID 导入为点号子卡） |
| 🗄️ shelved | 不迁，留冻结 md |

领活池 = triage「已定性」一列。卡被领取时按定性级别跨流迁到执行流（L1→`bug`、
L2→`feature`、L3→`domain`；跨流迁移机制已落地），列落点按卡当前进度显式指定。
**本轮卡只当账（记录状态与证据），不当驱动**——领取后实际工作照旧走本地 charter +
直派发，完成时 `card accept` 记证据。节点派发按钮日常化是下一份 spec 的事。

弃选：**按状态分流落位**（specced/doing 逐条判目标流直接迁入执行流）——30 行逐条
定性工作量大，且领活池散在各流各列，Entry 3 扫描逻辑复杂。

### 富文本备注的去向

markdown 行的「变更痕迹/备注」动辄几百字。导入时：标题、优先级、来源摘要进卡字段；
长备注**不搬**，卡上 `card note` 落一条「案情见冻结 md 对应行」的指针。弃选「全文
搬进 note」：md 冻结后原文不会漂移，指针足够，搬运只会失真。

## 用户故事

1. 说「记个需求」→ 建卡落 triage「待办」，B 号账本自动分配，任何机器任何分支不再撞号。
2. 说「把 Bxxx 聊透」→ 交棒 `charter:spec` 对话出 spec → spec 挂卡附件 → 卡移「已定性」。
3. 说「领个活」→ 扫 triage「已定性」推荐一张 → 领取即按级别迁执行流并交棒 `charter:plan`。
4. 活干完 → `card accept --evidence "go test ./... ok"`（或 `--unverified`）→ 卡落终态，
   证据在 timeline 可追溯。
5. shelved 条目要复活 → `card import` 按原 B 号补建卡 → `close --reason 搁置` 的旧路不走，
   直接落「待办」。

## 契约语义与接缝（L3）

**定语义，不定签名**（精确签名归 contract 节点查证落地）：

- 账本域新增**显式 ID 导入**入口：语义为「携带既有 B 号建卡」。撞号规则——目标 ID
  已存在 → 拒绝；目标 ID 顶层号高于当前水位 → 允许（导入不受 min_b 约束，min_b 只约束
  自动取号）；点号子卡要求父卡已存在。导入卡与普通卡建成后无任何行为差别（同样钉
  工作流版本、落 card_created 事件——事件里标注导入来源）。
- CLI 域新增 `card import` 子命令，薄壳转发账本域入口；这是**永久能力**（shelved 复活
  依赖它），不是一次性迁移脚本。
- 依赖方向不变：CLI → 账本，不新增反向依赖；agentd HTTP 面**本轮不开导入端点**
  （迁移与复活都是协调者本机操作，YAGNI）。

## 实现决定

- 迁移操作本身（30 行导入、md 冻结、minb 垫高）由协调者会话逐条执行，**不写批量
  迁移脚本**——量小、每行要人工摘要来源，脚本化收益为负。
- product-backlog skill（`~/.claude/skills/product-backlog/SKILL.md`）改写为卡操作：
  Entry 1 = `card add`；Entry 2 = 交棒 `charter:spec`（旧文的 brainstorming/writing-plans
  是 superpowers 旧名，一并改为 charter:spec/charter:plan）；Entry 3 = 扫 triage
  「已定性」；evidence gate = `card accept`；epic = 父卡/`card split`；状态机一节改为
  流与列的映射表。skill 的红旗与边界场景按卡语义重写（如「idea→done 非法」变为
  「待办卡不经定性不得领取」）。
- 全局 CLAUDE.md §3.2 product-backlog 行的触发词与简介对齐新形态；记忆
  `backlog-diverged-across-branches` 补一段「冻结后取号纪律作废，新号一律账本分配」。
- 迁移完成的收尾在 `charter:finish` 做：md 冻结提交与 skill 改写同分支收口。

## 测试决定（接缝清单）

最高可测缝一个：**账本域导入入口的 Store 层测试**（显式 ID 建卡成功、撞已存在 ID 拒绝、
子卡缺父拒绝、导入不受 min_b 约束、导入后自动取号水位正确跳过导入号）。CLI 薄壳加
一条端到端（import → show 字段核对）。skill 正文与文档改写无自动化测试，走 review
节点人工对账。

## Out of Scope

- **日常路径卡驱动**（节点派发按钮取代直派发、charter 节点收尾自动操作卡）——三个
  子问题之二，另立 spec；本轮卡只当账本。
- **纪律块正文与 charter skill 同源**（agentd 纪律块库仍是 superpowers 改写版的问题）
  ——子问题之三，另立 spec。
- done 历史行导入；backlog.md 删除（冻结不删）。
- agentd HTTP 导入端点、Web 控制台导入界面。
- AI 辅助定性（triage 流加派发节点）——workbench 基准 §6 已记，先人工定性跑通。
- `charter` 工作流（v1）与 `domain` 流的冗余去留——试点遗留，另议。
- product-backlog skill 中「未下放的强制必查 backlog」规则——维持不下放，真踩坑再补。

## 备注

- 本 spec 即 B153 的 spec；backlog 行状态 💡 idea → 📋 specced 的更新按现行 md 规则
  最后一次手工执行（更新落汇流点分支）。
- 迁移执行时活跃行数以当时 md 为准（本文的 13/5/7/1 是 08-22 快照）。
- 图覆盖债：无（本轮查证符号 CreateCard/nextTopID/EnsureMinB 均命中）。
