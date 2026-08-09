# Agent 启动前注入环境变量 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让用户在执行机 `~/.handoff/env/` 下用 dotenv 文件显式声明「某个 agent 启动时该带什么环境变量」，配置按 agent 名映射，任务执行者与审批者共用同一份。

**Architecture:** 新增纯解析包 `internal/envfile`（`Parse` 无 IO、`Resolver` 负责定位读盘）。注入经 `executor.StartReq.Env` 契约字段下发给 adapter（opencode 落成 `run_serve.sh` 里的 `export` 行），审批者侧在 `defaultRunCmd` 里追加 `cmd.Env`。manager 在 `Dispatch` 最前段解析，失败即拒发。

**Tech Stack:** Go 1.26.1，标准库（`os.Expand` / `regexp` / `bufio`），无新增依赖。

**Spec:** `docs/superpowers/specs/2026-08-09-handoff-agent-env-injection-design.md`

## Global Constraints

- **值绝不进日志**：所有日志只打 key 名与 count。`HTTPS_PROXY=http://user:pass@host` 是正常写法，值里带凭据的概率不低。
- **文件大小上限 64KiB**（`maxEnvFileSize = 64 << 10`）。
- **key 名正则**：`^[A-Za-z_][A-Za-z0-9_]*$`。
- **展开查找顺序**：本文件前面已解析的键 → 外部 `lookup`（生产传 `os.LookupEnv`）→ 空串。
- **配置里的文件名必须是纯文件名**：含 `/`、等于 `.`、等于 `..` 一律报错。
- **失败一律 fail-closed**：派发拒发（HTTP 500 带真因）、审批者 escalate、启动预检只 WARN。
- **`StartReq.Env` 是全体 adapter 的契约，本计划只接 opencode**：`fake` 不需要；`grok`（B3）与 `claude-code`（B2）的落地写在**它们各自的计划里**（[grok plan](2026-08-09-handoff-grok-adapter.md) Task 3/4/5 的 `WriteServeScript`/`StartServe`/`Start`；[claude plan](2026-08-09-handoff-claude-code-adapter.md) 的 `StartProcReq.Env` + `writeRunScript`），三处生成 export 行的形态同构。**本计划完成 ≠ env 注入对所有执行者生效**——不读 `req.Env` 的 adapter 照样编译通过，缺口是静默的。Task 4 收尾时确认这两处已在各自计划里登记（现已登记），不必在本计划里实现。
- **中文注释**：新文件写文件头（职责 + 边界），导出函数写 doc 注释，非显然分支写「为什么」。日志用 `slog`，禁止 `fmt.Printf`。
- **每个 task 结束前**跑 `gofmt -l .`（有输出即不合格）与 `go build ./...`。

## File Structure

| 文件 | 责任 |
|---|---|
| `internal/envfile/envfile.go`（新建） | 纯解析器：文本 → `[]KV`，含单层展开。无文件 IO、无配置依赖 |
| `internal/envfile/envfile_test.go`（新建） | 解析器表驱动单测 |
| `internal/envfile/resolver.go`（新建） | `Dir` / `Resolver.For` / `Resolver.Preflight`：定位、读盘、日志 |
| `internal/envfile/resolver_test.go`（新建） | Resolver 单测（含热更新） |
| `internal/config/config.go`（改） | `Config.Env map[string]string` + 默认值 + `decodeStrict` 已知键文本 |
| `internal/executor/executor.go`（改） | `StartReq.Env []string` 契约字段 |
| `internal/executor/opencode/proc.go`（改） | `StartServe` / `writeServeScript` 接 env，生成 export 行 |
| `internal/executor/opencode/adapter.go`（改） | `Start` 把 `req.Env` 透传给 `StartServe` |
| `internal/agentd/manager.go`（改） | `Manager.env` resolver、`Dispatch` 前段解析、`errEnvResolveFailed` 哨兵 |
| `internal/agentd/server.go`（改） | `writeDispatchError` 映射新哨兵 → 500 + 真因 |
| `internal/agentd/approver.go`（改） | `defaultRunCmd` 方法化并注入 `cmd.Env` |
| `cmd/agentd.go`（改） | 构造 resolver、`Preflight()`、传给 `NewApprover` |
| `README.md`（改） | 配置段补 `env` |

---

### Task 1: envfile 解析器

**Files:**
- Create: `internal/envfile/envfile.go`
- Test: `internal/envfile/envfile_test.go`

**Interfaces:**
- Consumes: 无（本包是叶子）
- Produces:
  - `type KV struct { Key, Value string }`
  - `func Parse(r io.Reader, lookup func(string) (string, bool)) (kvs []KV, dups []string, err error)`
  - `const maxEnvFileSize = 64 << 10`（非导出）

- [ ] **Step 1: 写失败测试**

创建 `internal/envfile/envfile_test.go`：

```go
package envfile

import (
	"strings"
	"testing"
)

// fixedLookup 造一个确定的外部环境，避免测试依赖真实 os.Environ。
func fixedLookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name  string
		input string
		outer map[string]string
		want  []KV
	}{
		{
			name:  "注释与空行被跳过",
			input: "# 注释\n\n   # 缩进后的注释\nA=1\n",
			want:  []KV{{"A", "1"}},
		},
		{
			name:  "export 前缀可选",
			input: "export A=1\nexport\tB=2\nC=3\n",
			want:  []KV{{"A", "1"}, {"B", "2"}, {"C", "3"}},
		},
		{
			name:  "单引号字面量不展开",
			input: "A='literal $B here'\n",
			outer: map[string]string{"B": "x"},
			want:  []KV{{"A", "literal $B here"}},
		},
		{
			name:  "双引号去引号后展开",
			input: `A="v=${B}"` + "\n",
			outer: map[string]string{"B": "x"},
			want:  []KV{{"A", "v=x"}},
		},
		{
			name:  "无引号展开 $VAR 与 ${VAR}",
			input: "A=$B-${B}\n",
			outer: map[string]string{"B": "x"},
			want:  []KV{{"A", "x-x"}},
		},
		{
			name:  "PATH 自引用取外部环境",
			input: "PATH=${PATH}:/usr/local/go/bin\n",
			outer: map[string]string{"PATH": "/usr/bin:/bin"},
			want:  []KV{{"PATH", "/usr/bin:/bin:/usr/local/go/bin"}},
		},
		{
			name:  "文件内前置键优先于外部环境",
			input: "B=inner\nA=${B}\n",
			outer: map[string]string{"B": "outer"},
			want:  []KV{{"B", "inner"}, {"A", "inner"}},
		},
		{
			name:  "未定义变量展开为空串",
			input: "A=[${NOPE}]\n",
			want:  []KV{{"A", "[]"}},
		},
		{
			name:  "值的首尾空白被 trim",
			input: "A=   spaced   \n",
			want:  []KV{{"A", "spaced"}},
		},
		{
			name:  "值里的 # 不是注释",
			input: "A=http://host/a#b\n",
			want:  []KV{{"A", "http://host/a#b"}},
		},
		{
			name:  "CRLF 行尾",
			input: "A=1\r\nB=2\r\n",
			want:  []KV{{"A", "1"}, {"B", "2"}},
		},
		{
			name:  "重复键后者覆盖前者且保持首次位置",
			input: "A=1\nB=2\nA=3\n",
			want:  []KV{{"A", "3"}, {"B", "2"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Parse(strings.NewReader(tc.input), fixedLookup(tc.outer))
			if err != nil {
				t.Fatalf("Parse 意外失败: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("条数不符: got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("第 %d 条: got %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseReportsDuplicateKeys(t *testing.T) {
	_, dups, err := Parse(strings.NewReader("A=1\nB=2\nA=3\n"), nil)
	if err != nil {
		t.Fatalf("Parse 意外失败: %v", err)
	}
	if len(dups) != 1 || dups[0] != "A" {
		t.Fatalf("重复键应为 [A], got %v", dups)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantInErr string
	}{
		{name: "缺等号", input: "A=1\nJUST_A_WORD\n", wantInErr: "第 2 行"},
		{name: "键名以数字开头", input: "1BAD=x\n", wantInErr: "第 1 行"},
		{name: "键名含连字符", input: "BAD-KEY=x\n", wantInErr: "第 1 行"},
		{name: "键名为空", input: "=x\n", wantInErr: "第 1 行"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Parse(strings.NewReader(tc.input), nil)
			if err == nil {
				t.Fatal("期望报错，实际成功")
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("错误应含 %q，实际 %q", tc.wantInErr, err.Error())
			}
		})
	}
}

func TestParseRejectsOversizedFile(t *testing.T) {
	big := strings.Repeat("A=1\n", maxEnvFileSize)
	_, _, err := Parse(strings.NewReader(big), nil)
	if err == nil {
		t.Fatal("超限文件应报错")
	}
	if !strings.Contains(err.Error(), "64") {
		t.Errorf("错误应提到大小上限，实际 %q", err.Error())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/envfile/`
Expected: 编译失败，`undefined: Parse` / `undefined: KV`（包还不存在）

- [ ] **Step 3: 实现解析器**

创建 `internal/envfile/envfile.go`：

```go
// Package envfile 解析 handoff 的 env 文件，并把它换算成可注入子进程的环境变量。
//
// 职责：
//   - Parse：把 dotenv 形态的文本解析为有序 KV，值支持单层 $VAR/${VAR} 展开
//   - Resolver（resolver.go）：按 agent 名定位 <DataDir>/env/<文件名>，读盘并
//     返回 KEY=VALUE 切片
//
// 边界：
//   - 不是 shell：不做命令替换、不支持多行值、不支持行内注释（理由见 Parse 注释）
//   - 不管密钥：不加密、不接 secret 后端；值一律不进日志（本包只在 Resolver 里
//     打 key 名）
//   - 不启动进程：注入由各 adapter 自行完成（经 executor.StartReq.Env）
package envfile

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// maxEnvFileSize 是单个 env 文件的大小上限（64KiB）。
//
// 为什么要有上限：误把二进制文件配成 env 文件时，逐行解析会产出一堆垃圾变量名，
// 或者报一长串无意义的行号错误；一个上限把它变成一句可读的拒绝。
const maxEnvFileSize = 64 << 10

// keyRe 是合法环境变量名的形状。宽于 POSIX 但与主流 shell 一致。
var keyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// KV 是一条解析结果，按文件内首次出现的顺序排列。
type KV struct {
	Key   string
	Value string
}

// Parse 解析 env 文件内容。
//
// 参数：
//   - r: 文件内容
//   - lookup: 展开时的外部变量查找（生产传 os.LookupEnv）；nil 表示外部无变量
//
// 返回：
//   - kvs: 按首次出现顺序排列的键值对（重复键后者覆盖前者的值，位置保持在首次出现处）
//   - dups: 出现过重复定义的键名，供调用方打 WARN（本函数是纯函数，不打日志）
//   - err: 语法错误（带行号与原行）或超出大小上限
//
// 语法（完整规则见 spec §3）：
//   - 行尾 \r 先剥离（兼容 CRLF）；trim 后的空行与 # 开头行跳过
//   - 可选 `export ` 前缀；第一个 = 分割；key 须匹配 keyRe
//   - 值 trim 后：'...' 字面量不展开，"..." 与无引号都展开
//
// 为什么不支持行内注释：`HTTPS_PROXY=http://host/a#b` 里 # 是合法字符，支持行内
// 注释会把这类值静默吃掉半截——症状是「代理配了但连不上」，离根因隔了十万八千里。
//
// 为什么展开时文件内的键优先于外部环境：让文件自洽，读文件的人不必脑补外部环境
// 是什么。查不到的变量展开为空串（os.Expand 的默认行为）。
func Parse(r io.Reader, lookup func(string) (string, bool)) (kvs []KV, dups []string, err error) {
	// 多读 1 字节用于判定「是否超限」：正好等于上限时 LimitReader 读满但未越界
	b, err := io.ReadAll(io.LimitReader(r, maxEnvFileSize+1))
	if err != nil {
		return nil, nil, fmt.Errorf("读取 env 内容: %w", err)
	}
	if len(b) > maxEnvFileSize {
		return nil, nil, fmt.Errorf("env 文件超过大小上限 %d 字节（64KiB）", maxEnvFileSize)
	}

	idx := map[string]int{}     // key → 在 kvs 中的下标，用于重复键就地覆盖
	vals := map[string]string{} // 已解析键的当前值，供后续行展开使用
	// expand 对值做一次变量展开，查找顺序为「本文件已解析的键 → lookup → 空串」。
	// 只展开一次：展开结果里的 $ 不再二次展开，这是「不是 shell」的边界所在。
	expand := func(s string) string {
		return os.Expand(s, func(name string) string {
			if v, ok := vals[name]; ok {
				return v
			}
			if lookup != nil {
				if v, ok := lookup(name); ok {
					return v
				}
			}
			return ""
		})
	}

	for i, raw := range strings.Split(string(b), "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export"); ok &&
			rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
			line = strings.TrimSpace(rest)
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, nil, fmt.Errorf("env 第 %d 行缺少 '='：%q", lineNo, line)
		}
		key = strings.TrimSpace(key)
		if !keyRe.MatchString(key) {
			return nil, nil, fmt.Errorf("env 第 %d 行键名非法 %q（须匹配 [A-Za-z_][A-Za-z0-9_]*）", lineNo, key)
		}
		val = strings.TrimSpace(val)
		switch {
		case len(val) >= 2 && strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'"):
			val = val[1 : len(val)-1] // 单引号：字面量，不展开
		case len(val) >= 2 && strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`):
			val = expand(val[1 : len(val)-1])
		default:
			val = expand(val)
		}
		if i, dup := idx[key]; dup {
			kvs[i].Value = val // 就地覆盖：保持首次出现的位置，语义是「后者生效」
			vals[key] = val
			dups = append(dups, key)
			continue
		}
		idx[key] = len(kvs)
		vals[key] = val
		kvs = append(kvs, KV{Key: key, Value: val})
	}
	return kvs, dups, nil
}
```

import 块为：`fmt` / `io` / `os` / `regexp` / `strings`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/envfile/ -v`
Expected: 全部 PASS

- [ ] **Step 5: 自检注释覆盖**

确认：包头有职责 + 边界；`Parse` 有参数/返回/语法规则/两条「为什么」；`maxEnvFileSize`、`expandOnce` 的非显然决策各有一句「为什么」。本 task 无日志点（纯函数，日志在 Task 2 的 Resolver）。

- [ ] **Step 6: gofmt 与提交**

```bash
gofmt -l . && go build ./... && go test ./internal/envfile/
git add internal/envfile/envfile.go internal/envfile/envfile_test.go
git commit -m "feat(B19): envfile 解析器（dotenv + 单层展开）"
```

---

### Task 2: envfile Resolver

**Files:**
- Create: `internal/envfile/resolver.go`
- Test: `internal/envfile/resolver_test.go`

**Interfaces:**
- Consumes: `Parse(r io.Reader, lookup func(string) (string, bool)) ([]KV, []string, error)`（Task 1）
- Produces:
  - `func Dir(dataDir string) string`
  - `func NewResolver(dir string, m map[string]string, log *slog.Logger) *Resolver`
  - `func (r *Resolver) For(agent string) ([]string, error)`
  - `func (r *Resolver) Preflight()`

- [ ] **Step 1: 写失败测试**

创建 `internal/envfile/resolver_test.go`：

```go
package envfile

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestResolver 造一个带 env 目录的 resolver，返回 resolver 与该目录。
func newTestResolver(t *testing.T, m map[string]string) (*Resolver, string) {
	t.Helper()
	dir := Dir(t.TempDir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return NewResolver(dir, m, quietLogger()), dir
}

func TestDirIsUnderDataDir(t *testing.T) {
	if got, want := Dir("/data"), filepath.Join("/data", "env"); got != want {
		t.Fatalf("Dir: got %q, want %q", got, want)
	}
}

func TestForReturnsNilWhenAgentNotConfigured(t *testing.T) {
	r, _ := newTestResolver(t, map[string]string{})
	got, err := r.For("opencode")
	if err != nil {
		t.Fatalf("未配置的 agent 不应报错: %v", err)
	}
	if got != nil {
		t.Fatalf("未配置的 agent 应返回 nil，实际 %v", got)
	}
}

func TestForLoadsFile(t *testing.T) {
	r, dir := newTestResolver(t, map[string]string{"opencode": "dev.env"})
	if err := os.WriteFile(filepath.Join(dir, "dev.env"),
		[]byte("HTTPS_PROXY=http://127.0.0.1:7890\nGOPROXY=https://goproxy.cn,direct\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := r.For("opencode")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	want := []string{"HTTPS_PROXY=http://127.0.0.1:7890", "GOPROXY=https://goproxy.cn,direct"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("第 %d 条: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestForRejectsNameWithPathSeparator(t *testing.T) {
	for _, name := range []string{"../secrets.env", "sub/dev.env", ".", ".."} {
		r, _ := newTestResolver(t, map[string]string{"opencode": name})
		if _, err := r.For("opencode"); err == nil {
			t.Errorf("文件名 %q 应被拒绝", name)
		}
	}
}

func TestForMissingFileErrorCarriesPath(t *testing.T) {
	r, dir := newTestResolver(t, map[string]string{"opencode": "nope.env"})
	_, err := r.For("opencode")
	if err == nil {
		t.Fatal("文件缺失应报错")
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, "nope.env")) {
		t.Errorf("错误应含完整路径，实际 %q", err.Error())
	}
}

// TestForRereadsFileEachCall 钉住 spec §5.3 的热更新承诺：改文件后无需重启，
// 下一次 For 就拿到新值。
func TestForRereadsFileEachCall(t *testing.T) {
	r, dir := newTestResolver(t, map[string]string{"opencode": "dev.env"})
	path := filepath.Join(dir, "dev.env")
	if err := os.WriteFile(path, []byte("A=1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	first, err := r.For("opencode")
	if err != nil || len(first) != 1 || first[0] != "A=1" {
		t.Fatalf("首次读取异常: %v, %v", first, err)
	}
	if err := os.WriteFile(path, []byte("A=2\n"), 0o600); err != nil {
		t.Fatalf("改写: %v", err)
	}
	second, err := r.For("opencode")
	if err != nil || len(second) != 1 || second[0] != "A=2" {
		t.Fatalf("改文件后应拿到新值，实际 %v, %v", second, err)
	}
}

// TestPreflightDoesNotPanicOnBrokenFile 确认预检不阻断、不 panic（只 WARN）。
func TestPreflightDoesNotPanicOnBrokenFile(t *testing.T) {
	r, dir := newTestResolver(t, map[string]string{"opencode": "bad.env", "claude": "nope.env"})
	if err := os.WriteFile(filepath.Join(dir, "bad.env"), []byte("NOT_A_PAIR\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	r.Preflight() // 不返回错误，问题只进日志
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/envfile/ -run 'TestDir|TestFor|TestPreflight'`
Expected: 编译失败，`undefined: Dir` / `undefined: NewResolver` / `undefined: Resolver`

- [ ] **Step 3: 实现 Resolver（含日志）**

创建 `internal/envfile/resolver.go`：

```go
// resolver.go —— env 文件的定位、读盘与日志。
//
// 职责：
//   - Dir：收口 <DataDir>/env 的目录布局知识，避免各调用方自己拼路径后漂移
//   - Resolver.For：按 agent 名解析出可注入的 KEY=VALUE 切片
//   - Resolver.Preflight：agentd 启动时把坏文件暴露在启动日志里
//
// 边界：
//   - 不解析语法（交 Parse）、不注入进程（交各 adapter）、不缓存（见 For 注释）
package envfile

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Dir 返回 env 文件目录（<dataDir>/env）。
//
// 目录布局知识只此一处：manager 与 agentd 各自构造 Resolver，若各拼各的路径，
// 日后改布局必然漏改一处。
func Dir(dataDir string) string { return filepath.Join(dataDir, "env") }

// Resolver 按 agent 名把配置里的文件名换算成可注入的环境变量。
//
// 无状态：每次 For 都重新读盘，因此多个实例之间不会发散（见 For 的热更新说明）。
type Resolver struct {
	dir string            // env 文件目录
	m   map[string]string // agent 名 → 文件名（纯文件名，不含路径）
	log *slog.Logger
}

// NewResolver 构造 Resolver。
//
// 参数：
//   - dir: env 文件目录，通常取 Dir(cfg.DataDir)
//   - m: agent 名 → 文件名映射（取自 config 的 env 段）；nil 视为空映射
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

// For 返回该 agent 启动时应注入的环境变量（KEY=VALUE 形式）。
//
// 参数：
//   - agent: executor 名（如 opencode）
//
// 返回：
//   - 该 agent 未配置 env 文件时返回 (nil, nil)——不是错误，是「没配」
//   - 文件名非法 / 打不开 / 解析失败时返回错误，错误文本带完整路径与行号
//
// 注意：
//   - 每次调用都重新读盘，不缓存。改了代理下一个任务就生效，不必重启 agentd
//     （重启会打断正在跑的任务的事件订阅，代价不小）；读一个几百字节的文件
//     相对于拉起一个 agent 的开销可以忽略
//   - 日志只打 key 名，绝不打值：环境类变量里 HTTPS_PROXY=http://user:pass@host
//     是正常写法，值里带凭据的概率不低
func (r *Resolver) For(agent string) ([]string, error) {
	name := strings.TrimSpace(r.m[agent])
	if name == "" {
		r.log.Debug("agent 未配置 env 文件，跳过注入", "agent", agent)
		return nil, nil
	}
	path, err := r.resolvePath(name)
	if err != nil {
		r.log.Error("env 文件名非法", "agent", agent, "name", name, "cause", err)
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		r.log.Error("打开 env 文件失败", "agent", agent, "path", path, "cause", err)
		return nil, fmt.Errorf("打开 env 文件 %s: %w", path, err)
	}
	defer f.Close()

	kvs, dups, err := Parse(f, os.LookupEnv)
	if err != nil {
		r.log.Error("解析 env 文件失败", "agent", agent, "path", path, "cause", err)
		return nil, fmt.Errorf("解析 env 文件 %s: %w", path, err)
	}
	for _, k := range dups {
		r.log.Warn("env 文件存在重复键，后者覆盖前者", "path", path, "key", k)
	}
	out := make([]string, 0, len(kvs))
	keys := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		out = append(out, kv.Key+"="+kv.Value)
		keys = append(keys, kv.Key)
	}
	r.log.Info("已加载 env 文件", "agent", agent, "path", path, "keys", keys, "count", len(keys))
	return out, nil
}

// resolvePath 把配置里的文件名换算为绝对路径，并拒绝一切非「纯文件名」的写法。
//
// 为什么只收纯文件名：一杜绝路径穿越（../../etc 之类），二保证 env 文件只有一个
// 家、不会散落各处——运维找配置时只需要看一个目录。
func (r *Resolver) resolvePath(name string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("env 文件名 %q 不能含路径分隔符：只支持 %s 下的纯文件名", name, r.dir)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("env 文件名 %q 非法：只支持 %s 下的纯文件名", name, r.dir)
	}
	return filepath.Join(r.dir, name), nil
}

// Preflight 读一遍所有被引用的 env 文件，把问题以 WARN 暴露在启动日志里。
//
// 为什么只 WARN 不阻断启动：env 文件是数据文件不是配置键，可能在 agentd 启动后
// 才创建，为它拒绝启动太硬；但完全不检查会把问题拖到第一次派发才暴露——WARN 让它
// 在启动日志里就可见，真正的拒发发生在 Dispatch（见 spec §6）。
func (r *Resolver) Preflight() {
	for agent := range r.m {
		if _, err := r.For(agent); err != nil {
			r.log.Warn("env 文件预检失败（不阻断启动，派发时会拒发）", "agent", agent, "cause", err)
		}
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/envfile/ -v`
Expected: 全部 PASS（含 Task 1 的用例）

- [ ] **Step 5: 自检日志与注释覆盖**

对照 instrumenting-code 清单确认：加载成功 Info（agent/path/keys/count，无值）、未配置 Debug、三类失败各有 Error 带 cause、重复键 Warn、预检失败 Warn。文件头有职责 + 边界；`Dir`/`NewResolver`/`For`/`Preflight`/`resolvePath` 均有 doc 注释且各带一句「为什么」。

- [ ] **Step 6: gofmt 与提交**

```bash
gofmt -l . && go build ./... && go test ./internal/envfile/
git add internal/envfile/resolver.go internal/envfile/resolver_test.go
git commit -m "feat(B19): envfile Resolver（按 agent 定位读盘，每次读盘支持热更新）"
```

---

### Task 3: config 增加 env 段

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `Config.Env map[string]string`（key = agent 名，value = `<DataDir>/env/` 下的纯文件名）

- [ ] **Step 1: 写失败测试**

在 `internal/config/config_test.go` 末尾追加。注意该文件是**外部测试包** `package config_test`，
所有调用都要带 `config.` 前缀；`os`/`path/filepath`/`strings` 已在 import 中，无需改动：

```go
func TestEnvSectionParsed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "listen: \"127.0.0.1:7777\"\ntoken: \"t\"\nenv:\n  opencode: dev.env\n  claude: work.env\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env["opencode"] != "dev.env" || cfg.Env["claude"] != "work.env" {
		t.Fatalf("env 段解析错误: %#v", cfg.Env)
	}
}

func TestEnvDefaultsToEmptyMap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("token: \"t\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env == nil {
		t.Fatal("未配置 env 时应为空 map 而非 nil，避免调用方各自判空")
	}
}

func TestUnknownKeyErrorMentionsEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("bogus_key: 1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("未知键应报错")
	}
	if !strings.Contains(err.Error(), "env{") {
		t.Errorf("已知键清单应含 env 段，实际 %q", err.Error())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run 'TestEnv|TestUnknownKeyErrorMentionsEnv' -v`
Expected: FAIL —— `cfg.Env` 未定义（编译错误），以及未知键提示文本不含 `env{`

- [ ] **Step 3: 实现**

在 `internal/config/config.go` 的 `Config` 结构体末尾（`Sync SyncConfig` 之后）加字段：

```go
	// Env 是 agent（executor）名 → env 文件名的映射：该 agent 启动时注入该文件里的
	// 环境变量。文件名必须是 <DataDir>/env/ 下的纯文件名（含路径分隔符会被拒绝）。
	// 未配置的 agent 不注入。任务执行者与审批者共用同一份（见 B19 spec §4）。
	Env map[string]string
```

在 `Load` 的默认值字面量里补一行（与 `Targets: map[string]Target{}` 同款，避免调用方各自判空）：

```go
		Env:      map[string]string{},
```

修改 `decodeStrict` 的错误文本，在 `sync{auto}` 之后补 `/env{<agent>: <文件名>}`：

```go
		return fmt.Errorf("配置包含未知字段（支持: listen/token/datadir/stalltimeout/targets{addr,user,token}/approver{executor,model,timeout,blacklist}/executor{default,model}/terminal{auto}/sync{auto}/env{<agent>: <文件名>}）: %w；旧版 access_key/secret_key 等键已废弃，请删除未知键或升级配置", err)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/config/ -v`
Expected: 全部 PASS

- [ ] **Step 5: gofmt 与提交**

```bash
gofmt -l . && go build ./... && go test ./internal/config/
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(B19): config 新增 env 段（agent 名 → env 文件名）"
```

---

### Task 4: StartReq.Env 契约与 opencode 注入

**Files:**
- Modify: `internal/executor/executor.go`（`StartReq` 结构体）
- Modify: `internal/executor/opencode/proc.go:90`（`StartServe`）、`internal/executor/opencode/proc.go:213`（`writeServeScript`）
- Modify: `internal/executor/opencode/adapter.go:283`（`StartServe` 调用点）
- Test: `internal/executor/opencode/proc_test.go`

**Interfaces:**
- Consumes: 无（本 task 只定义契约与 opencode 侧落地）
- Produces:
  - `executor.StartReq.Env []string`（形如 `KEY=VALUE`，已解析已展开）
  - `func StartServe(ctx context.Context, repoPath, taskID, taskDir, configPath string, env []string, log *slog.Logger) (*Proc, error)`
  - `func writeServeScript(taskDir string, port int, password, configPath string, env []string) (string, error)`

- [ ] **Step 1: 写失败测试**

在 `internal/executor/opencode/proc_test.go` 末尾追加：

```go
func TestServeScriptInjectsEnvBeforeOpencodeVars(t *testing.T) {
	taskDir := t.TempDir()
	configPath := filepath.Join(taskDir, "opencode.json")
	env := []string{"HTTPS_PROXY=http://127.0.0.1:7890", "PATH=/usr/bin:/bin:/usr/local/go/bin"}
	path, err := writeServeScript(taskDir, 35123, "pw", configPath, env)
	if err != nil {
		t.Fatalf("writeServeScript: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(b)
	proxyIdx := strings.Index(s, "export HTTPS_PROXY='http://127.0.0.1:7890'")
	if proxyIdx < 0 {
		t.Fatalf("脚本缺少注入的 HTTPS_PROXY export 行:\n%s", s)
	}
	if !strings.Contains(s, "export PATH='/usr/bin:/bin:/usr/local/go/bin'") {
		t.Errorf("脚本缺少注入的 PATH export 行:\n%s", s)
	}
	pwIdx := strings.Index(s, "export OPENCODE_SERVER_PASSWORD=")
	if pwIdx < 0 {
		t.Fatalf("脚本缺少 OPENCODE_SERVER_PASSWORD:\n%s", s)
	}
	// 顺序是硬要求：handoff 自身注入的变量必须排在后面才能覆盖 env 文件里的同名键
	if proxyIdx > pwIdx {
		t.Errorf("env 注入行应排在 OPENCODE_* 之前，实际 proxy=%d pw=%d", proxyIdx, pwIdx)
	}
}

// TestServeScriptQuotesEnvValues 钉住「Go 侧已展开过一次，shell 不得再展开第二次」：
// 值里的 $ 必须被单引号保护，否则 shell 会把它替换成别的东西。
func TestServeScriptQuotesEnvValues(t *testing.T) {
	taskDir := t.TempDir()
	path, err := writeServeScript(taskDir, 1, "pw", filepath.Join(taskDir, "opencode.json"),
		[]string{"LITERAL=$NOT_EXPANDED", "WITHSPACE=a b"})
	if err != nil {
		t.Fatalf("writeServeScript: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "export LITERAL='$NOT_EXPANDED'") {
		t.Errorf("含 $ 的值必须被单引号包裹:\n%s", s)
	}
	if !strings.Contains(s, "export WITHSPACE='a b'") {
		t.Errorf("含空格的值必须被单引号包裹:\n%s", s)
	}
}

func TestServeScriptWithoutEnvIsUnchangedInShape(t *testing.T) {
	taskDir := t.TempDir()
	path, err := writeServeScript(taskDir, 1, "pw", filepath.Join(taskDir, "opencode.json"), nil)
	if err != nil {
		t.Fatalf("writeServeScript: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(b), "export OPENCODE_SERVER_PASSWORD='pw'") {
		t.Errorf("无 env 时脚本形状不应改变:\n%s", string(b))
	}
}
```

该文件已 import `os` / `path/filepath` / `strings` / `testing`，无需改动 import。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestServeScript`
Expected: 编译失败 —— `writeServeScript` 接收 4 个参数，给了 5 个

- [ ] **Step 3: 实现**

`internal/executor/executor.go` 的 `StartReq` 加字段与文档：

```go
//   - Env: 启动 executor 进程时额外注入的环境变量（形如 KEY=VALUE，已解析已展开）。
//     由 manager 按 task.Executor 从 env 文件解析后填入；nil/空表示不注入。
//     实现方必须把它注入到自己拉起的进程环境中——这是 B19 对所有 adapter 的统一要求，
//     放在契约上而非各 adapter 的构造参数上，是为了让后续 adapter（Claude Code、grok）
//     不必各写一份注入逻辑
type StartReq struct {
	Task        proto.Task
	PlanContent string
	TaskDir     string
	Env         []string
}
```

`internal/executor/opencode/proc.go`：加保留变量表与 export 行生成，改两个函数签名。

```go
// protectedEnvKeys 是 handoff 自身注入、不容 env 文件覆盖的变量。
//
// 命中时不静默忽略用户写的行——注入顺序保证 handoff 的 export 排在后面因而胜出，
// 同时打 WARN 让用户知道自己那行没生效。
var protectedEnvKeys = map[string]bool{
	"OPENCODE_SERVER_PASSWORD": true,
	"OPENCODE_CONFIG":          true,
}
```

`StartServe` 签名加 `env []string`（放在 `configPath` 之后、`log` 之前），并在写脚本前打日志与告警：

```go
func StartServe(ctx context.Context, repoPath, taskID, taskDir, configPath string, env []string, log *slog.Logger) (*Proc, error) {
```

在 `session := "handoff-" + id8(taskID)` 之后、`writeServeScript` 之前插入：

```go
	// env 注入（B19）：只打 key 名不打值——值里可能带凭据（如 http://user:pass@host）
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for _, kv := range env {
			k, _, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			keys = append(keys, k)
			if protectedEnvKeys[k] {
				log.Warn("env 文件定义了 handoff 保留变量，将被 handoff 自身注入覆盖",
					"key", k, "session", session)
			}
		}
		log.Info("注入 env 变量到 serve 进程", "session", session, "keys", keys, "count", len(keys))
	}
```

并把 `writeServeScript` 调用改为 `writeServeScript(taskDir, port, password, configPath, env)`。

`writeServeScript` 改造：

```go
// （在既有 doc 注释末尾追加一段）
//
// why（env 行排在 OPENCODE_* 之前且值用单引号）：排在前面才能让 handoff 自身的
// 变量覆盖 env 文件里的同名键（见 protectedEnvKeys）；值必须单引号包裹，因为 Go 侧
// 已经展开过一次，不加引号会被 shell 再展开第二次，含 $ 的值会变成别的东西。
func writeServeScript(taskDir string, port int, password, configPath string, env []string) (string, error) {
	serveLogPath := filepath.Join(taskDir, serveLogFileName)
	var envLines strings.Builder
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue // 形如 KEY=VALUE 之外的条目直接跳过，不让它污染脚本语法
		}
		envLines.WriteString("export " + k + "=" + shellQuote(v) + "\n")
	}
	script := fmt.Sprintf(`#!/bin/sh
# 由 agentd 生成：opencode serve 启动脚本（0600，含随机密码，勿外泄）。
exec 2>> %s
%sexport OPENCODE_SERVER_PASSWORD=%s
export OPENCODE_CONFIG=%s
exec opencode serve --port %d --hostname 127.0.0.1 2>&1 | tee -a %s
`, shellQuote(serveLogPath), envLines.String(), shellQuote(password), shellQuote(configPath), port, shellQuote(serveLogPath))
	scriptPath := filepath.Join(taskDir, serveScriptFileName)
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return "", fmt.Errorf("写 serve 启动脚本 %s: %w", scriptPath, err)
	}
	return scriptPath, nil
}
```

确认 `proc.go` 已 import `strings`；未 import 则补上。

`internal/executor/opencode/adapter.go:283` 的调用改为：

```go
	proc, err := StartServe(ctx, req.Task.Workdir(), req.Task.ID, req.TaskDir, configPath, req.Env, a.log)
```

`internal/executor/opencode/proc_script_unix_test.go:74` 的既有调用补一个 `nil` 参数：

```go
	path, err := writeServeScript(taskDir, 35123, "pw", filepath.Join(taskDir, "opencode.json"), nil)
```

`internal/executor/opencode/proc_test.go` 里既有的两处 `writeServeScript(...)` 同样各补一个 `nil`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/... -v -run 'TestServeScript|TestWriteServeScript'`
然后 Run: `go test ./internal/executor/...`
Expected: 全部 PASS

- [ ] **Step 5: 自检日志与注释覆盖**

确认：`StartServe` 里有「注入 env 变量到 serve 进程」的 Info（只含 key 名）与保留变量 Warn；`StartReq.Env`、`protectedEnvKeys`、`writeServeScript` 的新段落各有「为什么」。

- [ ] **Step 6: gofmt 与提交**

```bash
gofmt -l . && go build ./... && go test ./internal/executor/...
git add internal/executor/
git commit -m "feat(B19): StartReq.Env 契约字段，opencode 经启动脚本注入 env"
```

---

### Task 5: manager 在 Dispatch 前段解析并下发

**Files:**
- Modify: `internal/agentd/manager.go`（`Manager` 结构体、`NewManager`、`Dispatch`、新增哨兵）
- Modify: `internal/agentd/server.go:480`（`writeDispatchError`）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: `envfile.Dir(dataDir string) string`、`envfile.NewResolver(dir string, m map[string]string, log *slog.Logger) *Resolver`、`(*envfile.Resolver).For(agent string) ([]string, error)`（Task 2）；`config.Config.Env`（Task 3）；`executor.StartReq.Env`（Task 4）
- Produces: `errEnvResolveFailed`（包内哨兵，server 层据此映射 500）

- [ ] **Step 1: 写失败测试**

在 `internal/agentd/manager_test.go` 末尾追加：

```go
// TestDispatchRejectsWhenEnvFileMissing 钉住 spec §6：env 解析失败必须在
// 建任务与工作区准备之前拒发，不能留下一个注定 failed 的任务。
func TestDispatchRejectsWhenEnvFileMissing(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{
		Token: "test", DataDir: t.TempDir(),
		Executor: config.ExecutorConfig{Default: "fake"},
		Env:      map[string]string{"fake": "missing.env"},
	}
	m := NewManager(st, NewHub(), map[string]executor.Adapter{"fake": fake.New(nil)}, cfg, nil, logger)

	// Repo 随便给一个不存在的路径即可：env 解析发生在任何 git 动作之前，
	// 这条断言同时证明了「解析确实排在最前段」
	_, derr := m.Dispatch(context.Background(), DispatchReq{
		Repo: "/nonexistent/repo", Prompt: "任意指令",
	})
	if derr == nil {
		t.Fatal("env 文件缺失时应拒发")
	}
	if !errors.Is(derr, errEnvResolveFailed) {
		t.Fatalf("应为 errEnvResolveFailed，实际 %v", derr)
	}
	if !strings.Contains(derr.Error(), "missing.env") {
		t.Errorf("错误应带文件名，实际 %q", derr.Error())
	}
	tasks, lerr := st.ListTasks()
	if lerr != nil {
		t.Fatalf("ListTasks: %v", lerr)
	}
	if len(tasks) != 0 {
		t.Fatalf("拒发时不应创建任务，实际创建了 %d 个", len(tasks))
	}
}

// TestDispatchPassesEnvToAdapter 钉住解析结果确实到达了 adapter。
func TestDispatchPassesEnvToAdapter(t *testing.T) {
	dataDir := t.TempDir()
	envDir := envfile.Dir(dataDir)
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "dev.env"), []byte("HTTPS_PROXY=http://p:1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	repo := initTestRepo(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{
		Token: "test", DataDir: dataDir,
		Executor: config.ExecutorConfig{Default: "fake"},
		Env:      map[string]string{"fake": "dev.env"},
	}
	rec := &envRecordingAdapter{Adapter: fake.New(nil)}
	m := NewManager(st, NewHub(), map[string]executor.Adapter{"fake": rec}, cfg, nil, logger)

	if _, derr := m.Dispatch(context.Background(), DispatchReq{Repo: repo, Prompt: "任意指令"}); derr != nil {
		t.Fatalf("Dispatch: %v", derr)
	}
	if len(rec.gotEnv) != 1 || rec.gotEnv[0] != "HTTPS_PROXY=http://p:1" {
		t.Fatalf("adapter 收到的 Env 不对: %v", rec.gotEnv)
	}
}

// envRecordingAdapter 包一层 fake adapter，只为记录 Start 收到的 Env。
type envRecordingAdapter struct {
	executor.Adapter
	gotEnv []string
}

func (a *envRecordingAdapter) Start(ctx context.Context, req executor.StartReq) error {
	a.gotEnv = req.Env
	return a.Adapter.Start(ctx, req)
}
```

`initTestRepo(t)` 是同包既有助手（定义在 `internal/agentd/workspace_test.go:35`，在 `t.TempDir()` 里
`git init` 并造一个初始提交），直接用即可。按需补 import：`errors`、`strings`、`os`、`context`、
`github.com/xushixin/handoff/internal/envfile`、`github.com/xushixin/handoff/internal/executor/fake`。

再在 `internal/agentd/server_test.go`（外部测试包 `agentd_test`，调用需带 `agentd.` 前缀）末尾追加
HTTP 层断言 —— 上面的 manager 测试只证明了「拒发」，这条证明「真因回到了派发者手里」：

```go
// TestDispatchEnvFailureReturns500WithCause 钉住 spec §6：env 解析失败必须回显真因，
// 不能落进 writeDispatchError 的 default 分支变成扁平的「派发任务失败」（B16 根因同款）。
func TestDispatchEnvFailureReturns500WithCause(t *testing.T) {
	cfg := &config.Config{Token: testToken, DataDir: t.TempDir(),
		Executor: config.ExecutorConfig{Default: "fake"},
		Env:      map[string]string{"fake": "missing.env"}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := newTestEnvWithCfg(t, cfg, logger)
	mgr := agentd.NewManager(env.st, env.srv.Hub(),
		map[string]executor.Adapter{"fake": fake.New(nil)}, cfg, nil, logger)
	env.srv.SetManager(mgr)

	resp := env.post(t, "/api/tasks", `{"repo":"/nonexistent/repo","prompt":"任意指令"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("返回 %d, want 500", resp.StatusCode)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解码响应: %v", err)
	}
	if !strings.Contains(body.Error, "missing.env") {
		t.Errorf("响应体应带真因（含文件名），实际 %q", body.Error)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestDispatchRejectsWhenEnvFileMissing|TestDispatchPassesEnvToAdapter|TestDispatchEnvFailureReturns500WithCause'`
Expected: 编译失败 —— `undefined: errEnvResolveFailed`（manager 测试）与 `unknown field Env`（cfg 字面量，若 Task 3 未先完成）

- [ ] **Step 3: 实现**

`internal/agentd/manager.go`：

结构体加字段（放在 `cfg` 之后）：

```go
	// env 是 env 文件解析器（B19）：Dispatch 时按 task.Executor 解析出要注入
	// executor 进程的环境变量。构造后只读，每次 For 都重新读盘（支持热更新）。
	env *envfile.Resolver
```

`NewManager` 里构造（签名不变 —— Manager 已持有完整 cfg，加参数会波及 10 个测试调用点却换不来任何收益；Resolver 无状态，多实例不会发散）：

```go
func NewManager(st *store.Store, hub *Hub, ads map[string]executor.Adapter, cfg *config.Config, approver *Approver, log *slog.Logger) *Manager {
	return &Manager{
		st: st, hub: hub, ads: ads, cfg: cfg, approver: approver, log: log,
		env:        envfile.NewResolver(envfile.Dir(cfg.DataDir), cfg.Env, log),
		apInflight: map[string]bool{},
		apFails:    map[string]int{},
		apDisabled: map[string]bool{},
	}
}
```

新增哨兵（放在 `errExecutorStartFailed` 旁边）：

```go
// errEnvResolveFailed 表示 env 文件解析失败（文件缺失/语法错/文件名非法）。
//
// server 层据此回 500 并回显真因：落到 writeDispatchError 的 default 分支只会回
// 扁平的「派发任务失败」，真因被吞——这正是 B16 的根因，不能再犯一次。
var errEnvResolveFailed = errors.New("解析 env 文件失败")
```

`Dispatch` 中，在 `model` 兜底之后、内容合成之前插入：

```go
	// env 注入（B19）：按 executor 名解析 env 文件。位置刻意排在最前段——早于建任务、
	// 早于 EnsureBaseCommit 与 PrepareWorkspace。解析失败是配置问题，此刻还没有任何
	// 落库/建树副作用，拒发是干净的；若放到 ad.Start 前才解析，任务已落库、worktree
	// 已建，就变成「创建了一个注定 failed 的任务」，与 spec §6「任务不创建」矛盾
	envKVs, err := m.env.For(execName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errEnvResolveFailed, err)
	}
```

`ad.Start` 调用改为：

```go
	if err := ad.Start(ctx, executor.StartReq{
		Task: *task, PlanContent: string(planContent), TaskDir: taskDir, Env: envKVs,
	}); err != nil {
```

`internal/agentd/server.go` 的 `writeDispatchError`，在 `errExecutorStartFailed` 分支之后、`default` 之前插入：

```go
	case errors.Is(err, errEnvResolveFailed):
		s.log.Error("dispatch 被拒：env 文件解析失败（配置问题，真因回显）", "repo", repo, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
```

并在该函数的 doc 注释映射规则列表里补一条：

```go
//   - errEnvResolveFailed → 500 + 可读真因：env 文件缺失/语法错是执行机上的配置
//     问题，响应体带完整路径与行号，派发者改完文件重派即可
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestDispatch' -v`
然后 Run: `go test ./internal/agentd/`
Expected: 全部 PASS

- [ ] **Step 5: 自检日志与注释覆盖**

确认：解析失败的日志已由 Resolver 打（Error 带 path/cause），server 层再打一条带 repo 的 Error；`errEnvResolveFailed`、`Dispatch` 插入点、`env` 字段各有「为什么」注释。

- [ ] **Step 6: gofmt 与提交**

```bash
gofmt -l . && go build ./... && go test ./internal/agentd/
git add internal/agentd/manager.go internal/agentd/server.go internal/agentd/manager_test.go internal/agentd/server_test.go
git commit -m "feat(B19): dispatch 前段解析 env 并下发 adapter，失败拒发回 500 带真因"
```

---

### Task 6: 审批者共用同一份 env

**Files:**
- Modify: `internal/agentd/approver.go`（`Approver` 结构体、`NewApprover`、`defaultRunCmd`）
- Create: `internal/agentd/approver_env_unix_test.go`

**Interfaces:**
- Consumes: `envfile.Dir` / `envfile.NewResolver` / `(*envfile.Resolver).For`（Task 2）
- Produces: `func NewApprover(cfg config.ApproverConfig, env *envfile.Resolver, log *slog.Logger) (*Approver, error)`（新增中间参数，nil = 不注入）

- [ ] **Step 1: 写失败测试**

创建 `internal/agentd/approver_env_unix_test.go`：

```go
//go:build unix

package agentd

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/envfile"
)

// newEnvApprover 造一个带 env 文件的审批者；body 为 env 文件内容。
func newEnvApprover(t *testing.T, body string) *Approver {
	t.Helper()
	dir := envfile.Dir(t.TempDir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.env"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	res := envfile.NewResolver(dir, map[string]string{"opencode": "a.env"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ap, err := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: 5 * time.Second},
		res, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApprover: %v", err)
	}
	if ap == nil {
		t.Fatal("NewApprover 返回 nil")
	}
	return ap
}

// TestApproverInjectsEnvIntoDecideCommand 断言子进程真的看到了变量——比断言
// 「切片被传下去了」更接近事实。
func TestApproverInjectsEnvIntoDecideCommand(t *testing.T) {
	ap := newEnvApprover(t, "HANDOFF_TEST_VAR=injected\n")
	out, err := ap.defaultRunCmd(context.Background(),
		[]string{"sh", "-c", `printf %s "$HANDOFF_TEST_VAR"`})
	if err != nil {
		t.Fatalf("defaultRunCmd: %v", err)
	}
	if out != "injected" {
		t.Fatalf("子进程应看到注入的变量，实际输出 %q", out)
	}
}

// TestApproverStillInheritsAgentdEnv 确认注入是「追加」而不是「替换」——
// 替换掉 agentd 环境会让审批者连 PATH 都没有。
func TestApproverStillInheritsAgentdEnv(t *testing.T) {
	t.Setenv("HANDOFF_INHERITED_VAR", "inherited")
	ap := newEnvApprover(t, "HANDOFF_TEST_VAR=injected\n")
	out, err := ap.defaultRunCmd(context.Background(),
		[]string{"sh", "-c", `printf %s "$HANDOFF_INHERITED_VAR"`})
	if err != nil {
		t.Fatalf("defaultRunCmd: %v", err)
	}
	if out != "inherited" {
		t.Fatalf("应继承 agentd 环境，实际输出 %q", out)
	}
}

// TestApproverEnvFailureDoesNotRunCommand 断言 env 解析失败时不执行裁决命令，
// 直接报错交给 Decide 走 escalate。
func TestApproverEnvFailureDoesNotRunCommand(t *testing.T) {
	dir := envfile.Dir(t.TempDir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	res := envfile.NewResolver(dir, map[string]string{"opencode": "nope.env"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ap, err := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: 5 * time.Second},
		res, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApprover: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	out, rerr := ap.defaultRunCmd(context.Background(),
		[]string{"sh", "-c", "touch " + marker})
	if rerr == nil {
		t.Fatal("env 解析失败时应报错")
	}
	if !strings.Contains(rerr.Error(), "nope.env") {
		t.Errorf("错误应带文件名，实际 %q", rerr.Error())
	}
	if out != "" {
		t.Errorf("不应有命令输出，实际 %q", out)
	}
	if _, serr := os.Stat(marker); serr == nil {
		t.Error("env 解析失败时不应执行裁决命令")
	}
}

// TestApproverWithNilResolverStillRuns 确认 nil resolver（未配置/测试场景）不注入也不报错。
func TestApproverWithNilResolverStillRuns(t *testing.T) {
	ap, err := NewApprover(config.ApproverConfig{Executor: "opencode", Timeout: 5 * time.Second},
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewApprover: %v", err)
	}
	out, rerr := ap.defaultRunCmd(context.Background(), []string{"sh", "-c", `printf ok`})
	if rerr != nil {
		t.Fatalf("defaultRunCmd: %v", rerr)
	}
	if out != "ok" {
		t.Fatalf("输出应为 ok，实际 %q", out)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestApprover`
Expected: 编译失败 —— `NewApprover` 只接收 2 个参数，给了 3 个

- [ ] **Step 3: 实现**

`internal/agentd/approver.go`：

结构体加字段（放在 `blacklist` 之后、`runCmd` 之前）：

```go
	// env 是本审批者所用 agent 的 env 文件解析器；nil=不注入（未配置或测试场景）。
	// 审批者与任务执行者是同一个 agent 的两次启动，必须共用同一份 env——代理只配
	// 半边会让审批者连不出去后静默 fail-closed 升级，是最难查的那类故障
	env *envfile.Resolver
```

`NewApprover` 加中间参数，并把 `runCmd` 绑到方法上：

```go
// NewApprover 构造审批者。
//
// 参数：
//   - cfg: 审批者配置；cfg.Executor 为空表示不启用审批链，返回 (nil, nil)
//   - env: 本 agent 的 env 文件解析器（B19）；nil=不注入
//   - log: 包日志入口
func NewApprover(cfg config.ApproverConfig, env *envfile.Resolver, log *slog.Logger) (*Approver, error) {
	if cfg.Executor == "" {
		return nil, nil
	}
	// ...（既有黑名单编译逻辑不动）...
	a := &Approver{
		log:          log,
		executorName: cfg.Executor,
		model:        cfg.Model,
		timeout:      cfg.Timeout,
		blacklist:    rx,
		env:          env,
	}
	// runCmd 绑到方法而不是包级函数：默认实现需要读 a.env 才能注入环境变量，
	// 同时保持 runCmd 字段签名不变——既有 15 处测试注入点一行都不用改
	a.runCmd = a.defaultRunCmd
	return a, nil
}
```

把包级 `defaultRunCmd` 改成方法并注入环境：

```go
// defaultRunCmd 是 runCmd 的默认实现：exec.CommandContext + CombinedOutput，
// 输出上限 maxDecideOutput（截断而非报错——解析只关心输出开头的 JSON 行）。
//
// 环境（B19）：继承 agentd 环境并**追加**本 agent 的 env 文件变量。
//   - 为什么是追加而不是替换：替换会让审批者连 PATH 都没有，executor 根本起不来
//   - 为什么解析失败直接返回错误而不是无环境硬跑：Decide 的既有失败分支会把它
//     变成 escalate（升级人工审核者）。让它带病裁决更危险——没有代理时模型请求
//     必然失败，而失败会被当成「审批者判不了」，与真正的判不了混为一谈
func (a *Approver) defaultRunCmd(ctx context.Context, argv []string) (string, error) {
	env := os.Environ()
	if a.env != nil {
		extra, err := a.env.For(a.executorName)
		if err != nil {
			a.log.Error("审批者 env 文件解析失败，本次裁决升级人工审核者",
				"executor", a.executorName, "cause", err)
			return "", fmt.Errorf("解析审批者 env 文件: %w", err)
		}
		env = append(env, extra...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	b := out.Bytes()
	if len(b) > maxDecideOutput {
		b = b[:maxDecideOutput]
	}
	return string(b), err
}
```

确认 `approver.go` 已 import `os` 与 `github.com/xushixin/handoff/internal/envfile`；缺则补。

`cmd/agentd.go:77` 的调用暂时补 `nil` 占位（Task 7 换成真实 resolver）：

```go
		ap, err := agentd.NewApprover(cfg.Approver, nil, logger)
```

`internal/agentd/approver_test.go` 里 **11 处** `NewApprover(cfg, logger)` 调用统一改为 `NewApprover(cfg, nil, logger)`。**不要动任何 `runCmd = func(...)` 注入行** —— 该字段签名没变，15 处注入点全部原样保留；改了就说明实现走偏了。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestApprover -v`
然后 Run: `go test ./internal/agentd/`
Expected: 全部 PASS

- [ ] **Step 5: 自检日志与注释覆盖**

确认：解析失败有 Error 带 executor + cause；`env` 字段、`a.runCmd = a.defaultRunCmd` 的绑定理由、`defaultRunCmd` 的两条「为什么」都在。

- [ ] **Step 6: gofmt 与提交**

```bash
gofmt -l . && go build ./... && go test ./internal/agentd/
git add internal/agentd/approver.go internal/agentd/approver_test.go internal/agentd/approver_env_unix_test.go cmd/agentd.go
git commit -m "feat(B19): 审批者与任务执行者共用同一份 env，解析失败走 escalate"
```

---

### Task 7: agentd 接线、启动预检与文档

**Files:**
- Modify: `cmd/agentd.go:77`（构造 resolver、Preflight、传给 NewApprover）
- Modify: `README.md`（配置段）

**Interfaces:**
- Consumes: `envfile.Dir` / `envfile.NewResolver` / `(*envfile.Resolver).Preflight()`（Task 2）；`NewApprover(cfg, env, log)`（Task 6）
- Produces: 无（本 task 是接线与文档收口）

- [ ] **Step 1: 接线**

`cmd/agentd.go`，在 `NewApprover` 调用之前插入：

```go
		// env 注入（B19）：agent 启动时注入 <DataDir>/env/ 下配置的环境变量文件。
		// 启动预检只 WARN 不阻断——env 文件是数据文件，可能在 agentd 启动后才创建；
		// 但完全不检查会把问题拖到第一次派发才暴露，预检让它在启动日志里就可见。
		// manager 侧自建同款 resolver（NewManager 内），两者无状态、不会发散。
		envRes := envfile.NewResolver(envfile.Dir(cfg.DataDir), cfg.Env, logger)
		envRes.Preflight()
```

并把 Task 6 留下的 `nil` 占位换掉：

```go
		ap, err := agentd.NewApprover(cfg.Approver, envRes, logger)
```

补 import `github.com/xushixin/handoff/internal/envfile`。

- [ ] **Step 2: 验证接线可用**

Run: `go build ./... && go vet ./...`
Expected: 均无输出

手工验证预检日志（在临时 DataDir 上跑，不碰真实数据）：

```bash
mkdir -p /tmp/b19check/env && printf 'listen: "127.0.0.1:7799"\ntoken: "t"\ndatadir: "/tmp/b19check"\nenv:\n  opencode: nope.env\n' > /tmp/b19check/config.yaml
# 后台起、记 PID、给它 3 秒写完启动日志，然后只按 PID 精确回收
go run . agentd --config /tmp/b19check/config.yaml > /tmp/b19check/out.log 2>&1 &
echo $! > /tmp/b19check/agentd.pid
sleep 3
grep -E "env 文件预检失败|agentd 服务启动" /tmp/b19check/out.log
kill "$(cat /tmp/b19check/agentd.pid)" 2>/dev/null
rm -rf /tmp/b19check
```

Expected: grep 同时打出 `env 文件预检失败（不阻断启动，派发时会拒发）` 与 `agentd 服务启动` 两行——前者证明预检生效，后者证明它没阻断启动。

**注意端口 7799 是刻意避开生产 agentd 的 7777 的，不要改。回收只按上面记下的 PID 来：
绝不要用 `pkill -f agentd` / `killall` 这类宽泛模式，它会匹配到本机正在跑的生产
agentd——那正是承载你这个任务的进程，一执行你自己就没了。**

- [ ] **Step 3: 补 README**

在 `README.md` 的「配置（~/.handoff/config.yaml）」代码块末尾（`sync:` 段之后）追加：

```yaml
env:                          # agent 启动时注入的环境变量文件（放 ~/.handoff/env/ 下）
  opencode: dev.env           # 值是纯文件名；未配置的 agent 不注入
```

并在该代码块之后补一段说明：

```markdown
`env` 段让 agent 启动时带上代理、私有 registry、额外 PATH 等环境变量。文件放执行机的
`~/.handoff/env/` 下，格式是 dotenv（`KEY=VALUE`，`#` 开头整行注释，`export` 前缀可选，
值支持 `${VAR}` 单层展开，单引号内不展开）：

```sh
export HTTPS_PROXY=http://127.0.0.1:7890
GOPROXY=https://goproxy.cn,direct
PATH=${PATH}:/usr/local/go/bin
```

同一份 env 也会注入审批者（`approver.executor`）—— 否则代理只配半边，审批者连不出去会
静默升级人工审核者。文件不存在或语法错时**拒绝派发**并回显完整路径与行号，不会带病启动。
不支持行内注释（`#` 只在行首生效，因为 URL 里 `#` 合法）。
```

- [ ] **Step 4: 全量验证**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... && go test -race ./internal/agentd/ ./internal/envfile/ ./internal/executor/opencode/ ./cmd/
```

Expected: `gofmt -l .` 无输出，其余全绿。

- [ ] **Step 5: 提交**

```bash
git add cmd/agentd.go README.md
git commit -m "feat(B19): agentd 接线 env resolver 与启动预检，README 补 env 段"
```

---

## 验收清单（全部 task 完成后）

- [ ] `go build ./...` / `go vet ./...` 无输出
- [ ] `gofmt -l .` 无输出
- [ ] `go test ./...` 全绿
- [ ] `go test -race ./internal/agentd/ ./internal/envfile/ ./internal/executor/opencode/ ./cmd/` 全绿
- [ ] 真机验证（执行机上）：写一个 `~/.handoff/env/dev.env` 含 `HANDOFF_PROBE=ok`，配 `env: {opencode: dev.env}`，重启 agentd 后派发一个任务，`attach` 进 tmux 在 serve 窗格确认 `run_serve.sh` 里有 `export HANDOFF_PROBE='ok'` 且排在 `OPENCODE_SERVER_PASSWORD` 之前
- [ ] 真机验证：把配置改成引用一个不存在的文件，派发应被拒绝，CLI 回显里带完整路径
- [ ] 日志抽查：`agentd.log` 里「已加载 env 文件」这条**只有 key 名没有值**
