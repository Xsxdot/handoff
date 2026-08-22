# B174 实现计划：日常路径卡驱动

> L2 轻计划，协调者自写自执行（自指活不派发）。spec：
> `docs/superpowers/specs/2026-08-22-b174-card-driven-charter.md`（批准于 08-22）。

## Task 1：charter 流 v2（加门）

- 取 v1 定义，仅改两处：`plan` 列 gate 加 `require_attachment: spec`；`implement` 列
  gate 加 `require_attachment: plan`。其余节点配置一字不动。
- `workflow put charter --file <def>` 发新版本。
- 验收：`workflow show charter` 最新版为 v2 且两门在位；旧卡（B174）仍钉 v1 不受影响。
- 门的拒绝路径（红测）不在本 task 造——留给 B171 试点按 spec 测试节做（真卡真门）。

## Task 2：product-backlog skill 第二遍改写

- 对照 spec §2 清单逐项落：单条 charter 流口径（替换「领取时迁 bug/feature/domain」）；
  三条路径跳列表；人工节点卡操作对照（spec 挂附件+move、acceptance accept、finish 移列、
  L3 重档 split 建子卡）；派发节点触发（`card dispatch --step`）与 `handoff` skill 的
  盯法分界；description 触发词补「推进卡/卡驱动/走流程/节点派发」。
- 验收：通读改后全文，spec §2 五个条目逐项能指到落点；旧口径（三流分派）零残留。

## Task 3：工作台基准文档对齐

- `docs/superpowers/specs/2026-08-21-workbench-workflow-baseline.md` §2 三级梯度表更新：
  L2/L3 轻/L3 重的「工作流」列统一为 `charter` 流（附跳列路径），`domain` 流标注
  「已被 charter 流吸收，退役出日常（定义保留）」。基准自己的规矩：先改本档再改实现——
  本 task 与 Task 1 同一提交落地。
- 验收：基准 §2 与 spec §1 的路径图一致。

## Task 4：试点 B171（活体验收，需用户参与）

- 按 spec 测试节对标清单执行：B171 聊透（charter:spec 对话）→ 挂 spec → triage 已定性
  → 领取 → 迁 charter v2 落 plan 列（门验附件，先做一次无附件 move 的红测）→
  plan/implement/review 三个派发节点 → acceptance（本地复跑→变异→真机 + accept 记证据）
  → finish（合并归人、移已完成）。
- 全程 timeline 走查：与实际动作零脱节。

## 不做

spec Out of Scope 全部条目；引擎代码改动（试点暴露的缺陷另立卡）。
