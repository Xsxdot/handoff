# 工作台一期：工作项账本 + 看板 + 主会话驱动（spec 骨架 v2）

> 状态：骨架，经业务/领域双审阅修订，待与用户逐节敲定后充实为可派发 spec。
> 上游蓝图：[2026-08-18-workbench-blueprint-design.md](2026-08-18-workbench-blueprint-design.md)
> （账本宿主、双聚合、正交标记、对账规则等设计裁决见蓝图 §3，本文不重复论证）。

## 1. 目标与非目标

**目标**：把 backlog 从 markdown 总账搬进中心账本库，成为带子任务、
阻塞边、自定义状态、工作流/派发模板双聚合的一等实体；事件镜像把跨机 task 事件
汇成账本单流；看板作为不说谎的注意力平面；CLI + 节点执行器支撑主会话按工作流
驱动推进；账本单流多路 wait 支撑「全程一个 wait」。

**非目标**（蓝图二期以后）：事件自动触发、群聊、富评论、上下文文档实体化、
蓝图 goal、自动合 main、executor 写账、agentd 自主唤醒协调者。

## 2. 数据模型（中心账本库 PG/MySQL，单机回退 SQLite）

> 待敲定：具体 DDL 与索引；**中心库选型（PG 还是 MySQL）**——倾向 PG
> （LISTEN/NOTIFY 可做事件推送，多路 wait 免轮询；MySQL 则事件消费走短周期
> 轮询），以用户实际已有的中心库为准。以下为定案的结构性决策：

- **账本表只存在于中心账本库**（或单机回退模式的本机 SQLite）；执行机 agentd
  的 store 永不建这些表，执行机不持有账本库凭据。账本持久化收在 store 接口
  后面，SQL 方言差异（占位符、upsert、通知机制）在该层吸收。
- `work_items`：id（**沿用 B 号连续编**，全部记忆/skill/commit 都用 B 号指代，
  换前缀会断上下文）、title、status、priority（仅展示/排序）、project、
  parent_id、workflow_id + 版本、spec_ref、plan_ref、driver lease（会话标识 +
  心跳时间）、验收记录、时间戳。
- `work_item_relations`（原 deps 泛化）：`from → to` + `type` 枚举
  {blocks / discovered_from（发现自）/ split_from（拆分自）/ relates（关联）}。
  环检测与 blocked 衍生态**只对 blocks 生效**；写入时单事务读全图判定（含
  parent 树与 blocks 边混合成环、祖先-后代间挂边）。全部关系类型双向可查——
  「由此发现→B142」这类考古链是真实总账的高频写法。
- **验收结构化**：work_items 加 `acceptance_criteria`（判据文本）；验收结果落
  事件类型 `acceptance_recorded`（含 `verified_on_real_machine` bool + 证据
  文本）。「已完成(已验)」与「已完成(待真机验)」是真实存在的正交维度，必须
  可表达、可过滤，不压进状态列。
- **终止态带 reason 枚举**：取消 / 废弃 / 搁置（可复活）。真实总账有 4 条
  🗄️ shelved 与 1 条 🧊，迁移需要此语义。
- **spec_ref 归一化**（相对 docs/superpowers/ 的规范路径），支撑「按 spec 聚合」
  的派生批次视图。**spec 不实体化**——批次有两种真实形态：刻意的 epic（父子树
  已覆盖）与平级条目共享一份 spec（如 B4–B9），后者靠 spec_ref 聚合表达；实体化
  会与父工作项、四期 goal 形成三个重叠分组概念，且 B 号与 spec 已现多对多苗头。
  批次公共验收注记挂在聚合视图层（或父 item），此裁决写死防实现自作主张建表。
- `work_item_tasks`：`(work_item_id, target, task_id, 用途)` 弱引用表——账本侧
  指向 task 的唯一通道；task 表只加 opaque `work_item_id` 标签列（不设 FK）。
- `work_item_events`：append-only 单流，独立 seq（沿用 events 表模式）。事件类型：
  状态转移（带 actor + CAS 前值）、派发（含模板版本 + 纪律块 hash 快照）、审阅
  裁决、合并记录、`acceptance_recorded`、**comment**（原 note 升级：body +
  引用 item id 列表——写入时自动落 relations 关联边；`kind` 子类 {普通/更正}
  承接「变更痕迹」文化；附件字段预留空数组，二期填）、**镜像 task 事件**
  （保留来源 target 与原 seq）。
- `workflows`：状态机形状，**不可变版本化**（edit 产生新版本，item 钉版本，
  旧 item 显式迁移）。**默认 feature 工作流出厂自带「已出 spec」插入状态**——
  对齐用户真实生命周期 💡→📋→🔨→✅ 的 📋 关口（人审 spec 是三个人工位之一），
  顺便示范插入机制。
- `dispatch_templates`：带版本；executor 类型、纪律块（引用 + hash）、prompt
  模板、目标机、分支策略、**per-target 模型覆盖**。
- 正交标记不落列：`blocked`（全部 blocker 达「已完成」才解除；blocker 终止 →
  下游打等人）与 `等人`（带 reason 枚举）均从边表 + 事件流推导，查询时计算。

## 3. 事件镜像（协调机 agentd 新子系统）

- 镜像者（任一协调机 agentd，由**账本库 lease 仲裁单实例**）订阅各 target 的
  `/ws/events`，把挂账 task 的事件写入 `work_item_events`；断线用既有 cursor
  续拉语义补齐；写入按（来源 target, task, 原 seq）幂等，**镜像 cursor 落
  账本库**——任意协调机接任镜像者后接续，不丢不重。
- 镜像滞后/断链落显式状态，看板卡片标「事件流滞后」。
- 工单（permission_request/question）随镜像入流，是「等人」显性化的数据源。

> 待敲定：镜像订阅的生命周期管理（挂账即订阅 or 常订全量过滤）、lease 时长与
> 抢占语义、镜像者切换/重启后的补拉窗口。

## 4. CLI

> 待敲定：命令面最终命名与 flag；哪些动作要二次确认。

- `handoff item add/list/show/update/close`：账本 CRUD；`list` 支持按状态/项目/
  blocked/等人 过滤（新会话领活前查账的入口）。骨架终态叫「已完成」，CLI 动作
  用 `close`，避免与 `handoff done` 撞名。
- `handoff item link/unlink`：阻塞边（写入即环检测）。
- `handoff item note <id> <text>`：记一笔。
- `handoff item move <id> <status>`：CAS 状态转移（带前值校验，冲突干净失败）。
- `handoff item dispatch <id> [--node <节点>]`：按模板拼装 prompt + 纪律块，走
  现有 dispatch 通道；**派发即认领**（待办→进行中 的 CAS 就是 claim，第二个
  会话干净失败并提示「已被 X 认领」）；task 回链 + 模板版本快照落事件。
- `handoff workflow ...` / `handoff template ...`：双聚合分开管理。
- `handoff wait --item <id> [--subtree]`：**账本单流多路 wait**。订阅 item 子树
  事件流（含镜像 task 事件），wait 挂起期间新派发的 task 天然进流；退出条件 =
  子树全部 item 达骨架终态；progress 类事件不唤醒（沿用现有过滤语义）。
- `handoff item export`：最薄 markdown 只读快照导出（逃生门）。
- **executor 白名单不扩**：新增 item/workflow/template 命令均不进 B115 自指令
  白名单，executor 永不写账。

## 5. 节点执行器（落码，不留 prose）

一期新增的唯一「编排」构件，主会话/看板按钮共用（三期规则引擎复用）：

- 输入：item + 节点定义（模板引用）；动作：派发审阅/合并 task、解析结构化裁决、
  落账、决定下一步。
- **裁决 schema 与通道**（待敲定细节）：审阅 task 以约定格式返回 pass/fail +
  发现项；解析失败不猜，打「等人」（reason=裁决解析失败）。
- **回合计数**：按 item × 节点粒度，从 work_item_events 推导（不存内存）；
  默认封顶 3 轮，超限打「等人」；人工插手（用户手动 continue/改裁决）是否重置
  计数：**重置**（人工介入视为新基线），落事件注明。
- 合并节点：客观判据先行（测试、gofmt），LLM 裁决 pass 仅为必要条件；只合
  集成分支；冲突打「等人」，冲突文件清单 + 双方 commit 范围落 timeline；合并
  顺序按 done 时序。
- 审阅 task 的生命周期由执行器收口（裁决落账后自动 `done` 归档），不留孤儿。

## 6. Web 看板（任一协调机 agentd 托管，读同一账本库）

> 待敲定：与 web-console 现有页面的关系（新页 or 重构）；一键动作确认交互。

**信息优先级原则（设计约束，用户反馈的直接教训）**：界面主角是知识流
（spec 批次、验收状态、引用关系、评论），**lease/镜像/CAS 类保真信号默认沉默、
异常才显形**（驱动正常不显示，只亮「无驱动会话」；镜像收敛为健康小点，断链才
展开告警）。

- 看板/**列表双视图**：看板列 = 工作流状态（骨架 + 插入）；列表复刻 markdown
  总账列（ID/标题/状态/验收/优先级/Spec/备注）+「含归档」过滤——领活与考古
  入口。「等人」横贯顶部高亮条；blocked 徽标；多项目过滤；「未挂账」收为一行
  摘要点开展开（异常态不常驻占位）。
- 卡片主信息：优先级、**spec 徽标（点开批次视图：同 spec_ref 全部条目 + 批次
  公共验收注记）**、已完成态的「已验/待真机验」徽标；异常徽标（等人/状态冲突/
  blocked/工单）。**实时 join 关联 task 状态**，账面与实况矛盾亮「状态冲突」。
- 详情抽屉：状态流水线、**验收区（判据 + 已验开关 + 证据摘要）**、**关系区
  （阻塞/发现自/拆分自/关联，双向）**、子任务树 rollup（父状态独立驱动）、
  关联 task 跳转、**分层 timeline**（评论=气泡主视觉，系统事件=浅色 meta 行，
  镜像 task 事件折叠成组，全部/评论/裁决/系统过滤）、评论框（`#B142` 引用自动
  成关系边，双向可见）。
- 一键动作（人工插手通道）：转移状态、按节点派发——调用与主会话同一节点执行器。
- **流程管理页**（独立页，不塞 settings）：工作流 / 派发模板两个 tab，各自
  版本列表 +「N 个 item 钉在 vX」+ 显式迁移动作；模板详情含 per-target 模型
  覆盖、纪律块正文与 hash、版本取证（哪次派发用了哪版）。

## 7. 主会话驱动（行为规约，落到 handoff skill 改写）

- 唤醒后先 `item show` 从账本 + 事件流重建现场，不信会话记忆。
- 派发前查账防重复开工；派发即认领；挂账本单流多路 wait。
- 子任务完成 → 推「待审阅」→ 调节点执行器（审阅→裁决→continue 或合并→已完成）
  → 查阻塞图派下一个。
- 全部完成 → 整功能验收（主会话亲自）→ 父 item 进「待合并」等用户合 main。
- 验收后发现 bug：开新 item 挂关联，不 reopen。
- 出问题（等人标记、状态冲突）：协调、裁决、或转人工，全部动作落事件。

## 8. 存量迁移（按序执行）

1. 迁移前对齐汇流点分支（web-console）实测 merge-base，确认无分叉遗漏。
2. backlog.md 未完成条目入库为 open item，历史 done 条目归档入库（只读）；
   **B 号→item id 映射表落库**，历史考古接得上。状态映射表：💡→待办、
   📋→已出 spec、🔨→进行中、✅done(已验)→已完成+已验、done(待真机验)→
   已完成+待验、🗄️/🧊→终止(搁置·可复活)；「验收」列→acceptance_criteria+
   验收事件；「变更痕迹/备注」→首条 comment；「见 B17/由 B115 发现」类引用
   尽力解析为 relations（解析不了的保留在 comment 原文里）。
3. backlog.md 顶部加冻结注记；**全局 skill（~/.claude 下 product-backlog）与
   CLAUDE.md §4 同一批次切换为指针**——先切 skill 再冻结文件，避免其他在途
   worktree 的旧 skill 副本继续追加。
4. 抽查 N 条历史条目字段无损 + 映射表可反查。

## 9. 验收判据（骨架）

> 待敲定：逐条真机判据。方向：
> ① 标准例（1→2→(3∥4)→5）真机上主会话全程一个 wait 推完，人工只出现在审 spec /
> 整功能验收 / 合 main；② 审阅 fail 3 轮封顶转「等人」可复现，回合数从事件流
> 可审计；③ blocker 终止不解锁下游、下游得「等人」标记可复现；④ 杀掉主会话，
> 看板在 task 判 failed 后亮「状态冲突」而非报假账；⑤ 多路 wait 在 wait 挂起
> 期间新派发的 task 事件不漏（对照账本库单流 seq）；⑥ 两个会话并发 dispatch
> 同一 item，恰一个成功；⑦ 镜像断链 → 看板亮「事件流滞后」，恢复后按来源 seq
> 幂等补齐；⑧ 迁移后抽查字段无损、B 号映射可反查；⑨ 看板与 CLI 对同一账本
> 读写一致；⑩ 双协调机指向同一账本库：A 机认领派发，B 机看板实时可见并可接续
> 驱动，镜像 lease 从 A 切到 B 后事件不丢不重。
