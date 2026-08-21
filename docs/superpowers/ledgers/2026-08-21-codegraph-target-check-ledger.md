# codegraph 目标图与契约对照执行记账

职责：记录本分支 Task 1–8 的双裁决、修复轮次、提交范围与亲自执行的验证结果。
边界：Task 9 的用户级配置改动归审核者本机执行，不在本账记录。

- 基线：`5a3a40d5`（实现计划提交），当前分支 `feat/codegraph-target-check`，开工时工作树干净。
- Task 1 / 完成裁决：spec 符合（Target/TargetMeta/TargetDomain/Assignment/Contract 模型、真实 target.json 加载、缺失显式报错、路径/type/引用/预算校验均落地）；代码质量符合（internal/codegraph 不依赖 handoff 内部包，职责边界注释与导出 API 注释齐全，图内路径比较未引入 filepath）。验证：`go test ./internal/codegraph/ -run 'Target|ContractBudget' -count=1` 实际通过；`go test ./internal/codegraph/ -count=1` 实际通过；`gofmt -l .` 实际无输出。Commit 范围：`HEAD^..HEAD`（Task 1 提交）。
- Task 2 / 完成裁决：spec 符合（assignments 精确指派优先于 paths，支持精确路径与 `dir/**` 整段前缀，未命中返回空串）；代码质量符合（路径全程按 `/` 进行字符串比较，方法职责与 target 模型边界清晰，测试覆盖图外与相似前缀）。验证：`go test ./internal/codegraph/ -run TestDomainOf -count=1` 实际通过；`go test ./internal/codegraph/ -count=1` 实际通过；`gofmt -l .` 实际无输出。Commit 范围：`HEAD^..HEAD`（Task 2 提交）。
- Task 3 / 完成裁决：spec 符合（Graph/Diff/View 的 implements wire 字段、基线/增量合并状态、基线与 diff 两侧引用完整性校验、真实 JSON 穿透测试与扫描配方文档均落地）；代码质量符合（implements 与 call 边表分列保持 wire 兼容，非法新增 implements 引用沿既有 Merge 宽容边界跳过，校验报文含语义上下文，internal/codegraph 仍无 handoff 内部依赖）。验证：`go test ./internal/codegraph/ -count=1` 实际通过；`go test ./cmd/ -count=1` 实际通过；`gofmt -l .` 实际无输出。Commit 范围：`HEAD^..HEAD`（Task 3 提交）。
- Task 4 / 完成裁决：spec 符合（跨域 call 入口声明/legacy 预算/无契约方向、implements 反向接口契约、组装点豁免、deleted 跳过、图外与死规则告警、稳定排序均实现）；代码质量符合（Check 纯函数无 I/O/日志，Report 显式初始化空切片与预算 map，路径和边状态判定职责集中）。验证：`go test ./internal/codegraph/ -run 'TestCheck' -count=1` 实际通过；`go test ./internal/codegraph/ -count=1` 实际通过；`gofmt -l .` 实际无输出。Commit 范围：`HEAD^..HEAD`（Task 4 提交）。
