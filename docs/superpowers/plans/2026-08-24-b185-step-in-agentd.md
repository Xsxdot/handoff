# B185 实现计划：环节执行体入驻 agentd

日期：2026-08-24
卡：B185
节点：`charter:plan`
实现顺序：`U0 → (U1, U2) → U3 → U4`
本计划只覆盖把 `card dispatch --step` 的执行宿主搬到本机 agentd；不实现 B225 回合恢复、B189 TTL/心跳、`card wait`、看板 UI，也不修改冻结契约文档。

## 0. 冻结依据、裁决和基线证据

输入只认以下仓内文件：

- `docs/superpowers/specs/2026-08-23-b185-step-runner-in-agentd.md`：已批准；范围是 CLI `--step` 秒回和 agentd 唯一执行宿主。
- `docs/superpowers/specs/b185-contract.md`：契约冻结；唯一 wire 类型为 `internal/proto.CardStepReq`，成功响应仍为 HTTP 202 与 `{"ok":true}`。
- `docs/superpowers/specs/b185-breakdown.md`：按 U0→(U1,U2)→U3→U4 执行。

已裁决事项直接落入本计划：

- P1：在既有 `internal/client.Client` 增加 step 方法；`cmd` 不复制 HTTP、Bearer 或错误体处理。
- P2：不扩展冻结响应；202 后 CLI 输出卡号+节点名，并提示 `handoff card wait <卡>`，不输出 task id。
- P3：JSON 使用宽松解码，未知字段忽略，不调用 `DisallowUnknownFields`。守卫是具名纯函数 `requiresInlineLocalFile(req proto.CardStepReq) bool`，今天恒返回 `false`，因为冻结的 `CardStepReq` 没有本地文件字段，`PlanPath` 只属于不带 `--step` 的直派路径。测试直接覆盖该函数的字段组合，并覆盖 `step == "implement"` 不因节点名被拒。

代码图优先查询已执行：

- `go run . graph sym StepRunner --repo . --stale` 命中 `internal/ledgerstep/runner.go:22`；图结果漏列当前 `Executor`/`Model` 字段。
- `go run . graph sym Client.Dispatch --repo . --stale` 命中 `internal/client/client.go:746`，签名为 `func (c *Client) Dispatch(ctx context.Context, opts DispatchOpts) (*proto.Task, error)`。
- `go run . graph sym handleCardStep --repo . --stale` 命中 `internal/agentd/ledgerapi.go:389`；`go run . graph sym runStepDispatch --repo . --stale` 命中 `cmd/card_node.go:17`。
- `who-calls` 图能确认 CLI `card dispatch → cardDispatchCmd.RunE → runStepDispatch`，但 handler/StepRunner 查询带原始告警 `基线仍有 7 个未扫描入口：查询结果为空不等于没有调用方`；因此本计划用定点 `rg`/源码行复核调用面，不把图的空边当成无调用方。
- `go run . graph resolve --repo . --doc docs/superpowers/specs/b185-breakdown.md` 已实际 exit 0，11 个锚点均为 `ok` 或既有 `moved`。

基线命令已在未改实现的工作树实际执行，原始结果记在 `docs/superpowers/ledgers/2026-08-24-b185-contract-ledger.md`：

- `go test ./internal/proto/ -run TestContractFixtures -count=1`：`ok github.com/Xsxdot/handoff/internal/proto 0.047s`。
- `go test ./internal/agentd/ -run 'TestCardStep(Returns202|SecondReturns409|RejectsImplement|AcceptsNodeName)$' -count=1`：`ok .../internal/agentd 1.065s`；这是旧行为基线。
- B203 四条 runner/dispatch 回归：`ok .../internal/ledgerstep 1.302s`。
- 旧 CLI `TestCardDispatchStepExecutorModelFlags` 与 `TestCardDispatchStepExtraReachesPrompt`：分别 `ok .../cmd 0.274s`、`ok .../cmd 0.238s`；它们验证的是旧同步 runner 接缝，U3 必须迁移其 step 断言。
- `go test ./internal/client -run 'TestCardStep' -count=1`：`ok .../internal/client 0.005s [no tests to run]`；因此 U1 的完成判据明确要求命中新增测试，不能接受 `[no tests to run]`。
- Web 两条基线命令均未通过：`(cd web && npm run test -- src/api/contract.test.ts src/api/ledger.test.ts)` 原始报错 `sh: 1: vitest: not found`；`(cd web && npm run typecheck)` 原始报错 `sh: 1: tsc: not found`。实现者必须把命令实际跑到 0 才能写绿，不能把缺少可执行文件算作通过。
- `go test ./...` 基线输出在 `cmd` 包结尾实际出现 `FAIL`、`FAIL github.com/Xsxdot/handoff/cmd ...`，工具未报告 exit；该结果只作为未通过基线事实，不预判原因。U4 必须重新实际跑完整命令并记录原始结果。
- `git diff --check` 基线无输出、exit 0。

现状签名/调用面还以源码为准：`internal/client/client.go:396-459` 是 `Client.do`/`httpError`；`internal/proto/contract_fixture_test.go:32-58,140-163` 是唯一 fixture 更新与逐字节断言；`internal/agentd/server.go:2165-2170` 是 `writeJSON`；`internal/ledgerstep/runner.go:23-45,131-180` 是 StepRunner 字段与既有覆盖投影；`internal/agentd/cardstep.go:39-115` 是异步装配、槽位和 target client 派发；`cmd/root.go:273-287` 是忽略 `--target` 的 `LocalEndpoint`。这些库/项目行为都已在本工作树定点读取，不凭记忆推断。

## 1. 跨卡接口和序列化边界

### U0 produces / U1 consumes / U2 consumes / U3 produces

唯一跨进程请求类型，完整签名固定如下；不得在 `cmd` 或 `agentd` 另造 wire DTO：

```go
// internal/proto/cardstep.go
package proto

// CardStepReq 是 POST /api/cards/{id}/step 的一次性请求。
//
// 参数/字段：Step 是卡钉工作流中的节点名；Target、Executor、Model、Extra
// 是本次环节的一次性 CLI 覆盖；Actor 是发起会话标识。Target/Executor/Model/Extra
// 为空时保持缺席语义，Actor 在规范请求中必须非空；旧看板缺席 Actor 时由 agentd
// 补 web:<r.RemoteAddr>。
//
// 注意：本类型没有 PlanPath 或任何本地文件字段；PlanPath 不得经 --step wire 传递。
type CardStepReq struct {
	Step     string `json:"step"`
	Target   string `json:"target,omitempty"`
	Executor string `json:"executor,omitempty"`
	Model    string `json:"model,omitempty"`
	Extra    string `json:"extra,omitempty"`
	Actor    string `json:"actor"`
}
```

每个手写投影必须有对应断言：

1. `cmd/card_node.go`：flags → `proto.CardStepReq`；`Actor` 必须来自 `ledgerSession()`。
2. `internal/client/client.go`：`CardStepReq` → `POST /api/cards/{id}/step` JSON；复用 `Client.do`。
3. `internal/agentd/ledgerapi.go`：JSON → `CardStepReq`；先保留 raw key presence，区分 actor 缺席与 `actor:""`，未知字段忽略。
4. `internal/agentd/cardstep.go`：规范请求 → 同一个 `StepRunner` 的 `Session`、`Target`、`Executor`、`Model`、`Extra` 与 `Dispatcher.Actor`。
5. `internal/ledgerstep/runner.go`/`dispatch.go`：继续使用现有 `StepRunner.dispatchNode` → `TemplateDispatch` → `DispatchOpts` 优先级与成对规则；B185 不复制规则。
6. `internal/proto/contract_fixture_test.go` → `web/src/api/testdata/CardStepReq.json` → `web/src/api/contract.test.ts`：真实 Go marshal、fixture、TS 类型镜像。

可空/零值判据：U1 用 `map[string]json.RawMessage` 的 key presence 断言可选字段空值时缺键；U2 用 raw actor key presence 区分缺席 fallback 和显式空值拒绝；fixture 样本六字段均非空，只锁键名和非零形状，不伪造缺席语义。

## 2. U0：`CardStepReq` 与 Go↔Web 固定件

### 2.1 文件、依赖和接口

文件范围（只触及这些文件）：

- 新增 `internal/proto/cardstep.go`：职责是定义纯协议类型；不做 I/O、校验或业务逻辑。
- 修改 `internal/proto/contract_fixture_test.go`：复用 `TestContractFixtures`，不得手写 fixture 写入逻辑。
- 新增 `web/src/api/testdata/CardStepReq.json`：只由 `-update` 生成。
- 修改 `web/src/api/ledger.ts`：增加 TS 请求镜像；`runCardStep(id: string, step: string)` 保持发送 legacy `{ step }`。
- 修改 `web/src/api/contract.test.ts`：import 新 fixture 并承接 `CardStepReq` 类型。
- 保持 `web/src/api/ledger.test.ts` 现有 `{ step: "收尾合并" }` 精确断言，不改看板调用签名。

Consumes：现有 `TestContractFixtures(t *testing.T)` 与 Web contract test 的 JSON import。
Produces：

```go
type proto.CardStepReq struct {
	Step string `json:"step"`
	Target string `json:"target,omitempty"`
	Executor string `json:"executor,omitempty"`
	Model string `json:"model,omitempty"`
	Extra string `json:"extra,omitempty"`
	Actor string `json:"actor"`
}
```

```ts
export interface CardStepReq {
  step: string
  target?: string
  executor?: string
  model?: string
  extra?: string
  actor: string
}
```

### 2.2 2–5 分钟动作序列

1. **基线判据先跑（已跑）**：实现者在开始 U0 前重跑并记录 `go test ./internal/proto/ -run TestContractFixtures -count=1`、`(cd web && npm run test -- src/api/contract.test.ts src/api/ledger.test.ts)`、`(cd web && npm run typecheck)`。预期分别是 Go exit 0、Web test exit 0、Web typecheck exit 0；当前 Web 基线原始错误是 `vitest: not found`/`tsc: not found`，不得写 pass。
2. **先写失败断言**：在 `internal/proto/contract_fixture_test.go` 的 `cases` 中加入完整样本，并在 `web/src/api/contract.test.ts` 加入 fixture import、类型承接和六键断言；此时先不创建 Go 类型。完整新增样本代码为：

   ```go
   {"CardStepReq", CardStepReq{
	   Step: "review", Target: "linux-01", Executor: "codex",
	   Model: "gpt-5", Extra: "只检查本轮改动", Actor: "cli:alice@linux-01#1234",
   }},
   ```

   ```ts
   import cardStepReqFixture from './testdata/CardStepReq.json'
   import type { CardStepReq } from './ledger'

   it('CardStepReq：六字段可由 Go fixture 解析', () => {
     const req: CardStepReq = cardStepReqFixture
     expect(req).toEqual({
       step: 'review', target: 'linux-01', executor: 'codex', model: 'gpt-5',
       extra: '只检查本轮改动', actor: 'cli:alice@linux-01#1234',
     })
     expect(Object.keys(req)).toEqual(['step', 'target', 'executor', 'model', 'extra', 'actor'])
   })
   ```

3. **跑红**：执行 `go test ./internal/proto/ -run TestContractFixtures -count=1`；预期必须因为 `undefined: CardStepReq` 或 fixture 不存在而失败，记录原始输出。Web 命令若仍为 `vitest: not found`，记录原文，不把环境错误冒充测试红。
4. **最小实现类型和 TS 镜像**：新增 `internal/proto/cardstep.go`，内容必须与 §1 完整代码块一致；在 `web/src/api/ledger.ts` 写入 §2.1 完整 `CardStepReq` 接口。新导出类型的字段注释、边界和 `PlanPath` 禁止事项必须保留。
5. **生成固定件**：先 review Go 类型与样本六键，再执行唯一允许的写入命令：

   ```text
   go test ./internal/proto/ -run TestContractFixtures -update
   ```

   预期 exit 0，新增 `web/src/api/testdata/CardStepReq.json`，内容逐字节应为：

   ```json
   {
     "step": "review",
     "target": "linux-01",
     "executor": "codex",
     "model": "gpt-5",
     "extra": "只检查本轮改动",
     "actor": "cli:alice@linux-01#1234"
   }
   ```

   随即执行 `git diff --name-only -- web/src/api/testdata`；预期只有 `web/src/api/testdata/CardStepReq.json`，不得接受其它 fixture 漂移。
6. **跑绿**：执行 `go test ./internal/proto/ -run TestContractFixtures -count=1`，再执行 `(cd web && npm run test -- src/api/contract.test.ts src/api/ledger.test.ts)` 与 `(cd web && npm run typecheck)`。三条命令都必须真实 exit 0；`web/src/api/ledger.test.ts` 的 legacy `{step}` 精确断言必须仍在。
7. **提交 U0**：先执行 `gofmt` 不适用纯 Markdown/TS；执行 `git diff --check`；再 `git add internal/proto/cardstep.go internal/proto/contract_fixture_test.go web/src/api/ledger.ts web/src/api/ledger.test.ts web/src/api/contract.test.ts web/src/api/testdata/CardStepReq.json`，提交信息固定为 `feat(b185): add card step wire type`。提交原始结果追加台账。

### 2.3 日志、注释、测试范围和验收

- U0 是纯类型与固定件，没有入口、外部调用或错误分支，不新增 logger；不能用 `fmt.Printf` 代替任何运行日志。新文件头写“纯协议类型、无 I/O/业务逻辑”的职责与边界，导出类型写字段语义和 `PlanPath` 边界。
- 最小测试范围：`internal/proto` 的 `TestContractFixtures`；Web 只跑 `src/api/contract.test.ts`、`src/api/ledger.test.ts` 和 `typecheck`，不跑 Go 全量。
- 完成判据：Go 固定件、Web contract 镜像和 legacy body 三者均真实绿；fixture 只新增 `CardStepReq.json`；`git diff --check` 真实 exit 0。

## 3. U1：既有 `internal/client.Client` 承载 step 请求

### 3.1 文件、依赖和接口

文件范围：

- 修改 `internal/client/client.go`：在现有 `Client.Dispatch` 附近新增 `CardStep`；复用 `do`、`httpError`、`ErrUnreachable`，不另造 HTTP client、Transport、Bearer 或错误类型。
- 修改 `internal/client/client_test.go`：复用当前文件已有 `httptest` 测试写法和 `newTestClientEnv` 约定；若测试必须用只接收原始 HTTP 的 `httptest.NewServer`，仍只在测试中起 server，不增加产品 seam。

Consumes：

```go
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error)
func (c *Client) httpError(op string, resp *http.Response) error
type proto.CardStepReq struct {
	Step string `json:"step"`
	Target string `json:"target,omitempty"`
	Executor string `json:"executor,omitempty"`
	Model string `json:"model,omitempty"`
	Extra string `json:"extra,omitempty"`
	Actor string `json:"actor"`
}
```

Produces：

```go
// CardStep 向 agentd 提交一个卡节点，成功只表示服务端已受理。
//
// 参数：ctx 控制本次短 HTTP 请求；cardID 是 URL 中的卡号；req 是带节点名、一次性
// 覆盖项和发起会话 actor 的规范请求。调用方不得把 --plan 文件塞进 req。
//
// 返回：agentd 返回 HTTP 202 且 JSON body 为 {"ok":true} 时返回 nil；非 2xx 保留
// agentd 的状态码和错误体；连接失败保留 ErrUnreachable；响应 JSON 非法或 ok=false
// 返回错误。nil 不代表回合已完成，进展由 card wait/事件流读取。
func (c *Client) CardStep(ctx context.Context, cardID string, req proto.CardStepReq) error
```

### 3.2 2–5 分钟动作序列

1. **基线判据先跑（已跑）**：执行 `go test ./internal/client -run 'TestCardStep' -count=1`。当前原始结果是 `ok ... [no tests to run]`；U1 不能以此为绿，完成时必须命中新测试。
2. **先写失败测试**：在 `internal/client/client_test.go` 新增 `TestCardStep` 族。测试 handler 必须真实读取 HTTP request，不能直接调用待测 helper。逐条断言：

   ```text
   TestCardStepSerializesAllFields：POST /api/cards/B185/step；Content-Type=application/json；Authorization=Bearer test-token；JSON 六键和值逐字匹配，actor 保持 cli:alice@linux-01#1234。
   TestCardStepOmitsEmptyOptionalFields：req={Step:"review",Actor:"cli:alice@linux-01#1234"}；map[string]json.RawMessage 中 key 只有 step、actor；target/executor/model/extra 缺席而不是显式空串。
   TestCardStepAccepts202：响应 202 + {"ok":true}，方法返回 nil。
   TestCardStepPreservesHTTPError：响应 400 或 409 + 可识别 body，error 同时包含状态码和 body。
   TestCardStepRejectsBadAck：响应 202 但 body 非法 JSON 或 {"ok":false}，方法返回非 nil。
   TestCardStepSurfacesUnreachable：关闭 httptest server 后调用，error 可由 errors.Is 识别为 client.ErrUnreachable。
   ```

   这是复用既有 httptest harness 的测试例外：断言逐条列全，形态照抄 `internal/client/client_test.go` 的 `TestDispatchErrorBodyNotTruncated`/`TestDispatchSerializesCardDefaultBaseMarker`，不新建生产接缝。
3. **跑红**：执行 `go test ./internal/client -run 'TestCardStep' -count=1`；预期先出现 `Client` 没有 `CardStep` 的编译失败，记录原始输出。
4. **最小实现**：在 `internal/client/client.go` 加入以下完整方法；路径使用既有路径拼接口径，状态码使用 `http.StatusAccepted`，不要改冻结响应。

   ```go
   // CardStep 向 agentd 提交一个卡节点，成功只表示服务端已受理。
   //
   // 参数：ctx 控制本次短 HTTP 请求；cardID 是 URL 中的卡号；req 是带节点名、一次性
   // 覆盖项和发起会话 actor 的规范请求。调用方不得把 --plan 文件塞进 req。
   //
   // 返回：HTTP 202 且 body 为 {"ok":true} 时返回 nil；其它状态保留原始错误体；
   // nil 不表示回合完成。
   func (c *Client) CardStep(ctx context.Context, cardID string, req proto.CardStepReq) error {
		logger := c.log().With("card", cardID, "step", req.Step, "actor", req.Actor)
		logger.Info("提交卡节点")
		resp, err := c.do(ctx, http.MethodPost, "/api/cards/"+cardID+"/step", req)
		if err != nil {
			logger.Warn("提交卡节点请求失败", "cause", err)
			return fmt.Errorf("card step 请求: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			err := c.httpError("card step", resp)
			logger.Warn("卡节点未受理", "status", resp.StatusCode, "cause", err)
			return err
		}
		var ack struct {
			OK bool `json:"ok"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
			logger.Warn("卡节点受理响应解码失败", "cause", err)
			return fmt.Errorf("解析 card step 响应: %w", err)
		}
		if !ack.OK {
			err := fmt.Errorf("card step 响应未确认受理")
			logger.Warn("卡节点受理响应为 ok=false", "cause", err)
			return err
		}
		logger.Info("卡节点已受理")
		return nil
	}
	```

5. **跑绿**：执行 `gofmt -w internal/client/client.go internal/client/client_test.go`，再跑 `go test ./internal/client -run 'TestCardStep' -count=1`。预期命中至少一条测试且输出 `ok`，不得出现 `[no tests to run]`。随后复跑 `go test ./internal/client -run TestDispatchErrorBodyNotTruncated -count=1`，确认既有错误体上限没有被新方法改坏。
6. **提交 U1**：`git diff --check`；`git add internal/client/client.go internal/client/client_test.go`；提交信息固定为 `feat(b185): add client card step request`；把提交原始输出追加台账。

### 3.3 日志、注释、测试范围和验收

- 入口日志带 `card/step/actor`；`do` 前后由 `CardStep` 记录；连接错误、非 202、响应 JSON 解码失败、`ok=false` 每条都带上下文；成功受理也打 Info。使用现有 `slog`，不使用 `fmt.Printf` 打日志。
- `CardStep` 导出方法必须有参数、返回和“202 仅受理”的注意事项注释。
- 最小测试范围：只跑 `internal/client` 的 `TestCardStep` 与一个既有 `Dispatch` 错误体安全网；不跑 agentd/CLI 全包。
- 完成判据：真实 HTTP 穿过 `Client.do`；六字段、缺席/零值、Bearer、路径、202/400/409/连接失败均有可判定断言。

## 4. U2：agentd 解码、actor 归一、守卫和异步装配

### 4.1 文件、依赖和接口

文件范围：

- 修改 `internal/agentd/ledgerapi.go`：`handleCardStep` 宽松解码、actor fallback、step/actor 校验、inline guard、错误日志和 202/409 翻译。
- 修改 `internal/agentd/cardstep.go`：改 `startCardStep` 入参，填充同一个 `StepRunner` 的全部字段；增加具名纯函数和 stepTransport 外部调用日志。
- 修改 `internal/agentd/ledgerapi_test.go`：迁移旧 implement 反断言，补规范 actor、legacy 缺席/显式空、未知字段和 request fields。
- 修改 `internal/agentd/cardstep_test.go`：保留槽位/释放测试，补纯函数组合测试与 runner 捕获测试。

Consumes：

```go
type proto.CardStepReq struct {
	Step string `json:"step"`
	Target string `json:"target,omitempty"`
	Executor string `json:"executor,omitempty"`
	Model string `json:"model,omitempty"`
	Extra string `json:"extra,omitempty"`
	Actor string `json:"actor"`
}
```

Produces：

```go
func (s *Server) startCardStep(cardID string, req proto.CardStepReq) error
func requiresInlineLocalFile(req proto.CardStepReq) bool
```

`startCardStep` 成功只表示后台 goroutine 已装配并启动；它不返回回合终态。`Session` 和 `Dispatcher.Actor` 必须接收同一个 `req.Actor`，四个覆盖项必须接收同一个 `req` 的值。

### 4.2 P3 解码与守卫的完整实现形状

默认 `encoding/json` 解码即可；明确不调用 `DisallowUnknownFields`。用 `map[string]json.RawMessage` 先保留 actor key presence，再把同一 JSON 解到冻结类型；未知字段自然丢弃，不能因新 CLI 的可选字段打到旧 agentd 而 400。

`internal/agentd/ledgerapi.go#handleCardStep` 的完整替换形状如下，错误分支必须保留字段上下文：

```go
// handleCardStep 受理一个卡节点，受理即 202；202 不代表回合已完成。
//
// 规范 CLI 请求必须带非空 actor；旧看板只发送 {"step":...}，仅在原始 JSON
// 缺少 actor 键时补 web:<r.RemoteAddr>。显式 actor:"" 不能借 fallback 进入驱动锁。
// JSON 使用宽松解码：未知字段忽略，避免版本错配把新可选字段变成 400。
func (s *Server) handleCardStep(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		s.log.Warn("卡节点请求解码失败", "card", id, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		s.log.Warn("卡节点请求重新编码失败", "card", id, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	var req proto.CardStepReq
	if err := json.Unmarshal(payload, &req); err != nil {
		s.log.Warn("卡节点请求字段解码失败", "card", id, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if _, ok := raw["actor"]; !ok {
		req.Actor = "web:" + r.RemoteAddr
		s.log.Info("legacy 卡节点请求补 actor", "card", id, "node", req.Step,
			"actor", req.Actor, "remote_addr", r.RemoteAddr)
	}
	if req.Step == "" {
		s.log.Warn("卡节点请求被拒：step 为空", "card", id)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "step 不能为空"})
		return
	}
	if req.Actor == "" {
		s.log.Warn("卡节点请求被拒：actor 为空", "card", id, "node", req.Step)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "actor 不能为空"})
		return
	}
	if requiresInlineLocalFile(req) {
		s.log.Warn("卡节点请求被拒：要求内联本地文件", "card", id, "node", req.Step,
			"actor", req.Actor)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "card step 不接受调用方本地文件",
		})
		return
	}
	s.log.Info("开始装配卡节点", "card", id, "node", req.Step, "actor", req.Actor,
			"target", req.Target, "executor", req.Executor, "model", req.Model,
			"has_extra", strings.TrimSpace(req.Extra) != "")
	err = s.startCardStep(id, req)
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	case errors.Is(err, errStepInFlight):
		s.log.Warn("节点被拒：已有在飞", "card", id, "node", req.Step,
			"actor", req.Actor, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ledger.ErrNotFound):
		s.log.Warn("卡节点所属卡不存在", "card", id, "node", req.Step, "cause", err)
		ledgerErr(w, err)
	default:
		s.log.Warn("节点被拒", "card", id, "node", req.Step, "actor", req.Actor, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}
```

`internal/agentd/cardstep.go` 中的纯守卫和装配替换形状如下：

```go
// requiresInlineLocalFile 判断一次 step 请求是否要求把调用方 CWD 的本地文件
// 内联发送给 agentd。
//
// 今天恒为 false 是有意的：CardStepReq 没有 PlanPath 或本地文件字段，StepRunner
// 也没有 PlanPath；PlanPath 只由不带 --step 的 CLI TemplateDispatch 在调用方 CWD 读取。
// 如果未来增加本地文件字段，必须先改冻结契约并把拒绝测试落在同一条 wire 上。
func requiresInlineLocalFile(req proto.CardStepReq) bool {
	return false
}

// startCardStep 装配并异步启动一个卡节点。
//
// 参数：cardID 是卡号；req 是已完成 actor 归一和字段校验的规范请求。
// 返回：同卡已有在飞节点时返回 errStepInFlight；其它前置校验错误原样返回；成功
// 只表示 goroutine 已启动，不表示节点完成。后台结束时必须释放既有卡槽位。
func (s *Server) startCardStep(cardID string, req proto.CardStepReq) error {
	if !s.claimCardStep(cardID) {
		return fmt.Errorf("%w: %s 的 %s 节点正在运行", errStepInFlight, cardID, req.Step)
	}
	runner := &ledgerstep.StepRunner{
		St: s.ledger, Session: req.Actor,
		Dispatcher: &ledgerstep.Dispatcher{
			St: s.ledger, Transport: s.stepTransport, Actor: req.Actor,
		},
		Clients: s.pool.For,
		Target: req.Target, Executor: req.Executor, Model: req.Model, Extra: req.Extra,
	}
	s.log.Info("卡节点装配完成", "card", cardID, "node", req.Step,
		"actor", req.Actor, "target", req.Target, "executor", req.Executor,
		"model", req.Model, "has_extra", strings.TrimSpace(req.Extra) != "")
	go func() {
		defer s.releaseCardStep(cardID)
		s.runStepFn(context.Background(), runner, cardID, req.Step)
	}()
	return nil
}
```

`stepTransport` 保留既有 `targetclient.Pool` 和 `Client.Dispatch`，只补关键节点日志；外部调用前后必须等价于：

```go
func (s *Server) stepTransport(ctx context.Context, opts ledgerstep.DispatchOpts) (string, error) {
	s.log.Info("agentd 节点派发请求", "target", opts.Target, "executor", opts.Executor,
		"model", opts.Model, "prompt_bytes", len(opts.Prompt))
	cl, err := s.pool.For(opts.Target)
	if err != nil {
		s.log.Warn("取得节点派发客户端失败", "target", opts.Target, "cause", err)
		return "", err
	}
	task, err := cl.Dispatch(ctx, client.DispatchOpts{
		Prompt: opts.Prompt, Target: opts.Target, NewBranch: opts.Branch,
		Branch: opts.ExistingBranch, ProjectName: opts.Project, Executor: opts.Executor,
		Model: opts.Model, Discipline: opts.Discipline, PlanB64: opts.PlanB64,
		PlanName: opts.PlanName, Base: opts.Base, ResolveDefaultBase: opts.ResolveDefaultBase,
		LocalBaseBranch: opts.LocalBaseBranch, NewWorktree: opts.NewWorktree,
	})
	if err != nil {
		s.log.Warn("agentd 节点派发失败", "target", opts.Target, "executor", opts.Executor,
			"model", opts.Model, "cause", err)
		return "", err
	}
	s.log.Info("agentd 节点派发已受理", "target", opts.Target, "task", task.ID)
	return task.ID, nil
}
```

### 4.3 2–5 分钟动作序列

1. **基线判据先跑（已跑）**：执行旧 agentd 命令 `go test ./internal/agentd/ -run 'TestCardStep(Returns202|SecondReturns409|RejectsImplement|AcceptsNodeName)$' -count=1`，预期在改动前为绿；执行四条 B203 回归，预期为绿。当前原始结果已入账。
2. **先改测试语义并跑红**：将 `TestCardStepRejectsImplement` 改名为 `TestCardStepAcceptsImplementWithoutInlineFile`，把期望改为 202，并用 `runStepFn` channel 捕获后台启动；新增以下用例断言。先不改生产代码，执行计划命令，预期旧实现因 implement 400 或不存在新符号而红：

   ```text
   TestRequiresInlineLocalFile：CardStepReq{Step:"implement"}、{Step:"review",Target:"linux-01",Executor:"codex",Model:"gpt-5",Extra:"x",Actor:"cli:u@h#1"}、所有可选字段空/非空组合，requiresInlineLocalFile 均必须 false。
   TestCardStepAcceptsImplementWithoutInlineFile：规范 actor 的 {"step":"implement","actor":"cli:u@h#123"} 返回 202，runStepFn 被调用；不得按节点名 400。
   TestCardStepLegacyActorFallback：原始 JSON 只有 {"step":"review"} 返回 202，捕获 runner.Session 与 runner.Dispatcher.Actor 都等于 web:<RemoteAddr>。
   TestCardStepRejectsEmptyActor：{"step":"review","actor":""} 返回 400，runStepFn 不调用。
   TestCardStepRejectsEmptyStep：缺 step 或 step:"" 返回 400，runStepFn 不调用。
   TestCardStepIgnoresUnknownFields：规范 JSON 增加 future_optional 或 plan_path 后仍按宽松解码；未知字段不进入 runner，不因 DisallowUnknownFields 400。
   TestCardStepPropagatesRequestFields：同一次请求的 target/executor/model/extra 全部进入捕获的同一个 runner；Session 与 Dispatcher.Actor 均保留 actor。
   TestCardStepSecondReturns409：保留既有卡槽在飞和错误体中的卡号/占用说明。
   TestCardStepReturns202：保留受理即返回，不等待 runStepFn channel 关闭或 runner 终态。
   ```

   测试必须照抄 `internal/agentd/ledgerapi_test.go` 的 `newLedgerEnv`/`ledgerPost`/`seedCardWithProject`，以及 `internal/agentd/cardstep_test.go` 的 `runStepFn`/`holdCardStep`/`waitFor`；这是复用既有 harness 的允许例外，断言已逐条列全，不新建生产 seam。
3. **跑红**：执行以下最小命令并记录原始结果：

   ```text
   go test ./internal/agentd -run 'Test(RequiresInlineLocalFile|CardStepAcceptsImplementWithoutInlineFile|CardStepLegacyActorFallback|CardStepRejectsEmptyActor|CardStepRejectsEmptyStep|CardStepIgnoresUnknownFields|CardStepPropagatesRequestFields|CardStepSecondReturns409|CardStepReturns202)$' -count=1
   ```

   预期旧 handler/旧 startCardStep 至少在 implement、字段透传或新纯函数处失败。
4. **最小解码实现**：把 `handleCardStep` 替换为 §4.2 完整代码；不要引入 `DisallowUnknownFields`；保持路由、Bearer/`withLedger` 门和 202 body `{"ok":true}` 不变。执行 `gofmt` 后重跑只命中新测试的 agentd 命令。
5. **最小装配实现**：把 `startCardStep` 改为 `func (s *Server) startCardStep(cardID string, req proto.CardStepReq) error`，更新 `ledgerapi_test.go`/`cardstep_test.go` 内部调用为 `proto.CardStepReq{Step: ..., Actor: ...}`，只在同一个 `StepRunner` 写入四个覆盖项和双 actor 落点。运行捕获测试，预期字段逐一通过。
6. **外部调用日志**：在既有 `stepTransport` 增加 §4.2 的调用前、pool 失败、Dispatch 失败、Dispatch 成功日志；不改 `ledgerstep.DispatchOpts` 或优先级规则。再跑 B203 四条安全网：

   ```text
   go test ./internal/ledgerstep -run 'Test(ViaTemplateExecutorModelOverridesAndPairRule|ViaTemplateSameExecutorKeepsTemplateModel|RunnerExecutorModelOverridePriorityAndPairRule|RunnerSameExecutorKeepsNodeModel)$' -count=1
   ```

7. **跑绿与提交**：执行 `gofmt -w internal/agentd/ledgerapi.go internal/agentd/cardstep.go internal/agentd/ledgerapi_test.go internal/agentd/cardstep_test.go`，再跑 U2 agentd 命令、B203 命令和 `go test ./internal/agentd -run 'TestCardStep' -count=1`。每条必须真实 exit 0 且命中测试。执行 `git diff --check`，`git add` 四个 agentd 文件，提交信息固定为 `feat(b185): normalize agentd card step requests`，把提交和每个命令原始结果追加台账。

### 4.4 日志、注释、测试范围和验收

- 解码失败带 `card`/cause；legacy actor fallback 带 `card/node/remote_addr/actor`；空 step、空 actor、未知字段不合规和 guard 拒绝均有上下文日志；装配完成带四项覆盖和 actor；外部 target client/Dispatch 前后都有 slog。禁止 `fmt.Printf` 记录日志。
- `requiresInlineLocalFile` 的注释必须写明“为何今天恒 false”；`startCardStep` 注释必须说明入参、异步返回语义、槽位释放；修改的导出/跨包符号继续满足 Go doc。
- 最小测试范围：`internal/agentd` 的 step 族和 `internal/ledgerstep` 四条 B203 回归；不跑全量 Go/Web。
- 完成判据：实现 `step == "implement"` 的 202；legacy 只有缺 actor 时 fallback；显式空 actor/空 step 400；未知字段被忽略；四个覆盖项和双 actor 落同一 runner；同卡第二次 409、结束释放槽位；HTTP 202 不等待终态。

## 5. U3：CLI `--step` 改为本机 agentd 202 提交

### 5.1 文件、依赖和接口

文件范围：

- 修改 `cmd/card_node.go`：删除 step 路径的本地 `StepRunner.Run`/`Outcome` 编码，改为调用本机 `Client.CardStep`。
- 修改 `cmd/card_dispatch.go`：`RunE` 在 step 分支不再先打开本地 ledger；非 step 模板直派分支保持现状。flag 定义保持现状，`cardDispatchTarget` 只成为请求字段，不是本机拨号目标。
- 修改 `cmd/card_dispatch_test.go`：将 `TestCardDispatchStepExecutorModelFlags`、`TestCardDispatchStepExtraReachesPrompt` 的 step 部分迁移为真实 `TargetEndpoint`/`httptest` JSON body 断言；非 step B203/B214 测试保持。
- 修改 `cmd/card_node_test.go`：补 immediate/no-fallback/plan/actor 断言；复用 `runLedgerCLI`、`writeTestConfig`、`swapDispatchTransportWithOpts` 作为负面 sentinel，不新增生产接缝。
- 只读复用 `cmd/root.go#LocalEndpoint`/`TargetEndpoint`；不修改 endpoint 语义。

Consumes：

```go
func LocalEndpoint() (addr, token string, err error)
func (c *client.Client) CardStep(ctx context.Context, cardID string, req proto.CardStepReq) error
func ledgerSession() string
```

Produces：

```go
func runStepDispatch(cmd *cobra.Command, id, node string) error
```

成功 stdout 的固定语义是“卡号+节点名+card wait 入口”，示例完整文本：

```text
卡 B185 的节点 review 已受理；进展见 handoff card wait B185
```

不得出现 task id、旧 `Outcome` JSON 或终态成功措辞。

### 5.2 CLI 入口的完整替换形状

`cmd/card_node.go#runStepDispatch` 使用本机 endpoint，不读取本地 plan、不跟随事件、不回落 `cliTransport`：

```go
// runStepDispatch 向本机 agentd 提交一次卡节点并在 202 后立即返回。
//
// 参数：cmd 提供 context、stdout/stderr；id 是卡号；node 是卡钉工作流节点名。
// 返回：本机 endpoint 配置、HTTP 受理或 agentd 错误；成功只表示受理。
// 注意：--target/--executor/--model/--extra 是请求覆盖项；--target 不改变本机
// agentd 拨号端点；--plan 与 --step 组合直接拒绝，绝不读取或上传调用方文件。
func runStepDispatch(cmd *cobra.Command, id, node string) error {
	if cardDispatchPlan != "" {
		slog.Default().Warn("card step 拒绝本地 plan", "card", id, "node", node,
			"plan", cardDispatchPlan)
		return fmt.Errorf("card dispatch --step 不接受 --plan：调用方本地文件不会被上传")
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	addr, token, err := LocalEndpoint()
	if err != nil {
		slog.Default().Warn("读取本机 agentd 端点失败", "card", id, "node", node, "cause", err)
		return err
	}
	cl := client.New(addr, token)
	req := proto.CardStepReq{
		Step: node, Target: cardDispatchTarget, Executor: cardDispatchExecutor,
		Model: cardDispatchModel, Extra: cardDispatchExtra, Actor: ledgerSession(),
	}
	slog.Default().Info("CLI 提交卡节点", "card", id, "node", node, "agentd", cl.BaseURL(),
		"target", req.Target, "executor", req.Executor, "model", req.Model,
		"has_extra", strings.TrimSpace(req.Extra) != "", "actor", req.Actor)
	if err := cl.CardStep(ctx, id, req); err != nil {
		slog.Default().Warn("CLI 卡节点未受理", "card", id, "node", node, "cause", err)
		return err
	}
	slog.Default().Info("CLI 卡节点已受理", "card", id, "node", node, "actor", req.Actor)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "卡 %s 的节点 %s 已受理；进展见 handoff card wait %s\n", id, node, id)
	return nil
}
```

`cmd/card_dispatch.go#cardDispatchCmd.RunE` 的 step 分支必须在 `openLedger()` 前短路；非 step 分支的完整控制流保留为：

```go
RunE: func(cmd *cobra.Command, args []string) error {
	id := args[0]
	if cardDispatchStep != "" {
		return runStepDispatch(cmd, id, cardDispatchStep)
	}
	st, err := openLedger()
	if err != nil {
		return err
	}
	defer st.Close()
	card, err := st.GetCard(id)
	if err != nil {
		return err
	}
	actor := ledgerActor()
	if card.Status == ledger.StatusDoing {
		return fmt.Errorf("卡 %s 已被认领（驱动 %s）", id, card.DriverSession)
	}
	if err := st.ClaimCard(id, ledger.StatusDoing, card.Status, ledgerSession()); err != nil {
		if current, getErr := st.GetCard(id); getErr == nil && current.DriverSession != "" &&
			current.DriverSession != ledgerSession() {
			return fmt.Errorf("卡 %s 已被 %s 认领: %w", id, current.DriverSession, ledger.ErrCASConflict)
		}
		return fmt.Errorf("认领失败（可能被并发抢先）: %w", err)
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	dispatcher := &ledgerstep.Dispatcher{St: st, Transport: cliTransport, Actor: actor}
	result, err := dispatcher.ViaTemplate(ctx, card, ledgerstep.TemplateDispatch{
		Template: cardDispatchTemplate, Target: cardDispatchTarget, PlanPath: cardDispatchPlan,
		DisciplineOverride: cardDispatchDiscipline, ExecutorOverride: cardDispatchExecutor,
		ModelOverride: cardDispatchModel, Extra: cardDispatchExtra,
	})
	if err != nil {
		_ = st.MoveCard(id, card.Status, ledger.StatusDoing, actor)
		_ = st.ReleaseCard(id, ledgerSession())
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
},
```

这段只允许删除旧 step 分支的本地 runner；非 step 的 `TemplateDispatch`、plan 读取、认领和回滚语义必须保持。

### 5.3 2–5 分钟动作序列

1. **基线判据先跑（已跑）**：执行旧 `TestCardDispatchStepExecutorModelFlags` 与 `TestCardDispatchStepExtraReachesPrompt`；当前分别为 `ok`，但它们证明的是旧同步 runner。另执行 `go test ./cmd -run 'TestStepFlagHelpMentionsNodeName|TestCardStepRejectsUnknown' -count=1`，确认节点名仍由卡钉工作流验证。
2. **先迁测试并跑红**：在 `cmd/card_dispatch_test.go`/`cmd/card_node_test.go` 写以下可判定测试，先不改 `runStepDispatch`：

   ```text
   TestCardDispatchStepSubmitsToLocalAgentd：httptest handler 收到 POST /api/cards/B185/step、Bearer、六字段 body；本机地址来自 LocalEndpoint，body.target 可以是 mac-02。
   TestCardDispatchStepReturnsImmediately：handler 立即回 202 + {"ok":true}；命令返回不等待任何 runner/事件终态，stdout 含卡号、节点名和 handoff card wait B185。
   TestCardDispatchStepCarriesOverrides：--target mac-02、--executor grok、--model grok-model、--extra 本轮只修 F1 逐字进入 JSON；不出现 task id/Outcome。
   TestCardDispatchStepUsesPIDActor：body.actor 等于 ledgerSession() 的 cli:<user>@<hostname>#<pid>，不是 ledgerActor()、不是 web:。
   TestCardDispatchStepRejectsPlan：--step review --plan local.md 在调用 LocalEndpoint/HTTP 前返回错误，handler 计数为 0。
   TestCardDispatchStepNoLocalFallback：本机 endpoint 不可达时返回 ErrUnreachable 相关错误；用 swapDispatchTransportWithOpts 设 bool sentinel，断言旧本地 runner/transport 从未调用。
   ```

   这是复用既有 `runLedgerCLI`/`writeTestConfig`/`swapDispatchTransportWithOpts` 的测试例外：断言逐条列全；HTTP body 必须由真实 `client.Client` 穿线，旧 seam 只作为“不能走 fallback”的负面 sentinel，不新增产品 seam。
3. **跑红**：执行：

   ```text
   go test ./cmd -run 'TestCardDispatchStep(SubmitsToLocalAgentd|ReturnsImmediately|CarriesOverrides|UsesPIDActor|RejectsPlan|NoLocalFallback)$' -count=1
   ```

   预期旧实现不向本机 endpoint 发请求、或测试无法编译新调用，记录原始输出。
4. **最小实现 CLI client 路径**：把 `runStepDispatch` 替换为 §5.2 完整代码；补 `fmt`、`log/slog`、`strings`、`internal/proto` import，删除 step 专用的 `encoding/json`/ledgerstep/client 依赖时必须确保非 step 文件依赖仍存在。先运行 `gofmt` 和 U3 目标测试。
5. **最小实现命令分支**：将 `cardDispatchCmd.RunE` 的 step 判断移动到 `openLedger()` 前，并把调用改为 `runStepDispatch(cmd, id, cardDispatchStep)`；非 step 代码逐字保持。跑 `TestCardDispatchStepRejectsPlan` 和 `TestCardDispatchStepNoLocalFallback`，确认 plan 零请求、agentd 不可用不 fallback。
6. **跑绿**：执行 U3 目标命令；再执行旧非 step 安全网：

   ```text
   go test ./cmd -run 'TestCardDispatch(ExecutorModelFlags|ExtraReachesPrompt)$' -count=1
   go test ./cmd -run 'TestStepFlagHelpMentionsNodeName|TestCardStepRejectsUnknown' -count=1
   ```

   目标是旧非 step 逻辑仍绿，step 测试真实命中且 stdout 不含 task id/Outcome。`--target mac-02` 必须只在 body 出现，HTTP URL 必须是本机 endpoint。
7. **提交 U3**：`git diff --check`；`git add cmd/card_node.go cmd/card_dispatch.go cmd/card_dispatch_test.go cmd/card_node_test.go`；提交信息固定为 `feat(b185): submit cli card steps to local agentd`，原始提交结果追加台账。

### 5.4 日志、注释、测试范围和验收

- `--plan` 拒绝、本机 endpoint 读取失败、HTTP 未受理、受理成功都用 `slog`，带 card/node/cause；用户可见 stdout 只用 `fmt.Fprintf` 写稳定提示，不把它当日志。
- `runStepDispatch` 导出性虽为包内符号，仍写完整参数/返回/“202 仅受理”注释；删掉 `Outcome` JSON 的旧注释，更新文件头说明 step 只提交到本机 agentd。
- 最小测试范围：只跑 `cmd` 的新 step 测试、旧 step flag/unknown node 测试和非 step B203/B214 安全网；不跑全量 Go/Web。
- 完成判据：CLI 只持有短 HTTP；202 后立即返回；stdout 有卡号、节点和 `handoff card wait <卡>`，无 task id/Outcome；四个 flags 与 PID actor 穿过真实 JSON；`--plan` 零请求；本机 agentd 不可用报错且不走本地 runner。

## 6. U4：回归网、CHANGELOG、变异复核和最终门禁

### 6.1 文件、依赖和接口

文件范围：

- 修改 `CHANGELOG.md` `[Unreleased]`，只记录本卡两条对外行为变化。
- 复跑 U0–U3 已列测试入口、B203/B214 回归、Web legacy body；不新增生产文件，不改 `card wait`/看板 UI。

Consumes：U0 `CardStepReq` fixture/type；U1 `Client.CardStep`；U2 handler/runner；U3 CLI stdout/HTTP。
Produces：一条真实 JSON/CLI/agentd/runner 回归网和可审计 changelog。

### 6.2 CHANGELOG 完整新增块

在 `[Unreleased]` 占位行前写入：

```markdown
### 变更

- `card dispatch --step` 现在把环节请求提交给本机 agentd，收到 HTTP 202 后立即返回；本机 agentd 不可用时命令失败，不再在 CLI 进程内回落本地编排。
- `card dispatch --step` 的 stdout 不再打印回合 `Outcome`，改为输出卡号、节点名和 `handoff card wait <卡>` 进展入口。
```

不得在本节记录 B225 恢复、B189 TTL/心跳、`card wait` 或看板 UI 改动。

### 6.3 2–5 分钟动作序列

1. **基线判据先跑（已跑）**：`git diff --check` 已 exit 0；Web 两条命令当前原始错误是缺少 `vitest`/`tsc`；`go test ./...` 当前在 `cmd` 观察到 `FAIL`。U4 不把这些旧结果写成最终通过。
2. **先写 changelog 并检查**：按 §6.2 修改 `CHANGELOG.md`，执行：

   ```text
   rg -n -C 2 'card dispatch --step|本机 agentd|Outcome|card wait' CHANGELOG.md
   git diff --check
   ```

   预期只看到两条 Unreleased 行，diff check 无输出。
3. **回归 U0 wire**：若 U0 类型/样本已审，按顺序执行：

   ```text
   go test ./internal/proto -run TestContractFixtures -count=1
   ```

   预期逐字节 fixture exit 0；若需要 `-update`，只能在 review 样本后执行 `go test ./internal/proto/ -run TestContractFixtures -update`，并确认 `git diff --name-only -- web/src/api/testdata` 只有 `CardStepReq.json`。
4. **回归 U1/U2 和 B203/B214**：执行：

   ```text
   go test ./internal/client -run 'TestCardStep' -count=1
   go test ./internal/ledgerstep -run 'Test(ViaTemplateExecutorModelOverridesAndPairRule|ViaTemplateSameExecutorKeepsTemplateModel|RunnerExecutorModelOverridePriorityAndPairRule|RunnerSameExecutorKeepsNodeModel)$' -count=1
   go test ./internal/agentd -run 'Test(CardStep|RequiresInlineLocalFile)' -count=1
   ```

   每条必须命中测试且真实 exit 0。
5. **回归 U3 与既有 CLI 安全网**：执行：

   ```text
   go test ./cmd -run 'TestCardDispatchStep(SubmitsToLocalAgentd|ReturnsImmediately|CarriesOverrides|UsesPIDActor|RejectsPlan|NoLocalFallback)$' -count=1
   go test ./cmd -run 'TestCardDispatch(ExecutorModelFlags|ExtraReachesPrompt)$' -count=1
   go test ./cmd -run 'TestStepFlagHelpMentionsNodeName|TestCardStepRejectsUnknown' -count=1
   ```

6. **回归 Web fixture 与 legacy body**：执行：

   ```text
   (cd web && npm run test -- src/api/contract.test.ts src/api/ledger.test.ts)
   (cd web && npm run typecheck)
   ```

   `web/src/api/ledger.test.ts` 的 `runCardStep` body 必须仍精确为 `{step: "..."}`；Web 依赖不可执行时，原始报错入台账并判 U4 未通过。
7. **最终 Go/静态门禁**：按实现后的真实环境执行：

   ```text
   go test ./...
   git diff --check
   ```

   两条都必须真实 exit 0；若失败只把命令原始报错写入台账，不替它归因。
8. **提交 U4**：`git add CHANGELOG.md` 加上 U0–U3 仍未提交的计划范围内文件，执行 `git diff --cached --check`，提交信息固定为 `docs(b185): record step runner behavior`；原始提交结果追加台账。最终分支不得切换、不得 push。

### 6.4 回归网与真机清单

本卡安全网明确为：

- B203：`TestViaTemplateExecutorModelOverridesAndPairRule`、`TestViaTemplateSameExecutorKeepsTemplateModel`、`TestRunnerExecutorModelOverridePriorityAndPairRule`、`TestRunnerSameExecutorKeepsNodeModel`；它们锁 CLI > 节点 > 模板与同名/异名 executor 的 model 成对规则。U2 只证明字段到达同一个 runner，不复制规则。
- B214：`TestCardDispatchExtraReachesPrompt` 与迁移后的 `TestCardDispatchStepExtraReachesPrompt`；前者保留非 step 模板路径，后者改为 step HTTP body 透传断言。
- Web：`web/src/api/ledger.test.ts` legacy `{step}` 精确 body 断言；`web/src/api/contract.test.ts` 新 `CardStepReq` fixture 镜像。
- agentd：既有 202/409/slot-release 与新增 actor/fields/implement/unknown-field 反面断言。

以下不由机内测试冒充通过，必须由协调者在 review/acceptance 真机执行并写“未验证，需真机”或原始结果：

1. 真实本机 agentd 启动时，一张卡执行 `card dispatch <卡> --step <节点>` 立即返回、后台继续运行；停掉本机 agentd 时 CLI 清楚失败且不本地跑第二份编排。
2. 真实目标机检查四个覆盖项：同名 executor + 空 model 保留下层 model；不同名 executor + 空 model 切断下层 model；extra 只进入本轮 prompt。
3. 同用户/同主机两个 CLI PID 对同卡发起时，timeline/driver CAS 能区分 `#pid`，不退化成 `web:`。
4. 真实看板仍发送 `{step}`，服务端补 `web:<RemoteAddr>` 并返回 202；事件流/`card wait` 观察行为不因本卡改变。
5. implement 不带 `--plan` 可提交；CLI 带 `--plan` 在发送前拒绝，文件未读取/上传；当前冻结 wire 没有 inline-file 字段，agentd 对未知字段按 P3 忽略。
6. relay/直连、Windows/Linux、服务管理器、真实 executor 和 agentd 重启后的已知孤儿行为；重启恢复属于 B225，不能写成本卡已解决。

## 7. 缺陷族对抗审查结论

| 缺陷族 | 计划结论与可判定证据 |
|---|---|
| 生命周期/状态机中断 | CLI 只持有短 HTTP；agentd 既有 goroutine 和卡槽负责本轮；`defer releaseCardStep` 的结束测试防永久槽位。本卡不做 agentd 重启恢复，归 B225，真机项明确未验证。 |
| 静默失败/误导报错 | JSON 解码、空 step、空 actor、未知节点、在飞 409、HTTP 非 202、连接失败和 bad ack 分开断言；202 只写“受理”，不写终态成功。client `httpError` 保留原始 body。 |
| 跨平台/网络假设 | 机内 httptest 只证明 URL/header/body；本机 loopback、IPv6、relay、权限、目标机 executor 和 Windows/Linux 作为真机项，不能由 Go 单测推出。 |
| 假红/假绿 | fixture 逐字节、client 真实 HTTP、handler 捕获真实 runner、CLI 真实 `Client.CardStep`、plan 零请求、旧 runner 未调用共同构成正反夹逼；只断言 202 不算完成。 |
| 门禁绕过 | 继续使用已有 Bearer/`withLedger`/卡钉工作流查找/同卡单飞；CLI 不开匿名 endpoint，不读取或上传 step plan，不引入 TTL/心跳。 |
| 序列化边界 | §1 的六处投影各有断言；可选字段用 key presence 区分缺席/零值，actor 用 raw presence 区分 legacy 缺席和显式空。 |
| 新枚举越过白名单 | 不新增状态、事件、kind 或节点白名单；implement 只是移除按节点名错误拒绝。新增 `plan_path`/新 kind 属契约越界，计划不吸收。 |
| 承重安全属性 | `Session == Dispatcher.Actor`、PID actor、同卡单飞、202≠终态、agentd 不可用不 fallback、未知字段兼容均有断言；不虚构一次性 token 属性。 |
| webview/平台表现 | 不改 `runCardStep` 签名/body，不改 UI；legacy body 测试保留，真实 Web/Wails 交给真机清单。 |

## 8. 变异必查表：这里写错 → 哪条测试会红

| 变异 | 应变红的命令/测试 |
|---|---|
| `CardStepReq` 漏字段、改 json key、给 actor 加 `omitempty` | `go test ./internal/proto -run TestContractFixtures -count=1`、Web contract fixture、U1 全字段 body、U3 overrides/actor。 |
| client 路径错、Bearer 丢失、202/400/409 误判成功 | U1 `TestCardStepSerializesAllFields`、`TestCardStepPreservesHTTPError`、U3 `StepNoLocalFallback`。 |
| 可选空值编码成显式 `""` | U1 `TestCardStepOmitsEmptyOptionalFields` 的 `json.RawMessage` key presence 断言。 |
| agentd 规范 actor 缺席直接拒绝 | U2 `TestCardStepLegacyActorFallback` 与 Web legacy body 回归。 |
| 显式 `actor:""` 走 web fallback | U2 `TestCardStepRejectsEmptyActor`。 |
| actor 只写 Session 或只写 Dispatcher.Actor | U2 `TestCardStepPropagatesRequestFields` 双落点断言。 |
| wire 丢 target/executor/model/extra 任一字段 | U1 全字段 body + U2 runner 捕获 + U3 flags body 断言。 |
| 在 agentd 重算/改写 B203 成对规则 | 四条 B203 runner/dispatch 回归，尤其同名 executor 保留 model 与异名 executor 空 model 反例。 |
| 保留 `if step == "implement"` 按节点名拒绝 | U2 `TestCardStepAcceptsImplementWithoutInlineFile`。 |
| 守卫把现有 target/executor/model/extra/extra 文本误判为内联文件，或未来把 guard 改宽到放行可表达的本地字段 | U2 `TestRequiresInlineLocalFile` 对各种非空/空字段组合均期望 false；当前冻结类型没有可表达的 inline 字段，真正新增该字段必须先改 contract，不得静默吸收。 |
| 使用 `DisallowUnknownFields` 把新可选字段/未知字段打成 400 | U2 `TestCardStepIgnoresUnknownFields`，其 JSON 带 `future_optional`/`plan_path`，预期仍按宽松规则受理。 |
| 空 step 绕过 `nodeFor` | U2 `TestCardStepRejectsEmptyStep` 与 U3 `TestCardStepRejectsUnknown`。 |
| 异步改同步或 HTTP 等待终态 | U2 `TestCardStepReturns202`、U3 `TestCardDispatchStepReturnsImmediately`。 |
| U3 仍调用本地 `StepRunner.Run`/`cliTransport` | U3 `TestCardDispatchStepNoLocalFallback` 的旧 seam sentinel 与 `StepSubmitsToLocalAgentd` HTTP 计数。 |
| U3 把 `--target` 当拨号目标 | U3 断言 URL 是本机 `LocalEndpoint`，body.target 才是 `mac-02`。 |
| U3 用 `ledgerActor()`/固定 `web:` | U3 `TestCardDispatchStepUsesPIDActor`。 |
| `--plan` 静默忽略或随 wire 发送 | U3 `TestCardDispatchStepRejectsPlan` 的零请求/错误断言。 |
| Web 改为发送 actor 或改签名 | `web/src/api/ledger.test.ts` 第 65–68 行的精确 `{step}` 断言。 |
| 删除 202/409/slot-release 旧门 | agentd 既有 `TestCardStepReturns202`、`TestCardStepSecondReturns409`、`TestStartCardStepReleasesSlotOnFinish`。 |

## 9. 自审三查和契约疑点

### spec 覆盖

- 用户故事 1（秒回、卡号+节点、`card wait` 入口）→ U3 `runStepDispatch`、U3 immediate/stdout 测试、U4 changelog。
- 用户故事 2（四项覆盖生效）→ U0 wire、U1 client body、U2 runner 捕获、U3 flags body、B203/B214 回归和真机项。
- 用户故事 3（CLI PID actor 并发区分）→ U0 actor 字段、U2 双落点、U3 PID body、真机并发项。
- 用户故事 4（implement 可经 step，非节点名拒绝）→ U2 pure guard、implement 202、空 actor/step 反面测试、U3 plan 预拒绝。
- 明确 out of scope（B225、B189、card wait、UI）→ 全计划文件范围、真机清单和 changelog 禁止项。

### 契约疑点

spec “打印任务标识”已按 P2 解释为卡号+节点名；冻结响应没有 task id，且 agentd 受理时没有单一回合 task id。计划不扩展 `{"ok":true}`，不在 U3 私加响应字段。若实现时要求 task id，必须退回修改 contract；本计划不自行假设。

### 占位符扫描声明

占位符扫描已完成，未发现模板占位符或“描述动作但不给判据”的骨架。U1/U2/U3 的测试部分使用了纪律允许的既有 harness 例外：每条断言逐条列出，并明确指认 `internal/client/client_test.go`、`internal/agentd/ledgerapi_test.go`、`internal/agentd/cardstep_test.go`、`cmd/ledgercli_test.go`、`cmd/root_test.go` 中要照抄的夹具；没有用空骨架测试替代代码。

### 上下文预算和文件边界

- U0：6 个明确 Go/Web 文件族，纯契约。
- U1：2 个 `internal/client` 文件。
- U2：4 个 `internal/agentd` 文件；不改 `internal/ledgerstep`，只跑其既有回归。
- U3：4 个 `cmd` 文件；`cmd/root.go` 只消费现有 `LocalEndpoint`，不改。
- U4：`CHANGELOG.md` 加既有测试入口；不扩大实现文件集。

### 计划完成判据

本节点只以本计划文档落盘、无占位符、逐条覆盖 spec、契约、序列化边界、缺陷族、变异表、日志/注释、最小测试范围和提交顺序为完成；不在本节点写实现代码或伪造实现级测试绿。
