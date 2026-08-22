# B186 ptyhost 测试临时目录执行 ledger

任务：d6052258-5177-4509-8c96-dc833e9f78fb  
分支：cards/B186-charter  
基线：408cd912

## 进度

- Task 1 / 修复轮 1：首次补丁替换函数声明后遗留旧函数体，`gofmt -w internal/ptyhost/client_test.go` 实际报错：`internal/ptyhost/client_test.go:317:2: expected declaration, found t`；已删除重复旧函数体，重新格式化通过。修复未改变规格范围。
- Task 1 / 完成裁决：spec PASS——Unix 使用 `os.MkdirTemp("/tmp", "ph-")`，Windows 显式走 `t.TempDir()`，保留短路径与包目录外两项约束及原因注释；quality PASS——只改测试辅助函数与 import，保留清理逻辑，无生产代码改动。修复轮次：1。提交范围：`HEAD^..HEAD`（Task 1）。
- Task 1 / 实际验证：沙箱内首次 `go test ./internal/ptyhost/ -count=1` 失败原文为 `mkdir /tmp/ph-1958940822: read-only file system`（同类错误发生于 10 个用例）；在获准可写 `/tmp` 环境重跑通过：`ok  github.com/Xsxdot/handoff/internal/ptyhost  10.489s`。`go test ./internal/ptyhost/ -run TestClient -count=3` 通过：`ok  github.com/Xsxdot/handoff/internal/ptyhost  2.759s`。
- Task 1 / 静态验证：`go build ./...`、`go vet ./...`、`gofmt -l .` 均无输出。提交前 `git status --porcelain internal/ptyhost/` 实际输出 ` M internal/ptyhost/client_test.go`（预期源改动）；`ls internal/ptyhost/ | grep -c '^pc-'` 实际输出 `0`。
