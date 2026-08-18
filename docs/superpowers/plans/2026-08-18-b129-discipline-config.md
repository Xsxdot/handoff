# 纪律配置与按执行者注入（B129）+ B117 + B118 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 handoff 按执行器能力自动注入正确的执行纪律块（不再人工粘贴），并修掉 codex 路径上的两处硬伤：审批裁决晚于回合边界时被静默丢弃、沙箱排除 TMPDIR 致 `go test` 直接失败。

**Architecture:** 新增 `internal/discipline` 包，逐字镜像 `internal/envfile` 的 Resolver 形状（`Dir`/`NewResolver`/`For`/`Preflight`/纯文件名校验）。内置 A/B 两版纪律块经 `go:embed` 随二进制分发，默认按执行器有无 subagent 机制映射，config 的 `discipline` 段可覆盖。纪律块经 `StartReq.Discipline` 流到各 adapter，拼进 `turn.RenderPrompt` 的产物；codex 额外把「三条铁律 + 纪律块」放进 `developerInstructions` 常驻。B117 在 `consultApprover` 的边界守卫里补一次 `RespondPermission("reject")` 与一条可见事件；B118 给 codex 沙箱开任务专属 tmp 并经 env 通道把 Go 的临时目录指过去。

**Tech Stack:** Go 1.x、`log/slog`、`go:embed`、`text/template`、`gopkg.in/yaml.v3`（严格未知字段）

## Global Constraints

- **协议层三条铁律不可配置**：`promptTemplate` 里的提问纪律 / 收尾纪律 / 不切分支必须保留，纪律块只能追加、不能覆盖。它是 `turn.ParseTrailer` 与 `turn.NoTrailerResult` 的前提，也是 B74「假完成」防线的上游。
- **首条用户消息四家逐字同构**：codex 的 `developerInstructions` 是额外一份，不是替代。跨 executor 评测要求首条消息可比。
- **任务专属 tmp 必须在工作区之外**：目录取 `<TaskDir>/tmp`（即 `<DataDir>/tasks/<id>/tmp`）。指进仓库会让一族「非 git 目录应报错」的用例假红（08-17 B119 实测命中 6 条，判据是「实得 nil」+ 路径带 `.gotmp/`）。
- **日志用 `log/slog`（`a.log` / `m.log` / `r.log`），禁止 `fmt.Printf`**。值可能含凭据的一律只打键名（`envfile/resolver.go:64` 同款纪律）。
- **未登记 executor 的默认档位取单上下文版**：单上下文版给有 subagent 的执行器只是没用上能力（B93 实测仍 6/6），subagent 版给没有 subagent 的执行器是灾难性的（9 次推动只到 3/6 且卡死）。
- **每个 task 完成即 commit**，提交信息用各 Task「Commit」步骤给定的原文。

## File Structure

| 文件 | 责任 |
|---|---|
| `internal/discipline/discipline.go`（新） | `Block` 类型、内置两版 `go:embed`、`defaultTier` 能力表 |
| `internal/discipline/resolver.go`（新） | `Dir` / `NewResolver` / `For` / `Preflight` / `resolvePath` |
| `internal/discipline/builtin/subagent.md`（新） | A 版纪律块原文 |
| `internal/discipline/builtin/single-context.md`（新） | B 版纪律块原文 |
| `internal/config/config.go` | 加 `Discipline map[string]string`、默认空 map、未知键错误文案 |
| `internal/executor/turn/protocol.go` | `RenderPrompt` 加参数、导出 `ProtocolRules` |
| `internal/executor/executor.go` / `resume.go` | `StartReq.Discipline` / `ResumeReq.Discipline` |
| `internal/proto/proto.go` | `Task.Discipline`、`EventTypeApprovalDropped` |
| `internal/agentd/manager.go` | Resolver 接线、dispatch 解析透传、回显、B117 修复 |
| `internal/executor/{claudecode,codex,grok,opencode}` | 传 `Discipline` 给 `RenderPrompt` |
| `internal/executor/codex/adapter.go` / `resume.go` | `developerInstructions`、任务专属 tmp |
| `cmd/dispatch.go` | stderr 回显实际注入的档位 |

---

### Task 1: `internal/discipline` 包 —— 内置两版、能力分档、三档覆盖

**Files:**
- Create: `internal/discipline/discipline.go`
- Create: `internal/discipline/resolver.go`
- Create: `internal/discipline/builtin/subagent.md`
- Create: `internal/discipline/builtin/single-context.md`
- Test: `internal/discipline/resolver_test.go`

**Interfaces:**
- Consumes: 无（本包是叶子）
- Produces: `discipline.Dir(dataDir string) string`、`discipline.NewResolver(dir string, m map[string]string, log *slog.Logger) *Resolver`、`(*Resolver).For(executor string) (Block, error)`、`(*Resolver).Preflight()`、`type Block struct{ Text, Source string }`、常量 `TierSubagent = "subagent"` / `TierSingleContext = "single-context"`

- [ ] **Step 1: 落两份内置纪律块原文**

`internal/discipline/builtin/subagent.md` 与 `internal/discipline/builtin/single-context.md` 的**逐字原文**见本计划末尾「附录 A：内置纪律块原文」。原样复制，不要改写、不要精简、不要重排编号——B93 的 7 组探针是针对这两份文本测的。

- [ ] **Step 2: 写失败的测试**

```go
// resolver_test.go
package discipline

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// 第三档：未配置的 executor 拿内置默认，且按能力分档。
func TestForUnconfiguredUsesBuiltinByTier(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, quietLog())
	for _, c := range []struct{ exec, wantSource, wantSubstr string }{
		{"opencode", "内置:" + TierSubagent, "用你自己的 subagent 机制"},
		{"claude", "内置:" + TierSubagent, "用你自己的 subagent 机制"},
		{"codex", "内置:" + TierSingleContext, "在本会话内自己逐 task 实现"},
		{"grok", "内置:" + TierSingleContext, "在本会话内自己逐 task 实现"},
	} {
		b, err := r.For(c.exec)
		if err != nil {
			t.Fatalf("%s: 意外错误 %v", c.exec, err)
		}
		if b.Source != c.wantSource {
			t.Errorf("%s: Source = %q, want %q", c.exec, b.Source, c.wantSource)
		}
		if !strings.Contains(b.Text, c.wantSubstr) {
			t.Errorf("%s: 正文未含 %q", c.exec, c.wantSubstr)
		}
	}
}

// 未登记的 executor 必须保守取单上下文版——subagent 版派错的代价是实测过的。
func TestForUnknownExecutorFallsBackToSingleContext(t *testing.T) {
	r := NewResolver(t.TempDir(), nil, quietLog())
	b, err := r.For("某个还没写适配器的执行器")
	if err != nil {
		t.Fatalf("意外错误 %v", err)
	}
	if b.Source != "内置:"+TierSingleContext {
		t.Fatalf("Source = %q，未登记的必须落单上下文版", b.Source)
	}
}

// 第一档：配置了文件名就读那个文件。
func TestForConfiguredReadsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.md"), []byte("我自己的纪律"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(dir, map[string]string{"codex": "mine.md"}, quietLog())
	b, err := r.For("codex")
	if err != nil {
		t.Fatalf("意外错误 %v", err)
	}
	if b.Text != "我自己的纪律" {
		t.Errorf("Text = %q", b.Text)
	}
	if b.Source != "配置:mine.md" {
		t.Errorf("Source = %q, want 配置:mine.md", b.Source)
	}
}

// 第二档：显式空串 = 关闭注入，且不能退回内置默认。
func TestForEmptyValueDisablesInjection(t *testing.T) {
	r := NewResolver(t.TempDir(), map[string]string{"codex": "  "}, quietLog())
	b, err := r.For("codex")
	if err != nil {
		t.Fatalf("意外错误 %v", err)
	}
	if b.Text != "" || b.Source != "" {
		t.Fatalf("显式关闭却拿到了纪律块：%+v", b)
	}
}

// 路径穿越必须被拒。
func TestForRejectsPathSeparator(t *testing.T) {
	for _, bad := range []string{"../etc/passwd", "sub/dir.md", "."} {
		r := NewResolver(t.TempDir(), map[string]string{"codex": bad}, quietLog())
		if _, err := r.For("codex"); err == nil {
			t.Errorf("%q 应被拒", bad)
		}
	}
}

// 配置指向的文件不存在 → 报错（不静默退回内置默认：用户明确配了，
// 悄悄换成别的比失败更危险）。
func TestForMissingFileErrors(t *testing.T) {
	r := NewResolver(t.TempDir(), map[string]string{"codex": "nope.md"}, quietLog())
	if _, err := r.For("codex"); err == nil {
		t.Fatal("文件缺失应报错")
	}
}

// Preflight 不阻断、不 panic，坏配置只应留在日志里。
func TestPreflightDoesNotPanicOnBadConfig(t *testing.T) {
	r := NewResolver(t.TempDir(), map[string]string{"codex": "nope.md", "grok": "../x"}, quietLog())
	r.Preflight()
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/discipline/ -run 'TestFor|TestPreflight' -v`
Expected: FAIL —— `undefined: NewResolver`（包尚不存在）

- [ ] **Step 4: 写 `discipline.go`**

```go
// discipline.go —— 执行纪律块的内置版本与能力分档。
//
// 职责：
//   - 内置 A/B 两版纪律块（go:embed），随二进制分发
//   - defaultTier：executor 名 → 内置档位的能力表
//   - Block：一次解析的产物（正文 + 人可读来源标注）
//
// 边界：
//   - 不理解纪律内容、不校验语义；不负责注入进 prompt（交各 adapter）
package discipline

import _ "embed"

//go:embed builtin/subagent.md
var builtinSubagent string

//go:embed builtin/single-context.md
var builtinSingleContext string

// 内置档位名。
const (
	TierSubagent      = "subagent"       // 有 subagent 机制的执行器（opencode / claude）
	TierSingleContext = "single-context" // 无 subagent 机制的执行器（codex / grok）
)

// Block 是一次纪律解析的产物。
//
// Source 是人可读的来源标注（「内置:single-context」/「配置:my-rules.md」），
// 供派发时回显给协调者：配置化把纪律块从 plan 文件里拿走之后，写 plan 的人
// 再也看不见它，回显是唯一的补偿（B126-A）。
type Block struct {
	Text   string // 纪律块正文；空表示不注入
	Source string // 人可读来源标注；Text 为空时同为空
}

// defaultTier 是「无配置时按执行器有没有 subagent 机制选内置版本」的能力表。
// 加新 executor 时加一行。
var defaultTier = map[string]string{
	"opencode": TierSubagent,
	"claude":   TierSubagent,
	"codex":    TierSingleContext,
	"grok":     TierSingleContext,
}

// builtinFor 返回该 executor 的内置纪律块。
//
// 未登记的 executor 一律取单上下文版，这个保守方向是刻意的：单上下文版给
// 有 subagent 的执行器只是没用上能力（B93 实测仍 6/6 全绿），而 subagent 版
// 给没有 subagent 的执行器是灾难性的——它会把自己当协调者、每完成一个工作
// 单元就交还控制权，同一份 6-task plan 从「0 推动 26 分钟跑完」退化成
// 「9 次人工推动只到 3/6 且最后卡死」。
func builtinFor(executor string) Block {
	if defaultTier[executor] == TierSubagent {
		return Block{Text: builtinSubagent, Source: "内置:" + TierSubagent}
	}
	return Block{Text: builtinSingleContext, Source: "内置:" + TierSingleContext}
}
```

- [ ] **Step 5: 写 `resolver.go`**

```go
// resolver.go —— 纪律块文件的定位、读盘与档位裁决。
//
// 职责：
//   - Dir：收口 <DataDir>/discipline 的目录布局知识，避免各调用方自己拼路径后漂移
//   - Resolver.For：按 executor 名裁出该注入哪块纪律（配置 > 显式关闭 > 内置默认）
//   - Resolver.Preflight：agentd 启动时把坏文件暴露在启动日志里
//
// 边界：
//   - 不理解纪律内容、不注入进程（交各 adapter）、不缓存（同 envfile：改完下个任务即生效）
package discipline

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// maxBlockSize 是纪律块文件大小上限，与 envfile 同为 64KiB——
// 防止误配一个二进制文件后把一堆垃圾塞进模型上下文。
const maxBlockSize = 64 << 10

// Dir 返回纪律块文件目录（<dataDir>/discipline）。
//
// 目录布局知识只此一处：manager 与 agentd 各自构造 Resolver，
// 若各拼各的路径，日后改布局必然漏改一处（envfile.Dir 同款理由）。
func Dir(dataDir string) string { return filepath.Join(dataDir, "discipline") }

// Resolver 按 executor 名裁出该次派发要注入的纪律块。
//
// 无状态：每次 For 都重新读盘，因此多个实例之间不会发散。
type Resolver struct {
	dir string            // 纪律块文件目录
	m   map[string]string // executor 名 → 文件名（纯文件名，不含路径）
	log *slog.Logger
}

// NewResolver 构造 Resolver。
//
// 参数：
//   - dir: 纪律块文件目录，通常取 Dir(cfg.DataDir)
//   - m: executor 名 → 文件名映射（取自 config 的 discipline 段）；nil 视为空映射
//   - log: 日志入口；nil 时退回 slog.Default()
func NewResolver(dir string, m map[string]string, log *slog.Logger) *Resolver {
	if log == nil {
		log = slog.Default()
	}
	if m == nil {
		m = map[string]string{}
	}
	return &Resolver{dir: dir, m: m, log: log}
}

// For 返回该 executor 派发时应注入的纪律块。
//
// 参数：
//   - executor: 执行者名（如 codex）
//
// 返回：
//   - 三档语义见下；文件名非法 / 读不到 / 超限时返回错误，错误文本带完整路径
//
// 三档语义（第三档是**与 envfile 刻意的偏离**）：
//
//	配置里有非空值   → 读 <dir>/<值>
//	配置里显式给空串 → 关闭注入，返回零值 Block
//	配置里没这个 key → 用内置默认（envfile 在这一档是「不注入」）
//
// 为什么第三档不同：env 的内容是机器特有的（代理地址、私有 registry），
// 猜错不如不猜；纪律块的内容是 handoff 通用的，不给默认等于让用户退回人工
// 粘贴，而选错档位的代价已被实测（见 builtinFor）。
//
// 为什么配置指向的文件缺失是错误而不是退回内置：用户明确配了一份，
// 悄悄换成另一份比失败更危险——他会以为跑的是自己那套纪律。
func (r *Resolver) For(executor string) (Block, error) {
	raw, configured := r.m[executor]
	name := strings.TrimSpace(raw)
	if !configured {
		b := builtinFor(executor)
		r.log.Info("executor 未配置纪律块，用内置默认", "executor", executor, "source", b.Source)
		return b, nil
	}
	if name == "" {
		r.log.Info("executor 显式关闭纪律块注入", "executor", executor)
		return Block{}, nil
	}
	path, err := r.resolvePath(name)
	if err != nil {
		r.log.Error("纪律块文件名非法", "executor", executor, "name", name, "cause", err)
		return Block{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		r.log.Error("纪律块文件不可读", "executor", executor, "path", path, "cause", err)
		return Block{}, fmt.Errorf("读取纪律块文件 %s: %w", path, err)
	}
	if fi.Size() > maxBlockSize {
		r.log.Error("纪律块文件超限", "executor", executor, "path", path, "bytes", fi.Size())
		return Block{}, fmt.Errorf("纪律块文件 %s 超过 %d 字节上限（实际 %d）", path, maxBlockSize, fi.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		r.log.Error("读取纪律块文件失败", "executor", executor, "path", path, "cause", err)
		return Block{}, fmt.Errorf("读取纪律块文件 %s: %w", path, err)
	}
	r.log.Info("已加载纪律块", "executor", executor, "path", path, "bytes", len(data))
	return Block{Text: string(data), Source: "配置:" + name}, nil
}

// resolvePath 把配置里的文件名换算为绝对路径，并拒绝一切非「纯文件名」的写法。
//
// 为什么只收纯文件名：一杜绝路径穿越（../../etc 之类），二保证纪律块只有一个家、
// 不会散落各处——运维找配置时只需要看一个目录（envfile.resolvePath 同款理由）。
func (r *Resolver) resolvePath(name string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("纪律块文件名 %q 不能含路径分隔符：只支持 %s 下的纯文件名", name, r.dir)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("纪律块文件名 %q 非法：只支持 %s 下的纯文件名", name, r.dir)
	}
	return filepath.Join(r.dir, name), nil
}

// Preflight 读一遍所有被引用的纪律块文件，把问题以 WARN 暴露在启动日志里。
//
// 为什么只 WARN 不阻断启动：纪律块是数据文件不是配置键，可能在 agentd 启动后
// 才创建；但完全不检查会把问题拖到第一次派发才暴露——WARN 让它在启动日志里
// 就可见，真正的拒发发生在 Dispatch（envfile.Preflight 同款理由）。
func (r *Resolver) Preflight() {
	for executor := range r.m {
		if _, err := r.For(executor); err != nil {
			r.log.Warn("纪律块预检失败（不阻断启动，派发时会拒发）", "executor", executor, "cause", err)
		}
	}
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/discipline/ -v`
Expected: PASS，7 个用例全绿

- [ ] **Step 7: 核对日志与注释覆盖**

对照检查（本 task 的代码已按此写，逐条确认没漏）：
- 三档裁决各有 Info 日志（内置默认 / 显式关闭 / 已加载配置文件）
- 四条错误分支各有 Error 日志且带 `executor` + `path` + `cause`
- `Preflight` 失败有 Warn 且写明「不阻断启动」
- 两个新文件都有文件头注释（职责 + 边界）
- `Dir` / `NewResolver` / `For` / `Preflight` / `Block` 都有导出注释
- `builtinFor` 的保守方向、`For` 第三档的偏离、`resolvePath` 的纯文件名约束各有「为什么」注释

- [ ] **Step 8: Commit**

```bash
git add internal/discipline/
git commit -m "feat(discipline): 纪律块 Resolver 与内置 A/B 两版（B129）

逐字镜像 internal/envfile 的形状：Dir/NewResolver/For/Preflight/纯文件名校验。
三档覆盖语义中第三档与 envfile 刻意不同——未配置时用内置默认而非不注入，
因 env 内容机器特有而纪律块内容 handoff 通用。未登记 executor 保守取单上下文版。"
```

---
### Task 2: config 加 `discipline` 段并接进 agentd

**Files:**
- Modify: `internal/config/config.go:116`（`Env` 字段之后）、`:312`（默认值字面量）、`:424`（未知字段错误文案）
- Modify: `internal/agentd/manager.go:112`（struct 字段）、`:214`（构造）
- Test: `internal/config/config_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 的 `discipline.Dir`、`discipline.NewResolver`
- Produces: `config.Config.Discipline map[string]string`（yaml 键 `discipline`）、`(*agentd.Manager).discipline *discipline.Resolver`

- [ ] **Step 1: 写失败的测试**

```go
// config_test.go 追加
func TestLoadAcceptsDisciplineSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("discipline:\n  codex: mine.md\n  grok: \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Discipline["codex"] != "mine.md" {
		t.Errorf("codex = %q, want mine.md", cfg.Discipline["codex"])
	}
	v, ok := cfg.Discipline["grok"]
	if !ok || v != "" {
		t.Errorf("grok 的显式空串必须被保留（它表示关闭注入），实得 %q ok=%v", v, ok)
	}
}

func TestDefaultConfigHasEmptyDisciplineMap(t *testing.T) {
	if c := Default(); c.Discipline == nil {
		t.Fatal("Discipline 必须初始化为空 map，与 Env 一致")
	}
}
```

> 注意：`Load` / `Default` 的实际函数名以 `internal/config/config.go` 现有导出为准；若签名不同，按现有测试文件里的调用方式改写这两个用例，**不要改生产代码去迁就测试**。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run 'Discipline' -v`
Expected: FAIL —— `cfg.Discipline undefined`

- [ ] **Step 3: 加配置字段**

`internal/config/config.go`，在 `Env map[string]string` 之后：

```go
	// Discipline 是 executor 名 → 纪律块文件名的映射：派发该 executor 的任务时，
	// 把该文件的内容作为「执行纪律」注入首回合 prompt。文件名必须是
	// <DataDir>/discipline/ 下的纯文件名（含路径分隔符会被拒绝）。
	//
	// 三档语义（**与 Env 刻意不同的是第三档**）：
	//   有非空值   → 用该文件
	//   显式空串   → 关闭注入
	//   未出现该键 → 用内置默认（Env 在这一档是「不注入」）
	//
	// 为什么第三档不同：env 内容是机器特有的，猜错不如不猜；纪律块内容是
	// handoff 通用的，不给默认等于让用户退回人工粘贴到 plan 头部（见 B129 spec §2.4）。
	Discipline map[string]string
```

`Default()` 的字面量里，`Env: map[string]string{},` 之后加：

```go
		Discipline: map[string]string{},
```

未知字段错误文案（`:424`）里，把 `env{<agent>: <文件名>}` 改为
`env{<agent>: <文件名>}/discipline{<executor>: <文件名>}`。

- [ ] **Step 4: 接进 agentd**

`internal/agentd/manager.go` 的 Manager struct，`env *envfile.Resolver` 之后：

```go
	// discipline 按 executor 名裁出该次派发要注入的纪律块（B129）。
	discipline *discipline.Resolver
```

构造处（`:214` 的 `env: envfile.NewResolver(...)` 之后同一字面量内）：

```go
		discipline:   discipline.NewResolver(discipline.Dir(cfg.DataDir), cfg.Discipline, log),
```

在 agentd 现有调用 `env.Preflight()` 的同一位置紧随其后加 `m.discipline.Preflight()`。
（用 `grep -rn "Preflight()" internal/agentd/ cmd/` 找到那个调用点；**不要新造启动钩子**。）

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/config/ ./internal/agentd/ -count=1`
Expected: PASS（agentd 包只需编译通过且既有用例不回归）

- [ ] **Step 6: 核对日志与注释覆盖**

- 新增的 `Discipline` 字段有完整注释，写明三档语义与「为什么与 Env 不同」
- 未知字段错误文案已含 `discipline`，否则用户配了会被严格解码直接拒绝且看不懂原因
- Preflight 的接线复用既有调用点，启动日志里能看到纪律块预检结果

- [ ] **Step 7: Commit**

```bash
git add internal/config/ internal/agentd/manager.go
git commit -m "feat(config,agentd): 加 discipline 配置段并接进 Manager（B129）

yaml 键 discipline，executor 名 → <DataDir>/discipline/ 下的纯文件名。
未知字段错误文案同步补上这个键，否则严格解码会拒绝且报不出原因。
Preflight 复用既有启动调用点。"
```

---

### Task 3: `turn.RenderPrompt` 接受纪律块，并导出协议铁律

**Files:**
- Modify: `internal/executor/turn/protocol.go:35-75`
- Modify: `internal/executor/claudecode/taskenv.go:150`、`internal/executor/codex/adapter.go:256`、`internal/executor/grok/adapter.go:198`、`internal/executor/opencode/taskenv.go:156`（四处调用点，本 task 一律传 `""`）
- Test: `internal/executor/turn/protocol_test.go`（追加）

**Interfaces:**
- Consumes: 无
- Produces: `turn.RenderPrompt(taskID, planContent, disciplineBlock string) (string, error)`（**签名变更：加第三个参数**）、`turn.ProtocolRules string`（协议铁律原文常量）

- [ ] **Step 1: 写失败的测试**

```go
// protocol_test.go 追加
func TestRenderPromptEmbedsDisciplineBlock(t *testing.T) {
	out, err := RenderPrompt("T1", "计划正文", "# 执行纪律\n自己逐 task 实现")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "自己逐 task 实现") {
		t.Error("纪律块正文未出现")
	}
	if !strings.Contains(out, "--- 执行纪律（先读这段，再读计划）---") {
		t.Error("纪律块小标题未出现")
	}
	// 顺序：铁律 → 纪律块 → 实现计划。顺序错了模型读到的优先级就错了。
	iRules := strings.Index(out, "提问纪律")
	iDisc := strings.Index(out, "自己逐 task 实现")
	iPlan := strings.Index(out, "--- 实现计划 ---")
	if !(iRules < iDisc && iDisc < iPlan) {
		t.Errorf("顺序错：铁律=%d 纪律块=%d 计划=%d", iRules, iDisc, iPlan)
	}
}

// 空纪律块必须渲染成与加参数之前逐字相同的产物——四个 adapter 在 Task 3
// 都还传空串，这条用例是「本次改动零行为变化」的证据。
func TestRenderPromptWithoutDisciplineHasNoMarker(t *testing.T) {
	out, err := RenderPrompt("T1", "计划正文", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "执行纪律") {
		t.Error("空纪律块不该留下任何小标题")
	}
	if !strings.Contains(out, "--- 实现计划 ---") || !strings.Contains(out, "计划正文") {
		t.Error("原有结构被破坏")
	}
}

// 导出的铁律常量必须与模板里那段逐字一致——两处漂移会让 codex 的
// developerInstructions 与首条消息说不同的话。
func TestProtocolRulesMatchesTemplate(t *testing.T) {
	out, err := RenderPrompt("T1", "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, ProtocolRules) {
		t.Fatal("ProtocolRules 与模板已漂移")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/turn/ -run 'RenderPrompt|ProtocolRules' -v`
Expected: FAIL —— `too many arguments in call to RenderPrompt` / `undefined: ProtocolRules`

- [ ] **Step 3: 改模板与签名**

`internal/executor/turn/protocol.go`，把 `promptTemplate` 拆成「铁律常量 + 模板」：

```go
// ProtocolRules 是回合制协议铁律的原文，逐字来自一期 spec §6 的落地。
//
// 为什么要单独导出：codex 的 developerInstructions 需要同一份文本作常驻指令
// （B129 spec §2.6），而模板与常量若各存一份必然漂移——漂移的后果是首回合
// 消息与常驻指令说的不是同一套协议。TestProtocolRulesMatchesTemplate 钉住这件事。
//
// 注意：内嵌的 {"ask":…}/{"branch":…} 是给模型看的协议样例，
// 与 text/template 语法不冲突（不含 {{ ），可直接放在字面文本中。
const ProtocolRules = `1. 提问纪律：任何需要人决策的问题，输出单行 JSON {"ask":"<问题>"}
   然后结束本回合。协调者的回答会作为下一条消息发给你。
   禁止自行假设，禁止用其它格式提问。
2. 收尾纪律：全部完成后必须 git add 并 commit（不要 push），
   然后输出单行 JSON：{"branch":"<分支>","commit":"<hash>","summary":"<50字内摘要>"}
   作为本回合最后一行。
3. 只在当前分支工作，不切分支、不改 git 配置。`

// promptTemplate 是任务 prompt 的骨架：标题行 + 协议铁律 + 可选纪律块 + 实现计划。
//
// 纪律块段落用 {{if}} 包住而不是无条件插入：显式关闭注入时（config 里给空串），
// 产物必须与加这个参数之前逐字相同，否则「关掉配置」就不是真的关掉。
const promptTemplate = `你是 handoff 任务 {{.TaskID}} 的执行者，按下方实现计划执行。铁律：
{{.ProtocolRules}}

{{if .Discipline}}--- 执行纪律（先读这段，再读计划）---
{{.Discipline}}

{{end}}--- 实现计划 ---
{{.PlanContent}}
`

type promptData struct {
	TaskID        string
	ProtocolRules string
	Discipline    string
	PlanContent   string
}

// RenderPrompt 渲染带回合纪律的启动 prompt。
//
// 参数：
//   - taskID: 任务 ID，写入 prompt 标题行
//   - planContent: 实现计划全文（dispatch 侧已把 --prompt 附加指令拼在其后），
//     原样嵌入「实现计划」段，本函数不再二次拼接
//   - disciplineBlock: 按执行者裁出的执行纪律块（B129）；**空串表示不注入**，
//     此时产物与本参数加入之前逐字相同
//
// 返回：渲染后的 prompt 全文；模板执行失败时返回错误
func RenderPrompt(taskID, planContent, disciplineBlock string) (string, error) {
	var buf bytes.Buffer
	if err := promptTmpl.Execute(&buf, promptData{
		TaskID:        taskID,
		ProtocolRules: ProtocolRules,
		Discipline:    strings.TrimSpace(disciplineBlock),
		PlanContent:   planContent,
	}); err != nil {
		return "", fmt.Errorf("渲染 prompt 模板: %w", err)
	}
	return buf.String(), nil
}
```

> 若 `protocol.go` 尚未 import `strings`，补上。

- [ ] **Step 4: 四处调用点先传空串**

本 task 只做签名迁移、不改行为。四处各加 `, ""`：

```
internal/executor/claudecode/taskenv.go:150  turn.RenderPrompt(taskID, planContent, "")
internal/executor/codex/adapter.go:256       turn.RenderPrompt(taskID, req.PlanContent, "")
internal/executor/grok/adapter.go:198        turn.RenderPrompt(taskID, req.PlanContent, "")
internal/executor/opencode/taskenv.go:156    turn.RenderPrompt(taskID, planContent, "")
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go build ./... && go test ./internal/executor/... -count=1`
Expected: PASS，四个 executor 包的既有用例零回归

- [ ] **Step 6: 核对日志与注释覆盖**

- `ProtocolRules` 有导出注释，写明「为什么单独导出」与漂移风险
- `promptTemplate` 有注释说明纪律块段为何用 `{{if}}` 包住
- `RenderPrompt` 的参数注释写明空串语义
- 本 task 是纯渲染逻辑无 I/O、无外部调用，不加运行时日志（渲染失败已由返回错误表达，调用方各自记日志）

- [ ] **Step 7: Commit**

```bash
git add internal/executor/turn/ internal/executor/claudecode/taskenv.go internal/executor/codex/adapter.go internal/executor/grok/adapter.go internal/executor/opencode/taskenv.go
git commit -m "feat(turn): RenderPrompt 接受纪律块并导出 ProtocolRules（B129）

铁律从模板里抽成导出常量，供 codex 的 developerInstructions 复用同一份文本；
TestProtocolRulesMatchesTemplate 钉住两处不漂移。纪律块段用 {{if}} 包住，
空串时产物与改动前逐字相同。本次四个 adapter 一律传空串，零行为变化。"
```

---
### Task 4: 契约字段、dispatch 解析透传与派发回显

**Files:**
- Modify: `internal/executor/executor.go:65-70`（`StartReq`）、`internal/executor/resume.go:29-38`（`ResumeReq`）
- Modify: `internal/proto/proto.go`（`Task` 加 `Discipline`）
- Modify: `internal/agentd/manager.go:573`（env 解析处紧邻）、`:760`（`ad.Start` 调用）、`:1062` 与 `:2973`（两处 `ResumeReq` 构造）
- Modify: `cmd/dispatch.go:258-263`（stderr 回显）
- Test: `internal/agentd/manager_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 的 `(*discipline.Resolver).For`、Task 2 的 `m.discipline`
- Produces: `executor.StartReq.Discipline string`、`executor.ResumeReq.Discipline string`、`proto.Task.Discipline string`（json `discipline,omitempty`）

- [ ] **Step 1: 写失败的测试**

```go
// manager_test.go 追加
// 派发时纪律块必须流进 StartReq，且档位落进 task 与事件流——
// 配置化把纪律块从 plan 文件里拿走后，这是协调者唯一能看见它的地方（B126-A）。
func TestDispatchPassesDisciplineAndRecordsSource(t *testing.T) {
	ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"codex": ad}, "codex")
	pid := seedProjectForTest(t, m) // 复用本包既有 Dispatch 用例的建项目写法
	task, err := m.Dispatch(context.Background(), DispatchReq{ProjectID: pid, Prompt: "做点事"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got := ad.lastStartReq().Discipline; !strings.Contains(got, "在本会话内自己逐 task 实现") {
		t.Errorf("StartReq.Discipline 没拿到单上下文版，实得前 40 字：%.40s", got)
	}
	if task.Discipline != "内置:single-context" {
		t.Errorf("task.Discipline = %q", task.Discipline)
	}
	evs, err := st.EventsFromAsc(task.ID, 0, 100)
	if err != nil {
		t.Fatalf("读取事件: %v", err)
	}
	var found bool
	for _, e := range evs {
		if e.Type == proto.EventTypeProgress && strings.Contains(string(e.Payload), "纪律块") {
			found = true
		}
	}
	if !found {
		t.Error("事件流里没有纪律块回显，handoff show 将看不见档位")
	}
}
```

> **脚手架说明（已核实过的真名，照抄即可）**：`newTestManagerWithAds(t, ads, defaultName) (*Manager, *store.Store, *Hub)` 与 `chanAdapter` 都在 `internal/agentd/manager_test.go`；读事件用 `st.EventsFromAsc(taskID, fromSeq, limit)`。
>
> 两处需要你补：
> 1. `chanAdapter.Start` 当前把 `StartReq` 整个丢掉（`func (a *chanAdapter) Start(context.Context, executor.StartReq) error { return nil }`）。给它加 `lastStart executor.StartReq` 字段并在 `Start` 里加锁记录，再加一个 `lastStartReq()` 读取器（与既有 `perms`/`respondErr` 一样由 `a.mu` 保护）。
> 2. `seedProjectForTest`：本包既有 Dispatch 用例（`manager_test.go:223` 一带）已有建项目并拿 `pid` 的写法，抽成 helper 或原样内联，**不要新造一套建项目路径**。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestDispatchPassesDiscipline -v`
Expected: FAIL —— `ad.lastStartReq undefined`（chanAdapter 尚未记录 StartReq）

- [ ] **Step 3: 加契约字段**

`internal/executor/executor.go` 的 `StartReq`，`Env []string` 之后：

```go
	// Discipline 是按执行者裁出的执行纪律块正文（B129）；空表示不注入。
	// 实现方必须把它作为第三个参数传给 turn.RenderPrompt——这是对所有 adapter
	// 的统一要求，放在契约上而非各 adapter 的构造参数上，理由同 Env。
	Discipline string
```

`internal/executor/resume.go` 的 `ResumeReq`，`Env []string` 之后加同名字段：

```go
	// Discipline 同 StartReq.Discipline。恢复路径也要带：codex 的 thread/resume
	// 需要重传 developerInstructions，漏了就等于恢复后纪律消失（B18 同款教训）。
	Discipline string
```

`internal/proto/proto.go` 的 `Task`，在 `Model` 之后：

```go
	// Discipline 是本任务实际注入的纪律块来源标注（如「内置:single-context」）。
	// 该列后加、不回填、不编造——老任务为空。
	//
	// 为什么要落进 Task 而不只是日志：配置化把纪律块从 plan 文件里拿走后，
	// 写 plan 的人再也看不见它，dispatch 必须当场回显（B126-A）；
	// CLI 拿到的就是这个对象，与 RepoDirtyCount/RepoDirtyFiles 同款用途。
	Discipline string `json:"discipline,omitempty"`
```

- [ ] **Step 4: dispatch 解析并透传**

`internal/agentd/manager.go`，在 env 解析（`envKVs, err := m.env.For(execName)` 的错误处理之后）紧接着加：

```go
	// 纪律块裁决（B129）：与 env 解析同段、同理由——失败是配置问题，此刻还没有
	// 任何落库/建树副作用，拒发是干净的
	discBlock, err := m.discipline.For(execName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errDisciplineResolveFailed, err)
	}
```

在 `errEnvResolveFailed` 旁边加哨兵：

```go
	// errDisciplineResolveFailed 表示纪律块文件不可用（文件名非法/读不到/超限）。
	// 与 errEnvResolveFailed 并列，让 server 层把真因回显给协调者而不是扁平成 500。
	errDisciplineResolveFailed = errors.New("纪律块解析失败")
```

> 照抄 `errEnvResolveFailed` 在 server 层的映射写法：`grep -rn "errEnvResolveFailed" internal/agentd/` 找到它被转成 HTTP 状态码的地方，给新哨兵加同款分支。

写 `task.Discipline`（与 `task.PlanSummary` 同段落，`ad.Start` 之前）：

```go
	task.Discipline = discBlock.Source
```

`ad.Start` 调用（`:760`）加实参：

```go
	if err := ad.Start(ctx, executor.StartReq{Task: *task, PlanContent: string(planContent),
		TaskDir: taskDir, Env: envKVs, Discipline: discBlock.Text}); err != nil {
```

`ad.Start` 成功之后（错误分支之外）加回显：

```go
	// 派发回显（B126-A）：纪律块配置化之后，写 plan 的人再也看不到它躺在
	// plan 头部了；不在事件流里说一句，冲突就只能等审核者事后发现
	if discBlock.Source != "" {
		m.appendProgress(taskID, "纪律块: "+discBlock.Source)
	}
	m.log.Info("纪律块已注入", "task", taskID, "executor", execName,
		"source", discBlock.Source, "bytes", len(discBlock.Text))
```

两处 `ResumeReq` 构造（`:1062`、`:2973`）各加：先在该函数内取 `execName`（两处都已有 `task.Executor`，用既有变量），再调 `m.discipline.For(...)`，失败时 Warn 并以空纪律块继续——**恢复路径不因纪律块读不到而判任务不可恢复**：

```go
	discBlock, derr := m.discipline.For(execName)
	if derr != nil {
		// 恢复比纪律更要紧：读不到就不注入，Warn 留痕，绝不因此判任务不可恢复
		m.log.Warn("恢复时纪律块读取失败，本次不注入", "task", taskID, "cause", derr)
	}
```

然后在 `ResumeReq{...}` 字面量里加 `Discipline: discBlock.Text,`。

- [ ] **Step 5: CLI 回显**

`cmd/dispatch.go`，在 `RepoDirtyCount` 那个 stderr 提示块之后、`json.Marshal(task)` 之前：

```go
		// B126-A：纪律块不再躺在 plan 文件头部，派发这一刻不说，协调者就不知道
		// 自己的 plan 将与哪份纪律共处。与基线行同走 stderr
		//（stdout 的单行任务 JSON 契约不能破）
		if task.Discipline != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "纪律块:", task.Discipline)
		}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go build ./... && go test ./internal/agentd/ ./internal/executor/... ./cmd/ -count=1`
Expected: PASS

- [ ] **Step 7: 核对日志与注释覆盖**

- dispatch 成功路径有 Info 日志带 `task`/`executor`/`source`/`bytes`——**成功路径不静默**
- 纪律块解析失败有独立哨兵错误，能回显给协调者而不是扁平成 500
- 恢复路径失败有 Warn 并写明「不因此判不可恢复」
- 三个新字段各有注释，`proto.Task.Discipline` 写明「后加不回填」与用途
- 事件流回显只在 `Source` 非空时落（显式关闭注入时不该凭空多一条 progress）

- [ ] **Step 8: Commit**

```bash
git add internal/executor/executor.go internal/executor/resume.go internal/proto/proto.go internal/agentd/ cmd/dispatch.go
git commit -m "feat(agentd,proto,cmd): 纪律块流进 StartReq/ResumeReq 并在派发时回显（B129/B126-A）

dispatch 解析位置与 env 同段：失败时还没有落库/建树副作用，拒发是干净的。
档位落进 task.Discipline 与一条 progress 事件，CLI 走 stderr 回显——
配置化把纪律块从 plan 文件里拿走后，这是协调者唯一能当场看见它的地方。
恢复路径读不到纪律块只 Warn 不注入，绝不因此判任务不可恢复。"
```

---

### Task 5: 四个 adapter 真正注入纪律块

**Files:**
- Modify: `internal/executor/claudecode/taskenv.go:150` 及其调用链（`req.Discipline` 需从 `Start` 传到 `taskenv`）
- Modify: `internal/executor/codex/adapter.go:256`
- Modify: `internal/executor/grok/adapter.go:198`
- Modify: `internal/executor/opencode/taskenv.go:156` 及其调用链
- Test: 四个包各追加一个用例

**Interfaces:**
- Consumes: Task 3 的 `turn.RenderPrompt(taskID, planContent, disciplineBlock)`、Task 4 的 `StartReq.Discipline`
- Produces: 无新导出

- [ ] **Step 1: 写失败的测试（四个包各一个，此处给 codex 版，另三个照改包名与脚手架）**

```go
// internal/executor/codex/adapter_test.go 追加
func TestStartInjectsDisciplineIntoPrompt(t *testing.T) {
	got := renderStartPromptForTest(t, executor.StartReq{
		Task:        proto.Task{ID: "T1"},
		PlanContent: "计划正文",
		Discipline:  "# 执行纪律\n单上下文版内容",
	})
	if !strings.Contains(got, "单上下文版内容") {
		t.Fatalf("纪律块没进 prompt，实得：%.120s", got)
	}
	if !strings.Contains(got, "计划正文") {
		t.Error("计划正文丢了")
	}
}
```

> `renderStartPromptForTest` 是需要你在该包 `export_test.go` 里加的薄壳：把 `Start` 中「调 `turn.RenderPrompt`」那一步单独暴露出来，避免为测一行渲染去拉起真进程。codex 包已有 `export_test.go`；其余包若没有就新建。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/codex/ -run TestStartInjectsDiscipline -v`
Expected: FAIL —— 纪律块没出现在产物里（当前传的是 `""`）

- [ ] **Step 3: 四处改传实参**

```
internal/executor/codex/adapter.go:256    turn.RenderPrompt(taskID, req.PlanContent, req.Discipline)
internal/executor/grok/adapter.go:198     turn.RenderPrompt(taskID, req.PlanContent, req.Discipline)
```

`claudecode/taskenv.go:150` 与 `opencode/taskenv.go:156` 在 taskenv 层，不直接持有 `req`：给这两个 taskenv 函数各加一个 `disciplineBlock string` 参数，由各自 `Start` 里传 `req.Discipline` 进来。**不要在 taskenv 里去读全局或重新解析**——纪律块的唯一来源是 `StartReq`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/... -count=1`
Expected: PASS，四个包新用例全绿

- [ ] **Step 5: 核对日志与注释覆盖**

- 四个 adapter 各在渲染后打一条 Debug/Info：`a.log.Info("纪律块已注入 prompt", "task", taskID, "bytes", len(req.Discipline))`，`len==0` 时打「未注入纪律块」——不能让「到底注没注」只能靠读 prompt 文件推断
- 两个 taskenv 函数的新参数有注释说明「唯一来源是 StartReq，不得自行解析」

- [ ] **Step 6: Commit**

```bash
git add internal/executor/
git commit -m "feat(executor): 四个 adapter 注入纪律块（B129）

codex/grok 直接传 req.Discipline；claudecode/opencode 的 taskenv 各加一个参数
由 Start 传入——纪律块唯一来源是 StartReq，taskenv 不得自行解析。
四个包各加一条渲染后日志，注没注不靠读 prompt 文件推断。"
```

---
### Task 6: codex `developerInstructions` 常驻通道（原 B116）

**Files:**
- Modify: `internal/executor/codex/adapter.go:289-311`（`openThread` 签名与 `thread/start` params）、`:252`（`Start` 里的调用点）
- Modify: `internal/executor/codex/resume.go:150-156`（`thread/resume` params）、`openThreadOnConn` 的调用点
- Test: `internal/executor/codex/adapter_test.go`、`resume_test.go`

**Interfaces:**
- Consumes: Task 3 的 `turn.ProtocolRules`、Task 4 的 `StartReq.Discipline` / `ResumeReq.Discipline`
- Produces: `openThread(ctx, r, cwd, model, developerInstructions string) error`（**加第五个参数**）

- [ ] **Step 1: 写失败的测试**

```go
// adapter_test.go 追加
// thread/start 必须带 developerInstructions，且内容 = 协议铁律 + 纪律块。
func TestThreadStartCarriesDeveloperInstructions(t *testing.T) {
	params := threadStartParamsForTest(t, "cwd", "gpt-5.6-luna",
		turn.ProtocolRules+"\n\n"+"# 执行纪律\n单上下文版内容")
	di, ok := params["developerInstructions"].(string)
	if !ok {
		t.Fatal("params 里没有 developerInstructions")
	}
	if !strings.Contains(di, "提问纪律") {
		t.Error("协议铁律没进 developerInstructions")
	}
	if !strings.Contains(di, "单上下文版内容") {
		t.Error("纪律块没进 developerInstructions")
	}
}

// resume 是最容易漏钉的路径（B18 教训）：thread/resume 同样必须重传。
func TestThreadResumeCarriesDeveloperInstructions(t *testing.T) {
	params := threadResumeParamsForTest(t, "th-1", "/repo", "指令原文")
	if params["developerInstructions"] != "指令原文" {
		t.Fatalf("thread/resume 漏传 developerInstructions，实得 %v", params["developerInstructions"])
	}
	// 三个安全参数一个都不能丢
	for _, k := range []string{"threadId", "cwd", "approvalPolicy", "approvalsReviewer"} {
		if _, ok := params[k]; !ok {
			t.Errorf("thread/resume 丢了 %s", k)
		}
	}
}
```

> `threadStartParamsForTest` / `threadResumeParamsForTest` 加在 `export_test.go`：把两处 params 字面量的构造抽成小函数（如 `buildThreadStartParams(cwd, model, devInstr string) map[string]any`），生产代码调它，测试也调它。**抽函数而不是在测试里复制字面量**——复制就是下一次漂移的种子。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/codex/ -run 'DeveloperInstructions' -v`
Expected: FAIL —— `undefined: threadStartParamsForTest`

- [ ] **Step 3: 实现**

`adapter.go`，把 params 构造抽出来并加字段：

```go
// buildThreadStartParams 构造 thread/start 的入参。
//
// 抽成函数是为了让测试与生产代码用同一份字面量——两处各写一份，
// 下次加安全参数时必然漏改一处（thread/resume 的三个安全参数已有前车之鉴）。
//
// developerInstructions 是 codex 协议直收的**持久**指令通道（spec §5.1）：
// 协议铁律与执行纪律放在这里才能跨回合常驻，只放首条用户消息的话，
// 多回合任务里它会随上下文滚出去——而收尾纪律正是 turn.ParseTrailer 的前提，
// 滚掉之后回合结束无 trailer，直接落进 B74 修过的「假完成」判定路径。
func buildThreadStartParams(cwd, model, developerInstructions string) map[string]any {
	params := map[string]any{
		"cwd":               cwd,
		"sandbox":           "workspace-write",
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
	}
	if model != "" {
		params["model"] = model
	}
	if developerInstructions != "" {
		params["developerInstructions"] = developerInstructions
	}
	return params
}

// buildThreadResumeParams 构造 thread/resume 的入参。
//
// 三个安全参数必须一起重传（spec §5.6）：恢复路径是最容易让安全档位悄悄退回
// 开发机 config 的地方。developerInstructions 同理——恢复后不重传等于纪律消失。
func buildThreadResumeParams(threadID, repoPath, developerInstructions string) map[string]any {
	params := map[string]any{
		"threadId":          threadID,
		"cwd":               repoPath,
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
	}
	if developerInstructions != "" {
		params["developerInstructions"] = developerInstructions
	}
	return params
}

// developerInstructionsFor 拼出常驻指令：协议铁律在前，执行纪律在后。
//
// 顺序与首条用户消息一致（turn.RenderPrompt 的模板同序）：协议是硬约束、
// 纪律是工作方式，两处顺序不同会让模型对优先级产生歧义。
func developerInstructionsFor(disciplineBlock string) string {
	if strings.TrimSpace(disciplineBlock) == "" {
		return turn.ProtocolRules
	}
	return turn.ProtocolRules + "\n\n" + strings.TrimSpace(disciplineBlock)
}
```

`openThread` 加第五个参数 `developerInstructions string`，内部改用 `buildThreadStartParams(cwd, model, developerInstructions)`。

`Start` 里的调用点（`:252`）改成：

```go
	if err := a.openThread(ctx, r, req.Task.Workdir(), req.Task.Model,
		developerInstructionsFor(req.Discipline)); err != nil {
		return err
	}
```

`resume.go:150` 改用 `buildThreadResumeParams(threadID, repoPath, developerInstructionsFor(req.Discipline))`；同文件里 `openThreadOnConn` 的调用点也把同一个值传下去（冷恢复降级第 4 级新开会话时，纪律不能丢）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/codex/ -count=1 && go test -race ./internal/executor/codex/ -count=1`
Expected: PASS

- [ ] **Step 5: 核对日志与注释覆盖**

- `openThread` 已有的「codex 会话建立中」Info 日志加 `dev_instr_bytes`，恢复路径同样加——**「常驻指令到底带没带」必须能从日志判定**，否则真机验收只能靠抓包
- 三个新函数各有导出/包内注释，写明「为什么抽函数」与「为什么恢复路径也要传」
- `developerInstructionsFor` 的顺序约定有注释

- [ ] **Step 6: Commit**

```bash
git add internal/executor/codex/
git commit -m "feat(codex): thread/start 与 thread/resume 带 developerInstructions（B129/原 B116）

内容 = turn.ProtocolRules + 纪律块。params 构造抽成 build*Params 函数，
测试与生产共用同一份字面量——两处各写一份，下次加安全参数必漏改一处。
恢复路径与冷恢复降级新开会话都传，漏了等于恢复后纪律消失（B18 教训）。"
```

---

### Task 7: codex 任务专属 tmp（原 B118）

**Files:**
- Modify: `internal/executor/codex/adapter.go:109-117`（`sandboxPolicy`）、`:358`（`startTurn` 里的调用）、`:218`（`Start` 里 `startServe` 之前）
- Modify: `internal/executor/codex/resume.go:105`（重起 serve 的路径）
- Test: `internal/executor/codex/adapter_test.go`

**Interfaces:**
- Consumes: `runState.taskDir`（已有）、`StartReq.TaskDir` / `ResumeReq.TaskDir`
- Produces: `sandboxPolicy(taskTmpDir string) map[string]any`（**加参数**）、`taskTmpDir(taskDir string) string`、`tmpEnvKVs(taskTmpDir string) []string`

- [ ] **Step 1: 写失败的测试**

```go
// adapter_test.go 追加
func TestSandboxPolicyGrantsTaskTmp(t *testing.T) {
	p := sandboxPolicy("/data/tasks/T1/tmp")
	roots, _ := p["writableRoots"].([]any)
	if len(roots) != 1 || roots[0] != "/data/tasks/T1/tmp" {
		t.Fatalf("writableRoots = %v，任务专属 tmp 没进可写域", p["writableRoots"])
	}
	// /tmp 与 $TMPDIR 的排除必须保持——放开它们等于把跨任务共享目录敞给所有 codex 任务
	if p["excludeSlashTmp"] != true || p["excludeTmpdirEnvVar"] != true {
		t.Error("两个 exclude 被放开了，任务隔离被破坏")
	}
	if p["networkAccess"] != true {
		t.Error("networkAccess 被改动")
	}
}

func TestTmpEnvPointsGoToolchainAtTaskTmp(t *testing.T) {
	kvs := tmpEnvKVs("/data/tasks/T1/tmp")
	want := map[string]string{
		"TMPDIR":    "/data/tasks/T1/tmp",
		"GOTMPDIR":  "/data/tasks/T1/tmp",
		"GOCACHE":   "/data/tasks/T1/tmp/gocache",
	}
	got := map[string]string{}
	for _, kv := range kvs {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// 任务专属 tmp 必须在 TaskDir 下（= DataDir 下），绝不能落在工作区里——
// 落进仓库会让一族「非 git 目录应报错」的用例假红（08-17 B119 实测 6 条）。
func TestTaskTmpDirLivesUnderTaskDir(t *testing.T) {
	got := taskTmpDir("/data/tasks/T1")
	if got != filepath.Join("/data/tasks/T1", "tmp") {
		t.Fatalf("taskTmpDir = %q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/codex/ -run 'SandboxPolicy|TmpEnv|TaskTmpDir' -v`
Expected: FAIL —— `too many arguments in call to sandboxPolicy` / `undefined: tmpEnvKVs`

- [ ] **Step 3: 实现**

```go
// taskTmpDir 返回任务专属临时目录（<taskDir>/tmp）。
//
// 为什么必须在 TaskDir 下而不是工作区里：把 TMPDIR 指进仓库会让一族
// 「造一个非 git 目录、断言 git 操作报错」的用例假性失败——临时目录落在
// git 仓库内，git 命令正常成功，断言全挂。08-17 B119 验收在本仓库实测命中 6 条
// （TestMainWorktreeRootRejectsNonRepo 等），识别判据是清一色的「实得 nil」。
// TaskDir 在 <DataDir>/tasks/<id> 下，天然在工作区之外。
func taskTmpDir(taskDir string) string { return filepath.Join(taskDir, "tmp") }

// tmpEnvKVs 返回把 Go 工具链的临时目录与构建缓存指向任务专属 tmp 的环境变量。
//
// 为什么开门与走门要配套：sandboxPolicy 的 writableRoots 只是把这个目录
// 加进可写域，Go 默认仍会用系统 /var/folders——照旧被沙箱拒。反过来只设 env
// 而不开可写域，同样被拒。两者缺一不可（B129 spec §4.2）。
//
// GOCACHE 单独放子目录：它的内容是构建产物不是临时文件，混在一起会让
// 「清 tmp」这类操作误删缓存。顺带收益是构建缓存也随任务隔离，
// 消除跨任务污染。
func tmpEnvKVs(taskTmp string) []string {
	return []string{
		"TMPDIR=" + taskTmp,
		"GOTMPDIR=" + taskTmp,
		"GOCACHE=" + filepath.Join(taskTmp, "gocache"),
	}
}

// sandboxPolicy 是每回合显式钉死的沙箱策略（spec §2 / §2.2）。
//
// 参数：
//   - taskTmp: 任务专属临时目录；空串时不新增可写域（保持历史行为）
//
// （原有的「为什么每回合都传」与 networkAccess 注释原样保留，不要删）
//
// 为什么只开任务专属 tmp 而不是放开 excludeSlashTmp：/tmp 是跨任务共享目录，
// 放开它两个并发任务能互相看见与覆盖对方的临时文件，与 handoff 一路在收的
// 任务隔离方向相反。任务专属 tmp 随任务目录一起回收，不留残迹。
func sandboxPolicy(taskTmp string) map[string]any {
	roots := []any{}
	if taskTmp != "" {
		roots = append(roots, taskTmp)
	}
	return map[string]any{
		"type":                "workspaceWrite",
		"networkAccess":       true,
		"excludeSlashTmp":     true,
		"excludeTmpdirEnvVar": true,
		"writableRoots":       roots,
	}
}
```

`startTurn`（`:358`）改为 `sandboxPolicy(taskTmpDir(r.taskDir))`。

`Start` 里 `startServe` 之前，建目录并把 env 拼进去：

```go
	taskTmp := taskTmpDir(req.TaskDir)
	if err := os.MkdirAll(filepath.Join(taskTmp, "gocache"), 0o755); err != nil {
		a.log.Error("创建任务专属 tmp 失败", "task", taskID, "dir", taskTmp, "cause", err)
		return fmt.Errorf("创建任务专属 tmp %s: %w", taskTmp, err)
	}
	a.log.Info("任务专属 tmp 就绪", "task", taskID, "dir", taskTmp)
	// 注入顺序：任务专属 tmp 在后，覆盖用户 env 文件里可能存在的同名键——
	// 沙箱只放行这一个目录，用户指向别处的 TMPDIR 在这里只会造成哑失败
	env := append(append([]string{}, req.Env...), tmpEnvKVs(taskTmp)...)
```

把原先传 `req.Env` 的地方改成 `env`。`resume.go` 重起 serve 的路径（`:105` 附近）做同样处理，用 `req.TaskDir`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/codex/ -count=1 && go build ./...`
Expected: PASS

- [ ] **Step 5: 核对日志与注释覆盖**

- 建目录失败有 Error 带 `dir` + `cause`，并中断 Start（沙箱开了门而目录不存在会变成更难查的哑失败）
- 成功路径有「任务专属 tmp 就绪」Info
- 三个新函数各有注释，`taskTmpDir` 写明 B119 假红的判据，`tmpEnvKVs` 写明「开门与走门配套」，`sandboxPolicy` 写明「为什么不放开 /tmp」
- `sandboxPolicy` 原有的两段注释（每回合重钉、networkAccess 的决策）**原样保留**

- [ ] **Step 6: Commit**

```bash
git add internal/executor/codex/
git commit -m "fix(codex): 沙箱开任务专属 tmp 并把 Go 临时目录指过去（B118）

writableRoots 加 <TaskDir>/tmp，两个 exclude 保持 true 不碰 /tmp 共享目录。
同时经 env 注入 TMPDIR/GOTMPDIR/GOCACHE——开门与走门缺一不可：
只开可写域 Go 仍用系统 /var/folders 照旧被拒，只设 env 目录不可写同样被拒。
目录在 TaskDir 下、工作区之外，避开 TMPDIR 指进仓库致 git 用例假红（B119 实测 6 条）。"
```

---

### Task 8: 回合边界后到达的裁决必须应答（B117）

**Files:**
- Modify: `internal/proto/proto.go:83`（`EventTypeDenyGuidanceDropped` 之后加新事件类型）
- Modify: `internal/agentd/manager.go:1765-1771`（边界守卫）
- Test: `internal/agentd/approver_test.go`

**Interfaces:**
- Consumes: 既有的 `m.adapterFor`、`ad.RespondPermission`、`m.st.AppendEvent`、`m.hub.Publish`
- Produces: `proto.EventTypeApprovalDropped EventType = "approval_dropped"`、`approvalDroppedPayload{TicketID, Decision, State string}`

- [ ] **Step 1: 写失败的测试**

```go
// approver_test.go 追加
// 裁决晚于回合边界时，必须回一个干净的 reject 并留可见事件——
// 不应答会让 codex 侧那条 ServerRequest 悬到自行 abort，打死的是下一个回合。
func TestLateApproverDecisionRespondsRejectAndPublishes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		approve bool
		state   proto.TaskState
	}{
		{"approve 晚到且任务已待审", true, proto.TaskStateWaitingReview},
		{"escalate 晚到且任务已待审", false, proto.TaskStateWaitingReview},
		{"approve 晚到且任务已归档", true, proto.TaskStateCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, ad, taskID := seedLateDecisionCase(t, tc.state)
			m.consultApproverForTest(taskID, tc.approve, "tk-1", "perm-1")

			var decisions []string
			for _, p := range ad.recordedPerms() {
				if strings.HasPrefix(p, "perm-1:") {
					decisions = append(decisions, strings.TrimPrefix(p, "perm-1:"))
				}
			}
			if len(decisions) != 1 || decisions[0] != "reject" {
				t.Errorf("回传给 executor 的是 %v，必须恰好一次 reject", decisions)
			}
			evs, err := st.EventsFromAsc(taskID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, e := range evs {
				if e.Type == proto.EventTypeApprovalDropped {
					found = true
				}
			}
			if !found {
				t.Error("没有 approval_dropped 事件，协调者只能去 agentd.log 里翻")
			}
		})
	}
}

// P1-1 的原意必须一字不改：不建工单。
func TestLateApproverDecisionDoesNotCreateTicket(t *testing.T) {
	m, st, _, taskID := seedLateDecisionCase(t, proto.TaskStateWaitingReview)
	m.consultApproverForTest(taskID, false, "tk-1", "perm-1")
	tks, err := st.PendingTickets(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tks) != 0 {
		t.Fatalf("边界之后不得建工单，实得 %d 张", len(tks))
	}
}

// executor 已死时回传失败不得改变任务状态。
func TestLateDecisionRespondFailureDoesNotChangeState(t *testing.T) {
	m, st, ad, taskID := seedLateDecisionCase(t, proto.TaskStateCompleted)
	ad.setRespondErr(errors.New("executor 已不在"))
	m.consultApproverForTest(taskID, true, "tk-1", "perm-1")
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != proto.TaskStateCompleted {
		t.Fatalf("回传失败不该改状态，实得 %s", task.State)
	}
}
```

> **脚手架说明（已核实过的真名）**：`chanAdapter` 的 `setRespondErr(err)` 已存在，`perms` 字段记录的是 `permID+":"+decision`（`manager_test.go:107`）；挂起工单用 `st.PendingTickets(taskID) ([]proto.Ticket, error)`；读事件用 `st.EventsFromAsc`。
>
> 三处需要你补：
> 1. `recordedPerms()`：给 `chanAdapter` 加一个由 `a.mu` 保护的 `perms` 副本读取器（既有测试直接读 `a.perms` 是竞态隐患，本用例走 goroutine 路径必须加读取器）。
> 2. `consultApproverForTest(taskID string, approve bool, ticketID, permID string)`：在 `export_test.go` 里薄壳包一层 `m.consultApprover`，构造一个必定返回指定裁决的审批者。**不要改 `consultApprover` 的生产签名**。
> 3. `seedLateDecisionCase(t, state) (*Manager, *store.Store, *chanAdapter, string)`：建 manager + 派一个任务 + 把它迁到目标状态，返回四件套。迁状态复用本包既有用例的写法（`grep -n "transitBestEffort\|SetState" internal/agentd/*_test.go`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'LateApproverDecision|LateDecision' -v`
Expected: FAIL —— `undefined: proto.EventTypeApprovalDropped`

- [ ] **Step 3: 加事件类型**

`internal/proto/proto.go`，`EventTypeDenyGuidanceDropped` 之后：

```go
	// EventTypeApprovalDropped 表示审批者的裁决没能下发给 executor——裁决回来时
	// 回合已经结束（任务离开 running/waiting_answer）。agentd 已代为回了一个
	// 干净的 reject，本事件说明「那条裁决去哪了」。
	//
	// 与 deny_guidance_dropped 是同一根因（回合结束即无下发通道）的 approve 方向，
	// 但后果更重：拒绝原因丢了只是少一段指导，批准丢了会让 executor 那条请求
	// 悬到自行 abort，**打断的是下一个回合**（08-17 实测两次同型）。
	EventTypeApprovalDropped EventType = "approval_dropped"
```

- [ ] **Step 4: 改边界守卫**

`internal/agentd/manager.go`，把 `:1765-1771` 那段的 return 之前补上应答与事件（**原有的 P1-1 注释整段保留**，在其后追加新注释）：

```go
	if cur.State != proto.TaskStateRunning && cur.State != proto.TaskStateWaitingAnswer {
		m.log.Warn("审批者裁决期间任务已离开 running/waiting_answer，回 reject 并留审计事件",
			"task", taskID, "ticket", ticketID, "decision", decision, "state", cur.State)
		// B117：不建工单/不唤醒/不消耗答案守卫这三条 P1-1 原意一字不改，
		// 但**必须应答**——不答等于让 executor 侧那条请求悬着，codex 实测悬 8.5 分钟后
		// 自行 Rejected("approval request aborted")，打断的是下一个回合。
		// 一律回 reject 而不是照裁决放行：任务此刻已在 waiting_review（语义是「等协调者」），
		// 放行等于让 executor 在无人看管时继续改工作区。命令没跑成可以 continue 重跑，
		// 回合被打死不能。
		m.respondLateDecision(taskID, ticketID, ev.PermissionID, decision, string(cur.State))
		return
	}
```

新增方法（放在 `autoAllowPermission` 附近，它是「必须应答」的同款形态）：

```go
// respondLateDecision 处置「裁决晚于回合边界」：回一个干净的 reject，并留一条
// 协调者可见事件说明那条裁决的去向。
//
// 参数：
//   - decision: 审批者原本的裁决（approve/escalate/error），只进事件不影响回传值
//   - state: 重读到的任务状态，进事件供协调者判断当时发生了什么
//
// 注意：
//   - 回传失败不改变任务状态。最常见成因就是 executor 已经不在了，
//     那正是这条路径的常态之一，不该因此把任务推向 failed
//   - 事件 Publish 而不是只落库：协调者需要知道「有一条批准空转了、
//     该 continue 重跑」，这与 deny_guidance_dropped 的可操作性理由相同
func (m *Manager) respondLateDecision(taskID, ticketID, permID, decision, state string) {
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Warn("裁决晚到：任务运行态已不在，无法回传",
			"task", taskID, "ticket", ticketID, "cause", err)
	} else {
		actx, acancel := unaryCtx(context.Background())
		defer acancel()
		if rerr := ad.RespondPermission(actx, taskID, permID, "reject"); rerr != nil {
			m.log.Warn("裁决晚到：回传 reject 失败（多为 executor 已退出）",
				"task", taskID, "ticket", ticketID, "cause", rerr)
		} else {
			m.log.Info("裁决晚到：已回传 reject，回合可正常收尾",
				"task", taskID, "ticket", ticketID)
		}
	}
	evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeApprovalDropped,
		approvalDroppedPayload{TicketID: ticketID, Decision: decision, State: state})
	if aerr != nil {
		m.log.Error("追加 approval_dropped 事件失败", "task", taskID, "ticket", ticketID, "cause", aerr)
		return
	}
	m.hub.Publish(evt)
}
```

payload 类型放在 `approverDecisionPayload` 旁边：

```go
// approvalDroppedPayload 是 approval_dropped 事件的 payload。
type approvalDroppedPayload struct {
	TicketID string `json:"ticket_id"`
	Decision string `json:"decision"` // 审批者原本的裁决：approve / escalate / error
	State    string `json:"state"`    // 裁决回来时任务已处的状态
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1 && go test -race ./internal/agentd/ -count=1`
Expected: PASS

- [ ] **Step 6: 核对日志与注释覆盖**

- 三条分支各有日志：回传成功 Info、回传失败 Warn、运行态已不在 Warn——**成功路径不静默**
- 事件落库失败有 Error
- 边界守卫处原有的 P1-1 注释整段保留，新注释解释「为什么一律 reject 而不是照裁决放行」
- 新事件类型有注释，写明与 `deny_guidance_dropped` 的关系与后果差异

- [ ] **Step 7: Commit**

```bash
git add internal/proto/proto.go internal/agentd/
git commit -m "fix(agentd): 回合边界后到达的审批裁决必须应答（B117）

不建工单/不唤醒/不消耗答案守卫这三条 P1-1 原意一字不改，但补上回传：
不答等于让 executor 那条请求悬着，codex 实测悬 8.5 分钟后自行
Rejected(\"approval request aborted\")，打断的是下一个回合。
一律回 reject 而非照裁决放行——任务此刻已在 waiting_review，
放行等于让 executor 在无人看管时继续改工作区。
新增 approval_dropped 事件说明裁决去向，Publish 以便协调者知道该 continue 重跑。"
```

---

### Task 9: 整分支终审与全量门

**Files:** 无新增；只跑门与修复发现项

- [ ] **Step 1: 全量门**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
```

Expected：`gofmt -l .` **无输出**（执行者的 ledger 漏报 gofmt 是有前科的，这条必须自己跑）；`go test` 全绿。

- [ ] **Step 2: 竞态门**

```bash
go test -race ./internal/agentd/ ./internal/executor/codex/ ./internal/executor/opencode/ ./internal/discipline/ ./cmd/ -count=1
```

Expected: 全绿

- [ ] **Step 3: 相对分支起点的完整 diff 终审**

```bash
git diff claude/b128-windows-claude-executor...HEAD
```

逐项核对：
- 协议层三条铁律仍在 `promptTemplate` 里，没有被改成可配置
- 四个 adapter 的首条消息结构逐字同构（只是纪律块取值不同）
- `sandboxPolicy` 的两个 `exclude` 仍为 `true`
- P1-1 那段原注释仍在
- 没有新增 `fmt.Printf` 作为日志

有发现项就一次性全量修，再做一次范围复审；不逐项修，也没有第二轮修复波。

- [ ] **Step 4: Commit**

```bash
git commit -am "chore: B129/B117/B118 整分支终审与全量门"
```

---

> **spec §5 的仓库外改动（B126-B）已于 08-18 落地**：`~/.claude/CLAUDE.md` §4「派发硬纪律」
> 已加入「派 plan 前自审『验收步骤归谁』」一条，本计划无需再动它。

## 真机验收（审核者执行，不派发）

> **本节由审核者在本地执行，不写进派发给 executor 的 plan**：这些步骤要驱动 handoff 自身
> （起 agentd、派子任务、读事件流），与单上下文版纪律块的「不要派发、不要调用 handoff CLI」
> 直接冲突（B126-B，08-18 B105 实测教训）。

1. 派一个小任务给 mac-02 + codex，确认 `dispatch` 的 stderr 出现 `纪律块: 内置:single-context`
2. `handoff show <id>`，确认事件流里有同款 progress 事件
3. 读该任务的渲染 prompt，确认含「在本会话内自己逐 task 实现」且**不含**「用你自己的 subagent 机制」
4. 在任务里让它跑 `go test ./... -count=1`，**不给任何 `.gocache-*` / `.gotmp-*` / `.codex-tmp` 绕行配方**。
   跑通 = B118 通过；出现绕行配方或 `operation not permitted` = 不通过
5. `handoff continue` 一次，确认续接回合仍守协议（trailer 正常解析）——验 `thread/resume` 的 developerInstructions
6. 回归另三家（spec §6.3）：opencode / claude 各派一个小任务，确认拿到 **subagent 版**；grok 派一个，确认拿到 **单上下文版**。三家行为均无变化
7. B117：单测已兜底；真机机会性验证——若出现 `turn_failed: 回合被中断（非 handoff 发起）`，
   查事件流应能看到 `approval_dropped`

---

## 附录 A：内置纪律块原文

以下两份**逐字**写入 `internal/discipline/builtin/`，不要改写、精简或重排编号。

### `builtin/subagent.md`

```markdown
# 执行纪律（先读这段，再读 plan）

你收到的是一份完整实现计划。用你自己的 subagent 机制按以下纪律执行，不要单上下文从头写到尾：

1. 逐 task 派全新 subagent 实现。每个 subagent 只给三样东西：该 task 的完整需求原文（含精确值、签名、测试用例）、它要接触的接口、全局约束。不要把会话历史或前序 task 总结灌进去。
2. 实现 subagent 不并行（避免改动冲突）。
3. 每个 task 完成后，派一个独立审查 subagent 做双裁决：spec 符合性（要求全实现、没有多做）+ 代码质量。输入是该 task 的需求原文 + 完整 diff。缺任一裁决不算过。
4. 审查不过进修复回路：一轮 = 一次修复 + 一次只看修复 diff 的复审，最多 5 轮。前 3 轮回原实现者，4-5 轮换全新实现者接手。5 轮后仍有未决项：非承重的记账搁置；承重的（后续 task 依赖它、或暴露 plan 缺陷）停下上报 BLOCKED。
5. 进度落盘到 ledger 文件：每 task 完成、每轮修复各追加一行，含 commit 范围。恢复现场以 ledger + git log 为准，不信记忆。
6. Minor 发现记账不进回路，留给终审统一 triage。
7. 全部 task 完成后做一次整分支终审（相对分支起点的完整 diff）。有发现项就一次性派一个修复 subagent 全量修，再做一次范围复审；不搞逐项派发，也没有第二轮修复波。
8. 协调上下文保持干净：你自己不亲自改代码，所有改动经 subagent 产出且经审查。
9. 每个 task 完成即 commit，提交信息说清做了什么。
10. 不停下来问「要不要继续」。只在 BLOCKED、真歧义、全部完成三种情况停；需求取舍拿不准就发工单问，等审核者裁决。
```

### `builtin/single-context.md`

```markdown
# 执行纪律（先读这段，再读 plan）

你收到的是一份完整实现计划。按以下纪律执行：

1. **在本会话内自己逐 task 实现**。不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程或子任务——你就是执行者，活由你自己干。
2. 一次只做一个 task，做完再开下一个，不要并行改动。
3. 每个 task 实现完成后，**自己对该 task 做一次双裁决**：spec 符合性（要求全实现、没有多做）+ 代码质量。判据是该 task 的需求原文 + 这个 task 的完整 diff。裁决不过就自己修，修完重判，最多 5 轮。5 轮后仍有未决项：非承重的记账搁置；承重的（后续 task 依赖它、或暴露 plan 缺陷）停下上报 BLOCKED。
4. **没有亲自跑到结果的命令，不许写它的结论**。跑了但失败，贴原始报错原文，不要替它归因；不确定就写「未验证」。
5. 进度落盘到 ledger 文件：每 task 完成、每轮修复各追加一行，含 commit 范围。恢复现场以 ledger + git log 为准，不信记忆。
6. Minor 发现记账不进修复回路，留给终审统一 triage。
7. 全部 task 完成后做一次整分支终审（相对分支起点的完整 diff）。有发现项就一次性全量修，再做一次范围复审；不搞逐项修，也没有第二轮修复波。
8. 每个 task 完成即 commit，提交信息按 plan 各 Task 的 Commit 步骤里给定的原文写。
9. **不停下来问「要不要继续」，也不要在做完一个 task 后结束回合**。做完一个直接接着下一个，直到全部 task 完成并做完终审，再按协议输出 trailer 结束回合。只在 BLOCKED、真歧义、全部完成三种情况停；需求取舍拿不准就发工单问，等审核者裁决。
```
