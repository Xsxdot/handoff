# B304 实现计划：agy hooks 生命周期 + oneshot argv

读者：零上下文执行者。工作目录：本分支工作树。spec：`docs/superpowers/specs/b304.md`（先读，含弃选与 Out of Scope）。
法定产出物：`docs/superpowers/plans/b304-plan.md`。
事实台账：`docs/superpowers/specs/b304-ledger.md`（每确立一个事实追加一行，与代码同批提交）。
有效基线：`feat/agy-executor` / 当前 HEAD `34b7265a`。实现只能在当前执行分支完成。

本卡只改 handoff 仓。禁止改 `executor.Adapter` 五动作签名、禁止改 Reaper 签名、禁止开 `--sandbox`、禁止 exclude `.agents/`、禁止动 `codegraph/baseline.json` / `target.json` / `best.json`。代码图视图 diff 不在本节点。

提交纪律：每个 task 一个 commit，消息 `fix(B304): <task 标题>`；红绿 task 先测试后实现同一提交。台账追加可进同一个 commit。

## 1. 基线、图覆盖与接口

### 1.1 实现前必跑（基线应绿）

```bash
go test ./internal/executor/agy ./internal/executor -count=1
go build ./...
```

基线 `oneshot_test.go` 的 `"agy 带模型"` 金样是 `[]string{"agy", "-p", "--model", "claude-3-5-sonnet", "p"}`——这是本卡要改掉的假绿，Task 2 会先把它改成新期望再让生产代码跟上。

### 1.2 图覆盖债

仓内有 `codegraph/`。本卡新符号 `RestoreTaskEnv` 在 implement 时还不进视图（归图对账列）。调用面以源码为准：

- `WriteTaskEnv`：`internal/executor/agy/taskenv.go`，`Adapter.Start` 调用（`adapter.go` 约 191 行）。
- `Adapter.Stop`：`adapter.go` 约 401 行，关 perm、Kill proc、drop，**不**碰 hooks。
- `rollback` 闭包：`adapter.go` 约 159-168 行，同样不碰 hooks。
- `Reap`：`reap.go` 约 11-24 行，只 Kill，没有 workdir 参数。
- `OneShotArgs`：`internal/executor/oneshot.go` 50-54 行；唯一生产调用方 `internal/agentd/approver.go`（本卡不改 approver）。

### 1.3 跨 task 签名（逐字）

```go
package agy

const restoreFileName = "agy-hooks-restore.json"

type hooksRestoreState struct {
	Workdir        string `json:"workdir"`
	HooksPath      string `json:"hooks_path"`
	CreatedFile    bool   `json:"created_file"`
	OriginalJSON   []byte `json:"original_json,omitempty"`
	SkipWorktree   bool   `json:"skip_worktree"`
	ExcludePattern string `json:"exclude_pattern,omitempty"`
}

func WriteTaskEnv(workdir, taskDir, taskID, planContent, sockPath, handoffBin, disciplineBlock string) (hooksPath, promptText string, err error)
func RestoreTaskEnv(taskDir string) error
```

`WriteTaskEnv` 签名保持现状，不得增减参数。`RestoreTaskEnv` 只读 `taskDir`，workdir 从 sidecar 来。

## 2. Task 1：hooks 安装/卸载成对

文件范围：

- 生产：`internal/executor/agy/taskenv.go`、`internal/executor/agy/adapter.go`、`internal/executor/agy/reap.go`
- 测试：`internal/executor/agy/taskenv_test.go`、`internal/executor/agy/adapter_test.go`、`internal/executor/agy/reap_test.go`（reap_test 若无则新建）
- 文档：`README.md`、`README.zh-CN.md` 的 agy 权限模型那一段（本 task 顺手改口径，避免 Task 2 再碰 adapter 行为描述）
- 可选一行：`codegraph/domains/d_execution_adapters.json` 的 `responsibility` 把「四家」改成含 agy。不动 baseline/target/best。

测试范围：`go test ./internal/executor/agy -count=1`。全量测试不在本 task。

### 2.1 先写失败测试（必须先红）

在 `taskenv_test.go` 增加下面三支。共用一个 git 仓库夹具（照抄现有 `TestWriteTaskEnvGitExcludeCleanStatus` 的 init/config/user/commit，不要另造 helper 包）。

**T-A 已跟踪。** 先 `git add`+`commit` 一份 `{"user-linter":{}}` 的 `.agents/hooks.json`，再 `WriteTaskEnv`，再 `RestoreTaskEnv`。

断言（每条可独立判 pass/fail）：

1. Restore 之后 `os.ReadFile(hooksPath)` 的字节**不含** `perm.sock`，且仍含 `user-linter`。
2. Restore 之后 `git status --porcelain` 为空。
3. Restore 之后再往该文件追加一行 `x`，`git status --porcelain` **非空**（证明 skip-worktree 已清；若仍 skip，这条会假绿成「porcelain 空」——所以必须有这一条反面断言）。
4. Restore 之后 `taskDir/agy-hooks-restore.json` 不存在。

写完先跑：

```bash
go test ./internal/executor/agy -run TestRestoreTrackedHooks -count=1
```

期望红：`RestoreTaskEnv` undefined，或 Restore 之后文件仍含 `perm.sock`。确认失败原因是功能缺失，不是夹具 typo，再写生产代码。

**T-B 新建文件。** 干净 git 仓库，无 `.agents/`。`WriteTaskEnv` 后文件存在且含 `handoff-safety-gate`；`RestoreTaskEnv` 后该路径 `os.IsNotExist`；`git status --porcelain` 为空；`info/exclude` 内容**不含** `.agents/`（作为目录规则）。允许短暂存在 `.agents/hooks.json` 那一行，但 Restore 之后必须撤掉（读 exclude 文件，`strings.Contains` 对 `.agents/hooks.json` 为 false，或该行不在）。

**T-C 收紧现有 exclude 测试。** 改 `TestWriteTaskEnvGitExcludeCleanStatus`：在现有「porcelain 空」之外增加 `exclude 文件不含 ".agents/"`（注意匹配要用目录规则，不要误伤 `.agents/hooks.json` 这一行——断言 `strings.Contains(exclude, ".agents/\n")` 或按行 trim 后等于 `.agents/` 的行数为 0）。这支在删掉 `ensureGitExclude(..., ".agents/")` 之前如果现在就会红，先跑确认；基线这支今天**不会**查 `.agents/`，所以改断言后应立刻红。

**T-D Adapter 入口**（`adapter_test.go` / `reap_test.go`）：

1. `TestStopRestoresHooks`：`WriteTaskEnv(workDir, taskDir, ...)` 后 `ad.newRun("T1", taskDir, workDir)` 再 `ad.Stop("T1")`。断言 workDir 里 hooks 已按 T-A/T-B 规则恢复（测「Stop 调用了 Restore」：用新建文件路径，Stop 后文件不在）。
2. `TestStartRollbackRestoresHooks`：把 `startProc` 换成返回 error 的桩；`Start` 必失败。`Start` 在 `WriteTaskEnv` **之后**才 `startProc`（`adapter.go` 现状）。断言失败后新建的 hooks 文件不在。需要给 `lookAgyPath` / `startProcHost` 桩，避免真拉 agy；若 `Start` 在 `WriteTaskEnv` 之前因其它原因失败，本测试无效——失败必须发生在 WriteTaskEnv 之后。最稳：只替换 `startProc`（已有测试缝 `var startProc = StartProc`，`start_ordering_test.go` 用过 `startProcHost`）。看 `Start`：它调 `startProc(...)` 不是 `startProcHost`。桩 `startProc`。
3. `TestReapRestoresHooks`：`WriteTaskEnv` 后写一份能让 `readProcInfo` 成功的 `proc.json`（照抄 `reap_test.go` 或 claude 同款：`writeProcInfo` + 假 PID）。`Reap` 的 Kill 可能失败（PID 不存在）——**即使 Kill 失败也必须 Restore**。断言 hooks 已恢复。若现有 Reap 在 Kill 失败时直接 return、且你把 Restore 放在 Kill 成功之后，这支会红，这正是要锁的行为：Restore 与 Kill 谁失败都要尝试另一半。

### 2.2 最小实现

`taskenv.go`：

1. 增加 `hooksRestoreState` 与 `restoreFileName`。
2. `WriteTaskEnv` 顺序：
   - `hooksPath := filepath.Join(workdir, ".agents", "hooks.json")`（mkdir 前也可以算路径）。
   - 读原文件：不存在 → `CreatedFile=true`，`OriginalJSON=nil`；存在 → 保存原字节。
   - **先**把 sidecar 写到 `filepath.Join(taskDir, restoreFileName)`（0600），这样后面任何失败都可以 Restore。
   - `MkdirAll` `.agents`，合并 named group（保持现有 merge 行为），`WriteFile` hooks。
   - `git -C workdir ls-files --error-unmatch .agents/hooks.json`：exit 0 视为已跟踪。
   - 已跟踪：`git -C workdir update-index --skip-worktree -- .agents/hooks.json`。失败则 `RestoreTaskEnv(taskDir)` 并 return 该错误。成功则 sidecar.SkipWorktree=true，重写 sidecar。
   - 未跟踪（含新建）：`ensureGitExclude(workdir, ".agents/hooks.json")` **只这一条**。删掉对 `.agents/` 的那次调用。exclude 成功则 sidecar.ExcludePattern=".agents/hooks.json"，重写 sidecar。exclude 失败打 `log.Error`（带 workdir/pattern/cause），**不**把 Start 打成失败（与「exclude 是为了 porcelain，失败时任务仍要跑、Stop 仍能删文件」一致）；但 Restore 仍能删/还原文件。
3. `ensureGitExclude`：`rev-parse`/`OpenFile` 失败从静默 return 改为 `log.Error` 后 return。追加前仍按行去重。
4. `RestoreTaskEnv(taskDir string) error`：
   - sidecar 不存在 → nil。
   - 读 sidecar 失败 → 返回 error。
   - `os.Stat(state.Workdir)` 不存在 → Info 日志「工作区已不在，跳过 hooks 还原」后删 sidecar，返回 nil。
   - `CreatedFile`：`os.Remove(hooksPath)`，`IsNotExist` 忽略。
   - 否则：`os.WriteFile(hooksPath, state.OriginalJSON, 0644)`。
   - `SkipWorktree`：`git -C workdir update-index --no-skip-worktree -- .agents/hooks.json`，失败打 Error 并计入返回 error（否则用户后续改动会被 git 吞掉）。
   - `ExcludePattern != ""`：读 exclude 文件，去掉 trim 后等于该 pattern 的行，写回。文件不在忽略。
   - 最后 `os.Remove(sidecarPath)`。
   - 成功打 Info：taskDir、workdir、createdFile、skipWorktree。
5. 文件头注释补一句边界：Stop/rollback/Reap 必须调 Restore；本文件不启停进程。

`adapter.go`：

- `rollback` 闭包在 drop 之前调用 `_ = RestoreTaskEnv(req.TaskDir)`（此时 TaskDir 已知）。Restore 失败打 Error，不覆盖原来的 Start error。
- `Stop`：在 Kill/perm Close 之后、`a.drop` 之前调用 `RestoreTaskEnv(r.taskDir)`。Restore 错误：Kill 已成功则把 Restore error 返回给调用方；Kill 已失败则打 Error 仍返回 Kill error，但 Restore 必须已经调用。
- `runState` 已有 `taskDir` 字段（`newRun` 写入），Stop 用它。

`reap.go`：

```go
killErr := prochost.Kill(pi.Handle)
restoreErr := RestoreTaskEnv(taskDir)
// 两个都尝试。都失败时 fmt.Errorf("兜底回收任务 %s: kill: %w; restore hooks: %v", taskID, killErr, restoreErr)
// 只 restore 失败：返回 restoreErr
// 只 kill 失败：返回 killErr（现状）
```

缺 sidecar 时 Restore 返回 nil，不改变纯 Kill 路径。

### 2.3 日志与注释

- WriteTaskEnv 入口 Info：task、workdir、hooks、tracked 与否。
- skip-worktree 成功 Info；失败 Error 后 Restore。
- Restore 入口 Info；workdir 缺失 Info；成功 Info；每条 git 失败 Error 带 cause。
- `RestoreTaskEnv` 写函数注释：参数 taskDir、返回、注意事项（幂等、workdir 消失、skip-worktree 必须清）。

### 2.4 README

英/中 agy 段改成：任务结束（Stop/失败回滚/Reap）会从 `.agents/hooks.json` 卸掉 `handoff-safety-gate` 并恢复原文；任务期间已跟踪文件走 skip-worktree，未跟踪只 exclude `.agents/hooks.json` 且结束时撤行。补一句：未挂钩工具（如 MCP/browser）仍走 agy 原生策略。

### 2.5 收尾

```bash
go test ./internal/executor/agy -count=1
go build ./...
```

绿后提交 `fix(B304): restore agy hooks.json on stop/rollback/reap`。

变异（本 task 做一次，打在 Restore 的「写回 OriginalJSON」上）：把 Restore 里写回原文字节改成写回当前文件不动（可编译的语义取反），确认 T-A 变红；再还原。命中必须唯一。

## 3. Task 2：oneshot argv + SKILL

文件范围：`internal/executor/oneshot.go`、`internal/executor/oneshot_test.go`、`skills/handoff/SKILL.md`。

测试范围：`go test ./internal/executor -run TestOneShotArgs -count=1`。

### 3.1 先改测试让它红

`oneshot_test.go` 把

```go
{"agy 带模型", "agy", "claude-3-5-sonnet", "p", []string{"agy", "-p", "--model", "claude-3-5-sonnet", "p"}, false},
```

改成

```go
{"agy 带模型", "agy", "claude-3-5-sonnet", "p", []string{"agy", "--model", "claude-3-5-sonnet", "-p", "p"}, false},
```

无模型那行保持 `[]string{"agy", "-p", "p"}`。

跑 `go test ./internal/executor -run TestOneShotArgs -count=1`，期望红：`got [agy -p --model ...] want [agy --model ... -p ...]`。

### 3.2 最小实现

```go
case "agy":
	// why 参数顺序不能抄 claude：agy 的 -p/--print/--prompt 是取值旗
	// （官方 headless 文档 Flag reference；示例均为 agy -p "prompt" --model ...）。
	// -p 必须紧挨 prompt，其它旗排在 -p 之前，否则 -p 会把 --model 当成 prompt。
	if model != "" {
		return []string{"agy", "--model", model, "-p", prompt}, nil
	}
	return []string{"agy", "-p", prompt}, nil
```

`OneShotArgs` 注释里「prompt 作为末位参数」对 agy 仍然成立（`-p` 的值是末位）。

### 3.3 SKILL

`skills/handoff/SKILL.md` 当前这段：

> 理由是否送达的留痕分执行器：claude 的理由与裁决**同帧送达**……其余 executor（opencode / grok / codex）走带外注入

改成：claude **与 agy** 同帧送达；其余 executor（opencode / grok / codex）走带外注入。

### 3.4 收尾

```bash
go test ./internal/executor -run TestOneShotArgs -count=1
go test ./internal/executor/agy ./internal/executor -count=1
go build ./...
```

提交 `fix(B304): agy OneShotArgs treats -p as a value flag`。

## 4. 缺陷族（本卡）

| 族 | 结论 |
|---|---|
| 生命周期 / 状态机中断 | 用 sidecar 扛 agentd 重启后的 Reap；workdir 被回收则跳过还原。sidecar 先写再改 hooks，降低写到一半崩溃的窗口。 |
| 静默失败 / 误导报错 | exclude 失败从静默改为 Error；skip-worktree 失败 Start 失败并 Restore；Restore 清 skip 失败必须返回 error。 |
| 跨平台假设 | skip-worktree / info/exclude 是 git 行为，Windows 与 POSIX 相同。hook command 的 `strconv.Quote` 本期不改。 |
| 假红 / 假绿测试 | T-A 第三条是反面断言，锁 skip-worktree 已清。oneshot 金样从错误值改成官方词法。 |
| 门禁绕过 | 不扩 matcher。任务期间 skip-worktree 防止把 perm.sock 提交进仓库。 |
| 序列化边界 | sidecar 是内部 JSON；加 `TestRestoreTrackedHooks` 穿过写盘再读盘。字段用可空：`OriginalJSON` 缺省=新建文件。 |
| 枚举新值过白名单 | 无新执行器名。 |

## 5. 占位符扫描

无 TBD。测试夹具「照抄 `TestWriteTaskEnvGitExcludeCleanStatus` 的 git init」已声明：不把完整 init 脚本再抄一份进本计划，断言已逐条列出。内部锁：无。全部新测试入口都在缝 A/B/C。

## 6. 协调者执行（不派发）

本 task 由协调者在实现回合之后跑：`go test ./internal/executor/agy ./internal/executor ./cmd -count=1` 与 `go build ./...`。执行者不要调 handoff CLI、不要 push、不要合 main。
