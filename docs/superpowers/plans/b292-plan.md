# B292：小队成员载体政策位与跨队物理封顶实现计划

## 0. 权威输入、状态与边界

- 本计划状态：已拍板（2026-08-29）。P1=A、P2=A、P3=A 是本轮实现约束，不再由实现者重新选择。
- 有效基线：`origin/acc/b156.2-156.3`，当前执行分支：`cards/B292-charter-3`。只在当前分支工作，不切分支，不改 git 配置。
- 权威规格：`docs/superpowers/specs/2026-08-29-b292-squad-member-concurrency-design.md`，状态为已批准。
- 冻结契约：`docs/superpowers/specs/b292-contract.md`，状态为上游已批准、冻结；实现必须服从 §2–§6 的字段、键、错误和清队语义。
- 上游拆解：`docs/superpowers/specs/b292-breakdown.md`。该文件仍保留“待拍板”的历史稿首行；本计划以本轮协调者补充裁决覆盖三项岔口，不修改上游输入。
- 本节点只产出本计划与 `docs/superpowers/ledgers/2026-08-29-b292-breakdown-ledger.md`；以下代码块是 implement 节点逐段落地的目标，不在本节点写实现代码。
- 不增加队级总并发字段、队级运行计数键、第三类成员计数池、公开 `PopReadyFor` 或旁路准入 API；不改变账户额度探测、真实 agentd 重启恢复和多进程 TOCTOU 的真机边界。

### 0.1 三项已拍板语义

1. P1=A：`handoff squad create/set --member <carrier>[:<positive-int>]`。无冒号表示政策不限；冒号后必须是正整数。载体名为空、含冒号、或值不是正整数都在 CLI 本地拒绝。成员名不做逗号拆分，保留空格、斜杠和中文。旧 `--max-concurrency` 不注册，Cobra 直接拒绝，CLI 不发送队级总帽。
2. P2=A：清队只在 `drainQueuesOnce` 内用局部 deferred 列表回填协调者 `ErrNoSlot` 请求并继续本轮；不新增公开 selector/Pop 入口。非 `ErrNoSlot` 的协调者错误仍回填当前请求并终止本轮当前既有错误路径；回填失败必须带请求和回填错误日志。
3. P3=A：CLI 与 Web 对非空且非正整数政策值统一拒绝，错误必须含合法示例；不拨号、不发 PUT、不把非法值静默规范化为不限。Go wire 的 `omitempty` 仍把 0/缺席读作不限；本轮新 UI/CLI 只能通过留空/省略表达不限。

## 1. 基线门禁与依赖事实

### 1.1 已于动手前实跑的基线

下表每条结果均已原样追加到 `docs/superpowers/ledgers/2026-08-29-b292-breakdown-ledger.md`。implement 节点在每个 task 开始时仍要重跑该 task 的最小命令，不能把本节结果当成修改后的通过证据。

| 范围 | 命令 | 本轮基线原始结果 | 用途 |
| --- | --- | --- | --- |
| scheduling | `go test ./internal/scheduling -count=1` | 退出 0；`ok  github.com/Xsxdot/handoff/internal/scheduling  0.550s` | Task A 红绿门禁 |
| agentd | `go test ./internal/agentd -run 'Scheduling|Automation|Queue' -count=1` | 退出 0；`ok  github.com/Xsxdot/handoff/internal/agentd  3.512s` | Task B/C 既有清队与调度门禁 |
| proto/agentd | `go test ./internal/proto ./internal/agentd -run 'ContractFixtures|Squad|Scheduling' -count=1` | 退出 0；原始输出含 `ok  github.com/Xsxdot/handoff/internal/proto  0.025s`，agentd 无匹配测试输出 | Task C 必须新增真实 HTTP seam，不能据此宣称 agentd 已覆盖 |
| CLI | `go test ./cmd -run 'Squad' -count=1` | 退出 0；`ok  github.com/Xsxdot/handoff/cmd  0.109s` | Task D 红绿门禁 |
| Web 依赖 | `cd web && npm ci` | 退出 0；`added 290 packages, and audited 291 packages in 2s`；`found 0 vulnerabilities` | 使 Web 基线可复跑，`node_modules` 不纳入提交 |
| Web typecheck | `cd web && npm run typecheck` | 退出 0；运行 `tsc -b` 无错误 | Task E 类型门禁 |
| Web seam tests | `cd web && npm test -- --run src/app/settings/SchedulingPage.test.tsx src/api/scheduling.fetch.test.ts src/api/contract.test.ts` | 退出 0；`Test Files 3 passed (3)`、`Tests 49 passed (49)`、Vitest `v4.1.10` | Task C/E fixture、页面和 fetch 门禁 |
| Go 组合编译 | `go build ./...` | 退出 0，无输出 | 组合门禁 |
| Go 静态检查 | `go vet ./internal/scheduling ./internal/agentd ./internal/proto ./cmd` | 退出 0，无输出 | 组合门禁 |
| diff 空白 | `git diff --check` | 退出 0，无输出 | 计划和实现收口门禁 |

基线首次运行 Web typecheck/test 时的原始错误分别是 `sh: 1: tsc: not found` 与 `sh: 1: vitest: not found`；安装依赖后才取得上表通过结果，不能将缺依赖写成代码失败。

### 1.2 契约对侧库行为已查证

- `encoding/json` 的 `omitempty` 对整数 0 省略字段：`docs/superpowers/specs/b292-contract.md` §2.2 引用 `/usr/local/go/src/encoding/json/encode.go:107-110`。因此 0/缺席是同一 wire 语义“不限”，测试必须另用 raw key presence 区分缺席和显式零。
- HTTP body 解码当前未调用 `DisallowUnknownFields`：`internal/agentd/schedapi.go:129-133`；标准库开关对应 `/usr/local/go/src/encoding/json/decode.go:739-741`。旧顶层 `max_concurrency` 输入按冻结行为被忽略，不能复制到成员。
- squad client 读体仍由 `internal/client/squads.go:20-30` 使用 `io.LimitReader(..., 1<<20)` 后整体 JSON 解码；请求时限仍由 `internal/client/client.go:399-439` 的调用方 context 施加。本卡不加新读限、全局 timeout、连接保活或请求重试。

### 1.3 代码图与覆盖债

- 已运行 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_scheduling`：best 领域命中 `d_scheduling`，包为 `internal/schedclient`、`internal/scheduling`，入口为 `func New(repo schedclient.Registry) *Service`；有 6 个未扫描入口和缺少领域声明 warning。
- `context d_coordination` 退出 1，原始错误说明 `d_coordination` 是现状视图 id，不在 best 树词表；本计划按 best 词表用 `d_gateway`、`d_orchestration`、`d_cli`、`d_protocol`、`d_web` 描述职责，不伪造一个可查询的 `d_coordination` best 领域。
- `context d_protocol`、`context d_web` 均实跑成功；Web actual 分散到 `d_web_shell`、`d_web_contract`、`d_web_admin` 等子域，计划只圈定文件清单，不扩大到整个 Web 域。
- `sym m_scheduling_Squad` 命中 `internal/scheduling/scheduling.go:77`；`sym n_scheduling_Service_Admit` 命中 `func (s *Service) Admit(req IgnitionRequest) (Binding, error)`；`sym n_scheduling_Service_LaunchAdmit` 命中 `func (s *Service) LaunchAdmit(squadName string) (Binding, error)`。
- `sym internal/agentd/scheddrain.go#drainQueuesOnce` 退出 1，原始错误为“符号不在图中（图未覆盖或名字有误）”；`chain`/`who-calls` 对相关入口只返回焦点且 `edges:null`，并报告 6 个未扫描入口。implement 只能按源码和测试 harness 查调用面；这组入口记为图覆盖债，不当作无调用方。

计划锚点（实现者按源码窗口再核对）：[scheduling.SquadMember](internal/scheduling/scheduling.go#SquadMember)、[scheduling.Admit](internal/scheduling/scheduling.go#Admit)、[scheduling.LaunchAdmit](internal/scheduling/scheduling.go#LaunchAdmit)、[scheduling.Release](internal/scheduling/scheduling.go#Release)、[agentd.drainQueuesOnce](internal/agentd/scheddrain.go#drainQueuesOnce)、[agentd.handleSquadPut](internal/agentd/schedapi.go#handleSquadPut)、[proto.SquadMember](internal/proto/scheduling.go#SquadMember)、[cmd.squadCreateCmd](cmd/squad.go#squadCreateCmd)、[web.SchedulingPage](web/src/app/settings/SchedulingPage.tsx#SchedulingPage)。其中清队和页面函数的图缺口仍以 §1.3 的覆盖债为准。

## 2. 变更边界和跨 task 接口

### 2.1 允许修改/新增的有界文件集

实现者只可触及以下 18 个路径；新增测试只能落在列表中的测试文件。若编译需要列表外路径，先停止并回到契约边界核对，不自行扩张。

```text
internal/scheduling/scheduling.go
internal/scheduling/scheduling_test.go
internal/scheduling/registry_read_test.go
internal/agentd/scheddrain.go
internal/agentd/scheddrain_test.go
internal/agentd/schedapi.go
internal/agentd/schedapi_test.go
internal/agentd/scheddispatch_test.go
internal/proto/scheduling.go
internal/proto/contract_fixture_test.go
cmd/squad.go
cmd/squad_test.go
web/src/api/scheduling.ts
web/src/api/contract.test.ts
web/src/api/scheduling.fetch.test.ts
web/src/api/testdata/SquadsResp.json
web/src/app/settings/SchedulingPage.tsx
web/src/app/settings/SchedulingPage.test.tsx
```

只读核验入口，不修改：`internal/agentd/coordapi.go`、`internal/agentd/coordapi_test.go`、`internal/client/squads.go`、`internal/client/client.go`、`internal/agentd/server.go`。

### 2.2 精确接口（跨 task 逐字对齐）

Scheduling 产生并消费：

```go
type SquadMember struct {
	Carrier        string `json:"carrier"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

type Squad struct {
	Name    string        `json:"name"`
	Role    SquadRole     `json:"role"`
	Members []SquadMember `json:"members"`
}

func (s *Service) PutSquad(q Squad, expect int) error
func (s *Service) Admit(req IgnitionRequest) (Binding, error)
func (s *Service) LaunchAdmit(squadName string) (Binding, error)
func (s *Service) Release(squadName, carrierName string) error
func (s *Service) Enqueue(req IgnitionRequest, kind string) (position int, err error)
func (s *Service) PopReady(kind string) (IgnitionRequest, bool, error)
```

Agentd consumes those scheduling signatures and produces the existing public endpoints:

```go
func (s *Server) drainQueuesOnce(ctx context.Context) (processed int, err error)
func (s *Server) launchCoordinatorRound(ctx context.Context, card, source string) (keystone.RoundResult, error)
func (s *Server) handleSquadPut(w http.ResponseWriter, r *http.Request)
```

Proto produces the exact Go wire types consumed by agentd, client tests, CLI and fixture generation:

```go
type SquadMember struct {
	Carrier        string `json:"carrier"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}
type SquadView struct {
	Name    string        `json:"name"`
	Role    string        `json:"role"`
	Members []SquadMember `json:"members"`
	Version int           `json:"version"`
}
type SquadInput struct {
	Name    string        `json:"name,omitempty"`
	Role    string        `json:"role"`
	Members []SquadMember `json:"members"`
}
```

CLI produces `proto.SquadInput` through the command seam and consumes `proto.SquadView`/`proto.SquadsResp`; its parser API is:

```go
func parseSquadMember(raw string) (proto.SquadMember, error)
func squadMemberInputs(raw []string) ([]proto.SquadMember, error)
func formatSquadMembers(members []proto.SquadMember) string
```

Web mirrors the same wire and consumes/produces:

```ts
export interface SquadMember {
  carrier: string
  max_concurrency?: number
}
export interface SquadView {
  name: string
  role: string
  members: SquadMember[]
  version: number
}
export interface SquadInput {
  name?: string
  role: string
  members: SquadMember[]
}
export const putSquad: (name: string, expect: number, input: SquadInput) => Promise<SquadPutResp>
```

### 2.3 生产接缝清单与测试入口对照

| 接缝 | 生产入口 | 必须覆盖的测试入口 |
| --- | --- | --- |
| S1 scheduling 入站准入/释放 | `Service.Admit`、`Service.LaunchAdmit`、`Service.Release` | `internal/scheduling/scheduling_test.go` 通过真实 ledger registry 的公开方法；`internal/agentd/scheddispatch_test.go` 的 `startCardStep` 作为执行派发接缝 |
| S2 持久 registry 兼容读 | `Squad.UnmarshalJSON`、`PutSquad`、`SquadRows` | `internal/scheduling/registry_read_test.go` 的真实临时 ledger fixture |
| S3 agentd 清队 | `Server.drainQueuesOnce` 经 `runAutomationPass`/队列 harness | `internal/agentd/scheddrain_test.go` 的真实 queue seed、`drainQueuesOnce` 和另一载体执行入口 |
| S4 HTTP JSON | `handleSquadPut`、`handleSquadsGet` | `internal/agentd/schedapi_test.go` 的 `newSchedEnv` + `schedReq` + Bearer 真实 Handler |
| S5 CLI | `squadCreateCmd`、`squadSetCmd`、`squadListCmd` | `cmd/squad_test.go` 的 `stubSquadAgentd`、`setStub`、`runLedgerCLI` |
| S6 Web fetch | `putSquad` | `web/src/api/scheduling.fetch.test.ts` 的真实 fetch mock body/URL 断言 |
| S7 Web 页面 | `SchedulingPage` 用户行为 | `web/src/app/settings/SchedulingPage.test.tsx` 的 RTL/user-event 打开弹窗、输入、保存、失败保留草稿 |

每支新增测试的入口必须属于上表某条接缝或调用链穿过该接缝；纯 helper 测试只能附加，不能顶替 S1–S7。允许的 harness 形态例外：各包已有夹具不同，测试代码直接照抄同文件的 `newCASFixture`、`newRowsFixture`、`newSchedEnv`、`setupSquadEnv`、`seedQueueCoordinator`、`stubSquadAgentd`、`SchedulingPage.test.tsx` 的 RTL setup；下列测试断言逐条列全，故不以未展开整份既有 harness 作为占位。

## 3. 实现 DAG

全量 Go/Web 测试只在组合收口运行，不归属于单个 task。每个 task 的第一动作是重跑自己的基线命令并记录原始输出，随后才写红测；纯投影/日志/注释步骤不单列红绿周期。

### Task A：scheduling 两级准入、兼容读和释放

**文件集：** `internal/scheduling/scheduling.go`、`internal/scheduling/scheduling_test.go`、`internal/scheduling/registry_read_test.go`。

**Consumes / Produces：** 消费 `schedclient.Registry` 的 `Get/List/Put/Delete` 和现有 `Carrier`/`Squad`；产生本文 §2.2 的 `SquadMember`、`Squad` 及公开 `PutSquad/Admit/LaunchAdmit/Release` 行为。不得改公开签名。

1. 判据先在基线跑：在修改前运行 `go test ./internal/scheduling -count=1`；预期是本计划 §1.1 的 package `ok`。同步确认 `scheduling.go:70-114,204-260,371-455` 与 `scheduling_test.go:newCASFixture` 的现状，若基线输出不同把原始输出写入 ledger 后再继续。
2. 锁 S1 的失败测试：从 `newCASFixture` 的真实临时账本和公开方法构造两队共享 `c1`、成员政策和载体物理上限；逐条断言：(a) 前成员满、后成员有位时 `Admit` 返回后成员的 `Binding.Carrier`；(b) 所有健康成员任一级满才 `errors.Is(err, ErrNoSlot)`；(c) 无健康成员为 `ErrNoHealthy`；(d) 成员政策空/0不限；(e) 两队政策和大于物理上限时跨队成功总数不超过物理上限；(f) 计数键只有 `sched_running/squad/<队>/<carrier>` 与 `sched_running/carrier/<carrier>`，不存在 `sched_running/squad/<队>` 或 `member/`；(g) 重复 `Release` 后两键均为 0 且不为负；(h) 请求 target/executor/model 覆盖优先，空模型载体与显式模型载体分别返回自己的身份/模型。
3. 跑红命令 `go test ./internal/scheduling -run 'Test.*(Member|Admit|Release|Concurrency|Binding)' -count=1`；若新增测试意外先绿，必须把断言改为当前缺陷的生产入口反例，不能改为直测 `acquire`。本 task 的 helper 纯计数读取只能辅助断言，不得代替 `Admit`/`LaunchAdmit`/`Release` 入口。
4. 最小实现/修正：保留并核对以下唯一计数逻辑，不增加第三类键；`admitInto` 对健康成员按登记顺序继续尝试；`acquire` 先读两级计数、分别以 `>0` 上限阻断、成员侧 CAS 成功后载体侧 CAS，后者失败用带符号减法回滚成员侧并在冲突上重试；`Release` 只递减成员键和载体键。保留 `MaxConcurrency <= 0` 为域内不限，保留 `ErrRetryExhausted` 原样上浮，不把 CAS 错误改成 `ErrNoSlot`。

目标计数核心必须保持下列完整形状（变量名可以沿现状，键和先后不可改）：

```go
func (s *Service) acquire(q Squad, member SquadMember, c Carrier, req IgnitionRequest) (Binding, error) {
	squadKey := kindSquad + "/" + q.Name + "/" + member.Carrier
	carrierKey := kindCarrier + "/" + c.Name
	for attempt := 0; attempt < maxCASAttempts; attempt++ {
		squadRunning, squadExpect, err := s.readCount(squadKey)
		if err != nil {
			return Binding{}, err
		}
		carrierRunning, carrierExpect, err := s.readCount(carrierKey)
		if err != nil {
			return Binding{}, err
		}
		if member.MaxConcurrency > 0 && squadRunning >= member.MaxConcurrency {
			return Binding{}, errMemberFull
		}
		if c.MaxConcurrency > 0 && carrierRunning >= c.MaxConcurrency {
			return Binding{}, errMemberFull
		}
		if err := s.casCount(squadKey, squadExpect, squadRunning+1); err != nil {
			if errors.Is(err, schedclient.ErrCASConflict) {
				continue
			}
			return Binding{}, err
		}
		if err := s.casCount(carrierKey, carrierExpect, carrierRunning+1); err != nil {
			_ = s.stepRunning(squadKey, -1)
			if errors.Is(err, schedclient.ErrCASConflict) {
				continue
			}
			return Binding{}, err
		}
		return bindingFor(q, c, req), nil
	}
	return Binding{}, fmt.Errorf("%w: %s 与 %s", ErrRetryExhausted, squadKey, carrierKey)
}
```

5. 加关键节点日志：scheduling 包当前没有注入式 logger；在 `Admit`、`LaunchAdmit`、`Release` 的入口/成功/错误分支使用 `slog.Default().Debug/Info/Error`，字段至少含 `squad`、`carrier`、`member_policy`、`carrier_cap`、`error_kind`，不得 `fmt.Print`。已有 agentd caller 日志保留，避免在 caller 复制计数规则。
6. 加注释：若变更 `acquire`/`admitInto`，在导出方法和非显然 CAS 回滚处保留“为什么先成员后载体、为什么回滚失败取保守高计数、为什么成员满要继续后序成员”的中文注释；新测试文件头写职责与不覆盖真机重启边界。
7. 跑绿并最小复核：`go test ./internal/scheduling -count=1`；再运行 `gofmt -w internal/scheduling/scheduling.go internal/scheduling/scheduling_test.go internal/scheduling/registry_read_test.go` 后 `gofmt -l` 对这三个文件应无输出，`git diff --check` 应无输出。

S2 兼容读的明确断言落在 `registry_read_test.go`：使用 `newRowsFixture`/真实 ledger body 写入旧 `members:["c1","c2"]` 与旧顶层 `max_concurrency`，通过 `Squad` 读成两个 `SquadMember{Carrier:...}`，再次成功写回后 raw body 只有新成员对象、不出现顶层键；另测空成员数组为合法空队。该测试穿过 registry JSON，不以直接构造 Go struct 代替。

### Task B：agentd 清队只对协调者 ErrNoSlot 延迟回填并继续

**文件集：** `internal/agentd/scheddrain.go`、`internal/agentd/scheddrain_test.go`、`internal/agentd/scheddispatch_test.go`。

**Consumes / Produces：** 消费 `Service.PopReady/Enqueue/LaunchAdmit/Release`、`coordinatorAdmissionError`、`scheduling.QueueKinds`；产生 `drainQueuesOnce(ctx) (int,error)` 的清队行为。不得暴露新 selector，不改 `launchCoordinatorRound`/`wakeCoordinatorRound` 的 `coordinatorAdmissionError` 包装签名。

1. 判据先在基线跑：在修改前运行 `go test ./internal/agentd -run 'Scheduling|Automation|Queue' -count=1`；预期 package `ok`。读 `scheddrain.go:84-124`，确认现状协调者任意错误回填后立即 return；读 `seedQueueCoordinator`、`setupNoPTYSquadEnv` 和既有 `queueTraceRunner`，记录实际 harness 输出。
2. 写锁缝失败测试并跑红：在 `scheddrain_test.go` 复用真实 ledger、scheduling service 和 queue runner，建立 launch queue 的协调者请求在载体 A 全满、ignition queue 的执行者请求能使用载体 B；单次调用真实 `drainQueuesOnce` 后逐条断言：(a) 协调者请求仍存在于 launch queue；(b) B 执行者已走真实 `drainIgnitionRequest`/派发入口；(c) A 的回填不改变 B 的计数/事件；(d) 同载体同时可用时 launch coordinator 先消费，之后才是 ignition；(e) 队内 ready→priority→FIFO 顺序不变；(f) 第二次重新出队再次调用 `LaunchAdmit`/`Admit`，队列行不保存 Binding；(g) 非 `ErrNoSlot` 仍回填当前行并停止既有错误路径；(h) 回填失败日志包含 kind/card/node/cause/requeue_error。
3. 最小实现：在 `drainQueuesOnce` 的一次调用局部维护 deferred 协调者请求，只有 `errors.Is(launchErr, scheduling.ErrNoSlot)` 才 append 并 continue；完成当前 kind 或本轮返回前调用局部 `flushDeferred`，按原请求调用 `requeueAutomation`。PopReady 错误、未知 kind、非 `ErrNoSlot` 错误和 ignition 错误均走既有 return/回填路径；达到 `automationBatchLimit` 也要 flush deferred。每次 append、flush、非预期退出和回填失败使用 `s.log.Warn/Error`，字段含 `kind`、`card`、`node`、`deferred_count`、`cause`。

控制流目标如下，执行者应将它嵌入现有函数并保留既有出队/业务日志，而不是新增公开入口：

```go
func (s *Server) drainQueuesOnce(ctx context.Context) (processed int, err error) {
	if s.scheduling == nil {
		s.log.Error("自动化队列清队失败：编制域未装配")
		return 0, errors.New("自动化队列清队：编制域未装配")
	}
	type deferredLaunch struct {
		req   scheduling.IgnitionRequest
		cause error
	}
	deferred := make([]deferredLaunch, 0)
	flushDeferred := func() int {
		count := len(deferred)
		for _, item := range deferred {
			s.requeueAutomation(item.req, scheduling.KindLaunchQueue, item.cause)
		}
		deferred = nil
		return count
	}
	for _, kind := range scheduling.QueueKinds {
		for processed < automationBatchLimit {
			req, ok, popErr := s.scheduling.PopReady(kind)
			if popErr != nil {
				requeued := flushDeferred()
				s.log.Error("自动化队列出队失败", "kind", kind,
					"deferred_count", requeued, "cause", popErr)
				return processed, fmt.Errorf("出队 %s 失败: %w", kind, popErr)
			}
			if !ok {
				break
			}
			processed++
			s.log.Info("自动化队列出队", "kind", kind, "card", req.Card,
				"node", req.Node, "squad", req.Squad, "priority", req.Priority)
			switch kind {
			case scheduling.KindLaunchQueue:
				if _, launchErr := s.launchCoordinatorRound(ctx, req.Card, "manual"); launchErr != nil {
					if errors.Is(launchErr, scheduling.ErrNoSlot) {
						deferred = append(deferred, deferredLaunch{req: req, cause: launchErr})
						s.log.Warn("协调者准入无位，延迟到本轮末回填", "kind", kind,
							"card", req.Card, "node", req.Node,
							"deferred_count", len(deferred), "cause", launchErr)
						continue
					}
					s.requeueAutomation(req, kind, launchErr)
					flushDeferred()
					return processed, nil
				}
			case scheduling.KindIgnitionQueue:
				if drainErr := s.drainIgnitionRequest(ctx, req); drainErr != nil {
					s.requeueAutomation(req, kind, drainErr)
					flushDeferred()
					return processed, nil
				}
			default:
				s.log.Error("自动化清队遇到未声明 kind", "kind", kind, "card", req.Card)
				flushDeferred()
				return processed, fmt.Errorf("清队遇到未声明 kind %q", kind)
			}
		}
		if processed >= automationBatchLimit {
			break
		}
	}
	requeued := flushDeferred()
	if requeued > 0 {
		s.log.Info("协调者无位请求已在本轮末回填", "deferred_count", requeued)
	}
	return processed, nil
}
```

4. 注意：`flushDeferred` 的回填必须只在协调者 launch queue 中发生；如果实现者需要保留原始错误，应让 `requeueAutomation` 的 cause 包含原始 `launchErr`，不能只写一个新错误丢失 `coordinatorAdmissionError`。deferred flush 的失败由既有 `requeueAutomation` 记录，不得把失败吞成“成功清队”。
5. 加注释：在 `drainQueuesOnce` 入口注释写清局部 deferred 的生命周期和“只影响本轮、只针对 ErrNoSlot”；在测试文件头说明 runner 只证明机内清队接缝，不证明 agentd 重启/SQLite 多进程恢复。
6. 跑绿：`go test ./internal/agentd -run 'Scheduling|Automation|Queue' -count=1`；并运行 `go test ./internal/agentd -run '^TestSquadNode(Admits|FullQueues)' -count=1` 验证真实 dispatch seam 未被清队修改破坏。测试入口是 `drainQueuesOnce`/`startCardStep` 调用链，不增加直喂 `admitInto` 的内部锁。

### Task C：Go/HTTP/fixture/TS wire 的真实序列化闭环

**文件集：** `internal/proto/scheduling.go`、`internal/proto/contract_fixture_test.go`、`internal/agentd/schedapi.go`、`internal/agentd/schedapi_test.go`、`web/src/api/scheduling.ts`、`web/src/api/contract.test.ts`、`web/src/api/scheduling.fetch.test.ts`、`web/src/api/testdata/SquadsResp.json`。

**Consumes / Produces：** Go handler 消费 `proto.SquadInput` 并产生 `proto.SquadView`；fixture 产生 `web/src/api/testdata/SquadsResp.json`；TS API 消费/产生 §2.2 的 TS 类型和 `putSquad` 请求。

1. 判据先在基线跑：修改前运行 `go test ./internal/proto ./internal/agentd -run 'ContractFixtures|Squad|Scheduling' -count=1`，并运行 Web 三支指定测试；预期 §1.1 的原始结果，agentd 无匹配测试这一事实必须通过新增 seam 补齐。
2. 写红测并跑红，所有断言穿真实 JSON 边界：在 `schedapi_test.go` 增加 `TestSquadPutMemberPolicyRoundtripThroughWire`，复用 `newSchedEnv`/`schedReq`，先 PUT 载体 c1、c2，再 PUT squad，body 依次覆盖 `max_concurrency:2`、成员缺席、旧顶层 `max_concurrency:9`、空 members；GET raw body 逐条断言 2 保留、0/缺席键缺席、顶层键缺席、`members:[]` 非 null，然后 decode `proto.SquadsResp` 逐字段比对。另断言 ghost 仍 400、role/type/expect 分类未变、auth 仍由真实 Handler 保护。
3. 最小实现：保持 `handleSquadPut` 只做解码、路径名一致性、成员对象投影和 `PutSquad` 错误分类；不要加 `DisallowUnknownFields`，不要把旧顶层值复制给任何成员；保持 `squadView` 逐成员复制 `Carrier` 和 `MaxConcurrency`。proto Go 类型必须与 §2.2 完全相同。
4. 更新 fixture 输入：`internal/proto/contract_fixture_test.go` 的 B292 样本同时包含默认模型载体、显式 `flash` 模型载体、成员政策 2、一个缺席政策成员和空 squad；用既有 `TestContractFixtures -update` 机制生成 TS fixture。手写核对 `web/src/api/testdata/SquadsResp.json`：至少有一条 `max_concurrency:2` 和一条成员对象缺席该键；不得加入顶层 `max_concurrency`。
5. TS API 只新增同构类型和专用于 squad 的 0 省略边界；完整目标 helper 如下。它不承担非正数校验，Web 页面 Task E 承担用户输入校验；已有载体 helper 不改。

```ts
function omitZeroSquadConcurrency(input: SquadInput): SquadInput {
  return {
    ...input,
    members: input.members.map((member) => {
      if (member.max_concurrency !== 0) return member
      const copy = { ...member }
      delete copy.max_concurrency
      return copy
    }),
  }
}

/** 按小队名和 CAS 版本更新小队；成员 max_concurrency 缺席表示不限。 */
export const putSquad = (name: string, expect: number, input: SquadInput) =>
  putJSON<SquadPutResp>(
    `/api/squads/squads/${encodeURIComponent(name)}?expect=${expect}`,
    omitZeroSquadConcurrency(input),
  )
```

6. 加关键日志/注释：HTTP 成功日志增加 `member_policy_count` 和 `empty_members`，解码失败、成员缺失、CAS 冲突分支保留 `name/expect/cause`；fixture 文件头写清它是 Go→TS 真实线格式，不是 UI mock；TS helper 注释说明 0 的兼容投影而非把非法文本变合法。
7. 跑绿：Go 指定测试、`go test ./internal/agentd -run '^TestSquadPutMemberPolicyRoundtripThroughWire$' -count=1`、Web `contract.test.ts`/`scheduling.fetch.test.ts`；确认 fetch 测试断言完整 URL、expect、raw JSON key presence，而不是只断言函数被调用。

### Task D：CLI 成员语法、拒绝前置和列表投影

**文件集：** `cmd/squad.go`、`cmd/squad_test.go`。

**Consumes / Produces：** Cobra 原始重复 `--member` 参数；产生 `proto.SquadMember` 数组给 `proto.SquadInput`；消费 `proto.SquadView` 并产生表格/NDJSON。请求前解析不得触碰 `newTargetClient`。

1. 判据先在基线跑：修改前运行 `go test ./cmd -run 'Squad' -count=1`，预期 `ok github.com/Xsxdot/handoff/cmd 0.109s`；读 `stubSquadAgentd`/`setStub`/`runLedgerCLI`，确定测试可观测拨号次数和 body。
2. 写红测并跑红：通过真实 `squadCreateCmd`/`squadSetCmd` 的 Cobra 调用逐条断言：(a) `--member c1:2 --member c2` 收到 `[{"carrier":"c1","max_concurrency":2},{"carrier":"c2"}]`；(b) `c1:0`、`c1:-1`、`c1:1.5`、`c1:abc`、`c1:`、`:2`、`c1:2:3` 被本地拒绝且错误含 `--member c1[:<positive-int>]` 示例；(c) 这些拒绝不启动 stub HTTP；(d) 载体名 `A B/中文` 原样进入 `carrier`；(e) `--max-concurrency` 被 Cobra 拒绝；(f) set 读取现状后只在 `--member` 给出时整体替换并使用现状 version，否则保持成员政策；(g) list 表格显示成员 `/2` 和载体物理帽，squad 行总帽为 `-`；(h) `--json` 逐行输出成员对象，不生成顶层总帽。
3. 最小实现：导入 `strconv`；把 create/set flag 从 `StringSliceVar` 改成 `StringArrayVar`，不做逗号分割；注册同一 flag 名但使用帮助文本说明语法，不注册任何 `--max-concurrency`。在 create 的 client 创建前、set 的 GET 前（仅当 `member` changed）调用 parser。

目标 parser 必须是以下完整行为：

```go
func parseSquadMember(raw string) (proto.SquadMember, error) {
	if strings.TrimSpace(raw) == "" {
		return proto.SquadMember{}, fmt.Errorf("--member 不能为空；合法示例：--member c1 或 --member c1:2")
	}
	if strings.Count(raw, ":") > 1 {
		return proto.SquadMember{}, fmt.Errorf("--member 载体名不能含冒号；合法示例：--member c1 或 --member c1:2")
	}
	carrier := raw
	max := 0
	if i := strings.IndexByte(raw, ':'); i >= 0 {
		carrier = raw[:i]
		rawMax := raw[i+1:]
		if strings.TrimSpace(carrier) == "" {
			return proto.SquadMember{}, fmt.Errorf("--member 必须先给载体名；合法示例：--member c1:2")
		}
		value, err := strconv.Atoi(rawMax)
		if err != nil || value <= 0 {
			return proto.SquadMember{}, fmt.Errorf("--member 政策必须是正整数；留空表示不限；合法示例：--member %s:2", carrier)
		}
		max = value
	}
	return proto.SquadMember{Carrier: carrier, MaxConcurrency: max}, nil
}

func squadMemberInputs(raw []string) ([]proto.SquadMember, error) {
	members := make([]proto.SquadMember, 0, len(raw))
	for _, value := range raw {
		member, err := parseSquadMember(value)
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, nil
}
```

create 分支应先执行 `members, err := squadMemberInputs(squadMembers)` 再 `newTargetClient()`；set 分支只在 `cmd.Flags().Changed("member")` 时同样解析，再把 `in.Members = members`。错误直接 return，不发请求。

4. 加日志/注释：CLI 用 `slog.Default().Info/Warn/Error` 记录命令、成员数、政策数、拒绝原因和 request 是否已拨号；stdout 仍只出 JSON，stderr 仍出人话/结构化日志。parser helper 写为什么“StringArray 保留完整成员名”和为什么空串才表示不限的注释；不要把 0 静默转换为 unlimited。
5. 跑绿：`go test ./cmd -run 'Squad' -count=1`，再单独跑新增的 create/set/list 测试名；用 `gofmt -w cmd/squad.go cmd/squad_test.go` 和 `gofmt -l` 收口。

### Task E：Web 每成员勾选+政策输入、非法值阻断和真实保存

**文件集：** `web/src/api/scheduling.ts`、`web/src/app/settings/SchedulingPage.tsx`、`web/src/app/settings/SchedulingPage.test.tsx`、`web/src/api/scheduling.fetch.test.ts`。

**Consumes / Produces：** 页面消费 `SquadsResp` 的 carriers/squads 和 `SquadMember`；产生 `SquadInput` 给 `putSquad`；保留现有 CAS expect、保存失败 modal/draft、成功后 reload。

1. 判据先在基线跑：修改前运行 `cd web && npm run typecheck` 与三支指定测试；预期 §1.1 的 typecheck 0、3 files/49 tests passed。读 `SchedulingPage.tsx:45-50,112-158,181-213` 和 prototype `prototypes/b292-squad-concurrency/pages/settings.html:233-304,439-500`。
2. 写锁缝失败测试并跑红：复用 `SchedulingPage.test.tsx` 的 RTL/user-event setup，不直测 helper。逐条断言：(a) 打开 squad modal 后每个 carrier 一行 checkbox、模型元信息（空 model 显示 CLI 默认，非空显示模型）和政策输入；(b) 勾选 c1 输入 `2`、勾选 c2 留空，点击保存，mock fetch body 为成员对象且只有 c1 `max_concurrency:2`；(c) 取消勾选禁用/移除该成员输入并不提交；(d) 输入 `0`、`-1`、`1.5`、`abc` 和超过安全整数范围的文本均显示含合法示例的 alert，保存不调用 `putSquad`；(e) 保存 409/网络错误后 modal、草稿和错误文本仍在；(f) 成功以服务端 GET 真值刷新，卡片显示 `/2`，没有队级总帽文本/输入；(g) 空队显示合法空队提示。
3. 最小实现：扩展 squad draft 保存原始输入文本，避免浏览器把非法文本先变成 0 或 NaN。目标类型和解析函数如下；非空仅接受 ASCII 正整数并限制 `Number.isSafeInteger`，空串返回 undefined。

```ts
type SquadDraft = Omit<SquadInput, 'name'> & {
  name: string
  memberConcurrencyText: Record<string, string>
}

function squadDraft(row: SquadView | null): SquadDraft {
  const members = row ? row.members.map((member) => ({ ...member })) : []
  const memberConcurrencyText: Record<string, string> = {}
  for (const member of members) {
    memberConcurrencyText[member.carrier] = member.max_concurrency?.toString() ?? ''
  }
  return {
    name: row?.name ?? '',
    role: row?.role === 'coordinator' ? 'coordinator' : 'executor',
    members,
    memberConcurrencyText,
  }
}

function parseSquadPolicy(raw: string): number | undefined {
  if (raw === '') return undefined
  if (!/^[1-9][0-9]*$/.test(raw)) {
    throw new Error('小队成员政策必须是正整数；留空表示不限；合法示例：2')
  }
  const value = Number(raw)
  if (!Number.isSafeInteger(value)) {
    throw new Error('小队成员政策超出安全整数范围；请使用较小的正整数或留空表示不限')
  }
  return value
}

function squadMembersForSave(draft: SquadDraft): SquadMember[] {
  return draft.members.map((member) => {
    const max = parseSquadPolicy(draft.memberConcurrencyText[member.carrier] ?? '')
    return max === undefined ? { carrier: member.carrier } : {
      carrier: member.carrier,
      max_concurrency: max,
    }
  })
}
```

成员行采用原型的每行 checkbox + number/text input 布局；checkbox 未选中时 input `disabled`，已登记载体的 model 空值显示“CLI 默认”，显式值显示模型名。输入变化只更新 `memberConcurrencyText`；checkbox 变化更新 `members` 和对应 text，不直接把非法文本写成 number。

4. 保存分支在调用 `putSquad` 前执行 `const members = squadMembersForSave(squad)`；捕获解析错误时设置 `saveError`、记录 `scheduling.save.validation`，return 且不进入 saving/网络调用。成功 body 只传 `{name, role, members}`，沿用现有 `expect`；失败日志补 `member_count` 和 `policy_count`，保留 modal/draft。
5. 加注释/可观测性：在 draft/parser/builder 上写参数、返回、空值语义和保留原始文本的原因；沿用现有 `console.info/warn/error` 结构化事件名，在 load/save/validation/error 成功路径填入 `name`、`expect`、`member_count`，不能静默 return。
6. 跑绿：`cd web && npm run typecheck`；`npm test -- --run src/app/settings/SchedulingPage.test.tsx src/api/scheduling.fetch.test.ts src/api/contract.test.ts`；再检查 fixture 请求 body 中 0/缺席字段的 key presence。此 task 的页面测试入口穿过 `SchedulingPage`，helper 断言只能作为附加测试。

## 4. 组合门禁、序列化边界与变异复验

### 4.1 序列化边界逐点锁定

字段 `MaxConcurrency/max_concurrency` 的产生→消费路径必须逐点覆盖：

1. `internal/scheduling/scheduling.go` 的 `Squad.UnmarshalJSON`、`Squad` encode 和 registry body：旧字符串兼容、新对象、旧顶层键丢弃。
2. `internal/agentd/schedapi.go` 的 `handleSquadPut` 成员投影和 `squadView` 输出投影。
3. `internal/proto/scheduling.go` 的三种成员对象类型与 `omitempty`。
4. `internal/proto/contract_fixture_test.go` 写出的 Go fixture → `web/src/api/testdata/SquadsResp.json`。
5. `cmd/squad.go` parser、set 编辑回路和 `formatSquadMembers`，由 stub HTTP 检查真实 body。
6. `web/src/api/scheduling.ts` 的 `putSquad` 和 `omitZeroSquadConcurrency`。
7. `SchedulingPage.tsx` draft 原文、save builder 和真实 user-event 页面。

至少一支 Go HTTP 测试要穿过 registry JSON→HTTP PUT/GET→proto JSON；至少一支 CLI stub 测试要穿参数→HTTP body；至少一支 Web fetch/page 测试要穿 input→fetch body。所有三条都用 `2`、缺席、raw `0`/非法值分别断言，不能用“两端各自测试通过”代替跨边界回归。

### 4.2 缺陷族对抗审查结论

- 生命周期/状态机：清队只回填当前协调者 `ErrNoSlot` 并继续；非该错误仍停止既有路径；Release defer 不变。agentd 重启窗口、重复消费、计数泄漏仍标“未验证，需真机”。
- 静默失败/误导报错：`ErrNoHealthy`、`ErrNoSlot`、`ErrRetryExhausted` 分开；CLI/Web 非正值有示例并在网络前拒绝；回填失败保留 cause 和 requeue error；旧顶层键忽略但不生效。
- 跨平台：计数键和 JSON 不依赖 OS；shell quoting、number input、中文/斜杠载体名、Chromium/WKWebView 差异标“未验证，需真机”。
- 假红/假绿：准入测试从公开 `Admit/LaunchAdmit/Release`，清队测试从真实 queue/drain，HTTP 从真实 Handler，CLI 从真实 Cobra，Web 从 RTL user-event；helper 只能辅助，不能顶替接缝。
- 门禁绕过：不新增公开 Pop/selector、不直写 registry、不让 CLI/Web 绕过 auth/expect/PutSquad；全量门禁必须最后执行，分域失败原文写 ledger。

### 4.3 变异复验清单

实现完成后逐项临时改动并运行对应最小测试，恢复改动后再跑绿；若没有真实翻红，ledger 写“未验证”，不得写通过：

1. 删除成员上界判断：Task A 的成员满/后序成员用位测试必须翻红。
2. 删除载体物理上界判断：两队共享 c1 的总成功数测试必须翻红。
3. 把成员键改成 `sched_running/squad/<队>`：raw registry 键断言必须翻红。
4. 把空模型与显式模型按 CLI 合池：Binding/物理键隔离断言必须翻红。
5. 将协调者 `ErrNoSlot` 分支改回直接 return：Task B 的 B 载体执行者同轮通过断言必须翻红。
6. 删除旧顶层键负向断言或将其复制到成员：HTTP/fixture roundtrip 必须翻红。
7. 删除 Web 每成员输入或将非法值归零：SchedulingPage user-event 测试必须翻红。

### 4.4 组合命令

按顺序运行并把每条原始输出写入 ledger：`go test ./internal/scheduling -count=1`、`go test ./internal/agentd -run 'Scheduling|Automation|Queue' -count=1`、`go test ./internal/proto ./internal/agentd -run 'ContractFixtures|Squad|Scheduling' -count=1`、`go test ./cmd -run 'Squad' -count=1`、`go build ./...`、`go vet ./internal/scheduling ./internal/agentd ./internal/proto ./cmd`、`cd web && npm run typecheck`、`cd web && npm test -- --run src/app/settings/SchedulingPage.test.tsx src/api/scheduling.fetch.test.ts src/api/contract.test.ts`、`go test ./... -count=1`、`cd web && npm test -- --run`、`git diff --check`。

全量 Go/Web 测试只有此组合阶段运行；任何失败必须把命令和原始报错逐字追加 ledger，不替它归因。

## 5. 真机清单（本地测试不得冒充通过）

以下均为“未验证，需协调者真机执行”，不纳入本地 pass：

1. agentd 在 PopReady 删除后、协调者 `LaunchAdmit` 返回 `ErrNoSlot`、deferred 回填中、部分计数已占用和 defer Release 临界点重启，确认请求不丢不重、计数不永久泄漏。
2. 两台真实载体 A/B 的空位适配：A 无位、B 有位时，协调者只优先 A 上可用请求，A 等待不阻断 B 执行者，不抢占运行任务。
3. 同 CLI 空模型与显式模型真实运行，确认空模型跟随 CLI 默认、显式模型钉住，两载体不共享物理计数；不宣称解决账户池额度。
4. 多进程 agentd/调用者并发登记、准入、释放，观察 SQLite/registry CAS 和文件锁没有超发窗口。
5. Linux/macOS/Windows shell 运行重复 `--member`，覆盖空格、斜杠、中文、冒号边界、引号和错误退出码。
6. Chromium 与项目支持的 WKWebView/Wails 容器填写成员政策、保存、刷新、并发编辑，确认 number/text input、fetch、409 和草稿保留。
7. 未登录、过期会话、合法会话访问 `/api/squads`，确认新增/修改只能走既有 auth/hostGuard/expect 门。

## 6. 计划自检与收口责任

- 规格覆盖：用户故事“成员级政策/跨队物理封顶”归 Task A；“协调者空位优先且 ErrNoSlot 不阻断其它载体”归 Task B；“Go/HTTP/TS wire 与旧数据兼容”归 Task C；“CLI 语法和本地拒绝”归 Task D；“Web 每成员行和非法输入拒绝”归 Task E；组合门禁与真机边界归 §4–§5。
- 占位符扫描：本计划不得出现未声明的骨架词、跨 task 代称、泛化错误处理语句或未声明的条件退路；测试复用既有 harness 的形态例外已在 §2.3 明确列出文件、函数和逐条断言。
- 类型/签名检查：逐字符核对 §2.2 的 Go/TS 类型与 contract §2；跨 task 仅通过已有公开签名、真实 wire 和指定 parser/helper 连接。
- 接缝双向检查：§2.3 每条 S1–S7 均有缝级测试入口；每支测试计划均从该入口或调用链穿缝；内部 helper 断言不能顶替接缝级断言。
- 边界型真机清单：§5 明列 agentd 重启、多进程、shell、浏览器/webview、认证/权限和模型身份，均不在本地测试中冒写 pass。
- 计划节点收口命令由当前执行者亲跑：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/plans/b292-plan.md --view cards-B292-charter`、`git diff --check`、暂存区范围检查、最终 `git status --short --branch`。本节点不派发、不调用 handoff CLI、不实现源码。
