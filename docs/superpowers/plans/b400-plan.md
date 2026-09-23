# B400 plan：新卡首个节点的基线护栏——默认线树里没有本卡附件就拒发

> 卡 B400 · 入口节点 charter:plan · spec `docs/superpowers/specs/b400.md`（已批准，2026-09-23）
> 基线分支 `cards/B400-charter-2`，起手 HEAD `54585a62`（本节点未做任何合并；spec 两提交已在此树）。
> 凡引用行号者动手前重核，漂了以符号为准。
> 台账 `docs/superpowers/ledgers/2026-09-23-b400-plan-ledger.md`（含亲跑命令、原始输出、git 探针记录）。
> **本节点只出计划，不写实现**。「未验证」= 本节点没亲自跑到结果，实现卡必须自己复核。
> 读者假设：对 handoff 仓零上下文的执行者。
>
> **执行机未装 codegraph**：本节点按协调者指示跳过图查询（该工具在协调者本机，执行机没有），
> 改为按 spec §6 接缝清单直读源码；不记图覆盖债为缺陷，只记「未用图」这一偏差。

---

## 0. 待拍板清单（一项；本计划已按推荐项出稿，协调者不认可可在 T3 前否决）

| # | 岔口 | 选项 | 影响 |
|---|------|------|------|
| **P1** | **护栏探针在本机查不到项目仓库时怎么办** | (甲) **跳过并告警（fail-open）**：项目在本机没有位置（如只登记在远端开发机）⇒ 探针返回 `ErrBaseProbeUnavailable`，`ViaTemplate` 记 Warn 后放行；(乙) **拒发（fail-closed）**：查不到本机仓库一律拒发 | **推荐 (甲)**。spec §3 的 fail-closed 针对的是「查得到基线树、树里缺附件」这个错配事实；「本机没有这份仓库」是另一类事实——本机镜像不代表目标机（远端目标机的 `origin/HEAD` 可能不同），按它拒发会把一切远端单机项目卡死，代价远大于它想修的静默落 main。**代价（须知晓）**：(甲) 下「项目仅登记在远端、且默认线不是工作线」的仓仍不被拦——该残余写入 §7 与 roadmap。**为什么不是阻塞项**：T1/T2 与 P1 无关；只有 T3 的探针「查不到位置」分支取值不同，按 (甲) 出稿即可，否决时只改那一个分支。 |

---

## 1. 问题与现状（证据驱动，全部亲读源码；原始读数见台账）

### R1（根因）：首个节点派发时，「基线的名字」有了，但「这条基线树里有没有本卡附件」从没被问过

派发决议段（`internal/ledgerstep/dispatch.go`）现状只算基线的**名字**：

- `:201-202` `workInfo, workErr := d.St.WorkBranch(c.ID)`；`hasWorkBranch := workErr == nil`
  —— `WorkBranch` 只在「已有非审阅 dispatched 快照」时命中（`internal/ledger/events.go:575-613`，
  空则返回 `ErrNotFound`）。所以 `!hasWorkBranch` **就是** spec §3 说的「卡上尚无任何非审阅 dispatched 快照」。
- `:283-296` `base, _ := d.St.EffectiveBaseBranch(c.ID)`；`resolveDefaultBase := base == ""`
  —— 无显式/继承基线时 `base=""`，真正落哪条线要等目标机 `Manager.Dispatch`
  （`internal/orchestration/manager.go:823-831` 调 `workspace.ResolveDefaultBaseBranch`）解析。

结论：**决策段能判断「这是不是首派」与「有没有显式基线」，但从不校验附件是否在解析出的树里**。
spec 的事故形态（本仓 main 落后功能线 438 个提交，spec 提交不在 main 的树里）因此静默发生。

### R2：附件路径是 git 仓内路径，`Card` 上现成可读

- `ledger.Card.Attachments []Attachment`（`internal/ledger/types.go:143`），`Attachment{Kind,Path}`
  （`:126-129`，Path 是仓内相对 git 路径）。
- `ledger.Card.BaseBranch` 是**卡自有**显式基线（`:145`）；继承来的基线由
  `EffectiveBaseBranch`（`internal/ledger/relations.go:194-205`）沿父链解析。
  ⇒ 豁免判据「卡上已显式设置 base_branch」读 `c.BaseBranch != ""` 即可，无需新查询。

### R3：仓库侧的读取能力已存在，缺的只是「这些路径在不在那棵树上」

- `workspace.ResolveDefaultBaseBranch`（`internal/workspace/gitworkspace.go:1318-1346`）读
  `refs/remotes/origin/HEAD` 得到默认分支**名**。
- `workspace.ResolveDispatchBase`（`:1074-1088`）对普通分支名走 `needsBaseBranchSync`
  （`:1045-1072`）⇒ `resolveRemoteBaseBranch`（`:1274-1316`）**无条件 fetch** 后
  `rev-parse refs/remotes/<remote>/<branch>^{commit}` —— 这正是 spec §3「SHA 靠远端补拉」的落点。
- 路径在场性判定没有现成函数：全仓 `git ls-tree` 零命中（本节点已 grep）。**需要新增一个只读
  `workspace.BaseTreeMissingPaths`**（见 T3）；它属既有 workspace 子系统，不新增依赖方向。

### R4：冻结写入契约（spec §5「冻结点不动」）

`SetCardBaseBranch`（`internal/ledger/cards.go:642-688`）在卡上出现**首条 dispatched 事件即拒**
（`ErrBadState: 基线已冻结`）。护栏必须在**写 dispatched 快照之前**（即 Transport 之前）拒发，
这样用户还能用 `handoff card update <卡> --base-branch <分支>`（`cmd/card.go:198-202`）设显式基线后重试。
本计划把护栏插在 `ViaTemplate` 的 `resolveDefaultBase := base == ""` 之后、`Transport` 之前，满足该约束。

---

## 2. 分流决定

| 事项 | 归属 | 处置 |
|---|---|---|
| 首派且无显式基线、有 spec/plan 附件时的路径在场判定 | 本卡，L2 / `internal/ledgerstep` | **T2**：`Dispatcher.ProbeBaseAttachments` 注入 + `guardFirstDispatchBase` 决策段 |
| 路径在不在基线远端树上（解析默认线 + 远端补拉 + ls-tree） | 本卡 / `internal/workspace`（既有子系统，只加只读函数） | **T3**：`workspace.BaseTreeMissingPaths` |
| 探针的生产装配（项目位置解析 + 调用 workspace） | 本卡 / `internal/agentd` | **T3**：`Server.probeBaseAttachments` + Dispatcher 接线 |
| 拒发文案（分支名 + 缺失路径 + 改法） | 本卡 / `internal/ledgerstep` | **T2**：文案由决策段产出，测试锁关键词 |
| `internal/ledger` 代码改动 | —— | **零改动**：首派判定复用 `WorkBranch`（`ErrNotFound`＝首派），豁免读 `Card.BaseBranch`，冻结点 `SetCardBaseBranch` 按 spec §5 不动。spec §2 把 ledger 列为改动面是在描述「基线语义归 ledger」的归属，不是要求新增查询；新增冗余查询会与 `WorkBranch` 同源判定漂移。 |
| 裸 `card dispatch`（无 `--step`）不经此闸 | 残余 | spec §6 的调用方清单只列 `card dispatch --step` 与队列出队；两者都经 `startCardStep`。裸派发在 CLI 侧另装 Dispatcher（`cmd/card_dispatch.go:355-378`），本卡不接线（探针 nil ⇒ 跳过）。残余写入 §7。 |
| 误基线已造成后的纠正（把工作分支指到正确分支） | 不在本卡 | 归 B382 spec §3（spec §7）。 |
| 真机重放 | 协调者执行 | 见 §11；本任务由协调者执行，不派发。 |

---

## 3. 任务 DAG

```
T1（红锚·缝级）：agentd 端到端用例落仓——HTTP step → startCardStep → ViaTemplate，
    默认线缺 spec 时必须拒发、不触达 Transport。今天编译得过、跑起来红。
      └→ T2（实现·缝级）：ledgerstep 探针字段 + 首派护栏 + ErrBaseProbeUnavailable，
            ViaTemplate 缝级单测（假探针，绿）。
            └→ T3（实现·缝级）：workspace.BaseTreeMissingPaths + agentd 生产探针与接线，
                  T1 转绿；补 workspace/agentd 附加边界锁。
                  └→ T4（收口）：不误伤回归 + 变异复验。
```

- **最薄路径条**：T1 是 spec §6 接缝 1 的最薄可跑路径，**今天编译得过且会红**（护栏不存在 ⇒
  默认线缺附件不被拦、Transport 被触达）。T1 转绿即全卡行为点亮。
- **次序承重**：T1 的红必须先出现，才能证明 T2+T3 的护栏是它转绿的原因；T4 的变异复验依赖护栏在场。
- T2 的 `ViaTemplate` 单测引用新增字段 `ProbeBaseAttachments`，**基线不可编译故无红**——红锚由 T1
  提供（T1 只用既有符号），T2 单测是护栏决策的附加缝级锁。此点在 §12 显式声明。

---

## 4. 基线事实（实现卡共享，动手前复核；原始输出见台账）

**亲跑读数（本节点，`fc645031` 工作树）**：

- `go version` → `go1.26.1 linux/amd64`。
- `go build ./...` → `BUILD_EXIT=0`。
- `go vet ./internal/ledgerstep/ ./internal/ledger/ ./internal/workspace/ ./internal/agentd/` → `VET_EXIT=0`。
- `go test ./internal/ledgerstep/ -count=1 -timeout 300s` → `ok ... 13.196s`。
- **红锚基线读数**（本节点以同形临时原型实跑，跑完已删，原文见台账 §8）：
  `go test ./internal/agentd/ -run TestB400TmpFirstDispatchRejectsWhenSpecMissingOnDefaultBase -count=1 -timeout 120s`
  → `--- FAIL ... 护栏未在 Transport 前拦下（transportCalled=1）`。
  即 T1 的形态在基线可编译、且因护栏缺失而红（生产装配 + 真仓库 + 真探针调用链全部走通）。

**git 行为事实（本节点在 `$TMPDIR` 真跑，原文见台账 §2）**：

- `git ls-tree -r --name-only <sha> -- ':(literal)<path>'`：路径在场 → 输出路径、退出 0；
  路径不在 → **空输出、退出 0**。故在场性判据 = `strings.TrimSpace(out) != ""`（不用退出码）。
- `git cat-file -e <sha>:<path>`：不在时退出非零（备选，本计划采用 ls-tree，因其带 `--` 更像参数安全面）。
- `git clone`（远端 HEAD 已 `symbolic-ref` 到 `refs/heads/main`）会自动写
  `refs/remotes/origin/HEAD → origin/main`，即 `ResolveDefaultBaseBranch` 可解析；普通分支名的
  `ResolveDispatchBase` 会对 file:// remote 正常 `git fetch`。

**库/框架行为事实（带出处）**：

- 默认线只解析「名字」，不 fetch：`internal/workspace/gitworkspace.go:1324-1326` 注释明确
  「这里只负责得到分支名，不在这里 fetch」；补拉归 `ResolveDispatchBase` D2（`:1074-1088`）。
- 附件 Path 是仓内相对 git 路径：`internal/ledger/types.go:126`。
- 附件 kind 白名单（HTTP 面）：`internal/agentd/ledgerapi.go:777`
  `{"spec","plan","doc","contract"}`。
- charter 首个被派发节点的附件前提：`deploy/workflows/charter-v4.json` 的 `contract` 节点
  `gate.require_attachment="spec"`（`:27-29`）、`plan` 节点 `gate.require_attachment="spec"`（`:69-71`）；
  `contract`/`doc`/`plan` 是**后续节点**的 `produces`（`:31-34`、`:52-55`、`:73-76`），首派时尚未挂上。
  ⇒ 首派时「本卡附件」就是 spec（以及可能被人工预挂的 plan），与 spec §3「至少 spec；有 plan 时一并查」同形。

**现状签名与调用面（直读源码；行号为现状读数）**：

- `Dispatcher.ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error)`
  — `internal/ledgerstep/dispatch.go:164`；Dispatchers 装配点 `internal/agentd/cardstep.go:199-218`。
- `Store.WorkBranch(cardID string) (WorkBranchInfo, error)` — `internal/ledger/events.go:575`。
- `Store.EffectiveBaseBranch(id string) (string, error)` — `internal/ledger/relations.go:197`。
- `Store.SetCardBaseBranch(id, branch, actor string) error` — `internal/ledger/cards.go:642`。
- `workspace.ResolveDefaultBaseBranch(ctx, repo) (string, error)` — `internal/workspace/gitworkspace.go:1328`。
- `workspace.ResolveDispatchBase(ctx, repo, rev, localBaseBranch) (string, bool, error)` — 同文件 `:1078`。
- `workspace.ResolveProject(projectID, projectName string, entries []proto.ProjectLocation) (proto.ProjectLocation, error)`
  — `internal/workspace/projectresolve.go:79`；`workspace.ErrProjectNotRegistered` — `:47`。
- `store.Store.ListProjectLocations() ([]proto.ProjectLocation, error)` — `internal/store/projects.go:99`；
  `Server.st` 即 `*store.Store`（`internal/agentd/server.go:109`）。
- `store.Store.CreateProjectLocation(loc *proto.ProjectLocation) error` — `internal/store/projects.go:59`。
- `Server.startCardStep(cardID string, req proto.CardStepReq) error` — `internal/agentd/cardstep.go:129`。

**既有测试夹具（复用，签名逐字）**：

- `newLedgerEnv(t *testing.T) *ledgerEnv` — `internal/agentd/ledgerapi_test.go:139`（内含 `env.st`、`env.ledger`、`env.testAgentdEnv`）。
- `seedAgentdLedger(t *testing.T, st *ledger.Store, workflowNames ...string)` — `internal/agentd/ledger_fixtures_test.go:13`。
- `seedDisciplineOnLedger(t *testing.T, env *ledgerEnv, name, body string) int` — `internal/agentd/cardstep_discipline_test.go:131`。
- `ledgerPost(t *testing.T, env *testAgentdEnv, path, body string) (int, string)` — `internal/agentd/ledgerapi_test.go:44`。
- `waitFor(t *testing.T, predicate func() bool)` — `internal/agentd/cardstep_test.go:88`。
- `dispatchTestCard(t *testing.T) (*ledger.Store, ledger.Card)` — `internal/ledgerstep/dispatch_test.go:48`。
- `seedLedgerStepStore(t *testing.T, st *ledger.Store)` — `internal/ledgerstep/dispatch_test.go:20`。
- `newOriginAndClone(t *testing.T) (origin, clone string)`、`commitOnOrigin(...)`、`gitAt(...)`、`writeAndCommit(...)`
  — `internal/workspace/gittesthelpers_test.go:110/129/19/42`（**workspace 包内测试专用**，T4 复用）。

---

## 5. 接口契约（执行者只看本 task，故这里列全）

### Consumes（本卡要用的既有签名，一字不改）

```go
// internal/ledger
type Attachment struct { Kind string; Path string }
type Card struct { /* … */ BaseBranch string; Attachments []Attachment; Project string /* … */ }
func (s *Store) WorkBranch(cardID string) (WorkBranchInfo, error)   // ErrNotFound = 无非审阅派发
func (s *Store) EffectiveBaseBranch(id string) (string, error)

// internal/workspace
func ResolveDefaultBaseBranch(ctx context.Context, repo string) (string, error)
func ResolveDispatchBase(ctx context.Context, repo, rev string, localBaseBranch bool) (string, bool, error)
func ResolveProject(projectID, projectName string, entries []proto.ProjectLocation) (proto.ProjectLocation, error)
var ErrProjectNotRegistered error

// internal/store
func (s *Store) ListProjectLocations() ([]proto.ProjectLocation, error)

// internal/agentd
type Server struct { st *store.Store; ledger *ledger.Store /* … */ }
func (s *Server) runStep(ctx context.Context, runner *ledgerstep.StepRunner, cardID, node string)
```

### Produces（本卡新增，跨 task 逐字对齐）

```go
// internal/ledgerstep/dispatch.go
var ErrBaseProbeUnavailable = errors.New("本机无法探查项目基线树")

// Dispatcher 新增字段：
ProbeBaseAttachments func(ctx context.Context, project, base string, paths []string) (resolvedBase string, missing []string, err error)

// 包内新增：
func baseAttachmentPaths(attachments []ledger.Attachment) []string
func (d *Dispatcher) guardFirstDispatchBase(ctx context.Context, c ledger.Card, hasWorkBranch bool, base string) error

// internal/workspace/basetree.go（新文件）
func BaseTreeMissingPaths(ctx context.Context, repo, base string, paths []string) (resolvedBase string, missing []string, err error)
func repoRelativePathSafe(p string) bool

// internal/agentd/cardstep.go（追加方法）
func (s *Server) probeBaseAttachments(ctx context.Context, project, base string, paths []string) (string, []string, error)

// 测试（新文件）
// internal/agentd/b400_first_dispatch_test.go
func TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase(t *testing.T)   // T1，红锚
func TestB400ProbeReportsMissingAndPresentPaths(t *testing.T)                // T3
func TestB400ProbeSkipsWhenProjectNotRegistered(t *testing.T)                // T3
// internal/ledgerstep/b400_base_guard_test.go
func TestB400FirstDispatchRejectsMissingAttachment(t *testing.T)             // T2 断言①
func TestB400FirstDispatchPassesWhenAttachmentPresent(t *testing.T)          // T2 断言②
func TestB400LaterNodeSkipsGuard(t *testing.T)                               // T2 断言③
func TestB400ExplicitBaseSkipsGuard(t *testing.T)                            // T2 断言④
func TestB400NoAttachmentsPasses(t *testing.T)                               // T2 接缝 2（边界）
func TestB400ProbeErrorRejects(t *testing.T)                                 // T2 fail-closed
func TestB400ProbeUnavailableSkips(t *testing.T)                             // T2 P1(甲) fail-open
// internal/workspace/b400_base_tree_test.go
func TestB400BaseTreeMissingPaths(t *testing.T)                              // T3 附加边界锁
```

> **序列化边界**：本卡**不新增任何数据字段**、不改任何 DTO/json tag、不改 wire 契约、不新增命令。
> 唯一跨界是「拒发错误文本 → `haltForHuman` 的 comment body（既有 JSON）」，由 T1 穿过真实事件流断言。
> 故无新增手写序列化/投影点（见 §7 追加设问一）。

---

## 6. 任务详情

### T1 红锚：首派基线护栏的端到端用例（先红）

**动作**：新建 `internal/agentd/b400_first_dispatch_test.go`，内容如下（完整，无占位）。
本节点已用**同形的临时原型文件**在基线实跑（跑完已删，原文见台账 §8），确认它编译得过、
且因护栏缺失而红——实现卡落正式文件后复跑一次确认同一红读数，再进 T2/T3。

```go
// b400_first_dispatch_test.go —— B400 首派基线护栏的端到端回归。
//
// 职责：锁住「卡首次非审阅派发时，若解析出的基线（默认线）树里没有本卡 spec
// 附件，派发必须被拒、留 needs_human 说明、且绝不触达 Transport」。
// 缝：HTTP `POST /api/cards/{id}/step` → startCardStep → StepRunner.Run →
// ViaTemplate 的派发决议段（spec §6 接缝 1）。断言落在真实生产装配上，不直调
// ViaTemplate，也不替换 runStepFn 的 Runner——只替换 Transport 以观测「护栏是否
// 在派发前拦下」。
// 边界：不复制 workspace 的路径在场判定；不改任何 git ref；不测 task 生命周期。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
)

// b400Git 在 dir 执行 git，失败即 Fatal。
func b400Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// b400CloneWithoutSpec 造「裸 origin + 克隆」：默认分支 main 上只有 README.md，
// 没有 docs/superpowers/specs/b400.md。返回克隆路径（= 本机项目仓库）。
//
// 必须用克隆而不是 git init：只有克隆才会自动写 refs/remotes/origin/HEAD，且
// ResolveDispatchBase 对普通分支名会真的 fetch（file:// remote 可在测试内离线完成）。
func b400CloneWithoutSpec(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	origin := filepath.Join(parent, "origin.git")
	b400Git(t, parent, "init", "--bare", "-q", origin)
	seed := filepath.Join(t.TempDir(), "seed")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	b400Git(t, seed, "init", "-q")
	b400Git(t, seed, "checkout", "-q", "-b", "main")
	b400Git(t, seed, "config", "user.email", "t@handoff.dev")
	b400Git(t, seed, "config", "user.name", "handoff test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b400Git(t, seed, "add", "README.md")
	b400Git(t, seed, "commit", "-q", "-m", "init")
	b400Git(t, seed, "remote", "add", "origin", origin)
	b400Git(t, seed, "push", "-q", "origin", "main")
	b400Git(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	clone := filepath.Join(t.TempDir(), "clone")
	b400Git(t, parent, "clone", "-q", origin, clone)
	return clone
}

// b400FlowEnv 建一张钉在「首个非审阅派发节点 impl」上的卡，并把项目位置登记到 repo。
func b400FlowEnv(t *testing.T, repo string) (*ledgerEnv, string) {
	t.Helper()
	env := newLedgerEnv(t)
	seedAgentdLedger(t, env.ledger, "bug")
	seedDisciplineOnLedger(t, env, discipline.NameImplement, "本机测试实现纪律")
	if _, err := env.ledger.PutWorkflow("b400-flow", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: "impl"},
		{Name: "impl", Dispatch: true, Verdict: true, Template: "feature-impl", MaxRounds: 3, Next: ledger.StatusDone},
		{Name: ledger.StatusDone},
	}}); err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	card, err := env.ledger.CreateCard(ledger.NewCard{
		Title: "B400 首派卡", Project: "handoff", Workflow: "b400-flow", Actor: "test",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "handoff-b400", Name: "handoff", Path: repo, OriginURL: "", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("登记项目位置: %v", err)
	}
	return env, card.ID
}

// b400CardState 从卡事件流取 needs_human 的 reason、最后一条 comment 正文与是否已有 dispatched。
func b400CardState(t *testing.T, env *ledgerEnv, cardID string) (reason, comment string, dispatched bool) {
	t.Helper()
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 200)
	if err != nil {
		t.Fatalf("读卡事件: %v", err)
	}
	for _, e := range events {
		switch e.Type {
		case ledger.EvNeedsHuman:
			var p struct {
				Reason string `json:"reason"`
			}
			if json.Unmarshal(e.Payload, &p) == nil && p.Reason != "" {
				reason = p.Reason
			}
		case ledger.EvComment:
			var p struct {
				Body string `json:"body"`
			}
			if json.Unmarshal(e.Payload, &p) == nil && p.Body != "" {
				comment = p.Body
			}
		case ledger.EvDispatched:
			dispatched = true
		}
	}
	return reason, comment, dispatched
}

// TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase 锁 spec §6 接缝 1 断言①：
// 卡无快照、无显式基线、spec 附件不在默认线树上 ⇒ 拒发；文案含解析到的分支名与缺失
// 路径；护栏在 Transport 之前（transportCalled=0），且不留 dispatched 快照。
//
// 红（当前 HEAD）：护栏不存在，ViaTemplate 直达被替换的 Transport；卡落的是运输失败
// 说明，transportCalled=1，断言全灭。
// 绿（T2+T3）：护栏拒发，transportCalled=0，reason=派发失败、comment 含 main 与路径。
func TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase(t *testing.T) {
	repo := b400CloneWithoutSpec(t)
	env, cardID := b400FlowEnv(t, repo)
	const specPath = "docs/superpowers/specs/b400.md"
	if _, err := env.ledger.AttachFile(cardID, "spec", specPath, "test"); err != nil {
		t.Fatalf("挂 spec: %v", err)
	}

	var transportCalled int32
	env.srv.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, id, node string) {
		runner.Dispatcher.Transport = func(context.Context, ledgerstep.DispatchOpts) (string, string, error) {
			atomic.StoreInt32(&transportCalled, 1)
			return "", "", errors.New("Transport 不应被触达：基线护栏缺失")
		}
		env.srv.runStep(ctx, runner, id, node)
	}

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/step",
		`{"step":"impl","actor":"cli:u@h#1"}`)
	if code != 202 {
		t.Fatalf("首派应 202 受理，实得 %d（%s）", code, body)
	}
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })

	if got := atomic.LoadInt32(&transportCalled); got != 0 {
		t.Fatalf("护栏未在 Transport 前拦下（transportCalled=%d）", got)
	}
	reason, comment, dispatched := b400CardState(t, env, cardID)
	if dispatched {
		t.Fatalf("被拒的首派不得留 dispatched 快照；comment=%s", comment)
	}
	if reason != "派发失败" {
		t.Fatalf("卡应落 needs_human(派发失败)，实得 reason=%q，comment=%s", reason, comment)
	}
	for _, want := range []string{"main", specPath, "card update", "--base-branch"} {
		if !strings.Contains(comment, want) {
			t.Fatalf("拒发文案缺 %q：\n%s", want, comment)
		}
	}
}
```

- **Interfaces**
  - Consumes：§4 夹具清单 + §5 Consumes（全部既有符号）。
  - Produces：本文件三个测试；T3 追加后两个。
- **步骤**
  1. 落文件。
  2. 跑红（**实现卡的第一步**）：
     `go test ./internal/agentd/ -run TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase -count=1 -timeout 120s`
     → 预期 FAIL：`--- FAIL ... 护栏未在 Transport 前拦下（transportCalled=1）`
     （本节点原型已实跑命中该行，原文见台账 §8）。把原文抄进实现台账。
- **测试范围声明**：只跑 `./internal/agentd/`（本 task 只新增测试文件）。
- **日志与注释**：文件头写职责/边界/缝；`b400CloneWithoutSpec` 写 why（必须克隆、file remote、origin/HEAD）；
  `b400CardState` 写 why（失败时保留卡事件现场）。

### T2 实现（缝级）：首派基线护栏的决策段

**文件**：`internal/ledgerstep/dispatch.go`（只改此文件）。

**改动一（哨兵，放在 `ErrWriteGateClosed` 语义附近；`ErrWriteGateClosed` 在 node.go，此处新增包级 var）**：

```go
// ErrBaseProbeUnavailable 表示首个节点基线护栏无法在**本机**完成探查（例如项目
// 只登记在远端开发机、本机没有仓库位置）。它不是「基线错了」，而是「本机查不了」：
// 护栏对这类派发放行并记 Warn（P1 推荐甲），把否决权留给真正能读到基线树的机器，
// 避免因为协调机没有本地仓而把远端派发全部误杀。
var ErrBaseProbeUnavailable = errors.New("本机无法探查项目基线树")
```

**改动二（`Dispatcher` 新增探针字段；插在 `NormalizeTarget func(target string) string` 之后）**：

```go
	// ProbeBaseAttachments 是首个节点派发前的基线附件探针（B400）。首个非审阅
	// 派发、卡无显式基线、且有 spec/plan 可查附件时，ViaTemplate 调它确认附件
	// 路径确实在解析出的基线远端提交树上；缺则拒发。nil = 不检查（旧调用/裸派发）。
	// 探针实现归调用方（需要项目仓库位置），本包只做决策与文案。
	ProbeBaseAttachments func(ctx context.Context, project, base string, paths []string) (resolvedBase string, missing []string, err error)
```

**改动三（新增包内 helper，放在 `ViaTemplate` 之前）**：

```go
// baseAttachmentPaths 取需要随基线一起在场的附件路径：spec 必查，plan 有时一并查。
// 其余 kind（contract/doc 等）要么是后续节点产出（不在首派基线树上），要么不由
// charter 首派保证，故不纳入——判据宁可少查一条，也不误杀合法首派。
func baseAttachmentPaths(attachments []ledger.Attachment) []string {
	paths := make([]string, 0, len(attachments))
	for _, att := range attachments {
		if att.Kind == "spec" || att.Kind == "plan" {
			if p := strings.TrimSpace(att.Path); p != "" {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// guardFirstDispatchBase 是 B400 首派基线护栏：卡的首次非审阅派发时，若没有显式
// 基线、又有可查附件，则确认附件路径存在于解析出的基线树；缺则拒发。
//
// 决策顺序（spec §5）：
//  1. hasWorkBranch（已有非审阅派发）= 后续节点 ⇒ 跳过；
//  2. c.BaseBranch 非空 = 卡自有显式基线，是人工否决权 ⇒ 跳过；
//  3. 没有 spec/plan 可查附件 ⇒ 放行（「无附件可检」交既有列门，避免双重执法）；
//  4. 否则探针查路径在场性：缺 ⇒ 拒发（文案含分支名+缺失路径+改法）；
//     探针不可用（ErrBaseProbeUnavailable）⇒ 放行并告警；其它探针错误 ⇒ 拒发（fail-closed）。
func (d *Dispatcher) guardFirstDispatchBase(ctx context.Context, c ledger.Card, hasWorkBranch bool, base string) error {
	if hasWorkBranch {
		return nil
	}
	if c.BaseBranch != "" {
		return nil
	}
	paths := baseAttachmentPaths(c.Attachments)
	if len(paths) == 0 || d.ProbeBaseAttachments == nil {
		return nil
	}
	resolvedBase, missing, err := d.ProbeBaseAttachments(ctx, c.Project, base, paths)
	if err != nil {
		if errors.Is(err, ErrBaseProbeUnavailable) {
			slog.Default().Warn("首派基线护栏无法探查，跳过", "card", c.ID,
				"project", c.Project, "base", base, "paths", paths, "cause", err)
			return nil
		}
		slog.Default().Warn("首派基线护栏探查失败，拒发", "card", c.ID,
			"project", c.Project, "base", base, "paths", paths, "cause", err)
		return fmt.Errorf("首个节点派发前的基线附件校验失败: %w", err)
	}
	if len(missing) > 0 {
		action := fmt.Sprintf("请显式声明基线后重试：handoff card update %s --base-branch <分支>（确认默认线正确的场景也请显式声明该线）", c.ID)
		slog.Default().Warn("首派基线缺附件，拒发", "card", c.ID, "project", c.Project,
			"resolved_base", resolvedBase, "missing_paths", missing)
		return fmt.Errorf("拒发：解析到的基线分支 %q 的提交树里找不到本卡附件 %s；默认线可能不是本卡工作线。%s",
			resolvedBase, strings.Join(missing, "、"), action)
	}
	slog.Default().Info("首派基线附件在场校验通过", "card", c.ID, "project", c.Project,
		"resolved_base", resolvedBase, "paths", paths)
	return nil
}
```

**改动四（在 `ViaTemplate` 的 `resolveDefaultBase := base == ""` 之后、`cardBase` 取用之前插入调用）**：

改前（现状 `:296`）：

```go
	resolveDefaultBase := base == ""
```

改后：

```go
	resolveDefaultBase := base == ""
	if err := d.guardFirstDispatchBase(ctx, c, hasWorkBranch, base); err != nil {
		return zero, err
	}
```

**测试追加**：新建 `internal/ledgerstep/b400_base_guard_test.go`，内容如下（完整）。

```go
// b400_base_guard_test.go —— B400 首派基线护栏的决策段回归（缝 1：ViaTemplate）。
//
// 职责：用假探针锁住「无显式基线 + 有 spec 附件 + 首个非审阅派发」时的四条决策
// 与两条探针异常；不碰 git（路径在场性由 T3 的 workspace/端到端用例覆盖）。
// 缝：`Dispatcher.ViaTemplate`——spec §6 承重缝的「派发决议段」本体；
// 生产调用方（startCardStep / 队列出队）最终都到达它。
package ledgerstep

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

const b400SpecPath = "docs/superpowers/specs/b400.md"

func b400CardWithSpec(t *testing.T) (*ledger.Store, ledger.Card) {
	t.Helper()
	st, card := dispatchTestCard(t)
	if _, err := st.AttachFile(card.ID, "spec", b400SpecPath, "test"); err != nil {
		t.Fatalf("挂 spec: %v", err)
	}
	return st, card
}

// TestB400FirstDispatchRejectsMissingAttachment 断言①：默认线树缺附件 ⇒ 拒发，
// 文案含分支名、缺失路径与可行动作；且拒发发生在 Transport 之前。
func TestB400FirstDispatchRejectsMissingAttachment(t *testing.T) {
	st, card := b400CardWithSpec(t)
	probeCalled, transportCalled := false, false
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(_ context.Context, project, base string, paths []string) (string, []string, error) {
			probeCalled = true
			if project != card.Project || base != "" || len(paths) != 1 || paths[0] != b400SpecPath {
				t.Fatalf("探针入参 = project:%q base:%q paths:%v", project, base, paths)
			}
			return "main", []string{b400SpecPath}, nil
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) {
			transportCalled = true
			return "T-b400", "", nil
		},
	}
	_, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"})
	if err == nil {
		t.Fatal("默认线缺附件应拒发，实得 nil")
	}
	if !probeCalled {
		t.Fatal("护栏未调用探针")
	}
	if transportCalled {
		t.Fatal("拒发必须发生在 Transport 之前")
	}
	for _, want := range []string{"main", b400SpecPath, "card update", "--base-branch"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("拒发文案缺 %q：%v", want, err)
		}
	}
}

// TestB400FirstDispatchPassesWhenAttachmentPresent 断言②：附件在场 ⇒ 放行，零假阳性。
func TestB400FirstDispatchPassesWhenAttachmentPresent(t *testing.T) {
	st, card := b400CardWithSpec(t)
	transportCalled := false
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			return "main", nil, nil
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) {
			transportCalled = true
			return "T-b400", "", nil
		},
	}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("附件在场应放行，实得 %v", err)
	}
	if !transportCalled {
		t.Fatal("放行后应到达 Transport")
	}
}

// TestB400LaterNodeSkipsGuard 断言③（回归锁）：已有非审阅派发 ⇒ 后续节点不再查护栏。
func TestB400LaterNodeSkipsGuard(t *testing.T) {
	st, card := b400CardWithSpec(t)
	if err := st.RecordDispatch(card.ID, ledger.DispatchSnapshot{
		Template: "feature-impl", TemplateVersion: 1, Target: "mac-02",
		TaskID: "T-prev", Branch: "cards/demo-implement", Purpose: "implement", Actor: "tester",
	}); err != nil {
		t.Fatalf("写先派快照: %v", err)
	}
	probeCalled := false
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			probeCalled = true
			return "main", []string{b400SpecPath}, nil
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) { return "T-b400", "", nil },
	}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("后续节点应放行，实得 %v", err)
	}
	if probeCalled {
		t.Fatal("首个非审阅派发之后不得再查护栏")
	}
}

// TestB400ExplicitBaseSkipsGuard 断言④（豁免通道，防死锁）：卡自有显式基线 ⇒ 跳过，
// 即使探针会报缺附件也不得拒发。
func TestB400ExplicitBaseSkipsGuard(t *testing.T) {
	st, _ := dispatchTestCard(t)
	card, err := st.CreateCard(ledger.NewCard{
		Title: "显式基线卡", Project: "demo", Workflow: "bug", BaseBranch: "cards/demo-work", Actor: "test",
	})
	if err != nil {
		t.Fatalf("建显式基线卡: %v", err)
	}
	if _, err := st.AttachFile(card.ID, "spec", b400SpecPath, "test"); err != nil {
		t.Fatalf("挂 spec: %v", err)
	}
	probeCalled := false
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			probeCalled = true
			return "cards/demo-work", []string{b400SpecPath}, nil
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) { return "T-b400", "", nil },
	}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("显式基线应豁免，实得 %v", err)
	}
	if probeCalled {
		t.Fatal("显式基线必须最先短路，探针不得被调用")
	}
}

// TestB400NoAttachmentsPasses 接缝 2（边界）：未挂任何可查附件 ⇒ 放行、不调探针。
func TestB400NoAttachmentsPasses(t *testing.T) {
	st, card := dispatchTestCard(t)
	probeCalled := false
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			probeCalled = true
			return "main", []string{b400SpecPath}, nil
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) { return "T-b400", "", nil },
	}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("无附件可检应放行，实得 %v", err)
	}
	if probeCalled {
		t.Fatal("无可查附件不得调用探针")
	}
}

// TestB400ProbeErrorRejects：探针自身失败（如远端不可达）⇒ fail-closed 拒发并保留 cause。
func TestB400ProbeErrorRejects(t *testing.T) {
	st, card := b400CardWithSpec(t)
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			return "", nil, errors.New("远端仓库不可达")
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) { return "T-b400", "", nil },
	}
	_, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"})
	if err == nil || !strings.Contains(err.Error(), "远端仓库不可达") {
		t.Fatalf("探针失败应 fail-closed 拒发并保留 cause，实得 %v", err)
	}
}

// TestB400ProbeUnavailableSkips：探针不可用（本机无该项目位置）⇒ 放行并告警（P1 推荐甲）。
func TestB400ProbeUnavailableSkips(t *testing.T) {
	st, card := b400CardWithSpec(t)
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			return "", nil, ErrBaseProbeUnavailable
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) { return "T-b400", "", nil },
	}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("探针不可用应放行，实得 %v", err)
	}
}
```

- **Interfaces**
  - Consumes：`dispatchTestCard`、`DispatchOpts`、`Dispatcher`、`ledger.DispatchSnapshot.RecordDispatch`（既有）。
  - Produces：见 §5。
- **步骤**
  1. 判据先在基线跑（复核）：T1 已红（实现卡第一步的原文）。
  2. 按改动一~四改 `internal/ledgerstep/dispatch.go`。
  3. 落 `internal/ledgerstep/b400_base_guard_test.go`。
  4. 跑绿：`go test ./internal/ledgerstep/ -run 'TestB400' -count=1 -timeout 120s` → 预期全 PASS。
  5. 静态检查：`go build ./...`、`go vet ./internal/ledgerstep/`（预期 `EXIT=0`）。
- **加关键节点日志**：`guardFirstDispatchBase` 四条分支各一条（跳过不可见？——`hasWorkBranch`/显式基线是常态静默，
  只有「探针不可用」「探针失败」「缺附件」「通过」四条记日志；前两条常态不记，避免刷屏）。
  缺附件与探针失败取 Warn（可行动），探针不可用取 Warn（说明为何没拦），通过取 Info（成功路径不静默）。
- **加注释**：哨兵、字段、两个函数的「为什么」已写全（首派语义、豁免防死锁、只查 spec/plan、fail-open/closed 取舍）。
- **测试范围声明**：只跑 `./internal/ledgerstep/`。

### T3 实现（缝级）：workspace 树读取 + agentd 生产探针接线，T1 转绿

**文件**：新建 `internal/workspace/basetree.go`；改 `internal/agentd/cardstep.go`；追加测试到 T1 的文件 + 新建 `internal/workspace/b400_base_tree_test.go`。

#### 改动五：`internal/workspace/basetree.go`（新文件，只读）

```go
// basetree.go —— B400 首派基线护栏的仓库侧读取：判断附件路径是否存在于解析出的
// 基线远端提交树。
//
// 职责：解析基线分支名（空则取项目默认分支）→ 远端补拉并解析到 SHA → 逐路径判在场性。
// 边界：只读；不改任何 ref、不建分支/工作树、不写盘；不判断「哪条才是工作线」，
// 只回答「这些路径在不在那棵树上」。
package workspace

import (
	"context"
	"strings"
)

// BaseTreeMissingPaths 报告 paths 里哪些不在 base 分支的远端提交树中。
//
// 参数：
//   - ctx: 上层上下文
//   - repo: 已通过 EnsureRepoUsable 的项目仓库路径
//   - base: 基线分支名；空 = 先解析项目默认分支（ResolveDefaultBaseBranch）
//   - paths: 仓内相对 git 路径
//
// 返回：实际使用的基线分支名、缺失路径（保持入参顺序、不去重）、错误。
//
// 注意：
//   - 一律经 ResolveDispatchBase 补拉并解析到**远端** SHA，不读本地陈旧分支——
//     这正是「检查读远端 commit 树」的落点；
//   - 路径在场性用 `git ls-tree -r --name-only <sha> -- ':(literal)<path>'` 判定：
//     输出非空即在场（本节点已在 $TMPDIR 亲跑：缺失时空输出且退出 0）；不做内容比对；
//   - base 为空且 origin/HEAD 缺失时返回 ResolveDefaultBaseBranch 的错误。
func BaseTreeMissingPaths(ctx context.Context, repo, base string, paths []string) (string, []string, error) {
	resolvedBase := strings.TrimSpace(base)
	if resolvedBase == "" {
		defaultBase, err := ResolveDefaultBaseBranch(ctx, repo)
		if err != nil {
			return "", nil, err
		}
		resolvedBase = defaultBase
	}
	sha, _, err := ResolveDispatchBase(ctx, repo, resolvedBase, false)
	if err != nil {
		return resolvedBase, nil, err
	}
	missing := make([]string, 0)
	for _, p := range paths {
		if !repoRelativePathSafe(p) {
			missing = append(missing, p)
			continue
		}
		out, _, err := gitProbe(ctx, repo, "ls-tree", "-r", "--name-only", sha, "--", ":(literal)"+p)
		if err != nil || strings.TrimSpace(out) == "" {
			missing = append(missing, p)
		}
	}
	return resolvedBase, missing, nil
}

// repoRelativePathSafe 拒绝会把 git 参数面打开的路径形态（空、绝对、以 - 开头、
// 含 .. 段）。附件路径由平台写入，正常都安全；这里 fail-closed 把可疑路径当作
// 「不在树上」，绝不把它拼进 git 参数。
func repoRelativePathSafe(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}
```

#### 改动六：`internal/agentd/cardstep.go` —— 生产探针 + 接线

1. import 段新增：`"github.com/Xsxdot/handoff/internal/workspace"`（`errors`、`fmt` 已在）。

2. 在 `stepTransport` 之后（文件末附近）新增方法：

```go
// probeBaseAttachments 是首派基线护栏（B400）的生产探针：在**本机**解析项目
// 仓库位置，交给 workspace 判断附件路径是否在基线远端提交树上。
//
// 参数：project 是卡的 project 名；base 是有效基线分支名（空=解析默认分支）；
// paths 是仓内相对路径。
// 返回：解析到的基线分支名、缺失路径、错误。项目在本机没有位置时返回
// ledgerstep.ErrBaseProbeUnavailable，由护栏侧跳过（不误杀远端派发，P1 甲）。
//
// 为什么在本机查：默认线与 SHA 的解析（ResolveDefaultBaseBranch/ResolveDispatchBase）
// 需要项目仓库路径，而仓库路径按设计只在目标机解析（Manager.Dispatch）；本机若
// 登记了同一项目，就用本机这份镜像查——这正是 spec §3「默认线在协调者侧解析」。
func (s *Server) probeBaseAttachments(ctx context.Context, project, base string, paths []string) (string, []string, error) {
	entries, err := s.st.ListProjectLocations()
	if err != nil {
		return "", nil, fmt.Errorf("列项目位置: %w", err)
	}
	loc, err := workspace.ResolveProject("", project, entries)
	if err != nil {
		if errors.Is(err, workspace.ErrProjectNotRegistered) {
			s.log.Warn("首派基线护栏：项目在本机无位置，跳过探查", "project", project)
			return "", nil, ledgerstep.ErrBaseProbeUnavailable
		}
		return "", nil, err
	}
	resolvedBase, missing, err := workspace.BaseTreeMissingPaths(ctx, loc.Path, base, paths)
	if err != nil {
		s.log.Warn("首派基线护栏探查基线树失败", "project", project, "repo", loc.Path,
			"base", base, "paths", paths, "cause", err)
		return resolvedBase, nil, err
	}
	s.log.Info("首派基线护栏探查完成", "project", project, "repo", loc.Path,
		"base", base, "resolved_base", resolvedBase, "paths", paths, "missing", missing)
	return resolvedBase, missing, nil
}
```

3. `Dispatcher{...}` 字面量（`cardstep.go:199-218`）里，`NormalizeTarget: s.CanonicalTarget,` 之后新增：

```go
			ProbeBaseAttachments: s.probeBaseAttachments,
```

#### 改动七：追加 agentd 附加边界锁到 `internal/agentd/b400_first_dispatch_test.go`

```go
// TestB400ProbeReportsMissingAndPresentPaths 覆盖生产探针正/负两态：默认分支树里有
// README.md、没有 spec 路径。
//
// 内部锁声明：入口 Server.probeBaseAttachments 不在 spec 两条缝的入口符号上；缝级断言
// 由同文件 T1 的端到端用例从 HTTP step 进入给出。本用例是「探针本身可读真仓库」的
// 附加边界锁，不顶替任何缝级断言（§10）。
func TestB400ProbeReportsMissingAndPresentPaths(t *testing.T) {
	repo := b400CloneWithoutSpec(t)
	env, _ := b400FlowEnv(t, repo)
	resolved, missing, err := env.srv.probeBaseAttachments(context.Background(), "handoff", "",
		[]string{"README.md", "docs/superpowers/specs/b400.md"})
	if err != nil {
		t.Fatalf("探针: %v", err)
	}
	if resolved != "main" {
		t.Fatalf("解析默认分支 = %q，want main", resolved)
	}
	if len(missing) != 1 || missing[0] != "docs/superpowers/specs/b400.md" {
		t.Fatalf("缺失路径 = %v，want 只有 spec 路径", missing)
	}
}

// TestB400ProbeSkipsWhenProjectNotRegistered：项目在本机无位置 ⇒ ErrBaseProbeUnavailable。
func TestB400ProbeSkipsWhenProjectNotRegistered(t *testing.T) {
	env := newLedgerEnv(t)
	_, _, err := env.srv.probeBaseAttachments(context.Background(), "nowhere", "", []string{"a"})
	if !errors.Is(err, ledgerstep.ErrBaseProbeUnavailable) {
		t.Fatalf("未登记项目应返回 ErrBaseProbeUnavailable，实得 %v", err)
	}
}
```

#### 改动八：新建 `internal/workspace/b400_base_tree_test.go`（附加边界锁）

```go
// b400_base_tree_test.go —— B400 BaseTreeMissingPaths 的真 git 夹具边界锁。
//
// 内部锁声明：workspace 函数不在 spec 两条缝的入口符号上；缝级断言由
// internal/agentd/b400_first_dispatch_test.go 的端到端用例给出。本用例覆盖
// 「在场/缺失/默认分支解析/非法路径」四类边界，不顶替任何缝级断言。
package workspace

import (
	"context"
	"testing"
)

func TestB400BaseTreeMissingPaths(t *testing.T) {
	origin, clone := newOriginAndClone(t)
	commitOnOrigin(t, origin, "docs/superpowers/specs/b400.md", "spec\n")

	cases := []struct {
		name        string
		base        string
		paths       []string
		wantResolved string
		wantMissing []string
	}{
		{name: "默认线：spec 在场", base: "", paths: []string{"docs/superpowers/specs/b400.md"},
			wantResolved: "main", wantMissing: nil},
		{name: "默认线：plan 缺失", base: "", paths: []string{"docs/superpowers/plans/b400-plan.md"},
			wantResolved: "main", wantMissing: []string{"docs/superpowers/plans/b400-plan.md"}},
		{name: "显式 main：README 在场", base: "main", paths: []string{"README.md"},
			wantResolved: "main", wantMissing: nil},
		{name: "非法路径按缺失处理", base: "main", paths: []string{"../secret"},
			wantResolved: "main", wantMissing: []string{"../secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolved, missing, err := BaseTreeMissingPaths(context.Background(), clone, tc.base, tc.paths)
			if err != nil {
				t.Fatalf("BaseTreeMissingPaths: %v", err)
			}
			if resolved != tc.wantResolved {
				t.Fatalf("resolved = %q，want %q", resolved, tc.wantResolved)
			}
			if len(missing) != len(tc.wantMissing) {
				t.Fatalf("missing = %v，want %v", missing, tc.wantMissing)
			}
			for i := range missing {
				if missing[i] != tc.wantMissing[i] {
					t.Fatalf("missing = %v，want %v", missing, tc.wantMissing)
				}
			}
		})
	}
}
```

> 注：`newOriginAndClone` 的克隆里 origin 的 HEAD 已 `symbolic-ref` 到 main，且 clone 自动带
> `refs/remotes/origin/HEAD → origin/main`（本节点已亲跑确认，见台账 §2）。`commitOnOrigin`
> 会再 fetch 到该提交，因此「默认线：spec 在场」用例覆盖「远端补拉后新提交可见」这条关键路径。

- **Interfaces**
  - Consumes：`ResolveDefaultBaseBranch`、`ResolveDispatchBase`、`gitProbe`、`Server.st`、`workspace.ResolveProject`（全部既有）。
  - Produces：见 §5。
- **步骤**
  1. 落 `internal/workspace/basetree.go`。
  2. 改 `internal/agentd/cardstep.go`（import + 方法 + 接线一行）。
  3. 追加上两条 agentd 测试 + 新建 workspace 测试。
  4. 跑绿：`go test ./internal/agentd/ -run 'TestB400' -count=1 -timeout 120s`
     与 `go test ./internal/workspace/ -run TestB400BaseTreeMissingPaths -count=1 -timeout 120s`
     → 预期全 PASS（T1 此时转绿）。
  5. 静态检查：`go build ./...`、`go vet ./internal/workspace/ ./internal/agentd/`（预期 `EXIT=0`）。
- **加关键节点日志**：`probeBaseAttachments` 未登记（Warn）、探查失败（Warn，带 cause）、探查完成（Info，带
  resolved_base/paths/missing）；`BaseTreeMissingPaths` 不发日志（git 层 `gitProbe`/`ResolveDispatchBase`
  已有 Info/Warn）。成功路径不静默。
- **加注释**：新文件头、导出函数、接线点、探测方法的「为什么」已写全（本机镜像、P1 甲、spec/plan 过滤）。
- **测试范围声明**：`./internal/workspace/`（树读取）与 `./internal/agentd/`（探针与接线）。

### T4 收口：不误伤回归 + 变异复验

**不误伤回归（跑既有，不新增）**：

```text
go test ./internal/ledgerstep/ -count=1 -timeout 300s
go test ./internal/workspace/ -run 'TestResolve|TestPrepare|TestBase|TestLocalBase' -count=1 -timeout 300s
go test ./internal/agentd/ -run 'TestStartCardStep|TestCardStep|TestB396|TestB398' -count=1 -timeout 300s
```

本节点已在基线实跑 `internal/ledgerstep` 全量绿（13.196s）；agentd/workspace 目标子集**本节点未跑**，
实现卡必须自己跑到结果并抄原文进台账（本计划不替它写结论）。

**变异复验（手动，不留代码）**：

1. 把 T2 改动四的 `guardFirstDispatchBase` 调用**临时注释掉** →
   `go test ./internal/agentd/ -run TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase -count=1`
   → **预期重新红**（与 T1 基线红同形）；撤回临改。
2. 把 `internal/workspace/basetree.go` 的在场面判定改成恒返回 `missing=nil` →
   同一条 T1 用例应重新红（证明 workspace 判据承重）；撤回临改。
3. 确认工作树只剩正式改动。

- **测试范围声明**：`./internal/ledgerstep/ ./internal/workspace/ ./internal/agentd/`。

---

## 7. 缺陷族对抗审查（逐族设问）

**族 1 生命周期/状态机中断**
- 新增字段是纯函数注入，无 goroutine、锁、文件句柄；探针是同步调用，在 Transport 之前返回。
- 护栏只在「首派」介入（`!hasWorkBranch`），后续轮次走原路径；拒绝时 `RunOnce` 走既有
  `haltForHuman("派发失败")`，卡落 `needs_human`，不留半状态（`ViaTemplate` 在 Transport 前返回，
  远端无 task、账本无 dispatched 快照）。T1 已断言 `dispatched==false`。
- 冻结点不动：护栏拒绝后用户仍可 `card update --base-branch`（此时无 dispatched 事件），满足 spec §5。

**族 2 静默失败 / 误导报错**
- 缺附件拒发**不静默**：错误文本进 `haltForHuman` 的 comment（T1 断言含分支名+路径+改法）。
- 探针不可用（本机无项目位置）**不静默**：`slog.Warn` + `ViaTemplate` `slog.Warn`，且这是 P1 明示的
  fail-open 取舍，不是无声放行。
- 反例保护：探针真失败（fetch/仓库错）**不是** fail-open，而是拒发并保留 cause（`TestB400ProbeErrorRejects`），
  避免「基线不可确认」被当成「基线正确」。

**族 3 跨平台假设**
- 只调 git（经既有 `gitProbe`）与读 `refs/remotes/origin/HEAD`，无平台分支代码。`git` 在支持平台上
  是既有依赖。**无新增平台假设**。

**族 4 假红 / 假绿测试**
- T1 走真实 `startCardStep → runner.Run → ViaTemplate → 生产探针 → workspace → git`，只替换 Transport
  观测「护栏是否在派发前拦下」，不是只测映射函数。
- 假绿防护：T2 同时含 ①拒发 ②放行 ③后续节点跳过 ④显式基线豁免 与两条边界；删掉护栏会让 T1 red
  （transportCalled=1）；把判据删成恒拒发会让 ②③④/接缝 2 red。
- 变异复验（T4）给「护栏承重」「workspace 判据承重」两处可红证据。

**族 5 门禁绕过**
- 护栏无旁路命令（spec §5「零新命令」）；唯一出口是显式 `base_branch`（设计内的豁免，T2 断言④锁死）。
- `SetCardBaseBranch` 的冻结点不放宽；护栏不写任何账本数据，只读。
- 裸 `card dispatch`（无 `--step`）不经闸是 spec §6 调用方范围外的残余，已在 §2/§7 记录，非静默绕过。

**追加设问一：序列化边界**——本卡不新增数据字段、不改 DTO/tag/wire、不新增命令。唯一跨界是
「拒发错误文本 → comment body（既有 JSON）」，T1 穿过真实事件流断言其内容。
`ProbeBaseAttachments` 是 Go 函数值，不过任何序列化边界。**无新增手写投影点**。

**追加设问二：枚举新值过既有白名单**——无新增枚举。**无风险**。

**追加设问三：承重安全属性有测试锁住**——承重属性是「附件不在默认线树上就拒发、且不触达 Transport」
由 T1 + T2① 锁定；豁免/边界由 T2②③④/接缝 2 锁定；变异复验（T4）给可红证据。

**残余风险（明示，不藏）**：
- 「项目仅登记在远端、且默认线≠工作线」的仓，本机探针查不到项目位置 ⇒ P1 甲放行，护栏不生效。
- 裸 `card dispatch` 不经闸。
- 本机镜像与远端目标机的 `origin/HEAD` 可能不同：护栏按 spec §3「默认线在协调者侧解析」查本机镜像；
  若远端默认线才是错的，本机查不出。以上均为 spec 未覆盖面，实现卡不得自行扩大范围，完成后回写 roadmap。

---

## 8. 上下文预算检查

有界文件集（圈得出）：
`internal/ledgerstep/dispatch.go`、`internal/workspace/basetree.go`（新）、
`internal/agentd/cardstep.go`、`internal/ledgerstep/b400_base_guard_test.go`（新）、
`internal/agentd/b400_first_dispatch_test.go`（新）、`internal/workspace/b400_base_tree_test.go`（新）、
`docs/superpowers/ledgers/2026-09-23-b400-plan-ledger.md`（本节点）。
不越出 `internal/ledgerstep`、`internal/workspace`、`internal/agentd`。**通过**。

## 9. 类型标注 / 边界型子系统

非 wire 边界型子系统（单进程内的决策 + 只读 git）。但护栏依赖真仓库与默认线，行为验收仍以显式
真机清单给出（§11，本 task 由协调者执行，不派发）。

## 10. 接缝覆盖（双向，对照 spec §6 接缝清单）

spec 的接缝 = ① 首个节点的派发前置校验（`internal/ledgerstep/dispatch.go` 派发决议段）；② 无附件卡的边界。

- **测试 → 缝**：
  - T1 `TestB400FirstDispatchRejectsWhenSpecMissingOnDefaultBase` 入口 =
    `ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/step", ...)`，经
    `startCardStep → runner.Run → ViaTemplate`，落在缝①；
  - T2 六支测试入口 = `Dispatcher.ViaTemplate`（缝①本体，spec 明写「现状入口：dispatch.go 的派发决议段」），
    不直调 `guardFirstDispatchBase`；
  - T2 `TestB400NoAttachmentsPasses` 落缝②。
- **缝 → 测试**：
  - 缝①被 T1 的端到端断言 + T2 断言①~④、探针两异常锁住（拒发/放行/跳过/豁免/错误处置）；
  - 缝②被 `TestB400NoAttachmentsPasses` 锁住。
- **内部锁（附加，不顶替）**：
  - `TestB400ProbeReportsMissingAndPresentPaths` / `TestB400ProbeSkipsWhenProjectNotRegistered` 入口 =
    `Server.probeBaseAttachments`，不在两条缝入口符号上；缝级断言由 T1 给出；
  - `TestB400BaseTreeMissingPaths` 入口 = `workspace.BaseTreeMissingPaths`，同上。
    二者理由同一形状：**从声明缝构造这些边界（真仓库的正/负路径组合、探针未登记分支）需要拉起真
    agentd + 真 git，成本远高于单元锁，且缝级断言已由 T1 覆盖**；它们是附加可观测性，不顶替任何缝级断言。
- **条件退路**：无（变异复验是显式步骤，不改任何测试的入口符号）。

## 11. 真机清单（归协调者执行；本 task 由协调者执行，不派发）

1. 在「默认线（origin/HEAD）不是工作线」的仓上（如本仓：main 落后功能线数百提交）：
   开一张新卡、挂上 spec、不设基线，`handoff card dispatch <卡> --step <首个节点>`：
   **应被拒**，`handoff card wait <卡>` 或 `card show` 的 comment 含解析到的基线分支名、缺失附件路径
   与改法；卡上无 dispatched 快照。
2. 按文案 `handoff card update <卡> --base-branch <工作线>` 设显式基线后重试同一节点：
   **应放行**（202 / dispatched 首态），附件在树上。
3. 对照：附件本来就在默认线上的卡，首派不受影响（零假阳性）。
4. 观察 agentd.log：同一派发能看到「首派基线附件在场校验通过」或「首派基线缺附件，拒发 / 无法探查，跳过」
   三类读数之一。

> 需要跑有 T2+T3 改动的 agentd，且依赖现场仓的分支现状；机内夹具验不了「线上那条默认线真的错」，
> 故单列交协调者。

## 12. 占位符扫描

- 无 TBD / 「加适当的错误处理」/「同 Task N 而略」；T1~T3 的代码块与改动块均完整可抄。
- **例外声明（无）**：不依赖「形态因包而异」的夹具复用——T2 复用 `dispatchTestCard`，T3 复用
  `newOriginAndClone`/`commitOnOrigin`，签名已在 §4 逐字列出并**本节点亲跑确认存在**（git 行为已亲跑）。
- **红基线声明**：T1 是唯一红锚（基线可编译、会红）；T2/T3 的单测引用新增符号，基线不可编译故无红——
  这是「缝签名今天不存在」的必然，按「最薄路径条」由 T1 承担红证据。
- **内部锁声明**：仅 §10 列出的三支附加用例；理由见其注释与 §10，均不顶替缝级断言。
- 条件退路：无。

## 13. 自审三查

1. **spec 覆盖（逐条指到 task）**：
   - §1 根因（默认线解析落在 main、附件不在树上）→ §1 R1/R3 取证；
   - §3 采用方案「首节点派发前附件在场护栏，fail-closed」→ T2 决策段 + T3 树读取；
   - §3 豁免通道（显式 base_branch 跳过）→ T2 `guardFirstDispatchBase` 第 2 步 + `TestB400ExplicitBaseSkipsGuard`；
   - §3 失败动作（文案三样：分支名/缺失路径/可行动作）→ T2 拒发文案 + T1/T2 关键词断言；
   - §3 无附件前提（有门列交列门，护栏只对有附件可查的派发否决）→ T2 第 3 步 + `TestB400NoAttachmentsPasses`；
   - §3 检查读远端 commit 树、SHA 靠远端补拉 → T3 `BaseTreeMissingPaths`（`ResolveDispatchBase` D2）；
   - §4 用户故事 1/2/3 → T1 端到端拒发 + 文案；
   - §5 实现决定（收口首个节点、豁免先于检查、路径在场性、fail-closed 可行动、冻结点不动、零新命令）→ T2/T3；
   - §6 接缝 1 四条断言 → T1 + T2①②③④；接缝 2 → `TestB400NoAttachmentsPasses`；假缝禁令（无未导出无调用方的纯函数占名额）→ §10；
   - §7 Out of Scope（不做无条件显式基线/自动推断/纠正窗口）→ §2 分流与族 5 确认不做；
   - §2 spec 说 ledger 有改动：本计划裁定为零改动并给出理由（§2 分流表末行）——**这是本计划与 spec 字面的唯一偏差，已明示**。
2. **占位符扫描**：见 §12，无占位。
3. **跨 task 类型/签名一致性**：
   - `ProbeBaseAttachments` 字段类型在 T2 定义（`func(ctx context.Context, project, base string, paths []string) (string, []string, error)`），
     T3 的 `s.probeBaseAttachments` 方法签名逐字一致，接线处 `ProbeBaseAttachments: s.probeBaseAttachments`；
   - `ErrBaseProbeUnavailable` 定义于 ledgerstep，T3 的 agentd 方法返回它、`guardFirstDispatchBase` 用
     `errors.Is` 判定；
   - `BaseTreeMissingPaths` 的返回 `(resolvedBase string, missing []string, err error)` 与探针透传一致；
   - T1 只引用既有符号，T2/T3 测试引用新增符号，逐字一致（见 §5 Produces）。

## 图覆盖债（本节点偏差记录）

- 本节点**未使用 codegraph**：执行机（linux-01）未安装，协调者明确指示跳过（工具在协调者本机）。
  按 spec §6 接缝清单直读源码定位，无「未命中符号」可记；不把「未用图」记成图覆盖债。

> 未验证项：T1 的红锚**已验证**（同形原型实跑红，台账 §8）；T2~T4 测试的绿（本节点不写实现，未跑）；
> 真机清单（§11）未跑；agentd/workspace 的目标子集回归本节点未跑（实现卡必须自跑并抄原文）。
