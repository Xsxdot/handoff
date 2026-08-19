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
