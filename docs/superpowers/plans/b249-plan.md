# B249 实现计划：权限判据降噪

**卡号/标题**：B249 / 权限判据降噪：草稿区落点纳入范围 + 法定动作与构建命令白名单
**产出路径**：docs/superpowers/plans/b249-plan.md（机器精确匹配键；不使用日期前缀）
**冻结输入**：docs/superpowers/specs/2026-08-25-b249-permission-noise-reduction.md、docs/superpowers/specs/b249-contract.md、docs/superpowers/specs/2026-08-25-b249-breakdown.md
**当前分支**：cards/B249-charter-3；本卡为 L3 轻档单卡，所有步骤在当前分支执行，不切分支、不 push。
**实现边界**：只改下列任务列出的文件；internal/permgate 不依赖 executor，唯一新增跨域入口是 agentd 调用 executor.TaskTmpDir。B248 是实现开始前的协调者前置核验，B248 黑名单实现不在本计划中。

## 0. 已跑基线与判据冻结

下列命令在实现前已经亲自运行，作为本卡判据基线：

| 命令 | 实跑结果 |
| --- | --- |
| go test ./internal/executor/... -count=1 | 退出 0；executor、claudecode、codex、fake、grok、opencode、rawtap、turn 均输出 ok |
| go test ./internal/permgate -count=1 | 退出 0；原始输出 ok github.com/Xsxdot/handoff/internal/permgate 0.006s |
| go build ./... | 退出 0，原始输出为空 |
| go vet ./internal/permgate ./internal/agentd ./internal/client ./internal/proto ./internal/store ./internal/ledgermirror ./internal/executor/... | 退出 0，原始输出为空 |
| go test ./internal/agentd ./internal/client ./internal/proto ./internal/store ./internal/ledgermirror ./internal/executor/... -count=1 | 退出 0；各包均输出 ok，agentd 原始耗时 159.754s |
| git diff --check | 退出 0，原始输出为空 |

B248 前置不能在本卡自行假定：已运行 git fetch origin，原始错误为：

```text
error: cannot open '/root/.handoff/repos/handoff/.git/worktrees/a1b0469b/FETCH_HEAD': Read-only file system
```

随后 a95178954 在本地对象库中不存在，git cat-file -t a95178954 原始错误为 fatal: Not a valid object name a95178954。因此本计划把 B248 状态记为“未验证”：实现轮开始的第一个动作由协调者核验/合入；执行者不得修改 internal/permgate/blacklist.go 来替代 B248，也不得把缓存远端 ref 当成已 fetch 的事实。

基线上的已核对事实：TaskTmpDir(dataDir, taskID string) string 已在 internal/executor/tempdir.go，金样本在 internal/executor/tempdir_test.go；Scope 当前只有 Workdir/TaskDir；judgeBash 已按“收集全部落点 → 范围判定 → 命令判定”顺序工作；autoAllowPermission 当前不写事件；EventTypePermissionAutoAllow 尚不存在；client.isDeliverable 未排除它。codegraph 不在 PATH，现状符号核验使用 grep，覆盖债已登记在台账。

每个实现 task 的最小测试范围只跑该 task 触及的包；最终全量/跨包验证只在 Task 5 由协调者执行，不把全量测试分摊给单个 task。

## 1. 跨 task 接口冻结

以下签名必须逐字保持；参数类型不使用别名替代：

```go
// internal/executor/tempdir.go
func TaskTmpDir(dataDir, taskID string) string

// internal/executor/executor.go 中既有 Adapter 接缝（签名不改）
func (a *Adapter) Start(ctx context.Context, req executor.StartReq) error
func (a *Adapter) Resume(req executor.ResumeReq) (executor.ResumeOutcome, error)

// internal/permgate/permgate.go
type Scope struct {
    Workdir   string
    TaskDir   string
    TaskTmpDir string
}
func InScope(path string, scope Scope) (in bool, base string, err error)
func (g *Gate) Judge(req Request, scope Scope) Verdict

// internal/agentd/manager.go
func (m *Manager) judgePermission(taskID string, ev executor.AdapterEvent) permgate.Verdict
func (m *Manager) autoAllowPermission(taskID string, ev executor.AdapterEvent, verdict permgate.Verdict)

// internal/proto/proto.go
const EventTypePermissionAutoAllow EventType = "permission_auto_allow"

// internal/agentd/manager.go：AppendEvent 的可替换调用点只为测试注入，生产默认值必须直通 Store。
type appendEventFunc func(string, proto.EventType, any) (proto.Event, error)
```

Task 1 Consumes：executor.TaskTmpDir、executor.StartReq{TaskDir string, Env []string}、executor.ResumeReq{TaskID string, TaskDir string, Env []string, Cold bool}；Produces：四个 adapter 的启动/冷恢复环境行为，且不改变 Adapter 接口。

Task 2 Consumes：Task 1 产生的任务临时目录契约（仅由 agentd 消费路径查询，不让 permgate import executor）；Produces：Scope.TaskTmpDir、WriteArgTargets 新形态、safeCommandID、handoff graph resolve 只读判定。

Task 3 Consumes：Task 2 的 permgate.Verdict{Action,Rule,Reason} 与 Task 1 的 executor.TaskTmpDir；Produces：permission_auto_allow 事件生产、不可交付过滤、Manager 分流。

Task 4 Consumes：Task 3 产生的精确事件类型和 payload JSON；Produces：真实 Store JSON → client/backlog → ledgermirror 的链路回归。

Task 5 Consumes：Task 1–4 的实现与测试；Produces：协调者执行的变异复验、门禁结果与最终提交。

## 2. Task 0：B248 基线前置（协调者执行，不派发）

### 文件

只读检查：当前分支 ref、用户指定的 B248 分支/提交、internal/permgate/blacklist.go。本 task 不改文件。

### 步骤

1. 在开始 Task 1 前由协调者重试 git fetch origin；若成功，亲自检查 a95178954 是否为当前基线祖先，并检查 execWrapperRx 是否包含契约要求的 \\s-[a-z]*c[a-z]*\\b。若 fetch 再次失败，保留原始错误并停止等待协调者决定，不把未验证状态改写成已合入。
2. 若 B248 已在基线，记录提交号并继续；若未在基线，协调者先按用户指定分支合入，再重新运行 go test ./internal/permgate -count=1。不得在 B249 中临时改 B248 黑名单或把“未验证”当绿灯。
3. 台账追加前置命令和原始输出；本 task 由协调者执行，不派发，原因是它会驱动 handoff 状态/分支前置。

### Interfaces / 判据

Consumes：当前基线与协调者提供的 B248 提交。Produces：一条可复核的 B248 已合入记录，或一条原始失败记录并阻断后续实现。判据：没有真实 fetch/祖先检查结果就不能写“B248 已合入”。

## 3. Task 1：统一四执行器任务临时目录环境

### 精确文件集

生产文件：

- internal/executor/tempdir.go（职责注释、纯查询保持现状）
- internal/executor/claudecode/adapter.go、internal/executor/claudecode/resume.go
- internal/executor/codex/adapter.go、internal/executor/codex/resume.go
- internal/executor/grok/adapter.go、internal/executor/grok/resume.go
- internal/executor/opencode/adapter.go、internal/executor/opencode/resume.go

测试文件：

- internal/executor/tempdir_test.go
- internal/executor/claudecode/resume_test.go、internal/executor/claudecode/start_ordering_test.go
- internal/executor/codex/resume_test.go
- internal/executor/grok/resume_cold_internal_test.go
- internal/executor/opencode/resume_cold_internal_test.go

不得改 StartReq/ResumeReq/Adapter 签名，不得把临时目录放到 worktree、系统共享 /tmp 或新的导出 API。

### Interfaces

Consumes：executor.TaskTmpDir(dataDir, taskID string) string、executor.StartReq{TaskDir string, Env []string}、executor.ResumeReq{TaskID string, TaskDir string, Env []string, Cold bool}。Produces：claudecode、codex、grok、opencode 的 Start 与 Cold Resume 传给真实启动 seam 的环境变量，用户变量先于 handoff 管理变量，且四家同一 taskID 使用同一 TaskTmpDir。

### 基线判据先跑

在编辑上述文件前运行 go test ./internal/executor/... -count=1；预期是 Task 0 已通过后的同一组 executor 包全部 ok。该命令已在当前基线实跑通过；实现后必须再次运行并把新原始输出写台账。

### 2–5 分钟步骤

1. 先在 internal/executor/tempdir.go 补齐文件头职责/边界注释和 TaskTmpDir 导出注释；完整函数固定为：

```go
// TaskTmpDir returns <dataDir>/tmp/<first eight bytes of taskID>.
// It performs no I/O and never creates the directory.
func TaskTmpDir(dataDir, taskID string) string {
    shortID := taskID
    if len(shortID) > 8 {
        shortID = shortID[:8]
    }
    return filepath.Join(dataDir, "tmp", shortID)
}
```

注释必须说明：查询无 I/O；短 ID 原样保留；空 ID 交给 filepath.Join；目录由 adapter 启动/Cold 路径创建。非显然的前 8 字节规则说明 AF_UNIX 长度预算：旧形状 61+51=112，新短目录段 27+51=78，但这项数字只作为注释知识搬家，不改变函数语义。

2. 用既有 Start/Cold Resume 缝逐个列出数据流：req.TaskDir 是 <DataDir>/tasks/<id>，因此用 filepath.Dir(filepath.Dir(req.TaskDir)) 得到 DataDir，再调用 executor.TaskTmpDir；不从 RepoPath/os.TempDir 推导。四家每个进程环境最终追加且只能追加以下三项：

```go
func managedTaskTmpEnv(taskDir, taskID string) (tmpDir string, env []string) {
    dataDir := filepath.Dir(filepath.Dir(taskDir))
    tmpDir = executor.TaskTmpDir(dataDir, taskID)
    env = []string{
        "TMPDIR=" + tmpDir,
        "GOTMPDIR=" + tmpDir,
        "GOCACHE=" + filepath.Join(tmpDir, "gocache"),
    }
    return tmpDir, env
}
```

该私有 helper 在各 adapter 包内按各自现有日志/错误风格实现；若包已有等价 tmpEnvKVs，保留其职责但把结果改成上述精确三项。创建与日志使用以下完整形状：

```go
func ensureTaskTmp(taskID, tmpDir string, log *slog.Logger) error {
    if err := os.MkdirAll(tmpDir, 0o700); err != nil {
        log.Error("创建任务临时目录失败", "task", taskID, "tmp_dir", tmpDir, "cause", err)
        return err
    }
    log.Info("任务临时目录已就绪", "task", taskID, "tmp_dir", tmpDir)
    return nil
}
```

实际包若已有创建函数，按此输入/输出/日志字段接入，不重复创建。Start/Cold Resume 的成功路径要有 Info；MkdirAll 错误要带 task/tmp/cause；禁止 print。用户 req.Env 必须先保留，受 handoff 管理的三项必须在合并结果最后，从而同名用户变量不能覆盖它们。目录只在真正启动新进程的冷路径创建，热重连不得制造新目录。

3. Claude：在 Start 组装传给 StartProc 的 env 前调用 helper；在 resume.go 仅进入 cold 新进程分支后调用 helper，并把三项追加到原 req.Env；热 reattach 不调用。保留 RepoPath 作为 cwd。对 cold 启动前后各加结构化日志，错误路径记录 task/session/tmp。
4. Codex：把已有私有 tmpEnvKVs 的数据源改为 executor.TaskTmpDir（若当前已直通，保留并补职责/边界注释）；Start 与 Cold Resume 均用同一 task ID 和 TaskDir 推导，保证两个入口返回同一 tmpDir。创建失败带 task/session/tmp，成功记录 tmpDir 与 env key 名，不记录 env value。
5. Grok：在 startServe 调用前和 Cold Resume 的新 serve 调用前接入 helper；用户 env 在前、托管三项在后；热重连路径不创建。现有 startServe 测试缝继续作为断言入口。
6. OpenCode：在 startServe 调用前和 Cold Resume 的新 serve 调用前接入同样的 helper；保留 ACP/serve 的 cwd 为 RepoPath，不使用 PWD 作为临时目录。
7. 在每个 touched 新增/改动导出函数或私有非显然 helper 旁补注释：职责、参数、返回、为何必须用户 env 在前/托管 env 在后；入口、外部启动前后、MkdirAll 错误、成功路径都用项目 slog，禁止 print。
8. 复用现有 adapter harness，不增加真实 CLI 依赖。逐条锁定：a) UUID 任务得到 <DataDir>/tmp/<id8>；b) TMPDIR/GOTMPDIR/GOCACHE 都指向同一 tmp；c) 用户同名变量在 req.Env 中不能覆盖托管值；d) env 未泄漏 task/session value 到日志；e) cold 启动收到 env，热重连不创建新目录；f) tmp 不等于 worktree、TaskDir 或共享 /tmp。测试入口必须穿过各包的 StartProc/startServe 或现有 cold start seam，而不是只调用内部拼接 helper。
9. 运行最小范围 go test ./internal/executor/... -count=1；记录原始输出。若失败只记录原始失败，不把失败归因成“环境问题”。
10. 运行 gofmt 只处理 Task 1 的列举 Go 文件，随后运行 git diff --check；结果追加台账。

### Task 1 验收与接缝双向清单

测试 → 缝：

- TestTaskTmpDirGoldenVectors 入口为导出 executor.TaskTmpDir，锁定契约查询缝。
- Claude Start/Cold 测试入口为现有 StartProc 测试缝或 Resume cold seam。
- Codex Start/Cold 入口为 adapter 的启动/恢复缝。
- Grok/OpenCode 测试入口分别穿过现有 startServe seam；不是内部 map 测试。

缝 → 测试：

- TaskTmpDir、四家 Start、四家 Cold Resume、用户 env 覆盖顺序、目录创建成功/失败、日志关键节点均有上述测试或明确断言。
- 真实 CLI、真实 AF_UNIX、真实重启列入 Task 5 真机清单，不在此 task 虚报已验证。

## 4. Task 2：三根范围、落点提取、白名单与 graph

### 精确文件集

生产：

- internal/permgate/permgate.go、internal/permgate/path.go、internal/permgate/writeargs.go、internal/permgate/selfcmd.go

测试：

- internal/permgate/permgate_test.go、internal/permgate/path_test.go、internal/permgate/writeargs_test.go、internal/permgate/selfcmd_test.go

不改 internal/permgate/blacklist.go；B248 只作为 Task 0 已核验的前置。

### Interfaces

Consumes：Scope{Workdir string, TaskDir string, TaskTmpDir string}、Request{Tool string, Text string, Command string, Paths []string, Truncated bool}。Produces：InScope(path string, scope Scope) (in bool, base string, err error)、WriteArgTargets(s string) []string、safeCommandID(s string) (id string, ok bool) 和 graph 只读自指令判定；permgate 不 import executor。

### 基线判据先跑与测试范围

在编辑前运行 go test ./internal/permgate -count=1；预期退出 0，当前基线原始输出为 ok github.com/Xsxdot/handoff/internal/permgate 0.006s。实现后只跑该包测试与 gofmt/git diff --check。

### 2–5 分钟步骤

1. 为 Scope 文件头和类型注释更新职责/边界，精确形状为：

```go
type Scope struct {
    Workdir   string
    TaskDir   string
    TaskTmpDir string
}
```

InScope 保持现有签名、最长已存在前缀和 fail-closed 逻辑，只将 roots 精确改为 []string{scope.Workdir, scope.TaskDir, scope.TaskTmpDir}，空根跳过；返回 base 仍为命中的根。不要加 /tmp。路径归一化失败和命中/拒绝处由现有调用方结构化记录 path/base/reason；纯函数不加全局 logger。

2. 从声明缝先写失败测试，再跑红：TestInScopeUsesTaskTmpAsThirdRoot 从 InScope 和 Gate.Judge 真实入口断言：
   - <DataDir>/tmp/abcd1234/out.txt 为 in=true 且 base 为 TaskTmpDir；
   - /tmp/shared/out.txt、worktree 外、TaskDirX/相似前缀均为 in=false 或由上层 Escalate；
   - 三根各自根目录和子目录均命中；
   - 空 TaskTmpDir 不扩大范围。
   若当前测试 harness 已有 Scope table helper，沿 internal/permgate/path_test.go 现有形状复用；每条断言都写出 want，不使用“适当错误处理”。
3. 最小实现第三根，跑同一测试绿；成功分支返回 base 的日志或调用方 Debug 必须保留。
4. 增补 WriteArgTargets，只新增以下确定形态，并保持不能确定落点时不放行：
   - gofmt -w 后所有非 flag 文件参数；
   - git add 在可选 -- 后的每个 pathspec；
   - git diff --output=<path> 与 git diff --output <path>；
   - 保留 tee/cp/mv/ln/install/dd 及现有重定向识别。
   用现有 shell segment/tokenizer，不做子串提取；引号剥离沿现有规则。完整映射逻辑必须覆盖没有参数、只有 flag、多个 pathspec、-- 后 pathspec、等号形式。
5. 从声明缝写失败测试并跑红：TestWriteArgTargetsB249Shapes 对每个形态逐条断言 exact targets，并对无值 --output、只含 flag、路径为空断言不产生误放行；测试入口为导出 WriteArgTargets，并再由 Gate.Judge 做至少一条越界落点断言。
6. 最小实现落点提取，跑绿；提取失败记录 command/shape/target context，不能把未知形态当成无落点。
7. 新增安全命令主体匹配器：

```go
const RuleSafeCommand = "safe-command"

func safeCommandID(s string) (id string, ok bool) {
    fields, ok := splitSafeCommand(s)
    if !ok || len(fields) == 0 {
        return "", false
    }
    switch {
    case fields[0] == "go" && len(fields) >= 2 && fields[1] == "build":
        return "go-build", true
    case fields[0] == "go" && len(fields) >= 2 && fields[1] == "test":
        return "go-test", true
    case fields[0] == "go" && len(fields) >= 2 && fields[1] == "vet":
        return "go-vet", true
    case fields[0] == "gofmt":
        return "gofmt", true
    case fields[0] == "npm" && len(fields) >= 2 && fields[1] == "test":
        return "npm-test", true
    case fields[0] == "npm" && len(fields) >= 3 && fields[1] == "run" && fields[2] != "":
        return "npm-run", true
    case fields[0] == "make":
        return "make", true
    case fields[0] == "ls":
        return "ls", true
    case fields[0] == "cat":
        return "cat", true
    case fields[0] == "grep":
        return "grep", true
    case fields[0] == "git" && len(fields) >= 2 && fields[1] == "status":
        return "git-status", true
    case fields[0] == "git" && len(fields) >= 2 && fields[1] == "diff":
        return "git-diff", true
    case fields[0] == "git" && len(fields) >= 2 && fields[1] == "log":
        return "git-log", true
    case isLedgerAmend(fields):
        return "git-ledger-amend", true
    default:
        return "", false
    }
}
```

splitSafeCommand 必须逐字符处理 shell 引号并拒绝未许可的 |、;、换行、单个 &、第二个 &&；只允许 git add <pathspec...> && git commit --amend --no-edit 这一条双段形态。它必须返回去引号后的 token；注释说明这是主体 token 化而非 shell 执行器。isLedgerAmend 必须要求左段第一个 token 为 git、第二个为 add、至少一个 pathspec，右段精确含 git commit --amend --no-edit，不接受额外 token/命令。
8. 从声明缝写表驱动失败测试并跑红：TestJudgeSafeCommandTable 经 Gate.Judge（不是直接只测 matcher）逐条覆盖以下 ID：git-ledger-amend、go-build、go-test、go-vet、gofmt、npm-test、npm-run、make、ls、cat、grep、git-status、git-diff、git-log；每个断言 Action == AutoAllow、Rule == RuleSafeCommand、Reason 含稳定 ID。TestJudgeSafeCommandRejectsMimicsAndConnectors 逐条断言 echo "go test ./..."、包装器、|、;、换行、额外 &&、未知 handoff 子命令、非 Bash tool 不进入该 AutoAllow。
9. 最小实现并跑绿。judgeBash 仍先收集三类落点，逐个 InScope；任一越界/归一化错误立即 Escalate。只有全部通过后才跑既有 self-command/blacklist/wrapper 优先级，再调用安全 matcher。命中返回 AutoAllow + RuleSafeCommand + stable ID；未命中保留 Consult/Escalate。禁止把范围内 write/edit 的 AutoAllow 设置成 RuleSafeCommand。
10. 在 selfcmd.go 的只读表加入 graph，并加注释说明 graph resolve --doc 是只读入口、未知 graph 子命令仍 fail-closed；selfCmdMutating 优先。TestSelfCommandGraphResolveReadOnly 入口经 Gate.Judge 断言 resolve 为非 RuleSelfCommand，graph dispatch/未知子命令仍是 RuleSelfCommand。
11. 更新所有导出/非显然函数注释：InScope 三根语义、WriteArgTargets 失败闭锁原因、safeCommandID token 主体/拒绝连接符原因、自指令表的保守默认。关键入口、越界、白名单命中、白名单未命中均结构化记录 command/tool/targets/rule/id；禁 print。
12. 运行 go test ./internal/permgate -count=1、gofmt（Task 2 文件）、git diff --check；每条原始结果写台账。

### Task 2 完整锁缝代码形状

测试至少包含下列完整断言函数（使用现有 package permgate 的构造 harness；不得把入口改成内部 helper）：

```go
func TestInScopeUsesTaskTmpAsThirdRoot(t *testing.T) {
    scope := Scope{
        Workdir: "/work/repo",
        TaskDir: "/data/tasks/task-1",
        TaskTmpDir: "/data/tmp/abcd1234",
    }
    cases := []struct {
        name string
        path string
        wantIn bool
        wantBase string
    }{
        {"worktree", "/work/repo/out.txt", true, "/work/repo"},
        {"task-dir", "/data/tasks/task-1/log.txt", true, "/data/tasks/task-1"},
        {"task-tmp", "/data/tmp/abcd1234/out.txt", true, "/data/tmp/abcd1234"},
        {"shared-tmp", "/tmp/shared/out.txt", false, ""},
        {"outside", "/data/tasks/task-1-sibling/out.txt", false, ""},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            gotIn, gotBase, err := InScope(tc.path, scope)
            if err != nil {
                t.Fatalf("InScope(%q) error: %v", tc.path, err)
            }
            if gotIn != tc.wantIn || gotBase != tc.wantBase {
                t.Fatalf("InScope(%q) = (%v, %q), want (%v, %q)", tc.path, gotIn, gotBase, tc.wantIn, tc.wantBase)
            }
        })
    }
}
```

```go
func TestWriteArgTargetsB249Shapes(t *testing.T) {
    cases := []struct {
        command string
        want []string
    }{
        {"gofmt -w a.go b.go", []string{"a.go", "b.go"}},
        {"git add -- a.go docs/ledger.md", []string{"a.go", "docs/ledger.md"}},
        {"git diff --output=tmp.diff", []string{"tmp.diff"}},
        {"git diff --output tmp.diff", []string{"tmp.diff"}},
    }
    for _, tc := range cases {
        got := WriteArgTargets(tc.command)
        if !reflect.DeepEqual(got, tc.want) {
            t.Errorf("WriteArgTargets(%q) = %#v, want %#v", tc.command, got, tc.want)
        }
    }
    for _, command := range []string{"gofmt -w", "git add --", "git diff --output"} {
        if got := WriteArgTargets(command); len(got) != 0 {
            t.Errorf("WriteArgTargets(%q) = %#v, want no target", command, got)
        }
    }
}
```

```go
func TestJudgeSafeCommandTable(t *testing.T) {
    cases := []struct {
        id string
        command string
    }{
        {"git-ledger-amend", "git add docs/ledger.md && git commit --amend --no-edit"},
        {"go-build", "go build ./..."},
        {"go-test", "go test ./..."},
        {"go-vet", "go vet ./..."},
        {"gofmt", "gofmt -w internal/a.go"},
        {"npm-test", "npm test -- --runInBand"},
        {"npm-run", "npm run lint -- --quiet"},
        {"make", "make test"},
        {"ls", "ls -la"},
        {"cat", "cat docs/spec.md"},
        {"grep", "grep -R pattern docs"},
        {"git-status", "git status --short"},
        {"git-diff", "git diff --stat"},
        {"git-log", "git log -5"},
    }
    for _, tc := range cases {
        t.Run(tc.id, func(t *testing.T) {
            gate, err := New(nil, slog.Default())
            if err != nil {
                t.Fatal(err)
            }
            verdict := gate.Judge(Request{Tool: executor.PermToolBash, Command: tc.command}, Scope{Workdir: ".", TaskDir: "/task", TaskTmpDir: "/tmp/task"})
            if verdict.Action != AutoAllow || verdict.Rule != RuleSafeCommand || !strings.Contains(verdict.Reason, tc.id) {
                t.Fatalf("command %q verdict = %#v, want AutoAllow safe-command %s", tc.command, verdict, tc.id)
            }
        })
    }
}
```

```go
func TestJudgeSafeCommandRejectsMimicsAndConnectors(t *testing.T) {
    gate, err := New(nil, slog.Default())
    if err != nil {
        t.Fatal(err)
    }
    cases := []string{
        "echo \"go test ./...\"",
        "bash -c \"go test ./...\"",
        "go test ./... | tee /tmp/out",
        "go test ./...; cat file",
        "go test ./...\ncat file",
        "git status && git log",
        "handoff graph dispatch --doc x",
        "handoff graph unknown --doc x",
    }
    for _, command := range cases {
        verdict := gate.Judge(Request{Tool: executor.PermToolBash, Command: command}, Scope{Workdir: ".", TaskDir: "/task", TaskTmpDir: "/tmp/task"})
        if verdict.Action == AutoAllow && verdict.Rule == RuleSafeCommand {
            t.Fatalf("unsafe mimic/connector %q was auto-allowed: %#v", command, verdict)
        }
    }
}
```

上面代码块中的 reflect、strings、log/slog、executor imports 必须按现有测试包整理；不得省略编译所需 import。Scope{TaskTmpDir:"/tmp/task"} 只用于命令本体测试，落点越界测试必须另用明确的 /tmp/shared 与任务 tmp 路径，不能让 tmp 根测试掩盖共享目录拒绝。

## 5. Task 3：Manager 白名单审计事件与不可交付过滤

### 精确文件集

生产：

- internal/agentd/manager.go
- internal/proto/proto.go
- internal/client/client.go

测试：

- internal/agentd/permgate_wire_test.go、internal/agentd/manager_test.go、internal/agentd/regression_round2_test.go
- internal/proto/proto_test.go、internal/proto/contract_fixture_test.go
- internal/client/client_internal_test.go、internal/client/follow_test.go、internal/client/backlog_internal_test.go

不得新增 HTTP/WS 端点；不得修改 Store AppendEvent 的落库语义；不得把新事件 Publish 到 Hub。

### Interfaces

Consumes：permgate.Verdict、executor.AdapterEvent、executor.TaskTmpDir、Store.AppendEvent(string, proto.EventType, any) (proto.Event, error)。Produces：permissionAutoAllowPayload、EventTypePermissionAutoAllow、Manager.autoAllowPermission(taskID string, ev executor.AdapterEvent, verdict permgate.Verdict) 和 client.isDeliverable 对新事件的 false 结果。

### 基线判据先跑与范围

编辑前运行 go test ./internal/agentd ./internal/proto ./internal/client -count=1；当前基线已真实退出 0，原始输出为 agentd 134.197s、proto 0.003s、client 9.319s 均 ok。Task 3 实现后只跑这三个包的测试，再跑 Task 3 文件 gofmt/diff check。

### 2–5 分钟步骤

1. 在 proto.go 新增精确常量，注释说明“白名单静默放行的审计事件，不是待交付事件”，不复用 approver_decision/permission_reuse。
2. 在 Manager 增加私有默认调用点：

```go
type appendEventFunc func(string, proto.EventType, any) (proto.Event, error)
```

在现有 `Manager` struct 中新增精确字段 `appendEvent appendEventFunc`；其余字段与布局保持当前源码不变。

NewManager 构造时设置：

```go
m.appendEvent = st.AppendEvent
```

如果已有 Manager literal 测试构造，补 appendEvent: st.AppendEvent 或在进入权限路径前由构造 helper 设置；生产路径绝不能为 nil。测试可替换为返回可控错误的函数，仍只模拟 Store.AppendEvent，不新增第二落库入口。
3. 修改 handlePermission 保存 verdict := m.judgePermission(taskID, ev)，AutoAllow 分支调用 m.autoAllowPermission(taskID, ev, verdict)；Consult/Escalate 既有 ticket/approver 路径不变。judgePermission 组装必须精确为：

```go
scope := permgate.Scope{
    Workdir:   task.Workdir(),
    TaskDir:   filepath.Join(m.cfg.DataDir, "tasks", taskID),
    TaskTmpDir: executor.TaskTmpDir(m.cfg.DataDir, taskID),
}
```

4. 新增 payload 到 manager.go，完整结构为：

```go
type permissionAutoAllowPayload struct {
    PermissionID string   `json:"permission_id"`
    Permission   string   `json:"permission"`
    Tool         string   `json:"tool"`
    Command      string   `json:"command,omitempty"`
    Paths        []string `json:"paths,omitempty"`
    Rule         string   `json:"rule"`
    Reason       string   `json:"reason"`
}
```

Permission 使用现有 permEventText(ev.Text) 有界展示；Tool/Command/Paths 来自 ev.Perm/结构化请求，Rule/Reason 来自传入 verdict；不得重新解析展示文本来生成结构化字段。
5. 把 autoAllowPermission 改为以下完整时序：入口 Info 带 task/perm/action/rule/reason；若 verdict.Rule == permgate.RuleSafeCommand，同步调用 m.appendEvent(taskID, proto.EventTypePermissionAutoAllow, payload)；AppendEvent 失败记录 Error 带 task/perm/rule/cause，但继续 Respond once；白名单事件成功或失败都不创建 ticket、不迁移状态、不 Publish。随后调用 adapter 的 RespondPermission(...,"once","")；响应失败记录 Error 并不累计；响应成功才调用 noteAutoAllowed。结构化代码块：

```go
func (m *Manager) autoAllowPermission(taskID string, ev executor.AdapterEvent, verdict permgate.Verdict) {
    m.log.Info("权限请求自动放行", "task", taskID, "perm", ev.PermissionID,
        "rule", verdict.Rule, "reason", verdict.Reason)
    if verdict.Rule == permgate.RuleSafeCommand {
        payload := permissionAutoAllowPayload{
            PermissionID: ev.PermissionID,
            Permission:   permEventText(ev.Text),
            Tool:         ev.Perm.Tool,
            Command:      ev.Perm.Command,
            Paths:        append([]string(nil), ev.Perm.Paths...),
            Rule:         verdict.Rule,
            Reason:       verdict.Reason,
        }
        if _, err := m.appendEvent(taskID, proto.EventTypePermissionAutoAllow, payload); err != nil {
            m.log.Error("白名单自动放行审计落库失败", "task", taskID,
                "perm", ev.PermissionID, "rule", verdict.Rule, "cause", err)
        }
    }
    ad, err := m.adapterFor(taskID)
    if err != nil {
        m.log.Error("自动放行找不到 executor", "task", taskID,
            "perm", ev.PermissionID, "cause", err)
        return
    }
    actx, acancel := unaryCtx(context.Background())
    defer acancel()
    if err := ad.RespondPermission(actx, taskID, ev.PermissionID, "once", ""); err != nil {
        m.log.Error("自动放行回传失败", "task", taskID,
            "perm", ev.PermissionID, "cause", err)
        return
    }
    m.noteAutoAllowed(taskID)
    m.log.Info("自动放行已回传 executor", "task", taskID, "perm", ev.PermissionID)
}
```

调用方必须保证 ev.Perm != nil 才进入此结构化 payload 分支；如果现有 judgePermission 允许 nil Perm 但 Action 为 Escalate，则不调用该函数。响应上下文必须复用现有 `unaryCtx(context.Background())`，不新增超时常量。
6. 在 client.go 的 isDeliverable switch/集合加入 proto.EventTypePermissionAutoAllow: return false；all=true 路径保持绕过该谓词。关键日志只在现有 client debug/trace logger，不能把审计事件变成交付唤醒。
7. 为新增协议常量补字面量与 JSON roundtrip 测试；为 Manager 补真实 newTestManager/fake adapter 入口的测试：发送范围内 bash: go test ./... > <TaskTmpDir>/out，断言一次 Respond once、恰好一个 permission_auto_allow 事件、payload 的 permission_id/rule/reason/tool/command/paths、无 ticket、状态仍 running、无 Hub Publish；发送范围内 write/edit AutoAllow，断言无 permission_auto_allow；把 appendEvent 替换为错误函数，断言仍 Respond once 且事件数为 0/错误被记录；重复同一 PermissionID 断言既有 replay 幂等，不产生第二事件/第二 Respond。
8. 通过 client isDeliverable/follow/backlog 入口断言新事件 all=false 不交付、all=true 可见；既有 permission_request/completed 的交付语义不变。
9. 新函数/字段注释写清职责、参数、返回、为何事件不 Publish；每个错误分支带结构化上下文，成功路径 Info，禁 print。
10. 运行最小范围测试命令、gofmt、git diff check，逐条台账记录。

### Task 3 接缝测试代码形状

```go
func TestSafeCommandPermissionAuditsOnceWithoutTicket(t *testing.T) {
    mgr, st, _, adapter := newTestManager(t)
    taskID := "safe-task"
    work := t.TempDir()
    now := time.Now().UTC()
    mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: work, Executor: "fake",
        State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
    tmpDir := executor.TaskTmpDir(mgr.cfg.DataDir, taskID)
    ev := executor.AdapterEvent{
        Type: "permission", PermissionID: "safe-1",
        Text: "bash: go test ./... > " + filepath.Join(tmpDir, "out"),
        Perm: &executor.PermRequest{
            Tool: executor.PermToolBash,
            Command: "go test ./... > " + filepath.Join(tmpDir, "out"),
        },
    }
    mgr.handlePermission(context.Background(), taskID, ev)
    if got := adapter.recordedPerms(); len(got) != 1 || got[0] != "safe-1:once" {
        t.Fatalf("responded permissions = %v, want [safe-1:once]", got)
    }
    events := mustEvents(t, st, taskID)
    var audit []proto.Event
    for _, event := range events {
        if event.Type == proto.EventTypePermissionAutoAllow {
            audit = append(audit, event)
        }
    }
    if len(audit) != 1 {
        t.Fatalf("audit count = %d, want 1", len(audit))
    }
    var payload permissionAutoAllowPayload
    if err := json.Unmarshal(audit[0].Payload, &payload); err != nil {
        t.Fatal(err)
    }
    if payload.PermissionID != "safe-1" || payload.Rule != permgate.RuleSafeCommand {
        t.Fatalf("payload identity/rule = %#v", payload)
    }
    if payload.Tool != executor.PermToolBash || payload.Command == "" || len(payload.Paths) != 0 {
        t.Fatalf("payload structure = %#v", payload)
    }
    if _, err := st.GetTicket(taskID + ":safe-1"); !errors.Is(err, store.ErrNotFound) {
        t.Fatalf("ticket lookup error = %v, want store.ErrNotFound", err)
    }
}
```

该函数所需 import 为 context、encoding/json、errors、path/filepath、testing、time，以及现有 agentd 测试包中的 executor、permgate、proto、store；测试 harness 复用 internal/agentd/manager_test.go 的 newTestManager、chanAdapter、recordedPerms、mustCreateTask 和 approver_test.go 的 mustEvents。不得用内部 safeCommandID 直测 Manager 入口替代。

## 6. Task 4：序列化边界与镜像消费回归

### 精确文件集

- internal/store/store_test.go、internal/store/eventhook_test.go
- internal/proto/proto_test.go、internal/proto/contract_fixture_test.go
- internal/client/client_internal_test.go、internal/client/follow_test.go、internal/client/backlog_internal_test.go
- internal/ledgermirror/mirror_test.go

默认不改生产 internal/store/store.go、internal/agentd/eventframes.go、internal/ledgermirror/mirror.go；只有测试证明冻结契约被现状阻断时才停下交协调者，不能自扩生产边界。

### Interfaces

Consumes：Store.AppendEvent(taskID string, typ proto.EventType, payload any) (proto.Event, error) 产生的 event row、EventTypePermissionAutoAllow、客户端 all=false/all=true 过滤入口和既有 mirror hook。Produces：穿过 Store JSON marshal、client/backlog 过滤及 ledgermirror 消费的真实回归断言，不新增生产接口。

### 基线判据先跑与范围

编辑前运行 go test ./internal/store ./internal/proto ./internal/client ./internal/ledgermirror -count=1；当前基线已真实退出 0，原始输出为 store 5.089s、proto 0.003s、client 9.357s、ledgermirror 2.252s 均 ok。实现后只跑上述四包，覆盖真实 SQLite Store、事件 JSON、client/backlog 与镜像 harness。

### 2–5 分钟步骤

1. 盘点并在台账列出每一处手写投影：Manager payload struct、Store.AppendEvent JSON marshal、event frame payload、client isDeliverable/backlog、mirror event switch。新增字段从产生到消费不得靠“生产端测试+消费端测试”两端孤证。
2. 写失败 roundtrip 回归并跑红：使用真实 Store.AppendEvent 写入 permission_auto_allow，读取数据库 event 的 type/payload，JSON decode 到 permissionAutoAllowPayload；分别构造 permission_id 缺失和 permission_id:""，用 map[string]json.RawMessage 断言缺失 key 与存在但空字符串不同；rule:"safe-command"、reason:""/非空分别断言 omitempty 只作用 command/paths，不吞必需字段。
3. 最小实现只在被测试证明需要处补通用透传；不新增事件类型 switch 的默认 Publish，不把 permission_auto_allow 放入 mirrorSkip。追加事件必须经既有 Store.AppendEvent，mirror hook 看到同一 type。
4. 写真实消费链断言并跑绿：all=false 的 follow/backlog 过滤新事件；all=true 返回该事件且 payload 字节可 decode；mirror 收到该事件；既有 deliverable 事件仍可见、既有被过滤事件仍过滤。
5. 用断言把 event type、payload、permission ID 的“缺失 vs 零值”逐项列全；不使用计数代替行为，不把只读 map 测试当序列化边界测试。
6. 加测试文件头职责/边界注释和非显然 JSON 边界说明；错误日志沿既有 harness，不用 print。运行四包测试、gofmt、git diff check 并写台账。

### Task 4 接缝/序列化判据

测试 → 缝：测试入口必须从真实 Store.AppendEvent 或 Store hook 进入 JSON 边界，再经 client/backlog 或 mirror；只调用 json.Marshal(permissionAutoAllowPayload) 的单元测试只能作为附加内部锁，不能顶替链路回归。
缝 → 测试：Manager payload → Store marshal、Store event → client filter、Store event → mirror 三条投影各至少一条断言；缺失/零值各一条断言；all=false/all=true 各一条断言。

## 7. Task 5：协调者最终门禁与收口（不派发）

### Interfaces

Consumes：Task 1–4 的实现文件、测试结果和台账原始记录。Produces：协调者亲自运行的最终门禁结果、变异复验记录，以及当前分支上的 git commit；本 task 由协调者执行，不派发。

### 只读/测试命令

Task 1–4 完成且各自台账已写后，由协调者依次亲自运行以下命令；每个命令的真实退出码和原始输出写入台账：

```text
go test ./internal/executor/... -count=1
go test ./internal/permgate -count=1
go test ./internal/agentd ./internal/proto ./internal/store ./internal/client ./internal/ledgermirror -count=1
go test ./internal/permgate ./internal/agentd ./internal/client ./internal/proto ./internal/store ./internal/ledgermirror ./internal/executor/... -count=1
go build ./...
go vet ./internal/permgate ./internal/agentd ./internal/client ./internal/proto ./internal/store ./internal/ledgermirror ./internal/executor/...
gofmt -l internal/executor/tempdir.go internal/executor/claudecode/adapter.go internal/executor/claudecode/resume.go internal/executor/codex/adapter.go internal/executor/codex/resume.go internal/executor/grok/adapter.go internal/executor/grok/resume.go internal/executor/opencode/adapter.go internal/executor/opencode/resume.go internal/permgate/permgate.go internal/permgate/path.go internal/permgate/writeargs.go internal/permgate/selfcmd.go internal/agentd/manager.go internal/proto/proto.go internal/client/client.go
git diff --check
```

验收只接受真实命令结果；任一失败原样登记，不写“应该通过”。

### 变异复验（行为判据，不钉计数）

逐项做临时变异并运行最小相关测试，确认安全属性被锁住；每次变异后恢复实现并再跑该包测试：

| 变异 | 必须变红的测试/行为 |
| --- | --- |
| 删除 TaskTmpDir 根 | task tmp 写入不再 AutoAllow，Task 2 范围/Manager 测试失败 |
| 把白名单 matcher 放在落点范围判断前 | /tmp/shared 重定向仍被 AutoAllow，Task 2 越界先行测试失败 |
| 删除 gofmt/git add/git diff output 的 WriteArgTargets | 越界对应命令漏出范围，Task 2 落点测试失败 |
| 去掉 verdict.Rule == RuleSafeCommand 守卫 | 范围内 write/edit 产生审计，Task 3 反面测试失败 |
| 将新事件 Publish 或从 isDeliverable 放行 | follow/backlog all=false 唤醒，Task 3/4 过滤测试失败 |
| 让用户 Env 追加在托管三项之后 | 同名变量覆盖 handoff 值，Task 1 环境顺序测试失败 |
| 恢复旧 <DataDir>/tmp/<id8> 的 adapter 私拼路径 | Task 1 TaskTmpDir/同一路径断言失败，或审计 Scope 与执行环境分叉 |

所有变异均只在当前 worktree、只读目标明确且可恢复；不使用 destructive git reset/checkout。若某变异无法安全施加，记录原始失败/未验证，不代替结论。

## 8. 五项计划自审与验收栏

### 8.1 缺陷族对抗审查

| 缺陷族 | 设问与本计划锁法 |
| --- | --- |
| 范围/路径 | Task tmp、worktree、TaskDir 三根逐条命中；共享 /tmp、相似前缀、归一化失败 Escalate；Task 2 InScope 与 Judge 双锁 |
| 命令解析/注入 | token 主体而非子串；引号、wrapper、|;newline;&、额外 && 反例；matcher 先后顺序有 Judge 入口锁 |
| 落点提取 | gofmt/git add/git diff output 四种形态和缺值反例；越界先于白名单 |
| 审计幂等 | Manager replay、AppendEvent failure、Respond once、RuleSafeCommand 守卫；一次且仅一次事件 |
| 事件消费 | Store JSON、event frame、client all=false/all=true、mirror 真实链路；缺失与空值区分 |
| 执行器隔离 | 四家 Start/Cold、用户 env 覆盖、tmp 不在 worktree、热重连不创建；使用已有进程启动缝 |
| 可观测性 | 入口、外部调用前后、错误分支、成功路径均有 slog；不记录 env value，不使用 print |
| 兼容/回归 | 非 Bash、未知 handoff 子命令、既有黑名单/写入 AutoAllow、既有交付事件保持原语义；Task 5 组合测试 |

### 8.2 序列化边界清单

- 产生：internal/agentd/manager.go 的 permissionAutoAllowPayload。
- 持久化：internal/store/store.go#Store.AppendEvent 的 JSON marshal 与 SQLite event row。
- 通用帧：internal/agentd/eventframes.go 的既有 payload/type 投影。
- 消费过滤：internal/client/client.go#isDeliverable、internal/client/backlog.go。
- 镜像：internal/ledgermirror/mirror.go 的既有 event hook/skip 选择。
- 每个边界必须被 Task 4 真实 Store → consumer 测试穿过；permission_id 缺失与零值、command/paths omitempty 与 rule/reason 必需性逐项断言。

### 8.3 上下文预算

Task 1 为 9 个生产文件 + 6 个测试文件，单一执行域；Task 2 为 4 + 4 个 permgate 文件；Task 3 为 3 + 8 个 manager/proto/client 文件；Task 4 为 8 个测试文件且生产文件默认冻结；Task 5 只运行门禁。每个集合可在一轮内读完并有界；没有越界横向重构，不插竖切卡。

### 8.4 边界型真机清单

以下不能由本机 harness 推广，必须标记“未验证”并由协调者另行执行：四家真实 CLI Start/Cold Resume 的实际子进程 env；Linux AF_UNIX 真实 socket 路径；agentd 重启时权限重放/AppendEvent/Respond 窗口；真实 WS 断线重连与 backlog；不同 OS/CLI 版本的 shell/env 形态；并发软链/TOCTOU。计划中的 Go 测试只证明契约形状，不把这些项目写成已通过。

### 8.5 接缝双向覆盖

声明缝：

1. executor.TaskTmpDir：Task 1 的 golden + 四 adapter Start/Cold，Task 3 Manager scope wiring。
2. InScope/Gate.Judge：Task 2 third-root、safe command、write target、graph tests。
3. Manager.handlePermission → Store.AppendEvent：Task 3 safe permission integration。
4. Store.AppendEvent → client/backlog/mirror：Task 4 roundtrip chain。

测试 → 缝：每支测试的入口按上述清单核对，内部 helper/matcher 只能附加，不能替代至少一支真实入口断言。若包内现有 harness 名字因形态不同，按列出的现有文件复用，并在测试函数中保留完整逐条断言。
缝 → 测试：四条声明缝每条至少有对应测试；漏锁则 Task 5 不得 pass。

### 8.6 用户故事归属

- 故事 1（任务 tmp 写草稿/构建不唤醒）：Task 1 环境 + Task 2 third root + Task 3 Manager 分流。
- 故事 2（ledger amend）：Task 2 git-ledger-amend。
- 故事 3（go test 重定向到 tmp）：Task 2 WriteArgTargets/范围顺序 + Task 3 audit。
- 故事 4（共享 tmp/越界仍升级）：Task 2 InScope/Judge 反例。
- 故事 5（graph resolve 只读）：Task 2 selfcmd。
- 故事 6（白名单放行可追溯）：Task 3 event + Task 4 Store/client/mirror。

### 8.7 占位符扫描声明

本计划不含未定事项、跨任务骨架指代或未具体化的错误处理等占位符。测试代码复用既有 harness 的唯一例外已显式声明：Task 1 的各 adapter 启动 seam 与 Task 3 的 agentd Store/adapter fixture 形态由现有测试包决定；计划已指明精确文件、入口符号和每条可判 pass/fail 断言。任何未列文件或未列入口都属于 plan failure，不由实现者自行扩大。

## 9. 收口

实现节点完成 Task 1–4 并在台账逐条记录命令、原始输出、失败尝试与判断后，由协调者执行 Task 5。最终仅提交：

```sh
git add docs/superpowers/plans/b249-plan.md docs/ledgers/2026-08-25-b249-ledger.md
git commit -m "docs(b249): add implementation plan"
```

不 push；提交前检查当前分支仍为 cards/B249-charter-3、工作树只有本卡计划/台账改动。
