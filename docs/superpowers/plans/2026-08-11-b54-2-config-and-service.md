# B54.2 配置与托管 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让「装完就能配、配完能被托管」——`handoff init` 把一台新机器问答式配完，`handoff service` 把 agentd 交给 launchd/systemd 托管，agentd 学会优雅关停，从而崩了会被拉起、退出 0 也会被拉起。

**Architecture:** 三块彼此独立、经 `cmd/` 层组合：`internal/toolchain` 只回答「这台机器上四家 executor 各是什么状态」；`internal/service` 只回答「怎么把一条命令交给本平台的进程管理器」；`internal/agentd/shutdown.go` 只回答「收到停机意图后怎么把连接和数据库收干净」。三者都不知道彼此，也都不知道自动更新——B54.3 才把它们串起来。

**Tech Stack:** Go 1.26.1、cobra、`gopkg.in/yaml.v3`、`github.com/mattn/go-isatty`（已在依赖里）、launchd（plist + `launchctl`）、systemd（unit + `systemctl`）

**Spec:** [docs/superpowers/specs/2026-08-11-install-and-autoupdate-design.md](../specs/2026-08-11-install-and-autoupdate-design.md)（本计划实现其中的 B 期，见 spec §6）

## Global Constraints

以下取值逐字来自 spec，**不得在实现时另行发明**：

- **launchd 标签**：`dev.gosuper.handoff.agentd`；plist 落点 `~/Library/LaunchAgents/dev.gosuper.handoff.agentd.plist`
- **systemd unit 名**：`handoff-agentd.service`；落点 `/etc/systemd/system/handoff-agentd.service`
- **plist 里不写 `AbandonProcessGroup`**。P1 探针（spec §7.1）实测：setsid 的执行者能活过 `launchctl kickstart -k` 与 `bootout`，不需要它。**不要"保险起见加上"**——加了等于给一条已被实测证伪的假设留下痕迹，下一个人会以为它是必需的。
- **systemd 的 `KillMode=process` 一个字都不能动**（B36 硬要求，理由写在 [deploy/handoff-agentd.service](../../../deploy/handoff-agentd.service) 的注释里）。本期只改 `Restart=on-failure` → `Restart=always`。
- **`ExecStart` / `ProgramArguments` 不带 `--executor`**（spec D5）：该 flag 只覆盖 `cfg.Executor.Default`，而 `config.go:132` 已经默认 `opencode`，写进单元只会让"改配置不生效"变成一个隐蔽坑。
- **claude 只有两态**：`没装` / `已安装（登录态未知）`。devbox 上不存在 `~/.claude/.credentials.json`，OAuth 凭据在 macOS Keychain 里，轻量判据够不着；`~/.claude.json` 是配置不是凭证。**不得猜成「就绪」，也不得猜成「未就绪」**——猜哪边都是编造。其余三家三态：`没装` / `已安装但无凭证` / `就绪`。
- **凭证判据路径**（devbox 已查实，逐字）：opencode `~/.local/share/opencode/auth.json`、codex `~/.codex/auth.json`、grok `~/.grok/auth.json`
- **init 绝不发起真实模型调用**。README 里「`claude -p "hi"` 能出结果即视为就绪」是给人看的验证方法，不是 init 的判据——那是一次真实付费调用。
- **探测结果只影响排序与标注，不阻断任何选择**：没装任何 executor 也要能配完（纯审核者机的正常情况），选了「无凭证」的执行者只警告不拦。
- **优雅关停的退出码约定**：正常关停 `exit 0`。这就是为什么 systemd 必须改 `Restart=always`——`on-failure` 在 exit 0 时**不会**重启，自更新换版后服务就此消失且无人知晓。

## 本期不做

这些属 B54.3，**看到相关代码点也不要顺手做**：`internal/release`、`internal/selfupdate`、`handoff upgrade`、CLI 更新提示、agentd 里的更新循环、「非托管则拒绝自动更新」的判据实现。本期只把 `update` 配置段和优雅关停的**接口**留出来。

---

## File Structure

**新建**

| 文件 | 职责 |
|---|---|
| `internal/agentd/shutdown.go` | 停机协调：信号 + 内部触发 → `http.Server.Shutdown` → 收尾闭包。**这是新建能力**，现 agentd 完全没有信号处理 |
| `internal/agentd/shutdown_test.go` | 触发幂等、优雅关停返回 nil、监听失败原样返回 |
| `internal/toolchain/detect.go` | 四家 executor 的三态探测（claude 两态） |
| `internal/toolchain/detect_test.go` | 表驱动：四家 × 各状态 |
| `internal/service/service.go` | 托管接口 + 平台分发 + `Spec`/`Status` 类型 |
| `internal/service/launchd.go` | plist 生成、`launchctl bootstrap/bootout/print` |
| `internal/service/launchd_test.go` | plist 内容断言（含「不含 AbandonProcessGroup」）、命令序列 |
| `internal/service/systemd.go` | unit 生成、`systemctl daemon-reload/enable --now/disable` |
| `internal/service/systemd_test.go` | unit 内容断言（含 `Restart=always` 与 `KillMode=process`）、无 sudo 权限时的报错文本 |
| `cmd/init.go` | `handoff init`：探测 + 11 项问答 + 写配置；非 tty 降级 |
| `cmd/init_test.go` | 非交互降级、交互全默认、幂等重跑、角色分支 |
| `cmd/service.go` | `handoff service install/uninstall/status` |
| `cmd/service_test.go` | 三个子命令的输出与错误路径 |

**修改**

| 文件 | 改动 |
|---|---|
| `internal/config/config.go` | 加 `Update UpdateConfig` 字段 + 默认值 + `validate` + **未知字段消息里的键名清单** |
| `internal/config/config_test.go` | update 段解析、默认值、非法 interval |
| `cmd/agentd.go` | 接优雅关停：ListenAndServe 改为经 `agentd.Shutdown` 运行 |
| `deploy/handoff-agentd.service` | `Restart=on-failure` → `Restart=always` |
| `README.md` | 补 `init` / `service` 说明 + 「停服务需 systemctl stop / launchctl bootout」的形态变化 |

**边界**

- `internal/toolchain` 只探测、不决策、不写配置。
- `internal/service` 只管服务单元，不下载、不判断版本、不读 handoff 配置（`Spec` 由调用方给全）。
- `internal/agentd/shutdown.go` 不知道为什么要停，只知道怎么停干净。

---

### Task 1: config 加 `update` 段

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `config.UpdateConfig{Auto bool; Interval time.Duration}`
  - `Config.Update UpdateConfig` 字段，默认 `{Auto: true, Interval: 6 * time.Hour}`
  - `config.Save(path string, cfg *Config) error` —— 既有未导出 `save` 的导出包装，Task 7 的 `handoff init` 要用

- [ ] **Step 1: 写失败的测试**

在 `internal/config/config_test.go` 末尾追加：

```go
// 没写 update 段时必须落在出厂默认上。
//
// why 单独钉一例：Load 用的是「字面量预置默认 + yaml 覆盖式解码」，
// 新加的段一旦忘了写进那个字面量，表现就是 Auto=false、Interval=0——
// 自动更新静默不工作，且没有任何报错。
func TestUpdateDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Update.Auto {
		t.Error("update.auto 默认应为 true")
	}
	if cfg.Update.Interval != 6*time.Hour {
		t.Errorf("update.interval 默认应为 6h，得到 %s", cfg.Update.Interval)
	}
}

// 显式写了就以写的为准。
func TestUpdateExplicit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:7777\nupdate:\n  auto: false\n  interval: 30m\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Update.Auto {
		t.Error("显式 auto: false 未生效")
	}
	if cfg.Update.Interval != 30*time.Minute {
		t.Errorf("interval=%s，期望 30m", cfg.Update.Interval)
	}
}

// 启用自动更新却给了非正 interval：必须在启动期拦下。
//
// why：0 会让更新循环退化成忙轮询，每个 tick 都立刻到期，把 GitHub API
// 的匿名限流（60 次/小时）几秒钟打满，然后所有版本检查一起失败。
// 这和 stalltimeout 必须为正是同一类问题，处置也保持一致：显式写错才拦，
// 省略该键走默认值是正常用法。
func TestUpdateIntervalMustBePositiveWhenAuto(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:7777\nupdate:\n  auto: true\n  interval: 0s\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("interval=0 且 auto=true 时应报错")
	} else if !strings.Contains(err.Error(), "update.interval") {
		t.Fatalf("报错应点名 update.interval，得到: %v", err)
	}
}

// 关掉自动更新时不校验 interval——没启用的东西写错不该拦启动。
func TestUpdateIntervalNotCheckedWhenAutoOff(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:7777\nupdate:\n  auto: false\n  interval: 0s\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("auto=false 时不应校验 interval，却报错: %v", err)
	}
}

// 未知字段的报错必须把 update 列进已知键清单。
//
// why：那条消息是用户唯一能看到的「支持哪些键」的清单。漏了 update，
// 用户配了正确的键、看到「不支持」的报错，会去删掉本来对的配置。
func TestUnknownFieldMessageListsUpdate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("nonsense_key: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("未知键应报错")
	}
	if !strings.Contains(err.Error(), "update{auto,interval}") {
		t.Fatalf("已知键清单里缺 update{auto,interval}: %v", err)
	}
}
```

如果 `internal/config/config_test.go` 的 import 块里缺 `"strings"` / `"time"` / `"path/filepath"` / `"os"`，补齐。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run TestUpdate -v`
Expected: 编译失败——`cfg.Update` 未定义。

- [ ] **Step 3: 加 `UpdateConfig` 类型与字段**

在 `internal/config/config.go` 的 `SyncConfig` 定义之后插入：

```go
// UpdateConfig 描述 agentd 的自动更新行为。
//
// 参数语义：
//   - Auto：是否启用后台自动更新循环。默认 true
//   - Interval：两轮版本检查之间的间隔。默认 6h
//
// 为什么默认 6h 而不是更勤：GitHub 匿名 API 限流 60 次/小时/IP，而版本
// 发布本来就是天级事件——查得更勤不会更早拿到新版，只会更早撞限流。
type UpdateConfig struct {
	Auto     bool
	Interval time.Duration
}
```

在 `Config` 结构体里，`Sync SyncConfig` 那一行之后加：

```go
	// Update 是自动更新配置。Auto 默认 true，Interval 默认 6h。
	Update UpdateConfig
```

- [ ] **Step 4: 加默认值与校验**

`Load` 的默认字面量里，`Sync: SyncConfig{Auto: true},` 之后加：

```go
		Update:   UpdateConfig{Auto: true, Interval: 6 * time.Hour},
```

`validate` 方法里，`approver` 那段之后、`return nil` 之前加：

```go
	// update.interval 只在启用自动更新时校验：没启用的东西写错不该拦启动，
	// 与 approver 那组的处置保持一致。
	//
	// 为什么非正值必须拦：0 会让更新循环的 ticker 每个 tick 都立刻到期，
	// 退化成忙轮询，几秒钟打满 GitHub 匿名限流（60 次/小时），此后所有
	// 版本检查一起失败——症状是「自动更新莫名其妙不工作了」，根因却在
	// 一行配置上。省略该键走默认 6h 是正常用法。
	if c.Update.Auto && c.Update.Interval <= 0 {
		return fmt.Errorf("update.interval 必须为正时长（当前 %s）；省略该键即用默认 6h", c.Update.Interval)
	}
```

- [ ] **Step 5: 更新未知字段的已知键清单**

把 `decodeStrict` 里那条错误消息中的 `sync{auto}` 改为 `sync{auto}/update{auto,interval}`：

```go
		return fmt.Errorf("配置包含未知字段（支持: listen/token/datadir/repo_root/stalltimeout/targets{addr,user,token}/approver{executor,model,timeout,blacklist}/executor{default,model}/terminal{auto}/sync{auto}/update{auto,interval}/env{<agent>: <文件名>}）: %w；旧版 access_key/secret_key 等键已废弃，请删除未知键或升级配置", err)
```

> 注意：顺手把清单里漏掉的 `repo_root` 也补上了——它是既有配置项，清单里一直没列，属同类缺陷。

- [ ] **Step 6: 导出 Save**

`handoff init`（Task 7）需要把改好的配置写盘，而本包已有一个做对了事的 `save`
（MkdirAll 0700 + yaml.Marshal + 0600）。**不要在 cmd 里重写一遍**——权限位写错
一次就是 token 泄露。在 `save` 定义之后加一个导出包装：

```go
// Save 把配置以 YAML 写盘，自动创建父目录，文件权限 0600。
//
// 参数：
//   - path: 目标路径
//   - cfg: 要写入的配置
//
// 返回：
//   - 错误信息：建目录、序列化或写盘失败时返回
//
// 注意：
//   - 0600 是硬要求：配置里含 token，组内可读就等于把令牌给了同机其他账号
func Save(path string, cfg *Config) error { return save(path, cfg) }
```

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./internal/config/ -count=1 -v 2>&1 | tail -20`
Expected: 新增五例全 PASS，既有用例不受影响。

- [ ] **Step 8: 加注释与日志自检**

`internal/config` 的包注释写明「不做网络请求」，且本包只在 Load 的关键节点打日志（首次运行、解析失败、校验失败）——新增的 validate 分支走的正是既有的「配置校验失败」Error 日志（`Load` 里 `log().Error("配置校验失败", ...)`），**无需另加日志**。确认这条路径确实覆盖了新分支即可，不要重复打。

- [ ] **Step 9: 全量回归并提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): 加 update 段（auto/interval）与取值域校验"
```

---

### Task 2: agentd 优雅关停

**Files:**
- Create: `internal/agentd/shutdown.go`
- Create: `internal/agentd/shutdown_test.go`
- Modify: `cmd/agentd.go:159-161`

**Interfaces:**
- Consumes: `*http.Server`（`cmd/agentd.go` 的 `newAgentdHTTPServer` 产出，签名不变）
- Produces:
  - `agentd.ShutdownGrace = 15 * time.Second`
  - `agentd.NewShutdown(log *slog.Logger) *agentd.Shutdown`
  - `(*Shutdown).Trigger(reason string) bool` —— 幂等，首次触发返回 true
  - `(*Shutdown).Reason() string`
  - `(*Shutdown).Serve(srv *http.Server, cleanup func()) error` —— 阻塞直到关停或监听失败
  - **B54.3 将调用 `Trigger("update:vX")` 来换版**，这是本期留出的接口

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/shutdown_test.go`：

```go
// shutdown 的行为测试。
//
// 信号路径不在单测里触发（给测试进程发 SIGTERM 会连带影响 go test 本身），
// 覆盖的是同一条汇合逻辑的另一个入口：Trigger。两者在 Serve 里汇到同一个
// select，测 Trigger 等于测通了那条路。
package agentd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// quietLogger 返回一个丢弃输出的 logger，避免测试刷屏。
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Trigger 必须幂等：第一次返回 true，之后一律 false。
//
// why 这条重要：B54.3 的更新循环与信号处理可能同时触发关停。若两次都"生效"，
// 就会有两个 goroutine 同时调 srv.Shutdown 并各自跑一遍 cleanup——数据库被
// 关两次、日志出现两套自相矛盾的关停原因。
func TestTriggerIsIdempotent(t *testing.T) {
	sd := NewShutdown(quietLogger())
	if !sd.Trigger("update:v0.2.0") {
		t.Fatal("首次 Trigger 应返回 true")
	}
	if sd.Trigger("signal:SIGTERM") {
		t.Fatal("二次 Trigger 应返回 false")
	}
	if got := sd.Reason(); got != "update:v0.2.0" {
		t.Fatalf("Reason=%q，应保留首次触发的原因", got)
	}
}

// Serve 在被 Trigger 后应优雅返回 nil，并且只跑一次 cleanup。
//
// 返回 nil 是**退出码约定**的实现：cobra 的 RunE 返回 nil → 进程 exit 0 →
// systemd Restart=always / launchd KeepAlive 把新版本拉起来。返回非 nil 就是
// exit 1，那条链会被当成崩溃处理。
func TestServeReturnsNilOnGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NewServeMux()}
	sd := NewShutdown(quietLogger())

	var cleanups atomic.Int32
	done := make(chan error, 1)
	go func() { done <- sd.serveWithListener(ln, srv, func() { cleanups.Add(1) }) }()

	// 等服务真的起来再触发，否则测的是"还没开始就停"的空路径
	waitListening(t, ln.Addr().String())
	sd.Trigger("test")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("优雅关停应返回 nil，得到 %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve 未在 10s 内返回")
	}
	if got := cleanups.Load(); got != 1 {
		t.Fatalf("cleanup 应恰好跑一次，跑了 %d 次", got)
	}
}

// 监听失败必须原样返回错误（→ exit 1），不能被当成优雅关停吞掉。
//
// why：端口被占是最常见的启动失败。若这里返回 nil，systemd 会认为服务
// "正常退出"，配合 Restart=always 变成每 3 秒重启一次的静默死循环。
func TestServeReturnsListenError(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:1", Handler: http.NewServeMux()}
	sd := NewShutdown(quietLogger())
	err := sd.Serve(srv, func() {})
	if err == nil {
		t.Fatal("监听 1 端口应失败并返回错误")
	}
	if errors.Is(err, http.ErrServerClosed) {
		t.Fatal("ErrServerClosed 不该外泄，它是优雅关停的正常信号")
	}
}

// waitListening 轮询到端口可连为止，避免用 sleep 猜时间。
func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("端口 %s 在 5s 内未就绪", addr)
}

// 确保未使用的 import 不报错（context 供 shutdown.go 使用，此处占位断言）
var _ = context.Background
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestTrigger|TestServe' -v`
Expected: 编译失败——`NewShutdown` 未定义。

- [ ] **Step 3: 实现 shutdown.go**

创建 `internal/agentd/shutdown.go`：

```go
// shutdown.go —— agentd 的优雅关停协调。
//
// 职责：
//   - 汇合两种停机意图：进程信号（SIGINT/SIGTERM）与进程内触发（Shutdown.Trigger）
//   - 收到意图后停止接受新连接、给在途请求一段收尾时间、跑调用方给的清理闭包
//   - 用返回值表达退出码约定：优雅关停返回 nil（→ exit 0），监听失败原样返回（→ exit 1）
//
// 边界：
//   - 不知道**为什么**要停：信号也好、自更新换版也好，本文件一视同仁
//   - 不关数据库、不释放锁：那些是调用方在 cleanup 闭包里做的事，顺序由调用方定
//   - 不负责重启：把进程拉回来是 systemd / launchd 的职责
//
// 为什么 exit 0 这件事必须写在这里而不是留给调用方：自更新换版的整条链
// （下载 → 替换 → 退出 → 管理器拉起新版）唯一的交接点就是退出码。systemd 的
// Restart=on-failure 在 exit 0 时**不会**重启——那样服务会在换版后无声消失。
// 本期把 deploy 模板改成 Restart=always 正是为此。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// ShutdownGrace 是停止接受新连接后，留给在途请求收尾的时间上限。
//
// 15s 的来由：agentd 最长的同步 handler 是 run 路由（RunCmdTimeout=10min），
// 但那类长跑请求本来就会被 http.Server.Shutdown 等到底或随进程退出而断，
// 拿 10min 当 grace 只会让每次重启都卡十分钟。15s 覆盖的是普通 API 调用
// （dispatch/reply/show 都是亚秒级）的收尾，够用且不拖慢换版。
const ShutdownGrace = 15 * time.Second

// Shutdown 协调 agentd 的停机。
//
// 用法：NewShutdown 之后调 Serve，它会一直阻塞到停机或监听失败。
// 进程内的其它组件（B54.3 的更新循环）调 Trigger 来请求停机。
type Shutdown struct {
	log *slog.Logger

	once   sync.Once
	fired  chan struct{}
	mu     sync.Mutex
	reason string
}

// NewShutdown 构造一个停机协调器。
//
// 参数：
//   - log: 日志入口；停机的每个阶段都会在这里留痕
func NewShutdown(log *slog.Logger) *Shutdown {
	return &Shutdown{log: log, fired: make(chan struct{})}
}

// Trigger 请求停机。
//
// 参数：
//   - reason: 停机原因，形如 "signal:terminated" / "update:v0.2.0"。会进日志
//
// 返回：
//   - true 表示本次调用真的触发了停机；false 表示已经在停了，本次被忽略
//
// 注意：
//   - 幂等。多路来源（信号 + 自更新）可能同时触发，只有第一个算数，
//     否则会有两条关停流程并发跑 cleanup
func (s *Shutdown) Trigger(reason string) bool {
	first := false
	s.once.Do(func() {
		first = true
		s.mu.Lock()
		s.reason = reason
		s.mu.Unlock()
		s.log.Info("收到停机请求", "reason", reason)
		close(s.fired)
	})
	if !first {
		s.log.Debug("停机请求被忽略（已在停机中）", "reason", reason, "first_reason", s.Reason())
	}
	return first
}

// Reason 返回首次触发的停机原因；未触发时返回空串。
func (s *Shutdown) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// Serve 在 srv.Addr 上监听并阻塞，直到停机或监听失败。
//
// 参数：
//   - srv: 已配置好 Addr 与 Handler 的 HTTP 服务
//   - cleanup: 停机时跑一次的清理闭包（关数据库、释放锁等）。**由调用方决定顺序**
//
// 返回：
//   - nil 表示优雅关停完成（进程应 exit 0，管理器据此重新拉起）
//   - 非 nil 表示监听/启动失败（进程应 exit 1）
func (s *Shutdown) Serve(srv *http.Server, cleanup func()) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		// 端口被占是最常见的启动失败，报文里带上地址，别让用户去日志里找
		return fmt.Errorf("监听 %s: %w", srv.Addr, err)
	}
	return s.serveWithListener(ln, srv, cleanup)
}

// serveWithListener 是 Serve 的可测形态：监听器由调用方给。
//
// 拆出来的理由：单测要在一个随机可用端口上跑（net.Listen ":0"），而 Serve
// 从 srv.Addr 里取地址、拿不到实际分配的端口。测试拿着 listener 才能知道
// 该往哪儿探活。
func (s *Shutdown) serveWithListener(ln net.Listener, srv *http.Server, cleanup func()) error {
	// 信号与进程内触发汇到同一个 Shutdown 上：signal.Notify 收到就转成一次 Trigger
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		sig, ok := <-sigCh
		if !ok {
			return
		}
		s.Trigger("signal:" + sig.String())
	}()

	errCh := make(chan error, 1)
	go func() {
		// Serve 正常收到 Shutdown 时返回 ErrServerClosed，那是预期信号不是错误
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		// 还没触发停机，Serve 就自己返回了——这是真失败
		if err != nil {
			s.log.Error("HTTP 服务异常退出", "cause", err)
			return fmt.Errorf("HTTP 服务: %w", err)
		}
		// 极少见：外部直接关了 srv。当作优雅关停处理，但要留痕
		s.log.Warn("HTTP 服务在未触发停机的情况下正常返回")
		cleanup()
		return nil
	case <-s.fired:
	}

	reason := s.Reason()
	s.log.Info("开始优雅关停", "reason", reason, "grace", ShutdownGrace)
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		// 超时不算失败：在途请求没等完也要继续收尾，否则数据库永远关不掉。
		// 但必须留痕——它意味着有请求被硬断了
		s.log.Warn("等待在途请求超时，继续收尾", "cause", err, "grace", ShutdownGrace)
	}
	cleanup()
	s.log.Info("优雅关停完成", "reason", reason)
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestTrigger|TestServe' -count=1 -v`
Expected: 三例全 PASS。

- [ ] **Step 5: 把 agentd.go 接上优雅关停**

`cmd/agentd.go` 里，把 watchdog 那段与最后的 `ListenAndServe` 替换。原文：

```go
		go agentd.RunWatchdog(context.Background(), st, srv.Hub(), cfg.StallTimeout, logger)
		logger.Info("agentd 服务启动", "addr", cfg.Listen, "data_dir", cfg.DataDir, "default_executor", cfg.Executor.Default)
		return newAgentdHTTPServer(cfg.Listen, srv.Handler()).ListenAndServe()
```

替换为：

```go
		// 看门狗随停机一起收：以前挂在 context.Background() 上靠进程退出终止，
		// 有了优雅关停之后必须显式取消，否则关停期间它还在扫任务、写事件，
		// 而数据库正要被关掉
		wdCtx, wdCancel := context.WithCancel(context.Background())
		defer wdCancel()
		go agentd.RunWatchdog(wdCtx, st, srv.Hub(), cfg.StallTimeout, logger)

		logger.Info("agentd 服务启动", "addr", cfg.Listen, "data_dir", cfg.DataDir, "default_executor", cfg.Executor.Default)

		// 优雅关停：收到 SIGINT/SIGTERM（或进程内 Trigger）后停收新连接、
		// 等在途请求、再按序收尾。返回 nil = exit 0，systemd Restart=always /
		// launchd KeepAlive 据此把新进程拉起来——这是自更新换版的交接点。
		//
		// cleanup 的顺序是有讲究的，不要调换：先停看门狗（它会写库），
		// 再关库，最后放锁（放早了别的 agentd 会在库还开着时进来）。
		// store.Close 与 lock.Release 上面已有 defer，这里不重复调用——
		// defer 在 RunE 返回后仍会执行，顺序是 lock.Release 后于 st.Close，
		// 正是我们要的。
		sd := agentd.NewShutdown(logger)
		return sd.Serve(newAgentdHTTPServer(cfg.Listen, srv.Handler()), wdCancel)
```

同时把文件头注释里这一行：

```go
//   - 优雅关停（signal 处理）不在 MVP 范围，进程退出即断开全部连接
```

改为：

```go
//   - 不决定何时停机：信号与进程内触发都汇到 agentd.Shutdown，本文件只接线
```

并在「职责」段末尾加一条：

```go
//   - 经 agentd.Shutdown 提供优雅关停：SIGINT/SIGTERM 停收新连接 → 等在途请求
//     → 停看门狗 → 关库 → 放锁；正常关停 exit 0，供进程管理器据此拉起新版
```

- [ ] **Step 6: 跑测试确认全绿**

Run: `go test ./cmd/ ./internal/agentd/ -count=1`
Expected: PASS。

- [ ] **Step 7: 手工验证信号路径真的通**

单测没有覆盖信号那一支（给测试进程发信号会伤到 `go test` 本身），必须手工验一次。**用隔离实例，绝不碰 7777**：

```bash
D=$(mktemp -d)
go build -o "$D/handoff" .
printf 'listen: 127.0.0.1:7911\ndatadir: %s/data\ntoken: probe-token\n' "$D" > "$D/config.yaml"
HOME="$D" "$D/handoff" agentd --config "$D/config.yaml" &
PID=$!
sleep 3
kill -TERM $PID
wait $PID; echo "退出码=$?"
tail -20 "$D/.handoff/agentd.log" 2>/dev/null || true
rm -rf "$D"
```

**必须分行写，不要压成一条 `&&` 链再挂 `&`**：`&` 绑定的是整条 `&&` 链而不是最后那个 agentd，压成一行会让 `go build` 也跑到后台（`sleep 3` 可能在编译还没完成时就到点）、`$!` 拿到包住整条链的子 shell（`kill` 杀错对象、`wait` 报的是子 shell 的码）、`D` 只在子 shell 里有值（末尾 `rm -rf "$D"` 什么也没清）。三个错叠在一起会得出一个看起来失败、实则根本没测到东西的结论。

Expected: 输出 `退出码=0`，且日志里能看到「收到停机请求 reason=signal:terminated」与「优雅关停完成」两行。若退出码非 0，说明 `Serve` 把 `ErrServerClosed` 当成了错误。

- [ ] **Step 8: 全量回归并提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
git add internal/agentd/shutdown.go internal/agentd/shutdown_test.go cmd/agentd.go
git commit -m "feat(agentd): 加优雅关停，正常停机 exit 0 供管理器拉起"
```

---

### Task 3: `internal/toolchain` 执行者探测

**Files:**
- Create: `internal/toolchain/detect.go`
- Create: `internal/toolchain/detect_test.go`

**Interfaces:**
- Produces:
  - `toolchain.State` 枚举：`StateMissing` / `StateNoCreds` / `StateReady` / `StateAuthUnknown`
  - `toolchain.Result{Name, Path string; State State}`
  - `toolchain.Detect() []Result` —— 固定按 `opencode, claude, grok, codex` 顺序返回四项
  - `(State).String() string` —— 中文短语，供 init 表格直接用
  - `(Result).Ready() bool` —— 仅 `StateReady` 为 true。**`StateAuthUnknown` 返回 false**

- [ ] **Step 1: 写失败的测试**

创建 `internal/toolchain/detect_test.go`：

```go
// toolchain 探测的表驱动测试。
//
// 三个外部依赖（PATH 查找、文件存在、HOME）全部经包级变量注入，
// 因此测试完全不依赖跑测机器上到底装没装这四家。
package toolchain

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// withStubs 替换三个探测缝，返回时自动还原。
func withStubs(t *testing.T, home string, inPath map[string]bool, files map[string]bool) {
	t.Helper()
	oldLook, oldStat, oldHome := lookPath, statFile, userHomeDir
	lookPath = func(name string) (string, error) {
		if inPath[name] {
			return "/usr/local/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	statFile = func(p string) error {
		if files[p] {
			return nil
		}
		return os.ErrNotExist
	}
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { lookPath, statFile, userHomeDir = oldLook, oldStat, oldHome })
}

// byName 从结果里取某一家，找不到直接失败。
func byName(t *testing.T, rs []Result, name string) Result {
	t.Helper()
	for _, r := range rs {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("结果里没有 %s", name)
	return Result{}
}

// 四家都没装：全部 StateMissing，且顺序稳定。
func TestDetectAllMissing(t *testing.T) {
	withStubs(t, "/home/u", nil, nil)
	rs := Detect()
	if len(rs) != 4 {
		t.Fatalf("应返回 4 项，得到 %d", len(rs))
	}
	want := []string{"opencode", "claude", "grok", "codex"}
	for i, w := range want {
		if rs[i].Name != w {
			t.Fatalf("第 %d 项应是 %s，得到 %s", i, w, rs[i].Name)
		}
		if rs[i].State != StateMissing {
			t.Errorf("%s 应为 StateMissing，得到 %v", w, rs[i].State)
		}
	}
}

// 装了但没凭证文件：StateNoCreds（claude 除外，见下一例）。
func TestDetectInstalledWithoutCreds(t *testing.T) {
	withStubs(t, "/home/u", map[string]bool{"opencode": true, "grok": true, "codex": true}, nil)
	rs := Detect()
	for _, n := range []string{"opencode", "grok", "codex"} {
		if got := byName(t, rs, n).State; got != StateNoCreds {
			t.Errorf("%s 应为 StateNoCreds，得到 %v", n, got)
		}
	}
}

// 凭证文件在：StateReady。路径必须逐字符合 spec 里查实的那三条。
func TestDetectReadyUsesVerifiedCredPaths(t *testing.T) {
	home := "/home/u"
	files := map[string]bool{
		filepath.Join(home, ".local/share/opencode/auth.json"): true,
		filepath.Join(home, ".grok/auth.json"):                 true,
		filepath.Join(home, ".codex/auth.json"):                true,
	}
	withStubs(t, home, map[string]bool{"opencode": true, "grok": true, "codex": true}, files)
	rs := Detect()
	for _, n := range []string{"opencode", "grok", "codex"} {
		r := byName(t, rs, n)
		if r.State != StateReady {
			t.Errorf("%s 应为 StateReady，得到 %v", n, r.State)
		}
		if !r.Ready() {
			t.Errorf("%s 的 Ready() 应为 true", n)
		}
	}
}

// claude 装了就只能是 StateAuthUnknown——不许猜成就绪，也不许猜成未就绪。
//
// why：Claude Code 的 OAuth 凭据在 macOS Keychain 里，轻量判据够不着；
// ~/.claude.json 存在但那是配置不是凭证。把它当就绪，用户会以为能派活；
// 当未就绪，用户会去重装一个其实已经能用的东西。两种都是编造。
func TestDetectClaudeIsAlwaysAuthUnknownWhenInstalled(t *testing.T) {
	home := "/home/u"
	// 连 ~/.claude.json 都放上，也不许因此判成就绪
	files := map[string]bool{
		filepath.Join(home, ".claude.json"):            true,
		filepath.Join(home, ".claude/.credentials.json"): true,
	}
	withStubs(t, home, map[string]bool{"claude": true}, files)
	r := byName(t, Detect(), "claude")
	if r.State != StateAuthUnknown {
		t.Fatalf("claude 装了就应是 StateAuthUnknown，得到 %v", r.State)
	}
	if r.Ready() {
		t.Fatal("StateAuthUnknown 的 Ready() 必须为 false——登录态未知不等于就绪")
	}
	if r.Path == "" {
		t.Fatal("装了就该带上可执行文件路径")
	}
}

// 没装的 claude 仍是 StateMissing，不是 StateAuthUnknown。
func TestDetectClaudeMissing(t *testing.T) {
	withStubs(t, "/home/u", nil, nil)
	if got := byName(t, Detect(), "claude").State; got != StateMissing {
		t.Fatalf("没装的 claude 应为 StateMissing，得到 %v", got)
	}
}

// 取不到 HOME 时不能崩，也不能把「查不到凭证」说成「没装」。
func TestDetectHomeUnavailable(t *testing.T) {
	oldHome := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("no home") }
	t.Cleanup(func() { userHomeDir = oldHome })
	withStubsKeepHome(t, map[string]bool{"opencode": true})
	r := byName(t, Detect(), "opencode")
	if r.State != StateNoCreds {
		t.Fatalf("HOME 不可用时装了的执行者应为 StateNoCreds（凭证查不到≠没装），得到 %v", r.State)
	}
}

// withStubsKeepHome 只替换 lookPath/statFile，保留调用方已设的 userHomeDir。
func withStubsKeepHome(t *testing.T, inPath map[string]bool) {
	t.Helper()
	oldLook, oldStat := lookPath, statFile
	lookPath = func(name string) (string, error) {
		if inPath[name] {
			return "/usr/local/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	statFile = func(string) error { return os.ErrNotExist }
	t.Cleanup(func() { lookPath, statFile = oldLook, oldStat })
}

// State 的中文短语是 init 表格直接打印的内容，钉住避免改文案时漏改一处。
func TestStateStrings(t *testing.T) {
	cases := map[State]string{
		StateMissing:     "没装",
		StateNoCreds:     "已安装，未登录",
		StateReady:       "就绪",
		StateAuthUnknown: "已安装，登录态未知",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("State(%d).String()=%q，期望 %q", s, got, want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/toolchain/ -v`
Expected: 包不存在，编译失败。

- [ ] **Step 3: 实现 detect.go**

创建 `internal/toolchain/detect.go`：

```go
// Package toolchain 探测本机装了哪些 executor、各自处于什么状态。
//
// 职责：
//   - 对 opencode / claude / grok / codex 四家，各查「可执行文件在不在 PATH」
//     与「凭证文件在不在」，归一成一个三态（claude 两态）
//
// 边界：
//   - **只探测，不决策**：不排序偏好、不写配置、不阻止任何选择。那是 cmd/init.go 的事
//   - **绝不发起真实模型调用**。README 里「claude -p "hi" 能出结果即视为就绪」是给人
//     看的验证方法，不是这里能用的判据——那是一次真实付费调用，几秒到几十秒且要联网
//   - 不打日志：本包是一组纯取值函数，无 I/O 副作用（只读文件是否存在），
//     在这里打日志只会给 init 的输出制造噪音；探测结果由调用方成表打印
package toolchain

import (
	"os"
	"os/exec"
	"path/filepath"
)

// 三个探测缝，生产实现即标准库；测试替换它们，从而不依赖跑测机器的真实环境。
var (
	lookPath    = exec.LookPath
	statFile    = func(p string) error { _, err := os.Stat(p); return err }
	userHomeDir = os.UserHomeDir
)

// State 是一家 executor 的可用状态。
type State int

const (
	// StateMissing：可执行文件不在 PATH 里。
	StateMissing State = iota
	// StateNoCreds：装了，但找不到凭证文件。
	StateNoCreds
	// StateReady：装了且凭证文件在。
	StateReady
	// StateAuthUnknown：装了，但本机没有可靠的轻量凭证判据——只有 claude 会是这个状态。
	//
	// 为什么单独一态而不是并进 StateNoCreds：两者要让用户做的事完全相反。
	// NoCreds 是「去登录」，AuthUnknown 是「大概率能用，自己心里有数」。
	// 合并等于把「不知道」说成「没登录」，那是编造。
	StateAuthUnknown
)

// String 返回给人看的中文短语（init 的表格直接打印它）。
func (s State) String() string {
	switch s {
	case StateMissing:
		return "没装"
	case StateNoCreds:
		return "已安装，未登录"
	case StateReady:
		return "就绪"
	case StateAuthUnknown:
		return "已安装，登录态未知"
	}
	return "未知"
}

// Result 是一家 executor 的探测结果。
type Result struct {
	// Name 是 executor 名，与 dispatch --executor 用的名字一致。
	Name string
	// Path 是可执行文件路径；StateMissing 时为空。
	Path string
	// State 是探测出的状态。
	State State
}

// Ready 表示「可以放心把它设成缺省执行者」。
//
// 注意：StateAuthUnknown 返回 **false**。它的语义是「不知道」，
// 把不知道当成就绪，就是替用户做了一个没有依据的判断。
func (r Result) Ready() bool { return r.State == StateReady }

// credRelPath 是各家凭证文件相对 HOME 的路径。
//
// 这三条在 devbox 上逐一查实过（2026-08-11），不是猜的。claude 不在表里——
// 它的 OAuth 凭据存在 macOS Keychain 里，没有可靠的文件判据（~/.claude.json
// 存在但那是配置不是凭证，拿它当登录判据会把没登录的机器报成就绪）。
var credRelPath = map[string]string{
	"opencode": ".local/share/opencode/auth.json",
	"grok":     ".grok/auth.json",
	"codex":    ".codex/auth.json",
}

// order 固定探测与返回顺序，让 init 的表格每次长得一样。
var order = []string{"opencode", "claude", "grok", "codex"}

// Detect 探测四家 executor 的状态。
//
// 返回：
//   - 固定四项，顺序恒为 opencode / claude / grok / codex
//
// 注意：
//   - 取不到 HOME 时，装了的执行者一律报 StateNoCreds 而不是 StateMissing——
//     「凭证查不到」和「没装」是两件事，混为一谈会让用户去重装一个已经装好的东西
func Detect() []Result {
	home, homeErr := userHomeDir()
	out := make([]Result, 0, len(order))
	for _, name := range order {
		r := Result{Name: name}
		p, err := lookPath(name)
		if err != nil {
			r.State = StateMissing
			out = append(out, r)
			continue
		}
		r.Path = p
		if name == "claude" {
			// claude 没有可靠的轻量判据，如实报「不知道」
			r.State = StateAuthUnknown
			out = append(out, r)
			continue
		}
		rel, ok := credRelPath[name]
		if !ok || homeErr != nil {
			r.State = StateNoCreds
			out = append(out, r)
			continue
		}
		if statFile(filepath.Join(home, rel)) == nil {
			r.State = StateReady
		} else {
			r.State = StateNoCreds
		}
		out = append(out, r)
	}
	return out
}

// FirstReady 返回第一个就绪的 executor 名；一个都没有时返回空串。
//
// 供 init 挑 executor.default 的默认值用。**不把 StateAuthUnknown 算进来**，
// 理由同 Result.Ready。
func FirstReady(rs []Result) string {
	for _, r := range rs {
		if r.Ready() {
			return r.Name
		}
	}
	return ""
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/toolchain/ -count=1 -v`
Expected: 七例全 PASS。

- [ ] **Step 5: 全量回归并提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
git add internal/toolchain/
git commit -m "feat(toolchain): 加四家 executor 的三态探测（claude 只报登录态未知）"
```

---

### Task 4: `internal/service` 接口与 launchd 实现

**Files:**
- Create: `internal/service/service.go`
- Create: `internal/service/launchd.go`
- Create: `internal/service/launchd_test.go`

**Interfaces:**
- Produces:
  - `service.Spec{BinPath, ConfigPath, LogPath string}`
  - `service.Status{Installed, Running bool; Detail string}`
  - `service.Manager` 接口：`Install(Spec) error` / `Uninstall() error` / `Status() (Status, error)` / `Kind() string` / `UnitPath() (string, error)`
  - `service.New(log *slog.Logger) (Manager, error)` —— 按 `runtime.GOOS` 分发
  - `service.LaunchdLabel = "dev.gosuper.handoff.agentd"`
  - `launchdManager` 内部两个缝：`run func(name string, args ...string) ([]byte, error)`、`writeFile`/`removeFile`

- [ ] **Step 1: 写失败的测试**

创建 `internal/service/launchd_test.go`：

```go
// launchd 实现的测试：plist 内容与 launchctl 调用序列都在这里钉住。
//
// 全部经缝注入，不真的调 launchctl、不真的写 ~/Library——测试跑完机器上
// 不会多出任何服务。
package service

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newTestLaunchd 造一个全缝替换的 launchd manager，并返回记录调用的切片指针。
func newTestLaunchd(t *testing.T, runErr error) (*launchdManager, *[]string, *map[string][]byte) {
	t.Helper()
	calls := []string{}
	written := map[string][]byte{}
	m := &launchdManager{
		log:      testLogger(),
		homeDir:  func() (string, error) { return "/home/u", nil },
		plistDir: "/home/u/Library/LaunchAgents",
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return []byte("ok"), runErr
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
	}
	return m, &calls, &written
}

// plist 的内容是这条路上最容易写错又最难发现的东西，逐项钉住。
func TestLaunchdPlistContent(t *testing.T) {
	m, _, written := newTestLaunchd(t, nil)
	err := m.Install(Spec{BinPath: "/opt/bin/handoff", ConfigPath: "/home/u/.handoff/config.yaml", LogPath: "/home/u/.handoff/agentd.log"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	body := string((*written)["/home/u/Library/LaunchAgents/dev.gosuper.handoff.agentd.plist"])
	if body == "" {
		t.Fatal("plist 没被写出来")
	}
	for _, want := range []string{
		"<string>dev.gosuper.handoff.agentd</string>",
		"<string>/opt/bin/handoff</string>",
		"<string>agentd</string>",
		"<key>KeepAlive</key>",
		"<key>RunAtLoad</key>",
		"/home/u/.handoff/agentd.log",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plist 缺少 %q:\n%s", want, body)
		}
	}
	// 两条禁止项，各有各的理由，都不能出现
	if strings.Contains(body, "AbandonProcessGroup") {
		t.Error("plist 不得含 AbandonProcessGroup：P1 探针已实测 setsid 的执行者本就活得过重启（spec §7.1），写上它等于给一条被证伪的假设留痕迹")
	}
	if strings.Contains(body, "--executor") {
		t.Error("plist 不得含 --executor：它只覆盖 cfg.Executor.Default，写死在单元里会让「改配置不生效」变成隐蔽坑（spec D5）")
	}
}

// 安装要按 bootout（清旧）→ 写盘 → bootstrap（加载）→ print（复核）的次序走。
//
// why 要复核：写盘 + bootstrap 成功不代表进程真起来了（二进制路径错、
// 端口被占都会让它起来即死）。不复核就报「安装成功」，用户会去查一个
// 根本不存在的服务。
func TestLaunchdInstallSequence(t *testing.T) {
	m, calls, _ := newTestLaunchd(t, nil)
	if err := m.Install(Spec{BinPath: "/opt/bin/handoff", ConfigPath: "/c.yaml", LogPath: "/l.log"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	for _, want := range []string{"bootout", "bootstrap", "print"} {
		if !strings.Contains(joined, want) {
			t.Errorf("调用序列缺 %q: %s", want, joined)
		}
	}
	if i, j := strings.Index(joined, "bootstrap"), strings.Index(joined, "print"); i > j {
		t.Errorf("bootstrap 必须先于 print（复核）: %s", joined)
	}
}

// bootstrap 失败必须回滚（删掉刚写的 plist），并把真因带出来。
//
// why 回滚：留下一个加载不了的 plist，下次登录 launchd 还会尝试加载它并
// 反复失败，用户却以为自己从没装过这个服务。
func TestLaunchdInstallRollsBackOnFailure(t *testing.T) {
	calls := []string{}
	written := map[string][]byte{}
	m := &launchdManager{
		log:      testLogger(),
		homeDir:  func() (string, error) { return "/home/u", nil },
		plistDir: "/home/u/Library/LaunchAgents",
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, strings.Join(args, " "))
			if len(args) > 0 && args[0] == "bootstrap" {
				return []byte("Load failed: 5: Input/output error"), errors.New("exit status 5")
			}
			return []byte("ok"), nil
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
	}
	err := m.Install(Spec{BinPath: "/opt/bin/handoff", ConfigPath: "/c.yaml", LogPath: "/l.log"})
	if err == nil {
		t.Fatal("bootstrap 失败时 Install 应报错")
	}
	if !strings.Contains(err.Error(), "Load failed") {
		t.Errorf("报错应带上 launchctl 的原文（真因），得到: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("失败后应回滚删掉 plist，却还剩 %d 个文件", len(written))
	}
}

// Status 在 print 成功时报已安装且在跑。
func TestLaunchdStatusRunning(t *testing.T) {
	m, _, _ := newTestLaunchd(t, nil)
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running {
		t.Fatalf("print 成功时应报已安装且在跑，得到 %+v", st)
	}
}

// print 失败（job 未注册）时报未安装，且**不返回错误**——「没装」是一个
// 正常答案，不是查询失败。
func TestLaunchdStatusNotInstalled(t *testing.T) {
	m, _, _ := newTestLaunchd(t, errors.New("exit status 113"))
	st, err := m.Status()
	if err != nil {
		t.Fatalf("未注册不该当成查询失败: %v", err)
	}
	if st.Installed || st.Running {
		t.Fatalf("应报未安装，得到 %+v", st)
	}
}

func TestLaunchdKind(t *testing.T) {
	m, _, _ := newTestLaunchd(t, nil)
	if m.Kind() != "launchd" {
		t.Fatalf("Kind()=%q", m.Kind())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -v`
Expected: 包不存在，编译失败。

- [ ] **Step 3: 写 service.go（接口与分发）**

创建 `internal/service/service.go`：

```go
// Package service 把 agentd 交给本平台的进程管理器托管。
//
// 职责：
//   - 生成服务单元（macOS 的 launchd plist / Linux 的 systemd unit）
//   - 安装、卸载、查询状态；安装后**复核服务真的起来了**
//
// 边界：
//   - 不下载、不判断版本、不读 handoff 的配置文件：要什么路径由调用方在 Spec 里给全
//   - 不负责重启策略之外的进程管理：拉起、崩溃重启都是管理器的事
//   - 不支持 Windows：agentd 依赖的进程承载层 Windows 实现尚未完成（backlog B37）
package service

import (
	"fmt"
	"log/slog"
	"runtime"
)

// LaunchdLabel 是 macOS 上的 job 标签，同时也是 plist 的文件名主干。
const LaunchdLabel = "dev.gosuper.handoff.agentd"

// SystemdUnit 是 Linux 上的 unit 文件名。
const SystemdUnit = "handoff-agentd.service"

// Spec 描述「要托管的是哪个 agentd」。
//
// 字段说明：
//   - BinPath: handoff 可执行文件的**绝对路径**，调用方须先做 EvalSymlinks，
//     否则服务会指向一个 symlink，升级换掉链接目标后单元还指着旧的
//   - ConfigPath: 传给 agentd 的 --config
//   - LogPath: 管理器把 stdout/stderr 重定向到哪
type Spec struct {
	BinPath    string
	ConfigPath string
	LogPath    string
}

// Status 是服务的当前状态。
//
// Installed 与 Running 是两件事：单元装了但没跑（崩溃循环、被手动 stop）
// 是一个真实且常见的状态，合并成一个布尔会让用户看不出区别。
type Status struct {
	Installed bool
	Running   bool
	// Detail 是管理器原文的摘要，供排障。查不到时为空
	Detail string
}

// Manager 是平台无关的服务托管接口。
type Manager interface {
	// Install 生成单元、写盘、加载、启动，并复核真的起来了。失败时回滚。
	Install(spec Spec) error
	// Uninstall 停止并移除单元。单元本来就不在时返回 nil（幂等）。
	Uninstall() error
	// Status 查询状态。「没装」是正常答案，不是错误。
	Status() (Status, error)
	// Kind 返回管理器种类："launchd" / "systemd"。
	Kind() string
	// UnitPath 返回单元文件的落点路径。
	UnitPath() (string, error)
}

// New 按当前平台返回对应的 Manager。
//
// 参数：
//   - log: 日志入口
//
// 返回：
//   - 平台对应的 Manager
//   - 不支持的平台返回错误，报文里说清为什么不支持而不是只说「不支持」
func New(log *slog.Logger) (Manager, error) {
	switch runtime.GOOS {
	case "darwin":
		return newLaunchd(log), nil
	case "linux":
		return newSystemd(log), nil
	case "windows":
		return nil, fmt.Errorf("暂不支持 Windows：agentd 依赖的进程承载层 Windows 实现尚未完成（backlog B37），托管起来也跑不了任务")
	default:
		return nil, fmt.Errorf("不支持的平台 %s（仅 darwin/linux）", runtime.GOOS)
	}
}
```

- [ ] **Step 4: 写 launchd.go**

创建 `internal/service/launchd.go`：

```go
// launchd.go —— macOS 侧的服务托管实现。
//
// 边界：
//   - plist 里**不写 AbandonProcessGroup**。P1 探针（spec §7.1）实测：以 setsid
//     拉起的执行者能活过 launchctl kickstart -k 与 bootout，本就不需要它。
//     写上它等于给一条已被实测证伪的假设留下痕迹，下一个人会以为它是必需的
//   - plist 的 ProgramArguments 里**不带 --executor**（spec D5）
package service

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// launchdManager 是 macOS 实现。四个字段是测试缝。
type launchdManager struct {
	log      *slog.Logger
	homeDir  func() (string, error)
	plistDir string
	run      func(name string, args ...string) ([]byte, error)
	writeFile func(path string, data []byte, perm uint32) error
	remove   func(path string) error
}

// newLaunchd 构造生产用的 launchd manager。
func newLaunchd(log *slog.Logger) *launchdManager {
	m := &launchdManager{
		log:     log,
		homeDir: os.UserHomeDir,
		run: func(name string, args ...string) ([]byte, error) {
			// CombinedOutput：launchctl 的真因大多写在 stderr 上，只取 stdout
			// 会得到一个空字符串加一个 "exit status 5"，等于没有诊断信息
			return exec.Command(name, args...).CombinedOutput()
		},
		writeFile: func(p string, b []byte, perm uint32) error { return os.WriteFile(p, b, os.FileMode(perm)) },
		remove:    os.Remove,
	}
	if h, err := m.homeDir(); err == nil {
		m.plistDir = filepath.Join(h, "Library", "LaunchAgents")
	}
	return m
}

func (m *launchdManager) Kind() string { return "launchd" }

// UnitPath 返回 plist 的落点。
func (m *launchdManager) UnitPath() (string, error) {
	if m.plistDir == "" {
		return "", fmt.Errorf("取不到用户主目录，无法定位 LaunchAgents 目录")
	}
	return filepath.Join(m.plistDir, LaunchdLabel+".plist"), nil
}

// domain 返回 launchctl 的目标域，形如 gui/501。
func (m *launchdManager) domain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

// target 返回 launchctl 的服务目标，形如 gui/501/dev.gosuper.handoff.agentd。
func (m *launchdManager) target() string { return m.domain() + "/" + LaunchdLabel }

// plistBody 渲染 plist 内容。
//
// 参数：
//   - spec: 要托管的 agentd 描述
//
// 返回：
//   - plist 全文
//
// 注意：
//   - KeepAlive=true 对应 systemd 的 Restart=always：**exit 0 也会被重新拉起**，
//     这正是自更新换版所依赖的（P1 实测确认，见 spec §7.1）
//   - launchd 对重生有约 10 秒节流，换版期间会有约 10 秒的服务空窗
func (m *launchdManager) plistBody(spec Spec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString("  <key>Label</key>\n  <string>" + LaunchdLabel + "</string>\n")
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	b.WriteString("    <string>" + spec.BinPath + "</string>\n")
	b.WriteString("    <string>agentd</string>\n")
	if spec.ConfigPath != "" {
		b.WriteString("    <string>--config</string>\n")
		b.WriteString("    <string>" + spec.ConfigPath + "</string>\n")
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	b.WriteString("  <key>KeepAlive</key>\n  <true/>\n")
	if spec.LogPath != "" {
		b.WriteString("  <key>StandardOutPath</key>\n  <string>" + spec.LogPath + "</string>\n")
		b.WriteString("  <key>StandardErrorPath</key>\n  <string>" + spec.LogPath + "</string>\n")
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// Install 写 plist 并加载，最后复核服务真的注册上了。
func (m *launchdManager) Install(spec Spec) error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	m.log.Info("安装 launchd 服务", "label", LaunchdLabel, "plist", path, "bin", spec.BinPath)

	// 先清旧：同名 job 还注册着时 bootstrap 会直接失败（"service already loaded"）。
	// 忽略这一步的错误——绝大多数情况下它本来就没装，报错是正常的
	if out, err := m.run("launchctl", "bootout", m.target()); err != nil {
		m.log.Debug("bootout 旧 job（未装时报错属正常）", "output", strings.TrimSpace(string(out)))
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建 %s: %w", filepath.Dir(path), err)
	}
	if err := m.writeFile(path, []byte(m.plistBody(spec)), 0o644); err != nil {
		return fmt.Errorf("写 plist %s: %w", path, err)
	}

	if out, err := m.run("launchctl", "bootstrap", m.domain(), path); err != nil {
		// 回滚：留下一个加载不了的 plist，下次登录 launchd 还会反复尝试加载它，
		// 而用户以为自己从没装过。报文带上 launchctl 原文——那才是真因
		if rmErr := m.remove(path); rmErr != nil {
			m.log.Error("回滚删除 plist 失败", "path", path, "cause", rmErr)
		}
		m.log.Error("加载 launchd 服务失败，已回滚", "cause", err, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("加载 launchd 服务失败: %s（%w）", strings.TrimSpace(string(out)), err)
	}

	// 复核：bootstrap 成功不等于进程起来了（二进制路径错、端口被占都会
	// 起来即死）。不复核就报「安装成功」，用户会去查一个不存在的服务
	if out, err := m.run("launchctl", "print", m.target()); err != nil {
		m.log.Error("服务已加载但复核失败", "cause", err, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("服务已加载但复核不到（可能起来即退出，检查 %s）: %w", spec.LogPath, err)
	}
	m.log.Info("launchd 服务安装完成", "label", LaunchdLabel)
	return nil
}

// Uninstall 卸载并删除 plist。本来就没装时返回 nil。
func (m *launchdManager) Uninstall() error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	m.log.Info("卸载 launchd 服务", "label", LaunchdLabel)
	if out, err := m.run("launchctl", "bootout", m.target()); err != nil {
		// 没装时 bootout 必然报错，这是正常的，不该让 uninstall 失败
		m.log.Debug("bootout 报错（未装时属正常）", "output", strings.TrimSpace(string(out)))
	}
	if err := m.remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 plist %s: %w", path, err)
	}
	m.log.Info("launchd 服务已卸载", "label", LaunchdLabel)
	return nil
}

// Status 查询 job 是否注册且在跑。
func (m *launchdManager) Status() (Status, error) {
	out, err := m.run("launchctl", "print", m.target())
	if err != nil {
		// 没注册时 launchctl print 退 113。这是一个正常答案，不是查询失败
		return Status{}, nil
	}
	s := Status{Installed: true, Running: true, Detail: firstLine(string(out))}
	// print 输出里带 "state = running" 才算真在跑；只注册没跑也是常见状态
	if strings.Contains(string(out), "state = not running") {
		s.Running = false
	}
	return s, nil
}

// firstLine 取多行输出的第一行，用作 Detail 摘要。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/service/ -count=1 -v`
Expected: 六例全 PASS。

> 若 `TestLaunchdStatusRunning` 因为 `newTestLaunchd` 的 stub 返回 `"ok"`（不含 `state = not running`）而通过——那正是预期。

- [ ] **Step 6: 提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
git add internal/service/service.go internal/service/launchd.go internal/service/launchd_test.go
git commit -m "feat(service): 加托管接口与 launchd 实现（安装后复核、失败回滚）"
```

---

### Task 5: `internal/service` systemd 实现

**Files:**
- Create: `internal/service/systemd.go`
- Create: `internal/service/systemd_test.go`

**Interfaces:**
- Consumes: Task 4 的 `Spec` / `Status` / `Manager` / `SystemdUnit`
- Produces: `newSystemd(log *slog.Logger) *systemdManager`（Task 4 的 `New` 已经引用它，本任务前 `internal/service` 在 Linux 上编译不过——这是预期）

> **说明**：本任务的实现无法在 macOS 上做真机验证（两台可用机器都是 macOS，原定的 Linux 临时机已回收，见 spec §10）。因此这里的测试**全部经命令缝注入**，覆盖 unit 内容与调用序列；真机验证记为待补。**不要因为无法真机验证就把测试写松**——恰恰相反，这里的单测是 Linux 侧唯一的防线。

- [ ] **Step 1: 写失败的测试**

创建 `internal/service/systemd_test.go`：

```go
// systemd 实现的测试。全部经缝注入，不真的调 systemctl、不真的写 /etc。
//
// 这些断言在 Linux 侧是唯一的防线：本仓库暂无 Linux 机器可做真机验证
//（spec §10），unit 内容写错在 macOS 上不会有任何症状。
package service

import (
	"errors"
	"strings"
	"testing"
)

func newTestSystemd(t *testing.T, runErr error) (*systemdManager, *[]string, *map[string][]byte) {
	t.Helper()
	calls := []string{}
	written := map[string][]byte{}
	m := &systemdManager{
		log:      testLogger(),
		unitDir:  "/etc/systemd/system",
		user:     "alice",
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return []byte("ok"), runErr
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
	}
	return m, &calls, &written
}

// unit 内容逐项钉住。两条硬要求各有各的理由，写错都不会在编译期暴露。
func TestSystemdUnitContent(t *testing.T) {
	m, _, written := newTestSystemd(t, nil)
	if err := m.Install(Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/home/alice/.handoff/config.yaml"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	body := string((*written)["/etc/systemd/system/handoff-agentd.service"])
	if body == "" {
		t.Fatal("unit 没被写出来")
	}
	if !strings.Contains(body, "KillMode=process") {
		t.Error("unit 必须含 KillMode=process：setsid 脱离了会话与进程组但改不了 cgroup 归属，默认的 control-group 会在重启时把执行者一并杀掉（B36 硬要求）")
	}
	if !strings.Contains(body, "Restart=always") {
		t.Error("unit 必须是 Restart=always 而非 on-failure：自更新换版靠 exit 0 交接，on-failure 在 exit 0 时不重启，服务会在换版后无声消失")
	}
	if strings.Contains(body, "Restart=on-failure") {
		t.Error("unit 不得含 Restart=on-failure")
	}
	if strings.Contains(body, "--executor") {
		t.Error("ExecStart 不得带 --executor（spec D5）")
	}
	if !strings.Contains(body, "User=alice") {
		t.Errorf("unit 应写实际用户名而不是占位符:\n%s", body)
	}
	if strings.Contains(body, "CHANGEME") || strings.Contains(body, "%i") {
		t.Error("unit 不得残留占位符：User= 空值会被 systemd 重置为 root，服务会以 root 静默跑起来")
	}
	if !strings.Contains(body, "/usr/local/bin/handoff agentd") {
		t.Errorf("ExecStart 路径不对:\n%s", body)
	}
}

// 安装序列：写盘 → daemon-reload → enable --now → is-active（复核）。
func TestSystemdInstallSequence(t *testing.T) {
	m, calls, _ := newTestSystemd(t, nil)
	if err := m.Install(Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/c.yaml"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	joined := strings.Join(*calls, " | ")
	for _, want := range []string{"daemon-reload", "enable", "is-active"} {
		if !strings.Contains(joined, want) {
			t.Errorf("调用序列缺 %q: %s", want, joined)
		}
	}
}

// 写 /etc 没权限时必须明确说「需要 sudo」，而不是把 permission denied 扁平抛出。
//
// why（B45 的教训）：真因只落在日志里等于没有。用户看到的是一句
// "open /etc/systemd/system/...: permission denied"，他不知道该 sudo 重跑。
func TestSystemdInstallSaysSudoOnPermissionError(t *testing.T) {
	m, _, _ := newTestSystemd(t, nil)
	m.writeFile = func(string, []byte, uint32) error { return errors.New("permission denied") }
	err := m.Install(Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/c.yaml"})
	if err == nil {
		t.Fatal("写盘失败时应报错")
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("报错必须提示需要 sudo，得到: %v", err)
	}
}

// enable 失败要回滚删掉 unit，并带出 systemctl 原文。
func TestSystemdInstallRollsBackOnFailure(t *testing.T) {
	calls := []string{}
	written := map[string][]byte{}
	m := &systemdManager{
		log:     testLogger(),
		unitDir: "/etc/systemd/system",
		user:    "alice",
		run: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, strings.Join(args, " "))
			if len(args) > 0 && args[0] == "enable" {
				return []byte("Failed to enable unit: Unit file is masked."), errors.New("exit status 1")
			}
			return []byte("ok"), nil
		},
		writeFile: func(p string, b []byte, _ uint32) error { written[p] = b; return nil },
		remove:    func(p string) error { delete(written, p); return nil },
	}
	err := m.Install(Spec{BinPath: "/usr/local/bin/handoff", ConfigPath: "/c.yaml"})
	if err == nil {
		t.Fatal("enable 失败时应报错")
	}
	if !strings.Contains(err.Error(), "masked") {
		t.Errorf("报错应带 systemctl 原文，得到: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("失败后应回滚，却还剩 %d 个文件", len(written))
	}
}

// is-active 成功 = 在跑；失败 = 装了但没跑，且不算查询错误。
func TestSystemdStatus(t *testing.T) {
	m, _, _ := newTestSystemd(t, nil)
	st, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Running {
		t.Fatalf("is-active 成功时应报在跑，得到 %+v", st)
	}

	m2, _, _ := newTestSystemd(t, errors.New("exit status 3"))
	st2, err := m2.Status()
	if err != nil {
		t.Fatalf("未激活不该当成查询失败: %v", err)
	}
	if st2.Running {
		t.Fatalf("is-active 失败时不该报在跑，得到 %+v", st2)
	}
}

func TestSystemdKind(t *testing.T) {
	m, _, _ := newTestSystemd(t, nil)
	if m.Kind() != "systemd" {
		t.Fatalf("Kind()=%q", m.Kind())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run TestSystemd -v`
Expected: 编译失败——`systemdManager` 未定义。

- [ ] **Step 3: 实现 systemd.go**

创建 `internal/service/systemd.go`：

```go
// systemd.go —— Linux 侧的服务托管实现。
//
// 边界：
//   - 写 /etc/systemd/system 需要 root。**无权限时必须明确提示「需要 sudo」**
//     而不是把 permission denied 扁平抛出（B45 的教训：真因只落在日志里等于没有）
//   - unit 里 KillMode=process 与 Restart=always 是硬要求，理由见各自注释
//
// 未真机验证：本仓库暂无 Linux 机器（spec §10）。本文件的正确性目前完全由
// systemd_test.go 的内容断言守着，改动时务必同步维护那些断言。
package service

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// systemdManager 是 Linux 实现。四个字段是测试缝。
type systemdManager struct {
	log       *slog.Logger
	unitDir   string
	user      string
	run       func(name string, args ...string) ([]byte, error)
	writeFile func(path string, data []byte, perm uint32) error
	remove    func(path string) error
}

// newSystemd 构造生产用的 systemd manager。
func newSystemd(log *slog.Logger) *systemdManager {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return &systemdManager{
		log:     log,
		unitDir: "/etc/systemd/system",
		user:    name,
		run: func(n string, args ...string) ([]byte, error) {
			return exec.Command(n, args...).CombinedOutput()
		},
		writeFile: func(p string, b []byte, perm uint32) error { return os.WriteFile(p, b, os.FileMode(perm)) },
		remove:    os.Remove,
	}
}

func (m *systemdManager) Kind() string { return "systemd" }

// UnitPath 返回 unit 文件落点。
func (m *systemdManager) UnitPath() (string, error) {
	return filepath.Join(m.unitDir, SystemdUnit), nil
}

// unitBody 渲染 unit 内容。
//
// 两条硬要求，改之前先读懂为什么：
//
//   - KillMode=process：执行者由 agentd 经 shim 以 setsid 拉起，setsid 脱离了
//     会话与进程组但**改不了 cgroup 归属**（cgroup 由 fork 继承）。systemd 默认的
//     KillMode=control-group 会在 stop/restart 时向整个 cgroup 发信号，执行者
//     一并被杀，正在跑的任务全部中断（B36）
//   - Restart=always：自更新换版靠「agentd 自己 exit 0 → 管理器拉起新版」交接。
//     on-failure 在 exit 0 时**不重启**——换完版服务就此消失，而且没有任何信号
//     告诉任何人。这是 D9 的直接结论
//
// User 必须写字面用户名。写 %i 会被当成模板 unit 的实例名占位符，在非模板
// unit 里解析为空串，而 `User=` 空值会被 systemd 重置为 root——服务会以 root
// 静默跑起来，不报任何错。
func (m *systemdManager) unitBody(spec Spec) string {
	exec := spec.BinPath + " agentd"
	if spec.ConfigPath != "" {
		exec += " --config " + spec.ConfigPath
	}
	var b strings.Builder
	b.WriteString("# 由 handoff service install 生成，勿手改——重装会覆盖。\n")
	b.WriteString("[Unit]\n")
	b.WriteString("Description=handoff agentd (executor host)\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString("User=" + m.user + "\n")
	b.WriteString("ExecStart=" + exec + "\n")
	// exit 0 也要拉起：自更新换版的交接点就是退出码（D9）
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=3\n\n")
	b.WriteString("# KillMode=process 是硬要求：setsid 改不了 cgroup 归属，\n")
	b.WriteString("# 默认的 control-group 会在重启时把执行者一并杀掉（B36）。\n")
	b.WriteString("KillMode=process\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=multi-user.target\n")
	return b.String()
}

// Install 写 unit、reload、enable --now，并复核真的活着。
func (m *systemdManager) Install(spec Spec) error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	if m.user == "" {
		// 空 User 会被 systemd 重置为 root，服务以 root 静默跑起来。宁可拦住
		return fmt.Errorf("取不到当前用户名，无法生成 unit（User= 留空会让服务以 root 运行）")
	}
	m.log.Info("安装 systemd 服务", "unit", SystemdUnit, "path", path, "bin", spec.BinPath, "user", m.user)

	if err := m.writeFile(path, []byte(m.unitBody(spec)), 0o644); err != nil {
		// B45 的教训：扁平抛 permission denied，用户不知道该 sudo 重跑
		if os.IsPermission(err) || strings.Contains(err.Error(), "permission denied") {
			return fmt.Errorf("写 %s 需要 root 权限，请用 sudo 重跑：sudo handoff service install（原因: %w）", path, err)
		}
		return fmt.Errorf("写 unit %s: %w", path, err)
	}

	if out, err := m.run("systemctl", "daemon-reload"); err != nil {
		m.rollback(path)
		return fmt.Errorf("systemctl daemon-reload 失败: %s（%w）", strings.TrimSpace(string(out)), err)
	}
	if out, err := m.run("systemctl", "enable", "--now", SystemdUnit); err != nil {
		m.rollback(path)
		m.log.Error("启用 systemd 服务失败，已回滚", "cause", err, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("启用 systemd 服务失败: %s（%w）", strings.TrimSpace(string(out)), err)
	}

	// 复核：enable --now 返回 0 不代表进程还活着（起来即崩同样返回 0）
	if out, err := m.run("systemctl", "is-active", SystemdUnit); err != nil {
		m.log.Error("服务已启用但复核不到活跃状态", "cause", err, "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("服务已启用但未处于活跃状态（可能起来即退出，查 journalctl -u %s）: %w", SystemdUnit, err)
	}
	m.log.Info("systemd 服务安装完成", "unit", SystemdUnit)
	return nil
}

// rollback 删掉刚写的 unit 并 reload，避免留下一个装不上又卸不掉的残件。
func (m *systemdManager) rollback(path string) {
	if err := m.remove(path); err != nil && !os.IsNotExist(err) {
		m.log.Error("回滚删除 unit 失败", "path", path, "cause", err)
		return
	}
	if _, err := m.run("systemctl", "daemon-reload"); err != nil {
		m.log.Warn("回滚后 daemon-reload 失败", "cause", err)
	}
}

// Uninstall 停用并删除 unit。本来就没装时返回 nil。
func (m *systemdManager) Uninstall() error {
	path, err := m.UnitPath()
	if err != nil {
		return err
	}
	m.log.Info("卸载 systemd 服务", "unit", SystemdUnit)
	if out, err := m.run("systemctl", "disable", "--now", SystemdUnit); err != nil {
		// 没装时 disable 必然报错，正常
		m.log.Debug("disable 报错（未装时属正常）", "output", strings.TrimSpace(string(out)))
	}
	if err := m.remove(path); err != nil && !os.IsNotExist(err) {
		if os.IsPermission(err) || strings.Contains(err.Error(), "permission denied") {
			return fmt.Errorf("删除 %s 需要 root 权限，请用 sudo 重跑（原因: %w）", path, err)
		}
		return fmt.Errorf("删除 unit %s: %w", path, err)
	}
	if _, err := m.run("systemctl", "daemon-reload"); err != nil {
		m.log.Warn("卸载后 daemon-reload 失败", "cause", err)
	}
	m.log.Info("systemd 服务已卸载", "unit", SystemdUnit)
	return nil
}

// Status 查询 unit 是否装了、是否活跃。
func (m *systemdManager) Status() (Status, error) {
	out, err := m.run("systemctl", "is-active", SystemdUnit)
	detail := firstLine(string(out))
	if err != nil {
		// is-active 对 inactive/failed/not-found 都返回非 0。这些都是正常答案。
		// 用 unit 文件在不在来区分「没装」与「装了没跑」
		path, _ := m.UnitPath()
		if _, statErr := os.Stat(path); statErr == nil {
			return Status{Installed: true, Running: false, Detail: detail}, nil
		}
		return Status{Detail: detail}, nil
	}
	return Status{Installed: true, Running: true, Detail: detail}, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/service/ -count=1 -v`
Expected: 全部 PASS（launchd 六例 + systemd 六例）。

- [ ] **Step 5: 确认 Linux 也能编译**

systemd.go 里用了 `os/user`，在交叉编译下可能触发 cgo 相关差异，确认一下：

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./... && echo "linux/amd64 编译 OK"
```

Expected: 输出 `linux/amd64 编译 OK`。

- [ ] **Step 6: 提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
git add internal/service/systemd.go internal/service/systemd_test.go
git commit -m "feat(service): 加 systemd 实现（Restart=always、KillMode=process、无权限时提示 sudo）"
```

---

### Task 6: `handoff service` 子命令

**Files:**
- Create: `cmd/service.go`
- Create: `cmd/service_test.go`

**Interfaces:**
- Consumes: `service.New` / `service.Spec` / `service.Status` / `service.Manager`（Task 4、5 产出）；`config.Load` / `config.DefaultPath`
- Produces:
  - `handoff service install` / `uninstall` / `status`
  - `cmd.newServiceManager`（包级变量形态的缝，测试替换它注入 fake Manager）

- [ ] **Step 1: 写失败的测试**

创建 `cmd/service_test.go`：

```go
// handoff service 三个子命令的 CLI 行为测试。
//
// 经 newServiceManager 缝注入 fake，测试不会真的装任何服务。
package cmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/service"
)

// fakeManager 是可编排的 service.Manager。
type fakeManager struct {
	installErr error
	uninstErr  error
	status     service.Status
	statusErr  error
	installed  *service.Spec
}

func (f *fakeManager) Install(s service.Spec) error {
	f.installed = &s
	return f.installErr
}
func (f *fakeManager) Uninstall() error                 { return f.uninstErr }
func (f *fakeManager) Status() (service.Status, error)  { return f.status, f.statusErr }
func (f *fakeManager) Kind() string                     { return "fake" }
func (f *fakeManager) UnitPath() (string, error)        { return "/tmp/fake.unit", nil }

// withFakeManager 替换 newServiceManager 缝。
func withFakeManager(t *testing.T, f *fakeManager) {
	t.Helper()
	old := newServiceManager
	newServiceManager = func(*slog.Logger) (service.Manager, error) { return f, nil }
	t.Cleanup(func() { newServiceManager = old })
}

// runService 跑一次 service 子命令，返回合并输出与错误。
func runService(t *testing.T, cfgPath string, args ...string) (string, error) {
	t.Helper()
	resetFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"service"}, append(args, "--config", cfgPath)...))
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// install 成功时要把实际用到的路径打出来——用户下一步排障全靠这几行。
func TestServiceInstallReportsPaths(t *testing.T) {
	f := &fakeManager{}
	withFakeManager(t, f)
	cfg := writeStatusConfig(t)
	out, err := runService(t, cfg, "install")
	if err != nil {
		t.Fatalf("install 不应报错: %v", err)
	}
	if f.installed == nil {
		t.Fatal("Install 未被调用")
	}
	if f.installed.BinPath == "" {
		t.Error("Spec.BinPath 不应为空")
	}
	if f.installed.ConfigPath != cfg {
		t.Errorf("Spec.ConfigPath=%q，应等于 --config 给的 %q", f.installed.ConfigPath, cfg)
	}
	if !strings.Contains(out, "/tmp/fake.unit") {
		t.Errorf("输出应含单元路径:\n%s", out)
	}
}

// install 失败要把真因带到用户面前，不能吞掉。
func TestServiceInstallSurfacesCause(t *testing.T) {
	withFakeManager(t, &fakeManager{installErr: errors.New("Load failed: 5: Input/output error")})
	_, err := runService(t, writeStatusConfig(t), "install")
	if err == nil {
		t.Fatal("install 失败时应返回错误")
	}
	if !strings.Contains(err.Error(), "Input/output error") {
		t.Fatalf("错误应带真因，得到: %v", err)
	}
}

// status 的三种形态都要有各自可读的一行，不能都打成同一句。
func TestServiceStatusStates(t *testing.T) {
	cases := []struct {
		name string
		st   service.Status
		want string
	}{
		{"装了在跑", service.Status{Installed: true, Running: true}, "已托管"},
		{"装了没跑", service.Status{Installed: true, Running: false}, "已安装但未运行"},
		{"没装", service.Status{}, "未托管"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withFakeManager(t, &fakeManager{status: c.st})
			out, err := runService(t, writeStatusConfig(t), "status")
			if err != nil {
				t.Fatalf("status 不应报错: %v", err)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("输出应含 %q:\n%s", c.want, out)
			}
		})
	}
}

// uninstall 幂等：没装时也应成功。
func TestServiceUninstallIsIdempotent(t *testing.T) {
	withFakeManager(t, &fakeManager{})
	if _, err := runService(t, writeStatusConfig(t), "uninstall"); err != nil {
		t.Fatalf("uninstall 不应报错: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestService -v`
Expected: 编译失败——`newServiceManager` 未定义。

- [ ] **Step 3: 实现 cmd/service.go**

创建 `cmd/service.go`：

```go
// 本文件实现 handoff service 子命令：把本机 agentd 交给进程管理器托管。
//
// 职责：
//   - install：解析当前二进制与配置路径，生成并安装服务单元，复核起来了
//   - uninstall：停止并移除单元
//   - status：报告托管状态
//
// 边界：
//   - 不启动/停止 agentd 进程本身：那是管理器的事，本命令只管单元
//   - 不改 handoff 的配置文件：托管与配置是两件事，配置走 handoff init
//   - 托管之后 agentd 的形态会变：手动 Ctrl-C 会被管理器拉回，停服务要用
//     systemctl stop / launchctl bootout。install 成功时会把这句打给用户
package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/service"
)

// newServiceManager 是构造平台 Manager 的缝，测试替换它注入 fake。
var newServiceManager = service.New

// serviceCmd 是 handoff service 的父命令。
var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "把 agentd 交给本机进程管理器托管（launchd / systemd）",
}

// resolveSpec 组装 Spec：二进制绝对路径 + 配置路径 + 日志路径。
//
// 返回：
//   - 填好的 Spec
//   - 错误：取不到可执行文件路径或加载配置失败
//
// 注意：
//   - BinPath 必须经 EvalSymlinks 解析。装在 ~/.local/bin/handoff 的二进制
//     常常是个 symlink；单元里写 symlink，换版换掉链接目标后单元还指着旧的
func resolveSpec(cfgPath string) (service.Spec, error) {
	exe, err := os.Executable()
	if err != nil {
		return service.Spec{}, fmt.Errorf("取当前可执行文件路径: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return service.Spec{}, fmt.Errorf("加载配置 %s: %w", cfgPath, err)
	}
	return service.Spec{
		BinPath:    exe,
		ConfigPath: cfgPath,
		LogPath:    filepath.Join(cfg.DataDir, "agentd.log"),
	}, nil
}

// effectiveConfigPath 返回本次命令实际使用的配置路径。
func effectiveConfigPath() string {
	if configPath != "" {
		return configPath
	}
	return config.DefaultPath()
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "安装并启动服务单元",
	RunE: func(cmd *cobra.Command, _ []string) error {
		log := slog.Default()
		m, err := newServiceManager(log)
		if err != nil {
			return err
		}
		p := effectiveConfigPath()
		spec, err := resolveSpec(p)
		if err != nil {
			return err
		}
		if err := m.Install(spec); err != nil {
			return fmt.Errorf("安装服务失败: %w", err)
		}
		unit, _ := m.UnitPath()
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "已托管   %s\n", m.Kind())
		fmt.Fprintf(out, "单元     %s\n", unit)
		fmt.Fprintf(out, "二进制   %s\n", spec.BinPath)
		fmt.Fprintf(out, "配置     %s\n", spec.ConfigPath)
		fmt.Fprintf(out, "日志     %s\n", spec.LogPath)
		// 形态变化必须说清楚：托管之后手动 Ctrl-C 会被拉回来，这是最容易
		// 让人以为「服务停不掉」的一点
		fmt.Fprintf(out, "\n注意     agentd 现在由 %s 托管，崩溃或退出都会被自动拉起。\n", m.Kind())
		fmt.Fprintf(out, "         想真正停掉它请用 handoff service uninstall，Ctrl-C 只会让它被重新拉起。\n")
		return nil
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "停止并移除服务单元",
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := newServiceManager(slog.Default())
		if err != nil {
			return err
		}
		if err := m.Uninstall(); err != nil {
			return fmt.Errorf("卸载服务失败: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已卸载   %s 单元；agentd 不再被自动拉起\n", m.Kind())
		return nil
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看托管状态",
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := newServiceManager(slog.Default())
		if err != nil {
			return err
		}
		st, err := m.Status()
		if err != nil {
			return fmt.Errorf("查询服务状态失败: %w", err)
		}
		unit, _ := m.UnitPath()
		out := cmd.OutOrStdout()
		switch {
		case st.Installed && st.Running:
			fmt.Fprintf(out, "已托管        %s   %s\n", m.Kind(), unit)
		case st.Installed:
			// 装了没跑是一个真实且常见的状态（崩溃循环、被手动 stop），
			// 必须与「没装」分开报，否则用户会去重装一个已经装了的东西
			fmt.Fprintf(out, "已安装但未运行  %s   %s\n", m.Kind(), unit)
			fmt.Fprintf(out, "处置          看日志找原因，或 handoff service install 重装\n")
		default:
			fmt.Fprintf(out, "未托管        %s 上没有 handoff 的服务单元\n", m.Kind())
			fmt.Fprintf(out, "处置          handoff service install\n")
		}
		if st.Detail != "" {
			fmt.Fprintf(out, "管理器原文    %s\n", st.Detail)
		}
		return nil
	},
}

func init() {
	serviceCmd.AddCommand(serviceInstallCmd, serviceUninstallCmd, serviceStatusCmd)
	rootCmd.AddCommand(serviceCmd)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run TestService -count=1 -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
git add cmd/service.go cmd/service_test.go
git commit -m "feat(cmd): 加 handoff service install/uninstall/status"
```

---

### Task 7: `handoff init`

**Files:**
- Create: `cmd/init.go`
- Create: `cmd/init_test.go`

**Interfaces:**
- Consumes: `toolchain.Detect` / `toolchain.FirstReady` / `toolchain.Result`（Task 3）；`config.Config` 全部字段与 `config.Save`（Task 1）；`effectiveConfigPath()`（Task 6，同包）
- Produces:
  - `handoff init` 子命令
  - `cmd.initStdinIsTTY` —— 包级变量形态的缝，测试替换它模拟 tty / 非 tty

**问答面共 11 项**（逐字取自 spec §4.4，顺序即提问顺序）：角色 → `executor.default` → `executor.model` → `listen` → `repo_root` → `approver.executor` → `approver.model` → 服务托管 → `update.auto` → `update.interval` → `sync.auto` → `targets` 循环。（"11 项"按 spec 的分组计，`approver` 两问与 `update` 两问各算一项。）

- [ ] **Step 1: 写失败的测试**

创建 `cmd/init_test.go`：

```go
// handoff init 的 CLI 行为测试。
//
// 交互经 rootCmd.SetIn 喂脚本化答案，tty 判定经 initStdinIsTTY 缝控制，
// 因此测试既能覆盖交互分支也不需要真的终端。
package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/config"
)

// runInit 跑一次 init：answers 是按行喂给 stdin 的答案，tty 控制是否走交互分支。
func runInit(t *testing.T, cfgPath string, tty bool, answers string) (string, error) {
	t.Helper()
	resetFlags(t)
	oldTTY := initStdinIsTTY
	initStdinIsTTY = func() bool { return tty }
	t.Cleanup(func() { initStdinIsTTY = oldTTY })

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetIn(strings.NewReader(answers))
	rootCmd.SetArgs([]string{"init", "--config", cfgPath})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetIn(nil)
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// loadCfg 读回写盘的配置。
func loadCfg(t *testing.T, p string) *config.Config {
	t.Helper()
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("回读配置: %v", err)
	}
	return cfg
}

// 非 tty 时一问不问，只探测 + 写出厂默认，并明确告诉用户下一步。
//
// why：init 会被 install.sh 经管道调起（curl … | bash），那种场景下 stdin
// 被脚本占着。问了也没人答，卡住比不问糟得多。
func TestInitNonInteractiveWritesDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, err := runInit(t, p, false, "")
	if err != nil {
		t.Fatalf("init 不应报错: %v", err)
	}
	if !strings.Contains(out, "未交互配置") {
		t.Errorf("非 tty 时应提示未交互配置:\n%s", out)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("配置应被写出来: %v", err)
	}
	cfg := loadCfg(t, p)
	if cfg.Listen != "127.0.0.1:7777" {
		t.Errorf("listen 应为出厂默认，得到 %q", cfg.Listen)
	}
	if cfg.Token == "" {
		t.Error("token 应被生成")
	}
	if !cfg.Update.Auto {
		t.Error("update.auto 应为出厂默认 true")
	}
}

// 探测表必须打印，且四家都在。
func TestInitPrintsDetectionTable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, _ := runInit(t, p, false, "")
	for _, n := range []string{"opencode", "claude", "grok", "codex"} {
		if !strings.Contains(out, n) {
			t.Errorf("探测表缺 %s:\n%s", n, out)
		}
	}
}

// 交互下全部回车（取默认）：应写出一份合法配置，且角色默认能走通。
func TestInitInteractiveAllDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	// 30 个空行足够覆盖所有提问，多余的不会被读
	out, err := runInit(t, p, true, strings.Repeat("\n", 30))
	if err != nil {
		t.Fatalf("init 不应报错: %v\n%s", err, out)
	}
	cfg := loadCfg(t, p)
	if cfg.Listen == "" || cfg.Token == "" {
		t.Fatalf("配置不完整: %+v", cfg)
	}
}

// 幂等：已有配置时，每一问的默认值取当前值，全回车即原样保持。
//
// why 这条是 init 能当「改配置工具」用的前提。若默认值退回出厂值，
// 用户重跑一次 init 就会把 listen、token、targets 全部冲掉。
func TestInitIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "listen: 0.0.0.0:7788\ntoken: keepme\nrepo_root: /srv/repos\nexecutor:\n  default: grok\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runInit(t, p, true, strings.Repeat("\n", 30)); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg := loadCfg(t, p)
	if cfg.Listen != "0.0.0.0:7788" {
		t.Errorf("listen 被冲掉了: %q", cfg.Listen)
	}
	if cfg.Token != "keepme" {
		t.Errorf("token 被冲掉了: %q", cfg.Token)
	}
	if cfg.RepoRoot != "/srv/repos" {
		t.Errorf("repo_root 被冲掉了: %q", cfg.RepoRoot)
	}
	if cfg.Executor.Default != "grok" {
		t.Errorf("executor.default 被冲掉了: %q", cfg.Executor.Default)
	}
}

// 显式回答要被采纳：选审核者机角色 + 给一个 listen。
func TestInitAcceptsAnswers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	// 角色选 1（执行机），listen 给 0.0.0.0:7799，其余回车
	answers := "1\n\n\n0.0.0.0:7799\n" + strings.Repeat("\n", 26)
	if _, err := runInit(t, p, true, answers); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := loadCfg(t, p).Listen; got != "0.0.0.0:7799" {
		t.Fatalf("listen=%q，期望采纳输入的 0.0.0.0:7799", got)
	}
}

// 末尾必须打印本机 token 与现成的配对片段——审核者机要靠它配 targets。
func TestInitPrintsPairingSnippet(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	out, err := runInit(t, p, true, "1\n"+strings.Repeat("\n", 29))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "targets:") {
		t.Errorf("应打印现成的配对 yaml 片段:\n%s", out)
	}
	if !strings.Contains(out, loadCfg(t, p).Token) {
		t.Error("配对片段里应含本机 token")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestInit -v`
Expected: 编译失败——`initStdinIsTTY` 未定义。

- [ ] **Step 3: 实现 cmd/init.go**

创建 `cmd/init.go`：

```go
// 本文件实现 handoff init 子命令：一台新机器的问答式配置。
//
// 职责：
//   - 探测四家 executor 的状态并成表打印
//   - 按角色分支问 11 组问题，把答案写进 config.yaml
//   - 末尾打印本机 token 与现成的配对 yaml 片段
//
// 边界：
//   - **不发起任何真实模型调用**：探测一律用轻量本地判据（见 internal/toolchain）
//   - **不装服务**：托管走 handoff service install。init 只在最后提示一句
//   - **不阻断任何选择**：探测结果只影响默认值与标注；没装任何 executor 也能配完
//     （纯审核者机的正常情况），选了「未登录」的执行者只警告不拦
//   - stdin 非 tty 时一问不问：init 会被 install.sh 经管道调起，问了没人答，
//     卡住比不问糟得多
package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/toolchain"
)

// initStdinIsTTY 判断 stdin 是不是终端。测试替换它以覆盖两条分支。
var initStdinIsTTY = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) }

// 角色取值。init 先问角色，再按角色决定后面问什么。
const (
	roleExecutor = 1 // 执行机：跑 agentd 与 executor
	roleReviewer = 2 // 审核者机：派发与审阅
	roleBoth     = 3
)

// initCmd 交互式配置本机。
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "探测本机 executor 并交互式生成配置",
	RunE: func(cmd *cobra.Command, _ []string) error {
		p := effectiveConfigPath()
		out := cmd.OutOrStdout()

		// config.Load 在文件不存在时会生成 token 并写盘，正好作为「当前值」的基线：
		// 已存在则读回实际值（幂等的前提），不存在则拿到一份带默认值的新配置
		cfg, err := config.Load(p)
		if err != nil {
			return fmt.Errorf("加载配置 %s: %w", p, err)
		}

		results := toolchain.Detect()
		printDetection(out, results)

		if !initStdinIsTTY() {
			// 非交互降级：只探测 + 写出厂默认，明确告诉用户下一步做什么
			fmt.Fprintln(out, "\n未交互配置（stdin 不是终端），已写入默认配置。")
			fmt.Fprintf(out, "请在终端里运行 handoff init 完成配置：%s\n", p)
			if err := config.Save(p, cfg); err != nil {
				return err
			}
			printPairing(out, cfg)
			return nil
		}

		r := bufio.NewReader(cmd.InOrStdin())
		if err := askAll(out, r, cfg, results); err != nil {
			return err
		}
		if err := config.Save(p, cfg); err != nil {
			return err
		}
		fmt.Fprintf(out, "\n已写入 %s\n", p)
		printPairing(out, cfg)
		fmt.Fprintln(out, "\n下一步   handoff service install   （把 agentd 交给本机进程管理器托管）")
		return nil
	},
}

// printDetection 打印四家 executor 的探测表。
func printDetection(w io.Writer, rs []toolchain.Result) {
	fmt.Fprintln(w, "本机 executor 探测：")
	for _, r := range rs {
		path := r.Path
		if path == "" {
			path = "—"
		}
		fmt.Fprintf(w, "  %-9s %-20s %s\n", r.Name, r.State.String(), path)
	}
	for _, r := range rs {
		if r.Name == "claude" && r.State == toolchain.StateAuthUnknown {
			// 如实说明为什么判不出来，免得用户以为是探测坏了
			fmt.Fprintln(w, "\n  claude 的登录凭据存在系统 Keychain 里，本机判据够不着，所以只报「登录态未知」。")
			fmt.Fprintln(w, "  想确认是否可用，自己跑一次 claude -p \"hi\" 看有没有输出。")
		}
		if r.Name == "codex" && r.State != toolchain.StateMissing {
			// B30：漏配代理的症状极具迷惑性，探到 codex 就提醒一次（只提醒，不问）
			fmt.Fprintln(w, "\n  codex 若需代理才能连 OpenAI，请在 config.yaml 的 env 段配 codex: codex.env。")
			fmt.Fprintln(w, "  漏配的症状是会话建得起来、状态 running、一个 token 不产，只有 serve.log 里刷")
			fmt.Fprintln(w, "  failed to refresh available models。")
		}
	}
}

// askAll 按角色分支问完全部问题，就地改写 cfg。
func askAll(w io.Writer, r *bufio.Reader, cfg *config.Config, rs []toolchain.Result) error {
	fmt.Fprintln(w, "\n以下每一问直接回车即取方括号里的当前值。")

	// 1. 角色。探到就绪 executor 则默认「执行机」
	defRole := roleReviewer
	if toolchain.FirstReady(rs) != "" {
		defRole = roleExecutor
	}
	role := askInt(w, r, "这台机器的角色 1=执行机 2=审核者机 3=两者", defRole)
	isExec := role == roleExecutor || role == roleBoth
	isReviewer := role == roleReviewer || role == roleBoth

	if isExec {
		// 2-3. 缺省执行者与模型
		defExec := cfg.Executor.Default
		if defExec == "" {
			if first := toolchain.FirstReady(rs); first != "" {
				defExec = first
			} else {
				defExec = "opencode"
			}
		}
		cfg.Executor.Default = askString(w, r, "缺省执行者 executor.default", defExec)
		warnIfNotReady(w, rs, cfg.Executor.Default)
		cfg.Executor.Model = askString(w, r, "执行者模型 executor.model（空=用执行者自身默认）", cfg.Executor.Model)

		// 4. 监听地址
		fmt.Fprintln(w, "  提示：要被外机访问需改成 0.0.0.0:7777")
		cfg.Listen = askString(w, r, "监听地址 listen", cfg.Listen)

		// 5. 仓库落点
		cfg.RepoRoot = askString(w, r, "仓库落点根目录 repo_root（空=repo add --clone 必须显式给路径）", cfg.RepoRoot)

		// 6. 审批链
		cfg.Approver.Executor = askString(w, r, "审批链执行者 approver.executor（空=不启用，权限请求直接找人）", cfg.Approver.Executor)
		if cfg.Approver.Executor != "" {
			cfg.Approver.Model = askString(w, r, "审批链模型 approver.model（空=用执行者自身默认）", cfg.Approver.Model)
		}
	}

	// 7-8. 自动更新（两种角色都要）
	cfg.Update.Auto = askBool(w, r, "启用自动更新 update.auto", cfg.Update.Auto)
	if cfg.Update.Auto {
		cfg.Update.Interval = askDuration(w, r, "检查频率 update.interval", cfg.Update.Interval)
	}

	if isReviewer {
		// 9. 任务结束自动同步分支
		cfg.Sync.Auto = askBool(w, r, "任务结束自动同步远程分支到本地 sync.auto", cfg.Sync.Auto)
		// 10. targets 配对，循环添加
		askTargets(w, r, cfg)
	}
	return nil
}

// warnIfNotReady 在选了「没装」或「未登录」的执行者时警告一句——只警告，不拦。
//
// why 不拦：一台刚装好的机器上什么都还没登录，但用户知道自己等会儿要登；
// 拦住等于逼他先去登录再回来重跑 init。
func warnIfNotReady(w io.Writer, rs []toolchain.Result, name string) {
	for _, r := range rs {
		if r.Name != name {
			continue
		}
		if r.State == toolchain.StateMissing {
			fmt.Fprintf(w, "  ⚠ %s 没装。配置照写，但派活前需要先装上。\n", name)
		} else if r.State == toolchain.StateNoCreds {
			fmt.Fprintf(w, "  ⚠ %s 已安装但未登录。配置照写，但派活前需要先登录。\n", name)
		}
		return
	}
	fmt.Fprintf(w, "  ⚠ %s 不在已知的四家里（opencode/claude/grok/codex），派发时会报未注册。\n", name)
}

// askTargets 循环添加远程执行机配对，回车即结束。
func askTargets(w io.Writer, r *bufio.Reader, cfg *config.Config) {
	if cfg.Targets == nil {
		cfg.Targets = map[string]config.Target{}
	}
	if len(cfg.Targets) > 0 {
		fmt.Fprintf(w, "\n已配对 %d 台远程执行机：\n", len(cfg.Targets))
		for name, t := range cfg.Targets {
			fmt.Fprintf(w, "  %-12s %s  user=%s\n", name, t.Addr, t.User)
		}
	}
	for {
		name := askString(w, r, "\n新增远程执行机名字（直接回车结束）", "")
		if name == "" {
			return
		}
		t := cfg.Targets[name]
		t.Addr = askString(w, r, "  地址 addr（形如 100.73.238.21:7777）", t.Addr)
		t.Token = askString(w, r, "  令牌 token（对方 handoff init 末尾会打出来）", t.Token)
		t.User = askString(w, r, "  ssh 用户名 user（attach/pull 要用）", t.User)
		cfg.Targets[name] = t
	}
}

// printPairing 打印本机 token 与现成的配对片段。
//
// why 直接给 yaml 片段而不是只报 token：配对是最容易配错的一步（键名、缩进、
// 地址形态），给一段能直接粘的比让用户照着文档拼强得多。
func printPairing(w io.Writer, cfg *config.Config) {
	fmt.Fprintln(w, "\n本机 token 与配对片段（贴到审核者机的 config.yaml 里）：")
	fmt.Fprintln(w, "\ntargets:")
	fmt.Fprintf(w, "  <给这台机器起个名字>:\n")
	fmt.Fprintf(w, "    addr: \"%s\"\n", pairAddr(cfg.Listen))
	fmt.Fprintf(w, "    token: \"%s\"\n", cfg.Token)
	fmt.Fprintf(w, "    user: \"%s\"\n", os.Getenv("USER"))
	fmt.Fprintln(w, "\n  注意：addr 里的地址要换成审核者机能连到的实际 IP。")
}

// pairAddr 把 listen 里的 0.0.0.0 换成占位提示，免得用户直接粘一个连不上的地址。
func pairAddr(listen string) string {
	if strings.HasPrefix(listen, "0.0.0.0:") {
		return "<本机IP>:" + strings.TrimPrefix(listen, "0.0.0.0:")
	}
	return listen
}

// ask 打印提示并读一行；空行返回空串（调用方据此取默认值）。
func ask(w io.Writer, r *bufio.Reader, prompt, def string) string {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(w, "%s []: ", prompt)
	}
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		// stdin 提前结束（脚本喂的答案用完了）：当作全部取默认，不报错。
		// 这样测试与真实的「Ctrl-D 提前结束」都能走到写盘
		fmt.Fprintln(w)
		return ""
	}
	return strings.TrimSpace(line)
}

// askString 读一个字符串，空行取默认。
func askString(w io.Writer, r *bufio.Reader, prompt, def string) string {
	if v := ask(w, r, prompt, def); v != "" {
		return v
	}
	return def
}

// askInt 读一个整数，空行或解析失败取默认。
func askInt(w io.Writer, r *bufio.Reader, prompt string, def int) int {
	v := ask(w, r, prompt, strconv.Itoa(def))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintf(w, "  «%s» 不是数字，取默认 %d\n", v, def)
		return def
	}
	return n
}

// askBool 读 y/n，空行取默认。
func askBool(w io.Writer, r *bufio.Reader, prompt string, def bool) bool {
	d := "n"
	if def {
		d = "y"
	}
	v := strings.ToLower(ask(w, r, prompt+" (y/n)", d))
	if v == "" {
		return def
	}
	return v == "y" || v == "yes"
}

// askDuration 读一个时长，空行或解析失败取默认。
func askDuration(w io.Writer, r *bufio.Reader, prompt string, def time.Duration) time.Duration {
	v := ask(w, r, prompt, def.String())
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Fprintf(w, "  «%s» 不是合法时长（如 6h / 30m），取默认 %s\n", v, def)
		return def
	}
	return d
}

func init() { rootCmd.AddCommand(initCmd) }
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run TestInit -count=1 -v`
Expected: 六例全 PASS。

- [ ] **Step 5: 手工跑一次真实交互，确认提示语读得通**

配置写到临时路径，**绝不碰 `~/.handoff/config.yaml`**：

```bash
D=$(mktemp -d) && go run . init --config "$D/config.yaml" < /dev/tty; echo "---- 写出的配置 ----"; cat "$D/config.yaml"; rm -rf "$D"
```

Expected: 问答能一路回车走完，末尾打印 token 与配对片段，写出的 yaml 里 `update` 段在。

> 若当前环境没有 tty（在自动化里执行），跳过这一步并在提交信息里说明；非交互分支已有单测覆盖。

- [ ] **Step 6: 提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
git add cmd/init.go cmd/init_test.go
git commit -m "feat(cmd): 加 handoff init（探测四家 executor + 11 项问答 + 幂等重跑）"
```

---

### Task 8: systemd 模板与 README

**Files:**
- Modify: `deploy/handoff-agentd.service:23`
- Modify: `README.md`

**Interfaces:**
- Consumes: Task 1–7 的全部产出
- Produces: 无代码产物

- [ ] **Step 1: 改 systemd 模板的重启策略**

`deploy/handoff-agentd.service` 里把：

```
Restart=on-failure
RestartSec=3
```

改为：

```
# Restart=always 而不是 on-failure：agentd 的优雅关停以 exit 0 结束
#（自更新换版就是靠它交接给新二进制）。on-failure 在 exit 0 时**不会**重启
# ——换完版服务就此消失，而且没有任何信号告诉任何人。
Restart=always
RestartSec=3
```

同时在文件头的「安装」注释块末尾加一句：

```
# 从 B54.2 起，这个模板可以由 `handoff service install` 自动生成并安装，
# 不必再手动 cp。本文件保留为参考与手动部署用。
```

- [ ] **Step 2: 确认单测钉住了这个改动**

Task 5 的 `TestSystemdUnitContent` 断言的是**生成的** unit，不是这个模板文件。模板本身没有测试守着，改错不会翻红——所以这一步必须人眼确认：

Run: `grep -n "Restart=\|KillMode=" deploy/handoff-agentd.service`
Expected: 输出恰好两行，`Restart=always` 与 `KillMode=process`，**没有** `Restart=on-failure`。

- [ ] **Step 3: README 补 init / service 说明**

在 README 的安装章节（B54.1 加的那段「装完用 handoff version 确认」之后）插入：

```markdown
装完先配一次：

```bash
handoff init                    # 探测本机 executor，问答式写出 config.yaml
handoff service install         # 把 agentd 交给 launchd / systemd 托管
handoff service status          # 看托管状态
```

`handoff init` 可以随时重跑当改配置用——每一问的默认值取当前配置的实际值，
一路回车即原样保持。stdin 不是终端时（例如经管道调起）它一问不问，只写默认配置。

**托管之后 agentd 的形态会变**：它由进程管理器拉起，崩溃或退出都会被自动拉回。
Ctrl-C 停不掉它（会被立刻重新拉起），要真正停掉请用 `handoff service uninstall`，
或 `systemctl stop handoff-agentd` / `launchctl bootout gui/$(id -u)/dev.gosuper.handoff.agentd`。
macOS 上 launchd 对重生有约 10 秒节流，重启期间会有约 10 秒的服务空窗——
执行者不受影响（它们在独立会话里），但期间的 `dispatch` / `reply` 会失败。
```

- [ ] **Step 4: README 命令表补三行**

在命令表里 `handoff version` 那行之后加：

```markdown
| `handoff init` | 探测本机 executor 并交互式生成/更新配置（幂等，可重跑） | — |
| `handoff service install\|uninstall\|status` | 把 agentd 交给 launchd / systemd 托管 | — |
```

- [ ] **Step 5: 全量回归并提交**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
git add deploy/handoff-agentd.service README.md
git commit -m "docs: systemd 模板改 Restart=always，README 补 init/service 与托管后的形态变化"
```

- [ ] **Step 6: 交付自检**

逐项确认，任一不过就回到对应 task 修：

- [ ] `go build ./...` / `go vet ./...` / `gofmt -l .` 全干净
- [ ] `go test ./... -count=1` 全绿
- [ ] `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...` 通过（systemd 那条路在 Linux 上编得过）
- [ ] Task 2 Step 7 的信号手工验证跑过，**亲眼看到退出码 0**
- [ ] `grep -c "AbandonProcessGroup" internal/service/launchd.go` 输出 0
- [ ] `grep -c "Restart=on-failure" deploy/handoff-agentd.service internal/service/systemd.go` 两个文件都是 0
- [ ] 八个 task 各有一个独立 commit

---

## 自审

**1. Spec 覆盖（对照 spec §6 的 B 期范围）**

| B 期要求 | 落在哪 |
|---|---|
| `handoff init` | Task 7 |
| `internal/toolchain` | Task 3 |
| `handoff service` | Task 6 |
| `internal/service`（launchd + systemd） | Task 4、Task 5 |
| `internal/agentd/shutdown.go` | Task 2 |
| systemd 模板改 `Restart=always` | Task 8 Step 1 |
| config 加 `update` 段 | Task 1 |
| 验收：init 在两种角色下都能配完并幂等重跑 | Task 7 Step 1 的 `TestInitIsIdempotent` / `TestInitAcceptsAnswers` |
| 验收：service install→status→uninstall 往返 | Task 6 Step 1（经 fake Manager）+ Task 4/5 的调用序列断言 |
| 验收：P1 探针通过 | **已于 08-11 完成**，结果写在 spec §7.1，本计划的 launchd 实现直接消费其结论 |

B 期无遗漏。**不属于本期**（已在「本期不做」里显式交接给 B54.3）：`internal/release`、`internal/selfupdate`、`handoff upgrade`、CLI 更新提示、agentd 的更新循环、「非托管则拒绝自动更新」的判据实现。

**2. 占位符扫描**：无 TBD / TODO / 「类似 Task N」/ 「加上适当的错误处理」。每个代码步骤都给了可直接落盘的完整内容。Task 7 Step 5 与 Task 8 Step 2 是人眼确认步骤，各自写明了确认什么、期望看到什么，不是省略。

**3. 类型一致性**

- `config.UpdateConfig{Auto, Interval}`（Task 1）→ Task 7 的 `cfg.Update.Auto` / `cfg.Update.Interval`。字段名一致。
- `agentd.Shutdown` 的 `Trigger` / `Reason` / `Serve` / `serveWithListener`（Task 2 Step 3）→ Task 2 Step 1 的测试、Step 5 的 `cmd/agentd.go` 接线。四个名字一致；`serveWithListener` 只在包内与测试里用。
- `toolchain.State` 的四个常量与 `Result.Ready()` / `Detect()` / `FirstReady()`（Task 3）→ Task 7 的 `toolchain.Detect()`、`toolchain.FirstReady(rs)`、`r.State == toolchain.StateAuthUnknown`、`toolchain.StateMissing` / `StateNoCreds`。全部一致。
- `service.Spec{BinPath, ConfigPath, LogPath}` / `service.Status{Installed, Running, Detail}` / `Manager` 五个方法（Task 4 Step 3）→ Task 5 的 `systemdManager` 实现、Task 6 的 `fakeManager` 与调用点。方法集一致（`Install`/`Uninstall`/`Status`/`Kind`/`UnitPath`）。
- `service.New` 的签名 `func(*slog.Logger) (Manager, error)` → Task 6 的 `var newServiceManager = service.New` 与测试里的替换函数签名一致。
- `firstLine` 在 `launchd.go`（Task 4）定义，`systemd.go`（Task 5）复用——同包，不重复定义。
- `effectiveConfigPath()` 在 `cmd/service.go`（Task 6）定义，`cmd/init.go`（Task 7）复用——同包，Task 7 必须排在 Task 6 之后，任务顺序已如此安排。
- `config.Save(path, cfg) error`（Task 1 Step 6）→ Task 7 的两处写盘调用。它只是既有未导出 `save` 的包装，权限位与建目录逻辑不重复实现。
- `writeStatusConfig(t)` 是 `cmd/status_test.go` 的既有 helper，Task 6 的测试复用它（同包测试文件）；`resetFlags(t)` 是 `cmd/root_test.go` 的既有 helper，Task 6、7 的测试复用它。两者都已在仓库里，不需要新建。
- `configPath` 是 `cmd/root.go:24` 的既有包级变量（`--config` 持久 flag 的绑定目标），`effectiveConfigPath()` 读它。
