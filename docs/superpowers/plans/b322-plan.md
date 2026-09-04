# B322 实现计划

卡：B322
spec：`docs/superpowers/specs/b322.md`
分支：`fix/B322-restore-pump`

## Task 1 — workspace 孤儿不再收编（缝 1）

文件：`web/src/app/workbench/restore.ts`、`web/src/app/workbench/restore.test.ts`

1. 改既有用例「恢复孤儿不填现有空列，每个工作区 PTY 独立成组」：S2/S3 在名单里时 `adopted===0`、`groups.length===1`、原 LIVE tab 仍在。这是基线红。
2. 加用例：本机 home 孤儿仍进 dock / dockOrphans（既有 home 用例保持绿）。
3. 加用例：剥掉本机死 id 后组数不变（tab 留位、adopted 不含 workspace）。
4. 实现：③ 循环在 `home && machine===''` 之外 `continue`（workspace 与外来 home 都不收）。`adopted` 只计真正收进 dock 的。
5. 恢复成功日志带 `adopted`（已有）即可；注释写清为什么 workspace 不再收。

测试：`npx vitest run src/app/workbench/restore.test.ts`

## Task 2 — TerminalTab spawn 门（缝 2）

文件：`web/src/app/workbench/TerminalTab.tsx`、`web/src/app/workbench/TerminalTab.test.tsx`、`web/src/app/workbench/tabs.ts`

1. `TabContent` terminal 增加可选运行时字段 `spawn?: boolean`。`persist.stripContent` 不写它（与 incompatible 同类）。
2. `spawnTerminalContent(seq, extra)` 返回 `{kind:'terminal', seq, spawn:true, ...extra}`。
3. 红：无 sessionId、无 spawn → `createPtySession` 0 次，出现「重开一个终端」。
4. 红：`spawn` → 建会话、onSession。
5. 红：无 id 点重开 → 建会话。
6. 实现：`shouldSpawn = spawn || forceSpawn`；无 id 且 !shouldSpawn 则 setDead+error，不 create。重开 `setForceSpawn(true)`，effect 依赖含 `shouldSpawn`。
7. 既有「无 id 即建会话」的 TerminalTab 测试改为显式 `spawn`，或改为带 `sessionId` 的连会话夹具（OSC 等）。

测试：`npx vitest run src/app/workbench/TerminalTab.test.ts`

## Task 3 — 用户新建写入 spawn

文件：`useWorkbench.ts`、`WorkbenchPage.tsx`、`Shell.tsx`、`RoomPanel.tsx`、`useHomeDock.ts`、`HomeDock`/`Shell` 的 TerminalTab 传参、对应测试。

所有用户点出来的新终端 content / HomeTab 带 `spawn: true`。`WorkbenchPage`/`Shell` 把 `c.spawn` / `t.spawn` 传给 `TerminalTab`。

`openTerminalWithCommand` 用例的 content 期望含 `spawn: true`。`newTerminal` 断言 `tabs[0].spawn===true`。hydrate/adopt 的 tab 不带 spawn。

测试：`npx vitest run src/app/workbench/useWorkbench.test.ts src/app/homedock/useHomeDock.test.ts src/app/workbench/persist.test.ts`

## Task 4 — 扇出跳过本机回环（缝 3）

文件：`internal/agentd/pty_api.go`、`internal/agentd/fanout_relay_test.go`（或新 `pty_fanout_self_test.go`）

1. 红：`Listen=100.64.0.5:7777`，targets `local=http://127.0.0.1:7777` + 一个远端；`ptySessionsAll` 的 machines 不含 `local`，仍含远端名。
2. 实现：遍历 `pool.Names()` 时 `IsSelfTarget(name)` 则 Info 日志跳过，不占 results 槽、不 append machines。
3. 成功路径：汇总日志的 machines/sessions 计数反映跳过后的集合。

测试：`go test ./internal/agentd -run TestPtyFanoutSkipsSelfTarget`

## Task 5 — 隔离实例 + 无头浏览器（协调者执行，不派发）

临时 datadir 起 agentd（listen 127.0.0.1 高位端口，token 随机，**登记一个指向自身的 local target**），写入含 2 个死 sessionId 终端 tab 的 global 快照 + 1 条活的无引用 workspace 会话（若平台支持 PTY）。vite `AGENTD_URL` 指向该实例。无头浏览器：ticket 登录 → 读组数/建会话次数 → reload → 组数不变、死 tab 未建新会话。脚本与夹具不碰 `~/.handoff/handoff.db`。

## 缺陷族

- 生命周期：剥 id 的 tab 变成可重开的死 tab，不留无主 shell；spawn 中途卸载仍走既有 delete 孤儿路径。
- 静默失败：无 spawn 的缺 id 必须给重开按钮，不许白屏。
- 跨平台：PTY 不支持时新建仍 501（既有）；隔离验收若本机无 PTY 则跳过「活孤儿」步，仍验死 tab 不自建。
- 假绿：缝 1 有「名单里有 workspace 活会话也不增组」反面断言；缝 2 有 spawn/不 spawn 双面。
- 门禁：不新开写路径。

## 接缝覆盖

- 测试→缝：restore.test 进 `buildRestore`；TerminalTab.test 进 `TerminalTab`；Go 测试进 `ptySessionsAll`。
- 缝→测试：三条缝各至少一支。
- 内部锁：Task 3 的 spawn 写入形状，理由见 spec。
