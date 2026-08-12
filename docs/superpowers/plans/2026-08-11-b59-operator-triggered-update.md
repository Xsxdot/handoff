# B59 操作者触发的更新与 skill 分发 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把「什么时候升级」的决策权从 agentd 的定时循环交还给操作者：一条 `handoff upgrade` 看清所有机器、升级所有机器，二进制由本机下载后推送给远端（执行机无需出网），同时把 skill 内嵌进二进制随安装/升级同步。

**Architecture:** CLI 侧新增「机器范围」维度：读配置里的 `targets` 得到机器清单，逐台查 `GET /api/status` 拿版本+平台+托管态+活跃任务，预检两道闸后按平台下载对应 release 资产，把 **tar.gz 原文**推给远端 `POST /api/update`；agentd 复检两道闸、校验 sha256、解包、自检、`release.Activate` 原子换版，返回 200 后触发优雅关停，由 systemd/launchd 拉起新版；CLI 轮询 status 确认新版本上线。同时删除 `internal/selfupdate` 的定时循环与待命状态，`skills/handoff/SKILL.md` 用 `go:embed` 进二进制。

**Tech Stack:** Go 1.22+（`net/http` `ServeMux` 方法路由）、cobra、slog、`go:embed`、`httptest`、SQLite（既有）。

## Global Constraints

以下约束来自 spec `docs/superpowers/specs/2026-08-11-update-and-skill-delivery-design.md`，**每个 task 的要求都隐含包含本节**：

- **不删任何现有配置字段。** 配置是 `KnownFields(true)` 严格解析的，未知键让 agentd **启动失败**；v0.1.0 首次运行会把 `update.auto` / `update.interval` 写进 `config.yaml`。这两个字段**保留结构体字段**、只标废弃并在取值非默认时打 Warn（D7）。
- **新增线格式字段一律 `omitempty`，且空值语义是「对端没给」而不是「值为零」**（与 `Watchers *int`、`Update *UpdateStatus` 同一条纪律）。对端没给平台就明确拒绝，**绝不猜默认值**。
- **闸二（非托管）`--force` 也不能越过。** `--force` 只越过闸一（活跃任务）。
- **活跃任务的口径固定为 `running` + `waiting_answer`**，`waiting_review` 不计入（沿用 B54.3 的 D12：它可能挂几天）。
- **处置建议必须对症，不对症就不给。** 非托管**不给** `--force` 行；够不着只报原始错误原文，不编处置。
- **不确认就不报成功。** 换版后必须轮询 status 确认新版本上线（等待 60s、间隔 2s），超时报「已换版但新进程未在 N 秒内上线」并附 `.prev` 路径与回滚命令。
- **日志用 `s.log` / `c.log()`（slog），绝不用 `fmt.Printf` 当日志。** CLI 打给用户看的产品输出走 `cmd.OutOrStdout()`，那是输出不是日志，两者都要有。
- **临时文件必须落在目标二进制的同目录**：`os.Rename` 的原子性只在同一文件系统内成立。
- 所有中文注释与报文沿用仓库既有风格：注释解释**为什么**与边界，不复述代码。

---

## 文件结构

| 文件 | 职责 | 归属 task |
|---|---|---|
| `internal/release/install.go` | 改：`Fetch` 拆成 `FetchArchive`（按指定平台下载+校验，不解包不自检）+ `InstallArchive`（校验+解包+自检），`Fetch` 保留为二者组合 | T1 |
| `internal/proto/status.go` | 改：`BuildInfo` 加 `Platform`；`UpdateStatus` 去掉 `Pending`/`DownloadedAt` | T2 |
| `internal/proto/update.go` | 新增：`/api/update` 的响应与拒绝原因线格式 | T2 |
| `internal/buildinfo/buildinfo.go` | 改：`Read()` 两条返回路径都填 `Platform` | T2 |
| `internal/selfupdate/managed.go` | 新增（从 `pending.go` 搬来）：`IsManaged` | T2 |
| `internal/agentd/status.go` | 改：`Update` 恒返回（只带 `Managed`），不再读 `pending.json` | T2 |
| `cmd/status.go` | 改：更新状态段落随 `UpdateStatus` 简化；skill 不一致时加提示行 | T2 / T6 |
| `internal/agentd/update.go` | 新增：`POST /api/update` 处理器——复检两道闸、落盘、校验、自检、换版、触发关停 | T3 |
| `internal/agentd/server.go` | 改：注册路由、持有 update 依赖与 `SetRestart` 注入点 | T3 |
| `internal/client/update.go` | 新增：`PushUpdate` / `RestartAgentd` / `WaitVersion` 与可判别的拒绝错误 | T4 |
| `internal/skill/install.go` | 新增：`Install(content, home) ([]Site, error)`——基准副本 + 软链 + 逐落点报告 | T5 |
| `internal/skill/state.go` | 新增：`Status(content, home) ([]Site, error)`——读各落点算 sha256 与内嵌比对 | T5 |
| `main.go` | 改：`//go:embed skills/handoff/SKILL.md`，注入 cmd | T6 |
| `cmd/skill.go` | 新增：`handoff skill` / `handoff skill install` | T6 |
| `install.sh` | 改：装完二进制后调 `"$INSTALL_DIR/handoff" skill install` | T6 |
| `skills/install.sh` | **删**，由 `go run . skill install` 取代 | T6 |
| `cmd/root.go` | 改：新增 `Endpoints(only string)` 供 upgrade 遍历机器 | T7 |
| `cmd/upgrade.go` | 改：巡检表、机器范围解析、远程推送编排、逐行报告与处置建议 | T7 |
| `internal/selfupdate/updater.go` + `updater_test.go` | **删** | T8 |
| `internal/selfupdate/pending.go` 的 Pending 相关 + 测试 | **删** | T8 |
| `cmd/agentd.go` | 改：删 updater 接线，注入 `SetRestart` | T3 / T8 |
| `internal/config/config.go` | 改：`update.auto` / `update.interval` 标废弃，非默认值打 Warn | T8 |
| `README.md` | 改：skill 安装说明改为 `handoff skill install`；升级章节改写 | T9 |

---

### Task 1: `release.Fetch` 按职责拆开

跨平台推送需要「按**指定**平台下载并校验」，而现有 `Fetch`（`internal/release/install.go:73`）写死 `CurrentPlatform()`，且末尾 `selfCheck` 会 `exec` 执行新二进制——在 macOS 上跑 linux 产物必然失败。自检本身是对的，但它的执行地点是**远端 agentd**，不是本机。

**Files:**
- Modify: `internal/release/install.go:60-127`
- Test: `internal/release/install_test.go`

**Interfaces:**
- Consumes: `Release{Tag, Assets}`、`(r Release) AssetFor(goos, goarch string) (Asset, bool)`、`(r Release) Checksums() (Asset, bool)`、`Asset{Name, URL}`、`ChecksumsName`、`AssetName(tag, goos, goarch string) string`、`CurrentPlatform() (string, string)`、`TempName(tag string) string`
- Produces:
  - `func (i *Installer) FetchArchive(ctx context.Context, rel Release, goos, goarch string) ([]byte, string, error)` — 返回 (tar.gz 字节, 期望 sha256 十六进制小写, error)
  - `func (i *Installer) InstallArchive(tgz []byte, wantSum, wantTag, destDir string) (string, error)` — 返回临时二进制完整路径
  - `func (i *Installer) Fetch(ctx context.Context, rel Release, destDir string) (string, error)` — 签名与行为**一字不变**

- [ ] **Step 1: 写失败测试**

在 `internal/release/install_test.go` 追加。先看文件里已有的 httptest 服务器搭法并复用；若没有可复用的 helper，用下面这份自带的：

```go
// newFakeRelease 起一个假的资产服务器，返回 (Release, 关闭函数)。
//
// 为什么要自带一个：跨平台断言必须能同时提供两个平台的资产，
// 而「下错平台」的失败模式恰恰是两个资产都在时才暴露得出来。
func newFakeRelease(t *testing.T, tag string, files map[string][]byte) (Release, func()) {
	t.Helper()
	mux := http.NewServeMux()
	for name, body := range files {
		b := body
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, _ *http.Request) { w.Write(b) })
	}
	srv := httptest.NewServer(mux)
	rel := Release{Tag: tag}
	for name := range files {
		rel.Assets = append(rel.Assets, Asset{Name: name, URL: srv.URL + "/" + name})
	}
	return rel, srv.Close
}

// tgzWith 造一个内含名为 handoff 的可执行文件的 tar.gz。
func tgzWith(t *testing.T, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "handoff", Mode: 0o755, Size: int64(len(script)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte(script))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// TestFetchArchiveHonorsRequestedPlatform 是这次拆分的核心断言：
// 请求 linux/amd64 时必须拿到 linux 那份，而不是本机平台那份。
// 拆分前的 Fetch 写死 CurrentPlatform，这条在任何机器上都会翻红。
func TestFetchArchiveHonorsRequestedPlatform(t *testing.T) {
	linux := tgzWith(t, "#!/bin/sh\necho v9.9.9\n")
	darwin := tgzWith(t, "#!/bin/sh\necho WRONG\n")
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	checks := fmt.Sprintf("%s  %s\n%s  %s\n",
		sum(linux), AssetName("v9.9.9", "linux", "amd64"),
		sum(darwin), AssetName("v9.9.9", "darwin", "arm64"))

	rel, closeFn := newFakeRelease(t, "v9.9.9", map[string][]byte{
		AssetName("v9.9.9", "linux", "amd64"):  linux,
		AssetName("v9.9.9", "darwin", "arm64"): darwin,
		ChecksumsName:                          []byte(checks),
	})
	defer closeFn()

	i := NewInstaller(slog.New(slog.NewTextHandler(io.Discard, nil)))
	got, gotSum, err := i.FetchArchive(context.Background(), rel, "linux", "amd64")
	if err != nil {
		t.Fatalf("FetchArchive: %v", err)
	}
	if !bytes.Equal(got, linux) {
		t.Fatalf("下到了别的平台的资产")
	}
	if gotSum != sum(linux) {
		t.Fatalf("返回的 sha256 不是 checksums.txt 里声明的那个：得 %s 期望 %s", gotSum, sum(linux))
	}
}

// TestFetchArchiveDoesNotSelfCheck 锁住「本机不执行远端平台的二进制」这条边界。
// 包内放一个根本不可执行的文件，FetchArchive 仍必须成功——自检归 InstallArchive。
func TestFetchArchiveDoesNotSelfCheck(t *testing.T) {
	body := tgzWith(t, "这不是一个可执行文件")
	s := sha256.Sum256(body)
	checks := fmt.Sprintf("%s  %s\n", hex.EncodeToString(s[:]), AssetName("v9.9.9", "linux", "amd64"))
	rel, closeFn := newFakeRelease(t, "v9.9.9", map[string][]byte{
		AssetName("v9.9.9", "linux", "amd64"): body,
		ChecksumsName:                         []byte(checks),
	})
	defer closeFn()

	i := NewInstaller(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, _, err := i.FetchArchive(context.Background(), rel, "linux", "amd64"); err != nil {
		t.Fatalf("FetchArchive 不该自检，却失败了: %v", err)
	}
}

// TestInstallArchiveRejectsBadSum 锁住 agentd 侧那道「传输完整性」校验。
func TestInstallArchiveRejectsBadSum(t *testing.T) {
	dir := t.TempDir()
	i := NewInstaller(slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := i.InstallArchive(tgzWith(t, "#!/bin/sh\necho v1\n"), strings.Repeat("0", 64), "v1", dir)
	if err == nil {
		t.Fatal("sha256 不符必须拒绝")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("拒绝后不该留残件，实得 %v", ents)
	}
}

// TestInstallArchiveSelfChecks 锁住「自检失败即删临时文件」。
func TestInstallArchiveSelfChecks(t *testing.T) {
	dir := t.TempDir()
	body := tgzWith(t, "#!/bin/sh\necho v-WRONG\n")
	s := sha256.Sum256(body)
	i := NewInstaller(slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := i.InstallArchive(body, hex.EncodeToString(s[:]), "v9.9.9", dir)
	if err == nil {
		t.Fatal("version 首行对不上目标 tag 必须拒绝")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("自检失败后不该留残件，实得 %v", ents)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/release/ -run 'FetchArchive|InstallArchive' -v`
Expected: FAIL，`i.FetchArchive undefined` / `i.InstallArchive undefined`

- [ ] **Step 3: 实现拆分**

把 `internal/release/install.go:60-127` 的 `Fetch` 整体替换为下面三个函数（注释在 Step 6 补全，这里先落最小实现）：

```go
func (i *Installer) FetchArchive(ctx context.Context, rel Release, goos, goarch string) ([]byte, string, error) {
	asset, ok := rel.AssetFor(goos, goarch)
	if !ok {
		return nil, "", fmt.Errorf("发布 %s 没有 %s/%s 的资产（%s）", rel.Tag, goos, goarch, AssetName(rel.Tag, goos, goarch))
	}
	ck, ok := rel.Checksums()
	if !ok {
		return nil, "", fmt.Errorf("发布 %s 没有 %s，无法校验完整性", rel.Tag, ChecksumsName)
	}

	tgz, err := i.get(ctx, asset.URL)
	if err != nil {
		return nil, "", fmt.Errorf("下载 %s: %w", asset.Name, err)
	}
	sums, err := i.get(ctx, ck.URL)
	if err != nil {
		return nil, "", fmt.Errorf("下载 %s: %w", ChecksumsName, err)
	}
	want, err := sumFor(string(sums), asset.Name)
	if err != nil {
		return nil, "", err
	}
	got := sha256.Sum256(tgz)
	if hex.EncodeToString(got[:]) != want {
		// 不重试：完整性失败重试只会重下同一份坏数据（spec §4.7）
		return nil, "", fmt.Errorf("sha256 校验不通过（期望 %s，实得 %s）", want, hex.EncodeToString(got[:]))
	}
	return tgz, want, nil
}

func (i *Installer) InstallArchive(tgz []byte, wantSum, wantTag, destDir string) (string, error) {
	got := sha256.Sum256(tgz)
	if hex.EncodeToString(got[:]) != wantSum {
		return "", fmt.Errorf("sha256 校验不通过（期望 %s，实得 %s）", wantSum, hex.EncodeToString(got[:]))
	}

	tmp := filepath.Join(destDir, TempName(wantTag))
	// 从这里往后任何失败都要清干净
	cleanup := func() {
		if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			i.Log.Warn("清理临时文件失败", "path", tmp, "cause", err)
		}
	}
	if err := extractBinary(tgz, tmp); err != nil {
		cleanup()
		return "", fmt.Errorf("解包 %s: %w", wantTag, err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		cleanup()
		return "", fmt.Errorf("置可执行位 %s: %w", tmp, err)
	}
	if err := i.selfCheck(tmp, wantTag); err != nil {
		cleanup()
		return "", err
	}
	return tmp, nil
}

func (i *Installer) Fetch(ctx context.Context, rel Release, destDir string) (string, error) {
	goos, goarch := CurrentPlatform()
	tgz, sum, err := i.FetchArchive(ctx, rel, goos, goarch)
	if err != nil {
		return "", err
	}
	return i.InstallArchive(tgz, sum, rel.Tag, destDir)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/release/ -v`
Expected: PASS，**包括原有的 `Fetch` 测试全部仍然通过**（拆分不许改变本机路径的行为）

- [ ] **Step 5: 加关键节点日志**

`FetchArchive`：
- 下载前 `i.Log.Info("开始下载资产", "tag", rel.Tag, "platform", goos+"/"+goarch, "asset", asset.Name, "url", asset.URL)`
- 校验通过后 `i.Log.Info("资产校验通过", "tag", rel.Tag, "asset", asset.Name, "sha256", want, "bytes", len(tgz))`
- 每个错误分支已带上下文（资产名 / 平台 / 期望与实得哈希），逐条确认不要退化成裸 `err`

`InstallArchive`：
- 入口 `i.Log.Info("开始安装资产", "tag", wantTag, "dest_dir", destDir, "bytes", len(tgz))`
- 自检通过后 `i.Log.Info("新版本已就绪", "tag", wantTag, "path", tmp)`（原 `Fetch` 的那行，位置下移到这里）
- 校验失败 / 解包失败 / 自检失败三条分支各 `i.Log.Error(...)`，带 `tag` 与 `path`

`Fetch` 本身不加日志：它只是组合，加了就是重复。

- [ ] **Step 6: 加意图注释**

- `FetchArchive` doc 注释：参数（含 `goos/goarch` 为**目标机器**平台而非本机）、返回（字节 + **来自 `checksums.txt` 声明**的期望哈希）、**注意：不解包、不自检——自检必须在目标平台上做，本机执行别的平台的二进制必然失败**。
- `InstallArchive` doc 注释：参数、返回、注意（任何一步失败都删临时文件不留残件；`destDir` **必须**与目标二进制同目录）。
- `Fetch` doc 注释：保留原文并补一句「本函数是 `FetchArchive` + `InstallArchive` 的本机组合，行为与拆分前一致」。
- 文件头注释补一行边界：「下载与安装是两件事，前者可跨平台、后者必须在目标平台执行」。

- [ ] **Step 7: 提交**

```bash
git add internal/release/install.go internal/release/install_test.go && git commit -m "refactor(release): Fetch 拆成 FetchArchive + InstallArchive

跨平台推送需要按指定平台下载，而 Fetch 写死 CurrentPlatform；且末尾
selfCheck 会 exec 新二进制，本机跑不了远端平台的产物。按职责拆开：
FetchArchive 只下载+校验（可跨平台），InstallArchive 校验+解包+自检
（必须在目标平台跑）。Fetch 保留为二者组合，本机路径行为一字不变。"
```

---

### Task 2: 平台字段、状态字段与 `IsManaged` 归位

远端可能是 `linux/amd64` 而本机是 `darwin/arm64`，CLI 必须知道该下哪个资产；`proto.BuildInfo` 目前没有平台。同时 `UpdateStatus` 要去掉待命概念、恒返回托管态，`IsManaged` 从行将被删的 `pending.go` 里搬出来。

**Files:**
- Modify: `internal/proto/status.go:30-36`（`BuildInfo`）、`internal/proto/status.go:69-80`（`UpdateStatus`）
- Create: `internal/proto/update.go`
- Modify: `internal/buildinfo/buildinfo.go:47-67`
- Create: `internal/selfupdate/managed.go`
- Modify: `internal/selfupdate/pending.go`（只搬走 `IsManaged`，其余留到 T8 再删）
- Modify: `internal/agentd/status.go:103-110`
- Modify: `cmd/status.go:103-111`
- Test: `internal/buildinfo/buildinfo_test.go`、`internal/selfupdate/managed_test.go`、`internal/agentd/status_test.go`、`cmd/status_test.go`

**Interfaces:**
- Consumes: T1 无依赖
- Produces:
  - `proto.BuildInfo.Platform string`（json `platform,omitempty`）
  - `proto.UpdateStatus{ Managed bool }`（`Pending` / `DownloadedAt` 已删）
  - `proto.UpdateResp{ OK bool; Version, Prev string; Restarted bool }`
  - `proto.UpdateError{ Error, Reason string }`，常量 `proto.UpdateReasonBusy = "busy"`、`proto.UpdateReasonUnmanaged = "unmanaged"`
  - `selfupdate.IsManaged(getenv func(string) string) bool`（位置变更，签名不变）

- [ ] **Step 1: 写失败测试**

`internal/buildinfo/buildinfo_test.go` 追加：

```go
// TestReadFillsPlatform 锁住「两条返回路径都填 Platform」。
//
// why：Read 有一条降级分支（读不到 debug.BuildInfo 时只返回 Version），
// 只在主路径填就会让「非 go build 产物」的 agentd 报空平台，而空平台
// 在远程升级里的语义是「对端过旧，拒绝升级」——一个填漏导致的假拒绝。
func TestReadFillsPlatform(t *testing.T) {
	bi, _ := Read()
	want := runtime.GOOS + "/" + runtime.GOARCH
	if bi.Platform != want {
		t.Fatalf("Platform = %q，期望 %q", bi.Platform, want)
	}
}
```

`internal/selfupdate/managed_test.go` 新建（把 `pending_test.go` 里 `IsManaged` 的用例整体搬来，**必须包含 PPID 反例**）：

```go
// TestIsManagedIgnoresPPID 是这条防线最重要的反例：手工 nohup 起的进程
// 被 init 收养后 PPID 同样是 1，拿 PPID 当判据会把所有裸进程误判成托管，
// 正好把「非托管则拒绝换版」这条防线打穿。
func TestIsManagedIgnoresPPID(t *testing.T) {
	if IsManaged(func(string) string { return "" }) {
		t.Fatal("环境变量全空必须判非托管（fail-closed）")
	}
}

func TestIsManagedSystemd(t *testing.T) {
	if !IsManaged(func(k string) string {
		if k == "INVOCATION_ID" {
			return "abc"
		}
		return ""
	}) {
		t.Fatal("INVOCATION_ID 非空应判托管")
	}
}

func TestIsManagedLaunchdPlaceholder(t *testing.T) {
	// 从 Finder / Terminal.app 启动的进程会继承 XPC_SERVICE_NAME=0
	//（launchd 给非 XPC 服务的占位值），只判非空会把桌面上手动跑的误判成托管
	if IsManaged(func(k string) string {
		if k == "XPC_SERVICE_NAME" {
			return "0"
		}
		return ""
	}) {
		t.Fatal("XPC_SERVICE_NAME=0 是占位值，必须判非托管")
	}
}
```

`internal/agentd/status_test.go` 追加（沿用该文件已有的 Manager 构造 helper；若没有，参照文件里现成的 status 测试写法）：

```go
// TestStatusAlwaysReportsUpdate 锁住「Update 恒返回」。
//
// why：改之前 Update 只在 pending.json 存在时才填，而现在闸二与巡检
// 每台机器都要读 Managed——只在特殊情况下才给的字段，消费方拿到 nil
// 只能猜，而猜出来的诊断会说谎。
func TestStatusAlwaysReportsUpdate(t *testing.T) {
	m := newTestManager(t) // 复用本文件既有构造
	resp, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Update == nil {
		t.Fatal("Update 必须恒返回，nil 的语义是「对端没给」")
	}
	if resp.Version.Platform == "" {
		t.Fatal("Platform 必须上报，空串的语义是「对端过旧」")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/buildinfo/ ./internal/selfupdate/ ./internal/agentd/ -run 'Platform|IsManaged|AlwaysReportsUpdate' -v`
Expected: FAIL — buildinfo 报 `bi.Platform undefined`，selfupdate 报 `managed_test.go` 与 `pending_test.go` 重复声明（Step 3 搬走后消失），agentd 报 `Update` 为 nil

- [ ] **Step 3: 实现**

`internal/proto/status.go` 的 `BuildInfo` 追加字段：

```go
	// Platform 是构建目标平台，形如 "linux/amd64"，在 buildinfo.Read() 里用
	// runtime.GOOS + "/" + runtime.GOARCH 现算填入（CLI 与 agentd 同一条路径，
	// 不会出现只有一端填的情况）。
	//
	// **空串表示对端没给这个字段**（老 agentd）。此时远程升级必须明确拒绝而不是
	// 猜一个默认值——猜错就是给一台 linux 机器推一个 darwin 二进制，自检会拦下，
	// 但那是白跑一次 15MB 上传换来的一条晦涩错误。
	Platform string `json:"platform,omitempty"`
```

`internal/proto/status.go` 的 `UpdateStatus` 整体替换：

```go
// UpdateStatus 是这台 agentd 与「换版」有关的状态。
//
// 字段说明：
//   - Managed: 当前 agentd 进程是不是被进程管理器（systemd / launchd）拉起的。
//     **false 时换版被硬拒绝**——换完 exit(0) 之后没人拉起，这台机器上就此
//     没有 agentd 在跑，且没有任何信号告诉任何人。`--force` 也不越过这一条
//
// 为什么没有「待命版本」了：B59 取消了「下载完等空闲窗口再换」的自主决策，
// 换版由操作者一条命令触发并当场完成，中间不存在待命态（见 B59 spec D1）。
type UpdateStatus struct {
	Managed bool `json:"managed"`
}
```

`internal/proto/update.go` 新建：

```go
// update.go —— POST /api/update 的线格式。
//
// 职责：
//   - 定义换版接口的成功响应、拒绝响应与可判别的拒绝原因常量
//
// 边界：
//   - 只有数据，无行为、无 I/O（与本包其余部分同规格）
//   - 请求参数不在这里：tag / sha256 / force 走 query，body 是 tar.gz 原文，
//     没有 JSON 请求体可定义
package proto

// UpdateResp 是换版成功的响应。
//
// Restarted 恒为 true——接口返回 200 就意味着 agentd 随后会触发优雅关停。
// 保留这个字段是为了让消费方读代码时不必去猜「返回之后还会发生什么」。
type UpdateResp struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version,omitempty"` // 换上的版本；纯重启模式为空
	Prev      string `json:"prev,omitempty"`    // 旧二进制留存路径，回滚要用
	Restarted bool   `json:"restarted"`
}

// 换版被拒的可判别原因。
//
// 为什么要机器可读而不只给一句人话：两种拒绝的处置**完全不同**——活跃任务
// 可以 --force 越过，非托管不行。CLI 要据此选处置建议，而给一条注定失败的
// 命令比不给更糟（spec §4.6）。
const (
	// UpdateReasonBusy: 有 running / waiting_answer 任务，且未带 force
	UpdateReasonBusy = "busy"
	// UpdateReasonUnmanaged: agentd 非托管启动，换完没人拉起。force 不越过
	UpdateReasonUnmanaged = "unmanaged"
)

// UpdateError 是换版被拒的响应体。
//
// Reason 为空表示这次失败不属于上面两道闸（参数错、校验不过、自检不过等），
// 此时消费方**不该编处置建议**，只报原始错误原文。
type UpdateError struct {
	Error  string `json:"error"`
	Reason string `json:"reason,omitempty"`
}
```

`internal/buildinfo/buildinfo.go` 的 `Read()` 两条返回路径都填平台：

```go
func Read() (proto.BuildInfo, bool) {
	// 平台是编译期确定的，与能否读到 debug.BuildInfo 无关——两条返回路径
	// 都必须带上它，漏一条就会让「非 go build 产物」的 agentd 报空平台，
	// 而空平台在远程升级里的语义是「对端过旧，拒绝」，等于一个填漏变成假拒绝
	platform := runtime.GOOS + "/" + runtime.GOARCH
	bi, ok := readBuildInfo()
	if !ok {
		return proto.BuildInfo{Version: releaseVersion, Platform: platform}, false
	}
	out := proto.BuildInfo{Go: bi.GoVersion, Version: releaseVersion, Platform: platform}
	// ...（下面的 for 循环原样不动）
```

`internal/selfupdate/managed.go` 新建：把 `pending.go:88-118` 的 `IsManaged` **连同全部注释原样搬过来**，加文件头注释：

```go
// managed.go —— 判断当前进程是不是被进程管理器拉起的。
//
// 职责：
//   - IsManaged：systemd / launchd 托管判据，fail-closed
//
// 边界：
//   - 只读环境变量，不看进程树、不读 /proc、不执行任何命令
//   - **绝不用 PPID**：理由见 IsManaged 的注释，这是整条防线最容易被打穿的地方
package selfupdate
```

从 `pending.go` 删掉 `IsManaged`，从 `pending_test.go` 删掉它的三条用例（已搬到 `managed_test.go`）。

`internal/agentd/status.go:103-110` 替换：

```go
	// 换版相关状态：**恒返回**。闸二（非托管则拒绝换版）与 upgrade 的巡检表
	// 每台机器都要读它，只在特殊情况下才给的字段会让消费方拿 nil 去猜
	resp.Update = &proto.UpdateStatus{Managed: selfupdate.IsManaged(os.Getenv)}
```

`cmd/status.go:103-111` 替换：

```go
		if u := st.Update; u != nil && !u.Managed {
			// 非托管的后果要在这里说清楚：handoff upgrade 会硬拒绝这台机器，
			// 而且 --force 也不越过。不说，用户只会看到一条没头没脑的拒绝
			fmt.Fprintf(w, "更新     agentd 非托管启动，换版会被拒绝（--force 也不越过）\n")
			fmt.Fprintf(w, "         处置 在该机器上 handoff service install\n")
		}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./... && go test ./internal/... ./cmd/... 2>&1 | tail -30`
Expected: 全部 PASS。若 `internal/selfupdate` 报 `LoadPending` 未使用之类的编译错，说明 `status.go` 的 import 没清干净——把 `selfupdate` 的 import 保留（仍用 `IsManaged`），去掉不再需要的。

- [ ] **Step 5: 加关键节点日志**

- `internal/agentd/status.go`：原先「读待命更新失败」那条 Warn 随代码一起删掉（不再读文件，没有这个失败分支了）。在状态聚合完成的那条 Info 上追加 `"managed", resp.Update.Managed` —— 这是排查「为什么这台机器升不了」时第一个要看的值。
- `internal/buildinfo`、`internal/proto` 是纯数据/纯计算，**不加日志**（本项目 instrumenting-code 的例外条款：无 I/O、无外部调用、无状态变更）。

- [ ] **Step 6: 加意图注释**

- 上面三段实现代码里的注释已就位，逐条核对是否落到了文件里（尤其 `Platform` 的「空串表示对端没给」、`UpdateStatus` 的「为什么没有待命版本了」、`managed.go` 的文件头）。
- `internal/proto/update.go` 是新文件，确认文件头注释含职责与边界。
- `internal/selfupdate/pending.go` 的文件头注释里若提到 `IsManaged`，改指向 `managed.go`。

- [ ] **Step 7: 提交**

```bash
git add internal/proto internal/buildinfo internal/selfupdate internal/agentd/status.go cmd/status.go && git commit -m "feat(proto): BuildInfo 加 Platform，UpdateStatus 只留 Managed

远程升级必须知道对端平台才知道下哪个资产；Platform 在 buildinfo.Read()
里填，CLI 与 agentd 同一条路径。UpdateStatus 去掉待命概念（B59 取消了
自主换版决策），只留 Managed 且恒返回——闸二与巡检每台机器都要读它。
IsManaged 从行将删除的 pending.go 搬到 managed.go，PPID 反例一并搬走。"
```

---

### Task 3: agentd 侧 `POST /api/update`

**Files:**
- Create: `internal/agentd/update.go`
- Modify: `internal/agentd/server.go`（`Server` 结构体、`NewServer`、路由表 `:135-151`）
- Modify: `cmd/agentd.go:180-200`（注入 `SetRestart`）
- Test: `internal/agentd/update_test.go`

**Interfaces:**
- Consumes: T1 的 `(*release.Installer).InstallArchive`、`release.Activate(newPath, target string) (string, error)`、T2 的 `proto.UpdateResp` / `proto.UpdateError` / `proto.UpdateReasonBusy` / `proto.UpdateReasonUnmanaged` / `selfupdate.IsManaged`、既有 `(*Shutdown).Trigger(reason string) bool`
- Produces:
  - `func (s *Server) SetRestart(fn func(reason string) bool)`
  - `type UpdateDeps struct { Getenv func(string) string; Executable func() (string, error); Install func(tgz []byte, wantSum, wantTag, destDir string) (string, error); Activate func(newPath, target string) (string, error) }`
  - `func (s *Server) SetUpdateDeps(d UpdateDeps)`（测试缝）
  - 路由 `POST /api/update`

**接口契约（写死在这里，T4 的客户端按它实现）：**

```
POST /api/update?tag=<v0.1.1>&sha256=<64位hex>&force=<1|空>
Authorization: Bearer <token>
Content-Type: application/octet-stream
Body: tar.gz 资产原文；**body 为空 = 只重启不换版**（D8）

200 -> proto.UpdateResp
409 -> proto.UpdateError（reason = busy | unmanaged）
400 -> proto.UpdateError（reason 为空：参数缺失、sha256 不符、解包/自检失败）
500 -> proto.UpdateError（reason 为空：换版本身失败）
```

- [ ] **Step 1: 写失败测试**

`internal/agentd/update_test.go` 新建。沿用本包既有的 `httptest` + `NewServer` 搭法（参考 `server_test.go` 里已有的构造），核心用例：

```go
// newUpdateServer 起一个只用于换版接口的 Server：注入全部外部依赖，
// 一次也不碰真实二进制、真实进程管理器、真实 GitHub。
func newUpdateServer(t *testing.T, st *store.Store, managed bool) (*Server, *[]string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(&config.Config{Token: "tk", DataDir: t.TempDir()}, st, log)
	acts := &[]string{}
	target := filepath.Join(t.TempDir(), "handoff")
	os.WriteFile(target, []byte("old"), 0o755)
	srv.SetUpdateDeps(UpdateDeps{
		Getenv: func(k string) string {
			if managed && k == "INVOCATION_ID" {
				return "test"
			}
			return ""
		},
		Executable: func() (string, error) { return target, nil },
		Install: func(_ []byte, _, tag, destDir string) (string, error) {
			p := filepath.Join(destDir, ".handoff.new-"+tag)
			os.WriteFile(p, []byte("new"), 0o755)
			return p, nil
		},
		Activate: func(newPath, tgt string) (string, error) {
			*acts = append(*acts, newPath+"->"+tgt)
			return tgt + ".prev", nil
		},
	})
	srv.SetRestart(func(reason string) bool { *acts = append(*acts, "restart:"+reason); return true })
	return srv, acts
}

// post 发一次换版请求，返回状态码与解出的错误体（200 时 UpdateError 为零值）。
func post(t *testing.T, srv *Server, query string, body []byte) (int, proto.UpdateError, proto.UpdateResp) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/update?"+query, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tk")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var e proto.UpdateError
	var ok proto.UpdateResp
	json.Unmarshal(w.Body.Bytes(), &e)
	json.Unmarshal(w.Body.Bytes(), &ok)
	return w.Code, e, ok
}

// TestUpdateRejectsUnmanagedEvenWithForce 是闸二的核心断言，也是整个接口
// 最不能出错的一条：换完 exit(0) 之后没人拉起，这台机器上就此没有 agentd
// 在跑，且没有任何信号告诉任何人。force 不是逃生口。
func TestUpdateRejectsUnmanagedEvenWithForce(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, false /*managed*/)
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64)+"&force=1", []byte("tgz"))
	if code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409", code)
	}
	if e.Reason != proto.UpdateReasonUnmanaged {
		t.Fatalf("reason = %q，期望 %q", e.Reason, proto.UpdateReasonUnmanaged)
	}
	if len(*acts) != 0 {
		t.Fatalf("被拒后不该有任何换版或重启动作，实得 %v", *acts)
	}
}

// TestUpdateRejectsBusyWithoutForce / TestUpdateForceCrossesBusy 是闸一的
// 两半：默认保护，--force 越过。activeTaskCount 的口径是 running +
// waiting_answer，waiting_review 不计入（它可能挂几天）。
func TestUpdateRejectsBusyWithoutForce(t *testing.T) {
	st := newTestStore(t)
	seedTask(t, st, proto.TaskStateRunning)
	srv, acts := newUpdateServer(t, st, true)
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64), []byte("tgz"))
	if code != http.StatusConflict || e.Reason != proto.UpdateReasonBusy {
		t.Fatalf("期望 409/busy，实得 %d/%q", code, e.Reason)
	}
	if len(*acts) != 0 {
		t.Fatalf("被拒后不该有动作，实得 %v", *acts)
	}
}

func TestUpdateForceCrossesBusy(t *testing.T) {
	st := newTestStore(t)
	seedTask(t, st, proto.TaskStateRunning)
	srv, acts := newUpdateServer(t, st, true)
	code, _, ok := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64)+"&force=1", []byte("tgz"))
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if !ok.OK || ok.Version != "v9.9.9" || ok.Prev == "" {
		t.Fatalf("响应体不完整: %+v", ok)
	}
	if len(*acts) != 2 || !strings.HasPrefix((*acts)[1], "restart:") {
		t.Fatalf("必须先 Activate 再 restart，实得 %v", *acts)
	}
}

// TestUpdateWaitingReviewDoesNotBlock 锁住活跃口径：waiting_review 不计入。
func TestUpdateWaitingReviewDoesNotBlock(t *testing.T) {
	st := newTestStore(t)
	seedTask(t, st, proto.TaskStateWaitingReview)
	srv, _ := newUpdateServer(t, st, true)
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64), []byte("tgz"))
	if code != http.StatusOK {
		t.Fatalf("waiting_review 不该拦下换版，实得 %d/%q", code, e.Error)
	}
}

// TestUpdateEmptyBodyRestartsOnly 是 D8：不带 body = 只重启不换版。
// 本机的二进制由 CLI 直接换（文件就在本地），但仍需要 agentd 重启才生效。
func TestUpdateEmptyBodyRestartsOnly(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, true)
	code, _, ok := post(t, srv, "", nil)
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", code)
	}
	if ok.Version != "" {
		t.Fatalf("纯重启不该报换上的版本，实得 %q", ok.Version)
	}
	if len(*acts) != 1 || !strings.HasPrefix((*acts)[0], "restart:") {
		t.Fatalf("纯重启只该有 restart 一个动作，实得 %v", *acts)
	}
}

// TestUpdateRequiresTagAndSum：带 body 就必须给 tag 与 sha256，
// 缺任一个都不能放行——少了它们，agentd 侧的完整性校验与自检都无从比对。
func TestUpdateRequiresTagAndSum(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, true)
	for _, q := range []string{"", "tag=v9.9.9", "sha256=" + strings.Repeat("a", 64)} {
		code, e, _ := post(t, srv, q, []byte("tgz"))
		if code != http.StatusBadRequest {
			t.Fatalf("query %q 应 400，实得 %d", q, code)
		}
		if e.Reason != "" {
			t.Fatalf("参数错不属于两道闸，reason 必须为空，实得 %q", e.Reason)
		}
	}
	if len(*acts) != 0 {
		t.Fatalf("参数错不该有动作，实得 %v", *acts)
	}
}

// TestUpdateInstallFailureDoesNotActivate：校验/解包/自检任一失败，
// 都不许走到 Activate——换上一个跑不起来的二进制，agentd 就再也起不来了。
func TestUpdateInstallFailureDoesNotActivate(t *testing.T) {
	st := newTestStore(t)
	srv, acts := newUpdateServer(t, st, true)
	srv.SetUpdateDeps(UpdateDeps{
		Getenv:     func(k string) string { if k == "INVOCATION_ID" { return "test" }; return "" },
		Executable: func() (string, error) { return filepath.Join(t.TempDir(), "handoff"), nil },
		Install:    func([]byte, string, string, string) (string, error) { return "", errors.New("自检失败") },
		Activate:   func(string, string) (string, error) { t.Fatal("不该走到 Activate"); return "", nil },
	})
	code, e, _ := post(t, srv, "tag=v9.9.9&sha256="+strings.Repeat("a", 64), []byte("tgz"))
	if code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", code)
	}
	if !strings.Contains(e.Error, "自检失败") {
		t.Fatalf("错误原文必须带出根因，实得 %q", e.Error)
	}
	if len(*acts) != 0 {
		t.Fatalf("失败后不该有动作，实得 %v", *acts)
	}
}
```

> `newTestStore` / `seedTask` 沿用本包已有的测试 helper；若不存在同名的，按 `internal/agentd` 里现成的任务落库写法自行补一个最小实现，**不要**引入新的测试框架。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestUpdate -v`
Expected: FAIL，`srv.SetUpdateDeps undefined` / `srv.SetRestart undefined`

- [ ] **Step 3: 实现**

`internal/agentd/update.go` 新建：

```go
// update.go —— POST /api/update：接收推来的二进制，换版并触发重启。
//
// 职责：
//   - 复检两道闸（活跃任务 / 非托管），拒绝时给出可判别的 reason
//   - 校验 sha256、解包、自检、原子换版并保留 .prev
//   - 换版成功后触发优雅关停，由进程管理器拉起新二进制
//
// 边界：
//   - **不出网**：资产由 CLI 下载并推来，这里只收字节（B59 spec D1）
//   - 不做回滚编排：换版失败时 release.Activate 自己把 .prev 换回去，
//     人工回滚是 handoff upgrade --rollback，不在这条路径上
//   - 不做鉴权加码：持有 bearer token 的人本来就能 handoff run 执行任意命令，
//     推二进制不构成提权。token 就是信任边界（spec D4）
package agentd

// UpdateDeps 是换版接口的外部依赖集合。
//
// 抽成结构体而不是散落的包级变量：这些依赖全都是「会真的动这台机器」的动作
// （执行文件、rename 二进制、停进程），测试必须能整体替换掉，漏替一个就会
// 在 CI 上真的把测试二进制 rename 掉。
type UpdateDeps struct {
	// Getenv 取环境变量，闸二的判据来源
	Getenv func(string) string
	// Executable 返回当前二进制的真实路径（须已 EvalSymlinks）
	Executable func() (string, error)
	// Install 校验+解包+自检，返回可供 Activate 的临时文件路径
	Install func(tgz []byte, wantSum, wantTag, destDir string) (string, error)
	// Activate 原子换版，返回旧二进制的留存路径
	Activate func(newPath, target string) (string, error)
}

// handleUpdate 处理换版请求。
//
// 请求：POST /api/update?tag=&sha256=&force=，body 为 tar.gz 原文；
// **body 为空表示只重启不换版**——本机的二进制由 CLI 直接换掉了，
// 但正在跑的 agentd 仍是旧进程，需要重启才生效（spec D8）。
//
// 注意：
//   - 两道闸的检查顺序是「闸二在前」：非托管是硬拒绝，先说这一条能让操作者
//     少绕一圈（他会先去装服务，而不是先去等任务结束）
//   - 触发关停必须在写完响应之后。优雅关停会等在途请求结束，所以本 handler
//     返回前进程不会退——但反过来先 Trigger 再写响应，客户端拿到的就是一个
//     断掉的连接，等于把一次成功的换版报成失败
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") != ""
	tag := r.URL.Query().Get("tag")
	sum := r.URL.Query().Get("sha256")
	s.log.Info("换版请求", "tag", tag, "force", force, "content_length", r.ContentLength)

	// 闸二在闸一之前：非托管是硬拒绝（force 也不越过），先说这一条能让操作者
	// 少绕一圈——他会直接去装服务，而不是先去等任务结束再撞第二堵墙
	if !selfupdate.IsManaged(s.upd.Getenv) {
		s.log.Warn("换版被拒：agentd 非托管启动", "tag", tag, "force", force)
		writeJSON(w, http.StatusConflict, proto.UpdateError{
			Error:  "agentd 非托管启动，换版后没有进程管理器把它拉起来",
			Reason: proto.UpdateReasonUnmanaged,
		})
		return
	}

	// 闸一：活跃任务，force 可越过
	busy, err := s.activeCount()
	if err != nil {
		s.log.Error("换版预检：查任务列表失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, proto.UpdateError{Error: "内部错误"})
		return
	}
	if busy > 0 && !force {
		s.log.Warn("换版被拒：有活跃任务", "tag", tag, "busy", busy)
		writeJSON(w, http.StatusConflict, proto.UpdateError{
			Error:  fmt.Sprintf("有 %d 个活跃任务（running/waiting_answer）", busy),
			Reason: proto.UpdateReasonBusy,
		})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxUpdateBytes))
	if err != nil {
		s.log.Error("换版：读请求体失败", "tag", tag, "cause", err)
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{Error: "读请求体: " + err.Error()})
		return
	}

	// 纯重启模式（D8）
	if len(body) == 0 {
		s.log.Info("换版：body 为空，只重启不换版", "busy", busy)
		writeJSON(w, http.StatusOK, proto.UpdateResp{OK: true, Restarted: true})
		s.triggerRestart("收到重启请求")
		return
	}

	if tag == "" || sum == "" {
		s.log.Warn("换版被拒：带 body 但缺 tag 或 sha256", "tag", tag, "has_sum", sum != "")
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{
			Error: "带二进制时 tag 与 sha256 都必须给：缺了它们无从校验完整性，也无从自检",
		})
		return
	}

	target, err := s.upd.Executable()
	if err != nil {
		s.log.Error("换版：取当前二进制路径失败", "tag", tag, "cause", err)
		writeJSON(w, http.StatusInternalServerError, proto.UpdateError{Error: "取当前二进制路径: " + err.Error()})
		return
	}
	// 临时文件必须与目标同目录：os.Rename 的原子性只在同一文件系统内成立
	s.log.Info("换版：开始校验与解包", "tag", tag, "target", target, "bytes", len(body))
	newPath, err := s.upd.Install(body, sum, tag, filepath.Dir(target))
	if err != nil {
		s.log.Error("换版被拒：校验或自检未通过", "tag", tag, "cause", err)
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{Error: err.Error()})
		return
	}
	prev, err := s.upd.Activate(newPath, target)
	if err != nil {
		s.log.Error("换版失败：替换二进制出错", "tag", tag, "target", target, "cause", err)
		writeJSON(w, http.StatusInternalServerError, proto.UpdateError{Error: "替换二进制: " + err.Error()})
		return
	}

	s.log.Info("换版完成，准备重启", "tag", tag, "target", target, "prev", prev, "busy", busy)
	writeJSON(w, http.StatusOK, proto.UpdateResp{OK: true, Version: tag, Prev: prev, Restarted: true})
	s.triggerRestart("换版到 " + tag)
}

// activeCount 返回活跃任务数（running + waiting_answer）。
//
// waiting_review 不计入：它在等审核者裁决，挂几天都正常，计入等于让升级
// 被无限期阻塞（沿用 B54.3 的 D12）。
func (s *Server) activeCount() (int, error) {
	tasks, err := s.st.ListTasks()
	if err != nil {
		return 0, fmt.Errorf("列任务: %w", err)
	}
	n := 0
	for _, t := range tasks {
		if t.State == proto.TaskStateRunning || t.State == proto.TaskStateWaitingAnswer {
			n++
		}
	}
	return n, nil
}

// triggerRestart 触发优雅关停。restart 未注入时只打日志——这只可能发生在
// 测试或 bootstrap 顺序出错时，静默返回会让「换版成功但永远不重启」变成
// 一个查不出根因的现象。
func (s *Server) triggerRestart(reason string) {
	if s.restart == nil {
		s.log.Error("换版后无法触发重启：restart 未注入", "reason", reason)
		return
	}
	if !s.restart(reason) {
		s.log.Warn("触发重启：已在关停中，本次触发被忽略", "reason", reason)
	}
}
```

`internal/agentd/server.go` 改动三处：

1. `Server` 结构体加两个字段：
```go
	// upd 是换版接口的外部依赖，NewServer 填生产实现，测试整体替换
	upd UpdateDeps
	// restart 触发优雅关停，由 cmd/agentd.go 注入 Shutdown.Trigger。
	// nil 表示未注入（只会发生在测试或 bootstrap 顺序出错时）
	restart func(reason string) bool
```

2. `NewServer` 里填生产默认值：
```go
	inst := release.NewInstaller(log)
	s.upd = UpdateDeps{
		Getenv:     os.Getenv,
		Executable: resolvedExecutable,
		Install:    inst.InstallArchive,
		Activate:   release.Activate,
	}
```
并在本包内加一个 `resolvedExecutable`（与 `cmd/agentd.go:295` 同款，**必须 EvalSymlinks**：装在 `~/.local/bin` 的二进制常常是 symlink，替换 symlink 本身只会把链接换成普通文件）。

3. 路由表加一行 `mux.HandleFunc("POST /api/update", s.handleUpdate)`；并加常量 `maxUpdateBytes = 100 << 20`（与 `release.maxAssetBytes` 同量级，注释说明「上限本身是防线：被劫持或出错的请求不该把内存吃光」）。

4. 两个注入点：
```go
// SetRestart 注入优雅关停的触发函数（Shutdown.Trigger）。
//
// 必须在监听之前注入：换版接口返回 200 之后就靠它退出进程交接给新二进制，
// 没注入时换版会成功但永远不重启，而现场只剩一个「版本没变」的空结论。
func (s *Server) SetRestart(fn func(reason string) bool) { s.restart = fn }

// SetUpdateDeps 替换换版接口的外部依赖。**仅供测试**：这些依赖会真的
// 执行文件、rename 二进制、停进程。
func (s *Server) SetUpdateDeps(d UpdateDeps) { s.upd = d }
```

`cmd/agentd.go`：在 `sd := agentd.NewShutdown(logger)` 之后、`sd.Serve(...)` 之前加一行
```go
	// 换版接口靠它退出进程，交接给进程管理器拉起的新二进制
	srv.SetRestart(sd.Trigger)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestUpdate -v && go build ./...`
Expected: 全部 PASS

- [ ] **Step 5: 加关键节点日志**

上面实现里已内联，逐条核对齐全：
- 入口 Info 带 `tag` / `force` / `content_length`
- 闸一、闸二各一条 Warn，带拒绝理由的数值（`busy` 个数 / `force` 取值）
- 读 body 失败、参数缺失、校验失败、Activate 失败四条各一条 Error/Warn，**都带 `cause` 原文**
- 纯重启模式一条 Info（否则「什么都没换但重启了」在日志里是一片空白）
- 换版成功一条 Info，带 `target` 与 `prev`——`prev` 是回滚时唯一要用的路径
- `triggerRestart` 的未注入分支一条 Error

- [ ] **Step 6: 加意图注释**

- `update.go` 文件头：职责 + 三条边界（不出网 / 不做回滚编排 / 不做鉴权加码）。
- `handleUpdate` doc：请求形态、body 为空的语义、**两道闸的顺序为什么是闸二在前**、**为什么必须先写响应再 Trigger**。
- `activeCount` doc：为什么 `waiting_review` 不计入。
- `UpdateDeps` doc：为什么抽成结构体（测试必须能整体替换，漏替一个会真的动 CI 机器）。
- `SetRestart` doc：不注入的后果。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/update.go internal/agentd/update_test.go internal/agentd/server.go cmd/agentd.go && git commit -m "feat(agentd): 新增 POST /api/update 换版接口

收 CLI 推来的 tar.gz，复检两道闸（非托管硬拒绝且 --force 不越过、活跃任务
默认拒绝可 --force 越过），校验 sha256、解包、自检、原子换版保留 .prev，
写完响应后触发优雅关停交接给进程管理器。body 为空时只重启不换版（D8）。"
```

---

### Task 4: 客户端侧推送与轮询确认

**Files:**
- Create: `internal/client/update.go`
- Test: `internal/client/update_test.go`

**Interfaces:**
- Consumes: T2 的 `proto.UpdateResp` / `proto.UpdateError` / 两个 reason 常量、T3 的接口契约、既有 `Client{baseURL, token, hc}`、`(*Client) Status(ctx)`、`(*Client) log()`、`ErrStatusUnsupported`
- Produces:
  - `type UpdateRejected struct { Reason, Msg string }`，`func (e *UpdateRejected) Error() string`
  - `var ErrUpdateUnsupported = errors.New("对端 agentd 不支持 /api/update")`
  - `func (c *Client) PushUpdate(ctx context.Context, tag, sum string, tgz []byte, force bool) (*proto.UpdateResp, error)`
  - `func (c *Client) RestartAgentd(ctx context.Context, force bool) (*proto.UpdateResp, error)`
  - `func (c *Client) WaitVersion(ctx context.Context, want string, timeout, interval time.Duration) error`

- [ ] **Step 1: 写失败测试**

```go
// TestPushUpdateSurfacesReason 锁住「拒绝原因必须可判别」。
//
// why：busy 与 unmanaged 的处置完全不同（前者能 --force，后者不能），
// 把 409 压成一句人话字符串，CLI 就只能靠 strings.Contains 猜——而猜出来的
// 处置建议会给用户一条注定失败的命令。
func TestPushUpdateSurfacesReason(t *testing.T) {
	for _, tc := range []struct{ reason string }{
		{proto.UpdateReasonBusy}, {proto.UpdateReasonUnmanaged},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(proto.UpdateError{Error: "拒了", Reason: tc.reason})
		}))
		_, err := New(srv.URL, "tk").PushUpdate(context.Background(), "v1", strings.Repeat("a", 64), []byte("x"), false)
		var rej *UpdateRejected
		if !errors.As(err, &rej) {
			t.Fatalf("reason=%s：期望 *UpdateRejected，实得 %v", tc.reason, err)
		}
		if rej.Reason != tc.reason {
			t.Fatalf("Reason = %q，期望 %q", rej.Reason, tc.reason)
		}
		srv.Close()
	}
}

// TestPushUpdateOldAgentd：v0.1.0 的 agentd 没有这个端点，404 必须译成
// 一条可判别的哨兵，而不是一句「状态码 404」——巡检要据此说「对端过旧，
// 这一跳得手工做」，那是一条有用的结论，不是一个失败。
func TestPushUpdateOldAgentd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "tk").PushUpdate(context.Background(), "v1", strings.Repeat("a", 64), []byte("x"), false)
	if !errors.Is(err, ErrUpdateUnsupported) {
		t.Fatalf("期望 ErrUpdateUnsupported，实得 %v", err)
	}
}

// TestPushUpdateSendsRawBodyAndParams 锁住线格式：body 是 tar.gz 原文，
// tag / sha256 / force 走 query。
func TestPushUpdateSendsRawBodyAndParams(t *testing.T) {
	var gotBody []byte
	var gotQuery url.Values
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotQuery = r.URL.Query()
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(proto.UpdateResp{OK: true, Version: "v1", Prev: "/x.prev", Restarted: true})
	}))
	defer srv.Close()
	resp, err := New(srv.URL, "tk").PushUpdate(context.Background(), "v1", strings.Repeat("a", 64), []byte("TGZ"), true)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBody) != "TGZ" {
		t.Fatalf("body = %q，期望原样的 tar.gz 字节", gotBody)
	}
	if gotQuery.Get("tag") != "v1" || gotQuery.Get("sha256") == "" || gotQuery.Get("force") == "" {
		t.Fatalf("query 不全: %v", gotQuery)
	}
	if gotAuth != "Bearer tk" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if resp.Prev != "/x.prev" {
		t.Fatalf("Prev 必须带出来，回滚要用它")
	}
}

// TestRestartAgentdSendsNoBody 锁住 D8 的纯重启模式。
func TestRestartAgentdSendsNoBody(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		n = len(b)
		json.NewEncoder(w).Encode(proto.UpdateResp{OK: true, Restarted: true})
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "tk").RestartAgentd(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("纯重启必须不带 body，实得 %d 字节", n)
	}
}

// TestWaitVersionTimesOut：新进程没起来时必须超时报错，绝不能悄悄成功。
// 这是「不确认就不报成功」那条纪律在客户端的落点。
func TestWaitVersionTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(proto.StatusResp{Version: proto.BuildInfo{Version: "v0"}})
	}))
	defer srv.Close()
	err := New(srv.URL, "tk").WaitVersion(context.Background(), "v1", 150*time.Millisecond, 20*time.Millisecond)
	if err == nil {
		t.Fatal("版本一直没变必须报超时")
	}
}

// TestWaitVersionSucceedsAfterRestart：重启期间 status 会连不上（连接被拒），
// 那不是失败，是过程——必须继续等，而不是第一次 dial 失败就放弃。
func TestWaitVersionSucceedsAfterRestart(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(proto.StatusResp{Version: proto.BuildInfo{Version: "v1"}})
	}))
	defer srv.Close()
	if err := New(srv.URL, "tk").WaitVersion(context.Background(), "v1", 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatalf("中途的失败是重启过程，不该放弃: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/client/ -run 'PushUpdate|RestartAgentd|WaitVersion' -v`
Expected: FAIL，`PushUpdate undefined` 等

- [ ] **Step 3: 实现**

```go
// update.go —— 换版接口的客户端侧：推送二进制、触发重启、轮询确认上线。
//
// 职责：
//   - PushUpdate / RestartAgentd：调 POST /api/update 的两种模式
//   - WaitVersion：换版后轮询 status 直到新版本上线或超时
//
// 边界：
//   - 不下载、不校验资产：那是 internal/release 的职责，本层只搬字节
//   - 不做重试：换版是有副作用的动作，失败了要让操作者看见并决定
package client

// UpdateRejected 是被两道闸拒绝时的错误。
//
// 为什么要带 Reason 而不只是一句话：busy 与 unmanaged 的处置**完全不同**
// ——前者能 --force 越过，后者不能。把它压成字符串，调用方就只能靠
// strings.Contains 猜，而猜错的代价是给用户一条注定失败的命令。
type UpdateRejected struct {
	Reason string // proto.UpdateReasonBusy / proto.UpdateReasonUnmanaged / ""
	Msg    string
}

func (e *UpdateRejected) Error() string { return e.Msg }

// ErrUpdateUnsupported 表示对端 agentd 不认识 /api/update（v0.1.0 及更早）。
//
// 与 ErrStatusUnsupported 同一条纪律：这是一条**有用的结论**——对端过旧，
// 这一跳必须手工做（spec §8），不是一个含糊的失败。
var ErrUpdateUnsupported = errors.New("对端 agentd 不支持 /api/update")

// PushUpdate 把 tar.gz 资产原文推给对端 agentd 并触发换版重启。
//
// 参数：
//   - tag: 目标版本，agentd 用它做自检比对（新二进制 version 首行必须等于它）
//   - sum: 资产的 sha256（十六进制小写），来自 release 的 checksums.txt
//   - tgz: **tar.gz 原文**，不是解包后的裸二进制——这样三处校验比的是同一个
//     来自 release 的声明，传输两端不会互相背书
//   - force: 越过闸一（活跃任务）。**不越过闸二（非托管）**
//
// 返回：
//   - 成功响应（含 Prev：旧二进制留存路径，回滚要用）
//   - *UpdateRejected（两道闸）/ ErrUpdateUnsupported（对端过旧）/ 其他错误
func (c *Client) PushUpdate(ctx context.Context, tag, sum string, tgz []byte, force bool) (*proto.UpdateResp, error) {
	return c.postUpdate(ctx, tag, sum, tgz, force)
}

// RestartAgentd 让对端 agentd 重启但不换版（body 为空，spec D8）。
//
// 用于本机：二进制由 CLI 直接换掉了，但正在跑的 agentd 仍是旧进程。
func (c *Client) RestartAgentd(ctx context.Context, force bool) (*proto.UpdateResp, error) {
	return c.postUpdate(ctx, "", "", nil, force)
}

func (c *Client) postUpdate(ctx context.Context, tag, sum string, tgz []byte, force bool) (*proto.UpdateResp, error) {
	q := url.Values{}
	if tag != "" {
		q.Set("tag", tag)
	}
	if sum != "" {
		q.Set("sha256", sum)
	}
	if force {
		q.Set("force", "1")
	}
	u := c.baseURL + "/api/update"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if len(tgz) > 0 {
		rd = bytes.NewReader(tgz)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, rd)
	if err != nil {
		return nil, fmt.Errorf("构造换版请求: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	c.log().Info("推送换版请求", "tag", tag, "bytes", len(tgz), "force", force)
	resp, err := c.hc.Do(req)
	if err != nil {
		c.log().Error("换版请求失败", "tag", tag, "cause", err)
		return nil, fmt.Errorf("换版请求: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		c.log().Debug("对端 agentd 不支持 /api/update，按版本过旧处理")
		return nil, ErrUpdateUnsupported
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		var e proto.UpdateError
		// 解不出结构化错误就退回原文：一条读得懂的原文好过一句「解析失败」
		if err := json.Unmarshal(body, &e); err != nil || e.Error == "" {
			c.log().Warn("换版被拒", "tag", tag, "status", resp.StatusCode, "body", string(body))
			return nil, fmt.Errorf("换版: 状态码 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		c.log().Warn("换版被拒", "tag", tag, "status", resp.StatusCode, "reason", e.Reason, "detail", e.Error)
		return nil, &UpdateRejected{Reason: e.Reason, Msg: e.Error}
	}
	var out proto.UpdateResp
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析换版响应: %w", err)
	}
	c.log().Info("换版已受理，对端将重启", "tag", out.Version, "prev", out.Prev)
	return &out, nil
}

// WaitVersion 轮询 status 直到对端版本变成 want，或超时。
//
// 参数：
//   - want: 期望的版本号（形如 v0.1.1）
//   - timeout / interval: 等待时限与轮询间隔（生产取 60s / 2s）
//
// 注意：
//   - **轮询期间的失败一律忽略继续等**。重启窗口里连接被拒、502、503 都是
//     过程而不是结论；第一次 dial 失败就放弃，等于把每一次正常换版都报成失败
//   - 超时返回错误。不确认就报成功是主张不是事实，而 agentd 起不来恰恰是最
//     需要立刻知道的时刻
func (c *Client) WaitVersion(ctx context.Context, want string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for attempt := 1; ; attempt++ {
		st, err := c.Status(ctx)
		switch {
		case err == nil && st.Version.Version == want:
			c.log().Info("新版本已上线", "want", want, "attempts", attempt)
			return nil
		case err == nil:
			last = fmt.Errorf("对端版本仍是 %q", st.Version.Version)
		default:
			last = err
		}
		if time.Now().After(deadline) {
			c.log().Error("等待新版本上线超时", "want", want, "timeout", timeout, "last", last)
			return fmt.Errorf("等待 %s 上线超时（%s）：%w", want, timeout, last)
		}
		c.log().Debug("等待新版本上线", "want", want, "attempt", attempt, "last", last)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/client/ -v 2>&1 | tail -20`
Expected: 全部 PASS（含既有用例）

- [ ] **Step 5: 加关键节点日志**

已内联，核对四处：推送前 Info（tag/bytes/force）、请求失败 Error（带 cause）、被拒 Warn（带 status/reason/detail）、受理成功 Info（带 prev）；`WaitVersion` 的成功 Info、超时 Error、每轮 Debug（Debug 级别是刻意的：60s / 2s 会打 30 行，Info 会把一次正常升级的输出淹掉）。

- [ ] **Step 6: 加意图注释**

已内联，核对：文件头（职责 + 「不做重试」的边界）、`UpdateRejected` 的「为什么要带 Reason」、`ErrUpdateUnsupported` 的「这是结论不是失败」、`PushUpdate` 参数里「为什么是 tar.gz 原文」、`WaitVersion` 的「轮询期间失败一律忽略」。

- [ ] **Step 7: 提交**

```bash
git add internal/client/update.go internal/client/update_test.go && git commit -m "feat(client): 换版接口的推送、重启与轮询确认

PushUpdate 推 tar.gz 原文（query 带 tag/sha256/force），两道闸的拒绝译成
可判别的 *UpdateRejected，404 译成 ErrUpdateUnsupported（对端过旧是结论
不是失败）。WaitVersion 轮询确认新版本上线，重启窗口内的连接失败属过程
不放弃，超时如实报错——不确认就报成功是主张不是事实。"
```

---

### Task 5: `internal/skill` 安装与一致性检查

**Files:**
- Create: `internal/skill/install.go`
- Create: `internal/skill/state.go`
- Test: `internal/skill/install_test.go`、`internal/skill/state_test.go`

**Interfaces:**
- Consumes: 无（纯入参函数，不依赖前面的 task）
- Produces:
  - `type Site struct { Path string; State string; Note string }`
  - 常量 `skill.StateInstalled = "installed"`、`StateSkipped = "skipped"`、`StateInSync = "in_sync"`、`StateStale = "stale"`、`StateMissing = "missing"`
  - `func Install(content, home string) ([]Site, error)`
  - `func Status(content, home string) ([]Site, error)`
  - `func InSync(sites []Site) bool`
  - `func BasePath(home string) string`

**落点表（照搬 `skills/install.sh` 的目录，基准副本位置按 spec §4.7 改到 `~/.handoff/skill`）：**

| 落点 | 形态 |
|---|---|
| `<home>/.handoff/skill/SKILL.md` | 基准副本（真实文件），恒安装 |
| `<home>/.claude/skills/handoff` | 软链 → `<home>/.handoff/skill` |
| `<home>/.codex/skills/handoff` | 软链 → 同上 |
| `<home>/.config/opencode/skills/handoff` | 软链 → 同上 |
| `<home>/.grok/skills/handoff` | 软链 → 同上 |

- [ ] **Step 1: 写失败测试**

```go
// TestInstallSkipsMissingAgentDirs 锁住「目录不存在就跳过，不代为创建」。
//
// why：给没装 codex 的机器造一个 ~/.codex 目录，下次那台机器上真的装了
// codex 时会拿到一个我们凭空造的半截目录结构。不给没装的 agent 造目录是
// skills/install.sh 一直以来的行为，这次搬进二进制不许悄悄改掉。
func TestInstallSkipsMissingAgentDirs(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	sites, err := Install("内容", home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatal("不该给没装的 agent 造目录")
	}
	var installed, skipped int
	for _, s := range sites {
		switch s.State {
		case StateInstalled:
			installed++
		case StateSkipped:
			skipped++
			if s.Note == "" {
				t.Fatalf("跳过必须给理由: %+v", s)
			}
		}
	}
	if installed != 2 { // 基准副本 + .claude
		t.Fatalf("已装落点 = %d，期望 2", installed)
	}
	if skipped != 3 {
		t.Fatalf("跳过落点 = %d，期望 3", skipped)
	}
}

// TestInstallIsIdempotent：重复执行等于「把当前版本重新同步过去」。
// 升级路径每次都会调它，不幂等就会在第二次升级时炸。
func TestInstallIsIdempotent(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".claude", ".codex", ".config/opencode", ".grok"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	if _, err := Install("v1", home); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("v2", home); err != nil {
		t.Fatalf("第二次安装失败: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".handoff", "skill", "SKILL.md"))
	if string(b) != "v2" {
		t.Fatalf("基准副本没被同步成新内容，实得 %q", b)
	}
}

// TestInstallLinksPointAtBase 锁住软链拓扑：四家都指向同一份基准副本，
// 改一次基准四家同时生效。指错了的症状是「升级后有的 agent 是新的有的是旧的」。
func TestInstallLinksPointAtBase(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".grok"), 0o755)
	if _, err := Install("x", home); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(home, ".grok", "skills", "handoff"))
	if err != nil {
		t.Fatal(err)
	}
	if got != BasePath(home) {
		t.Fatalf("软链指向 %q，期望 %q", got, BasePath(home))
	}
}

// TestInstallReplacesRealDirectory：目标可能是上一次装的软链，也可能是
// 手工放的实体目录（skills/install.sh 的 rm -rf 就是为了这个）。
func TestInstallReplacesRealDirectory(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".grok", "skills", "handoff"), 0o755)
	os.WriteFile(filepath.Join(home, ".grok", "skills", "handoff", "SKILL.md"), []byte("手工放的"), 0o644)
	if _, err := Install("x", home); err != nil {
		t.Fatalf("目标是实体目录时必须能覆盖: %v", err)
	}
}

// TestStatusDetectsStaleSite 是一致性检查的核心断言：改坏某一处落点后
// 能准确报出**是哪一处**旧了。
//
// why：因为安装是我们自己做的、落点已知，所以这个判断是准确的而不是猜的。
// 一条会说谎的诊断命令比没有更糟。
func TestStatusDetectsStaleSite(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.MkdirAll(filepath.Join(home, ".grok"), 0o755)
	Install("新内容", home)
	// 把 .grok 那处换成一个内容不同的实体文件
	p := filepath.Join(home, ".grok", "skills", "handoff")
	os.RemoveAll(p)
	os.MkdirAll(p, 0o755)
	os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("旧内容"), 0o644)

	sites, err := Status("新内容", home)
	if err != nil {
		t.Fatal(err)
	}
	var stale []string
	for _, s := range sites {
		if s.State == StateStale {
			stale = append(stale, s.Path)
		}
	}
	if len(stale) != 1 || !strings.Contains(stale[0], ".grok") {
		t.Fatalf("应准确报出 .grok 一处旧了，实得 %v", stale)
	}
}

// TestStatusNeverClaimsNotInstalled：落点不存在时只报 missing，
// 绝不断言「你没装」——我们只知道自己写过哪几处，不知道 agent 从别处读没读到。
func TestStatusNeverClaimsNotInstalled(t *testing.T) {
	sites, err := Status("x", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sites {
		if s.State != StateMissing {
			t.Fatalf("空 HOME 下每处都该是 missing，实得 %+v", s)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/skill/ -v`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

```go
// install.go —— 把 skill 内容装到本机各家 agent。
//
// 职责：
//   - 写基准副本到 <home>/.handoff/skill/SKILL.md
//   - 给**存在的** agent 目录建软链指向基准副本
//   - 返回每个落点的实际处置，供命令层如实打印
//
// 边界：
//   - 不改任何 agent 的配置文件（四家都按约定自动扫描 skills 目录）
//   - **agent 的 home 目录不存在就跳过，不代为创建**：给没装 codex 的机器
//     造一个 ~/.codex，下次那台机器真装了 codex 时会拿到我们凭空造的半截结构
//   - 不含 go:embed：内容与 home 都是入参，测试给临时目录与任意字符串即可，
//     不需要构建产物
//   - 不装到远端：skill 服务于审核者，审核者在本机（spec 非目标）
package skill

// 落点状态。
const (
	StateInstalled = "installed" // 本次已写入/已建链
	StateSkipped   = "skipped"   // agent 目录不存在，跳过（Note 说明理由）
	StateInSync    = "in_sync"   // 内容与当前二进制内嵌的一致
	StateStale     = "stale"     // 存在但内容不一致
	StateMissing   = "missing"   // 落点不存在
)

// Site 是一个落点及其状态。
//
// Note 只在需要解释时非空（跳过的理由、读取失败的原因）。
type Site struct {
	Path  string
	State string
	Note  string
}

// agentDirs 是各家 agent 的 skills 目录（相对 home）。
//
// 顺序即打印顺序。加新 agent 时只改这里——落点表随二进制一起更新，
// 这正是「我们自己装就知道装到了哪」的前提（spec D6）。
var agentDirs = []string{
	".claude/skills",
	".codex/skills",
	".config/opencode/skills",
	".grok/skills",
}

// skillDirName 是软链在各家 skills 目录下的名字。
const skillDirName = "handoff"

// fileName 是基准副本里的文件名。
const fileName = "SKILL.md"

// BasePath 返回基准副本目录。
//
// 为什么用副本而不是让四家都软链到仓库：仓库切分支/移动时四个 agent 会
// 一起失效。代价是改动后要重新同步，而这正由 upgrade 与一行安装自动完成。
func BasePath(home string) string { return filepath.Join(home, ".handoff", "skill") }

// Install 把 content 装到本机。
//
// 参数：
//   - content: SKILL.md 的全文（生产由 go:embed 注入）
//   - home: 用户 home 目录（测试注入临时目录）
//
// 返回：
//   - 每个落点的处置结果，顺序为 [基准副本, .claude, .codex, opencode, .grok]
//   - err: 只有基准副本写失败才返回错误——那是这个功能的地基；
//     单个 agent 落点失败记进 Site.Note 继续，不因为一家没装成就全盘失败
func Install(content, home string) ([]Site, error) {
	base := BasePath(home)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("创建基准副本目录 %s: %w", base, err)
	}
	target := filepath.Join(base, fileName)
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("写基准副本 %s: %w", target, err)
	}
	sites := []Site{{Path: target, State: StateInstalled}}

	for _, rel := range agentDirs {
		dir := filepath.Join(home, rel)
		parent := filepath.Dir(dir)
		if _, err := os.Stat(parent); err != nil {
			sites = append(sites, Site{
				Path: filepath.Join(dir, skillDirName), State: StateSkipped,
				Note: parent + " 不存在（该 agent 未安装）",
			})
			continue
		}
		link := filepath.Join(dir, skillDirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			sites = append(sites, Site{Path: link, State: StateSkipped, Note: "创建目录失败: " + err.Error()})
			continue
		}
		// 先删再建：目标可能是上一次装的软链，也可能是手工放的实体目录
		if err := os.RemoveAll(link); err != nil {
			sites = append(sites, Site{Path: link, State: StateSkipped, Note: "清理旧落点失败: " + err.Error()})
			continue
		}
		if err := os.Symlink(base, link); err != nil {
			sites = append(sites, Site{Path: link, State: StateSkipped, Note: "建软链失败: " + err.Error()})
			continue
		}
		sites = append(sites, Site{Path: link, State: StateInstalled})
	}
	return sites, nil
}
```

```go
// state.go —— 各落点相对于当前二进制内嵌内容的一致性。
//
// 职责：
//   - Status：逐个落点读出实际内容，与 content 比 sha256，报 in_sync / stale / missing
//
// 边界：
//   - **只报有，不报无**：落点不存在只说 missing，绝不断言「你没装 skill」。
//     agent 可能从我们表外的位置读到它，而一条会说谎的诊断命令比没有更糟
//   - 不修复：发现不一致只报告，同步是 handoff skill install
package skill

// Status 报告每个落点的一致性。
//
// 参数：
//   - content: 当前二进制内嵌的 SKILL.md 全文，作为比对基准
//   - home: 用户 home 目录
//
// 返回：
//   - 每个落点的状态，顺序与 Install 一致
//   - err: 恒为 nil（保留返回值是为了让调用方的签名不必因将来加 I/O 而变）；
//     单个落点读失败落到该 Site 的 Note 上，不让一处坏掉的落点吃掉整份报告
func Status(content, home string) ([]Site, error) {
	want := sha256.Sum256([]byte(content))
	check := func(p string) Site {
		b, err := os.ReadFile(p)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return Site{Path: p, State: StateMissing}
		case err != nil:
			return Site{Path: p, State: StateMissing, Note: "读取失败: " + err.Error()}
		}
		got := sha256.Sum256(b)
		if got == want {
			return Site{Path: p, State: StateInSync}
		}
		return Site{Path: p, State: StateStale}
	}

	sites := []Site{check(filepath.Join(BasePath(home), fileName))}
	for _, rel := range agentDirs {
		// 经软链读到的就是基准副本；落点是实体目录时读到的是它自己那份
		sites = append(sites, check(filepath.Join(home, rel, skillDirName, fileName)))
	}
	return sites, nil
}

// InSync 判断全部**存在的**落点是否都与 content 一致。
//
// missing 不算不一致：那家 agent 没装，本来就不该有落点。
func InSync(sites []Site) bool {
	for _, s := range sites {
		if s.State == StateStale {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/skill/ -v`
Expected: 全部 PASS

- [ ] **Step 5: 加关键节点日志**

`internal/skill` 是纯文件操作的库层，**不打日志**——它把每个落点的处置作为返回值交出去，由命令层（T6）打印。这是刻意的：库层再打一遍等于同一件事说两次，而 `handoff skill install` 的输出本来就是给人看的。
**但必须确认**：每个失败分支都把原因写进了 `Site.Note`（不是丢掉 err），否则「跳过了但不知道为什么」就是静默失败。逐条核对上面五个 `continue` 分支。

- [ ] **Step 6: 加意图注释**

已内联，核对：两个文件头（职责 + 边界，尤其 install.go 的「不代为创建」与 state.go 的「只报有不报无」）、`BasePath` 的「为什么用副本而不是软链到仓库」、`agentDirs` 的「顺序即打印顺序」、`Install` 返回值里「只有基准副本写失败才返回错误」、`Status` 的 err 恒 nil 说明、`InSync` 的「missing 不算不一致」。

- [ ] **Step 7: 提交**

```bash
git add internal/skill && git commit -m "feat(skill): skill 的安装与一致性检查

照搬 skills/install.sh 的落点表与「目录不存在就跳过」行为，基准副本改到
~/.handoff/skill，四家 agent 一律软链过去。Install/Status 都是纯入参函数
（不含 embed），测试给临时 HOME 即可。一致性检查因为落点是我们自己写的
所以是准确的，而不是靠扫描猜——只报有不报无。"
```

---

### Task 6: skill 内嵌与 `handoff skill` 命令

**Files:**
- Modify: `main.go`
- Create: `cmd/skill.go`
- Modify: `cmd/status.go`（加 skill 不一致提示行）
- Modify: `install.sh:137`（装完二进制后调 `skill install`）
- Modify: `install_test.sh`（成功路径的桩二进制要能吃下 `skill install`）
- Delete: `skills/install.sh`
- Test: `cmd/skill_test.go`

**Interfaces:**
- Consumes: T5 的 `skill.Install` / `skill.Status` / `skill.InSync` / `skill.Site` 与状态常量
- Produces:
  - `func cmd.SetSkillContent(s string)`
  - `func cmd.SkillContent() string`
  - 命令 `handoff skill`、`handoff skill install`

- [ ] **Step 1: 写失败测试**

```go
// TestSkillInstallReportsEverySite：命令层必须把每个落点的处置逐行打出来，
// 包括跳过的。只打成功的那几行，用户就无从知道 codex 那份为什么没更新。
func TestSkillInstallReportsEverySite(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	t.Setenv("HOME", home)
	SetSkillContent("测试内容")

	var buf bytes.Buffer
	c := newSkillCmdForTest(&buf) // 复用 cmd 包既有的测试构造方式
	c.SetArgs([]string{"install"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{".handoff/skill", ".claude", ".codex", "opencode", ".grok"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少落点 %s:\n%s", want, out)
		}
	}
}

// TestSkillReportsStale：改坏一处后 handoff skill 必须准确点名。
func TestSkillReportsStale(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".grok"), 0o755)
	t.Setenv("HOME", home)
	SetSkillContent("新内容")
	skill.Install("新内容", home)
	p := filepath.Join(home, ".grok", "skills", "handoff")
	os.RemoveAll(p)
	os.MkdirAll(p, 0o755)
	os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("旧"), 0o644)

	var buf bytes.Buffer
	c := newSkillCmdForTest(&buf)
	c.SetArgs(nil)
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), ".grok") || !strings.Contains(buf.String(), "旧") {
		t.Fatalf("应点名 .grok 那处旧了:\n%s", buf.String())
	}
}

// TestSkillContentNotEmptyInBinary 是一条防漏接线的断言：main.go 忘了调
// SetSkillContent 时，handoff skill install 会静静地装一份空文件——
// 症状是「装成功了但 skill 是空的」，肉眼极难发现。
func TestSkillContentEmptyIsRefused(t *testing.T) {
	SetSkillContent("")
	var buf bytes.Buffer
	c := newSkillCmdForTest(&buf)
	c.SetArgs([]string{"install"})
	if err := c.Execute(); err == nil {
		t.Fatal("内嵌内容为空必须拒绝安装，而不是装一份空文件")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestSkill -v`
Expected: FAIL，`SetSkillContent undefined`

- [ ] **Step 3: 实现**

`main.go`：

```go
package main

import (
	_ "embed"
	"os"

	"github.com/xushixin/handoff/cmd"
)

// skillContent 是给 AI 审核者的 skill 全文，编译期嵌进二进制。
//
// 为什么必须在 main 包做：go:embed 不能引用父目录，而 cmd 在子目录里。
//
// 为什么内嵌而不是走 npm/独立文件：skill 版本 == 二进制版本，**结构上不可能
// 漂移**——而漂移正是要解决的病根（旧 skill 会按已经变了的规则主动误导审核者）。
// 用一条会漂移的分发通道去修漂移，自相矛盾（B59 spec D5）。
//
//go:embed skills/handoff/SKILL.md
var skillContent string

func main() {
	cmd.SetSkillContent(skillContent)
	// ...（下面原样不动）
}
```

`cmd/skill.go` 新建：

```go
// 本文件实现 handoff skill：报告与同步内嵌 skill 的安装状态。
//
// 职责：
//   - handoff skill：逐落点报告是否与当前二进制一致
//   - handoff skill install：把内嵌内容装到本机各家 agent
//
// 边界：
//   - 不含安装逻辑本身（在 internal/skill）：本层只做参数、打印与退出码
//   - 不装到远端：skill 服务于审核者，审核者在本机
package cmd

// skillContent 是 main 包用 go:embed 注入的 SKILL.md 全文。
//
// 为什么用注入而不是在本包 embed：go:embed 不能引用父目录，而 skills/
// 在仓库根。为什么不在本包放一份拷贝：那份拷贝会和二进制一样漂移。
var skillContent string

// SetSkillContent 由 main 在启动时注入内嵌的 skill 全文。
func SetSkillContent(s string) { skillContent = s }

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "查看或安装给 AI 审核者的 handoff skill",
	Long: "不带参数报告各落点是否与当前二进制内嵌的 skill 一致。\n" +
		"skill install 把内嵌版本装到本机各家 agent（Claude Code / codex / opencode / grok）。\n" +
		"安装与升级会自动调用它，正常不需要手工跑。",
	RunE: func(cmd *cobra.Command, _ []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("取 home 目录: %w", err)
		}
		sites, _ := skill.Status(skillContent, home)
		out := cmd.OutOrStdout()
		for _, s := range sites {
			fmt.Fprintf(out, "%-8s %s%s\n", skillStateText(s.State), s.Path, noteSuffix(s))
		}
		if !skill.InSync(sites) {
			fmt.Fprintf(out, "处置     handoff skill install 重新同步\n")
		}
		return nil
	},
}

var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "把内嵌的 skill 装到本机各家 agent",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if skillContent == "" {
			// 不拦下就会静静装出一份空 SKILL.md，症状是「装成功了但 skill 是空的」
			return fmt.Errorf("内嵌 skill 为空：这个二进制的构建漏了 go:embed 注入，拒绝安装")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("取 home 目录: %w", err)
		}
		sites, err := skill.Install(skillContent, home)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		for _, s := range sites {
			fmt.Fprintf(out, "%-8s %s%s\n", skillStateText(s.State), s.Path, noteSuffix(s))
		}
		return nil
	},
}

// skillStateText 把落点状态渲染成一个定宽中文标签。
func skillStateText(state string) string {
	switch state {
	case skill.StateInstalled:
		return "已安装"
	case skill.StateSkipped:
		return "已跳过"
	case skill.StateInSync:
		return "一致"
	case skill.StateStale:
		return "旧"
	default:
		return "未安装"
	}
}

// noteSuffix 把理由缀在落点后面。理由为空时不留一个空括号。
func noteSuffix(s skill.Site) string {
	if s.Note == "" {
		return ""
	}
	return "（" + s.Note + "）"
}

func init() {
	skillCmd.AddCommand(skillInstallCmd)
	rootCmd.AddCommand(skillCmd)
}
```

`cmd/status.go` 的 `renderStatus`，在「更新」段落之后加：

```go
	// 只在**本机**查 skill：skill 服务于审核者，审核者在本机；对着远端
	// agentd 报本机的 skill 状态会让人以为那台机器上装了什么
	if targetName == "" && skillContent != "" {
		if home, err := os.UserHomeDir(); err == nil {
			if sites, _ := skill.Status(skillContent, home); !skill.InSync(sites) {
				fmt.Fprintf(w, "skill    有落点与当前二进制不一致，handoff skill install 重新同步\n")
			}
		}
	}
```

> `renderStatus` 目前不接 `targetName`，直接读包级变量即可（`cmd` 包内已有多处这么用）。

`install.sh` 在 `log "已安装 ${INSTALL_DIR}/handoff  ${tag}"` 之后插入：

```bash
  # 顺手把 skill 装给本机各家 agent。**必须调刚装好的那个文件**，不是别的
  # handoff——skill 内嵌在二进制里，调旧的就装旧的。
  #
  # 失败不算安装失败：二进制已经装好了，skill 少一份不影响 CLI 可用，
  # 而让整条安装因为一个附属动作退非零，用户会以为 handoff 没装上
  if "${INSTALL_DIR}/handoff" skill install >&2; then
    :
  else
    log "注意：skill 安装失败，可稍后手动跑 ${INSTALL_DIR}/handoff skill install"
  fi
```

`install_test.sh` 的桩二进制（第 86 行）已经是 `#!/bin/sh\nexit 0\n`，能吃下任何参数，**不需要改**；但要在该测试后面追加一条断言，确认 `main` 仍退 0（skill 安装失败不影响安装退出码）——把桩改成 `exit 3` 再跑一次即可。

删除 `skills/install.sh`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./... && go test ./cmd/ -run TestSkill -v && bash install_test.sh && echo "install_test OK"`
Expected: 全部 PASS

另外手工验一次开发路径（`go:embed` 在构建时读工作树里的 SKILL.md）：
```bash
go run . skill install && go run . skill
```
Expected: 逐落点打印，且全部「一致」。

- [ ] **Step 5: 加关键节点日志**

`cmd/skill.go` 的输出**就是**产品输出（逐落点一行），不另打日志——这里加 slog 只会把同一件事说两遍。但确认两点：
- `skillContent == ""` 的拒绝分支带出了原因（构建漏了 embed），不是一句「失败」。
- `skill.Status` 的 err 被显式丢弃处写了 `_`，且上面的注释说明了为什么可以丢（它恒为 nil，单点失败已落到 `Site.Note`）。

`install.sh` 的 skill 安装失败分支必须 `log` 一行——静默失败会让用户拿到一份旧 skill 而毫不知情。

- [ ] **Step 6: 加意图注释**

已内联，核对：`main.go` 的 `//go:embed` 上方三段（为什么在 main 包、为什么内嵌不走 npm）、`cmd/skill.go` 文件头与 `skillContent` 的注释、空内容拒绝分支的 why、`cmd/status.go` 里「只在本机查 skill」的 why、`install.sh` 里「必须调刚装好的那个文件」与「失败不算安装失败」。

- [ ] **Step 7: 提交**

```bash
git add main.go cmd/skill.go cmd/skill_test.go cmd/status.go install.sh install_test.sh && git rm skills/install.sh && git commit -m "feat(skill): skill 用 go:embed 进二进制，随安装/升级同步

skill 版本 == 二进制版本，结构上不可能漂移——而漂移正是病根：旧 skill 会
按已经变了的规则主动误导审核者。新增 handoff skill / skill install，
一行安装脚本装完二进制后自动调一次。skills/install.sh 由 go run . skill
install 取代，开发与生产共用一套逻辑。"
```

---

### Task 7: `handoff upgrade` 的机器范围维度

这是整个计划的收口：巡检表 + 多机编排。

**Files:**
- Modify: `cmd/root.go`（新增 `Endpoints`）
- Modify: `cmd/upgrade.go`（整体重写命令体，保留 `--rollback` 与 `currentBinary`）
- Test: `cmd/upgrade_test.go`

**Interfaces:**
- Consumes: T1 的 `FetchArchive`、T2 的 `BuildInfo.Platform` / `UpdateStatus.Managed`、T4 的 `PushUpdate` / `RestartAgentd` / `WaitVersion` / `UpdateRejected` / `ErrUpdateUnsupported`、T5–T6 的 `skill.Install`、既有 `release.Activate` / `release.Rollback` / `client.ErrStatusUnsupported`
- Produces:
  - `type Endpoint struct { Name, Addr, Token string; Local bool }`
  - `func Endpoints(only string) ([]Endpoint, error)` — `only == ""` 返回 [本机, 各 target 按名字排序]；否则只返回那一台（本机用名字 `""` 或 `"本机"` 都不接受，本机由 `only == ""` 之外的路径取不到——`--target` 非空即远端）
  - `cmd/upgrade.go` 内部：`type machineState struct { Ep Endpoint; Bin, Agentd, Platform string; Managed bool; Busy int; Err error }`

**巡检输出（照 spec §4.1，逐字对齐）：**

```
最新     v0.1.1
本机     二进制 v0.1.0 · agentd v0.1.0   需要升级
devbox   v0.1.0                          需要升级
prod     v0.1.1                          已是最新
aliyun   够不着（dial tcp 10.0.0.5:7777: connect: connection refused）
```

**失败报告（照 spec §4.6，逐字对齐）：**

```
devbox   跳过   3 个活跃任务
         handoff upgrade --now --target devbox --force
prod     跳过   agentd 非托管启动，重启后不会被拉起
         先在该机器上 handoff service install
aliyun   失败   dial tcp 10.0.0.5:7777: connect: connection refused
```

- [ ] **Step 1: 写失败测试**

```go
// fakeMachine 是一台被完全替身化的机器：既不联网也不动任何真实文件。
type fakeMachine struct {
	platform string
	version  string
	managed  bool
	busy     int
	statusErr error
	pushErr  error
	pushed   bool
}

// TestUpgradeCheckRendersEveryMachine：巡检必须一台不落，够不着的也要有一行。
//
// why：漏掉一台够不着的机器，操作者会以为它已经是最新的——而它恰恰是最
// 需要被看见的那台。
func TestUpgradeCheckRendersEveryMachine(t *testing.T) {
	out := runUpgradeCheck(t, map[string]fakeMachine{
		"devbox": {platform: "linux/amd64", version: "v0.1.0", managed: true},
		"prod":   {platform: "linux/amd64", version: "v0.1.1", managed: true},
		"aliyun": {statusErr: errors.New("dial tcp 10.0.0.5:7777: connect: connection refused")},
	})
	for _, want := range []string{
		"最新     v0.1.1",
		"本机",
		"devbox", "需要升级",
		"prod", "已是最新",
		"aliyun", "够不着", "connection refused",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("巡检输出缺 %q:\n%s", want, out)
		}
	}
	// 本机那一行必须分别显示二进制与 agentd 两个版本：换掉磁盘上的文件后
	// 正在跑的 agentd 仍是旧进程，这是正常且常见的中间态。合成一个数字就
	// 必然骗人——显示旧版让人以为没升成，显示新版让人以为已在跑新代码
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "本机") {
			if !strings.Contains(line, "二进制") || !strings.Contains(line, "agentd") {
				t.Fatalf("本机行必须分别显示二进制与 agentd 版本，实得 %q", line)
			}
		}
	}
}

// TestUpgradeSkipsBusyAndOffersForceLine：闸一被拦时必须给出**可直接复制**的
// --force 命令行，且行里带正确的 target 名字。
func TestUpgradeSkipsBusyAndOffersForceLine(t *testing.T) {
	out := runUpgradeNow(t, map[string]fakeMachine{
		"devbox": {platform: "linux/amd64", version: "v0.1.0", managed: true, busy: 3},
	})
	if !strings.Contains(out, "handoff upgrade --now --target devbox --force") {
		t.Fatalf("缺可复制的 --force 行:\n%s", out)
	}
}

// TestUpgradeUnmanagedNeverOffersForce 是「处置必须对症」最要紧的一条：
// --force 不越过闸二，给出这条命令等于让用户跑一条注定失败的命令。
func TestUpgradeUnmanagedNeverOffersForce(t *testing.T) {
	out := runUpgradeNow(t, map[string]fakeMachine{
		"prod": {platform: "linux/amd64", version: "v0.1.0", managed: false},
	})
	if strings.Contains(out, "--force") {
		t.Fatalf("非托管不该给 --force：它不越过闸二\n%s", out)
	}
	if !strings.Contains(out, "handoff service install") {
		t.Fatalf("非托管应提示先装服务:\n%s", out)
	}
}

// TestUpgradeUnreachableInventsNoRemedy：够不着时只报原始错误原文。
// 编一条处置建议就是在猜，而猜出来的建议会把人引到错误的方向。
func TestUpgradeUnreachableInventsNoRemedy(t *testing.T) {
	out := runUpgradeNow(t, map[string]fakeMachine{
		"aliyun": {statusErr: errors.New("dial tcp 10.0.0.5:7777: connect: connection refused")},
	})
	if !strings.Contains(out, "connection refused") {
		t.Fatalf("必须原样带出错误原文:\n%s", out)
	}
	if strings.Contains(out, "handoff ") {
		t.Fatalf("够不着时不该编任何处置命令:\n%s", out)
	}
}

// TestUpgradePartialFailureContinues：一台失败不阻断其余，且退出码非零。
//
// why：这些机器之间本来就没有事务关系，让一台连不上的机器阻止其余全部
// 升级是纯损失。
func TestUpgradePartialFailureContinues(t *testing.T) {
	machines := map[string]fakeMachine{
		"aliyun": {statusErr: errors.New("connection refused")},
		"devbox": {platform: "linux/amd64", version: "v0.1.0", managed: true},
	}
	out, err := runUpgradeNowErr(t, machines)
	if err == nil {
		t.Fatal("有机器失败时退出码必须非零")
	}
	if !machines["devbox"].pushed && !strings.Contains(out, "devbox") {
		t.Fatalf("其余机器必须照常升级:\n%s", out)
	}
}

// TestUpgradeRefusesUnknownPlatform：对端没上报平台（老 agentd）时必须
// 明确拒绝，而不是猜一个默认值给一台 linux 机器推 darwin 二进制。
func TestUpgradeRefusesUnknownPlatform(t *testing.T) {
	out := runUpgradeNow(t, map[string]fakeMachine{
		"old": {platform: "", version: "v0.1.0", managed: true},
	})
	if !strings.Contains(out, "未上报平台") {
		t.Fatalf("应明说对端过旧未上报平台:\n%s", out)
	}
}

// TestUpgradeLocalGoesLast：本机换版会重启本机 agentd，而操作者很可能正用
// 它盯着任务。把干扰最大的一步放最后，前面出问题时不至于白扰一次。
//
// 断言的是**动作序列**而不是输出顺序：输出可以排版成任何样子，真正要锁住的
// 是「本机的重启发生在所有 target 都处理完之后」。
func TestUpgradeLocalGoesLast(t *testing.T) {
	var order []string
	recordOrder = func(name string) { order = append(order, name) }
	t.Cleanup(func() { recordOrder = func(string) {} })

	runUpgradeNow(t, map[string]fakeMachine{
		"aaa":    {platform: "linux/amd64", version: "v0.1.0", managed: true},
		"zzz":    {platform: "linux/amd64", version: "v0.1.0", managed: true},
		"__本机": {platform: "darwin/arm64", version: "v0.1.0", managed: true},
	})
	if len(order) == 0 {
		t.Fatal("没有记录到任何机器被处理")
	}
	if order[len(order)-1] != "本机" {
		t.Fatalf("本机必须排最后，实得顺序 %v", order)
	}
}

// TestUpgradeReportsUnconfirmedRestart：轮询超时必须报「已换版但新进程未上线」
// 并附回滚命令，**绝不含糊成「升级完成」**。
func TestUpgradeReportsUnconfirmedRestart(t *testing.T) {
	out := runUpgradeNow(t, map[string]fakeMachine{
		"devbox": {platform: "linux/amd64", version: "v0.1.0", managed: true /* 版本永不变 */},
	})
	if strings.Contains(out, "升级完成") {
		t.Fatalf("未确认上线不许报完成:\n%s", out)
	}
	if !strings.Contains(out, "--rollback") {
		t.Fatalf("未上线时必须给回滚出路:\n%s", out)
	}
}
```

> 上面用到的三个 helper 由实现者在 `cmd/upgrade_test.go` 里补齐：
> - `runUpgradeCheck(t, machines) string` — 不带 `--now`，返回 stdout
> - `runUpgradeNow(t, machines) string` — 带 `--now`，返回 stdout（吞掉错误）
> - `runUpgradeNowErr(t, machines) (string, error)` — 带 `--now`，同时返回错误（测退出码）
>
> 三者共用一套搭建：构造临时 `config.yaml`（含 `targets`，键 `__本机` 表示那台机器的数据用于本机端点）、把 Step 3 列出的六个测试缝换成读 `map[string]fakeMachine` 的替身、执行 `upgradeCmd`。替身 `Latest` 恒返回 `v0.1.1`。
> **这些 helper 不许联网、不许写 `~/`、不许触碰真实二进制**——`activateBinary` 与 `newReleaseFetcher` 都必须是替身。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestUpgrade -v`
Expected: FAIL

- [ ] **Step 3: 实现**

`cmd/root.go` 新增：

```go
// Endpoint 是一台可被升级的机器。
//
// Local 为 true 时 Name 恒为「本机」：它的二进制由 CLI 直接换（文件就在本地），
// 与远端走的是两条不同的路径（spec §4.2）。
type Endpoint struct {
	Name  string
	Addr  string
	Token string
	Local bool
}

// Endpoints 返回要处理的机器清单。
//
// 参数：
//   - only: 为空时返回 [本机, 全部 target（按名字排序）]；非空时只返回该 target
//
// 返回：
//   - 机器清单；only 指定的 target 不存在时返回错误
//
// 为什么本机也在清单里：版本一致本身就是要解决的问题，把本机排除在外，
// 「本机新远端旧」就会成为常态。而操作者不必记住配置里有哪些 target——
// 这正是「一条命令看清所有机器」的前提（spec D2）。
func Endpoints(only string) ([]Endpoint, error) {
	p := configPath
	if p == "" {
		p = config.DefaultPath()
	}
	cfg, err := config.Load(p)
	if err != nil {
		return nil, fmt.Errorf("加载配置 %s: %w", p, err)
	}
	if only != "" {
		t, ok := cfg.Targets[only]
		if !ok {
			return nil, fmt.Errorf("target %q 未在配置 %s 中定义", only, p)
		}
		return []Endpoint{{Name: only, Addr: "http://" + t.Addr, Token: t.Token}}, nil
	}
	local := cfg.Listen
	if !strings.Contains(local, "://") {
		local = "http://" + local
	}
	eps := []Endpoint{{Name: "本机", Addr: local, Token: cfg.Token, Local: true}}
	names := make([]string, 0, len(cfg.Targets))
	for n := range cfg.Targets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		eps = append(eps, Endpoint{Name: n, Addr: "http://" + cfg.Targets[n].Addr, Token: cfg.Targets[n].Token})
	}
	return eps, nil
}
```

`cmd/upgrade.go` 的测试缝扩到六个（前四个保留原样）：

```go
var (
	newReleaseChecker = func() releaseChecker { return release.NewClient() }
	newReleaseFetcher = func() releaseFetcher { return release.NewInstaller(slog.Default()) }
	activateBinary    = release.Activate
	rollbackBinary    = release.Rollback
	// newAgentdClient 是「怎么跟一台 agentd 说话」这一层的缝：测试替换它
	// 就能整套替身化远端，而不必起真实 HTTP 服务
	newAgentdClient = func(ep Endpoint) agentdPeer { return client.New(ep.Addr, ep.Token) }
	// listEndpoints 是机器清单的缝：测试注入临时配置里的机器
	listEndpoints = Endpoints
	// recordOrder 在每台机器开始处理时被调用一次，生产是空实现。
	//
	// 存在的唯一理由：「本机排最后」是一条**顺序**约束，它无法从输出文本可靠
	// 断言（排版随时会改），只能观察动作序列。为一条真实约束留一个空钩子，
	// 好过让这条约束没有测试
	recordOrder = func(string) {}
)

// agentdPeer 是 upgrade 需要的 agentd 能力子集。
//
// 声明成接口而不是直接用 *client.Client：这条命令会真的推二进制并重启对端，
// 测试必须能整体替换掉，漏替一个就会在 CI 上真的去动一台机器。
type agentdPeer interface {
	Status(ctx context.Context) (*proto.StatusResp, error)
	PushUpdate(ctx context.Context, tag, sum string, tgz []byte, force bool) (*proto.UpdateResp, error)
	RestartAgentd(ctx context.Context, force bool) (*proto.UpdateResp, error)
	WaitVersion(ctx context.Context, want string, timeout, interval time.Duration) error
}

const (
	// upgradeWaitTimeout / upgradeWaitInterval 是换版后等新进程上线的时限与轮询间隔。
	//
	// 60s 的依据：systemd Restart=always 的默认 RestartSec 是 100ms，
	// launchd KeepAlive 是立即拉起；60s 给足了慢机器加载与 SQLite 打开的余量，
	// 又不至于让一台真的起不来的机器把操作者晾太久
	upgradeWaitTimeout  = 60 * time.Second
	upgradeWaitInterval = 2 * time.Second
)
```

命令体的骨架（实现者按此展开，每个分支的文案照上面的输出样例逐字对齐）：

```go
// 1. --rollback：原样保留，不接 --target（回滚是单机应急动作，
//    批量回滚一组机器不是任何真实场景，给它批量入口只会让误操作更省事）
// 2. 取机器清单 listEndpoints(targetName)
// 3. 取最新 release：newReleaseChecker().Latest(ctx)
// 4. 逐台 probe：Status → 填 machineState{Bin, Agentd, Platform, Managed, Busy, Err}
//    - 本机的 Bin 取 buildinfo.Read().Version（CLI 自己），Agentd 取 status
//    - 本机 status 够不着 = agentd 未运行，不是失败：只比二进制版本
//    - client.ErrStatusUnsupported = 对端过旧，Platform 视为空
// 5. !upgradeNow：渲染巡检表并返回（--check 是默认行为）
// 6. upgradeNow：排序为 [远端…, 本机]，逐台执行，收集结果
//    - 预检闸二（!Managed）→ 跳过，处置行 = service install，不给 --force
//    - 预检闸一（Busy>0 && !force）→ 跳过，处置行 = 带 target 名的 --force 命令
//    - Platform 为空 → 跳过，说明「对端 agentd 过旧，未上报平台，需先手工升级一次」
//    - 已是最新 → 跳过，不算失败
//    - 远端：FetchArchive(该平台) → PushUpdate → WaitVersion
//    - 本机：activateBinary（现有逻辑）→ exec 新二进制 skill install → RestartAgentd
//      · 本机 agentd 不在或非托管时，退回纯换文件并如实提示需自己重启
// 7. 逐行报告；任一台失败则返回错误（退出码非零）
```

本机路径里换版后同步 skill 的那一步**必须调新二进制**：

```go
	// 当前进程是旧二进制，它内嵌的是**旧 skill**；新 skill 在刚换上去的那个
	// 文件里。所以是 exec 新二进制，不是直接调用本进程的 skill.Install
	if out, err := exec.CommandContext(ctx, target, "skill", "install").CombinedOutput(); err != nil {
		// skill 没同步不算升级失败：二进制已经换好了。但必须说出来——
		// 悄悄留一份旧 skill，它会按已经变了的规则主动误导审核者
		fmt.Fprintf(w, "         注意 skill 同步失败，请手动跑 %s skill install：%s\n",
			target, firstLineOf(string(out)))
	}
```

`firstLineOf` 在 `cmd` 包里补一个（`internal/release` 有个同款 `firstLine`，但它不导出，不跨包借用）：

```go
// firstLineOf 取多行输出的首行，用于把子进程的失败原因压成一行。
//
// 为什么只取首行：这行文案是缀在升级报告里的，把几十行 stderr 原样铺进去
// 会把真正的结论淹掉；完整输出用户可以自己重跑那条命令拿到。
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -v 2>&1 | tail -30 && go build ./...`
Expected: 全部 PASS

再做一次不联网的人工核对（应打出巡检表，且 `--target` 不存在时报配置错）：
```bash
go run . upgrade --help && go run . upgrade --target 不存在的名字
```

- [ ] **Step 5: 加关键节点日志**

`cmd/upgrade.go` 的逐行报告是产品输出。**日志另加**，因为多机编排的失败最常发生在没人看着的时候：
- 每台机器开始处理时 `slog.Default().Info("开始处理机器", "name", ep.Name, "addr", ep.Addr, "local", ep.Local)`
- probe 失败 `Warn`，带 `name` 与 `cause`
- 每道闸的跳过各一条 `Info`，带 `name` / `reason` / `busy`
- 下载前后各一条 `Info`，带 `name` / `platform` / `tag` / `bytes`
- 推送成功、轮询确认成功各一条 `Info`
- 轮询超时一条 `Error`，带 `name` / `prev`（回滚要用的路径）
- 全部处理完一条 `Info` 汇总：`"升级完成", "总数", n, "成功", ok, "跳过", skip, "失败", fail`

`internal/client` 侧的日志已在 T4 就位，这里不重复。

- [ ] **Step 6: 加意图注释**

- `Endpoint` / `Endpoints` doc：为什么本机也在清单里。
- `agentdPeer` doc：为什么抽接口（测试必须能整体替身化，漏替一个会真的动机器）。
- `upgradeWaitTimeout` / `upgradeWaitInterval`：60s / 2s 的依据。
- `--rollback` 分支：为什么不接 `--target`。
- 本机排最后的那行代码上方：为什么（会重启操作者正用着的 agentd）。
- 三条处置建议分支各一句 why，尤其**非托管不给 `--force`**（它不越过闸二，给了就是让用户跑一条注定失败的命令）与**够不着不编处置**。
- exec 新二进制装 skill 那段的 why（当前进程内嵌的是旧 skill）。
- 文件头注释改写：把「不重启 agentd」那条边界更新成新行为（现在会通过接口触发重启），并补上机器范围维度。

- [ ] **Step 7: 提交**

```bash
git add cmd/root.go cmd/upgrade.go cmd/upgrade_test.go && git commit -m "feat(upgrade): 加机器范围维度，一条命令巡检并升级所有机器

本机与全部 target 同一入口（操作者不必记 target 名字）；二进制由本机按各机
平台下载后推送，执行机无需出网。两道闸预检在下载之前，--force 只越过活跃
任务闸、永不越过非托管闸。部分失败不中断其余，逐行报告且处置建议对症——
非托管不给 --force，够不着只报原文不编处置。本机排最后：它会重启操作者
正用着的 agentd。"
```

---

### Task 8: 删除自动更新，配置字段标废弃

**Files:**
- Delete: `internal/selfupdate/updater.go`、`internal/selfupdate/updater_test.go`
- Modify: `internal/selfupdate/pending.go`（删 `Pending` / `LoadPending` / `SavePending` / `ClearPending` / `PendingPath`，整个文件很可能可以删掉）与对应测试
- Modify: `cmd/agentd.go:180-206`（删 updater 接线）
- Modify: `internal/config/config.go`（`UpdateConfig` 标废弃 + 非默认值 Warn）
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: T2 已把 `IsManaged` 搬到 `managed.go`，T7 已让 upgrade 承担全部换版职责
- Produces: 无新接口；`config.UpdateConfig` 字段**保留**，新增 `func (c *Config) WarnDeprecated(log *slog.Logger)`（或在 `Load` 的调用方打）

- [ ] **Step 1: 写失败测试**

```go
// TestLoadAcceptsDeprecatedUpdateKeys 是这次删除里唯一不能出错的一条。
//
// why：配置是 KnownFields(true) 严格解析的——未知键让 agentd **启动失败**。
// v0.1.0 首次运行会把 update.auto / update.interval 写进 config.yaml，
// 直接删字段等于让所有装过 v0.1.0 的机器升级后起不来，正是这个设计要
// 消灭的那类失配的最狠形态。
func TestLoadAcceptsDeprecatedUpdateKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("token: tk\nupdate:\n  auto: false\n  interval: 12h\n"), 0o600)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("含 update 键的旧配置必须能正常加载: %v", err)
	}
	if cfg.Update.Auto {
		t.Fatal("字段值仍应被解出来（只是不再有效果）")
	}
}

// TestWarnDeprecatedFiresOnNonDefault：取值非默认时必须 Warn。
// 用户把 auto 设成 false 是有意图的，悄悄让它失效等于骗人。
func TestWarnDeprecatedFiresOnNonDefault(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	(&Config{Update: UpdateConfig{Auto: false, Interval: 6 * time.Hour}}).WarnDeprecated(log)
	if !strings.Contains(buf.String(), "update.auto") {
		t.Fatalf("非默认值必须 Warn:\n%s", buf.String())
	}
}

// TestWarnDeprecatedSilentOnDefault：默认值不打——绝大多数机器都是默认值，
// 每次启动打一条无从处置的 Warn，只会让人学会忽略日志。
func TestWarnDeprecatedSilentOnDefault(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	(&Config{Update: UpdateConfig{Auto: true, Interval: 6 * time.Hour}}).WarnDeprecated(log)
	if buf.Len() != 0 {
		t.Fatalf("默认值不该打 Warn:\n%s", buf.String())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run 'Deprecated' -v`
Expected: FAIL，`WarnDeprecated undefined`

- [ ] **Step 3: 实现**

`internal/config/config.go` 的 `UpdateConfig` 注释整体替换：

```go
// UpdateConfig 是**已废弃**的自动更新配置。
//
// B59 取消了 agentd 的定时自更新循环：升级改由操作者一条 handoff upgrade
// 触发，二进制由本机下载后推送给远端。这两个字段因此不再有任何效果。
//
// **为什么保留字段而不是删掉**：配置是 KnownFields(true) 严格解析的，未知键
// 让 agentd **启动失败**。v0.1.0 的首次运行会把这两个键写进 config.yaml，
// 直接删字段等于让所有装过 v0.1.0 的机器升级后起不来——正是这个设计要消灭
// 的那类失配的最狠形态（B59 spec D7）。
//
// 取值非默认时由 WarnDeprecated 打一条 Warn：用户把 auto 设成 false 是有
// 意图的，悄悄让它失效等于骗人。
type UpdateConfig struct {
	Auto     bool
	Interval time.Duration
}

// WarnDeprecated 对已废弃且被显式改过的配置打一条 Warn。
//
// 参数：
//   - log: 日志器（agentd 启动时传自己的）
//
// 注意：
//   - 默认值不打。绝大多数机器都是默认值，每次启动打一条无从处置的 Warn，
//     只会让人学会忽略日志——而那是比不打更糟的结果
func (c *Config) WarnDeprecated(log *slog.Logger) {
	if !c.Update.Auto {
		log.Warn("配置 update.auto 已废弃且不再有效果：agentd 不再自动更新，升级请在审核者机器上跑 handoff upgrade --now")
	}
	if c.Update.Interval != 6*time.Hour {
		log.Warn("配置 update.interval 已废弃且不再有效果：agentd 不再定时检查版本",
			"配置值", c.Update.Interval)
	}
}
```

删除文件：`internal/selfupdate/updater.go`、`internal/selfupdate/updater_test.go`。
从 `internal/selfupdate/pending.go` 删除 `Pending` / `PendingPath` / `LoadPending` / `SavePending` / `ClearPending` 及其测试；若该文件已空则整个删掉。

`cmd/agentd.go` 删掉 `up := selfupdate.New(...)` 到 `logger.Info("自动更新已关闭…")` 整段（约 `:180-206`），保留 `sd := agentd.NewShutdown(logger)` 与 T3 加的 `srv.SetRestart(sd.Trigger)`，并在 `logger.Info("agentd 服务启动", …)` 之前加 `cfg.WarnDeprecated(logger)`。同时删除 `activeTaskCount`（已由 `Server.activeCount` 取代）与不再被引用的 import。

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./... && go vet ./... && go test ./... 2>&1 | tail -30`
Expected: 全部 PASS，且 `go vet` 不报未使用的 import/变量

再确认删除量对得上 spec 的估算：
```bash
git diff --stat HEAD~1
```

- [ ] **Step 5: 加关键节点日志**

- `WarnDeprecated` 本身就是日志，确认两条 Warn 都说清了「已废弃 + 不再有效果 + 替代做法」——只说「已废弃」而不给替代，用户只会困惑。
- `cmd/agentd.go` 删掉 updater 后，确认启动日志里不再有任何提及自动更新的文案（否则日志会继续宣称一个已经不存在的功能）。

- [ ] **Step 6: 加意图注释**

- `UpdateConfig` 的废弃说明（含**为什么保留字段**）已内联，核对落地。
- `internal/selfupdate` 包的文件头注释（若在 `pending.go` 里）随文件删除一并迁移到 `managed.go`，确认包注释不再描述已删除的定时循环。
- `cmd/agentd.go` 里 `sd` 那段原有的注释提到「这是整条自更新链的交接点」，改写成「换版接口靠它退出进程交接给新二进制」。

- [ ] **Step 7: 提交**

```bash
git add -A && git commit -m "refactor: 删除 agentd 自动更新循环，配置字段标废弃

定时循环与待命状态由 B59 的操作者触发换版取代。update.auto /
update.interval **保留字段**只标废弃：配置是 KnownFields(true) 严格解析的，
删字段会让所有装过 v0.1.0 的机器升级后起不来。取值非默认时打 Warn——
用户把 auto 设成 false 是有意图的，悄悄让它失效等于骗人。"
```

---

### Task 9: 变异检查、文档与全量自检

**Files:**
- Modify: `README.md`（skill 安装说明、升级章节）
- Test: 全仓库

**Interfaces:**
- Consumes: T1–T8 全部
- Produces: 无代码接口；产出是一份可信的测试套件与更新后的文档

- [ ] **Step 1: 变异检查——闸二**

把 `internal/selfupdate/managed.go` 的 `IsManaged` 临时改成 `return true`，跑：

Run: `go test ./internal/agentd/ -run TestUpdateRejectsUnmanaged -v`
Expected: **FAIL**。若仍 PASS，说明那条测试是恒真的，必须修好测试再继续。改回实现。

- [ ] **Step 2: 变异检查——自检**

把 `internal/release/install.go` 的 `selfCheck` 里 `if got != wantTag` 临时改成 `if false`，跑：

Run: `go test ./internal/release/ -run TestInstallArchiveSelfChecks -v`
Expected: **FAIL**。改回实现。

- [ ] **Step 3: 变异检查——skill 跳过逻辑**

把 `internal/skill/install.go` 里「parent 不存在则跳过」改成无条件 `os.MkdirAll(dir, 0o755)`，跑：

Run: `go test ./internal/skill/ -run TestInstallSkipsMissingAgentDirs -v`
Expected: **FAIL**。改回实现。

- [ ] **Step 4: 变异检查——处置建议对症**

把 `cmd/upgrade.go` 里非托管分支的处置行临时改成带 `--force` 的那条，跑：

Run: `go test ./cmd/ -run TestUpgradeUnmanagedNeverOffersForce -v`
Expected: **FAIL**。改回实现。

- [ ] **Step 5: 更新 README**

- 第 337 行附近的 skill 安装说明：`bash skills/install.sh` → `handoff skill install`（并说明一行安装与 `handoff upgrade --now` 会自动调用，开发时用 `go run . skill install`）。
- 升级章节：把「agentd 定时自动更新」改写为「操作者触发」，给出四条命令形态（巡检 / 全量升级 / 单机 / `--force`），并写明**执行机无需出网**与**两道闸**（含 `--force` 不越过非托管这一条）。
- 若 README 有配置字段表，给 `update.auto` / `update.interval` 标注「已废弃，无效果」。

- [ ] **Step 6: 全量自检**

按 CLAUDE.md §5 与 instrumenting-code 的清单逐项过一遍：

```bash
go build ./... && go vet ./... && go test ./... && bash install_test.sh
```

逐项确认（任一未过就回去修）：
- [ ] 每个错误分支都带上下文 + cause，没有裸 `err`
- [ ] 每个外部调用（HTTP 推送、exec 自检、文件 rename）前后都有日志
- [ ] 成功路径打了结论日志（换版成功、新版本上线、逐机汇总）
- [ ] 全仓库没有把 `fmt.Printf` 当日志用（`grep -rn 'fmt.Print' --include='*.go' internal/`，命中的必须都是 cmd 层的产品输出）
- [ ] 新建文件都有文件头注释（职责 + 边界）：`internal/proto/update.go`、`internal/agentd/update.go`、`internal/client/update.go`、`internal/skill/install.go`、`internal/skill/state.go`、`internal/selfupdate/managed.go`、`cmd/skill.go`
- [ ] 导出方法都有 doc 注释（参数 / 返回 / 注意）
- [ ] 无跨层调用、跨模块走既有边界
- [ ] 无硬编码：60s / 2s / 100MiB 都提成了带注释的常量

- [ ] **Step 7: 提交**

```bash
git add README.md && git commit -m "docs: README 同步 B59——升级改为操作者触发，skill 随二进制分发"
```

---

## 真机验收（V1–V4）

**实现完成后不算完工**：这四条顶替 B54.3 卡住的 P3/P4，是这个子系统最核心行为的唯一实证。在 devbox（`100.73.238.21`，用户 `sycm`，免密）执行。

**前置**：必须先发布 v0.1.1，且 v0.1.0 → v0.1.1 这一跳**要手工做一次**——v0.1.0 的 agentd 没有 `/api/update`，也不上报 `Platform`（spec §8）。手工做法：在 devbox 上跑 `handoff upgrade --now` 再重启该机器的 agentd。

**红线（每条都不许越）**：不 `git push`；不动 `main` 或其他分支；不改 `docs/superpowers/backlog.md`、`plans/`、`specs/`；不动任何机器的 `~/.handoff/`；杀进程只按精确 pid 或精确全路径匹配，**绝不 `pkill -f agentd`**；验证实例必须在端口 / DataDir / 二进制 / HOME 四个维度上与生产实例隔离。

- [ ] **V1 干净升级**：devbox 无活跃任务时跑 `handoff upgrade --now --target devbox`。验证：换版成功、agentd 重启、CLI 轮询确认新版本上线、随后 `handoff status --target devbox` 版本一致。
- [ ] **V2 闸一生效**：devbox 有活跃任务时跑同样命令，验证被拒且输出里含**可直接复制**的 `handoff upgrade --now --target devbox --force` 行。
- [ ] **V3 强制升级不伤执行者**（接 V2，带 `--force`）：验证 ①换版成功 ②**执行者进程存活**（这是 setsid + systemd `KillMode=process` 的实证）③该任务随后能 `continue` 并正常产出。
- [ ] **V4 skill 同步**：升级后 `handoff skill` 报告全部一致；手工改坏一处落点后能被**准确点名**。

V3 是这四条里最有价值的一条：`--force` 路径的执行者存活性此前从未在真实换版下走过。在 V3 通过之前，`--force` 相关文案应保持「这条路径尚未在真实换版下验证过」的措辞。

---

## 自审记录

**Spec 覆盖：** §4.1 命令形态→T7；§4.2 数据流→T1/T4/T7，`Fetch` 拆分订正→T1；§4.3 两道闸→T3（agentd 复检）+T7（CLI 预检）；§4.4 平台字段→T2；§4.5 信任链三道校验→T1（下载后）+T3（收到后、换版前）；§4.6 错误处理与处置建议→T7；§4.7 skill 分发→T5/T6；§4.8 清理清单→T2（`IsManaged` 归位）+T8（删除）；§5 文件结构→本文件的文件结构表逐行对应；§6 测试策略→各 task 的 Step 1 + T9 的变异检查 + 真机验收章节；§7 人工前置→真机验收章节的前置；§8 已知风险→真机验收章节的前置与 V3 措辞要求。

**已知的实现者判断点（不是 TBD，是明确交给实现者按现场决定的）：**
- T3/T7 的测试 helper（`newTestStore` / `seedTask` / `runUpgradeNow`）沿用各包已有的构造方式。计划给出了它们必须满足的约束（不联网、不写 `~/`、不碰真实二进制），具体形态随包内现状。
- T8 里 `internal/selfupdate/pending.go` 删空后是否整个删除，取决于该文件删除 `Pending` 相关之后还剩什么。
