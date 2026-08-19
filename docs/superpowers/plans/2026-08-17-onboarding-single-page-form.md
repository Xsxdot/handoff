# 首次配置改单页表单 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **前置依赖：先完成 [控制台新增开发机](2026-08-17-console-machine-add.md)。** 本计划会从首次配置里删掉远程配对循环；控制台还不能加机器时就删，会留下一个「配对既不在向导里、也不在控制台里」的窗口期。

**Goal:** 把桌面首次配置从「9 问 9 屏」变成一页表单——默认值已填，最快路径是「确认角色 → 点完成」。

**Architecture:** 把今天藏在 `AskAll` 控制流里的「有哪些字段、什么控件、什么默认值、什么时候显示」抽成一张**字段描述表**。`AskAll` 退化为按表逐项问的消费者（CLI 行为逐字不变），桌面壳按同一张表一次性铺成整页。远程配对循环删除（已移交控制台）。

**Tech Stack:** Go 1.26、Wails v3 beta.8 事件通道（本项目**没有**注册任何 Wails binding，全走 `Events.On`/`Emit`）、TypeScript + Vite。

## Global Constraints

以下为 spec 的项目级约束，**每个 task 的需求都隐含包含本节**：

- **CLI 的 `handoff init` 行为逐字不变**：提问文本、顺序、默认值、选项集合全部不得变化。唯一的证法是金样比对，而**金样必须在改造前先录**——录晚了就是录的改造后行为，等于没测。
- 字段、默认值、显隐规则**只有一份真相**（`internal/initflow`）。桌面前端不得内嵌任何一条分支规则。
- **落盘只发生在校验成功之后**。向导未成功完成时，磁盘上不得留下会让 `shell.Resolve` 判为「已配置」的文件（W5b-2 已钉住的判据，`kill -9` 实测）。
- `desktop/internal/shell` **不得 import Wails**——装配与 Wails API 只出现在 `desktop/main.go`。
- 桌面表单**不得出现终端语言**（如「以下每一问直接回车即保留预选项」，它是 CLI 专有前言）。
- 日志用 `log/slog`，**禁止 `fmt.Printf` 当日志**。新文件写文件头注释（职责 + 边界），导出方法写 doc 注释，非显然分支写「为什么」的中文注释。
- 不改 `config.Load` 的首次运行语义；不改 `MaybeInstallService` 与 `EnsureRunning` 的分工。
- 不得往仓库提交任何构建产物；薄壳前端构建必须走 `wails3 task build`。

## File Structure

| 文件 | 责任 |
|---|---|
| `internal/initflow/form.go`（新建） | `Kind` / `Field` / `Cond` / `Form` / `Visible` / `Apply`——字段描述表本身 |
| `internal/initflow/form_test.go`（新建） | 字段表、显隐矩阵、`Apply` 校验的用例 |
| `internal/initflow/golden_test.go`（新建） | CLI 提问金样：录制 + 比对 |
| `internal/initflow/testdata/golden_askall_*.txt`（新建） | 金样文件，**Task 1 先录** |
| `internal/initflow/initflow.go`（改） | `AskAll` 改成按表消费；删远程配对循环；`Option` 加 json tag |
| `internal/initflow/prompter.go`（改） | `Option` 加 json tag |
| `desktop/internal/shell/wizard.go`（改） | 删 `eventPrompter` 与 `NewNoticeWriter`；改为一次性交表/收答案的纯逻辑 |
| `desktop/main.go`（改） | 接线：`pathenv.Apply` → `Detect` → `Form` → 发表 → 收答案 → `Apply` → `Save` |
| `desktop/frontend/src/wizard.ts`（重写） | 单页表单渲染、本地显隐、一次提交 |

---

## Task 1: 先录 CLI 提问金样（改造前必做）

**Files:**
- Create: `internal/initflow/golden_test.go`
- Create: `internal/initflow/testdata/golden_askall_coordinator.txt`、`golden_askall_executor.txt`、`golden_askall_both.txt`

**Interfaces:**
- Consumes: `initflow.AskAll(w io.Writer, p Prompter, cfg *config.Config, rs []toolchain.Result, cfgExisted bool) (bool, string, error)`（现状，未改动）
- Produces: 三份金样文件 + `TestAskAllGolden`

**为什么单列一个 task**：这是整个计划唯一的回归防线，且**只在改造前录才有意义**。把它并进改造 task，实现者极可能先改再录。

**关于 spec §8 提到的 `goos=windows` 那一份金样：录不了，本计划不录。** 改造前的
`AskAll` 在函数内部读 `runtime.GOOS`，没有任何注入点，在 macOS 上跑不出 Windows 的
提问序列——硬要录只能先改签名，那就变成了「改造后再录」，正是本 task 要防的事。
Windows 那一档改由 Task 2 的 `TestFormWindowsRoleLimited` 覆盖（`Form` 收 `goos` 参数，
可直接传 `"windows"`）。这是**有意的偏离**，不是遗漏。

- [ ] **Step 1: 写录制器与金样测试**

新建 `internal/initflow/golden_test.go`：

```go
package initflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// recordingPrompter 把每一问的类型、标题、默认值、选项记进共享日志，
// 答案默认取 def；force 可按标题强制某个答案，用来走遍各角色分支。
type recordingPrompter struct {
	force map[string]string
	log   *[]string
}

func (p *recordingPrompter) answer(title, def string) string {
	if v, ok := p.force[title]; ok {
		return v
	}
	return def
}

func (p *recordingPrompter) Select(title string, options []Option, def string) (string, error) {
	vals := make([]string, len(options))
	for i, o := range options {
		vals[i] = o.Value + "=" + o.Label
	}
	*p.log = append(*p.log, fmt.Sprintf("select|%s|def=%s|opts=%s", title, def, strings.Join(vals, ";")))
	return p.answer(title, def), nil
}

func (p *recordingPrompter) Input(title, def string) (string, error) {
	*p.log = append(*p.log, fmt.Sprintf("input|%s|def=%s", title, def))
	return p.answer(title, def), nil
}

func (p *recordingPrompter) Confirm(title string, def bool) (bool, error) {
	*p.log = append(*p.log, fmt.Sprintf("confirm|%s|def=%v", title, def))
	return def, nil
}

// recordingWriter 把 AskAll 写进 io.Writer 的产品输出记进同一条日志，
// 从而把「打印」与「提问」的**真实交错顺序**一并锁住。
type recordingWriter struct{ log *[]string }

func (w recordingWriter) Write(b []byte) (int, error) {
	for _, line := range strings.Split(string(b), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			*w.log = append(*w.log, "out|"+s)
		}
	}
	return len(b), nil
}

// goldenFixture 造一份确定的输入：固定的 cfg 与固定的探测结果。
// 探测结果写死而不调 toolchain.Detect()，否则金样会随开发机装了什么而变。
func goldenFixture() (*config.Config, []toolchain.Result) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:7777",
		DataDir: "/tmp/handoff",
		Targets: map[string]config.Target{},
	}
	rs := []toolchain.Result{
		{Name: "opencode", Path: "/usr/bin/opencode", State: toolchain.StateReady},
		{Name: "claude", Path: "/usr/bin/claude", State: toolchain.StateAuthUnknown},
		{Name: "grok", State: toolchain.StateMissing},
		{Name: "codex", Path: "/usr/bin/codex", State: toolchain.StateNoCreds},
	}
	return cfg, rs
}

// TestAskAllGolden 锁住 CLI 的提问文本、顺序、默认值与选项集合。
//
// 用 -update 重录：go test ./internal/initflow/ -run TestAskAllGolden -update
// **重录前必须确认差异是有意的**——这个测试的全部价值就在于它会挡住
// 无意的行为漂移。
func TestAskAllGolden(t *testing.T) {
	roles := map[string]string{
		"coordinator": RoleCoordinator,
		"executor":    RoleExecutor,
		"both":        RoleBoth,
	}
	for name, role := range roles {
		t.Run(name, func(t *testing.T) {
			var log []string
			cfg, rs := goldenFixture()
			p := &recordingPrompter{force: map[string]string{"这台机器的角色": role}, log: &log}
			if _, _, err := AskAll(recordingWriter{log: &log}, p, cfg, rs, false); err != nil {
				t.Fatalf("AskAll 不该出错: %v", err)
			}
			got := strings.Join(log, "\n") + "\n"
			path := filepath.Join("testdata", "golden_askall_"+name+".txt")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("读金样失败（先用 -update 录一次）: %v", err)
			}
			if got != string(want) {
				t.Fatalf("提问序列与金样不一致\n--- 期望 ---\n%s\n--- 实际 ---\n%s", want, got)
			}
		})
	}
}
```

同文件顶部加 flag：

```go
var update = flag.Bool("update", false, "重录金样文件")
```

- [ ] **Step 2: 录金样**

Run: `go test ./internal/initflow/ -run TestAskAllGolden -update -count=1`
Expected: PASS，`internal/initflow/testdata/` 下出现三个 `.txt`

- [ ] **Step 3: 人工核对金样内容**

打开三份金样，逐行确认它们与今天 `handoff init` 的实际提问一致：`coordinator` 一份里**不应**出现「默认执行者」「监听地址」（那些在 `isExec` 分支内），`executor` 一份里**不应**出现「任务结束自动同步」。
**这一步不能跳过**——录下一份错的金样比没有金样更糟。

- [ ] **Step 4: 确认比对模式能跑通**

Run: `go test ./internal/initflow/ -run TestAskAllGolden -count=1`
Expected: PASS（不带 `-update`，走比对分支）

- [ ] **Step 5: Commit**

```bash
git add internal/initflow/golden_test.go internal/initflow/testdata
git commit -m "test(initflow): 先录 CLI 提问金样，作为 AskAll 改造的回归防线

金样把提问文本、顺序、默认值、选项集合与产品输出的交错顺序一并锁住。
必须在改造前录——录晚了锁的是改造后的行为，等于没测。探测结果写死而不
调 toolchain.Detect()，否则金样会随开发机装了什么而漂。"
```

---

## Task 2: 字段描述表

**Files:**
- Create: `internal/initflow/form.go`
- Create: `internal/initflow/form_test.go`
- Modify: `internal/initflow/prompter.go`（`Option` 加 json tag）

**Interfaces:**
- Consumes: `RoleOptions(goos string) []Option`、`DefaultRole(cfg, cfgExisted, rs, goos) string`、`ExecutorOptions(rs) []Option`、`ListenPreset(listen string, cfgExisted, isExec bool) string`（均已导出）
- Produces:
  - `type Kind string`，常量 `KindSelect` / `KindInput` / `KindConfirm`
  - `type Field struct{ Key string; Kind Kind; Title, Notice, Default string; Options []Option; Roles []string; Advanced bool; ShowWhen *Cond; DefaultWhen []DefaultRule }`（全部字段带 json tag，蛇形键名）
  - `type Cond struct{ Key, Equal string; In []string; NonEmpty bool }`
  - `type DefaultRule struct{ Cond Cond; Value string }`
  - `func Form(cfg *config.Config, rs []toolchain.Result, goos string, cfgExisted bool) []Field`
  - `func Visible(f Field, answers map[string]string) bool`
  - `func DefaultOf(f Field, answers map[string]string) string`
  - `func Apply(cfg *config.Config, fields []Field, answers map[string]string) error`

- [ ] **Step 1: 写失败的测试**

新建 `internal/initflow/form_test.go`：

```go
package initflow

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// 字段表的键集合与顺序是对外契约（桌面前端按 Key 取值），钉死它。
func TestFormKeysAndOrder(t *testing.T) {
	cfg, rs := goldenFixture()
	got := []string{}
	for _, f := range Form(cfg, rs, "darwin", false) {
		got = append(got, f.Key)
	}
	want := []string{
		// 顺序 = 今天 AskAll 的实际提问顺序（initflow.go:72-129）。
		// askListen 是在 executor_model **之后**调用的——把监听排到执行者
		// 前面会让 CLI 顺序变化，直接违反全局约束第一条。
		"role", "executor_default", "executor_model",
		"listen_preset", "listen", "repo_root",
		"approver_executor", "approver_model", "sync_auto",
	}
	if len(got) != len(want) {
		t.Fatalf("字段数不对：期望 %v，实际 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个字段：期望 %s，实际 %s", i, want[i], got[i])
		}
	}
}

// Windows 上角色只有协调者一档，且必须带解释文案。
func TestFormWindowsRoleLimited(t *testing.T) {
	cfg, rs := goldenFixture()
	for _, f := range Form(cfg, rs, "windows", false) {
		if f.Key != "role" {
			continue
		}
		if len(f.Options) != 1 || f.Options[0].Value != RoleCoordinator {
			t.Fatalf("Windows 上角色应只有协调者，实际 %+v", f.Options)
		}
		if f.Notice == "" {
			t.Fatal("Windows 上必须解释为什么只有一个选项，否则用户会以为是 bug")
		}
		return
	}
	t.Fatal("字段表里没有 role")
}

func TestVisible(t *testing.T) {
	cfg, rs := goldenFixture()
	fields := map[string]Field{}
	for _, f := range Form(cfg, rs, "darwin", false) {
		fields[f.Key] = f
	}
	cases := []struct {
		name    string
		key     string
		answers map[string]string
		want    bool
	}{
		{"协调者看不到执行者选择", "executor_default", map[string]string{"role": RoleCoordinator}, false},
		{"执行机看得到执行者选择", "executor_default", map[string]string{"role": RoleExecutor}, true},
		{"两者都看得到", "executor_default", map[string]string{"role": RoleBoth}, true},
		{"执行机看不到 sync.auto", "sync_auto", map[string]string{"role": RoleExecutor}, false},
		{"协调者看得到 sync.auto", "sync_auto", map[string]string{"role": RoleCoordinator}, true},
		{"未选自定义则不问监听地址", "listen", map[string]string{"role": RoleExecutor, "listen_preset": "loopback"}, false},
		{"选了自定义才问监听地址", "listen", map[string]string{"role": RoleExecutor, "listen_preset": "custom"}, true},
		{"没选审批者就不问审批模型", "approver_model", map[string]string{"role": RoleExecutor, "approver_executor": ""}, false},
		{"选了审批者才问审批模型", "approver_model", map[string]string{"role": RoleExecutor, "approver_executor": "opencode"}, true},
		{"切角色后残留答案不影响判定", "sync_auto", map[string]string{"role": RoleExecutor, "sync_auto": "true"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Visible(fields[c.key], c.answers); got != c.want {
				t.Fatalf("期望 %v，实际 %v", c.want, got)
			}
		})
	}
}

func TestApply(t *testing.T) {
	cfg, rs := goldenFixture()
	fields := Form(cfg, rs, "darwin", false)
	answers := map[string]string{
		"role":              RoleBoth,
		"listen_preset":     "custom",
		"listen":            "0.0.0.0:7777",
		"executor_default":  "opencode",
		"executor_model":    "",
		"repo_root":         "/srv/repos",
		"approver_executor": "opencode",
		"approver_model":    "cheap",
		"sync_auto":         "false",
	}
	if err := Apply(cfg, fields, answers); err != nil {
		t.Fatalf("Apply 不该出错: %v", err)
	}
	if cfg.Listen != "0.0.0.0:7777" || cfg.Executor.Default != "opencode" ||
		cfg.RepoRoot != "/srv/repos" || cfg.Approver.Model != "cheap" || cfg.Sync.Auto {
		t.Fatalf("写回不对: %+v", cfg)
	}
}

func TestApplyRejectsAnswerOutsideOptions(t *testing.T) {
	cfg, rs := goldenFixture()
	fields := Form(cfg, rs, "darwin", false)
	err := Apply(cfg, fields, map[string]string{"role": RoleBoth, "executor_default": "不存在的执行者"})
	if err == nil {
		t.Fatal("越界的 Select 答案必须被拒")
	}
}

func TestApplyRejectsBadConfirmValue(t *testing.T) {
	cfg, rs := goldenFixture()
	fields := Form(cfg, rs, "darwin", false)
	err := Apply(cfg, fields, map[string]string{"role": RoleCoordinator, "sync_auto": "yes"})
	if err == nil {
		t.Fatal("Confirm 只接受 true/false，\"yes\" 必须被拒——否则会静默写成 false")
	}
}

func TestApplyIgnoresInvisibleAnswers(t *testing.T) {
	cfg, rs := goldenFixture()
	fields := Form(cfg, rs, "darwin", false)
	// 协调者角色下 executor_default 不可见；前端可能残留切角色前填的值
	err := Apply(cfg, fields, map[string]string{
		"role": RoleCoordinator, "executor_default": "不存在的执行者",
	})
	if err != nil {
		t.Fatalf("不可见字段的残留答案应被忽略而不是报错，实际 %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/initflow/ -run 'TestForm|TestVisible|TestApply' -count=1`
Expected: 编译失败，`Form undefined` 等

- [ ] **Step 3: 给 `Option` 加 json tag**

`internal/initflow/prompter.go`：

```go
// Option 是一个可选项。
//
// json tag 是**对外契约**：桌面壳把字段表整体序列化给前端，前端按这些
// 小写键名取值。改名等于改协议，必须同步改 desktop/frontend/src/wizard.ts。
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}
```

- [ ] **Step 4: 实现字段表**

新建 `internal/initflow/form.go`（文件头注释写清职责与边界；`Field` 的 doc 注释按 spec §4.1 原文；`Visible` 先判 `Roles` 再判 `ShowWhen`；`Apply` 跳过不可见字段、校验 Select 答案在 `Options` 内、`Confirm` 只接受 `"true"`/`"false"`）。

`Form` 逐字段照下表构造。**标题与选项标签必须与今天 `initflow.go` 里的字符串一模一样**——金样比的就是它们：

| Key | Title（逐字） | Options | Default | Apply 写回 |
|---|---|---|---|---|
| `role` | `这台机器的角色` | `RoleOptions(goos)` | `DefaultRole(cfg, cfgExisted, rs, goos)` | 不写回（调用方用它算 isExec） |
| `executor_default` | `默认执行者` | `ExecutorOptions(rs)` | `cfg.Executor.Default`，空则 `toolchain.FirstReady(rs)`，再空则 `"opencode"` | `cfg.Executor.Default` |
| `executor_model` | `执行者模型（空=用执行者自身默认）` | — | `cfg.Executor.Model` | `cfg.Executor.Model` |
| `listen_preset` | `监听地址` | 三档，Value/Label 直接复用 `initflow.go:207-211` 的字面量 | `ListenPreset(cfg.Listen, cfgExisted, isExecOf(role))` | `loopback` → `cfg.Listen = listenLoopbackAddr`；`all` → `listenAllAddr`；`custom` → 不写，交给 `listen` |
| `listen` | `监听地址 listen` | — | `cfg.Listen` | `cfg.Listen` |
| `repo_root` | `项目落点根目录 repo_root（自动登记时 clone 到这里）` | — | `cfg.RepoRoot` | `cfg.RepoRoot` |
| `approver_executor` | `审批链执行者` | `{Value:"", Label:"不启用（权限直接找人）"}` 打头，其后接 `ExecutorOptions(rs)` | `cfg.Approver.Executor` | `cfg.Approver.Executor` |
| `approver_model` | `审批链模型（空=用执行者自身默认）` | — | `cfg.Approver.Model` | `cfg.Approver.Model` |
| `sync_auto` | `任务结束自动同步远程分支到本地 sync.auto` | — | `strconv.FormatBool(cfg.Sync.Auto)` | `cfg.Sync.Auto` |

两处需要注意，别按 spec §4.3 的表照抄：

- **`listen_preset` 是会写回的**（spec 表里那格「不落配置」不准确）。选 `loopback` / `all` 时地址由档位写死，只有 `custom` 才轮到 `listen` 字段。`Apply` 里这两个字段要一起处理。
- **`listen_preset` 的默认档依赖 `role` 的答案**，而 `Form` 在用户答题前就要返回。今天的 CLI 是顺序执行，`askListen` 拿到的 `isExec` 是用户**刚选完**的角色；照搬 `DefaultRole` 的结果会在「默认推协调者、用户改选执行机」时把预选档从「所有网卡」错成「仅本机」——这台机器随后协调者就连不上，正是 `ListenPreset` 那段注释要防的事。

  所以字段表要能表达「默认值随某个答案变」。加一条**数据化**的规则，两种渲染器同样地求值：

  ```go
  // DefaultRule 描述一条「满足条件时默认值改用 Value」的规则。
  //
  // 存在的唯一理由是监听预设依赖角色答案，而字段表必须在答题前就交出去。
  // 规则是数据，因此 CLI 与桌面前端求值方式相同——不会出现两边预选不一致。
  type DefaultRule struct {
      Cond  Cond   `json:"cond"`
      Value string `json:"value"`
  }

  // DefaultOf 返回该字段在当前答案下的默认值：命中的第一条 DefaultWhen 规则
  // 优先，都不命中才用 Default。
  func DefaultOf(f Field, answers map[string]string) string
  ```

  `Cond` 相应增加 `In []string`（`Equal` 表达不了「executor 或 both」）：

  ```go
  type Cond struct {
      Key      string   `json:"key"`
      Equal    string   `json:"equal,omitempty"`
      In       []string `json:"in,omitempty"`
      NonEmpty bool     `json:"non_empty,omitempty"`
  }
  ```

  `Form` **仅在** `ListenPreset` 真的会因 `isExec` 而翻档时才挂这条规则——即 `!cfgExisted && listenKind(cfg.Listen) == listenLoopback`。判断「会不会翻」的知识仍然只有 `ListenPreset` 一处：

  ```go
  // 监听预设：不含 isExec 时的档位作为静态默认，
  // 「用户选了执行机才翻成所有网卡」写成一条规则，由 DefaultOf 求值。
  f := Field{Key: "listen_preset", Kind: KindSelect, Title: "监听地址", /* ... */
      Default: ListenPreset(cfg.Listen, cfgExisted, false)}
  if ListenPreset(cfg.Listen, cfgExisted, true) != f.Default {
      f.DefaultWhen = []DefaultRule{{
          Cond:  Cond{Key: "role", In: []string{RoleExecutor, RoleBoth}},
          Value: ListenPreset(cfg.Listen, cfgExisted, true),
      }}
  }
  ```

  `askField` 用 `DefaultOf(f, answers)` 而不是 `f.Default`。补一条用例：

  ```go
  // 默认推协调者（一家执行者都没装）时，用户改选执行机，监听预设必须翻成「所有网卡」。
  func TestListenPresetFollowsRoleAnswer(t *testing.T) {
      cfg := &config.Config{Listen: "127.0.0.1:7777", Targets: map[string]config.Target{}}
      fields := Form(cfg, nil, "darwin", false) // rs 为空 → DefaultRole 推协调者
      var lp Field
      for _, f := range fields {
          if f.Key == "listen_preset" {
              lp = f
          }
      }
      if got := DefaultOf(lp, map[string]string{"role": RoleCoordinator}); got != "loopback" {
          t.Fatalf("协调者应预选仅本机，实际 %s", got)
      }
      if got := DefaultOf(lp, map[string]string{"role": RoleExecutor}); got != "all" {
          t.Fatalf("改选执行机后应预选所有网卡（否则协调者连不上），实际 %s", got)
      }
  }
  ```

角色展开的判定抽成一个内部函数，供 `Visible` 与 `AskAll` 共用，避免两处各写一遍：

```go
// roleMatches 判断某字段的适用角色是否覆盖当前角色答案。
//
// RoleBoth 同时算执行机与协调者——这是「两者」这个角色的全部含义，
// 也是 AskAll 里 isExec/isCoord 两个布尔的来源。
func roleMatches(fieldRoles []string, role string) bool {
	if len(fieldRoles) == 0 {
		return true // 与角色无关的字段恒显示
	}
	for _, r := range fieldRoles {
		if r == role || (role == RoleBoth && (r == RoleExecutor || r == RoleCoordinator)) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/initflow/ -run 'TestForm|TestVisible|TestApply' -count=1`
Expected: 全部 PASS

- [ ] **Step 6: 加注释自检**

- `form.go` 有文件头（职责：描述首次配置问什么；边界：不描述怎么问、不碰终端与窗口、不落盘）
- `Field` 每个字段有说明，`Key` 处注明「一旦发布不得更名（前端按它取值）」
- `Apply` 的 doc 注明「不可见字段的答案被忽略而非报错」及其原因（前端切角色后的残留）

- [ ] **Step 7: Commit**

```bash
git add internal/initflow/form.go internal/initflow/form_test.go internal/initflow/prompter.go
git commit -m "feat(initflow): 字段描述表——把「问什么」从控制流里抽成数据

Field/Cond/Form/Visible/Apply。有哪些字段、什么控件、什么默认值、随角色
与其他答案怎么显隐，全部变成可被两种渲染器消费的数据：CLI 逐项问，桌面壳
一次铺成整页。Option 加 json tag（对外契约，改名等于改协议）。"
```

---

## Task 3: `AskAll` 改成按表消费

**Files:**
- Modify: `internal/initflow/initflow.go`
- Test: `internal/initflow/golden_test.go`（Task 1 的金样，不改动，作为判据）

**Interfaces:**
- Consumes: `Form` / `Visible` / `Apply` / `roleMatches`（Task 2）
- Produces: `AskAll` 签名**不变**：`func AskAll(w io.Writer, p Prompter, cfg *config.Config, rs []toolchain.Result, cfgExisted bool) (bool, string, error)`

- [ ] **Step 1: 改造前先跑一次金样，确认基线绿**

Run: `go test ./internal/initflow/ -run TestAskAllGolden -count=1`
Expected: PASS。**不绿就停下来**——基线不对，后面的比对没有意义。

- [ ] **Step 2: 记录改造前的用例名集合**

Run:
```bash
go test ./cmd/... ./internal/initflow/... -count=1 -v 2>&1 \
  | grep -E "^--- (PASS|FAIL)" | awk '{print $3}' | sort > /tmp/before-askall.txt
wc -l /tmp/before-askall.txt
```

- [ ] **Step 3: 重写 `AskAll`**

```go
// AskAll 按字段表逐项提问并把答案写回 cfg。
//
// 参数：
//   - w: 产品输出（前言与字段的 Notice）；桌面壳不走这条路径
//   - p: 问答实现（生产 TTY 走 huh，测试走脚本化实现）
//   - cfg: 就地写回；出错时不保证未被部分修改，调用方**绝不可**在出错后落盘
//   - rs: 工具链探测结果，决定执行者选项与默认值
//   - cfgExisted: 配置文件是否已存在，影响监听预设的默认档
//
// 返回：
//   - isExec: 本机是否承担执行机角色（调用方据此决定后续是否装 service）
//   - role: 角色答案原文
//   - err: 用户取消或校验失败
//
// 注意：提问顺序即 Form 返回的切片顺序。想改问什么、问的顺序、默认值，
// 改 form.go，**不要改本函数**——本函数只负责把表渲染成一问一答。
func AskAll(w io.Writer, p Prompter, cfg *config.Config, rs []toolchain.Result, cfgExisted bool) (bool, string, error) {
	fmt.Fprintln(w, "\n以下每一问直接回车即保留预选项。") // CLI 专有前言，不进字段表

	fields := Form(cfg, rs, runtime.GOOS, cfgExisted)
	answers := make(map[string]string, len(fields))
	for _, f := range fields {
		if !Visible(f, answers) {
			continue
		}
		if f.Notice != "" {
			fmt.Fprintln(w, "\n"+f.Notice)
		}
		ans, err := askField(p, f, answers)
		if err != nil {
			return false, answers["role"], err
		}
		answers[f.Key] = ans
		// CLI 专有的答后提示：选了没装/未登录的执行者时警告一句（只警告不拦）。
		// 它不进字段表——字段表描述的是「问什么」，这是「答完之后往终端写什么」，
		// 桌面端不需要（选项标签里已经带着就绪状态）。
		if f.Key == "executor_default" {
			warnIfNotReady(w, rs, ans)
		}
	}
	if err := Apply(cfg, fields, answers); err != nil {
		return false, answers["role"], err
	}
	role := answers["role"]
	return role == RoleExecutor || role == RoleBoth, role, nil
}

// askField 按 Kind 把一个字段分派给 Prompter。
//
// 默认值走 DefaultOf 而不是 f.Default：监听预设要跟着刚答完的角色翻档。
// Confirm 的答案统一编码成 "true"/"false" 字符串：字段表是同构的，
// 让答案 map 保持 map[string]string 才能被前端原样回传。
func askField(p Prompter, f Field, answers map[string]string) (string, error) {
	def := DefaultOf(f, answers)
	switch f.Kind {
	case KindSelect:
		return p.Select(f.Title, f.Options, def)
	case KindInput:
		return p.Input(f.Title, def)
	case KindConfirm:
		v, err := p.Confirm(f.Title, def == "true")
		if err != nil {
			return "", err
		}
		return strconv.FormatBool(v), nil
	}
	return "", fmt.Errorf("未知的字段类型 %q（字段 %s）", f.Kind, f.Key)
}
```

同时**删除**：`askListen` 函数、`askTargets` 函数及其调用（远程配对循环，配对已移交控制台）、以及函数开头的 Windows 特判 `if`（产品输出改由 `role` 字段的 `Notice` 承担，文案逐字不变）。

**别一起删掉的两样**：

- `warnIfNotReady` 保留，由上面那个 `f.Key == "executor_default"` 分支调用。
- 今天 Windows 特判里那行 `slog.Info("Windows 平台：角色选项限定为协调者", "reason", "agentd 进程承载层未实现（B37）")` **要保留**，移进 `Form` 里构造 Windows 角色字段的分支。它是关键节点日志：桌面端没有终端可看 `Notice`，日志是唯一能事后确认「这台机器为什么只给了一个角色」的地方。

- [ ] **Step 4: 跑金样比对**

Run: `go test ./internal/initflow/ -run TestAskAllGolden -count=1`
Expected: **PASS**。

失败时**不要用 `-update` 把金样刷掉**——差异就是回归。逐行看 diff：多半是 `Notice` 的换行数或字段顺序对不上。远程配对那几问的消失是**预期内**的差异，此时按下面的方式一次性重录并在提交信息里写明：

```bash
go test ./internal/initflow/ -run TestAskAllGolden -update -count=1
git diff internal/initflow/testdata   # 差异必须只有配对那几行
```

- [ ] **Step 5: 用例名集合比对**

Run:
```bash
go test ./cmd/... ./internal/initflow/... -count=1 -v 2>&1 \
  | grep -E "^--- (PASS|FAIL)" | awk '{print $3}' | sort > /tmp/after-askall.txt
diff /tmp/before-askall.txt /tmp/after-askall.txt
grep -c "^--- FAIL" /tmp/after-askall.txt || true
```
Expected: `diff` 只应出现**新增**的用例（Task 2 的 form 用例），**不得有任何删除**；after 侧无 FAIL。

- [ ] **Step 6: 全量回归**

Run: `go test ./... -count=1`
Expected: 全绿

- [ ] **Step 7: Commit**

```bash
git add internal/initflow
git commit -m "refactor(initflow): AskAll 改为字段表的消费者

提问顺序即 Form 的切片顺序；Windows 特判从函数开头的 if 变成 role 字段的
Notice；askListen 与远程配对循环删除（配对已移交控制台，见
2026-08-17-console-machine-add.md）。CLI 行为由改造前先录的金样把关：
除配对那几问的消失外逐字不变。"
```

---

## Task 4: 桌面 Go 侧改为一次性交表

**Files:**
- Modify: `desktop/internal/shell/wizard.go`
- Modify: `desktop/main.go`（`startWizard` 内，行 315–400 一带）
- Test: `desktop/internal/shell/wizard_test.go`

**Interfaces:**
- Consumes: `initflow.Form` / `initflow.Apply`（Task 2/3）、`pathenv.Apply`、`toolchain.Detect`、`config.Defaults`、`config.Save`
- Produces:
  - `func BuildForm(cfg *config.Config, rs []toolchain.Result, goos string) []initflow.Field`（薄封装，供测试不经 Wails 覆盖）
  - `func ApplyAnswers(cfg *config.Config, fields []initflow.Field, answers map[string]string) error`

**注意：本项目没有注册任何 Wails binding**（`desktop/frontend/bindings/` 下只有 Wails 自身的内部文件）。因此仍走**事件通道**，只把形状从「一问一答」换成「一次交表 + 一次回传」。spec §5.1 写的「经 binding」应按此理解。

- [ ] **Step 1: 写失败的测试**

`desktop/internal/shell/wizard_test.go` 重写为（不 import Wails）：

```go
func TestBuildFormHasNoTerminalLanguage(t *testing.T) {
	cfg := &config.Config{Listen: "127.0.0.1:7777", Targets: map[string]config.Target{}}
	for _, f := range BuildForm(cfg, nil, "darwin") {
		if strings.Contains(f.Title, "回车") || strings.Contains(f.Notice, "回车") {
			t.Fatalf("字段 %s 含终端语言：%q / %q", f.Key, f.Title, f.Notice)
		}
	}
}

func TestApplyAnswersRejectsBadValue(t *testing.T) {
	cfg := &config.Config{Listen: "127.0.0.1:7777", Targets: map[string]config.Target{}}
	fields := BuildForm(cfg, nil, "darwin")
	if err := ApplyAnswers(cfg, fields, map[string]string{
		"role": initflow.RoleBoth, "executor_default": "不存在",
	}); err == nil {
		t.Fatal("越界答案必须被拒，否则半截配置会落盘")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && go test ./internal/shell/ -run 'TestBuildForm|TestApplyAnswers' -count=1`
Expected: 编译失败，`BuildForm undefined`

- [ ] **Step 3: 重写 `wizard.go`**

删除 `eventPrompter`（`Select`/`Input`/`Confirm` 三个阻塞方法）、`Question`、`Transport`、`NewEventPrompter`、`noticeWriter`、`NewNoticeWriter`。

> `NewNoticeWriter` 必须删而不是留着：它的作用是把 `AskAll` 写进 `io.Writer` 的流式提示转成事件，而新设计里桌面壳**根本不调用 `AskAll`**，那个 writer 没有生产者。

改为两个纯函数：

```go
// BuildForm 造出首次配置的字段表。
//
// 它只是 initflow.Form 的一层薄封装，存在的理由是让桌面侧的用法可以被
// 不 import Wails 的普通 go test 覆盖（薄壳纪律：shell 包不碰 Wails）。
func BuildForm(cfg *config.Config, rs []toolchain.Result, goos string) []initflow.Field {
	return initflow.Form(cfg, rs, goos, false) // 首次配置恒 cfgExisted=false
}

// ApplyAnswers 校验前端回传的答案并写回 cfg。
//
// 承重：**返回错误时调用方绝不可落盘**。半截答案落盘会造出一份让
// shell.Resolve 判为「已配置」的文件，向导从此再也不会出现（W5b-2 缺陷 A）。
func ApplyAnswers(cfg *config.Config, fields []initflow.Field, answers map[string]string) error {
	return initflow.Apply(cfg, fields, answers)
}
```

- [ ] **Step 4: 改 `desktop/main.go` 的接线**

`startWizard` 内（现行 356–380 行一带）改为：

```go
cfg := config.Defaults()
// 先补 PATH 再探测：双击启动的 GUI 继承 launchd 的默认 PATH
//（/usr/bin:/bin:/usr/sbin:/sbin），四家 executor 全部装在它之外，
// 不补就会全部报「未安装」——而「双击就能用」正是薄壳的立项理由。
// pathenv 正是为这件事存在（其目录表里 .opencode/bin 一行注明「B71 故障现场」）。
pathenv.Apply(ctx, pathenv.Options{}, logger)
results := toolchain.Detect()
logger.Info("工具链探测完成", "count", len(results))

fields := shell.BuildForm(cfg, results, runtime.GOOS)
logger.Info("首次配置表已生成", "fields", len(fields))
app.Event.Emit("wizard-form", fields)

answers, err := waitAnswers(wizCtx) // 阻塞等前端一次性回传
if err != nil {
	logger.Info("首次配置被取消", "cause", err)
	return // 承重：不落盘
}
if err := shell.ApplyAnswers(cfg, fields, answers); err != nil {
	logger.Error("首次配置答案校验失败", "cause", err)
	app.Event.Emit("wizard-error", err.Error())
	return // 承重：不落盘
}
if err := config.Save(path, cfg); err != nil { /* 现状不变 */ }
logger.Info("首次配置已写盘", "path", path, "role", answers["role"])
```

`waitAnswers` 监听一次 `wizard-submit` 事件（payload 为 `map[string]string`），与 `wizCtx` 取消竞争，先到者胜。事件注册必须在 `Emit("wizard-form", ...)` **之前**完成，否则前端秒填秒交时会丢事件。

同时保留 W5b-2 已验证的两处：`WindowRuntimeReady` 之后才发第一个事件（早发会被前端 `window._wails` 未就绪的守卫静默丢弃）；`WindowClosing` 调 `wizCancel`。

- [ ] **Step 5: 跑测试确认通过**

Run: `cd desktop && go test ./... -count=1`
Expected: PASS

- [ ] **Step 6: 关键节点日志自检**

确认这些日志存在（Step 4 已写，此步核对）：工具链探测完成（带数量）、表已生成（带字段数）、被取消（带 cause）、答案校验失败（带 cause）、写盘成功（带路径与角色）。**成功路径不得静默**。

- [ ] **Step 7: Commit**

```bash
git add desktop
git commit -m "feat(desktop): 首次配置改为一次性交表，并在探测前补 PATH

事件形状从「一问一答」换成「一次 wizard-form + 一次 wizard-submit」。
eventPrompter 与 NewNoticeWriter 删除——桌面壳不再调 AskAll，后者没有
生产者。顺带修好双击启动时四家 executor 全报「未安装」：GUI 继承 launchd
默认 PATH，Detect 前先 pathenv.Apply。承重不变：校验失败绝不落盘。"
```

---

## Task 5: 前端单页表单

**Files:**
- Rewrite: `desktop/frontend/src/wizard.ts`

**Interfaces:**
- Consumes: `wizard-form` 事件（payload：`Field[]`）、`wizard-error` 事件（payload：`string`）
- Produces: `wizard-submit` 事件（payload：`Record<string, string>`）

- [ ] **Step 1: 定义与 Go 对齐的类型**

```ts
// 与 internal/initflow 的 Field/Cond 一一对应。
// **键名是对外契约**：Go 侧 Option 已加 json tag（value/label），
// Field 的 json tag 见 internal/initflow/form.go。改名等于改协议。
interface Option { value: string; label: string }
interface Cond { key: string; equal?: string; in?: string[]; non_empty?: boolean }
interface DefaultRule { cond: Cond; value: string }
interface Field {
  key: string
  kind: 'select' | 'input' | 'confirm'
  title: string
  notice: string
  default: string
  options?: Option[]
  roles?: string[]
  advanced: boolean
  show_when?: Cond | null
  default_when?: DefaultRule[]
}
```

- [ ] **Step 2: 实现渲染与显隐**

- 一次性渲染全部字段，`advanced === false` 的放上部常显区，`advanced === true` 的放进默认折叠的 `<details>`「高级设置」。
- 每个控件的当前值存进一个 `answers: Record<string, string>`；`confirm` 存 `"true"`/`"false"`。
- 用户没碰过的字段取 `defaultOf(f, answers)`，与 Go 的 `DefaultOf` 同构（命中的第一条 `default_when` 优先，都不命中用 `default`）。角色一变，监听预设的预选档要跟着翻——**但只在用户还没手动改过监听预设时翻**，否则会把用户刚选的档冲掉。用一个 `touched: Set<string>` 记录被手动改过的字段：

```ts
function match(c: Cond, ans: Record<string, string>): boolean {
  const v = ans[c.key] ?? ''
  if (c.non_empty) return v !== ''
  if (c.in) return c.in.includes(v)
  return v === (c.equal ?? '')
}

function defaultOf(f: Field, ans: Record<string, string>): string {
  for (const r of f.default_when ?? []) if (match(r.cond, ans)) return r.value
  return f.default
}
```

- 显隐规则**从 `roles` / `show_when` 数据算**，不得在前端内嵌任何具体字段名的分支：

```ts
// roleMatches 与 Go 侧 initflow.roleMatches 同构：RoleBoth 同时算执行机与协调者。
// 这里是**渲染**，不是规则来源——规则来自 field.roles 这份数据。
function visible(f: Field, ans: Record<string, string>): boolean {
  const role = ans['role'] ?? ''
  if (f.roles && f.roles.length > 0) {
    const ok = f.roles.some(r => r === role || (role === 'both' && (r === 'executor' || r === 'coordinator')))
    if (!ok) return false
  }
  return f.show_when ? match(f.show_when, ans) : true
}
```

- 任一控件变化后重算全部字段的可见性并重绘（字段总数 ≤ 9，全量重绘最简单也最不容易出错）。
- 底部单个「完成」按钮 → `Events.Emit('wizard-submit', answers)`。提交的 `answers` **必须包含每个当前可见字段**，折叠在「高级设置」里、用户一次都没碰过的也要带上它的 `defaultOf` 值（spec §5.2：折叠状态下这些字段仍以默认值参与提交）。不可见字段带不带都行——`Apply` 会忽略它们。
- `wizard-error` 事件到达时在按钮上方展示原文，**不清空已填内容**。

- [ ] **Step 3: 构建并人工看一眼**

Run: `cd desktop && wails3 task build && HOME=$(mktemp -d) ./bin/handoff-desktop`
Expected: 打开即整页表单；顶部只有角色与监听地址；「高级设置」默认折叠；把角色切成「协调者」后执行者相关区块消失、`sync.auto` 出现。

- [ ] **Step 4: 确认工作区仍干净**

Run: `git status --porcelain`
Expected: **零输出**。构建产物不得入库（W5b-1 已在此栽过一次）。

- [ ] **Step 5: Commit**

```bash
git add desktop/frontend/src/wizard.ts
git commit -m "feat(desktop): 首次配置单页表单

一次渲染全部字段，advanced 收进默认折叠的「高级设置」，显隐从 roles /
show_when 数据算——前端不内嵌任何具体字段名的分支，规则仍只有 Go 一份。
不再出现「直接回车即保留预选项」这类终端语言。"
```

---

## Task 6: 端到端验收

**Files:** 无（纯验证）

- [ ] **Step 1: 全量测试**

Run: `go test ./... -count=1 && cd desktop && go test ./... -count=1`
Expected: 全绿

- [ ] **Step 2: 判据复验（SIGKILL 不留痕）**

```bash
cd desktop && wails3 task build
FIX=$(mktemp -d) && go build -o "$FIX/handoff" ../
TH=$(mktemp -d)
PATH="$FIX:$PATH" HOME="$TH" ./bin/handoff-desktop >/tmp/wizverify.log 2>&1 &
PID=$!; sleep 8; kill -9 $PID; sleep 1
find "$TH" ; grep -o 'existing=[^ ]*' /tmp/wizverify.log | head -1
```
Expected: `$TH/.handoff` **整个不存在**；`existing=` 指向 `$FIX/handoff`（证明验的是新构建，不是已安装的旧 CLI）。

> **不要**把新构建的 CLI 覆盖到 `~/.local/bin/handoff`。macOS 的 `com.apple.provenance` 会让就地覆盖后的二进制被 SIGKILL（rc=137），且用备份 `cp` 回同一 inode 会再中一次。PATH 前置即可——`binpath.go` 的候选顺序是 `~/.local/bin` 优先、取不到才 `exec.LookPath`，而临时 HOME 下第一档不存在。

- [ ] **Step 3: 真机走查（必须双击启动，不经终端）**

把 `.app` 放到 Finder 里**双击**打开（从终端启动会继承终端 PATH，验不出 §6 那条缺陷）：

1. 首次配置页上四家 executor 的探测结果与登录态正确（本机实际装了哪些、登录了哪些）
2. 默认值已填，不改任何东西直接点「完成」能走通
3. 切角色时区块显隐正确；一台没装任何 executor 的机器上把角色从「协调者」改成「执行机」，监听预设应自动翻到「所有网卡」（手动改过监听预设后再切角色则不翻）
4. 「高级设置」折叠状态下提交，配置里那些字段仍是默认值而非空值
5. 完成后进入控制台
6. 页面上**没有**远程配对那一段

逐条把实际观察记进 ledger。**「代码看起来对」不能替代观察。**

- [ ] **Step 4: 回归确认配对没丢**

在控制台机器页新增一台开发机（前置计划的产物），确认它出现在列表里——证明配对能力确实转移了，而不是消失了。

- [ ] **Step 5: Commit ledger**

```bash
git add docs/ledger-onboarding-form.md
git commit -m "docs(ledger): 首次配置单页表单真机走查记录"
```
