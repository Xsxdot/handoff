# 工作台一期：工作项账本 + 看板 + 主会话驱动（spec 骨架）

> 状态：骨架，待与用户逐节敲定后充实为可派发 spec。
> 上游蓝图：[2026-08-18-workbench-blueprint-design.md](2026-08-18-workbench-blueprint-design.md)。

## 1. 目标与非目标

**目标**：把 backlog 从 markdown 总账搬进 agentd store，成为带子任务、阻塞边、
自定义状态、工作流配置的一等实体；给用户一个看板作为注意力平面；给主会话一套
CLI 读写账本、按工作流驱动推进的能力；多路 wait 支撑「全程一个 wait」。

**非目标**（蓝图二期以后）：事件自动触发、群聊、富评论、上下文文档实体化、
蓝图 goal、自动合 main、agentd 自主唤醒协调者。

## 2. 数据模型（agentd store / SQLite）

> 待敲定：表结构细节、ID 形态（沿用 B 号？新前缀？）、与现有 task 表的外键约束。

- `work_items`：id、title、status、priority、project、parent_id（子任务树）、
  workflow_id、spec_ref、plan_ref、创建/更新时间、验收记录。
- `work_item_deps`：blocker_id → blocked_id 有向边；环检测在写入时拒绝。
- `work_item_events`：append-only timeline（状态转移、派发、审阅结论、note）。
- `workflows`：命名工作流；状态集合（骨架锚点 + 插入节点）、转移、派发模板
  （executor、纪律块文本、prompt 模板、目标机、分支策略）。
- `tasks` 加可选列 `work_item_id`。
- 衍生态 `blocked` 查询时计算（存在未终态 blocker），不落库。

## 3. CLI

> 待敲定：命令面拆分与命名；哪些动作要二次确认。

- `handoff item add/list/show/update/close`：账本 CRUD；`list` 支持按状态/项目/
  blocked 过滤（新会话领活前先查账的入口）。
- `handoff item link <blocker> <blocked>` / `unlink`：阻塞边。
- `handoff item note <id> <text>`:「记一笔」。
- `handoff item move <id> <status>`：状态转移（校验工作流合法转移）。
- `handoff item dispatch <id> [--node <节点>]`：按工作流派发模板拼装 prompt +
  纪律块，走现有 dispatch 通道，task 回链 work_item_id。
- `handoff workflow add/show/edit`：工作流配置管理。
- `handoff wait --item <id> [--subtree]`：**多路 wait**，聚合工作项子树下全部
  task 的事件为一条流（现状 `wait <task>` 单任务，需扩展）。

## 4. Web 看板（agentd 托管 Web UI）

> 待敲定：与现有 web-console 页面的关系（新页 or 重构）；一键动作的确认交互。

- 列 = 工作流状态（骨架 + 自定义），卡片 = 工作项；blocked 徽标；多项目过滤。
- 卡片详情：timeline、子任务树、阻塞图、关联 task 列表与跳转。
- 一键动作（人工插手通道）：转移状态、按节点派发——与主会话驱动共用同一节点
  定义。

## 5. 主会话驱动（行为规约，落到 handoff skill 改写）

- 派发前查账（避免重复开工）；派发后挂多路 wait。
- 子任务完成 → 触发审阅节点（派审阅执行者，结构化裁决 pass/fail + 发现项）。
- fail → 自动 continue 带发现项，封顶 3 轮，超限转「等人」。
- pass → 自动合入功能集成分支（冲突转「等人」），item 转 done，查阻塞图派下一个。
- 全部 done → 整功能验收（主会话）→ 父 item 进「待合并」等用户合 main。
- 每个动作落 work_item_events，用户随时可从看板接管。

## 6. 存量迁移

- backlog.md 未完成条目入库为 open 工作项；历史 done 条目归档入库（只读）。
- 迁移前先对齐汇流点分支（web-console）实测 merge-base，确认无分叉遗漏。
- 迁移完成后 backlog.md 冻结（顶部加指针注记）；product-backlog skill、
  CLAUDE.md §4 纪律块同步改写为指针。

## 7. 验收判据（骨架）

> 待敲定：逐条真机判据。方向：
> ① 标准例（1→2→(3∥4)→5）在真机上由主会话全程一个 wait 推完，人工只出现在
> 审 spec / 整功能验收 / 合 main；② 审阅 fail 3 轮封顶转「等人」可复现；
> ③ 看板与 CLI 对同一账本的读写一致；④ 迁移后抽查 N 条历史条目字段无损；
> ⑤ 多路 wait 在子树内任意 task 出事件时唤醒且不漏（对照 journal）。
