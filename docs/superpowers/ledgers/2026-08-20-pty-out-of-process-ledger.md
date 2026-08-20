# PTY 托管出 agentd 进程实现 ledger

| Task | 轮次 | 结果 | commit 范围 |
|---|---:|---|---|
| Task 1 | 1 | 双裁决通过：已确认 executor 围栏、机器级压力与任务级压力的统计口径；spec 已回写，追加后续排除 ptyhost task | `504f5d12` |
| Task 2 | 1 | 双裁决通过：帧布局、JSON 控制帧、EOF 区分、1 MiB 上限与未知字段/类型兼容测试通过 | `internal/ptyhost/wire/**` |
| Task 3 | 1 | 双裁决通过：会话目录权限、静态元数据原子写读、live/dead/broken 三态扫描及幂等删除测试通过 | `internal/ptyhost/sessdir/**` |
| Task 4 | 1 | 双裁决通过：引擎纯搬家、公共类型/Attachment 壳上移、agentd 过渡接线；ptyhost 测试与两平台构建通过。`go test ./...` 原始失败：`TestDispatchAutoRegisterSurvivesMissingAgentd`、`TestRegisterDegradesWhenLocalAgentdMissing` 等 cmd 测试报 `404 page not found` | `internal/ptyhost/engine/**`, `internal/ptyhost/{types,attachment,supported_*,errors}.go`, `internal/agentd/server.go` |
| Task 5 | 修复 1 | 首次 attach 原始日志为 `终端会话不存在`：hostproc 使用了外部目录 ID 而非 engine.Open 返回的内部 ID；增加 `engineID` 映射后，7 条 hostproc 测试与两平台构建通过 | `internal/ptyhost/hostproc/**`, `cmd/ptyhost.go` |
| Task 5 | 2 | 双裁决通过：先锁后 shell、socket 0600、独立写协程、断连只 detach、kill 单独收摊、shell 退出守屏 24 小时；`go test ./internal/ptyhost/hostproc/ -v` 通过。`go test ./...` 原始失败仍为既有 cmd 测试的 `404 page not found` | `internal/ptyhost/hostproc/**`, `cmd/ptyhost.go` |
| Task 5 | 修复 2 | 删除误纳入提交的 hostproc 测试临时目录与日志产物；其余 Task 5 代码未变 | `internal/ptyhost/hostproc/ph-2000195815/**` |
| Task 6 | 基线 | 计划命令 `go test ./internal/ptyhost/ -run TestClient` 在实现前返回成功但显示 `[no tests to run]`，未形成失败基线 | `internal/ptyhost/` |
| Task 6 | 修复 1 | 首次实现因父包导入 hostproc 形成 import cycle；改为同形私有启动 JSON，保留 hostproc→ptyhost 单向依赖 | `internal/ptyhost/client.go` |
| Task 6 | 修复 2 | 首次客户端 Open 测试构建了 `cmd` 库而非根目录 handoff 主程序，原始错误为 `exec format error`；改为构建仓库根命令并显式 chmod | `internal/ptyhost/client_test.go` |
| Task 6 | 1 | 双裁决通过：Open detached 拉起并超时清理、List 并发 stat、Attach 先版本拦截并配对 backlog、Write 复用订阅、Detach 不发 kill、Close 发 kill；7 条客户端测试、ptyhost 全包测试、两平台构建通过。agentd PTY/反代选定测试通过；完整 agentd 测试仍有既有 `TestMachineUpgradeUnreachableInventsNoRemedy` 的 422 原始失败 | `internal/ptyhost/client*.go`, `internal/agentd/server.go`, `internal/agentd/w3a_testhelpers_test.go` |
| Task 7 | 1 | 双裁决通过：启动只扫目录+试锁，live 登记、dead 删除、broken 留存并 Error；显式 stop 并发 kill 且总预算 2 秒；4 条 reclaim 测试、PTY 相关回归与两平台构建通过。完整 agentd 测试唯一原始失败：`TestMachineUpgradeUnreachableInventsNoRemedy` 返回 422（既有问题） | `internal/agentd/ptyreclaim.go`, `internal/agentd/ptyreclaim_test.go`, `internal/agentd/server.go`, `cmd/agentd.go`, `internal/agentd/w3a_testhelpers_test.go` |
| Task 8 | 基线 | `go test ./internal/proto/ -run TestContractFixtures` 初始通过；新增字段后不带 `-update` 原始失败：`PtySession` 与 `PtySessionsResp` 序列化缺 `"incompatible": false` | `internal/proto/`, `web/src/api/testdata/` |
| Task 8 | 1 | 双裁决通过：Host/List/Get 标记协议错配但保留会话，REST/线格式/fixture 与前端恢复链路传递 incompatible，TerminalTab 不建连不重连并提供重开；proto、ptyhost、agentd 定向测试、两平台构建、web typecheck 与 836 条前端测试通过。`go test ./...` 原始失败行包括：`TestCursorRootFallsBackToCwdWhenHomeUnwritable`、`TestCursorRootErrorNamesBothPaths`、`TestLoadStripUpdateDoesNotBlockOnSaveFailure`、3 个 `TestPermServer...` 路径过长、`TestSyncAuthKeepsTaskCopyWhenWriteFails`；另有 `internal/client` 失败 | `internal/ptyhost/client.go`, `internal/ptyhost/types.go`, `internal/ptyhost/client_test.go`, `internal/agentd/pty_api.go`, `internal/proto/pty.go`, `web/src/api/types.ts`, `web/src/api/testdata/PtySession*.json`, `web/src/app/{homedock,shell,workbench}/**` |
