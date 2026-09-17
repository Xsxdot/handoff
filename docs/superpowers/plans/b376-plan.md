# B376 实现计划

读者：零上下文执行者。级别 L2。spec：`docs/superpowers/specs/b376.md`。
本计划由协调者在本机功能线写出（远程 plan 两轮：错基线 main、reset 后 harness 崩）。

**工作树必须基于 `origin/cards/B233.1-charter-7` @ `59ff4db8`（含 spec）。** 若 HEAD 没有 `docs/superpowers/specs/b376.md` 或没有 `internal/approval/client.go`：

```
git fetch origin cards/B233.1-charter-7
git reset --hard origin/cards/B233.1-charter-7
```

然后再动手。禁止基于 origin/main 改 `internal/agentd/manager.go` 冒充审批链。

## 基线复核（动手前已在 `59ff4db8` 核对）

- `internal/permgate/permgate.go#safeCommandID`：`splitSafeCommand` 遇 `|` `;` 换行即拒；`&&` 只允许 ledger amend 那一对。现表白名单见 `TestJudgeSafeCommandTable`。
- `TestJudgeSafeCommandRejectsMimicsAndConnectors` 把 `echo "go test"`、`go test; cat`、`git status && git log`、`go test | tee` 都当非静默。本卡要改其中三条（echo 静默、`;`/`&&` 全静默段静默），`| tee` 仍非静默。
- `internal/config/config.go#ApproverConfig.Executor` 是 `string`；`if c.Approver.Executor != ""` 才校验 timeout。
- `internal/orchestration/approver.go#Approver` 单 `executorName` + 单 `shot`；`Decide` 调一次 `shot.Invoke`。`cmd/agentd.go#bindApproverOneShot` 按一个名字从 ads 取 OneShot。
- `internal/approval/client.go#Client.Request`：`ShouldConsult==false` 时 `escalate(..., verdict.Reason)`。停用后常见理由「黑名单未命中」。
- `consult` 写一条 `approver_decision`，payload 无 `executor` 字段（`approverDecisionPayload`）。
- `WaitDeliveryPolicy` 不改。

图覆盖债：`codegraph who-calls Judge` 空边。生产调用方：`Client.Request` 经 `Hooks.JudgePermission` → `Manager.judgePermission`。

## 测试范围

- Task 1：`go test ./internal/permgate/ -count=1`
- Task 2：`go test ./internal/config/ ./internal/orchestration/ -count=1`（approver 相关）
- Task 3：`go test ./internal/approval/ ./internal/orchestration/ -count=1`
- 每 task 收尾：`go build ./...`
- 全量测试不在本 plan 任一 task 内（acceptance / 图对账）。

## Task 1：拆段静默 + 白名单补洞

文件：`internal/permgate/permgate.go`、`internal/permgate/permgate_test.go`

Consumes：现有 `Gate.Judge` / `judgeBash` / `safeCommandID` / `splitSafeCommand`（ledger amend 路径保留）。
Produces：`judgeBash` 在黑名单/自指令/越界之后，对仍为 Consult 的命令走「拆段 + 每段可静默」；白名单仍只认单段。

**不要改 `splitSafeCommand` 去吞所有 `&&`。** 另写拆段层。ledger amend 继续走现 `safeCommandID`。

### 拆段规则（实现）

新函数（未导出即可）：

- `splitSilentSegments(cmd string) ([]string, bool)`：引号外按 `&&` / `||` / `;` 切开。未闭合引号、含换行、`` ` ``、`$()`、`${`、`(` / `)` → `ok=false`（整条不静默）。
- `splitPipeStages(seg string) ([]string, bool)`：引号外按 `|` 切开。
- `matchSilentSegment(seg string) (id string, ok bool)`：
  1. 该段含非丢弃写重定向（token 为 `>` / `>>` / `N>` 且目标不是 `/dev/null`，或 `tee` 为管道末级）→ 否。`2>/dev/null`、`2>&1`、`>/dev/null` 不算写。
  2. 无管道：把段按空白切成 fields（沿用 `splitSafeCommand` 的引号规则，但允许该段内部不再含连接符），交给扩展后的单段匹配。
  3. 有管道：每一级都单段可静默，且末级二进制不是 `tee`。

整条 `compoundSilent(cmd) (id string, ok bool)`：所有段 ok 才 ok；id 取「compound」或第一段 id。`judgeBash` 在现 `safeCommandID` 未命中且 `judgeCommand` 为 Consult 时调用；命中 → AutoAllow + `RuleSafeCommand`。

单段匹配在现表上追加（`matchSilentFields(fields []string)`，现表分支可抽到这里，`safeCommandID` 单段路径复用）：

| id | 匹配 |
|---|---|
| `rg` | `fields[0]=="rg"` |
| `codegraph-*` | `fields[0]=="codegraph"`；无子命令 / `--help` / `--version` / 子命令 ∈ `{sym,who-calls,chain,flow,tree,context,entity,domains,check,validate,summary}`。未知子命令 → 否 |
| `git-branch` | `git branch`，且 fields 不含 `-d/-D/-m/-c/-C/--delete/--move/--copy/--force` |
| `git-merge-base` / `git-ls-tree` / `git-rev-list` | 对应 sub |
| `sed-n` | `sed` 且含 `-n` 或 `--quiet`，且不含 `-i` / `--in-place` |
| `npm-test` / `npm-run` | 现有 `npm test\|run` **或** `npm --prefix <dir> test\|run`（`--prefix` 下一 token 是目录） |
| `cd` | `cd` 或 `cd <单路径>`（len 1 或 2） |
| `echo` / `printf` | `fields[0]` 为二者，且无非丢弃写重定向 |

`echo "go test ./..."` 命中 `echo`，Reason 含 `echo`，**不得**含 `go-test`。

### 步骤

1. **先改/加缝级测试（红）**，入口必须是 `g.Judge(Request{Tool:"bash", Command:...}, sc)`：

   扩 `TestJudgeSafeCommandTable`（或新 `TestJudgeCompoundSilentTable`）正例：

   - `grep a && grep b`
   - `cd src && rg foo`
   - `git status && git branch --show-current && git rev-parse HEAD`
   - `ls | head`
   - `sed -n '1,20p' file`
   - `sed -n '219,300p' f; echo "=== cursor"`
   - `codegraph sym Gate.Judge`
   - `npm --prefix web test`
   - `printf '%s\n' hello`
   - 既有单段（go-test / grep / git-status）回归不得红

   反例（新 `TestJudgeCompoundNotSilent`，不得 AutoAllow+safe-command）：

   - `grep a && rm x`
   - `git branch -d topic`
   - `sed -i 's/a/b/' f`
   - `ls | tee out`
   - `echo x > file`
   - `go test ./... | tee /tmp/out`（保留）
   - `bash -c "go test ./..."`（保留）
   - `go run ./...`（保留）
   - `git log -S x --oneline || true`（`true` 不在表内 → 整条 Consult）
   - `codegraph absorb`（未知子命令）
   - `npm --prefix web ci`

   改 `TestJudgeSafeCommandRejectsMimicsAndConnectors`：删掉会变成静默的 `echo "go test ./..."`、`go test ./...; cat file`、`git status && git log`（它们改走正例）。保留 `bash -c`、`| tee`、换行、`go run`、`--output=`。

   加一条：`echo "go test ./..."` → AutoAllow 且 Reason 含 `echo`、不含 `go-test`。

2. 跑 `go test ./internal/permgate/ -count=1` → 正例红（未实现）、反例仍绿。确认红因是缺匹配不是 typo。
3. 最小实现：拆段层 + 扩单段表 + `judgeBash` 接上。禁止把 `a && b` 写进旧 `splitSafeCommand` 白名单表。
4. 日志：`judgeBash` 复合命中时 Debug/Info「复合只读静默」带段数与 id；未知段保持 Consult 不另打 Error。
5. 注释：`splitSilentSegments` 文件头/函数注释写清「只服务静默判定、半段不放行、白名单仍只认单段」。
6. `go test ./internal/permgate/ -count=1` 绿；`go build ./...`。

## Task 2：executor 列表 + Decide failover

文件：

- `internal/config/config.go`、`internal/config/config_test.go`
- `internal/orchestration/approver.go`、`internal/orchestration/approver_test.go`
- `cmd/agentd.go`（`bindApproverOneShot`）
- 所有 `ApproverConfig{Executor: "opencode"}` 字面量改为 `Executor: config.ExecutorList{"opencode"}`（编译红，机械替换）

Consumes：`ApproverConfig.Executor string`、`Approver.Decide`、`bindApproverOneShot`、`executor.OneShot`。
Produces：同键 `approver.executor` 接受标量或序列；`Decide` 按序试候选；干净 approve/escalate 停；全 Err 才返回 Err。一次请求 failover 用尽计 1 次失败（计数仍在 Client/Manager，Decide 只返回一次总 Err）。

### 配置

```go
// ExecutorList 是 approver.executor 的值：YAML 标量或序列都合法。
type ExecutorList []string

func (l *ExecutorList) UnmarshalYAML(value *yaml.Node) error { /* scalar → 单元素；sequence → 原样；其它 → error */ }
func (l ExecutorList) Empty() bool { return len(l) == 0 }
```

`ApproverConfig.Executor` 类型改为 `ExecutorList`。校验：`!Empty()` 时 timeout>0；拒绝空串元素；重复名 error。**不要另起 YAML 键。**

`NewApprover`：`cfg.Executor.Empty()` → `(nil, nil)`。结构体改存 `names []string`，默认 `executorName` 仍等于 `names[0]`（单测/日志兼容）。

### OneShot

`Approver` 增加 `shots map[string]executor.OneShot`。

- 保留 `BindOneShot(shot)`：绑到 `names[0]`（现有单测继续）。
- 新增 `BindOneShotFor(name string, shot executor.OneShot)`。
- `cmd/agentd.go#bindApproverOneShot`：对 `names` 逐个从 ads 取；一个都没有 → 启动失败（与今天单名未注册相同）；部分缺失 → Warn，Decide 时该名当不可用（返回 Err 再试下一个）。

`Decide`：

```
for _, name := range a.names {
  shot := a.shots[name]
  if shot == nil { 记录 attempt error「未绑定 OneShot」; continue }
  env := env.For(name)  // 失败则该候选 Err，试下一个
  req := OneShotReq{Prompt: prompt, Model: a.model, Env: env}
  if name == grok { req.Limits.Effort = EffortLow }
  reply, err := shot.Invoke(...)
  解析 decision
  把 attempt 追加到 d.Attempts（Executor=name, Decision=approve|escalate|error）
  若 err==nil（干净 approve 或干净 escalate）→ 返回该结果（含 Attempts）
}
全部失败 → ApproverDecision{Err: 综合错误, Attempts: ...}
```

`ApproverDecision` 增：

```go
Executor string // 最终生效的候选
Attempts []ApproverAttempt
type ApproverAttempt struct {
  Executor string
  Decision string // approve|escalate|error
  Reason   string
  Err      error
  ElapsedMS int64
}
```

函数签名 `Decide(ctx, permission, taskSummary) ApproverDecision` 不变。

`bindApproval` 把 `Attempts` 拷进 `approval.ConsultDecision`（Task 3 用）。本 task 先让 Decide 填 Attempts，Client 下一 task 才写多条事件。

### 步骤

1. 配置测试（`internal/config/config_test.go`）：标量 `executor: codex` → `[]{"codex"}`；序列 `[codex, grok]`；空=关闭；重复名 Load error。
2. 跑红。
3. 实现 UnmarshalYAML + 校验。机械替换测试字面量。
4. `TestDecideFailoverSecondApproves`：names=`[codex,grok]`；codex shot 返回 err；grok shot 返回 approve JSON。断言 Approve=true、Executor=grok、Attempts 长度 2（error 然后 approve）。
5. `TestDecideFailoverCleanEscalateDoesNotTryNext`：第一候选干净 escalate（Err=nil, Approve=false）→ 不调第二 shot。
6. `TestDecideAllCandidatesError`：两候选都 Invoke err → 总 Err 非 nil，Attempts 长度 2。
7. 跑红 → 实现 Decide 循环 + bind 多 shot。
8. 日志：每个候选 Info「审批者候选开始/结束」带 executor；全部失败 Error 带 tried 列表。`NewApprover` 文档注释写清列表语义。
9. `go test ./internal/config/ ./internal/orchestration/ -count=1`；`go build ./...`。

## Task 3：失败可观测 + 停用理由

文件：

- `internal/approval/client.go`、`internal/approval/client_test.go`
- `internal/orchestration/approval_client.go`（组装钩子）
- `internal/orchestration/manager.go`（`shouldConsultApprover` 旁）

Consumes：`Client.Request` / `consult` / `approverDecisionPayload` / `Hooks.ShouldConsult`。
Produces：每次候选一条 `approver_decision`（含 `executor`）；停用后 Escalate 理由为「审批链已停用（连续 3 次失败）」，不得再是「黑名单未命中」。

`ConsultDecision` 增 `Attempts []ApproverAttempt`（或 approval 包内同构类型）。`consult` 对每次 attempt `AppendEvent` 一条 `approver_decision`，payload：

```go
type approverDecisionPayload struct {
  TicketID   string `json:"ticket_id"`
  Permission string `json:"permission"`
  Decision   string `json:"decision"`
  Reason     string `json:"reason"`
  ElapsedMS  int64  `json:"elapsed_ms"`
  Executor   string `json:"executor,omitempty"`
}
```

无 Attempts 时保持今天一条（Executor 可空）——单候选回归。

`Hooks` 增 `ConsultDisabledReason func(taskID string) string`。`Request` 在不进 consult 而 Escalate 时：

```
reason := verdict.Reason
if c.hooks.ConsultDisabledReason != nil {
  if r := c.hooks.ConsultDisabledReason(c.taskID); r != "" {
    reason = r
  }
}
return c.escalate(ctx, ev, reason)
```

`Manager.consultDisabledReason`：`apDisabled[taskID]` 为真时返回 `审批链已停用（连续 3 次失败）`，否则 `""`。`bindApproval` 绑上。

**不改** `WaitDeliveryPolicy`。`approver_disabled` 事件现有路径保留。

### 步骤

1. `TestClientDisabledEscalatesWithDisabledReason`（`internal/approval/client_test.go`）：Judge 返回 Consult「黑名单未命中」；ShouldConsult=false；ConsultDisabledReason 返回停用句。断言创建的工单/escalate 理由含「审批链已停用」，不含「黑名单未命中」为唯一理由。
2. `TestClientConsultWritesAttemptEvents`：Decide 返回 Attempts 两条（codex error、grok approve）。断言 Store 里两条 `approver_decision`，executor 分别为 codex/grok。
3. 跑红。
4. 最小实现 payload 字段 + 钩子 + consult 循环写事件。
5. 日志：停用升级 Warn「审批链已停用，升级理由改写」带 task；每条 attempt Info 带 executor/decision。
6. `go test ./internal/approval/ ./internal/orchestration/ -count=1`；`go build ./...`。

## 缺陷族

- **生命周期**：Decide 中途 ctx 取消 → 当前候选 Invoke 随 ctx 返回 Err，试下一个仍受同一 ctx（取消则全停，计 1 次失败）。`approver_disabled` 仍按任务维。无新孤儿进程（OneShot 现有取消契约）。
- **静默失败**：停用后禁止「黑名单未命中」掩盖；attempt 事件可观测。复合未知段 Consult 不是静默放行。
- **跨平台**：拆段按字节/rune，不依赖 bash 在 Mac 上执行。YAML 标量/序列与 go-yaml v3。
- **假红/假绿**：正例走 `Gate.Judge`；反例断言「不得 AutoAllow+safe-command」而不是「必须 Escalate」（未知段是 Consult）。`echo "go test"` 反锁 go-test 子串。failover 第二候选测试必须断言第一 shot 被调用且第二也调用。
- **门禁绕过**：拆段只加 AutoAllow 路径，黑名单/自指令/越界仍先于拆段。`bash -c` 包装器仍 Escalate。不剥 `-lc`。

## 序列化边界

新增字段：`approver_decision.executor`（JSON）。断言：`TestClientConsultWritesAttemptEvents` 从 Store 读出的 payload 反序列化后 `Executor` 非空——穿过 `AppendEvent` 真实 JSON，不是内存结构。YAML `approver.executor` 标量/序列 roundtrip 在 config 测试。

无新事件类型、无新枚举过白名单（decision 仍是 approve/escalate/error）。

## 接缝覆盖

| spec 缝 | 锁它的测试 | 入口符号 |
|---|---|---|
| `Gate.Judge` | Task 1 表驱动 | `Gate.Judge` |
| `Approver.Decide` | Task 2 failover 三测 | `Approver.Decide` |
| `Client.Request` | Task 3 disabled 理由 + attempt 事件 | `Client.Request` / `consult` |

无内部锁。无「若意外先绿就改入口」退路。

## 占位符扫描

无 TBD。测试 harness 复用 `newTestGate` / `stubShot` / `newTestApprover`（已有文件）。config YAML 用现有 Load 测试夹具风格。

## spec 故事归属

1. 复合只读零审批 → Task 1
2. 写段不得静默 → Task 1 反例
3. 白名单外只读走审批者 → 不改默认 Consult；Task 2/3 不把未知段改 AutoAllow
4. failover → Task 2
5. 停用可观测 → Task 3

## 本节点不派发

无（plan 由协调者已写完）。implement 派发时若工作树不在 `59ff4db8` 线，先 reset（见文首）。
