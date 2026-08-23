# B203 实现计划（实况回填 + 修复轮任务单）

> spec：`docs/superpowers/specs/2026-08-23-b203-dispatch-executor-model-override.md`（已批准，L2）
> 分支：`cards/B203-charter` · 基线：`80bb199ab`

## 本文档为什么是「回填」

**plan 节点的执行者越轨了。** 2026-08-23 的 plan 轮（task `32500acc`，codex/gpt-5.6-luna）
没有产出计划文档，而是直接把 B203 实现完并提交（`9277fe58` 实现 + `62309393` 台账）。
裁决块写了 `pass=true`，但本节点的法定产出物**不存在**。

这是已登记的缺陷 B182 的第三次复现（前两次 B175/B176），处置照先例：协调者逐行审 diff、
人工裁决、**回填实况 plan 文档补门**、卡上记越轨。本文档的前半是对已发生实现的对账，
后半是审出的剩余任务——它对下一轮执行者是一份正常的、可独立验收的任务单。

## 已完成部分（协调者独立复核通过）

复核不采信执行者的自述结论，在本机 `/private/tmp/b203w` 检出 `62309393a` 独立跑：

| 门 | 结果 |
|---|---|
| `gofmt -l .` | 无输出 |
| `go vet ./...` | 0 |
| `go build ./...` | 0 |
| `go test ./internal/ledgerstep/ ./internal/ledger/ ./cmd/` | 三个包全绿 |

**变异三连（证明新测试有牙，不是摆设）**：

1. 拿掉 `ViaTemplate` 的成对规则（删 `model = ""`）
   → `TestViaTemplateExecutorModelOverridesAndPairRule/只覆盖执行器清空模板模型` 红、
   `TestRunnerExecutorModelOverridePriorityAndPairRule` 红；
2. 让 `dispatchNode` 的 CLI executor 覆盖失效（`if r.Executor != ""` → `if false`）
   → `TestRunnerExecutorModelOverridePriorityAndPairRule` 红、
   `TestCardDispatchStepExecutorModelFlags` 红；
3. 快照不落 executor/model（删 `Executor: executor, Model: model,`）
   → `TestViaTemplateSnapshotRecordsExecutorModel` 红、`TestCardDispatchExecutorModelFlags` 红。

三条复原后全绿，工作区干净。

**对 spec 的逐条对账**：

| spec 条目 | 实况 |
|---|---|
| CLI 加 `--executor` / `--model` | done（`cmd/card_dispatch.go`） |
| 两条路径都吃（`--step` 与模板） | done（`cmd/card_node.go` 传 StepRunner；模板路径直传 ViaTemplate） |
| `StepRunner` 加 Executor/Model，优先级 CLI > 节点 > 模板 | done（`internal/ledgerstep/runner.go#dispatchNode`） |
| 成对规则 | done，但**边界判错**，见下方 F1 |
| `dispatched` 事件补记 executor/model | done（`internal/ledger/events.go#DispatchSnapshot`，新键不回填老事件） |
| CLI 侧不做校验 | done（一行校验都没加，符合 Out of Scope） |
| 七条测试 | 全部落地，且经上述变异证明有牙 |

## F1（唯一剩余任务）：成对规则的触发条件判错了

### 缺陷

`internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate` 现在是：

```go
if req.ExecutorOverride != "" {
    executor = req.ExecutorOverride
    model = ""          // ← 只要「传了 executor」就清空，不问它是否真的换了执行器
}
```

于是**传一个与模板相同的执行器名，会静默丢掉模板的模型**。协调者本机探针实测
（在 `/private/tmp/b203w` 加一条临时用例后删除）：

```
显式传与模板相同的 executor 后：executor="opencode" model=""（不传 flag 时 model 应是 template-model）
模板模型被清空了——而执行器根本没变
```

`feature-impl` 模板的执行器就是 `opencode`；传 `--executor opencode` 语义上是空操作，
实际却改变了派发结果。

### 为什么这是错的（不是口味问题）

同一个仓库对**同一个问题**已经有过相反的、写在注释里的判决——
`internal/agentd/manager.go#Manager.resolveModel`：

> 边界：显式传 `--executor` 且它恰好等于 `cfg.Executor.Default` 时，**照样套配置模型**
> ——语义与调用方有没有把名字显式写出来无关。

本卡的成对规则要守的是「**换**执行器时别把上一个执行器的模型带过去」，判据应当是
**执行器是否真的发生了变化**，而不是「调用方有没有把名字打出来」。

今天这条不可达（活模板 `charter-default` 没有 `model_by_target`，charter 各节点也不声明
executor），与 spec 里成对规则本身同属潜伏缺陷——**修它的理由和当初选择做成对规则的理由
是同一条**：写进代码就不用记。

### 任务

1. 把触发条件从「`ExecutorOverride` 非空」收紧为「**有效执行器实际发生变化**」：
   即 `req.ExecutorOverride != "" && req.ExecutorOverride != tpl.Def.Executor` 时才清空
   下层模型。注释里点明与 `resolveModel` 的边界注释同源，避免下一个人再判反。
2. **同一条判据也要覆盖节点层**：`runner.go#dispatchNode` 里 CLI 覆盖节点时同理——
   `r.Executor` 与 `node.Override.Executor` 相同时不应清掉 `node.Override.Model`。
3. 补用例，且**先红后绿**：
   - `ExecutorOverride` == 模板执行器、不给 model → model **保持**模板值（今天必红）；
   - `ExecutorOverride` != 模板执行器、不给 model → model 为空（今天已绿，防回归）；
   - CLI `Executor` == `node.Override.Executor`、不给 model → 保持 `node.Override.Model`（今天必红）；
   - CLI `Executor` != `node.Override.Executor`、不给 model → 清空（今天已绿，防回归）。
4. 已有的四条 `TestViaTemplateExecutorModelOverridesAndPairRule` 子用例与
   `TestRunnerExecutorModelOverridePriorityAndPairRule` **一个断言都不许改**——它们是这次
   收紧的防回归网。改动它们等于把网拆了（若确有必要改，必须在报文里逐条说明改了什么、为什么）。

### 验收判据

- `gofmt -l .` 无输出；`go vet ./...` 与 `go build ./...` 退出码 0；
- `go test ./internal/ledgerstep/ ./cmd/` 全绿；
- **变异证明**：把第 1 条的新条件改回 `req.ExecutorOverride != ""`，第 3 条新增的两条用例
  必须变红、其余保持绿；改回后复跑全绿。没跑到这个结果不许写 pass。
- 全仓 `go test ./...` 如出现 `internal/executor/claudecode` 的
  「裁决 socket 路径过长（上限 107）」，那是执行机 DataDir 路径长度的既有环境事实
  （见 B202），**如实记录为环境失败，不要归因本卡，也不要尝试修它**。

## Out of Scope（本轮不做）

- 任何 CLI 侧的执行器/模型校验（spec 已定为永不做）。
- 给模板填 `model_by_target`（那是配置动作，不是代码）。
- 卡级持久执行器绑定（已落 roadmap）。
