# B286 spec 台账

- 2026-08-28 用户「还有最后一批是吧？做完吧」。原始六批 bug 已合完。剩余高优 charter 卡 C7/C8/C11。C7/C8 剩余代码在 handoff，C11 在 charter，拆成 B286 + C11 两张 L2。
- 2026-08-28 origin/main `f8e252ef3`（含 B234）。linux-01 agentd `016aef7e` 2026-08-25。本机 agentd 仍旧。
- 2026-08-28 `handoff template show charter-default` Version=5，Def.target 空。B271 已把空 target 收成本机。
- 2026-08-28 `cmd/card_node.go` `runStepDispatch`：202 后固定「已受理」，退出 0。`TestCardDispatchStepReturnsImmediately` 锁住该 stdout。
- 2026-08-28 `DispatchSnapshot` 已有 `target`/`branch`/`base`/`base_commit`/`discipline_name`。CLI 不读。
- 2026-08-28 `NodeStep.RunOnce` 只在 `Produces != nil` 时 `Diff`。review 节点 produces 空。`Override.Purpose` 可区分 review。
- 2026-08-28 C7 卡 note 已更正原题（v4 并无 target）。剩余是 202 静默 + 基线静默。修法「缺 target 当场拒」与 B271 冲突，弃选。
- 2026-08-28 C8 法条矛盾半已由 B229.7 删除平台层台账句。真越轨样本 C1.5（生产代码）与 C1.11（测试文件）。白名单只留台账目录。
- 2026-08-28 `handoff project ls --target linux-01` 有 handoff 与 charter。B286 派 handoff 仓。
- 2026-08-28 未跑 codegraph CLI（不在 PATH）。
- 2026-08-28 独立审查总判修订后再批（C1 水位/通道；I1–I8）。r1 吸收：Follow 且 POST 前水位；成功行不打本地 ref/origin；fetch 族移出必堵；Diff 失败不落裁决；Name=review purpose 空负例；探活 400；skill 改 441 与 607。批准推进。
