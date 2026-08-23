# Plan：平台不变量恒在层（B207）

> 节点：charter-plan，产出物=本计划文档；实现者只按本文件执行，不在本回合直接改实现。
> 冻结物：docs/superpowers/specs/2026-08-23-b207-discipline-provenance.md（已批准，L2）。
> 目标：把平台四条底线从可被 Resolver.ByName 整份替换的纪律块中拆出，作为 discipline 包内无条件组装的第四层来源；保留角色块选择语义，并增加一个可留痕的机器级关闭开关。

## 0. 边界与完成定义

本卡只实现 spec「方案」与「实现决定」中的恒在层、显式关闭、来源多值回显及其测试。以下内容明确不改：

- 不重构 workflow 纪律块的任务契约层、执行器适配层或选择键。
- 不移除 Resolver.For 未配置时的内置默认，不给纪律块增加版本号，也不处理两机副本漂移。
- 不新增 adapter 的 trailer 文案。基线已核实：四家 adapter 都把 StartReq.Discipline 传给 turn.RenderPrompt；Codex 另把同一份 ProtocolRules 放进常驻 developerInstructions。平台层只注入四条平台不变量与尾部自查，不能再复制协议 trailer。
- 不新增 handoff CLI、executor 进程或外部写入。

完成定义：

1. 点名覆盖、点名内置、未点名默认、未点名显式空串四种 resolver 输入都经 discipline.Compose 得到正确结果；平台头部和尾部各一次。
2. 旧配置缺少新键时平台不变量默认开启；只有 platform_invariants: false 才关闭，并在 YAML 中保留这个显式 false。
3. Dispatch、resumeForContinue、ResumeTask 继续只经过 Manager.resolveDisciplineFor，其返回值同时含正文与多值来源；关闭时来源明确包含「平台不变量已关闭」。
4. Task.Discipline、progress 回显、StartReq.Discipline 与真实 prompt 渲染链路均有回归断言；协议层已有的 trailer 指令保持不变且不重复。

## 1. 基线复核（出稿时真实运行）

工作树分支：cards/B207-charter。基线提交：bd000a1b59fd3ca5ebb414df6e898594dfc87e66。

以下命令在基线执行成功，执行者开工前必须重新运行；数字变化或出现新失败时，把原始输出写入台账并停在该 task 的基线步骤，不把基线失败归因给本卡：

~~~text
$ go test ./internal/discipline ./internal/config ./internal/executor/turn
ok   github.com/Xsxdot/handoff/internal/discipline  (cached)
ok   github.com/Xsxdot/handoff/internal/config      (cached)
ok   github.com/Xsxdot/handoff/internal/executor/turn (cached)

$ go test ./internal/agentd -run 'TestResolveDisciplineForPrefersName|TestDispatchPassesDisciplineAndRecordsSource|TestDispatchNamedDisciplineInjectsNamedBlock|TestDispatchUnnamedDisciplineUnchanged'
ok   github.com/Xsxdot/handoff/internal/agentd 0.679s
~~~

基线还尝试过以下覆盖更大的命令，但它不能作为本卡的绿灯依据，执行者不要为了本卡修改这些存量问题：

~~~text
$ go test ./internal/discipline ./internal/config ./internal/executor/turn ./internal/executor/codex ./internal/executor/grok ./internal/executor/opencode ./internal/executor/claudecode ./internal/agentd
--- FAIL: TestPermServerAskThenRespond
    perm_test.go:56: newPermServer: 裁决 socket 路径过长（114 字节，上限 107）: /root/.handoff/tasks/cf1956af-4cfd-4c5d-ae42-ddb03eaf1e1d/tmp/TestPermServerAskThenRespond.../perm.sock——把 DataDir 配到更浅的位置
--- FAIL: TestStartWritesPromptBeforeWaitingReady
    start_ordering_test.go:33: mkdir /tmp/hc-666988718: read-only file system
FAIL
FAIL github.com/Xsxdot/handoff/internal/executor/claudecode
~~~

失败原因是当前 handoff 任务路径使 Unix socket 超过 107 字节，且现有 claude 测试硬编码 /tmp/hc-*；它们与 B207 无关。B207 的 task 测试范围只触及 internal/discipline、internal/config、internal/agentd 和 internal/executor/turn。

## 2. 冻结接口与序列化边界

~~~go
// internal/discipline/platform.go
func Compose(base Block, platformEnabled bool) Block

// internal/config/config.go
type Config struct {
    PlatformInvariants *bool `yaml:"platform_invariants,omitempty"`
}

func (c *Config) PlatformInvariantsEnabled() bool
~~~

platformEnabled=true 表示注入平台不变量；false 表示明确关闭；Config.PlatformInvariants == nil 由 PlatformInvariantsEnabled 解释为 true。指针是必要的：普通 bool 无法把「配置缺失」与「显式 false」区分开，omitempty 也会把关闭值吞掉。

既有消费接口保持逐字不变：

~~~go
func (r *Resolver) For(executor string) (Block, error)
func (r *Resolver) ByName(name, executor string) (Block, error)
func (m *Manager) resolveDisciplineFor(name, execName string) (discipline.Block, error)
func RenderPrompt(taskID, planContent, disciplineBlock string) (string, error)
~~~

### 来源与正文口径

启用时 Source 按顺序为：

~~~text
内置:平台不变量 + <base.Source>
~~~

base.Source 为空时只返回 内置:平台不变量。关闭时保留 base.Text，Source 为：

~~~text
平台不变量已关闭 + <base.Source>
~~~

base.Source 为空时只返回 平台不变量已关闭。关闭只取消平台层，不取消角色块或 executor 文件块。

平台头部和尾部必须逐字为：

~~~text
# 平台不变量（恒在层）

1. 不要派发、不要调用 handoff CLI（只读本地图数据的 handoff graph 子命令除外）、不要起任何新的 executor 进程或子任务。
2. 没有亲自跑到结果的命令，不许写它的结论。跑了但失败，贴原始报错原文，不要替它归因；不确定就写「未验证」。
3. 每确立一个事实就往台账文件追加一行——提交、跑过的命令与原始输出、放弃的尝试、做出的判断。不要攒到回合结束再写：回合可能不会有结束。
4. 按协议输出 trailer 收口。

收口前逐条自查：① 有没有把没亲自跑到结果的命令写成结论？② 台账是边干边追加的吗？③ 这一轮碰过 handoff CLI 或起过新 executor 吗？
~~~

### 手写序列化/投影清单

| 边界 | 生产点 | 消费点 | 本卡断言 |
|---|---|---|---|
| YAML | Config.PlatformInvariants 经 yaml.Marshal/严格解码 | PlatformInvariantsEnabled | 缺失=nil→true；显式 false 保存为 platform_invariants: false 并读回 false |
| 来源正文组装 | discipline.Compose | manager.resolveDisciplineFor | 头、base、尾顺序；各平台标记一次；多来源顺序固定 |
| 任务投影 | Dispatch 将 Block.Source/Text 投影到 Task.Discipline 与 StartReq.Discipline | turn.RenderPrompt | task、事件、StartReq、prompt 看到同一来源/正文 |

不新增 DTO 或跨语言字段；Task.Discipline 与 StartReq.Discipline 是已有字符串字段。必须保留一条穿过 turn.RenderPrompt 的链路断言，不能只测 Compose 返回值。

## 3. 任务顺序总览

| Task | 内容 | 精确文件集 | 提交信息 |
|---|---|---|---|
| 1 | discipline 包增加平台层组装函数与四种输入组合测试 | internal/discipline/platform.go、internal/discipline/platform_test.go | feat(discipline): add always-on platform invariant layer |
| 2 | 配置增加可留痕的三态开关与 YAML roundtrip 测试 | internal/config/config.go、internal/config/config_test.go | feat(config): add explicit platform invariant switch |
| 3 | manager 唯一接线、来源回显、任务到 prompt 的链路回归 | internal/agentd/manager.go、internal/agentd/manager_test.go | feat(agentd): compose platform invariants at discipline boundary |

每个 task 只跑列出的包；最后的跨包与真机实验属于整卡验收，不归入任一单 task。

---

## Task 1：在 discipline 包内组装恒在平台层

### 文件与接口

- Create: internal/discipline/platform.go
- Create: internal/discipline/platform_test.go
- Consumes: Block、Block.Text、Block.Source，来自 internal/discipline/discipline.go:54-62；无外部 I/O。
- Produces: func Compose(base Block, platformEnabled bool) Block，供唯一调用方 manager.resolveDisciplineFor 使用。

### 步骤 1.1：先在基线跑判据

~~~bash
go test ./internal/discipline -run 'TestForUnconfiguredUsesBuiltinByTier|TestForEmptyValueDisablesInjection|TestByNameFileOverridesBuiltin|TestByNameBuiltinReview'
~~~

预期：基线全绿。出稿基线真实结果为 ok github.com/Xsxdot/handoff/internal/discipline (cached)。

测试范围声明：本 task 只跑 ./internal/discipline；不跑全量测试。

### 步骤 1.2：写失败测试并确认红灯

新建 internal/discipline/platform_test.go，完整内容如下：

~~~go
package discipline

import (
    "strings"
    "testing"
)

func TestComposeEnabledKeepsHeadBaseTailOrderAndSources(t *testing.T) {
    base := Block{Text: "角色纪律正文", Source: "配置:charter-plan"}
    got := Compose(base, true)

    if got.Source != "内置:平台不变量 + 配置:charter-plan" {
        t.Fatalf("Source = %q", got.Source)
    }
    head := "# 平台不变量（恒在层）"
    tail := "收口前逐条自查："
    if strings.Count(got.Text, head) != 1 {
        t.Fatalf("平台头部出现次数 = %d，want 1", strings.Count(got.Text, head))
    }
    if strings.Count(got.Text, "角色纪律正文") != 1 {
        t.Fatalf("base 正文出现次数 = %d，want 1", strings.Count(got.Text, "角色纪律正文"))
    }
    if strings.Count(got.Text, tail) != 1 {
        t.Fatalf("平台尾部出现次数 = %d，want 1", strings.Count(got.Text, tail))
    }
    if !(strings.Index(got.Text, head) < strings.Index(got.Text, "角色纪律正文") &&
        strings.Index(got.Text, "角色纪律正文") < strings.Index(got.Text, tail)) {
        t.Fatalf("正文顺序错误：%q", got.Text)
    }
}

func TestComposeEnabledWithEmptyBaseStillInjectsPlatformLayer(t *testing.T) {
    got := Compose(Block{}, true)
    if got.Source != "内置:平台不变量" {
        t.Fatalf("Source = %q", got.Source)
    }
    if !strings.Contains(got.Text, "# 平台不变量（恒在层）") {
        t.Fatal("空 base 时缺平台头部")
    }
    if !strings.Contains(got.Text, "收口前逐条自查：") {
        t.Fatal("空 base 时缺平台尾部自查")
    }
}

func TestComposeDisabledPreservesBaseAndLeavesAuditSource(t *testing.T) {
    base := Block{Text: "角色纪律正文\n", Source: "内置:review"}
    got := Compose(base, false)
    if got.Text != base.Text {
        t.Fatalf("关闭平台层后 Text = %q，want 原 base %q", got.Text, base.Text)
    }
    if got.Source != "平台不变量已关闭 + 内置:review" {
        t.Fatalf("关闭平台层后的 Source = %q", got.Source)
    }
    if strings.Contains(got.Text, "# 平台不变量（恒在层）") ||
        strings.Contains(got.Text, "收口前逐条自查：") {
        t.Fatal("关闭平台层后仍注入平台正文")
    }
}

func TestComposeDisabledWithEmptyBaseHasOnlyAuditSource(t *testing.T) {
    got := Compose(Block{}, false)
    if got.Text != "" {
        t.Fatalf("空 base 且关闭平台层后 Text = %q", got.Text)
    }
    if got.Source != "平台不变量已关闭" {
        t.Fatalf("空 base 且关闭平台层后的 Source = %q", got.Source)
    }
}
~~~

运行：

~~~bash
go test ./internal/discipline -run 'TestCompose' -count=1
~~~

预期红灯：undefined: Compose。

### 步骤 1.3：写最小实现

新建 internal/discipline/platform.go。为避免复制现有 ProtocolRules，平台层只放 spec 的四条正文和收口自查：

~~~go
// platform.go —— 平台不变量恒在层的正文与组装边界。
//
// 职责：持有平台四条底线与收口自查，并把角色/执行者纪律块组装成一个 Block。
// 边界：纯函数，不读配置、不读文件、不写日志、不启动 executor；不复制
// turn.ProtocolRules，提问与 trailer 协议由 executor/turn 负责。
package discipline

import "strings"

const platformInvariantHead = `# 平台不变量（恒在层）

1. 不要派发、不要调用 handoff CLI（只读本地图数据的 handoff graph 子命令除外）、不要起任何新的 executor 进程或子任务。
2. 没有亲自跑到结果的命令，不许写它的结论。跑了但失败，贴原始报错原文，不要替它归因；不确定就写「未验证」。
3. 每确立一个事实就往台账文件追加一行——提交、跑过的命令与原始输出、放弃的尝试、做出的判断。不要攒到回合结束再写：回合可能不会有结束。
4. 按协议输出 trailer 收口。`

const platformInvariantTail = `收口前逐条自查：① 有没有把没亲自跑到结果的命令写成结论？② 台账是边干边追加的吗？③ 这一轮碰过 handoff CLI 或起过新 executor 吗？`

// Compose 把 Resolver 产出的角色/执行者纪律块与平台层组装成一个 Block。
//
// 参数：base 为 Resolver.ByName 或 Resolver.For 的结果；platformEnabled 控制平台层。
// 返回：启用时按「平台头部、base 正文、平台尾部」组装；关闭时保留 base。
// 注意：这是唯一的平台正文组装函数，调用方不得自己拼接头、base、尾。
func Compose(base Block, platformEnabled bool) Block {
    baseSource := strings.TrimSpace(base.Source)
    if !platformEnabled {
        source := "平台不变量已关闭"
        if baseSource != "" {
            source += " + " + baseSource
        }
        return Block{Text: base.Text, Source: source}
    }

    parts := []string{strings.TrimSpace(platformInvariantHead)}
    if strings.TrimSpace(base.Text) != "" {
        parts = append(parts, strings.TrimSpace(base.Text))
    }
    parts = append(parts, strings.TrimSpace(platformInvariantTail))
    source := "内置:平台不变量"
    if baseSource != "" {
        source += " + " + baseSource
    }
    return Block{Text: strings.Join(parts, "\n\n"), Source: source}
}
~~~

### 步骤 1.4：日志、注释与跑绿

本 task 是无副作用纯组装，不能为每次调用增加噪声日志；关键节点、外部调用、错误分支和成功日志由 Task 3 的 Manager.resolveDisciplineFor 统一记录。检查 platform.go 文件头、Compose 参数/返回/注意事项注释均存在，且无 fmt.Printf、print、文件 I/O 或 handoff CLI。

~~~bash
go test ./internal/discipline -run 'TestCompose' -count=1
gofmt -w internal/discipline/platform.go internal/discipline/platform_test.go
go test ./internal/discipline
~~~

预期：四个 TestCompose... 全部通过，输出 ok github.com/Xsxdot/handoff/internal/discipline。测试范围仍只有 internal/discipline。

提交：

~~~bash
git add internal/discipline/platform.go internal/discipline/platform_test.go
git commit -m "feat(discipline): add always-on platform invariant layer"
~~~
+

---

## Task 2：配置增加显式且可留痕的关闭开关

### 文件与接口

- Modify: internal/config/config.go
- Modify: internal/config/config_test.go
- Consumes: Config、Load、Save、Defaults、decodeStrict；出处 internal/config/config.go:43-139,316-394,397-420,516-540。
- Produces: Config.PlatformInvariants *bool、(*Config).PlatformInvariantsEnabled() bool；YAML 键 platform_invariants。

### 步骤 2.1：先在基线跑判据

~~~bash
go test ./internal/config -run 'TestLoadAcceptsDisciplineSection|TestDefaultsHasEmptyDisciplineMap|TestLoadRejectsUnknownKeys'
~~~

预期：基线全绿。出稿基线真实结果为 ok github.com/Xsxdot/handoff/internal/config (cached)。测试范围声明：本 task 只跑 ./internal/config；不跑全量测试。

### 步骤 2.2：写失败测试并确认红灯

在 internal/config/config_test.go 追加以下完整测试函数。它逐条断言缺失、false、true 三种结果，并检查真实 YAML 序列化边界：

~~~go
func TestPlatformInvariantsConfigRoundTripsMissingFalseAndTrue(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "config.yaml")

    missing := &config.Config{Listen: "127.0.0.1:0", Token: "t", DataDir: dir,
        StallTimeout: 2 * time.Hour}
    if missing.PlatformInvariants != nil {
        t.Fatal("未设置平台开关时字段必须保持 nil，不能把默认 true 写成配置")
    }
    if !missing.PlatformInvariantsEnabled() {
        t.Fatal("缺少 platform_invariants 时必须默认启用")
    }
    if err := config.Save(path, missing); err != nil {
        t.Fatalf("Save missing: %v", err)
    }
    raw, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("Read missing config: %v", err)
    }
    if strings.Contains(string(raw), "platform_invariants:") {
        t.Fatalf("默认启用不应伪造成显式配置：%s", raw)
    }
    gotMissing, err := config.Load(path)
    if err != nil {
        t.Fatalf("Load missing: %v", err)
    }
    if gotMissing.PlatformInvariants != nil || !gotMissing.PlatformInvariantsEnabled() {
        t.Fatalf("缺失开关读回 = %#v / enabled=%v", gotMissing.PlatformInvariants,
            gotMissing.PlatformInvariantsEnabled())
    }

    disabled := false
    missing.PlatformInvariants = &disabled
    if err := config.Save(path, missing); err != nil {
        t.Fatalf("Save false: %v", err)
    }
    raw, err = os.ReadFile(path)
    if err != nil {
        t.Fatalf("Read false config: %v", err)
    }
    if !strings.Contains(string(raw), "platform_invariants: false") {
        t.Fatalf("显式关闭必须落盘为 false：%s", raw)
    }
    gotFalse, err := config.Load(path)
    if err != nil {
        t.Fatalf("Load false: %v", err)
    }
    if gotFalse.PlatformInvariants == nil || gotFalse.PlatformInvariantsEnabled() {
        t.Fatalf("显式 false 读回 = %#v / enabled=%v", gotFalse.PlatformInvariants,
            gotFalse.PlatformInvariantsEnabled())
    }

    enabled := true
    missing.PlatformInvariants = &enabled
    if err := config.Save(path, missing); err != nil {
        t.Fatalf("Save true: %v", err)
    }
    raw, err = os.ReadFile(path)
    if err != nil {
        t.Fatalf("Read true config: %v", err)
    }
    if !strings.Contains(string(raw), "platform_invariants: true") {
        t.Fatalf("显式 true 必须可落盘：%s", raw)
    }
    gotTrue, err := config.Load(path)
    if err != nil {
        t.Fatalf("Load true: %v", err)
    }
    if gotTrue.PlatformInvariants == nil || !gotTrue.PlatformInvariantsEnabled() {
        t.Fatalf("显式 true 读回 = %#v / enabled=%v", gotTrue.PlatformInvariants,
            gotTrue.PlatformInvariantsEnabled())
    }
}
~~~

运行：

~~~bash
go test ./internal/config -run 'TestPlatformInvariantsConfigRoundTripsMissingFalseAndTrue' -count=1
~~~

预期红灯：undefined: (*config.Config).PlatformInvariantsEnabled 或 Config 无 PlatformInvariants 字段。

### 步骤 2.3：写最小实现

在 internal/config/config.go 的 Config 中紧接 Discipline 字段加入：

~~~go
    // PlatformInvariants 是平台底线恒在层的显式开关。
    //
    // nil 表示未配置，PlatformInvariantsEnabled 将其解释为 true；非 nil 的 false
    // 才是关闭平台不变量的明确机器级选择。使用指针并保留 omitempty，是为了同时
    // 区分旧配置的「没有这个键」与用户明确写入的 false，并避免默认值污染旧配置。
    PlatformInvariants *bool `yaml:"platform_invariants,omitempty"`
~~~

在同一文件 Config 定义之后加入：

~~~go
// PlatformInvariantsEnabled 返回本机是否注入平台不变量恒在层。
//
// 参数：无；接收 nil Config 时按默认启用处理，便于启动早期与测试构造使用。
// 返回：配置缺失或显式 true 时为 true；只有显式 false 时为 false。
// 注意：调用方不要直接解引用 PlatformInvariants，否则旧配置会把默认底线误关掉。
func (c *Config) PlatformInvariantsEnabled() bool {
    if c == nil || c.PlatformInvariants == nil {
        return true
    }
    return *c.PlatformInvariants
}
~~~

newDefaultConfig 不设置 PlatformInvariants，保持 nil；不要把 true 指针放入默认字面量。这样 Defaults 与首次 Load 的 YAML 不会写入新键，旧版本不会因正常默认配置读到未知键。

在 Load 的 cfg.validate() 成功之后、已有 stripped 回写逻辑之前加入显式关闭日志：

~~~go
    if !cfg.PlatformInvariantsEnabled() {
        log().Warn("平台不变量已通过显式配置关闭", "path", path, "key", "platform_invariants")
    }
~~~

在 decodeStrict 的支持键清单中，把末尾的 discipline{<executor>: <文件名>} 改为 discipline{<executor>: <文件名>}/platform_invariants。完整返回语句为：

~~~go
        return fmt.Errorf("配置包含未知字段（支持: listen/token/datadir/repo_root/path_dirs/proxy/env_forward/stalltimeout/relay{url,credential,node}/targets{addr,user,token,relay,credential,node}/ledger{enabled,dsn}/approver{executor,model,timeout,blacklist}/executor{default,model}/terminal{auto}/sync{auto}/proc_fence/env{<agent>: <文件名>}/discipline{<executor>: <文件名>}/platform_invariants）: %w；旧版 access_key/secret_key 等键已废弃，请删除未知键或升级配置", err)
~~~

### 步骤 2.4：日志与注释检查

- Load 的显式关闭分支必须使用现有 log()/slog，不能 fmt.Printf 或打印 token。
- 新字段注释必须说明 nil/false 两态为何不能合并；方法注释必须写参数、返回和注意事项。
- 不修改 swapConf 的 map 深拷贝逻辑：该字段是不可变指针快照；discipline mapping 写入不应改变平台开关。
- 不在 PlatformInvariantsEnabled 这种高频纯读取方法内打日志；显式关闭只在配置加载时告警，manager 在每次组装时记录结构化状态。

### 步骤 2.5：跑绿并提交

~~~bash
gofmt -w internal/config/config.go internal/config/config_test.go
go test ./internal/config -run 'TestPlatformInvariantsConfigRoundTripsMissingFalseAndTrue|TestLoadAcceptsDisciplineSection|TestDefaultsHasEmptyDisciplineMap|TestLoadRejectsUnknownKeys' -count=1
go test ./internal/config
~~~

预期：新增三态 roundtrip 与既有配置测试全部通过，输出 ok github.com/Xsxdot/handoff/internal/config。测试范围只触及 internal/config。

提交：

~~~bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add explicit platform invariant switch"
~~~
+

---

## Task 3：manager 唯一接线、来源回显与真实 prompt 边界

### 文件与接口

- Modify: internal/agentd/manager.go
- Modify: internal/agentd/manager_test.go
- Consumes: discipline.Compose、(*config.Config).PlatformInvariantsEnabled、Resolver.For/ByName、executor.StartReq、turn.RenderPrompt。
- Produces: resolveDisciplineFor 的既有签名不变：func (m *Manager) resolveDisciplineFor(name, execName string) (discipline.Block, error)；三个调用点继续消费组装后的 Block，无需新增跨包字段。

### 步骤 3.1：先在基线跑判据

~~~bash
go test ./internal/agentd -run 'TestResolveDisciplineForPrefersName|TestDispatchPassesDisciplineAndRecordsSource|TestDispatchNamedDisciplineInjectsNamedBlock|TestDispatchUnnamedDisciplineUnchanged' -count=1
~~~

预期：基线通过；出稿时真实结果为 ok github.com/Xsxdot/handoff/internal/agentd 0.679s。测试范围声明：本 task 只跑上述 internal/agentd 白盒回归；不跑全量测试。

### 步骤 3.2：先写失败/需更新的测试

在 internal/agentd/manager_test.go 的 import 中加入：

~~~go
    "github.com/Xsxdot/handoff/internal/executor/turn"
~~~

把现有 TestDispatchPassesDisciplineAndRecordsSource 完整替换为以下内容。它同时断言 Task 投影、progress 来源和 turn.RenderPrompt 真实模板边界：

~~~go
func TestDispatchPassesDisciplineAndRecordsSource(t *testing.T) {
    ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
    m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"codex": ad}, "codex")
    repo := initTestRepo(t)
    pid := registerTestProject(t, m, repo)
    task, err := m.Dispatch(context.Background(), DispatchReq{ProjectID: pid, Prompt: "做点事"})
    if err != nil {
        t.Fatalf("Dispatch: %v", err)
    }
    start := ad.lastStartReq()
    if !strings.Contains(start.Discipline, "在本会话内自己逐 task 实现") {
        t.Errorf("StartReq.Discipline 没拿到单上下文版，实得前 80 字：%.80s", start.Discipline)
    }
    if !strings.Contains(start.Discipline, "# 平台不变量（恒在层）") ||
        !strings.Contains(start.Discipline, "收口前逐条自查：") {
        t.Fatalf("StartReq.Discipline 缺平台头尾：%.160s", start.Discipline)
    }
    if strings.Count(start.Discipline, "# 平台不变量（恒在层）") != 1 ||
        strings.Count(start.Discipline, "收口前逐条自查：") != 1 {
        t.Fatalf("平台头尾必须各一次：头=%d 尾=%d",
            strings.Count(start.Discipline, "# 平台不变量（恒在层）"),
            strings.Count(start.Discipline, "收口前逐条自查："))
    }
    if task.Discipline != "内置:平台不变量 + 内置:single-context" {
        t.Fatalf("task.Discipline = %q", task.Discipline)
    }
    rendered, err := turn.RenderPrompt(task.ID, "计划正文", start.Discipline)
    if err != nil {
        t.Fatalf("RenderPrompt: %v", err)
    }
    if strings.Count(rendered, "# 平台不变量（恒在层）") != 1 ||
        strings.Count(rendered, "收口前逐条自查：") != 1 {
        t.Fatalf("真实 prompt 边界重复或丢失：头=%d 尾=%d",
            strings.Count(rendered, "# 平台不变量（恒在层）"),
            strings.Count(rendered, "收口前逐条自查："))
    }
    evs, err := st.EventsFromAsc(task.ID, 0, 100)
    if err != nil {
        t.Fatalf("读取事件: %v", err)
    }
    var found bool
    for _, e := range evs {
        if e.Type == proto.EventTypeProgress &&
            strings.Contains(string(e.Payload), "纪律块: 内置:平台不变量 + 内置:single-context") {
            found = true
        }
    }
    if !found {
        t.Error("事件流里没有多来源纪律块回显")
    }
}
~~~

把现有 TestDispatchNamedDisciplineInjectsNamedBlock 中的来源断言替换为：

~~~go
    if task.Discipline != "内置:平台不变量 + 内置:review" {
        t.Fatalf("来源标注应同时包含平台层和 review，实得 %q", task.Discipline)
    }
~~~

把现有 TestDispatchUnnamedDisciplineUnchanged 改为精确断言：

~~~go
    if task.Discipline != "内置:平台不变量 + 内置:single-context" {
        t.Fatalf("不点名时来源 = %q", task.Discipline)
    }
~~~

把现有 TestResolveDisciplineForPrefersName 完整替换为：

~~~go
func TestResolveDisciplineForPrefersName(t *testing.T) {
    m := compensateOnlyManager(t)

    named, err := m.resolveDisciplineFor(discipline.NameReview, "codex")
    if err != nil {
        t.Fatalf("有名字: %v", err)
    }
    if named.Source != "内置:平台不变量 + 内置:review" {
        t.Fatalf("有名字应走 ByName 后再组装平台层，实得 %q", named.Source)
    }
    if !strings.Contains(named.Text, "只读，不写") ||
        !strings.Contains(named.Text, "# 平台不变量（恒在层）") {
        t.Fatalf("有名字的正文缺 review 或平台层：%.160s", named.Text)
    }

    fallback, err := m.resolveDisciplineFor("", "codex")
    if err != nil {
        t.Fatalf("无名字: %v", err)
    }
    if fallback.Source != "内置:平台不变量 + 内置:single-context" {
        t.Fatalf("无名字应走 For 后再组装平台层，实得 %q", fallback.Source)
    }
}
~~~

在 manager_test.go 追加以下关闭出口回归。它复用既有真实 git 仓库与 chanAdapter harness，断言显式关闭只移除平台正文，不会把 executor 默认层一并吞掉：

~~~go
func TestDispatchExplicitlyDisablesPlatformInvariantsWithSourceEcho(t *testing.T) {
    ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
    m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"codex": ad}, "codex")
    disabled := false
    m.cfg.PlatformInvariants = &disabled
    repo := initTestRepo(t)
    pid := registerTestProject(t, m, repo)

    task, err := m.Dispatch(context.Background(), DispatchReq{
        ProjectID: pid, Prompt: "做点事", Executor: "codex",
    })
    if err != nil {
        t.Fatalf("Dispatch: %v", err)
    }
    wantSource := "平台不变量已关闭 + 内置:single-context"
    if task.Discipline != wantSource {
        t.Fatalf("显式关闭后的 task.Discipline = %q, want %q", task.Discipline, wantSource)
    }
    got := ad.lastStartReq().Discipline
    if strings.Contains(got, "# 平台不变量（恒在层）") ||
        strings.Contains(got, "收口前逐条自查：") {
        t.Fatalf("显式关闭后仍有平台正文：%.160s", got)
    }
    if !strings.Contains(got, "在本会话内自己逐 task 实现") {
        t.Fatalf("显式关闭不应移除 executor 默认纪律：%.160s", got)
    }
}
~~~

先运行：

~~~bash
go test ./internal/agentd -run 'TestResolveDisciplineForPrefersName|TestDispatchPassesDisciplineAndRecordsSource|TestDispatchNamedDisciplineInjectsNamedBlock|TestDispatchUnnamedDisciplineUnchanged|TestDispatchExplicitlyDisablesPlatformInvariantsWithSourceEcho' -count=1
~~~

预期红灯是来源仍为单值，例如 task.Discipline = "内置:single-context"，或新测试因 PlatformInvariants 尚未存在而编译失败；两者都证明测试钉在旧行为上。

### 步骤 3.3：修改唯一组装入口

在 internal/agentd/manager.go:351-364 用以下完整函数替换现状。解析失败不组装、不改变既有错误语义；成功后只在这里读取活配置并调用 discipline.Compose：

~~~go
// resolveDisciplineFor 按「有名字用名字、无名字按 executor 兜底」解析并组装纪律块。
//
// 参数：name 是角色名（空=不点名）；execName 是执行者名。
// 返回：解析后的角色/执行者正文加平台恒在层；错误保持 Resolver 原因。
// 为什么放在这里：Dispatch、resumeForContinue、ResumeTask 三个调用点都经过此函数，
// 分开拼接会让首回合与 continue/重启恢复得到不同纪律。
func (m *Manager) resolveDisciplineFor(name, execName string) (discipline.Block, error) {
    platformEnabled := m.conf().PlatformInvariantsEnabled()
    m.log.Info("开始解析纪律块", "name", name, "executor", execName,
        "platform_invariants", platformEnabled)

    var (
        base discipline.Block
        err  error
    )
    if strings.TrimSpace(name) != "" {
        base, err = m.discipline.ByName(name, execName)
    } else {
        base, err = m.discipline.For(execName)
    }
    if err != nil {
        m.log.Error("纪律块解析失败", "name", name, "executor", execName,
            "platform_invariants", platformEnabled, "cause", err)
        return discipline.Block{}, err
    }
    m.log.Info("纪律块基础来源解析完成", "name", name, "executor", execName,
        "source", base.Source, "bytes", len(base.Text))

    assembled := discipline.Compose(base, platformEnabled)
    m.log.Info("纪律块组装完成", "name", name, "executor", execName,
        "platform_invariants", platformEnabled, "source", assembled.Source,
        "bytes", len(assembled.Text))
    return assembled, nil
}
~~~

不改三处既有调用方的字段投影：

~~~go
// Dispatch：discBlock 已经是 Compose 后的 Block。
Task.Discipline = discBlock.Source
StartReq{Discipline: discBlock.Text}

// resumeForContinue / ResumeTask：
ResumeReq{Discipline: discBlock.Text}
~~~

不改 internal/executor/{codex,grok,opencode,claudecode}：基线契约 StartReq.Discipline 的第三个参数已经由四家传给 turn.RenderPrompt，出处 internal/executor/executor.go:71-74、internal/executor/turn/protocol.go:48-89、internal/executor/{codex,grok}/adapter.go:230/170、internal/executor/{opencode,claudecode}/taskenv.go:190/200。Codex 的 developerInstructionsFor 复用 turn.ProtocolRules，平台层正文由 StartReq.Discipline 进入首条 prompt，不追加 trailer 文案。

### 步骤 3.4：日志、注释与缺陷族对抗检查

执行者必须逐条确认：

- 入口日志带 name、executor、platform_invariants；Resolver 前后有日志；Resolver 错误有 cause；组装成功有 source 与正文字节数但不打正文。
- Dispatch 既有 errDisciplineResolveFailed 包装和 progress 回显保留；关闭时 progress 不能因为 source 非空而被跳过。
- 三个恢复调用点不再直接调用 For/ByName；rg -n 'discipline\.(For|ByName)' internal/agentd/manager.go 只能命中 resolveDisciplineFor 的两行。
- 关闭开关只影响平台层：点名 review 仍包含「只读，不写」；executor 显式空串在平台启用时仍得到平台头尾。
- 默认/覆盖/错误/恢复四类缺陷：未知角色仍拒发；覆盖文件仍优先；缺失文件仍报错；热/冷恢复的既有错误分支不被改写。
- 序列化链路：Task.Discipline 与 progress payload 使用同一 assembled.Source；StartReq.Discipline 与 prompt 使用同一 assembled.Text，禁止手搭第二份 source。

### 步骤 3.5：跑绿并提交

~~~bash
gofmt -w internal/agentd/manager.go internal/agentd/manager_test.go
go test ./internal/agentd -run 'TestResolveDisciplineForPrefersName|TestDispatchPassesDisciplineAndRecordsSource|TestDispatchNamedDisciplineInjectsNamedBlock|TestDispatchUnnamedDisciplineUnchanged|TestDispatchExplicitlyDisablesPlatformInvariantsWithSourceEcho' -count=1
go test ./internal/agentd
~~~

预期：新增与更新回归全部通过，输出 ok github.com/Xsxdot/handoff/internal/agentd。本 task 测试范围只触及 internal/agentd。

提交：

~~~bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(agentd): compose platform invariants at discipline boundary"
~~~

---

## 4. 整卡验收（协调者执行，不派发）

以下步骤驱动派发系统自身，按纪律由协调者执行，不交给实现 executor。协调者必须在三个 task 提交均存在后执行，并把命令原文、真实输出和分支提交号写入卡台账。

### 4.1 定向与整分支测试

~~~bash
go test ./internal/discipline ./internal/config ./internal/executor/turn ./internal/agentd
go test ./internal/executor/codex -run 'TestStartInjectsDisciplineIntoPrompt|TestThreadStartCarriesDeveloperInstructions|TestThreadResumeCarriesDeveloperInstructions'
go test ./internal/executor/grok -run 'TestStartInjectsDisciplineIntoPrompt'
go test ./internal/executor/opencode -run 'TestWriteTaskEnvInjectsDiscipline'
go test ./internal/executor/claudecode -run 'TestWriteTaskEnvInjectsDiscipline'
~~~

预期：前四个包全绿；四家 adapter 的既有 discipline 测试全绿。若 claude 测试再次命中 socket 路径或 /tmp 只读失败，记录原文与环境限制，不把它写成 B207 通过。完整仓库测试不是任何单个 task 的判据，协调者可另跑 go test ./...。

### 4.2 四组合白盒判据

协调者逐项核对：

| 输入 | 预期正文 | 预期 Source |
|---|---|---|
| 点名且覆盖文件存在 | 平台头 + 覆盖正文 + 平台尾 | 内置:平台不变量 + 配置:<name> |
| 点名且退回内置 | 平台头 + 内置角色正文 + 平台尾 | 内置:平台不变量 + 内置:<role> |
| 未点名 executor 默认 | 平台头 + executor 默认正文 + 平台尾 | 内置:平台不变量 + 内置:<tier> |
| 未点名且 executor 映射显式空串 | 平台头 + 平台尾 | 内置:平台不变量 |

再核对显式关闭：platform_invariants: false 时 base 仍按原选择，只返回 平台不变量已关闭 + <base.Source>，正文不含平台头/尾。

### 4.3 真机对照实验：尾部自查是否有效

由协调者在同一机器、同一张卡、同一模板、同一角色纪律下各派一轮，只改变平台层尾部自查是否注入：

1. 用当前实现构建「尾部存在」版本，确认派发回显同时列出平台来源与角色来源。
2. 在隔离临时构建目录中只把 platformInvariantTail 替换为空字符串，构建「尾部不存在」版本；不提交临时差异，不改变头部、角色正文、计划、executor、模型或工作区。
3. 两轮执行同一份包含一个成功命令、一个失败命令、一次台账追加和一次最终收口的最小计划；均等待真实 trailer 或失败结果。
4. 对比 Task.Discipline/progress 来源、台账追加时点、失败命令是否保留原始输出、是否碰过 handoff CLI/起过 executor、最后 trailer 是否存在。

判定：

- 尾部存在版本通过而尾部不存在版本失败：保留 platformInvariantTail，把两轮原始输出与差异写入验收台账。
- 两轮全部判据无差异：按 spec 实现决定第 4 条删除尾部注入，并记录删除原因、两轮原始输出和新的测试结果；不得为了保留设计而忽略无差异结果。
- 任一轮没有真实结果、executor 断连、或无法保证只改变尾部变量：写「未验证」，不得写 pass。

### 4.4 四项计划自审

- 缺陷族：覆盖优先级、显式空串、nil/default、错误 fail-closed、恢复一致性、日志可观测性、协议重复、来源顺序、旧配置兼容均有测试或验收结论。
- 序列化边界：YAML 缺失/false roundtrip、Compose、Task/progress/StartReq、RenderPrompt 均在文件清单与测试中点名；nil 与 false 不用零值混淆。
- 上下文预算：Task 1 两文件、Task 2 两文件、Task 3 两文件；adapter 只作基线消费核对，不扩大实现范围。
- 类型标注：配置使用 *bool 表达缺失/显式 false；Block.Source 是多来源字符串；真实 prompt 仍消费 string，没有隐式类型转换。

## 5. Spec 覆盖、自检与占位符扫描

### 5.1 用户故事归属

| spec 用户故事 | 归属 |
|---|---|
| 1. 点名旧块也不能派发/起 executor | Task 1 恒在正文 + Task 3 唯一组装入口 + 4.2/4.3 |
| 2. 断点前台账事实可恢复 | Task 1 平台正文第 3 条；Task 3 保证所有派发/恢复路径都注入；4.3 检查真实时点 |
| 3. 回显同时显示多个来源 | Task 1 Source 组装 + Task 3 Task/progress 回归 |
| 4. 显式关闭且留痕 | Task 2 YAML 指针 roundtrip + Task 3 关闭派发回归 + 4.2 |

### 5.2 占位符扫描声明

本计划没有占位标记、未定义的错误处理或跨 task 省略。所有测试代码均已贴出；已有 adapter harness 被直接点名复用，断言命令与测试函数名均列全。真机实验的隔离构建、变量控制和 pass/fail 判据已逐条写明，不能以「以后补实验」代替。

### 5.3 跨 task 类型/签名一致性

- Task 1 先定义 Compose(Block, bool) Block，Task 3 逐字消费该签名。
- Task 2 先定义 PlatformInvariantsEnabled() bool，Task 3 逐字消费该签名。
- Task 3 保持 resolveDisciplineFor(name, execName) (discipline.Block, error) 与所有既有调用点不变，只改变返回 Block 的来源与正文。
- StartReq.Discipline string、RenderPrompt(taskID, planContent, disciplineBlock string) 与现状逐字对齐；不增加 adapter 参数。
