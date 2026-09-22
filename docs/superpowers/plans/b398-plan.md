# B398 plan：冻结物理身份与路由 target 分字段——派发链不得改写冻结身份

> 卡 B398 · 入口节点 charter:plan · spec `docs/superpowers/specs/b398.md`（已批准）
> 基线分支 `cards/B398-charter-3`，起手 HEAD `9ddd7dbe`（本节点未做任何合并；spec 台账同基线）。
> 凡引用行号者动手前重核，漂了以符号为准。
> 台账 `docs/superpowers/ledgers/2026-09-22-b398-plan-ledger.md`（含全部亲跑命令、
> 红诊断原始输出、图查询记录）。
> **本节点只出计划，不写实现**。「未验证」= 本节点没亲自跑到结果，实现卡必须自己复核。
> 读者假设：对 handoff 仓零上下文的执行者。

---

## 0. 待拍板清单

无阻塞岔口。字段命名已由 spec §5 下放本节点，本计划定 `FrozenTarget`（Go）/ `frozen_target`（wire），
不需要人裁决；读完本计划即可直接实现。

---

## 1. 根因（证据驱动，全部亲跑或亲读；原始输出见台账 §1.3）

### R1（根因，决定性）：路由值与冻结物理身份共用 `DispatchOpts.Target`，归一路径把身份折成本机空串

链路逐点（对着 `9ddd7dbe` 现状，行号为现状读数）：

1. `Select` 正确冻结 `binding.Target=linux-01`（`internal/scheduling/scheduling.go:598` `bindingFor`）；
2. 装配点取用冻结值：`internal/agentd/cardstep.go:175-178`
   （`target := req.Target; if binding.Target != "" { target = binding.Target }`）；
3. `cardstep.go:186` `resolved, target, err := s.resolveStepDiscipline(node, target)` 把 `target`
   换成 canonical（别名→`""`）；
4. **回写掩盖**：`cardstep.go:232-239` 的 squad 分支又把原始值写回
   `runner.Target = binding.Target`（`linux-01`），所以第 3 步的改写被掩盖；
5. **真丢值点**：`internal/ledgerstep/dispatch.go:169-173`
   `rawTarget := target; if d.NormalizeTarget != nil { target = d.NormalizeTarget(target) }`
   ——`rawTarget` 只进日志（`:178`），`DispatchOpts.Target` 拿到的是 `""`；
6. 透传载荷 `dispatch.go:341-356` 只有 `Target`，**没有独立冻结字段**，身份快照无处安放；
7. `stepTransport`（`cardstep.go:387-398`）本身是对的：`canonical` 只决定 client，
   `opts.Carrier != ""` 时 wire 用 `opts.Target`——但它拿到的已是空串；
8. 接收端 `handlers.go:527-531` 用 `req.Target`（`""`）重建 `Binding` 交给 `AdmitFrozen`；
   `scheduling.go:667-674` 比对 `samePhysicalMachine("", identity.Machine)`，
   `samePhysicalMachine`（`:701-708`）只认「两边都 `IsLocalMachine`」或字符串相等
   ⇒ `ErrRoleMismatch: 冻结物理身份与载体登记不一致`。

**亲跑复现（本节点，诊断测试跑完已删，原始输出见台账 §1.3）**：

- 装配路径日志：
  `准备派发节点 target=linux-01` → `模板派发目标已归一 raw_target=linux-01 target=""`
  → `Dispatch 进入 ... target=""`；
- 整条装配+stepTransport+本机 HTTP+handleDispatch 的 E2E：
  `本机别名冻结派发未形成 task（len=0）`，卡上落
  `本节点派发失败：\n派发: dispatch: 状态码 400: {"error":"scheduling: 小队角色不符: 冻结物理身份与载体登记不一致"}`；
- 接收端直投：带 `frozen_target` → `400`；只带 `target:"linux-01"` → `200`。

> spec §1 点 3 把丢值点标在 `cardstep.go:186`；本节点复核发现该处被 `:236` 回写掩盖，
> **真正折空在 `dispatch.go:169-173` 的 `NormalizeTarget`**。修向不变：两条链路都拆字段。

### R2：接收端把路由字段当唯一身份来源

`handlers.go:527-531` 只读 `req.Target` 建 `Binding`，wire 上没有第二个可承载冻结身份的位置，
所以第 5 步一旦折空，接收端无从还原。

---

## 2. 分流决定

| 根因 | 归属 | 处置 |
|---|---|---|
| R1 路由值与冻结身份共用 `Target` | 本卡，L2 / `internal/ledgerstep`+`internal/agentd`+`internal/client` | **T2**：新增独立冻结字段 `FrozenTarget`/`frozen_target`，装配点从 `binding.Target` 填，`DispatchOpts` 原样透传 |
| R2 接收端只读 `req.Target` | 本卡，同一条链第二段 | **T2**：`handlers.go` 有冻结字段用它、缺席回落 `req.Target` |
| `AdmitFrozen`/`samePhysicalMachine` 判据本身 | 互斥/身份语义面 | **不在本卡**（spec §7 Out of Scope，用户已否 A/B） |
| 存量盘点（还有多少冻结绑定被改写） | 后续只读取数 | **不在本卡**（spec §7，残余落 roadmap） |
| 真机重放（spec §6 接缝 3 的真机面） | 协调者执行 | 见 §11；本节点不派发 |

---

## 3. 任务 DAG

```
T1（三条缝的回归测试落仓：接缝 2/3 今天红，旧发送方回落绿作回归锁）
  └→ T2（拆字段实现 + 接缝 1 断言 + 序列化/投影断言，T1 全绿）

（独立）AdmitFrozen 判据放松、存量盘点 → 回 spec / 另立卡，不在本 DAG
```

次序承重：T1 的红必须先出现，才能证明 T2 的字段拆分是绿的原因；T1 只含能引用既有符号的
断言（接缝 2/3），接缝 1 断言引用新增字段，只能随 T2 一起落——因此 T1 是本卡的**最薄可跑路径**
（E2E 行为今天红，点亮它即点亮全卡行为）。

---

## 4. 基线事实（实现卡共享，动手前复核；原始输出见台账 §1.1/§1.2/§1.3）

**亲跑读数**：

- `go build ./...` → `BUILD_EXIT=0`；`go vet ./internal/agentd/ ./internal/ledgerstep/ ./internal/client/ ./internal/scheduling/ ./cmd/` → `VET_EXIT=0`。
- `go test ./internal/ledgerstep/ ./internal/client/ ./internal/scheduling/ -count=1 -timeout 300s` → 三包全 `ok`（13.9s/13.4s/6.2s）。
- `go test ./internal/agentd/ -run 'TestB23310|TestB23327|TestSquadNode|TestLocalStep|TestCanonicalTarget|TestCardStep|TestStartCardStep|TestReceiver' -count=1 -timeout 300s` → `ok ... 13.190s`。
- 红回路基线：见 §1 R1（诊断 3 FAIL，`len=0`）。

**预存在 flake（非本卡引入，勿误判为回归）**：`internal/agentd` 全量跑时
`TestPtyWSAttachedBacklogBytesKeyPresent` 偶发失败（B396 plan §4 记录）。本卡测试范围不含它。

**库/框架行为事实（带出处）**：

- `config.IsSelfTarget(listen, target)`：地址与 listen/loopback 相符即本机
  （`internal/config/listenclass.go:80-105`）。
- `IsLocalMachine`：空串/`local`/`本机`/当前 hostname 算本机（`internal/scheduling/scheduling.go:182-189`）。
- `client.Dispatch` 手搭请求体 map（`internal/client/client.go:763-786`）——新增字段的投影点。
- `DispatchOpts.Target` 与 `client.DispatchOpts.Target` 同名不同类型，两处在
  `internal/ledgerstep/dispatch.go:27` 与 `internal/client/client.go:713`，勿混。
- 本机别名场景（配置里 `linux-01` 的 Addr 指向本进程 Listen）：
  `CanonicalTarget("linux-01")==""` 且接收端闸 `errReceiverNotCarrierMachine` 放行
  （`TestB23327SelfTargetNameStillDispatches` 已锁）。

**现状签名与调用面（`codegraph sym` 命中；图覆盖债见 §12）**：

- `Dispatcher.ViaTemplate(ctx, c ledger.Card, req TemplateDispatch) (DispatchResult, error)`
  — `internal/ledgerstep/dispatch.go:155`；
- `Dispatcher{St, Transport, Compensate, Actor, HomeDir, Carrier, Squad, NormalizeTarget, DisciplineText, DisciplineVersion}`
  — `internal/ledgerstep/dispatch.go:84-108`；
- `ledgerstep.DispatchOpts`（大 struct）— `internal/ledgerstep/dispatch.go:27-51`；
- `client.DispatchOpts` — `internal/client/client.go:713-753`；
- `Server.startCardStep(cardID string, req proto.CardStepReq) error` — `internal/agentd/cardstep.go:128`；
- `dispatchStep(ctx, cl client.ExecutionClient, opts ledgerstep.DispatchOpts, target string) (*proto.Task, error)`
  — `internal/agentd/cardstep.go:60`；
- `Server.stepTransport(ctx, opts ledgerstep.DispatchOpts) (string, string, error)` — `internal/agentd/cardstep.go:387`；
- `Server.handleDispatch(w, r)` — `internal/agentd/handlers.go:497`；
- `scheduling.Service.AdmitFrozen(binding Binding) (Binding, error)` — `internal/scheduling/scheduling.go:618`。

**签名锁（改动不得破坏，编译期判据）**：
`internal/agentd/execution_signature_lock_test.go:18,23` 钉
`dispatchStep` 与 `(*Server).stepTransport` 的签名——本卡只读 `opts.FrozenTarget`，不动签名。
`internal/ledgerstep/execution_signature_lock_test.go:19` 钉 `StepRunner.Clients` 类型——不动。

**既有测试夹具（T1/T2 复用，签名逐字）**：

- `setupB23310CardTaskEnvWithMachine(t *testing.T, script []fake.Step, machine string) *ledgerEnv`
  — `internal/agentd/cardstep_test.go:222`（把载体 c1 的 Machine 换成 `machine` 并把该名写进 targets 指向本进程）；
- `seedSquadFlow(t *testing.T, env *ledgerEnv, squad string, count int) []string`
  — `internal/agentd/scheddispatch_test.go:38`；
- `newReceiverTestEnv(t) *receiverTestEnv` — `internal/agentd/receiver_dispatch_test.go:33`；
- `postDispatch(t, srv *Server, body string) *httptest.ResponseRecorder` — `internal/agentd/receiver_dispatch_test.go:62`；
- `dispatchBody(projectID, extra string) string` — `internal/agentd/receiver_dispatch_test.go:71`；
- `decodeDispatchTask(t, rr) proto.Task` — `internal/agentd/receiver_dispatch_test.go:75`；
- `putTarget(t, srv *Server, name, addr string)` — `internal/agentd/receiver_dispatch_test.go:111`；
- `putOnlineCarrier(t, svc *scheduling.Service, carrier scheduling.Carrier)` — `internal/agentd/coordapi_test.go:121`；
- `runAction` / `actionRequest` — `internal/agentd/receiver_occupancy_test.go:54,60`；
- `ledgerPost(t, env *testAgentdEnv, path, body string) (int, string)` — `internal/agentd/ledgerapi_test.go:44`；
- `waitFor(t, predicate func() bool)` — `internal/agentd/cardstep_test.go:88`；
- `swapDispatchTransportWithOpts(fn func(dispatchRequest) (string, string, error)) func()` — `cmd/card_dispatch.go:155`；
- `testhttp.NewServer(t, handler)` — `internal/testhttp/server.go`（必须用它，见 §6 族 5）。

---

## 5. 接口契约（执行者只看本 task，故这里列全）

### Consumes（本卡要用的既有签名，一字不改）

```go
// internal/scheduling/scheduling.go
type Binding struct {
	Squad, Carrier, Target, Executor, Model, HomeDir string
}
func (s *Service) AdmitFrozen(binding Binding) (Binding, error)

// internal/agentd/cardstep_squad（形态）
func (s *Server) admitSquadStep(cardID string, req proto.CardStepReq, node ledger.NodeDef) (scheduling.Binding, squadDispatchOutcome, error)

// internal/ledgerstep/runner.go
func (r *StepRunner) Run(ctx context.Context, cardID, nodeName string) (Outcome, error)

// internal/proto
type CardStepReq struct {
	Step, Target, Executor, Model, Extra, Actor string
}
```

### Produces（本卡新增，跨 task 逐字对齐）

```go
// internal/ledgerstep/dispatch.go
// DispatchOpts 与 Dispatcher 各新增一个字段：
FrozenTarget string
// ViaTemplate 把 d.FrozenTarget 原样写进 DispatchOpts.FrozenTarget（不归一、不校验）。

// internal/client/client.go
type DispatchOpts struct {
	// …既有字段…
	FrozenTarget string
}
// Dispatch 在 opts.FrozenTarget != "" 时写 body["frozen_target"]。

// internal/agentd/handlers.go
type dispatchRequest struct {
	// …既有字段…
	FrozenTarget *string `json:"frozen_target,omitempty"`
}
// handleDispatch 在 req.Carrier != "" 分支：req.FrozenTarget != nil 时用它建
// Binding.Target，否则回落 req.Target。

// internal/agentd/b398_frozen_target_test.go（新文件）
func TestB398FrozenCarrierLocalAliasDispatches(t *testing.T)      // 接缝 3（T1）
func TestB398ReceiverUsesFrozenTargetForFrozenAdmission(t *testing.T) // 接缝 2（T1）
func TestB398ReceiverLegacyTargetFallback(t *testing.T)           // 接缝 2 回归锁（T1）
func TestB398ReceiverEmptyFrozenTargetIsTakenVerbatim(t *testing.T) // 接缝 2 缺失/零值分辨（T2）
func TestB398AssemblySeparatesFrozenIdentityFromRoute(t *testing.T) // 接缝 1（T2）

// internal/client/client_test.go（追加）
func TestDispatchSerializesFrozenTargetPresence(t *testing.T)     // 投影点 1（T2）

// cmd/card_dispatch_test.go（追加）
func TestCliTransportForwardsFrozenTarget(t *testing.T)           // 投影点 2/3（T2）
```

---

## 6. 任务详情

### T1 红色回路：接缝 2/3 的回归断言落仓（先红）

**动作**：新建 `internal/agentd/b398_frozen_target_test.go`，内容如下（完整，无占位）。
本节点已在基线实跑红色（原始输出见台账 §1.3）：

```go
// b398_frozen_target_test.go —— B398 冻结物理身份与路由 target 分字段的缝级回归。
//
// 职责：锁住「冻结载体的原始机器名（Binding.Target）不被路由归一改写，原样到达
// 接收端并被 AdmitFrozen 采用」，以及「旧发送方不带冻结字段时行为与今天逐字一致」。
// 缝：
//   - 接缝 3（端到端）：HTTP `POST /api/cards/{id}/step` → startCardStep → runner.Run →
//     ViaTemplate → stepTransport → 本机 HTTP `POST /api/tasks` → handleDispatch；
//   - 接缝 2（接收端准入）：`POST /api/tasks` 的冻结分支。
// 边界：不复制 AdmitFrozen/samePhysicalMachine 的判据；不直调 stepTransport 冒充接缝 3。
package agentd

import (
	"net/http"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// T2 追加两个测试后会补 import：context、time、ledgerstep、proto。

// TestB398FrozenCarrierLocalAliasDispatches 接缝 3：本机别名（配置里 linux-01 指向
// 本 agentd）作冻结载体机器时，整条装配→派发→接收准入必须成功形成 task，且任务
// 物理身份仍是原名 linux-01。基线红：NormalizeTarget 把冻结值折成空串，接收端
// AdmitFrozen 报「冻结物理身份与载体登记不一致」。
func TestB398FrozenCarrierLocalAliasDispatches(t *testing.T) {
	env := setupB23310CardTaskEnvWithMachine(t, []fake.Step{{Finish: executor.Result{OK: true}}}, "linux-01")
	cardID := seedSquadFlow(t, env, "sq1", 1)[0]

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/step",
		`{"step":"implement","actor":"cli:t@h#1"}`)
	if code != http.StatusAccepted {
		t.Fatalf("本机别名冻结节点应受理（202），实得 %d（%s）", code, body)
	}
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })
	links, err := env.ledger.TasksOf(cardID)
	if err != nil {
		t.Fatalf("读卡挂账: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("本机别名冻结派发应形成 1 条 task，实得 %d；卡事件=%s",
			len(links), b398CardEvents(t, env, cardID))
	}
	task, err := env.st.GetTask(links[0].TaskID)
	if err != nil {
		t.Fatalf("读回 task: %v", err)
	}
	if task.Target != "linux-01" {
		t.Fatalf("冻结任务物理身份 = %q，want linux-01", task.Target)
	}
}

// TestB398ReceiverUsesFrozenTargetForFrozenAdmission 接缝 2：新发送方（路由字段空、
// 冻结字段带原名）必须用冻结字段建 Binding 并准入通过。基线红：字段缺席于协议，
// 接收端只读 target=""，AdmitFrozen 报角色不符。
func TestB398ReceiverUsesFrozenTargetForFrozenAdmission(t *testing.T) {
	env := newReceiverTestEnv(t)
	putTarget(t, env.srv, "linux-01", env.srv.conf().Listen)
	putOnlineCarrier(t, mustScheduling(t, env.srv), scheduling.Carrier{
		Name: "self-box", Machine: "linux-01", CLI: "fake", HomeDir: "",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
		Status: scheduling.StatusOnline,
	})
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID,
		`,"carrier":"self-box","target":"","frozen_target":"linux-01","executor":"fake","home_dir":""`))
	if rr.Code != http.StatusOK {
		t.Fatalf("带冻结字段的冻结派发应 200，实得 %d（%s）", rr.Code, rr.Body.String())
	}
	task := decodeDispatchTask(t, rr)
	if task.Carrier != "self-box" || task.Target != "linux-01" {
		t.Fatalf("任务身份 = carrier:%q target:%q，want self-box/linux-01", task.Carrier, task.Target)
	}
	stop := runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
	if stop.Code != http.StatusOK {
		t.Fatalf("清理冻结任务返回 %d: %s", stop.Code, stop.Body.String())
	}
}

// TestB398ReceiverLegacyTargetFallback 接缝 2 回归锁：旧发送方不带 frozen_target，
// 行为必须与今天逐字一致（用 req.Target 建 Binding 并准入通过）。基线即绿。
func TestB398ReceiverLegacyTargetFallback(t *testing.T) {
	env := newReceiverTestEnv(t)
	putTarget(t, env.srv, "linux-01", env.srv.conf().Listen)
	putOnlineCarrier(t, mustScheduling(t, env.srv), scheduling.Carrier{
		Name: "self-box", Machine: "linux-01", CLI: "fake", HomeDir: "",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
		Status: scheduling.StatusOnline,
	})
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID,
		`,"carrier":"self-box","target":"linux-01","executor":"fake","home_dir":""`))
	if rr.Code != http.StatusOK {
		t.Fatalf("旧发送方冻结派发应 200，实得 %d（%s）", rr.Code, rr.Body.String())
	}
	task := decodeDispatchTask(t, rr)
	if task.Target != "linux-01" {
		t.Fatalf("旧发送方任务物理身份 = %q，want linux-01", task.Target)
	}
	stop := runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
	if stop.Code != http.StatusOK {
		t.Fatalf("清理旧发送方任务返回 %d: %s", stop.Code, stop.Body.String())
	}
}

// b398CardEvents 把卡的事件流拼成一行，供失败时保留现场。
func b398CardEvents(t *testing.T, env *ledgerEnv, cardID string) string {
	t.Helper()
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 100)
	if err != nil {
		return "读事件失败: " + err.Error()
	}
	out := ""
	for _, e := range events {
		out += "\n  " + string(e.Type) + " " + string(e.Payload)
	}
	return out
}

var _ = proto.Task{}
```

- **Interfaces**
  - Consumes：见 §4 夹具清单（全部既有符号）。
  - Produces：本文件三个测试（接缝 2/3）；T2 再补接缝 1 测试与空值分辨测试。
- **步骤**
  1. 落文件。
  2. 跑红：`go test ./internal/agentd/ -run 'TestB398FrozenCarrierLocalAliasDispatches|TestB398ReceiverUsesFrozenTargetForFrozenAdmission|TestB398ReceiverLegacyTargetFallback' -count=1 -timeout 120s`
     → 预期：前两支 FAIL、`TestB398ReceiverLegacyTargetFallback` PASS（回归锁基线即绿）。
     本节点已在基线亲跑同形诊断（E2E FAIL + 接收端带 frozen_target 400 / 旧 target 200，原文见台账 §1.3）。
     把原文抄进实现台账。
- **测试范围声明**：只跑 `./internal/agentd/`（本 task 只新增测试文件）。
- **日志与注释**：文件头写职责/边界/缝；每个测试头写红/绿/为什么必须穿装配路径；
  `b398CardEvents` 写 why（失败时保留卡事件现场）。
- **占位符扫描**：本 task 代码块完整，无占位。

### T2 实现：拆「路由 target」与「冻结物理身份」，点亮三条缝

**文件**：`internal/ledgerstep/dispatch.go`、`internal/agentd/cardstep.go`、
`internal/client/client.go`、`internal/agentd/handlers.go`、`cmd/card_dispatch.go`，
以及测试追加：`internal/agentd/b398_frozen_target_test.go`（接缝 1 + 空值分辨）、
`internal/client/client_test.go`（投影点 1）、`cmd/card_dispatch_test.go`（投影点 2/3）。

#### 改动一：`internal/ledgerstep/dispatch.go` —— `DispatchOpts` 新增冻结字段

在 `DispatchOpts` 的 `HomeDir *string` 行之后新增：

```go
	// FrozenTarget 是卡节点冻结的执行物理身份（Binding.Target 的原始机器名）。
	// 与 Target 分工：Target 是路由值，可被 NormalizeTarget 折成本机空串；
	// FrozenTarget 是身份快照，原样透传，不解析、不归一、不校验（B398）。
	// 空=普通派发或旧调用。
	FrozenTarget string
```

#### 改动二：`internal/ledgerstep/dispatch.go` —— `Dispatcher` 新增冻结字段

在 `Dispatcher` 的 `Carrier string` / `Squad string` 两行之后新增：

```go
	// FrozenTarget 是卡节点起源侧已冻结的执行物理身份（Binding.Target 原始机器名）。
	// 与 ViaTemplate 的 Target 分工：Target 参与路由归一，FrozenTarget 不归一，
	// 原样进入 DispatchOpts 供接收端 AdmitFrozen（B398）。空=普通派发或旧调用。
	FrozenTarget string
```

#### 改动三：`internal/ledgerstep/dispatch.go` —— `ViaTemplate` 透传 + 日志三读数

1. `Transport(ctx, DispatchOpts{...})` 里，`Squad: d.Squad,` 之后新增：

```go
		FrozenTarget: d.FrozenTarget,
```

2. 「模板派发目标已归一」日志（`:177-178`）三个读数：

```go
	slog.Default().Info("模板派发目标已归一", "card", c.ID, "template", req.Template,
		"raw_target", rawTarget, "target", target, "frozen_target", d.FrozenTarget)
```

3. 「按模板派发」日志（`:325` 起）在 `"carrier", d.Carrier, "squad", d.Squad,` 后加
   `"frozen_target", d.FrozenTarget,`；「模板派发完成」日志（`:410` 起）同加一项。

#### 改动四：`internal/agentd/cardstep.go` —— 装配点填冻结字段

1. `Dispatcher{...}` 字面量里，`Squad: binding.Squad,` 之后新增：

```go
			FrozenTarget:      binding.Target,
```

2. `dispatchStep` 的 `client.DispatchOpts{...}` 映射里，`HomeDir: opts.HomeDir,` 之后新增：

```go
		FrozenTarget:      opts.FrozenTarget,
```

（`dispatchStep` 只做镜像，见其注释；`internal/agentd/execution_signature_lock_test.go:18`
的签名锁要求参数类型不变，本改动不触。）

3. `stepTransport`（`:387`）**不改选路逻辑**（`dispatchTarget` 仍按既有规则取
   `canonical`/`opts.Target`），只补两个日志读数：开头的
   `s.log.Info("agentd 节点派发请求", ...)` 与末尾 `s.log.Info("agentd 节点派发已受理", ...)`
   各加 `"frozen_target", opts.FrozenTarget,`。
   冻结身份**只走新字段**（经改动四.2 的 `dispatchStep` 映射进 `client.DispatchOpts.FrozenTarget`），
   绝不写回 wire 的路由字段 `target`——两值在传输层重新合并就退回本卡要修的根因。

4. `startCardStep` 的「卡节点装配完成」日志（`:228-231`）加
   `"frozen_target", binding.Target,`（三读数：`target`=请求值、`canonical_target`、`frozen_target`）。

#### 改动五：`internal/client/client.go` —— `DispatchOpts` 新增字段并序列化

1. `DispatchOpts` 的 `Carrier string` / `Squad string` / `HomeDir *string` 之后新增：

```go
	// FrozenTarget 是起源侧已冻结的执行物理身份机器名（B398）；空=普通派发。
	// 与 Target 分工：Target 是路由值（客户端拿它选路），FrozenTarget 是身份快照，
	// 原样进 wire 的 frozen_target，接收端据此做冻结准入。
	FrozenTarget string
```

2. `Dispatch` 请求体拼装（`:763-786`）里，`HomeDir` 条件块之后新增：

```go
	if opts.FrozenTarget != "" {
		body["frozen_target"] = opts.FrozenTarget
	}
```

#### 改动六：`internal/agentd/handlers.go` —— 接收端冻结分支优先用冻结字段

1. `dispatchRequest` 的 `HomeDir *string` 之后新增：

```go
	// FrozenTarget 是 B398 的冻结物理身份字段（原始载体机器名）；缺席=旧发送方，
	// 接收端回落 req.Target。用指针区分「字段缺席」与「显式空串」：带 Carrier 的
	// 请求里显式空串按快照丢失处理（不静默回落），使发送方 bug 保持可见。
	FrozenTarget *string `json:"frozen_target,omitempty"`
```

2. `handleDispatch` 的 Carrier 分支（`:520-534`）改为：

```go
		} else {
			homeDir := *req.HomeDir
			frozenTarget := req.Target
			if req.FrozenTarget != nil {
				frozenTarget = *req.FrozenTarget
			}
			binding, err = s.scheduling.AdmitFrozen(scheduling.Binding{
				Squad: req.Squad, Carrier: req.Carrier, Target: frozenTarget,
				Executor: req.Executor, Model: req.Model, HomeDir: homeDir,
			})
			s.log.Info("dispatch 冻结身份准入", "project", req.ProjectID, "receiver", req.Receiver,
				"carrier", req.Carrier, "squad", req.Squad, "route_target", req.Target,
				"frozen_target", frozenTarget,
				"executor", req.Executor, "model", req.Model, "error_kind", "frozen_admit")
		}
```

3. 函数开头的「dispatch 请求身份」日志（`:511-513`）加 `"frozen_target", req.FrozenTarget,`。

#### 改动七：`cmd/card_dispatch.go` —— CLI Transport 投影补齐（不丢字段）

1. `dispatchRequest` struct（`:36`）的 `homeDir *string` 之后新增：

```go
	frozenTarget       string
```

2. `dispatchTransportWithOpts`（`:96`）的 `HomeDir: req.homeDir,` 之后新增：

```go
		FrozenTarget:      req.frozenTarget,
```

3. `cliTransport`（`:120`）的 `homeDir: opts.HomeDir,` 之后新增：

```go
		frozenTarget:       opts.FrozenTarget,
```

#### 测试追加

**(a) 接缝 1 + 空值分辨**，追加到 `internal/agentd/b398_frozen_target_test.go`：

```go
// TestB398AssemblySeparatesFrozenIdentityFromRoute 接缝 1：装配路径（HTTP
// `POST /api/cards/{id}/step` 受理 → startCardStep → runner.Run）产出的
// DispatchOpts 必须把「路由 target」与「冻结身份」分成两个值——本机别名
// linux-01 时路由字段是 canonical 空串，冻结字段是原名。断言落在 transport
// 收到的 DispatchOpts 上，不绕开装配路径。
func TestB398AssemblySeparatesFrozenIdentityFromRoute(t *testing.T) {
	env := setupB23310CardTaskEnvWithMachine(t, []fake.Step{{Finish: executor.Result{OK: true}}}, "linux-01")
	cardID := seedSquadFlow(t, env, "sq1", 1)[0]

	type captured struct {
		opts ledgerstep.DispatchOpts
	}
	gotCh := make(chan captured, 1)
	env.srv.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, id, node string) {
		runner.Dispatcher.Transport = func(_ context.Context, opts ledgerstep.DispatchOpts) (string, string, error) {
			gotCh <- captured{opts: opts}
			return "T-b398-seam1", "", nil
		}
		_, _ = runner.Run(ctx, id, node)
	}
	if err := env.srv.startCardStep(cardID, proto.CardStepReq{Step: "implement", Actor: "test"}); err != nil {
		t.Fatalf("受理: %v", err)
	}
	var got captured
	select {
	case got = <-gotCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("装配路径未到达 Transport；卡事件=%s", b398CardEvents(t, env, cardID))
	}
	if got.opts.FrozenTarget != "linux-01" {
		t.Fatalf("冻结身份字段 = %q，want linux-01", got.opts.FrozenTarget)
	}
	if got.opts.Target != "" {
		t.Fatalf("路由字段 = %q，want 空（canonical）", got.opts.Target)
	}
}

// TestB398ReceiverEmptyFrozenTargetIsTakenVerbatim 接缝 2：字段缺席与显式空串必须可
// 分辨——缺席回落 req.Target；显式空串按快照丢失处理（400），不静默回落。
func TestB398ReceiverEmptyFrozenTargetIsTakenVerbatim(t *testing.T) {
	env := newReceiverTestEnv(t)
	putTarget(t, env.srv, "linux-01", env.srv.conf().Listen)
	putOnlineCarrier(t, mustScheduling(t, env.srv), scheduling.Carrier{
		Name: "self-box", Machine: "linux-01", CLI: "fake", HomeDir: "",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
		Status: scheduling.StatusOnline,
	})
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID,
		`,"carrier":"self-box","target":"linux-01","frozen_target":"","executor":"fake","home_dir":""`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("显式空冻结字段应被拒（快照丢失可见），实得 %d（%s）", rr.Code, rr.Body.String())
	}
}
```

（该文件头部 import 需补 `"context"` 与 `ledgerstep`；T1 已建文件，T2 只追加 import 与函数。）

**(b) 投影点 1**，追加到 `internal/client/client_test.go`（镜像
`TestDispatchSerializesHomeDirThreeStates` 的写法）：

```go
// TestDispatchSerializesFrozenTargetPresence 钉住 B398 冻结身份字段穿过真实 JSON
// 的缺席/非空两态：空值必须完全不出现键（旧发送方兼容），非空原样出现。
func TestDispatchSerializesFrozenTargetPresence(t *testing.T) {
	cases := []struct {
		name        string
		frozen      string
		wantPresent bool
		wantValue   string
	}{
		{name: "缺席（空值）", frozen: ""},
		{name: "非空", frozen: "linux-01", wantPresent: true, wantValue: "linux-01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatalf("解析 dispatch 请求: %v", err)
				}
				raw, present := got["frozen_target"]
				if present != tc.wantPresent {
					t.Errorf("frozen_target 是否出现 = %v, want %v; body=%s", present, tc.wantPresent, raw)
				}
				if present {
					var value string
					if err := json.Unmarshal(raw, &value); err != nil {
						t.Errorf("frozen_target 应为 JSON string: %v", err)
					} else if value != tc.wantValue {
						t.Errorf("frozen_target = %q, want %q", value, tc.wantValue)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"T-frozen"}`))
			}))
			defer ts.Close()
			if _, err := client.New(ts.URL, testToken).Dispatch(context.Background(), client.DispatchOpts{
				ProjectID: "deadbeefdeadbeef", Prompt: "frozen", FrozenTarget: tc.frozen,
			}); err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
		})
	}
}
```

**(c) 投影点 2/3**，追加到 `cmd/card_dispatch_test.go`（用既有
`swapDispatchTransportWithOpts` 缝捕获 `dispatchRequest`）：

```go
// TestCliTransportForwardsFrozenTarget CLI 侧 Transport 必须把
// ledgerstep.DispatchOpts.FrozenTarget 原样投影进 client 请求，不得丢字段。
// 内部锁声明：CLI 今日没有生产冻结派发（Dispatcher.Carrier 恒空），从 spec 三条缝
// 构造不出这条断言；它是「手写投影点必须显式锁定」的附加锁，不顶替任何缝级断言。
func TestCliTransportForwardsFrozenTarget(t *testing.T) {
	var got dispatchRequest
	var called bool
	restore := swapDispatchTransportWithOpts(func(req dispatchRequest) (string, string, error) {
		got = req
		called = true
		return "T-frozen", "", nil
	})
	defer restore()
	if _, _, err := cliTransport(context.Background(), ledgerstep.DispatchOpts{
		Prompt: "p", FrozenTarget: "linux-01",
	}); err != nil {
		t.Fatalf("cliTransport: %v", err)
	}
	if !called || got.frozenTarget != "linux-01" {
		t.Fatalf("cliTransport 未透传 frozenTarget: called=%v got=%q", called, got.frozenTarget)
	}
}
```

（**注意**：`cmd/card_dispatch_test.go` 现状 import 不含 `context` 与 `internal/ledgerstep`；
本测试需补这两个 import——`cliTransport` 是包内未导出符号，测试必须在 `package cmd` 内。）

- **Interfaces**
  - Consumes：`proto.EventTypeArchived` 无关；本卡消费 `Binding.Target`、`scheduling.Carrier`、
    `ledgerstep.TemplateDispatch`、`client.ExecutionClient`（签名锁约束见 §4）。
  - Produces：见 §5 Produces。
- **步骤**
  1. 判据先在基线跑（复核）：T1 已红（原文见台账 §1.3）。
  2. 按改动一~七改代码（七处，逐处对照）。
  3. 跑 T1 转绿 + T2 新测试：
     `go test ./internal/agentd/ -run 'TestB398' -count=1 -timeout 120s` → 预期全 PASS。
     同时 `go test ./internal/client/ -run TestDispatchSerializesFrozenTargetPresence -count=1`
     与 `go test ./cmd/ -run TestCliTransportForwardsFrozenTarget -count=1` → 预期 PASS。
  4. 跑编译与静态检查：`go build ./...`、`go vet ./internal/agentd/ ./internal/ledgerstep/ ./internal/client/ ./cmd/`（预期 `EXIT=0`）。
- **加关键节点日志**：已在改动三/四补「raw_target / target / frozen_target」三读数与
  「route_target / frozen_target」接收端读数；成功路径不静默（原有「模板派发完成」「节点派发完成」保留）。
- **加注释**：新增字段与分支均带「为什么」（路由值 vs 身份快照、缺席 vs 显式空串）；
  `stepTransport` 的新分支注明「归一可能折空、身份必须原样」；`handlers.go` 冻结分支注明
  回落语义。函数头无需改签名。
- **测试范围声明**：`./internal/agentd/`（缝 1/2/3）、`./internal/client/`（投影点 1）、
  `./cmd/`（投影点 2/3）。**不跑全量**：全量不属于任何单个 task。

---

## 7. 缺陷族对抗审查

**族 1 生命周期/状态机中断**
- 新增字段是纯数据透传，不新增 goroutine、锁、文件句柄；`stepTransport` 只新增日志读数，
  选路逻辑（`dispatchTarget`/`canonical`）一字不改，不影响 client 选择或错误路径。
- 冻结字段缺失时所有分支走旧路径（`FrozenTarget==""` → 不写 wire 键、接收端回落 `req.Target`），
  旧发送方逐字兼容由 `TestB398ReceiverLegacyTargetFallback` 锁定。

**族 2 静默失败 / 误导报错**
- 显式空冻结字段**不静默回落**而走 400（`TestB398ReceiverEmptyFrozenTargetIsTakenVerbatim`），
  避免「快照丢失被静默成功掩盖」（spec 弃选 A 的同一理由）。
- 旧发送方缺席仍回落（回归锁）。
- 路由失败路径不变：`stepTransport` 的 `clientForTarget` 仍用 `canonical`，冻结字段不参与选路。

**族 3 跨平台假设**
- 纯字段增补与 switch/if 分支，无平台假设。**无**。

**族 4 假红 / 假绿测试**
- 接缝 3 走真实 `startCardStep → runner.Run → ViaTemplate → stepTransport → client HTTP →
  handleDispatch → AdmitFrozen`，不是直测 `stepTransport`（spec §1 明确现状直测证明不了这条链）。
- 接缝 1 断言的是装配后 transport 收到的 `DispatchOpts`，入口是 HTTP step，不绕装配点。
- 假绿防护：接缝 2 同时含「带冻结→200」与「旧 target→200」与「空冻结→400」三态，
  删掉实现只会让第一支红、第三支可能变绿而第二支仍绿——不等于把判据删成恒放行。
- 变异复验（T2 步骤后手动）：把 `handlers.go` 的 `frozenTarget := *req.FrozenTarget` 分支
  临时删回 `req.Target` → 接缝 2/3 应重新红；撤回临改。

**族 5 门禁绕过**
- 接收端仍有 `errReceiverNotCarrierMachine` 闸（`handlers.go:571-578`）与
  `AdmitFrozen` 三项物理比对，均不改；冻结字段只换「喂给判据的值来源」，不放宽判据。
- `putTarget` 写活配置 targets 是既有测试夹具；本卡不新增绕过面。

**追加设问一：序列化边界**——本卡新增 `frozen_target` 一个 wire 字段。手写投影点：
①`client.Dispatch` 的 body map（`client.go:763-786`）；②`cmd.dispatchTransportWithOpts`
（`cmd/card_dispatch.go:96`）；③`cmd.cliTransport`（`:120`）；④`agentd.dispatchRequest` 解码。
四处全部列入文件清单并各有断言：①`TestDispatchSerializesFrozenTargetPresence`；
②/③`TestCliTransportForwardsFrozenTarget`；④接缝 2/3（含缺失/非空/空三态）。**已覆盖**。

**追加设问二：枚举新值过既有白名单**——无新增枚举。**无风险**。

**追加设问三：承重安全属性有测试锁住**——承重属性是「冻结机器名不被路由归一改写」，
由接缝 1（`DispatchOpts.FrozenTarget=linux-01` 且 `Target==""`）+ 接缝 3（E2E 形成 task
且 `task.Target=linux-01`）共同锁定；回归属性（旧发送方）由接缝 2 回落锁定。

---

## 8. 上下文预算检查

有界文件集（圈得出）：`internal/ledgerstep/dispatch.go`、`internal/agentd/cardstep.go`、
`internal/agentd/handlers.go`、`internal/client/client.go`、`cmd/card_dispatch.go`、
`internal/agentd/b398_frozen_target_test.go`（新）、`internal/client/client_test.go`（追加）、
`cmd/card_dispatch_test.go`（追加）、`docs/superpowers/ledgers/2026-09-22-b398-plan-ledger.md`。
不越出 `internal/ledgerstep`、`internal/agentd`、`internal/client`、`cmd`。**通过**。

## 9. 类型标注 / 边界型子系统

跨进程 wire 边界（发送端 `internal/client` ↔ 接收端 `internal/agentd`）——属边界型子系统。
行为验收以显式真机清单给出（§11，由协调者执行）；机内以接缝 3 的真实 HTTP 边界测试兜底。

## 10. 接缝覆盖（双向，对照 spec §6 测试决定的接缝清单）

spec 的接缝 = ①装配路径（冻结身份已取得→归一路由→装配 StepRunner/Dispatcher）；
②接收端准入（`handlers.go` step 请求处理→`AdmitFrozen`）；③端到端回归锁（本机别名+
冻结载体的最小派发，现状在最后一步被拒）。

- **测试 → 缝**：
  - `TestB398FrozenCarrierLocalAliasDispatches` 入口 = `ledgerPost(.../api/cards/{id}/step)`，
    经 startCardStep→runner.Run→ViaTemplate→stepTransport→client HTTP→handleDispatch，落在缝①→③；
  - `TestB398AssemblySeparatesFrozenIdentityFromRoute` 入口 = `startCardStep`（HTTP step 的同步段），
    断言落缝①的 transport 契约；
  - `TestB398ReceiverUsesFrozenTargetForFrozenAdmission` / `TestB398ReceiverLegacyTargetFallback` /
    `TestB398ReceiverEmptyFrozenTargetIsTakenVerbatim` 入口 = `postDispatch`（`POST /api/tasks`
    → `handleDispatch`），落在缝②。
- **缝 → 测试**：
  - 缝①被 `TestB398AssemblySeparatesFrozenIdentityFromRoute` 锁（冻结字段==原名、路由字段==canonical）；
  - 缝②被接收端三态测试锁；
  - 缝③被 E2E 测试锁。
- **内部锁**：`TestCliTransportForwardsFrozenTarget` 入口 `cliTransport` 不在 spec 三条缝上，
  是附加内部锁，理由已在其注释与 §6(c) 声明：CLI 今日无生产冻结派发，从三条缝构造不出
  该投影断言；它**不顶替**任何缝级断言。  `TestDispatchSerializesFrozenTargetPresence` 入口
  `client.Dispatch` 在缝③的调用链上（`startCardStep→…→client HTTP` 必经它），是缝级，
  非内部锁。
  **通过**。

## 11. 真机清单（归协调者执行；本 task 由协调者执行，不派发）

1. 升级带 T2 改动的 agentd 到「载体登记名是本机别名」的机器（如 `linux-01`）后，对一张
   绑该载体小队的卡执行 `handoff card dispatch <卡> --step <节点>`：**应被受理（202）**，
   不再出现 `小队角色不符: 冻结物理身份与载体登记不一致`（spec §1 真机现场）。
2. 对照：普通（非小队）派发行为不变；对真正远端载体仍按 `errReceiverNotCarrierMachine` 拒。
3. 观察 agentd.log：同一派发能看到 `raw_target` / `target` / `frozen_target` 三个读数，
   且本机别名场景 `target==""`、`frozen_target=="linux-01"`。

> 真机需要在跑有 T2 改动的 agentd 的机器上执行，并依赖现场卡/载体席位；机内夹具验不了
> 「线上同名机器真能重派」，故单列交协调者。

## 12. 占位符扫描

- 无 TBD / 「加适当的错误处理」/「同 Task N 而略」；T1/T2 的代码块与改动块均完整可抄。
- **例外声明（无）**：不依赖「形态因包而异」的夹具复用，测试代码写全（夹具签名已在 §4 列出）。
- 条件退路：无（T2 的变异复验是显式步骤，不改变任何测试的入口符号）。
- **内部锁声明**：仅 `TestCliTransportForwardsFrozenTarget` 一条，理由见 §10；其余测试入口均在缝上。
  `TestDispatchSerializesFrozenTargetPresence` 不是内部锁（它穿真实 JSON 边界，是缝①→②的运输段）。

## 13. 自审三查

1. **spec 覆盖（逐条指到 task）**：
   - §3 采用方案「拆两个字段，各走各通路」→ T2 改动一~七；
   - §4 用户故事 1（本机别名送出冻结身份仍是原名、路由仍本机直连）→ 接缝 1 + 接缝 3；
   - §4-2（接收端用冻结字段、缺席逐字兼容）→ 接缝 2 三态；
   - §4-3（日志三读数）→ T2 改动三/四日志；
   - §4-4（穿过装配路径的测试）→ 接缝 1/3；
   - §5 实现决定（来源唯一 `binding.Target`、透传、接收端取舍、命名）→ T2 改动四/三/六；
   - §6 接缝 1/2/3 → §10；§7 Out of Scope（不改 `AdmitFrozen`/`samePhysicalMachine`/`CanonicalTarget`）
     → §2 分流与族 5 确认不改；
   - §8 最小红色读数（`raw_target=linux-01 → target=""`）→ 本节点台账已复现。
2. **占位符扫描**：见 §12，无占位。
3. **跨 task 类型/签名一致性**：T1 定义三个测试；T2 追加 `TestB398AssemblySeparatesFrozenIdentityFromRoute`
   与 `TestB398ReceiverEmptyFrozenTargetIsTakenVerbatim`、`TestDispatchSerializesFrozenTargetPresence`、
   `TestCliTransportForwardsFrozenTarget`。T1/T2 共享的 `FrozenTarget` 在
   `ledgerstep.DispatchOpts`（T2 改动一）与 `client.DispatchOpts`（改动五）同名不同类型，
   引用处逐字：`opts.FrozenTarget`（agentd/stepTransport）、`d.FrozenTarget`（ledgerstep/ViaTemplate）、
   `req.FrozenTarget *string`（agentd/handlers）、`req.frozenTarget`（cmd）。
   `dispatchStep`/`stepTransport` 签名不变（§4 签名锁）。

## 图覆盖债（本节点发现，记入台账 §3）

- `codegraph flow ViaTemplate` → `degraded=true steps=0`（基线无 flows 段），按纪律读源码，未拿 chain 冒充。
- `codegraph who-calls handleDispatch` 短名未命中，报需全限定名 `Server.handleDispatch`；用 grep 补调用面。
- `sym DispatchOpts` 返回 `ledgerstep` 与 `client` 两处同名类型，已用源码/grep 区分。

> 未验证项：接缝 1 断言与 T2 新测试的绿（本节点不写实现，未跑）；真机清单（§11）未跑；
> `internal/agentd` 全量 `TestPtyWSAttachedBacklogBytesKeyPresent` 预存在 flake，不属本卡。
