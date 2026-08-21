# 更新面（B166）二期实现计划：执行机一键升级

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让控制台的「设置 → 更新 → 执行机」能一键把一台远端机器升到最新，而**不重写** `handoff upgrade` 已经在生产上跑着的编排。

**Architecture:** 把 `cmd/upgrade.go` 里**单台远端机器**那段（七种结论、两道闸、pull/push 择路、等新版本上线）原样搬进新的 `internal/upgrade` 包，改成**返回结构化结论**而不是往 `io.Writer` 打字；CLI 把结论渲染成它现在那张表（输出逐字节不变），agentd 新增一个端点调同一个函数。本机路径（`localUpgrade` / `localSwap` / `swapAndTell` / `syncSkill`）**留在 CLI 不动**——本机 agentd 的版本由薄壳的同步路决定，控制台上本机那行不给按钮。

**Tech Stack:** Go 1.26、`net/http` 方法路由、React + TypeScript + vitest。

**Spec:** `docs/superpowers/specs/2026-08-19-desktop-update-surface-design.md` §6.5。一期（`prototypes` 形态、提示框、更新页只读、下载端点、托盘）已合并，本计划在它之上做。

---

## Global Constraints

**执行环境**（与一期相同，再说一遍因为它救过一次时间）

- 执行机是 Linux。`desktop/` 根包 import 了 Wails，**编译不过**。本计划**不改 `desktop/`**，所以只要不去碰它就不会遇到。
- 沙箱的 `/tmp` 只读、进程是 root。以下测试在本机会红，**它们是环境假红，与本计划无关，不要去修**：
  `internal/client` 的 `TestCursorRoot*`、`internal/config` 的 `TestLoadStripUpdateDoesNotBlockOnSaveFailure`、
  `internal/executor/claudecode` 的 `TestPermServer*` / `TestResumeContinuesFromOffset`、
  `internal/executor/grok` 的 `TestSyncAuthKeepsTaskCopyWhenWriteFails`、
  `desktop/internal/shell` 的 `TestSyncOnOpenOrderIsLoadBearing`。
  这份清单是一期审核时在 macOS 上逐条复跑确认的。**跑测试时把它们排除在结论之外，但要在 ledger 里写清哪些红属于这一类。**
- `npm ci` 要指定可写缓存：`npm ci --cache <任务目录>/tmp/npm-cache`（默认 `/root/.npm` 只读）。

**这一期最重要的一条约束**

> **`cmd/upgrade_test.go` 一条用例都不许改。**

它是这次搬家唯一可靠的回归网：那些用例断言的是**表格输出的原文**与**动作顺序**。
抽取如果是纯搬家，它们必然全绿；一旦需要改其中任何一条，就说明行为变了——
**停下来，在 ledger 里写清哪条、为什么，不要顺手改测试让它变绿**。

唯一允许迁移的既有测试是 `cmd/upgrade_verdict_test.go`：`classify` 会搬到新包，
它跟着搬，**用例逻辑一条不减、断言一条不弱**。

**其它约定**（与一期相同）

- 日志一律 `slog`；新文件写「职责 + 边界」文件头；导出函数写参数/返回/注意事项；
  非显然分支写「为什么」的中文注释。
- 提交信息中文，格式 `<type>(<scope>): <说明>`。
- 不新增第三方依赖。

**每个 task 收尾前跑**

```bash
gofmt -l . && go vet ./... && go test ./cmd/ ./internal/upgrade/ ./internal/agentd/
cd web && npm run typecheck && npm test
```

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/upgrade/machine.go`（新） | `Machine` / `Verdict` / `Classify`：一台机器的探测结果与唯一结论 |
| `internal/upgrade/remote.go`（新） | `RemoteOne`：把一台**远端**机器升到指定版本，返回结构化结论 |
| `internal/upgrade/*_test.go`（新） | 从 `cmd/upgrade_verdict_test.go` 迁移 + 新增 `RemoteOne` 的用例 |
| `cmd/upgrade_verdict.go`（删） | 内容搬进 `internal/upgrade/machine.go` |
| `cmd/upgrade_verdict_test.go`（删） | 迁到新包 |
| `cmd/upgrade.go`（改） | `machineState` 保留为 CLI 侧的探测载体，转成 `upgrade.Machine` 调新包；把 `Result` 渲染成现在这张表 |
| `internal/agentd/machineupgrade.go`（新） | `POST /api/machines/{name}/upgrade` |
| `internal/agentd/server.go`（改） | 一条新路由 |
| `internal/proto/desktop.go`（改） | `MachineUpgradeResp` |
| `web/src/api/types.ts`、`client.ts`（改） | 升级请求与响应类型 |
| `web/src/app/settings/UpdatePage.tsx`（改） | 执行机块加按钮 |

---

## Task 1: 把结论判据搬进 internal/upgrade

**Files:**
- Create: `internal/upgrade/machine.go`、`internal/upgrade/machine_test.go`
- Delete: `cmd/upgrade_verdict.go`、`cmd/upgrade_verdict_test.go`
- Modify: `cmd/upgrade.go`

**Interfaces:**
- Produces：

```go
// Machine 是一台机器的探测结果。字段语义与 cmd 里原来的 machineState 逐字段一致。
type Machine struct {
	Name     string
	Local    bool
	Agentd   string // 对端上报的 release 版本号
	Revision string // 仅 Agentd 为空时用于渲染
	Platform string // "goos/goarch"；空 = 对端过旧未上报
	// Managed / Pull 是**三态指针**：nil = 对端没上报，与「对端说 false」是两回事。
	// 用 bool 零值把前者塌成后者，就会把「我不知道」讲成「它非托管」——B64 的病根。
	Managed *bool
	Pull    *bool
	Busy    int
	Err     error
}

type Verdict int
const (VerdictUnreachable Verdict = iota; VerdictAgentdDown; VerdictTooOld; VerdictLatest;
       VerdictUnmanaged; VerdictManagedUnknown; VerdictNeedsUpgrade)
func (v Verdict) String() string
func Classify(m Machine, latest string) Verdict
func (m Machine) IsLatest(latest string) bool
```

- [ ] **Step 1: 先读原文**

完整读 `cmd/upgrade_verdict.go`（96 行）与 `cmd/upgrade.go` 里的 `machineState`、`isLatest`。
**那个文件头的注释块（「唯一判据来源」「优先级顺序即判据」B64 的病根）要一并搬过去**，
一个字不改——它是这段代码为什么长这样的全部理由。

- [ ] **Step 2: 迁移测试**

把 `cmd/upgrade_verdict_test.go` 整体搬到 `internal/upgrade/machine_test.go`，
包名改 `upgrade`，`*machineState` 换成 `Machine`，`classify` 换成 `Classify`。
**用例数量与断言强度都不许变。** 搬完先跑，此时应该编译不过（`Classify` 还没有）。

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/upgrade/`
Expected: FAIL，`undefined: Classify`

- [ ] **Step 4: 实现**

新建 `internal/upgrade/machine.go`，把 `verdict` / `classify` / `isLatest` 平移进来并导出。
文件头保留原注释并补一句边界：

```
// 边界：
//   - 纯函数：不做 I/O、不打日志、不产出面向操作者的文案
//   - **不判 busy**：活跃任务是「要不要现在换」的闸，不是「这台机器是什么状态」
//     的结论；它由调用方在 VerdictNeedsUpgrade 之后施加
//   - 不认识 cobra、不认识 Endpoint：本包要能被 agentd 直接调
```

- [ ] **Step 5: 改 CLI 接上新包**

`cmd/upgrade.go` 里：删掉 `verdict`/`classify`/`isLatest`，给 `machineState` 加一个
`func (ms *machineState) toUpgrade() upgrade.Machine`，`process` 改调 `upgrade.Classify`，
`switch` 的 case 换成 `upgrade.VerdictXxx`。**输出的每一个字符都不许变。**

- [ ] **Step 6: 跑回归——这一步是本 task 的正题**

```bash
go test ./internal/upgrade/ -v
go test ./cmd/ -run TestUpgrade -v
go test ./cmd/
```

Expected：全部 PASS，且 **`cmd/upgrade_test.go` 一个字符都没改过**（用 `git diff --stat cmd/upgrade_test.go` 确认无输出）。
若有任何一条红，**先怀疑自己搬错了，不要改测试**。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./cmd/ ./internal/upgrade/
git add internal/upgrade cmd/
git commit -m "refactor(upgrade): 结论判据搬进 internal/upgrade，CLI 输出不变"
```

---

## Task 2: 把单台远端升级搬进 internal/upgrade

**Files:**
- Create: `internal/upgrade/remote.go`、`internal/upgrade/remote_test.go`
- Modify: `cmd/upgrade.go`

**Interfaces:**
- Produces：

```go
// Status 是一台机器处理完的三态。
type Status int
const (StatusOK Status = iota; StatusSkip; StatusFail)

// Result 是结构化结论。**不含任何排版**：机器名、列宽、缩进都由调用方决定。
type Result struct {
	Verdict Verdict
	Status  Status
	// Reason 是一句人话的结论（例如「2 个活跃任务」「对端 agentd 过旧，未上报平台…」）。
	Reason string
	// Remedy 是处置建议；**空串表示不给建议**。够不着时必须为空——
	// 编一条建议就是在猜，而猜出来的建议会把人引到错误的方向。
	Remedy string
	// Forcible 表示这个 Skip 能不能被 Force 越过。
	// **闸二（非托管）与「已有自拉在跑」永远为 false**：force 不越过它们，
	// 给了就是让人跑一条注定失败的命令。
	Forcible bool
	From, To string // Status==StatusOK 时的版本迁移
}

type Peer interface {
	PushUpdate(ctx context.Context, tag, sum string, tgz []byte, force bool) (*proto.UpdateResp, error)
	PullUpdate(ctx context.Context, tag, sum string, force bool) (*proto.UpdateResp, error)
	WaitVersion(ctx context.Context, want string, timeout, interval time.Duration, checkPull bool) error
}

type Fetcher interface {
	FetchArchive(ctx context.Context, rel release.Release, goos, goarch string) ([]byte, string, error)
	FetchChecksum(ctx context.Context, rel release.Release, goos, goarch string) (string, error)
}

type Options struct {
	Force      bool          // 越过闸一（活跃任务）。**永不越过闸二**
	PreferPush bool          // 对应 CLI 的 --push
	WaitPull, WaitPush, WaitInterval time.Duration
}

// RemoteOne 把一台**远端**机器升到 rel，返回结构化结论。
//
// 注意：
//   - **只处理远端。** 本机路径（换文件后自己重启、skill 同步）留在 CLI，
//     两者的失败语义完全不同，合并会让本已复杂的分支表再翻一倍
//   - progress 可为 nil；非 nil 时按阶段回调一句人话，供 agentd 落进日志
func RemoteOne(ctx context.Context, log *slog.Logger, m Machine, peer Peer, f Fetcher,
	rel release.Release, o Options, progress func(string)) Result
```

- [ ] **Step 1: 先读原文**

完整读 `cmd/upgrade.go:603-682` 的 `remoteUpgrade`，以及它读到的两个包级变量
`upgradeForce` / `upgradePush`（它们要变成 `Options` 的字段）与三个超时常量。
**三处「措辞必须不同」的注释**（三种拒绝的出路不同、自拉与推送的失败措辞不同）
是这段代码的承重理由，搬过去时一并搬。

- [ ] **Step 2: 写新包的测试**

`internal/upgrade/remote_test.go`。用假 `Peer` 与假 `Fetcher`（**必须整套替身化——
漏替一个就会在 CI 上真的去动一台机器**，这是 `cmd/upgrade.go` 文件头写死的纪律）。
至少覆盖：

```go
// 对端支持自拉时走 pull，不下载资产
func TestRemoteOneUsesPullWhenCapable(t *testing.T)
// 对端没上报 pull 能力（nil）时退回 push——nil 不许当 true 用
func TestRemoteOneFallsBackToPushWhenPullUnknown(t *testing.T)
// 三种拒绝各给各的处置，且非托管与自拉在跑的 Forcible 必须是 false
func TestRemoteOneRejectionsCarryMatchingRemedy(t *testing.T)
// 等新版本上线超时：pull 与 push 的 Reason 必须不同措辞
//（push 模式二进制已在对端，提回滚是对的；pull 模式可能连下载都没成，提回滚是误导）
func TestRemoteOneWaitFailureWordingDiffersByMode(t *testing.T)
// 平台格式非法时失败，且不去猜一个默认平台
func TestRemoteOneRefusesMalformedPlatform(t *testing.T)
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/upgrade/ -run TestRemoteOne -v`
Expected: FAIL，`undefined: RemoteOne`

- [ ] **Step 4: 实现**

平移 `remoteUpgrade`，把每一处 `fmt.Fprintf(out, ...)` 换成给 `Result` 赋值。
**分支集合与优先级一个都不许增删。**

- [ ] **Step 5: 改 CLI**

`cmd/upgrade.go` 的 `remoteUpgrade` 退化成：转成 `upgrade.Machine` → 调 `RemoteOne`
→ 把 `Result` 渲染成原来那两行（`%-8s 跳过   %s\n` 与缩进 9 空格的处置行）。
闸一（`ms.Busy > 0 && !upgradeForce`）留在 CLI 的 `process` 里还是搬进 `RemoteOne`，
**二选一并在 ledger 里说明理由**；不管选哪个，CLI 的输出都不许变。

- [ ] **Step 6: 跑回归——正题**

```bash
go test ./internal/upgrade/ -v
go test ./cmd/ -v 2>&1 | tail -30
git diff --stat cmd/upgrade_test.go     # 必须无输出
```

- [ ] **Step 7: 变异复验（必做）**

把 `RemoteOne` 里「对端没上报 pull 能力时退回 push」的判断改成「nil 当 true」，
`TestRemoteOneFallsBackToPushWhenPullUnknown` **必须红**；改回来必须绿。
再把非托管拒绝的 `Forcible` 改成 `true`，对应用例**必须红**。两次原文都记进 ledger。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go vet ./... && go test ./cmd/ ./internal/upgrade/
git add internal/upgrade cmd/
git commit -m "refactor(upgrade): 单台远端升级搬进 internal/upgrade，返回结构化结论"
```

---

## Task 3: agentd 的升级端点

**Files:**
- Create: `internal/agentd/machineupgrade.go`、`internal/agentd/machineupgrade_test.go`
- Modify: `internal/proto/desktop.go`、`internal/agentd/server.go`

**Interfaces:**
- Produces：`POST /api/machines/{name}/upgrade`（`?force=1` 越过闸一）

```go
// MachineUpgradeResp 是 POST /api/machines/{name}/upgrade 的响应。
type MachineUpgradeResp struct {
	// Accepted=true 时升级已在后台开始，进度靠 GET /api/machines 的 version 变化观察。
	Accepted bool   `json:"accepted"`
	Verdict  string `json:"verdict"`
	Reason   string `json:"reason,omitempty"`
	Remedy   string `json:"remedy,omitempty"`
	// Forcible 表示这次拒绝能不能被 ?force=1 越过。**非托管永远 false。**
	Forcible bool `json:"forcible"`
	Busy     int  `json:"busy,omitempty"`
}
```

- [ ] **Step 1: 写失败的测试**

照 `internal/agentd/machines_test.go` 的建 env 方式。至少五条：

```go
// 机器不在配置里 → 404
func TestMachineUpgradeUnknownMachine(t *testing.T)
// 本机（name 为空或本机名）→ 400：本机版本由薄壳同步路决定，
// 这里再给一个入口就是第二条换版路径，两条会打架
func TestMachineUpgradeRefusesLocal(t *testing.T)
// 有活跃任务且未 force → 409，体里带 busy 与 forcible=true
func TestMachineUpgradeBusyIsForcible(t *testing.T)
// 对端明确非托管 → 422，且 forcible=false（force 不越过闸二）
func TestMachineUpgradeUnmanagedIsNotForcible(t *testing.T)
// 够不着 → 502，且 remedy 为空（不编处置）
func TestMachineUpgradeUnreachableInventsNoRemedy(t *testing.T)
// 可升级 → 202，且后台真的调了 RemoteOne（用假 runner 缝断言）
func TestMachineUpgradeAcceptedRunsInBackground(t *testing.T)
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestMachineUpgrade -v`
Expected: FAIL

- [ ] **Step 3: 实现**

新建 `internal/agentd/machineupgrade.go`，文件头写清：

```
// 本文件实现 POST /api/machines/{name}/upgrade：把一台远端执行机升到最新。
//
// 边界（承重）：
//   - **不重写编排。** 七种结论、两道闸、pull/push 择路、等新版本上线全在
//     internal/upgrade 里，与 handoff upgrade 共用同一份。这里只做三件事：
//     探一台机器、把结论翻成 HTTP 状态码、把动作丢进后台
//   - **不处理本机。** 本机 agentd 的版本由薄壳的同步路决定（spec §6.5）；
//     在这里再开一个入口就是第二条换版路径，两条会打架
//   - **不造进度流。** 升级完成的判据就是 GET /api/machines 里那台的 version 变了
```

要点：

- 探测：对该 target 调一次 `client.Status`，把 `StatusResp` 投影成 `upgrade.Machine`
  （`Managed`/`Pull`/`Platform` 三个字段**照抄指针，nil 保持 nil**）。
- 结论 → 状态码：`VerdictUnreachable`→502、`VerdictUnmanaged`→422、
  `VerdictTooOld`/`VerdictManagedUnknown`→422、`VerdictLatest`→200（`accepted:false`）、
  闸一未越过→409、其余→202。
- 202 之后起 goroutine 跑 `upgrade.RemoteOne`，**用 `context.Background()` 加自己的超时**，
  不要用请求的 ctx——请求早就返回了，用它会让升级在响应写完的瞬间被取消。
- 同一台机器同时只允许一个升级在跑：`s.upgradeMu` 保护一个 `map[string]bool`，
  重复请求返回 409。**goroutine 结束时无论成败都要清掉**（用 defer）。
- 日志：受理（机器名 + 目标版本）、每道闸的拒绝理由、后台结果（成功的版本迁移 / 失败原因）。
- 路由：`api.HandleFunc("POST /api/machines/{name}/upgrade", s.handleMachineUpgrade)`

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestMachineUpgrade -v`

- [ ] **Step 5: 变异复验（必做）**

把非托管的状态码从 422 改成 409、`Forcible` 改成 true，
`TestMachineUpgradeUnmanagedIsNotForcible` **必须红**；改回来绿。原文记进 ledger。

- [ ] **Step 6: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/agentd/ ./internal/upgrade/ ./cmd/
git add internal/agentd internal/proto
git commit -m "feat(agentd): 单台执行机升级端点，复用 internal/upgrade 的编排"
```

---

## Task 4: 更新页的升级按钮

**Files:**
- Modify: `web/src/api/types.ts`、`web/src/api/client.ts`、`web/src/app/settings/UpdatePage.tsx`
- Test: `web/src/app/settings/UpdatePage.test.tsx`

- [ ] **Step 1: 写失败的测试**

```tsx
// 远端可升级的机器给按钮；点了之后按钮进「升级中…」并禁用
it('远端可升级的机器显示升级按钮并在点击后禁用', async () => { … })
// 本机那行永远没有按钮
it('本机行不给升级按钮', () => { … })
// 409 且 forcible=true → 就地给「仍要升级」
it('有活跃任务时给「仍要升级」', async () => { … })
// 422（非托管）→ 显示 remedy，**不给**「仍要升级」
it('非托管时只显示处置建议，不给强制入口', async () => { … })
```

- [ ] **Step 2–4: 跑红 → 实现 → 跑绿**

`upgradeMachine(name, force)` 走 `POST /api/machines/${name}/upgrade${force ? '?force=1' : ''}`。
升级发起后靠已有的 `fetchMachines()` 轮询观察 `version` 变化，**不新造进度状态**。
成功的判据是那台机器的 `version` 等于 `latest.tag`。

- [ ] **Step 5: 提交**

```bash
cd web && npm run typecheck && npm test && npm run build
git add web/src
git commit -m "feat(web): 更新页支持一键升级执行机"
```

---

## Task 5: 整分支终审

- [ ] **Step 1: 全量回归**

```bash
gofmt -l .
go vet ./...
go test ./...
cd web && npm run typecheck && npm test && npm run build
```

每条命令的实际输出都贴进 ledger。**根模块的红要逐条对照 Global Constraints 里那份
环境假红清单分类**：属于清单的写「环境假红」，不属于的就是真问题，必须修。

- [ ] **Step 2: 回归网复查（本期最重要的一条）**

```bash
git diff --stat 3addd708..HEAD -- cmd/upgrade_test.go
```

Expected：**无输出**。有输出就说明搬家改了行为，在 ledger 里逐条说明改了什么、为什么。

- [ ] **Step 3: 对照 spec §6.5 自查**

逐条列出落点文件与行号：不重写编排、五种状态码、非托管永不给强制入口、
不造进度流、本机不给按钮。

- [ ] **Step 4: 写交付摘要**

分支名、提交数、每条验证命令的实际输出、未验证项清单。

---

## 审核者本地验收（**不派发，执行者不要做这一段**）

1. 在 macOS 上复跑三条链路，确认环境假红清单之外全绿。
2. 用真实的 linux-01 做一次端到端：从控制台把它的 agentd 从旧版升到最新，
   确认 `GET /api/machines` 里那台的 `version` 变了。
   **这一步要驱动 handoff 自身，执行机做不了也不该做。**
3. 形态对照 `prototypes/desktop-update/pages/settings.html` 的执行机块。
