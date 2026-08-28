# B277 实现计划：Go flows 增量扫描

> 读者：linux-01 Codex，对 handoff 仓零上下文。不要手填几百条 flow JSON。
> 卡 B277 · L2 · 上游 spec `docs/superpowers/specs/2026-08-28-go-flows-scan-spec.md`（已批准 2026-08-28）。
> 台账：`docs/superpowers/ledgers/2026-08-28-b277-plan-ledger.md`（边干边追加）。

## 0. 基线事实（动手前核对）

在仓库根执行并原样记台账：

```
python3 - <<'PY'
import json
g=json.load(open("codegraph/baseline.json"))
print("generator", g["meta"].get("generator"), "commit", g["meta"].get("commit"))
print("nodes", len(g["nodes"]), "edges", len(g["edges"]), "flows", len(g.get("flows") or {}))
print("entry", sum(1 for n in g["nodes"].values() if n.get("kind")=="entry"))
PY
```

预期：`flows` 为 0。若非 0，停下来问协调者，不要覆盖来路不明的段。

`go test ./internal/store/ -count=0` 或任意已有包编译通过即可证明模块可构建。不要本卡开头跑全仓测试。

## 1. 文件集

| 路径 | 动作 |
|---|---|
| `docs/codegraph-scan-recipe.md` | 改承重集合为 C17 文案（Go only，entry 不是默认键） |
| `scripts/codegraph-flows/` | **新建** 抽取器（`main.go` + `extract.go` + `_test.go` + testdata） |
| `codegraph/baseline.json` | **只增改** `flows` 与 `meta.scannedAt`/`meta.generator`/`meta.commit` |
| `docs/superpowers/ledgers/2026-08-28-b277-plan-ledger.md` | 过程台账 |
| `docs/ledgers/2026-08-28-b277-flows-report.md` | 交付说明：集合大小、抽到几条、跳过原因 |

禁动：`codegraph/target.json`、`best.json`、`domains/*`、`internal/**` 业务代码、`web/**`、`cmd/**`。

抽取器挂在本模块下：`go run ./scripts/codegraph-flows --repo .`。不要另起 go.mod。

## 2. T0 配方

把 `docs/codegraph-scan-recipe.md` 「只给承重函数建 flow」那四条替换为：

承重 `flows` 键（C17）= **未折叠跨域入缝 ∪ 这些 flow 中 `iface:true` 的实现方法**。

- 入缝：`edges` 两端叶子领域不同，`to.kind==func`。折叠噪声（容器 kind ∈ {函数组, TypeScript 函数组} 且从 `kind=entry` BFS 复用度 ≥ 10）**不建 flow**。
- `kind=entry` **不是**默认键。通道残留 flow 本卡直接不写。
- 本轮 **只给 `file` 以 `.go` 结尾的符号建 flow**。`.ts`/`.tsx` 一律跳过。
- 禁止把 BFS/`chain` 邻居写成 `steps`。
- 配方文末 python 自检：`if not flows` 仍 fail；**额外** fail：任何 flow 键对应节点 `kind==entry`，或 `file` 不是 `.go`。

## 3. T1 抽取器（先红后绿）

### 3.1 testdata

`scripts/codegraph-flows/testdata/mod/go.mod` 最小 module `example.com/mod`。

`scripts/codegraph-flows/testdata/mod/svc/store.go`：

```go
package svc

type Store interface {
	Put(id string) error
}

type Memory struct{}

func (Memory) Put(id string) error { return nil }

func (s *Server) Run(st Store) error {
	if err := st.Put("x"); err != nil {
		return err
	}
	return s.Save()
}

func (s *Server) Save() error { return nil }

type Server struct{}
```

同目录放一份 **迷你 baseline JSON**（节点：`n_run`/`n_save`/`n_put_iface`/`n_put_mem`，边 `n_run→n_put_iface` 与 `n_run→n_save`，implements `[n_put_mem, n_put_iface]`，两个容器分属 domain `d_a`/`d_b` 以便 `n_run` 能被算成入缝——或测试直接把 id 列表喂给 Extract，不经过入缝发现）。

**推荐测试入口**：`Extract(g, ids []string, repoRoot string)` 显式传 `[]string{"n_run"}`，不在单测里测入缝发现。入缝发现另测纯函数 `SeedGoSeams(g) []string`。

### 3.2 缝上断言（必须先红）

文件 `scripts/codegraph-flows/extract_test.go`：

1. `TestGuardReturnIsBranchChildNotSequentialRoot`：`Extract` 后 `n_run` 的 steps 含 `kind=branch` 且 `cond` 含 `err != nil`；存在 `kind=return` 的 id 出现在该 branch 的 `then` 或 `else`；该 return id **不**出现在「未被 then/else/body 引用的步骤」集合。
2. `TestInterfaceCallSetsIfaceAndToIsInterface`：`n_run` 中有一步 `kind=call`、`to=n_put_iface`、`iface=true`。不得 `to=n_put_mem`。
3. `TestCallToMustExist`：baseline 删掉 `n_save` 节点后重抽，不得出现 `to` 指向缺失 id 的 step（整步省略）。
4. `TestSeedGoSeamsSkipsEntryAndTS`：构造含 entry 节点、`.tsx` func、跨域 Go func 的图；`SeedGoSeams` 只返回那个 Go func。

跑红：`go test ./scripts/codegraph-flows/ -count=1`。确认失败原因是功能缺失不是编译拼写。

### 3.3 最小实现

`SeedGoSeams(g *Graph)`：

- 用 `containers[node.container].domain` 当叶子领域。
- 复用度：从每个 `kind=entry` BFS 沿 `edges` 走到的 func，计数。
- 跨域 `to` 且 `kind=func` 且 `strings.HasSuffix(file, ".go")` 且不是（兜底桶 ∧ 复用≥10）。

`Extract`：对每个 id，用 `go/parser` 解析 `file`，按 `name` 找到函数（`Receiver.Method` 对 `ast.FuncDecl` 的 Recv+Name；无 Recv 则短名）。`go/types` 可加载 testdata module；对真实仓用 `golang.org/x/tools/go/packages` 按需要加载（失败则退回：只在 **现有 edges 中 from==当前 id 的 callee 集合** 里按选择子短名唯一匹配）。

Walk：

- `IfStmt` → branch，`cond` = 源码原文（`token.FileSet` 截取），递归 then/else。若 then 块**只有** `ReturnStmt`（可带空行），then=[return step]，不要把该 return 再放进父级顺序序列。
- `ForStmt`/`RangeStmt` → loop。
- 其它 `ReturnStmt` → return（顺序步）。
- 含 `CallExpr` 的语句 → 解析 callee：图内才发 `call`。若 callee id 出现在某条 `implements[1]`（接口侧），`iface=true`。

`order` 从 1 起按发出顺序。`id` 形如 `s1`。标准库/第三方/解析失败：省略。

日志：slog，入口记 ids 数量，每个符号成功/跳过原因，结束记写出条数。禁止 fmt.Print。

文件头注释：职责=从 Go 函数体抽 FlowStep；边界=不改 nodes/edges、不扫 TS。

绿：同上 `go test ./scripts/codegraph-flows/ -count=1`。

## 4. T2 跑仓并写基线

```
go run ./scripts/codegraph-flows --repo .
```

行为：读 `codegraph/baseline.json`，Seed+实现方法闭包（对已抽 steps 里 `iface:true` 的 `to`，把 `implements` 中接口=to 的实现 id 若为 Go func 则纳入再抽一轮，最多两轮），写回 **同一文件** 的 `flows`。保留其余顶层键字节级语义不变（不要重排无关段除非 json marshal 不可避免；用 json 缩进 2 空格、不 HTML escape）。

`meta.generator=codegraph-flows-b277-go`；`meta.scannedAt` 当天；`meta.commit` = `git rev-parse --short HEAD`。

禁止：重写 nodes/edges；给 entry 写 flow；给非 `.go` 写 flow。

## 5. T3 自检与交付说明

必须亲跑，原始输出进台账：

```
python3 - <<'PY'
import json,sys
g=json.load(open("codegraph/baseline.json"))
flows=g.get("flows") or {}
nodes=g["nodes"]
if not flows:
    sys.exit("FAIL: no flows")
bad=[]
for fid, fl in flows.items():
    n=nodes.get(fid)
    if not n: bad.append("dangling "+fid); continue
    if n.get("kind")=="entry": bad.append("entry key "+fid)
    if not str(n.get("file","")).endswith(".go"): bad.append("non-go "+fid)
    steps=fl.get("steps") or []
    ids={s.get("id") for s in steps}
    refs=set()
    for s in steps:
        for k in ("then","else","body"):
            refs.update(s.get(k) or [])
        if s.get("kind")=="call":
            to=s.get("to")
            if to not in nodes: bad.append("call.to missing "+fid+":"+str(to))
    for s in steps:
        if s.get("kind")=="return" and s.get("id") not in refs:
            # 允许快乐路径上的 sequential return；卫语句必须被引用。
            # 若该 return 的前序是 branch 且 cond 含 err，则必须在 refs 里。
            pass
    for s in steps:
        if s.get("kind")=="branch" and "err" in str(s.get("cond","")):
            kids=list(s.get("then") or [])+list(s.get("else") or [])
            if not any((x in ids) for x in kids):
                bad.append("err branch no kids "+fid)
print("flows", len(flows), "bad", len(bad))
for x in bad[:20]:
    print(" ", x)
if bad:
    sys.exit(1)
PY
```

再跑：

```
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate
go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . flow <任一条 Seed 出的方法短名>
```

后者必须 `degraded=false` 且 `steps` 非空。再对一个 **entry** 名字跑 `flow`：允许锚定到 func 或报错，但产物 JSON 的 subject.kind 不得靠本卡变成 entry 当主语——本卡不改 CLI。

抽查 2 个真实函数：打开源码，核对第一处 `if err != nil { return }` 是否对应 branch+return 子步。原文进行台账。

写 `docs/ledgers/2026-08-28-b277-flows-report.md`：Seed 个数、写出 flows 个数、跳过（解析失败/无图内 call）列表摘要、抽查结果。

## 6. 缺陷族（验收栏保留）

| 族 | 结论 |
|---|---|
| 静默失败 | 解析失败 slog 警告并跳过该符号，不写假 steps；空 flows 自检 fail |
| 假绿 | testdata 卫语句引用、iface to、缺失 to 省略，三条必须能红 |
| 序列化 | 真实 baseline 读回脚本区分缺失 to 与写了 to |
| 跨平台 | 只用 go/parser；不调 shell 平台 API |
| 门禁 | 不新增写业务代码的入口 |
| 枚举 | kind 仅 call/branch/loop/return |
| 生命周期 | 抽取器一次性进程，无常驻 |
| 承重安全 | 无 |

## 7. 真机（acceptance，本节点不跑）

协调者在有 flows 的分支上：`codegraph flow` 一条入缝；浏览器打开对应方法图见菱形或至少非 degraded。TS 入缝仍 degraded。

## 8. 提交

可分两到三个 commit：配方；抽取器+测试；baseline+报告。不要 push。工作树只含上表文件。
