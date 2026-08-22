# B173 调用边门控与假边清洗执行记账

职责：记录本计划各 task 的双裁决、修复轮次、提交范围与亲自执行的验证结果。

- 基线：`232333e6`（计划提交），当前分支 `feat/b173-edgegate`，开工时工作树干净。
- Task 1 / 完成裁决：spec 符合（CheckEdges 实现跨语言与 Go 包粒度 import 门控，端点缺失/同包/无生产 Go 文件保守放行；EdgeIssue JSON 契约、validate 视图新增边门控、absorb 拒绝门控均接线）；代码质量符合（internal/codegraph 仅依赖 stdlib，go/parser 排除 `_test.go`，缓存包 import 集合，不改图数据）。验证：先跑 `go test ./internal/codegraph -run 'TestCheckEdges|TestEdgeIssue'`，原始红错为 `undefined: CheckEdges` 与 `undefined: EdgeIssue`；实现后该命令实际通过；`go build ./...` 实际通过；`go test ./internal/codegraph ./cmd` 实际退出 1，原始失败首行为 `--- FAIL: TestRepoContractGate (0.02s)`，其后为基线既有契约违规；`go test ./cmd -run 'TestGraph' -count=1` 实际通过；`gofmt -l internal/codegraph cmd` 与 `git diff --check` 实际无输出；真实 `go run . graph validate --repo .` 实际退出 1，输出 `nodes=3664`、`edges=4748`、`edgeIssues=106`，原因计数 `no-import=90`、`cross-language=16`。修复轮次：0。Commit 范围：`HEAD^..HEAD`。
