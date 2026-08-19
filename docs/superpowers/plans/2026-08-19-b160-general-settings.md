# B160 控制台「常规」分区与机器级缺省执行者 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把设置页的「常规」占位换成这个浏览器的显示偏好（与左栏同一份状态），并在开发机详情新增可改的「缺省执行者 + 它的默认模型」块，保存后下一个任务即生效、不必重启 agentd。

**Architecture:** 承重改动是让 `Manager` 不再读构造时的配置快照——`Server.SetManager` 挂接时把活配置取值函数注入进去，7 处 `m.cfg.Executor.*` 读点改走它。其上加两个走 `?machine=` 转发的端点。前端把左栏私有的偏好状态提到模块级共享层，设置页与左栏共用。

**Tech Stack:** Go 1.26.1（slog、`net/http` Go1.22 方法路由、`gopkg.in/yaml.v3`）、React + TypeScript + Vite + vitest + Testing Library、Tailwind/shadcn。

**Spec:** `docs/superpowers/specs/2026-08-19-b160-general-settings-design.md`
**Base:** 以 `handoff/web-console` 最新处开分支
**并行注意:** **B158（env 配置面）正在并行进行**，它改了 `NewManager` 的签名与所有测试调用点。本计划**刻意不动 `NewManager` 的签名**（走 `SetManager` 注入，见 Task 1），以免两条分支在十几个调用点上冲突。**任何时候都不要为了「顺手」去改 `NewManager` 的参数表。**

## Global Constraints

- Go 侧日志一律 `slog`（agentd 内用 `s.log` / `m.log`），**禁止 `fmt.Printf`**；前端**禁止 `console.log`**（`console.warn` 仅限降级诊断）。
- 新建文件必须有文件头注释（职责 + 边界）；导出函数必须有 doc 注释（参数、返回、注意事项）；非显然分支写「为什么」的中文注释。
- `internal/` 下禁止 `os.RemoveAll`。
- **`NewManager` 的签名与参数表不许动**（理由见上）。
- **`Executor.Model` 的语义（已实测，不许按旧印象写）**：`resolveModel` 只在 `execName == cfg.Executor.Default` 时才套用它，派别的执行器返回空串。所以它是「**缺省执行者的**默认模型」，不是全局默认。界面文案必须照这个语义写。
- **`Executor.Model` 服务端不校验**：agentd 不认识任何执行器的模型名单，没有可判据。`Executor.Default` 必须校验（在已注册 adapter 名单内且非空）。
- `swapConf` **不需要**为 `Executor` 补深拷：`config.ExecutorConfig` 是两个 `string` 的值类型，结构体浅拷即完整拷贝。**不要照着 `Targets`/`Discipline`/`Env` 的样子再加一层**——对无引用字段的 struct 做「深拷」是噪声，还会误导下一个人以为它有引用字段。
- 契约改动流程：改 Go 结构 → `go test ./internal/proto/ -run TestContractFixtures -update` → 同步 `web/src/api/types.ts` 与 `web/src/api/contract.test.ts`。
- 每个 task 完成即 commit；提交信息用各 task「Commit」步骤给出的原文。
- 完工前必须跑：`gofmt -l .`（无输出）、`go build ./... && go vet ./... && go test ./...`、`cd web && npx vitest run && npx tsc -b && npx eslint .`。

---

### Task 1: `Manager` 读活配置（承重）

**Files:**
- Modify: `internal/agentd/manager.go`（加 `conf` 字段；`adapterFor` / `resolveExecutor` / `resolveModel` 与另两处读点改走它）
- Modify: `internal/agentd/status.go`（两处读点）
- Modify: `internal/agentd/server.go`（`SetManager` 注入）
- Modify: `internal/agentd/manager_test.go`（或就近的测试文件，加回归用例）

**Interfaces:**
- Consumes: 既有的 `(*Server).conf()`（同包私有，可直接取函数值）
- Produces: `Manager.conf func() *config.Config`（私有字段）；`SetManager` 挂接时注入

> **本 task 是整个 B160 的地基。** 不做它，Task 4 的保存端点会「落盘成功但派发照旧用旧值」，且 `GET /api/status` 会一直回旧的 `default_executor`——开发机列表上那个「默认」标记不动，看起来像没保存成功。

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentd/manager_test.go`：

```go
// TestManagerReadsLiveExecutorDefault 钉住：swapConf 改完缺省执行者后，
// Manager 立即用新值解析 adapter，**不需要重建 Manager、不需要重启 agentd**。
//
// 为什么这条是承重的：m.cfg 存的是 NewManager 那一刻的 *config.Config 指针，
// 而 swapConf 做的是 next := *old + s.cfg.Store(&next)——换的是新指针，
// m.cfg 永远停在构造那一刻。B157/B158 的热更新走的是各自的取值函数旁路，
// Executor.Default 没有那条旁路。
func TestManagerReadsLiveExecutorDefault(t *testing.T) {
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: t.TempDir(),
		Executor: config.ExecutorConfig{Default: "fake", Model: "m-fake"},
	}, discardLogger())
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	ads := map[string]executor.Adapter{"fake": &failStartAdapter{}, "opencode": &failStartAdapter{}}
	mgr := NewManager(env.st, env.srv.Hub(), ads, env.srv.conf(),
		env.srv.DisciplineMapping, nil, newTestGate(t), discardLogger())
	env.srv.SetManager(mgr) // 注入活配置就发生在这一刻

	name, _, err := mgr.resolveExecutor("")
	if err != nil || name != "fake" {
		t.Fatalf("改之前 name = %q, err = %v，想要 fake", name, err)
	}
	if got := mgr.resolveModel("", "fake"); got != "m-fake" {
		t.Fatalf("改之前 model = %q，想要 m-fake", got)
	}

	if err := env.srv.swapConf(func(c *config.Config) error {
		c.Executor = config.ExecutorConfig{Default: "opencode", Model: "m-oc"}
		return nil
	}); err != nil {
		t.Fatalf("swapConf: %v", err)
	}

	name, _, err = mgr.resolveExecutor("")
	if err != nil || name != "opencode" {
		t.Fatalf("改之后 name = %q, err = %v，想要 opencode（Manager 必须读活配置）", name, err)
	}
	// model 跟着 default 一起换作用对象：现在 fake 不再是缺省，不该再套配置模型
	if got := mgr.resolveModel("", "opencode"); got != "m-oc" {
		t.Fatalf("改之后 opencode 的 model = %q，想要 m-oc", got)
	}
	if got := mgr.resolveModel("", "fake"); got != "" {
		t.Fatalf("改之后 fake 的 model = %q，想要空串（它已不是缺省执行者）", got)
	}
}

// TestStatusReportsLiveExecutorDefault 钉住 status.go 那两处读点。
//
// 漏改它的症状很隐蔽：保存成功、config.yaml 也对，但开发机列表上的「默认」
// 标记来自 GET /api/status 的 default_executor，一直显示旧值——看起来像没保存成功。
func TestStatusReportsLiveExecutorDefault(t *testing.T) {
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: t.TempDir(),
		Executor: config.ExecutorConfig{Default: "fake"},
	}, discardLogger())
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	mgr := NewManager(env.st, env.srv.Hub(),
		map[string]executor.Adapter{"fake": &failStartAdapter{}, "opencode": &failStartAdapter{}},
		env.srv.conf(), env.srv.DisciplineMapping, nil, newTestGate(t), discardLogger())
	env.srv.SetManager(mgr)

	if err := env.srv.swapConf(func(c *config.Config) error {
		c.Executor.Default = "opencode"
		return nil
	}); err != nil {
		t.Fatalf("swapConf: %v", err)
	}
	var st proto.StatusResp
	if code := env.getJSON(t, "/api/status", &st); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if st.DefaultExecutor != "opencode" {
		t.Fatalf("default_executor = %q，想要 opencode", st.DefaultExecutor)
	}
}
```

> **注意**：上面两个用例里的 `NewManager(...)` 实参表按**当前分支**写。若本分支已经并入 B158（多一个 `envMapping` 参数），照当前签名补实参即可——**但不要反过来去改 `NewManager` 的签名**。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run 'TestManagerReadsLiveExecutorDefault|TestStatusReportsLiveExecutorDefault' -count=1`
Expected: FAIL —— 改之后仍解析出 `fake`；`default_executor` 仍是 `fake`。

- [ ] **Step 3: 加字段与注入点**

`internal/agentd/manager.go` 的 `Manager` 结构体加字段：

```go
	// conf 取当前配置快照。**不要改回直接读 cfg**：cfg 是 NewManager 那一刻的
	// 指针，而 swapConf 换的是新指针，运行期可变的字段（Executor / Targets）
	// 读 cfg 永远是旧值（B160 §4.2）。默认值等价于旧行为，Server.SetManager
	// 挂接时会换成活的。
	conf func() *config.Config
```

`NewManager` 的结构体字面量里补（**签名不动**）：

```go
		conf: func() *config.Config { return cfg },
```

`internal/agentd/server.go` 的 `SetManager`：

```go
// SetManager 把任务管理器挂到 Server 上。
//
// 注意：挂接时会把 Server 的活配置取值函数交给 Manager——Manager 构造时收到的
// 是一份配置**快照指针**，而 swapConf 换的是新指针，读快照永远拿不到控制台改过
// 的值（B160 §4.2）。这一步是「保存后下一个任务即生效」成立的前提。
func (s *Server) SetManager(m *Manager) {
	s.mgr = m
	if m != nil {
		m.conf = s.conf
	}
}
```

- [ ] **Step 4: 替换 7 处读点**

Run: `grep -n "m\.cfg\.Executor\." internal/agentd/*.go | grep -v _test.go`
Expected: 恰好 7 行 —— `manager.go` 的 `adapterFor`(288)、`resolveExecutor`(305)、`resolveModel`(333/334)、1155、3196，`status.go` 的 72、138。

把这 7 处的 `m.cfg.Executor.` 全部改成 `m.conf().Executor.`。改完再跑一次上面的 grep，**必须无输出**。

> `m.cfg.DataDir` / `m.cfg.Listen` / `m.cfg.RepoRoot` / `m.cfg.StallTimeout` **保持不动**：它们不是运行期可变字段，改了只是噪声。

- [ ] **Step 5: 跑测试确认它通过**

Run: `go test ./internal/agentd/ -count=1`
Expected: 全绿（整包，不只本组）。

- [ ] **Step 6: 加关键节点日志**

`SetManager` 里注入后打一行：

```go
		s.log.Info("manager 已挂接，配置读取切到活快照", "default_executor", s.conf().Executor.Default)
```

**不要在 `m.conf()` 的每个调用点打日志**——`resolveExecutor` 在每次派发都会走，逐次打是噪声；关键节点是「注入这件事发生了没有」，那只发生一次。

- [ ] **Step 7: 加注释**

已随 Step 3：`conf` 字段的「不要改回直接读 cfg」与为什么；`SetManager` 的注入说明。另在 `NewManager` 的 doc 注释里补一句：

```go
//   - cfg: 配置**快照**。运行期可变的字段（Executor / Targets）不要直接读它，
//     走 m.conf()——Server.SetManager 会把活取值函数注入进来（B160 §4.2）
```

- [ ] **Step 8: 记一条 backlog 发现（不修）**

`m.cfg.Targets` 有 7 处读点（全在 `mirror.go`），Targets 同样是 swapConf 可写的——**从控制台加一台开发机，跨机镜像要等 agentd 重启才认识它**。这是同一个 bug 的同一个形状。

在 `docs/superpowers/backlog.md` 里加一行（ID 取当前 max+1），说明现象、7 处读点的位置、以及「`m.conf()` 已铺好，修它只剩替换读点」。**本 task 不修**：镜像有自己的生命周期与并发假设，不该在设置页的改动里顺手动。

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "fix(b160): Manager 改读活配置，控制台改缺省执行者不必重启 agentd"
```

---

### Task 2: 契约层（proto 类型 + fixture + TS 类型）

**Files:**
- Create: `internal/proto/executor_default.go`
- Modify: `internal/proto/contract_fixture_test.go`
- Create: `web/src/api/testdata/ExecutorDefaultResp.json`、`ExecutorDefaultReq.json`（由 `-update` 生成，不手写）
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/contract.test.ts`

**Interfaces:**
- Consumes: 无
- Produces:
  - `proto.ExecutorDefaultResp{Default string; Model string; Available []string}`
  - `proto.ExecutorDefaultReq{Default string; Model string}`
  - TS: `ExecutorDefaultResp` / `ExecutorDefaultReq`

- [ ] **Step 1: 写 Go 结构**

创建 `internal/proto/executor_default.go`：

```go
// executor_default.go —— 控制台配置机器级缺省执行者的线格式（B160）。
//
// 职责：GET / PUT /api/executor/default 的请求与响应结构。
//
// 边界：
//   - 只覆盖 config 的 executor 段两个标量字段，不碰 approver、proc_fence 等
//     其它机器级配置（哪些不给写、为什么，见 spec §1.2）
//   - 不含任何密钥字段
package proto

// ExecutorDefaultResp 是 GET /api/executor/default 的响应。
//
// Model 的语义是「**Default 的**默认模型」，不是全局默认——agentd 的
// resolveModel 只在 execName == Default 时才套用它，派别的执行器返回空串。
// 界面文案必须照这个语义写，不要写成「不分执行器」（那是修过的旧行为）。
type ExecutorDefaultResp struct {
	Default   string   `json:"default"`   // 当前缺省执行者名
	Model     string   `json:"model"`     // 缺省执行者的默认模型；空串 = 用执行器自身默认
	Available []string `json:"available"` // 该机已注册的 adapter 名，按名字升序
}

// ExecutorDefaultReq 是 PUT /api/executor/default 的请求体。
//
// 两个字段都是**整体替换**语义：缺席与空串一视同仁。Model 为空串是一个有意义
// 的取值（= 不设默认模型），不是「这一项不改」——本接口没有「不改」这个表达。
type ExecutorDefaultReq struct {
	Default string `json:"default"`
	Model   string `json:"model"`
}
```

- [ ] **Step 2: 加 fixture 样本**

`cases` 末尾追加：

```go
		{"ExecutorDefaultResp", executorDefaultRespSample()},
		{"ExecutorDefaultReq", executorDefaultReqSample()},
```

文件末尾追加：

```go
// executorDefaultRespSample 返回 ExecutorDefaultResp 的代表性样本。
func executorDefaultRespSample() ExecutorDefaultResp {
	return ExecutorDefaultResp{
		Default:   "opencode",
		Model:     "opencode-go/deepseek-v4-flash",
		Available: []string{"claude", "codex", "fake", "grok", "opencode"},
	}
}

// executorDefaultReqSample 返回 ExecutorDefaultReq 的代表性样本：
// model 刻意给空串——「不设默认模型」是常态取值，必须在线格式里出现过一次。
func executorDefaultReqSample() ExecutorDefaultReq {
	return ExecutorDefaultReq{Default: "codex", Model: ""}
}
```

- [ ] **Step 3: 跑测试确认它失败**

Run: `go test ./internal/proto/ -run TestContractFixtures -count=1`
Expected: FAIL —— 两个 fixture 文件不存在。

- [ ] **Step 4: 生成 fixture**

Run: `go test ./internal/proto/ -run TestContractFixtures -update -count=1 && go test ./internal/proto/ -count=1`
Expected: 第二次 PASS。

**逐字检查 `ExecutorDefaultReq.json`**：`"model": ""` 必须**在场**（无 omitempty）——缺了它前端就没法表达「清空默认模型」。

- [ ] **Step 5: 同步 TS 类型**

`web/src/api/types.ts` 追加：

```ts
// ExecutorDefaultResp 是 GET /api/executor/default 的响应。
//
// model 是「**default 的**默认模型」，不是全局默认：agentd 只在派缺省执行者时
// 套用它。改 default 会连带改变 model 的作用对象——界面必须让这个连带效应在
// 保存前就可见（标签随 default 变）。
export interface ExecutorDefaultResp {
  default: string
  model: string
  available: string[]
}

// ExecutorDefaultReq 是 PUT /api/executor/default 的请求体：整体替换。
// model 为空串是有意义的取值（不设默认模型），不是「不改」。
export interface ExecutorDefaultReq {
  default: string
  model: string
}
```

- [ ] **Step 6: 加契约测试**

`web/src/api/contract.test.ts` 末尾追加：

```ts
describe('缺省执行者契约', () => {
  it('ExecutorDefaultResp：available 是升序名单，default 在其中', () => {
    const resp = executorDefaultRespFixture as ExecutorDefaultResp
    expect([...resp.available].sort()).toEqual(resp.available)
    expect(resp.available).toContain(resp.default)
  })

  it('ExecutorDefaultReq：model 空串必须在场，不能被 omitempty 吃掉', () => {
    // 缺了这个键，前端就没法表达「清空默认模型」——只能表达「不改」
    expect('model' in executorDefaultReqFixture).toBe(true)
    expect((executorDefaultReqFixture as ExecutorDefaultReq).model).toBe('')
  })
})
```

- [ ] **Step 7: 跑测试确认它通过**

Run: `cd web && npx vitest run src/api/contract.test.ts && npx tsc -b`
Expected: 全绿。

- [ ] **Step 8: 加关键节点日志**

**本 task 不加日志**——`internal/proto` 是纯线格式定义，没有运行时分支。

- [ ] **Step 9: 加注释**

已随 Step 1/Step 5：两个结构各自写明 Model 的真实语义与「整体替换、空串有意义」。

- [ ] **Step 10: Commit**

```bash
git add internal/proto/ web/src/api/
git commit -m "feat(b160): 缺省执行者配置的线格式与契约 fixture"
```

---
### Task 3: `GET/PUT /api/executor/default`

**Files:**
- Create: `internal/agentd/executor_default.go`
- Create: `internal/agentd/executor_default_test.go`
- Modify: `internal/agentd/server.go`（注册两条路由 + 路由表注释）

**Interfaces:**
- Consumes: Task 1 的 `m.conf()`、Task 2 的 proto 类型、`(*Manager).ExecutorNames()`、`Server.swapConf`
- Produces: `(*Server).handleExecutorDefaultGet`、`(*Server).handleExecutorDefaultPut`

- [ ] **Step 1: 写失败测试**

创建 `internal/agentd/executor_default_test.go`：

```go
// executor_default_test.go —— 缺省执行者配置端点的测试（白盒包：要看 manager 的解析结果）。
package agentd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newExecDefaultEnv 构造带 executor 配置与若干已注册 adapter 的白盒环境。
func newExecDefaultEnv(t *testing.T, def, model string, execs ...string) *testAgentdEnv {
	t.Helper()
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: t.TempDir(),
		Executor: config.ExecutorConfig{Default: def, Model: model},
	}, discardLogger())
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	ads := map[string]executor.Adapter{}
	for _, n := range execs {
		ads[n] = &failStartAdapter{}
	}
	mgr := NewManager(env.st, env.srv.Hub(), ads, env.srv.conf(),
		env.srv.DisciplineMapping, nil, newTestGate(t), discardLogger())
	env.srv.SetManager(mgr)
	env.mgr = mgr
	return env
}

func TestExecutorDefaultGet(t *testing.T) {
	env := newExecDefaultEnv(t, "opencode", "m-oc", "opencode", "codex", "fake")
	var resp proto.ExecutorDefaultResp
	if code := env.getJSON(t, "/api/executor/default", &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if resp.Default != "opencode" || resp.Model != "m-oc" {
		t.Fatalf("resp = %+v", resp)
	}
	if strings.Join(resp.Available, ",") != "codex,fake,opencode" {
		t.Fatalf("available = %v，想要按名字升序", resp.Available)
	}
}

func TestExecutorDefaultGetWithoutManagerIs503(t *testing.T) {
	// 名单来自 manager；未就绪时不能装作「一个 executor 都没有」，
	// 那会让界面画出一个空下拉框，用户选无可选还以为是配置丢了
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: t.TempDir(),
	}, discardLogger())
	var body map[string]string
	if code := env.getJSON(t, "/api/executor/default", &body); code != 503 {
		t.Fatalf("code = %d, want 503", code)
	}
}

func TestExecutorDefaultPutSaves(t *testing.T) {
	env := newExecDefaultEnv(t, "opencode", "m-oc", "opencode", "codex")
	var resp proto.ExecutorDefaultResp
	code := env.putJSON(t, "/api/executor/default",
		proto.ExecutorDefaultReq{Default: "codex", Model: " gpt-5.6-luna "}, &resp)
	if code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	// 前后空白必须 TrimSpace：粘贴模型名时带上空格是常事，
	// 而带空格的模型名会被 provider 当成另一个名字直接 400
	if resp.Default != "codex" || resp.Model != "gpt-5.6-luna" {
		t.Fatalf("resp = %+v，想要 codex / gpt-5.6-luna（已去空白）", resp)
	}
	saved := env.srv.conf().Executor
	if saved.Default != "codex" || saved.Model != "gpt-5.6-luna" {
		t.Fatalf("落盘 = %+v", saved)
	}
	// 承重：不重建 Manager，派发路径立即用新值
	if name, _, err := env.mgr.resolveExecutor(""); err != nil || name != "codex" {
		t.Fatalf("resolveExecutor = %q, err = %v，想要 codex", name, err)
	}
	if got := env.mgr.resolveModel("", "codex"); got != "gpt-5.6-luna" {
		t.Fatalf("resolveModel = %q，想要 gpt-5.6-luna", got)
	}
}

func TestExecutorDefaultPutClearsModel(t *testing.T) {
	// 空串是有意义的取值（不设默认模型），不是「不改」
	env := newExecDefaultEnv(t, "opencode", "m-oc", "opencode")
	var resp proto.ExecutorDefaultResp
	if code := env.putJSON(t, "/api/executor/default",
		proto.ExecutorDefaultReq{Default: "opencode", Model: ""}, &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if resp.Model != "" {
		t.Fatalf("model = %q，想要空串", resp.Model)
	}
	if got := env.mgr.resolveModel("", "opencode"); got != "" {
		t.Fatalf("resolveModel = %q，想要空串（清空后由执行器自身默认接管）", got)
	}
}

func TestExecutorDefaultPutRejects(t *testing.T) {
	env := newExecDefaultEnv(t, "opencode", "", "opencode", "codex")
	var body map[string]string

	// 未注册的名字：错误里必须列出可选名单，否则用户只知道错了不知道该填什么
	if code := env.putJSON(t, "/api/executor/default",
		proto.ExecutorDefaultReq{Default: "opencde"}, &body); code != 400 {
		t.Fatalf("未注册 code = %d, want 400", code)
	}
	if !strings.Contains(body["error"], "codex") || !strings.Contains(body["error"], "opencode") {
		t.Fatalf("error = %q，想要列出可选名单", body["error"])
	}
	// 空串：缺省执行者不能没有——为空时每一次不带 --executor 的派发都会失败
	if code := env.putJSON(t, "/api/executor/default",
		proto.ExecutorDefaultReq{Default: "  "}, &body); code != 400 {
		t.Fatalf("空 default code = %d, want 400", code)
	}
	// 拒绝后配置必须没动
	if got := env.srv.conf().Executor.Default; got != "opencode" {
		t.Fatalf("被拒后配置被改了：%q", got)
	}
}

func TestExecutorDefaultPutDoesNotValidateModel(t *testing.T) {
	// agentd 不认识任何执行器的模型名单，没有可判据——校验它只能是瞎猜。
	// 这条用例存在的意义是**钉住「不校验」这个决定**，防止有人日后加一个白名单
	env := newExecDefaultEnv(t, "opencode", "", "opencode")
	var resp proto.ExecutorDefaultResp
	if code := env.putJSON(t, "/api/executor/default",
		proto.ExecutorDefaultReq{Default: "opencode", Model: "完全不存在的模型名"}, &resp); code != 200 {
		t.Fatalf("code = %d, want 200（model 不校验）", code)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestExecutorDefault -count=1`
Expected: FAIL —— 路由不存在，404。

- [ ] **Step 3: 写实现**

创建 `internal/agentd/executor_default.go`：

```go
// executor_default.go —— 控制台配置机器级缺省执行者的 HTTP 面（B160）。
//
// 职责：
//   - GET  /api/executor/default   该机的缺省执行者、它的默认模型、可选名单
//   - PUT  /api/executor/default   保存这两项（整体替换）
//
// 边界：
//   - 只碰 config 的 executor 段。approver / proc_fence / listen 等机器级配置
//     一律不给写，理由逐条见 spec §1.2
//   - 跨机由 forwardIfRequested 处理（?machine=），本文件只管本机
//   - **Model 不校验**：agentd 不认识任何执行器的模型名单，没有可判据
//   - 落盘走 swapConf。**不要**为 Executor 补深拷：ExecutorConfig 是两个 string
//     的值类型，结构体浅拷即完整拷贝
package agentd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// handleExecutorDefaultGet 处理 GET /api/executor/default[?machine=]。
//
// 响应：200 proto.ExecutorDefaultResp / 503 manager 未就绪。
//
// 为什么 manager 未就绪要 503 而不是回一个空名单：空名单会让界面画出一个
// 选无可选的下拉框，用户会以为配置丢了。诚实地说「现在答不上来」更好。
func (s *Server) handleExecutorDefaultGet(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	s.log.Info("缺省执行者查询请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Warn("缺省执行者查询：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp := s.executorDefaultResp()
	s.log.Info("缺省执行者查询完成",
		"default", resp.Default, "has_model", resp.Model != "", "available", len(resp.Available))
	writeJSON(w, http.StatusOK, resp)
}

// executorDefaultResp 组装当前状态（GET 与 PUT 的成功响应共用，保证两处一致）。
func (s *Server) executorDefaultResp() proto.ExecutorDefaultResp {
	c := s.conf()
	return proto.ExecutorDefaultResp{
		Default:   c.Executor.Default,
		Model:     c.Executor.Model,
		Available: s.mgr.ExecutorNames(), // 已按名字升序（registeredNames）
	}
}

// handleExecutorDefaultPut 处理 PUT /api/executor/default[?machine=]。
//
// 请求体 proto.ExecutorDefaultReq：**整体替换** executor 段的两个字段。
//
// 响应：200 proto.ExecutorDefaultResp（保存后的最新状态，界面直接拿它刷新）
//
//	400 default 为空或未注册
//	503 manager 未就绪
//
// 为什么 default 必须校验：它是 resolveExecutor 的兜底值。写进一个该机没有的
// 名字，此后**每一次**不带 --executor 的派发都会失败——一个下拉框搞挂一台机。
//
// 为什么 model 不校验：agentd 不认识任何执行器的模型名单（模型名按执行器、
// 也按机器不同），没有可判据。它的失败面也小得多：只影响缺省执行者、只影响
// 不带 --model 的派发，且失败是当场的（第一个事件就是 400 或秒退）。
func (s *Server) handleExecutorDefaultPut(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	if s.mgr == nil {
		s.log.Warn("缺省执行者保存：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var req proto.ExecutorDefaultReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("缺省执行者保存：请求体无法解析", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	// 粘贴模型名时带空格是常事，而带空格的名字会被 provider 当成另一个名字直接 400。
	def := strings.TrimSpace(req.Default)
	model := strings.TrimSpace(req.Model)
	s.log.Info("缺省执行者保存请求", "default", def, "has_model", model != "")

	names := s.mgr.ExecutorNames()
	if def == "" {
		s.log.Warn("缺省执行者保存被拒：为空", "cause", "缺省执行者不能为空")
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("缺省执行者不能为空（可选: %s）", strings.Join(names, ", "))})
		return
	}
	if !containsString(names, def) {
		s.log.Warn("缺省执行者保存被拒：未注册", "default", def, "registered", names,
			"cause", "该机没有这个执行者")
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("未知 executor %q（可选: %s）", def, strings.Join(names, ", "))})
		return
	}

	if err := s.swapConf(func(c *config.Config) error {
		// ExecutorConfig 是值类型，整体赋值即完整替换——不需要也不该补深拷。
		c.Executor = config.ExecutorConfig{Default: def, Model: model}
		return nil
	}); err != nil {
		s.log.Error("缺省执行者落盘失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("缺省执行者已保存", "default", def, "has_model", model != "")
	writeJSON(w, http.StatusOK, s.executorDefaultResp())
}

// containsString 判断名单里有没有某个名字（名单只有个位数长度，线性扫足够）。
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
```

> **模型名不进日志的正文**，只记 `has_model` 布尔。模型名本身不是秘密，但它经常
> 是从别处粘过来的一长串，逐次刷进日志没有价值；要查「配的是哪个」看响应体或
> config.yaml。

- [ ] **Step 4: 注册路由**

```go
	api.HandleFunc("GET /api/executor/default", s.handleExecutorDefaultGet)
	api.HandleFunc("PUT /api/executor/default", s.handleExecutorDefaultPut)
```

路由表注释补两行。

- [ ] **Step 5: 跑测试确认它通过**

Run: `go test ./internal/agentd/ -count=1`
Expected: 全绿（整包）。

- [ ] **Step 6: 加关键节点日志**

已随 Step 3：两个 handler 的进入日志、503/400 各分支的 Warn（**都带 cause**）、落盘失败 Error、成功退出日志。

自检：没有任何一条日志打出 `model` 的值。

- [ ] **Step 7: 加注释**

已随 Step 3：文件头两条路由 + 四条边界（含「不校验 model」与「不要补深拷」）；两个 handler 的「为什么 503 不回空名单」「为什么 default 要校验、model 不要」。

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/
git commit -m "feat(b160): GET/PUT /api/executor/default，default 校验、model 不校验"
```

---

### Task 4: 前端 API 客户端

**Files:**
- Modify: `web/src/api/client.ts`

**Interfaces:**
- Consumes: Task 2 的 TS 类型
- Produces:
  - `fetchExecutorDefault(machine: string): Promise<ExecutorDefaultResp>`
  - `saveExecutorDefault(machine: string, req: ExecutorDefaultReq): Promise<ExecutorDefaultResp>`

- [ ] **Step 1: 写实现**

`web/src/api/client.ts` 追加：

```ts
// fetchExecutorDefault 取某台机器的缺省执行者配置（GET /api/executor/default）。
export function fetchExecutorDefault(machine: string): Promise<ExecutorDefaultResp> {
  return request<ExecutorDefaultResp>(`/api/executor/default${machineQuery(machine)}`)
}

// saveExecutorDefault 整体替换某台机器的缺省执行者与其默认模型
//（PUT /api/executor/default），返回保存后的最新状态。
//
// req.model 为空串表示「清空默认模型」，是有意义的取值，不是「不改」。
// req.default 不在该机名单内时后端回 400，message 里带可选名单——原样展示。
export function saveExecutorDefault(
  machine: string, req: ExecutorDefaultReq,
): Promise<ExecutorDefaultResp> {
  return putJSON<ExecutorDefaultResp>(`/api/executor/default${machineQuery(machine)}`, req)
}
```

（顶部类型 import 补 `ExecutorDefaultResp` / `ExecutorDefaultReq`。）

- [ ] **Step 2: 跑校验**

Run: `cd web && npx tsc -b && npx eslint src/api/client.ts`
Expected: 0 错误。

- [ ] **Step 3: 加关键节点日志 / 注释**

前端不打日志；两个函数各自的 doc 注释已随 Step 1（写明「空串是有意义的取值」与「400 原文要展示」）。

- [ ] **Step 4: Commit**

```bash
git add web/src/api/client.ts
git commit -m "feat(b160): 缺省执行者配置的前端客户端函数"
```

---

### Task 5: `useTreePrefs` 共享状态层

**Files:**
- Create: `web/src/app/tree/useTreePrefs.ts`
- Create: `web/src/app/tree/useTreePrefs.test.ts`
- Modify: `web/src/app/tree/ProjectTree.tsx`（删私有 `useState` + `updatePrefs`，改用 hook）

**Interfaces:**
- Consumes: `treePrefs.ts` 的 `loadPrefs` / `savePrefs` / `TreePrefs`
- Produces: `useTreePrefs(): [TreePrefs, (next: TreePrefs) => void]`

> **不做这一步，「常规」分区就只能是只读的**——设置页改一份、左栏那份不知道。
> 一个看得见改不动的偏好页毫无价值。

- [ ] **Step 1: 写失败测试**

创建 `web/src/app/tree/useTreePrefs.test.ts`：

```ts
import { describe, expect, it, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTreePrefs } from './useTreePrefs'
import { PREFS_KEY, DEFAULT_PREFS } from './treePrefs'

beforeEach(() => {
  localStorage.clear()
})

describe('useTreePrefs', () => {
  it('初值取自 localStorage，改动落盘', () => {
    const { result } = renderHook(() => useTreePrefs())
    expect(result.current[0]).toEqual(DEFAULT_PREFS)
    act(() => result.current[1]({ ...DEFAULT_PREFS, projectSort: 'name' }))
    expect(result.current[0].projectSort).toBe('name')
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!).projectSort).toBe('name')
  })

  it('两个挂载点共享同一份状态——这是「常规」分区能改左栏的前提', () => {
    const a = renderHook(() => useTreePrefs())
    const b = renderHook(() => useTreePrefs())
    act(() => a.result.current[1]({ ...DEFAULT_PREFS, hideIdleWorktrees: true }))
    expect(b.result.current[0].hideIdleWorktrees).toBe(true)
  })

  it('卸载后不再收到通知（不泄漏订阅）', () => {
    const a = renderHook(() => useTreePrefs())
    const b = renderHook(() => useTreePrefs())
    b.unmount()
    // 卸载的那个不该再被 setState，React 会在控制台报警告；这里断言不抛即可
    expect(() => act(() => a.result.current[1]({ ...DEFAULT_PREFS, projectSort: 'recent' })))
      .not.toThrow()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/tree/useTreePrefs.test.ts`
Expected: FAIL —— 模块不存在。

- [ ] **Step 3: 写实现**

创建 `web/src/app/tree/useTreePrefs.ts`：

```ts
// useTreePrefs —— 左栏显示偏好的**共享**状态层（B160 §4.3）。
//
// 职责：让「左栏的偏好菜单」与「设置页的常规分区」读写同一份状态。
//
// 为什么是模块级单例 + 订阅，而不是 Context：ProjectTree 与 SettingsPage 不在
// 同一棵子树下（设置页整页替换中央内容区），套 Provider 要动到 Shell，收益
// 不抵改动面。模块级订阅是这个形状最小的解。
//
// 为什么不是「每个挂载点各自 useState(loadPrefs())」：那正是本文件要修的 bug
// ——设置页改一份，左栏那份不会知道，直到刷新页面。
//
// 边界：
//   - 不认识 React 之外的东西；规则本身仍在 treePrefs.ts（本文件只管状态与订阅）
//   - 不跨标签页同步（不监听 storage 事件）：两个标签页各改各的属于罕见场景，
//     为它引入「另一个标签页把我正在改的覆盖掉」的新问题不划算
import { useCallback, useEffect, useState } from 'react'
import { loadPrefs, savePrefs, type TreePrefs } from './treePrefs'

// current 是进程内唯一的一份偏好。惰性初始化：模块加载时读一次 localStorage。
let current: TreePrefs = loadPrefs()

// subscribers 是全部活着的挂载点。用 Set 而不是数组：退订是按引用删，O(1)。
const subscribers = new Set<(p: TreePrefs) => void>()

// setPrefs 落盘并通知全部订阅者。落盘与通知必须成对，分开写迟早漏一处。
function setPrefs(next: TreePrefs) {
  current = next
  savePrefs(next)
  for (const notify of subscribers) notify(next)
}

// useTreePrefs 返回当前偏好与更新函数。任意多个挂载点共享同一份状态。
//
// 返回：
//   - [0] 当前偏好（同一时刻所有挂载点拿到的是同一个对象）
//   - [1] 更新函数：落盘 + 通知全部挂载点
export function useTreePrefs(): [TreePrefs, (next: TreePrefs) => void] {
  const [prefs, setLocal] = useState<TreePrefs>(current)
  useEffect(() => {
    subscribers.add(setLocal)
    // 订阅建立与初始 useState 之间可能已有一次更新，补一次对齐
    setLocal(current)
    return () => {
      subscribers.delete(setLocal)
    }
  }, [])
  return [prefs, useCallback(setPrefs, [])]
}
```

- [ ] **Step 4: `ProjectTree` 改用它**

删掉：

```tsx
  const [prefs, setPrefs] = useState<TreePrefs>(() => loadPrefs())
  const updatePrefs = (next: TreePrefs) => {
    setPrefs(next)
    savePrefs(next)
  }
```

换成：

```tsx
  // 显示偏好走共享层：设置页的「常规」分区改的是同一份，两处即时同步（B160 §4.3）
  const [prefs, updatePrefs] = useTreePrefs()
```

import 相应调整（`loadPrefs` / `savePrefs` / `TreePrefs` 若不再用就删掉，避免 eslint 报未使用）。

- [ ] **Step 5: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/tree/ && npx tsc -b && npx eslint src/app/tree/`
Expected: 全绿，`ProjectTree.test.tsx` 不得回归。

> 若 `ProjectTree.test.tsx` 里有依赖「每个测试从干净默认值开始」的用例，模块级单例
> 会让上一个测试的改动漏到下一个。**在 `useTreePrefs.ts` 里导出一个仅供测试用的
> `__resetTreePrefsForTest()`**（`current = loadPrefs(); subscribers.clear()`），在
> 受影响测试的 `beforeEach` 里调用。不要为此改回 per-mount state。

- [ ] **Step 6: 加关键节点日志**

前端不打日志。可观测性判据：改一项后 `localStorage` 里那一项确实变了（Step 1 的用例已钉住）。

- [ ] **Step 7: 加注释**

已随 Step 3：文件头写明为什么是模块级单例、为什么不用 Context、为什么不跨标签页同步；`setPrefs` 的「落盘与通知必须成对」。

- [ ] **Step 8: Commit**

```bash
git add web/src/app/tree/
git commit -m "refactor(b160): 偏好状态提到共享层，为设置页与左栏共用做准备"
```

---

### Task 6: 「常规」分区

**Files:**
- Create: `web/src/app/settings/GeneralPage.tsx`
- Create: `web/src/app/settings/GeneralPage.test.tsx`
- Modify: `web/src/app/settings/SettingsPage.tsx`（占位换成 `<GeneralPage tree={treeState.data} />`）

**Interfaces:**
- Consumes: Task 5 的 `useTreePrefs`、`treePrefs.ts` 的 `ProjectSort`
- Produces: `<GeneralPage tree={ProjectTreeResp | null} />`

- [ ] **Step 1: 写失败测试**

创建 `web/src/app/settings/GeneralPage.test.tsx`：

```tsx
import { describe, expect, it, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { renderHook } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { GeneralPage } from './GeneralPage'
import { useTreePrefs } from '../tree/useTreePrefs'
import { PREFS_KEY } from '../tree/treePrefs'

const tree = {
  projects: [
    { project_id: 'p1', name: 'handoff' },
    { project_id: 'p2', name: 'nova' },
  ],
  unowned: [],
} as never

beforeEach(() => {
  localStorage.clear()
})

describe('GeneralPage', () => {
  it('点明范围：这些只保存在当前浏览器', () => {
    render(<GeneralPage tree={tree} />)
    expect(screen.getByText(/只保存在当前浏览器/)).toBeInTheDocument()
  })

  it('改排序后落盘，且共享状态里也变了（左栏会跟着变）', async () => {
    const shared = renderHook(() => useTreePrefs())
    render(<GeneralPage tree={tree} />)
    await userEvent.click(screen.getByRole('radio', { name: '名称' }))
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!).projectSort).toBe('name')
    // 这条才是 B160 的重点：不是「设置页自己变了」，而是「同一份状态变了」
    expect(shared.result.current[0].projectSort).toBe('name')
  })

  it('取消勾选一个项目 = 加进隐藏名单（名单存的是不显示谁）', async () => {
    render(<GeneralPage tree={tree} />)
    await userEvent.click(screen.getByRole('checkbox', { name: 'nova' }))
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!).hiddenProjects).toEqual(['p2'])
  })

  it('折叠空闲工作树是个开关', async () => {
    render(<GeneralPage tree={tree} />)
    await userEvent.click(screen.getByRole('checkbox', { name: /隐藏无活跃任务的工作树/ }))
    expect(JSON.parse(localStorage.getItem(PREFS_KEY)!).hideIdleWorktrees).toBe(true)
  })

  it('项目树还没到时不画项目那一组，但另外两项照常可用', () => {
    render(<GeneralPage tree={null} />)
    expect(screen.getByRole('checkbox', { name: /隐藏无活跃任务的工作树/ })).toBeInTheDocument()
    expect(screen.queryByRole('checkbox', { name: 'nova' })).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/settings/GeneralPage.test.tsx`
Expected: FAIL —— 模块不存在。

- [ ] **Step 3: 写实现**

创建 `web/src/app/settings/GeneralPage.tsx`：

```tsx
// GeneralPage —— 设置页「常规」分区（B160 spec §2.1）。
//
// 职责：把左栏那个紧凑的显示偏好菜单在设置页里**平铺展开**。
//
// **这里只放客户端偏好**（落 localStorage、只影响当前浏览器）。归属判据见
// spec §1.1：某台机器的运行参数（config.yaml）归开发机详情；协调者 CLI 本机的
// 行为（sync.auto / terminal.auto）两处都不放——控制台连的 agentd 与你敲 CLI
// 那台机可能不是同一台，一个改了不生效的开关比没有这个开关更糟。
//
// **不为了填满一屏去发明设置**：今天就这三项，主题与快捷键都还不存在。
//
// 边界：
//   - 不复用 TreePrefsMenu 的紧凑形态：设置页有空间，菜单没有。共用的是
//     treePrefs.ts 的类型与 useTreePrefs 的状态，不是那个下拉的渲染
//   - 不自己取项目树：由 SettingsPage 传进来（它已经有一份）
import type { ProjectTreeResp } from '../../api/types'
import { useTreePrefs } from '../tree/useTreePrefs'
import type { ProjectSort } from '../tree/treePrefs'

// SORT_LABELS 与左栏菜单同源同序：两处标签不一致会让人以为是两套设置。
const SORT_LABELS: { value: ProjectSort; label: string }[] = [
  { value: 'active', label: '活跃优先' },
  { value: 'name', label: '名称' },
  { value: 'recent', label: '最近活动' },
]

// GeneralPage 渲染当前浏览器的显示偏好。tree 为 null 表示项目树还没到。
export function GeneralPage({ tree }: { tree: ProjectTreeResp | null }) {
  const [prefs, update] = useTreePrefs()
  const hidden = new Set(prefs.hiddenProjects)
  const projects = tree?.projects ?? []

  return (
    <div className="flex flex-col gap-5 p-4">
      <div className="border-b pb-3">
        <h2 className="text-sm font-semibold">常规</h2>
        <p className="mt-1 text-xs text-muted-foreground">
          这些设置只保存在当前浏览器里，不同步到其他设备，也不影响任何一台开发机。
        </p>
      </div>

      <section>
        <h3 className="text-xs font-medium text-muted-foreground">显示</h3>
        <label className="mt-2 flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={prefs.hideIdleWorktrees}
            onChange={() => update({ ...prefs, hideIdleWorktrees: !prefs.hideIdleWorktrees })}
          />
          隐藏无活跃任务的工作树
        </label>
      </section>

      <section>
        <h3 className="text-xs font-medium text-muted-foreground">项目排序</h3>
        <div className="mt-2 flex flex-col gap-1.5">
          {SORT_LABELS.map((s) => (
            <label key={s.value} className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name="project-sort"
                aria-label={s.label}
                checked={prefs.projectSort === s.value}
                onChange={() => update({ ...prefs, projectSort: s.value })}
              />
              {s.label}
            </label>
          ))}
        </div>
      </section>

      <section>
        <h3 className="text-xs font-medium text-muted-foreground">左栏显示哪些项目</h3>
        {projects.length === 0 ? (
          // 空分区也要有话说：一块空白会让人以为页面坏了
          <p className="mt-2 text-xs text-muted-foreground">项目树还没加载出来。</p>
        ) : (
          <>
            <div className="mt-2 flex flex-col gap-1.5">
              {projects.map((p) => (
                <label key={p.project_id} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    aria-label={p.name}
                    checked={!hidden.has(p.project_id)}
                    onChange={() => {
                      // 名单存的是「不显示谁」：勾 = 从名单拿掉，取消勾 = 加进名单。
                      // 取反方向与直觉相反，改的时候看清楚（与左栏菜单同一条注释）
                      const next = new Set(prefs.hiddenProjects)
                      if (next.has(p.project_id)) next.delete(p.project_id)
                      else next.add(p.project_id)
                      update({ ...prefs, hiddenProjects: [...next] })
                    }}
                  />
                  {p.name}
                </label>
              ))}
            </div>
            <div className="mt-2 flex gap-3 text-xs">
              <button type="button" className="text-primary hover:underline"
                onClick={() => update({ ...prefs, hiddenProjects: [] })}>全选</button>
              <button type="button" className="text-primary hover:underline"
                onClick={() => update({ ...prefs, hiddenProjects: projects.map((p) => p.project_id) })}>全不选</button>
            </div>
          </>
        )}
      </section>
    </div>
  )
}
```

- [ ] **Step 4: 挂进设置页**

`SettingsPage.tsx`：import `GeneralPage`，把 `section === 'general'` 的占位段落换成
`<GeneralPage tree={treeState.data} />`。

- [ ] **Step 5: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/settings/ && npx tsc -b`
Expected: 全绿（`SettingsPage.test.tsx` 若断言了「本期没有可配置项」那句占位文案，
改成断言「只保存在当前浏览器」那句）。

- [ ] **Step 6: 加关键节点日志**

前端不打日志。用户可见状态自检两条：范围说明恒在；项目树没到时有一句话而不是空白。

- [ ] **Step 7: 加注释**

已随 Step 3：文件头的归属判据引用与「不为了填满一屏发明设置」；隐藏名单取反方向的提醒。

- [ ] **Step 8: Commit**

```bash
git add web/src/
git commit -m "feat(b160): 设置页常规分区，与左栏共用同一份显示偏好"
```

---

### Task 7: 开发机详情的缺省执行者块

**Files:**
- Create: `web/src/app/machines/MachineExecutor.tsx`
- Create: `web/src/app/machines/MachineExecutor.test.tsx`
- Modify: `web/src/app/machines/MachineDetail.tsx`（挂上新块；`NOT_WIRED` 删掉「可用执行者」那条）

**Interfaces:**
- Consumes: Task 4 的两个客户端函数
- Produces: `<MachineExecutor machine={machine} />`

- [ ] **Step 1: 写失败测试**

创建 `web/src/app/machines/MachineExecutor.test.tsx`：

```tsx
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MachineExecutor } from './MachineExecutor'
import * as client from '../../api/client'
import type { Machine } from '../../api/types'

const machine = { name: 'mac-02', reachable: true, executors: ['codex', 'opencode'], error: '' } as Machine
const resp = { default: 'opencode', model: 'm-oc', available: ['codex', 'opencode'] }

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(client, 'fetchExecutorDefault').mockResolvedValue(resp)
})

describe('MachineExecutor', () => {
  it('模型标签随缺省执行者变——让连带效应在保存前就可见', async () => {
    render(<MachineExecutor machine={machine} />)
    expect(await screen.findByLabelText('opencode 的默认模型')).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '缺省执行者' }), 'codex')
    // 还没保存，但标签已经改了：用户看得见「这个模型名要套到 codex 头上了」
    expect(screen.getByLabelText('codex 的默认模型')).toBeInTheDocument()
  })

  it('下拉只列 available，不能自由输入', async () => {
    render(<MachineExecutor machine={machine} />)
    const select = await screen.findByRole('combobox', { name: '缺省执行者' })
    expect(Array.from(select.querySelectorAll('option')).map((o) => o.textContent))
      .toEqual(['codex', 'opencode'])
  })

  it('保存 payload 是整体替换的两项', async () => {
    const save = vi.spyOn(client, 'saveExecutorDefault').mockResolvedValue(
      { default: 'codex', model: 'gpt-5.6-luna', available: ['codex', 'opencode'] })
    render(<MachineExecutor machine={machine} />)
    await userEvent.selectOptions(await screen.findByRole('combobox', { name: '缺省执行者' }), 'codex')
    const box = screen.getByLabelText('codex 的默认模型')
    await userEvent.clear(box)
    await userEvent.type(box, 'gpt-5.6-luna')
    expect(screen.getByText('有未保存的改动')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(save).toHaveBeenCalledWith('mac-02', { default: 'codex', model: 'gpt-5.6-luna' })
  })

  it('后端 400 原样展示（可选名单是用户改对的线索）', async () => {
    vi.spyOn(client, 'saveExecutorDefault').mockRejectedValue(
      new client.ApiError(400, '未知 executor "opencde"（可选: codex, opencode）'))
    render(<MachineExecutor machine={machine} />)
    await userEvent.selectOptions(await screen.findByRole('combobox', { name: '缺省执行者' }), 'codex')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/可选: codex, opencode/)).toBeInTheDocument()
  })

  it('机器断开时不发请求，展示 error 原文', () => {
    const f = vi.spyOn(client, 'fetchExecutorDefault')
    render(<MachineExecutor machine={{ ...machine, reachable: false, error: 'dial tcp: refused' } as Machine} />)
    expect(f).not.toHaveBeenCalled()
    expect(screen.getByText(/dial tcp: refused/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/machines/MachineExecutor.test.tsx`
Expected: FAIL —— 模块不存在。

- [ ] **Step 3: 写实现**

创建 `web/src/app/machines/MachineExecutor.tsx`，结构与 `MachineDiscipline.tsx` 同构
（进入拉一次、不轮询、脏态标记、整块一个保存、断开降级），要点：

```tsx
// MachineExecutor —— 开发机详情里的「缺省执行者」块（B160 spec §2.2）。
//
// 职责：改这台机器不带 --executor 派发时用哪个执行者，以及**它的**默认模型。
//
// **model 是「default 的模型」，不是全局默认**：agentd 的 resolveModel 只在
// execName == default 时才套用它，派别的执行器返回空串。所以：
//   - 两项必须同块、共用一个保存按钮，中间不插别的东西
//   - model 输入框的标签随 default 变（「opencode 的默认模型」→「codex 的…」），
//     让「改 default 会连带改变 model 的作用对象」这个效应在保存前就可见
//
// **model 服务端不校验**：agentd 不认识任何执行器的模型名单，没有可判据
//（模型名按执行器、也按机器不同）。这是「用文案承担校验」的少数正当场合。
//
// 边界：
//   - 缺省执行者只能从 available 里选，不给自由输入：填一个该机没有的名字，
//     此后每一次不带 --executor 的派发都会失败（服务端还有第二道校验）
//   - 不轮询：进入详情拉一次，保存后用响应刷新
//   - 机器断开时不发请求、不渲染控件
```

模型输入的提示行：

```tsx
          <p className="mt-0.5 text-[11px] text-muted-foreground">
            只对上面选的缺省执行者生效。派其他执行器时用 <code>--model</code> 逐次指定。
          </p>
```

placeholder：`留空——用 ${draftDefault} 自己的默认模型`。

底部提示：`保存后下一个任务即生效，不必重启 agentd。`

- [ ] **Step 4: 挂进详情并退役一条 NOT_WIRED**

`MachineDetail.tsx`：

1. import 后在 `<MachineDiscipline machine={machine} />`（以及 B158 若已并入的
   `<MachineEnv />`）之后加 `<MachineExecutor machine={machine} />`；
2. 从 `NOT_WIRED` 数组里**删掉** `{ key: 'executors', … }` 那一条——它承诺的
   「需要 agentd 提供机器级配置的写接口」正是本块。`restart` 与 `terminal`
   两条**保持原样**；
3. 上方「可用执行者」那个只读列表**保留**（带「默认」标记），它与新块是「看」
   与「改」的关系，不重复。

- [ ] **Step 5: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/machines/ && npx tsc -b && npx eslint src/app/machines/`
Expected: 全绿。`MachinesPage.test.tsx` 若断言了三条 NOT_WIRED 的数量或「可用执行者」
那条的文案，改成两条。

- [ ] **Step 6: 加关键节点日志**

前端不打日志。用户可见状态自检四条：加载中、加载失败（原文）、脏态「有未保存的改动」、
保存失败时 `role="alert"` 展示后端 400 原文（含可选名单）。

- [ ] **Step 7: 加注释**

已随 Step 3：文件头写明 model 的真实语义、为什么两项同块、为什么标签要跟着变、
为什么不给自由输入、为什么服务端不校验 model。

- [ ] **Step 8: Commit**

```bash
git add web/src/
git commit -m "feat(b160): 开发机详情可改缺省执行者与其默认模型，退役对应 NOT_WIRED"
```

---

### Task 8: 全量校验

**Files:** 无新增；只跑校验与修复

- [ ] **Step 1: Go 全量**

Run: `gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1`
Expected: `gofmt -l .` **无输出**；其余 exit 0、无失败包。

> `gofmt -l .` 无输出是硬判据：测试全绿 ≠ 格式干净，两件事互不覆盖。

- [ ] **Step 2: 前端全量**

Run: `cd web && npx vitest run && npx tsc -b && npx eslint . && npm run build`
Expected: 测试全绿、0 类型错误、0 eslint error（既有 warning 不计）、构建成功。

- [ ] **Step 3: 红线自查**

Run:
```bash
grep -rn "fmt.Printf\|os.RemoveAll" internal/ | grep -v _test.go
grep -rn "console.log" web/src/
grep -n "m\.cfg\.Executor\." internal/agentd/*.go | grep -v _test.go
```
Expected: 前两条与本次改动相关的命中为 0；**第三条必须无输出**（7 处读点全部改完）。

- [ ] **Step 4: 签名未被改动的自查**

Run: `git diff <分支起点>..HEAD -- internal/agentd/manager.go | grep -n "^[-+]func NewManager"`
Expected: **无输出**。`NewManager` 的签名一行都不该动（并行的 B158 在改它）。

- [ ] **Step 5: Commit（如有修复）**

```bash
git add -A
git commit -m "chore(b160): 全量校验与红线自查的修复"
```

---

## 附：留给审核者的验收（**不派发**）

以下两条**不写进执行者的工作范围**——它们要驱动 handoff 自身（起 agentd、派任务、
调 CLI）或开浏览器人工比对，与执行纪律块里「不要派发、不要调用 handoff CLI、
不要起任何新的 executor 进程」直接冲突。执行者做完 Task 8 即完工。

- [ ] **A. 承重真机判据（spec §8.3）**

在一台真实执行机上：控制台改掉该机的缺省执行者并保存，**不重启 agentd**，随即向
该机派一个**不带 `--executor`** 的任务，确认起来的是新选的执行者。同时刷新开发机
列表，确认「默认」标记（来自 `GET /api/status`）也跟着变了——这条专门钉 `status.go`
那两处读点有没有漏改。

- [ ] **B. 两处偏好即时同步（spec §8.1）**

在浏览器里：设置页「常规」改排序 → **不刷新页面** → 返回工作台，左栏当场是新排序。
反向再来一次（左栏菜单改 → 进设置页看到新值）。

- [ ] **C. 与 B158 的合并**

B158（env 配置面）与本分支都改了 `MachineDetail.tsx`（各自加一块）与
`internal/agentd/server.go` 的路由注册。**预期是可自动合并的相邻插入**，但合并后
必须重跑一次 `go test ./... && cd web && npx vitest run`。若 B158 已并入主线且改了
`NewManager` 的签名，本分支的 Task 1/Task 3 测试里的 `NewManager(...)` 实参表要补齐。

---

## 附：本计划与 spec 的对应

| spec 章节 | 落点 |
|---|---|
| §1.1 归属判据 | Task 6（文件头写进代码）+ Task 7（B 类落在开发机详情） |
| §1.2 不做清单 | Task 3（文件头边界）——代码里不出现这些字段即为落实 |
| §2.1 常规分区 | Task 6 |
| §2.2 开发机详情块 + 退役 NOT_WIRED | Task 7 |
| §2.3 Model 的真实语义与文案 | Task 2（类型注释）+ Task 3（handler 注释）+ Task 7（界面文案） |
| §3.1 为什么不复用 Machine.default_executor | Task 3（新端点即答案） |
| §3.2 数据结构 | Task 2 |
| §3.3 校验与错误语义 | Task 3 |
| §4.1 不补深拷 | Global Constraints + Task 3 的注释 |
| §4.2 Manager 读活配置（承重） | **Task 1** |
| §4.2 Targets 同款陈旧读 | Task 1 Step 8（记 backlog，不修） |
| §4.3 偏好提到共享层 | Task 5 |
| §5 前端落点 | Task 4 / 5 / 6 / 7 |
| §6 契约与测试 | Task 2 + 各 task 的测试步骤 |
| §7 风险（两道门） | Task 3（服务端校验）+ Task 7（下拉不给自由输入） |
| §8.1 / §8.3 | 留给审核者（见上一节） |
| §8.2 / §8.4 / §8.5 | Task 7 / Task 3 的用例 |
