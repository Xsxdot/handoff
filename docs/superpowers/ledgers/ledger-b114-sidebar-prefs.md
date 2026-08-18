# B114 左栏偏好与手工工作树执行记录

| Task | 状态 | Commit 范围 | 自动化验证实得输出 |
|---|---|---|---|
| 1 | 完成 | 待提交：proto 契约、fixture、TS 类型、ledger | `go test ./internal/proto/ -run TestContractFixtures -update`：`ok github.com/Xsxdot/handoff/internal/proto 0.628s`；`go test ./internal/proto/`：`ok github.com/Xsxdot/handoff/internal/proto 0.215s`；`cd web && npx vitest run src/api/contract.test.ts && npx tsc -b`：`1` 个文件、`22` 个测试通过；`gofmt -l . && go test ./internal/...`：exit 0，`gofmt -l .` 无输出。首次前端运行因缺失依赖失败，随后 `npm ci` 成功安装 287 packages；首次沙箱运行因 `/var/folders` EPERM 失败，提升权限后通过。 |
