# B305 实现计划：agy 任务级 HOME 权限门

读者：零上下文执行者。工作目录：本分支工作树。spec：`docs/superpowers/specs/b305.md`（先读，含弃选与 OOS）。
法定产出物：`docs/superpowers/plans/b305-plan.md`。
事实台账：`docs/superpowers/specs/b305-ledger.md`（每确立一个事实追加一行，与代码同批提交）。
有效基线：`cards/B304-charter-3` @ `b6078825`。实现只能在当前执行分支完成。

本卡只改 handoff 仓 agy adapter。禁止改 `executor.Adapter` 五动作签名、禁止写用户 `~/.gemini/config/hooks.json`、禁止 argv `--dangerously-skip-permissions`、禁止 `command(*)`、禁止 `always-proceed`、禁止 `--sandbox`、禁止动 `codegraph/baseline.json` / `target.json` / `best.json`。

提交纪律：每个 task 一个 commit，消息 `fix(B305): <task 标题>`；红绿 task 先测试后实现同一提交。台账追加可进同一个 commit。

真机验收（故事 2/3/5）由**协调者**在 linux-01 隔离实例跑，**不派发**。

## 1. 基线与接口

### 1.1 实现前必跑

```bash
go test ./internal/executor/agy -count=1
go build ./...
```

基线应绿（macOS 上 `perm` unix-socket 因 TMPDIR 超 `sun_path` 可能红，那是预存；linux-01 绿。本卡新测试不要依赖长 socket 路径）。

### 1.2 跨 task 签名（逐字，不得改 WriteTaskEnv 参数表）

```go
package agy

const agyHomeDirName = "agyhome"

func WriteTaskEnv(workdir, taskDir, taskID, planContent, sockPath, handoffBin, disciplineBlock string) (hooksPath, promptText string, err error)
func RestoreTaskEnv(taskDir string) error
func managedTaskTmpEnv(taskDir, taskID string) (tmpDir string, env []string)
func agyArgv(req StartProcReq) []string
```

`managedTaskTmpEnv` 今天只回 `TMPDIR`/`GOTMPDIR`/`GOCACHE`。本卡给返回的 `env` **追加** `HOME=<taskDir>/agyhome`。

### 1.3 任务 HOME 落盘布局（WriteTaskEnv 写完必须存在）

```
<taskDir>/agyhome/.gemini/config/hooks.json
<taskDir>/agyhome/.gemini/antigravity-cli/settings.json
<taskDir>/agyhome/.gemini/antigravity-cli/antigravity-oauth-token   # 从用户 home 拷贝；源不存在则 Start 失败
```

`hooks.json` 与 workspace `.agents/hooks.json` 的 `handoff-safety-gate` 字节级同一份 gate（可以 marshal 一次写两处）。

`settings.json` 精确为（key 顺序不限，值必须逐字包含这些 allow 项、禁止多 `command(*)`）：

```json
{
  "permissions": {
    "allow": [
      "command(go)",
      "command(git)",
      "command(echo)",
      "command(make)",
      "command(npm)",
      "command(npx)",
      "command(pnpm)",
      "command(yarn)",
      "command(node)",
      "command(python)",
      "command(python3)",
      "command(pip)",
      "command(pip3)",
      "command(cargo)",
      "command(bash)",
      "command(sh)",
      "command(ls)",
      "command(cat)",
      "command(grep)",
      "command(sed)",
      "command(find)",
      "command(mkdir)",
      "command(chmod)",
      "command(head)",
      "command(tail)",
      "command(rg)",
      "command(gofmt)",
      "command(handoff)"
    ]
  }
}
```

不要写 `toolPermission` 字段（缺省 request-review）。不要写 `command(uname)`。

oauth 源路径：`filepath.Join(userHome, ".gemini", "antigravity-cli", "antigravity-oauth-token")`，`userHome` 用 `os.UserHomeDir()`（agentd 进程的 HOME，不是任务 HOME）。若存在 `antigravity-oauth-token.orig-google-oauth` 一并拷。源 token 不存在：`WriteTaskEnv` 返回 error，文案含源路径。

## 2. Task 1：任务级 HOME + 原生前缀 allow

文件范围：

- 生产：`internal/executor/agy/taskenv.go`、`internal/executor/agy/adapter.go`（`managedTaskTmpEnv`）、`internal/executor/agy/proc.go`（只改注释：权限门改为「任务 HOME 的 hooks.json + settings.allow 前缀」；**argv 不要加新旗**）
- 测试：`internal/executor/agy/taskenv_test.go`、`internal/executor/agy/proc_test.go`、`internal/executor/agy/adapter_test.go`（若已有测 `managedTaskTmpEnv` 则改那里，没有就在 `adapter_test.go` 或 `proc_test.go` 加）
- 文档：`README.md`、`README.zh-CN.md` 的 agy 权限模型段：写明 headless 读的是 `$HOME/.gemini/config/hooks.json`，handoff 用任务级 HOME 注入，不写用户全局；原生 allow 是前缀清单不是 `*`；Stop 仍只 Restore workspace hooks。

测试范围：`go test ./internal/executor/agy -count=1`。

### 2.1 先写失败测试（必须先红）

**T-A WriteTaskEnv 任务 HOME。** 在 `taskenv_test.go` 用现有 git 仓库夹具（照抄 `TestWriteTaskEnvGitExcludeCleanStatus` 的 init）调 `WriteTaskEnv`。

先把「用户 HOME」接到可写临时目录：测试里 `t.Setenv("HOME", fakeUserHome)`，并在 `fakeUserHome/.gemini/antigravity-cli/antigravity-oauth-token` 写任意非空字节（如 `token-b305`）。再调 `WriteTaskEnv`。

断言（每条独立 pass/fail）：

1. `taskDir/agyhome/.gemini/config/hooks.json` 存在，内容含 `handoff-safety-gate` 且含本次 `sockPath`。
2. `workdir/.agents/hooks.json` 仍含同一 `handoff-safety-gate`（B304 行为不丢）。
3. `taskDir/agyhome/.gemini/antigravity-cli/settings.json` 能 `json.Unmarshal` 到带 `permissions.allow` 的结构；`allow` 含 `"command(go)"`；`allow` 每一项都不等于 `"command(*)"`；文件全文不含 `always-proceed`。
4. `taskDir/agyhome/.gemini/antigravity-cli/antigravity-oauth-token` 字节等于 `token-b305`。
5. 测试过程中 `fakeUserHome/.gemini/config/hooks.json` **不存在**（没写用户全局）。

**T-B oauth 缺失失败。** `t.Setenv("HOME", emptyHome)`（没有 token 文件）。`WriteTaskEnv` 返回 error，error 文本含 `antigravity-oauth-token`。

**T-C env HOME。** 调 `managedTaskTmpEnv(taskDir, "t1")`，返回的 `env` 切片必须含 `HOME=`+`filepath.Join(taskDir, "agyhome")`，且仍含 `TMPDIR=` 前缀（旧行为）。

**T-D argv 不加 skip。** `agyArgv(StartProcReq{})` 的切片任何元素都不等于 `--dangerously-skip-permissions`。把这条加进现有 `TestAgyArgv` 的每个 case 的 want **不要**出现该旗即可；另加一断言循环 `for _, a := range got` 拒绝该字符串，防止以后有人加在中间。

写完先跑（T-A/T-B/T-C 应变红，T-D 已绿因为今天本来就没这旗——T-D 是防回归锁，允许先绿）：

```bash
go test ./internal/executor/agy -count=1 -run 'TestWriteTaskEnvAgyHome|TestWriteTaskEnvMissingOAuth|TestManagedTaskTmpEnvHome|TestAgyArgv'
```

测试函数名可更准，语义必须覆盖上面四组。T-A 函数名须含 `AgyHome` 或 `TaskHome` 以便本 plan 的 -run 对得上；若改名，同步改本段命令。

### 2.2 最小实现

在 `WriteTaskEnv` 里、写完 workspace hooks 之后（失败仍走现有 Restore）：

1. `agyHome := filepath.Join(taskDir, agyHomeDirName)`
2. `MkdirAll` config 与 antigravity-cli 两目录 `0700`（与 taskDir 同机密）。
3. 拷贝 oauth（`os.ReadFile`/`os.WriteFile` 即可，不要 symlink）。
4. `json.MarshalIndent` 写出 `settings.json`（allow 用包内 `var nativeCommandAllow = []string{...}` 常量，测试也可读同一变量比对，避免测试与生产各写一份清单）。
5. 把已经 marshal 好的 workspace hooks JSON **同一份**写入 `agyHome/.gemini/config/hooks.json`。

`managedTaskTmpEnv` 追加 `"HOME=" + filepath.Join(taskDir, agyHomeDirName)`。

`proc.go` `agyArgv` 注释改成：权限门是任务 HOME 的 PreToolUse + `settings.allow` 前缀；headless 不读 workspace `.agents/hooks.json`；禁止 skip-permissions。

日志：写 agyhome / 拷贝 token / 写 settings 各一条 Info，失败 Error 带路径。

`RestoreTaskEnv` **不要**删用户全局、不要新增 sidecar 字段。agyhome 留在 taskDir。

### 2.3 跑绿 + 文档

```bash
go test ./internal/executor/agy -count=1
go build ./...
```

README 两语各改 agy「权限模型」段，三句必须出现：

1. headless 读 `$HOME/.gemini/config/hooks.json`；
2. handoff 注入任务级 `HOME=taskDir/agyhome`，不改用户全局；
3. 原生 `permissions.allow` 是命令前缀清单，不是 `command(*)`，也没有 `--dangerously-skip-permissions`。

### 2.4 缺陷族（本 task 验收栏）

- 生命周期：agyhome 在 taskDir，Stop/Reap 不单独卸；agentd 重启冷 Resume 再走 WriteTaskEnv，会覆盖 agyhome。无用户全局孤儿。
- 静默失败：token 缺失必须返回 error，禁止空 HOME 让 agy 去用户目录找 hooks。
- 跨平台：`os.UserHomeDir` + `filepath.Join`；Windows 真机 OOS。
- 假绿：T-A.5 反面断言「没写用户全局」；T-D 反面断言「argv 无 skip」。
- 门禁绕过：禁止 `command(*)` / skip-permissions / always-proceed 写进产物（T-A.3、T-D）。

## 3. Task 2（协调者，不派发）：linux-01 隔离真机

不改代码。隔离实例：独立 datadir、`127.0.0.1:17777`、B305 二进制，禁止碰 pid 生产 `:7777`。

1. 确认用户 `~/.gemini/config/hooks.json` 不存在（或测试前备份并在结束后还原；默认应不存在）。
2. 干净 git 仓库 dispatch `--executor agy`，prompt 只跑 `go env GOVERSION`。
3. `waiting_answer` 工单 permission 含 `go env GOVERSION` → `--approve` → `completed` 且产出含 `go1.`。
4. 再派一次同 prompt → `--deny --reason "b305-deny"` → 命令未执行（render/out 含 `denied by pre-tool hook` 或等价，且无 `go1.` 作为命令输出）。
5. 全过程 `~/.gemini/config/hooks.json` 不出现。Stop 后 workspace porcelain 空（B304）。
6. 把命令+原文追加进 `b305-ledger.md`，`handoff card accept B305 --evidence "…"`。

## 4. 占位符扫描

无 TBD。内部锁：无。T-D 先绿是防回归锁，已声明。
