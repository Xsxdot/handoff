# B90 home 基准终端浮窗 — 执行 ledger

任务：c016d734-0ce8-4b5f-84bf-a33530117258
分支：feat/b90-home-floating-terminal
基线：c61b0c5e（docs(plan,spec): B90 实现计划）

## 进度

- 2026-08-14 Task 1（useHomeDock 状态）完成，commit 200c5c4a，审查 1 轮修复后 PASS（文件头/test 注释逐字一致性 2 处）。
- 2026-08-14 Task 2（HomeWindow 浮窗容器）完成，commit 4fb1a248，审查直接 PASS。Minor 记账 3 条：grab 不处理 pointercancel；stopPropagation 注释机理存疑（兄弟节点不冒泡）；收起按钮在可拖 header 内按下会冒泡启动 grab。
- 2026-08-14 Task 3（HomeDock 入口面板）完成，commit 988b843b，审查直接 PASS。面板做成深色（原型是白底）——审查裁决：真实控制台深色体系下的忠实转译，不构成违规，记录备查。Minor 记账 3 条：测试文件多了 4 行惰性注释（非字节级逐字）；面板存活点绿 #3fce6c 与角标绿 #18a86b 不统一；清单 tabLabel 恒显号与 HomeWindow tab 条 seq=1 不显号不一致（spec 测试各自钉死，如实遵从）。
- 2026-08-14 Task 4（接线 Shell、退役 FloatingNewPane）完成，commit 88057397，审查直接 PASS（392−4+2=390 用例，>373）。Minor 记账 3 条（均为沿用中央既有路径，非新增）：busy 中按 Esc 的竞态；confirmClosePty 的 machine 在确认时读 wb.base；setCloseBusy 无 finally。
