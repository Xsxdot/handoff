# B254：零产出的失败轮不占工作分支指针，卡不再被钉死在执行机上

状态：**已批准**（用户 2026-08-25 问答裁决三选一，选定「WorkBranch 跳零产出轮」）
级别：**L2**（单子系统 ledger/ledgerstep，不动跨子系统契约）
卡：B254

## 问题陈述

一轮派发即便**零产出**（执行器首个回合就失败、分支上一行提交都没有），它的
dispatched 快照仍会被 `WorkBranch` 当成「卡的工作分支在那台机器上」的证据；改派
其他执行机被跨机校验拒绝，且拒绝报文给出的两条建议（push origin / 显式 --base）
对 `card dispatch` 路径**全部无效**。后果：执行机凭据失效、机器下线这类完全外部的
原因，会让卡永久换不了执行机，除非改代码或手改库（红线）。
（2026-08-25 B156.2 contract 轮实测：mac-02 codex refresh token 被吊销 → turn_failed
→ task archived → 改派 linux-01 被拒；push origin 后重派报错逐字不变。）

**现状读数**（本轮工作树，供 plan/review 复核）：

- `internal/ledger/events.go#Store.WorkBranch`（:401 起）：遍历卡的全部 dispatched
  事件，跳过审阅轮（purpose==review），**覆盖式取最后一条** Branch 非空的快照——
  不问该轮 task 终态、不问有无产出。
- `internal/ledgerstep/dispatch.go`（:148-159）：`hasWorkBranch && workInfo.Target != target`
  即拒，报文建议「push origin + 显式 --base」；但该校验**不查 origin**（push 后重派
  报错不变，实测 afea153c1 已在 origin），且 `card dispatch` 的 flag 集里**没有 --base**
  （只有 --discipline-override/--executor/--extra/--model/--plan/--step/--target/--template）。
- 快照本身（DispatchSnapshot）落在派发时刻，天然不含结局信息——零产出的判定必须
  交叉引用该轮 task 的后续状态。

## 方案

**选定：WorkBranch 跳零产出轮。**「占有工作分支指针」的资格从「派过」收紧为
「派过且该轮有产出」。凭据失效这类外部失败不再占坑，改派自然放行。

弃选：

- **「origin 上存在工作分支即放行、执行机自行 fetch」**：与现有报文建议对齐，但改动
  续派语义、要编排执行机侧 fetch，面大；且对「上一轮有产出、但没 push origin」的
  常态路径引入新的静默丢产出风险。落 roadmap 备查。
- **`card dispatch` 补 `--base`**：逃生舱不修根因，每次撞上都要人工判断，且要与
  「卡基线在首派冻结」的语义对齐，复杂度不小于根因修复。

## 用户故事

1. 执行机凭据失效导致一轮零产出失败后，协调者直接 `card dispatch --step <节点>
   --target <另一台机>`，放行，新轮从卡基线起步——因为上一轮本来就什么都没产出，
   没有可丢的东西。
2. 上一轮**有产出**的跨机改派仍被拒（保护产出不静默丢失），且拒绝报文给出的建议
   在 `card dispatch` 路径上真实可走。

## 实现决定

- **资格语义**：一条非审阅 dispatched 快照占有分支指针，当且仅当该轮产出了工作分支
  上的提交。零产出轮 =该轮挂账 task 已达终态失败/归档，且分支上没有该轮产出的提交。
- **判定约束（决策级）**：判定必须在协调者侧的账本数据内完成，不得要求访问执行机
  的 git（那台机器可能正是因为不可达才要改派）。task 终态与提交存在性从账本的哪些
  事实读，归 plan。
- **报文修正**：跨机拒绝报文是用户可见文案（产品决策）——不得再给对 `card dispatch`
  无效的建议；改为陈述真实可走的路（上一轮有产出时如何处置）。
- 审阅轮跳过、正常轮覆盖取最后一条——既有语义一律不变。

## 测试决定（接缝清单）

一条缝：**`internal/ledger/events.go#Store.WorkBranch`**，调用方
`internal/ledgerstep/dispatch.go`（跨机校验）与合并节点（取工作分支）。缝级断言：
零产出失败轮的快照不占指针（其后改派新目标机放行）；有产出轮语义不变（跨机仍拒）；
审阅轮跳过的既有行为回归。dispatch 侧的报文断言挂在同一条缝的调用方测试上，
不另开缝。

## Out of Scope

- 「origin 存在即放行」的续派语义改造（本期不做，roadmap 备查）。
- `card dispatch --base`（不做；根因修复后无需求支撑，需求再现时重走 spec）。
- 派发失败的订阅可见性（同族但另卡：B253）。
- codex 凭据刷新失败本身的处置（外部原因，非本仓范围）。

## 备注

案情原始取证在卡 B254 timeline（seq 3685），关联 B253（同族：派发前置失败的可见性
与处置）。
