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
