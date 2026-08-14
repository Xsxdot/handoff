# B90 home 基准终端浮窗 — 执行 ledger

任务：c016d734-0ce8-4b5f-84bf-a33530117258
分支：feat/b90-home-floating-terminal
基线：c61b0c5e（docs(plan,spec): B90 实现计划）

## 进度

- 2026-08-14 Task 1（useHomeDock 状态）完成，commit 200c5c4a，审查 1 轮修复后 PASS（文件头/test 注释逐字一致性 2 处）。
- 2026-08-14 Task 2（HomeWindow 浮窗容器）完成，commit 4fb1a248，审查直接 PASS。Minor 记账 3 条：grab 不处理 pointercancel；stopPropagation 注释机理存疑（兄弟节点不冒泡）；收起按钮在可拖 header 内按下会冒泡启动 grab。
