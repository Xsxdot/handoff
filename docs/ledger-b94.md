# B94 控制台走查第三轮四条交互缺陷 — 执行 ledger

任务：bef560c4-5e36-456e-9816-b6c80665a1ed
分支：feat/b94-console-interaction-fixes
基线：4f86c41ff（w4-delivery）

## 进度

- 2026-08-15 Task 1（setTabContent 不再抢焦点）由审核者在本地完成并提交 770a476bb（含变异测试证据），不重做。
- 2026-08-15 Task 2（onNew 不再吞 MouseEvent）完成，commit 94ade7c9。审查直接 PASS。已知偏离一条：计划中 why 注释字面在 JSX 属性间，esbuild 拒绝（实测报错），实现者移到 button 元素上方，审查裁决可接受。
- 2026-08-15 Task 3（悬浮入口删中间层）完成，commit 3484718a。审查直接 PASS。已知偏离一条：测试里 `d.activeId = 'a'` 被工厂字面量 null 收窄报 TS2322，改为 `as { activeId: string | null }` 断言绕行，审查裁决可接受。Minor 记账 1 条：测试工厂 over 参数未声明 activeId，多处以 as never 传入，为既有模式非本任务引入。
- 2026-08-15 Task 4（轻量 ContextMenu 组件）完成，commit 49db5866。审查直接 PASS。计划授权内偏离一条：翻转用例因 jsdom getBoundingClientRect 恒返回 0，按计划授权打桩 {width:140,height:40} 并在用例内 restore，审查裁决可接受。
- 2026-08-15 Task 5（机器行改右键菜单）完成，commit 24f3df06。审查 FAIL 1 处（机器行按钮内两条设计意图注释被删，计划要求「既有内容一字不改」）→ 修复轮 1 由原实现者补回两条注释，复审 PASS。已知偏离一条（审查裁决可接受）：测试工厂 `over.onUnregister ?? vi.fn()` 会把显式 undefined 兜底成 mock，致「未传 onUnregister 不弹菜单」用例假绿，改为 `'onUnregister' in over ? over.onUnregister : vi.fn()`。Minor 记账 1 条：新增 data-testid="machine-row" 未被测试引用（测试用 .group.relative[0] 定位），无害可留可删。总回归：452 用例全绿、typecheck 无错、eslint 0 error（10 既有 warning 不变）、vite build 通过。
- 2026-08-15 终审：整分支相对 4f86c41ff 完整 diff（web/ 下 722 行）复核，四条判据全 PASS、全局约束全 PASS（Go 零改动、无新增十六进制、无新增 console/logger、对外签名不变、panelOpen 无残留）。Minor 记账 3 条：M1 机器行右键用例用 .group.relative 未用 data-testid；M2 HomeDock 测试 as never+断言外赋值绕过类型（既有已知，审查已裁决可接受）；M3 ContextMenu 点外部关闭测试用 pointerdown(document.body)（无问题仅记录）。triage：修 M1（测试改用 data-testid 定位），M2/M3 留。修复波 commit da3d5c51，范围复审 PASS。
