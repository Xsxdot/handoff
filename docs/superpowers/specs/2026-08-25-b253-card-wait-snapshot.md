# B253：card wait 建连时先吐卡快照行，瞬时失败不再静默漏掉

状态：**已批准**（用户 2026-08-25 问答裁决三选一，选定「建连时吐卡快照行」）
级别：**L2**（单子系统 ledger/cli，不动跨子系统契约；快照行内容与子树语义有 plan 增量，非 L1）
卡：B253

## 问题陈述

`handoff card dispatch <id> --step <节点>` 返回 202「已受理；进展见 handoff card wait」，
但派发在 agentd 侧受理后立即失败时，失败事件（comment + needs_human）落在协调者挂
`card wait` 建连**之前**。`card wait` 从建连时的账本水位起订、无对账回放，于是订阅进程
活着、stdout 恒为 0 字节、永不唤醒——失败形态与「任务正常长跑中」完全一致，无法区分。
无人值守时表现为静默停摆（2026-08-25 B156.2 contract 首派实测，卡上 seq 3663/3664
的失败事件已落、订阅方永远看不到）。

**现状读数**（本轮工作树，供 plan/review 复核）：

- `cmd/card_wait.go` `runCardWait`：`start, err := st.MaxSeq()` 后直接
  `st.Follow(ctx, members, start, …)`——从当前全库最大 seq 起订，建连前的事件天然不在流里；
  flag 只有 `--subtree` / `--timeout`，无 cursor、无回放。
- 对照物：task 级 `handoff wait <task> --follow`（`cmd/wait.go`）建连前先对账，本机
  cursor 之后有积压时吐**一行** `backlog_summary`（`internal/client/backlog.go:29`
  `BacklogSummaryType`）；线格式钉在 `cmd/wait_backlog_test.go`。
- B239 已部署：入口失败确实上卡（comment actor=node:<节点> + needs_human）。事件不缺，
  缺的是订阅方的可见性。

## 方案

**选定：建连时吐卡快照行。**`card wait` 建连后、进入事件跟随之前，先输出卡的当前状态
快照（一行 JSON），让「建连之前已经发生的事」在订阅的第一行就可见。瞬时失败场景下
快照里直接带着 needs_human，协调者/脚本立刻醒。

弃选：

- **完整 cursor + 事件回放**（对齐 task wait 机制）：卡流是多成员动态子树（`--subtree`
  每轮重算成员集），cursor 语义要处理成员集漂移，成本高；快照已覆盖「漏掉建连前状态」
  这个根因，回放属 YAGNI。若快照实践后仍不够，再从 roadmap 取回。
- **只改 `--step` 202 同步回吐**：只覆盖同步失败；本次实测形态是**受理后异步失败**，
  该方案对根因无效。

## 用户故事

1. 协调者派发后按 skill 指引挂 `card wait`，即便失败发生在挂订阅之前，第一行输出
   就能看到卡已 needs_human，立即处置，不再挂着一条永远等不到东西的订阅。
2. 无人值守脚本用同一行快照判断「有没有积压的未决事项」，不再把静默停摆当正常长跑。

## 实现决定

- 快照行走 stdout，与既有事件行同流：**单行 JSON，靠 `type` 字段与事件行区分**。
  type 命名是产品决策（用户可见线格式），定为 `card_snapshot`。
- 逐成员一行：不带 `--subtree` 时就是一行；`--subtree` 时按建连时刻的成员集每成员一行。
- 快照内容（决策级）：卡号、当前列（status）、有无未决 needs_human 及最近一条的摘要。
  字段的精确形态归 plan。
- 快照只在建连时输出一次；此后跟随、退出、超时语义一律不变（含「子树已全部完成」
  提前退出的现行为）。

## 测试决定（接缝清单）

一条缝：**`card wait` 的 stdout 线格式**（边界型例外：wire 格式入口即缝上符号）——
`cmd/card_wait.go#runCardWait`，调用方是协调者会话与无人值守脚本（消费 stdout 的
逐行 JSON）。缝级断言：建连时先于任何事件行输出 `type=card_snapshot` 行；卡上已有
未决 needs_human 时快照如实体现；快照后事件跟随行为与现状一致。参照
`cmd/wait_backlog_test.go` 的钉线格式手法。

## Out of Scope

- cursor 持久化与事件回放（本期不做、快照不够用时再做；已落 roadmap）。
- `--step` 202 报文同步回吐已知失败（本期不做；价值仅剩锦上添花，已落 roadmap）。
- task 级 `wait --follow` 的任何改动（对照物，不是改动对象）。
- 「每次 --step 后先 card show 再挂订阅」的口传 workaround 落纪律（快照落地后该
  workaround 作废，无需成文）。

## 备注

案情原始取证在卡 B253 timeline（seq 3670）。
