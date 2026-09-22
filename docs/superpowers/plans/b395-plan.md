# B395 计划：SSE 断连丢权限请求，回合永挂不自愈——根因、分流与「重连重问未决权限」修复

> 卡 B395 · 节点 charter:plan（debug 入口） · spec `docs/superpowers/specs/b395.md`（已批准）
> 基线分支 `cards/B395-charter-2`，起手 HEAD `c5505a44`（已 `git merge origin/cards/B233.1-charter-7` → `Already up to date`）。
> 台账 `docs/superpowers/ledgers/2026-09-22-b395-plan-ledger.md`（含全部亲跑命令、原始输出、真机日志行、图查询记录、进程清理原文）。
> **读者假设**：对 handoff 仓零上下文的执行者。凡引用行号者动手前重核，漂了以符号为准。
> **本节点只出计划，不写实现**。「未验证」= 本节点没亲自跑到结果，实现卡必须自己复核。

---

## 0. 根因（证据驱动，全部亲跑 / 真机原文）

**一句话**：`GET /event` 无重放语义，SSE 断连间隙产生的 `permission.asked` **永久丢失**；adapter 在重连/重启后**没有**「重新发现未决权限」的路径（提问有，权限没有），于是 executor 阻塞在一个没人应答的权限门上 → 会话永不 idle → 无终态事件 → 对账也判「回合未结束」不补发 → 任务停在 `running` 直到 2h `stallTimeout`（而 stalled 只唤醒不修复），只能人工 `resume --force` 收口。

### R1 触发：SSE 单行超 1MB 反复断连（真机日志原文）

事故任务 `d0a4ee47-32d5-4049-9e7c-9267bf50bf6d`（`/root/.handoff/agentd.log` 行 11455-11466）：

```text
11455 10:28:55.442 WARN SSE 流读取异常 {"cause":"bufio.Scanner: token too long"}
11456 10:28:56.453 WARN SSE 断连已恢复：断连间隙的权限请求可能丢失（/event 无重放语义）... task=d0a4ee47... session=ses_f390eb616ffe5Xp905iWkvd7JO
11459 10:28:58.095 WARN SSE 流读取异常 {"cause":"bufio.Scanner: token too long"}
11460 10:28:59.105 WARN SSE 断连已恢复：断连间隙的权限请求可能丢失 ... task=d0a4ee47...
11461 10:29:00.653 WARN SSE 流读取异常 {"cause":"bufio.Scanner: token too long"}
11462 10:29:02.677 WARN SSE 断连已恢复：断连间隙的权限请求可能丢失 ... task=d0a4ee47...
11463 10:29:05.042 WARN SSE 流读取异常 {"cause":"bufio.Scanner: token too long"}
11464 10:29:09.049 WARN SSE 断连已恢复：断连间隙的权限请求可能丢失 ... task=d0a4ee47...
11465 10:29:09.179 WARN opencode 权限请求未找到对应工具等待窗口  task=d0a4ee47... perm=per_0c6f215b80011b1wKkDe7cORMi part=call_01_MCkRmOL6vfJUtTLQUIek8789
11466 10:29:09.191 WARN opencode 权限应答成功但未找到工具等待窗口  task=d0a4ee47... perm=per_0c6f215b80011b1wKkDe7cORMi part=call_01_MCkRmOL6vfJUtTLQUIek8789
```

`bufio.Scanner: token too long` 出自 `internal/executor/opencode/api.go:857-858`（`sc.Buffer(64*1024, sseScanBuffer)`，`sseScanBuffer = 1<<20`，`api.go:54`）→ `streamOnce` 返回错误 → `SubscribeEvents` 指数退避重连（`api.go:748-801`）。

### R2 两条 WARN 的发出点（grep 原文）

```text
internal/executor/opencode/adapter.go:1635  a.log.Warn("opencode 权限请求未找到对应工具等待窗口", ...)   ← mapPermissionAsked 内
internal/executor/opencode/adapter.go:762   a.log.Warn("opencode 权限应答成功但未找到工具等待窗口", ...)  ← RespondPermission 内
```

- `adapter.go:1635`：`mapPermissionAsked`（`adapter.go:1542`）里 `entries := r.seg.PauseWaiting(pa.Tool.CallID)`（`adapter.go:1633`）返回空 → 工具等待窗口没打开。
- `adapter.go:762`：`RespondPermission`（`adapter.go:702`）里 `entries := r.seg.Resume(timing.part)`（`adapter.go:760`）返回空 → 恢复时窗口不在。

### R3 等待窗口的归属：按**任务运行态**持有，断连时**无人清**

- `Segmenter.open map[string]*openTool`（`internal/executor/turn/timing.go:63`）与 `runState.permTiming map[string]permissionTiming`（`adapter.go:298`，值 `{part string; paused bool}`，`adapter.go:307-310`）都是**进程内、挂在单个任务的 `runState` 上**，`sessMu`/`turnMu` 保护。
- `SubscribeEvents` 断连只重建连接，**不清理任何 `runState` 字段**；`Stop`/归档才会注销整个运行态。
- ⇒ 所以「窗口找不到」不是「被谁清掉了」，而是**该权限的 tool part 与/或权限事件在断连间隙一起丢了**，窗口从未（或未能）打开。两条 WARN 是同一次断连的可观测伴随症状；**真正致命的是权限事件整体丢失**（见 R4/R5）。

### R4 永挂的机制（真机帧文件 + opencode DB，决定性）

任务目录 `/root/.handoff/tasks/d0a4ee47-32d5-4049-9e7c-9267bf50bf6d/`：

- `frames.jsonl` 帧类型计数 `{turn_start:1, event:13, reasoning:661, tool_call:11, tool_result:11}`；最后一条真实动作帧是 `seq 695 10:29:09.210 tool_result call_01_MCkRmOL... ok`，其后只剩 `10:49:14 progress`（人工 `resume --force`）与 `10:50:13 archived`——**10:29:09 → 10:49:14 共 20 分钟零帧产出**（与 spec §1「帧文件 20 分钟零产出」吻合）。
- opencode 全局 DB（只读查询，未改动）会话 `ses_f390eb616ffe5Xp905iWkvd7JO`：最后一条 assistant 消息 `msg_0c6f204cb001sLfDQOsjbdie5a` **未 finalize**（`time.completed` 缺失、`finish=None`）；其 parts 含 `tool bash callID=call_00_OZ79upuGZJ7W98vuty5k6263 status=running`。
- **`frames.jsonl` 里根本没有 `call_00_OZ79...` 这条 tool_call**：adapter 从未收到它的 tool part，也从未收到它的 `permission.asked`。opencode 侧 bash 工具长期 `running` 的唯一原因是**在等一个 handoff 永远收不到的权限应答**。

⇒ 环路闭合点：**丢失的 `permission.asked` + 无重问路径**。与之对照，提问（question）有 `rediscoverPendingQuestions`（`internal/executor/opencode/resume.go:213`，走 `GET /question`），权限**没有**。

### R5 为什么「兜底」都不生效

1. **对账救不了权限**：`Reconcile`（`reconcile.go:43`）取会话尾部消息，`reconcileTurnEnded`（`reconcile.go:158`）第一判据 `msg.CompletedMS == 0 → 未结束（row1）`（`reconcile.go:159-161`）——挂死会话的消息恰恰 `completed` 缺失，直接被判「回合仍在进行，不补发」。
2. **旧注释断言「无端点」**：`reconcile.go:12-13` 与 `adapter.go:1123-1124` 写死「消息流 tool part 只有 callID 没有权限 id，应答端点要求真实 id，无按会话拉取未决权限 id 的可用端点」，据此刻意让 `ReconcileOutcome.Pending` 恒 0。**该断言已过期**（见 §1 决定性新事实）。
3. **30 分钟回合上界不覆盖派发任务**：`hostapi.DefaultTurnTimeout = 30m`（`internal/hostapi/driver.go:46`）只作用于 `hostapi.Host.RunTurn` 驱动的**协调者唤醒回合子进程**（调用方 `internal/executor/opencode/coordinator.go:94`）。派发出来的 executor 任务走 `opencode serve` + SSE 订阅（`adapter.Start/subscribeLoop`），**adapter 层没有任何按回合的墙钟上界**。

   ```text
   $ grep -rn "hostapi" internal/orchestration/manager.go internal/executor/opencode/adapter.go
   （无输出：派发路径不经过 hostapi）
   ```
   spec §3 Q3 答案：上界**只覆盖「回合进程」，完全不覆盖「等待权限」**。
4. **stall 看门狗只唤醒不修复**：`scanStalled`（`internal/orchestration/watchdog.go:175-230`）对 `running/waiting_answer` 任务超 `stallTimeout` 追加 `stalled` 事件并广播，**不改状态、不重启、不重问**（`watchdog.go:16-17`）；生产 `StallTimeout` 缺省 **2h**（`internal/config/config.go:483`）。挂死 20 分钟时它连事件都还没到。
5. **人工收口**：`10:49:14 恢复操作：人工强制收口`（`resume --force`）→ `10:50:13 archived`。今晚同型 ≥3 次（spec §1）。

---

## 1. 决定性新事实：`GET /permission` 存在且列出挂起权限

旧注释「无可用端点」**已过期**。本机 `opencode --version → 1.18.31`，亲跑只读探测（见台账 §5）：

```text
$ curl -s -u opencode:<pw> http://127.0.0.1:<port>/permission
[] [HTTP 200]

$ curl -s -u opencode:<pw> http://127.0.0.1:<port>/doc      # OpenAPI
GET /permission -> permission.list | List pending permissions
GET /api/session/{sessionID}/permission -> v2.session.permission.list
POST /permission/{requestID}/reply -> permission.reply
```

`GET /permission` 响应 schema（OpenAPI `#/components/schemas/PermissionRequest`）与 SSE `permission.asked` 的 properties **同形**：

```json
{"id":"^per","sessionID":"^ses","permission":"...","patterns":["..."],
 "metadata":{...},"always":["..."],"tool":{"messageID":"...","callID":"..."}}
```

`internal/executor/opencode/testdata/perm_bash.json`、`perm_edit.json` 等已是同形真实抓包，可直接做序列化边界夹具。

**版本边界（已声明，必须处理）**：sample 抓包来自 1.18.15（`replay_spike_test.go`），`GET /permission` 是本机 1.18.31 才实测到。修面必须对 404 **优雅降级**（保留旧人工兜底告警），不得因端点缺失把恢复打断。

---

## 2. 分流决定

| 根因 | 归属 | 处置 |
|---|---|---|
| R1 SSE 单行 >1MB 导致断连 | 触发条件，不是病根 | **不在本卡**改扫描器上限（属另一卡）；本卡只把它当触发场景。 |
| R4 断连间隙权限事件丢失、无重问路径 | 承重缺陷，本卡核心 | **T1**：`GET /permission` + 重连/重启后重新发现并重放未决权限。 |
| R5.2 旧注释「无可用端点」过期 | 文档债 | **T1** 更新 `reconcile.go`/`adapter.go` 相关注释。 |
| R5.3 派发任务无回合上界（waiting permission 不计） | 架构级（adapter 回合看门狗 / 与 lease 语义交织） | **回 spec 重新定级，另立卡**；本卡用「重问未决项」使回合有限收口，不在此加全局上界。 |
| R5.4 stall 只唤醒不修复、2h 太长 | 语义设计（stalled 的定义就是「只告警」） | **不在本卡**改；T1 后挂死窗口从「2h+人工」缩到「一次重连」。 |
| R3 两条 WARN 属良性到达顺序（权限先于 tool part 时窗口本就不该开） | 观测性 | **T2**：加一条反例回归，锁「正常权限流程不回归」+ 端点缺失降级，不改日志字符串语义。 |

> 遵 spec §「一两行小修顺手修掉；架构级修复回本 spec 重新定级，不许在排查现场顺手动架构」。T1/T2 都是单子系统（`internal/executor/opencode`）内的小修。

---

## 3. 任务 DAG

```text
T1（承重：GET /permission 客户端 + 重连/重启重问未决权限）
  └→ T2（反例/不误伤：正常权限流程不回归 + 端点缺失优雅降级）
（独立，回 spec）派发任务回合上界 / stall 自愈 —— 不在本 DAG
```

次序承重：T2 的「不回归」断言在 T1 落地后才有意义（它验证 T1 没破坏正常权限门、且旧版端点场景不炸）。全量测试不属于任何单个 task（implement 三段律）。

---

## 4. 基线事实（实现卡共享，动手前复核）

**亲跑读数（原始输出见台账 §6）**：

```text
$ go build ./...            → BUILD_EXIT=0
$ go vet ./internal/executor/opencode/  → VET_EXIT=0
$ go test ./internal/executor/opencode/ -run 'TestStartToPermissionFlow|TestReconnectWarnsLostPermission|TestOpencodePermissionWaitNotToolTime|TestOpencodePermissionReplyFailureKeepsWaitingWindow|TestRediscoverPendingQuestionsFiltersBySession' -count=1
ok  github.com/Xsxdot/handoff/internal/executor/opencode  0.212s
```

**红色回路已在基线亲跑坐实**（临时探针，取证后删除）：

- 编译红：`a.rediscoverPendingPermissions undefined (type *Adapter has no field or method rediscoverPendingPermissions)`。
- 行为红：假 server 在重连后 `GET /permission` 返回一条挂起权限 `per_lost`，断言重连后应产出 `permission` 事件：

  ```text
  --- FAIL: TestB395TmpReconnectLosesPendingPermission (3.00s)
      b395_tmp_probe_test.go:85: 重连后未重新发现挂起权限 per_lost（B395 红：权限请求丢失后回合永挂）
  ```
  真实日志现场（同一次跑）：重连只走了 `恢复后对账完成 ... turn_ended=false emitted=0 note=会话里还没有模型消息，无需对账`，**零 permission 事件**。

**既有夹具（实现卡直接复用，不新建）**：

- `internal/executor/opencode/adapter_test.go`：`newFakeServer(t)` / `fakeServer`（脚本化 SSE + 记录请求）、`startFakeRun(t, fs, taskID, repo, taskDir)`、`fakeProbe`、`captureLog`/`quietLog`、`adapterTestPassword`、`waitEventType`、`isTimingEvent`。
- `internal/executor/opencode/reconcile_internal_test.go`：`newTestAdapter(t)`、`drainOne(r)`、`newTestRun(...)`。
- `internal/executor/opencode/api.go:120`：`NewAPIWithSSEBackoff(baseURL, password, initial, max)`（测试注入毫秒级重连退避）。
- 真实抓包：`internal/executor/opencode/testdata/perm_bash.json`（bash+command）、`perm_edit.json`（edit+filepath）、`perm_external_directory_bash.json`。

**图查询**（先图后 grep，未命中记债）：`codegraph sym` 命中 `mapPermissionAsked`(adapter.go:1542)、`PauseWaiting`(timing.go:183)、`rediscoverPendingQuestions`(resume.go:213)、`ListPendingQuestions`(api.go:702)、`subscribeLoop`(adapter.go:1089)；`codegraph context permission` 被拒（非最优树领域 id），改用 `context d_execution_adapters`（`misplaced=[]`、`fociTruncated={total:9,shown:5}`）。**图覆盖债**：`DefaultTurnTimeout`、`scanStalled`、`sseScanBuffer`、`reconcileTurnEnded` 未逐条 `sym`，按源码 file:line 给出；`flow` 在本基线无 flows 段（`degraded=true, missing="基线没有 flows 段"`），一律回退读源码（未拿 `chain` 冒充 `flow`）。

---

## 5. 接口契约（执行者只看本 task，故这里列全）

### Consumes（本卡要用的既有签名，一字不改）

```go
// internal/executor/opencode/api.go
func NewAPIWithSSEBackoff(baseURL, password string, initial, max time.Duration) *API
func (a *API) ListPendingQuestions(ctx context.Context) (out []PendingQuestion, err error) // api.go:702，本卡照抄其形态
func (a *API) do(ctx context.Context, method, path string, body any) (*http.Response, error) // api.go:166
func (a *API) httpError(op string, resp *http.Response) error                                // api.go:193
func (a *API) log() *slog.Logger                                                             // api.go:159

// internal/executor/opencode/adapter.go（本卡重放的目标入口，签名一字不改）
func (a *Adapter) mapPermissionAsked(r *runState, props json.RawMessage) // adapter.go:1542；须在 turnMu 下调用
func (a *Adapter) acceptForeign(r *runState, ev sseEvent, sessionID string) bool // adapter.go:1431
func (a *Adapter) lookup(taskID string) *runState                        // adapter.go:349
func (a *Adapter) emit(r *runState, ev executor.AdapterEvent) bool       // adapter.go:1249

// internal/executor/turn/timing.go（不改）
func (s *Segmenter) PauseWaiting(part string) []proto.TimingEntry // timing.go:183
func (s *Segmenter) Resume(part string) []proto.TimingEntry       // timing.go:208

// internal/executor/opencode/resume.go（重启恢复接线参照）
func (a *Adapter) rediscoverPendingQuestions(ctx context.Context, taskID string) // resume.go:213
```

> 说明：`sseEvent` 是同包未导出类型（`adapter.go:1318`），T1 在同包内构造它交给 `acceptForeign` 做归属判定，不新增导出面。以上 `Consumes` 均为**既有**签名，本卡不改。

### Produces（本卡新增，跨 task 逐字对齐）

```go
// internal/executor/opencode/api.go（T1 新增）
//
// PendingPermission 是一条挂在 opencode 侧、尚未裁决的权限请求
// （GET /permission 数组元素；形状与 SSE permission.asked properties 同形）。
type PendingPermission struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"sessionID"`
	Permission string          `json:"permission"`
	Patterns   []string        `json:"patterns"`
	// Metadata 保留原始 JSON：键随权限类别而变（bash: command / edit: filepath /
	// external_directory: directories,parentDir），本层不解析，解析归 adapter。
	Metadata json.RawMessage `json:"metadata"`
	Tool     PendingPermissionTool `json:"tool"`
}

// PendingPermissionTool 是挂起权限的工具配对键（messageID + callID）。
// 单独成类型而非匿名结构：命名类型让跨文件构造（测试、恢复路径）不必复述
// 匿名结构字面量，也避免两处字段名漂移。
type PendingPermissionTool struct {
	MessageID string `json:"messageID"`
	CallID    string `json:"callID"`
}

// ListPendingPermissions 拉取当前全部挂起的权限请求（跨会话）。
func (a *API) ListPendingPermissions(ctx context.Context) (out []PendingPermission, err error)

// internal/executor/opencode/permission_recover.go（T1 新增）
func permissionAskedProps(p PendingPermission) json.RawMessage
func (a *Adapter) rediscoverPendingPermissions(ctx context.Context, taskID string)
```

> 序列化边界：`permissionAskedProps` 是 `PendingPermission → SSE properties JSON` 的唯一编码点，`mapPermissionAsked` 是唯一解码点。**必须有一条穿过该边界的断言**（见 T1.4 的 `TestPendingPermissionAskedPropsRoundTrip` 与 `TestPendingPermissionPropsProjection`）；`metadata` 缺失 vs 值为空由前者三个用例区分。

---

## 6. 任务

### T1（承重）：`GET /permission` + 重连/重启后重问未决权限

**目标（R4）**：SSE 断连重连或 agentd 重启后，adapter 主动查询 opencode 侧未决权限，把本任务会话的未决项重放成与实时路径同形的 `permission` 事件，使 executor 不再等一个永远收不到应答的权限门。

**基线判据（实现卡动手前先跑一次，应红）**：T1.3 的 `TestB395ReconnectRecoversPendingPermission` 在基线必须失败（本节点已亲跑：`FAIL ... 重连后未重新发现挂起权限 per_lost`）。红了才动实现。

---

#### T1.1 `internal/executor/opencode/api.go`：新增 `PendingPermission` 与 `ListPendingPermissions`

在 `ListPendingQuestions`（`api.go:702-727`）之后插入（照抄其请求/校验/日志形态）：

```go
// PendingPermission 是一条挂在 opencode 侧、尚未裁决的权限请求
// （GET /permission 的数组元素）。
//
// 形状与 SSE permission.asked 的 properties 同形（OpenAPI
// #/components/schemas/PermissionRequest；本机 opencode 1.18.31 的 GET /doc 实证），
// 故可直接重放给 mapPermissionAsked。响应里还有 always 字段，本层不消费（描述与
// 结构化载荷都不需要它），按未知字段忽略。
//
// Metadata 保留原始 JSON：键随权限类别而变（bash 的 command、edit 的 filepath、
// external_directory 的 directories/parentDir），本层不解析——解析归 adapter。
type PendingPermission struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"sessionID"`
	Permission string          `json:"permission"`
	Patterns   []string        `json:"patterns"`
	Metadata   json.RawMessage `json:"metadata"`
	Tool       PendingPermissionTool `json:"tool"`
}

// PendingPermissionTool 是挂起权限的工具配对键（messageID + callID）。单独成
// 命名类型而非匿名结构：跨文件构造（测试/恢复路径）不必复述匿名结构字面量，
// 也避免两处字段名漂移。
type PendingPermissionTool struct {
	MessageID string `json:"messageID"`
	CallID    string `json:"callID"`
}

// ListPendingPermissions 拉取当前全部挂起的权限请求（跨会话）。
//
// 参数：ctx 控制单次请求超时。
// 返回：挂起权限列表；请求失败或解析失败返回错误，列表为 nil。
//
// 注意：
//   - 返回的是**全部会话**的挂起权限，调用方必须按 SessionID 过滤出自己的
//     （与 ListPendingQuestions 同款约定）
//   - GET /permission 在本机 opencode 1.18.31 实测存在且返回 200；更早版本可能
//     404。调用方（rediscoverPendingPermissions）必须把错误当**可降级**处理，
//     保留旧的人工兜底告警，不得因端点缺失打断恢复——这条降级是修面的一部分
func (a *API) ListPendingPermissions(ctx context.Context) (out []PendingPermission, err error) {
	start := time.Now()
	const path = "/permission"
	a.log().Info("opencode 查询挂起权限", "path", path)
	defer func() {
		if err != nil {
			a.log().Error("opencode 查询挂起权限失败", "path", path, "cause", err)
		} else {
			a.log().Info("opencode 挂起权限已取得", "path", path, "count", len(out),
				"elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("查询挂起权限请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, a.httpError("查询挂起权限", resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析挂起权限: %w", err)
	}
	return out, nil
}
```

**测试范围声明**：本 task 只跑 `go test ./internal/executor/opencode/ -run 'TestB395|TestPendingPermission' -count=1`；全量测试不属本 task。

**日志**：入口 Info（path）、失败 Error（path/cause）、成功 Info（path/count/elapsed_ms）——见上方代码，全部结构化 logger，禁 print。

**注释**：类型与函数头已含职责/边界/版本边界/why。

---

#### T1.2 `internal/executor/opencode/permission_recover.go`（新文件）：重发现问题

新文件完整内容：

```go
// permission_recover.go —— 断连/重启后重新发现挂起权限（B395）。
//
// 职责：
//   - rediscoverPendingPermissions：查 GET /permission，把本任务会话的未决权限
//     重放成与实时路径同形的 permission 事件（经 mapPermissionAsked），使 executor
//     不再等一个永远收不到应答的权限门
//
// 边界：
//   - 不写 store、不改任务状态：重放的事件经既有 evCh 交 manager，幂等由 manager
//     的工单去重（approval.Authority.IsReplay，authority.go:113）承担
//   - 不做权限判定、不新造描述逻辑：结构化载荷、描述组合、子会话标注、暂停窗口
//     全部复用 mapPermissionAsked（adapter.go:1542），避免与实时路径漂移
//   - 端点缺失（旧版 404）只降级告警，不打断恢复
package opencode

import (
	"context"
	"encoding/json"
)

// permissionAskedProps 把一条挂起权限还原成 permission.asked 的 properties 形状，
// 供 mapPermissionAsked 原样消费（单一解析入口，避免两套字段名漂移）。
//
// 为什么走 JSON 往返而不是直接构造 AdapterEvent：mapPermissionAsked 已承载描述
// 组合、空描述兜底、子会话标注、permSession/permText/permTiming 登记与
// PauseWaiting 全套语义；再造一条「恢复专用」事件构造会与实时路径漂移。且
// testdata/perm_bash.json 的真实形态正是这个形状——往返本身就是可回归的序列化边界。
//
// 注意：Metadata 为空（服务端未给 metadata）时不写该键，mapPermissionAsked 解析时
// 得零值结构——与「字段缺席」同义，不会 panic。patterns 为空时写成 null/[]，两者
// 都被解析成空切片，语义相同。
func permissionAskedProps(p PendingPermission) json.RawMessage {
	m := map[string]any{
		"id":         p.ID,
		"sessionID":  p.SessionID,
		"permission": p.Permission,
		"patterns":   p.Patterns,
		"tool":       map[string]any{"messageID": p.Tool.MessageID, "callID": p.Tool.CallID},
	}
	if len(p.Metadata) > 0 {
		m["metadata"] = p.Metadata
	}
	b, _ := json.Marshal(m)
	return b
}

// rediscoverPendingPermissions 在断连重连/agentd 重启后重新发现本任务挂起的权限并重放。
//
// 为什么必须有这一步（B395 根因）：/event 无重放语义，断连间隙服务端产出的
// permission.asked 永久丢失（adapter.go:1111-1117 的 onReconnect 告警）。而 opencode
// 侧那个工具还在阻塞等应答——不重新发现，任务就是一个谁也叫不醒的孤儿：回合不产
// 事件、不 idle、无终态，只能在 2h 后由 stall 看门狗唤醒人工（真机：10:29:09 起
// 20 分钟零帧产出，最终 resume --force 收口）。提问有 rediscoverPendingQuestions
// （resume.go:213）兜这条，权限此前没有。
//
// 注意：
//   - 本函数在 goroutine 里跑，不阻塞重连/重启返回；失败只记日志（恢复不了还有
//     stall 看门狗与人工兜底，不该让恢复本身失败）
//   - GET /permission 返回该 serve 上全部会话的挂起权限。每个任务一个 serve
//     （proc.go 的 freePort），流上的陌生会话只可能是本任务派生的子会话——归属
//     判定复用实时路径的 acceptForeign（adapter.go:1431），不另立一套规则
//   - 只处理未决权限：已应答的不会出现在 GET /permission 里，故不会重复唤醒；
//     即便服务端同时经 SSE 重放同一 request，也由 manager 的工单去重吸收
func (a *Adapter) rediscoverPendingPermissions(ctx context.Context, taskID string) {
	r := a.lookup(taskID)
	if r == nil || r.api == nil {
		a.log.Debug("重新发现挂起权限跳过：该任务无运行态", "task", taskID)
		return
	}
	pending, err := r.api.ListPendingPermissions(ctx)
	if err != nil {
		// 旧版 opencode 无 GET /permission（404）属预期降级：保留人工兜底告警，
		// 不让恢复本身失败
		a.log.Warn("重新发现挂起权限失败，若任务卡在等待决策请 handoff attach 查看或 handoff resume --force 收口",
			"task", taskID, "cause", err)
		return
	}
	n := 0
	for _, p := range pending {
		if p.ID == "" || p.SessionID == "" {
			// 缺 id/会话无法归属，跳过；与 mapPermissionAsked 的守卫同义（不静默丢整批）
			a.log.Warn("恢复时跳过缺 id/会话的挂起权限", "task", taskID, "perm", p.ID)
			continue
		}
		props := permissionAskedProps(p)
		if p.SessionID != r.session &&
			!a.acceptForeign(r, sseEvent{Type: "permission.asked", Properties: props}, p.SessionID) {
			a.log.Warn("恢复时跳过非本任务的挂起权限", "task", taskID,
				"perm", p.ID, "session", p.SessionID, "own_session", r.session)
			continue
		}
		// 与 mapEvent 的 switch 契约一致：mapPermissionAsked 必须在 turnMu 下执行
		r.turnMu.Lock()
		a.mapPermissionAsked(r, props)
		r.turnMu.Unlock()
		n++
	}
	a.log.Info("恢复后重新发现挂起权限", "task", taskID, "count", n, "total", len(pending))
}
```

**日志**：入口/失败 Warn 带 task 与 cause；每条跳过带 perm/session；成功 Info 带 count/total——见上方代码，全部结构化 logger。

**注释**：文件头职责/边界；两个函数头含参数/返回/为什么/注意。

---

#### T1.3 接线 + 更新过期注释

**T1.3a `internal/executor/opencode/adapter.go`（`subscribeLoop` 的 `onReconnect`，`adapter.go:1108-1119`）**

把现在的：

```go
	}, func() {
		// P1-10b：/event 无重放语义，断连间隙服务端产出的事件永久丢失。
		// B38 起，回合终态那半边由对账补回；权限请求那半边补不回来——消息流的
		// tool part 只有 callID 没有权限 id，应答端点要求真实 id、伪造即 404，
		// 故仍保留本告警，它是协调者知道「可能需要 attach 人工兜底」的唯一信号
		a.log.Warn("SSE 断连已恢复：断连间隙的权限请求可能丢失（/event 无重放语义），"+
			"若任务卡在等待决策请 handoff attach 查看或 handoff resume --force 收口",
			"task", r.taskID, "session", r.session)
		go a.reconcileAfterRecovery(context.Background(), r.taskID, "reconnect")
	})
```

改成：

```go
	}, func() {
		// /event 无重放语义，断连间隙服务端产出的事件永久丢失。B38 起回合终态由
		// 对账补回；B395 起权限请求由 rediscoverPendingPermissions 经 GET /permission
		// 重问未决项补回（旧版无该端点时该方法自身降级、保留人工兜底）。
		// 保留本 Warn：它是「恢复已尝试、若仍卡住需 attach / resume --force」的可见信号
		a.log.Warn("SSE 断连已恢复：断连间隙的权限请求可能丢失（/event 无重放语义），正在重问未决项，"+
			"若任务仍卡在等待决策请 handoff attach 查看或 handoff resume --force 收口",
			"task", r.taskID, "session", r.session)
		go a.reconcileAfterRecovery(context.Background(), r.taskID, "reconnect")
		go a.rediscoverPendingPermissions(context.Background(), r.taskID)
	})
```

**T1.3b `internal/executor/opencode/resume.go`（重启恢复接线，`resume.go:190-193`）**

把：

```go
	if mode != executor.ResumeModeFresh {
		go a.reconcileAfterRecovery(context.Background(), req.TaskID, "startup")
		go a.rediscoverPendingQuestions(context.Background(), req.TaskID)
	}
```

改成：

```go
	if mode != executor.ResumeModeFresh {
		go a.reconcileAfterRecovery(context.Background(), req.TaskID, "startup")
		go a.rediscoverPendingQuestions(context.Background(), req.TaskID)
		// B395：提问之外，权限请求同样会随 agentd 重启窗口丢失（/event 无重放
		// 语义）——不重问，executor 就阻塞在一个没人应答的权限门上
		go a.rediscoverPendingPermissions(context.Background(), req.TaskID)
	}
```

**T1.3c `internal/executor/opencode/reconcile.go`（更新过期注释，`reconcile.go:10-13`）**

把文件头：

```go
//   - **不捧回权限请求**：opencode 的消息流里 tool part 只有 callID 没有权限 id，
//     而 RespondPermission 要求真实 id、伪造即 404（更早的 spike 结论，见
//     adapter.go 的 onReconnect 降级告警）。建一张批了也送不回去的工单比不建更糟，
//     故 ReconcileOutcome.Pending 在本 adapter 恒为 0
```

改成：

```go
//   - **不在此捧回权限请求**：消息流里 tool part 只有 callID 没有权限 id。
//     B395 起，未决权限改由 rediscoverPendingPermissions（permission_recover.go）
//     经 GET /permission 重新发现——它重放的是与实时路径同形的事件，不走对账的
//     消息尾部判据，故 ReconcileOutcome.Pending 在本 adapter 仍恒为 0
```

（只改注释，不动签名/行为。）

**测试范围声明**：`go test ./internal/executor/opencode/ -run TestB395 -count=1`。

**注释**：T1.3a/b 的 why 已写在代码块内（为什么恢复 + 保留 Warn 的理由）；T1.3c 是注释修订。

---

#### T1.4 红色回路（缝 = `Adapter` 重连恢复，入口 = `startRun` + 真实 SSE 断连重连 + `Events`）

新文件 `internal/executor/opencode/b395_permission_recover_test.go`：

```go
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestB395ReconnectRecoversPendingPermission 锁 spec §4.2/§4.5：SSE 断连重连后
// 必须重新发现并重放本任务未决的权限，使回合有限收口，而不是永挂等人工。
//
// 红（本节点已亲跑，原始输出见台账 §6）：重连只做回合对账，零 permission 事件，
// 3s 窗超时红。
// 绿（T1.3a）：重连后 GET /permission 命中 per_lost 并重放 permission 事件。
// 变异自验：注释掉 onReconnect 里的 rediscoverPendingPermissions 调用 → 复红。
//
// 缝：真实 subscribeLoop 的 onReconnect（生产重连路径），不是直接调内部方法——
// 判据钉在「重连这一动作之后用户能收到权限事件」，而不是「方法被调用过」。
func TestB395ReconnectRecoversPendingPermission(t *testing.T) {
	quietLog(t)
	var mu sync.Mutex
	conns := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			fmt.Fprint(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/permission":
			// 断连间隙新产生的挂起权限（本会话一条 + 别的会话一条，验证过滤）
			fmt.Fprint(w, `[
				{"id":"per_lost","sessionID":"sess-1","permission":"bash","patterns":["ls"],
				 "metadata":{"command":"ls"},"tool":{"messageID":"m","callID":"c"}},
				{"id":"per_other","sessionID":"ses_other","permission":"bash","patterns":[],
				 "metadata":{"command":"whoami"},"tool":{"messageID":"m2","callID":"c2"}}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/session/sess-1/message":
			fmt.Fprint(w, `[]`) // 对账无终态，不干扰权限断言
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			mu.Lock()
			conns++
			n := conns
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			if n == 1 {
				fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
				fl.Flush()
				return // 断流：触发重连
			}
			fl.Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer func() {
		srv.CloseClientConnections()
		srv.Close()
	}()

	ad := New(nil)
	taskID := "task-b395-reconnect"
	taskDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(taskDir, promptFileName), []byte("plan"), 0o644); err != nil {
		t.Fatalf("写 prompt.md: %v", err)
	}
	req := executor.StartReq{Task: proto.Task{ID: taskID, RepoPath: t.TempDir()}, TaskDir: taskDir}
	t.Cleanup(func() { _ = ad.Stop(taskID) })
	api := NewAPIWithSSEBackoff(srv.URL, adapterTestPassword, 50*time.Millisecond, 200*time.Millisecond)
	if _, err := ad.startRun(context.Background(), req, api, &fakeProbe{alive: true}); err != nil {
		t.Fatalf("startRun: %v", err)
	}

	ch := ad.Events(taskID)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != "permission" {
				continue
			}
			if ev.PermissionID != "per_lost" {
				t.Fatalf("只应重放本会话的 per_lost，实得 %q（别的会话的权限不得串台）",
					ev.PermissionID)
			}
			if !strings.Contains(ev.Text, "ls") {
				t.Fatalf("重放权限描述应含命令 ls，实得 %q", ev.Text)
			}
			return // 绿
		case <-deadline:
			t.Fatal("重连后未重新发现挂起权限 per_lost（B395 红：权限丢失后回合永挂）")
		}
	}
}

// TestB395RediscoverFiltersNonTaskSession 锁归属过滤：GET /permission 返回别的
// 会话的挂起权限时不得重放（与实时路径 acceptForeign 同规则）。
//
// 本用例入口是未导出方法 rediscoverPendingPermissions——属**内部锁**，理由见
// §8 占位符扫描自我声明（从声明缝可构造，但会与上一条重复起同一张假 server；
// 本条只作附加，不顶替上一条缝级断言）。
func TestB395RediscoverFiltersNonTaskSession(t *testing.T) {
	quietLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/permission" {
			fmt.Fprint(w, `[
				{"id":"per_other","sessionID":"ses_other","permission":"bash","patterns":[],
				 "metadata":{"command":"whoami"},"tool":{"messageID":"m2","callID":"c2"}}]`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")

	a.rediscoverPendingPermissions(context.Background(), "task-1")
	if ev, ok := drainOne(r); ok {
		t.Fatalf("别的会话的挂起权限不应重放，却收到 %+v", ev)
	}
}

// TestPendingPermissionAskedPropsRoundTrip 锁序列化边界（五项检查 #2）：PendingPermission
// 经 permissionAskedProps 编码成 SSE properties，再经 mapPermissionAsked 解码，
// 描述必须与实时路径同形（metadata.command / patterns / 无描述兜底三条真实形态），
// 且不 panic。入口是编码函数 permissionAskedProps（内部锁，声明见 §8），跑的是
// **真实序列化边界**。
func TestPendingPermissionAskedPropsRoundTrip(t *testing.T) {
	quietLog(t)
	a := New(nil)
	r := a.newRun("task-1", t.TempDir(), t.TempDir())
	r.session = "ses_a"
	r.approval = nil // 走 a.emit，便于 drainOne 读事件

	cases := []struct {
		name    string
		perm    PendingPermission
		wantID  string
		wantTxt string
	}{
		{
			name: "metadata.command 存在（真实 perm_bash 形态）",
			perm: PendingPermission{
				ID: "per_1", SessionID: "ses_a", Permission: "bash",
				Patterns: []string{"ls"}, Metadata: json.RawMessage(`{"command":"ls -la"}`),
				Tool:     PendingPermissionTool{MessageID: "m", CallID: "c"},
			},
			wantID: "per_1", wantTxt: "bash: ls -la",
		},
		{
			name: "metadata 缺失 → 退回 patterns（区分缺失与零值）",
			perm: PendingPermission{
				ID: "per_2", SessionID: "ses_a", Permission: "edit",
				Patterns: []string{"probe.md"}, Metadata: nil,
				Tool:     PendingPermissionTool{MessageID: "m2", CallID: "c2"},
			},
			wantID: "per_2", wantTxt: "edit: probe.md",
		},
		{
			name: "metadata 为空对象 → 结构提取不出，仍带兜底描述（不 panic）",
			perm: PendingPermission{
				ID: "per_3", SessionID: "ses_a", Permission: "",
				Patterns: nil, Metadata: json.RawMessage(`{}`),
				Tool:     PendingPermissionTool{},
			},
			wantID: "per_3", wantTxt: "opencode 未提供权限描述（id per_3），请 handoff attach 查看现场",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := permissionAskedProps(tc.perm)
			r.turnMu.Lock()
			a.mapPermissionAsked(r, props)
			r.turnMu.Unlock()
			ev, ok := drainOne(r)
			if !ok {
				t.Fatal("未产出 permission 事件")
			}
			if ev.Type != "permission" || ev.PermissionID != tc.wantID {
				t.Fatalf("事件 = %+v，want permission/%s", ev, tc.wantID)
			}
			if ev.Text != tc.wantTxt {
				t.Fatalf("描述 = %q，want %q", ev.Text, tc.wantTxt)
			}
		})
	}
}

// TestPendingPermissionPropsProjection 锁序列化边界的关键投影性质：
//   - metadata 缺失（len==0）时 permissionAskedProps **不得**写出 metadata 键，
//     否则消费侧无法区分「服务端没给」与「给了空对象」；
//   - metadata 非空时必须原样带出（不得丢字段）；
//   - patterns 缺失与空切片都编码成数组（消费侧同义），id/tool 键逐字保留。
//
// 为什么不断言 json.RawMessage 的 nil/零值往返：encoding/json 把 nil RawMessage
// 编码成字面量 null、解码回 []byte("null") 而非 nil，这是标准库既定行为，不是本
// 卡要锁的性质。真正承重的是「producer 是否写出该键」（上方三条），消费侧对
// 「键缺失 vs 空对象」的区分由 TestPendingPermissionAskedPropsRoundTrip 的三个
// 用例覆盖（缺失→退回 patterns；空对象→无描述兜底）。
func TestPendingPermissionPropsProjection(t *testing.T) {
	noMeta := permissionAskedProps(PendingPermission{
		ID: "per_2", SessionID: "ses_a", Permission: "edit",
		Patterns: []string{"probe.md"}, Metadata: nil,
	})
	if strings.Contains(string(noMeta), `"metadata"`) {
		t.Fatalf("metadata 缺失时不得写出该键，实得 %s", noMeta)
	}
	if !strings.Contains(string(noMeta), `"callID"`) {
		t.Fatalf("tool 键必须保留，实得 %s", noMeta)
	}

	withMeta := permissionAskedProps(PendingPermission{
		ID: "per_1", SessionID: "ses_a", Permission: "bash",
		Patterns: []string{"ls"}, Metadata: json.RawMessage(`{"command":"ls -la"}`),
		Tool:     PendingPermissionTool{MessageID: "m", CallID: "c"},
	})
	var back map[string]any
	if err := json.Unmarshal(withMeta, &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back["id"] != "per_1" || back["sessionID"] != "ses_a" || back["permission"] != "bash" {
		t.Fatalf("关键字段丢失: %v", back)
	}
	md, ok := back["metadata"].(map[string]any)
	if !ok || md["command"] != "ls -la" {
		t.Fatalf("metadata 未原样带出: %v", back["metadata"])
	}
	tool, ok := back["tool"].(map[string]any)
	if !ok || tool["messageID"] != "m" || tool["callID"] != "c" {
		t.Fatalf("tool 未原样带出: %v", back["tool"])
	}

	// PendingPermission 自身能被 GET /permission 的真实响应解析（perm_bash 形态）。
	var p PendingPermission
	raw := []byte(`{"id":"per_bash","sessionID":"ses_x","permission":"bash",
		"patterns":["ls"],"metadata":{"command":"ls"},"always":["ls *"],
		"tool":{"messageID":"m","callID":"c"}}`)
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("解析真实响应失败: %v", err)
	}
	if p.ID != "per_bash" || p.Tool.CallID != "c" || string(p.Metadata) == "" {
		t.Fatalf("真实响应字段丢失: %+v", p)
	}
}
```

**测试范围声明**：`go test ./internal/executor/opencode/ -run 'TestB395|TestPendingPermission' -count=1`。
**变异复验**：注释掉 T1.3a 的 `go a.rediscoverPendingPermissions(...)` → `TestB395ReconnectRecoversPendingPermission` 复红。
**日志与注释**：测试文件头写职责/缝/红绿/变异；每个测试写 why。

> **夹具说明（正当出口，见 §8）**：`quietLog`/`promptFileName`/`adapterTestPassword`/`newTestAdapter`/`drainOne` 均为 `internal/executor/opencode` 包内既有夹具（`adapter_test.go`、`reconcile_internal_test.go`），本文件直接复用，不新建。断言已逐条列全（可判 pass/fail）。

---

### T2（反例/不误伤）：正常权限流程不回归 + 端点缺失优雅降级

**目标**：验证 T1 没有破坏正常权限门流程（请求→裁决→工具继续），且旧版 opencode 无 `GET /permission` 时重连不炸、不误放行、保留可见告警。

**本 task 只加测试，不改生产代码。**

新文件 `internal/executor/opencode/b395_regression_test.go`：

```go
package opencode

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestB395Reconnect404DegradesGracefully 锁版本边界：旧版 opencode 无 GET /permission
// （404）时，重连恢复必须**降级**——保留可见告警、不产 permission 事件、不死循环、
// 不把任务判死。
//
// 绿色基线（今天已绿）：今天重连本就不查该端点。本用例是**反例回归**，防 T1 把
// 404 当致命错误重试/崩掉。变异：让 ListPendingPermissions 的 404 走 panic 或让
// rediscoverPendingPermissions 返回错误并中断恢复 → 本用例（或既有恢复用例）复红。
func TestB395Reconnect404DegradesGracefully(t *testing.T) {
	buf := captureLog(t)
	var mu sync.Mutex
	conns := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			fmt.Fprint(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/permission":
			w.WriteHeader(http.StatusNotFound) // 旧版：端点不存在
		case r.Method == http.MethodGet && r.URL.Path == "/session/sess-1/message":
			fmt.Fprint(w, `[]`)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			mu.Lock()
			conns++
			n := conns
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			if n == 1 {
				fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
				fl.Flush()
				return
			}
			fl.Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer func() {
		srv.CloseClientConnections()
		srv.Close()
	}()

	ad := New(nil)
	taskID := "task-b395-404"
	taskDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(taskDir, promptFileName), []byte("plan"), 0o644); err != nil {
		t.Fatalf("写 prompt.md: %v", err)
	}
	req := executor.StartReq{Task: proto.Task{ID: taskID, RepoPath: t.TempDir()}, TaskDir: taskDir}
	t.Cleanup(func() { _ = ad.Stop(taskID) })
	api := NewAPIWithSSEBackoff(srv.URL, adapterTestPassword, 50*time.Millisecond, 200*time.Millisecond)
	if _, err := ad.startRun(context.Background(), req, api, &fakeProbe{alive: true}); err != nil {
		t.Fatalf("startRun: %v", err)
	}
	ch := ad.Events(taskID)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == "permission" {
				t.Fatalf("端点 404 时不得重放任何权限事件，却收到 %+v", ev)
			}
		case <-deadline:
			// 断言可见告警仍在（降级而非静默）
			if !strings.Contains(buf.String(), "重新发现挂起权限失败") {
				t.Fatal("旧版端点缺失时应保留可见告警（降级而非静默）")
			}
			return
		}
	}
}

// TestB395LivePermissionFlowNotRegressed 锁 spec §4.4「不误伤」：T1 落地后，正常的
// 权限门流程（permission.asked → RespondPermission → 工具继续）行为逐字不变——
// 事件产出一次、应答路径与 body 契约不变。复用既有 TestStartToPermissionFlow 的
// 假 server 形态，断言 T1 没有把权限事件变成重放/双份。
//
// 绿色基线（今天已绿）：既有 TestStartToPermissionFlow 已覆盖；本用例补一条
// 「重连前收到真实 permission.asked，重连后 GET /permission 返回同一 id 时不得
// 产生第二张/第二份」（幂等由 manager 工单去重承担，adapter 侧只断言事件可重放
// 且不 panic）。变异：把 T1 的 rediscoverPendingPermissions 写成无条件重发且
// 破坏 mapPermissionAsked 的去重 → 本用例或 TestReconnectWarnsLostPermission 复红。
func TestB395LivePermissionFlowNotRegressed(t *testing.T) {
	quietLog(t)
	taskID := "task-b395-live"
	fs := newFakeServer(t)
	// 真实 permission.asked 先到（正常路径）
	fs.push(permissionAskedEvent("perm-live", "bash", "echo hi"))

	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "permission")
	if ev.PermissionID != "perm-live" {
		t.Fatalf("PermissionID=%q，want perm-live", ev.PermissionID)
	}
	if err := ad.RespondPermission(context.Background(), taskID, "perm-live", "once", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	perms := fs.perms()
	if len(perms) != 1 {
		t.Fatalf("正常权限流程应恰回传一次，实得 %d", len(perms))
	}
	if perms[0].path != "/session/sess-1/permissions/perm-live" {
		t.Fatalf("应答路径=%q，契约被改", perms[0].path)
	}
	if !strings.Contains(perms[0].body, `"response":"once"`) {
		t.Fatalf("应答体=%q，应含 \"response\":\"once\"", perms[0].body)
	}
}
```

**测试范围声明**：`go test ./internal/executor/opencode/ -run 'TestB395' -count=1` 与
`go test ./internal/executor/opencode/ -run 'TestStartToPermissionFlow|TestReconnectWarnsLostPermission|TestOpencodePermissionWaitNotToolTime|TestOpencodePermissionReplyFailureKeepsWaitingWindow|TestReplaySpike3Permission|TestReplaySpike5Classifies' -count=1`。
**注释**：每个测试写职责/红绿或回归性质/变异方向。

---

## 7. 占位符扫描（自我声明）

- **内部锁声明**：
  - T1.4 的 `TestB395RediscoverFiltersNonTaskSession` 入口是未导出方法 `rediscoverPendingPermissions`，属**内部锁**。理由：该断言（会话过滤）**可从声明缝构造**，但会与 `TestB395ReconnectRecoversPendingPermission` 重复起同一张假 server；故只作**附加**，不顶替缝级断言。缝级断言由 `TestB395ReconnectRecoversPendingPermission`（入口 = 真实重连 + `Events`）承担。
  - T1.4 的 `TestPendingPermissionPropsProjection` 入口是未导出编码函数 `permissionAskedProps`，属**内部锁**；它锁的是**序列化边界**（五项检查 #2 强制要求），不顶替任何缝级断言。
  - T2 的两条测试入口分别是真实重连（缝级）与既有真实权限流程（缝级），无内部锁。
- **测试夹具正当出口**：T1.4/T2 复用 `adapter_test.go` 的 `newFakeServer/startFakeRun/fakeProbe/captureLog/quietLog/adapterTestPassword/waitEventType/isTimingEvent` 与 `reconcile_internal_test.go` 的 `newTestAdapter/drainOne`（夹具形态因包而异，属正当出口）。所有断言已逐条列全（可判 pass/fail）。
- **无 TBD /「加适当的错误处理」/「同 Task N」/ 描述做什么却不给代码**；每个生产改动给出完整代码块，每条测试给出完整可编译代码块。
- **退路同闸**：本计划无「若意外先绿就改成直喂 X」式条件退路；T1.4 的变异步骤只改**一处接线**（删一行 `go a.rediscoverPendingPermissions(...)`），不改任何测试入口符号。

## 8. 自审三查

1. **spec 覆盖（逐条指到 task）**：
   - §3 Q1（WARN 发出点 + 窗口按什么持有 + 谁清掉）→ §0 R2/R3（file:line + 真机行号；结论：按任务持有，无人清，是事件丢失致窗口未开）。
   - §3 Q2（断连期间是否重放/缓冲）→ §0 R3/R4（`/event` 无重放；本次事故任务最终仍挂死，反推未重放；**显式查询**修面对重放与否不敏感）。
   - §3 Q3（30m 上界为何没判死）→ §0 R5.3（`hostapi` 只覆盖协调者回合子进程，派发任务不经过；`grep` 无输出佐证）。
   - §3 Q4（复现路径）→ T1.4 红色回路（缝 = 重连恢复，替身 = 假 server + 假探活；今天红已亲跑）。
   - §4.1 根因 → §0 R1-R5。
   - §4.2 红色回路（修后转绿）→ T1.4 `TestB395ReconnectRecoversPendingPermission`。
   - §4.3 变异复验 → T1.4 变异自验（删接线一行复红）。
   - §4.4 不误伤 → T2 `TestB395LivePermissionFlowNotRegressed` + 既有 `TestStartToPermissionFlow`/`TestOpencodePermissionWaitNotToolTime` 等在场。
   - §4.5 真机（一次真实回合人为断连，有限收口）→ **本 task 由协调者执行，不派发**（与执行者纪律「不派发、不调起 executor」冲突）；T1/T2 是它的前置条件。
   - §2 不做：未动权限裁决语义（只重放既有事件，判定仍走 `handlePermission`/`approval.Authority`）；未动 B394 判据白名单；未动线上状态（取证全只读，探针 server 按端口精确 kill，见台账 §7）。✓
2. **占位符扫描**：见 §7，已声明。
3. **跨 task 类型/签名一致性**：`PendingPermission` 在 T1.1 定义、T1.2 `permissionAskedProps` 消费、T1.4 两处测试构造——字段/tag 逐字一致；`ListPendingPermissions` 在 §5 Produces 声明、T1.1 定义、T1.2 调用；`rediscoverPendingPermissions`/`permissionAskedProps` 在 §5 声明、T1.2 定义、T1.3 接线、T1.4 测试调用——各组签名逐字对齐。

## 9. 缺陷族对抗审查（五项检查 #1）

| 族 | 设问 | 结论 |
|---|---|---|
| 静默失败 | 端点 404 时会不会静默不恢复？ | T1.2 保留 Warn；T2 `TestB395Reconnect404DegradesGracefully` 锁可见告警。 |
| 幂等/重放 | 重连时服务端同时经 SSE 重放同一权限，会不会双发工单/双份事件？ | manager `approval.Authority.IsReplay`（`authority.go:113`）按工单+事件去重；adapter 只重放事件，不建工单。T2 的 404 与 T1.4 的过滤断言覆盖边界。 |
| 跨任务串台 | `GET /permission` 返回别的会话/任务挂起权限？ | 每个任务一个 serve（`proc.go` freePort）+ `acceptForeign` 归属判定；T1.4 `TestB395ReconnectRecoversPendingPermission`（含 `per_other` 过滤断言）与 `TestB395RediscoverFiltersNonTaskSession` 锁。 |
| 上下文预算 | 每个 task 有界文件集？ | T1 触达 `api.go`、`permission_recover.go`(新)、`adapter.go`(一行+注释)、`resume.go`(一行)、`reconcile.go`(注释) + 1 测试文件；T2 仅 1 测试文件。有界。 |
| 序列化边界 | 新数据字段经手写投影？ | `PendingPermission → permissionAskedProps → mapPermissionAsked` 是唯一编解码对；T1.4 的 roundtrip 测试穿过该真实边界，用 `json.RawMessage` 可空区分缺失/零值。 |
| 接缝双向 | 每支测试入口在缝上？每条缝有断言？ | 缝1（重连恢复）→ `TestB395ReconnectRecoversPendingPermission`；缝2（重启恢复）→ 与缝1 同一函数（`rediscoverPendingPermissions`），接线一行由 T1.3b；缝3（正常权限流程）→ `TestB395LivePermissionFlowNotRegressed` + 既有测试。内部锁（过滤/roundtrip）已声明且不顶替。 |
| 版本边界 | 目标版本真有该能力？ | `GET /permission` 在本机 1.18.31 实测 200 + OpenAPI 声明；旧版 404 走降级（T2）。 |
