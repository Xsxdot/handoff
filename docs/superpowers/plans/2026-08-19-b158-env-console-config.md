# B158 控制台配置 Env 文件 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让控制台能按机器编辑 `<DataDir>/env/` 下的 env 文件正文（默认只显示变量清单、值不进浏览器），并给该机每个 executor 指定注入哪个 env 文件（两档），保存后下一个任务即生效、不必重启 agentd。

**Architecture:** 后端在 `internal/envfile` 补一层纯文件操作面（`List`/`Read`/`Write` + 包级 `resolvePath`），agentd 新增五个走 `?machine=` 转发的端点；`envfile.Resolver` 从「构造时吞一份映射」改成「每次取活配置」，配置落盘复用 `Server.swapConf`（需补深拷 `Env`）。前端在设置页新增「Env 文件」分区、在开发机详情新增映射块，并把 B157 的编辑器抽成共用组件 `settings/BlockEditor.tsx`。

**Tech Stack:** Go 1.26.1（slog、`net/http` Go1.22 方法路由、`gopkg.in/yaml.v3`）、React + TypeScript + Vite + vitest + Testing Library、Tailwind/shadcn。

**Spec:** `docs/superpowers/specs/2026-08-19-b158-env-console-config-design.md`
**Base:** 以 `handoff/web-console` 最新处开分支（该线才有 B157 的纪律配置面与控制台代码；`main` 两者都没有）
**孪生参照:** B157 已落地的 `internal/discipline/files.go`、`internal/agentd/discipline.go`、`web/src/app/settings/DisciplinePage.tsx`、`web/src/app/machines/MachineDiscipline.tsx`。**结构相同处一律照搬，不发明新概念**；真实差异只有三处，全部在下面的 Global Constraints 里点名。

## Global Constraints

- Go 侧日志一律 `slog`（agentd 内用 `s.log` / `m.log`），**禁止 `fmt.Printf`**；前端**禁止 `console.log`**（`console.warn` 仅限降级诊断）。
- 新建文件必须有文件头注释（职责 + 边界）；导出函数必须有 doc 注释（参数、返回、注意事项）；非显然分支写「为什么」的中文注释。
- `internal/` 下禁止 `os.RemoveAll`。
- env 文件大小上限 **64 KiB**，取自 `internal/envfile/envfile.go` 既有的 `maxEnvFileSize`，**不另立常量**。
- env 文件名只收**纯文件名**：含 `/` 或 `filepath.Separator`、等于 `""` / `.` / `..` 一律拒。
- **差异一 —— 两档语义（不可改写）**：配置里键**不存在** = 不注入；值为**文件名** = 读该文件。**env 没有第三档，也绝不写空串**——空串在 `Resolver.For` 里会走到「读 `<dir>/`」这种无意义路径，是纯粹的脏数据。服务端收到 `mode=off` 时**删键**，收到 `mode=file` 但文件名为空一律 400。
  > 与 discipline 的对应关系是**错位**的：discipline 的「键不存在」= 用内置默认、「空串」= 关闭注入。照抄 discipline 的三档翻译到这里就是错的。
- **差异二 —— 写前必须解析校验**：`PUT /api/env/file` 在落盘前用 `envfile.Parse(bytes.NewReader([]byte(content)), nil)` 跑一遍，失败回 400 且**原样透传 Parse 的错误**（它自带行号与原行）。重复键**不拦**（Resolver 既有行为是 WARN + 后者覆盖），只在清单里标注。
- **差异三 —— 值不出后端**：`GET /api/env/file/keys` 的响应结构里**没有任何字段承载值**；`Parse` 的 `lookup` 传 **nil**（不查 agentd 自己的环境变量）；日志只记 key 名、字节数与短哈希，**任何路径都不得把 env 的值写进日志或错误文本**。
- 契约改动流程：改 Go 结构 → `go test ./internal/proto/ -run TestContractFixtures -update` → 同步 `web/src/api/types.ts` 与 `web/src/api/contract.test.ts`，fixture 差异随提交一并 review。
- 每个 task 完成即 commit；提交信息用各 task「Commit」步骤给出的原文。
- 完工前必须跑：`gofmt -l .`（无输出）、`go build ./... && go vet ./... && go test ./...`、`cd web && npx vitest run && npx tsc -b && npx eslint .`。

---

### Task 1: `internal/envfile` 的文件操作面

**Files:**
- Create: `internal/envfile/files.go`
- Create: `internal/envfile/files_test.go`
- Modify: `internal/envfile/resolver.go`（把 `Resolver.resolvePath` 提成包级函数）

**Interfaces:**
- Consumes: 无（本包最底层）
- Produces:
  - `envfile.MaxFileSize` (int)
  - `envfile.ErrBadName / ErrTooLarge / ErrExists / ErrBaseMismatch` (error)
  - `envfile.FileInfo{Name string; Size int64; SHA256 string}`
  - `envfile.List(dir string) ([]FileInfo, error)`
  - `envfile.Read(dir, name string) (content, sha string, size int64, err error)`
  - `envfile.Write(dir, name, content, baseSHA string) (sha string, size int64, err error)`
  - 包级 `resolvePath(dir, name string) (string, error)`（不导出，包内共用）

> **不要把这层做进 handler**：那会让 `internal/envfile` 与 `internal/agentd` 对「什么是合法文件名」各有一套，正是 `Dir()` 当初收口目录知识要防的漂移。

- [ ] **Step 1: 写失败测试**

创建 `internal/envfile/files_test.go`：

```go
// files_test.go —— env 文件操作面的测试：列举、读、写（新建/覆盖/冲突）与名字校验。
package envfile_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/envfile"
)

// helloSHA 是 "hello\n" 的 sha256，用于钉住 Read/Write 的哈希口径。
const helloSHA = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"

func TestListEmptyWhenDirMissing(t *testing.T) {
	// 目录不存在不是错误：<DataDir>/env 没有任何东西自动创建，
	// 首次打开设置页时它本来就不存在，报错会把「还没建」画成「读不了」。
	files, err := envfile.List(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("len = %d, want 0", len(files))
	}
}

func TestListSortedWithSizeAndHash(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "b.env"), "hello\n")
	mustWrite(t, filepath.Join(dir, "a.env"), "X=1\n")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	files, err := envfile.List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len = %d, want 2（子目录必须被跳过）", len(files))
	}
	if files[0].Name != "a.env" || files[1].Name != "b.env" {
		t.Fatalf("顺序 = %v，想要按名字升序", []string{files[0].Name, files[1].Name})
	}
	if files[1].Size != 6 || files[1].SHA256 != helloSHA {
		t.Fatalf("b.env = %d/%s，想要 6/%s", files[1].Size, files[1].SHA256, helloSHA)
	}
}

func TestReadRejectsBadName(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../x.env", "a/b.env", "", ".", ".."} {
		if _, _, _, err := envfile.Read(dir, name); !errors.Is(err, envfile.ErrBadName) {
			t.Fatalf("Read(%q) err = %v，想要 ErrBadName", name, err)
		}
	}
}

func TestReadMissingIsNotExist(t *testing.T) {
	if _, _, _, err := envfile.Read(t.TempDir(), "gone.env"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v，想要 fs.ErrNotExist", err)
	}
}

func TestWriteCreatesDirAndFileWith0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "env")
	sha, size, err := envfile.Write(dir, "a.env", "hello\n", "")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if sha != helloSHA || size != 6 {
		t.Fatalf("sha/size = %s/%d，想要 %s/6", sha, size, helloSHA)
	}
	// env 文件常含凭据，权限基线必须是 0600，不给同机别的账号留缝。
	fi, err := os.Stat(filepath.Join(dir, "a.env"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o，想要 600", perm)
	}
}

func TestWriteNewOnExistingIsErrExists(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.env"), "hello\n")
	// base 为空串 = 新建；撞名必须显式失败，避免「新建」把别人的文件静默覆盖。
	if _, _, err := envfile.Write(dir, "a.env", "X=1\n", ""); !errors.Is(err, envfile.ErrExists) {
		t.Fatalf("err = %v，想要 ErrExists", err)
	}
}

func TestWriteStaleBaseIsErrBaseMismatch(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.env"), "hello\n")
	if _, _, err := envfile.Write(dir, "a.env", "X=1\n", "deadbeef"); !errors.Is(err, envfile.ErrBaseMismatch) {
		t.Fatalf("err = %v，想要 ErrBaseMismatch", err)
	}
}

func TestWriteTooLarge(t *testing.T) {
	big := make([]byte, envfile.MaxFileSize+1)
	for i := range big {
		big[i] = 'x'
	}
	if _, _, err := envfile.Write(t.TempDir(), "a.env", string(big), ""); !errors.Is(err, envfile.ErrTooLarge) {
		t.Fatalf("err = %v，想要 ErrTooLarge", err)
	}
}

func TestWriteOverwriteWithMatchingBase(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.env"), "hello\n")
	sha, _, err := envfile.Write(dir, "a.env", "X=1\n", helloSHA)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	content, cur, _, err := envfile.Read(dir, "a.env")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content != "X=1\n" || cur != sha {
		t.Fatalf("content/sha = %q/%s，想要 \"X=1\\n\"/%s", content, cur, sha)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/envfile/ -run 'TestList|TestRead|TestWrite' -count=1`
Expected: 编译失败 —— `undefined: envfile.List` 等。

- [ ] **Step 3: 把 `resolvePath` 提成包级函数**

在 `internal/envfile/resolver.go` 里删除 `func (r *Resolver) resolvePath(...)`，改为包级函数（放在 `files.go` 里，见 Step 4），并把 `For` 内的调用点改为：

```go
	path, err := resolvePath(r.dir, name)
```

- [ ] **Step 4: 写实现**

创建 `internal/envfile/files.go`：

```go
// files.go —— env 文件的列举与读写（控制台配置面用，B158）。
//
// 职责：
//   - List/Read/Write：<DataDir>/env 下**纯文件名**的查与改
//   - resolvePath：包级的纯文件名校验，与 Resolver 共用，判据只有一处
//
// 边界：
//   - **本层不打日志**：纯文件操作，错误一律 %w 带上下文，日志由 agentd 的
//     handler 层统一打（与 internal/discipline/files.go 同一条纪律）
//   - **不解析内容**：语法校验是 Parse 的事，调用方在写盘前自行调用；本层
//     连「这是不是一个 env 文件」都不判断
//   - **错误文本里绝不出现文件内容**：env 的值常是凭据，错误会进日志与响应体
//   - 不碰配置映射（那是 Resolver 与 config 的事）
//   - 不做删除与改名：改名会让配置里的映射静默指空（见 spec §1.1）
package envfile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxFileSize 是单个 env 文件的大小上限（64 KiB），与 Parse 的判据同源。
const MaxFileSize = maxEnvFileSize

var (
	// ErrBadName 表示文件名不是「纯文件名」，调用方应答 400。
	ErrBadName = errors.New("env 文件名非法")
	// ErrTooLarge 表示正文超过 MaxFileSize，调用方应答 400。
	ErrTooLarge = errors.New("env 文件超过大小上限")
	// ErrExists 表示新建时同名文件已存在，调用方应答 409。
	ErrExists = errors.New("同名 env 文件已存在")
	// ErrBaseMismatch 表示前置哈希与磁盘现状不符，调用方应答 409 并回带现状。
	ErrBaseMismatch = errors.New("env 文件已被改动")
)

// FileInfo 是 env 目录下的一个文件（不含正文）。
type FileInfo struct {
	Name   string
	Size   int64
	SHA256 string
}

// List 列举 env 目录下的全部普通文件，按名字升序。
//
// 参数：
//   - dir: env 目录，通常取 Dir(cfg.DataDir)
//
// 返回：
//   - 文件列表（含大小与哈希）；目录不存在时返回空切片与 nil
//
// 注意：
//   - **目录不存在不是错误**：<DataDir>/env 没有任何东西自动创建，首次打开
//     设置页时它本来就不存在，报错会把「还没建」画成「读不了」
//   - 子目录与非普通文件跳过：env 文件只有一层，不递归
func List(dir string) ([]FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []FileInfo{}, nil
		}
		return nil, fmt.Errorf("读取 env 目录 %s: %w", dir, err)
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("读取 env 文件 %s: %w", e.Name(), err)
		}
		out = append(out, FileInfo{Name: e.Name(), Size: int64(len(data)), SHA256: hashOf(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Read 读一个 env 文件的正文。
//
// 返回：
//   - 正文、sha256、字节数；文件不存在时错误可用 errors.Is(err, fs.ErrNotExist) 判定
//
// 注意：返回的正文**含值**。调用方只应在用户显式要求「编辑正文」时把它交出去；
// 默认视图走 Parse + 丢值的路径（见 agentd 的 keys 端点）。
func Read(dir, name string) (content, sha string, size int64, err error) {
	path, err := resolvePath(dir, name)
	if err != nil {
		return "", "", 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", 0, fmt.Errorf("读取 env 文件 %s: %w", path, err)
	}
	return string(data), hashOf(data), int64(len(data)), nil
}

// Write 写一个 env 文件，带前置哈希保护。
//
// 参数：
//   - baseSHA: 空串 = 新建（目标必须不存在）；非空 = 覆盖（须与磁盘现状一致）
//
// 返回：
//   - 新内容的 sha256 与字节数；调用方可直接拿 sha 当下一次写入的 base
//   - 冲突时返回**磁盘现状的哈希** + ErrBaseMismatch，供 409 响应体带上现状
//
// 注意：
//   - **本函数不做语法校验**。调用方须在此之前跑 Parse——先校验再落盘，
//     写坏的文件不该进磁盘（写进去了才发现，症状会拖到下一次派发）
//   - 目录不存在时以 0700 创建；文件 0600——env 里带凭据是常态，权限基线
//     不能松于 DataDir 下其余内容
func Write(dir, name, content, baseSHA string) (sha string, size int64, err error) {
	path, err := resolvePath(dir, name)
	if err != nil {
		return "", 0, err
	}
	if len(content) > MaxFileSize {
		return "", 0, fmt.Errorf("%w: %s 有 %d 字节，上限 %d", ErrTooLarge, name, len(content), MaxFileSize)
	}
	cur, statErr := os.ReadFile(path)
	switch {
	case statErr == nil && baseSHA == "":
		// 新建撞名必须显式失败，避免保存按钮把别人的文件静默覆盖。
		return hashOf(cur), int64(len(cur)), fmt.Errorf("%w: %s", ErrExists, name)
	case statErr == nil && hashOf(cur) != baseSHA:
		return hashOf(cur), int64(len(cur)), fmt.Errorf("%w: %s", ErrBaseMismatch, name)
	case statErr != nil && !os.IsNotExist(statErr):
		return "", 0, fmt.Errorf("读取 env 文件 %s: %w", path, statErr)
	case statErr != nil && baseSHA != "":
		// 带 base 却读不到：文件在编辑期间被删了，与哈希不符同属冲突语义。
		return "", 0, fmt.Errorf("读取 env 文件 %s: %w", path, statErr)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("创建 env 目录 %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", 0, fmt.Errorf("写入 env 文件 %s: %w", path, err)
	}
	return hashOf(content), int64(len(content)), nil
}

// resolvePath 把配置里的文件名换算为绝对路径，并拒绝一切非「纯文件名」的写法。
//
// 为什么只收纯文件名：一杜绝路径穿越（../../etc 之类），二保证 env 文件只有
// 一个家、不会散落各处——运维找配置时只需要看一个目录。
func resolvePath(dir, name string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("%w: %q 不能含路径分隔符：只支持 %s 下的纯文件名", ErrBadName, name, dir)
	}
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("%w: %q：只支持 %s 下的纯文件名", ErrBadName, name, dir)
	}
	return filepath.Join(dir, name), nil
}

// hashOf 返回内容的 sha256 十六进制串（写入与列举共用，保证两处口径一致）。
func hashOf[T string | []byte](data T) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 5: 跑测试确认它通过**

Run: `go test ./internal/envfile/ -count=1`
Expected: PASS（含既有的 `envfile_test.go` / `resolver_test.go`，不得回归）。

- [ ] **Step 6: 加关键节点日志**

**本 task 刻意不加日志**——`files.go` 是纯文件操作层，与 `internal/discipline/files.go`、`internal/store` 同一条纪律：错误一律 `%w` 带上下文（路径、文件名、字节数）向上抛，日志由 agentd 的 handler 层统一打（Task 5–8 会打）。

自检：本层每个 error 返回点都带上了「哪个目录、哪个文件名」的上下文，且**没有任何错误文本包含文件内容**。

- [ ] **Step 7: 加注释**

已随 Step 4 写入：文件头「职责 + 边界」（明写三条边界：不打日志、不解析内容、错误不含内容）、五个导出符号的 doc 注释、`resolvePath` 的「为什么只收纯文件名」、`Write` 的「为什么不做语法校验」与「为什么 0600」。

- [ ] **Step 8: Commit**

```bash
git add internal/envfile/
git commit -m "feat(b158): envfile 补文件操作面（List/Read/Write + 包级纯文件名校验）"
```

---

### Task 2: `envfile.Resolver` 改吃活映射（热更新的地基）

**Files:**
- Modify: `internal/envfile/resolver.go`
- Modify: `internal/envfile/resolver_test.go`
- Modify: `internal/agentd/server.go`（新增 `EnvMapping()` 访问器）
- Modify: `internal/agentd/manager.go`（`NewManager` 多收一个取值函数）
- Modify: `cmd/agentd.go`（构造点接线）
- Modify: 所有 `NewManager` 的测试调用点（`grep -rln "NewManager(" --include='*_test.go' internal/`）

**Interfaces:**
- Consumes: Task 1 的包级 `resolvePath`
- Produces:
  - `envfile.NewResolver(dir string, mapping func() map[string]string, log *slog.Logger) *Resolver`（**第二参换类型**）
  - `envfile.Static(m map[string]string) func() map[string]string`
  - `(*agentd.Server).EnvMapping() map[string]string`
  - `agentd.NewManager(st, hub, ads, cfg, discMapping, envMapping func() map[string]string, approver, gate, log)`（**新增第 6 参 `envMapping`，插在 `discMapping` 之后**）

> **不改的后果是具体的**：控制台改完 env 映射要重启 agentd 才生效，而「重启 agent」在控制台里至今没实现。B157 已经拒绝过这个尾巴一次。

- [ ] **Step 1: 写失败测试**

在 `internal/envfile/resolver_test.go` 末尾追加：

```go
// TestResolverReadsLiveMapping 钉住热更新：映射函数返回值变了，For 立即反映，
// 不需要重建 Resolver。这是控制台「保存后下一个任务即生效」的地基。
func TestResolverReadsLiveMapping(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.env"), []byte("B=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := map[string]string{"opencode": "a.env"}
	r := envfile.NewResolver(dir, func() map[string]string { return m }, nil)

	got, err := r.For("opencode")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(got) != 1 || got[0] != "A=1" {
		t.Fatalf("got = %v，想要 [A=1]", got)
	}

	m = map[string]string{"opencode": "b.env"} // 模拟控制台改了配置
	got, err = r.For("opencode")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(got) != 1 || got[0] != "B=2" {
		t.Fatalf("换映射后 got = %v，想要 [B=2]（Resolver 必须每次取活映射）", got)
	}
}

// TestStaticWrapsFixedMapping 钉住 Static 助手：测试与不需要热更新的调用方用它。
func TestStaticWrapsFixedMapping(t *testing.T) {
	f := envfile.Static(map[string]string{"grok": "x.env"})
	if f()["grok"] != "x.env" {
		t.Fatalf("Static 没有原样透传映射")
	}
}
```

（若 `resolver_test.go` 尚未 import `os` / `path/filepath`，一并补上。）

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/envfile/ -run 'TestResolverReadsLiveMapping|TestStaticWrapsFixedMapping' -count=1`
Expected: 编译失败 —— `cannot use func literal ... as map[string]string` 与 `undefined: envfile.Static`。

- [ ] **Step 3: 改 `Resolver`**

`internal/envfile/resolver.go`：把结构体的 `m map[string]string` 换成 `mapping func() map[string]string`，改写构造函数并新增 `Static`：

```go
// Resolver 按 agent 名把配置里的文件名换算成可注入的环境变量。
//
// 无状态：每次 For 都重新取映射并重新读盘，因此配置改动与文件改动有同一种
// 时效——都在下一个任务生效，都不需要重启 agentd。
type Resolver struct {
	dir     string                   // env 文件目录
	mapping func() map[string]string // 取当前 agent 名 → 文件名映射
	log     *slog.Logger
}

// NewResolver 构造 Resolver。
//
// 参数：
//   - dir: env 文件目录，通常取 Dir(cfg.DataDir)
//   - mapping: 取当前映射的函数（生产上指向 agentd 的活配置）；nil 视为空映射，
//     此时所有 agent 都不注入
//   - log: 日志入口；nil 时退回 slog.Default()
//
// 注意：mapping 会在每次 For 时被调用，实现方必须是廉价且并发安全的
// （Server.EnvMapping 读的是 atomic 快照，满足这两条）。
func NewResolver(dir string, mapping func() map[string]string, log *slog.Logger) *Resolver {
	if log == nil {
		log = slog.Default()
	}
	if mapping == nil {
		log.Warn("env 映射取值函数为空，所有 agent 都不会注入环境变量", "dir", dir)
		mapping = func() map[string]string { return nil }
	}
	return &Resolver{dir: dir, mapping: mapping, log: log}
}

// Static 把一份固定映射包成取值函数，供测试与不需要热更新的调用方使用。
func Static(m map[string]string) func() map[string]string {
	return func() map[string]string { return m }
}
```

`For` 的第一行改为取活映射：

```go
	name := strings.TrimSpace(r.mapping()[agent])
```

`Preflight` 的遍历同理：

```go
	for agent := range r.mapping() {
```

- [ ] **Step 4: `Server` 加访问器**

`internal/agentd/server.go`，紧跟既有的 `DisciplineMapping` 之后：

```go
// EnvMapping 返回当前配置里的 agent 名 → env 文件名映射。
//
// 供 envfile.Resolver 每次派发时取活值：控制台改完映射不必重启 agentd。
// 返回的是配置快照里的 map 本体，**调用方不得修改**（写入一律走 swapConf）。
func (s *Server) EnvMapping() map[string]string { return s.conf().Env }
```

- [ ] **Step 5: `NewManager` 与构造点接线**

`internal/agentd/manager.go`：`NewManager` 签名在 `discMapping` 之后新增 `envMapping func() map[string]string`，doc 注释补一条：

```go
//   - envMapping: 取当前 env 文件映射的函数（生产上传 (*Server).EnvMapping）；
//     nil 时所有 agent 都不注入环境变量
```

函数体：

```go
	env := envfile.NewResolver(envfile.Dir(cfg.DataDir), envMapping, log)
```

并把结构体字面量里的 `env:` 改为 `env: env,`。

`cmd/agentd.go` 第 133 行附近改为：

```go
		// 启动预检用一份静态映射即可：预检是启动时的一次性体检，之后的活映射
		// 由 manager 侧的 resolver 承担（它拿的是 srv.EnvMapping）。
		envRes := envfile.NewResolver(envfile.Dir(cfg.DataDir), envfile.Static(cfg.Env), logger)
		envRes.Preflight()
```

`NewManager` 的调用点补上 `srv.EnvMapping`（紧跟 `srv.DisciplineMapping` 之后）。

- [ ] **Step 6: 修所有测试调用点**

Run: `grep -rn "NewManager(" --include='*_test.go' internal/`
对每个命中，在 `discMapping` 实参之后插入一个 env 映射实参：不关心 env 的用例传 `nil`，关心的传 `envfile.Static(map[string]string{...})`。

- [ ] **Step 7: 跑测试确认它通过**

Run: `go build ./... && go test ./internal/envfile/ ./internal/agentd/ -count=1`
Expected: 全绿。

- [ ] **Step 8: 加关键节点日志**

- `NewResolver` 收到 nil mapping 时 `Warn`（已在 Step 3 写入），带 `dir`——这是「所有 agent 突然都不注入了」唯一的线索。
- `For` 既有的进入/成功/失败日志保持不变；确认成功日志仍只打 `keys` 不打值。

自检：`grep -n 'r.log' internal/envfile/resolver.go` 的每一条都不含 `kv.Value` / `out`。

- [ ] **Step 9: 加注释**

- `Resolver` 结构体注释改成「每次 For 都重新取映射并重新读盘」（已在 Step 3 写入）。
- `NewResolver` 的「mapping 必须廉价且并发安全」注意事项。
- `cmd/agentd.go` 里「预检用静态映射、活映射在 manager 侧」的为什么（已在 Step 5 写入）。

- [ ] **Step 10: Commit**

```bash
git add -A
git commit -m "feat(b158): envfile.Resolver 改吃活映射，env 配置改完下个任务即生效"
```

---

### Task 3: `swapConf` 深拷 `Env`

**Files:**
- Modify: `internal/agentd/server.go:249` 附近（`swapConf`）
- Modify: `internal/agentd/server_test.go`（或就近的 swapConf 测试文件）

**Interfaces:**
- Consumes: 无
- Produces: `swapConf` 的 mutate 回调里可安全改写 `c.Env`，不污染旧快照

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentd/server_test.go`：

```go
// TestSwapConfDeepCopiesEnv 钉住写时复制：改 Env 不得污染改之前取到的旧快照。
//
// 为什么这条要单独测：swapConf 用的是结构体浅拷 + 逐字段深拷，新增运行期
// 可变的 map 字段时**极容易漏掉一层**，而漏掉的症状是「并发读到半改状态」
// ——不会当场报错，只会在别处诡异。
func TestSwapConfDeepCopiesEnv(t *testing.T) {
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: t.TempDir(),
		Env: map[string]string{"opencode": "old.env"},
	}, discardLogger())
	path := filepath.Join(t.TempDir(), "config.yaml")
	env.srv.SetConfigPath(path)

	before := env.srv.conf().Env // 改动前取到的快照
	if err := env.srv.swapConf(func(c *config.Config) error {
		c.Env = map[string]string{"codex": "proxy.env"}
		return nil
	}); err != nil {
		t.Fatalf("swapConf: %v", err)
	}
	if before["opencode"] != "old.env" || len(before) != 1 {
		t.Fatalf("旧快照被污染：%v", before)
	}
	if got := env.srv.EnvMapping(); got["codex"] != "proxy.env" || len(got) != 1 {
		t.Fatalf("新快照 = %v，想要 {codex: proxy.env}", got)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestSwapConfDeepCopiesEnv -count=1`
Expected: FAIL —— 现有 `swapConf` 没有深拷 `Env`，`next.Env` 与 `old.Env` 同一个 map。

> 若因 mutate 里整个替换了 map 而侥幸通过：仍然要改。判据是「代码里有没有那一层深拷」，不是「这个用例红不红」——下一个改单键的调用方会踩到。

- [ ] **Step 3: 写实现**

`swapConf` 里在 `Discipline` 的深拷之后补：

```go
	next.Env = make(map[string]string, len(old.Env)+1)
	for k, v := range old.Env {
		next.Env[k] = v
	}
```

落盘成功后的日志补一个字段：

```go
	s.log.Info("配置已更新并落盘", "path", s.cfgPath,
		"targets", len(next.Targets), "discipline", len(next.Discipline), "env", len(next.Env))
```

- [ ] **Step 4: 跑测试确认它通过**

Run: `go test ./internal/agentd/ -run TestSwapConf -count=1`
Expected: PASS。

- [ ] **Step 5: 加关键节点日志**

已随 Step 3：落盘成功日志新增 `env` 计数。**不要打 env 的键名或值**——这条日志每次配置变更都会出现，计数足够回答「有没有生效」。

- [ ] **Step 6: 加注释**

`swapConf` 的深拷块上方那条既有注释「新增运行期可变字段时必须在此补一层深拷」保持不变——它正是为这一刻写的；在 `next.Env` 这一层补一行：

```go
	// Env 与 Discipline 同为运行期可写的映射（B158 起可从控制台改），必须深拷。
```

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/
git commit -m "fix(b158): swapConf 深拷 Env，避免写时复制漏一层"
```

---
### Task 4: 契约层（proto 类型 + fixture + TS 类型）

**Files:**
- Create: `internal/proto/env.go`
- Modify: `internal/proto/contract_fixture_test.go`
- Create: `web/src/api/testdata/EnvResp.json`、`EnvKeysResp.json`、`EnvMappingReq.json`（由 `-update` 生成，不手写）
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/contract.test.ts`

**Interfaces:**
- Consumes: 无
- Produces:
  - `proto.EnvResp{Dir string; Files []EnvFile; Bindings []EnvBinding}`
  - `proto.EnvFile{Name string; Size int64; SHA256 string}`
  - `proto.EnvBinding{Executor string; Mode string; File string}`
  - `proto.EnvKey{Key string; ValueBytes int; Duplicate bool}`
  - `proto.EnvKeysResp{Keys []EnvKey}`
  - `proto.EnvMappingReq{Bindings []EnvBinding}`
  - `proto.EnvModeFile = "file"` / `proto.EnvModeOff = "off"`
  - TS: `EnvResp` / `EnvFile` / `EnvBinding` / `EnvKey` / `EnvKeysResp` / `EnvMappingReq`

- [ ] **Step 1: 写 Go 结构**

创建 `internal/proto/env.go`：

```go
// env.go —— 控制台配置 env 文件的线格式（B158）。
//
// 职责：GET /api/env、GET /api/env/file/keys、PUT /api/env/mapping 的请求/响应结构。
//
// 边界：
//   - 文件正文的读写复用 FileRead / FileWriteReq / FileWriteResp / FileConflictResp，
//     不另造一套——那与工作树在线编辑是同一件事的同一形状
//   - **本文件里没有任何字段承载 env 的值**：默认视图只交出 key 名与值长度，
//     全文只走 FileRead（且只在用户点「编辑正文」时）。这条是 spec §7 的凭据边界
//   - 与 DisciplineResp 同构，少了 Builtins 一节——env 没有内置默认
package proto

// EnvResp 是 GET /api/env 的响应：一次给全配置面要用的三样东西。
//
// 为什么一次给全：Env 分区要文件列表，开发机详情要 executor 档位 + 可选文件名，
// 同一份数据喂两处界面，不做两套接口。文件正文与变量清单都**不在这里**（按需单读）。
type EnvResp struct {
	Dir      string       `json:"dir"`      // <DataDir>/env 绝对路径，界面照原样显示
	Files    []EnvFile    `json:"files"`    // 该机 env 目录下的文件（不含正文）
	Bindings []EnvBinding `json:"bindings"` // 该机每个 executor 的当前档位
}

// EnvFile 是 env 目录下的一个文件。Size 是磁盘真实大小。
type EnvFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// EnvBinding 是一个 executor 的当前档位。**只有两档**：
//
//   - "off"：配置里**没有这个键** → 启动时不注入任何环境变量
//   - "file"：用 File 指定的文件
//
// 注意与 DisciplineBinding 的**错位**：discipline 的「键不存在」是「用内置默认」、
// 「空串」才是关闭；env 没有内置默认，「键不存在」就是唯一的关闭表达。落盘时
// **绝不写空串**——空串会让 Resolver 走到「读 <dir>/」这种无意义路径。
type EnvBinding struct {
	Executor string `json:"executor"`
	Mode     string `json:"mode"`
	File     string `json:"file,omitempty"`
}

// env 档位取值。与 config 的两档语义一一对应（键不存在 / 值为文件名）。
const (
	EnvModeFile = "file"
	EnvModeOff  = "off"
)

// EnvKey 是解析出的一个变量。**永不含值**——这是本设计的凭据边界所在。
//
// ValueBytes 是值的字节长度，口径是**展开后**（Parse 的产物）：它让「这个变量
// 是不是空的」可判断，而不泄露内容。注意展开用 lookup=nil，所以引用了外部变量
// 的值在这里会显示为更短甚至 0——这不是 bug，是刻意不查 agentd 自己的环境，
// 否则同一个文件在不同机器上会显示出不同的长度，既误导又多泄露一层信息。
//
// Duplicate 为真表示该键在文件里出现过多次（Resolver 的既有行为是 WARN 不拒，
// 界面照此标注、不拦保存）。
//
// **刻意没有「是否单引号字面量」这一项**：Parse 只回 Key/Value，不暴露引号风格，
// 要标它就得在 handler 里重扫一遍原始行、再造一套与 Parse 可能漂移的解析。
type EnvKey struct {
	Key        string `json:"key"`
	ValueBytes int    `json:"value_bytes"`
	Duplicate  bool   `json:"duplicate,omitempty"`
}

// EnvKeysResp 是 GET /api/env/file/keys 的响应。
type EnvKeysResp struct {
	Keys []EnvKey `json:"keys"`
}

// EnvMappingReq 是 PUT /api/env/mapping 的请求体：**整段替换**。
//
// 为什么整段替换而不是逐项 patch：界面就是整块保存，整段替换让「界面所见 =
// 落盘所得」无需推理。这条成立的前提是 GET 返回的 Bindings 是全集（注册的
// adapter ∪ 配置里的键），若日后有只送部分键的写入方，本语义必须重新审视。
type EnvMappingReq struct {
	Bindings []EnvBinding `json:"bindings"`
}
```

- [ ] **Step 2: 加 fixture 样本**

`internal/proto/contract_fixture_test.go` 的 `cases` 末尾追加三行：

```go
		{"EnvResp", envRespSample()},
		{"EnvKeysResp", envKeysRespSample()},
		{"EnvMappingReq", envMappingReqSample()},
```

并在文件末尾追加三个样本函数：

```go
// envRespSample 返回 EnvResp 的代表性样本：两档各出现一次（off / file），
// 并含一个未注册但配置里有的 executor。
func envRespSample() EnvResp {
	return EnvResp{
		Dir: "/Users/dev/.handoff/env",
		Files: []EnvFile{
			{Name: "proxy.env", Size: 64,
				SHA256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
		},
		Bindings: []EnvBinding{
			{Executor: "codex", Mode: "file", File: "proxy.env"},
			{Executor: "opencode", Mode: "off"},
		},
	}
}

// envKeysRespSample 返回 EnvKeysResp 的代表性样本：一条普通、一条重复、
// 一条空值（ValueBytes=0，omitempty 不适用于它，必须仍然出现在 JSON 里）。
func envKeysRespSample() EnvKeysResp {
	return EnvKeysResp{Keys: []EnvKey{
		{Key: "HTTPS_PROXY", ValueBytes: 34},
		{Key: "GOPROXY", ValueBytes: 21, Duplicate: true},
		{Key: "EMPTY_ONE", ValueBytes: 0},
	}}
}

// envMappingReqSample 返回 EnvMappingReq 的代表性样本。
func envMappingReqSample() EnvMappingReq {
	return EnvMappingReq{Bindings: []EnvBinding{
		{Executor: "codex", Mode: "file", File: "proxy.env"},
		{Executor: "opencode", Mode: "off"},
	}}
}
```

- [ ] **Step 3: 跑测试确认它失败**

Run: `go test ./internal/proto/ -run TestContractFixtures -count=1`
Expected: FAIL —— 三个 fixture 文件不存在。

- [ ] **Step 4: 生成 fixture**

Run: `go test ./internal/proto/ -run TestContractFixtures -update -count=1 && go test ./internal/proto/ -count=1`
Expected: 第二次 PASS；`git status` 里出现三个新 JSON。

**逐字检查生成的 `EnvKeysResp.json`**：`value_bytes` 必须是数字，`EMPTY_ONE` 的那条必须**仍然带 `"value_bytes": 0`**（int 无 omitempty），且**整个文件里不出现任何看起来像值的字符串**。

- [ ] **Step 5: 同步 TS 类型**

`web/src/api/types.ts` 在 discipline 那组之后追加：

```ts
// EnvFile 是 env 目录下的一个文件（不含正文，正文按需单读）。
export interface EnvFile {
  name: string
  size: number
  sha256: string
}

// EnvBinding 是一个 executor 的当前档位。**只有两档**：
//   - 'off'：配置里没有这个键，启动时不注入任何环境变量
//   - 'file'：用 file 指定的文件
// 与 DisciplineBinding 的三档是**错位**的，不要照抄翻译。
export interface EnvBinding {
  executor: string
  mode: 'off' | 'file'
  file?: string
}

// EnvResp 是 GET /api/env 的响应。
export interface EnvResp {
  dir: string
  files: EnvFile[]
  bindings: EnvBinding[]
}

// EnvKey 是解析出的一个变量。**永不含值**——只有 key 名与值的字节长度。
export interface EnvKey {
  key: string
  value_bytes: number
  duplicate?: boolean
}

// EnvKeysResp 是 GET /api/env/file/keys 的响应。
export interface EnvKeysResp {
  keys: EnvKey[]
}

// EnvMappingReq 是 PUT /api/env/mapping 的请求体：整段替换。
export interface EnvMappingReq {
  bindings: EnvBinding[]
}
```

- [ ] **Step 6: 加契约测试**

`web/src/api/contract.test.ts` 顶部按既有写法 import 三个 fixture，末尾追加：

```ts
describe('Env 文件契约', () => {
  it('EnvResp 两档都在线格式里，off 档不带 file 键', () => {
    const resp = envRespFixture as EnvResp
    expect(resp.bindings.map((b) => b.mode).sort()).toEqual(['file', 'off'])
    const off = resp.bindings.find((b) => b.mode === 'off')!
    expect(off.file).toBeUndefined()
    const file = resp.bindings.find((b) => b.mode === 'file')!
    expect(file.file).toBe('proxy.env')
    // env 没有内置默认：响应里不得出现 builtins/default_tier 这类 discipline 概念
    expect('builtins' in envRespFixture).toBe(false)
    expect('default_tier' in (off as object)).toBe(false)
  })

  it('EnvKeysResp：只有 key 名与值长度，值不在线格式里', () => {
    const resp = envKeysFixture as EnvKeysResp
    expect(resp.keys.map((k) => k.key)).toEqual(['HTTPS_PROXY', 'GOPROXY', 'EMPTY_ONE'])
    // 值为空的那条也必须带 value_bytes: 0（int 无 omitempty），否则界面判不出「空值」
    expect(resp.keys[2].value_bytes).toBe(0)
    expect(resp.keys[1].duplicate).toBe(true)
    // 结构性判据：整份 fixture 里没有任何名为 value/content 的键
    const raw = JSON.stringify(envKeysFixture)
    expect(raw).not.toMatch(/"value"|"content"/)
  })

  it('EnvMappingReq：整段替换，两条 binding', () => {
    const req = envMappingReqFixture as EnvMappingReq
    expect(req.bindings).toHaveLength(2)
  })
})
```

- [ ] **Step 7: 跑测试确认它通过**

Run: `cd web && npx vitest run src/api/contract.test.ts && npx tsc -b`
Expected: 全绿、0 类型错误。

- [ ] **Step 8: 加关键节点日志**

**本 task 不加日志**——`internal/proto` 是纯线格式定义，没有任何运行时分支。自检：本 task 没有引入任何函数体。

- [ ] **Step 9: 加注释**

已随 Step 1/Step 5：文件头写明「本文件里没有任何字段承载 env 的值」这条边界；`EnvBinding` 与 `EnvKey` 各自写明与 discipline 的错位、`lookup=nil` 的口径、为什么没有引号风格标记。

- [ ] **Step 10: Commit**

```bash
git add internal/proto/ web/src/api/
git commit -m "feat(b158): env 配置面的线格式与契约 fixture"
```

---

### Task 5: `GET /api/env`

**Files:**
- Create: `internal/agentd/env.go`
- Create: `internal/agentd/env_test.go`
- Modify: `internal/agentd/server.go`（注册路由 + 路由表注释）

**Interfaces:**
- Consumes: Task 1 的 `envfile.List`、Task 4 的 `proto.EnvResp`
- Produces:
  - `(*Server).handleEnvGet(w http.ResponseWriter, r *http.Request)`
  - `(*Server).envBindings() []proto.EnvBinding`

- [ ] **Step 1: 写失败测试**

创建 `internal/agentd/env_test.go`：

```go
// env_test.go —— env 配置端点的测试（白盒包：要直接看 manager 的 resolver）。
package agentd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newEnvEnv 构造带 DataDir、env 映射与若干已注册 executor 的白盒环境，
// 返回环境与该机的 env 目录路径（目录本身不预先创建——「还没建」是必测的一档）。
func newEnvEnv(t *testing.T, mapping map[string]string, execs ...string) (*testAgentdEnv, string) {
	t.Helper()
	dataDir := t.TempDir()
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: dataDir, Env: mapping,
	}, discardLogger())
	ads := map[string]executor.Adapter{}
	for _, n := range execs {
		ads[n] = &failStartAdapter{} // 只需要名字进注册表，本组用例不启动任何 executor
	}
	mgr := NewManager(env.st, env.srv.Hub(), ads, env.srv.conf(),
		env.srv.DisciplineMapping, env.srv.EnvMapping, nil, newTestGate(t), discardLogger())
	env.srv.SetManager(mgr)
	env.mgr = mgr
	return env, filepath.Join(dataDir, "env")
}

func TestEnvGetListsFilesAndBindings(t *testing.T) {
	// 配置里放一个当前没注册的 executor 名（ghost）：它必须仍然出现在 bindings 里，
	// 否则界面看不见它、而它还在配置里生效
	env, envDir := newEnvEnv(t,
		map[string]string{"codex": "proxy.env", "ghost": "ghost.env"}, "opencode", "codex")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "proxy.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var resp proto.EnvResp
	if code := env.getJSON(t, "/api/env", &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if resp.Dir != envDir {
		t.Fatalf("dir = %q, want %q", resp.Dir, envDir)
	}
	if len(resp.Files) != 1 || resp.Files[0].Name != "proxy.env" || resp.Files[0].Size != 4 {
		t.Fatalf("files = %+v", resp.Files)
	}
	got := map[string]proto.EnvBinding{}
	for _, b := range resp.Bindings {
		got[b.Executor] = b
	}
	if len(got) != 3 {
		t.Fatalf("bindings = %+v，想要 codex/ghost/opencode 三条（注册 ∪ 配置的并集）", resp.Bindings)
	}
	if got["codex"].Mode != proto.EnvModeFile || got["codex"].File != "proxy.env" {
		t.Fatalf("codex = %+v，想要 file/proxy.env", got["codex"])
	}
	if got["opencode"].Mode != proto.EnvModeOff || got["opencode"].File != "" {
		t.Fatalf("opencode = %+v，想要 off 且不带 file（配置里没这个键）", got["opencode"])
	}
	if got["ghost"].Mode != proto.EnvModeFile {
		t.Fatalf("ghost = %+v，想要 file（配置里有键，虽然 adapter 没注册）", got["ghost"])
	}
	// 排序稳定：界面每次刷新不该跳行
	names := []string{}
	for _, b := range resp.Bindings {
		names = append(names, b.Executor)
	}
	if strings.Join(names, ",") != "codex,ghost,opencode" {
		t.Fatalf("顺序 = %v，想要按名字升序", names)
	}
}

func TestEnvGetWhenDirMissing(t *testing.T) {
	// 目录还没建是常态，不是错误：必须 200 + 空列表
	env, envDir := newEnvEnv(t, nil, "opencode")
	var resp proto.EnvResp
	if code := env.getJSON(t, "/api/env", &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if resp.Dir != envDir || len(resp.Files) != 0 {
		t.Fatalf("resp = %+v，想要 dir 有值、files 空", resp)
	}
	if len(resp.Bindings) != 1 || resp.Bindings[0].Mode != proto.EnvModeOff {
		t.Fatalf("bindings = %+v，想要 opencode/off", resp.Bindings)
	}
}

func TestEnvGetWithoutManagerIs503(t *testing.T) {
	// executor 名单来自 manager；manager 未就绪时不能装作「一个 executor 都没有」
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token: testToken, DataDir: t.TempDir(),
	}, discardLogger())
	var body map[string]string
	if code := env.getJSON(t, "/api/env", &body); code != 503 {
		t.Fatalf("code = %d, want 503", code)
	}
	if !strings.Contains(body["error"], "manager") {
		t.Fatalf("error = %q，想要提到 manager", body["error"])
	}
}

// 断言 JSON 里没有出现被测样本的值。整个 env 组共用。
func assertNoSecret(t *testing.T, raw []byte, secret string) {
	t.Helper()
	if strings.Contains(string(raw), secret) {
		t.Fatalf("响应体里出现了 env 的值 %q：%s", secret, raw)
	}
}
```

> 若 `testAgentdEnv` 上还没有 `getJSON` 助手，照 `discipline_test.go` 里 `putJSON` 的写法补一个：带 `Authorization: Bearer` 的 GET，返回状态码并解码到 out。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestEnvGet -count=1`
Expected: FAIL —— 路由不存在，返回 404。

- [ ] **Step 3: 写实现**

创建 `internal/agentd/env.go`（先只放 GET，后续 task 往同一文件追加）：

```go
// env.go —— 控制台的 env 文件配置 HTTP 面（B158）。
//
// 职责：
//   - GET  /api/env                列出该机 env 文件与每个 executor 的档位
//   - GET  /api/env/file/keys      解析出的变量清单（**只有 key 名与值长度**）
//   - GET  /api/env/file           读单个 env 文件正文（含值，仅编辑时调用）
//   - PUT  /api/env/file           写单个 env 文件（前置哈希 + **写前解析校验**）
//   - PUT  /api/env/mapping        整段替换该机的 env 配置段
//
// 边界：
//   - 文件判断力全在 internal/envfile（名字校验、大小上限、冲突判定），本层
//     只做 HTTP 编解码与错误映射，**中文错误原文原样透传**
//   - 跨机由 forwardIfRequested 处理（?machine=），本文件只管本机
//   - **任何路径都不得把 env 的值写进日志或响应**（正文读写端点除外——它就是
//     为编辑而存在的，见 spec §7 的诚实边界）
//   - 两档语义：键不存在 = 不注入；值为文件名 = 读该文件。**绝不写空串**
package agentd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/proto"
)

// handleEnvGet 处理 GET /api/env[?machine=]。
//
// 响应：
//   - 200 proto.EnvResp
//   - 503：manager 未就绪（与 dispatch 等路由同款：executor 名单来自 manager）
func (s *Server) handleEnvGet(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	s.log.Info("env 配置查询请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Warn("env 配置查询：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	dir := envfile.Dir(s.conf().DataDir)
	files, err := envfile.List(dir)
	if err != nil {
		s.log.Error("env 配置查询：列举文件失败", "dir", dir, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := proto.EnvResp{
		Dir:      dir,
		Files:    make([]proto.EnvFile, 0, len(files)),
		Bindings: s.envBindings(),
	}
	for _, f := range files {
		resp.Files = append(resp.Files, proto.EnvFile{Name: f.Name, Size: f.Size, SHA256: f.SHA256})
	}
	s.log.Info("env 配置查询完成", "dir", dir, "files", len(resp.Files), "bindings", len(resp.Bindings))
	writeJSON(w, http.StatusOK, resp)
}

// envBindings 把「已注册的 executor ∪ 配置里已出现的键」折成档位列表，按名字升序。
//
// **两档映射**：键不存在（或值 trim 后为空）→ off；否则 → file。
//
// 为什么空串也归到 off：历史配置里可能已经写着空串（手改 yaml 留下的），把它
// 显示成「指向一个名字为空的文件」只会让人困惑。**但保存时绝不写回空串**——
// 读宽写严，脏数据经过一次保存就被洗掉。
func (s *Server) envBindings() []proto.EnvBinding {
	m := s.conf().Env
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

	out := make([]proto.EnvBinding, 0, len(names))
	for _, n := range names {
		b := proto.EnvBinding{Executor: n}
		if v := strings.TrimSpace(m[n]); v == "" {
			b.Mode = proto.EnvModeOff
		} else {
			b.Mode, b.File = proto.EnvModeFile, v
		}
		out = append(out, b)
	}
	return out
}
```

- [ ] **Step 4: 注册路由**

`internal/agentd/server.go` 的 `Handler()` 里，紧跟 discipline 那组之后：

```go
	api.HandleFunc("GET /api/env", s.handleEnvGet)
```

并在 `Handler` 上方的路由表注释里补一行：

```go
//   - GET  /api/env                    env 文件列表与 executor 档位
```

- [ ] **Step 5: 跑测试确认它通过**

Run: `go test ./internal/agentd/ -run TestEnvGet -count=1`
Expected: PASS（三条全过）。

- [ ] **Step 6: 加关键节点日志**

已随 Step 3：进入日志（method/path）、manager 未就绪的 Warn、列举失败的 Error（带 dir + cause）、成功退出日志（dir + 文件数 + 档位数）。**不打文件名列表**——文件名不是秘密，但每次进设置页都刷一屏没有价值；数量足以回答「读到了没有」。

- [ ] **Step 7: 加注释**

已随 Step 3：文件头五条路由 + 四条边界（含「值不进日志」与「绝不写空串」）；`envBindings` 的「为什么读宽写严」。

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/
git commit -m "feat(b158): GET /api/env 列出 env 文件与 executor 档位"
```

---

### Task 6: `GET /api/env/file/keys`（值不出后端的那一屏）

**Files:**
- Modify: `internal/agentd/env.go`
- Modify: `internal/agentd/env_test.go`
- Modify: `internal/agentd/server.go`（路由 + 注释）

**Interfaces:**
- Consumes: `envfile.Read`、`envfile.Parse`、`proto.EnvKeysResp`
- Produces: `(*Server).handleEnvKeys(w http.ResponseWriter, r *http.Request)`

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentd/env_test.go`：

```go
func TestEnvKeysHidesValues(t *testing.T) {
	env, envDir := newEnvEnv(t, nil, "opencode")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "# 注释\nexport TOKEN=zzz-secret-zzz\nGOPROXY=https://proxy.example\nGOPROXY=https://mirror.example\nEMPTY_ONE=\n"
	if err := os.WriteFile(filepath.Join(envDir, "a.env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	raw, code := env.getRaw(t, "/api/env/file/keys?name=a.env")
	if code != 200 {
		t.Fatalf("code = %d, want 200: %s", code, raw)
	}
	// spec §6 的机器判据：响应体里不得出现任何值
	assertNoSecret(t, raw, "zzz-secret-zzz")
	assertNoSecret(t, raw, "proxy.example")

	var resp proto.EnvKeysResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("解码: %v", err)
	}
	if len(resp.Keys) != 3 {
		t.Fatalf("keys = %+v，想要 TOKEN/GOPROXY/EMPTY_ONE 三条（重复键只出现一次）", resp.Keys)
	}
	byKey := map[string]proto.EnvKey{}
	for _, k := range resp.Keys {
		byKey[k.Key] = k
	}
	if byKey["TOKEN"].ValueBytes != len("zzz-secret-zzz") {
		t.Fatalf("TOKEN.value_bytes = %d，想要 %d", byKey["TOKEN"].ValueBytes, len("zzz-secret-zzz"))
	}
	if !byKey["GOPROXY"].Duplicate {
		t.Fatalf("GOPROXY 应标记 duplicate（后者覆盖，位置留在首次出现处）")
	}
	if byKey["EMPTY_ONE"].ValueBytes != 0 {
		t.Fatalf("EMPTY_ONE.value_bytes = %d，想要 0", byKey["EMPTY_ONE"].ValueBytes)
	}
}

func TestEnvKeysDoesNotConsultProcessEnv(t *testing.T) {
	// lookup 传 nil：展开时不查 agentd 自己的环境，否则同一个文件在不同机器上
	// 会显示出不同的值长度，既误导又多泄露一层信息
	t.Setenv("B158_OUTER", "0123456789")
	env, envDir := newEnvEnv(t, nil, "opencode")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "a.env"), []byte("X=$B158_OUTER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var resp proto.EnvKeysResp
	if code := env.getJSON(t, "/api/env/file/keys?name=a.env", &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if len(resp.Keys) != 1 || resp.Keys[0].ValueBytes != 0 {
		t.Fatalf("keys = %+v，想要 X 且 value_bytes=0（外部变量不查）", resp.Keys)
	}
}

func TestEnvKeysErrors(t *testing.T) {
	env, envDir := newEnvEnv(t, nil, "opencode")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "bad.env"), []byte("1BAD=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	if code := env.getJSON(t, "/api/env/file/keys?name=../x", &body); code != 400 {
		t.Fatalf("穿越名 code = %d, want 400", code)
	}
	if code := env.getJSON(t, "/api/env/file/keys?name=gone.env", &body); code != 404 {
		t.Fatalf("不存在 code = %d, want 404", code)
	}
	if code := env.getJSON(t, "/api/env/file/keys?name=bad.env", &body); code != 400 {
		t.Fatalf("语法错 code = %d, want 400", code)
	}
	// Parse 的错误自带行号，必须原样透传——它是用户改对的唯一线索
	if !strings.Contains(body["error"], "第 1 行") && !strings.Contains(body["error"], "1BAD") {
		t.Fatalf("error = %q，想要带行号或原行", body["error"])
	}
}
```

> 本 task 的用例开始用 `json.Unmarshal`，在 `env_test.go` 的 import 里补 `"encoding/json"`。
>
> `getRaw` 助手：带 token 的 GET，返回原始响应字节与状态码。`w3a_testhelpers_test.go` 里目前只有 `getJSON`（直接解码），照它的写法在同一文件里补一个 `getRaw`——「断言响应体不含某子串」必须看**原始字节**，解码后再判等于放过了未来新增字段。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestEnvKeys -count=1`
Expected: FAIL —— 路由不存在，404。

- [ ] **Step 3: 写实现**

追加到 `internal/agentd/env.go`：

```go
// handleEnvKeys 处理 GET /api/env/file/keys?name=[&machine=]。
//
// 响应：200 proto.EnvKeysResp / 400 名字非法或**语法错误** / 404 文件不存在。
//
// 这是 Env 分区的默认视图，也是 spec §7 凭据边界的落点：**响应结构里没有
// 任何字段承载值**，只有 key 名、值的字节长度与重复标记。日常最高频的问题
// 是「这台机给某个 executor 注了哪些变量」，回答它不需要看见任何一个值。
//
// 注意：Parse 的 lookup 传 **nil**。展开时不查 agentd 自己的环境变量——否则
// 同一个文件在不同机器上会显示出不同的值长度，既误导又多泄露一层信息。
// 引用了外部变量的值因此会显示为 0，这是刻意的，不是 bug。
func (s *Server) handleEnvKeys(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	dir := envfile.Dir(s.conf().DataDir)
	s.log.Info("env 变量清单请求", "dir", dir, "name", name)

	content, _, size, err := envfile.Read(dir, name)
	if err != nil {
		s.writeEnvReadError(w, dir, name, err)
		return
	}
	kvs, dups, err := envfile.Parse(bytes.NewReader([]byte(content)), nil)
	if err != nil {
		// 原样透传：Parse 的错误自带行号与原行，是用户改对的唯一线索。
		// **错误文本里可能含原行**——但那一行本来就是用户自己写的、且正要在
		// 编辑器里被改，与「默认视图不显示值」不矛盾（见 spec §7）。
		s.log.Warn("env 变量清单：解析失败", "dir", dir, "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	dupSet := make(map[string]bool, len(dups))
	for _, k := range dups {
		dupSet[k] = true
	}
	resp := proto.EnvKeysResp{Keys: make([]proto.EnvKey, 0, len(kvs))}
	for _, kv := range kvs {
		resp.Keys = append(resp.Keys, proto.EnvKey{
			Key: kv.Key, ValueBytes: len(kv.Value), Duplicate: dupSet[kv.Key],
		})
	}
	// 日志只记数量与字节数：key 名不是秘密，但一屏几十个没价值；值绝不出现。
	s.log.Info("env 变量清单完成", "dir", dir, "name", name,
		"keys", len(resp.Keys), "dups", len(dups), "bytes", size)
	writeJSON(w, http.StatusOK, resp)
}

// writeEnvReadError 把 envfile.Read 的错误映射成 HTTP 响应（三处读路径共用）。
//
// 为什么抽出来：keys / file 两个 GET 与 mapping 的存在性校验对同一组错误要给
// 同一组状态码，散在三处必然漂移。
func (s *Server) writeEnvReadError(w http.ResponseWriter, dir, name string, err error) {
	switch {
	case errors.Is(err, envfile.ErrBadName):
		s.log.Warn("env 读文件被拒：名字非法", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, fs.ErrNotExist):
		s.log.Warn("env 读文件：目标不存在", "dir", dir, "name", name, "cause", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "env 文件不存在"})
	default:
		s.log.Error("env 读文件失败", "dir", dir, "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取 env 文件失败"})
	}
}
```

- [ ] **Step 4: 注册路由**

```go
	api.HandleFunc("GET /api/env/file/keys", s.handleEnvKeys)
```

路由表注释补：`//   - GET  /api/env/file/keys          env 文件的变量清单（不含值）`

> **注册顺序无关，但路径必须比 `/api/env/file` 更长更具体**——Go 1.22 的方法路由按最具体匹配，`/api/env/file/keys` 与 `/api/env/file` 是两条不同的精确路径，不会互相吞。

- [ ] **Step 5: 跑测试确认它通过**

Run: `go test ./internal/agentd/ -run TestEnv -count=1`
Expected: PASS。

- [ ] **Step 6: 加关键节点日志**

已随 Step 3：进入（dir/name）、解析失败 Warn（带 cause）、读错误三分支各自的 Warn/Error、成功退出（keys 数 / dups 数 / 字节数）。

自检：`grep -n 's.log' internal/agentd/env.go` 的每一条参数里都没有 `content` / `kv.Value`。

- [ ] **Step 7: 加注释**

已随 Step 3：`handleEnvKeys` 的凭据边界说明与 `lookup=nil` 的为什么；解析错误分支里「原行可能出现在错误文本里，且为什么这不矛盾」；`writeEnvReadError` 的「为什么抽出来」。

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/
git commit -m "feat(b158): GET /api/env/file/keys 只交出 key 名与值长度"
```

---

### Task 7: `GET/PUT /api/env/file`（含写前解析校验）

**Files:**
- Modify: `internal/agentd/env.go`
- Modify: `internal/agentd/env_test.go`
- Modify: `internal/agentd/server.go`（路由 + 注释）

**Interfaces:**
- Consumes: `envfile.Read/Write/Parse`、`proto.FileRead/FileWriteReq/FileWriteResp/FileConflictResp`
- Produces: `(*Server).handleEnvFileRead`、`(*Server).handleEnvFileWrite`

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentd/env_test.go`：

```go
func TestEnvFileReadReturnsFullText(t *testing.T) {
	// 与 keys 端点相反：这条**就是**为编辑而存在的，全文含值必须原样交出
	env, envDir := newEnvEnv(t, nil, "opencode")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "a.env"), []byte("TOKEN=zzz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got proto.FileRead
	if code := env.getJSON(t, "/api/env/file?name=a.env", &got); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	if got.Content != "TOKEN=zzz\n" || got.Size != 10 || got.SHA256 == "" {
		t.Fatalf("got = %+v", got)
	}
}

func TestEnvFileWriteRejectsBadSyntax(t *testing.T) {
	// 差异二：env 比纪律块多一道解析门。写坏的文件不该进磁盘——写进去了才发现，
	// 症状会拖到下一次派发（「代理配了但连不上」离根因十万八千里）
	env, envDir := newEnvEnv(t, nil, "opencode")
	var body map[string]string
	code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "1BAD=x\n", BaseSHA256: ""}, &body)
	if code != 400 {
		t.Fatalf("code = %d, want 400", code)
	}
	if _, err := os.Stat(filepath.Join(envDir, "a.env")); !os.IsNotExist(err) {
		t.Fatalf("语法错的文件不该落盘")
	}
	if body["error"] == "" {
		t.Fatalf("必须透传 Parse 的错误原文")
	}
}

func TestEnvFileWriteCreateAndOverwrite(t *testing.T) {
	env, envDir := newEnvEnv(t, nil, "opencode")
	var created proto.FileWriteResp
	if code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "A=1\n", BaseSHA256: ""}, &created); code != 200 {
		t.Fatalf("新建 code = %d, want 200", code)
	}
	if created.SHA256 == "" || created.Size != 4 {
		t.Fatalf("created = %+v", created)
	}
	// 撞名新建 409
	var conflict map[string]string
	if code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "B=2\n", BaseSHA256: ""}, &conflict); code != 409 {
		t.Fatalf("撞名 code = %d, want 409", code)
	}
	// 陈旧 base 409 且带磁盘现状
	var stale proto.FileConflictResp
	if code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "B=2\n", BaseSHA256: "deadbeef"}, &stale); code != 409 {
		t.Fatalf("陈旧 base code = %d, want 409", code)
	}
	if stale.Current.Content != "A=1\n" {
		t.Fatalf("409 体必须带磁盘现状，got = %+v", stale.Current)
	}
	// 正确 base 覆盖成功
	var ok proto.FileWriteResp
	if code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "B=2\n", BaseSHA256: created.SHA256}, &ok); code != 200 {
		t.Fatalf("覆盖 code = %d, want 200", code)
	}
	data, err := os.ReadFile(filepath.Join(envDir, "a.env"))
	if err != nil || string(data) != "B=2\n" {
		t.Fatalf("落盘 = %q, err = %v", data, err)
	}
}

func TestEnvFileWriteAllowsDuplicateKeys(t *testing.T) {
	// 重复键不拦：Resolver 既有行为是 WARN + 后者覆盖。拦它等于在控制台里
	// 发明一条 agentd 不认的规则
	env, _ := newEnvEnv(t, nil, "opencode")
	var resp proto.FileWriteResp
	if code := env.putJSON(t, "/api/env/file?name=a.env",
		proto.FileWriteReq{Content: "A=1\nA=2\n", BaseSHA256: ""}, &resp); code != 200 {
		t.Fatalf("code = %d, want 200（重复键只标注不拦）", code)
	}
}

func TestEnvFileWriteRejectsBadNameAndTooLarge(t *testing.T) {
	env, _ := newEnvEnv(t, nil, "opencode")
	var body map[string]string
	if code := env.putJSON(t, "/api/env/file?name=../x",
		proto.FileWriteReq{Content: "A=1\n"}, &body); code != 400 {
		t.Fatalf("穿越名 code = %d, want 400", code)
	}
	big := strings.Repeat("A=1\n", envfile.MaxFileSize/4+1)
	if code := env.putJSON(t, "/api/env/file?name=big.env",
		proto.FileWriteReq{Content: big}, &body); code != 400 {
		t.Fatalf("超限 code = %d, want 400", code)
	}
}
```

（在 import 里补 `"github.com/Xsxdot/handoff/internal/envfile"`。）

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestEnvFile -count=1`
Expected: FAIL —— 404。

- [ ] **Step 3: 写实现**

追加到 `internal/agentd/env.go`：

```go
// handleEnvFileRead 处理 GET /api/env/file?name=[&machine=]。
//
// 响应：200 proto.FileRead / 400 名字非法 / 404 文件不存在。
//
// **这条会把含值的全文交给浏览器**，且这是刻意的——不然没法编辑。默认视图
// 走 keys 端点；界面只在用户点「编辑正文」时调这条。spec §7 已写明这条边界：
// 掩码防的是肩窥、截图、录屏、整页粘贴，不是防浏览器本身，更不是加密。
func (s *Server) handleEnvFileRead(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	dir := envfile.Dir(s.conf().DataDir)
	s.log.Info("env 读文件请求（含值全文）", "dir", dir, "name", name)

	content, sha, size, err := envfile.Read(dir, name)
	if err != nil {
		s.writeEnvReadError(w, dir, name, err)
		return
	}
	s.log.Info("env 读文件完成", "dir", dir, "name", name, "bytes", size, "sha256", shortHash(sha))
	writeJSON(w, http.StatusOK, proto.FileRead{Content: content, Size: size, SHA256: sha})
}

// handleEnvFileWrite 处理 PUT /api/env/file?name=[&machine=]。
//
// 请求体 proto.FileWriteReq：base_sha256 为空串 = 新建（目标必须不存在）。
//
// 响应：200 FileWriteResp / 400 名字非法、超限或**语法错误** / 409 撞名或冲突
//
//	（带磁盘现状）/ 404 目标在编辑期间被删。
//
// **写前必须解析**（与纪律块的唯一实质差异）：纪律块写错了模型顶多读到一段
// 怪话；env 写错了，症状是「代理配了但连不上」「go test 突然全红」，离根因
// 十万八千里。Parse 已经能产出带行号的错误，白不用。
//
// 重复键**不拦**：Resolver 的既有行为是 WARN + 后者覆盖，界面照此标注即可。
func (s *Server) handleEnvFileWrite(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	dir := envfile.Dir(s.conf().DataDir)

	var req proto.FileWriteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("env 写文件请求体解析失败", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	// 正文含值，日志只记长度与前置哈希，绝不记内容。
	s.log.Info("env 写文件请求", "dir", dir, "name", name,
		"bytes", len(req.Content), "base", shortHash(req.BaseSHA256))

	// 语法门在落盘之前：写坏的文件不该进磁盘。
	if _, _, perr := envfile.Parse(bytes.NewReader([]byte(req.Content)), nil); perr != nil {
		s.log.Warn("env 写文件被拒：语法错误", "dir", dir, "name", name, "cause", perr)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": perr.Error()})
		return
	}

	sha, size, err := envfile.Write(dir, name, req.Content, req.BaseSHA256)
	if err != nil {
		switch {
		case errors.Is(err, envfile.ErrBadName), errors.Is(err, envfile.ErrTooLarge):
			s.log.Warn("env 写文件被拒", "name", name, "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, envfile.ErrExists):
			s.log.Warn("env 写文件被拒：撞名", "dir", dir, "name", name, "cause", err)
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, envfile.ErrBaseMismatch):
			// 409 的 body 带磁盘现状：界面据此提供「重新加载」，绝不静默覆盖。
			cur, curSHA, curSize, rerr := envfile.Read(dir, name)
			if rerr != nil {
				s.log.Error("env 写文件冲突后读现状失败", "name", name, "cause", rerr)
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			s.log.Warn("env 写文件冲突", "dir", dir, "name", name,
				"base", shortHash(req.BaseSHA256), "current", shortHash(curSHA), "cause", err)
			writeJSON(w, http.StatusConflict, proto.FileConflictResp{
				Error:   "env 文件已被改动",
				Current: proto.FileRead{Content: cur, Size: curSize, SHA256: curSHA},
			})
		case errors.Is(err, fs.ErrNotExist):
			s.log.Warn("env 写文件：目标在编辑期间被删", "dir", dir, "name", name, "cause", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "env 文件不存在"})
		default:
			s.log.Error("env 写文件失败", "dir", dir, "name", name, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "写入 env 文件失败"})
		}
		return
	}
	s.log.Info("env 写文件完成", "dir", dir, "name", name, "bytes", size, "sha256", shortHash(sha))
	writeJSON(w, http.StatusOK, proto.FileWriteResp{SHA256: sha, Size: size})
}
```

- [ ] **Step 4: 注册路由**

```go
	api.HandleFunc("GET /api/env/file", s.handleEnvFileRead)
	api.HandleFunc("PUT /api/env/file", s.handleEnvFileWrite)
```

路由表注释补两行。

- [ ] **Step 5: 跑测试确认它通过**

Run: `go test ./internal/agentd/ -run TestEnv -count=1`
Expected: PASS。

- [ ] **Step 6: 加关键节点日志**

已随 Step 3。**逐条自查这两个 handler 的日志参数里没有 `req.Content` 与 `content`**——读端点的进入日志刻意写成「（含值全文）」，让日后审计一眼看出这条是有意为之的那条。

- [ ] **Step 7: 加注释**

已随 Step 3：读端点写明「这条会把含值全文交给浏览器，且这是刻意的」并指向 spec §7；写端点写明「写前必须解析」的为什么与「重复键不拦」的为什么。

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/
git commit -m "feat(b158): GET/PUT /api/env/file，写前用 Parse 校验语法"
```

---

### Task 8: `PUT /api/env/mapping` 与热更新端到端回归

**Files:**
- Modify: `internal/agentd/env.go`
- Modify: `internal/agentd/env_test.go`
- Modify: `internal/agentd/server.go`（路由 + 注释）

**Interfaces:**
- Consumes: `proto.EnvMappingReq`、`Server.swapConf`（Task 3 已深拷 `Env`）、`Server.EnvMapping`（Task 2）
- Produces: `(*Server).handleEnvMapping`

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentd/env_test.go`：

```go
func TestEnvMappingSaveTranslatesTwoModes(t *testing.T) {
	env, envDir := newEnvEnv(t, map[string]string{"opencode": "old.env"}, "opencode", "codex")
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "proxy.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var resp proto.EnvResp
	code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: "codex", Mode: proto.EnvModeFile, File: "proxy.env"},
		{Executor: "opencode", Mode: proto.EnvModeOff},
	}}, &resp)
	if code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	// 差异一：off 落盘必须是**键不存在**，不是空串
	saved := env.srv.EnvMapping()
	if _, ok := saved["opencode"]; ok {
		t.Fatalf("off 档必须删键，实际 = %+v（空串是脏数据，会让 Resolver 读 <dir>/）", saved)
	}
	if saved["codex"] != "proxy.env" || len(saved) != 1 {
		t.Fatalf("saved = %+v，想要只有 codex", saved)
	}
	// 保存后直接回最新状态，界面拿它刷新
	got := map[string]string{}
	for _, b := range resp.Bindings {
		got[b.Executor] = b.Mode
	}
	if got["opencode"] != proto.EnvModeOff || got["codex"] != proto.EnvModeFile {
		t.Fatalf("响应 = %+v", resp.Bindings)
	}
}

func TestEnvMappingRejectsMissingFileAndBadMode(t *testing.T) {
	env, _ := newEnvEnv(t, nil, "opencode")
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	var body map[string]string

	// file 档指向不存在的文件：把错误挡在保存这一刻，好过三天后某次派发才炸
	if code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: "opencode", Mode: proto.EnvModeFile, File: "gone.env"},
	}}, &body); code != 400 {
		t.Fatalf("缺文件 code = %d, want 400", code)
	}
	// file 档但文件名为空：绝不能落成空串
	if code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: "opencode", Mode: proto.EnvModeFile, File: "  "},
	}}, &body); code != 400 {
		t.Fatalf("空文件名 code = %d, want 400", code)
	}
	// mode 非法
	if code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: "opencode", Mode: "default"},
	}}, &body); code != 400 {
		t.Fatalf("非法 mode code = %d, want 400（env 没有 default 档）", code)
	}
	// executor 名为空
	if code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: " ", Mode: proto.EnvModeOff},
	}}, &body); code != 400 {
		t.Fatalf("空 executor code = %d, want 400", code)
	}
}

func TestEnvMappingHotReloadsWithoutRebuildingManager(t *testing.T) {
	// 这条是整个 B158 的承重判据：改完映射**不重建 Manager**，
	// manager 侧的 resolver 必须立即反映新值
	env, envDir := newEnvEnv(t, nil, "opencode")
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "proxy.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	before, err := env.mgr.env.For("opencode")
	if err != nil || len(before) != 0 {
		t.Fatalf("before = %v, err = %v，想要未配置时不注入", before, err)
	}
	var resp proto.EnvResp
	if code := env.putJSON(t, "/api/env/mapping", proto.EnvMappingReq{Bindings: []proto.EnvBinding{
		{Executor: "opencode", Mode: proto.EnvModeFile, File: "proxy.env"},
	}}, &resp); code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}
	after, err := env.mgr.env.For("opencode")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(after) != 1 || after[0] != "A=1" {
		t.Fatalf("after = %v，想要 [A=1]（不重启 agentd 就该生效）", after)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestEnvMapping -count=1`
Expected: FAIL —— 404。

- [ ] **Step 3: 写实现**

追加到 `internal/agentd/env.go`：

```go
// handleEnvMapping 处理 PUT /api/env/mapping[?machine=]。
//
// 请求体 proto.EnvMappingReq：**整段替换**该机的 env 配置段。
//
// 响应：200 proto.EnvResp（保存后的最新状态，界面直接拿它刷新）
//
//	400 mode 非法 / executor 为空 / file 档文件名为空或指向不存在的文件
//	503 manager 未就绪
//
// **两档翻译（与 discipline 错位，照抄必错）**：
//
//	off  → 配置里**删掉这个键**（不是写空串！空串会让 Resolver 走到「读
//	       <dir>/」这种无意义路径，是纯粹的脏数据）
//	file → 校验文件存在后写入文件名
//
// 为什么要校验「file 档的文件必须存在」：Resolver 的既定语义是「配了但读不到
// = 派发失败」。把错误挡在保存这一刻，好过让它在三天后某次派发时炸出来。
// 注意这只是保存时的一次性校验——文件仍可能事后被删，那时的失败仍由派发路径承担。
func (s *Server) handleEnvMapping(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	if s.mgr == nil {
		s.log.Warn("env 映射保存：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var req proto.EnvMappingReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("env 映射保存：请求体无法解析", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	dir := envfile.Dir(s.conf().DataDir)
	s.log.Info("env 映射保存请求", "bindings", len(req.Bindings), "dir", dir)

	next := map[string]string{}
	for _, b := range req.Bindings {
		name := strings.TrimSpace(b.Executor)
		if name == "" {
			s.log.Warn("env 映射保存被拒：executor 名为空", "cause", "executor 名不能为空")
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "executor 名不能为空"})
			return
		}
		switch b.Mode {
		case proto.EnvModeOff:
			// 不注入 = 配置里**不出现这个键**，什么都不写。
		case proto.EnvModeFile:
			file := strings.TrimSpace(b.File)
			if file == "" {
				s.log.Warn("env 映射保存被拒：file 档文件名为空", "executor", name,
					"cause", "空串会被 Resolver 当成未配置，且是脏数据")
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": fmt.Sprintf("%s 选了「指定文件」但没给文件名", name)})
				return
			}
			if _, _, _, err := envfile.Read(dir, file); err != nil {
				s.log.Warn("env 映射保存被拒：文件不可用", "executor", name, "file", file, "cause", err)
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": fmt.Sprintf("%s 指定的 env 文件不可用：%v", name, err)})
				return
			}
			next[name] = file
		default:
			s.log.Warn("env 映射保存被拒：档位非法", "executor", name, "mode", b.Mode,
				"cause", "只支持 file/off")
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("%s 的档位 %q 非法：只支持 file/off", name, b.Mode)})
			return
		}
	}
	if err := s.swapConf(func(c *config.Config) error {
		c.Env = next
		return nil
	}); err != nil {
		s.log.Error("env 映射落盘失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("env 映射已保存", "configured", len(next))
	s.handleEnvGet(w, r) // 回最新状态，界面直接拿它刷新
}
```

- [ ] **Step 4: 注册路由**

```go
	api.HandleFunc("PUT /api/env/mapping", s.handleEnvMapping)
```

路由表注释补一行。

- [ ] **Step 5: 跑测试确认它通过**

Run: `go test ./internal/agentd/ -count=1`
Expected: PASS（整包，不只本组——`NewManager` 签名变过，回归必须全跑）。

- [ ] **Step 6: 加关键节点日志**

已随 Step 3：进入（binding 数 + dir）、四类拒绝各自的 Warn（**都带 cause**）、落盘失败 Error、成功（`configured` 计数）。**不打映射内容**——文件名会随保存后的 GET 响应回到界面，日志里再复述一遍没有价值。

- [ ] **Step 7: 加注释**

已随 Step 3：doc 注释里用代码块钉住两档翻译并显式写「不是写空串！」；「为什么要校验文件存在」与「这只是一次性校验」。

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/
git commit -m "feat(b158): PUT /api/env/mapping 两档落盘，off 档删键不写空串"
```

---
### Task 9: 前端 API 客户端 + 抽出共用编辑器 `BlockEditor`

**Files:**
- Modify: `web/src/api/client.ts`
- Create: `web/src/app/settings/BlockEditor.tsx`
- Modify: `web/src/app/settings/DisciplinePage.tsx`（改用 `BlockEditor`，并修掉 409 判定的 Minor）
- Modify: `web/src/app/settings/DisciplinePage.test.tsx`（`aria-label` 从「纪律块正文」改为通用值后同步）
- Create: `web/src/app/settings/BlockEditor.test.tsx`

**Interfaces:**
- Consumes: Task 4 的 TS 类型
- Produces:
  - `fetchEnv(machine: string): Promise<EnvResp>`
  - `fetchEnvKeys(machine: string, name: string): Promise<EnvKeysResp>`
  - `fetchEnvFile(machine: string, name: string): Promise<FileRead>`
  - `saveEnvFile(machine: string, name: string, req: FileWriteReq): Promise<FileWriteResp>`
  - `saveEnvMapping(machine: string, bindings: EnvBinding[]): Promise<EnvResp>`
  - `<BlockEditor>` 组件，props 见下

> **这是抽取，不是重写**：`BlockEditor` 的行为必须与 `DisciplinePage` 里的 `Editor` 逐像素一致，唯一的行为改动是 409 判定——B157 记账的那处 Minor：当前用 `error === '已被改动'` **字符串相等**决定是否显示「重新加载」，任何人改一下文案就会静默失去这个按钮。改成显式的 `conflict: boolean`。

- [ ] **Step 1: 写失败测试**

创建 `web/src/app/settings/BlockEditor.test.tsx`：

```tsx
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BlockEditor } from './BlockEditor'

describe('BlockEditor', () => {
  it('只读态：textarea 带 readonly，显示模板按钮而非保存', () => {
    render(<BlockEditor title="内置 subagent" ariaLabel="纪律块正文" content="正文" readOnly
      templateLabel="以此为模板新建" onTemplate={() => {}} />)
    expect(screen.getByRole('textbox', { name: /纪律块正文/ })).toHaveAttribute('readonly')
    expect(screen.getByRole('button', { name: '以此为模板新建' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '保存' })).not.toBeInTheDocument()
  })

  it('可写态：改动走 onChange，保存走 onSave', async () => {
    const onChange = vi.fn()
    const onSave = vi.fn()
    render(<BlockEditor title="a.env" ariaLabel="env 文件正文" content="A=1" readOnly={false}
      onChange={onChange} onSave={onSave} />)
    await userEvent.type(screen.getByRole('textbox', { name: /env 文件正文/ }), '2')
    expect(onChange).toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('冲突由 conflict 布尔决定，不看错误文案', () => {
    // 这条正是 B157 记账的 Minor：以前用 error === '已被改动' 判定，
    // 改一下文案就会静默失去「重新加载」按钮
    const { rerender } = render(<BlockEditor title="a.env" ariaLabel="env 文件正文" content=""
      readOnly={false} error="随便什么别的错误" />)
    expect(screen.queryByRole('button', { name: '重新加载' })).not.toBeInTheDocument()

    rerender(<BlockEditor title="a.env" ariaLabel="env 文件正文" content="" readOnly={false}
      error="盘上的内容和你打开时不一样了" conflict onReload={() => {}} />)
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/settings/BlockEditor.test.tsx`
Expected: FAIL —— 模块不存在。

- [ ] **Step 3: 写 `BlockEditor`**

创建 `web/src/app/settings/BlockEditor.tsx`：

```tsx
// BlockEditor —— 设置页里「一个纯文本文件的正文编辑器」（B158 从 B157 抽出）。
//
// 职责：正文 textarea + 保存 / 只读态的「以此为模板新建」 + 错误与冲突提示。
//
// 由「执行纪律」与「Env 文件」两个分区共用。抽取时唯一的行为改动：冲突态
// 从「按错误文案字符串相等判定」改成显式的 conflict 布尔——文案是给人看的，
// 拿它当控制流，改一个字就会静默失去「重新加载」按钮。
//
// 边界：
//   - **不发请求**：读、写、冲突判定全在调用方，本组件只是受控展示
//   - 不认识 env 或纪律块的语义：aria-label 与底部提示都由调用方给
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export interface BlockEditorProps {
  /** 标题（文件名或「内置 <tier>」） */
  title: string
  /** textarea 的 aria-label，各分区自定（如「env 文件正文」） */
  ariaLabel: string
  content: string
  readOnly: boolean
  loading?: boolean
  onChange?: (value: string) => void
  onSave?: () => void
  /** 只读态的主按钮；给了 templateLabel 才渲染 */
  onTemplate?: () => void
  templateLabel?: string
  saving?: boolean
  error?: string
  /** 冲突态（HTTP 409）。为真才显示「重新加载」——不看 error 的文案 */
  conflict?: boolean
  notice?: string
  onReload?: () => void
  /** 磁盘上的字节数，显示在底部提示里 */
  size?: number
  /** 底部提示的前半句，各分区自定 */
  footerHint?: string
  /** 大小上限提示里的字节数，默认 64 KiB */
  maxLabel?: string
}

// BlockEditor 渲染一个受控的纯文本正文编辑器。行为见 BlockEditorProps 各字段。
export function BlockEditor({
  title, ariaLabel, content, readOnly, loading = false, onChange, onSave, onTemplate,
  templateLabel, saving = false, error = '', conflict = false, notice = '', onReload, size,
  footerHint = '保存后下一个任务即生效（正在跑的任务不受影响）', maxLabel = '64 KiB',
}: BlockEditorProps) {
  return (
    <>
      <div className="flex items-center justify-between gap-2">
        <div>
          <h3 className="text-sm font-semibold">{title}</h3>
          {readOnly && <span className="text-[11px] text-muted-foreground">只读</span>}
        </div>
        {readOnly ? (
          templateLabel !== undefined && <Button size="sm" onClick={onTemplate}>{templateLabel}</Button>
        ) : (
          <Button size="sm" onClick={onSave} disabled={saving || loading}>保存</Button>
        )}
      </div>
      <textarea
        aria-label={ariaLabel}
        value={content}
        readOnly={readOnly}
        disabled={loading}
        onChange={(event) => onChange?.(event.target.value)}
        className={cn(
          'mt-3 min-h-[28rem] w-full resize-y rounded-md border p-3 font-mono text-xs leading-5 outline-none focus-visible:ring-1 focus-visible:ring-ring',
          readOnly ? 'bg-muted/50' : 'bg-background',
        )}
      />
      {!readOnly && (
        <p className="mt-2 text-[11px] text-muted-foreground">
          {footerHint}；上限 {maxLabel}{size !== undefined && `；当前 ${size} 字节`}
        </p>
      )}
      {error && (
        <div role="alert" className="mt-2 flex flex-wrap items-center gap-2 text-xs text-destructive">
          <span>{error}</span>
          {conflict && <Button type="button" variant="outline" size="sm" onClick={onReload}>重新加载</Button>}
        </div>
      )}
      {notice && <p className="mt-2 text-xs text-emerald-700">{notice}</p>}
    </>
  )
}
```

- [ ] **Step 4: `DisciplinePage` 改用它**

删掉文件末尾的本地 `Editor` 组件，import `BlockEditor`，两处调用改为：

```tsx
            {selectedBuiltin && (
              <BlockEditor
                title={`内置 ${selectedBuiltin.tier}`}
                ariaLabel="纪律块正文"
                content={selectedBuiltin.content}
                readOnly
                templateLabel="以此为模板新建"
                onTemplate={() => openNew(selectedBuiltin.content)}
              />
            )}
            {selectedFile !== null && (
              <BlockEditor
                title={selectedFile}
                ariaLabel="纪律块正文"
                content={draft}
                readOnly={false}
                loading={loadingFile}
                onChange={setDraft}
                onSave={() => void save()}
                saving={busy}
                error={error}
                conflict={conflict}
                notice={notice}
                onReload={() => void reloadFile()}
                size={selectedFileInfo?.size}
              />
            )}
```

新增 `const [conflict, setConflict] = useState(false)`；`save()` 的 catch 改为：

```tsx
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setConflict(true)
        setError('盘上的内容和你打开时不一样了——重新加载会丢弃当前编辑')
      } else {
        setError(errorMessage(err))
      }
    } finally {
```

并在每处 `setError('')` 旁补 `setConflict(false)`（`selectBuiltin` / `selectFile` / `reloadFile` / `save` 入口 / 切机器的 effect）。

- [ ] **Step 5: 加 API 客户端函数**

`web/src/api/client.ts` 在 discipline 那组之后追加：

```ts
// fetchEnv 取某台机器的 env 配置面（GET /api/env）：
// 目录、该机文件列表、每个 executor 的档位（两档）。
export function fetchEnv(machine: string): Promise<EnvResp> {
  return request<EnvResp>(`/api/env${machineQuery(machine)}`)
}

// fetchEnvKeys 取一个 env 文件的变量清单（GET /api/env/file/keys）。
//
// **响应里没有值**，只有 key 名、值的字节长度与重复标记。这是 Env 分区的
// 默认视图；要看值必须显式调 fetchEnvFile。
export function fetchEnvKeys(machine: string, name: string): Promise<EnvKeysResp> {
  return request<EnvKeysResp>(
    `/api/env/file/keys?name=${encodeURIComponent(name)}${machineQuery(machine, '&')}`,
  )
}

// fetchEnvFile 读一个 env 文件的**含值全文**（GET /api/env/file）。
//
// 只在用户点「编辑正文」时调用——默认视图走 fetchEnvKeys。
export function fetchEnvFile(machine: string, name: string): Promise<FileRead> {
  return request<FileRead>(
    `/api/env/file?name=${encodeURIComponent(name)}${machineQuery(machine, '&')}`,
  )
}

// saveEnvFile 写一个 env 文件（PUT /api/env/file）。
//
// req.base_sha256 为空串表示新建：目标已存在时后端回 409，绝不静默覆盖。
// 正文语法错误时后端回 400，message 是 Parse 的原文（自带行号）——调用方
// 应原样展示，那是用户改对的唯一线索。
export function saveEnvFile(
  machine: string, name: string, req: FileWriteReq,
): Promise<FileWriteResp> {
  return putJSON<FileWriteResp>(
    `/api/env/file?name=${encodeURIComponent(name)}${machineQuery(machine, '&')}`, req,
  )
}

// saveEnvMapping 整段替换某台机器的 executor→env 文件映射
//（PUT /api/env/mapping），返回保存后的最新配置面。
export function saveEnvMapping(
  machine: string, bindings: EnvBinding[],
): Promise<EnvResp> {
  return putJSON<EnvResp>(`/api/env/mapping${machineQuery(machine)}`, { bindings })
}
```

（在文件顶部的类型 import 里补 `EnvResp` / `EnvKeysResp` / `EnvBinding`。）

- [ ] **Step 6: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/settings/ && npx tsc -b`
Expected: `BlockEditor.test.tsx` 全绿，`DisciplinePage.test.tsx` **不得回归**（若因 aria-label 或文案变动而红，改测试到新形态，但「有 readonly 属性」「保存后刷新」这些断言的语义不许放宽）。

- [ ] **Step 7: 加关键节点日志**

前端不打 `console.log`。可观测性落在**用户可见的状态上**，自检这三条都在：保存中（按钮 `disabled`）、保存失败（`role="alert"` 的错误原文）、冲突（「重新加载」按钮）。

- [ ] **Step 8: 加注释**

已随 Step 3/Step 5：`BlockEditor` 的文件头写明「这是抽取」与 conflict 布尔的为什么；五个客户端函数各自写明「有没有值」这条边界。

- [ ] **Step 9: Commit**

```bash
git add web/src/
git commit -m "refactor(b158): 抽出 BlockEditor 并加 env 五个客户端函数，409 改布尔判定"
```

---

### Task 10: 设置页「Env 文件」分区

**Files:**
- Create: `web/src/app/settings/EnvPage.tsx`
- Create: `web/src/app/settings/EnvPage.test.tsx`
- Modify: `web/src/app/settings/SettingsPage.tsx`（占位换成 `<EnvPage />`）

**Interfaces:**
- Consumes: Task 9 的五个客户端函数与 `BlockEditor`
- Produces: `<EnvPage />`

形态基准：`prototypes/discipline-config/pages/settings.html` 的三栏骨架（机器切换条 + 左文件列表 + 右内容区）。**唯一新增的一屏是「变量清单（值掩码）」**。

- [ ] **Step 1: 写失败测试**

创建 `web/src/app/settings/EnvPage.test.tsx`：

```tsx
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { EnvPage } from './EnvPage'
import * as client from '../../api/client'

vi.mock('../data/useMachines', () => ({
  useMachines: () => ({
    data: { machines: [{ name: '', reachable: true, executors: ['opencode'], error: '' }] },
    errorText: '', sessionExpired: false,
  }),
}))

const envResp = {
  dir: '/home/dev/.handoff/env',
  files: [{ name: 'proxy.env', size: 64, sha256: 'aa' }],
  bindings: [{ executor: 'opencode', mode: 'file' as const, file: 'proxy.env' }],
}

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(client, 'fetchEnv').mockResolvedValue(envResp)
  vi.spyOn(client, 'fetchEnvKeys').mockResolvedValue({
    keys: [
      { key: 'HTTPS_PROXY', value_bytes: 34 },
      { key: 'GOPROXY', value_bytes: 21, duplicate: true },
    ],
  })
})

describe('EnvPage', () => {
  it('默认显示变量清单，不显示值，也不拉全文', async () => {
    const full = vi.spyOn(client, 'fetchEnvFile')
    render(<EnvPage />)
    await userEvent.click(await screen.findByRole('button', { name: /proxy\.env/ }))
    expect(await screen.findByText('HTTPS_PROXY')).toBeInTheDocument()
    expect(screen.getByText(/34 字节/)).toBeInTheDocument()
    expect(screen.getByText(/重复定义/)).toBeInTheDocument()
    // 承重判据：默认视图不得触碰含值的全文接口
    expect(full).not.toHaveBeenCalled()
    expect(screen.queryByRole('textbox', { name: /env 文件正文/ })).not.toBeInTheDocument()
  })

  it('点「编辑正文」才拉全文并给出编辑器', async () => {
    const full = vi.spyOn(client, 'fetchEnvFile')
      .mockResolvedValue({ content: 'HTTPS_PROXY=http://x\n', size: 21, sha256: 'bb' })
    render(<EnvPage />)
    await userEvent.click(await screen.findByRole('button', { name: /proxy\.env/ }))
    await userEvent.click(await screen.findByRole('button', { name: '编辑正文' }))
    expect(full).toHaveBeenCalledWith('', 'proxy.env')
    expect(await screen.findByRole('textbox', { name: /env 文件正文/ })).toHaveValue('HTTPS_PROXY=http://x\n')
  })

  it('语法错误时展示后端原文且不清空编辑内容', async () => {
    vi.spyOn(client, 'fetchEnvFile')
      .mockResolvedValue({ content: 'A=1\n', size: 4, sha256: 'bb' })
    vi.spyOn(client, 'saveEnvFile').mockRejectedValue(
      new client.ApiError(400, 'env 文件第 2 行语法错误：1BAD=x'))
    render(<EnvPage />)
    await userEvent.click(await screen.findByRole('button', { name: /proxy\.env/ }))
    await userEvent.click(await screen.findByRole('button', { name: '编辑正文' }))
    const box = await screen.findByRole('textbox', { name: /env 文件正文/ })
    await userEvent.type(box, '1BAD=x')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/第 2 行语法错误/)).toBeInTheDocument()
    expect(box).toHaveValue('A=1\n1BAD=x') // 编辑内容不许被清掉
  })

  it('没有内置版：左栏只有一组文件，也没有「以此为模板新建」', async () => {
    render(<EnvPage />)
    await screen.findByRole('button', { name: /proxy\.env/ })
    expect(screen.queryByText(/内置/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '以此为模板新建' })).not.toBeInTheDocument()
  })
})
```

再补一条断开降级用例（`useMachines` mock 成 `reachable: false`，断言不发请求且展示 `error` 原文），写法照 `DisciplinePage.test.tsx` 里同名用例。

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/settings/EnvPage.test.tsx`
Expected: FAIL —— 模块不存在。

- [ ] **Step 3: 写实现**

创建 `web/src/app/settings/EnvPage.tsx`。骨架与 `DisciplinePage.tsx` 同构（机器切换条、左文件列表、右内容区、新建弹层、断开降级、不轮询），**四处不同**：

1. 左栏**只有一组**「<机器> 上的文件」——没有内置版，右上角也没有「以此为模板新建」；
2. 选中文件后**默认渲染 `KeyList`**（变量清单），不是编辑器；
3. 顶部有「编辑正文」按钮，点了才 `fetchEnvFile` 并切成 `BlockEditor`；保存成功后切回清单并重拉 `fetchEnvKeys`；
4. 新建弹层只有「文件名 + 起始内容（空白）」两项。

文件头注释：

```tsx
// EnvPage —— 设置页「Env 文件」分区（B158 spec §2.1）。
//
// 职责：按机器查看与编辑 <DataDir>/env/ 下的 env 文件。
//
// **默认视图是变量清单，不是正文**：日常最高频的问题是「这台机给某个 executor
// 注了哪些变量」，回答它不需要看见任何一个值。点「编辑正文」才拉含值全文。
//
// 诚实的边界（spec §7）：点「编辑正文」后全文（含值）确实会到浏览器——不然
// 没法编辑。默认掩码防的是肩窥、截图、录屏、把整页贴给别人，**不是防浏览器
// 本身，更不是加密**。不要在界面上写出任何「凭据不出执行机」之类的承诺。
//
// 形态基准：prototypes/discipline-config/pages/settings.html（三栏骨架照搬 B157）。
//
// 边界：
//   - **不轮询**：进分区/切机器/保存后各拉一次即可，照抄开发机的 15s 探活会
//     把用户正在编辑的正文覆盖掉
//   - 不做删除与改名（改名会让映射静默指空）
//   - 映射不在这里改：那是开发机详情的事
//   - 断开的机器不发请求、不画编辑器，直接展示 error 原文
```

`KeyList` 子组件（同文件内）：

```tsx
// KeyList 渲染变量清单。**只有 key 名与值长度**——本组件连接收值的 prop 都没有，
// 这是 spec §7 凭据边界在前端的结构性保证。
function KeyList({ keys }: { keys: EnvKey[] }) {
  if (keys.length === 0) {
    return <p className="mt-3 text-xs text-muted-foreground">这个文件里没有变量（可能只有注释或空行）。</p>
  }
  return (
    <ul className="mt-3 divide-y rounded-md border">
      {keys.map((k) => (
        <li key={k.key} className="flex items-center gap-3 px-3 py-1.5 text-xs">
          <span className="font-mono font-medium">{k.key}</span>
          <span className="text-muted-foreground">{k.value_bytes} 字节</span>
          {k.duplicate && <span className="text-amber-700">重复定义（后者覆盖）</span>}
          <span className="ml-auto font-mono text-muted-foreground">••••••</span>
        </li>
      ))}
    </ul>
  )
}
```

保存与冲突处理照 `DisciplinePage` 的 `save()`（`conflict` 布尔 + `BlockEditor`）；**语法错误（400）走普通 error 分支，不置 conflict**——那不是冲突，给「重新加载」会误导。

- [ ] **Step 4: 挂进设置页**

`SettingsPage.tsx`：import `EnvPage`，把 `section === 'env'` 的占位段落整段换成 `<EnvPage />`。

- [ ] **Step 5: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/settings/ && npx tsc -b`
Expected: 全绿（含 `SettingsPage.test.tsx` 的既有断言——若它断言了「本期不做」那句占位文案，改成断言 Env 分区渲染出了机器切换条）。

- [ ] **Step 6: 加关键节点日志**

同 Task 9：可观测性落在用户可见状态上。自检四条：加载中（「正在加载 env 配置…」）、加载失败（`role="alert"` + 原文）、保存中（按钮 disabled）、保存成功（绿色 notice「已保存；下一个任务即生效」）。**空清单也要有话说**（「这个文件里没有变量」），一块空白会让人以为页面坏了。

- [ ] **Step 7: 加注释**

已随 Step 3：文件头的职责 + 诚实边界 + 四条边界；`KeyList` 的「本组件连接收值的 prop 都没有」；「语法错误不置 conflict」的为什么。

- [ ] **Step 8: Commit**

```bash
git add web/src/
git commit -m "feat(b158): 设置页 Env 文件分区，默认只显变量清单"
```

---

### Task 11: 开发机详情的 env 映射块

**Files:**
- Create: `web/src/app/machines/MachineEnv.tsx`
- Create: `web/src/app/machines/MachineEnv.test.tsx`
- Modify: `web/src/app/machines/MachineDetail.tsx`（挂在 `<MachineDiscipline />` 之后）

**Interfaces:**
- Consumes: `fetchEnv` / `saveEnvMapping`
- Produces: `<MachineEnv machine={machine} />`

- [ ] **Step 1: 写失败测试**

创建 `web/src/app/machines/MachineEnv.test.tsx`：

```tsx
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MachineEnv } from './MachineEnv'
import * as client from '../../api/client'
import type { Machine } from '../../api/types'

const machine = { name: 'mac-02', reachable: true, executors: ['codex', 'opencode'], error: '' } as Machine

const resp = {
  dir: '/home/dev/.handoff/env',
  files: [{ name: 'proxy.env', size: 64, sha256: 'aa' }],
  bindings: [
    { executor: 'codex', mode: 'off' as const },
    { executor: 'opencode', mode: 'file' as const, file: 'proxy.env' },
  ],
}

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(client, 'fetchEnv').mockResolvedValue(resp)
})

describe('MachineEnv', () => {
  it('两档下拉：只有「不注入」与文件名，没有「内置默认」', async () => {
    render(<MachineEnv machine={machine} />)
    const select = await screen.findByRole('combobox', { name: /codex 的 env 文件/ })
    const options = Array.from(select.querySelectorAll('option')).map((o) => o.textContent)
    expect(options).toEqual(['不注入', 'proxy.env'])
  })

  it('保存 payload 用两档编码，off 不带 file', async () => {
    const save = vi.spyOn(client, 'saveEnvMapping').mockResolvedValue(resp)
    render(<MachineEnv machine={machine} />)
    const select = await screen.findByRole('combobox', { name: /codex 的 env 文件/ })
    await userEvent.selectOptions(select, 'file:proxy.env')
    expect(screen.getByText('有未保存的改动')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(save).toHaveBeenCalledWith('mac-02', [
      { executor: 'codex', mode: 'file', file: 'proxy.env' },
      { executor: 'opencode', mode: 'file', file: 'proxy.env' },
    ])
  })

  it('机器断开时不发请求，展示 error 原文', () => {
    const fetchEnv = vi.spyOn(client, 'fetchEnv')
    render(<MachineEnv machine={{ ...machine, reachable: false, error: 'dial tcp: refused' } as Machine} />)
    expect(fetchEnv).not.toHaveBeenCalled()
    expect(screen.getByText(/dial tcp: refused/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `cd web && npx vitest run src/app/machines/MachineEnv.test.tsx`
Expected: FAIL —— 模块不存在。

- [ ] **Step 3: 写实现**

创建 `web/src/app/machines/MachineEnv.tsx`，结构与 `MachineDiscipline.tsx` 同构（拉一次、脏态标记、整块一个保存、断开降级），**两处不同**：

1. 下拉只有两档，`decode` 只认 `off` 与 `file:<名>`；
2. 描述文案：`off` → 「未配置——启动时不注入任何环境变量」，`file` → 「正文在「Env 文件」分区里编辑」。

```tsx
// MachineEnv —— 开发机详情里的「Env 文件」块（B158 spec §2.2）。
//
// 职责：给这台机器的每个 executor 指定注入哪个 env 文件（**两档**），整块一次保存。
//
// **两档不是三档**：env 没有内置默认。「不注入」在配置里表现为**键不存在**，
// 不是空串——照抄 MachineDiscipline 的三档翻译会写出脏数据（见 spec §2.3）。
//
// 形态基准：prototypes/discipline-config/pages/settings.html 的映射块。
//
// 边界：
//   - **不编辑正文**：正文在设置页的「Env 文件」分区里改，这里只选文件
//   - 不轮询：进入详情拉一次，保存后用响应刷新（响应就是最新状态）
//   - 机器断开时不发请求、不渲染控件——配置读不到也写不了，画出来只会骗人
//
// 下拉的 value 编码：'off' / `file:<文件名>`。用前缀而不是裸文件名，是为了让一个
// 名叫 "off" 的文件不会与「不注入」撞值。
```

`decodeBinding`：

```tsx
function decodeBinding(executor: string, value: string): EnvBinding {
  if (value === 'off') return { executor, mode: 'off' }
  return { executor, mode: 'file', file: value.startsWith('file:') ? value.slice('file:'.length) : value }
}
```

下拉选项：

```tsx
                    <option value="off">不注入</option>
                    {response.files.map((file) => (
                      <option key={file.name} value={`file:${file.name}`}>{file.name}</option>
                    ))}
```

底部提示：`保存后下一个任务即生效，不必重启 agentd。`

- [ ] **Step 4: 挂进开发机详情**

`MachineDetail.tsx`：import 后在 `<MachineDiscipline machine={machine} />` 之下加一行 `<MachineEnv machine={machine} />`。

- [ ] **Step 5: 跑测试确认它通过**

Run: `cd web && npx vitest run src/app/machines/ && npx tsc -b`
Expected: 全绿（`MachinesPage.test.tsx` 不得回归）。

- [ ] **Step 6: 加关键节点日志**

自检三条用户可见状态：加载中、加载失败（原文）、脏态提示「有未保存的改动」。保存失败时 `role="alert"` 展示后端原文——「指定的 env 文件不可用」这类 400 必须原样出现，那是用户改对的唯一线索。

- [ ] **Step 7: 加注释**

已随 Step 3：文件头显式写「两档不是三档」与「键不存在 ≠ 空串」，以及 value 前缀编码的为什么。

- [ ] **Step 8: Commit**

```bash
git add web/src/
git commit -m "feat(b158): 开发机详情新增 env 文件映射块（两档）"
```

---

### Task 12: 全量校验

**Files:** 无新增；只跑校验与修复

- [ ] **Step 1: Go 全量**

Run: `gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1`
Expected: `gofmt -l .` **无输出**；其余 exit 0、无失败包。

> `gofmt -l .` 无输出这条是硬判据，不是形式：测试全绿 ≠ 格式干净，这两件事互不覆盖。

- [ ] **Step 2: 前端全量**

Run: `cd web && npx vitest run && npx tsc -b && npx eslint . && npm run build`
Expected: 测试全绿、0 类型错误、0 eslint error（既有 warning 不计）、构建成功。

- [ ] **Step 3: 红线自查**

Run:
```bash
grep -rn "fmt.Printf\|os.RemoveAll" internal/ | grep -v _test.go
grep -rn "console.log" web/src/
```
Expected: 与本次改动相关的命中为 0。

- [ ] **Step 4: 凭据红线自查（本 task 的重点）**

Run:
```bash
grep -n 's.log' internal/agentd/env.go
grep -n 'r.log' internal/envfile/resolver.go
```
逐条确认：**没有任何一条日志的参数里出现文件正文或变量的值**（允许出现 key 名、字节数、短哈希、文件名）。

再确认结构性判据：`internal/proto/env.go` 里除 `EnvResp/EnvFile/EnvBinding/EnvKey/EnvKeysResp/EnvMappingReq` 外没有别的字段，且这几个结构里**没有任何字段承载值**。

- [ ] **Step 5: Commit（如有修复）**

```bash
git add -A
git commit -m "chore(b158): 全量校验与红线自查的修复"
```

---

## 附：留给审核者的验收（**不派发**）

以下两条**不写进执行者的工作范围**——一条要驱动 handoff 自身（起 agentd、派任务、调 CLI），与执行纪律块里「不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程」直接冲突；一条要开浏览器并排比对原型。执行者做完 Task 12 即完工。

- [ ] **A. 形态走查（spec §8.5）**

打开 `prototypes/discipline-config/pages/settings.html` 与真实控制台的设置页并排比对：分区位置与命名、机器切换、文件列表、断开降级、映射两档下拉与脏态提示。**新增的「变量清单（值掩码）」那一屏没有原型基准**——按 spec §2.1 的描述人工判断，确认后回流进 `prototypes/base/pages/settings.html`。

- [ ] **B. 真机判据（spec §8.3 + §8.4）**

在一台真实执行机上：控制台把该机某个 executor 的 env 从「不注入」改成一份自定义文件并保存，**不重启 agentd**，随即向该机派一个该 executor 的任务，确认该任务的进程确实拿到了新文件里的变量（用一个可观测的自定义变量验证）。同时抓一次 `GET /api/env/file/keys` 的原始响应，人工确认里面不含任何值。

---

## 附：本计划与 spec 的对应

| spec 章节 | 落点 |
|---|---|
| §1.2 差异一（两档、off = 删键） | Task 4（类型）+ Task 5（读）+ Task 8（写）+ Task 11（界面） |
| §1.2 差异二（写前解析） | Task 7 |
| §1.2 差异三（值不出后端） | Task 6 + Task 10 + Task 12 Step 4 |
| §2.1 Env 分区 | Task 10 |
| §2.2 开发机详情映射块 | Task 11 |
| §2.3 两档 vs 三档 | Task 8（服务端翻译）+ Task 11（界面） |
| §3.1 数据结构 | Task 4 |
| §3.1 Bindings 并集与整段替换 | Task 5（并集）+ Task 8（替换） |
| §3.2 keys 端点与 lookup=nil | Task 6 |
| §3.3 校验与错误语义 | Task 6（读）+ Task 7（写）+ Task 8（映射） |
| §3.4 目录不存在 | Task 1（List）+ Task 5（用例） |
| §4.0 envfile 补文件操作面 | Task 1 |
| §4.1 swapConf 深拷 Env | Task 3 |
| §4.2 Resolver 吃活映射 | Task 2 |
| §5 前端落点与不轮询 | Task 9 / 10 / 11 |
| §5 抽出 BlockEditor + 修 B157 Minor | Task 9 |
| §6 契约与测试 | Task 4 + 各 task 的测试步骤 |
| §7 凭据边界 | Task 1（0600）+ Task 6（结构性无值）+ Task 12 Step 4（红线自查） |
| §8.1 / §8.2 验收 | Task 10 / Task 11 的用例 |
| §8.3 / §8.4 / §8.5 | 留给审核者（见上一节） |
