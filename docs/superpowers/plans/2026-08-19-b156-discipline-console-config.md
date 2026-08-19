# B156 控制台配置执行纪律 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让控制台能按机器编辑执行纪律块正文，并给该机每个 executor 指定用哪块（三档），保存后下一个任务即生效、不必重启 agentd。

**Architecture:** 后端在 `internal/discipline` 补一层纯文件操作面（List/Read/Write），agentd 新增四个走 `?machine=` 转发的端点；`Resolver` 从「构造时吞一份映射」改成「每次取活配置」，配置落盘复用 `Server.swapConf`（需补深拷 `Discipline`）。前端在设置页新增「执行纪律」分区，并在开发机详情内新增映射块。

**Tech Stack:** Go 1.26.1（slog、`net/http` Go1.22 方法路由、`gopkg.in/yaml.v3`）、React + TypeScript + Vite + vitest + Testing Library、Tailwind/shadcn。

**Spec:** `docs/superpowers/specs/2026-08-19-b156-discipline-console-config-design.md`
**形态基准:** `prototypes/discipline-config/pages/settings.html`（走查已确认；真实页面对照它验收）

## Global Constraints

- Go 侧日志一律 `slog`（agentd 内用 `s.log` / `m.log`），**禁止 `fmt.Printf`**；前端**禁止 `console.log`**（`console.warn` 仅限降级诊断）。
- 新建文件必须有文件头注释（职责 + 边界）；导出函数必须有 doc 注释（参数、返回、注意事项）；非显然分支写「为什么」的中文注释。
- `internal/` 下禁止 `os.RemoveAll`。
- 纪律块大小上限 **64 KiB**，取自 `internal/discipline/resolver.go` 既有的 `maxBlockSize`，不另立常量。
- 纪律块文件名只收**纯文件名**：含 `/` 或 `filepath.Separator`、等于 `""` / `.` / `..` 一律拒。
- 三档语义（不可改写）：配置里键**不存在** = 用内置默认；值为**空串** = 关闭注入；值为**文件名** = 读该文件。
- 契约改动流程：改 Go 结构 → `go test ./internal/proto/ -run TestContractFixtures -update` → 同步 `web/src/api/types.ts` 与 `web/src/api/contract.test.ts`，fixture 差异随提交一并 review。
- 每个 task 完成即 commit；提交信息用各 task「Commit」步骤给出的原文。
- 完工前必须跑：`gofmt -l .`（无输出）、`go build ./... && go vet ./... && go test ./...`、`cd web && npx vitest run && npx tsc -b && npx eslint .`。

---

### Task 1: `internal/discipline` 的文件操作面与内置导出

**Files:**
- Create: `internal/discipline/files.go`
- Create: `internal/discipline/files_test.go`
- Modify: `internal/discipline/resolver.go`（把 `Resolver.resolvePath` 提成包级函数）
- Modify: `internal/discipline/discipline.go`（导出内置两版与默认档位）

**Interfaces:**
- Consumes: 无（本包最底层）
- Produces:
  - `discipline.MaxBlockSize` (int)
  - `discipline.ErrBadName / ErrTooLarge / ErrExists / ErrBaseMismatch` (error)
  - `discipline.FileInfo{Name string; Size int64; SHA256 string}`
  - `discipline.List(dir string) ([]FileInfo, error)`
  - `discipline.Read(dir, name string) (content, sha string, size int64, err error)`
  - `discipline.Write(dir, name, content, baseSHA string) (sha string, size int64, err error)`
  - `discipline.Builtin{Tier, Content string}` 与 `discipline.Builtins() []Builtin`
  - `discipline.DefaultTierFor(executor string) string`

- [ ] **Step 1: 写失败测试**

创建 `internal/discipline/files_test.go`：

```go
// files_test.go —— 纪律块文件操作面的测试：列举、读、写（新建/覆盖/冲突）与名字校验。
package discipline_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/discipline"
)

// sha256 of "hello\n"
const helloSHA = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"

func TestListEmptyWhenDirMissing(t *testing.T) {
	// 目录不存在不是错误：<DataDir>/discipline 没有任何东西自动创建，
	// 首次打开设置页时它本来就不存在。
	files, err := discipline.List(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("len = %d, want 0", len(files))
	}
}

func TestListReturnsNameSizeHashSorted(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("hello\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("x"), 0o600)
	os.Mkdir(filepath.Join(dir, "sub"), 0o700) // 子目录必须被跳过

	files, err := discipline.List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len = %d, want 2（子目录应被跳过）", len(files))
	}
	if files[0].Name != "a.md" || files[1].Name != "b.md" {
		t.Fatalf("顺序 = %q/%q, want a.md/b.md", files[0].Name, files[1].Name)
	}
	if files[1].Size != 6 || files[1].SHA256 != helloSHA {
		t.Errorf("b.md = size %d sha %s", files[1].Size, files[1].SHA256)
	}
}

func TestReadReturnsContentAndHash(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello\n"), 0o600)

	content, sha, size, err := discipline.Read(dir, "a.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content != "hello\n" || sha != helloSHA || size != 6 {
		t.Errorf("Read = %q / %s / %d", content, sha, size)
	}
}

func TestReadMissingIsNotExist(t *testing.T) {
	if _, _, _, err := discipline.Read(t.TempDir(), "a.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestWriteCreatesDirAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "discipline")
	sha, size, err := discipline.Write(dir, "a.md", "hello\n", "")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if sha != helloSHA || size != 6 {
		t.Errorf("Write = %s / %d", sha, size)
	}
	b, err := os.ReadFile(filepath.Join(dir, "a.md"))
	if err != nil || string(b) != "hello\n" {
		t.Fatalf("落盘内容 = %q, err=%v", b, err)
	}
}

func TestWriteNewOnExistingIsErrExists(t *testing.T) {
	// base 为空串 = 「新建」，此时目标必须不存在，否则会静默覆盖别人的文件
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("old"), 0o600)
	if _, _, err := discipline.Write(dir, "a.md", "new", ""); !errors.Is(err, discipline.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
}

func TestWriteBaseMismatchReturnsCurrentHash(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello\n"), 0o600)
	sha, _, err := discipline.Write(dir, "a.md", "new", "deadbeef")
	if !errors.Is(err, discipline.ErrBaseMismatch) {
		t.Fatalf("err = %v, want ErrBaseMismatch", err)
	}
	if sha != helloSHA {
		t.Errorf("冲突时应回磁盘现状哈希，得到 %s", sha)
	}
}

func TestWriteBaseMatchOverwrites(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello\n"), 0o600)
	if _, _, err := discipline.Write(dir, "a.md", "new", helloSHA); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "a.md"))
	if string(b) != "new" {
		t.Fatalf("内容 = %q, want new", b)
	}
}

func TestWriteRejectsBadNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b.md", "sub" + string(filepath.Separator) + "x.md"} {
		if _, _, err := discipline.Write(t.TempDir(), name, "x", ""); !errors.Is(err, discipline.ErrBadName) {
			t.Errorf("name=%q err = %v, want ErrBadName", name, err)
		}
	}
}

func TestWriteRejectsOversize(t *testing.T) {
	big := make([]byte, discipline.MaxBlockSize+1)
	if _, _, err := discipline.Write(t.TempDir(), "a.md", string(big), ""); !errors.Is(err, discipline.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestBuiltinsAndDefaultTier(t *testing.T) {
	bs := discipline.Builtins()
	if len(bs) != 2 {
		t.Fatalf("len = %d, want 2", len(bs))
	}
	if bs[0].Tier != discipline.TierSubagent || bs[1].Tier != discipline.TierSingleContext {
		t.Fatalf("顺序 = %q/%q", bs[0].Tier, bs[1].Tier)
	}
	if bs[0].Content == "" || bs[1].Content == "" {
		t.Fatal("内置正文不能为空")
	}
	for exec, want := range map[string]string{
		"opencode": discipline.TierSubagent,
		"claude":   discipline.TierSubagent,
		"codex":    discipline.TierSingleContext,
		"grok":     discipline.TierSingleContext,
		"fake":     discipline.TierSingleContext, // 未登记一律保守取单上下文版
	} {
		if got := discipline.DefaultTierFor(exec); got != want {
			t.Errorf("DefaultTierFor(%q) = %q, want %q", exec, got, want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/discipline/ -run 'TestList|TestRead|TestWrite|TestBuiltins' -v`
Expected: 编译失败，`undefined: discipline.List` 等。

- [ ] **Step 3: 提取包级 `resolvePath`**

在 `internal/discipline/resolver.go` 里把 `func (r *Resolver) resolvePath(name string)` 改成包级函数，并让 `Resolver.For` 改调 `resolvePath(r.dir, name)`：

```go
// resolvePath 把配置里的文件名换算为绝对路径，并拒绝一切非「纯文件名」的写法。
//
// 参数：
//   - dir: 纪律块目录；name: 配置或请求里给的文件名
//
// 返回：
//   - 绝对路径；名字非法时返回包装了 ErrBadName 的错误（文案保留目录路径，
//     用户一眼能看出该把文件放哪）
//
// 为什么只收纯文件名：一杜绝路径穿越（../../etc 之类），二保证纪律块只有一个家、
// 不会散落各处——运维找配置时只需要看一个目录（envfile.resolvePath 同款理由）。
func resolvePath(dir, name string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("%w: %q 不能含路径分隔符：只支持 %s 下的纯文件名", ErrBadName, name, dir)
	}
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("%w: %q：只支持 %s 下的纯文件名", ErrBadName, name, dir)
	}
	return filepath.Join(dir, name), nil
}
```

- [ ] **Step 4: 导出内置两版与默认档位**

在 `internal/discipline/discipline.go` 末尾追加：

```go
// Builtin 是一份内置纪律块（Tier + 正文）。控制台把它作为只读条目展示，
// 并允许「以此为模板新建」——用户想微调内置纪律时不必去仓库里翻原文。
type Builtin struct {
	Tier    string
	Content string
}

// Builtins 返回全部内置纪律块，顺序固定为 subagent、single-context。
//
// 顺序固定是给界面用的：列表次序不该随 map 迭代而抖动。
func Builtins() []Builtin {
	return []Builtin{
		{Tier: TierSubagent, Content: builtinSubagent},
		{Tier: TierSingleContext, Content: builtinSingleContext},
	}
}

// DefaultTierFor 返回该 executor 在「未配置」这一档会用到的内置版本名。
//
// 未登记的 executor 一律 TierSingleContext，理由见 builtinFor 的注释。
// 界面即使在已配置的档位上也要显示它——那是「改回默认会变成什么」的预告。
func DefaultTierFor(executor string) string {
	if defaultTier[executor] == TierSubagent {
		return TierSubagent
	}
	return TierSingleContext
}
```

- [ ] **Step 5: 实现 `files.go`**

```go
// files.go —— 纪律块文件的列举与读写（控制台配置面用）。
//
// 职责：
//   - List/Read/Write：<DataDir>/discipline 下**纯文件名**的查与改
//   - 与 Resolver 共用 resolvePath 与 maxBlockSize，判据只有一处
//
// 边界：
//   - **本层不打日志**：纯文件操作，错误一律 %w 带上下文，日志由 agentd 的
//     handler 层统一打（与 internal/store 同一条纪律）
//   - 不理解纪律内容、不碰配置映射（那是 Resolver 与 config 的事）
//   - 不做删除与改名：改名会让配置里的映射静默指空（见 spec §1.1）
package discipline

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
)

// MaxBlockSize 是单个纪律块文件的大小上限（64 KiB），与 Resolver 读盘时的判据同源。
const MaxBlockSize = maxBlockSize

var (
	// ErrBadName 表示文件名不是「纯文件名」，调用方应答 400。
	ErrBadName = errors.New("纪律块文件名非法")
	// ErrTooLarge 表示正文超过 MaxBlockSize，调用方应答 400。
	ErrTooLarge = errors.New("纪律块文件超过大小上限")
	// ErrExists 表示新建时同名文件已存在，调用方应答 409。
	ErrExists = errors.New("同名纪律块文件已存在")
	// ErrBaseMismatch 表示前置哈希与磁盘现状不符，调用方应答 409 并回带现状。
	ErrBaseMismatch = errors.New("纪律块文件已被改动")
)

// FileInfo 是纪律块目录下的一个文件（不含正文）。
type FileInfo struct {
	Name   string
	Size   int64
	SHA256 string
}

// List 列举纪律块目录下的全部普通文件，按名字升序。
//
// 参数：
//   - dir: 纪律块目录，通常取 Dir(cfg.DataDir)
//
// 返回：
//   - 文件列表（含大小与哈希）；目录不存在时返回空切片与 nil
//
// 注意：
//   - **目录不存在不是错误**：<DataDir>/discipline 没有任何东西自动创建，
//     首次打开设置页时它本来就不存在，报错会把「还没建」画成「读不了」
//   - 子目录与非普通文件跳过：纪律块只有一层，不递归
func List(dir string) ([]FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []FileInfo{}, nil
		}
		return nil, fmt.Errorf("读取纪律块目录 %s: %w", dir, err)
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("读取纪律块文件 %s: %w", e.Name(), err)
		}
		out = append(out, FileInfo{Name: e.Name(), Size: int64(len(data)), SHA256: hashOf(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Read 读一个纪律块文件的正文。
//
// 返回：
//   - 正文、sha256、字节数；文件不存在时错误可用 errors.Is(err, fs.ErrNotExist) 判定
func Read(dir, name string) (content, sha string, size int64, err error) {
	path, err := resolvePath(dir, name)
	if err != nil {
		return "", "", 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", 0, fmt.Errorf("读取纪律块文件 %s: %w", path, err)
	}
	return string(data), hashOf(data), int64(len(data)), nil
}

// Write 写一个纪律块文件，带前置哈希保护。
//
// 参数：
//   - baseSHA: 空串 = 新建（目标必须不存在）；非空 = 覆盖（须与磁盘现状一致）
//
// 返回：
//   - 新内容的 sha256 与字节数；调用方可直接拿 sha 当下一次写入的 base
//   - 冲突时返回**磁盘现状的哈希** + ErrBaseMismatch，供 409 响应体带上现状
//
// 注意：
//   - 目录不存在时以 0700 创建；文件 0600——纪律块虽不含密钥，但与 DataDir
//     下其余内容保持同一权限基线，不给「有的能被同机别的账号读」留缝
func Write(dir, name, content, baseSHA string) (sha string, size int64, err error) {
	path, err := resolvePath(dir, name)
	if err != nil {
		return "", 0, err
	}
	if len(content) > MaxBlockSize {
		return "", 0, fmt.Errorf("%w: %s 有 %d 字节，上限 %d", ErrTooLarge, name, len(content), MaxBlockSize)
	}
	cur, statErr := os.ReadFile(path)
	switch {
	case statErr == nil && baseSHA == "":
		return hashOf(cur), int64(len(cur)), fmt.Errorf("%w: %s", ErrExists, name)
	case statErr == nil && hashOf(cur) != baseSHA:
		return hashOf(cur), int64(len(cur)), fmt.Errorf("%w: %s", ErrBaseMismatch, name)
	case statErr != nil && !os.IsNotExist(statErr):
		return "", 0, fmt.Errorf("读取纪律块文件 %s: %w", path, statErr)
	case statErr != nil && baseSHA != "":
		// 带 base 却读不到：文件在编辑期间被删了，与哈希不符同属冲突语义
		return "", 0, fmt.Errorf("读取纪律块文件 %s: %w", path, statErr)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("创建纪律块目录 %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", 0, fmt.Errorf("写入纪律块文件 %s: %w", path, err)
	}
	return hashOf(content), int64(len(content)), nil
}

// hashOf 返回内容的 sha256 十六进制串（写入与列举共用，保证两处口径一致）。
func hashOf[T string | []byte](data T) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
```

import 需要 `crypto/sha256`、`encoding/hex`、`errors`、`fmt`、`os`、`path/filepath`、`sort`。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/discipline/ -v`
Expected: 全部 PASS（含既有的 resolver 测试——`resolvePath` 提级不改变对外行为）。

- [ ] **Step 7: 日志与注释自检**

- 本包**刻意不打日志**，理由写在 `files.go` 的文件头注释里（handler 层统一打）；不要在这里加 `slog`。
- 确认：`files.go` 有文件头注释；`List`/`Read`/`Write`/`Builtins`/`DefaultTierFor` 都有 doc 注释（参数、返回、注意事项）；`Write` 里「新建撞名 = 409」「0600 权限」两处有「为什么」注释。

- [ ] **Step 8: Commit**

```bash
git add internal/discipline/
git commit -m "feat(discipline): 补文件操作面（List/Read/Write）与内置两版导出"
```

---

### Task 2: Resolver 改吃活映射（热更新的地基）

**Files:**
- Modify: `internal/discipline/resolver.go`
- Modify: `internal/discipline/resolver_test.go`
- Modify: `internal/agentd/manager.go:257-272`（`NewManager`）
- Modify: `internal/agentd/server.go`（新增 `DisciplineMapping`）
- Modify: `cmd/agentd.go:183`（`NewManager` 调用）

**Interfaces:**
- Consumes: Task 1 的 `resolvePath`
- Produces:
  - `discipline.NewResolver(dir string, mapping func() map[string]string, log *slog.Logger) *Resolver`
  - `discipline.Static(m map[string]string) func() map[string]string`
  - `(*agentd.Server).DisciplineMapping() map[string]string`
  - `agentd.NewManager(st, hub, ads, cfg, discMapping func() map[string]string, approver, gate, log)`

- [ ] **Step 1: 写失败测试**

在 `internal/discipline/resolver_test.go` 追加：

```go
func TestForReadsMappingEveryCall(t *testing.T) {
	// 映射是活的：改配置后**不重建 Resolver**，下一次 For 就该看到新值。
	// 这是控制台改映射能「下个任务即生效」的唯一地基。
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mine.md"), []byte("自定义纪律\n"), 0o600)

	m := map[string]string{}
	r := discipline.NewResolver(dir, func() map[string]string { return m }, testLogger())

	if b, err := r.For("codex"); err != nil || b.Source != "内置:single-context" {
		t.Fatalf("改前 Source = %q err=%v", b.Source, err)
	}
	m["codex"] = "mine.md"
	b, err := r.For("codex")
	if err != nil {
		t.Fatalf("改后 For: %v", err)
	}
	if b.Source != "配置:mine.md" || b.Text != "自定义纪律\n" {
		t.Fatalf("改后 = %q / %q", b.Source, b.Text)
	}
}

func TestNilMappingBehavesAsEmpty(t *testing.T) {
	// nil 取值函数不能 panic：测试与早期引导路径都可能不传
	r := discipline.NewResolver(t.TempDir(), nil, testLogger())
	if b, err := r.For("opencode"); err != nil || b.Source != "内置:subagent" {
		t.Fatalf("Source = %q err = %v", b.Source, err)
	}
}
```

`testLogger()` 若文件里还没有，加一个：`func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/discipline/ -run 'TestForReadsMappingEveryCall|TestNilMapping' -v`
Expected: 编译失败（`NewResolver` 第二参类型不符）。

- [ ] **Step 3: 改 Resolver**

```go
// Resolver 按 executor 名裁出该次派发要注入的纪律块。
//
// 无状态：每次 For 都重新读盘**并重新取映射**，因此配置改动与文件改动有同一种
// 时效——都在下一个任务生效，都不需要重启 agentd。
type Resolver struct {
	dir     string                   // 纪律块文件目录
	mapping func() map[string]string // 取当前 executor 名 → 文件名映射
	log     *slog.Logger
}

// NewResolver 构造 Resolver。
//
// 参数：
//   - dir: 纪律块文件目录，通常取 Dir(cfg.DataDir)
//   - mapping: 取当前映射的函数（生产上指向 agentd 的活配置）；nil 视为空映射，
//     此时全部 executor 走内置默认
//   - log: 日志入口；nil 时退回 slog.Default()
//
// 注意：mapping 会在每次 For 时被调用，实现方必须是廉价且并发安全的
//（Server.DisciplineMapping 读的是 atomic 快照，满足这两条）。
func NewResolver(dir string, mapping func() map[string]string, log *slog.Logger) *Resolver {
	if log == nil {
		log = slog.Default()
	}
	if mapping == nil {
		log.Warn("纪律块映射取值函数为空，全部 executor 将走内置默认", "dir", dir)
		mapping = func() map[string]string { return nil }
	}
	return &Resolver{dir: dir, mapping: mapping, log: log}
}

// Static 把一份固定映射包成取值函数，供测试与不需要热更新的调用方使用。
func Static(m map[string]string) func() map[string]string {
	return func() map[string]string { return m }
}
```

`For` 开头改为 `raw, configured := r.mapping()[executor]`；`Preflight` 改为 `for executor := range r.mapping()`。

- [ ] **Step 4: 接线到活配置**

`internal/agentd/server.go` 新增（放在 `conf()` 附近）：

```go
// DisciplineMapping 返回当前配置里的 executor 名 → 纪律块文件名映射。
//
// 交给 Manager 的 discipline.Resolver 每次派发时调用，因此控制台改完映射
// **下一个任务就生效**，不必重启 agentd。返回的是当前快照持有的 map，
// 调用方只读不改（写入方永不原地修改配置，只整体换新）。
func (s *Server) DisciplineMapping() map[string]string { return s.conf().Discipline }
```

`internal/agentd/manager.go` 的 `NewManager` 加参数并改构造：

```go
//   - discMapping: 取当前纪律块映射的函数（生产上传 (*Server).DisciplineMapping）；
//     nil 时全部 executor 走内置默认
func NewManager(st *store.Store, hub *Hub, ads map[string]executor.Adapter, cfg *config.Config,
	discMapping func() map[string]string, approver *Approver, gate *permgate.Gate, log *slog.Logger) *Manager {
	disc := discipline.NewResolver(discipline.Dir(cfg.DataDir), discMapping, log)
	disc.Preflight()
	...
```

`cmd/agentd.go:183` 改为 `mgr := agentd.NewManager(st, srv.Hub(), ads, cfg, srv.DisciplineMapping, ap, gate, logger)`。

- [ ] **Step 5: 修全部调用点**

Run: `grep -rn "NewManager(\|discipline.NewResolver(" --include=*.go . | grep -v _test.go` 与同样的 `--include=*_test.go` 一遍。
测试里的固定映射一律换成 `discipline.Static(map[string]string{...})`。

- [ ] **Step 6: 跑测试确认通过**

Run: `go build ./... && go test ./internal/discipline/ ./internal/agentd/ -count=1`
Expected: 全部 PASS。

- [ ] **Step 7: 日志与注释自检**

- `NewResolver` 在 mapping 为 nil 时打 Warn（上面已含）——这是「全部走内置默认」这个重要降级的唯一可见痕迹，不能静默。
- `For` 既有的三条 Info/Error 日志保持不变（未配置 / 显式关闭 / 已加载）。
- 确认 `Resolver` 结构体注释已改成「每次 For 重新取映射」，`DisciplineMapping` 有 doc 注释。

- [ ] **Step 8: Commit**

```bash
git add internal/discipline/ internal/agentd/ cmd/agentd.go
git commit -m "refactor(discipline): Resolver 改吃活映射，配置改动下个任务即生效"
```

---

### Task 3: `swapConf` 深拷 `Discipline`

**Files:**
- Modify: `internal/agentd/server.go:240-264`
- Create: `internal/agentd/swapconf_discipline_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `swapConf` 对 `Discipline` 的写时复制保证（Task 7 依赖它）

- [ ] **Step 1: 写失败测试**

```go
// swapconf_discipline_test.go —— swapConf 对 Discipline 段的写时复制回归。
//
// 为什么单独一个文件：这条性质是「配置读者拿到的快照恒定」的一部分，
// 漏了不会有任何测试变红，但会让并发读者看到改到一半的映射。
//
// 用白盒包（package agentd）：本测试要直接调 swapConf，不值得为它开一个导出面。
package agentd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

func TestSwapConfDeepCopiesDiscipline(t *testing.T) {
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:      testToken,
		Discipline: map[string]string{"codex": "old.md"},
	}, discardLogger())

	before := env.srv.DisciplineMapping()
	if err := env.srv.swapConf(func(c *config.Config) error {
		c.Discipline["codex"] = "new.md"
		return nil
	}); err != nil {
		t.Fatalf("swapConf: %v", err)
	}
	if before["codex"] != "old.md" {
		t.Fatalf("旧快照被就地改动：codex = %q，写时复制失效", before["codex"])
	}
	if got := env.srv.DisciplineMapping()["codex"]; got != "new.md" {
		t.Fatalf("新快照未生效：%q", got)
	}
}
```

`discardLogger()` 加进 `internal/agentd/w3a_testhelpers_test.go`（白盒包共用）：

```go
// discardLogger 返回丢弃一切输出的 logger，供不关心日志的白盒用例复用。
func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestSwapConfDeepCopiesDiscipline -v`
Expected: FAIL —— `旧快照被就地改动：codex = "new.md"`。

- [ ] **Step 3: 改 swapConf**

在 `next.Targets` 的复制之后追加：

```go
	next.Discipline = make(map[string]string, len(old.Discipline)+1)
	for k, v := range old.Discipline {
		next.Discipline[k] = v
	}
```

并把结构体上方那条注释改对：

```go
//   - 深拷贝 Targets 与 Discipline 两层——它们在 agentd 运行期可被写接口修改。
//     **新增运行期可变字段时必须在此补一层深拷**：漏了不会有测试变红，但读者
//     会看到改到一半的配置，与 conf() 承诺的「快照自洽」直接冲突
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestSwapConf -v`
Expected: PASS。

- [ ] **Step 5: 日志与注释自检**

- `swapConf` 落盘成功那条 Info 加上映射规模：`s.log.Info("配置已更新并落盘", "path", s.cfgPath, "targets", len(next.Targets), "discipline", len(next.Discipline))`——写配置的两个来源从此都能在日志里分辨。
- 确认新增的 `swapconf_discipline_test.go` 与 `discardLogger` 助手都有注释。

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/
git commit -m "fix(agentd): swapConf 深拷 Discipline，否则改映射会污染旧快照"
```

---

### Task 4: 契约层（proto 类型 + fixture + TS 类型）

**Files:**
- Create: `internal/proto/discipline.go`
- Modify: `internal/proto/contract_fixture_test.go`
- Create: `web/src/api/testdata/DisciplineResp.json`（由 `-update` 生成）
- Create: `web/src/api/testdata/DisciplineMappingReq.json`（同上）
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/contract.test.ts`

**Interfaces:**
- Consumes: 无
- Produces（Go）：`proto.DisciplineResp / DisciplineBuiltin / DisciplineFile / DisciplineBinding / DisciplineMappingReq`
- Produces（TS）：同名 interface，字段与 JSON 一致

- [ ] **Step 1: 写 Go 类型**

```go
// discipline.go —— 控制台配置执行纪律的线格式（B156）。
//
// 职责：GET /api/discipline 与 PUT /api/discipline/mapping 的请求/响应结构。
// 边界：
//   - 文件正文的读写复用 FileRead / FileWriteReq / FileWriteResp / FileConflictResp，
//     不另造一套——那与工作树在线编辑是同一件事的同一形状
//   - 不含任何密钥字段：纪律块是纯文本指令
package proto

// DisciplineResp 是 GET /api/discipline 的响应：一次给全配置面要用的四样东西。
//
// 为什么一次给全：纪律分区要文件列表 + 内置全文，开发机详情要 executor 档位 +
// 可选文件名，同一份数据喂两处界面，不做两套接口。用户文件的**正文不在这里**
// （按需单读），内置全文只有两份、几 KB，随列表带走。
type DisciplineResp struct {
	Dir      string              `json:"dir"`      // <DataDir>/discipline 绝对路径，界面照原样显示
	Builtins []DisciplineBuiltin `json:"builtins"` // 内置两版全文，随二进制走，只读
	Files    []DisciplineFile    `json:"files"`    // 该机纪律块目录下的文件（不含正文）
	Bindings []DisciplineBinding `json:"bindings"` // 该机每个 executor 的当前档位
}

// DisciplineBuiltin 是一份内置纪律块。Tier 取 "subagent" / "single-context"。
type DisciplineBuiltin struct {
	Tier    string `json:"tier"`
	Content string `json:"content"`
}

// DisciplineFile 是纪律块目录下的一个文件。Size 是磁盘真实大小。
type DisciplineFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// DisciplineBinding 是一个 executor 的当前档位。
//
// Mode 三值：
//   - "default"：配置里没有这个键，用内置默认（DefaultTier 指出是哪版）
//   - "file"：用 File 指定的文件
//   - "off"：显式关闭注入
//
// DefaultTier 恒有值：Mode 为 default 时界面要显示「内置默认（single-context）」，
// 其余两档它是「改回默认会变成什么」的预告，同样要显示。
type DisciplineBinding struct {
	Executor    string `json:"executor"`
	Mode        string `json:"mode"`
	File        string `json:"file,omitempty"`
	DefaultTier string `json:"default_tier"`
}

// 纪律档位取值。与 config 的三档语义一一对应（键不存在 / 值为文件名 / 值为空串）。
const (
	DisciplineModeDefault = "default"
	DisciplineModeFile    = "file"
	DisciplineModeOff     = "off"
)

// DisciplineMappingReq 是 PUT /api/discipline/mapping 的请求体：**整段替换**。
//
// 为什么整段替换而不是逐项 patch：界面就是整块保存，整段替换让「界面所见 =
// 落盘所得」无需推理；逐项 patch 还要额外定义「没出现的键是保持还是删除」。
// 这条成立的前提是 GET 返回的 Bindings 是全集（注册的 adapter ∪ 配置里的键），
// 若日后有只送部分键的写入方，本语义必须重新审视。
type DisciplineMappingReq struct {
	Bindings []DisciplineBinding `json:"bindings"`
}
```

- [ ] **Step 2: 加 fixture 样本**

在 `contract_fixture_test.go` 的 `cases` 里追加两行，并在文件末尾加两个样本函数：

```go
		{"DisciplineResp", disciplineRespSample()},
		{"DisciplineMappingReq", disciplineMappingReqSample()},
```

```go
// disciplineRespSample 返回 DisciplineResp 的代表性样本：三档各出现一次
//（default / file / off），并含一个未注册但配置里有的 executor。
func disciplineRespSample() DisciplineResp {
	return DisciplineResp{
		Dir: "/Users/dev/.handoff/discipline",
		Builtins: []DisciplineBuiltin{
			{Tier: "subagent", Content: "# 执行纪律（先读这段，再读 plan）\n\n1. 逐 task 派全新 subagent 实现。\n"},
			{Tier: "single-context", Content: "# 执行纪律（先读这段，再读 plan）\n\n1. 在本会话内自己逐 task 实现。\n"},
		},
		Files: []DisciplineFile{
			{Name: "codex-strict.md", Size: 128,
				SHA256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
		},
		Bindings: []DisciplineBinding{
			{Executor: "codex", Mode: "file", File: "codex-strict.md", DefaultTier: "single-context"},
			{Executor: "grok", Mode: "off", DefaultTier: "single-context"},
			{Executor: "opencode", Mode: "default", DefaultTier: "subagent"},
		},
	}
}

// disciplineMappingReqSample 返回 DisciplineMappingReq 的代表性样本。
func disciplineMappingReqSample() DisciplineMappingReq {
	return DisciplineMappingReq{
		Bindings: []DisciplineBinding{
			{Executor: "codex", Mode: "file", File: "codex-strict.md", DefaultTier: "single-context"},
			{Executor: "opencode", Mode: "default", DefaultTier: "subagent"},
		},
	}
}
```

- [ ] **Step 3: 生成 fixture 并确认差异**

Run: `go test ./internal/proto/ -run TestContractFixtures -update && git status --short web/src/api/testdata/`
Expected: 新增两个 json 文件，无既有 fixture 被改动（若有，说明动到了别的结构，停下来查）。

- [ ] **Step 4: 写 TS 类型与契约断言**

`web/src/api/types.ts` 追加（注释沿用 Go 侧的语义，不逐字复制长篇理由）：

```ts
// DisciplineBuiltin 是一份内置纪律块（随二进制分发，只读）。
export interface DisciplineBuiltin {
  tier: string        // 'subagent' | 'single-context'
  content: string
}

// DisciplineFile 是纪律块目录下的一个文件（不含正文，正文按需单读）。
export interface DisciplineFile {
  name: string
  size: number
  sha256: string
}

// DisciplineBinding 是一个 executor 的当前档位。
// default_tier 恒有值：mode='default' 时用于显示「内置默认（xxx）」，
// 其余两档是「改回默认会变成什么」的预告。
export interface DisciplineBinding {
  executor: string
  mode: 'default' | 'file' | 'off'
  file?: string
  default_tier: string
}

// DisciplineResp 是 GET /api/discipline 的响应。
export interface DisciplineResp {
  dir: string
  builtins: DisciplineBuiltin[]
  files: DisciplineFile[]
  bindings: DisciplineBinding[]
}

// DisciplineMappingReq 是 PUT /api/discipline/mapping 的请求体：整段替换。
export interface DisciplineMappingReq {
  bindings: DisciplineBinding[]
}
```

`web/src/api/contract.test.ts` 追加（照该文件既有写法读 fixture）：

```ts
  it('DisciplineResp 三档与内置两版都在线格式里', async () => {
    const resp = (await import('./testdata/DisciplineResp.json')).default as DisciplineResp
    expect(resp.builtins.map((b) => b.tier)).toEqual(['subagent', 'single-context'])
    expect(resp.bindings.map((b) => b.mode).sort()).toEqual(['default', 'file', 'off'])
    // mode=default 的条目不带 file 键（omitempty），但 default_tier 必须在
    const def = resp.bindings.find((b) => b.mode === 'default')!
    expect(def.file).toBeUndefined()
    expect(def.default_tier).toBe('subagent')
  })
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/proto/ -count=1 && cd web && npx vitest run src/api/contract.test.ts`
Expected: 双绿。

- [ ] **Step 6: 日志与注释自检**

- 本 task 只有类型定义，无运行时路径，**无日志**；确认 `internal/proto/discipline.go` 有文件头注释、每个导出结构与常量组有注释，`DisciplineMappingReq` 上写清「整段替换」的前提。

- [ ] **Step 7: Commit**

```bash
git add internal/proto/ web/src/api/
git commit -m "feat(proto): 加纪律配置的线格式与契约 fixture"
```

---

### Task 5: `GET /api/discipline`

**Files:**
- Create: `internal/agentd/discipline.go`
- Create: `internal/agentd/discipline_test.go`
- Modify: `internal/agentd/server.go`（注册路由 + 路由表注释）
- Modify: `internal/agentd/manager.go`（新增 `ExecutorNames`）

**Interfaces:**
- Consumes: `discipline.List/Builtins/DefaultTierFor/Dir`（Task 1）、`proto.DisciplineResp`（Task 4）
- Produces:
  - `GET /api/discipline[?machine=]` → `proto.DisciplineResp`
  - `(*Manager).ExecutorNames() []string`（已排序的已注册 adapter 名）

- [ ] **Step 1: 写失败测试**

```go
// discipline_test.go —— 纪律配置端点的测试（白盒包：要直接看 manager 的 resolver）。
package agentd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newDisciplineEnv 构造带 DataDir、纪律映射与若干已注册 executor 的白盒环境，
// 返回环境与该机的纪律块目录路径（目录本身不预先创建——「还没建」是必测的一档）。
func newDisciplineEnv(t *testing.T, mapping map[string]string, execs ...string) (*testAgentdEnv, string) {
	t.Helper()
	dataDir := t.TempDir()
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: dataDir, Discipline: mapping,
	}, discardLogger())
	ads := map[string]executor.Adapter{}
	for _, n := range execs {
		ads[n] = &failStartAdapter{} // 只需要名字进注册表，本组用例不启动任何 executor
	}
	mgr := NewManager(env.st, env.srv.Hub(), ads, env.srv.conf(),
		env.srv.DisciplineMapping, nil, newTestGate(t), discardLogger())
	env.srv.SetManager(mgr)
	env.mgr = mgr
	return env, filepath.Join(dataDir, "discipline")
}

// putJSON 发起带 token 的 PUT（JSON body），返回状态码并把响应体解码到 out。
func (e *testAgentdEnv) putJSON(t *testing.T, path string, body any, out any) int {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut, e.ts.URL+path, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("PUT %s 解码: %v", path, err)
		}
	}
	return resp.StatusCode
}

func TestDisciplineGetListsBuiltinsFilesAndBindings(t *testing.T) {
	// 配置里放一个当前没注册的 executor 名（ghost）：它必须仍然出现在 bindings 里，
	// 否则界面看不见它、而它还在配置里生效
	env, discDir := newDisciplineEnv(t,
		map[string]string{"codex": "codex-strict.md", "ghost": ""}, "opencode", "codex")
	os.MkdirAll(discDir, 0o700)
	os.WriteFile(filepath.Join(discDir, "codex-strict.md"), []byte("自定义\n"), 0o600)

	var got proto.DisciplineResp
	if code := env.getJSON(t, "/api/discipline", &got); code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	if got.Dir != discDir {
		t.Errorf("Dir = %q, want %q", got.Dir, discDir)
	}
	if len(got.Builtins) != 2 || got.Builtins[0].Tier != "subagent" {
		t.Errorf("Builtins = %+v", got.Builtins)
	}
	if len(got.Files) != 1 || got.Files[0].Name != "codex-strict.md" {
		t.Errorf("Files = %+v", got.Files)
	}
	want := map[string]proto.DisciplineBinding{
		"codex":    {Executor: "codex", Mode: "file", File: "codex-strict.md", DefaultTier: "single-context"},
		"ghost":    {Executor: "ghost", Mode: "off", DefaultTier: "single-context"},
		"opencode": {Executor: "opencode", Mode: "default", DefaultTier: "subagent"},
	}
	if len(got.Bindings) != 3 {
		t.Fatalf("Bindings = %+v，want 3 条（注册的 ∪ 配置里的）", got.Bindings)
	}
	for _, b := range got.Bindings {
		if want[b.Executor] != b {
			t.Errorf("binding %s = %+v, want %+v", b.Executor, b, want[b.Executor])
		}
	}
}

func TestDisciplineGetOnMissingDirReturnsEmptyFiles(t *testing.T) {
	env, _ := newDisciplineEnv(t, nil, "opencode")

	var got proto.DisciplineResp
	if code := env.getJSON(t, "/api/discipline", &got); code != http.StatusOK {
		t.Fatalf("code = %d，目录不存在应当是 200 空列表而不是错误", code)
	}
	if len(got.Files) != 0 {
		t.Fatalf("Files = %+v, want 空", got.Files)
	}
}
```

`failStartAdapter`、`newTestGate`、`testAgentdEnv`/`newTestAgentdEnvWithCfg`/`getJSON` 都是包内白盒测试里现成的（分别在 `manager_test.go:431`、`manager_test.go:66`、`w3a_testhelpers_test.go`）；`discardLogger` 在 Task 3 已加。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestDisciplineGet -v`
Expected: FAIL，404（路由未注册）。

- [ ] **Step 3: 加 `Manager.ExecutorNames`**

`internal/agentd/manager.go`：

```go
// ExecutorNames 返回本机已注册的 executor 名，按名字升序。
//
// 用途：纪律配置端点要把「注册的 adapter」与「配置里已出现的键」取并集，
// 后者不能省——一个配了纪律块但当前没注册的名字（改名、临时摘掉）若不列出，
// 界面就看不见它，而它还躺在配置里。
func (m *Manager) ExecutorNames() []string { return registeredNames(m.ads) }
```

- [ ] **Step 4: 实现 handler**

```go
// 本文件实现控制台的纪律配置面（B156）：
//   - GET  /api/discipline            列出内置两版、该机纪律块文件、每个 executor 的档位
//   - GET  /api/discipline/file       读单个纪律块文件正文
//   - PUT  /api/discipline/file       写单个纪律块文件（带前置哈希）
//   - PUT  /api/discipline/mapping    整段替换该机的 discipline 配置段
//
// 边界：
//   - 文件判断力全在 internal/discipline（名字校验、大小上限、冲突判定），
//     本层只做 HTTP 编解码与错误映射，**中文错误原文原样透传**
//   - 跨机由 forwardIfRequested 处理（?machine=），本文件只管本机
//   - 不理解纪律内容；不碰任务与派发
package agentd

// handleDisciplineGet 处理 GET /api/discipline[?machine=]。
//
// 响应：
//   - 200 proto.DisciplineResp
//   - 503：manager 未就绪（与 dispatch 等路由同款：executor 名单来自 manager）
func (s *Server) handleDisciplineGet(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	s.log.Info("纪律配置查询请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Warn("纪律配置查询：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	dir := discipline.Dir(s.conf().DataDir)
	files, err := discipline.List(dir)
	if err != nil {
		s.log.Error("纪律配置查询：列举文件失败", "dir", dir, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := proto.DisciplineResp{
		Dir:      dir,
		Builtins: make([]proto.DisciplineBuiltin, 0, 2),
		Files:    make([]proto.DisciplineFile, 0, len(files)),
		Bindings: s.disciplineBindings(),
	}
	for _, b := range discipline.Builtins() {
		resp.Builtins = append(resp.Builtins, proto.DisciplineBuiltin{Tier: b.Tier, Content: b.Content})
	}
	for _, f := range files {
		resp.Files = append(resp.Files, proto.DisciplineFile{Name: f.Name, Size: f.Size, SHA256: f.SHA256})
	}
	s.log.Info("纪律配置查询完成", "dir", dir,
		"files", len(resp.Files), "bindings", len(resp.Bindings))
	writeJSON(w, http.StatusOK, resp)
}

// disciplineBindings 把「已注册的 executor ∪ 配置里已出现的键」折成档位列表，按名字升序。
//
// 三档映射：键不存在 → default；值为空串 → off；否则 → file。
func (s *Server) disciplineBindings() []proto.DisciplineBinding {
	m := s.conf().Discipline
	seen := map[string]bool{}
	names := []string{}
	for _, n := range s.mgr.ExecutorNames() {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for n := range m {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)

	out := make([]proto.DisciplineBinding, 0, len(names))
	for _, n := range names {
		b := proto.DisciplineBinding{Executor: n, DefaultTier: discipline.DefaultTierFor(n)}
		v, configured := m[n]
		switch {
		case !configured:
			b.Mode = proto.DisciplineModeDefault
		case strings.TrimSpace(v) == "":
			b.Mode = proto.DisciplineModeOff
		default:
			b.Mode, b.File = proto.DisciplineModeFile, strings.TrimSpace(v)
		}
		out = append(out, b)
	}
	return out
}
```

在 `server.go` 注册路由并在 Handler 的路由表注释里加一行：

```go
	api.HandleFunc("GET /api/discipline", s.handleDisciplineGet)
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestDiscipline -v`
Expected: PASS。

- [ ] **Step 6: 日志与注释自检**

- 入口 Info（带 method/path）、列举失败 Error（带 dir + cause）、成功 Info（带 files/bindings 计数）——**成功路径必须有日志**，否则「读到了几个文件」只能靠猜。
- 确认 `discipline.go` 有文件头注释；`handleDisciplineGet` 与 `disciplineBindings` 有 doc 注释；并集那段有「为什么不能只列注册的」的注释。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/
git commit -m "feat(agentd): GET /api/discipline 交出内置两版、文件列表与各 executor 档位"
```

---

### Task 6: `GET/PUT /api/discipline/file`

**Files:**
- Modify: `internal/agentd/discipline.go`
- Modify: `internal/agentd/discipline_test.go`
- Modify: `internal/agentd/server.go`（两条路由 + 路由表注释）

**Interfaces:**
- Consumes: `discipline.Read/Write` 与四个哨兵错误（Task 1）
- Produces:
  - `GET /api/discipline/file?name=[&machine=]` → `proto.FileRead`
  - `PUT /api/discipline/file?name=[&machine=]`（body `proto.FileWriteReq`）→ `proto.FileWriteResp`；409 时 `proto.FileConflictResp`

- [ ] **Step 1: 写失败测试**

```go
func TestDisciplineFileReadWriteRoundTrip(t *testing.T) {
	env, dir := newDisciplineEnv(t, nil, "codex")

	var wrote proto.FileWriteResp
	code := env.putJSON(t, "/api/discipline/file?name=mine.md",
		proto.FileWriteReq{Content: "纪律正文\n"}, &wrote)
	if code != http.StatusOK {
		t.Fatalf("新建 code = %d", code)
	}
	var read proto.FileRead
	if code := env.getJSON(t, "/api/discipline/file?name=mine.md", &read); code != http.StatusOK {
		t.Fatalf("读 code = %d", code)
	}
	if read.Content != "纪律正文\n" || read.SHA256 != wrote.SHA256 {
		t.Fatalf("读回 = %q / %s，写入回的是 %s", read.Content, read.SHA256, wrote.SHA256)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.md")); err != nil {
		t.Fatalf("落盘: %v", err)
	}
}

func TestDisciplineFileNewOnExistingIs409(t *testing.T) {
	env, dir := newDisciplineEnv(t, nil, "codex")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "mine.md"), []byte("old"), 0o600)

	var body map[string]string
	code := env.putJSON(t, "/api/discipline/file?name=mine.md",
		proto.FileWriteReq{Content: "new"}, &body)
	if code != http.StatusConflict {
		t.Fatalf("code = %d, want 409", code)
	}
}

func TestDisciplineFileBaseMismatchIs409WithCurrent(t *testing.T) {
	env, dir := newDisciplineEnv(t, nil, "codex")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "mine.md"), []byte("hello\n"), 0o600)

	var conflict proto.FileConflictResp
	code := env.putJSON(t, "/api/discipline/file?name=mine.md",
		proto.FileWriteReq{Content: "new", BaseSHA256: "deadbeef"}, &conflict)
	if code != http.StatusConflict {
		t.Fatalf("code = %d, want 409", code)
	}
	if conflict.Current.Content != "hello\n" {
		t.Fatalf("409 必须带磁盘现状，得到 %q", conflict.Current.Content)
	}
}

func TestDisciplineFileRejectsBadNameAndOversize(t *testing.T) {
	env, _ := newDisciplineEnv(t, nil, "codex")
	var body map[string]string
	if code := env.putJSON(t, "/api/discipline/file?name=sub%2Fx.md",
		proto.FileWriteReq{Content: "x"}, &body); code != http.StatusBadRequest {
		t.Errorf("含分隔符 code = %d, want 400", code)
	}
	big := strings.Repeat("x", 64*1024+1)
	if code := env.putJSON(t, "/api/discipline/file?name=big.md",
		proto.FileWriteReq{Content: big}, &body); code != http.StatusBadRequest {
		t.Errorf("超限 code = %d, want 400", code)
	}
}

func TestDisciplineFileReadMissingIs404(t *testing.T) {
	env, _ := newDisciplineEnv(t, nil, "codex")
	var body map[string]string
	if code := env.getJSON(t, "/api/discipline/file?name=nope.md", &body); code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", code)
	}
}
```

`env.putJSON` 与 `newDisciplineEnv` 已在 Task 5 的测试文件里定义，本 task 直接复用。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestDisciplineFile -v`
Expected: FAIL，404（路由未注册）。

- [ ] **Step 3: 实现两个 handler**

```go
// handleDisciplineFileRead 处理 GET /api/discipline/file?name=[&machine=]。
//
// 响应：200 proto.FileRead / 400 名字非法 / 404 文件不存在。
// 注意：内置两版**不走这条**——它们的全文已随 GET /api/discipline 一并交出。
func (s *Server) handleDisciplineFileRead(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	dir := discipline.Dir(s.conf().DataDir)
	s.log.Info("纪律块读文件请求", "dir", dir, "name", name)

	content, sha, size, err := discipline.Read(dir, name)
	if err != nil {
		switch {
		case errors.Is(err, discipline.ErrBadName):
			s.log.Warn("纪律块读文件被拒：名字非法", "name", name, "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, fs.ErrNotExist):
			s.log.Warn("纪律块读文件：目标不存在", "dir", dir, "name", name)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "纪律块文件不存在"})
		default:
			s.log.Error("纪律块读文件失败", "dir", dir, "name", name, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取纪律块文件失败"})
		}
		return
	}
	s.log.Info("纪律块读文件完成", "dir", dir, "name", name, "bytes", size, "sha256", shortHash(sha))
	writeJSON(w, http.StatusOK, proto.FileRead{Content: content, Size: size, SHA256: sha})
}

// handleDisciplineFileWrite 处理 PUT /api/discipline/file?name=[&machine=]。
//
// 请求体 proto.FileWriteReq：base_sha256 为空串 = 新建（目标必须不存在）。
//
// 响应：200 FileWriteResp / 400 名字非法或超限 / 409 撞名或冲突（带磁盘现状）。
//
// 注意：**中文错误原文原样透传**，不吞成「操作失败」——用户看到「不能含路径
// 分隔符：只支持 …/discipline 下的纯文件名」能立刻改对（沿工作树写文件的纪律）。
func (s *Server) handleDisciplineFileWrite(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	dir := discipline.Dir(s.conf().DataDir)

	var req proto.FileWriteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("纪律块写文件请求体解析失败", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	// 正文可能有几十 KB，日志只记长度不记内容
	s.log.Info("纪律块写文件请求", "dir", dir, "name", name,
		"bytes", len(req.Content), "base", shortHash(req.BaseSHA256))

	sha, size, err := discipline.Write(dir, name, req.Content, req.BaseSHA256)
	if err != nil {
		switch {
		case errors.Is(err, discipline.ErrBadName), errors.Is(err, discipline.ErrTooLarge):
			s.log.Warn("纪律块写文件被拒", "name", name, "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, discipline.ErrExists):
			s.log.Warn("纪律块写文件被拒：撞名", "dir", dir, "name", name)
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, discipline.ErrBaseMismatch):
			// 409 的 body 带磁盘现状：界面据此提供「重新加载」，绝不静默覆盖
			cur, curSHA, curSize, rerr := discipline.Read(dir, name)
			if rerr != nil {
				s.log.Error("纪律块写文件冲突后读现状失败", "name", name, "cause", rerr)
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			s.log.Warn("纪律块写文件冲突", "dir", dir, "name", name,
				"base", shortHash(req.BaseSHA256), "current", shortHash(curSHA))
			writeJSON(w, http.StatusConflict, proto.FileConflictResp{
				Error:   "纪律块文件已被改动",
				Current: proto.FileRead{Content: cur, Size: curSize, SHA256: curSHA},
			})
		case errors.Is(err, fs.ErrNotExist):
			s.log.Warn("纪律块写文件：目标在编辑期间被删", "dir", dir, "name", name)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "纪律块文件不存在"})
		default:
			s.log.Error("纪律块写文件失败", "dir", dir, "name", name, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "写入纪律块文件失败"})
		}
		return
	}
	s.log.Info("纪律块写文件完成", "dir", dir, "name", name, "bytes", size, "sha256", shortHash(sha))
	writeJSON(w, http.StatusOK, proto.FileWriteResp{SHA256: sha, Size: size})
}
```

注册路由：

```go
	api.HandleFunc("GET /api/discipline/file", s.handleDisciplineFileRead)
	api.HandleFunc("PUT /api/discipline/file", s.handleDisciplineFileWrite)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestDisciplineFile -v`
Expected: 全部 PASS。

- [ ] **Step 5: 日志与注释自检**

- 每个错误分支都有 Warn/Error 且带 name + cause；4xx 用 Warn、5xx 用 Error（沿 `writeEntryError` 的既有分级）。
- 读与写的**成功路径**都有 Info（带 bytes 与短哈希）。
- 正文只记长度不记内容——这条要在代码里有注释，否则下一个人会顺手把 content 打进日志。

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/
git commit -m "feat(agentd): 纪律块文件的读写端点（带前置哈希与撞名保护）"
```

---

### Task 7: `PUT /api/discipline/mapping` 与热更新端到端回归

**Files:**
- Modify: `internal/agentd/discipline.go`
- Modify: `internal/agentd/discipline_test.go`
- Modify: `internal/agentd/server.go`（路由 + 路由表注释）

**Interfaces:**
- Consumes: `swapConf`（Task 3）、`discipline.Resolver` 的活映射（Task 2）、`proto.DisciplineMappingReq`（Task 4）
- Produces: `PUT /api/discipline/mapping[?machine=]` → `proto.DisciplineResp`（保存后的最新状态，界面直接拿它刷新）

- [ ] **Step 1: 写失败测试**

```go
func TestDisciplineMappingSavesThreeModes(t *testing.T) {
	env, dir := newDisciplineEnv(t, nil, "codex")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "mine.md"), []byte("x"), 0o600)

	var got proto.DisciplineResp
	code := env.putJSON(t, "/api/discipline/mapping", proto.DisciplineMappingReq{
		Bindings: []proto.DisciplineBinding{
			{Executor: "codex", Mode: "file", File: "mine.md"},
			{Executor: "grok", Mode: "off"},
			{Executor: "opencode", Mode: "default"},
		},
	}, &got)
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	m := env.srv.DisciplineMapping()
	if m["codex"] != "mine.md" {
		t.Errorf("file 档 = %q", m["codex"])
	}
	if v, ok := m["grok"]; !ok || v != "" {
		t.Errorf("off 档应是空串且键存在，得到 %q/%v", v, ok)
	}
	if _, ok := m["opencode"]; ok {
		t.Errorf("default 档必须是**键不存在**，现在键还在")
	}
}

func TestDisciplineMappingRejectsMissingFile(t *testing.T) {
	env, _ := newDisciplineEnv(t, nil, "codex")
	var body map[string]string
	code := env.putJSON(t, "/api/discipline/mapping", proto.DisciplineMappingReq{
		Bindings: []proto.DisciplineBinding{{Executor: "codex", Mode: "file", File: "nope.md"}},
	}, &body)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400（配一个不存在的文件等于埋一次必然失败的派发）", code)
	}
	if _, ok := env.srv.DisciplineMapping()["codex"]; ok {
		t.Error("校验失败时不得落盘任何改动")
	}
}

func TestDisciplineMappingRejectsBadMode(t *testing.T) {
	env, _ := newDisciplineEnv(t, nil, "codex")
	var body map[string]string
	if code := env.putJSON(t, "/api/discipline/mapping", proto.DisciplineMappingReq{
		Bindings: []proto.DisciplineBinding{{Executor: "codex", Mode: "sometimes"}},
	}, &body); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", code)
	}
}

func TestDisciplineMappingTakesEffectWithoutRestart(t *testing.T) {
	// 本条是 Task 2 + Task 3 + 本 task 合起来的唯一判据：
	// 改完映射**不重建 Manager**，下一次纪律解析就该看到新值。
	env, dir := newDisciplineEnv(t, nil, "codex")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "mine.md"), []byte("自定义纪律\n"), 0o600)

	// 白盒：直接问 manager 自己的 resolver，绕过任何缓存层
	before, err := env.mgr.discipline.For("codex")
	if err != nil || before.Source != "内置:single-context" {
		t.Fatalf("改前 = %q err=%v", before.Source, err)
	}
	var got proto.DisciplineResp
	if code := env.putJSON(t, "/api/discipline/mapping", proto.DisciplineMappingReq{
		Bindings: []proto.DisciplineBinding{{Executor: "codex", Mode: "file", File: "mine.md"}},
	}, &got); code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	after, err := env.mgr.discipline.For("codex")
	if err != nil {
		t.Fatalf("改后 For: %v", err)
	}
	if after.Source != "配置:mine.md" || after.Text != "自定义纪律\n" {
		t.Fatalf("改后 = %q / %q，热更新失效（要重启才生效等于界面在骗人）", after.Source, after.Text)
	}
}
```

本组用例全在白盒包里，`env.mgr.discipline.For(...)` 直接可用——不需要为测试开任何导出面。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestDisciplineMapping -v`
Expected: FAIL，404。

- [ ] **Step 3: 实现 handler**

```go
// handleDisciplineMapping 处理 PUT /api/discipline/mapping[?machine=]。
//
// 请求体 proto.DisciplineMappingReq：**整段替换**该机的 discipline 配置段。
//
// 响应：200 proto.DisciplineResp（保存后的最新状态，界面直接拿它刷新）
//       400 mode 非法 / executor 为空 / file 档指向不存在的文件
//       503 manager 未就绪
//
// 为什么要校验「file 档的文件必须存在」：Resolver 的既定语义是「配了但读不到 =
// 派发失败」（刻意不退回内置，否则用户会以为跑的是自己那套）。把错误挡在保存
// 这一刻，好过让它在三天后某次派发时炸出来。注意这只是保存时的一次性校验——
// 文件仍可能事后被删，那时的失败仍由派发路径承担。
func (s *Server) handleDisciplineMapping(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	if s.mgr == nil {
		s.log.Warn("纪律映射保存：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var req proto.DisciplineMappingReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("纪律映射保存：请求体无法解析", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	dir := discipline.Dir(s.conf().DataDir)
	s.log.Info("纪律映射保存请求", "bindings", len(req.Bindings), "dir", dir)

	next := map[string]string{}
	for _, b := range req.Bindings {
		name := strings.TrimSpace(b.Executor)
		if name == "" {
			s.log.Warn("纪律映射保存被拒：executor 名为空")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "executor 名不能为空"})
			return
		}
		switch b.Mode {
		case proto.DisciplineModeDefault:
			// 默认档 = 配置里**不出现这个键**，什么都不写
		case proto.DisciplineModeOff:
			next[name] = ""
		case proto.DisciplineModeFile:
			file := strings.TrimSpace(b.File)
			if _, _, _, err := discipline.Read(dir, file); err != nil {
				s.log.Warn("纪律映射保存被拒：文件不可用", "executor", name, "file", file, "cause", err)
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": fmt.Sprintf("%s 指定的纪律块文件不可用：%v", name, err)})
				return
			}
			next[name] = file
		default:
			s.log.Warn("纪律映射保存被拒：档位非法", "executor", name, "mode", b.Mode)
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("%s 的档位 %q 非法：只支持 default/file/off", name, b.Mode)})
			return
		}
	}
	if err := s.swapConf(func(c *config.Config) error {
		c.Discipline = next
		return nil
	}); err != nil {
		s.log.Error("纪律映射落盘失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("纪律映射已保存", "configured", len(next))
	s.handleDisciplineGet(w, r) // 回最新状态，界面直接拿它刷新
}
```

注册路由：`api.HandleFunc("PUT /api/discipline/mapping", s.handleDisciplineMapping)`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestDiscipline -v -count=1`
Expected: 本 task 与 Task 5/6 的用例全绿。

- [ ] **Step 5: 日志与注释自检**

- 入口 Info（bindings 计数）、每个拒绝分支 Warn（带 executor + 原因）、落盘失败 Error、**成功 Info**（configured 计数）。
- 「default 档不写键」这行必须有注释——它看起来像漏了赋值。

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/
git commit -m "feat(agentd): 纪律映射保存端点，落盘即生效不必重启"
```

---

### Task 8: 前端 API 客户端

**Files:**
- Modify: `web/src/api/client.ts`
- Modify: `web/src/api/client.test.ts`

**Interfaces:**
- Consumes: Task 4 的 TS 类型
- Produces:
  - `fetchDiscipline(machine: string): Promise<DisciplineResp>`
  - `fetchDisciplineFile(machine: string, name: string): Promise<FileRead>`
  - `saveDisciplineFile(machine: string, name: string, req: FileWriteReq): Promise<FileWriteResp>`
  - `saveDisciplineMapping(machine: string, bindings: DisciplineBinding[]): Promise<DisciplineResp>`

- [ ] **Step 1: 写失败测试**

在 `web/src/api/client.test.ts` 追加（照该文件既有的 fetch mock 写法）：

```ts
describe('纪律配置接口', () => {
  it('本机不带 machine 参数，远程机带', async () => {
    const fetchMock = mockFetchJSON({ dir: '/d', builtins: [], files: [], bindings: [] })
    await fetchDiscipline('')
    expect(fetchMock.mock.calls[0][0]).toBe('/api/discipline')
    await fetchDiscipline('mac-02')
    expect(fetchMock.mock.calls[1][0]).toBe('/api/discipline?machine=mac-02')
  })

  it('文件名与机器名都过 encodeURIComponent', async () => {
    const fetchMock = mockFetchJSON({ content: '', size: 0, sha256: '' })
    await fetchDisciplineFile('mac 02', 'my rules.md')
    expect(fetchMock.mock.calls[0][0]).toBe('/api/discipline/file?name=my%20rules.md&machine=mac%2002')
  })

  it('保存映射走 PUT 并带 bindings', async () => {
    const fetchMock = mockFetchJSON({ dir: '/d', builtins: [], files: [], bindings: [] })
    await saveDisciplineMapping('', [{ executor: 'codex', mode: 'off', default_tier: 'single-context' }])
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(init.method).toBe('PUT')
    expect(JSON.parse(init.body as string)).toEqual({
      bindings: [{ executor: 'codex', mode: 'off', default_tier: 'single-context' }],
    })
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/api/client.test.ts`
Expected: FAIL，`fetchDiscipline is not a function`。

- [ ] **Step 3: 实现**

```ts
// machineQuery 把机器名折成查询串片段：本机（''）不带参数，远程机带 ?machine=。
//
// 为什么本机不带：agentd 的 forwardIfRequested 只认「非空 machine」，
// 带一个空值会被当成机器名去 targets 里查，然后 400。
function machineQuery(machine: string, prefix: '?' | '&' = '?'): string {
  return machine === '' ? '' : `${prefix}machine=${encodeURIComponent(machine)}`
}

// fetchDiscipline 取某台机器的纪律配置面（GET /api/discipline）：
// 目录、内置两版全文、该机文件列表、每个 executor 的档位。
export function fetchDiscipline(machine: string): Promise<DisciplineResp> {
  return request<DisciplineResp>(`/api/discipline${machineQuery(machine)}`)
}

// fetchDisciplineFile 读某台机器上一个纪律块文件的正文（GET /api/discipline/file）。
// 内置两版不走这条——它们的全文已在 fetchDiscipline 的结果里。
export function fetchDisciplineFile(machine: string, name: string): Promise<FileRead> {
  return request<FileRead>(
    `/api/discipline/file?name=${encodeURIComponent(name)}${machineQuery(machine, '&')}`,
  )
}

// saveDisciplineFile 写一个纪律块文件（PUT /api/discipline/file）。
//
// req.base_sha256 为空串表示新建：目标已存在时后端回 409，绝不静默覆盖。
// 冲突（409）时响应体是 FileConflictResp，由调用方按 ApiError 处理。
export function saveDisciplineFile(
  machine: string, name: string, req: FileWriteReq,
): Promise<FileWriteResp> {
  return putJSON<FileWriteResp>(
    `/api/discipline/file?name=${encodeURIComponent(name)}${machineQuery(machine, '&')}`, req,
  )
}

// saveDisciplineMapping 整段替换某台机器的 executor→纪律块映射
//（PUT /api/discipline/mapping），返回保存后的最新配置面。
export function saveDisciplineMapping(
  machine: string, bindings: DisciplineBinding[],
): Promise<DisciplineResp> {
  return putJSON<DisciplineResp>(`/api/discipline/mapping${machineQuery(machine)}`, { bindings })
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/api/client.test.ts`
Expected: PASS。

- [ ] **Step 5: 可观测性与注释自检**

- 前端**不打 console.log**（红线）；这四个函数的失败经既有 `ApiError` 冒泡，由调用方在界面上原文展示。
- 确认每个导出函数有注释；`machineQuery` 有「为什么本机不带参数」的注释。

- [ ] **Step 6: Commit**

```bash
git add web/src/api/
git commit -m "feat(web): 纪律配置的四个 API 客户端函数"
```

---

### Task 9: 设置页「执行纪律」分区

**Files:**
- Create: `web/src/app/settings/DisciplinePage.tsx`
- Create: `web/src/app/settings/DisciplinePage.test.tsx`
- Modify: `web/src/app/settings/SettingsPage.tsx`
- Modify: `web/src/app/settings/SettingsPage.test.tsx`

**Interfaces:**
- Consumes: Task 8 的 `fetchDiscipline / fetchDisciplineFile / saveDisciplineFile`、`useMachines`
- Produces: `<DisciplinePage />`（无 props，自取机器列表）

**形态基准：** `prototypes/discipline-config/pages/settings.html` 的「执行纪律」分区。

- [ ] **Step 1: 写失败测试**

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchDiscipline, fetchDisciplineFile, saveDisciplineFile } from '../../api/client'
import { useMachines } from '../data/useMachines'
import { DisciplinePage } from './DisciplinePage'

vi.mock('../data/useMachines', () => ({ useMachines: vi.fn() }))
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchDiscipline: vi.fn(), fetchDisciplineFile: vi.fn(), saveDisciplineFile: vi.fn() }
})

const RESP = {
  dir: '/home/dev/.handoff/discipline',
  builtins: [
    { tier: 'subagent', content: '内置 A 版正文' },
    { tier: 'single-context', content: '内置 B 版正文' },
  ],
  files: [{ name: 'codex-strict.md', size: 12, sha256: 'abc' }],
  bindings: [
    { executor: 'codex', mode: 'file' as const, file: 'codex-strict.md', default_tier: 'single-context' },
    { executor: 'opencode', mode: 'default' as const, default_tier: 'subagent' },
  ],
}

beforeEach(() => {
  vi.mocked(useMachines).mockReturnValue({
    data: { machines: [
      { name: '', addr: '127.0.0.1:7777', reachable: true, version: 'v1', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '' },
      { name: 'mac-02', addr: '10.0.0.2:7777', reachable: false, version: '', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: 'connection refused' },
    ] },
    disconnected: false, sessionExpired: false, refresh: vi.fn(),
  } as never)
  vi.mocked(fetchDiscipline).mockResolvedValue(RESP as never)
  vi.mocked(fetchDisciplineFile).mockResolvedValue({ content: '我的纪律', size: 12, sha256: 'abc' } as never)
})

it('内置版只读且给出「以此为模板新建」', async () => {
  render(<DisciplinePage />)
  await userEvent.click(await screen.findByRole('button', { name: /subagent/ }))
  expect(await screen.findByRole('textbox', { name: /纪律块正文/ })).toHaveAttribute('readonly')
  expect(screen.getByRole('button', { name: '以此为模板新建' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '保存' })).not.toBeInTheDocument()
})

it('用户文件可编辑并保存，正文与 base_sha256 一并送出', async () => {
  vi.mocked(saveDisciplineFile).mockResolvedValue({ sha256: 'def', size: 20 } as never)
  render(<DisciplinePage />)
  await userEvent.click(await screen.findByRole('button', { name: /codex-strict\.md/ }))
  const box = await screen.findByRole('textbox', { name: /纪律块正文/ })
  await userEvent.clear(box)
  await userEvent.type(box, '新正文')
  await userEvent.click(screen.getByRole('button', { name: '保存' }))
  await waitFor(() => expect(saveDisciplineFile).toHaveBeenCalledWith(
    '', 'codex-strict.md', { content: '新正文', base_sha256: 'abc' }))
})

it('每个文件标注被哪些 executor 引用', async () => {
  render(<DisciplinePage />)
  expect(await screen.findByText(/codex 在用/)).toBeInTheDocument()
  // opencode 是 default 档，引用的是内置 subagent 版
  expect(screen.getByText(/opencode 在用/)).toBeInTheDocument()
})

it('断开的机器不给编辑器，给断开原因原文', async () => {
  render(<DisciplinePage />)
  await userEvent.click(screen.getByRole('button', { name: /mac-02/ }))
  expect(await screen.findByText(/connection refused/)).toBeInTheDocument()
  expect(screen.queryByRole('textbox', { name: /纪律块正文/ })).not.toBeInTheDocument()
  expect(fetchDiscipline).not.toHaveBeenCalledWith('mac-02')
})

it('正在编辑时机器列表刷新不覆盖输入', async () => {
  const { rerender } = render(<DisciplinePage />)
  await userEvent.click(await screen.findByRole('button', { name: /codex-strict\.md/ }))
  const box = await screen.findByRole('textbox', { name: /纪律块正文/ })
  await userEvent.clear(box)
  await userEvent.type(box, '编辑中')
  rerender(<DisciplinePage />)
  expect(box).toHaveValue('编辑中')
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/settings/DisciplinePage.test.tsx`
Expected: FAIL，模块不存在。

- [ ] **Step 3: 实现 `DisciplinePage.tsx`**

按原型形态实现，要点（写进文件头注释）：

```tsx
// DisciplinePage —— 设置页「执行纪律」分区（B156 spec §2.1）。
//
// 职责：按机器编辑 <DataDir>/discipline/ 下的纪律块正文；内置两版只读展示，
// 可「以此为模板新建」。
//
// 形态基准：prototypes/discipline-config/pages/settings.html。
//
// 边界：
//   - **不轮询**：配置不是实时事实，进分区/切机器/保存后各拉一次即可。
//     照抄开发机分区的 15s 探活会把用户正在编辑的正文覆盖掉
//   - 不做删除与改名（改名会让映射静默指空，见 spec §1.1）
//   - 映射不在这里改：那是开发机详情的事，本页只标注「谁在用」
//   - 断开的机器不发请求、不画编辑器，直接展示 error 原文（诚实展示纪律）
```

实现要点：

- 机器切换用 `useMachines(true)` 的列表；选中机器 `machine`（`''` = 本机）。
- 机器可达时 `useEffect` 调 `fetchDiscipline(machine)`；不可达时**不发请求**。
- 选中项 `selected` 两种形态：`{kind:'builtin', tier}` 与 `{kind:'file', name}`。
- 选中用户文件时调 `fetchDisciplineFile` 取正文与 `sha256`，存进本地 `draft` 与 `baseSha`；`textarea` 受控于 `draft`，**只有切换选中项或保存成功后才重置**。
- 「谁在用」由 `bindings` 推导：`mode==='file' && file===name`，或 `mode==='default' && default_tier===tier`。
- 保存：`saveDisciplineFile(machine, name, {content: draft, base_sha256: baseSha})`；成功后用返回的 `sha256` 更新 `baseSha` 并 `fetchDiscipline` 刷新列表；`ApiError` 的 `status===409` 时提示「已被改动」并给「重新加载」按钮（调 `fetchDisciplineFile` 覆盖 draft）。
- 新建弹层：文件名 + 起始内容（空白 / 复制当前选中的内置版）；提交调 `saveDisciplineFile(machine, name, {content, base_sha256: ''})`。
- textarea 必须有可访问名（`aria-label="纪律块正文"`），内置版加 `readOnly`。
- 错误一律 `errorMessage(err)` 原文展示。

- [ ] **Step 4: 挂进设置页**

`SettingsPage.tsx` 的 `SECTIONS` 改为：

```tsx
const SECTIONS = [
  { key: 'machines', label: '开发机' },
  { key: 'discipline', label: '执行纪律' },
  { key: 'general', label: '常规' },
  { key: 'env', label: 'Env 文件' },
] as const
```

并在 body 里加 `{section === 'discipline' && <DisciplinePage />}`。同步给 `SettingsPage.test.tsx` 加一条「点『执行纪律』能切到该分区」的用例。

- [ ] **Step 5: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/settings/`
Expected: 全绿。

- [ ] **Step 6: 可观测性与注释自检**

- 无 `console.log`；所有失败在界面上有落点（顶部错误条或按钮旁提示），不静默吞。
- 文件头注释含「不轮询」这条纪律与它的理由；「谁在用」的推导有注释说明 default 档对应内置版。

- [ ] **Step 7: Commit**

```bash
git add web/src/app/settings/
git commit -m "feat(web): 设置页新增执行纪律分区（按机器编辑纪律块正文）"
```

---

### Task 10: 开发机详情的映射块

**Files:**
- Create: `web/src/app/machines/MachineDiscipline.tsx`
- Create: `web/src/app/machines/MachineDiscipline.test.tsx`
- Modify: `web/src/app/machines/MachineDetail.tsx`

**Interfaces:**
- Consumes: Task 8 的 `fetchDiscipline / saveDisciplineMapping`
- Produces: `<MachineDiscipline machine={machine} />`（`machine: Machine`）

**形态基准：** `prototypes/discipline-config/pages/settings.html` 开发机详情里的「执行纪律」块。

- [ ] **Step 1: 写失败测试**

```tsx
it('三档下拉按当前配置回显', async () => {
  render(<MachineDiscipline machine={online} />)
  expect(await screen.findByLabelText('codex 的纪律块')).toHaveValue('file:codex-strict.md')
  expect(screen.getByLabelText('opencode 的纪律块')).toHaveValue('default')
  expect(screen.getByLabelText('grok 的纪律块')).toHaveValue('off')
})

it('默认档的选项文案写明是哪一版内置', async () => {
  render(<MachineDiscipline machine={online} />)
  expect(await screen.findByRole('option', { name: '内置默认（subagent）' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: '内置默认（single-context）' })).toBeInTheDocument()
})

it('改动标脏，保存后送出三档并清脏', async () => {
  vi.mocked(saveDisciplineMapping).mockResolvedValue(RESP as never)
  render(<MachineDiscipline machine={online} />)
  await userEvent.selectOptions(await screen.findByLabelText('opencode 的纪律块'), 'off')
  expect(screen.getByText('有未保存的改动')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '保存' }))
  await waitFor(() => expect(saveDisciplineMapping).toHaveBeenCalledWith('mac-02', [
    { executor: 'codex', mode: 'file', file: 'codex-strict.md', default_tier: 'single-context' },
    { executor: 'grok', mode: 'off', default_tier: 'single-context' },
    { executor: 'opencode', mode: 'off', default_tier: 'subagent' },
  ]))
  await waitFor(() => expect(screen.queryByText('有未保存的改动')).not.toBeInTheDocument())
})

it('保存失败时原文展示后端错误且不清脏', async () => {
  vi.mocked(saveDisciplineMapping).mockRejectedValue(new ApiError(400, 'codex 指定的纪律块文件不可用：读取纪律块文件 …/nope.md: no such file'))
  render(<MachineDiscipline machine={online} />)
  await userEvent.selectOptions(await screen.findByLabelText('codex 的纪律块'), 'default')
  await userEvent.click(screen.getByRole('button', { name: '保存' }))
  expect(await screen.findByText(/纪律块文件不可用/)).toBeInTheDocument()
  expect(screen.getByText('有未保存的改动')).toBeInTheDocument()
})

it('断开的机器不发请求也不给控件', async () => {
  render(<MachineDiscipline machine={offline} />)
  expect(await screen.findByText(/机器已断开/)).toBeInTheDocument()
  expect(fetchDiscipline).not.toHaveBeenCalled()
})
```

`online` / `offline` 用 `MachinesPage.test.tsx` 里同款的 `Machine` 样本（`online.name = 'mac-02'`，`executors: ['opencode','codex','grok']`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/machines/MachineDiscipline.test.tsx`
Expected: FAIL，模块不存在。

- [ ] **Step 3: 实现**

```tsx
// MachineDiscipline —— 开发机详情里的「执行纪律」块（B156 spec §2.2）。
//
// 职责：给这台机器的每个 executor 指定注入哪块纪律（三档），整块一次保存。
//
// 形态基准：prototypes/discipline-config/pages/settings.html。
//
// 边界：
//   - **不编辑正文**：正文在设置页的「执行纪律」分区里改，这里只选文件
//   - 不轮询：进入详情拉一次，保存后用响应刷新（响应就是最新状态）
//   - 机器断开时不发请求、不渲染控件——配置读不到也写不了，画出来只会骗人
//
// 下拉的 value 编码：'default' / 'off' / `file:<文件名>`。用前缀而不是裸文件名，
// 是为了让一个名叫 "off" 的文件不会与「关闭注入」撞值。
```

要点：

- 进场（且 `machine.reachable`）调 `fetchDiscipline(machine.name)`，拿 `bindings` 与 `files`。
- 每行 `<select aria-label={`${executor} 的纪律块`}>`：选项 = `内置默认（{default_tier}）` + 每个文件 `file:<name>` + `关闭注入（不发纪律块）`。
- 本地 `edits: Record<string, string>` 覆盖服务端值；`dirty = Object.keys(edits).length > 0`。
- 保存：把三档解码回 `DisciplineBinding[]`（**顺序按 executor 名升序**，与后端返回一致，便于断言与 diff），调 `saveDisciplineMapping`；成功用响应刷新并清 `edits`，失败保留 `edits` 并原文展示错误。
- 挂进 `MachineDetail.tsx`：在现有 `<dl>` 与「可用执行者」之后插入 `<MachineDiscipline machine={machine} />`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/machines/`
Expected: 全绿（含既有 `MachinesPage.test.tsx`）。

- [ ] **Step 5: 可观测性与注释自检**

- 无 `console.log`；保存失败原文上屏且不清脏（用户的选择不能因为一次失败被抹掉）。
- 下拉 value 前缀编码的理由必须有注释（见上）。

- [ ] **Step 6: Commit**

```bash
git add web/src/app/machines/
git commit -m "feat(web): 开发机详情新增 executor→纪律块映射配置"
```

---

### Task 11: 全量校验与形态走查

**Files:** 无新增；只跑校验与修复

- [ ] **Step 1: Go 全量**

Run: `gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1`
Expected: `gofmt -l .` **无输出**；其余 exit 0、无失败包。

- [ ] **Step 2: 前端全量**

Run: `cd web && npx vitest run && npx tsc -b && npx eslint . && npm run build`
Expected: 测试全绿、0 类型错误、0 eslint error（既有 warning 不计）、构建成功。

- [ ] **Step 3: 红线自查**

Run: `grep -rn "fmt.Printf\|os.RemoveAll" internal/ | grep -v _test.go` 与 `grep -rn "console.log" web/src/`
Expected: 与本次改动相关的命中为 0。

- [ ] **Step 4: 形态走查**

打开 `prototypes/discipline-config/pages/settings.html` 与真实控制台的设置页并排比对：分区位置与命名、机器切换、内置只读 + 另存、引用标注、断开降级、映射三档下拉与脏态提示。差异要么改真实页面，要么记账说明为何偏离。

- [ ] **Step 5: 真机判据（spec §8.3）**

在一台真实执行机上：控制台把该机某个 executor 从「内置默认」改成一份自定义文件并保存，**不重启 agentd**，随即向该机派一个该 executor 的任务，确认 `dispatch` 的 stderr 打出 `纪律块: 配置:<文件名>`，且执行者抄回的第一条纪律与该文件正文一致。

> **本步骤由审核者执行，不派发**——它要驱动 handoff 自身（起/连 agentd、派任务），与执行纪律块里「不要派发、不要调用 handoff CLI」直接冲突（B126 的教训）。

- [ ] **Step 6: Commit（如有修复）**

```bash
git add -A
git commit -m "chore(b156): 全量校验与形态走查的修复"
```

---

## 附：本计划与 spec 的对应

| spec 章节 | 落点 |
|---|---|
| §2.1 设置页分区 | Task 9 |
| §2.2 开发机详情映射块 | Task 10 |
| §2.3 三档翻译 | Task 5（读）+ Task 7（写）+ Task 10（界面） |
| §3.1 数据结构 | Task 4 |
| §3.1 Bindings 并集与整段替换 | Task 5（并集）+ Task 7（替换） |
| §3.2 校验与错误语义 | Task 6（文件）+ Task 7（映射） |
| §3.3 目录不存在 | Task 1（List）+ Task 5（用例） |
| §4.1 swapConf 深拷 | Task 3 |
| §4.2 Resolver 吃活配置 | Task 2 |
| §4.3 config.Save 丢注释 | 不做，spec 已记 |
| §5 前端落点与不轮询 | Task 9 / Task 10 |
| §6 契约与测试 | Task 4 + 各 task 的测试步骤 |
| §7 安全 | Task 1（名字校验、上限、0600） |
| §8 验收判据 | Task 11 |
