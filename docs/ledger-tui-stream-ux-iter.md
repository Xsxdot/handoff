# TUI 会话流 UX 迭代执行 ledger

任务：c6667921-2089-4190-9173-a39c7cf60e04
分支：feat/tui-stream-ux-iter
基线：f7f53a45
计划：用户消息中的《TUI 会话流 UX 迭代（真机走查五项反馈）Implementation Plan》

## 进度

- 2026-08-17 初始化：Task 1–5 待完成；Task 5 Step 3 真机走查按计划留给审核者执行。
- 2026-08-17 Task 1 完成，commit 范围 `f7f53a45..47f64aa3`：新增连续工具块分组纯函数与 4 个测试；定向测试 4/4、typecheck 通过；spec/质量双裁决通过，无修复轮。
- 2026-08-17 Task 2 第 1 轮完成，commit 范围 `cab53fcb..aca0deae`：ConversationStream 下沉跳转、近顶回翻、工具组折叠、运行中/工单状态提示，TuiTab 接 ref 与 active；task 测试 126/126、typecheck 通过；spec/质量双裁决通过，无修复轮。
- 2026-08-17 Task 3 第 1 轮修复后完成，commit 范围 `c0eb5f24..97548d89`：ToolCard 增加已知工具中文映射、未知名原样透出，并同步既有 bash 展示断言；task 测试 128/128、typecheck 通过；spec/质量双裁决通过。
- 2026-08-17 Task 4 第 1 轮修复后完成，commit 范围 `891fb074..be0ac812`：回合下拉在部分加载时提示「更早的回合会边跳边加载」；task 测试 129/129、typecheck 通过；spec/质量双裁决通过。
- 2026-08-17 Task 5 终审完成，commit 范围 `f08c27fa..f791d3fb`：相对基线完整 diff 复审通过；终审修复回翻回调稳定性与运行中本地时钟，最终全量 57 files/566 tests、typecheck 通过、lint 0 errors/10 既有 warnings；真机走查留给审核者执行，不算 BLOCKED。
- 2026-08-17 返工缺陷 A 完成，commit 范围 `f85d70b0..0ff92dda`：forwardTo 仅透传 X-Handoff-* 响应头并新增跨机回归；RED 原始失败为 `X-Handoff-Frames-Size = ""`，修复后 Forward 测试通过，`gofmt -l .` 无输出。
- 2026-08-17 返工缺陷 B 完成，commit 范围 `df1c7c9f..125ca995`：useFramesStream 增加 sizeUnknown，ConversationStream 如实提示缺失边界并接线 TuiTab；2 files/12 tests、typecheck 通过，旧版 agentd 的静默降级已改为可见状态。
- 2026-08-17 返工缺陷 C 完成，commit 范围 `fdc0b34b..87545633`：groupBlocks 过滤 quiet event 与空 text/thinking，保留有内容块和 unknown 的分隔语义；RED 原始失败为 5 个流单元而非 1 组，修复后 task 测试 17 files/134 tests、typecheck 通过。
