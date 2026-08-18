# B142 + B122 Windows 平台层执行 ledger

- 基线：`5ba99f0625700d9c0c7f201147616c88b82df039`，分支 `feat/b142-b122-windows-platform-layer`。
- 规则：每个 task 完成后记录门禁、双裁决结果与对应 commit；Windows 行为仅按计划要求如实记为未验证。
- Task 1 完成：`5d5166634c384e8a858e71c2cb87f1162845c66b`；双裁决通过（XML 四项承重配置、UTF-16LE BOM、XML 转义符合 spec；实现范围与注释边界合格）。门禁 `go build ./...`、`go vet ./...`、`gofmt -l .`（无输出）、`go test ./internal/service/` 均实际退出 0。
- Task 2 完成：`26839b4788386f3c28e9d5f7bea747dc4399a4d2`；双裁决通过（Install/Uninstall/Status/Kind/UnitPath、调用序列、PID 复核、幂等卸载与 New 接线符合 spec；跨宿主 Windows 路径处理仅为跨平台单测所需）。门禁 `go build ./...`、`go vet ./...`、`gofmt -l .`（无输出）、`go test ./internal/service/ ./cmd/` 均实际退出 0。
- Task 3 完成：`4990eaa09e840273205766883df82f54c0feb1d3`；双裁决通过（常量为 `120 * time.Second`，测试钉住两倍 60 秒窗口，注释说明 Windows 机制与代价；无新增执行路径）。门禁 `go build ./...`、`go vet ./...`、`gofmt -l .`（无输出）、`go test ./cmd/` 均实际退出 0。
