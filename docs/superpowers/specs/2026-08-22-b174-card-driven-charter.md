# B174：日常路径卡驱动——在卡上跑通完整 charter 流程

> 2026-08-22 定稿。charter 与工作流整合三部曲之二（之一 B153 backlog 迁卡已完成，
> 之三纪律块同源机制并入本篇 §6）。目标：日常开发从「本地 charter + 直派发」迁到
> 「卡驱动」——卡不再只当账，成为流程的载体。

## 问题陈述

B153 之后卡是账本（记需求/领活/记证据），但流程驱动仍是旁路：spec/plan 落盘在
docs、派发走裸 `handoff dispatch`、审核回路与卡无关联，timeline 记不到过程。
工作流引擎（节点=纪律块+执行者+能力开关、裁决路由、附件门）在试点期已配好
`charter` 流 v1，但从未跑过一张真实卡。

## 级别与档位

**L2**。改动为工作流定义（数据）、skill 文本、纪律块文件与流程操作，不动跨子系统
代码契约面；若试点暴露引擎缺陷，修引擎另立卡（跨流迁移或新卡），不并入本卡。

## 核心决定（本轮对话拍板）

### 1. 单条 charter 流，L2 是 L3 的子集（用户模型）

**L3 = contract/breakdown 的头 + N 个 L2 形状的子卡 + integrate 的尾。**一条流承载
三条路径，条件边一期用人工 move 表达（定级判决本就产生于 spec 收尾的人工环节）：

```
待办→spec→contract→breakdown→plan→implement→review→acceptance→integrate→finish→已完成
L2：      spec ──跳──→ plan …… acceptance ──跳→ finish
L3轻：    spec→contract→breakdown→plan …… acceptance ──跳→ finish
L3重：    spec→contract→breakdown ──跳→ integrate（等聚合闸）；子卡各从 plan 起步
```

- L3 重档子卡：同 charter 流，建卡后 move 到 plan 起步（spec/contract 已在父卡做完），
  跑到 review 过即人工 move 到已完成；聚合闸等全部子卡完结后父卡 integrate 派发。
- 条件边引擎化（裁决/产出带 route 字段）押后：真踩坑（常忘跳、常跳错）再做。
  与 triage「先人工定性跑通再谈 AI 辅助」同一先例。
- `domain` 流退役出日常（不删定义，防 seed/测试依赖；标注为 charter 流吸收）。
  `feature`/`bug` 流维持纯人工列，bug 流的节点化（charter-debug 块 + grok）另立卡。

### 2. charter 零耦合，缝合层 = product-backlog skill（用户拍板）

charter 套件保持独立可运作，**不写任何卡操作进 charter 仓**。协调者侧「节点收尾
该做什么卡操作」全部落在 product-backlog skill——它从「backlog 操作手册」长成
**卡驱动驾驶手册**，第二遍改写补入本轮新决定：

- 单条 charter 流取代「领取时迁 bug/feature/domain」的旧口径（B153 那版按旧设计写）；
- 三条路径的跳列表（上图）与各人工节点的卡操作对照：spec 收尾=挂 spec 附件+按定级
  move；plan 产出=plan 附件在卡上；acceptance=`card accept --evidence`；finish=移
  「已完成」；L3 重档 breakdown 拍板后=`card split` 建子卡；
- 派发型节点的触发与盯法：`card dispatch --step <节点>`（任务挂卡、timeline 留痕），
  审核回路仍按 `handoff` skill（wait --follow，不写轮询循环）；
- description 触发词扩展：除记需求/领活外，补「推进卡/卡驱动/走流程/节点派发」。

### 3. 防漏靠门，不靠记忆（硬执法）

skill 是软指引，**gate 是执法者**：漏做卡操作应当被下一步动作结构性拦下。
charter 流 v2 的门配置：

- `plan` 列加 `require_attachment: spec`（L2 跳列的执法点——spec 没挂附件跳不进来）；
- `contract` 要 spec、`breakdown` 要 contract（v1 已有，保留）；
- `implement` 列加 `require_attachment: plan`（plan 产出必须落卡才能开工）；
- acceptance/finish 为人工列不设门，证据缺失由 `card accept` 的语义兜底（未验落 unverified）。

### 4. 节点推进模型：逐节点人工触发，裁决自动路由

维持引擎现状：每个派发节点由协调者触发（`card dispatch --step`），裁决块
pass→Next / fail→OnFail / 3 轮封顶→等人。**触发下一节点前天然是协调者检查点**，
这就是工作台基准 §5「主会话审核」的落点（plan 审核=触发 implement 前看 plan diff）。
自动连跑归 B156.3 自动化层，本卡不做。

## 实现决定

1. **charter 流 v2**（`workflow put`，版本化只增）：§3 的门配置；节点其余配置沿用 v1
   （charter-* 纪律块、codex、3 轮封顶、fail 回退）。
2. **product-backlog skill 第二遍改写**（§2 清单）。
3. **纪律块同源机制**（三部曲之三，就地解决）：`~/.handoff/discipline/charter-*.md`
   是 charter skill 正文的执行器改写版。同源方向单向：charter 仓 → 块文件。机制一期
   从简——charter 仓改动对应节点正文时顺手同步块文件（写进 charter 仓的贡献说明，
   不写进 skill 正文），真发生漂移事故再上脚本/校验。与条件边同哲学：先人工，踩坑再固化。
4. **试点 B171**（执行耗时打点，非自指 L2）：从 triage 领取 → 迁 charter 流 spec 列
   → 全程走到 finish。它是本卡的活体验收。

## 测试决定（接缝清单）

无新增 Go 代码，无自动化测试。**试点即验收**，最高可测缝一个：B171 全程的对标走查——

- spec 列：spec 对话在本地会话完成，附件挂卡后 gate 放行跳列（故意不挂附件 move 一次，
  确认 plan 门拒绝且文案说清缺什么——红绿都要看）；
- plan/implement/review 三个派发节点：`card dispatch --step` 派发、任务挂卡、裁决块
  解析路由正确（至少目击一次 pass 前进；fail 回退若自然出现则记录，不人为制造）；
- acceptance：本地验收（复跑→变异→真机）后 `card accept --evidence` 落证据；
- finish：合并归人、卡移已完成；
- 全程 timeline 完整可追溯，与实际动作零脱节。

## Out of Scope

- 条件边引擎化（route 字段）；节点自动连跑与裁决自动唤醒（B156.3）；
- bug 流节点化（charter-debug 块 + grok 排查节点）；AI 辅助定性（triage 派发节点）；
- domain/feature 流定义的删除；Web 控制台针对 charter 流的专项界面增强；
- 纪律块同源的脚本化校验；charter 仓任何正文改动（零耦合红线）。

## 备注

- 本卡 B174 自身是自指活（改工作台自身），implement 不派发、本地执行；但人工节点
  （spec 挂卡、移列、accept、finish）照走卡机制——它是人工节点缝合的第一次演练，
  B171 才是含派发节点的完整试点。
- 图覆盖债：无（本轮查证走 API/CLI 实测与 workflow show，未涉图查询未命中项）。
