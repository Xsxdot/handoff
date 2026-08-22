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
- commit 范围：`4d908dd9..HEAD`（本 task）。
