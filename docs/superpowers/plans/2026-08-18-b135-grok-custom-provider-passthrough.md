# grok 自定义 provider 透传实现计划（B135）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让配了自定义 provider 的机器能正常派 grok —— 把用户权威 `~/.grok/config.toml` 里的 `[model.*]` 段与 `[models] default` 搬进任务级 config.toml。

**Architecture:** 新增一个只做文本切段的纯函数模块，`WriteTaskEnv` 是唯一调用点。不解析 TOML、不改写任何字段值、不引入新依赖；`[ui]` / `[permission]` / `[cli]` 三段仍然只由 handoff 生成。

**Tech Stack:** Go（标准库 `strings` / `os` / `log/slog`），无新增模块。

## Global Constraints

- **不引入任何新 Go 模块。** 本仓零 TOML 依赖，段抽取靠手写文本切分；`go.mod` 不得有任何改动。
- **不改任何已有函数签名。** `WriteTaskEnv(taskDir, model string) (homeDir string, err error)` 的参数与返回保持不变。
- **不动 opencode / claude / codex 的任务环境生成。** 本条只碰 `internal/executor/grok/`。
- **日志纪律（承重）**：任何情况下不打印段内容、不打印字段值。`[model.*]` 段里有 `api_key`。只允许打段名（如 `model.deepseek-v4-pro`）与条数。
- **不改写段内任何字段。** 不把 `api_key` 转成 `env_key`，不做任何"脱敏"。理由：`~/.grok/config.toml` 本来就在同一用户 home 里明文存着这个 key，任务目录是 0700 / 文件 0600 / 同机同用户，复制一份不跨越信任边界；而只改 `api_key` 不管 `extra_headers` / `query_params` 会制造"任务目录无密钥"的错觉。
- **搬运失败一律不许拖垮派发。** 权威配置不存在、读不动、无自定义 provider，三种情况都按"无操作"继续。
- 每个 task 完成即 commit，提交信息按各 Task 的 Commit 步骤给定的原文写。
- 完工门（六条全绿才算完）：

  ```
  go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go') && GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=amd64 go vet ./...
  ```

  `gofmt -l` 必须**无输出**。测试全绿不等于格式干净，这两件事分开验。

## 背景：为什么要做这个

在配了自定义 provider 的机器上，派 grok 一律起不来：

```
handoff dispatch --target win-b37 --executor grok plan.md
→ 500: 启动 executor 失败: grok 未登录或凭据已失效，请在本机执行 `grok login`
       后重试: ACP 错误 -32000: Authentication required
```

报错指向"没登录"，根因却不是登录。`WriteTaskEnv` **重新生成**一份任务级 `config.toml`，只写 `[ui]` / `[models]` / `[permission]` / `[cli]` 四段；用户权威配置里的 `[model.<名字>]` provider 定义块不在其中。任务 home 里的 grok 因此不认识那个模型名，回落内建 x.ai provider，于是要认证。

已排除更省事的解释：显式传 `--model deepseek-v4-pro` 重派，同样 500、同样报文。缺的是 provider 定义本身，不是默认模型名。

## File Structure

| 文件 | 职责 | 新建/改 |
|---|---|---|
| `internal/executor/grok/providercarry.go` | 从权威 config.toml 文本里抽 `[model.*]` 段与 `[models] default`；读文件与日志 | 新建 |
| `internal/executor/grok/providercarry_internal_test.go` | 纯抽取逻辑的表驱动单测（`package grok`，要够着未导出函数） | 新建 |
| `internal/executor/grok/taskenv.go` | `WriteTaskEnv` 织入搬运结果 | 改 |
| `internal/executor/grok/taskenv_test.go` | 生成结果的端到端断言（`package grok_test`） | 改 |

**为什么抽取逻辑单开一个文件**：`taskenv.go` 已经承担"生成 config + 生成 auth 软链 + 权限规则表"三件事，再塞进一套文本切段解析会让它同时管"生成什么"和"怎么读别人的格式"。切段是可以独立理解、独立测试的一件事，给它自己的文件。

---

## Task 1: 段抽取纯函数

只做文本进、结构出。不碰文件系统、不打日志——日志在 Task 2 的调用方。

**Files:**
- Create: `internal/executor/grok/providercarry.go`
- Test: `internal/executor/grok/providercarry_internal_test.go`

**Interfaces:**
- Produces:
  - `type carryResult struct { ModelSections string; SectionNames []string; DefaultModel string }`
  - `func extractProviderConfig(content string) carryResult`
  - `func sectionHeader(line string) (string, bool)`
  - `func defaultValue(line string) (string, bool)`

  Task 2 会调用 `extractProviderConfig` 并读 `carryResult` 的三个字段。

- [ ] **Step 1: 写失败的测试**

新建 `internal/executor/grok/providercarry_internal_test.go`。注意 `package grok`（内部测试包）——`extractProviderConfig` 未导出，外部测试包够不着；仓内已有 `stop_internal_test.go`、`watchdog_internal_test.go` 等同样写法。

```go
// providercarry_internal_test.go —— 段抽取纯逻辑的表驱动断言。
//
// 用内部测试包（package grok）：被测函数未导出。这些用例全是字符串进、
// 结构出，不碰文件系统、不起进程。
package grok

import (
	"strings"
	"testing"
)

func TestExtractProviderConfig(t *testing.T) {
	cases := []struct {
		name         string
		content      string
		wantNames    []string
		wantDefault  string
		wantContains []string // ModelSections 必须含有的片段
		wantAbsent   []string // ModelSections 必须不含的片段
	}{
		{
			name: "两个 provider 段与 default 全部抽出",
			content: `[models]
default = "deepseek-v4-pro"

[model.deepseek-v4-pro]
model = "deepseek-v4-pro"
base_url = "https://example.invalid/v1"
api_key = "sk-SENTINEL-PRO"

[model.deepseek-v4-flash]
model = "deepseek-v4-flash"
api_key = "sk-SENTINEL-FLASH"
`,
			wantNames:   []string{"model.deepseek-v4-pro", "model.deepseek-v4-flash"},
			wantDefault: "deepseek-v4-pro",
			wantContains: []string{
				"[model.deepseek-v4-pro]", "sk-SENTINEL-PRO",
				"[model.deepseek-v4-flash]", "sk-SENTINEL-FLASH",
			},
			wantAbsent: []string{"[models]"},
		},
		{
			name: "provider 段后跟别的段：切在边界不吞下一段",
			content: `[model.x]
model = "x"

[marketplace]
enabled = true

[ui]
permission_mode = "always-approve"
`,
			wantNames:    []string{"model.x"},
			wantDefault:  "",
			wantContains: []string{"[model.x]", `model = "x"`},
			wantAbsent:   []string{"[marketplace]", "enabled = true", "always-approve"},
		},
		{
			name: "段内注释与缩进原样保留",
			content: `[model.y]
  # 这条注释必须活下来
  model = "y"
`,
			wantNames:    []string{"model.y"},
			wantContains: []string{"  # 这条注释必须活下来", `  model = "y"`},
		},
		{
			name: "数组表 [[x]] 终结 provider 段，不被误收",
			content: `[model.z]
model = "z"

[[servers]]
url = "https://example.invalid"
`,
			wantNames:    []string{"model.z"},
			wantContains: []string{"[model.z]"},
			wantAbsent:   []string{"[[servers]]", "url ="},
		},
		{
			name: "default 带行内注释",
			content: `[models]
default = "abc"  # 平时用这个
`,
			wantDefault: "abc",
		},
		{
			name: "default_reasoning_effort 不得被误当成 default",
			content: `[models]
default_reasoning_effort = "high"
`,
			wantDefault: "",
		},
		{
			name: "无 provider 段无 default：返回零值",
			content: `[ui]
permission_mode = "always-approve"
`,
			wantDefault: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractProviderConfig(c.content)

			if got.DefaultModel != c.wantDefault {
				t.Errorf("DefaultModel = %q，期望 %q", got.DefaultModel, c.wantDefault)
			}
			if len(got.SectionNames) != len(c.wantNames) {
				t.Fatalf("SectionNames = %v，期望 %v", got.SectionNames, c.wantNames)
			}
			for i, want := range c.wantNames {
				if got.SectionNames[i] != want {
					t.Errorf("SectionNames[%d] = %q，期望 %q", i, got.SectionNames[i], want)
				}
			}
			for _, want := range c.wantContains {
				if !strings.Contains(got.ModelSections, want) {
					t.Errorf("ModelSections 缺 %q，实际:\n%s", want, got.ModelSections)
				}
			}
			for _, bad := range c.wantAbsent {
				if strings.Contains(got.ModelSections, bad) {
					t.Errorf("ModelSections 不该含 %q，实际:\n%s", bad, got.ModelSections)
				}
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/executor/grok/ -run TestExtractProviderConfig -count=1`
Expected: 编译失败，`undefined: extractProviderConfig`

- [ ] **Step 3: 写最小实现**

新建 `internal/executor/grok/providercarry.go`：

```go
// providercarry.go —— 从用户权威 grok 配置里搬运自定义 provider 定义。
//
// 职责：
//   - 从 ~/.grok/config.toml 的文本里抽出 [model.*] 段与 [models] 的 default
//   - 只做文本切段，不解析 TOML、不改写任何字段值
//
// 边界：
//   - 不写文件：结果交 WriteTaskEnv 织进任务级 config.toml
//   - 不判断字段含义、不识别密钥：段内字节原样搬运
//   - 不搬 [ui] / [permission] / [cli]：那三段永远以 handoff 为准，搬过来
//     等于让用户的个人配置覆盖任务级权限隔离
//
// 为什么不引入 TOML 库：本仓零 TOML 依赖，且 WriteTaskEnv 本身就是手写字符串
// 拼接。「解析 + 再序列化」会重排键、丢掉用户注释，生成的 config 不再一眼可读；
// 原样搬字节连注释都保得住。代价是自己认段边界，已知边界见 extractProviderConfig。
//
// 日志纪律：只打段名与条数，任何情况下不打段内容、不打字段值——[model.*] 段里
// 有 api_key。与 authsync.go 文件头「不打 token 值」同源。
package grok

import (
	"strings"
)

// carryResult 是从权威配置里抽出的可搬运部分。
type carryResult struct {
	// ModelSections 是 [model.*] 各段的原文（原样字节，含注释与段间空行）。
	// 无自定义 provider 时为空串。
	ModelSections string
	// SectionNames 是各段段名（如 "model.deepseek-v4-pro"），**仅供日志**。
	// 段名不是密钥，可以打；段内容不行。
	SectionNames []string
	// DefaultModel 是 [models] 段里 default 的值；权威配置没写时为空串。
	DefaultModel string
}

// extractProviderConfig 从 config.toml 的内容里抽出可搬运部分。
//
// 参数：
//   - content: 权威 config.toml 的全文
//
// 返回：抽取结果；没有任何 [model.*] 段与 default 时返回零值（非错误）。
//
// 注意：段边界靠「行首（允许前导空白）以 [ 开头」判定，不解析 TOML。
// **已知边界**：若某字段值是跨行数组、且续行顶格以 [ 开头，会被误判成段边界，
// 导致该 provider 段被截断。真实 provider 段的字段都是单行标量或内联表
// （extra_headers = { … }），不触发这条；用测试固化这个形态，不做更复杂的解析。
func extractProviderConfig(content string) carryResult {
	var (
		res      carryResult
		buf      strings.Builder
		inModel  bool
		inModels bool
	)
	for _, line := range strings.Split(content, "\n") {
		if name, ok := sectionHeader(line); ok {
			inModel = strings.HasPrefix(name, "model.")
			inModels = name == "models"
			if inModel {
				res.SectionNames = append(res.SectionNames, name)
			}
		}
		switch {
		case inModel:
			buf.WriteString(line)
			buf.WriteString("\n")
		case inModels:
			if v, ok := defaultValue(line); ok {
				res.DefaultModel = v
			}
		}
	}
	res.ModelSections = buf.String()
	return res
}

// sectionHeader 判断一行是不是段头，是则返回段名。
//
// 段名取 '[' 之后到第一个 ']' 之前的原文。数组表 [[x]] 会得到 "[x"——它不以
// "model." 开头，因此被当成普通段，**正确地终结**上一个 model 段而不被误收。
func sectionHeader(line string) (string, bool) {
	t := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(t, "[") {
		return "", false
	}
	end := strings.Index(t, "]")
	if end < 1 { // 只有 "[" 没有 "]"，或形如 "[]"，都不是有效段头
		return "", false
	}
	return t[1:end], true
}

// defaultValue 从 [models] 段的一行里取 default 的值。
//
// 返回：值与是否命中。带引号的值取到闭引号——这样 `default = "x"  # 注释`
// 也能正确解析；不带引号的截到第一个 '#'。
//
// 为什么要检查 '=' 紧跟在 "default" 之后：grok 的 [models] 段里还有
// default_reasoning_effort，只按前缀匹配会把它的值错当成默认模型名。
func defaultValue(line string) (string, bool) {
	t := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(t, "default") {
		return "", false
	}
	rest := strings.TrimSpace(t[len("default"):])
	if !strings.HasPrefix(rest, "=") {
		return "", false
	}
	rest = strings.TrimSpace(rest[1:])
	if strings.HasPrefix(rest, `"`) {
		if end := strings.Index(rest[1:], `"`); end >= 0 {
			return rest[1 : 1+end], true
		}
		return "", false // 引号没闭合，当没写
	}
	if i := strings.Index(rest, "#"); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	return rest, true
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/grok/ -run TestExtractProviderConfig -count=1 -v`
Expected: 七个子用例全 PASS

- [ ] **Step 5: 加注释**

Step 3 的代码里，文件头块注释、`carryResult` 三个字段注释、三个函数的 doc 注释即为本步产出。逐条自检它们回答的是"为什么"而不是"做了什么"：

- 文件头必须写明**为什么不引入 TOML 库**（否则下一个人第一反应就是加依赖）
- `extractProviderConfig` 必须写明**已知边界**（跨行数组续行顶格 `[`），不许省略
- `sectionHeader` 必须写明 `[[x]]` 为什么是**对的**行为而不是漏洞
- `defaultValue` 必须写明为什么要检查 `=`（`default_reasoning_effort`）

**本 task 不加日志**：三个函数都是纯函数，无 I/O、无状态变更，日志加在 Task 2 的调用方（那里才知道任务 home、才有失败路径）。

- [ ] **Step 6: Commit**

```bash
git add internal/executor/grok/providercarry.go internal/executor/grok/providercarry_internal_test.go
git commit -m "feat(grok): 加权威配置的 provider 段抽取"
```

---

## Task 2: 织入 WriteTaskEnv

读权威配置、按优先级定 default、把 provider 段追加进生成结果。

**Files:**
- Modify: `internal/executor/grok/providercarry.go`（追加读文件与日志两个函数）
- Modify: `internal/executor/grok/taskenv.go:76-135`（`WriteTaskEnv` 函数体）
- Modify: `internal/executor/grok/taskenv_test.go`（追加用例与一个 helper）

**Interfaces:**
- Consumes: Task 1 的 `extractProviderConfig(content string) carryResult` 与 `carryResult` 的三个字段 `ModelSections` / `SectionNames` / `DefaultModel`
- Produces:
  - `func authorityConfigPath() (string, error)` —— 返回 `~/.grok/config.toml`
  - `func loadAuthorityProviderConfig(log *slog.Logger) carryResult` —— 读+抽，**不返回 error**

- [ ] **Step 1: 写失败的测试**

在 `internal/executor/grok/taskenv_test.go` 末尾追加。注意本文件是 `package grok_test`（外部测试包），所以只能经 `WriteTaskEnv` 这个导出入口断言。

```go
// fakeAuthorityConfig 把 HOME 指向临时目录并写出一份权威 config.toml，
// 返回该临时 HOME。body 为空串表示「权威配置不存在」。
//
// 同时设 USERPROFILE：os.UserHomeDir 在 Windows 上读的是它而不是 HOME。
// 本包的测试目前只在 unix runner 上跑，但多设一个变量零成本，省得以后
// CI 扩包时才发现这里是个哑弹。
func fakeAuthorityConfig(t *testing.T, body string) string {
	t.Helper()
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	t.Setenv("USERPROFILE", fake)
	if body == "" {
		return fake
	}
	grokDir := filepath.Join(fake, ".grok")
	if err := os.MkdirAll(grokDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grokDir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return fake
}

const testAuthorityConfig = `[models]
default = "deepseek-v4-pro"

[model.deepseek-v4-pro]
model = "deepseek-v4-pro"
base_url = "https://example.invalid/v1"
api_key = "sk-SENTINEL-DO-NOT-LOG"
api_backend = "chat_completions"
context_window = 131072

[marketplace]
enabled = true
`

// TestWriteTaskEnvCarriesCustomProvider 验证权威配置里的 provider 定义被搬进
// 任务 config——不搬的话，配了自定义 provider 的机器上 grok 会回落内建
// provider 并报 Authentication required（B135）。
func TestWriteTaskEnvCarriesCustomProvider(t *testing.T) {
	fakeAuthorityConfig(t, testAuthorityConfig)

	home, err := grok.WriteTaskEnv(t.TempDir(), "")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)

	if !strings.Contains(cfg, "[model.deepseek-v4-pro]") {
		t.Errorf("provider 段没被搬进来，实际:\n%s", cfg)
	}
	if !strings.Contains(cfg, `base_url = "https://example.invalid/v1"`) {
		t.Errorf("provider 段字段丢失，实际:\n%s", cfg)
	}
	// 没传 model 时用权威 default 兜底
	if !strings.Contains(cfg, `default = "deepseek-v4-pro"`) {
		t.Errorf("应以权威 default 兜底，实际:\n%s", cfg)
	}
	// 权威的 [marketplace] 不该被搬：只搬 [model.*]
	if strings.Contains(cfg, "[marketplace]") {
		t.Errorf("[marketplace] 不该被搬进任务 config，实际:\n%s", cfg)
	}
	// [models] 只能出现一次——TOML 不允许同名表定义两次，出现两次 grok 直接报错
	if n := strings.Count(cfg, "[models]"); n != 1 {
		t.Errorf("[models] 出现 %d 次，必须恰好 1 次（TOML 不允许重复表定义），实际:\n%s", n, cfg)
	}
}

// TestWriteTaskEnvFlagModelBeatsAuthorityDefault 验证 --model 传入值压过权威
// default——协调者显式指定的模型不能被用户的个人默认值悄悄换掉。
func TestWriteTaskEnvFlagModelBeatsAuthorityDefault(t *testing.T) {
	fakeAuthorityConfig(t, testAuthorityConfig)

	home, err := grok.WriteTaskEnv(t.TempDir(), "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)

	if !strings.Contains(cfg, `default = "deepseek-v4-flash"`) {
		t.Errorf("应以 --model 传入值为准，实际:\n%s", cfg)
	}
	if strings.Contains(cfg, `default = "deepseek-v4-pro"`) {
		t.Errorf("权威 default 不该压过传入值，实际:\n%s", cfg)
	}
}

// TestWriteTaskEnvOmitsDefaultWhenNeitherSourceHasOne 验证两个来源都没有模型名
// 时**不写 default 这一行**——写一行空值会让 grok 拿空串当模型名去请求。
func TestWriteTaskEnvOmitsDefaultWhenNeitherSourceHasOne(t *testing.T) {
	fakeAuthorityConfig(t, `[model.x]
model = "x"
base_url = "https://example.invalid/v1"
`)

	home, err := grok.WriteTaskEnv(t.TempDir(), "")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)

	if strings.Contains(cfg, "default =") {
		t.Errorf("两个来源都没模型名时不该写 default 行，实际:\n%s", cfg)
	}
	// 但 provider 段照搬不误——这两件事互不依赖
	if !strings.Contains(cfg, "[model.x]") {
		t.Errorf("provider 段仍应搬运，实际:\n%s", cfg)
	}
}

// TestWriteTaskEnvWithoutAuthorityIsByteIdentical 是**回归保护**：不用自定义
// provider 的用户必须零影响。权威配置不存在时，生成结果要与本条改动之前
// 逐字节相同。
//
// golden 取自改动前 `WriteTaskEnv(dir, "grok-4.5")` 的实际输出。
func TestWriteTaskEnvWithoutAuthorityIsByteIdentical(t *testing.T) {
	fakeAuthorityConfig(t, "") // 权威配置不存在

	home, err := grok.WriteTaskEnv(t.TempDir(), "grok-4.5")
	if err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "taskenv_config_golden.toml"))
	if err != nil {
		t.Fatalf("读 golden 失败（先按 Step 3 生成它）: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("无权威配置时生成结果与 golden 不一致——不用自定义 provider 的用户被影响了。\n实际:\n%s\n期望:\n%s", got, want)
	}
}

// TestWriteTaskEnvNeverLogsSecrets 是**承重**用例：日志里出现 api_key 等于
// 把用户密钥写进 agentd.log。
func TestWriteTaskEnvNeverLogsSecrets(t *testing.T) {
	fakeAuthorityConfig(t, testAuthorityConfig)

	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	if _, err := grok.WriteTaskEnv(t.TempDir(), ""); err != nil {
		t.Fatalf("WriteTaskEnv 出错: %v", err)
	}
	if strings.Contains(buf.String(), "sk-SENTINEL-DO-NOT-LOG") {
		t.Errorf("日志里出现了 api_key，实际日志:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "example.invalid") {
		t.Errorf("日志里出现了 base_url，段内容一律不许进日志，实际日志:\n%s", buf.String())
	}
	// 段名可以打，且应该打——否则出问题时无从判断到底搬了什么
	if !strings.Contains(buf.String(), "model.deepseek-v4-pro") {
		t.Errorf("日志应含段名以便排障，实际日志:\n%s", buf.String())
	}
}
```

本文件的 import 块需补 `bytes` 与 `log/slog`（`os` / `path/filepath` / `strings` / `testing` 已有）：

```go
import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/grok"
)
```

- [ ] **Step 2: 生成 golden，然后跑测试确认它失败**

golden 必须取自**改动前**的输出，否则这条回归保护就是自证。先在当前（未改 `taskenv.go`）状态下生成。

用一个**临时测试**来生成，而不是 `go run` 一个仓外的 .go 文件——仓外文件不在 module 里，`go run` 会因找不到 `github.com/Xsxdot/handoff/...` 而失败：

```bash
mkdir -p internal/executor/grok/testdata
cat > internal/executor/grok/gen_golden_test.go <<'EOF'
package grok_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/grok"
)

// TestGenGolden 是**一次性**的 golden 生成器，跑完即删。
func TestGenGolden(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	home, err := grok.WriteTaskEnv(t.TempDir(), "grok-4.5")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", "taskenv_config_golden.toml"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("golden 已生成，字节数: %d", len(b))
}
EOF
go test ./internal/executor/grok/ -run TestGenGolden -count=1 -v
rm internal/executor/grok/gen_golden_test.go
```

Expected: `-v` 输出里有 `golden 已生成，字节数: <某个数>`，且 `internal/executor/grok/testdata/taskenv_config_golden.toml` 存在

> `t.Setenv("HOME", ...)` 那两行在这里也要写：生成 golden 时必须保证读不到真实的 `~/.grok/config.toml`，否则 golden 会带上跑这条命令那台机器的 provider 段，回归保护就废了。**改动前的实现根本不读权威配置，所以这两行此刻是冗余的——但它们让这段脚本在改动后重跑也仍然正确。**

`rm` 那一步不能忘：留着它会让 `go test ./...` 每次都重写 golden，回归保护变成自证。

然后跑新用例：

Run: `go test ./internal/executor/grok/ -run 'TestWriteTaskEnvCarries|TestWriteTaskEnvFlagModel|TestWriteTaskEnvNeverLogs|TestWriteTaskEnvOmitsDefault' -count=1`
Expected: 四条全 FAIL —— `provider 段没被搬进来`、`应以权威 default 兜底`、`日志应含段名以便排障`、`provider 段仍应搬运`

`TestWriteTaskEnvWithoutAuthorityIsByteIdentical` 此刻应当 PASS（还没改实现），这是正常的：它是回归保护，作用在 Step 3 之后。

- [ ] **Step 3: 写最小实现**

**3a. 在 `internal/executor/grok/providercarry.go` 的 import 块补三个包**：

```go
import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)
```

**3b. 在同文件末尾追加两个函数**：

```go
// authorityConfigPath 返回权威配置路径 ~/.grok/config.toml。
//
// 单开一个函数而不是在调用点拼路径：与 authsync.go 的 authorityAuthPath 同样
// 的理由——两处各拼一遍，将来改动时漏掉一处就会读错文件。
func authorityConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析用户主目录: %w", err)
	}
	return filepath.Join(home, ".grok", configFileName), nil
}

// loadAuthorityProviderConfig 读权威 config.toml 并抽出可搬运部分。
//
// 参数：
//   - log: 日志入口；nil 时退回 slog.Default()
//
// 返回：抽取结果。
//
// **本函数不返回 error**，这是刻意的：搬运是增强不是必需。权威配置不存在
// （用内建 provider 的人就是这样）、读不动、或压根没有自定义 provider，三种
// 情况都按「无操作」继续，失败原因经日志留痕。让一个可选文件拖垮派发是错的。
//
// 日志纪律：只打路径、段名与条数，绝不打段内容——[model.*] 段里有 api_key。
func loadAuthorityProviderConfig(log *slog.Logger) carryResult {
	if log == nil {
		log = slog.Default()
	}
	path, err := authorityConfigPath()
	if err != nil {
		log.Warn("解析权威 grok 配置路径失败，任务 home 不带自定义 provider", "cause", err)
		return carryResult{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debug("未发现权威 grok 配置，任务 home 不带自定义 provider", "path", path)
		} else {
			log.Warn("读权威 grok 配置失败，任务 home 不带自定义 provider", "path", path, "cause", err)
		}
		return carryResult{}
	}
	res := extractProviderConfig(string(b))
	if len(res.SectionNames) == 0 && res.DefaultModel == "" {
		log.Debug("权威 grok 配置无自定义 provider 与 default", "path", path)
	}
	return res
}
```

**3c. 改 `internal/executor/grok/taskenv.go` 的 `WriteTaskEnv`。**

在 `log := slog.Default()` 之后、`defer` 之前插入两个变量声明（defer 里的日志要用到它们，所以必须先声明）：

```go
	// 搬运结果与 default 来源在 defer 的日志里要用，先声明再赋值
	var (
		carried     carryResult
		defaultFrom = "none"
	)
```

把 defer 里的成功分支改成带搬运信息：

```go
	defer func() {
		if err != nil {
			log.Error("grok 生成任务环境失败", "home", homeDir, "cause", err)
		} else {
			log.Info("grok 任务环境已生成", "home", homeDir, "model", model,
				"provider_sections", len(carried.SectionNames),
				"provider_names", carried.SectionNames,
				"default_from", defaultFrom)
		}
	}()
```

在 `os.MkdirAll(homeDir, 0o700)` 之后、`var b strings.Builder` 之前插入：

```go
	// 权威配置里的自定义 provider 定义（B135）。不搬的话，配了自定义 provider
	// 的机器上 grok 不认识任务级 default 里的模型名，会回落内建 x.ai provider
	// 并报 Authentication required——报文指向「没登录」，根因其实是 provider 缺失。
	carried = loadAuthorityProviderConfig(log)

	// default 的取值优先级：--model 传入值 > 权威 config 的 default > 不写。
	// 三选一而不是各写一段：TOML 不允许同名表定义两次，[models] 只能出现一次。
	defaultModel := strings.TrimSpace(model)
	switch {
	case defaultModel != "":
		defaultFrom = "flag"
	case carried.DefaultModel != "":
		defaultModel, defaultFrom = carried.DefaultModel, "authority"
	}
```

把原来的 `[models]` 写入段：

```go
	if m := strings.TrimSpace(model); m != "" {
		b.WriteString("[models]\n")
		fmt.Fprintf(&b, "default = %q\n\n", m)
	}
```

替换成：

```go
	if defaultModel != "" {
		b.WriteString("[models]\n")
		fmt.Fprintf(&b, "default = %q\n\n", defaultModel)
	}
```

在 `b.WriteString("auto_update = false\n")` 之后、`cfgPath := ...` 之前追加：

```go
	// provider 定义追加在末尾：handoff 自己写的四段保持连续，便于逐字节比对与
	// 人工核对。TOML 段顺序无语义，放末尾不影响 grok 读取。
	if carried.ModelSections != "" {
		b.WriteString("\n# 以下 provider 定义由 handoff 从 ~/.grok/config.toml 原样透传，勿手工编辑。\n\n")
		b.WriteString(carried.ModelSections)
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/grok/ -count=1`
Expected: 全 PASS，含新加的四条与既有的 `TestWriteTaskEnvGeneratesPinnedPermissionConfig`

特别确认 `TestWriteTaskEnvWithoutAuthorityIsByteIdentical` 仍然 PASS —— 它现在才真正开始发挥作用（实现已改，golden 是改前的）。若它 FAIL，说明改动影响了不用自定义 provider 的用户，**必须修到它绿为止，不许改 golden**。

- [ ] **Step 5: 补日志与注释自检**

本 task 的日志产出是 Step 3 里的四条：`loadAuthorityProviderConfig` 的三条降级日志（Warn/Debug/Debug）+ `WriteTaskEnv` defer 里的成功日志。对照自检：

- 每个错误分支都带上下文（`path`、`cause`）✓
- 成功路径不静默：`provider_sections` / `provider_names` / `default_from` 三个字段让人一眼看出「到底搬了什么、default 从哪来」✓
- 没有用 `fmt.Printf` ✓
- **段内容与字段值一个字都没进日志** —— 由 `TestWriteTaskEnvNeverLogsSecrets` 机器验证，不靠自觉 ✓

注释产出是 Step 3 的三处：`authorityConfigPath` / `loadAuthorityProviderConfig` 的 doc 注释、`WriteTaskEnv` 里三段"为什么"注释。自检 `loadAuthorityProviderConfig` 是否写明了**为什么不返回 error**（这是最容易被后人"顺手修正"成返回 error 的地方）。

- [ ] **Step 6: 跑完工门**

Run:
```
go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go') && GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=amd64 go vet ./...
```
Expected: 全部通过，`gofmt -l` 无输出

- [ ] **Step 7: Commit**

```bash
git add internal/executor/grok/providercarry.go internal/executor/grok/taskenv.go internal/executor/grok/taskenv_test.go internal/executor/grok/testdata/taskenv_config_golden.toml
git commit -m "fix(grok): 任务级 config 透传权威 provider 定义与 default 兜底"
```

---

## 审核者专属（不要执行这一节）

以下步骤要驱动 handoff CLI 与远程 agentd 本身，与"你就是执行者、不要调用 handoff CLI"的执行纪律直接冲突。**执行者请跳过整节，不要尝试、也不要在 ledger 里声称验过。**

真机验收（win-b37，由审核者在本地驱动）：

1. 把新二进制装到 win-b37，重启 `handoff-agentd` 计划任务
2. 派一份验收任务书 —— **不传 `--model`**，验证权威 default 兜底：

   ```
   handoff dispatch --target win-b37 --new-worktree --new-branch tmp/b135-accept-grok \
     --executor grok <任务书>
   ```

   任务书内容与 B128 验收时用的那份相同：①跑 `echo b135-grok-round-1` 贴回输出；
   ②用写文件工具（不是 shell 重定向）往 `C:\Users\administrator\b135-grok-outside.txt`
   写一行 `grok-approved`，该路径在工作区外会触发协调者授权，等放行、不要绕开。
3. 权限门拦截产工单 → `reply --approve` 放行 → `completed`
4. `Get-Item <taskDir>\grokhome\auth.json` 看到 `ReparsePoint`（auth 软链真的建成）
5. 检查 `<taskDir>\grokhome\config.toml` 含 `[model.deepseek-v4-pro]`
6. `done` 后 worktree 与进程清干净

这一节走完，B128 spec §10 第 7 条才算补验完成。
