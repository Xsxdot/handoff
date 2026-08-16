# 发布路径加固实施计划（B86）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把「从 push 到用户装上」这条路径补完整：加验证门、发 Windows 协调者资产、给 macOS 资产签名公证、补 Apache-2.0 许可证。

**Architecture:** 四件事共用一条 GitHub Actions 流水线。新增的 `ci.yml` 用 `workflow_call` 同时服务 PR 与 release，保证两处校验是同一份定义。release 矩阵从 4 涨到 6，Windows 出 `.zip`（内含 `handoff.exe`），darwin 两项迁到 macOS runner 做 Developer ID 签名与公证。Go 侧只需三处小改（资产扩展名、按魔数分派解包、临时文件补 `.exe`），自更新链路的三个函数签名一个都不动。

**Tech Stack:** Go 1.26.1、GitHub Actions、bash、PowerShell（5.1 与 7 双版本）、Cloudflare Workers、`codesign` / `notarytool` / `spctl`。

**Spec:** [2026-08-13-release-pipeline-windows-and-signing-design.md](../specs/2026-08-13-release-pipeline-windows-and-signing-design.md)

**分支前置：** 本计划在 `handoff/release-pipeline-hardening` 上执行，从 **B84 合入之后的 `main`** 拉出。B84（含本 spec 与本计划）当前还在 `handoff/coordinator-only-machine` 上未合 main——**先把它合掉再开分支**，否则 Task 3 会与 B84 对 `cmd/init.go` 的改动冲突，Task 5 的平台断言也会踩到 B84 尚未落地的前提。

## Global Constraints

以下约束对**每一个 task** 生效，不再逐条重复：

- **Go module 路径**：`github.com/xushixin/handoff`。ldflags 注入路径逐字为 `-X github.com/xushixin/handoff/internal/buildinfo.releaseVersion=`，写成 GitHub owner（`Xsxdot`）会静默失效。
- **GitHub 仓库**：`Xsxdot/handoff`。install 脚本里的 `REPO` 用这个，不是 module path。
- **平台矩阵正好 6 项**：`darwin/arm64`、`darwin/amd64`、`linux/amd64`、`linux/arm64`、`windows/amd64`、`windows/arm64`。
- **归档格式**：Windows 用 `.zip`，包内文件名 `handoff.exe`；darwin 与 linux 用 `.tar.gz`，包内文件名 `handoff`。
- **资产命名**：`handoff_<tag>_<goos>_<goarch><ext>`，外加一个 `checksums.txt`。这是 workflow 产出、`install.sh`、`install.ps1`、自更新四处消费的契约，任一处改动必须四处同步。
- **`CGO_ENABLED=0` 所有构建都要显式设**。macOS runner 上 CGO 默认是开的，一旦开启产物会动态链接系统库并被打上构建机的最低系统版本约束，症状要等到用户的老 mac 上才出现。
- **Windows 上 handoff 只能当协调者**。agentd 的进程承载层在非 unix 平台全部返回 not implemented（backlog B37，维持「已评估·暂不做」）。本次不推进 B37。
- **`install.ps1` 必须同时兼容 PowerShell 5.1 与 7**。Windows 自带的就是 5.1。
- **日志**：Go 侧用 `log/slog`（`internal/release` 里是 `Installer.Log`），**禁止 `fmt.Printf` 当日志**。CLI 面向用户的 `fmt.Fprintln(w, ...)` 是产品输出，不是日志，两者都要有。
- **注释**：新建文件写文件头（职责 + 边界，含「不做什么」）；导出函数写 doc（参数、返回、注意事项）；非显然的分支写「为什么」。一律中文。
- **每个 task 结束即 commit**，提交信息说清做了什么与为什么。
- **验证命令**（每个 task 至少跑相关的那几条）：

```bash
go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go')
```

`gofmt -l` 用 `git ls-files` 限定范围：仓库里可能存在嵌套的 `.claude/worktrees/`，直接 `gofmt -l .` 会扫进去并误报。

---

## 文件结构

| 文件 | 状态 | 职责 |
|---|---|---|
| `.github/workflows/ci.yml` | 新建 | 验证门。PR、push main、以及经 `workflow_call` 被 release 复用 |
| `.github/workflows/release.yml` | 改 | 前置 verify、矩阵 6 项、darwin 签名 job、release notes 取自 CHANGELOG |
| `release_workflow_test.go` | 改 | workflow 契约断言（反转旧的「不得含 windows」，新增两条「不可摘」） |
| `internal/release/client.go` | 改 | `AssetName` 按平台选扩展名 |
| `internal/release/install.go` | 改 | `extractBinary` 按魔数分派 gzip/zip；`TempName` 补 `.exe` |
| `internal/release/client_test.go` | 改 | 扩展名契约测试 |
| `internal/release/install_test.go` | 改 | zip 解包、格式识别、`.exe` 后缀测试 |
| `cmd/init.go` | 改 | `roleOptions(goos)` 纯函数；Windows 上只给协调者 |
| `cmd/init_role_test.go` | 新建 | 角色选项与预选项的平台行为 |
| `install.sh` | 改 | Windows 分支从「B37」改指向 `install.ps1` |
| `install_test.sh` | 改 | Windows 拒绝理由的断言同步 |
| `install.ps1` | 新建 | Windows 一行安装 |
| `install_test.ps1` | 新建 | `install.ps1` 的单元测试 |
| `deploy/install-redirect/worker.js` | 改 | 加 `/install.ps1` 路由 |
| `LICENSE` | 新建 | Apache-2.0 全文 |
| `CHANGELOG.md` | 新建 | Keep a Changelog，回填四个已发 tag |
| `README.md` | 改 | badge、License 章节、Windows 安装、Troubleshooting 两条 |

**依赖顺序**：Task 1（验证门）必须最先，此后每个 task 都被它挡着。Task 2（Go 支持 zip）必须早于 Task 5（release 真的出 zip），否则会有一个「资产发出去了但自更新解不开」的中间态。Task 4 会往 Task 1 建的 `ci.yml` 里加 Windows job。

---

## Task 1: 验证门（`ci.yml` + release 前置）

**Files:**
- Create: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`
- Test: `release_workflow_test.go`

**Interfaces:**
- Consumes: 无（第一个 task）
- Produces: `ci.yml` 里名为 `go` 的 job（Task 4 会在同文件里加名为 `powershell` 的 job）；`release.yml` 里名为 `verify` 的 job（后续 task 新增的构建 job 都要 `needs: verify`）；测试辅助函数 `releaseJobs(t) map[string]wfJob`、`needsSet(any) map[string]bool`、`dependsOnVerify(map[string]wfJob, string, map[string]bool) bool`、`readCI(t) string`

- [ ] **Step 1: 写失败的测试**

在 `release_workflow_test.go` 末尾追加。注意这个文件已有 `readWorkflow(t)` 辅助函数，直接复用；新加的 YAML 解析辅助另起。

```go
// wfJob 是 workflow 里一个 job 的关键字段。
//
// Needs 声明成 any：GitHub Actions 允许它是单个字符串，也允许是字符串数组，
// 两种写法都合法且都会在真实 workflow 里出现。
//
// Strategy 与 Env 是给平台矩阵断言用的：平台清单分散在两处（交叉编译 job 的
// matrix，与签名 job 的 DARWIN_ARCHES），解析结构比 grep 字符串稳。
type wfJob struct {
	Uses     string            `yaml:"uses"`
	RunsOn   string            `yaml:"runs-on"`
	Needs    any               `yaml:"needs"`
	Env      map[string]string `yaml:"env"`
	Strategy struct {
		Matrix struct {
			Include []struct {
				Goos   string `yaml:"goos"`
				Goarch string `yaml:"goarch"`
			} `yaml:"include"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
}

// releaseJobs 解析 release.yml 的 jobs 段。
func releaseJobs(t *testing.T) map[string]wfJob {
	t.Helper()
	b, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("读 release.yml 失败: %v", err)
	}
	var doc struct {
		Jobs map[string]wfJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("release.yml 不是合法 YAML: %v", err)
	}
	return doc.Jobs
}

// needsSet 把 needs 字段归一成集合。
func needsSet(v any) map[string]bool {
	out := map[string]bool{}
	switch n := v.(type) {
	case string:
		out[n] = true
	case []any:
		for _, e := range n {
			if s, ok := e.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}

// dependsOnVerify 判断某 job 的 needs 闭包里是否含 verify。
//
// 判闭包而不是判直接依赖：release job 依赖 build、build 依赖 verify，
// 这已经被挡住了，不该强迫它再直接写一遍 verify。
func dependsOnVerify(jobs map[string]wfJob, name string, seen map[string]bool) bool {
	if seen[name] {
		return false
	}
	seen[name] = true
	for dep := range needsSet(jobs[name].Needs) {
		if dep == "verify" || dependsOnVerify(jobs, dep, seen) {
			return true
		}
	}
	return false
}

// readCI 读 ci.yml 原文，并顺带验证它是合法 YAML。
func readCI(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("读 ci.yml 失败: %v", err)
	}
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("ci.yml 不是合法 YAML: %v", err)
	}
	return string(b)
}

// 每个 job 都必须被验证门挡着。
//
// 这条守的是「删掉之后一切照常绿、只有用户遭殃」的东西：把 verify 摘掉，
// release 会跑得更快、照样出资产，只是从此没有任何测试挡在推 tag 之前——
// 而 release_workflow_test.go 与 install_test.sh 恰恰是专为发布路径写的。
func TestEveryReleaseJobIsGatedByVerify(t *testing.T) {
	jobs := releaseJobs(t)
	v, ok := jobs["verify"]
	if !ok {
		t.Fatal("release.yml 缺 verify job")
	}
	if v.Uses != "./.github/workflows/ci.yml" {
		t.Fatalf("verify 必须复用 ci.yml（写两份定义必然漂移），实得 uses=%q", v.Uses)
	}
	for name := range jobs {
		if name == "verify" {
			continue
		}
		if !dependsOnVerify(jobs, name, map[string]bool{}) {
			t.Fatalf("job %q 的 needs 闭包里没有 verify —— 验证门挡不住它", name)
		}
	}
}

// 验证门的内容不能被悄悄掏空。
func TestCIGateCoversFullCheckSuite(t *testing.T) {
	ci := readCI(t)
	for _, want := range []string{
		"workflow_call",
		"go build ./...",
		"go vet ./...",
		"go test ./... -count=1",
		"gofmt -l",
		"GOOS=windows GOARCH=amd64 go build ./...",
		"GOOS=windows GOARCH=arm64 go build ./...",
		"bash install_test.sh",
	} {
		if !strings.Contains(ci, want) {
			t.Fatalf("ci.yml 缺检查项 %q", want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test . -run 'TestEveryReleaseJobIsGatedByVerify|TestCIGateCoversFullCheckSuite' -v`
Expected: FAIL，两条都因为 `读 ci.yml 失败` / `缺 verify job`

- [ ] **Step 3: 建 `ci.yml`**

```yaml
# PR 与 push main 上的验证门，同时经 workflow_call 供 release.yml 复用。
#
# 职责：
#   - 在任何代码进 main 之前、以及任何 tag 出资产之前，跑同一套检查
#
# 边界：
#   - 不产出任何资产、不发布任何东西——那是 release.yml 的事
#   - 与 release 的前置校验必须是**同一份定义**：写两份必然漂移，
#     且方向总是 release 那份更松（推 tag 时没人盯着它）
name: ci

on:
  pull_request:
  push:
    branches: [main]
  workflow_call:

jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: 构建
        run: go build ./...
      - name: 静态检查
        run: go vet ./...
      - name: 测试
        run: go test ./... -count=1
      - name: 格式
        # gofmt -l 对未格式化的文件只打印文件名、退出码仍是 0，
        # 直接当命令用等于没有这道门，必须自己判空。
        # 用 git ls-files 限定范围：仓库里可能存在嵌套的 .claude/worktrees/，
        # 直接 gofmt -l . 会把它们扫进来误报。
        run: |
          set -euo pipefail
          out="$(gofmt -l $(git ls-files '*.go'))"
          if [ -n "$out" ]; then
            printf '以下文件未格式化：\n%s\n' "$out" >&2
            exit 1
          fi
      - name: Windows 交叉编译门禁
        # agentd 在 Windows 上跑不起来（B37），但协调者路径必须能编译——
        # 从 B36 起这就是硬约束，B86 之后 Windows 还要真发资产，两个 arch 都要过
        run: |
          set -euo pipefail
          GOOS=windows GOARCH=amd64 go build ./...
          GOOS=windows GOARCH=arm64 go build ./...
      - name: install.sh 单测
        run: bash install_test.sh
```

- [ ] **Step 4: 给 `release.yml` 加前置 verify**

在 `jobs:` 下、`build:` 之前插入 verify，并给 `build` 加 `needs`：

```yaml
jobs:
  # 发布前必须过与 PR 同一套验证门。复用而非复制：写两份定义必然漂移，
  # 且方向总是这边更松——推 tag 时没人盯着它。
  verify:
    uses: ./.github/workflows/ci.yml

  build:
    needs: verify
    runs-on: ubuntu-latest
```

（`build` 的其余内容本 task 不动；`release` job 已有 `needs: build`，闭包里就有 verify，不用改。）

- [ ] **Step 5: 跑测试确认通过**

Run: `go test . -run 'TestEveryReleaseJobIsGatedByVerify|TestCIGateCoversFullCheckSuite' -v`
Expected: PASS

- [ ] **Step 6: 跑全量验证**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go')`
Expected: 全绿，`gofmt -l` 无输出

- [ ] **Step 7: 补注释自检**

确认：`ci.yml` 有文件头（职责 + 边界）；`gofmt` 那步的「为什么要判空」与「为什么用 git ls-files」两条 why 注释在；`release.yml` 的 verify job 有「为什么复用而非复制」的注释；三个新测试各有说明它守什么的注释。

本 task 不涉及 Go 运行时行为，无日志步骤。

- [ ] **Step 8: Commit**

```bash
git add .github/workflows/ci.yml .github/workflows/release.yml release_workflow_test.go
git commit -m "ci(b86): 加验证门，release 复用同一份定义

此前仓库只有 release.yml 一个 workflow 且不跑 go test——B54.1 专为发布
路径写的 release_workflow_test.go 与 install_test.sh 在推 tag 时一次都
不执行。ci.yml 用 workflow_call 同时供 PR 与 release 消费，写两份必然
漂移且方向总是 release 那份更松。

新增两条契约断言守「删掉之后一切照常绿、只有用户遭殃」的东西：每个
release job 的 needs 闭包必须含 verify；验证门的检查项不能被掏空。"
```

---

## Task 2: `internal/release` 支持 zip 与 `.exe`

**Files:**
- Modify: `internal/release/client.go:53-67`（`AssetName`）
- Modify: `internal/release/install.go:42-48`（`TempName`）、`:128-161`（`InstallArchive`）、`:220-249`（`extractBinary`）
- Test: `internal/release/client_test.go`、`internal/release/install_test.go`

**Interfaces:**
- Consumes: Task 1 的验证门（无代码依赖）
- Produces:
  - `func AssetName(tag, goos, goarch string) string` —— 签名不变，行为变（Windows 返回 `.zip`）
  - `func archiveExt(goos string) string` —— 包内
  - `func TempName(tag string) string` —— 签名不变；新增包内 `func tempName(tag, goos string) string`
  - `func extractBinary(data []byte, dest string) (format string, err error)` —— **返回值从 1 个变 2 个**，新增的 `format` 是 `"tar.gz"` 或 `"zip"`，供 `InstallArchive` 打进日志
  - `var binaryNames = map[string]bool{"handoff": true, "handoff.exe": true}` —— 包内
  - `InstallArchive` / `Fetch` / `FetchArchive` / `Activate` 四个导出函数的签名**都不变**

- [ ] **Step 1: 写失败的测试（资产扩展名）**

追加到 `internal/release/client_test.go`：

```go
// AssetName 的扩展名是与 release.yml 的契约。Windows 出 zip 而非 tar.gz：
// zip 在资源管理器里双击即开，且 Expand-Archive 人人都有，tar.exe 只有
// Win10 1803+ 才有。改这里必须同步改 workflow 与两个 install 脚本。
func TestAssetNameExtensionPerOS(t *testing.T) {
	for _, c := range []struct{ goos, goarch, want string }{
		{"darwin", "arm64", "handoff_v1.2.3_darwin_arm64.tar.gz"},
		{"darwin", "amd64", "handoff_v1.2.3_darwin_amd64.tar.gz"},
		{"linux", "amd64", "handoff_v1.2.3_linux_amd64.tar.gz"},
		{"linux", "arm64", "handoff_v1.2.3_linux_arm64.tar.gz"},
		{"windows", "amd64", "handoff_v1.2.3_windows_amd64.zip"},
		{"windows", "arm64", "handoff_v1.2.3_windows_arm64.zip"},
	} {
		if got := AssetName("v1.2.3", c.goos, c.goarch); got != c.want {
			t.Errorf("AssetName(v1.2.3, %s, %s) = %q，期望 %q", c.goos, c.goarch, got, c.want)
		}
	}
}
```

- [ ] **Step 2: 写失败的测试（解包与临时文件名）**

追加到 `internal/release/install_test.go`。这个文件已有 `makeTarGz(t, script)` 造 tar.gz 的辅助，新增一个造 zip 的：

```go
// makeZip 造一个含单个指定文件名的 zip。
//
// 参数：
//   - name: 包内文件名（用来覆盖 handoff.exe 与「包里没有目标文件」两种情形）
//   - content: 文件内容
func makeZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("建 zip 条目失败: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("写 zip 条目失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
	return buf.Bytes()
}

// Windows 资产是 zip，包内是 handoff.exe，两者都要认。
func TestExtractBinaryReadsZip(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	format, err := extractBinary(makeZip(t, "handoff.exe", []byte("BINARY")), dest)
	if err != nil {
		t.Fatalf("解 zip 失败: %v", err)
	}
	if format != "zip" {
		t.Fatalf("format = %q，期望 zip", format)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("读解出的文件失败: %v", err)
	}
	if string(b) != "BINARY" {
		t.Fatalf("解出的内容 = %q，期望 BINARY", b)
	}
}

// darwin/linux 资产仍是 tar.gz，包内是 handoff。
func TestExtractBinaryReadsTarGz(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	format, err := extractBinary(makeTarGz(t, "#!/bin/sh\necho hi\n"), dest)
	if err != nil {
		t.Fatalf("解 tar.gz 失败: %v", err)
	}
	if format != "tar.gz" {
		t.Fatalf("format = %q，期望 tar.gz", format)
	}
}

// 格式按魔数判定，不按调用方传的平台——所以「两者都不是」必须有明确报文，
// 否则症状会是一句无从下手的 gzip 解析错误。
func TestExtractBinaryRejectsUnknownFormat(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	_, err := extractBinary([]byte("not an archive at all"), dest)
	if err == nil {
		t.Fatal("无法识别的格式应当报错")
	}
	if !strings.Contains(err.Error(), "既不是 gzip 也不是 zip") {
		t.Fatalf("报文应说清两种格式都不匹配，实得 %v", err)
	}
}

// tgzNamed 造一个含单个指定文件名的 tar.gz。
//
// 与既有的 tgzWith 的区别只在于文件名可指定：tgzWith 把名字写死成 handoff，
// 测不了「包里没有目标文件」这一支。
func tgzNamed(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// 包里没有可执行文件时两种格式都要报错，不能留一个空文件在目标路径上。
func TestExtractBinaryRejectsArchiveWithoutBinary(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	if _, err := extractBinary(makeZip(t, "README.txt", []byte("x")), dest); err == nil {
		t.Fatal("zip 里没有 handoff.exe 时应当报错")
	}
	if _, err := extractBinary(tgzNamed(t, "README.txt", "x"), dest); err == nil {
		t.Fatal("tar.gz 里没有 handoff 时应当报错")
	}
}

// Windows 上临时文件必须以 .exe 结尾：selfCheck 要 exec 它跑 version，
// 没有该后缀的文件在 Windows 上起不来——症状是「自检失败」，真因是文件名。
func TestTempNameHasExeSuffixOnWindows(t *testing.T) {
	if got := tempName("v1.2.3", "windows"); got != ".handoff.new-v1.2.3.exe" {
		t.Fatalf("windows: tempName = %q，期望 .handoff.new-v1.2.3.exe", got)
	}
	for _, goos := range []string{"darwin", "linux"} {
		if got := tempName("v1.2.3", goos); got != ".handoff.new-v1.2.3" {
			t.Fatalf("%s: tempName = %q，期望 .handoff.new-v1.2.3", goos, got)
		}
	}
}
```

`makeTarGz(t, script)` 是该文件里已有的辅助（造一个含名为 `handoff` 的 tar.gz），直接复用。`tgzWith` 也是已有的，但它把文件名写死成 `handoff`，所以上面另加了 `tgzNamed`——**不要改 tgzWith 的签名**，它有既有调用方。

新增 import 只有 `archive/zip`：`bytes` / `archive/tar` / `compress/gzip` / `strings` / `os` / `path/filepath` 在该文件里都已 import。

- [ ] **Step 3: 跑测试确认它失败**

Run: `go test ./internal/release/ -run 'AssetName|ExtractBinary|TempName' -v`
Expected: 编译失败（`extractBinary` 只返回一个值、`tempName` 未定义、`makeZip` 未定义前是可以编译的但断言会挂）——**编译不过也算 FAIL，继续下一步**

- [ ] **Step 4: 改 `AssetName`**

`internal/release/client.go`，把现有 `AssetName` 替换为：

```go
// archiveExt 返回某平台的归档扩展名。
//
// Windows 用 zip 而非 tar.gz：zip 在资源管理器里双击即开，而 tar.gz 必须敲
// 命令行；且 Expand-Archive 存在于每一个 PowerShell，tar.exe 只有 Win10
// 1803+ 才有。手动下载是 Windows 用户的常见路径，这个差异值得多一种格式。
func archiveExt(goos string) string {
	if goos == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// AssetName 拼装某平台的资产名。
//
// 参数：
//   - tag: 版本号，形如 v0.1.0
//   - goos / goarch: 目标平台
//
// 返回：
//   - 资产文件名
//
// 注意：
//   - 格式必须与 .github/workflows/release.yml 里的产出**逐字一致**。
//     不一致的症状是查得到版本但下不到东西，且每轮重试
//   - 扩展名按平台分（见 archiveExt），install.sh / install.ps1 两边也依赖这条
func AssetName(tag, goos, goarch string) string {
	return fmt.Sprintf("handoff_%s_%s_%s%s", tag, goos, goarch, archiveExt(goos))
}
```

同时把 `AssetFor` 的 doc 注释里「例如 Windows，B37 未支持」改成「例如某次发布漏了某平台」——B86 之后 Windows 是有资产的，这句话已过时。

- [ ] **Step 5: 改 `TempName`**

`internal/release/install.go`，替换现有 `TempName`：

```go
// TempName 返回某版本的临时文件名。
//
// 前导点让它在目录列表里不显眼；带上 tag 使多次尝试不同版本时互不覆盖。
// Windows 上追加 .exe——selfCheck 要 exec 这个临时文件跑 version，
// 没有该后缀的文件在 Windows 上起不来。
func TempName(tag string) string { return tempName(tag, runtime.GOOS) }

// tempName 是 TempName 的可测实现，平台由调用方给定。
//
// 拆出这一层的唯一理由是可测性：判据写死成 runtime.GOOS 时，
// 「Windows 上带 .exe」这条行为在非 Windows 的 CI 上永远测不到。
func tempName(tag, goos string) string {
	name := ".handoff.new-" + tag
	if goos == "windows" {
		name += ".exe"
	}
	return name
}
```

新增 import `runtime`。

- [ ] **Step 6: 改 `extractBinary` 为按魔数分派**

`internal/release/install.go`，把现有 `extractBinary` 整个替换：

```go
// gzipMagic / zipMagic 是两种归档的文件头。
var (
	gzipMagic = []byte{0x1f, 0x8b}
	zipMagic  = []byte{'P', 'K', 0x03, 0x04}
)

// binaryNames 是归档内可接受的可执行文件名。
//
// Windows 资产里是 handoff.exe，其余平台是 handoff。两个都认而不按平台分派，
// 理由同 extractBinary：判据来自归档本身，不来自调用方对平台的声明。
var binaryNames = map[string]bool{"handoff": true, "handoff.exe": true}

// extractBinary 从归档里取出 handoff 可执行文件写到 dest。
//
// 参数：
//   - data: 归档原文（tar.gz 或 zip）
//   - dest: 落点路径
//
// 返回：
//   - format: 实际识别出的格式（"tar.gz" / "zip"），供调用方打进日志
//   - 错误：格式不认、解包失败、包内没有可执行文件
//
// 注意：
//   - 格式**按魔数判定，不按调用方传入的平台**。传平台会制造第二个真相来源，
//     一旦它与实际字节不符，报错会指向错误的方向；字节才是权威。这条选择的
//     额外好处是 InstallArchive / Fetch / FetchArchive 三个签名都不用动
func extractBinary(data []byte, dest string) (string, error) {
	switch {
	case bytes.HasPrefix(data, gzipMagic):
		return "tar.gz", extractFromTarGz(data, dest)
	case bytes.HasPrefix(data, zipMagic):
		return "zip", extractFromZip(data, dest)
	default:
		head := data
		if len(head) > 4 {
			head = head[:4]
		}
		return "", fmt.Errorf("无法识别的归档格式：既不是 gzip 也不是 zip（前 %d 字节 %x）", len(head), head)
	}
}

// extractFromTarGz 从 tar.gz 里取出可执行文件写到 dest。
func extractFromTarGz(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("包里没有名为 handoff / handoff.exe 的文件")
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if !binaryNames[filepath.Base(h.Name)] || h.Typeflag != tar.TypeReg {
			continue
		}
		return writeExtracted(tr, dest)
	}
}

// extractFromZip 从 zip 里取出可执行文件写到 dest。
func extractFromZip(data []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !binaryNames[filepath.Base(f.Name)] {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("zip 打开 %s: %w", f.Name, err)
		}
		defer rc.Close()
		return writeExtracted(rc, dest)
	}
	return errors.New("包里没有名为 handoff / handoff.exe 的文件")
}

// writeExtracted 把归档条目的内容写到 dest，带大小上限。
//
// 抽出来是因为两种格式的写盘部分完全一样，而这段恰好是唯一会在磁盘上
// 留下痕迹的地方——只有一处，出问题时也只需要看一处。
func writeExtracted(r io.Reader, dest string) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, io.LimitReader(r, maxAssetBytes))
	return err
}
```

新增 import：`archive/zip`、`bytes`。移除不再需要的 `strings`（若该文件其他地方仍在用 `strings`，如 `sumFor`，则保留）。

- [ ] **Step 7: 改 `InstallArchive` 的调用点并打上格式日志**

`internal/release/install.go` 的 `InstallArchive` 里，把：

```go
	if err := extractBinary(tgz, tmp); err != nil {
```

改成：

```go
	format, err := extractBinary(tgz, tmp)
	if err != nil {
```

并在 `os.Chmod` 之前补一条 Info（**这是本 task 的日志步骤**）：

```go
	// 装的到底是 zip 还是 tar.gz 是排查「资产格式与平台不符」时的第一个问题，
	// 而它此前只能靠资产名去猜
	i.Log.Info("归档解包完成", "tag", wantTag, "format", format, "path", tmp)
```

错误分支已有 `i.Log.Error("安装被拒：解包失败", ..., "cause", err)`，保持不变——`extractBinary` 返回的错误已带足上下文，**不要**再给它加 logger 参数。

- [ ] **Step 8: 跑测试确认通过**

Run: `go test ./internal/release/ -count=1 -v`
Expected: 全部 PASS，包括原有用例

- [ ] **Step 9: 跑全量验证**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go')`
Expected: 全绿

- [ ] **Step 10: 给 `Activate` 补一条防误改的注释**

`internal/release/install.go` 的 `Activate` doc 注释「注意」段末尾追加一条：

```go
//   - **两次 rename 的顺序在 Windows 上是承重的**：Windows 允许 rename 一个
//     正在运行的 exe，但不允许覆盖或删除它。所以「先把旧的挪走、再把新的挪进来」
//     恰好就是 Windows 自更新的标准手法。**不要**把它「优化」成先删后写——
//     那在 unix 上照样绿，在 Windows 上当场炸
```

- [ ] **Step 11: 注释与日志自检**

对照清单确认：`archiveExt` / `tempName` / `extractBinary` / `extractFromZip` / `writeExtracted` / `binaryNames` 各有说明「为什么」的注释；`extractBinary` 的 doc 写清了参数、返回、以及「按魔数不按平台」的理由；`InstallArchive` 的成功路径有格式日志（非静默）；错误分支保持带 cause；无 `fmt.Printf` 当日志。

- [ ] **Step 12: Commit**

```bash
git add internal/release/
git commit -m "feat(b86): release 包支持 Windows 的 zip 资产与 .exe 临时文件

- AssetName 按平台选扩展名：Windows .zip，其余 .tar.gz
- extractBinary 按魔数分派 gzip/zip，不按调用方传的平台——传平台会制造
  第二个真相来源，与字节不符时报错会指向错误的方向。副作用是
  InstallArchive / Fetch / FetchArchive 三个签名一个都不用动
- TempName 在 Windows 上补 .exe：selfCheck 要 exec 它跑 version，没有该
  后缀在 Windows 上起不来，症状会是「自检失败」而真因是文件名
- 解包成功打一条带 format 的日志：装的是 zip 还是 tar.gz 此前只能靠资产名猜
- Activate 补注释钉死两次 rename 的顺序：它恰好是 Windows 自更新的标准
  手法，改成先删后写在 unix 上照样绿、在 Windows 上当场炸"
```

---

## Task 3: `handoff init` 在 Windows 上只给协调者

**Files:**
- Modify: `cmd/init.go:205-218`（`askAll` 的角色问答）、`:273-296`（`defaultRole`）
- Test: `cmd/init_role_test.go`（新建）

**Interfaces:**
- Consumes: 无
- Produces:
  - `func roleOptions(goos string) []promptOption` —— 包内
  - `func defaultRole(cfg *config.Config, cfgExisted bool, rs []toolchain.Result, goos string) string` —— **多一个 goos 参数**
  - 既有常量 `roleExecutor` / `roleCoordinator` / `roleBoth` 与类型 `promptOption{Value, Label string}` 不变

- [ ] **Step 1: 写失败的测试**

新建 `cmd/init_role_test.go`：

```go
// 角色选项与预选项的平台行为测试。
//
// 为什么单独一个文件：这两条行为的判据是 GOOS，而 CI 跑在 linux 上——
// 只有把判据参数化才能测到 Windows 分支，测试本身就是这个设计的理由。
package cmd

import (
	"testing"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/toolchain"
)

// Windows 上选执行机会一路走到 service install 才撞墙（agentd 的进程承载层
// 在非 unix 平台返回 not implemented，B37）。不如在这里就不给这个选项。
func TestRoleOptionsWindowsOnlyCoordinator(t *testing.T) {
	got := roleOptions("windows")
	if len(got) != 1 {
		t.Fatalf("Windows 上应只有一个角色选项，实得 %d 个: %+v", len(got), got)
	}
	if got[0].Value != roleCoordinator {
		t.Fatalf("Windows 上唯一的角色应是协调者，实得 %q", got[0].Value)
	}
}

func TestRoleOptionsUnixHasAllThree(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		got := roleOptions(goos)
		if len(got) != 3 {
			t.Fatalf("%s 上应有三个角色选项，实得 %d 个: %+v", goos, len(got), got)
		}
	}
}

// 预选项必须落在 roleOptions 给出的列表里，否则 huh 拿一个不在列表里的
// 默认值去匹配，选中项会落空——B83 刚踩过一次同类问题。
func TestDefaultRoleOnWindowsIgnoresProbe(t *testing.T) {
	rs := []toolchain.Result{{Name: "opencode", State: toolchain.StateReady}}
	if got := defaultRole(&config.Config{}, false, rs, "windows"); got != roleCoordinator {
		t.Fatalf("Windows 预选角色应为协调者，实得 %q", got)
	}
	// 同样的输入在 darwin 上仍应预选执行机——证明上一条不是因为探测结果为空
	if got := defaultRole(&config.Config{}, false, rs, "darwin"); got != roleExecutor {
		t.Fatalf("darwin 预选角色应为执行机，实得 %q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./cmd/ -run 'RoleOptions|DefaultRole' -v`
Expected: 编译失败（`roleOptions` 未定义、`defaultRole` 参数个数不符）

- [ ] **Step 3: 加 `roleOptions`**

在 `cmd/init.go` 里 `defaultRole` 之前插入：

```go
// roleOptions 按平台给出可选角色。
//
// 参数：
//   - goos: 目标平台。参数化而非直接读 runtime.GOOS 是为了可测——
//     判据写死则 Windows 分支在 linux 的 CI 上永远测不到
//
// 返回：
//   - 角色选项列表
//
// 注意：
//   - Windows 上只有协调者。agentd 的进程承载层在非 unix 平台全部返回
//     not implemented（backlog B37），选执行机要一路走到 service install
//     才撞墙——不给这个选项比给一个走不通的选项诚实
func roleOptions(goos string) []promptOption {
	if goos == "windows" {
		return []promptOption{{Value: roleCoordinator, Label: "协调者"}}
	}
	return []promptOption{
		{Value: roleExecutor, Label: "执行机"},
		{Value: roleCoordinator, Label: "协调者"},
		{Value: roleBoth, Label: "两者"},
	}
}
```

- [ ] **Step 4: 给 `defaultRole` 加 goos 参数**

把签名改成 `func defaultRole(cfg *config.Config, cfgExisted bool, rs []toolchain.Result, goos string) string`，并在函数体最开头插入：

```go
	// 预选项必须落在 roleOptions 给出的列表里：Windows 上那个列表只有协调者，
	// 预选成执行机会让 huh 拿一个不在列表里的值去匹配，选中项落空
	if goos == "windows" {
		return roleCoordinator
	}
```

doc 注释末尾追加一句：`// Windows 上无条件返回协调者，见 roleOptions。`

- [ ] **Step 5: 改 `askAll` 的调用点，并加提示与日志**

`cmd/init.go:208-213` 那一段替换为：

```go
	if runtime.GOOS == "windows" {
		// 产品输出：用户必须当场知道为什么只有一个选项，否则会以为是 bug
		fmt.Fprintln(w, "\n注意：Windows 上 handoff 只能当协调者——agentd 的进程承载层在非 unix 平台尚未实现（backlog B37），执行机角色跑不起来。")
		slog.Info("Windows 平台：角色选项限定为协调者", "reason", "agentd 进程承载层未实现（B37）")
	}
	defRole := defaultRole(cfg, cfgExisted, rs, runtime.GOOS)
	role, err := p.Select("这台机器的角色", roleOptions(runtime.GOOS), defRole)
```

`runtime` 与 `log/slog` 在 `cmd/init.go` 里是否已 import：`runtime` 已有（`:399` 在用）；`slog` 若未 import 则加上。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./cmd/ -run 'RoleOptions|DefaultRole' -v`
Expected: PASS

- [ ] **Step 7: 跑全量验证**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go')`
Expected: 全绿。特别确认 `cmd/init_huh_test.go` 与既有 init 用例仍绿（它们调 `renderHuhSelect`，不经 `roleOptions`，应不受影响）。

- [ ] **Step 8: 注释与日志自检**

确认：`roleOptions` 有完整 doc（参数、返回、以及「为什么参数化」）；`defaultRole` 的 Windows 早返回有 why 注释；Windows 分支既有面向用户的 stdout 提示、又有一条带 reason 的 `slog.Info`（状态相关的分支不静默）；新测试文件有文件头注释。

- [ ] **Step 9: Commit**

```bash
git add cmd/init.go cmd/init_role_test.go
git commit -m "feat(b86): init 在 Windows 上只给协调者角色

Windows 上选执行机会一路走到 service install 才撞墙（agentd 的进程承载
层在非 unix 平台返回 not implemented，B37）。不给这个选项比给一个走不通
的选项诚实。

判据抽成 roleOptions(goos) / defaultRole(..., goos) 两个带参数的函数而不
是直接读 runtime.GOOS——否则这条行为在 linux 的 CI 上永远测不到。预选项
同步限定为协调者：预选一个不在选项列表里的值会让 huh 的选中项落空。"
```

---

## Task 4: `install.ps1` 与它的测试

**Files:**
- Create: `install.ps1`
- Create: `install_test.ps1`
- Modify: `.github/workflows/ci.yml`（加 Windows job）
- Modify: `install.sh:52-54`（Windows 分支改指向）
- Modify: `install_test.sh:47-54`（断言同步）
- Modify: `deploy/install-redirect/worker.js`（加 `/install.ps1` 路由）
- Test: `install_test.ps1` 自身

**Interfaces:**
- Consumes: Task 2 的资产命名约定（Windows → `handoff_<tag>_windows_<arch>.zip`，包内 `handoff.exe`）；Task 1 的 `ci.yml`
- Produces: `install.ps1` 里的三个可测函数 `Get-HandoffArch`、`Get-HandoffInstallDir`、`Test-HandoffChecksum`；`ci.yml` 里名为 `powershell` 的 job

- [ ] **Step 1: 写 `install.ps1`**

```powershell
#!/usr/bin/env pwsh
# handoff 的 Windows 一行安装脚本。
#
# 用法：irm https://handoff.gosuper.dev/install.ps1 | iex
#
# 职责：
#   - 探测架构，从 GitHub Release 拉对应的 zip，校验 sha256，装到 %LOCALAPPDATA%\Programs\handoff
#
# 边界：
#   - 只在「本机还没有 handoff」时用一次；后续换版走 handoff upgrade
#   - **不改 PATH、不写服务、不提权**——与 install.sh 的边界逐条一致
#   - Windows 上 handoff 只能当协调者：agentd 的进程承载层在非 unix 平台
#     尚未实现（backlog B37），派发目标必须是一台 macOS/Linux 执行机
#
# 环境变量：
#   HANDOFF_INSTALL_DIR  覆盖安装目录
#   HANDOFF_INSTALL_LIB  设为 1 时只定义函数不执行主流程（供 install_test.ps1 用）
#
# 兼容性：必须同时在 Windows 自带的 PowerShell 5.1 与 PowerShell 7 上可用。
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Repo = 'Xsxdot/handoff'

# Write-Log 输出到 stderr：stdout 留给可能被管道消费的内容。
function Write-Log([string]$Message) {
    [Console]::Error.WriteLine($Message)
}

# Stop-Install 打印失败原因后抛出。
#
# 每个失败分支都必须经它退出——脚本挂掉时用户能看到的只有这一行，
# 缺上下文的「安装失败」等于让用户去猜网络、权限还是架构。
function Stop-Install([string]$Message) {
    throw "handoff 安装失败：$Message"
}

# Get-HandoffArch 把处理器架构归一成 Release 资产用的 arch 名。
#
# 返回：amd64 或 arm64
#
# 注意：不在矩阵内的架构一律抛错。静默装一个跑不起来的二进制，
# 比当场报错糟得多——症状会推迟到运行时才出现，且看不出根因。
function Get-HandoffArch {
    switch ($env:PROCESSOR_ARCHITECTURE) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { Stop-Install "不支持的架构 $($env:PROCESSOR_ARCHITECTURE)（仅 AMD64/ARM64）" }
    }
}

# Get-HandoffInstallDir 解析安装目录。
#
# 默认 %LOCALAPPDATA%\Programs\handoff——这是 Windows 上的用户级安装惯例，
# 无需管理员权限。install.sh 用的 ~/.local/bin 在 Windows 上没有工具认。
function Get-HandoffInstallDir {
    if ($env:HANDOFF_INSTALL_DIR) { return $env:HANDOFF_INSTALL_DIR }
    return (Join-Path $env:LOCALAPPDATA 'Programs\handoff')
}

# Get-LatestTag 解析 releases/latest 的重定向，取最新 tag。
#
# 返回：形如 v0.1.0
#
# why（不打 api.github.com）：匿名 API 限流 60 次/小时/IP，安装这条路径
# 不该被限流影响。重定向没有限流。
#
# why（两套取 URL 的写法）：PowerShell 5.1 的 BaseResponse 是 HttpWebResponse
# （用 .ResponseUri），7 上是 HttpResponseMessage（用 .RequestMessage.RequestUri）。
# 只写 7 的写法会让脚本在绝大多数 Windows 机器上第一步就挂。
function Get-LatestTag {
    $url = "https://github.com/$Repo/releases/latest"
    try {
        $resp = Invoke-WebRequest -Uri $url -UseBasicParsing -MaximumRedirection 10
    } catch {
        Stop-Install "取最新版本失败：连不上 github.com（$($_.Exception.Message)）"
    }
    $final = $null
    if ($resp.BaseResponse.PSObject.Properties.Name -contains 'ResponseUri') {
        $final = $resp.BaseResponse.ResponseUri.AbsoluteUri
    } elseif ($resp.BaseResponse.PSObject.Properties.Name -contains 'RequestMessage') {
        $final = $resp.BaseResponse.RequestMessage.RequestUri.AbsoluteUri
    }
    if (-not $final) { Stop-Install '取最新版本失败：无法从响应里取出最终地址' }
    $tag = $final.Split('/')[-1]
    # 仓库一个 release 都没有时，GitHub 重定向到 .../releases，末段不是版本号
    if ($tag -notmatch '^v') { Stop-Install "取最新版本失败：$Repo 还没有任何 release（重定向到 $final）" }
    return $tag
}

# Test-HandoffChecksum 比对文件的 sha256 与 checksums.txt 里的声明。
#
# 参数：
#   - Path: 待校验的文件
#   - ChecksumsText: checksums.txt 全文
#   - Name: 资产的裸文件名
#
# 返回：校验通过返回 $true；条目缺失或不符抛错（不返回 $false——
# 让调用方忘记检查返回值就装上一个坏包，是这里最不能接受的失败模式）
function Test-HandoffChecksum([string]$Path, [string]$ChecksumsText, [string]$Name) {
    $want = $null
    foreach ($line in $ChecksumsText -split "`n") {
        $f = $line.Trim() -split '\s+'
        if ($f.Count -ne 2) { continue }
        # sha256sum 在二进制模式下会给文件名加 * 前缀，一并容忍
        if ($f[1].TrimStart('*') -eq $Name) { $want = $f[0]; break }
    }
    if (-not $want) { Stop-Install "checksums.txt 里没有 $Name 的条目" }
    $got = (Get-FileHash -Path $Path -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $want.ToLower()) {
        Stop-Install "校验失败：期望 $want，实得 $got。不安装，下载物已清理"
    }
    return $true
}

# Write-NextSteps 打印装完之后该做什么。
function Write-NextSteps([string]$Dir) {
    Write-Log ''
    Write-Log '下一步   handoff init'
    Write-Log '         Windows 上 handoff 只能当协调者，init 会带你配对一台远程执行机。'
    Write-Log '         agentd 在 Windows 上跑不起来（backlog B37），本机不能当执行机。'
    if (($env:PATH -split ';') -notcontains $Dir) {
        Write-Log ''
        Write-Log "注意：$Dir 不在 PATH 里。在 PowerShell 里跑下面这行把它加上（只需一次）："
        Write-Log "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';$Dir', 'User')"
        Write-Log '（本脚本不会去改你的 PATH）'
    }
}

# Invoke-Main 是安装主流程。
function Invoke-Main {
    $arch = Get-HandoffArch
    $tag = Get-LatestTag
    $dir = Get-HandoffInstallDir
    $zip = "handoff_${tag}_windows_${arch}.zip"
    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("handoff-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null
    try {
        Write-Log "handoff $tag  windows_$arch"
        $zipPath = Join-Path $tmp $zip
        Invoke-WebRequest -Uri "https://github.com/$Repo/releases/download/$tag/$zip" -OutFile $zipPath -UseBasicParsing
        $sums = (Invoke-WebRequest -Uri "https://github.com/$Repo/releases/download/$tag/checksums.txt" -UseBasicParsing).Content
        Test-HandoffChecksum -Path $zipPath -ChecksumsText $sums -Name $zip | Out-Null

        Expand-Archive -Path $zipPath -DestinationPath $tmp -Force
        $exe = Join-Path $tmp 'handoff.exe'
        if (-not (Test-Path $exe)) { Stop-Install "包内没有 handoff.exe" }

        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        $dest = Join-Path $dir 'handoff.exe'
        Copy-Item -Path $exe -Destination $dest -Force
        Write-Log "已安装 $dest  $tag"

        # 顺手把 skill 装给本机各家 agent。**必须调刚装好的那个文件**——
        # skill 内嵌在二进制里，调旧的就装旧的。
        # 失败不算安装失败：二进制已经装好了，skill 少一份不影响 CLI 可用。
        try {
            & $dest skill install
        } catch {
            Write-Log "注意：skill 安装失败，可稍后手动跑 `"$dest`" skill install"
        }

        Write-NextSteps -Dir $dir
    } finally {
        Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# 被 install_test.ps1 dot-source 时只定义函数，不执行主流程
if ($env:HANDOFF_INSTALL_LIB -ne '1') { Invoke-Main }
```

- [ ] **Step 2: 写 `install_test.ps1`**

```powershell
#!/usr/bin/env pwsh
# install.ps1 的单元测试：只测能纯函数化的部分（架构归一、安装目录、校验和）。
#
# 用法：pwsh -File install_test.ps1  /  powershell.exe -File install_test.ps1
# 全通过时静默退出 0；有失败时逐条打印期望/实得并退 1。
#
# 边界：不测下载与安装本身——那需要真实 Release，属真机验证。
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$env:HANDOFF_INSTALL_LIB = '1'
. (Join-Path $PSScriptRoot 'install.ps1')

$script:Fails = 0

# Assert-Equal <说明> <期望> <实得>
function Assert-Equal([string]$What, $Expected, $Actual) {
    if ("$Expected" -ne "$Actual") {
        [Console]::Error.WriteLine("FAIL  $What`n      期望 $Expected`n      实得 $Actual")
        $script:Fails++
    }
}

# Assert-Throws <说明> <脚本块>：脚本块必须抛错，否则算失败。
function Assert-Throws([string]$What, [scriptblock]$Block) {
    try {
        & $Block
        [Console]::Error.WriteLine("FAIL  $What`n      期望抛错，实际正常返回")
        $script:Fails++
    } catch {
        # 预期内
    }
}

# 两个受支持的架构都要归一正确
$env:PROCESSOR_ARCHITECTURE = 'AMD64'
Assert-Equal 'AMD64 归一' 'amd64' (Get-HandoffArch)
$env:PROCESSOR_ARCHITECTURE = 'ARM64'
Assert-Equal 'ARM64 归一' 'arm64' (Get-HandoffArch)

# 32 位与其他架构不在矩阵内，必须拒绝而不是装一个跑不起来的包
$env:PROCESSOR_ARCHITECTURE = 'x86'
Assert-Throws 'x86 必须被拒绝' { Get-HandoffArch }

# 安装目录：默认值与环境变量覆盖
$env:HANDOFF_INSTALL_DIR = ''
Assert-Equal '默认安装目录' (Join-Path $env:LOCALAPPDATA 'Programs\handoff') (Get-HandoffInstallDir)
$env:HANDOFF_INSTALL_DIR = 'C:\tmp\hf'
Assert-Equal '安装目录可被环境变量覆盖' 'C:\tmp\hf' (Get-HandoffInstallDir)
$env:HANDOFF_INSTALL_DIR = ''

# 校验和：相符放行、不符拒绝、条目缺失拒绝
$probe = Join-Path ([System.IO.Path]::GetTempPath()) ("hf-probe-" + [Guid]::NewGuid().ToString('N'))
Set-Content -Path $probe -Value 'handoff' -NoNewline
$real = (Get-FileHash -Path $probe -Algorithm SHA256).Hash.ToLower()
Assert-Equal '校验和相符时放行' $true (Test-HandoffChecksum -Path $probe -ChecksumsText "$real  a.zip" -Name 'a.zip')
Assert-Equal 'sha256sum 的 * 前缀也认' $true (Test-HandoffChecksum -Path $probe -ChecksumsText "$real *a.zip" -Name 'a.zip')
Assert-Throws '校验和不符必须拒绝' {
    Test-HandoffChecksum -Path $probe -ChecksumsText ('0' * 64 + '  a.zip') -Name 'a.zip'
}
Assert-Throws '条目缺失必须拒绝' {
    Test-HandoffChecksum -Path $probe -ChecksumsText "$real  b.zip" -Name 'a.zip'
}
Remove-Item -Path $probe -Force -ErrorAction SilentlyContinue

if ($script:Fails -gt 0) {
    [Console]::Error.WriteLine("$($script:Fails) 条失败")
    exit 1
}
exit 0
```

- [ ] **Step 3: 在有 PowerShell 的机器上跑测试**

Run（Windows 上）：`pwsh -File install_test.ps1; echo $LASTEXITCODE`
Expected: 无输出，退出码 0

**如果开发机是 macOS/Linux 且装了 pwsh**，同样可以跑（`$env:LOCALAPPDATA` 会是空，`Join-Path` 仍能拼出路径，断言两边用同一个表达式所以仍成立）。**如果本机完全没有 PowerShell**，这一步交给 Step 5 的 CI 验证，并在提交信息里注明本地未跑。

- [ ] **Step 4: 给 `ci.yml` 加 Windows job**

在 `ci.yml` 的 `jobs:` 下追加：

```yaml
  powershell:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v4
      # 两个版本都要跑，不是冗余：install.ps1 把「兼容 Windows 自带的 PS 5.1」
      # 列为硬要求，而 5.1 与 7 在 Invoke-WebRequest 的响应对象上有实质差异。
      # 只跑 pwsh 等于把这条硬要求写了却不验。
      - name: install.ps1 单测（PowerShell 5.1）
        shell: powershell
        run: .\install_test.ps1
      - name: install.ps1 单测（PowerShell 7）
        shell: pwsh
        run: .\install_test.ps1
```

- [ ] **Step 5: 改 `install.sh` 的 Windows 分支**

`install.sh:53-54` 替换为：

```bash
    MINGW* | MSYS* | CYGWIN* | Windows_NT)
      die "Windows 请改用 PowerShell 安装：irm https://handoff.gosuper.dev/install.ps1 | iex（Windows 上 handoff 只能当协调者，见 README）" ;;
```

同时把文件头「边界」段里那条：

```
#   - 不支持 Windows：agentd 依赖的进程承载层 Windows 实现尚未完成（backlog B37）
```

改为：

```
#   - 不装 Windows：Windows 走 install.ps1（那边装的是 zip 资产里的 handoff.exe）。
#     Windows 上 handoff 只能当协调者——agentd 的进程承载层在非 unix 平台
#     尚未实现（backlog B37）
```

`detect_platform` 的 doc 注释里若提到 B37，一并订正为指向 install.ps1。

- [ ] **Step 6: 改 `install_test.sh` 的断言**

`install_test.sh:47-54` 替换为：

```bash
# Windows 必须被明确拒绝，且理由里要给出路（install.ps1）——
# 只说「不支持」会让用户以为 Windows 根本装不了，而现在它是能装的
out="$( (with_uname Windows_NT x86_64 detect_platform) 2>&1 )" && rc=0 || rc=$?
check "Windows 退出码" "1" "$rc"
case "$out" in
  *install.ps1*) ;;
  *) printf 'FAIL  Windows 的拒绝理由应指向 install.ps1\n      实得 %s\n' "$out" >&2
     fails=$((fails + 1)) ;;
esac
```

- [ ] **Step 7: 跑 install.sh 的测试**

Run: `bash install_test.sh; echo "rc=$?"`
Expected: 无输出，`rc=0`

- [ ] **Step 8: 给 Worker 加 `/install.ps1` 路由**

`deploy/install-redirect/worker.js`：把 `TARGET` 改成两个常量并加分支。

```js
const TARGET_SH = "https://raw.githubusercontent.com/Xsxdot/handoff/main/install.sh";
const TARGET_PS1 = "https://raw.githubusercontent.com/Xsxdot/handoff/main/install.ps1";

export default {
  fetch(request) {
    const { pathname } = new URL(request.url);
    // 302 而不是 301：301 会被浏览器与部分代理长期缓存，
    // 将来想换目标（比如改成打 tag 的固定版本）就撤不回来了
    if (pathname === "/install" || pathname === "/install/") {
      return Response.redirect(TARGET_SH, 302);
    }
    // Windows 入口。两条路径分开而不是按 User-Agent 猜：
    // 用户敲的命令本身已经说清了要哪一个，猜只会在 WSL / Git Bash 上出错
    if (pathname === "/install.ps1" || pathname === "/install.ps1/") {
      return Response.redirect(TARGET_PS1, 302);
    }
    if (pathname === "/") {
      return Response.redirect("https://github.com/Xsxdot/handoff", 302);
    }
    return new Response("not found\n", { status: 404 });
  },
};
```

文件头注释同步改两处：
- 第一行「把 handoff.gosuper.dev/install 302 到仓库里的 install.sh」→「把 handoff.gosuper.dev 的 /install 与 /install.ps1 分别 302 到仓库里的 install.sh 与 install.ps1」；
- 「边界」段里「除 /install 与 / 之外一律 404」→「除 /install、/install.ps1 与 / 之外一律 404」。

「职责」段里「不托管脚本内容」那条保持不变，它对两个脚本同样成立。

**注意：本 task 只改仓库里的 Worker 源码，不部署。** 部署属对外动作，留到真机验收时由人执行。

- [ ] **Step 9: 跑全量验证**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go') && bash install_test.sh`
Expected: 全绿

- [ ] **Step 10: 注释与日志自检**

确认：`install.ps1` 有完整文件头（职责、边界、环境变量、兼容性）；每个函数有 doc 注释；`Get-LatestTag` 里「为什么不打 API」与「为什么两套取 URL 的写法」两条 why 在；`Test-HandoffChecksum` 里「为什么抛错而不返回 false」的 why 在；每个失败分支都经 `Stop-Install` 且带上下文；成功路径打印了「已安装 <path> <tag>」（不静默）；`install_test.ps1` 有文件头；Worker 的两条路径各有注释。

- [ ] **Step 11: Commit**

```bash
git add install.ps1 install_test.ps1 install.sh install_test.sh .github/workflows/ci.yml deploy/install-redirect/worker.js
git commit -m "feat(b86): 加 Windows 一行安装（install.ps1）

irm https://handoff.gosuper.dev/install.ps1 | iex，与 install.sh 边界逐条
一致：不改 PATH、不写服务、不提权。落点用 %LOCALAPPDATA%\\Programs\\handoff
（Windows 的用户级安装惯例；~/.local/bin 在 Windows 上没有工具认）。

必须兼容 Windows 自带的 PowerShell 5.1：5.1 与 7 在 Invoke-WebRequest 的
BaseResponse 上是两个不同的类型，取重定向后最终 URL 的写法不同，只写 7
的写法会让脚本在绝大多数 Windows 机器上第一步就挂。CI 里两个版本各跑一遍。

install.sh 的 Windows 分支从「B37 所以不支持」改成指向 install.ps1——
断言的意图始终是「拒绝时必须给出路」，只是出路变了。Worker 加 /install.ps1
路由（只改源码不部署，部署属对外动作）。"
```

---

## Task 5: release 矩阵扩到 6 项并出 Windows zip

**Files:**
- Modify: `.github/workflows/release.yml`（`build` job 改名 `build-unix`，矩阵加两项，Windows 出 zip）
- Test: `release_workflow_test.go`

**Interfaces:**
- Consumes: Task 1 的 `verify` job；Task 2 的 `AssetName` 扩展名约定
- Produces: `release.yml` 里名为 `build-unix` 的 job（Task 6 会加 `build-darwin`）

- [ ] **Step 1: 重写平台矩阵的契约测试**

`release_workflow_test.go` 里，把现有的 `TestWorkflowCoversExactlyFourPlatforms` **整个替换**（不是删除——它守的位置仍然需要有东西守着，只是约束变了）：

```go
// 平台清单必须正好是这六项。
//
// 这条断言在 B86 之前是「正好四项，且不得含 windows」——理由是 agentd 在
// Windows 上跑不起来（B37），发一个装了也用不了的二进制是负价值。B84 让
// 纯协调者机不再需要 agentd 之后，Windows 二进制第一次有了真实用途（只当
// 协调者），于是断言反转。**反转不等于取消**：少一项等于某个平台装不上，
// 多一项等于发一个没人验证过的资产。
//
// 清单从两处收集并取并集：交叉编译 job 的 matrix.include，以及签名 job 的
// DARWIN_ARCHES（darwin 两项要在同一个 job 里合并成一个 zip 一次提交公证，
// 所以它不是矩阵）。取并集而不是写死取哪个 job，这条断言才能跨越
// 「darwin 还在矩阵里」与「darwin 已拆走」两种布局都成立。
func TestWorkflowCoversExactlySixPlatforms(t *testing.T) {
	jobs := releaseJobs(t)
	got := map[string]bool{}
	for _, j := range jobs {
		for _, e := range j.Strategy.Matrix.Include {
			got[e.Goos+"/"+e.Goarch] = true
		}
		for _, a := range strings.Fields(j.Env["DARWIN_ARCHES"]) {
			got["darwin/"+a] = true
		}
	}
	want := map[string]bool{
		"darwin/arm64": true, "darwin/amd64": true,
		"linux/amd64": true, "linux/arm64": true,
		"windows/amd64": true, "windows/arm64": true,
	}
	for p := range want {
		if !got[p] {
			t.Errorf("平台清单缺 %s", p)
		}
	}
	for p := range got {
		if !want[p] {
			t.Errorf("平台清单多出 %s —— 多一项等于发一个没人验证过的资产", p)
		}
	}
}

// 归档格式按平台分，这是与 internal/release.archiveExt 及两个 install 脚本
// 四处共同的契约：Windows 出 zip（资源管理器可双击、Expand-Archive 人人有），
// 其余出 tar.gz。
func TestWorkflowUsesZipForWindowsOnly(t *testing.T) {
	wf := readWorkflow(t)
	for _, want := range []string{
		`handoff_${TAG}_${GOOS}_${GOARCH}.zip`,
		`handoff_${TAG}_${GOOS}_${GOARCH}.tar.gz`,
		"handoff.exe",
	} {
		if !strings.Contains(wf, want) {
			t.Fatalf("workflow 缺约定 %q", want)
		}
	}
}
```

同时把既有的 `TestWorkflowUsesAgreedAssetNaming` 里的 `handoff_${TAG}_${GOOS}_${GOARCH}.tar.gz` 保留不动（它现在由 unix 分支满足）。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test . -run 'SixPlatforms|ZipForWindows' -v`
Expected: FAIL，`矩阵缺组合: goos: windows...`

- [ ] **Step 3: 改 `release.yml` 的构建 job**

把 `build` job 改名为 `build-unix`（本 task 里它仍然构建全部六项；Task 6 会把 darwin 两项摘走），矩阵加两项，打包按平台分叉：

```yaml
  build-unix:
    needs: verify
    runs-on: ubuntu-latest
    strategy:
      fail-fast: true
      matrix:
        include:
          - goos: darwin
            goarch: arm64
          - goos: darwin
            goarch: amd64
          - goos: linux
            goarch: amd64
          - goos: linux
            goarch: arm64
          - goos: windows
            goarch: amd64
          - goos: windows
            goarch: arm64
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: 交叉编译并打包
        env:
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
          CGO_ENABLED: '0'
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          # Windows 出 zip 且包内是 handoff.exe；其余出 tar.gz 且包内是 handoff。
          # 这四处（这里、internal/release.archiveExt、install.sh、install.ps1）
          # 是同一个契约，改一处必须四处同步。
          if [ "${GOOS}" = "windows" ]; then
            BIN=handoff.exe
          else
            BIN=handoff
          fi
          go build -trimpath \
            -ldflags "-s -w -X github.com/xushixin/handoff/internal/buildinfo.releaseVersion=${TAG}" \
            -o "${BIN}" .
          if [ "${GOOS}" = "windows" ]; then
            zip -q "handoff_${TAG}_${GOOS}_${GOARCH}.zip" "${BIN}"
          else
            tar czf "handoff_${TAG}_${GOOS}_${GOARCH}.tar.gz" "${BIN}"
          fi
      - uses: actions/upload-artifact@v4
        with:
          name: handoff_${{ matrix.goos }}_${{ matrix.goarch }}
          path: |
            handoff_*.tar.gz
            handoff_*.zip
          if-no-files-found: error
```

`release` job 的 `needs: build` 改成 `needs: build-unix`，上传资产那步的通配加上 zip：

```yaml
            dist/handoff_*.tar.gz dist/handoff_*.zip dist/checksums.txt
```

`checksums` 那步的 `sha256sum handoff_*.tar.gz` 改成 `sha256sum handoff_*.tar.gz handoff_*.zip`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test . -run 'SixPlatforms|ZipForWindows|AssetNaming|VerifyGate' -v`
Expected: 全部 PASS

- [ ] **Step 5: 跑全量验证**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go')`
Expected: 全绿

- [ ] **Step 6: 注释自检**

确认 `release.yml` 顶部的文件头注释里「产出：四个平台的 handoff_<tag>_<os>_<arch>.tar.gz」订正为六个平台与两种格式；打包分叉处有「四处同一契约」的注释；替换掉的测试有说明「为什么反转、反转不等于取消」的注释。

- [ ] **Step 7: Commit**

```bash
git add .github/workflows/release.yml release_workflow_test.go
git commit -m "feat(b86): release 矩阵扩到六项，Windows 出 zip

B84 让纯协调者机不再需要 agentd 之后，Windows 二进制第一次有了真实用途
（只当协调者），于是「不得含 windows」的断言反转。反转不等于取消：新断言
仍钉死正好六项，少一项等于某平台装不上、多一项等于发一个没人验证过的资产。

Windows 出 zip 而非 tar.gz：资源管理器可双击，且 Expand-Archive 人人都有
而 tar.exe 只有 Win10 1803+ 才有。这个契约同时存在于 workflow、
internal/release.archiveExt、install.sh、install.ps1 四处。"
```

---

## Task 6: darwin 资产签名与公证

**Files:**
- Modify: `.github/workflows/release.yml`（darwin 两项从 `build-unix` 摘到新的 `build-darwin` job）
- Test: `release_workflow_test.go`

**Interfaces:**
- Consumes: Task 1 的 `verify`；Task 5 的 `build-unix` job 与资产命名
- Produces: `release.yml` 里名为 `build-darwin` 的 job，产出与 `build-unix` 同名规则的 darwin tar.gz

**前置（人工，不在代码里）：** 把 super-dev 仓库里的六个 secret 复制到 `Xsxdot/handoff`：`APPLE_CERTIFICATE`、`APPLE_CERTIFICATE_PASSWORD`、`APPLE_SIGNING_IDENTITY`、`APPLE_API_ISSUER`、`APPLE_API_KEY`、`APPLE_API_KEY_CONTENT`。（`APPLE_TEAM_ID` 用不上：用 API Key 公证时不需要它。）**这一步属对外动作，由人执行，实现者不要代劳。**

- [ ] **Step 1: 写失败的测试**

追加到 `release_workflow_test.go`：

```go
// 签名与公证不能被摘掉。
//
// 这条与 TestEveryReleaseJobIsGatedByVerify 是同一类断言：把 codesign /
// notarytool 那几步删了，release 会跑得更快、照样出资产，只是从此发出去的
// 是未签名版本——症状出现在用户机器上（浏览器下载被 Gatekeeper 拦），
// 且从 CI 的绿色里完全看不出来。
func TestDarwinJobSignsAndNotarizes(t *testing.T) {
	jobs := releaseJobs(t)
	j, ok := jobs["build-darwin"]
	if !ok {
		t.Fatal("release.yml 缺 build-darwin job")
	}
	if !strings.HasPrefix(j.RunsOn, "macos") {
		t.Fatalf("darwin 资产必须在 macOS runner 上构建（codesign/notarytool 只在那儿有），实得 runs-on=%q", j.RunsOn)
	}
	wf := readWorkflow(t)
	for _, want := range []string{
		"--options runtime", // 硬化运行时是公证的前置条件，不加会被拒
		"notarytool submit",
		"spctl -a -t exec",
		"CGO_ENABLED", // macOS 上 CGO 默认开，开了会引入动态链接与最低系统版本约束
	} {
		if !strings.Contains(wf, want) {
			t.Fatalf("build-darwin 缺关键步骤 %q", want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test . -run TestDarwinJobSignsAndNotarizes -v`
Expected: FAIL，`release.yml 缺 build-darwin job`

- [ ] **Step 3: 从 `build-unix` 摘掉 darwin 两项**

`build-unix` 的矩阵删掉两个 darwin 条目，剩四项（linux×2 + windows×2）。job 名保持 `build-unix`，矩阵上方加注释：

```yaml
    # darwin 两项不在这里：它们要签名与公证，在 build-darwin 里构建。
    # 那两项也不用矩阵——合并成一个 zip 一次提交公证能省一轮 1-3 分钟的等待。
```

`TestWorkflowCoversExactlySixPlatforms` **不用改**：它取的是所有 job 的 `matrix.include` 与 `env.DARWIN_ARCHES` 的并集，两种布局下都是同样的六项。这正是 Task 5 里不用字符串匹配的理由。

- [ ] **Step 4: 加 `build-darwin` job**

在 `build-unix` 之后插入：

```yaml
  # darwin 资产必须在 macOS runner 上构建：codesign 与 notarytool 只在那儿有。
  #
  # 签名买的是且仅是「从 Releases 页面用浏览器下载」那条路径——浏览器打
  # com.apple.quarantine，归档工具把它传播到解出的文件上，Gatekeeper 才会介入。
  # curl | bash 这条主路径本来就不受影响（curl 不打 quarantine）。
  #
  # 裸 CLI 无法 staple 公证票据（stapler 只支持 .app/.dmg/.pkg），票据存在
  # Apple 服务端按 cdhash 匹配，用户联网时校验通过。这是固有限制不是配置问题。
  build-darwin:
    needs: verify
    runs-on: macos-latest
    env:
      # 两个 arch 在同一个 job 里构建，不用矩阵：合并成一个 zip 一次提交公证，
      # 省一轮 1-3 分钟的等待。这个变量也是平台清单契约测试读的那一处。
      DARWIN_ARCHES: arm64 amd64
      APPLE_CERTIFICATE: ${{ secrets.APPLE_CERTIFICATE }}
      APPLE_CERTIFICATE_PASSWORD: ${{ secrets.APPLE_CERTIFICATE_PASSWORD }}
      APPLE_SIGNING_IDENTITY: ${{ secrets.APPLE_SIGNING_IDENTITY }}
      APPLE_API_ISSUER: ${{ secrets.APPLE_API_ISSUER }}
      APPLE_API_KEY: ${{ secrets.APPLE_API_KEY }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      # secrets 缺失即硬失败，不产出未签名资产。
      # 这与 super-dev 的策略相反（它未配置时仍出未签名包，那是给 desktop 的
      # 兼容退路）。handoff 不需要那条退路：打 tag 只有仓库所有者会做，而一个
      # 静默发出去的未签名版本是会在几个月后咬人的陷阱——症状出现在用户机器上。
      - name: 检查签名凭据齐备
        env:
          APPLE_API_KEY_CONTENT: ${{ secrets.APPLE_API_KEY_CONTENT }}
        run: |
          set -euo pipefail
          missing=""
          for v in APPLE_CERTIFICATE APPLE_CERTIFICATE_PASSWORD APPLE_SIGNING_IDENTITY \
                   APPLE_API_ISSUER APPLE_API_KEY APPLE_API_KEY_CONTENT; do
            if [ -z "${!v:-}" ]; then missing="${missing} ${v}"; fi
          done
          if [ -n "$missing" ]; then
            printf '缺少签名 secret：%s\n未签名的 release 不发。\n' "$missing" >&2
            exit 1
          fi
          echo "签名凭据齐备"

      # App Store Connect API Key 只能以文件路径传给 notarytool。
      - name: 写 App Store Connect API Key
        env:
          APPLE_API_KEY_CONTENT: ${{ secrets.APPLE_API_KEY_CONTENT }}
        run: |
          set -euo pipefail
          key_path="${RUNNER_TEMP}/AuthKey.p8"
          printf '%s\n' "$APPLE_API_KEY_CONTENT" > "$key_path"
          chmod 600 "$key_path"
          # 只确认私钥文件头形态，不打印密钥正文
          head -n 1 "$key_path" | grep -q 'BEGIN PRIVATE KEY' || {
            echo "APPLE_API_KEY_CONTENT 不像是一份 .p8 私钥" >&2; exit 1; }
          echo "APPLE_API_KEY_PATH=${key_path}" >> "$GITHUB_ENV"

      - name: 导入签名证书到临时钥匙串
        run: |
          set -euo pipefail
          cert_path="${RUNNER_TEMP}/certificate.p12"
          keychain_path="${RUNNER_TEMP}/handoff-build.keychain-db"
          keychain_password="$(openssl rand -base64 32)"
          # Secrets 存的是 openssl base64 -A 的单行内容
          echo "$APPLE_CERTIFICATE" | base64 --decode > "$cert_path"
          security create-keychain -p "$keychain_password" "$keychain_path"
          security set-keychain-settings -lut 21600 "$keychain_path"
          security unlock-keychain -p "$keychain_password" "$keychain_path"
          security import "$cert_path" -P "$APPLE_CERTIFICATE_PASSWORD" \
            -A -t cert -f pkcs12 -k "$keychain_path"
          # 让 codesign 能在无 UI 提示下使用私钥
          security set-key-partition-list -S apple-tool:,apple:,codesign: \
            -s -k "$keychain_password" "$keychain_path"
          security list-keychains -d user -s "$keychain_path" \
            $(security list-keychains -d user | sed 's/"//g')
          security find-identity -v -p codesigning "$keychain_path" \
            | grep -F "$APPLE_SIGNING_IDENTITY" > /dev/null || {
              echo "钥匙串里找不到 APPLE_SIGNING_IDENTITY" >&2; exit 1; }
          rm -f "$cert_path"

      - name: 构建、签名、公证并打包
        env:
          # macOS 上 CGO 默认是开的。开了会让产物动态链接系统库并被打上构建机的
          # 最低系统版本约束——二进制会在更老的 macOS 上拒绝启动，而这个症状要
          # 等到用户机器上才出现。这一行在 ubuntu 上是冗余，在这里是承重的。
          CGO_ENABLED: '0'
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          for arch in $DARWIN_ARCHES; do
            mkdir -p "build/${arch}"
            # 目录名带 arch、文件名固定 handoff：tar 里的文件名是与 install.sh
            # 及 extractBinary 的契约，不能带 arch 后缀
            GOOS=darwin GOARCH="$arch" go build -trimpath \
              -ldflags "-s -w -X github.com/xushixin/handoff/internal/buildinfo.releaseVersion=${TAG}" \
              -o "build/${arch}/handoff" .
            # --options runtime（硬化运行时）是公证的前置条件，不加会被拒
            codesign --force --options runtime --timestamp \
              --sign "$APPLE_SIGNING_IDENTITY" "build/${arch}/handoff"
            codesign --verify --strict --verbose=2 "build/${arch}/handoff"
          done

          # 两个 arch 打进同一个 zip 一次提交：notarytool 接受 zip 内多个 Mach-O，
          # 合并提交省一轮 1-3 分钟的等待
          ditto -c -k --keepParent build notarize.zip
          xcrun notarytool submit notarize.zip \
            --key "$APPLE_API_KEY_PATH" \
            --key-id "$APPLE_API_KEY" \
            --issuer "$APPLE_API_ISSUER" \
            --wait 2>&1 | tee /tmp/notary.log
          # notarytool 在 status 为 Invalid 时仍可能退 0，光看退出码不够
          grep -q 'status: Accepted' /tmp/notary.log || {
            echo "公证未被接受：" >&2; cat /tmp/notary.log >&2; exit 1; }

          for arch in $DARWIN_ARCHES; do
            bin="build/${arch}/handoff"
            # 票据在 Apple 侧生效有传播延迟，紧跟 submit 之后立刻 spctl 有概率
            # 报未公证。重试三次；仍失败即整个 job 失败——「只警告」会让这道
            # 验证退化成装饰。
            ok=0
            for i in 1 2 3; do
              if spctl -a -t exec -vv "$bin" 2>&1 | tee /tmp/spctl.log \
                 | grep -q 'source=Notarized Developer ID'; then
                ok=1; break
              fi
              echo "第 ${i} 次 spctl 未通过（${arch}），20s 后重试"
              sleep 20
            done
            if [ "$ok" != "1" ]; then
              echo "spctl 三次均未报 Notarized Developer ID（${arch}）：" >&2
              cat /tmp/spctl.log >&2
              exit 1
            fi
            # 打包必须在签名之后：签名会改字节，先算校验和再签就全错
            tar czf "handoff_${TAG}_darwin_${arch}.tar.gz" -C "build/${arch}" handoff
          done
      - uses: actions/upload-artifact@v4
        with:
          name: handoff_darwin
          path: handoff_*.tar.gz
          if-no-files-found: error
```

**这一步的已知不确定性（实现者请读）：** `spctl -a -t exec` 的设计对象是 app bundle，对**裸命令行工具**有时会报 `rejected (the code is valid but does not seem to be an app)`。若真跑 tag 时撞上这句，那**不是**公证失败——`notarytool` 的 `status: Accepted` 才是公证是否成功的权威判据，上面已单独 grep。撞上时**停下上报 BLOCKED，不要就地把 spctl 降级成警告**：spec §9 风险一明确把「spctl 只警告」列为不接受，改这条判据是设计决定，要回到审核者手里。

- [ ] **Step 5: 让 `release` job 等两个构建 job**

`release` job 的 `needs: build-unix` 改成：

```yaml
    needs: [build-unix, build-darwin]
```

`checksums` 那步补一条注释：

```bash
          # checksums 必须在签名之后算——签名会改字节。这里对下载下来的
          # 成品资产统一算，天然满足这个顺序，不要把它挪到构建 job 里去
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test . -count=1 -v`
Expected: 全部 PASS

- [ ] **Step 7: 跑全量验证**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go')`
Expected: 全绿

- [ ] **Step 8: 注释自检**

确认：`build-darwin` 的 job 级注释说清了「签名买的是哪条路径」与「为什么不能 staple」；`CGO_ENABLED` 有「在这里是承重的」注释；secrets 检查有「为什么与 super-dev 策略相反」；spctl 重试有「为什么不能只警告」；打包顺序有「签名会改字节」；`checksums` 步骤有顺序注释。

- [ ] **Step 9: Commit**

```bash
git add .github/workflows/release.yml release_workflow_test.go
git commit -m "feat(b86): darwin 资产做 Developer ID 签名与公证

darwin 两项从交叉编译摘到 macos-latest 上单独构建。签名买的是且仅是「从
Releases 页面用浏览器下载」那条路径——curl | bash 不打 quarantine，
Gatekeeper 根本不介入。裸 CLI 无法 staple 票据（stapler 只支持
.app/.dmg/.pkg），联网时按 cdhash 校验。

三处容易踩的：CGO_ENABLED=0 在 macOS runner 上从冗余变承重（CGO 默认开，
会引入动态链接与最低系统版本约束，症状要到用户的老 mac 上才出现）；
secrets 缺失硬失败而不出未签名包；spctl 因公证传播延迟重试三次但绝不
降级成警告——那会让验证退化成装饰。

新增契约断言守「签名步骤不可摘」：删了它 release 更快、照样出资产，只是
发出去的是未签名版本，从 CI 的绿色里完全看不出来。"
```

---

## Task 7: LICENSE、CHANGELOG 与 README

**Files:**
- Create: `LICENSE`
- Create: `CHANGELOG.md`
- Modify: `.github/workflows/release.yml`（release notes 取自 CHANGELOG）
- Modify: `README.md`
- Test: `release_workflow_test.go`

**Interfaces:**
- Consumes: Task 6 的 `release` job
- Produces: 无代码接口

- [ ] **Step 1: 写 LICENSE**

把 Apache License 2.0 的标准全文写入 `LICENSE`。可从本机已有的一份取：

```bash
cp /Users/xushixin/workspace/super-debug/LICENSE ./LICENSE
```

若该路径不可用，从 https://www.apache.org/licenses/LICENSE-2.0.txt 取标准全文。**不修改正文**，只在文件末尾的 APPENDIX 样板之后追加一行版权声明：

```
   Copyright 2026 Xsxdot

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
```

**不建 NOTICE**：Apache-2.0 并不要求它，加了反而给下游多一条必须传播的义务。
**不给 `.go` 文件加 license header**：本仓库的文件头注释承担的是「职责 + 边界」，是给读代码的人的第一句话，在它上面压 11 行法律样板会把真正该读的内容挤到屏幕外。Apache-2.0 推荐但不要求逐文件标注。

- [ ] **Step 2: 写 CHANGELOG.md**

回填已发的四个 tag。**内容以 `git log` 为准，不要凭印象编**：

```bash
git log --oneline v0.1.0 | head -30
git log --oneline v0.1.0..v0.1.1
git log --oneline v0.1.1..v0.1.2
git log --oneline v0.1.2..v0.2.0
```

骨架（每节的条目按上面的 git log 实际填写；Unreleased 一节写 B86 本身的改动）：

```markdown
# Changelog

本文件记录 handoff 的所有值得用户知道的改动。

格式依据 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号依据 [语义化版本](https://semver.org/lang/zh-CN/)。

**这份文件是承重的**：release workflow 按 tag 抽取对应小节作为 GitHub Release
的说明。抽不到时会回落成自动生成的 commit 列表，并在日志里打一条警告。

## [Unreleased]

### 新增

- Windows 协调者分发：发 `windows/amd64` 与 `windows/arm64` 资产（`.zip`，内含
  `handoff.exe`），新增 `install.ps1` 一行安装。**Windows 上 handoff 只能当协调者**，
  agentd 仍不支持 Windows。
- macOS 资产做 Developer ID 签名与公证，从 Releases 页面用浏览器下载不再被
  Gatekeeper 拦下。
- 新增 CI 验证门：PR 与发布前跑同一套 `go build` / `vet` / `test` / `gofmt` /
  Windows 交叉编译 / 安装脚本单测。
- 本项目以 Apache License 2.0 发布。

### 变更

- `handoff init` 在 Windows 上只提供「协调者」一个角色选项。

## [v0.2.0] - 2026-08-13

...

## [v0.1.2] - 2026-08-11

...

## [v0.1.1] - 2026-08-11

...

## [v0.1.0] - 2026-08-11

### 新增

- 首个公开版本。
```

- [ ] **Step 3: 写失败的测试（release notes 取自 CHANGELOG）**

追加到 `release_workflow_test.go`：

```go
// release notes 必须优先取自 CHANGELOG。
//
// 没有这条，CHANGELOG 就是个没人看也没人维护的摆设——而没人维护的文档
// 比没有更糟：它会让读者相信一份过期的事实。
func TestReleaseNotesComeFromChangelog(t *testing.T) {
	wf := readWorkflow(t)
	for _, want := range []string{"CHANGELOG.md", "--notes-file"} {
		if !strings.Contains(wf, want) {
			t.Fatalf("release job 缺 %q —— release notes 应优先取自 CHANGELOG", want)
		}
	}
	// 抽不到时仍要能发布，否则一次格式失误会把整条发布卡死
	if !strings.Contains(wf, "--generate-notes") {
		t.Fatal("缺 --generate-notes 回落分支：CHANGELOG 抽取失败不该卡死发布")
	}
}

// CHANGELOG 必须存在且有 Unreleased 一节可供下次发布填写。
func TestChangelogExists(t *testing.T) {
	b, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatalf("读 CHANGELOG.md 失败: %v", err)
	}
	if !strings.Contains(string(b), "## [Unreleased]") {
		t.Fatal("CHANGELOG.md 缺 [Unreleased] 一节")
	}
}
```

- [ ] **Step 4: 跑测试确认它失败**

Run: `go test . -run 'ChangelogExists|NotesComeFromChangelog' -v`
Expected: FAIL

- [ ] **Step 5: 改 `release.yml` 的建 Release 那步**

需要 checkout（现在的 `release` job 没有 checkout，只 download-artifact），因为要读 CHANGELOG：

```yaml
      - uses: actions/checkout@v4
      - name: 从 CHANGELOG 抽取本版说明
        run: |
          set -euo pipefail
          TAG="${GITHUB_REF_NAME}"
          # 抽取 "## [<tag>]" 到下一个 "## " 之前的内容。
          # 抽不到不算失败——一次格式失误不该把整条发布卡死；但必须打警告，
          # 否则「CHANGELOG 承重」这个设计会在无人察觉中失效
          awk -v tag="## [${TAG}]" '
            index($0, tag) == 1 { on = 1; next }
            on && /^## / { exit }
            on { print }
          ' CHANGELOG.md > notes.md || true
          if [ -s notes.md ]; then
            echo "NOTES_ARG=--notes-file=notes.md" >> "$GITHUB_ENV"
            echo "已从 CHANGELOG.md 抽到 ${TAG} 的说明"
          else
            echo "NOTES_ARG=--generate-notes" >> "$GITHUB_ENV"
            echo "警告：CHANGELOG.md 里没有 ${TAG} 的小节，回落成自动生成的 commit 列表" >&2
          fi
      - name: 建 Release 并上传资产
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          set -euo pipefail
          gh release create "${GITHUB_REF_NAME}" \
            --repo "${GITHUB_REPOSITORY}" \
            --title "${GITHUB_REF_NAME}" \
            "${NOTES_ARG}" \
            dist/handoff_*.tar.gz dist/handoff_*.zip dist/checksums.txt
```

**注意**：`download-artifact` 的 `path: dist` 与 checkout 会在同一个工作目录里；checkout 要放在 download 之前，否则 checkout 可能清掉已下载的内容。把 checkout 排到该 job 的第一步。

- [ ] **Step 6: 改 README**

五处改动：

1. **顶部**（第 1 行 `# handoff` 之后、加粗标语 `**把实现计划派发给另一个 AI 执行…**` 之前）加 badge：

```markdown
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
```

2. **「## 安装」章节**（[README.md:26](../../../README.md)）。现有的 `:28` 那一整段已经过时——它写着「没有安装脚本与 release 资产，需自行 `go build`；因此 `handoff upgrade` 升不了本机这一份」，本次之后三条都不成立了。把它**整段替换**：

```markdown
macOS / Linux（amd64 / arm64）：

```bash
curl -fsSL https://handoff.gosuper.dev/install | bash
```

Windows（amd64 / arm64，PowerShell）：

```powershell
irm https://handoff.gosuper.dev/install.ps1 | iex
```

**Windows 上 handoff 只能当协调者**——派发、审阅、裁决权限、`upgrade` 升远端都可以，
但本机不能当执行机：agentd 依赖的进程承载层在非 unix 平台尚未实现（backlog B37）。
派发目标必须是一台 macOS 或 Linux 执行机。另外 `wait --notify` 的桌面通知只有 macOS
有，Windows 上唤醒通道是 `wait` 的 stdout。
```

原来紧跟在 curl 命令之后的那段（「脚本把二进制装到 `~/.local/bin/handoff`…」）保留，位置挪到两个安装命令之后，并在其中补一句 Windows 落点：

```markdown
脚本把二进制装到 `~/.local/bin/handoff`（Windows 是 `%LOCALAPPDATA%\Programs\handoff\handoff.exe`），
免 sudo / 免管理员，`HANDOFF_INSTALL_DIR` 可换目录，校验 sha256 后才落盘，可反复重跑。装完确认：
```

3. **「## 升级」章节**（[README.md:247](../../../README.md)），在「CLI 每天最多后台查一次新版本…」那段之前补一句：

```markdown
三个平台都能自更新，Windows 也不例外——替换是「先把旧的改名成 `.prev`，再把新的移进来」，
这正是 Windows 允许对一个正在运行的 exe 做的操作。
```

4. **「## Troubleshooting」章节**补两条：

```markdown
**macOS：从 Releases 页面下载后提示「无法打开，因为无法验证开发者」**

发布的 darwin 资产已做 Developer ID 签名与公证，但裸命令行工具无法内嵌公证票据
（Apple 的 stapler 只支持 .app / .dmg / .pkg），首次运行需要联网让系统去 Apple
校验。断网时会被拦下。处置：联网后重试，或手动摘掉隔离标记：

```bash
xattr -d com.apple.quarantine ~/.local/bin/handoff
```

用 `curl | bash` 安装不会遇到这个问题——curl 不打隔离标记。

**Windows：提示「Windows 已保护你的电脑」**

Windows 二进制未做 Authenticode 签名（需另购 OV/EV 证书），SmartScreen 会提示
未知发布者。处置：点「更多信息」→「仍要运行」。可先用 `checksums.txt` 核对
下载物的 sha256 再运行。
```

Troubleshooting 是个表格（`| 症状 | 原因与处置 |`），上面两条内容较长，作为**表格之后的两个小段**追加，不要硬塞进表格单元格。

5. **底部**（「## 友情链接」之后）加 License 章节：

```markdown
## License

[Apache License 2.0](LICENSE)
```

**改完通读一遍**：全文搜 `Windows`，确认没有别处还写着「不支持 Windows」「需自行 go build」之类的话。

- [ ] **Step 7: 跑测试确认通过**

Run: `go test . -count=1 -v`
Expected: 全部 PASS

- [ ] **Step 8: 跑全量验证**

Run: `go build ./... && go vet ./... && go test ./... -count=1 && gofmt -l $(git ls-files '*.go') && bash install_test.sh`
Expected: 全绿

- [ ] **Step 9: 注释自检**

确认：CHANGELOG 顶部写明了「这份文件是承重的」及其机制；`release.yml` 的抽取步骤有「为什么抽不到不算失败、但必须打警告」的注释；README 的 Windows 安装段里「只能当协调者」与安装命令**同屏**（不在别的章节）。

- [ ] **Step 10: Commit**

```bash
git add LICENSE CHANGELOG.md README.md .github/workflows/release.yml release_workflow_test.go
git commit -m "docs(b86): 以 Apache-2.0 发布，补 CHANGELOG 与 README

仓库已公开且已发到 v0.2.0，但 licenseInfo 为 null——默认即保留所有权利，
别人下载安装处在法律灰地带。选 Apache-2.0（与 super-dev 一致，带明确的
专利授权条款）。不加 NOTICE（Apache-2.0 不要求，加了给下游多一条传播
义务），不加逐文件 header（会把「职责+边界」的文件头挤到屏幕外）。

CHANGELOG 承重：release workflow 按 tag 抽取小节走 --notes-file，抽不到
回落 --generate-notes 并打警告。不这么做的话它就是个没人维护的摆设，
而没人维护的文档比没有更糟。

README 补 license badge 与章节、Windows 安装（「只能当协调者」与安装命令
同屏）、以及 Gatekeeper 与 SmartScreen 两条 Troubleshooting。"
```

---

## 收尾：真机验收清单（不在任何 task 里，由人执行）

代码全部完成后仍有三件**必须由人做**的事。它们涉及对外动作或需要真实机器，实现者不要代劳：

1. **把六个 Apple secret 复制到 `Xsxdot/handoff`**（Task 6 的前置）。
2. **部署 Cloudflare Worker**，让 `/install.ps1` 真的可用（Task 4 只改了源码）。
3. **发一个真 tag 并验收**：
   - 产出 6 个资产 + `checksums.txt`；
   - release notes 来自 CHANGELOG 而非自动生成的 commit 列表；
   - **macOS**：从 Releases 页面用**浏览器**下 darwin tar.gz，解开后直接运行不被
     Gatekeeper 拦。**用 curl 下载验证等于没验证**——curl 不打 quarantine，
     Gatekeeper 根本不介入，而浏览器下载正是签名唯一要买的那条路径；
   - **Windows**：`irm .../install.ps1 | iex` 装上 → `handoff init`（角色只有协调者）
     → `dispatch --target <执行机>` → `wait` → `diff` → `done` 全程走通 →
     `handoff upgrade` 能自更新。

**没有 Windows 机器时，第 3 条的 Windows 部分如实记为「未验」，不得因为 CI 绿就记已验。** CI 的 windows-latest 只覆盖 `install.ps1` 与编译，覆盖不了 init / dispatch / upgrade 的真实回路。
