# 更新面（B166）一期实现计划：形态与下载

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把「有新版」从 macOS 菜单栏挪进控制台——右下角提示框 + 设置「更新」页，主按钮由 agentd 下载安装包、校验 sha256、下完自动打开；托盘瘦身到两项并换成标志。

**Architecture:** 控制台在薄壳里是 agentd 伺服的**外链页面**，没有 Wails 运行时，所以控制台够不着薄壳、薄壳也推不动控制台。因此**动作全部放在 agentd 一侧**（它已经会下载校验发布资产），薄壳只**单向上报**自身状态（版本 + 同步结论），agentd 内存持有并带 TTL。没有指令通道、没有平台换版代码。

**Tech Stack:** Go 1.26（根模块 + `desktop/` 独立模块）、`net/http` + `http.ServeMux` 的方法路由、React 18 + TypeScript + Vite + vitest（`web/`）、Wails v3.0.0-beta.8（仅 `desktop/` 根包）。

**Spec:** `docs/superpowers/specs/2026-08-19-desktop-update-surface-design.md`（下称 spec）。本计划实现 spec §11 的**计划一**：6.1 状态上报、6.2 下载端点、6.3 提示框、6.4 更新页、6.6 托盘。**6.5 执行机升级的写侧不在本计划内**——执行机块此期只读。

---

## Global Constraints

**执行环境（承重，先读）**

- 执行机是 **Linux**。`desktop/` 是独立 Go 模块，它的**根包 import 了 Wails**，在没有
  `webkit2gtk` / GTK 开发头文件的机器上**编译不过**。所以：
  - **禁止**在 `desktop/` 下跑 `go build ./...` 或 `go test ./...`。
  - 只跑 `cd desktop && go test ./internal/...` 与 `gofmt -l .`。
  - `desktop/main.go` 的改动**编译验证由审核者在 macOS 上做**。你改它、你保证语法与
    命名正确、你在 ledger 里如实写「本机未编译验证（Wails 依赖缺失）」，**不要**为了
    让它编译而去装 GTK 或改动 Wails 相关代码结构。
- 根模块（仓库根的 `go.mod`）与 `web/` 不受此限，必须全绿。

**每个 task 收尾前必须跑（按改动范围取子集）**

```bash
gofmt -l .                      # 仓库根；无输出才算过
go vet ./...                    # 仓库根
go test ./...                   # 仓库根
cd desktop && gofmt -l . && go test ./internal/...
cd web && npm run typecheck && npm test && npm run build
```

**不可违背的编码约定**

- 日志一律 `slog`（agentd 侧用 `s.log`，薄壳侧用包级 `logger`）。**禁止 `fmt.Printf`**。
- 每个新建文件顶部写「职责 + 边界」块注释；每个导出函数写参数/返回/注意事项；
  非显然的分支写「为什么」的中文注释。
- 版本比较**一律**走 `selfupdate.CompareVersion`，**禁止**用字符串不等或字典序。
- 前端新文件顶部同样写中文块注释（现有 `web/src/app/**` 全都有，照抄那个风格）。
- 提交信息用中文，格式 `<type>(<scope>): <说明>`。

**不要做的事**

- 不改同步路（`desktop/internal/shell/{sync,open_sync,waitback}.go`）的任何判据。
- 不新增第三方依赖。
- 不动 `internal/agentd/reveal.go`（见 Task 3 的说明）。
- 不实现桌面端自我替换、不实现强制同步入口——spec §2 已明确排除。

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/release/client.go`（改） | 新增 `DesktopAssetName`：桌面端发布物按平台的文件名 |
| `internal/release/install.go`（改） | `FetchChecksum` 拆出 `FetchChecksumFor(ctx, rel, assetName)` |
| `internal/proto/desktop.go`（新） | `DesktopState` 与下载相关的请求/响应类型 |
| `internal/agentd/desktopstate.go`（新） | 薄壳状态的内存持有 + `PUT/GET /api/desktop/state` |
| `internal/agentd/updatedownload.go`（新） | `GET /api/update/latest`、桌面端安装包的下载/校验/打开/进度 |
| `internal/agentd/server.go`（改） | 四条新路由 |
| `desktop/internal/shell/report.go`（新） | 上报循环：组装 `DesktopState` 并周期 PUT |
| `desktop/main.go`（改） | 接线上报；托盘瘦身；设置托盘图标 |
| `desktop/build/trayicon.png`（**已存在，勿改**） | 44×44 单色+alpha 的托盘模板图标，已由审核者生成并入库 |
| `desktop/panel.go`、`desktop/remote_upgrade.go`、`desktop/frontend/upgrade.html`（删） | 调用方全部移除后成为死代码 |
| `web/src/api/types.ts`（改） | `DesktopState` / `LatestResp` / `DownloadState` 的 TS 类型 |
| `web/src/api/client.ts`（改） | 四个新请求函数 |
| `web/src/app/data/useUpdate.ts`（新） | 三条数据流的 hook |
| `web/src/app/update/UpdateToasts.tsx`（新） | 右下角提示框 |
| `web/src/app/settings/UpdatePage.tsx`（新） | 设置「更新」页 |
| `web/src/app/settings/SettingsPage.tsx`（改） | `SECTIONS` 加「更新」 |
| `web/src/app/shell/Shell.tsx`（改） | 挂 `<UpdateToasts />` |

---

## Task 0: 环境自检

**Files:** 无（只记录）

- [ ] **Step 1: 采集工具链事实**

```bash
go version
node -v || echo "NO_NODE"
npm -v  || echo "NO_NPM"
cd web && ls node_modules >/dev/null 2>&1 && echo "NODE_MODULES_OK" || echo "NODE_MODULES_MISSING"
```

- [ ] **Step 2: 把结果原样写进 ledger**

写清四行的实际输出。**如果 `node`/`npm` 不存在**：先跑 `npm ci`（若有 npm 无 node_modules）；
若连 npm 都没有，**停下来，在 ledger 里写明「Task 6–8 因缺少 Node 工具链无法执行」并继续做
Task 1–5、9**，不要跳过也不要假装跑过。这条是「没跑到结果不许写结论」的直接应用。

- [ ] **Step 3: 确认 desktop 模块的限制成立**

```bash
cd desktop && go test ./internal/... 2>&1 | tail -5
```

期望：`ok`（若干包）。若这一步就失败，说明限制比预想更严，如实记录后再继续。

---

## Task 1: 桌面端发布物的名字与校验和

**Files:**
- Modify: `internal/release/client.go`
- Modify: `internal/release/install.go`
- Test: `internal/release/client_test.go`、`internal/release/install_test.go`

**Interfaces:**
- Produces:
  - `func DesktopAssetName(tag, goos, goarch string) (string, bool)` —— 返回资产名与「该平台有没有桌面端发布物」
  - `func (i *Installer) FetchChecksumFor(ctx context.Context, rel Release, assetName string) (string, error)`
- Consumes: 既有的 `AssetName`、`Checksums`、`(*Installer).get`

- [ ] **Step 1: 写失败的测试**

在 `internal/release/client_test.go` 追加：

```go
func TestDesktopAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
		ok           bool
	}{
		{"darwin", "arm64", "handoff-desktop_v0.3.1_darwin_arm64.dmg", true},
		{"windows", "amd64", "handoff-desktop_v0.3.1_windows_amd64.zip", true},
		{"linux", "amd64", "handoff-desktop_v0.3.1_linux_amd64.AppImage", true},
		// 发布流水线只出 darwin/arm64，没有 darwin/amd64 的薄壳资产
		{"darwin", "amd64", "", false},
		{"freebsd", "amd64", "", false},
	}
	for _, c := range cases {
		got, ok := DesktopAssetName("v0.3.1", c.goos, c.goarch)
		if got != c.want || ok != c.ok {
			t.Fatalf("DesktopAssetName(%s/%s) = %q,%v，想要 %q,%v", c.goos, c.goarch, got, ok, c.want, c.ok)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/release/ -run TestDesktopAssetName -v`
Expected: FAIL，`undefined: DesktopAssetName`

- [ ] **Step 3: 实现 DesktopAssetName**

在 `internal/release/client.go` 里 `AssetName` 旁边加：

```go
// DesktopAssetName 返回桌面薄壳在某平台的发布物文件名。
//
// 参数：tag 形如 v0.3.1；goos/goarch 用 runtime 的取值。
// 返回：文件名与「该平台有没有薄壳发布物」。**ok 为 false 时文件名为空**，
// 调用方必须先判 ok，不能拿空串去拼下载地址。
//
// 注意：
//   - 前缀是 handoff-desktop_，与 CLI 的 handoff_ **不同**。checksums.txt 里两者
//     并列，用 handoff_* 通配是匹配不到薄壳资产的（release.yml 的注释点名了这一条）
//   - **不要把本函数与 AssetName 合并成一个带 flag 的函数**：两者的扩展名规则完全
//     不同（CLI 按 goos 选 tar.gz/zip，薄壳按 goos 选 dmg/zip/AppImage），
//     合并出来的函数会有两套互不相干的分支挤在一起
//   - 发布流水线只构建 darwin/arm64、windows/amd64、linux/amd64 三种薄壳，
//     其余平台一律返回 false——**判不出就说没有**，不要猜一个不存在的文件名
func DesktopAssetName(tag, goos, goarch string) (string, bool) {
	ext := ""
	switch {
	case goos == "darwin" && goarch == "arm64":
		ext = "dmg"
	case goos == "windows" && goarch == "amd64":
		ext = "zip"
	case goos == "linux" && goarch == "amd64":
		ext = "AppImage"
	default:
		return "", false
	}
	return fmt.Sprintf("handoff-desktop_%s_%s_%s.%s", tag, goos, goarch, ext), true
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/release/ -run TestDesktopAssetName -v`
Expected: PASS

- [ ] **Step 5: 写 FetchChecksumFor 的失败测试**

先读 `internal/release/install.go` 里现有的 `FetchChecksum` 与它的测试，照它的 fixture 写：

```go
func TestFetchChecksumForResolvesGivenAssetName(t *testing.T) {
	// checksums.txt 里 CLI 与薄壳两种资产并列，必须按**给定的名字**取，
	// 不能按平台推导——那正是 FetchChecksum 写死的行为
	body := "aaa  handoff_v0.3.1_darwin_arm64.tar.gz\n" +
		"bbb  handoff-desktop_v0.3.1_darwin_arm64.dmg\n"
	// …用与现有 FetchChecksum 测试相同的方式起 httptest 服务并构造 Release…
	sum, err := inst.FetchChecksumFor(context.Background(), rel, "handoff-desktop_v0.3.1_darwin_arm64.dmg")
	if err != nil {
		t.Fatalf("FetchChecksumFor 失败：%v", err)
	}
	if sum != "bbb" {
		t.Fatalf("取到 %q，想要 bbb（拿到 aaa 说明还在按平台推导名字）", sum)
	}
	_ = body
}
```

**注意**：上面的 `inst` / `rel` 构造请**照抄同文件里现有 `FetchChecksum` 测试的写法**，
不要另起一套 fixture。

- [ ] **Step 6: 跑测试确认失败**

Run: `go test ./internal/release/ -run TestFetchChecksumFor -v`
Expected: FAIL，`undefined: FetchChecksumFor`

- [ ] **Step 7: 重构 FetchChecksum**

把 `FetchChecksum` 的函数体整体挪进新的 `FetchChecksumFor(ctx, rel, assetName)`，
原函数退化成一行转发：

```go
// FetchChecksum 只下载 checksums.txt 并解出**本平台 CLI 资产**的期望哈希。
// 保留是为了不动既有调用方；新代码请直接用 FetchChecksumFor。
func (i *Installer) FetchChecksum(ctx context.Context, rel Release, goos, goarch string) (string, error) {
	return i.FetchChecksumFor(ctx, rel, AssetName(rel.Tag, goos, goarch))
}
```

`FetchChecksumFor` 的文档注释要写清：**资产名由调用方给**，因为 checksums.txt 里
CLI 与薄壳两种资产并列，按平台推导只能推出其中一种。

- [ ] **Step 8: 跑全包测试**

Run: `go test ./internal/release/ -v`
Expected: PASS（含既有的 `FetchChecksum` 用例——重构不能改它的行为）

- [ ] **Step 9: 加日志与注释自检**

`FetchChecksumFor` 在解析失败时的错误里必须带上**要找的资产名**（现有实现若已带，保持）。
确认新增的两个导出函数都有完整文档注释。

- [ ] **Step 10: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/release/
git add internal/release/
git commit -m "feat(release): 桌面端发布物的资产名与按名取校验和"
```

---

## Task 2: agentd 持有薄壳状态

**Files:**
- Create: `internal/proto/desktop.go`
- Create: `internal/agentd/desktopstate.go`
- Modify: `internal/agentd/server.go`（两条路由）
- Test: `internal/agentd/desktopstate_test.go`

**Interfaces:**
- Produces:
  - `proto.DesktopState`（见下）
  - `PUT /api/desktop/state`（薄壳上报，200）
  - `GET /api/desktop/state`（控制台读；**无壳或过期返回 204**）
- Consumes: `writeJSON`、`s.log`，以及 `internal/agentd` 既有的测试脚手架 `env`（见 `discipline_test.go` 里的 `env.getJSON` / `env.putJSON` 用法）

- [ ] **Step 1: 定义类型**

新建 `internal/proto/desktop.go`：

```go
// 本文件定义桌面薄壳与控制台之间经 agentd 中转的数据类型。
//
// 职责：只声明线上的数据形状。
// 边界：
//   - **不含任何指令类型**。薄壳只上报、不接指令：让控制台点得动薄壳需要一条
//     反向通道，那条通道比它服务的动作还贵，设计上已排除（spec §5）
//   - 不含凭据字段。这条通道只走版本与同步结论
package proto

// DesktopState 是薄壳向控制台公开的自身状态。
//
// 字段全部由薄壳填，agentd 只做带 TTL 的转发。控制台据此判断「有没有新版」
// ——**必须用薄壳的版本比，不能用 agentd 自己的版本**：同步被拦或失败时两者
// 恰好不等，用 agentd 的版本会去劝用户下载一个他已经装好了的版本。
type DesktopState struct {
	// AppVersion 是薄壳自身版本（desktop 侧的 embedbin.Version）。空串=判不出
	// （开发构建未注入版本），此时控制台一律不提示。
	AppVersion string `json:"app_version"`
	// SyncPlan 是本次开机同步的结论：skip / blocked / failed / done。
	SyncPlan string `json:"sync_plan"`
	// SyncBusy 是 blocked 时的活跃任务数；**-1 表示探测失败**，不要当 0 用。
	SyncBusy int `json:"sync_busy"`
	// SyncError 是 failed 时的原文，供控制台原样展示。
	SyncError string `json:"sync_error,omitempty"`
}
```

- [ ] **Step 2: 写失败的测试**

新建 `internal/agentd/desktopstate_test.go`。**先读 `internal/agentd/discipline_test.go`
的头部**，照抄它建 `env` 的方式；下面只写断言主体：

```go
func TestDesktopStateAbsentUntilReported(t *testing.T) {
	env := newEnv(t) // ← 照抄 discipline_test.go 的建法
	if code := env.getRaw2(t, "/api/desktop/state"); code != http.StatusNoContent {
		t.Fatalf("没上报过时得到 %d，想要 204——控制台靠 204 判断「没有壳」", code)
	}
}

func TestDesktopStateRoundTrip(t *testing.T) {
	env := newEnv(t)
	want := proto.DesktopState{AppVersion: "v0.3.1", SyncPlan: "blocked", SyncBusy: 2}
	if code := env.putJSON(t, "/api/desktop/state", want); code != http.StatusOK {
		t.Fatalf("上报得到 %d，想要 200", code)
	}
	var got proto.DesktopState
	if code := env.getJSON(t, "/api/desktop/state", &got); code != http.StatusOK {
		t.Fatalf("读取得到 %d，想要 200", code)
	}
	if got != want {
		t.Fatalf("读到 %+v，想要 %+v", got, want)
	}
}

func TestDesktopStateExpiresAfterTTL(t *testing.T) {
	env := newEnv(t)
	if code := env.putJSON(t, "/api/desktop/state",
		proto.DesktopState{AppVersion: "v0.3.1", SyncPlan: "done"}); code != http.StatusOK {
		t.Fatalf("上报失败")
	}
	// 把时钟推过 TTL：壳没在跑就等于没有壳，陈旧状态必须消失，
	// 否则纯浏览器会话会看到一个点了没反应的按钮
	env.srv.desktopNow = func() time.Time { return time.Now().Add(desktopStateTTL + time.Second) }
	if code := env.getRaw2(t, "/api/desktop/state"); code != http.StatusNoContent {
		t.Fatalf("过期后得到 %d，想要 204", code)
	}
}
```

若既有 `env` 没有「只取状态码」的辅助函数，在本测试文件里加一个私有小函数
`getRaw2(t, path) int`，**不要改动既有的 env 结构**。

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestDesktopState -v`
Expected: FAIL（404 或编译错误）

- [ ] **Step 4: 实现**

新建 `internal/agentd/desktopstate.go`：

```go
// 本文件实现薄壳状态的中转：PUT/GET /api/desktop/state。
//
// 职责：把薄壳上报的自身状态（版本 + 本次开机同步的结论）在内存里持有一小段
// 时间，供控制台读取。
//
// 边界（承重）：
//   - **只在内存，不落盘。** 壳没在跑就等于没有壳。落盘会让「上次开过桌面端」
//     的痕迹在纯浏览器会话里伪装成「现在有个壳」，于是控制台渲染出一个点了
//     没反应的按钮
//   - **不解释内容。** SyncPlan 的取值语义属于薄壳，这里不做校验、不做映射
//   - 不含反向通道：薄壳只上报、不接指令（spec §5）
package agentd

// desktopStateTTL 是薄壳状态的有效期。
//
// 取 30s = 薄壳上报间隔（10s）的三倍：容得下两次丢包，又不会在薄壳退出后
// 让控制台继续显示半分钟以上的幻影。
const desktopStateTTL = 30 * time.Second

func (s *Server) handleDesktopStatePut(w http.ResponseWriter, r *http.Request) { … }
func (s *Server) handleDesktopStateGet(w http.ResponseWriter, r *http.Request) { … }
```

实现要点：

- 在 `Server` 上加三个字段（放在既有字段旁，**加锁**，因为上报与读取来自不同连接）：
  `desktopMu sync.Mutex` / `desktopState *proto.DesktopState` / `desktopAt time.Time`，
  外加一个测试缝 `desktopNow func() time.Time`（nil 时用 `time.Now`）。
- PUT：解 JSON 失败返回 400 + `{"error": "..."}`；成功后 `s.log.Info("薄壳状态已上报",
  "app_version", st.AppVersion, "sync_plan", st.SyncPlan, "busy", st.SyncBusy)`，
  返回 200 与空体。
- GET：无状态或已过期 → `s.log.Debug("薄壳状态缺席或已过期")` + `w.WriteHeader(204)`；
  否则 `writeJSON(w, 200, st)`。

在 `server.go` 的路由块里、`GET /api/machines` 附近加：

```go
api.HandleFunc("PUT /api/desktop/state", s.handleDesktopStatePut)
api.HandleFunc("GET /api/desktop/state", s.handleDesktopStateGet)
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestDesktopState -v`
Expected: PASS

- [ ] **Step 6: 变异复验（必做）**

把 `desktopStateTTL` 的过期判断从 `>` 改成 `>=` 之外的**真实反转**：删掉过期判断，
让 GET 无条件返回 200。跑 `TestDesktopStateExpiresAfterTTL`，**必须红**。
改回来，必须绿。在 ledger 里记这两次结果。红不了就说明测试是假门，重写测试。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/agentd/
git add internal/proto/desktop.go internal/agentd/desktopstate.go internal/agentd/desktopstate_test.go internal/agentd/server.go
git commit -m "feat(agentd): 中转薄壳状态，带 30s TTL"
```

---

## Task 3: agentd 下载桌面端安装包

**Files:**
- Create: `internal/agentd/updatedownload.go`
- Modify: `internal/proto/desktop.go`（追加类型）
- Modify: `internal/agentd/server.go`（三条路由）
- Test: `internal/agentd/updatedownload_test.go`

**Interfaces:**
- Consumes: Task 1 的 `release.DesktopAssetName` / `(*Installer).FetchChecksumFor`；
  既有的 `selfupdate.LoadCLICheck` / `CLICheckStale` / `SaveCLICheck`
- Produces:
  - `GET /api/update/latest`（`?refresh=1` 绕过 24h 缓存）
  - `POST /api/update/desktop/download`
  - `GET /api/update/desktop/download`（进度）

- [ ] **Step 1: 追加类型**

在 `internal/proto/desktop.go` 追加：

```go
// LatestResp 是 GET /api/update/latest 的响应。
type LatestResp struct {
	// Tag 是最新发布的版本号。**空串表示查不出**（限流、断网、缓存为空），
	// 消费方一律按「没有新版」处理——通知是锦上添花，绝不能自己变成故障源。
	Tag       string `json:"tag"`
	CheckedAt string `json:"checked_at,omitempty"` // RFC3339；空=从未查过
}

// DownloadState 是桌面端安装包下载的进度与结果。
type DownloadState struct {
	// Stage：idle / downloading / verifying / done / failed
	Stage string `json:"stage"`
	Tag   string `json:"tag,omitempty"`
	// Percent 为 -1 表示不可知（服务端没给 Content-Length）。
	Percent int    `json:"percent"`
	Path    string `json:"path,omitempty"`  // done 时的绝对路径
	Opened  bool   `json:"opened"`          // 是否成功唤起文件管理器
	Error   string `json:"error,omitempty"`
}
```

- [ ] **Step 2: 写失败的测试**

新建 `internal/agentd/updatedownload_test.go`。把外部依赖做成 Server 上的缝
（下一步实现时加）：`downloadFetch func(ctx, tag, assetName) ([]byte, string, error)`
与 `downloadOpen func(path string) error`。测试断言四条：

```go
// 校验不过必须删文件：留一份坏安装包在下载目录里，用户下次会装上它
func TestDownloadDeletesFileOnChecksumMismatch(t *testing.T)

// 同时只允许一个下载：重复 POST 返回 409
func TestDownloadRejectsConcurrent(t *testing.T)

// 唤起文件管理器失败**不影响下载成功**：仍 200，opened=false 且带绝对路径，
// 页面据此显示路径让用户自己去找
func TestDownloadSucceedsWhenOpenerFails(t *testing.T)

// 平台没有薄壳发布物时明确拒绝，而不是去下一个不存在的文件名
func TestDownloadRefusesUnsupportedPlatform(t *testing.T)
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestDownload -v`
Expected: FAIL

- [ ] **Step 4: 实现**

新建 `internal/agentd/updatedownload.go`，文件头写清：

```
// 本文件实现「查最新版」与「下载桌面端安装包」：
//   GET  /api/update/latest            —— 缓存的最新 tag（?refresh=1 绕过）
//   POST /api/update/desktop/download  —— 下载本平台薄壳安装包、校验、打开
//   GET  /api/update/desktop/download  —— 进度
//
// 边界（承重）：
//   - **不做换版。** 下完把安装包交给用户，最后一步（拖进应用程序 / 解压覆盖）
//     由人完成。自我替换需要一条控制台→薄壳的指令通道，比它服务的动作还贵（spec §5）
//   - **不复用 POST /api/workspaces/reveal。** 那个端点的 revealTarget 会硬拒绝
//     跑出工作树的路径——那是它的设计目的，不是缺陷。这里揭示的是下载目录里的
//     文件，两者的安全边界不同，必须各写各的
//   - 检查缓存与 CLI、薄壳**共用同一个文件**（selfupdate.CLICheckPath）：
//     api.github.com 有 60 次/小时/IP 的匿名限流，多消费者各查各的正是触发它的方式
```

要点：

- `GET /api/update/latest`：读 `selfupdate.LoadCLICheck(s.dataDir)`；
  陈旧（或 `?refresh=1`）则查一次并 `SaveCLICheck`。**任何失败都返回 200 + 空 Tag**，
  不返回错误——沿用 `CheckLatest` 的既有约定。
- `POST /api/update/desktop/download`：
  1. `release.DesktopAssetName(tag, runtime.GOOS, runtime.GOARCH)`，`ok==false` → 400。
  2. 已有同名文件且 sha256 对得上 → 跳过下载，直接进第 5 步（`s.log.Info("安装包已存在，跳过下载", …)`）。
  3. 取校验和（`FetchChecksumFor`）→ 下载 → 比对。**不符：删除文件**，
     `s.log.Error("安装包校验不通过，已删除", "want", want, "got", got, "path", p)`，
     置 `Stage: "failed"`，端点返回 502。
  4. 落到 `<DataDir>/downloads/<资产名>`，落盘后删掉同目录里其它 `handoff-desktop_*`
     （只留最新一个，每个 20MB 上下）。
  5. 调 `downloadOpen(path)`。**失败只记 Warn**，`Opened=false`，整体仍算成功。
  6. 全程用 `s.downloadMu` 保护一个 `*proto.DownloadState`；已有 `downloading`/`verifying`
     在跑时 POST 返回 409。
- `downloadOpen` 的生产实现按平台：darwin `open <file>`、windows
  `explorer /select,<file>`（**注意 explorer 的退出码天生非 0，不要据此判失败**，
  只在进程起不来时算失败）、其余 `xdg-open <dir>`。
- 路由：

```go
api.HandleFunc("GET /api/update/latest", s.handleUpdateLatest)
api.HandleFunc("POST /api/update/desktop/download", s.handleDesktopDownloadStart)
api.HandleFunc("GET /api/update/desktop/download", s.handleDesktopDownloadState)
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run "TestDownload|TestUpdateLatest" -v`
Expected: PASS

- [ ] **Step 6: 变异复验（必做）**

把「校验不符时删文件」那行删掉，`TestDownloadDeletesFileOnChecksumMismatch` **必须红**；
把 409 的并发判断删掉，`TestDownloadRejectsConcurrent` **必须红**。两次都改回来跑绿，
结果记进 ledger。

- [ ] **Step 7: 日志与注释自检**

对照 `instrumenting-code`：下载开始（tag + 资产名 + 目标路径）、校验通过/不通过（带两个
sha 值）、跳过下载、唤起文件管理器的成败、清理旧安装包的条数——六处都要有日志，
错误分支必须带 cause。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/agentd/
git add internal/agentd/updatedownload.go internal/agentd/updatedownload_test.go internal/proto/desktop.go internal/agentd/server.go
git commit -m "feat(agentd): 下载并校验桌面端安装包，下完唤起文件管理器"
```

---

## Task 4: 薄壳上报自身状态

**Files:**
- Create: `internal/client/desktop.go`（根模块：`PutDesktopState` 方法）
- Test: `internal/client/desktop_test.go`
- Create: `desktop/internal/shell/report.go`
- Test: `desktop/internal/shell/report_test.go`
- Modify: `desktop/main.go`（接线）

**先做 client 方法**（在根模块里，能编译能测）：照 `internal/client/update.go` 的写法加

```go
// PutDesktopState 上报薄壳自身状态（PUT /api/desktop/state）。
//
// 注意：这是**单向**通道。agentd 只持有并转发给控制台，不会有任何指令回来
// ——让控制台点得动薄壳需要一条反向通道，设计上已排除（spec §5）。
func (c *Client) PutDesktopState(ctx context.Context, st proto.DesktopState) error
```

测试用 `httptest` 起一个假 agentd，断言方法、路径与请求体。

**Interfaces:**
- Produces:
  - `type ReportDeps struct { Put func(context.Context, proto.DesktopState) error; Now func() time.Time }`
  - `func RunReporter(ctx context.Context, log *slog.Logger, snapshot func() proto.DesktopState, d ReportDeps)` —— 阻塞循环，调用方起 goroutine

- [ ] **Step 1: 写失败的测试**

```go
func TestReporterKeepsBeatingAfterFailure(t *testing.T) {
	// 上报失败绝不能中断循环：这条通道坏掉时，托盘、控制台加载、同步路
	// 都必须照常工作——所以它只能退避重试，不能返回错误往上抛
	var calls int
	d := ReportDeps{Put: func(context.Context, proto.DesktopState) error {
		calls++
		if calls == 1 {
			return errors.New("连接被拒")
		}
		return nil
	}}
	// …用短 interval 的测试缝跑三轮，断言 calls >= 3…
}

func TestReporterSendsCurrentSnapshot(t *testing.T) {
	// snapshot 每轮重新取：同步结论会在开机后才落定，
	// 上报一次就不管会让控制台永远看到 skip
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd desktop && go test ./internal/shell/ -run TestReporter -v`
Expected: FAIL

- [ ] **Step 3: 实现 report.go**

文件头写清职责与边界：**只负责「按节奏把快照发出去」，不决定快照内容**（内容由
`main.go` 的 `snapshot` 闭包从 `traySync`/`traySyncErr` 组装），**失败只退避不上抛**。
间隔常量 `reportInterval = 10 * time.Second`，注释写清它与 agentd 侧 `desktopStateTTL`
（30s）的三倍关系——**改一个必须改另一个**。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd desktop && go test ./internal/shell/ -run TestReporter -v`
Expected: PASS

- [ ] **Step 5: 在 main.go 接线**

在 `ApplicationStarted` 的回调里、`go openConsole()` 之后加一个 goroutine：

```go
// 上报自身状态供控制台读取。**放在 openConsole 之后、独立 goroutine**：
// 它要发 HTTP、可失败，绝不能挡在打开控制台前面（与新版检查同一条纪律）。
go shell.RunReporter(context.Background(), logger, snapshotDesktopState, reportDeps(ep))
```

`snapshotDesktopState` 读 `trayMu` 保护下的 `traySync` / `traySyncErr`，
组装成 `proto.DesktopState`；`SyncPlan` 用 `traySync.Plan.String()`，
`SyncBusy` 在非 blocked 时填 0、blocked 且探测失败时填 -1（**沿用既有语义，不要改**）。
在 `noteSyncFailed` / `noteSyncBlocked` 末尾各加一次立即上报（不等下一个 10s 周期）。

**本步无法在 Linux 上编译验证**（见 Global Constraints）。写完后：
- 逐行自检导入是否齐全、`proto` 包路径是否正确（`desktop/go.mod` 里根模块是
  `replace` 还是正常依赖，先看清楚再写 import）；
- 在 ledger 里如实写「main.go 改动未经编译验证，原因：执行机缺 Wails 构建依赖」。

- [ ] **Step 6: 提交**

```bash
cd desktop && gofmt -l . && go test ./internal/...
cd .. && gofmt -l .
git add desktop/
git commit -m "feat(desktop): 薄壳单向上报自身状态，10s 一次"
```

---

## Task 5: 托盘瘦身与图标，删除死代码

**Files:**
- Modify: `desktop/main.go`
- Delete: `desktop/panel.go`、`desktop/panel_test.go`（若有）、`desktop/remote_upgrade.go`、`desktop/remote_upgrade_test.go`、`desktop/frontend/upgrade.html`
- Modify: `desktop/frontend/vite.config.ts`（若它把 upgrade.html 列为入口，必须同步删）

- [ ] **Step 1: 先确认死代码的调用方真的都没了**

```bash
cd desktop && grep -rn "openUpgradePanel\|runRemoteUpgrade\|showBlockedPanel\|showSyncFailurePanel\|openReleasePage\|forceSyncNow\|upgrade.html" . --include='*.go' --include='*.ts' --include='*.html' --include='*.tsx'
```

把输出贴进 ledger。**只有当剩下的引用全在将被删除的文件里时才继续**；
若发现别处还在用，停下来记录，不要硬删。

- [ ] **Step 2: 改 rebuildTray**

删到只剩两项，函数退化成常量菜单：

```go
// rebuildTray 构建托盘菜单。
//
// 只有两项。三条动态提示（有新版 / 有更新待应用 / 上次同步失败）已经移到控制台
// 右下角的提示框，「升级执行机」并入控制台设置的更新页，强制同步入口直接删除
// ——重开一次桌面端就会重走 SyncOnOpen，不必再造第二个入口（spec §2）。
//
// 保留函数名与加锁：它仍会被启动序列调用一次，且 Wails 的菜单对象不并发安全。
func rebuildTray() { … }
```

`traySync` / `traySyncErr` / `trayLatest` 三个包级变量**保留**——它们现在是 Task 4
上报的数据源。在它们的声明处补一句注释说明这一点，否则下一个人会当成死变量删掉。

- [ ] **Step 3: 设置托盘图标**

`desktop/build/trayicon.png` 已在库里（44×44、单色 + alpha，由 `docs/assets/handoff-mark.svg`
渲染而来）。加 embed 并设置：

```go
//go:embed build/trayicon.png
var trayIconTemplate []byte

//go:embed build/appicon.png
var trayIconColor []byte
```

```go
tray := app.SystemTray.New()
// macOS 用模板图标：系统只取 alpha 通道，自动随明暗菜单栏反色。
// 其余平台没有这个机制，用彩色图。
//
// 尺寸不用操心：Wails 的 systemTraySetIcon 会 setSize 到状态栏厚度（22pt），
// 44px 是为了 retina 下清晰。
if runtime.GOOS == "darwin" {
	tray.SetTemplateIcon(trayIconTemplate)
} else {
	tray.SetIcon(trayIconColor)
}
// 标签清空：之前只设 label 不设图标，菜单栏里显示的是「handoff」四个字
tray.SetLabel("")
```

- [ ] **Step 4: 删除死代码**

```bash
cd desktop && git rm panel.go remote_upgrade.go remote_upgrade_test.go frontend/upgrade.html
# panel_test.go 若存在也一并删
```

删完再跑一次 Step 1 的 grep，**必须无输出**（除了注释里提到的历史说明）。

- [ ] **Step 5: 验证能验的部分**

```bash
cd desktop && gofmt -l . && go test ./internal/...
cd frontend && npm run build   # 确认删掉 upgrade.html 后前端仍能构建
```

若 `desktop/frontend` 没有独立的构建脚本，跳过前端那条并在 ledger 里说明。
**main.go 与托盘改动无法在 Linux 上编译验证**，如实记录。

- [ ] **Step 6: 提交**

```bash
git add -A desktop/
git commit -m "refactor(desktop): 托盘瘦身到两项并换成标志，删掉升级面板与远程升级"
```

---

## Task 6: 前端数据层

**Files:**
- Modify: `web/src/api/types.ts`、`web/src/api/client.ts`
- Create: `web/src/app/data/useUpdate.ts`、`web/src/app/lib/version.ts`
- Test: `web/src/app/data/useUpdate.test.ts`、`web/src/app/lib/version.test.ts`

**Interfaces:**
- Produces：
  - `fetchDesktopState(): Promise<DesktopState | null>`（**204 解成 `null`**）
  - `fetchLatest(refresh?: boolean): Promise<LatestResp>`
  - `fetchDownloadState(): Promise<DownloadState>` / `startDownload(): Promise<void>`
  - `useDesktopState()` / `useLatest()` / `useDownload(active: boolean)`

- [ ] **Step 1: 加类型**

在 `web/src/api/types.ts` 加与 `internal/proto/desktop.go` 一一对应的三个 interface，
字段名照抄 json tag。**注释里点明「与 internal/proto/desktop.go 对应，两边一起改」。**

- [ ] **Step 2: 写失败的测试**

```ts
// 204 必须解成 null：控制台靠 null 判断「没有壳」，
// 解成 {} 会让「桌面应用」整块渲染出一个点了没反应的按钮
it('fetchDesktopState 把 204 解成 null', async () => { … })

// 版本比较：v0.10.0 比 v0.9.0 新。字典序会判反，
// 这条用例专门守住「不许用字符串比大小」
it('hasNewer 认为 v0.10.0 比 v0.9.0 新', () => { … })
```

`hasNewer` 放在 `web/src/app/lib/version.ts`（新建），实现按数字段比较，
**与 Go 侧 `selfupdate.CompareVersion` 同语义**，含预发布号（`v0.3.0-rc8 < v0.3.0`）。
读一遍 `internal/selfupdate/clicheck.go` 里 `CompareVersion` 的实现再写，行为要一致。

- [ ] **Step 3: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/data/useUpdate.test.ts`
Expected: FAIL

- [ ] **Step 4: 实现**

`request` 现有实现遇到 204 可能会尝试解 JSON——**先读 `web/src/api/client.ts` 的
`request` 是怎么处理空体的**，需要时为 `fetchDesktopState` 单写一个不经 `request`
的小函数，而不是改 `request` 的全局行为（那会影响所有接口）。

三个 hook 用既有的 `usePoll`：`useDesktopState` 10s、`useLatest` 10s、
`useDownload` 在 `active` 为真时 1s（`usePoll` 的 `opts.enabled` 已支持）。

- [ ] **Step 5: 跑测试确认通过 + 提交**

```bash
cd web && npm run typecheck && npx vitest run src/app/data/useUpdate.test.ts src/app/lib/version.test.ts
git add web/src
git commit -m "feat(web): 更新面的数据层与版本比较"
```

---

## Task 7: 右下角提示框

**Files:**
- Create: `web/src/app/update/UpdateToasts.tsx`
- Test: `web/src/app/update/UpdateToasts.test.tsx`
- Modify: `web/src/app/shell/Shell.tsx`

**形态基准**：`prototypes/desktop-update/index.html`。**开工前把它在浏览器里打开看一眼**
（或直接读它的 HTML/CSS），文案、按钮位置、堆叠方向、让位行为都以它为准。

- [ ] **Step 1: 写失败的测试**

```tsx
// 浏览器里一条都不出现：isDesktopShell 为假时整个组件不渲染
it('非桌面壳时不渲染任何提示', () => { … })

// 三种提示的出现条件
it('app_version 落后于 latest 时弹「有新版」', () => { … })
it('sync_plan=blocked 时弹「有更新待应用」，且主按钮是「知道了」', () => { … })
it('sync_plan=failed 时弹「上次同步失败」', () => { … })

// 关掉之后本次会话不再弹（sessionStorage，不是 localStorage——
// 后者会让「我上个月点过稍后」永久吃掉提示，那正是本期要修的病）
it('关闭后同一 (kind, tag) 不再出现', () => { … })
```

- [ ] **Step 2–4: 跑红 → 实现 → 跑绿**

实现要点：

- 顶层 `if (!isDesktopShell()) return null`。
- 「有新版」的主按钮调 `startDownload()`，之后把 `useDownload(true)` 的
  `stage`/`percent` 画成进度条；`stage === 'done'` 时文案换成
  「已下载 <文件名>，已在访达中打开」（`opened === false` 时换成「已下载到 <path>」）。
- 另两条的主按钮是「知道了」，点击=关闭，**没有次按钮**。
- 每条右下角一个「查看详情」链到设置的更新分区。
- 与 home 悬浮窗的让位：home 打开时容器 `bottom` 从 20px 抬到 236px。
  **先读 `web/src/app/homedock/` 是怎么表达「home 开着」的**，用它已有的状态，
  不要新造一个全局。

- [ ] **Step 5: 挂进 Shell + 提交**

```bash
cd web && npm run typecheck && npm test && npm run build
git add web/src
git commit -m "feat(web): 控制台右下角的更新提示框"
```

---

## Task 8: 设置「更新」页

**Files:**
- Create: `web/src/app/settings/UpdatePage.tsx`
- Test: `web/src/app/settings/UpdatePage.test.tsx`
- Modify: `web/src/app/settings/SettingsPage.tsx`

**形态基准**：`prototypes/desktop-update/pages/settings.html` 的「更新」分区。

- [ ] **Step 1: 写失败的测试**

```tsx
// 没有壳（204）时前两块不渲染，但执行机块必须还在——
// 它是本页唯一浏览器里也能用的能力
it('desktopState 为 null 时只渲染执行机块', () => { … })

// 本机那行不给升级按钮：本机 agentd 的版本由薄壳的同步路决定，
// 这里再给一个入口就是第二条换版路径
it('本机行显示「随桌面应用一起更新」且没有按钮', () => { … })

// 一期执行机块只读
it('远端机器行显示「可升级」但本期不渲染升级按钮', () => { … })
```

- [ ] **Step 2–4: 跑红 → 实现 → 跑绿**

三块：桌面应用（当前/最新版本、变更摘要折叠、「下载安装包」+「重新检查」）、
同步状态（待应用/上次同步各一行，**不给「立即应用」按钮**）、
执行机（`fetchMachines()` + `useLatest()`，本机行特殊处理）。

`SettingsPage.tsx` 的 `SECTIONS` 追加 `{ key: 'update', label: '更新' }`，接在 `env` 之后；
有可用更新时在该导航项上挂一个琥珀点。

- [ ] **Step 5: 提交**

```bash
cd web && npm run typecheck && npm test && npm run build
git add web/src
git commit -m "feat(web): 设置页新增更新分区"
```

---

## Task 9: 整分支终审

- [ ] **Step 1: 全量回归**

```bash
gofmt -l .
go vet ./...
go test ./...
cd desktop && gofmt -l . && go test ./internal/...
cd ../web && npm run typecheck && npm test && npm run build
```

**每条命令的实际输出（含包数与用例数）都要贴进 ledger**，不要只写「全绿」。

- [ ] **Step 2: 死代码复查**

```bash
grep -rn "upgrade.html\|openUpgradePanel\|runRemoteUpgrade" --include='*.go' --include='*.ts' --include='*.tsx' --include='*.html' . | grep -v node_modules | grep -v '^./docs'
```

期望：无输出（`docs/` 下的历史说明不算）。

- [ ] **Step 3: 对照 spec 自查**

逐条走 spec §6.1–6.4、§6.6 与 §9（日志与注释），在 ledger 里列出每条的落点文件与行号。
**§3 表里的每条承重事实**是否都在对应代码处留了「为什么」的中文注释——
尤其是「不复用 `workspaces/reveal`」那条。

- [ ] **Step 4: 写交付摘要**

分支名、提交数、每条验证命令的实际输出、**未验证项的清单**（至少包含：
`desktop/main.go` 与托盘改动未经编译验证、真机走查未做）。

---

## 审核者本地验收（**不派发，执行者不要做这一段**）

以下步骤需要 GUI、需要在 macOS/Windows 上构建薄壳，且要驱动桌面端自身，
执行机做不了也不该做。由审核者在本地完成：

1. `cd desktop && go build ./...`（macOS）——确认 Task 4/5 的 `main.go` 改动真的能编译。
2. 构建并运行薄壳，确认：菜单栏显示的是标志不是「handoff」四个字，明暗两种菜单栏下都看得清；
   菜单只剩两项。
3. 控制台里确认右下角提示框出现、点「下载」后进度条走完、DMG 被挂载弹出。
4. 校验落盘文件的 sha256 与 release 的 `checksums.txt` 一致。
5. 浏览器（非薄壳）打开同一个控制台：一条提示框都不出现，更新页只有执行机那一块。
6. 形态对照 `prototypes/desktop-update/` 副本。
