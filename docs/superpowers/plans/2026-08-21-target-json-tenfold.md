# 按十域清单重写 codegraph/target.json

**目标：** 把 `codegraph/target.json` 的域划分从当前的临时六域，重写为项目实例化清单
拍板过的十域，并让真实数据下的契约闸继续成立。

## 背景（为什么要重写）

`codegraph/target.json` 是「事前基准」：人写的域划分与允许的跨域契约面，
`handoff graph check` 拿扫描出的实际图机械套在它上面对照。

当前仓库里的 target.json 是**六域**，由实现者照着扫描现状描出来的临时版本
（`d_coordination` 一个域塞进了 agentd / cmd / proto / store / codegraph）。
而分域开发协议的项目实例化清单已经**拍板了十域划分**，并附有域类型标注和归属理由。
两者冲突时以实例化清单为准——机器闸门不能执行一套协议文档已经否掉的划分。

## 权威输入（按权威性排序）

1. `docs/superpowers/specs/2026-08-21-handoff-instantiation-checklist.md`
   —— **§2 领域清单与域类型标注是本任务的权威依据**，含十域的主要包、类型（逻辑域/边界域）
   与拍板记录；§2 末尾「与机械底稿的分组差异（拍板记录）」里对 `prochost` / `ptyhost` /
   `ledgerstep` / `config` / `permgate` 的归属有明确裁定，必须遵守。
2. `docs/superpowers/specs/2026-08-21-codegraph-target-check-design.md`
   —— target.json 的格式与语义定义（§4、§5）。
3. `docs/superpowers/specs/2026-08-21-handoff-domain-inventory-draft.md`
   —— 机械底稿（包规模、依赖边、暴露面），是参考数据不是裁定。
4. `internal/codegraph/target.go`（schema 与 `ValidateTarget` 的实际规则）、
   `internal/codegraph/check.go`（对照语义的实际实现）——**代码是最终事实**，
   文档与代码冲突时以代码为准，并在报告里指出冲突。
5. `codegraph/baseline.json` —— 实际图（1240 节点 / 2444 边 / implements 边为 0）。

## 硬约束

- **只改 `codegraph/target.json` 一个文件。** 不许改 `internal/codegraph/*.go`、
  不许改 `cmd/graph_gate_test.go`、不许改任何测试来让闸门变绿。
  如果你认为代码或测试有问题，写进报告，不要动它。
- `paths` 语法**只支持两种形态**：精确文件路径，或 `dir/**`（目录前缀匹配）。
  其他写法 `ValidateTarget` 会报非法。
- 每个域必须有 `type`，取值只能是 `logic` 或 `boundary`，且必须与实例化清单 §2 的标注一致。
- `entries` 本轮**一律留空**（v1 全额挂 legacy 账）。本任务只重写域划分与预算，
  不引入入口级契约声明——那是后续需求的活。
- `legacyBudget` 必须等于**实测值**，不许估、不许取整、不许为了好看留余量。
  预算的语义是「只减不增的棘轮上限」，填大了等于放水。
- 域 id 用 `d_` 前缀的稳定短名；`name` 用实例化清单里的中文域名。

## 需要你判断的地方（这些没有标准答案，要在报告里说明理由）

1. **实例化清单未覆盖的包**：baseline 里存在、但清单 §2 十域表里没点名的包。
   处置原则：按职责归入最贴近的域，**不要新造第十一个域**，也**不要静默留白**。
   每一个这样的包都要在报告里单列「包 → 域 → 理由」。
2. **清单点名了、但当前 baseline 里没有任何节点的包**（例如前端、以及部分后端包）。
   你要决定这些域是否仍然写进 target.json。注意两个事实：
   target.json 是「应该是什么」的事前基准，而 `check` 对匹配不到任何存活文件的 paths 规则
   会报 warn（spec §5 的规则漂移信号）。你的选择和理由都要写进报告。
3. **`assembly` 与 `assignments`**：现有 target.json 里有两条 assignments 例外
   （`internal/agentd/mirror.go`、`reclaim.go`）。十域划分下它们是否还需要例外，
   以及是否需要新增例外（路径规则切不开的文件），由你判断。

## 预算实测方法

预算不能拍脑袋，要从真实数据里读出来。可行路径（不限于此）：

1. 先按十域写好 `domains` / `assignments` / `assembly`，`contracts` 暂时留空或全填 0；
2. 跑一次对照，从输出里读出**实际发生的每一个跨域方向**及其命中数；
3. 把每个实际存在的方向补成一条 contract，`legacyBudget` 填第 2 步读到的实测值；
4. 复跑，直到 `Fails` 为空。

读数手段二选一：

```bash
go test -run TestRepoContractGate -v ./cmd/     # 日志里有 legacy 命中 map 与 warn 条数
go run . graph check                            # 输出结构化 JSON 报告
```

参考：重写前的六域基线，实测是 12 条跨域方向、legacy 命中总计 333、warn 12 条。
十域切得更细，方向数和命中总数都会显著变多——这是正常的，切细了本来就会把
原先「域内」的边暴露成跨域边。**不要因为数字变大就往回退成粗划分。**

## 验收

以下三条全部通过才算完成：

```bash
go run . graph check          # 退出码 0，Fails 为空
go test -run TestRepoContractGate -v ./cmd/     # PASS
go build ./... && gofmt -l .  # 构建通过、无格式问题
```

另外自查：`ValidateTarget` 零 issue（前两条命令已覆盖）；十个域的 `type` 与清单逐一核对无误。

## 产出形态

**一个提交，改动仅限 `codegraph/target.json`**（本任务书文件本身不要改）。

报告写在**提交信息正文**里，用下列固定小标题，便于横向对照：

```
1. 域划分：十域 id/名/类型对照清单，与实例化清单 §2 的一致性说明
2. 清单未覆盖包的归属决定：包 → 域 → 理由（逐条）
3. 无节点域的处置：写了还是没写、为什么
4. assignments / assembly 的判断
5. 预算实测：方向数、命中总数、warn 条数，以及读数用的命令
6. 发现的问题：文档与代码的冲突、数据缺口、你认为下一步该做但本次没做的事
```

第 6 条不是可选项——如果你在过程中什么问题都没发现，明确写「无」，
但先确认你真的核对过而不是没看。
