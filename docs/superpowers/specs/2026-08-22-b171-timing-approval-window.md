# B171：耗时打点——等审批的时间归 other，不计进工具段

> 2026-08-22。B174 卡驱动流程的首个完整试点卡。根因与决定性证据见冻结
> backlog.md B171 行（08-22 需求 A 验收时定案）。

## 问题陈述

三分法耗时统计（模型/工具/other）把权限审批的等待计进了工具段：工具段是
`ToolStart`→`ToolEnd` 的整窗（`internal/executor/turn/timing.go`，现状读数：
ToolStart :101、ToolEnd :136，均无暂停语义），权限门的 `waiting_answer` 恰好夹在
窗内。决定性证据：一次 `git add && git commit` 记 72060ms，同任务真跑活的 `sleep 3`
仅 2882ms——72 秒里绝大部分在等协调者批准。效果是系统性把 other 报小、tool 报大，
且数字自洽不报错（spec §A.3 明确「等审批归 other」，实测违约）。

## 级别与档位

**L2**（单执行器域：turn 包 + 四家 adapter 的接线处；不动 proto wire 契约——
TimingEntry 形状不变，只改时间归属的计算）。若实现中发现必须改 proto，升级
跨流迁移，不静默扩权。

## 方案

`Segmenter` 增加一对通用的**等人窗口**信号（如 `PauseWaiting`/`Resume`）：窗口内
的时间从当前工具段挪出、归 other。四家 adapter（claudecode/codex/grok/opencode）
的权限门各接一处——挂起进 waiting 时打 Pause，收到应答恢复时打 Resume。

- 信号语义做成通用「等人」而非专名「审批」：提问工单等其他等人形态将来接同一缝，
  但**本卡只接权限门**（YAGNI，缝通用、接线最小）。
- nil 安全与既有约定保持：nil 接收者空操作；窗口不闭合（只 Pause 没 Resume，
  回合终止）按回合结束时刻收口，不留负数或悬挂段。
- 弃选：在 adapter 侧各自扣时（四处重复实现同一段算术，漂移温床）；改 proto 增
  「等待段」新类型（消费端全要跟着改，本卡目的只是把归属改对）。

## 测试决定（接缝清单）

最高可测缝一个：`timing_test.go` 表驱动——Pause/Resume 夹在 ToolStart/ToolEnd
之间时工具段时长不含窗口、other 含窗口；窗口不闭合的收口；nil 接收者；连续多次
Pause/Resume。adapter 接线各加一条最小用例（权限挂起→恢复路径触发信号）。

## Out of Scope

- 提问工单等非权限的等人形态接线；历史已落库耗时数据的修正；
- 控制台/报表展示侧任何改动；proto 契约变更。

## 备注

- 图覆盖债：`Segmenter` 图查询未命中（timing 为全量重扫后新代码），待 B173
  重标定后随下轮重扫消化。
- 试点属性：本卡按 B174 spec 测试节走完整卡驱动流程，含领取时的门红测
  （先不挂附件迁 plan 列验证拒绝）。
