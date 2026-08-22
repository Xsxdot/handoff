# codegraph v0.3.0 消费侧升级 ledger

## 执行元数据

- 任务：`f60d3c19-c536-4835-9c3d-6fda9052d2d7`
- 分支起点：`4d908dd9`
- 分支：`feat/codegraph-v030-consume`
- 计划：codegraph 刀 1+2 · T4（handoff 仓消费侧升级）

## 升级前基线

命令：`go run . graph check --repo .`

实测 stdout JSON：

```json
{
 "fails": [],
 "warns": [
  {
   "kind": "dead-assembly",
   "detail": "组装点 \"main.go\" 未命中视图中任何节点文件"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_contract",
   "detail": "d_cli->d_contract 预算内直调 4/4（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_controlplane",
   "detail": "d_cli->d_controlplane 预算内直调 12/12（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_executor",
   "detail": "d_cli->d_executor 预算内直调 7/7（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_host",
   "detail": "d_cli->d_host 预算内直调 6/6（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_ledger",
   "detail": "d_cli->d_ledger 预算内直调 1/1（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_localint",
   "detail": "d_cli->d_localint 预算内直调 35/35（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_release",
   "detail": "d_cli->d_release 预算内直调 8/8（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_remote",
   "detail": "d_cli->d_remote 预算内直调 10/10（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_contract",
   "detail": "d_controlplane->d_contract 预算内直调 19/19（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_executor",
   "detail": "d_controlplane->d_executor 预算内直调 3/3（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_host",
   "detail": "d_controlplane->d_host 预算内直调 16/16（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_ledger",
   "detail": "d_controlplane->d_ledger 预算内直调 3/3（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_localint",
   "detail": "d_controlplane->d_localint 预算内直调 18/18（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_release",
   "detail": "d_controlplane->d_release 预算内直调 14/14（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_remote",
   "detail": "d_controlplane->d_remote 预算内直调 6/6（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_executor",
   "to": "d_host",
   "detail": "d_executor->d_host 预算内直调 9/9（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_ledger",
   "to": "d_contract",
   "detail": "d_ledger->d_contract 预算内直调 2/2（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_localint",
   "to": "d_remote",
   "detail": "d_localint->d_remote 预算内直调 2/2（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_release",
   "to": "d_localint",
   "detail": "d_release->d_localint 预算内直调 1/1（可收窄后调低预算）"
  }
 ],
 "legacyHits": {
  "d_cli->d_contract": 4,
  "d_cli->d_controlplane": 12,
  "d_cli->d_executor": 7,
  "d_cli->d_host": 6,
  "d_cli->d_ledger": 1,
  "d_cli->d_localint": 35,
  "d_cli->d_release": 8,
  "d_cli->d_remote": 10,
  "d_controlplane->d_contract": 19,
  "d_controlplane->d_executor": 3,
  "d_controlplane->d_host": 16,
  "d_controlplane->d_ledger": 3,
  "d_controlplane->d_localint": 18,
  "d_controlplane->d_release": 14,
  "d_controlplane->d_remote": 6,
  "d_executor->d_host": 9,
  "d_ledger->d_contract": 2,
  "d_localint->d_remote": 2,
  "d_release->d_localint": 1
 }
}
```

stderr 同次命令有两条环境日志：

```text
2026/08/22 21:50:48 INFO relay egress configured url=wss://handoff.chanliu.net/relay node=linux-01
2026/08/22 21:50:48 INFO 已配置出网代理 proxy=socks5://127.0.0.1:1080
```

## Task 记录

后续按计划每完成一个 task 追加：spec/质量双裁决、验证命令原文结果、提交范围和 commit。

## 升级后 check（Task 1）

命令：

```text
TMPDIR=/root/.handoff/worktrees/f60d3c19/.tmp GOTMPDIR=/root/.handoff/worktrees/f60d3c19/.tmp GIT_CEILING_DIRECTORIES=/root/.handoff/worktrees/f60d3c19 go run . graph check --repo .
```

实测 stdout JSON：

```json
{
 "fails": [],
 "warns": [
  {
   "kind": "dead-assembly",
   "detail": "组装点 \"main.go\" 未命中视图中任何节点文件"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_contract",
   "detail": "d_cli->d_contract 预算内直调 4/4（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_controlplane",
   "detail": "d_cli->d_controlplane 预算内直调 12/12（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_executor",
   "detail": "d_cli->d_executor 预算内直调 7/7（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_host",
   "detail": "d_cli->d_host 预算内直调 6/6（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_ledger",
   "detail": "d_cli->d_ledger 预算内直调 1/1（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_localint",
   "detail": "d_cli->d_localint 预算内直调 35/35（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_release",
   "detail": "d_cli->d_release 预算内直调 8/8（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_cli",
   "to": "d_remote",
   "detail": "d_cli->d_remote 预算内直调 10/10（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_contract",
   "detail": "d_controlplane->d_contract 预算内直调 19/19（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_executor",
   "detail": "d_controlplane->d_executor 预算内直调 3/3（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_host",
   "detail": "d_controlplane->d_host 预算内直调 16/16（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_ledger",
   "detail": "d_controlplane->d_ledger 预算内直调 3/3（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_localint",
   "detail": "d_controlplane->d_localint 预算内直调 18/18（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_release",
   "detail": "d_controlplane->d_release 预算内直调 14/14（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_controlplane",
   "to": "d_remote",
   "detail": "d_controlplane->d_remote 预算内直调 6/6（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_executor",
   "to": "d_host",
   "detail": "d_executor->d_host 预算内直调 9/9（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_ledger",
   "to": "d_contract",
   "detail": "d_ledger->d_contract 预算内直调 2/2（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_localint",
   "to": "d_remote",
   "detail": "d_localint->d_remote 预算内直调 2/2（可收窄后调低预算）"
  },
  {
   "kind": "legacy",
   "from": "d_release",
   "to": "d_localint",
   "detail": "d_release->d_localint 预算内直调 1/1（可收窄后调低预算）"
  }
 ],
 "legacyHits": {
  "d_cli->d_contract": 4,
  "d_cli->d_controlplane": 12,
  "d_cli->d_executor": 7,
  "d_cli->d_host": 6,
  "d_cli->d_ledger": 1,
  "d_cli->d_localint": 35,
  "d_cli->d_release": 8,
  "d_cli->d_remote": 10,
  "d_controlplane->d_contract": 19,
  "d_controlplane->d_executor": 3,
  "d_controlplane->d_host": 16,
  "d_controlplane->d_ledger": 3,
  "d_controlplane->d_localint": 18,
  "d_controlplane->d_release": 14,
  "d_controlplane->d_remote": 6,
  "d_executor->d_host": 9,
  "d_ledger->d_contract": 2,
  "d_localint->d_remote": 2,
  "d_release->d_localint": 1
 }
}
```

stderr 同次命令实测仍有：

```text
2026/08/22 22:04:24 INFO relay egress configured url=wss://handoff.chanliu.net/relay node=linux-01
2026/08/22 22:04:24 INFO 已配置出网代理 proxy=socks5://127.0.0.1:1080
```

## Task 1：依赖升级与 target 迁移

- 变更：`go.mod` 的 `github.com/Xsxdot/charter/graph` 从 `v0.2.1` 升为 `v0.3.0`，`go.sum` 增加 v0.3.0 校验；`grep -c '^replace' go.mod` 实测为 `0`。
- 迁移命令：`go run . graph migrate --repo .`。
- 迁移 stdout：`{"migrated":true,"from":1,"to":2}`。
- `target.json` 实测：`meta.version=2`，`subsystems` 已替代 `domains`；旧空 `assignments` 经 canonical v0.3.0 的 `omitempty` 序列化省略，`LoadTarget` 读取后为空集合；未手改 JSON。
- `go build ./...`：exit 0，无输出。
- `go vet ./...`：exit 0，无输出。
- `TMPDIR=... GOTMPDIR=... GIT_CEILING_DIRECTORIES=... go test ./... -count=1`：exit 0；所有包均输出 `ok`，含 `github.com/Xsxdot/handoff/cmd`（含 `TestRepoContractGate`）。
- 升级前后 check：均 `fails=[]`、20 条 warns，legacyHits 逐项一致。

### 双裁决

- spec 符合性：通过。依赖与 migrate 在同一待提交范围；target 由 canonical migrate 生成；检查读数和全量构建/静态检查/测试通过。
- 代码质量：通过。无源码逻辑改动；go.mod/go.sum 与迁移产物为最小变更；ledger 固化了前后原始 JSON 和环境偏差。
- 修复轮：无。
- commit 范围：`4d908dd9..f55c9b88`（本 task）。

## Task 2：扫描配方扩展

- 变更：`docs/codegraph-scan-recipe.md` 第 20 行附近改为 `subsystems[].paths`；baseline 顶层表新增 `lifecycle: LifecycleRef[]` 及 `who/model/kind/field` 字段说明；diff 表新增 `lifecycleAdded` 与 `lifecycleDeleted`。
- 变更：新增 creator/writer 产出纪律，明确 creator 必须是返回该 model 类型的真构造点、writer 必须是对状态类字段的真写入，并沿既有反裸名撞库规则要求按类型/字段归属确认，定不出宁缺毋滥。
- 验证：`git diff --check` exit 0；`rg` 实测命中 `subsystems[].paths`、两个 lifecycle diff 字段、creator/writer 规则及“宁缺毋滥”。

### 双裁决

- spec 符合性：通过。计划要求的路径术语、baseline 生命周期段、diff 两个生命周期字段和 creator/writer 纪律均逐项落文档；未新增扫描数据或源码范围。
- 代码质量：通过。字段说明与 charter v0.3.0 `LifecycleRef` 对齐，规则集中在独立章节并补充硬校验提示，表格和现有 schema 结构保持一致。
- 修复轮：无。
- commit 范围：`f55c9b88..1db7c86b`（本 task）。

## Task 3：任务与工作区样板声明

- 变更：新增 `codegraph/domains/d_coordination_task.json`，按 `internal/proto/proto.go#transitTable` 的真实六状态、十二条合法迁移填写 stateMachine；责任、不变式、生命周期分别锚定真实协调任务代码，测试引用为仓内实际测试函数。
- 变更：新增 `codegraph/domains/d_workspace.json`，覆盖 `PrepareWorkspace` 到 `RemoveManagedWorktree` 的生命周期，以及分支/工作树互斥注入防护、脏主仓快照、工作区白名单、任务分支同步四条真实不变式。
- 验证：两个声明分别经 `python3 -m json.tool` 解析；`git diff --check` exit 0。
- 验证命令：`TMPDIR=/root/.handoff/worktrees/f60d3c19/.tmp GOTMPDIR=/root/.handoff/worktrees/f60d3c19/.tmp GIT_CEILING_DIRECTORIES=/root/.handoff/worktrees/f60d3c19 go run . graph validate --repo .`。
- 验证 stdout：`containers=237`、`nodes=3564`、`edges=4522`、`domainDecls=2`、`issues=null`、`edgeIssues=null`；exit 0。该校验实际解析了 lifecycle/stateMachine 锚点与 invariants.testRef，因此锚点和测试引用均通过 v0.3.0 校验。

### 双裁决

- spec 符合性：通过。两个计划指定领域均有声明；`d_coordination_task` 的状态迁移和测试引用来自实读源码，`d_workspace` 覆盖跨子系统职责并仅引用真实锚点；validate 绿且 `domainDecls=2`。
- 代码质量：通过。声明保持最小且可审计，不编造终态后继或测试名；生命周期和状态迁移锚点均由库校验解析。
- 修复轮：无。
- commit 范围：`1db7c86b..c81f0a62`（本 task）。

## Task 4：整分支终审与收尾验收

- 最终构建：`TMPDIR=... GOTMPDIR=... GIT_CEILING_DIRECTORIES=... go build ./...` exit 0，无输出。
- 最终静态检查：`TMPDIR=... GOTMPDIR=... GIT_CEILING_DIRECTORIES=... go vet ./...` exit 0，无输出。
- 最终测试：`TMPDIR=... GOTMPDIR=... GIT_CEILING_DIRECTORIES=... go test ./... -count=1` exit 0；`github.com/Xsxdot/handoff` 与 `github.com/Xsxdot/handoff/cmd` 输出 `ok`，cmd 包含 `TestRepoContractGate`。
- 最终声明校验：`graph validate --repo .` exit 0，stdout 为 `containers=237`、`nodes=3564`、`edges=4522`、`domainDecls=2`、`issues=null`、`edgeIssues=null`。
- 最终契约检查：`go run . graph check --repo .` exit 0，`fails=[]`、`warns` 共 20 条（19 legacy + 1 dead-assembly），legacyHits 与升级前逐项一致。
- 兼容入口：`go run . graph --help` stdout 首行含 `[deprecated：请改用 codegraph 二进制]`；`go run . graph summary --repo .` stdout 仅为图摘要文本，无告警污染。两条命令的 stderr 仅有 relay/proxy 环境日志。
- target 断言：`meta.version=2`、`subsystems` 存在；`LoadTarget` 语义中的 assignments 长度为 0。`grep -c '^replace' go.mod` stdout 为 `0`（grep 因无匹配返回 exit 1）；canonical v0.3.0 的 `Assignments json:"assignments,omitempty"` 使空数组键不落盘，未手改迁移产物。

### 双裁决

- spec 符合性：通过。T1 原子提交、T2 配方、T3 声明和全部卡面机内验收均完成；前后 check JSON 已完整留档，真实机跨版本对账、坏锚变异、Web 控制台和执行机 hook 按计划留给协调者。
- 代码质量：通过。相对分支起点 `4d908dd9` 的完整 diff 仅包含计划范围文件；`git diff 4d908dd9..HEAD --check` exit 0，无发现项，无需修复波。
- 修复轮：无。
- commit 范围：`c81f0a62..HEAD`（本 task 的最终收尾提交）。

## 执行偏差与未验证项

- 计划示例 `go run ./cmd/handoff graph check --repo .` 实测失败，原始 stderr：`stat /root/.handoff/worktrees/f60d3c19/cmd/handoff: directory not found`；本仓实际入口为 `go run .`，据此执行了同一 graph 子命令。
- 初次依赖下载实测报错：`open /root/go/pkg/mod/cache/.../v0.3.0.lock: read-only file system`；改用已获准的 escalated `go get` 完成下载。`TMPDIR=/tmp` 测试实测报错：`mktemp: failed to create file via template ‘/tmp/tmp.XXXXXXXXXX’: Read-only file system`。默认任务临时目录还实测产生 `裁决 socket 路径过长（113/114/115/116 字节，上限 107）`；最终使用仓内 `.tmp` 加 `GIT_CEILING_DIRECTORIES`，全量测试 exit 0。
- 未验证：升级前 v0.2.1 二进制逐条跨版本对账、坏锚变异复验、Web 控制台渲染、执行机 hook；这些是计划明确划给协调者的真机项。
