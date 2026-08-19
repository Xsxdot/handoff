# 任务：执行「项目登记干净流程」实现计划

完整实现计划在同目录：

`docs/superpowers/plans/2026-08-12-project-register-clean-flow.md`

**先读那份 plan 全文，再按 Task 1→6 执行。不要只凭本 brief 发挥。**

## 硬性要求

1. **superpowers:subagent-driven-development**：每个 Task 派一个独立 subagent；Task 之间两阶段 review（实现 review + 规格 review）。不要自己一口气糊完六个 Task。
2. **instrumenting-code**：关键节点日志 + 意图注释（文件头/导出方法/边界 why）。
3. **不凑合**：按 plan 决策表扩展后端 `RegisterProject`；前端单页本机优先；复用 `inspectRepoDir` / `persistProject` / 幂等。
4. **TDD**：先红灯测试再实现；每 Task 按 plan 的 checkbox 步骤。
5. **每完成一个 Task 按 plan 提交**；全部完成后进入 waiting_review，报告测试结果。

基线分支：`handoff/w4-shell-calibration` @ 含 plan 的 HEAD。工作在 `--new-worktree` 任务分支上。
