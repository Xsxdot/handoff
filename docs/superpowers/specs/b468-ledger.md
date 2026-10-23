# B468 台账（implement）

只记实际读数与原始输出。spec 即 plan：`b468.md`。

## 偏离与自主决定

1. 测试落在新建的 `web/src/app/accounts/QuotaBits.test.tsx`，而不是既有测试文件——`QuotaBits.tsx` 此前没有自己的测试文件，`StatusBar.test.tsx` 等测的是别的组件；KindIcon 属于 QuotaBits，按「只测当前组件」单开一个文件。属于 spec「实现决定」第 2 条（测试）的实现细节，在 L1 执行者自主范围内。
2. viewBox 用一张单独的 `ICON_VIEWBOX` 表（只登记 codex、grok 为 `0 0 24 24`，缺席即 `0 0 16 16`），`ICON_PATHS` 的形状不变——spec 第 1 条给了「按 kind 给 viewBox」这条路，任务说明允许二选一；选它是因为其余三个 kind 的条目一行不用动。
3. 原型 `prototypes/base/shared/accounts.js` 的 `ICONS` 表除了 codex/grok 两行，在表头注释下加了一行来源说明（与真实页面注释同口径）；其他 kind 原样。

## 图形来源

- `@lobehub/icons-static-svg@1.95.1` 的 `openai.svg`、`grok.svg`（主会话下载并审过，存于会话 scratchpad，未入库）。只取 `<path d>`，原样内联；属性取原文件 `fill="currentColor" fill-rule="evenodd"`，viewBox `0 0 24 24`。
- 内联一致性核对：从源文件抽出的 d 串（sha256 前缀 openai `417519aa39cd`、grok `b1c7535771ab`）在 `QuotaBits.tsx` 与 `accounts.js` 中各精确出现 1 次（`/usr/bin/grep -cF` → `tsx=1 proto=1`，两家都是）。

## 红（2026-10-23 14:25，实现前）

`cd web && npx vitest run src/app/accounts/QuotaBits.test.tsx`：

```
 ❯ src/app/accounts/QuotaBits.test.tsx (3 tests | 2 failed) 15ms
     × codex 用 24×24 官方标志单色路径 13ms
     × grok 用 24×24 官方标志单色路径 1ms
AssertionError: expected '0 0 16 16' to be '0 0 24 24' // Object.is equality
 Test Files  1 failed (1)
      Tests  2 failed | 1 passed (3)
```

断言红，原因是功能缺失（仍是 16×16 自绘线稿），不是拼写错误；opencode-go 那条照预期是绿的。

## 绿（14:26，实现后）

- `npx vitest run src/app/accounts/QuotaBits.test.tsx` → `Test Files  1 passed (1)` / `Tests  3 passed (3)`
- `npx vitest run`（web 全量）→ `Test Files  151 passed (151)` / `Tests  2043 passed (2043)`，退出 0
- `npx tsc -b` → 退出 0，无输出
- `npx eslint . --quiet`（只看 error）→ 退出 0，无输出
- `node --check prototypes/base/shared/accounts.js` → 退出 0

## 改动文件

- `web/src/app/accounts/QuotaBits.tsx`：`ICON_PATHS.codex` / `.grok` 换成官方标志单色路径（`fill="currentColor" fillRule="evenodd"`）；新增 `ICON_VIEWBOX`；`KindIcon` 的 viewBox 按 kind 取；注释写明来源包与版本。
- `prototypes/base/shared/accounts.js`：`ICONS.codex` / `.grok` 同步换成同一路径（viewBox 24×24），加来源注释。
- `web/src/app/accounts/QuotaBits.test.tsx`（新）：codex/grok 的 viewBox 是 24×24、只有一个 path 且 fill=currentColor、没有彩色值；opencode-go 仍是 16×16。

## 未验证

- 浏览器亮/暗截图（spec S1 验收）不在本节点做，交主会话验收。

## review / 验收（主会话，2026-10-23）

- diff 199dbc73..HEAD 逐行读过：只换 `ICON_PATHS.codex/grok` 两条 path（与下载文件 d 串一致）+ `ICON_VIEWBOX` 表；其余三 kind 未动；原型 `accounts.js` 同步。偏离与自主决定三条（新测试文件、viewBox 另表、原型注释）均在 L1 执行者权限内，已核。
- 变异：`ICON_VIEWBOX` 去掉 grok → `× grok 用 24×24 官方标志单色路径`，`Tests 1 failed | 2 passed (3)`；还原 `3 passed (3)`。
- 浏览器（工作树 vite 开发版 :5242，反代在役 agentd，只读）：弹层菜单只剩「管理账号…」（确认是新前端）；/accounts 分组标题 Codex `viewBox 0 0 24 24`、d 以 `M9.205 8.658` 起、fill currentColor → oklch(0.196)；Grok `0 0 24 24`、`M9.27 15.29`；OpenCode Go / Antigravity 仍 `0 0 16 16`；底栏、弹层同；暗色（产品无主题入口，`documentElement.classList.add('dark')` 模拟）图标 fill 随文字变为 oklch(0.804)/oklch(0.708)；控制台无 error。两标志 15px 下可辨。观察：实心标志与线稿图标视觉重量不同（记账，不改）。
- 过程：首次起的 `console-dev` 跑在主检出（旧代码），截到旧图标，作废；临时在主检出 `.claude/launch.json` 加 `console-dev-b464`（--prefix 工作树 web，端口 5242），验收后原样恢复。
