# B166 一期执行 ledger

## Task 0：环境自检

- `go version`：`go version go1.26.1 linux/amd64`
- `node -v || echo "NO_NODE"`：`v24.16.0`
- `npm -v || echo "NO_NPM"`：`11.13.0`
- `cd web && ls node_modules >/dev/null 2>&1 && echo "NODE_MODULES_OK" || echo "NODE_MODULES_MISSING"`：`NODE_MODULES_MISSING`
- 随后执行 `cd web && npm ci`：`added 290 packages, and audited 291 packages in 2s`；`found 0 vulnerabilities`
- `cd desktop && go test ./internal/... 2>&1 | tail -5`：

  ```text
  2026/08/19 22:26:39 INFO 已催进程管理器拉起 agentd
  2026/08/19 22:26:39 ERROR 等待被取消 want_version=v0.4.0 attempts=2 cause="context canceled"
  FAIL
  FAIL github.com/Xsxdot/handoff/desktop/internal/shell 0.070s
  ```

  本步失败，原始输出如上；先继续按计划实现，后续收尾复跑并如实记录。

## 进度

- Task 0：已完成，范围无代码提交。

## Task 1：桌面端发布物的名字与校验和

- 失败测试确认：`go test ./internal/release/ -run 'TestDesktopAssetName|TestFetchChecksumFor' -v` 原始编译错误为 `undefined: DesktopAssetName` 与 `FetchChecksumFor undefined`。
- 实现后：上述定向测试通过；`go test ./internal/release/ -v` 全部通过，`TestActivateUnwritableDir` 按既有测试在 root 下 skip。
- 双裁决第 1 轮：spec 符合，新增 `DesktopAssetName` 三平台精确映射，`FetchChecksum` 保持转发兼容，`FetchChecksumFor` 按传入资产名解析且错误/日志带资产名；代码质量通过，gofmt/vet/包测试均通过，无修复轮次。
- 收尾验证：`gofmt -l .` 无输出；`go vet ./...` 无输出；`go test ./internal/release/`：`ok github.com/Xsxdot/handoff/internal/release 0.086s`。
- 提交范围：`internal/release/` 与本 ledger，提交信息按计划为 `feat(release): 桌面端发布物的资产名与按名取校验和`。

## Task 2：agentd 持有薄壳状态

- 失败测试确认：`go test ./internal/agentd/ -run TestDesktopState -v` 原始编译错误为 `env.srv.desktopNow undefined` 与 `undefined: desktopStateTTL`。
- 实现后 focused 测试：`go test ./internal/agentd/ -run TestDesktopState -v`：3 个用例全部 `PASS`，包结果 `ok github.com/Xsxdot/handoff/internal/agentd 0.161s`。
- 变异复验第 1 次（反转过期判据）：`TestDesktopStateExpiresAfterTTL` 实际失败原文：`desktopstate_test.go:47: 过期后得到 200，想要 204`；包结果 `FAIL github.com/Xsxdot/handoff/internal/agentd 0.047s`。已恢复 TTL 判据。
- 双裁决第 1 轮：spec 符合，PUT/GET、内存存储、互斥锁与 30s TTL 均落地，204/200 契约和过期行为有测试；代码质量通过，无修复轮次。
- 收尾验证：`gofmt -l .` 无输出；`go vet ./...` 无输出；`go test ./internal/agentd/`：`ok github.com/Xsxdot/handoff/internal/agentd 99.849s`。
- 额外实际命令 `cd desktop && gofmt -l . && go test ./internal/...` 失败，原始关键报错：`--- FAIL: TestSyncOnOpenOrderIsLoadBearing`；`open /tmp/.handoff-sync-642072607: read-only file system`；`FAIL github.com/Xsxdot/handoff/desktop/internal/shell 0.072s`。
- 提交范围：`internal/proto/desktop.go`、`internal/agentd/desktopstate.go`、`internal/agentd/desktopstate_test.go`、`internal/agentd/server.go` 与本 ledger，提交信息按计划为 `feat(agentd): 中转薄壳状态，带 30s TTL`。

## Task 3：agentd 下载桌面端安装包

- 失败测试确认：`go test ./internal/agentd/ -run TestDownload -v` 原始编译错误为 `downloadFetch undefined`、`downloadOpen undefined`、`downloadPlatform undefined`、`downloadState undefined`。
- 实现后定向验证：`go test ./internal/agentd/ -run 'TestDownload|TestUpdateLatest' -v`：8 个用例全部 `PASS`，最终结果 `ok github.com/Xsxdot/handoff/internal/agentd 0.557s`；覆盖校验失败删除、并发 409、已有文件跳过、唤起失败仍成功、平台拒绝、缓存命中/刷新/失败空结果。
- 变异复验第 1 次（删掉校验失败后的 `os.Remove`）：原始失败为 `updatedownload_test.go:70: 校验失败后文件仍存在，stat err=<nil>`，包结果 `FAIL github.com/Xsxdot/handoff/internal/agentd 0.032s`；已恢复。
- 变异复验第 2 次（删掉并发判断）：用 `-timeout 3s` 实际失败，原始报错含 `panic: test timed out after 3s`、`POST 下载: ... EOF`，包结果 `FAIL github.com/Xsxdot/handoff/internal/agentd 3.013s`；已恢复。
- 双裁决第 1 轮：spec 符合，latest 共用 24h CLICheck 缓存，下载按 DesktopAssetName、按名校验、失败删包、并发锁、平台 opener 与旧包清理均落地；代码质量通过，无修复轮次。
- 收尾验证：`gofmt -l .` 无输出；`go vet ./...` 无输出；`go test ./internal/agentd/`：`ok github.com/Xsxdot/handoff/internal/agentd 104.406s`。
- 提交范围：`internal/agentd/updatedownload.go`、`internal/agentd/updatedownload_test.go`、`internal/proto/desktop.go`、`internal/agentd/server.go` 与本 ledger，提交信息按计划为 `feat(agentd): 下载并校验桌面端安装包，下完唤起文件管理器`。

## Task 4：薄壳上报自身状态

- 根模块 client 定向验证：`go test ./internal/client/ -run TestPutDesktopState -v`：`PASS`，`ok github.com/Xsxdot/handoff/internal/client (cached)`；断言 PUT 方法、路径、Bearer 与 JSON body。
- reporter 定向验证：`cd desktop && go test ./internal/shell/ -run TestReporter -v`：两个用例均 `PASS`，`ok github.com/Xsxdot/handoff/desktop/internal/shell (cached)`。
- 双裁决第 1 轮：spec 符合，client 单向 PUT、10s reporter、失败继续退避、每轮读取快照、main.go 的版本/同步结论组装与失败/阻塞立即上报均落地；代码质量通过，无修复轮次。
- 收尾静态验证：`gofmt -l .` 无输出；`go vet ./...` 无输出。
- 实际全包命令 `go test ./internal/client/` 失败，原始报错：`--- FAIL: TestCursorRootFallsBackToCwdWhenHomeUnwritable`；`根 = ".../001/.handoff/cursors"，want ".../002/.handoff/cursors"（应降级到 cwd）`；`--- FAIL: TestCursorRootErrorNamesBothPaths`；`两处都不可写时必须报错，不得静默`；包结果 `FAIL github.com/Xsxdot/handoff/internal/client 9.101s`。
- 实际允许的 desktop 全包命令 `cd desktop && gofmt -l . && go test ./internal/...` 失败，原始报错：`--- FAIL: TestSyncOnOpenOrderIsLoadBearing`；`一切顺利时不该有错误：创建临时文件: open /tmp/.handoff-sync-2396126359: read-only file system`；包结果 `FAIL github.com/Xsxdot/handoff/desktop/internal/shell 0.075s`。
- main.go 改动未经编译验证，原因：执行机缺 Wails 构建依赖；按 Global Constraints 由 macOS 审核者验证。
- 提交范围：`internal/client/desktop.go`、`internal/client/desktop_test.go`、`desktop/internal/shell/report.go`、`desktop/internal/shell/report_test.go`、`desktop/main.go` 与本 ledger，提交信息按计划为 `feat(desktop): 薄壳单向上报自身状态，10s 一次`。

## Task 5：托盘瘦身与图标，删除死代码

- Step 1 初始 grep 原始输出：

  ```text
  ./panel.go:4://   - 创建并持有一个独立窗口，加载内嵌前端的 /upgrade.html
  ./panel.go:46:// openUpgradePanel 创建并显示升级面板窗口。
  ./panel.go:50:func openUpgradePanel(app *application.App) *upgradePanel {
  ./panel.go:57:        URL:    "/upgrade.html",
  ./main.go:316:        menu.Add(label).OnClick(func(*application.Context) { go showBlockedPanel() })
  ./main.go:319:        menu.Add("上次同步失败，查看详情").OnClick(func(*application.Context) { go showSyncFailurePanel() })
  ./main.go:323:            OnClick(func(*application.Context) { go openReleasePage(trayLatest) })
  ./main.go:325:    menu.Add("升级执行机…").OnClick(func(*application.Context) { go runRemoteUpgrade(false) })
  ./remote_upgrade.go:5://   - runRemoteUpgrade 用它跑 handoff upgrade --now 并把输出流进升级面板
  ./remote_upgrade.go:80:// runRemoteUpgrade 跑 handoff upgrade --now 并把输出流进升级面板。
  ./remote_upgrade.go:88:func runRemoteUpgrade(force bool) {
  ./main.go:514:// showBlockedPanel 打开面板，说明为什么没同步，并提供强制入口。
  ./main.go:519:func showBlockedPanel() {
  ./main.go:520:    p := openUpgradePanel(trayApp)
  ./main.go:535:    p.OnForceRetry(func() { forceSyncNow(p) })
  ./main.go:538:// showSyncFailurePanel 打开面板展示上次同步失败的原因。
  ./main.go:539:func showSyncFailurePanel() {
  ./main.go:540:    p := openUpgradePanel(trayApp)
  ./main.go:550:    p.OnForceRetry(func() { forceSyncNow(p) })
  ./main.go:553:// forceSyncNow 越过闸一立即同步。只由用户在面板上点击触发。
  ./main.go:554:func forceSyncNow(p *upgradePanel) {
  ./main.go:589:// openReleasePage 打开 release 页面让用户自己下载。
  ./main.go:593:func openReleasePage(tag string) {
  ./frontend/vite.config.ts:13:      // 多页入口。缺了这段，upgrade.html 不会被打进 dist，而 go:embed
  ./frontend/vite.config.ts:18:        upgrade: "upgrade.html",
  ```

  输出中的引用全部位于本 task 将删除/修改的调用方。
- 删除后同一 grep 实际无输出。
- `cd desktop && gofmt -l .` 无输出；`go vet ./...` 无输出。
- `cd desktop && go test ./internal/...` 失败，原始报错：`--- FAIL: TestSyncOnOpenOrderIsLoadBearing`；`一切顺利时不该有错误：创建临时文件: open /tmp/.handoff-sync-3837148909: read-only file system`；包结果 `FAIL github.com/Xsxdot/handoff/desktop/internal/shell 0.075s`。
- 首次 `cd desktop/frontend && npm run build` 失败原始报错：`sh: 1: tsc: not found`。随后 `npm ci` 默认缓存失败原始报错：`npm error code EROFS`、`npm error rofs EROFS: read-only file system, open '/root/.npm/_cacache/tmp/***'`；改用任务可写缓存执行 `npm ci --cache /root/.handoff/tasks/dce76326-0f86-4e0a-b56e-b010f4c1a62e/tmp/npm-cache` 成功：`added 17 packages, and audited 18 packages in 2s`、`found 0 vulnerabilities`。
- 最终 `cd desktop/frontend && npm run build` 通过：`vite v8.2.1`、`35 modules transformed`、`dist/index.html 0.48 kB`、`dist/assets/main-QvimaT1l.js 20.92 kB`、`✓ built in 42ms`。
- 双裁决第 1 轮：spec 符合，托盘保留打开控制台/退出两项，macOS 模板图标与其他平台彩色图标均接入，升级面板/远程升级死代码与多页入口已删除；代码质量通过，无修复轮次。main.go 仍未经 Linux Wails 编译验证。
- 提交范围：`desktop/main.go`、`desktop/frontend/vite.config.ts`、`desktop/build/trayicon.png`（仅引用，未修改）及删除的 4 个 desktop 文件与本 ledger，提交信息按计划为 `refactor(desktop): 托盘瘦身到两项并换成标志，删掉升级面板与远程升级`。

## Task 6：前端数据层

- 失败测试确认：`npx vitest run src/app/data/useUpdate.test.ts` 原始失败为 `TypeError: fetchDesktopState is not a function`；`npx vitest run src/app/lib/version.test.ts` 原始失败为 `Failed to resolve import "./version"`。
- 实现后：`npm run typecheck` 通过；`npx vitest run src/app/data/useUpdate.test.ts src/app/lib/version.test.ts`：2 个测试文件、3 个用例全部通过。
- 双裁决第 1 轮：spec 符合，三种线上类型、四个请求函数、10s/1s 轮询 hooks、204 空状态解码和与 Go 侧一致的数字/预发布版本比较均落地；代码质量通过，`parseResponse` 未被全局放宽，无修复轮次。
- 收尾验证：`git diff --check` 无输出；`npm run typecheck` 通过；定向 vitest 结果为 `Test Files 2 passed (2)`、`Tests 3 passed (3)`。
- 提交范围：`web/src/api/types.ts`、`web/src/api/client.ts`、`web/src/app/data/useUpdate.ts` 及测试、`web/src/app/lib/version.ts` 及测试与本 ledger，提交信息按计划为 `feat(web): 更新面的数据层与版本比较`。

## Task 7：右下角提示框

- 形态基准检查：计划引用的 `prototypes/desktop-update/index.html` 在当前仓库中不存在；实际读取并遵循 `docs/superpowers/specs/2026-08-19-desktop-update-surface-design.md` §4、§6.3，完成三种可堆叠提示、右下角让位、下载进度与会话级关闭。
- 失败测试确认：`npx vitest run src/app/update/UpdateToasts.test.tsx` 首次原始失败为 `Failed to resolve import "./UpdateToasts"`。
- 实现后定向验证：`npx vitest run src/app/update/UpdateToasts.test.tsx`：1 个测试文件、5 个用例全部通过。
- 全量验证：`npm run typecheck` 通过；`npm test`：`Test Files 80 passed (80)`、`Tests 793 passed (793)`；`npm run build` 通过，`1943 modules transformed`，产物 `index-DX7Zrx4l.css 47.53 kB`、`index-lgefZoLt.js 892.54 kB`，仅有 Vite 的大 chunk warning。
- 双裁决第 1 轮：spec 符合，非桌面壳不渲染、三种提示条件、sessionStorage 按 `(kind, tag)` 关闭、agentd 下载进度与 home 浮窗 236px 让位均落地；代码质量通过，`git diff --check` 无输出，无修复轮次。
- 提交范围：`web/src/app/update/UpdateToasts.tsx`、测试、`web/src/app/shell/Shell.tsx` 与本 ledger，提交信息按计划为 `feat(web): 控制台右下角的更新提示框`。

## Task 8：设置「更新」页

- 失败测试确认：`npx vitest run src/app/settings/UpdatePage.test.tsx` 原始失败为 `Failed to resolve import "./UpdatePage"`。
- 实现后定向验证：`npm run typecheck` 通过；`npx vitest run src/app/settings/UpdatePage.test.tsx src/app/settings/SettingsPage.test.tsx`：2 个测试文件、8 个用例全部通过。
- 全量验证：`npm run typecheck` 通过；`npm test`：`Test Files 81 passed (81)`、`Tests 796 passed (796)`；`npm run build` 通过，`1944 modules transformed`，产物 `index-xnjDc_rI.css 48.03 kB`、`index-BzwGnVwc.js 898.87 kB`，仅有 Vite 的大 chunk warning。
- 双裁决第 1 轮：spec 符合，更新导航接在 Env 文件之后并带琥珀点，桌面应用/同步状态在 204 时隐藏，执行机块始终只读，本机显示随桌面应用更新，远端只显示可升级状态；重新检查走 `fetchLatest(true)`，无修复轮次。
- 提交范围：`web/src/app/settings/UpdatePage.tsx`、测试、`web/src/app/settings/SettingsPage.tsx` 与本 ledger，提交信息按计划为 `feat(web): 设置页新增更新分区`。

## Task 9：整分支终审

- 分支与提交：`feat/b166-update-surface`；相对起点 `3addd708` 共 9 个实现/修复提交（Task 0 无提交），当前终审修复待提交。
- `gofmt -l .`：无输出；`go vet ./...`：无输出。
- 根模块 `go test ./...`：命令退出码 1。实际汇总输出包含：`ok github.com/Xsxdot/handoff`、`ok github.com/Xsxdot/handoff/cmd`、`ok internal/agentd`；失败原文为 `TestCursorRootFallsBackToCwdWhenHomeUnwritable`、`TestCursorRootErrorNamesBothPaths`（`internal/client`），`TestLoadStripUpdateDoesNotBlockOnSaveFailure`（`internal/config`），`TestPermServerAskThenRespond`、`TestPermServerRespondUnknownID`、`TestPermServerReRegisterSameID`、`TestResumeContinuesFromOffset`（`internal/executor/claudecode`），`TestSyncAuthKeepsTaskCopyWhenWriteFails`（`internal/executor/grok`），最终 `FAIL`。本次改动未触及这些失败测试所在实现文件。
- `cd desktop && gofmt -l . && go test ./internal/...`：gofmt 无输出；`embedbin` 为 `ok`，`TestSyncOnOpenOrderIsLoadBearing` 失败，原文为 `一切顺利时不该有错误：创建临时文件: open /tmp/.handoff-sync-1906946391: read-only file system`，最终 `FAIL github.com/Xsxdot/handoff/desktop/internal/shell`。按 Global Constraints 未运行 desktop 根包 Wails 编译。
- `cd web && npm run typecheck && npm test && npm run build`：全部通过；`Test Files 81 passed (81)`、`Tests 796 passed (796)`；Vite `1944 modules transformed`，产物 `index-xnjDc_rI.css 48.03 kB`、`index-BzwGnVwc.js 898.87 kB`，仅有大 chunk warning。
- 死代码复查命令无输出：`upgrade.html`、`openUpgradePanel`、`runRemoteUpgrade` 均无残留（排除 node_modules 与 docs）。
- spec §6.1 落点：`internal/proto/desktop.go:1-46`、`internal/agentd/desktopstate.go:21-72`、`internal/client/desktop.go:15-31`、`desktop/internal/shell/report.go:17-74`、`desktop/main.go:453-505`；内存 TTL、单向 PUT、10s/30s 关系与失败退避注释均在代码中。
- spec §6.2 落点：`internal/agentd/updatedownload.go:109-273`、`:278-356`；latest 缓存/刷新、平台资产名、sha256 校验删包、并发 409、下载目录清理、平台打开器与进度端点均已覆盖，`reveal` 边界说明在文件头。
- spec §6.3 落点：`web/src/api/client.ts:179-194`、`web/src/app/data/useUpdate.ts:13-24`、`web/src/app/update/UpdateToasts.tsx:59-211`、`web/src/app/shell/Shell.tsx:456-457`；204/null、版本比较、三种提示、sessionStorage、下载状态与 home 236px 让位均已覆盖。
- spec §6.4 落点：`web/src/app/settings/SettingsPage.tsx:28-98`、`web/src/app/settings/UpdatePage.tsx:29-190`；更新导航/琥珀点、无壳降级、桌面应用/同步状态/执行机三块、重新检查与本机只读边界均已覆盖。
- spec §6.6 落点：`desktop/main.go:44-48`、`:216-334`、`desktop/frontend/vite.config.ts:10-16`；托盘图标、空标签、两项菜单、多页入口删除均已覆盖。
- §9 日志与注释复查：agentd/薄壳新路径均使用 slog；下载开始、校验、跳过、打开器、清理均有日志；承重「不复用 reveal」「不做自我替换」「状态只在内存」说明已落在对应文件头。最终范围复审命令 `gofmt -l .`、`git diff --check 3addd708..HEAD`、死代码 grep 均无输出；除已记录环境/平台未验证项外无待修代码发现。
- 未验证项：`desktop/main.go` 与托盘改动未经 Linux Wails 编译验证（缺 GTK/Wails 构建依赖）；macOS/Windows 真机菜单、图标、DMG 挂载与控制台下载走查未做；计划引用的 `prototypes/desktop-update/` 副本在仓库中不存在，未能逐像素对照。
- 终审修复轮：补充 `desktop/main.go:100` 的 `trayLatest` 生命周期注释；修复后复跑静态范围检查与死代码 grep，均无输出。无第二轮修复波。

## 审核修复轮：跨平台下载测试、子进程回收与提示框按钮矩阵

- 修复 `internal/agentd/updatedownload_test.go`：
  `TestDownloadDeletesFileOnChecksumMismatch` 与 `TestDownloadSkipsMatchingExistingFile` 均加入
  `env.srv.downloadPlatform = func() (string, string) { return "linux", "amd64" }`。
  两个测试的预置/断言文件名本来就由 `DesktopAssetName("v0.3.1", "linux", "amd64")` 生成，
  现在 handler 的平台缝也固定为同一值；因此 handler、预置文件和 stat 断言始终指向同一资产，
  与运行平台的 `runtime.GOOS/GOARCH` 无关。
- 变异复验在 Linux（非 macOS）完成：临时删除校验失败分支的 `os.Remove(path)` 后，
  `go test ./internal/agentd/ -run TestDownloadDeletesFileOnChecksumMismatch -v` 原始失败为：
  `updatedownload_test.go:71: 校验失败后文件仍存在，stat err=<nil>`，包结果 `FAIL`；恢复删除逻辑后，
  同一测试与 `TestDownloadSkipsMatchingExistingFile` 均 `PASS`，包结果：
  `ok github.com/Xsxdot/handoff/internal/agentd 0.074s`（缓存复跑为 `ok ... (cached)`）。
- `internal/agentd/updatedownload.go` 的 `openDownloadedFile` 在 `Start` 成功后以 goroutine 调用
  `Wait` 收尸，并注释说明长驻 agentd 若不回收会积累 open/xdg-open 的僵尸子进程；Wait 的退出码
  仍不影响 Windows explorer 的唤起判定。
- 提示框补齐原型按钮矩阵：有新版显示「下载」+「稍后」+「查看详情」，点击「稍后」复用关闭逻辑；
  下载中显示禁用的进度主按钮并隐藏「稍后」；下载完成显示「已下载 <文件名>」及校验通过文案、
  移除主按钮；blocked/failed 仍只有「知道了」，失败下载保留「重试」。新增/更新测试覆盖这些行为。
- 定向验证：
  `go test ./internal/agentd/ -run 'TestDownload|TestUpdateLatest' -v`：8 个测试全部 `PASS`，
  包结果 `ok github.com/Xsxdot/handoff/internal/agentd 0.494s`；
  `npm run typecheck && npx vitest run src/app/update/UpdateToasts.test.tsx`：1 文件、7 用例通过。
- 收尾验证：`gofmt -l .` 无输出；`go vet ./...` 无输出；
  `go test ./internal/agentd/`：`ok github.com/Xsxdot/handoff/internal/agentd (cached)`；
  `cd web && npm run typecheck && npm test && npm run build`：typecheck 通过，`Test Files 81 passed (81)`、
  `Tests 799 passed (799)`，Vite `1944 modules transformed`，产物 `index-CuFMmnUq.js 899.47 kB`，
  仅有大 chunk warning。
- `go test ./...` 退出码 1，实际新增相关包仍为 `ok github.com/Xsxdot/handoff/internal/agentd 98.670s`；
  其余失败与前次终审相同（`internal/client` 游标根目录、`internal/config` 回写失败、
  `internal/executor/claudecode` 长 socket 路径、`internal/executor/grok` 写回失败），均为当前沙箱
  `/tmp` 只读/root 身份造成，未触及其实现。`cd desktop && gofmt -l . && go test ./internal/...` 的
  `gofmt` 无输出，`embedbin` 为 `ok`，`desktop/internal/shell` 仍因
  `open /tmp/.handoff-sync-1998365080: read-only file system` 在 `TestSyncOnOpenOrderIsLoadBearing` 失败。
  按约束未在 Linux 执行 desktop 根包 Wails 编译；macOS 编译由审核者另行验证为退出 0。
- `git diff --check` 无输出；死代码复查 grep 无输出。
- 双裁决第 1 轮：spec 符合（两个测试平台缝一致、opener 收尸、按钮矩阵完整）且代码质量通过；
  无额外修复轮。提交范围：四个下载/提示框实现与测试文件及本 ledger，待提交信息为
  `fix(update): 修正跨平台下载测试与提示框按钮矩阵`。
