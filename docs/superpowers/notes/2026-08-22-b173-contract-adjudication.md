# B173 尾部裁决：跨域契约面首次成文

> 2026-08-22 · `charter:contract` 节点产出 · 冻结物 = `codegraph/target.json`（随本提交冻结）
> 前置：调用边门控（charter `graph/v0.2.0`）+ 两轮假边清洗（4748→4524 边）

## 一、这次裁决的对象

`graph check` 的 15 条红：4 条 `new-direction`（2 个唯一方向）+ 11 条 `over-budget`。
裁决**不是调数字**，是回答每个方向「这条依赖该不该存在、该从哪走」。

## 二、拍板：entries 只认门面类型（用户 2026-08-22 裁决）

**判据**（写进 target.json 的语义，此后按它执法）：

| 容器形态 | 判定 | 处置 |
|---|---|---|
| `pkg.Type`（导出类型容器）、`pkg 实体`（类型集合） | **门面**——领域有意对外的能力面 | 进 `entries`，调用不计债 |
| `pkg（包级函数）`（散装能力） | **非门面**——该领域还没长出门面 | 留 `legacyBudget`，锁到当前实际值 |

**被否掉的两个方案**：

1. **全部实际用到的容器都进 entries**（预算全归零）。否掉的理由是用户 08-22 已有的裁决：
   「一次性把方向写进契约等于宣布现状全部合法，会把闸门的意义抽空」。它会让 `d_cli->d_localint`
   的 11 个容器（含 logx/pathenv/toolchain 这类工具包）全部取得永久合法地位。
2. **完全不动 entries，只把预算调到实际值**。改动最小，但契约面词汇表始终是空的——
   图上看不出「这个领域对外的正当入口是什么」，而那正是目标图存在的理由。

**为什么这个切法值得记**（反过来写不会有任何测试变红）：门面与散装的区别只活在
`is_facade()` 那一行判据里，代码里没有任何东西阻止后人把 `config（包级函数）` 写进
entries。一次「顺手让闸门变绿」就能无声推翻它。

## 三、效果

裁决后 `check` 由 15 条红转 **0 fails / 20 warns**，且棘轮生效：

- 识别出 13 个真门面并首次成文：`client.Client`、`ledger.Store`、`ptyhost.Host`、
  `proto.Task`、`proto 实体`、`proto.TaskState`、`config 实体`、`config.Target`、
  `localsync 实体`、`permgate 实体`、`ptyhost 实体`、`targetclient.Pool`、
  `discipline.Resolver`、`client 实体`。
- **多数方向预算下降**（门面从匿名额度移进具名入口）：
  `d_remote->d_contract` 25→0、`d_cli->d_remote` 62→10、`d_controlplane->d_localint` 36→18、
  `d_cli->d_contract` 22→4、`d_controlplane->d_host` 25→16。
- **3 条真实增长的认账上调**（无门面可提，全是包级函数散调）：
  `d_executor->d_host` 4→9（executor 调 prochost）、`d_cli->d_release` 3→8、
  `d_controlplane->d_executor` 2→3。

## 四、两条 new-direction 的裁决：均为正当，补条目

1. **`d_release → d_localint`**（1 条，budget=1，entries 空）
   `internal/selfupdate/managed_windows.go#windowsManaged` → `internal/service#UnitReferences`。
   自我更新前判断「本进程是否被服务管理器托管」；Windows 的 Task Scheduler 不向被拉起的
   进程注入任何标识（见该函数上方注释），只能反过来问服务域「登记的二进制是不是我」。
   callee 容器是 `service（包级函数）`，按判据不进 entries，锁 1 条预算。

2. **`d_release → d_remote`**（3 条，budget=0，entries=`client.Client`）
   `internal/upgrade/remote.go#RemoteOne` → `client.Client` 的 `PullUpdate`/`PushUpdate`/`WaitVersion`。
   远程升级编排必须经远程通信门面；该函数注释明写「只处理远端，本机路径留在 CLI」。

## 五、变异实验（证明闸门有牙齿，非假绿）

| 变异 | 预期 | 实测 |
|---|---|---|
| 给 `d_cli->d_remote` 加 1 条包级函数散调 | 变红 | ✅ `直调 11 条超出预算 10` |
| 加一条全新跨域方向 `d_ledger→d_remote` | 变红 | ✅ `无契约条目` |
| 给已声明门面 `client.Client` 加 10 条调用 | 仍绿 | ✅ 0 fails（门面调用自由） |

## 六、图覆盖债

无。本轮引用的符号锚（`windowsManaged`、`UnitReferences`、`RemoteOne`、`client.Client`）
均由 `graph sym` 命中并复核过 file:line。
