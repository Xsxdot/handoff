# B89 W4 控制台形态修复 — 执行 ledger

任务：4356d318-644d-4564-8e6c-c06b2237e9df
分支：feat/b89-console-form-fixes
基线：87b2cd5e（docs(plan): B89 形态修复实现计划（7 个 task））
开工基线测试：373 passed / 40 files；typecheck、lint（10 个既有 warning，0 error）、build 全绿

## 进度

- 2026-08-14 Task 1（行尾计数改图标形态）完成，commit 271e2183，审查 PASS（无修复轮）。实现者偏离记录：ProjectTree.test 的「工单数 0 不显示角标」断言从 queryByText('0') 改为按角标 token 查（RowCounts 渲染 0 后 queryByText('0') 多命中），审查裁决必要且符合语义化纪律。测试 373→376。
- 2026-08-14 Task 2（左栏三段式滚动 + 「项目 N」间距）完成，commit 751d04c3，审查 PASS（无修复轮）。搬入滚动容器部分与原样逐字一致，测试 376→378。
- 2026-08-14 Task 3（注销按钮定位上下文收到机器行）完成，commit f1743328，审查 PASS（无修复轮）。目录行补 data-testid="workspace-row" 作判据；grep 确认 group-hover 仅注销按钮使用，group 随 relative 一起挪安全。测试 378→379。
- 2026-08-14 Task 4（看板弹层固定 70vh + 四列滚动）完成，commit cbe1ca77，审查 PASS（无修复轮）。测试 379→381。
- 2026-08-14 Task 5（项目色稳定哈希取色）完成，commit 634b2d23，审查 PASS（无修复轮）。构建产物 grep 五个 text-project-N 类全在；可观测性走 data-project-color。测试 381→386。
- 2026-08-14 Task 6（文件树配色 + 机器行连接态）完成，commit 7cdd866f，审查 PASS（无修复轮）。实现者偏离记录：FileTree 两用例从 plan 的 props() 写法内联展开并改 findByTestId（文件列举异步，querySelector 渲染前为 null，plan 允许无工厂时内联）；既有「圆点跟随任务状态」断言 .bg-state-active 1→2（本机行连接态 + running 任务）；注释从 plan 的 JSX children 位置移到元素层（原位置 TS1005）。三处偏离审查均裁决可接受。测试 386→389。
- 2026-08-14 Task 7（走查记录与收口）完成，commit e43ff08e，审查 PASS（无修复轮）。八条验收逐条落档，条 3/5/6/7 如实标未验（无浏览器）；有意偏离（机器行保留三段）单开一节；回归原文贴四条命令实际输出 + go diff 空证明。
