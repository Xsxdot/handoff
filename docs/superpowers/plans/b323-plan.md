# B323 协调者隔离 HOME：供给全套并在拉起 / 续接 / attach 使用同一展开路径

## 0. 交付边界与已核对基线

本计划只实现 `docs/superpowers/specs/b323.md` 已批准的 L2 行为：不新增 HTTP/JSON
字段，不改变 `keysclient.SessionSpec.Env`、PTY env API、`WakeHome` 的空目录一次性
凭据语义，也不把 `RoundResult.Output` 镜像进房间。生产上唯一的不变式是：协调者
Launch、Resume 和 attach command 使用同一份 `HomeDir` 展开后的绝对路径；Launch/
Resume 之前只按白名单补齐配置、规则/skill 和缺失的表内凭据。

输入冻结物：

- 需求与行为：`docs/superpowers/specs/b323.md:11-94`，状态为已批准；
- 独立审查：`docs/superpowers/reviews/b323-spec-review.md:17-21,33-61,143-175`，
  已吸收 I1–I6；
- 台账：`docs/superpowers/specs/b323-ledger.md`，本计划的每个基线事实、判断和命令
  继续追加到该文件；
- 有效分支：`cards/B323-charter`，不切分支、不改 git 配置、不 push。

已在实现前亲自跑过的基线判据：

| 触及范围 | 命令 | 基线原始结果 |
|---|---|---|
| hostapi | `go test ./internal/hostapi -count=1` | `ok   github.com/Xsxdot/handoff/internal/hostapi  0.790s`，退出码 0 |
| keystone | `go test ./internal/keystone -count=1` | `ok   github.com/Xsxdot/handoff/internal/keystone  0.151s`，退出码 0 |
| agentd 协调者现有测试 | `go test ./internal/agentd -run 'Test(Coord|Wake|ResumeTurnRequest)' -count=1` | `ok   github.com/Xsxdot/handoff/internal/agentd  2.347s`，退出码 0 |
| config/toolchain/skill | `go test ./internal/config ./internal/toolchain ./internal/skill -count=1` | 三包均 `ok`，退出码 0；原始耗时分别 `0.101s`、`0.022s`、`0.008s` |
| proto | `go test ./internal/proto -count=1` | `ok   github.com/Xsxdot/handoff/internal/proto 0.003s`，退出码 0 |
| Go 构建 | `go build ./...` | 标准输出为空，退出码 0 |
| web | `npm test -- --run src/api/scheduling.fetch.test.ts src/api/contract.test.ts`（cwd `web`） | 失败原文：`sh: 1: vitest: not found` |
| web 类型 | `npm run typecheck`（cwd `web`） | 失败原文：`sh: 1: tsc: not found` |

web 依赖在本工作树不可用，所以实现后的 web 测试/类型检查只能在工具可用时给出
结果；若仍出现上述原文，记录为「未验证」，不得写成通过。

代码图先查结果：

- `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context agentd`
  原始失败为 `ERROR context domain not found domain=agentd`，错误建议的 best 领域含
  `d_execution_host`、`d_keystone`、`d_gateway`；改查 `context d_execution_host`。
- `sym RunTurn` 命中 `n_hostapi_Host_RunTurn`，签名是
  `func (h *Host) RunTurn(ctx context.Context, req TurnRequest) (TurnReply, error)`，
  文件 `internal/hostapi/hostapi.go:66`；`flow` 只显示 `return runTurn(ctx, req)`
  和已识别的 `coordinatorRunner.Resume` 调用者，Launch 链路没有被图覆盖。
- `keystone.Service.Locate`、`attachLocator`、`coordinatorRunner` 的直接符号查询未
  命中或仅返回近似候选；候选确认了 `attachLocator.Locate` 在
  `internal/agentd/server.go:2581`，`coordinatorRunner.Launch/Resume` 在
  `internal/agentd/server.go:2522/2530`。因此这些文件有图覆盖债，以下源码签名是
  本计划的实际依据，不把图的非命中误判为不存在。

## 1. 接缝、接口和不变量

### 1.1 消费的现有接口（逐字签名）

```go
// internal/hostapi/hostapi.go
func (h *Host) RunTurn(ctx context.Context, req TurnRequest) (TurnReply, error)

// internal/keystone/keystone.go
func (s *Service) Locate(card, workdir string) (keysclient.AttachInfo, error)

// internal/keysclient/keysclient.go
type Runner interface {
    Launch(spec SessionSpec, prompt string) (TurnResult, error)
    Resume(ref SessionRef, prompt string) (TurnResult, error)
}
type TerminalLocator interface {
    Locate(ref SessionRef, workdir string) (AttachInfo, error)
}

// internal/scheduling/scheduling.go
func (s *Service) Carrier(name string) (Carrier, error)
func (s *Service) LaunchAdmit(squadName string) (Binding, error)

// internal/config/config.go
func Save(path string, cfg *Config) error
func Load(path string) (*Config, error)
```

`LaunchAdmit` 只消费于 launch/wake；冷 `Locate` 明确不能调用它。`Carrier` 是只读
登记读取，冷 `Locate` 只复用 `resolveCoordinatorSquad` + 成员顺序/online 过滤 +
`Carrier`，不读写 `sched_running`。

### 1.2 本卡新增的进程内接口（不进 wire）

```go
// internal/hostapi/probe.go
func ExpandHomePath(path string) (string, error)

// internal/keystone/keystone.go
type SessionRefResolver interface {
    ResolveSessionRef(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error)
}

// internal/agentd/coordinator_home.go
type coordinatorHomePreparer func(spec keysclient.SessionSpec) (string, error)
type coordinatorHomeSupplier struct {
    currentConfig  func() *config.Config
    userHomeDir    func() (string, error)
    expandHomeDir  func(string) (string, error)
    credentialPath func(string) (string, bool)
}
func (p coordinatorHomeSupplier) Prepare(spec keysclient.SessionSpec) (string, error)
func (r coordinatorSessionRefResolver) ResolveSessionRef(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error)
```

Produces 的精确语义：`ExpandHomePath` 只做目标机 `~` 前缀展开并返回错误；
`SessionRefResolver` 在 ref 交给 `TerminalLocator` 之前补齐并展开 `HomeDir`；
`coordinatorHomePreparer` 在 runner 调 CLI 前写白名单并返回同一绝对路径。没有新
JSON 字段，`CoordinatorAttachInfo.Command` 仍是既有字符串字段。

### 1.3 供给白名单与禁写集

供给实现选“复制”而不是 symlink：源文件/目录按普通文件和目录递归复制到隔离
HOME，源 symlink 直接报错并阻止本次 CLI；目标 symlink 不跟随，直接报错。这样
隔离 HOME 不依赖主 HOME 的生命周期，也不会通过 symlink 越出白名单。

允许写入的目标只有：

1. `.handoff/config.yaml`：覆盖旧 first-run 配置；使用 `s.conf()` 当前活快照的
   `Token`，不是 `config.DefaultPath()`；`DataDir`、`RepoRoot` 和相对 SQLite
   `Ledger.DSN` 在落盘前按 agentd 当前工作目录转为绝对路径，`postgres://`/
   `postgresql://` DSN 原样保留。其余活配置字段从快照复制，不在目标侧重新生成。
2. `.config/opencode/AGENTS.md` 与 `.config/opencode/skills/`：源存在就覆盖同名
   普通文件/创建目录；不删目标中额外文件。
3. `toolchain.CredRelPathFor(filepath.Base(spec.CLI))` 指出的单个缺失凭据文件；
   例如 opencode 是 `.local/share/opencode/auth.json`。目标已有该路径时保留原文，
   不覆盖；源不存在时记录结构化 warning 并跳过。

禁止 `RemoveAll` 整棵 HOME；禁止写 `.local/share/opencode/` 下除上述缺失凭据以外
的任何条目，尤其不能碰 session db。历史仓库中已有的 `~/` 垃圾不删、不迁移。

## 2. Task 1：hostapi 展开 fail-closed，并把展开结果用于日志

### 文件范围

- `internal/hostapi/probe.go`
- `internal/hostapi/driver.go`
- `internal/hostapi/runturn_test.go`
- `internal/hostapi/probe_test.go`

### Interfaces

- Consumes：`Host.RunTurn(ctx context.Context, req TurnRequest) (TurnReply, error)`；
  `userHomeDir func() (string, error)` 测试替身；现有 `buildArgv`/`cmd.Env`。
- Produces：`ExpandHomePath(path string) (string, error)`；`RunTurn` 在
  `userHomeDir` 失败时返回错误且不启动子进程；`buildEnv` 内部返回
  `(env []string, expandedHome string, err error)`，供 driver 同时设置 env 和安全
  日志字段。

### 步骤

1. 基线已在本计划第 0 节跑过 `go test ./internal/hostapi -count=1`，预期基线是
   `ok`；先在 `internal/hostapi/probe.go:222-235` 把现有 `expandHomePath` 改为
   导出 `ExpandHomePath`，保留空路径错误、`~`/`~/`/`~\\` 前缀规则和
   `filepath.Clean`，更新 `inspectHome`、driver 和测试内的引用。导出函数头写清
   参数、返回值、它使用目标机 `userHomeDir` 而非协调机 HOME 的边界。
2. 在 `internal/hostapi/driver.go` 把 fail-open 辅助函数改成下面的完整契约；不要
   在错误时返回请求原串：

   ```go
   func expandTurnHomeDir(homeDir string) (string, error) {
       if homeDir == "" {
           return "", nil
       }
       return ExpandHomePath(homeDir)
   }

   func buildEnv(req TurnRequest) ([]string, string, error) {
       expandedHome, err := expandTurnHomeDir(req.HomeDir)
       if err != nil {
           return nil, "", err
       }

       override := map[string]bool{}
       for _, kv := range req.Env {
           if k, _, ok := strings.Cut(kv, "="); ok && k != "" {
               override[k] = true
           }
       }
       if expandedHome != "" {
           override["HOME"] = true
       }
       out := make([]string, 0, len(os.Environ())+len(req.Env)+1)
       for _, kv := range os.Environ() {
           k, _, _ := strings.Cut(kv, "=")
           if override[k] {
               continue
           }
           out = append(out, kv)
       }
       for _, kv := range req.Env {
           if expandedHome != "" {
               if k, _, _ := strings.Cut(kv, "="); k == "HOME" {
                   continue
               }
           }
           out = append(out, kv)
       }
       if expandedHome != "" {
           out = append(out, "HOME="+expandedHome)
       }
       return out, expandedHome, nil
   }
   ```

   这段保持 `HomeDir` 赢过 `req.Env` 的现有语义，同时把失败置于 `exec.Cmd` 启动
   之前。只写结构化 `slog`，不打印 env 值或 prompt。
3. 调整 `driveTurn`：在 `exec.Command`/`cmd.Start` 前调用
   `env, expandedHome, err := buildEnv(req)`；失败记录 `cli`、原始 `home_dir`、
   `workdir`、`cause` 后返回原错误；成功把 `cmd.Env = env`，并把现有
   `“协调者回合开始”` 日志的 `home_dir` 改为 `expandedHome`。外部 CLI 查找、启动
   前后、解析错误和成功路径继续各有结构化日志；成功路径不静默。更新 driver
   文件头/非显然注释，说明为什么错误不能再把字面 `~` 交给子进程。
4. 在 `runturn_test.go` 复用已有 `installFakeCLI`、`withArgvCapture`、`readLines`，
   保留现有内部测试但适配新的三返回值；新增下面两条**都从 `Host.RunTurn` 进入**
   的缝级测试：

   ```go
   func TestRunTurnExpandsTildeHomeDirAtSeam(t *testing.T) {
       installFakeCLI(t)
       capture := withArgvCapture(t)
       fakeHome := t.TempDir()
       swapUserHomeDir(t, fakeHome)
       workdir := t.TempDir()

       _, err := New().RunTurn(context.Background(), TurnRequest{
           CLI: "opencode", HomeDir: "~/handoff-home-x", Workdir: workdir, Prompt: "x",
       })
       if err != nil {
           t.Fatalf("RunTurn: %v", err)
       }
       lines := readLines(t, capture)
       want := "env:HOME=" + filepath.Join(fakeHome, "handoff-home-x")
       found := false
       for _, line := range lines {
           if line == "env:HOME=~/handoff-home-x" {
               t.Fatalf("字面 HOME 进入子进程: %v", lines)
           }
           if line == want {
               found = true
           }
       }
       if !found {
           t.Fatalf("缺展开后的 HOME=%q: %v", want, lines)
       }
       entries, err := os.ReadDir(workdir)
       if err != nil {
           t.Fatalf("读 workdir: %v", err)
       }
       for _, entry := range entries {
           if entry.Name() == "~" {
               t.Fatalf("workdir 下出现字面 ~ 目录")
           }
       }
   }

   func TestRunTurnHomeExpansionFailureDoesNotLaunch(t *testing.T) {
       installFakeCLI(t)
       capture := withArgvCapture(t)
       previous := userHomeDir
       userHomeDir = func() (string, error) { return "", errors.New("home unavailable") }
       t.Cleanup(func() { userHomeDir = previous })
       workdir := t.TempDir()

       _, err := New().RunTurn(context.Background(), TurnRequest{
           CLI: "opencode", HomeDir: "~/handoff-home-x", Workdir: workdir, Prompt: "x",
       })
       if err == nil || !strings.Contains(err.Error(), "展开目标 HOME") ||
           !strings.Contains(err.Error(), "~/handoff-home-x") {
           t.Fatalf("展开失败必须带原串，err=%v", err)
       }
       if raw, readErr := os.ReadFile(capture); readErr == nil && len(raw) != 0 {
           t.Fatalf("展开失败后不应启动 fake CLI，证据=%q", raw)
       } else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
           t.Fatalf("读 fake CLI 证据: %v", readErr)
       }
       entries, readErr := os.ReadDir(workdir)
       if readErr != nil {
           t.Fatalf("读 workdir: %v", readErr)
       }
       for _, entry := range entries {
           if entry.Name() == "~" {
               t.Fatalf("失败路径仍创建了字面 ~ 目录")
           }
       }
   }
   ```

   第二支是 fail-open 变异的红灯：`expandTurnHomeDir` 若恢复为返回原串，fake
   CLI 会留下 `env:HOME=~/handoff-home-x`，测试必须失败。内部 `buildEnv` 测试只能
   作为附加锁，不能替代这两条声明缝测试。
5. 在 `probe_test.go` 的既有 `TestWakeHomeSuppliesMainCredentialBeforeTurn` 中把
   负断言钉到真实规则树路径：`os.Stat(filepath.Join(target, ".config", "opencode", "skills"))`
   必须是 `os.ErrNotExist`。不要调用新供给器，不要修改 `WakeHome` 的“目标为空且
   `main_home_sync` 才拷一个凭据”判定；这条测试正是检测按钮不搬技能树的对照。
6. 最小绿测：`go test ./internal/hostapi -count=1`；预期 `ok`，并核对失败测试的
   fake CLI 证据文件没有写入。该 task 不跑全仓测试。

## 3. Task 2：keystone Locate 在 locator 前补齐 SessionRef HomeDir

### 文件范围

- `internal/keystone/keystone.go`
- `internal/keystone/keystone_spec_test.go`（追加 Locate 缝测试）

### Interfaces

- Consumes：`keysclient.TerminalLocator.Locate(ref keysclient.SessionRef, workdir string) (keysclient.AttachInfo, error)`；账本 `proto.Card` 的既有 seat identity；注入的 `SessionRefResolver`。
- Produces：

  ```go
  type SessionRefResolver interface {
      ResolveSessionRef(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error)
  }
  func (s *Service) SetSessionRefResolver(resolver SessionRefResolver)
  ```

  `Service.Locate(card, workdir string) (keysclient.AttachInfo, error)` 对 hot ref 和
  ledger cold ref 都先走 resolver，再调用 locator；未装配 resolver 时保留现有
  absolute-ref 可用行为，生产 `SetupAutomation` 必须装配它。

### 步骤

1. 基线已跑 `go test ./internal/keystone -count=1`，预期 `ok`。在 `Service` 增加
   `refResolver SessionRefResolver` 字段与 `SetSessionRefResolver`；setter 注释写明
   必须在 HTTP handler 启动前调用、这是进程内端口而非持久化/wire 字段。
2. 只改 `Locate` 的交接顺序，不改 `Wake` 的 rebuild/pointer 逻辑。具体顺序必须是：
   读 hot `sessions`；miss 时从 ledger 解析 `CLI`/`SessionID`/`Workdir`；若
   `refResolver != nil`，调用
   `ResolveSessionRef(card, ref)`；resolver 返回错误时记录 card、是否 hot、是否有
   session/home 等上下文并返回；resolver 成功后记录一次出站 locator 开始日志，
   调 `s.locator.Locate(ref, workdir)`，前后及错误均用 `slog`，不记录 prompt/token。
   locator 只消费 ref，不读取 scheduling。
3. 在 `keystone_spec_test.go` 的同包测试中复用 `specRecorder`、`stubLedgerView`，
   增加一个 `recordingLocator`：它保存收到的 `SessionRef`，并返回
   `AttachInfo{Dir: workdir, Command: "HOME=" + ref.HomeDir + " " + ref.CLI + " --session " + ref.SessionID}`。
   增加 resolver 函数替身，断言入口收到的 cold ref 的 `HomeDir == ""`，再返回
   `/abs/cold-home`。两条测试必须分别存在，不能用“或”：

   ```go
   func TestLocateHotRefResolvesHomeBeforeLocator(t *testing.T) {
       rec := &specRecorder{}
       loc := &recordingLocator{}
       svc := New(rec, nil, stubLedgerView{}, loc)
       svc.SetSessionRefResolver(resolveRefFunc(func(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error) {
           if card != "B1" || ref.HomeDir != "~/hot" {
               t.Fatalf("hot ref 未原样交给 resolver: card=%q ref=%+v", card, ref)
           }
           ref.HomeDir = "/abs/hot"
           return ref, nil
       }))
       if _, err := svc.LaunchForCard(context.Background(), "B1", "coordinate",
           keysclient.SessionSpec{CLI: "opencode", HomeDir: "~/hot", Workdir: "/w"}); err != nil {
           t.Fatalf("seed LaunchForCard: %v", err)
       }
       got, err := svc.Locate("B1", "/w")
       if err != nil {
           t.Fatalf("Locate hot: %v", err)
       }
       if got.Command != "HOME=/abs/hot opencode --session sess-new" || loc.Ref.HomeDir != "/abs/hot" {
           t.Fatalf("hot locator 未消费展开 HomeDir: got=%+v ref=%+v", got, loc.Ref)
       }
   }

   func TestLocateColdRefResolvesRegisteredHomeBeforeLocator(t *testing.T) {
       loc := &recordingLocator{}
       svc := New(&specRecorder{}, nil, stubLedgerView{}, loc)
       svc.SetSessionRefResolver(resolveRefFunc(func(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error) {
           if card != "B1" || ref.HomeDir != "" || ref.SessionID != "sess-new" {
               t.Fatalf("cold ref 形状错误: card=%q ref=%+v", card, ref)
           }
           ref.HomeDir = "/abs/cold"
           return ref, nil
       }))
       got, err := svc.Locate("B1", "/w")
       if err != nil {
           t.Fatalf("Locate cold: %v", err)
       }
       if got.Command != "HOME=/abs/cold opencode --session sess-new" || loc.Ref.HomeDir != "/abs/cold" {
           t.Fatalf("cold locator 未消费登记 HomeDir: got=%+v ref=%+v", got, loc.Ref)
       }
   }
   ```

   测试中 `resolveRefFunc` 和 `recordingLocator` 的完整定义放在同一测试文件：

   ```go
   type resolveRefFunc func(string, keysclient.SessionRef) (keysclient.SessionRef, error)
   func (f resolveRefFunc) ResolveSessionRef(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error) {
       return f(card, ref)
   }
   type recordingLocator struct{ Ref keysclient.SessionRef }
   func (l *recordingLocator) Locate(ref keysclient.SessionRef, workdir string) (keysclient.AttachInfo, error) {
       l.Ref = ref
       return keysclient.AttachInfo{Dir: workdir,
           Command: "HOME=" + ref.HomeDir + " " + ref.CLI + " --session " + ref.SessionID}, nil
   }
   ```

   两支的入口都是 `Service.Locate`，因此不是只测 resolver/helper 的内部锁。
4. 运行最小绿测 `go test ./internal/keystone -count=1`，预期 `ok`。保留既有
   `TestWakeResumeCarriesIsolatedHome`、rebuild pointer 和 output 返回断言，确认本
   task 没有把 `RoundResult.Output` 或非 rebuild pointer 接进房间。

## 4. Task 3：agentd 全套供给、Launch/Resume 统一展开、冷 Locate 读登记载体、attach 生成 HOME

### 文件范围

- 新增 `internal/agentd/coordinator_home.go`
- 新增 `internal/agentd/coordinator_home_test.go`
- `internal/agentd/server.go`
- `internal/agentd/scheddrain.go`
- `internal/agentd/coordapi.go`（仅更新职责注释与必要的状态/attach 日志上下文）
- `internal/agentd/coordapi_test.go`

### Interfaces

- Consumes：

  ```go
  func (s *Server) conf() *config.Config
  func (s *Server) resolveCoordinatorSquad() (scheduling.Squad, error)
  func (s *Server) Scheduling() *scheduling.Service
  func (s *Server) SetupAutomation(st *ledger.Store)
  func (s *Server) SetKeystone(svc *keystone.Service)
  func (r coordinatorRunner) Launch(spec keysclient.SessionSpec, prompt string) (keysclient.TurnResult, error)
  func (r coordinatorRunner) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error)
  func (l attachLocator) Locate(ref keysclient.SessionRef, workdir string) (keysclient.AttachInfo, error)
  ```

- Produces：

  ```go
  func (p coordinatorHomeSupplier) Prepare(spec keysclient.SessionSpec) (string, error)
  func (r coordinatorSessionRefResolver) ResolveSessionRef(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error)
  func normalizeCoordinatorSpec(spec keysclient.SessionSpec) (keysclient.SessionSpec, error)
  ```

  `coordinatorRunner` 的 `prepareHome` 字段类型就是
  `func(keysclient.SessionSpec) (string, error)`；`attachLocator` 只把已有 ref 的
  展开绝对 HomeDir 编进 `AttachInfo.Command`，不读取 scheduling。

### 步骤 A：建立 coordinator_home.go 的供给和 ref 解析

1. 基线已跑 agentd 协调者命令
   `go test ./internal/agentd -run 'Test(Coord|Wake|ResumeTurnRequest)' -count=1`，
   预期 `ok`；config/toolchain/skill 基线也已经是三包 `ok`。新文件头写职责和边界：
   只服务协调者无头 Launch/Resume 与 attach ref，绝不被 `WakeHome` 调用。
2. 写入以下字段和配置投影算法；默认函数由 `SetupAutomation` 注入，测试可替换，
   不在全局另造第二份凭据表：

   ```go
   type coordinatorHomeSupplier struct {
       currentConfig func() *config.Config
       userHomeDir   func() (string, error)
       expandHomeDir func(string) (string, error)
       credentialPath func(string) (string, bool)
   }

   func (p coordinatorHomeSupplier) Prepare(spec keysclient.SessionSpec) (string, error) {
       if strings.TrimSpace(spec.CLI) == "" {
           return "", errors.New("协调者供给缺少 CLI")
       }
       if strings.TrimSpace(spec.HomeDir) == "" {
           return "", errors.New("协调者供给缺少 HomeDir")
       }
       expand := p.expandHomeDir
       if expand == nil {
           expand = hostapi.ExpandHomePath
       }
       targetHome, err := expand(spec.HomeDir)
       if err != nil {
           return "", fmt.Errorf("展开协调者供给 HOME %q: %w", spec.HomeDir, err)
       }
       if !filepath.IsAbs(targetHome) {
           return "", fmt.Errorf("协调者供给 HOME 未展开为绝对路径: %q", targetHome)
       }
       if p.userHomeDir == nil {
           return "", errors.New("协调者供给缺少主 HOME 读取函数")
       }
       mainHome, err := p.userHomeDir()
       if err != nil {
           return "", fmt.Errorf("读取主 HOME: %w", err)
       }
       if p.currentConfig == nil {
           return "", errors.New("协调者供给缺少活配置读取函数")
       }
       cfg := p.currentConfig()
       if cfg == nil {
           return "", errors.New("协调者供给缺少 agentd 活配置")
       }
       if err := os.MkdirAll(targetHome, 0o700); err != nil {
           return "", fmt.Errorf("创建协调者隔离 HOME %q: %w", targetHome, err)
       }
       projected, err := projectCoordinatorConfig(cfg)
       if err != nil {
           return "", err
       }
       configPath := filepath.Join(targetHome, ".handoff", "config.yaml")
       if err := config.Save(configPath, &projected); err != nil {
           return "", fmt.Errorf("写协调者隔离配置 %q: %w", configPath, err)
       }
       if err := copyMissingCoordinatorCredential(mainHome, targetHome, spec.CLI, p.credentialPath); err != nil {
           return "", err
       }
       if err := copyCoordinatorRules(mainHome, targetHome); err != nil {
           return "", err
       }
       return targetHome, nil
   }
   ```

   `Prepare` 每次调用都先覆盖 `.handoff/config.yaml`，因此占用目录的 first-run
   token 会被当前活 token 替换；它从不删除目录，假 session 文件保持不变。日志在
   该函数入口、配置写前后、凭据/规则复制前后各有结构化节点，错误带 `cli`、源/目
   标路径和 cause；token、凭据内容、prompt 不进日志。
3. `projectCoordinatorConfig` 复制 `cfg` 值后只规范路径，不改活快照：

   ```go
   func projectCoordinatorConfig(cfg *config.Config) (config.Config, error) {
       if cfg == nil {
           return config.Config{}, errors.New("agentd 活配置为空")
       }
       if strings.TrimSpace(cfg.DataDir) == "" {
           return config.Config{}, errors.New("agentd DataDir 为空")
       }
       projected := *cfg
       var err error
       if projected.DataDir, err = filepath.Abs(projected.DataDir); err != nil {
           return config.Config{}, fmt.Errorf("解析 agentd DataDir %q: %w", cfg.DataDir, err)
       }
       if projected.RepoRoot != "" {
           if projected.RepoRoot, err = filepath.Abs(projected.RepoRoot); err != nil {
               return config.Config{}, fmt.Errorf("解析 agentd RepoRoot %q: %w", cfg.RepoRoot, err)
           }
       }
       if dsn := projected.Ledger.DSN; dsn != "" &&
           !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
           if projected.Ledger.DSN, err = filepath.Abs(dsn); err != nil {
               return config.Config{}, fmt.Errorf("解析 SQLite ledger DSN %q: %w", dsn, err)
           }
       }
       return projected, nil
   }
   ```

   实现者需注意：`internal/ledger/store.go:49-60` 规定只有两个 postgres 前缀走 PG，
   其余 DSN 是 SQLite 文件路径；这是相对 DSN 转绝对的依据。配置保存后测试必须
   `config.Load` 回读，而不是只读内存 clone。
4. `copyMissingCoordinatorCredential` 只接受 `credentialPath(filepath.Base(cli))`
   返回的相对路径；源不存在 warning + no-op，源存在且目标 `os.Lstat` 已存在则
   warning + no-op，目标缺失才创建父目录、复制普通文件权限并 `Chmod`。目标 symlink、
   源 symlink、源目录和写入错误都带路径返回错误。`copyCoordinatorRules` 只处理
   `mainHome/.config/opencode/AGENTS.md` 与 `mainHome/.config/opencode/skills`：
   源不存在 warning + no-op；普通文件可覆盖，目录递归创建/覆盖同名文件但不删除
   目标额外文件；任何 symlink 都报错。函数必须使用 `os.Lstat` 和递归白名单，不能
   使用 `RemoveAll`，不能调用 `skill.Install`，不能触碰 `.local/share/opencode` 的
   其他文件。每个新 helper 加“为什么不用整树同步”的注释。

   这三个复制 helper 的控制流按下面的完整形状实现，避免执行者把“缺失时不覆盖”
   误写成整树同步：

   ```go
   func copyMissingCoordinatorCredential(mainHome, targetHome, cli string,
       credentialPath func(string) (string, bool)) error {
       if credentialPath == nil {
           return nil
       }
       rel, ok := credentialPath(filepath.Base(cli))
       if !ok || rel == "" || filepath.IsAbs(rel) {
           return nil
       }
       source := filepath.Join(mainHome, rel)
       info, err := os.Lstat(source)
       if errors.Is(err, os.ErrNotExist) {
           slog.Default().Warn("协调者主 HOME 缺少表内凭据，跳过供给", "cli", cli, "source", source)
           return nil
       }
       if err != nil {
           return fmt.Errorf("stat 主 HOME 凭据 %q: %w", source, err)
       }
       if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
           return fmt.Errorf("主 HOME 凭据不是普通文件 %q", source)
       }
       destination := filepath.Join(targetHome, rel)
       if existing, statErr := os.Lstat(destination); statErr == nil {
           if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() {
               return fmt.Errorf("隔离凭据目标不是普通文件 %q", destination)
           }
           slog.Default().Info("协调者隔离凭据已存在，保留原文件", "cli", cli, "target", destination,
               "mode", existing.Mode().String())
           return nil
       } else if !errors.Is(statErr, os.ErrNotExist) {
           return fmt.Errorf("检查隔离凭据 %q: %w", destination, statErr)
       }
       if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
           return fmt.Errorf("创建隔离凭据父目录 %q: %w", filepath.Dir(destination), err)
       }
       data, err := os.ReadFile(source)
       if err != nil {
           return fmt.Errorf("读取主 HOME 凭据 %q: %w", source, err)
       }
       if err := os.WriteFile(destination, data, info.Mode().Perm()); err != nil {
           return fmt.Errorf("写隔离凭据 %q: %w", destination, err)
       }
       if err := os.Chmod(destination, info.Mode().Perm()); err != nil {
           return fmt.Errorf("设置隔离凭据权限 %q: %w", destination, err)
       }
       slog.Default().Info("协调者缺失凭据已供给", "cli", cli, "target", destination)
       return nil
   }

   func copyCoordinatorRules(mainHome, targetHome string) error {
       sourceRoot := filepath.Join(mainHome, ".config", "opencode")
       targetRoot := filepath.Join(targetHome, ".config", "opencode")
       if err := copyCoordinatorFileIfPresent(filepath.Join(sourceRoot, "AGENTS.md"),
           filepath.Join(targetRoot, "AGENTS.md"), true); err != nil {
           return err
       }
       return copyCoordinatorTreeIfPresent(filepath.Join(sourceRoot, "skills"),
           filepath.Join(targetRoot, "skills"))
   }

   func copyCoordinatorFileIfPresent(source, destination string, overwrite bool) error {
       info, err := os.Lstat(source)
       if errors.Is(err, os.ErrNotExist) {
           slog.Default().Warn("协调者主 HOME 缺少规则文件，跳过供给", "source", source)
           return nil
       }
       if err != nil {
           return fmt.Errorf("stat 协调者规则源 %q: %w", source, err)
       }
       if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
           return fmt.Errorf("协调者规则源不是普通文件 %q", source)
       }
       if existing, statErr := os.Lstat(destination); statErr == nil {
           if existing.Mode()&os.ModeSymlink != 0 {
               return fmt.Errorf("协调者规则目标是 symlink %q", destination)
           }
           if !overwrite {
               return nil
           }
       } else if !errors.Is(statErr, os.ErrNotExist) {
           return fmt.Errorf("检查协调者规则目标 %q: %w", destination, statErr)
       }
       if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
           return fmt.Errorf("创建协调者规则父目录 %q: %w", filepath.Dir(destination), err)
       }
       data, err := os.ReadFile(source)
       if err != nil {
           return fmt.Errorf("读取协调者规则 %q: %w", source, err)
       }
       if err := os.WriteFile(destination, data, info.Mode().Perm()); err != nil {
           return fmt.Errorf("写协调者规则 %q: %w", destination, err)
       }
       return os.Chmod(destination, info.Mode().Perm())
   }

   func copyCoordinatorTreeIfPresent(source, destination string) error {
       info, err := os.Lstat(source)
       if errors.Is(err, os.ErrNotExist) {
           slog.Default().Warn("协调者主 HOME 缺少 skills 目录，跳过供给", "source", source)
           return nil
       }
       if err != nil {
           return fmt.Errorf("stat 协调者 skills 源 %q: %w", source, err)
       }
       if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
           return fmt.Errorf("协调者 skills 源不是普通目录 %q", source)
       }
       if existing, statErr := os.Lstat(destination); statErr == nil {
           if existing.Mode()&os.ModeSymlink != 0 || !existing.IsDir() {
               return fmt.Errorf("协调者 skills 目标不是普通目录 %q", destination)
           }
       } else if !errors.Is(statErr, os.ErrNotExist) {
           return fmt.Errorf("检查协调者 skills 目标 %q: %w", destination, statErr)
       }
       if err := os.MkdirAll(destination, 0o700); err != nil {
           return fmt.Errorf("创建协调者 skills 目标 %q: %w", destination, err)
       }
       entries, err := os.ReadDir(source)
       if err != nil {
           return fmt.Errorf("读取协调者 skills 目录 %q: %w", source, err)
       }
       for _, entry := range entries {
           src := filepath.Join(source, entry.Name())
           dst := filepath.Join(destination, entry.Name())
           if entry.Type()&os.ModeSymlink != 0 {
               return fmt.Errorf("协调者 skills 源含 symlink %q", src)
           }
           if entry.IsDir() {
               if err := copyCoordinatorTreeIfPresent(src, dst); err != nil {
                   return err
               }
               continue
           }
           if err := copyCoordinatorFileIfPresent(src, dst, true); err != nil {
               return err
           }
       }
       return nil
   }
   ```
5. 实现 `normalizeCoordinatorSpec`：调用 `hostapi.ExpandHomePath(spec.HomeDir)`，
   空值或错误直接返回含原串的错误，结果不是 `filepath.IsAbs` 也返回错误，成功把
   绝对路径写回 spec。`launchCoordinatorRoundWithExpect` 和
   `wakeCoordinatorRound` 在拿到 carrier 后、日志与 keystone 调用前都调用它；失败
   记录 card/squad/carrier/cause，并让既有 binding defer 归还名额。这样 keystone
   保存的 `SessionRef.HomeDir` 从生产入口开始就是绝对值。
6. 实现 `coordinatorSessionRefResolver`：

   ```go
   type coordinatorSessionRefResolver struct {
       server       *Server
       expandHomeDir func(string) (string, error)
   }

   func (r coordinatorSessionRefResolver) ResolveSessionRef(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error) {
       if r.server == nil || r.server.scheduling == nil {
           return ref, errors.New("协调者 attach 无编制域读取端口")
       }
       if ref.HomeDir == "" {
           squad, err := r.server.resolveCoordinatorSquad()
           if err != nil {
               return ref, fmt.Errorf("读取协调者小队以恢复卡 %s HOME: %w", card, err)
           }
           for _, member := range squad.Members {
               carrier, readErr := r.server.scheduling.Carrier(member.Carrier)
               if readErr != nil {
                   return ref, fmt.Errorf("读取协调者载体 %s HOME: %w", member.Carrier, readErr)
               }
               if carrier.Status != scheduling.StatusOnline {
                   continue
               }
               ref.HomeDir = carrier.HomeDir
               break
           }
           if ref.HomeDir == "" {
               return ref, fmt.Errorf("协调者小队 %s 没有已上线载体可恢复 HOME", squad.Name)
           }
       }
       expand := r.expandHomeDir
       if expand == nil {
           expand = hostapi.ExpandHomePath
       }
       expanded, err := expand(ref.HomeDir)
       if err != nil {
           return ref, fmt.Errorf("展开卡 %s 的协调者 attach HOME %q: %w", card, ref.HomeDir, err)
       }
       if !filepath.IsAbs(expanded) {
           return ref, fmt.Errorf("卡 %s 的协调者 attach HOME 不是绝对路径: %q", card, expanded)
       }
       ref.HomeDir = expanded
       return ref, nil
   }
   ```

   成员遍历保持 `scheduling.admitInto` 的声明顺序和 online 过滤，但**不**调用
   `LaunchAdmit`、`acquire`、`Release` 或任何计数写入。记录 resolver 入口、squad/
   carrier 读取前后、错误和成功路径日志；session id 不写进返回日志正文。该选择
   使唯一协调者小队中“当前已上线的第一个登记成员”成为冷读的确定性来源；已有热
   ref 则优先使用它自己的 HomeDir。

### 步骤 B：装配 runner、resolver、locator

7. 在 `server.go:SetupAutomation` 用活配置和现有凭据表装配：

   ```go
   s.hostAPI = hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor)
   supplier := coordinatorHomeSupplier{
       currentConfig: s.conf,
       userHomeDir: os.UserHomeDir,
       expandHomeDir: hostapi.ExpandHomePath,
       credentialPath: toolchain.CredRelPathFor,
   }
   runner := coordinatorRunner{h: s.hostAPI, prepareHome: supplier.Prepare}
   resolver := coordinatorSessionRefResolver{server: s, expandHomeDir: hostapi.ExpandHomePath}
   ks := keystone.New(runner, roomNarrator{c: s.rooms}, facade,
       attachLocator{expandHome: hostapi.ExpandHomePath})
   ks.SetSessionRefResolver(resolver)
   s.keystone = ks
   ```

   继续让 `WakeHome` 由 hostapi 自己的旧路径运行，不把 `supplier.Prepare` 传给它。
8. 在 `server.go` 更新 `coordinatorRunner`：Launch 入口记录 cli/raw home/workdir，
   调 `prepareHome`，将返回值写回 `spec.HomeDir` 后调用现有
   `Host.RunTurn(context.Background(), hostapi.TurnRequest{...})`；Resume 把 ref
   先映射成 `SessionSpec{CLI,HomeDir,Model,Workdir}` 调同一 preparer，把返回绝对
   路径写回 ref，再调用 `resumeTurnRequest`。供给失败在 child 启动前返回；RunTurn
   前后、错误与成功都记录结构化日志，成功日志只记 session 是否非空/输出字节数，
   不记输出正文。保留 `resumeTurnRequest` 的既有 `HANDOFF_SESSION_*` env 形状。
   runner 的字段形状固定为：

   ```go
   type coordinatorRunner struct {
       h           *hostapi.Host
       prepareHome func(keysclient.SessionSpec) (string, error)
   }
   ```

   `prepareHome == nil` 只允许现有单元测试构造的无供给替身继续工作；生产
   `SetupAutomation` 必须传入 `supplier.Prepare`，否则应在测试中直接失败而不静默
   退回旧路径。
9. 在 `server.go` 把 `attachLocator` 改为持有可注入
   `expandHome func(string)(string,error)`。`Locate` 先拒绝空 SessionID/空 HomeDir，
   再展开并检查绝对路径；命令由服务端生成，CLI/session/home 均按 POSIX shell word
   处理：`[A-Za-z0-9_+\-.,/:@%]` 内的绝对路径原样输出，其他路径用单引号并把 `'`
   写成 `'\''` 形态。因此普通路径精确为
   `HOME=/abs opencode --session id`，带空格为
   `HOME='/abs/My Docs' opencode --session id`；绝不调用或复制
   `scheduling.RunCommand` 的 `HOME=~`。展开错误返回含原串的 error，不能降级为不含
   HOME 的 command。日志只记 card 不可得时的 ref 是否有 home、workdir、错误；不记
   command/session 正文。

### 步骤 C：从声明缝锁供给和冷读

10. 新增 `internal/agentd/coordinator_home_test.go`，因为
    `internal/hostapi/runturn_test.go` 的 fake CLI helper 是未导出符号，测试文件内
    增加一个完整的短 shell harness：写入临时 PATH 的 `opencode`，把每个 argv 和
    `env:HOME=$HOME` 写到捕获文件，并输出带 `sessionID` 与 text part 的两行 JSONL。
    harness 的完整定义固定为：

    ```go
    func installCoordinatorFakeCLI(t *testing.T) string {
        t.Helper()
        binDir := t.TempDir()
        capture := filepath.Join(t.TempDir(), "coordinator-cli.txt")
        script := `#!/bin/sh
: "${COORD_CAPTURE:?COORD_CAPTURE 必须设置}"
for arg in "$@"; do printf 'arg:%s\n' "$arg" >>"$COORD_CAPTURE"; done
printf 'env:HOME=%s\n' "$HOME" >>"$COORD_CAPTURE"
printf '%s\n' '{"type":"step_start","sessionID":"runner-sess","part":{"type":"step-start"}}'
printf '%s\n' '{"type":"text","sessionID":"runner-sess","part":{"type":"text","text":"runner-ok"}}'
printf '%s\n' '{"type":"step_finish","sessionID":"runner-sess","part":{"type":"step-finish","reason":"stop"}}'
`
        fake := filepath.Join(binDir, "opencode")
        if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
            t.Fatalf("写 coordinator fake CLI: %v", err)
        }
        t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
        t.Setenv("COORD_CAPTURE", capture)
        return capture
    }
    ```

    测试真实调用 `coordinatorRunner.Launch`，不能只调用 `Prepare`。使用注入的
    `coordinatorHomeSupplier`：

    - 主 HOME 建立 `AGENTS.md`、`skills/plan/SKILL.md`、opencode
      `.local/share/opencode/auth.json`；活配置 token=`agentd-live`、DataDir 为
      临时绝对目录、Ledger.DSN 为相对 SQLite 路径；展开函数把
      `~/.handoff/home/muse` 映射到临时 target；
    - Launch 后用 `config.Load(target/.handoff/config.yaml)` 回读，断言 token 等于
      `agentd-live`、DataDir 等于活进程实际绝对 DataDir、Ledger.DSN 等于相对路径的
      `filepath.Abs`；逐字节断言 AGENTS、skill、auth 可见，捕获到的 HOME 等于 target
      且不含 `~`；
    - 占用 subtest 先写旧 first-run config、`sessions.db` sentinel 和旧 auth，调用
      同一个 `coordinatorRunner.Launch`，断言 token 被覆盖、sentinel 和已存在 auth
      原文不变，`.local/share/opencode` 没有新增同步树；
    - 另用同一 preparer 调 `coordinatorRunner.Resume`，断言目标文件仍满足上述白名
      单且 argv 含 `-s runner-sess`，证明 Resume 也走供给缝，而不是只测 helper。

    测试每条断言均从 `coordinatorRunner.Launch/Resume` 入口穿过
    `Host.RunTurn`；供给前的 config/credential/rules 顺序由 fake CLI 启动后的磁盘
    读回证实。
11. 在 `coordapi_test.go` 调整 `newCoordEnv` 与 `newNoPTYCoordEnv`：它们创建测试
    keystone 后必须调用 `SetSessionRefResolver(coordinatorSessionRefResolver{server: env.srv, expandHomeDir: hostapi.ExpandHomePath})`，
    locator 使用 `attachLocator{expandHome: hostapi.ExpandHomePath}`。保留 fake runner
    对 `SessionSpec` 的已有断言。
12. 新增 HTTP 缝级 `TestCoordStatusColdLocateUsesRegisteredHomeWithoutAdmission`：
    用既有 `newCoordEnv`、`seedCoordinatorSquad`、`createCoordCard`，直接绑定
    `cli:opencode#sess-cold`/`coordinate` seat，不先 seed keystone session；GET
    `/api/cards/{id}/coordinator` 后 JSON `attach.command` 必须含
    `HOME=/home/coordinator` 与 `--session sess-cold`。GET 前后逐一读取
    `runningCountIn(t, env.srv.autoLedger, "squad/coord/c1")` 和
    `runningCountIn(..., "carrier/c1")`，均保持 0；测试应能在把 resolver 误改为
    `LaunchAdmit` 时翻红。该入口真实经过 `handleCoordStatus → Service.Locate →
    SessionRefResolver → attachLocator`。
    同一测试文件再加 `TestCoordStatusQuotesHomePathWithSpaces`：登记载体
    `HomeDir="/home/coord docs"`，从空 keystone session 的 ledger cold path GET
    状态，断言 command 精确为
    `HOME='/home/coord docs' opencode --session sess-cold`，并执行
    `exec.Command("sh", "-n", "-c", command).Run()` 断言 POSIX shell 语法可解析；
    这仍从 HTTP → `Service.Locate` 进入，不把直接 locator 单测当作唯一锁。
13. 新增 HTTP 缝级 `TestCoordAttachHomeExpansionFailureReturns400`：用已有 card 和
    keystone hot session，注入 `attachLocator{expandHome: func(string)(string,error){
    return "", errors.New("home unavailable") }}`，POST `/attach` body
    `{"active":true,"workdir":"/repo/handoff"}`，断言 HTTP 400、响应包含展开错误、
    `AttachState(card)==false`（失败回滚）。这锁住 attach 的 fail-closed，而不是只锁
    locator helper。
14. 更新 `TestCoordLaunchEndpointSuccess`，增加断言：Launch 后 GET
    coordinator status 的 command 同时含 `HOME=/home/coordinator`、`opencode` 和
    `--session sess-coord`；保留现有 `runner.launches` 恰一次、两级计数归零和无 task
    断言。不要加 `RoundResult.Output` 到 room 的任何测试或实现。
15. 最小绿测顺序：

    ```text
    go test ./internal/agentd -run 'Test(Coord|Wake|ResumeTurnRequest|CoordinatorHome)' -count=1
    go test ./internal/agentd -run 'Test(Coord|Wake|ResumeTurnRequest)' -count=1
    ```

    预期两条都 `ok`；新增供给测试的名称必须以 `TestCoordinatorHome` 开头，保证
    第一条确实命中而不是空测试集。该 task 只跑 agentd 触及包，不跑全仓。

## 5. Task 4：真实序列化边界与前端既有 fixture 更新

### 文件范围

- `internal/proto/contract_fixture_test.go`
- `web/src/api/testdata/CoordinatorStatus.json`
- `web/src/api/scheduling.fetch.test.ts`
- `web/src/api/contract.test.ts`

### Interfaces

- Consumes：既有 `proto.CoordinatorStatus`、`proto.CoordinatorAttachInfo`；
  `internal/proto/contract_fixture_test.go` 的 `TestContractFixtures` 生成/逐字节
  校验机制；web 的 `CoordinatorStatus` TS 类型和 JSON import。
- Produces：同一既有三字段 JSON，`attach.command` 样例更新为
  `HOME=/repo/coordinator opencode --session sess-coord`；不新增 `home_dir` 或任意
  PTY/env 字段。`CarrierRunCommandResp` 的 `HOME=~/.handoff/...` fixture 保持原样，
  因为它是另一个 out-of-scope 载体运行命令。

### 步骤

1. 基线已跑 `go test ./internal/proto -count=1`，预期 `ok`；web 两条基线因
   `vitest`/`tsc` 缺失已按原文记为未验证。先把 Go sample
   `internal/proto/contract_fixture_test.go:176-180` 的 `CoordinatorStatus.Attach.Command`
   改成 `HOME=/repo/coordinator opencode --session sess-coord`。
2. 用既有显式更新命令
   `go test ./internal/proto/ -run TestContractFixtures -update` 生成
   `web/src/api/testdata/CoordinatorStatus.json`，然后只检查该 JSON 的
   `bound/attach_active/attach.machine/attach.dir/attach.command` 仍存在且 command
   精确等于新样例；不要手工生成第二份格式。该命令若失败，保留原始错误并停止把
   fixture 写成通过。
3. 在 `web/src/api/scheduling.fetch.test.ts:83-99` 把 status 样例的 command 更新为
   新绝对 HOME；在 `web/src/api/contract.test.ts:632-645` 把原有宽松的
   `toContain('sess-coord')` 加强为精确 `toBe('HOME=/repo/coordinator opencode --session sess-coord')`。
   两处仍断言 `Object.keys(attach)` 只有 `machine/dir/command` 和未绑定 `attach:null`。
4. 序列化边界回归必须按以下链路核验：Go struct → `json.Marshal` → fixture 文件 →
   TS JSON import → fetch 测试对象。没有新增可空字段，所以“缺失 vs 零值”沿用已有
   `attach` 指针的 `null` 断言与 `machine:""` 断言；不以新增字段计数代替行为断言。
   配置 YAML 的另一条手写投影链已由 Task 3 的 `config.Save → config.Load` 断言
   token/DataDir/DSN，且检查旧 session 文件内容，覆盖字段值不为零与缺失路径两类。
5. 最小绿测：

   ```text
   go test ./internal/proto -run TestContractFixtures -count=1
   npm test -- --run src/api/scheduling.fetch.test.ts src/api/contract.test.ts   # cwd web
   npm run typecheck                                                         # cwd web
   ```

   Go 命令预期 `ok`；web 若仍出现基线的 `vitest: not found`/`tsc: not found`，最终
   记为未验证，不写 pass。该 task 不运行 web 全套以外的测试。

## 6. 五项自审与最终验收

### 6.1 缺陷族对抗审查

仓内没有独立 `defect-families` 文件；按本 spec/review 已显出的缺陷族逐族设问，
结论如下：

| 缺陷族 | 对抗问题 | 锁点与结论 |
|---|---|---|
| 展开/输入 | `~`、空 HomeDir、UserHomeDir 失败、相对路径会不会被 fail-open？ | Task 1 两支 RunTurn、Task 3 normalize/resolver/attach 绝对路径检查；失败不启动、不返回无 HOME command。 |
| 状态/持久化 | 占用目录供给会不会删 session db 或旧凭据？冷读会不会改计数？ | Task 3 occupied 磁盘断言；HTTP cold Locate 前后两级 running 均 0；无 `RemoveAll`。 |
| 选择/并发 | GET 状态会不会抄 LaunchAdmit，或改变 coordinator carrier 选择的计数？ | resolver 只走 SquadRows/Carrier、online/声明顺序；HTTP counter 断言锁死。 |
| 错误/可观测 | 展开、配置、复制、外部 CLI 各错误是否带上下文，成功是否静默？ | 三个生产 task 步骤要求 `slog` 入口/前后/错误/成功；token、prompt、文件内容不进日志。 |
| 安全/路径 | symlink、带空格路径、凭据覆盖、整树同步是否能越界？ | copy 使用 Lstat、拒绝 symlink、白名单；attach shell word 测试；目标已有 auth 不覆盖。 |
| 序列化/兼容 | Go 与 TS 是否只改字符串、不漂移字段；null/空串仍可分辨？ | Task 4 Go fixture + JSON + TS 两测试；Task 3 config roundtrip；不改 CarrierRunCommand。 |
| 用户旅程 | Launch→同 HOME Resume→同 HOME attach 是否各自走同一展开结果，WakeHome 是否仍干净？ | runner Launch/Resume 真 CLI 测试、hot/cold Locate、HTTP attach/GET、WakeHome `.config/opencode/skills` 负断言。 |

### 6.2 接缝覆盖双向审计

声明缝清单：

1. `Host.RunTurn`：Task 1 的成功/失败两支，以及 Task 3 runner Launch/Resume 的 fake
   CLI 调用链穿过；两支都从公开 `RunTurn` 入口进入。
2. `keystone.Service.Locate`：Task 2 hot/cold 两支和 Task 3 HTTP GET/POST；它们都从
   `Service.Locate` 或穿过它进入，冷支还断言 resolver 前 `HomeDir==""`。
3. `coordinatorRunner.Launch/Resume`：Task 3 供给测试从两方法进入，磁盘与 child
   env 是行为断言；WakeHome 仅在 Task 1 作为不搬 skill 的对照，不冒充供给缝。

逐缝反查：1 的每条 RunTurn 测试入口是 `RunTurn`；2 的每条 Locate 测试入口是
`Service.Locate`，HTTP 测试调用链穿过；3 的每条供给测试入口是 runner 方法。没有
只测 helper 顶替声明缝的测试。反向反查：三条缝均至少有成功和失败/边界锁；冷
Locate 的“禁止 LaunchAdmit”由 HTTP counter 断言独立锁住。

### 6.3 序列化边界清单

- `config.Save` → target `.handoff/config.yaml` → `config.Load`：token、绝对
  DataDir、绝对 SQLite DSN、occupied old token 覆盖/缺失 credential 保持均有 Task 3
  断言；这是新供给行为的真实 YAML 边界。
- `coordinatorAttachInfo` → `writeJSON` → `CoordinatorStatus` JSON：Task 3 HTTP
  断言 command 含绝对 HOME/session，Task 4 Go fixture/TS fixture 逐字校验；字段
  集合不变。
- `CoordinatorStatus.json` → TS import → `getCoordinatorStatus` 与 contract test：
  Task 4 两个前端测试；`attach:null`、`machine:""`、精确 command 保持。
- 不改 `SessionRef`/`SessionSpec` wire 结构，不改 `CarrierRunCommandResp`；因此没有
  额外跨语言字段投影。

### 6.4 上下文预算、注释和日志

每个 task 文件集合已在章节开头封闭：hostapi、keystone、agentd、proto/web 四组，
没有需要另插竖切卡的开放目录。新文件头、所有新增导出函数、非显然的“复制而非
symlink/不删 session db/只读不准入”逻辑均必须有职责、边界和 why 注释。生产变更
只用结构化 `slog`，禁止 `print`/`fmt.Print`；日志不得写 token、凭据正文、prompt
或回合 output。

### 6.5 收口命令与真机门

实现者在各 task 的最小测试全绿后，按顺序跑：

```text
gofmt -w internal/hostapi/probe.go internal/hostapi/driver.go internal/hostapi/runturn_test.go internal/hostapi/probe_test.go internal/keystone/keystone.go internal/keystone/keystone_spec_test.go internal/agentd/coordinator_home.go internal/agentd/coordinator_home_test.go internal/agentd/server.go internal/agentd/scheddrain.go internal/agentd/coordapi.go internal/agentd/coordapi_test.go
go test ./internal/hostapi ./internal/keystone ./internal/agentd ./internal/proto ./internal/config ./internal/toolchain ./internal/skill -count=1
go build ./...
cd web && npm test -- --run src/api/scheduling.fetch.test.ts src/api/contract.test.ts && npm run typecheck
```

`gofmt` 无输出不等于测试通过；每条命令必须亲自取得结果。web 依赖缺失则贴实际
报错并标未验证。合 main 前的本机门按 spec `b323.md:78,88`：协调者真实
`card coordinate` 后，在展开后的隔离 HOME 执行 `handoff status` 退出 0；二次房间
唤醒同 session、`rebuilt=false` 且没有新 pointer；attach command 在该 HOME 下能
解析会话。Linux 实现节点不能把未执行的本机门写成 pass。

本节点只提交本计划和同步台账，不实现上述代码，不派发、不调用 handoff CLI、不起
executor；实现节点按当前分支创建一个提交后停止，不 push。
