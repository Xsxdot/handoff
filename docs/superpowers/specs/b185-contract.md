# B185 契约增量：`card dispatch --step` 的 agentd 请求

- 卡：B185
- 上游 spec：`docs/superpowers/specs/2026-08-23-b185-step-runner-in-agentd.md`
- 上游状态：已批准（2026-08-23）
- 本文性质：契约冻结；本轮不改 `cmd/`、`internal/agentd` 业务逻辑、`internal/ledgerstep`、看板 UI 或 `card wait`。

## 1. HTTP 端点与请求类型

端点保持：

```text
POST /api/cards/{id}/step
Content-Type: application/json
```

跨进程请求的唯一类型名冻结为 `internal/proto.CardStepReq`。字段签名为：

```go
type CardStepReq struct {
	Step     string `json:"step"`
	Target   string `json:"target,omitempty"`
	Executor string `json:"executor,omitempty"`
	Model    string `json:"model,omitempty"`
	Extra    string `json:"extra,omitempty"`
	Actor    string `json:"actor"`
}
```

`CardStepReq` 是待实现的新导出类型；上面是本轮冻结的精确 Go 签名，不是当前代码已经存在的声明。当前 handler 仍是 `handleCardStep`（`internal/agentd/ledgerapi.go#handleCardStep`，现状行 398），请求体局部结构目前只有 `Step string json:"step"`（现状行 399-401）。

字段契约：

| 字段 | 必填性 | 零值/缺席语义 |
| --- | --- | --- |
| `step` | 必填，非空节点名 | 缺席或空字符串不构成合法节点请求；节点名仍由卡钉住的工作流查找，不增加 `review\|merge` 白名单。现状查找入口为 `StepRunner.nodeFor`（`internal/ledgerstep/runner.go#StepRunner.nodeFor`，112-129）。 |
| `target` | 可选 | `""` 等同缺席：`StepRunner.Target` 不覆盖节点/模板；现状先取节点覆盖，再由 `ViaTemplate` 回落模板 target（`internal/ledgerstep/runner.go#StepRunner.dispatchNode`，134-137；`internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate`，102-105）。 |
| `executor` | 可选 | `""` 等同缺席：不施加本次调用方的 executor 覆盖，沿用节点/模板有效 executor。非空时是本次一次性覆盖。 |
| `model` | 可选 | `""` 等同缺席，除非同一请求的非空 `executor` 实际改变了下层有效 executor：此时空 model 是成对规则的明确结果，切断下层 model，交给新 executor 的默认模型。若 executor 只是把同名 executor 显式重述，保留节点/模板 model；该现状判据见 `StepRunner.dispatchNode`（`internal/ledgerstep/runner.go#StepRunner.dispatchNode`，140-149）与 `Dispatcher.ViaTemplate`（`internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate`，209-225）。 |
| `extra` | 可选 | `""` 或仅空白等同没有本次补充；不落卡、不影响后续轮次。非空内容进入 prompt 的 `## 本次补充` 段，现状以 `strings.TrimSpace` 判定并写入（`internal/ledgerstep/dispatch.go#buildPrompt`，306-335）。 |
| `actor` | 规范请求必填，且规范化后必须非空 | CLI 必须发送本次运行会话标识；详见第 2 节。为保持今天看板只发 `{step}` 的兼容性，**只有 legacy 看板形状允许原始 JSON 缺席 `actor`**，agentd 在进入执行层前补成 `web:<r.RemoteAddr>`；规范化后的内部请求仍始终有非空 actor。新 CLI、非看板调用方不得依赖该 fallback。 |

覆盖优先级冻结为：CLI 请求覆盖 > 节点覆盖 > 模板值。`executor` 在某一层实际改变有效 executor 而该层 `model` 为空时，不继承更低层 model；这是已有 B203 规则的 wire 搬运，不在 B185 重定义。

成功响应保持现状：HTTP `202 Accepted`，JSON body 为 `{"ok":true}`；现状 `handleCardStep` 的受理分支为 `internal/agentd/ledgerapi.go#handleCardStep`（416-417）。HTTP body 的 JSON 编码由现状 `writeJSON`（`internal/agentd/server.go#writeJSON`，2165-2170）产生。

### 兼容说明

看板现状 `runCardStep(id: string, step: string)`（`web/src/api/ledger.ts#runCardStep`，236-240）继续发送 `{ "step": "..." }`，现有测试也冻结了这个形状（`web/src/api/ledger.test.ts`，65-68）。本卡不要求看板改变调用签名；服务端只把这条 legacy 形状规范化为 `actor = "web:" + r.RemoteAddr`。CLI 不能使用 `web:` fallback，因为同一台机器的并发 CLI 会话必须不同。

## 2. 发起人身份：表示法与落点

### 2.1 表示法

CLI 的 `actor` 值不是人类可读的 `ledgerActor()`，而是现有 `ledgerSession()` 的返回值：

```text
cli:<USER>@<hostname>#<pid>
```

现状来源：`ledgerActor` 的格式是 `cli:<user>@<host>`（`cmd/ledgercli.go#ledgerActor`，53-62），`ledgerSession` 追加当前进程 PID（`cmd/ledgercli.go#ledgerSession`，64-70）。因此两个同用户、同主机但不同 PID 的 CLI 进程仍是两个不同驱动者。`actor` 原始 HTTP 字段必须逐字传递该字符串，不得改成 `ledgerActor()`，不得由 agentd 改写为 `web:<remote>`。

看板 legacy fallback 保持现状格式：

```text
web:<r.RemoteAddr>
```

现状硬编码位置是 `internal/agentd/ledgerapi.go#handleCardStep`（413）；它只服务于未带 actor 的旧看板请求，CLI 请求不走这条分支。

### 2.2 落点

规范化后的 actor 必须同时落在以下两个现有调用参数上：

```go
// internal/agentd/cardstep.go#Server.startCardStep（现状签名）
func (s *Server) startCardStep(cardID, node, actor string) error

// internal/ledgerstep/runner.go#StepRunner（现状字段）
StepRunner{
	Session: actor,
	Dispatcher: &ledgerstep.Dispatcher{Actor: actor},
}
```

现状 `startCardStep` 已将其 `actor` 同时写入 `StepRunner.Session` 与 `Dispatcher.Actor`（`internal/agentd/cardstep.go#Server.startCardStep`，39-49）。B185 实现扩展四个覆盖项时必须保留这一落点，并把 `Target`、`Executor`、`Model`、`Extra` 从规范化请求一一填入同一个 `StepRunner`。

落账链路是：

1. `StepRunner.Run` 用 `Session` 调 `Store.ClaimDriver(cardID, session)`，并在退出时用同一 session 调 `ReleaseCard`（`internal/ledgerstep/runner.go#StepRunner.Run`，82-104）。
2. `Dispatcher.ViaTemplate` 用 `Dispatcher.Actor` 传给 `LinkTask` 和 `RecordDispatch`（`internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate`，266-274）。因此 timeline 的 `actor` 与驱动锁的 `driver_session` 都归属于发起 CLI 会话，而不是 agentd 进程。
3. 不引入 TTL、心跳或 agentd 代持身份；释放仍沿用 `StepRunner.Run` 的 defer 与现有 `ReleaseCard` 语义（`internal/ledger/move.go#Store.ReleaseCard`，161-181）。

## 3. implement 守卫

守卫主语冻结为“本次请求是否要求内联调用方本地文件”，不是节点名：

```text
reject_inline_local_file := request_requires_inline_local_file
```

若为真则拒绝；若为假则不因 `step == "implement"` 拒绝。该判据必须独立于节点名。

本轮 `POST /api/cards/{id}/step` 的精确结论是：`request_requires_inline_local_file == false` 恒成立。

- `StepRunner` 没有 `PlanPath` 字段（`internal/ledgerstep/runner.go#StepRunner`，23-45）。
- `TemplateDispatch.PlanPath` 只在调用方进程 CWD 读取（`internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate`，227-235），并且只属于不带 `--step` 的 CLI 直派路径（`cmd/card_dispatch.go`，163-165 与 189-197）。
- `StepRunner.dispatchNode` 构造 `TemplateDispatch` 时没有 `PlanPath`（`internal/ledgerstep/runner.go#StepRunner.dispatchNode`，160-170）；agentd 的 `startCardStep` 也没有本地 plan 输入（`internal/agentd/cardstep.go#Server.startCardStep`，43-49）。

因此 B185 的 `--step` 请求体不增加 `plan_path` 或其它本地文件字段，`implement` 在没有内联文件时放行；只有未来另行扩展出“内联调用方本地文件”的请求能力并且该值为非空时，才命中拒绝分支。现有按节点名硬拒的错误文案必须删除，不得换成另一种 `implement` 白名单。

## 4. 固定件与同步方式

`CardStepReq` 进入 `internal/proto` 后，按现有固定件机制同步，不能手写或只改前端：

1. 在 `internal/proto/contract_fixture_test.go` 的 cases 中加入 `CardStepReq` 的固定样本，样本同时填非空 `step`、`target`、`executor`、`model`、`extra`、`actor`，让六个键都被锁住；fixture 文件名为 `web/src/api/testdata/CardStepReq.json`。
2. 先运行：

   ```text
   go test ./internal/proto/ -run TestContractFixtures -update
   ```

   这是唯一允许重写 `web/src/api/testdata/*.json` 的方式。该命令由 `internal/proto/contract_fixture_test.go` 顶部注释（1-18）和 `updateFixtures`（32-36）明确规定。

3. 再运行不带 `-update` 的逐字节断言：

   ```text
   go test ./internal/proto/ -run TestContractFixtures -count=1
   ```

   断言实现位于 `internal/proto/contract_fixture_test.go#TestContractFixtures`（52-163），使用 `json.MarshalIndent` 后追加换行，再与 fixture `bytes.Equal` 比较。
4. Web 侧将同一字段形状加入 `web/src/api/ledger.ts` 的类型/contract test；看板现有 `runCardStep(id, step)` 的 legacy `{step}` 调用保持不变，不能为了 fixture 迫使 UI 发送 actor。

## 待实现清单（本轮不落码）

- [ ] 在 `internal/proto` 新增 `CardStepReq`，并让 agentd handler 解码它；对规范请求校验非空 `step`/`actor`，对 legacy `{step}` 只执行 web actor fallback。
- [ ] 扩展 agentd 的 step 装配，把四个一次性覆盖项注入 `StepRunner{Target, Executor, Model, Extra}`，同时保持 `Session` 与 `Dispatcher.Actor` 使用同一个规范化 actor。
- [ ] 将 implement 守卫改为“内联本地文件请求”判据；当前 `--step` 没有 `PlanPath`，所以 `implement` 不因节点名被拒。
- [ ] 按第 4 节生成 `CardStepReq.json` 并更新 Web 契约类型/测试；本轮没有修改 `internal/proto`、`web/src/api` 或 fixture 文件。
- [ ] 不在本卡实现回合状态恢复（B225）、TTL/心跳（B189）、`card wait` 或看板 UI。

## 拍板记录

本轮命中三重闸门的一项决定是：wire 上的 CLI actor 固定采用带 PID 的 `ledgerSession`，而不是现有事件层的人类可读 `ledgerActor` 或匿名 agentd 身份。它跨 HTTP、agentd、`StepRunner`、驱动 CAS 和 timeline，回改会同时动多个子系统；没有本文上下文时后人会自然想把两者统一；被否掉的方案是 `cli:<user>@<host>`（同机并发会碰撞）和 `web:<RemoteAddr>`（CLI 退化为匿名看板）。TTL/心跳不作为替代方案，本卡显式不做。

另一个兼容取舍是：规范请求把 actor 定为必填，但保留旧看板 `{step}` 的原始形状，由 agentd 在服务层补 `web:<RemoteAddr>`；被否掉的是要求本轮同步改看板调用方。该 fallback 不适用于新 CLI，且规范化后内部 actor 仍不得为空。

## 目标图冻结

契约入口已写入 `codegraph/target.json` 并随本契约提交：

- `d_cli → d_controlplane`：`POST /api/cards/{id}/step`；
- `d_web → d_controlplane`：`POST /api/cards/{id}/step`；
- `d_cli → d_contract`、`d_controlplane → d_contract`：`proto.CardStepReq`；
- `d_controlplane → d_ledger`：`ledgerstep.StepRunner`。

本轮只新增文档与目标图，不引入新的 Go 符号，因此不创建 `codegraph/diffs/cards-B185-charter.json`。
