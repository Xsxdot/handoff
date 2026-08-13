# B87：proxy 配置项 + 更新分发反转为执行机自拉 —— 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 handoff 自身出网加一个 `proxy` 配置项（http/https/socks5/socks5h），并把换版分发从「协调者推 20MB 给执行机」反转为「协调者只下发 tag+sha256，执行机自己下」。

**Architecture:** 新增 `internal/proxycfg` 把配置字符串翻译成 `*http.Transport` / git `-c` 参数 / 脱敏文本；`internal/release` 改为收 `http.RoundTripper`（守住它「不读配置」的包边界）。换版协议加显式 `mode` 参数，`mode=pull` 走异步受理：agentd 立即返回 202，后台下载→比对协调者下发的 sha256→解包自检→原子换版→重启，进度与错误经 `/api/status` 的 `update.pull_state` 回报。老 agentd 靠 `update.pull` 能力位自动降级回推送模式。

**Tech Stack:** Go 1.26（`net/http` 原生支持 socks5/socks5h 代理，**不引新依赖**）、`gopkg.in/yaml.v3`、`spf13/cobra`、`log/slog`。

**Spec:** [2026-08-13-proxy-config-and-executor-pull-update-design.md](../specs/2026-08-13-proxy-config-and-executor-pull-update-design.md)

## Global Constraints

- **不新增任何 Go 依赖**。socks5/socks5h 由 `net/http` 与 `git` 原生支持。若你发现自己在写 `go get`，说明走错了。
- **日志一律用 `slog`**（本仓惯例：包级 `func log() *slog.Logger { return slog.Default() }` 运行时取值，或结构体上已注入的 `s.log` / `i.Log`）。**禁止 `fmt.Printf` 作为日志机制**。CLI 面向用户的输出走 `fmt.Fprintf(out, ...)`，那是输出不是日志，两者不混。
- **绝不打印代理原文**。代理 URL 常含 `user:pass@`。任何日志字段里的代理值必须先过 `proxycfg.Redact`。本仓已有同款纪律：`internal/envfile/resolver.go:64`。
- **新配置字段必须带 `omitempty`**。`config` 以 `KnownFields(true)` 严格解析，未知键让 agentd **启动失败**；没有 `omitempty`，新版 `Save` 会把空值写进每台机器的 `config.yaml`，旧版 agentd 从此起不来。
- **新文件必须有文件头注释**（职责 + 边界，边界要写清「不做什么」）；**导出函数必须有 doc 注释**（参数、返回、注意事项）；复杂分支写中文「为什么」注释，不写「做了什么」。
- **每个 task 结束即 commit**，提交信息说清做了什么。
- 六道验收闸（全部 task 完成后跑）：`go build ./...` / `go vet ./...` / `gofmt -l $(git ls-files '*.go')` 无输出 / `go test ./... -count=1` / `GOOS=windows GOARCH=amd64 go build ./...` / `go test -race ./internal/agentd/ ./internal/config/ ./internal/proxycfg/ ./cmd/`。

## 本计划显式不做

- **下载重试**。v0.2.2 验收时踩到的「本机连 `github.com:443` 间歇不通、一次抖动即判整机失败」已有独立任务 `fix/download-retry` 在修，本计划不并入——两条改动都动 `internal/release`，合在一起会让 review 无法分辨哪个 diff 属于哪个结论。Task 8 的后台下载失败即落 `PullState.Stage = "failed"`，重试逻辑将来加在 `Installer` 内部时对本计划透明。
- SSH 协议 remote 的代理（`http.proxy` 对 `ssh://` 无效，见 spec §3.4 与 Task 11 的文档要求）。
- `install.sh` / `install.ps1` 的代理。
- executor 出网代理（归 `env` 段，不动）。

---

## 文件结构

| 文件 | 动作 | 职责 |
|------|------|------|
| `internal/proxycfg/proxycfg.go` | 新建 | 把 proxy 配置字符串翻译成 Transport / git 参数 / 脱敏文本。**只翻译，不读配置、不碰网络** |
| `internal/proxycfg/proxycfg_test.go` | 新建 | 上述三个出口的行为 |
| `internal/config/config.go` | 改 | 加 `Proxy` 字段、校验、未知键提示串 |
| `internal/config/config_test.go` | 改 | 合法/非法值、omitempty 回归 |
| `internal/release/client.go` | 改 | `NewClient(tr)`；新增 `AssetURL` |
| `internal/release/install.go` | 改 | `NewInstaller(log, tr)`；新增 `FetchChecksum`、`FetchByTag` |
| `internal/release/client_test.go`、`install_test.go` | 改 | 跟随签名 + 新方法的测试 |
| `cmd/root.go` | 改 | `update-check` 用配置里的代理 |
| `cmd/upgrade.go` | 改 | 代理接线、`--push`、选路、checksums 缓存、超时放宽 |
| `internal/agentd/workspace.go` | 改 | 新增 `gitRunNet`；`fetch` 改走它 |
| `internal/agentd/projectadmin.go` | 改 | `clone` 改走 `gitRunNet` |
| `internal/agentd/server.go` | 改 | bootstrap 注入代理；`UpdateDeps` 加自拉依赖；`handleStatus` 装配 pull 状态 |
| `internal/agentd/update.go` | 改 | `mode` 判别、参数校验、并发锁、异步自拉 |
| `internal/agentd/pullstate.go` | 新建 | 自拉状态的内存持有者（并发锁 + 阶段流转），从 `update.go` 分出以免它继续膨胀 |
| `internal/proto/status.go` | 改 | `UpdateStatus.Pull` / `PullState` |
| `internal/proto/update.go` | 改 | `UpdateResp.Accepted`、`UpdateReasonPullInProgress`、`UpdateMode*` 常量 |
| `internal/client/update.go` | 改 | `PullUpdate`；`WaitVersion` 读 pull 状态提前失败 |
| `README.md` | 改 | `proxy` 配置、升级章节、排障表、SSH 限制 |
| `docs/superpowers/specs/2026-08-11-update-and-skill-delivery-design.md` | 改 | D1 推翻记录 |

---

## Task 1: `internal/proxycfg` 新包

**Files:**
- Create: `internal/proxycfg/proxycfg.go`
- Test: `internal/proxycfg/proxycfg_test.go`

**Interfaces:**
- Consumes: 无（叶子包，只依赖标准库）
- Produces:
  - `func Transport(proxy string) (*http.Transport, error)`
  - `func GitArgs(proxy string) []string`
  - `func Redact(proxy string) string`
  - `func Validate(proxy string) error`
  - `var SupportedSchemes = []string{"http", "https", "socks5", "socks5h"}`

- [ ] **Step 1: 写失败的测试**

创建 `internal/proxycfg/proxycfg_test.go`：

```go
package proxycfg_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/proxycfg"
)

// 空串必须保持现行为：Transport 的 Proxy 等价于 http.ProxyFromEnvironment。
// 判据不看函数指针（不可比较），看行为——设了 HTTPS_PROXY 环境变量后，
// 对 https URL 的请求应当被解析到那个代理。
func TestTransportEmptyHonorsEnv(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy.example:3128")
	tr, err := proxycfg.Transport("")
	if err != nil {
		t.Fatalf("Transport(\"\"): %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/x", nil)
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy(): %v", err)
	}
	if u == nil || u.Host != "env-proxy.example:3128" {
		t.Fatalf("空 proxy 应沿用环境变量，实得 %v", u)
	}
}

// 非空时固定返回配置的代理，且**不受环境变量影响**——显式配置就是显式意图。
func TestTransportExplicitOverridesEnv(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy.example:3128")
	tr, err := proxycfg.Transport("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("Transport: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/x", nil)
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy(): %v", err)
	}
	if u == nil || u.Scheme != "socks5" || u.Host != "127.0.0.1:1080" {
		t.Fatalf("显式 proxy 应压过环境变量，实得 %v", u)
	}
}

func TestValidateAcceptsAllSupportedSchemes(t *testing.T) {
	for _, p := range []string{
		"http://h:8080", "https://h:8080", "socks5://h:1080", "socks5h://h:1080",
	} {
		if err := proxycfg.Validate(p); err != nil {
			t.Errorf("Validate(%q) 应通过，实得 %v", p, err)
		}
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"socks4://h:1080": "不支持的 scheme",
		"127.0.0.1:1080":  "裸 host:port 没有 scheme",
		"http://":         "缺 host",
		"://h:1080":       "畸形 URL",
	}
	for in, why := range cases {
		err := proxycfg.Validate(in)
		if err == nil {
			t.Errorf("Validate(%q) 应被拒（%s）", in, why)
			continue
		}
		// 错误文本必须列出支持的 scheme，否则用户只知道错了不知道该写什么
		for _, s := range proxycfg.SupportedSchemes {
			if !strings.Contains(err.Error(), s) {
				t.Errorf("Validate(%q) 的错误文本应列出 %q，实得 %q", in, s, err)
			}
		}
	}
}

// 空串是合法的（= 不配代理），Validate 必须放行。
func TestValidateAcceptsEmpty(t *testing.T) {
	if err := proxycfg.Validate(""); err != nil {
		t.Fatalf("空 proxy 应合法，实得 %v", err)
	}
}

func TestGitArgs(t *testing.T) {
	if got := proxycfg.GitArgs(""); got != nil {
		t.Errorf("空 proxy 应返回 nil，实得 %v", got)
	}
	got := proxycfg.GitArgs("socks5://127.0.0.1:1080")
	want := []string{"-c", "http.proxy=socks5://127.0.0.1:1080"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("GitArgs = %v，期望 %v", got, want)
	}
}

// 凭据纪律的回归测试：代理 URL 里的密码绝不能出现在任何日志文本里。
func TestRedactHidesCredentials(t *testing.T) {
	got := proxycfg.Redact("socks5://alice:s3cr3t@proxy.example:1080")
	if strings.Contains(got, "s3cr3t") {
		t.Fatalf("Redact 泄漏了密码：%q", got)
	}
	if !strings.Contains(got, "proxy.example:1080") {
		t.Errorf("Redact 应保留主机端口（排障要用），实得 %q", got)
	}
	// 无凭据时原样返回，别把好端端的地址也打成星号
	if got := proxycfg.Redact("http://h:8080"); got != "http://h:8080" {
		t.Errorf("无凭据时应原样返回，实得 %q", got)
	}
	if got := proxycfg.Redact(""); got != "" {
		t.Errorf("空串应返回空串，实得 %q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/proxycfg/ -v`
Expected: FAIL —— `no Go files in .../internal/proxycfg`（包还不存在）

- [ ] **Step 3: 写实现**

创建 `internal/proxycfg/proxycfg.go`：

```go
// Package proxycfg 把 handoff 配置里的 proxy 字符串翻译成各消费方要的形态。
//
// 职责：
//   - Validate：取值域校验，供配置加载在启动期硬拒坏值
//   - Transport：给 net/http 用的 *http.Transport
//   - GitArgs：给 git 子进程用的 `-c http.proxy=<url>` 前缀参数
//   - Redact：日志用的脱敏文本
//
// 边界：
//   - 不读配置文件：调用方把字符串给它，它只做翻译
//   - 不碰网络，不判断代理是否可达（那是消费方真发请求时才知道的事）
//   - 不决定「谁该走代理」：协调者↔agentd 那条链路永不走代理，这条纪律由
//     调用方（只有 release 与 agentd 的出网 git 接线）保证，不在本包
package proxycfg

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// SupportedSchemes 是 proxy 允许的 scheme。
//
// 这四种是 net/http 的 Transport 与 git 的 http.proxy **都**原生支持的交集。
// socks4 不在其中：Go 从来没支持过它，配上去的表现是运行期报一句
// "unsupported protocol scheme"，而那时早已过了任何人会看的时刻。
var SupportedSchemes = []string{"http", "https", "socks5", "socks5h"}

// Validate 校验 proxy 取值域。空串合法（= 不配代理）。
//
// 参数：
//   - proxy: 代理地址，形如 socks5://127.0.0.1:1080
//
// 返回：
//   - 错误：URL 畸形、scheme 不在 SupportedSchemes、或缺 host。
//     错误文本一律列出支持的 scheme——只说"不支持"而不说"支持什么"，
//     等于让用户去猜
func Validate(proxy string) error {
	if proxy == "" {
		return nil
	}
	u, err := url.Parse(proxy)
	if err != nil {
		return fmt.Errorf("proxy %q 不是合法 URL: %w（支持 %s）",
			proxy, err, strings.Join(SupportedSchemes, "/"))
	}
	if !schemeSupported(u.Scheme) {
		return fmt.Errorf("proxy %q 的 scheme 为 %q，只支持 %s（裸 host:port 也不行，必须带 scheme）",
			proxy, u.Scheme, strings.Join(SupportedSchemes, "/"))
	}
	if u.Host == "" {
		return fmt.Errorf("proxy %q 缺少主机地址（支持 %s）",
			proxy, strings.Join(SupportedSchemes, "/"))
	}
	return nil
}

func schemeSupported(s string) bool {
	for _, want := range SupportedSchemes {
		if s == want {
			return true
		}
	}
	return false
}

// Transport 按 proxy 造一个 *http.Transport。
//
// 参数：
//   - proxy: 代理地址；**空串 = 不配**，返回的 Transport 沿用
//     http.ProxyFromEnvironment（即 HTTPS_PROXY/HTTP_PROXY/NO_PROXY），
//     与本功能上线前的行为一字不差
//
// 返回：
//   - 可直接塞进 http.Client 的 Transport
//   - 错误：proxy 未通过 Validate
//
// 注意：
//   - 非空 proxy 时**固定返回**该地址，不再看 NO_PROXY。显式配置就是显式意图，
//     而 handoff 自己走代理的出网只有 GitHub 一个域，"某些域不走代理"这个需求
//     在这里不存在
//   - 基于 http.DefaultTransport 克隆，因此连接池、HTTP/2、超时等默认值全部保留；
//     从零 new 一个 &http.Transport{} 会静默丢掉这些，症状是并发下载变慢而无人知晓
func Transport(proxy string) (*http.Transport, error) {
	if err := Validate(proxy); err != nil {
		return nil, err
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// 正常不可能：标准库的 DefaultTransport 就是 *http.Transport。
		// 真发生了说明有人在进程里换掉了它，此时静默用零值 Transport 会丢掉
		// 那个人的意图，如实报错更好
		return nil, fmt.Errorf("http.DefaultTransport 不是 *http.Transport（被第三方替换？）")
	}
	tr := base.Clone()
	if proxy == "" {
		return tr, nil // Clone 已带 ProxyFromEnvironment
	}
	u, err := url.Parse(proxy)
	if err != nil {
		return nil, fmt.Errorf("解析 proxy %q: %w", proxy, err) // Validate 已过，这里防御性兜底
	}
	tr.Proxy = func(*http.Request) (*url.URL, error) { return u, nil }
	return tr, nil
}

// GitArgs 返回给 git 子进程的代理参数，须插在子命令**之前**。
//
// 参数：
//   - proxy: 代理地址；空串返回 nil
//
// 返回：
//   - 形如 []string{"-c", "http.proxy=socks5://127.0.0.1:1080"}
//
// 注意：
//   - git 的 http.proxy 只对 http(s):// 的 remote 生效，**对 ssh:// 与
//     git@host:path 无效**。SSH remote 要走代理得配 ssh 的 ProxyCommand，
//     那会动到用户的 ssh 配置面，不在 handoff 的职责内（见 README）
//   - 用 -c 而不是注入 HTTPS_PROXY 环境变量：不污染子进程环境，也不会让
//     本地 git 操作平白多一个配置
func GitArgs(proxy string) []string {
	if proxy == "" {
		return nil
	}
	return []string{"-c", "http.proxy=" + proxy}
}

// Redact 返回可安全打进日志的代理文本。
//
// 参数：
//   - proxy: 代理地址；空串返回空串
//
// 返回：
//   - 含 user:pass@ 时凭据部分替换为 ***，其余原样；解析不了时**只返回 scheme
//     加省略号**，绝不返回原文——解析失败恰恰是最可能把整串密码原样打出去的场合
//
// 注意：
//   - 这不是可选的美化。代理 URL 常含凭据，本仓纪律见 internal/envfile/resolver.go:64
func Redact(proxy string) string {
	if proxy == "" {
		return ""
	}
	u, err := url.Parse(proxy)
	if err != nil || u.Host == "" {
		return "<无法解析的 proxy 值，已隐藏>"
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	return u.String()
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/proxycfg/ -v`
Expected: PASS（7 个测试全绿）

- [ ] **Step 5: 加关键节点日志**

本包是纯翻译函数，**无 I/O、无外部调用、无状态变更**——按 instrumenting-code 的适用范围，本包自身不加日志。**日志由消费方在调用点打**，且必须经 `Redact`：

- Task 4：CLI 造 transport 后打一行 `slog.Debug("更新链路使用代理", "proxy", proxycfg.Redact(cfg.Proxy))`
- Task 5：agentd bootstrap 注入代理时打 `log.Info("git 出网将使用代理", "proxy", proxycfg.Redact(...))`
- Task 8：自拉开始时把 `Redact` 后的代理带进日志

在 `proxycfg.go` 的 package 注释里补一句说明「本包不打日志，日志由调用方在接线点打并须经 Redact」，避免后来者以为是漏了。

- [ ] **Step 6: 加意图注释**

上面的实现已包含：文件头（职责 + 三条边界）、四个导出函数的 doc 注释、以及五处「为什么」注释（socks4 为何不支持、非空时为何不看 NO_PROXY、为何克隆 DefaultTransport 而非 new、为何用 `-c` 而非环境变量、Redact 解析失败为何不返回原文）。自查这六项都在。

- [ ] **Step 7: Commit**

```bash
go vet ./internal/proxycfg/ && gofmt -l internal/proxycfg/
git add internal/proxycfg/
git commit -m "feat(proxycfg): 新包，把 proxy 配置翻译成 transport/git 参数/脱敏文本"
```

---

## Task 2: `config.Proxy` 字段与启动期校验

**Files:**
- Modify: `internal/config/config.go`（`Config` 结构、`validate`、`decodeStrict` 的提示串）
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `proxycfg.Validate`（Task 1）
- Produces: `Config.Proxy string`（yaml 键 `proxy`）

- [ ] **Step 1: 写失败的测试**

追加到 `internal/config/config_test.go`：

```go
func TestProxyParsedAndValidated(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("proxy: socks5://127.0.0.1:1080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	if cfg.Proxy != "socks5://127.0.0.1:1080" {
		t.Errorf("proxy = %q，期望 socks5://127.0.0.1:1080", cfg.Proxy)
	}
}

// 坏代理必须在**启动期**被拒。运行期容错会让它表现为"后台更新检查什么都不发生"，
// 而那条路径的纪律是失败静默跳过，于是错误配置可以数月无人察觉。
func TestLoadRejectsBadProxy(t *testing.T) {
	for _, bad := range []string{"socks4://h:1080", "127.0.0.1:1080", "http://"} {
		dir := t.TempDir()
		p := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(p, []byte("proxy: \""+bad+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(p); err == nil {
			t.Errorf("proxy=%q 应拒绝加载", bad)
		}
	}
}

// 旧版本兼容契约：未配置时 proxy 键不得落盘，否则旧 agentd 的 KnownFields
// 读到未知键就再也起不来（与 path_dirs 同款教训）。
func TestProxyOmitEmptyOnSave(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if _, err := config.Load(p); err != nil { // 首次运行写盘
		t.Fatalf("首次加载: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "proxy") {
		t.Errorf("未配置时 proxy 不得落盘，实得:\n%s", b)
	}
}

// 未知键的错误提示必须把 proxy 列进"支持的键"，否则用户配对了却被拒时无从判断。
func TestUnknownKeyErrorMentionsProxy(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("nosuchkey: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(p)
	if err == nil {
		t.Fatal("未知键应被拒")
	}
	if !strings.Contains(err.Error(), "proxy") {
		t.Errorf("错误文本应列出 proxy，实得 %q", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/config/ -run 'Proxy' -v`
Expected: FAIL —— `cfg.Proxy undefined`（编译错）

- [ ] **Step 3: 写实现**

在 `internal/config/config.go` 的 `Config` 结构里，紧跟 `PathDirs` 之后加：

```go
	// Proxy 是 handoff **自身**出网时使用的代理地址，形如 http://host:port、
	// https://host:port、socks5://host:port、socks5h://host:port。
	// 空 = 不配，沿用 HTTPS_PROXY/HTTP_PROXY/NO_PROXY 环境变量（现行为不变）。
	//
	// 作用范围只有两处：更新链路的 HTTP 出网（查 release、下资产）与 agentd 的
	// git clone/fetch。**不作用于协调者↔agentd 链路**——那是 LAN/loopback 地址，
	// 代理化轻则每次请求多绕一跳，重则 socks5 代理解析不了 100.x.y.z 直接断链，
	// 而这条链路的可达性是 handoff 的命根子。也**不作用于 executor**：executor
	// 的出网归 env 段（B19），两者故障域不交叉——代理挂了只影响升级，不影响任务执行。
	//
	// 为什么放顶层而不是放进 Target：它描述的是「**这台机器**怎么出网」，
	// 与 RepoRoot / PathDirs 同一个道理。
	//
	// omitempty 是硬要求，不是风格：配置以 KnownFields(true) 严格解析，未知键让
	// agentd **启动失败**。没有 omitempty 时，新版 Save 会把 proxy: "" 写进
	// 每一台机器的 config.yaml，而一台还没换版的旧 agentd 读到它就再也起不来了
	//（PathDirs 同款）。
	Proxy string `yaml:"proxy,omitempty"`
```

在 `validate()` 的 `StallTimeout` 检查之后加：

```go
	// 坏代理必须在启动期硬拒。运行期容错的后果是：后台更新检查那条路径的纪律
	// 是「任何一步失败都静默跳过」（它挂在每条命令上，自己不能成为故障源），
	// 于是一个拼错的代理表现为**什么都不发生**，可以存在数月而无人察觉。
	// 与 approver.blacklist 的正则在启动期编译校验是同一条纪律。
	if err := proxycfg.Validate(c.Proxy); err != nil {
		return err
	}
```

import 加 `"github.com/xushixin/handoff/internal/proxycfg"`。

`decodeStrict` 的提示串把 `path_dirs/` 后面补上 `proxy/`：

```go
		return fmt.Errorf("配置包含未知字段（支持: listen/token/datadir/repo_root/path_dirs/proxy/stalltimeout/targets{addr,user,token}/approver{executor,model,timeout,blacklist}/executor{default,model}/terminal{auto}/sync{auto}/env{<agent>: <文件名>}）: %w；旧版 access_key/secret_key 等键已废弃，请删除未知键或升级配置", err)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/config/ -count=1 -v`
Expected: PASS（含既有全部测试——特别确认 `TestLoadRejectsUnknownKeys` 与 `TestUnknownFieldMessageOmitsUpdate` 仍绿）

- [ ] **Step 5: 加关键节点日志**

在 `Load` 里，`validate` 通过之后、`return cfg` 之前加一行——**必须过 `Redact`**：

```go
	if cfg.Proxy != "" {
		// 只打脱敏值：代理 URL 常含 user:pass@（envfile/resolver.go:64 同款纪律）
		log().Info("已配置出网代理", "proxy", proxycfg.Redact(cfg.Proxy))
	}
```

为什么是 Info 不是 Debug：「这台机器的出网走不走代理」是排查更新失败时的第一个问题，且每进程只打一次，不构成噪音。

- [ ] **Step 6: 加意图注释**

Step 3 的字段注释已含：作用范围、两条不作用的链路及其理由、为何放顶层、omitempty 为何是硬要求。`validate` 的新分支已含「为何启动期硬拒」。自查这两处都在。

- [ ] **Step 7: Commit**

```bash
go vet ./internal/config/ && gofmt -l internal/config/
git add internal/config/
git commit -m "feat(config): 加 proxy 配置项，启动期校验取值域"
```

---

## Task 3: `release` 收 RoundTripper，新增 `AssetURL` / `FetchChecksum` / `FetchByTag`

**Files:**
- Modify: `internal/release/client.go`、`internal/release/install.go`
- Modify: `internal/release/client_test.go`、`internal/release/install_test.go`（跟随签名）
- Modify: `internal/agentd/server.go:104`、`cmd/upgrade.go:68-69`、`cmd/root.go:265`（**仅跟随签名传 nil**，真正接线在 Task 4/8）

**Interfaces:**
- Consumes: 无新增
- Produces:
  - `func NewClient(tr http.RoundTripper) *Client`
  - `func NewInstaller(log *slog.Logger, tr http.RoundTripper) *Installer`
  - `func AssetURL(repo, tag, name string) string`
  - `func (i *Installer) FetchChecksum(ctx context.Context, rel Release, goos, goarch string) (string, error)`
  - `func (i *Installer) FetchByTag(ctx context.Context, repo, tag, goos, goarch, wantSum string) ([]byte, error)`

- [ ] **Step 1: 写失败的测试**

追加到 `internal/release/install_test.go`：

```go
// countingRT 数一共发了几个请求，并把全部请求转给内部的真实 transport。
type countingRT struct {
	n    int
	base http.RoundTripper
}

func (c *countingRT) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n++
	return c.base.RoundTrip(r)
}

// 传进来的 transport 必须真的被用上——否则代理配了等于没配，
// 而症状是"配了代理还是连不上"，没人会想到是接线断了。
func TestInstallerUsesGivenTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	defer srv.Close()
	rt := &countingRT{base: http.DefaultTransport}
	i := NewInstaller(quietLog(), rt)
	if _, err := i.get(context.Background(), srv.URL); err != nil {
		t.Fatalf("get: %v", err)
	}
	if rt.n != 1 {
		t.Fatalf("传入的 transport 未被使用，请求数 = %d", rt.n)
	}
}

func TestClientUsesGivenTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v9.9.9","assets":[]}`))
	}))
	defer srv.Close()
	rt := &countingRT{base: http.DefaultTransport}
	c := NewClient(rt)
	c.APIBase = srv.URL
	if _, err := c.Latest(context.Background()); err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rt.n != 1 {
		t.Fatalf("传入的 transport 未被使用，请求数 = %d", rt.n)
	}
}

// nil transport 必须与改造前行为一致（默认 transport，认环境变量）。
func TestNilTransportKeepsDefault(t *testing.T) {
	if NewInstaller(quietLog(), nil).HTTP.Transport != nil {
		t.Error("nil 应保持 http.Client 的零值 Transport（即 http.DefaultTransport）")
	}
	if NewClient(nil).HTTP.Transport != nil {
		t.Error("nil 应保持 http.Client 的零值 Transport（即 http.DefaultTransport）")
	}
}

// 资产下载地址是确定性的，不需要查 API 就能拼出来。
// 这是 agentd 自拉不打 api.github.com 的前提（后者有 60 次/小时/IP 匿名限流，
// 而多台执行机很可能共用一个代理出口 IP）。
func TestAssetURL(t *testing.T) {
	got := AssetURL("Xsxdot/handoff", "v0.2.3", "handoff_v0.2.3_linux_amd64.tar.gz")
	want := "https://github.com/Xsxdot/handoff/releases/download/v0.2.3/handoff_v0.2.3_linux_amd64.tar.gz"
	if got != want {
		t.Errorf("AssetURL = %q，期望 %q", got, want)
	}
}

// FetchChecksum 只下 checksums.txt，**不下资产**——自拉模式下协调者靠它
// 拿 sha256 下发，20MB 的资产由执行机自己去下。
func TestFetchChecksumDownloadsOnlyChecksums(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Write([]byte("abc123  handoff_v1.0.0_linux_amd64.tar.gz\n"))
	}))
	defer srv.Close()
	rel := Release{Tag: "v1.0.0", Assets: []Asset{
		{Name: "handoff_v1.0.0_linux_amd64.tar.gz", URL: srv.URL + "/asset"},
		{Name: ChecksumsName, URL: srv.URL + "/checksums"},
	}}
	sum, err := NewInstaller(quietLog(), nil).FetchChecksum(context.Background(), rel, "linux", "amd64")
	if err != nil {
		t.Fatalf("FetchChecksum: %v", err)
	}
	if sum != "abc123" {
		t.Errorf("sum = %q，期望 abc123", sum)
	}
	if len(paths) != 1 || paths[0] != "/checksums" {
		t.Errorf("只应请求 checksums，实得 %v", paths)
	}
}

// FetchByTag 拼 URL 自己下，并用**传进来的** sum 校验（协调者下发的那个）。
func TestFetchByTagVerifiesGivenSum(t *testing.T) {
	payload := []byte("fake-archive")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()
	i := NewInstaller(quietLog(), nil)
	i.DownloadBase = srv.URL // 测试缝：把 github.com 换成本地服务

	got, err := i.FetchByTag(context.Background(), "o/r", "v1.0.0", "linux", "amd64",
		hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("FetchByTag: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("下到的字节不对")
	}

	// sum 不符必须失败——这是「协调者下发 sum」这条信任链的落点：
	// agentd 侧的代理/镜像被投毒时就在这里被抓住
	if _, err := i.FetchByTag(context.Background(), "o/r", "v1.0.0", "linux", "amd64",
		strings.Repeat("0", 64)); err == nil {
		t.Error("sha256 不符时 FetchByTag 必须失败")
	}
}
```

测试文件 import 需补 `bytes`、`crypto/sha256`、`encoding/hex`、`net/http`、`net/http/httptest`、`strings`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/release/ -count=1`
Expected: FAIL —— `too many arguments in call to NewInstaller`、`undefined: AssetURL` 等编译错

- [ ] **Step 3: 写实现**

`internal/release/client.go`：

```go
// DownloadBase 是 release 资产的下载根（GitHub 的确定性地址）。
//
// D11 同理：自动更新链路一律打 GitHub 原生地址，不走自有域名。
const DownloadBase = "https://github.com"

// AssetURL 拼一个 release 资产的下载地址。
//
// 参数：
//   - repo: owner/name，如 Xsxdot/handoff
//   - tag: 版本号，形如 v0.2.3
//   - name: 资产文件名，用 AssetName 生成
//
// 返回：
//   - 完整下载地址
//
// 注意：
//   - GitHub 的这个地址是**确定性**的，不需要先查 API 就能拼出来。agentd
//     自拉时用的正是它——api.github.com 有 60 次/小时/IP 的匿名限流，
//     而多台执行机很可能共用一个代理出口 IP，走 API 迟早互相打架
func AssetURL(repo, tag, name string) string {
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", DownloadBase, repo, tag, name)
}

// NewClient 构造 release 查询 client：30s 超时，打 GitHub 官方端点。
//
// 参数：
//   - tr: HTTP transport；**nil = 用标准库默认**（认 HTTPS_PROXY 等环境变量），
//     与本参数加入前的行为一字不差。要走配置里的代理，传 proxycfg.Transport 的产物
//
// 注意：
//   - 本包不读 handoff 配置（见 package 注释），所以收的是造好的 transport
//     而不是配置字符串——这条边界是刻意的，别"顺手"改成传 *config.Config
//   - 30s 而不是更长：查版本是一个可以失败的后台动作（失败就等下一个 interval），
//     卡住一个 goroutine 几分钟没有任何好处
func NewClient(tr http.RoundTripper) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second, Transport: tr},
		APIBase: DefaultAPIBase,
		Repo:    DefaultRepo,
	}
}
```

`internal/release/install.go`：

```go
// Installer 执行下载与安装。
type Installer struct {
	HTTP *http.Client
	Log  *slog.Logger
	// DownloadBase 是资产下载根，默认 release.DownloadBase。
	// 存在的唯一理由是可测性：不覆盖它，FetchByTag 的测试必须真的打 github.com
	DownloadBase string
}

// NewInstaller 构造默认 installer（10 分钟超时，覆盖慢网下的 20MB 下载）。
//
// 参数：
//   - log: 日志入口
//   - tr: HTTP transport；**nil = 用标准库默认**（认 HTTPS_PROXY 等环境变量）
func NewInstaller(log *slog.Logger, tr http.RoundTripper) *Installer {
	return &Installer{
		HTTP:         &http.Client{Timeout: 10 * time.Minute, Transport: tr},
		Log:          log,
		DownloadBase: DownloadBase,
	}
}

// FetchChecksum 只下载 checksums.txt 并解出某平台资产的期望哈希。
//
// 参数：
//   - ctx: 上下文
//   - rel: 目标发布（需要它的 Assets 里有 checksums.txt 的 URL）
//   - goos / goarch: 目标机器的平台
//
// 返回：
//   - 该平台资产的 sha256（十六进制小写）
//   - 错误：缺 checksums 资产、下载失败、文件里没有该资产的行
//
// 注意：
//   - **不下资产**。这正是自拉模式的省流量点：协调者只下几百字节的 checksums，
//     20MB 的资产由执行机自己去下（spec §5.5）
//   - 一次 upgrade --now 涉及多台机器时，调用方应当只调一次并缓存——
//     同一个 release 的 checksums.txt 对所有平台是同一份
func (i *Installer) FetchChecksum(ctx context.Context, rel Release, goos, goarch string) (string, error) {
	ck, ok := rel.Checksums()
	if !ok {
		return "", fmt.Errorf("发布 %s 没有 %s，无法校验完整性", rel.Tag, ChecksumsName)
	}
	i.Log.Info("下载校验和文件", "tag", rel.Tag, "url", ck.URL)
	sums, err := i.get(ctx, ck.URL)
	if err != nil {
		i.Log.Error("下载校验和文件失败", "tag", rel.Tag, "url", ck.URL, "cause", err)
		return "", fmt.Errorf("下载 %s: %w", ChecksumsName, err)
	}
	sum, err := sumFor(string(sums), AssetName(rel.Tag, goos, goarch))
	if err != nil {
		i.Log.Error("校验和文件里没有该平台的行", "tag", rel.Tag,
			"platform", goos+"/"+goarch, "cause", err)
		return "", err
	}
	i.Log.Info("取得校验和", "tag", rel.Tag, "platform", goos+"/"+goarch, "sha256", sum)
	return sum, nil
}

// FetchByTag 按 tag 拼出下载地址、下载资产并用**给定的** sha256 校验。
//
// 参数：
//   - ctx: 上下文
//   - repo: owner/name
//   - tag: 目标版本
//   - goos / goarch: 本机平台
//   - wantSum: 期望的 sha256（十六进制小写）。自拉模式下**来自协调者下发**
//
// 返回：
//   - 资产原文（tar.gz / zip 字节，未解包）
//   - 错误：下载失败、sha256 不符
//
// 注意：
//   - 与 FetchArchive 的区别是**不需要 Release 对象、不查 API**：地址由
//     AssetURL 确定性拼出，wantSum 由调用方给。这让执行机完全不碰
//     api.github.com（避开 60 次/小时/IP 的匿名限流）
//   - wantSum 由调用方给而不是自己去取 checksums，是刻意的：校验和与资产
//     走两条不同的信任路径，本机代理/镜像被投毒时才抓得住（spec §5.5）。
//     **别"优化"成自己下 checksums**
//   - 不重试：完整性失败重试只会重下同一份坏数据（spec §4.7）
func (i *Installer) FetchByTag(ctx context.Context, repo, tag, goos, goarch, wantSum string) ([]byte, error) {
	base := i.DownloadBase
	if base == "" {
		base = DownloadBase
	}
	name := AssetName(tag, goos, goarch)
	url := fmt.Sprintf("%s/%s/releases/download/%s/%s", base, repo, tag, name)

	i.Log.Info("开始下载资产", "tag", tag, "platform", goos+"/"+goarch, "asset", name, "url", url)
	b, err := i.get(ctx, url)
	if err != nil {
		i.Log.Error("下载资产失败", "tag", tag, "url", url, "cause", err)
		return nil, fmt.Errorf("下载 %s: %w", name, err)
	}
	got := sha256.Sum256(b)
	if hex.EncodeToString(got[:]) != wantSum {
		i.Log.Error("资产校验不通过", "tag", tag, "asset", name,
			"want", wantSum, "got", hex.EncodeToString(got[:]), "bytes", len(b))
		return nil, fmt.Errorf("sha256 校验不通过（期望 %s，实得 %s）", wantSum, hex.EncodeToString(got[:]))
	}
	i.Log.Info("资产校验通过", "tag", tag, "asset", name, "sha256", wantSum, "bytes", len(b))
	return b, nil
}
```

跟随签名改三处调用点（**本 task 只传 nil，真正接线在 Task 4/8**）：

- `internal/agentd/server.go:104` → `release.NewInstaller(log, nil)`
- `cmd/upgrade.go:68-69` → `release.NewClient(nil)` / `release.NewInstaller(slog.Default(), nil)`
- `cmd/root.go:265` → `release.NewClient(nil)`

以及 `internal/release/install_test.go` 里既有 8 处 `NewInstaller(...)` 调用补第二个参数 `nil`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./... && go test ./internal/release/ ./cmd/ ./internal/agentd/ -count=1`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

Step 3 的两个新方法已按 instrumenting-code 要求打全：外部调用前后各一条 Info（`下载校验和文件` / `取得校验和`、`开始下载资产` / `资产校验通过`），三条错误分支各一条 Error 带 tag/url/cause 上下文，**成功路径不静默**。自查：`FetchChecksum` 与 `FetchByTag` 的每个 `return err` 前都有一条 Error。

- [ ] **Step 6: 加意图注释**

已含：`AssetURL` 为何不查 API、`NewClient` 的 tr 参数语义与「别改成传 config」的边界、`FetchChecksum` 为何不下资产与缓存提示、`FetchByTag` 的 wantSum 为何由调用方给（附「别优化成自己下 checksums」的警告）、`DownloadBase` 字段为何存在。

- [ ] **Step 7: Commit**

```bash
go vet ./internal/release/ && gofmt -l internal/release/
git add internal/release/ internal/agentd/server.go cmd/upgrade.go cmd/root.go
git commit -m "feat(release): 收 RoundTripper；新增 AssetURL/FetchChecksum/FetchByTag"
```

---

## Task 4: CLI 更新链路接上代理

**Files:**
- Modify: `cmd/root.go`（`updateCheckCmd` 的 RunE）
- Modify: `cmd/upgrade.go`（两个测试缝 `newReleaseChecker` / `newReleaseFetcher`）
- Test: `cmd/upgrade_test.go`

**Interfaces:**
- Consumes: `proxycfg.Transport`（Task 1）、`config.Config.Proxy`（Task 2）、`release.NewClient(tr)` / `NewInstaller(log, tr)`（Task 3）
- Produces: `func proxyTransport(cfg *config.Config) http.RoundTripper`（`cmd` 包内，unexported）

- [ ] **Step 1: 写失败的测试**

追加到 `cmd/upgrade_test.go`（包名按该文件现有的来）：

```go
// 配置里的代理必须真的传进 release 的 client。断言打在 Transport 的 Proxy
// 行为上而不是指针相等——函数值不可比较，且我们要的本来就是行为。
func TestProxyTransportUsesConfig(t *testing.T) {
	rt := proxyTransport(&config.Config{Proxy: "socks5://127.0.0.1:1080"})
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("期望 *http.Transport，实得 %T", rt)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/x", nil)
	u, err := tr.Proxy(req)
	if err != nil || u == nil || u.Host != "127.0.0.1:1080" {
		t.Fatalf("代理未接上，proxy=%v err=%v", u, err)
	}
}

// 未配代理时必须返回 nil（= 标准库默认 transport），而不是一个"什么都不代理"
// 的自造 transport——后者会丢掉 DefaultTransport 的连接池与 HTTP/2 设置。
func TestProxyTransportNilWhenUnset(t *testing.T) {
	if rt := proxyTransport(&config.Config{}); rt != nil {
		t.Fatalf("未配代理时应返回 nil，实得 %T", rt)
	}
}

// 坏代理不得让 CLI 崩：配置校验已经在 Load 时挡过一道，这里是纵深防御，
// 走到这儿只可能是有人绕过 Load 直接构造了 Config。降级为不用代理并打日志。
func TestProxyTransportBadValueDegrades(t *testing.T) {
	if rt := proxyTransport(&config.Config{Proxy: "socks4://h:1"}); rt != nil {
		t.Fatalf("坏代理应降级为 nil，实得 %T", rt)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run 'ProxyTransport' -v`
Expected: FAIL —— `undefined: proxyTransport`

- [ ] **Step 3: 写实现**

在 `cmd/upgrade.go` 里加（放在 `currentBinary` 附近）：

```go
// proxyTransport 按配置造更新链路用的 HTTP transport。
//
// 参数：
//   - cfg: 已加载的配置
//
// 返回：
//   - 配了代理的 *http.Transport；**未配代理或配置有问题时返回 nil**，
//     调用方把 nil 直接传给 release.NewClient/NewInstaller 即为标准库默认行为
//
// 注意：
//   - 坏代理走到这里只可能是绕过了 config.Load 的校验（那里已经硬拒过一道）。
//     此时降级为不用代理并打 Error，而不是 panic 或让整条命令失败——
//     升级链路本身不该因为一个附属设置而彻底不可用
func proxyTransport(cfg *config.Config) http.RoundTripper {
	if cfg == nil || cfg.Proxy == "" {
		return nil
	}
	tr, err := proxycfg.Transport(cfg.Proxy)
	if err != nil {
		slog.Default().Error("代理配置无法使用，本次出网不走代理",
			"proxy", proxycfg.Redact(cfg.Proxy), "cause", err)
		return nil
	}
	slog.Default().Info("更新链路使用代理", "proxy", proxycfg.Redact(cfg.Proxy))
	return tr
}
```

`cmd/upgrade.go` 的两个缝改成读配置：

```go
	newReleaseChecker = func() releaseChecker {
		return release.NewClient(proxyTransport(loadCLIConfig()))
	}
	newReleaseFetcher = func() releaseFetcher {
		return release.NewInstaller(slog.Default(), proxyTransport(loadCLIConfig()))
	}
```

`cmd/root.go:265` 的 `updateCheckCmd`（该处已有 `cfg`）：

```go
		rel, err := release.NewClient(proxyTransport(cfg)).Latest(cmd.Context())
```

import 补 `net/http`、`github.com/xushixin/handoff/internal/proxycfg`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -count=1`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

Step 3 已含两条：成功接上代理时 Info（脱敏值）、坏代理降级时 Error（带 cause）。这里**不要**再往 `Latest()` 调用前后加日志——`release` 包内部已经打了（Task 3），重复打会让一次查版本在日志里出现四行。

- [ ] **Step 6: 加意图注释**

已含 `proxyTransport` 的 doc 注释（三个返回语义 + 坏值为何降级而不是失败）。另在 `newReleaseChecker` 那两行上方补一句：

```go
	// 每次调用重新 loadCLIConfig：这两个缝在测试里会被整体替换，生产路径上
	// 一条命令最多调一两次，重读一次 YAML 的代价远小于把配置提到包级变量后
	// 与 --config 标志的求值时序纠缠
```

- [ ] **Step 7: Commit**

```bash
go vet ./cmd/ && gofmt -l cmd/
git add cmd/
git commit -m "feat(cmd): 更新链路接上配置里的代理（upgrade 与后台 update-check）"
```

---

## Task 5: agentd 的 git clone/fetch 走代理

**Files:**
- Modify: `internal/agentd/workspace.go`（新增 `gitRunNet`；`fetch --all --prune` 改走它）
- Modify: `internal/agentd/projectadmin.go:359`（`clone` 改走 `gitRunNet`）
- Modify: `internal/agentd/server.go`（bootstrap 注入代理）
- Test: `internal/agentd/workspace_test.go`

**Interfaces:**
- Consumes: `proxycfg.GitArgs` / `proxycfg.Redact`（Task 1）、`config.Config.Proxy`（Task 2）
- Produces:
  - `func SetGitProxy(proxy string)`（`agentd` 包导出，供 bootstrap 注入）
  - `func gitRunNet(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error)`（包内）

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/workspace_test.go`：

```go
// 出网 git 必须带上 -c http.proxy，且它要排在子命令**之前**——
// git 的 -c 是全局选项，放到子命令后面 git 会当成子命令的参数直接报错。
func TestGitNetArgsCarryProxyBeforeSubcommand(t *testing.T) {
	SetGitProxy("socks5://127.0.0.1:1080")
	defer SetGitProxy("")

	got := gitNetArgs("clone", "--", "url", "dest")
	if len(got) < 3 {
		t.Fatalf("参数太少: %v", got)
	}
	if got[0] != "-c" || got[1] != "http.proxy=socks5://127.0.0.1:1080" {
		t.Fatalf("代理参数不在最前: %v", got)
	}
	if got[2] != "clone" {
		t.Fatalf("子命令应紧跟在代理参数之后: %v", got)
	}
}

// 未配代理时参数一字不变——不能让所有没配代理的机器平白多两个参数。
func TestGitNetArgsUnchangedWithoutProxy(t *testing.T) {
	SetGitProxy("")
	got := gitNetArgs("fetch", "--all", "--prune")
	want := []string{"fetch", "--all", "--prune"}
	if len(got) != len(want) {
		t.Fatalf("gitNetArgs = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("gitNetArgs = %v，期望 %v", got, want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'GitNetArgs' -v`
Expected: FAIL —— `undefined: SetGitProxy` / `undefined: gitNetArgs`

- [ ] **Step 3: 写实现**

在 `internal/agentd/workspace.go` 的 `gitRun` 下方加：

```go
// gitProxy 是本机出网 git（clone/fetch）使用的代理地址，由 agentd bootstrap
// 经 SetGitProxy 注入一次。
//
// 为什么是包级变量而不是把 proxy 串进签名：ResolveBaseline 等函数是包级函数，
// 调用链上大部分环节与网络无关，把 proxy 串进每个签名会污染一大片无关代码。
// 这与本包 log() 用运行时取值而非依赖注入是同一个权衡。
var gitProxy string

// SetGitProxy 设置出网 git 使用的代理，由 agentd bootstrap 调用一次。
//
// 参数：
//   - proxy: 代理地址；空串 = 不用代理
//
// 注意：
//   - 只影响 clone / fetch 两处出网操作，本地 git 操作（rev-parse/status/
//     worktree/diff…）一律不带代理——它们根本不出网，带上只会平白多一个配置
//   - 非并发安全：只在启动期调用一次。测试里改它必须串行
func SetGitProxy(proxy string) { gitProxy = proxy }

// gitNetArgs 在 git 参数前插入代理参数。
//
// 代理参数必须排在子命令**之前**：git 的 -c 是全局选项，放到子命令后面
// git 会把它当成子命令的参数并直接报错。
func gitNetArgs(args ...string) []string {
	p := proxycfg.GitArgs(gitProxy)
	if len(p) == 0 {
		return args
	}
	return append(p, args...)
}

// gitRunNet 执行**会出网**的 git 操作（clone / fetch），自动带上代理参数。
//
// 参数与返回同 gitRun。
//
// 注意：
//   - 只有 clone 与 fetch 该用它。给本地操作用不会出错，但会让「哪些操作出网」
//     这个信息从代码里消失，下一个人就无从判断代理配错会影响哪些功能
//   - git 的 http.proxy 对 ssh:// 与 git@host:path 的 remote **无效**。
//     SSH remote 要走代理得配 ssh 的 ProxyCommand，那会动到用户的 ssh 配置面，
//     不在 handoff 职责内；README 给出改用 HTTPS remote（insteadOf）的解法
func gitRunNet(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error) {
	if gitProxy != "" {
		log().Info("git 出网操作将走代理", "repo", repo, "args", args,
			"proxy", proxycfg.Redact(gitProxy))
	}
	return gitRun(ctx, repo, gitNetArgs(args...)...)
}
```

import 补 `github.com/xushixin/handoff/internal/proxycfg`。

改两个调用点：

- `internal/agentd/workspace.go:789`：`gitRun(fctx, repo, "fetch", "--all", "--prune")` → `gitRunNet(fctx, repo, "fetch", "--all", "--prune")`
- `internal/agentd/projectadmin.go:359`：`gitRun(ctx, parent, "clone", "--", req.OriginURL, dest)` → `gitRunNet(ctx, parent, "clone", "--", req.OriginURL, dest)`

在 `cmd/agentd.go` 的 bootstrap（`NewServer` 之前，配置已加载处）加：

```go
	// git 出网代理必须在任何 clone/fetch 之前注入。放在 NewServer 之前而不是
	// 之后：自动登记（B62）的 clone 可能在服务起来后的第一个请求就发生
	agentd.SetGitProxy(cfg.Proxy)
	if cfg.Proxy != "" {
		log.Info("git 出网将使用代理", "proxy", proxycfg.Redact(cfg.Proxy))
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

Step 3 已含：`gitRunNet` 在带代理时打一条 Info（脱敏），bootstrap 注入时打一条 Info。**不重复打失败日志**——`gitRun` 内部已经有完整的失败 Error（带 stderr 原文），再包一层只会让每次 git 失败出现两条 Error。

- [ ] **Step 6: 加意图注释**

已含：包级变量为何不用依赖注入、`SetGitProxy` 的非并发安全约束、代理参数为何必须在子命令之前、`gitRunNet` 只该给出网操作用的理由、SSH remote 的限制、bootstrap 里注入时机为何在 `NewServer` 之前。

- [ ] **Step 7: Commit**

```bash
go vet ./internal/agentd/ ./cmd/ && gofmt -l internal/agentd/ cmd/
git add internal/agentd/ cmd/agentd.go
git commit -m "feat(agentd): git clone/fetch 走配置里的代理，本地 git 操作不受影响"
```

---

## Task 6: 协议扩展（`proto`）

**Files:**
- Modify: `internal/proto/status.go`（`UpdateStatus`）
- Modify: `internal/proto/update.go`（`UpdateResp`、reason 常量、mode 常量）
- Test: `internal/proto/update_test.go`（新建或追加）

**Interfaces:**
- Consumes: 无
- Produces:
  - `proto.UpdateStatus.Pull *bool`、`proto.UpdateStatus.PullState *PullState`
  - `type PullState struct { Tag, Stage, Error string; StartedAt, UpdatedAt time.Time }`
  - `proto.PullStage{Downloading,Verifying,Installing,Failed}` 四个常量
  - `proto.UpdateResp.Accepted bool`
  - `proto.UpdateReasonPullInProgress = "pull_in_progress"`
  - `proto.UpdateModePull = "pull"` / `proto.UpdateModePush = "push"`

- [ ] **Step 1: 写失败的测试**

创建 `internal/proto/update_test.go`：

```go
package proto_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// Pull 与 PullState 都必须 omitempty：老 CLI 解到未知字段无所谓，但新 CLI
// 拿到 nil 要能分辨"对端没给"（老 agentd）与"对端说 false"。
func TestUpdateStatusOmitsPullWhenAbsent(t *testing.T) {
	b, err := json.Marshal(proto.UpdateStatus{Managed: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "pull") {
		t.Errorf("未设置时不得出现 pull 字段，实得 %s", b)
	}
}

func TestUpdateStatusPullRoundTrip(t *testing.T) {
	yes := true
	in := proto.UpdateStatus{Managed: true, Pull: &yes}
	b, _ := json.Marshal(in)
	var out proto.UpdateStatus
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Pull == nil || !*out.Pull {
		t.Fatalf("pull 未往返，实得 %v", out.Pull)
	}
}

// 老 agentd 的响应里没有 pull 字段，解出来必须是 nil 而不是 false。
// 这条区分是选路的判据：nil = 对端过旧，降级推送。
func TestUpdateStatusLegacyDecodesToNilPull(t *testing.T) {
	var out proto.UpdateStatus
	if err := json.Unmarshal([]byte(`{"managed":true}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.Pull != nil {
		t.Fatalf("老 agentd 的响应应解出 nil pull，实得 %v", *out.Pull)
	}
}

func TestUpdateRespAcceptedRoundTrip(t *testing.T) {
	b, _ := json.Marshal(proto.UpdateResp{OK: true, Accepted: true, Version: "v1.0.0"})
	var out proto.UpdateResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Accepted || out.Version != "v1.0.0" {
		t.Fatalf("accepted/version 未往返: %+v", out)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/proto/ -count=1`
Expected: FAIL —— `unknown field Pull` / `unknown field Accepted`

- [ ] **Step 3: 写实现**

`internal/proto/status.go` 的 `UpdateStatus` 改为：

```go
// UpdateStatus 是这台 agentd 与「换版」有关的状态。
//
// 字段说明：
//   - Managed: 当前 agentd 进程是不是被进程管理器（systemd / launchd）拉起的。
//     **false 时换版被硬拒绝**——换完 exit(0) 之后没人拉起，这台机器上就此
//     没有 agentd 在跑，且没有任何信号告诉任何人。`--force` 也不越过这一条
//   - Pull: 本 agentd 支持「自拉换版」（POST /api/update?mode=pull）
//   - PullState: 最近一次自拉的状态；nil = 本进程还没自拉过，或上一次已成功
//     （成功的终点是重启，状态随进程一起消失）
//
// 为什么没有「待命版本」了：B59 取消了「下载完等空闲窗口再换」的自主决策，
// 换版由操作者一条命令触发并当场完成，中间不存在待命态（见 B59 spec D1）。
type UpdateStatus struct {
	Managed bool `json:"managed"`

	// Pull 表示对端支持自拉换版。
	//
	// 为什么是指针：nil 表示**对端没给这个字段**（老 agentd 不上报），与
	// 「对端说 false」是两回事。这条区分是选路判据——老 agentd 收到
	// mode=pull + 空 body 会掉进「纯重启」分支并回 200，CLI 若据此以为
	// 受理了，就会干等到超时报「已换版但新进程未上线」，一次纯误导。
	// 与同结构族里 BuildInfo.Platform 空串、ActiveTask.Watchers *int 同款纪律。
	Pull *bool `json:"pull,omitempty"`

	// PullState 是最近一次自拉换版的状态，仅存内存、不落盘。
	//
	// 为什么没有 done 态：成功路径的终点是**进程重启**，状态自然消失——
	// 而那时 status 报的版本号已经变了，调用方靠版本号就能确认。一个落盘的
	// done 会在下次启动时变成误导性的陈旧数据。失败时进程不重启，状态留在
	// 内存里可查，这正是需要它的场合。
	PullState *PullState `json:"pull_state,omitempty"`
}

// PullState 是一次自拉换版的进度与结局。
type PullState struct {
	Tag   string `json:"tag"`
	Stage string `json:"stage"` // PullStage* 之一
	// Error 是 Stage=failed 时的原文。**必须带原文**：调用方拿到它才能
	// 直接看到 "proxyconnect tcp: dial tcp 127.0.0.1:1080: connection refused"
	// 这种一眼定位的信息，而不是一句「版本仍是 X」
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// 自拉的阶段取值。
//
// 只有三个：没有 "done"（见 UpdateStatus.PullState 的注释——成功的终点是重启），
// 也没有单独的 "verifying"（sha256 比对与解包后自检都发生在 installing 内部）。
// **不要为了让阶段看起来更完整而加一个实现从不产出的取值**——消费方会写死
// 代码去处理它，而那段代码永远不会被执行，也永远不会被测到。
const (
	PullStageDownloading = "downloading"
	PullStageInstalling  = "installing"
	PullStageFailed      = "failed"
)
```

`internal/proto/update.go`：

```go
// 换版模式，走 query 参数 mode。
//
// 为什么要显式 mode 而不靠「tag 有没有」隐式判别：现有判别已经压在
// 「body 空不空」这一个维度上，再叠一层「tag 有没有」，三种模式的判据就散在
// 两个维度上，加第四种时必然出错。显式 mode 还让新旧 agentd 的分歧点变成
// 一个可测的单点。
const (
	// UpdateModePull: 只下发 tag + sha256，由 agentd 自己去下载（body 必须为空）
	UpdateModePull = "pull"
	// UpdateModePush: 协调者推 tar.gz 原文（body 必须非空）。省略 mode 且 body
	// 非空等价于本模式，这是为了让老 CLI 的请求继续被正确处理
	UpdateModePush = "push"
)
```

`UpdateResp` 加字段：

```go
type UpdateResp struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"` // 换上的版本；纯重启模式为空
	Prev    string `json:"prev,omitempty"`    // 旧二进制留存路径，回滚要用

	// Accepted 表示这次请求只是被**受理**，换版还没发生（自拉模式，202）。
	// 与 Restarted 的区别是时态：Restarted 说"我这就重启"，Accepted 说
	// "我开始下载了，结果去 status 里看"。调用方据此决定是直接等版本号变，
	// 还是要一路盯着 pull_state
	Accepted bool `json:"accepted,omitempty"`

	Restarted bool `json:"restarted"`
}
```

reason 常量补：

```go
	// UpdateReasonPullInProgress: 已有一个自拉在跑。force 不越过——两个自拉
	// 会往同一个临时文件路径（release.TempName(tag) 是确定性的）写，
	// 互相截断出一个坏二进制，而 Activate 会把它装上去
	UpdateReasonPullInProgress = "pull_in_progress"
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/proto/ -count=1 && go build ./...`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

`proto` 是纯数据包（package 注释明写「只有数据，无行为、无 I/O」），**不加日志**。消费方的日志在 Task 7/8/9/10。

- [ ] **Step 6: 加意图注释**

已含：`Pull` 为何是指针（连同它防的那个具体误导）、`PullState` 为何只在内存且没有 done 态、`PullState.Error` 为何必须带原文、`mode` 常量为何要显式、`Accepted` 与 `Restarted` 的时态区别、`pull_in_progress` 为何 force 不越过。

- [ ] **Step 7: Commit**

```bash
go vet ./internal/proto/ && gofmt -l internal/proto/
git add internal/proto/
git commit -m "feat(proto): 换版协议加 mode 常量、pull 能力位与自拉状态"
```

---

## Task 7: agentd 侧 `mode` 判别、参数校验与并发锁

**Files:**
- Create: `internal/agentd/pullstate.go`
- Modify: `internal/agentd/update.go`（`handleUpdate` 的模式分派）
- Modify: `internal/agentd/server.go`（`Server` 加 `pull *pullTracker`）
- Test: `internal/agentd/update_test.go`

**Interfaces:**
- Consumes: `proto.UpdateMode*`、`proto.UpdateReasonPullInProgress`、`proto.PullState`、`proto.PullStage*`（Task 6）
- Produces（`agentd` 包内）：
  - `type pullTracker struct{ ... }`
  - `func newPullTracker() *pullTracker`
  - `func (p *pullTracker) begin(tag string) bool` —— 抢锁；已有在跑返回 false
  - `func (p *pullTracker) stage(s string)`
  - `func (p *pullTracker) fail(err error)` —— 落 failed 并释放锁
  - `func (p *pullTracker) snapshot() *proto.PullState` —— 无进行中且无失败记录时返回 nil

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/update_test.go`（沿用该文件既有的建 Server 助手）：

```go
// mode=pull 缺 tag 或缺 sha256 一律 400：缺了它们无从校验完整性，
// 而"下一个来路不明的二进制装上去"是这条链路最不能容忍的失败。
func TestPullRequiresTagAndSum(t *testing.T) {
	for _, q := range []string{
		"?mode=pull",
		"?mode=pull&tag=v1.0.0",
		"?mode=pull&sha256=abc",
	} {
		s := newTestServerManaged(t) // 既有助手：托管 + 无活跃任务
		rr := doUpdate(t, s, q, nil)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s 应 400，实得 %d", q, rr.Code)
		}
	}
}

// mode 非法值不得静默降级成某个默认模式——猜错的代价是装错东西或白重启。
func TestUnknownModeRejected(t *testing.T) {
	s := newTestServerManaged(t)
	rr := doUpdate(t, s, "?mode=sideload&tag=v1.0.0&sha256=abc", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("非法 mode 应 400，实得 %d", rr.Code)
	}
}

// mode=push 带空 body → 400。调用方显式说了"我要推送"却没带字节，这是个 bug；
// 静默当成纯重启会让它以为换版成功了。
func TestPushModeRejectsEmptyBody(t *testing.T) {
	s := newTestServerManaged(t)
	rr := doUpdate(t, s, "?mode=push&tag=v1.0.0&sha256=abc", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("mode=push 空 body 应 400，实得 %d", rr.Code)
	}
}

// mode=pull 带非空 body → 400。两种模式的意图互斥，不做"猜一个"的兼容。
func TestPullModeRejectsBody(t *testing.T) {
	s := newTestServerManaged(t)
	rr := doUpdate(t, s, "?mode=pull&tag=v1.0.0&sha256=abc", []byte("x"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("mode=pull 带 body 应 400，实得 %d", rr.Code)
	}
}

// 回归：空 body 且无 mode 仍是纯重启（B59 D8 行为一字不变）。
func TestEmptyBodyNoModeStillRestarts(t *testing.T) {
	s := newTestServerManaged(t)
	rr := doUpdate(t, s, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("纯重启应 200，实得 %d: %s", rr.Code, rr.Body)
	}
	var out proto.UpdateResp
	json.Unmarshal(rr.Body.Bytes(), &out)
	if !out.Restarted || out.Accepted {
		t.Fatalf("纯重启应 restarted=true accepted=false，实得 %+v", out)
	}
}

// 并发锁：一个自拉在跑时，第二个请求 409 + pull_in_progress。
// 没有这道锁，两个 goroutine 会往同一个临时文件路径写，互相截断出一个坏二进制。
func TestPullTrackerRejectsConcurrent(t *testing.T) {
	p := newPullTracker()
	if !p.begin("v1.0.0") {
		t.Fatal("首次 begin 应成功")
	}
	if p.begin("v1.0.1") {
		t.Fatal("已有自拉在跑时 begin 应失败")
	}
	p.fail(errors.New("boom"))
	if !p.begin("v1.0.2") {
		t.Fatal("失败释放后 begin 应能再次成功")
	}
}

// 没跑过自拉时 snapshot 返回 nil：status 不该显示一个编出来的空状态。
func TestPullTrackerSnapshotNilWhenIdle(t *testing.T) {
	if got := newPullTracker().snapshot(); got != nil {
		t.Fatalf("空闲时应返回 nil，实得 %+v", got)
	}
}

// 失败后 snapshot 必须留住阶段与错误原文——进程不重启，这正是要查它的场合。
func TestPullTrackerKeepsFailure(t *testing.T) {
	p := newPullTracker()
	p.begin("v1.0.0")
	p.stage(proto.PullStageDownloading)
	p.fail(errors.New("proxyconnect tcp: connection refused"))
	got := p.snapshot()
	if got == nil || got.Stage != proto.PullStageFailed {
		t.Fatalf("应留下 failed 状态，实得 %+v", got)
	}
	if !strings.Contains(got.Error, "connection refused") {
		t.Errorf("应留下错误原文，实得 %q", got.Error)
	}
	if got.Tag != "v1.0.0" {
		t.Errorf("应留下 tag，实得 %q", got.Tag)
	}
}
```

若 `update_test.go` 里还没有 `newTestServerManaged` / `doUpdate` 助手，按该文件既有的 Server 构造方式补两个薄助手（`doUpdate` 用 `httptest.NewRecorder` + `httptest.NewRequest(http.MethodPost, "/api/update"+q, body)` 直接调 `s.handleUpdate`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'Pull|Mode|EmptyBodyNoMode' -v`
Expected: FAIL —— `undefined: newPullTracker`，以及模式测试返回 200 而非 400

- [ ] **Step 3: 写实现**

创建 `internal/agentd/pullstate.go`：

```go
// pullstate.go —— 自拉换版的内存状态：并发锁 + 阶段流转 + 快照。
//
// 职责：
//   - 保证同一时刻只有一个自拉在跑（begin 抢锁）
//   - 记录阶段与失败原文，供 /api/status 回报
//
// 边界：
//   - **只在内存，不落盘**：成功路径的终点是进程重启，状态随之消失，而那时
//     版本号已经变了、调用方靠它就能确认；一个落盘的 done 会在下次启动时
//     变成误导性的陈旧数据。失败时进程不重启，内存态正好可查
//   - 不做下载、不碰文件：它只记「现在到哪一步了」，动作在 update.go
//   - 不做超时：自拉的总时限由 Installer 的 HTTP 超时兜底
package agentd

import (
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// pullTracker 持有自拉换版的并发锁与状态。
//
// 为什么锁与状态放在一起：它们的不变量是同一条——「running 为真当且仅当
// 有一个自拉正在推进」。拆成两个对象后，任何一处忘记同步都会造出
// 「状态说在下载、锁却是空闲」的幽灵态。
type pullTracker struct {
	mu      sync.Mutex
	running bool
	st      *proto.PullState
}

func newPullTracker() *pullTracker { return &pullTracker{} }

// begin 尝试开始一次自拉。
//
// 返回：
//   - true 表示抢到了，调用方可以起后台 goroutine；false 表示已有一个在跑，
//     调用方应当回 409 + proto.UpdateReasonPullInProgress
//
// 注意：
//   - 抢到锁的一方**必须**最终调用 fail 或让进程重启，否则锁永不释放。
//     成功路径不需要显式释放：换版成功即触发重启，进程整个换掉
func (p *pullTracker) begin(tag string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return false
	}
	now := time.Now()
	p.running = true
	p.st = &proto.PullState{
		Tag: tag, Stage: proto.PullStageDownloading,
		StartedAt: now, UpdatedAt: now,
	}
	return true
}

// stage 推进阶段。没有进行中的自拉时是空操作。
func (p *pullTracker) stage(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.st == nil {
		return
	}
	p.st.Stage = s
	p.st.UpdatedAt = time.Now()
}

// fail 记录失败并释放锁。
//
// 注意：
//   - 失败状态**保留**在内存里（不清空 st）：进程不会重启，操作者要靠
//     /api/status 拿到这条原文才知道该改代理还是改网络
func (p *pullTracker) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running = false
	if p.st == nil {
		return
	}
	p.st.Stage = proto.PullStageFailed
	p.st.Error = err.Error()
	p.st.UpdatedAt = time.Now()
}

// snapshot 返回状态副本，供 status 装配。没跑过时返回 nil。
//
// 返回副本而不是内部指针：status 的装配与后台 goroutine 的阶段推进并发，
// 直接外露指针会让 json.Marshal 撞上数据竞争。
func (p *pullTracker) snapshot() *proto.PullState {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.st == nil {
		return nil
	}
	cp := *p.st
	return &cp
}
```

`internal/agentd/server.go`：`Server` 结构加 `pull *pullTracker`，`NewServer` 里 `pull: newPullTracker(),`。

`internal/agentd/update.go` 的 `handleUpdate`：在读完 body 之后、现有的「纯重启」判断之前，插入模式分派。把现有的两道闸检查保持在最前不动，然后：

```go
	mode := r.URL.Query().Get("mode")
	switch mode {
	case "", proto.UpdateModePush, proto.UpdateModePull:
	default:
		// 不静默降级成某个默认模式：猜错的代价是装错东西，或者白重启一次
		// 而调用方以为换版成功了
		s.log.Warn("换版被拒：未知 mode", "mode", mode, "tag", tag)
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{
			Error: fmt.Sprintf("未知 mode %q，只支持 %q 与 %q（省略 mode 时按 body 空不空判别）",
				mode, proto.UpdateModePull, proto.UpdateModePush),
		})
		return
	}

	// 显式 mode 与 body 必须自洽。两种模式的意图互斥，不做"猜一个"的兼容：
	// 调用方说了 push 却没带字节、说了 pull 却带了字节，都是 bug，
	// 静默按另一种模式处理会让那个 bug 以"换版成功了"的样子存活下去
	if mode == proto.UpdateModePush && len(body) == 0 {
		s.log.Warn("换版被拒：mode=push 但 body 为空", "tag", tag)
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{
			Error: "mode=push 必须带 tar.gz 原文；只想重启请省略 mode 并发空 body",
		})
		return
	}
	if mode == proto.UpdateModePull && len(body) > 0 {
		s.log.Warn("换版被拒：mode=pull 但带了 body", "tag", tag, "bytes", len(body))
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{
			Error: "mode=pull 由 agentd 自己下载，不接受 body",
		})
		return
	}

	if mode == proto.UpdateModePull {
		s.handlePullUpdate(w, tag, sum, busy)
		return
	}
```

本 task 先给 `handlePullUpdate` 一个只做参数校验与抢锁的版本（真正的下载在 Task 8）：

```go
// handlePullUpdate 处理自拉换版：校验参数、抢并发锁、受理后交给后台。
//
// 参数：
//   - tag / sum: 协调者下发的目标版本与期望 sha256，**必须成对**
//   - busy: 已统计出的活跃任务数，只用于日志
//
// 注意：
//   - 两道闸已由调用方检查过，这里不重复
func (s *Server) handlePullUpdate(w http.ResponseWriter, tag, sum string, busy int) {
	if tag == "" || sum == "" {
		s.log.Warn("自拉被拒：缺 tag 或 sha256", "tag", tag, "has_sum", sum != "")
		writeJSON(w, http.StatusBadRequest, proto.UpdateError{
			Error: "mode=pull 时 tag 与 sha256 都必须给：缺了它们无从校验完整性，也无从自检",
		})
		return
	}
	if !s.pull.begin(tag) {
		// force 不越过这一条：两个自拉会往同一个临时文件路径写
		//（release.TempName(tag) 是确定性的），互相截断出一个坏二进制
		cur := s.pull.snapshot()
		s.log.Warn("自拉被拒：已有一个自拉在跑", "tag", tag, "current", cur)
		writeJSON(w, http.StatusConflict, proto.UpdateError{
			Error:  "已有一个自拉换版在进行中，去 status 看 pull_state",
			Reason: proto.UpdateReasonPullInProgress,
		})
		return
	}
	s.log.Info("自拉换版已受理", "tag", tag, "sha256", sum, "busy", busy)
	writeJSON(w, http.StatusAccepted, proto.UpdateResp{OK: true, Accepted: true, Version: tag})
	// Task 8 在此起后台 goroutine
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1 && go test -race ./internal/agentd/ -run Pull`
Expected: PASS（特别确认既有的推送与纯重启测试仍绿）

- [ ] **Step 5: 加关键节点日志**

Step 3 已含：三条拒绝分支各一条 Warn 带 tag/mode/bytes 上下文，受理成功一条 Info 带 tag/sha256/busy。`pullstate.go` 本身不打日志（它是状态容器，日志在动作侧 `update.go`）——在文件头注释里写明这一点，避免后来者以为漏了。

- [ ] **Step 6: 加意图注释**

已含：`pullstate.go` 文件头（职责 + 三条边界，含「为何只在内存」）、`pullTracker` 为何把锁与状态放一起、`begin` 的「抢到必须 fail 或重启」契约、`fail` 为何保留状态、`snapshot` 为何返回副本、模式分派为何不静默降级、并发锁为何 force 不越过。

- [ ] **Step 7: Commit**

```bash
go vet ./internal/agentd/ && gofmt -l internal/agentd/
git add internal/agentd/
git commit -m "feat(agentd): 换版接口加显式 mode 判别与自拉并发锁"
```

---

## Task 8: agentd 异步自拉执行与状态回报

**Files:**
- Modify: `internal/agentd/update.go`（`handlePullUpdate` 起后台 goroutine + `runPull`）
- Modify: `internal/agentd/server.go`（`UpdateDeps` 加 `FetchByTag`；bootstrap 用代理造 Installer；`handleStatus` 装配 pull 状态）
- Test: `internal/agentd/update_test.go`

**Interfaces:**
- Consumes: `release.Installer.FetchByTag`（Task 3）、`pullTracker`（Task 7）、`proto.PullStage*`（Task 6）
- Produces:
  - `UpdateDeps.FetchByTag func(ctx context.Context, tag, goos, goarch, wantSum string) ([]byte, error)`
  - `UpdateDeps.Platform func() (string, string)`
  - `func (s *Server) runPull(tag, sum string)`（包内）

- [ ] **Step 1: 写失败的测试**

追加到 `internal/agentd/update_test.go`：

```go
// 成功路径：下载 → 安装 → 换版 → 触发重启，且四步都被真的调到。
func TestPullSuccessInstallsAndRestarts(t *testing.T) {
	s := newTestServerManaged(t)
	var activated, restarted bool
	done := make(chan struct{})
	s.upd.FetchByTag = func(_ context.Context, tag, goos, goarch, sum string) ([]byte, error) {
		return []byte("archive"), nil
	}
	s.upd.Install = func(tgz []byte, wantSum, wantTag, destDir string) (string, error) {
		return filepath.Join(destDir, "new"), nil
	}
	s.upd.Activate = func(newPath, target string) (string, error) {
		activated = true
		return target + ".prev", nil
	}
	s.SetRestart(func(string) bool { restarted = true; close(done); return true })

	rr := doUpdate(t, s, "?mode=pull&tag=v1.0.0&sha256=abc", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("自拉应 202，实得 %d: %s", rr.Code, rr.Body)
	}
	var out proto.UpdateResp
	json.Unmarshal(rr.Body.Bytes(), &out)
	if !out.Accepted || out.Version != "v1.0.0" {
		t.Fatalf("响应应为 accepted + version，实得 %+v", out)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("后台自拉未在 5s 内触发重启")
	}
	if !activated || !restarted {
		t.Fatalf("activated=%v restarted=%v，两者都应为 true", activated, restarted)
	}
}

// 下载失败：不得 Activate、不得重启，状态落 failed 且带错误原文。
// 这条是"失败时协调者能拿到原因"的落点——没有它，一次代理配错要让人
// 干等到超时才看到一句「版本仍是 X」。
func TestPullDownloadFailureRecordsAndDoesNotActivate(t *testing.T) {
	s := newTestServerManaged(t)
	var activated, restarted bool
	s.upd.FetchByTag = func(_ context.Context, _, _, _, _ string) ([]byte, error) {
		return nil, errors.New("proxyconnect tcp: dial tcp 127.0.0.1:1080: connection refused")
	}
	s.upd.Activate = func(string, string) (string, error) { activated = true; return "", nil }
	s.SetRestart(func(string) bool { restarted = true; return true })

	rr := doUpdate(t, s, "?mode=pull&tag=v1.0.0&sha256=abc", nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("受理阶段应 202，实得 %d", rr.Code)
	}
	waitPullStage(t, s, proto.PullStageFailed, 5*time.Second)

	if activated || restarted {
		t.Fatalf("下载失败不得换版或重启，activated=%v restarted=%v", activated, restarted)
	}
	got := s.pull.snapshot()
	if !strings.Contains(got.Error, "connection refused") {
		t.Errorf("状态应留下错误原文，实得 %q", got.Error)
	}
}

// 安装失败（sha256 不符、自检不过等）同样不得 Activate。
func TestPullInstallFailureDoesNotActivate(t *testing.T) {
	s := newTestServerManaged(t)
	var activated bool
	s.upd.FetchByTag = func(_ context.Context, _, _, _, _ string) ([]byte, error) {
		return []byte("archive"), nil
	}
	s.upd.Install = func([]byte, string, string, string) (string, error) {
		return "", errors.New("自检失败：版本号对不上")
	}
	s.upd.Activate = func(string, string) (string, error) { activated = true; return "", nil }

	doUpdate(t, s, "?mode=pull&tag=v1.0.0&sha256=abc", nil)
	waitPullStage(t, s, proto.PullStageFailed, 5*time.Second)
	if activated {
		t.Fatal("安装失败不得换版")
	}
}

// status 必须上报能力位与自拉状态：前者是协调者的选路判据，
// 后者是失败时唯一能拿到原因的地方。
func TestStatusReportsPullCapabilityAndState(t *testing.T) {
	s := newTestServerManaged(t)
	rr := httptest.NewRecorder()
	s.handleStatus(rr, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	var st proto.StatusResp
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Update == nil || st.Update.Pull == nil || !*st.Update.Pull {
		t.Fatalf("status 应上报 pull=true，实得 %+v", st.Update)
	}
	if st.Update.PullState != nil {
		t.Errorf("没跑过自拉时 pull_state 应为 nil，实得 %+v", st.Update.PullState)
	}
}

// waitPullStage 轮询等状态到达期望阶段。
// 用轮询而不是 sleep：后台 goroutine 的耗时不确定，固定 sleep 要么慢要么脆。
func waitPullStage(t *testing.T, s *Server, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st := s.pull.snapshot(); st != nil && st.Stage == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待 pull 阶段 %q 超时，实得 %+v", want, s.pull.snapshot())
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'Pull(Success|Download|Install)|StatusReportsPull' -v`
Expected: FAIL —— `s.upd.FetchByTag undefined`；status 测试报 `Pull` 为 nil

- [ ] **Step 3: 写实现**

`internal/agentd/server.go`：`UpdateDeps` 加两个字段：

```go
	// FetchByTag 按 tag 下载本机平台的资产并用 wantSum 校验。自拉模式专用。
	//
	// 为什么是 tag 而不是 release.Release：agentd **不查 GitHub API**——
	// 资产地址是确定性的（release.AssetURL），而 api.github.com 有
	// 60 次/小时/IP 的匿名限流，多台执行机很可能共用一个代理出口 IP
	FetchByTag func(ctx context.Context, tag, goos, goarch, wantSum string) ([]byte, error)
	// Platform 返回本机 goos/goarch。抽成缝只为可测：写死 runtime.GOOS 时
	// "按本机平台取资产名"这条行为在单一平台的 CI 上验不出来
	Platform func() (string, string)
```

`NewServer` 里：

```go
	// 出网 transport 按配置里的代理造。坏值不阻断启动（config.Load 已经硬拒过
	// 一道，走到这儿只可能是绕过了它），降级为不用代理并打 Error——
	// agentd 不该因为一个附属设置而起不来
	var tr http.RoundTripper
	if cfg.Proxy != "" {
		t, err := proxycfg.Transport(cfg.Proxy)
		if err != nil {
			log.Error("代理配置无法使用，自拉换版将不走代理",
				"proxy", proxycfg.Redact(cfg.Proxy), "cause", err)
		} else {
			tr = t
			log.Info("自拉换版将使用代理", "proxy", proxycfg.Redact(cfg.Proxy))
		}
	}
	inst := release.NewInstaller(log, tr)
	...
	s.upd = UpdateDeps{
		Getenv:     os.Getenv,
		Executable: resolvedExecutable,
		Install:    inst.InstallArchive,
		Activate:   release.Activate,
		Platform:   release.CurrentPlatform,
		FetchByTag: func(ctx context.Context, tag, goos, goarch, wantSum string) ([]byte, error) {
			return inst.FetchByTag(ctx, release.DefaultRepo, tag, goos, goarch, wantSum)
		},
	}
```

`Server` 结构加 `pullCtx context.Context`——后台 goroutine 的 ctx **不能用 `r.Context()`**（handler 一返回它就被取消，下载会当场断）。用 agentd 的生命周期 ctx；若 `NewServer` 拿不到，就用 `context.Background()` 并在注释里写清：

```go
	// pullBaseCtx 是后台自拉的基准上下文。
	//
	// **绝不能用 r.Context()**：handler 一返回它就被取消，下载会在受理后的
	// 下一毫秒当场断掉。总时限由 Installer 的 HTTP 超时（10min）兜底
	pullBaseCtx context.Context
```

`handlePullUpdate` 的末尾（写完响应后）起 goroutine：

```go
	// 必须在写完响应之后再起 goroutine——与 triggerRestart 同一条纪律：
	// 先动作后响应会让客户端拿到一个断掉的连接
	go s.runPull(tag, sum)
```

新增 `runPull`：

```go
// runPull 在后台执行一次自拉换版：下载 → 安装（校验+解包+自检）→ 换版 → 重启。
//
// 参数：
//   - tag: 目标版本
//   - sum: 协调者下发的期望 sha256。**不是本机自己取的**——校验和与资产走
//     两条不同的信任路径，本机代理/镜像被投毒时才抓得住（spec §5.5）
//
// 注意：
//   - 由 handlePullUpdate 在抢到 pull 锁之后起。任何失败路径都必须调
//     s.pull.fail 释放锁，否则这台 agentd 从此再也不能自拉
//   - 成功路径不释放锁：换版成功即触发重启，进程整个换掉
func (s *Server) runPull(tag, sum string) {
	ctx := s.pullBaseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	goos, goarch := s.upd.Platform()
	s.log.Info("自拉换版开始", "tag", tag, "platform", goos+"/"+goarch, "sha256", sum)

	s.pull.stage(proto.PullStageDownloading)
	tgz, err := s.upd.FetchByTag(ctx, tag, goos, goarch, sum)
	if err != nil {
		s.log.Error("自拉换版失败：下载或校验不过", "tag", tag,
			"platform", goos+"/"+goarch, "cause", err)
		s.pull.fail(err)
		return
	}
	s.log.Info("自拉换版：资产已就绪", "tag", tag, "bytes", len(tgz))

	target, err := s.upd.Executable()
	if err != nil {
		s.log.Error("自拉换版失败：取当前二进制路径", "tag", tag, "cause", err)
		s.pull.fail(err)
		return
	}

	s.pull.stage(proto.PullStageInstalling)
	// 临时文件必须与目标同目录：os.Rename 的原子性只在同一文件系统内成立
	newPath, err := s.upd.Install(tgz, sum, tag, filepath.Dir(target))
	if err != nil {
		s.log.Error("自拉换版失败：校验或自检未通过", "tag", tag, "target", target, "cause", err)
		s.pull.fail(err)
		return
	}
	prev, err := s.upd.Activate(newPath, target)
	if err != nil {
		s.log.Error("自拉换版失败：替换二进制出错", "tag", tag, "target", target, "cause", err)
		s.pull.fail(err)
		return
	}

	s.log.Info("自拉换版完成，准备重启", "tag", tag, "target", target, "prev", prev)
	s.triggerRestart("自拉换版到 " + tag)
}
```

注：只有 `downloading` 与 `installing` 两个进行中阶段——sha256 比对与解包后自检都发生在 `Install` 内部，不单独立一个 `verifying`（Task 6 的常量注释已写明理由）。

`handleStatus` 里，在 `s.mgr.Status()` 返回之后、`writeJSON` 之前装配：

```go
	// pull 能力位与自拉状态由 Server 装配而不是 Manager：它们的持有者是
	// Server（换版 handler 在这里），Manager 不该为了填两个字段而反向依赖它
	if resp.Update != nil {
		yes := true
		resp.Update.Pull = &yes
		resp.Update.PullState = s.pull.snapshot()
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1 && go test -race ./internal/agentd/ -count=1`
Expected: PASS（`-race` 必须过：后台 goroutine 与 status 装配确实并发读写状态）

- [ ] **Step 5: 加关键节点日志**

Step 3 已覆盖 instrumenting-code 的六类关键点：进入 `runPull` 打 tag/platform/sha256；外部调用（下载）前后各一条；四条失败分支各一条 Error 带 tag + 具体上下文 + cause；两处状态变更（`资产已就绪`、`准备重启`）；成功路径**不静默**。自查：`runPull` 的每个 `return` 前都有一条日志。

- [ ] **Step 6: 加意图注释**

已含：`FetchByTag` 为何按 tag 而不是 Release（连同限流的理由）、`Platform` 缝为何存在、`pullBaseCtx` 为何不能用 `r.Context()`、goroutine 为何在写响应之后起、`runPull` 的 sum 为何来自协调者、失败路径必须 `fail` 的契约、成功路径为何不释放锁、临时文件为何同目录、status 装配为何在 Server 而非 Manager。

- [ ] **Step 7: Commit**

```bash
go vet ./internal/agentd/ && gofmt -l internal/agentd/
git add internal/agentd/
git commit -m "feat(agentd): 自拉换版后台执行，状态与失败原文经 status 回报"
```

---

## Task 9: client 侧 `PullUpdate` 与 `WaitVersion` 提前失败

**Files:**
- Modify: `internal/client/update.go`
- Test: `internal/client/update_test.go`

**Interfaces:**
- Consumes: `proto.UpdateModePull`、`proto.PullStageFailed`、`proto.UpdateResp.Accepted`（Task 6）
- Produces:
  - `func (c *Client) PullUpdate(ctx context.Context, tag, sum string, force bool) (*proto.UpdateResp, error)`
  - `WaitVersion` 行为变更：读到 `pull_state.stage == "failed"` 立刻返回带原文的错误

- [ ] **Step 1: 写失败的测试**

追加到 `internal/client/update_test.go`：

```go
// PullUpdate 必须发 mode=pull、带 tag 与 sha256、且 body 为空。
func TestPullUpdateSendsModeAndNoBody(t *testing.T) {
	var gotQuery url.Values
	var gotLen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		gotLen = r.ContentLength
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(proto.UpdateResp{OK: true, Accepted: true, Version: "v1.0.0"})
	}))
	defer srv.Close()

	resp, err := New(srv.URL, "tok").PullUpdate(context.Background(), "v1.0.0", "abc", false)
	if err != nil {
		t.Fatalf("PullUpdate: %v", err)
	}
	if !resp.Accepted {
		t.Errorf("应解出 accepted=true，实得 %+v", resp)
	}
	if gotQuery.Get("mode") != proto.UpdateModePull {
		t.Errorf("mode = %q，期望 pull", gotQuery.Get("mode"))
	}
	if gotQuery.Get("tag") != "v1.0.0" || gotQuery.Get("sha256") != "abc" {
		t.Errorf("tag/sha256 未带上: %v", gotQuery)
	}
	if gotLen > 0 {
		t.Errorf("自拉不得带 body，ContentLength = %d", gotLen)
	}
}

// 409 + pull_in_progress 要解成可判别的 UpdateRejected，调用方才能给对处置。
func TestPullUpdateRejectedInProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(proto.UpdateError{
			Error: "已有一个自拉换版在进行中", Reason: proto.UpdateReasonPullInProgress,
		})
	}))
	defer srv.Close()

	_, err := New(srv.URL, "tok").PullUpdate(context.Background(), "v1.0.0", "abc", false)
	var rej *UpdateRejected
	if !errors.As(err, &rej) || rej.Reason != proto.UpdateReasonPullInProgress {
		t.Fatalf("应解出 pull_in_progress，实得 %v", err)
	}
}

// 核心行为：WaitVersion 看到 pull 失败必须**立刻**返回并带上原文，
// 而不是等满超时才说一句"版本仍是 X"。没有这条，一次代理配错要干等 10 分钟。
func TestWaitVersionAbortsOnPullFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(proto.StatusResp{
			Version: proto.BuildInfo{Version: "v0.9.0"},
			Update: &proto.UpdateStatus{
				Managed: true,
				PullState: &proto.PullState{
					Tag: "v1.0.0", Stage: proto.PullStageFailed,
					Error: "proxyconnect tcp: dial tcp 127.0.0.1:1080: connection refused",
				},
			},
		})
	}))
	defer srv.Close()

	start := time.Now()
	err := New(srv.URL, "tok").WaitVersion(context.Background(), "v1.0.0",
		30*time.Second, 50*time.Millisecond)
	if err == nil {
		t.Fatal("pull 失败时 WaitVersion 应返回错误")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("错误应带上对端的原文，实得 %q", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("应立刻中止，实际等了 %s", elapsed)
	}
}

// 只有**目标 tag** 的失败才中止。上一次别的版本留下的陈旧 failed 状态
// 不该把这一次的等待打断——否则一台曾经失败过的机器再也升不上去。
func TestWaitVersionIgnoresStaleFailureOfOtherTag(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		v := "v0.9.0"
		if n > 2 {
			v = "v1.0.0" // 第三次轮询时新版本上线
		}
		json.NewEncoder(w).Encode(proto.StatusResp{
			Version: proto.BuildInfo{Version: v},
			Update: &proto.UpdateStatus{
				Managed: true,
				PullState: &proto.PullState{
					Tag: "v0.8.0", Stage: proto.PullStageFailed, Error: "旧的失败",
				},
			},
		})
	}))
	defer srv.Close()

	if err := New(srv.URL, "tok").WaitVersion(context.Background(), "v1.0.0",
		30*time.Second, 20*time.Millisecond); err != nil {
		t.Fatalf("陈旧的其他版本失败态不该中止本次等待，实得 %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/client/ -run 'Pull|WaitVersion' -v`
Expected: FAIL —— `undefined: PullUpdate`；`TestWaitVersionAbortsOnPullFailure` 等满 30s 后才失败

- [ ] **Step 3: 写实现**

`internal/client/update.go`：

```go
// PullUpdate 让对端 agentd **自己**去下载指定版本并换版。
//
// 参数：
//   - tag: 目标版本，agentd 用它拼下载地址、并在自检时比对新二进制的 version
//   - sum: 资产的 sha256（十六进制小写），来自**协调者**下的 checksums.txt。
//     agentd 下完资产比对它——校验和与资产因此走两条不同的信任路径，
//     执行机侧的代理/镜像被投毒时会当场被抓住
//   - force: 越过闸一（活跃任务）。**不越过闸二（非托管）**，也不越过
//     「已有自拉在跑」
//
// 返回：
//   - 202 的受理响应（Accepted=true）。**换版还没发生**——结果要靠
//     WaitVersion 轮询确认
//   - *UpdateRejected（三道闸）/ ErrUpdateUnsupported（对端过旧）/ 其他错误
//
// 注意：
//   - 调用前必须确认对端支持自拉（status 的 update.pull 为 true）。老 agentd
//     会把这个请求当成纯重启并回 200，于是这次"升级"什么都没发生而调用方
//     以为受理了——选路判据见 cmd/upgrade.go
func (c *Client) PullUpdate(ctx context.Context, tag, sum string, force bool) (*proto.UpdateResp, error) {
	return c.postUpdate(ctx, tag, sum, nil, force, proto.UpdateModePull)
}
```

`postUpdate` 加一个 `mode string` 参数（`PushUpdate` 传 `proto.UpdateModePush`，`RestartAgentd` 传 `""`），非空时 `q.Set("mode", mode)`；并把接受的成功状态码从「只有 200」放宽到「200 或 202」。

`WaitVersion` 的循环体改为：

```go
	for attempt := 1; ; attempt++ {
		st, err := c.Status(ctx)
		switch {
		case err == nil && st.Version.Version == want:
			c.log().Info("新版本已上线", "want", want, "attempts", attempt)
			return nil
		case err == nil:
			// 对端自拉失败时立刻中止：干等到超时只会得到一句「版本仍是 X」，
			// 而真正的原因（代理连不上、sha256 不符、自检没过）就在对端的
			// pull_state 里躺着。**只认目标 tag 的失败**——上一次别的版本留下的
			// 陈旧 failed 态若也中止，一台曾经失败过的机器就再也升不上去了
			if ps := pullFailure(st, want); ps != nil {
				c.log().Error("对端自拉换版失败", "want", want,
					"stage", ps.Stage, "detail", ps.Error)
				return fmt.Errorf("对端自拉 %s 失败：%s", want, ps.Error)
			}
			last = fmt.Errorf("对端版本仍是 %q", st.Version.Version)
			if ps := pullProgress(st, want); ps != nil {
				c.log().Info("对端自拉进行中", "want", want, "stage", ps.Stage)
			}
		default:
			last = err
		}
		...
	}
```

配套两个小助手：

```go
// pullFailure 返回对端针对 want 这个版本的自拉失败状态；无失败时返回 nil。
//
// 为什么要比对 tag：陈旧的失败态（上一次升别的版本没成）留在内存里，
// 若不加区分地据此中止，这台机器就永远升不上去了。
func pullFailure(st *proto.StatusResp, want string) *proto.PullState {
	if st == nil || st.Update == nil || st.Update.PullState == nil {
		return nil
	}
	ps := st.Update.PullState
	if ps.Tag != want || ps.Stage != proto.PullStageFailed {
		return nil
	}
	return ps
}

// pullProgress 返回对端针对 want 的进行中状态，供轮询时打进度日志。
func pullProgress(st *proto.StatusResp, want string) *proto.PullState {
	if st == nil || st.Update == nil || st.Update.PullState == nil {
		return nil
	}
	ps := st.Update.PullState
	if ps.Tag != want || ps.Stage == proto.PullStageFailed {
		return nil
	}
	return ps
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/client/ -count=1 && go build ./...`
Expected: PASS（既有的 `TestWaitVersion*` 与推送测试仍绿）

- [ ] **Step 5: 加关键节点日志**

Step 3 已含：自拉失败一条 Error（带 want/stage/detail 原文）、进行中每轮一条 Info 带 stage。`postUpdate` 既有的请求前 Info 与被拒 Warn 保持不变，只把 `mode` 加进字段——**这条必做**，否则日志里分不出一次换版走的是推送还是自拉。

- [ ] **Step 6: 加意图注释**

已含：`PullUpdate` 的 doc（sum 为何来自协调者、202 的时态、必须先探能力的前置条件）、`WaitVersion` 里为何提前中止、为何只认目标 tag 的失败、两个助手的 doc。

- [ ] **Step 7: Commit**

```bash
go vet ./internal/client/ && gofmt -l internal/client/
git add internal/client/
git commit -m "feat(client): 加 PullUpdate；WaitVersion 读对端 pull 状态提前失败"
```

---

## Task 10: `upgrade` 选路、`--push`、checksums 缓存与超时放宽

**Files:**
- Modify: `cmd/upgrade.go`
- Test: `cmd/upgrade_test.go`

**Interfaces:**
- Consumes: `client.PullUpdate`（Task 9）、`release.Installer.FetchChecksum`（Task 3）、`proto.UpdateStatus.Pull`（Task 6）
- Produces:
  - `machineState.Pull *bool`
  - `agentdPeer` 接口加 `PullUpdate(ctx, tag, sum string, force bool) (*proto.UpdateResp, error)`
  - `releaseFetcher` 接口加 `FetchChecksum(ctx, rel, goos, goarch) (string, error)`
  - `var upgradePush bool`（`--push` 标志）

- [ ] **Step 1: 写失败的测试**

追加到 `cmd/upgrade_test.go`：

```go
// 对端上报 pull=true 时走自拉：不下 20MB 资产，只下 checksums 并下发 tag+sum。
func TestRemoteUpgradeUsesPullWhenCapable(t *testing.T) {
	// 沿用本文件既有的替身装配方式（listEndpoints / newAgentdClient /
	// newReleaseChecker / newReleaseFetcher 四个缝全部替换）
	fetcher := &fakeFetcher{sum: "abc"}
	peer := &fakePeer{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0"}
	withStubs(t, fetcher, peer)

	runUpgradeNow(t)

	if fetcher.archiveCalls != 0 {
		t.Errorf("自拉模式不得下载资产，实得 %d 次", fetcher.archiveCalls)
	}
	if peer.pullCalls != 1 {
		t.Errorf("应调一次 PullUpdate，实得 %d", peer.pullCalls)
	}
	if peer.pushCalls != 0 {
		t.Errorf("不该调 PushUpdate，实得 %d", peer.pushCalls)
	}
	if peer.lastSum != "abc" {
		t.Errorf("下发的 sha256 = %q，期望 abc", peer.lastSum)
	}
}

// 对端没上报 pull（老 agentd，nil）→ 自动降级推送，升级链路不断。
func TestRemoteUpgradeFallsBackToPushWhenPullNil(t *testing.T) {
	fetcher := &fakeFetcher{sum: "abc"}
	peer := &fakePeer{pull: nil, platform: "linux/amd64", version: "v0.9.0"}
	withStubs(t, fetcher, peer)

	runUpgradeNow(t)

	if peer.pushCalls != 1 {
		t.Errorf("老 agentd 应降级推送，实得 push=%d pull=%d", peer.pushCalls, peer.pullCalls)
	}
}

// --push 强制走推送，无论对端能力如何——内网执行机出不了网时的逃生路径。
func TestPushFlagForcesPushMode(t *testing.T) {
	fetcher := &fakeFetcher{sum: "abc"}
	peer := &fakePeer{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0"}
	withStubs(t, fetcher, peer)

	upgradePush = true
	defer func() { upgradePush = false }()
	runUpgradeNow(t)

	if peer.pushCalls != 1 || peer.pullCalls != 0 {
		t.Errorf("--push 应强制推送，实得 push=%d pull=%d", peer.pushCalls, peer.pullCalls)
	}
}

// checksums.txt 对一个 release 只下一次，多台机器共用——这正是自拉省流量的点，
// 每台机器各下一次会把省下的流量又还回去一部分，而且平白多几次 GitHub 请求。
func TestChecksumFetchedOncePerRun(t *testing.T) {
	fetcher := &fakeFetcher{sum: "abc"}
	withStubsMulti(t, fetcher, []*fakePeer{
		{pull: boolPtr(true), platform: "linux/amd64", version: "v0.9.0"},
		{pull: boolPtr(true), platform: "darwin/arm64", version: "v0.9.0"},
	})

	runUpgradeNow(t)

	if fetcher.checksumCalls != 1 {
		t.Errorf("checksums 应只下一次，实得 %d 次", fetcher.checksumCalls)
	}
}
```

替身（`fakeFetcher` / `fakePeer` / `withStubs` / `withStubsMulti` / `runUpgradeNow` / `boolPtr`）按本文件既有替身的写法补齐：`fakeFetcher` 实现 `Fetch` / `FetchArchive` / `FetchChecksum` 三个方法并各自计数；`fakePeer` 实现 `Status` / `PushUpdate` / `PullUpdate` / `RestartAgentd` / `WaitVersion` 并记录 `pullCalls` / `pushCalls` / `lastSum`，`Status` 按 `pull` 字段构造 `proto.UpdateStatus`；`WaitVersion` 直接返回 nil。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run 'Pull|Push|Checksum' -v`
Expected: FAIL —— `undefined: upgradePush`、`fakePeer does not implement agentdPeer`

- [ ] **Step 3: 写实现**

`cmd/upgrade.go` 依次改：

1. `releaseFetcher` 接口加：

```go
	// FetchChecksum 只下 checksums.txt 取某平台的 sha256，供自拉模式下发。
	// 不下资产——这正是自拉的省流量点
	FetchChecksum(ctx context.Context, rel release.Release, goos, goarch string) (string, error)
```

2. `agentdPeer` 接口加：

```go
	PullUpdate(ctx context.Context, tag, sum string, force bool) (*proto.UpdateResp, error)
```

3. `machineState` 加：

```go
	// Pull 是对端上报的「支持自拉换版」。
	//
	// 为什么是指针：nil 表示**对端没给这个字段**（老 agentd），与「对端说 false」
	// 是两回事。老 agentd 收到 mode=pull 会当成纯重启并回 200，据此以为受理了
	// 就会干等到超时报一句误导性的「已换版但新进程未上线」。与同结构里的
	// Managed *bool 同款纪律
	Pull *bool
```

`probeMachine` 的 `st.Update != nil` 分支里补 `ms.Pull = st.Update.Pull`。

4. 标志与超时：

```go
var upgradePush bool

// upgradeWaitTimeout 是换版后等新进程上线的时限。
//
// 自拉模式下这段时间里对端要下 20MB（慢网 + 代理下几分钟很正常），
// 所以从推送时代的 60s 放宽到 10min。**放宽是安全的**：WaitVersion 会读对端
// 的 pull_state，真失败时立刻中止，不会真的干等满 10 分钟（见 internal/client）
const (
	upgradeWaitTimeout  = 10 * time.Minute
	upgradeWaitInterval = 2 * time.Second
)
```

`init()` 里注册：

```go
	upgradeCmd.Flags().BoolVar(&upgradePush, "push", false,
		"强制由本机下载并推送二进制（默认让执行机自己下；执行机出不了网时用它）")
```

5. checksums 缓存：在 `RunE` 里查到 `rel` 之后、遍历机器之前，建一个按平台缓存的取 sum 函数：

```go
			// checksums.txt 对一个 release 只下一次，多台机器共用同一份内容
			//（按各自平台取自己那行）。每台机器各下一次会把自拉省下的流量
			// 还回去一部分，还平白多几次 GitHub 请求——而它有 60 次/小时/IP 的限流
			sumCache := map[string]string{}
			var sumMu sync.Mutex
			sumFor := func(ctx context.Context, goos, goarch string) (string, error) {
				key := goos + "/" + goarch
				sumMu.Lock()
				defer sumMu.Unlock()
				if s, ok := sumCache[key]; ok {
					return s, nil
				}
				s, err := newReleaseFetcher().FetchChecksum(ctx, rel, goos, goarch)
				if err != nil {
					return "", err
				}
				sumCache[key] = s
				return s, nil
			}
```

把 `sumFor` 传进 `process` → `remoteUpgrade`（加参数，不用包级变量：多机循环里的共享状态用参数传递才看得见）。

> 注：机器是**串行**处理的（现有 `for i := range states` 就是串行），`sync.Mutex` 在这里是防御性的——将来有人改成并发时不至于静默数据竞争。若不想引入 `sync`，去掉锁也正确；**二选一，别留一个"看起来防了但没防住"的半吊子**。选保留锁。

6. `remoteUpgrade` 分两条：

```go
// remoteUpgrade 远端的完整升级路径。
//
// 两条路：对端支持自拉（且未 --push）时只下发 tag+sha256，20MB 由执行机自己下；
// 否则按本机下载 + 推送的老路走。选路判据是 ms.Pull——nil（老 agentd）一律降级
// 推送，因为老 agentd 会把 mode=pull 当成纯重启并回 200。
func (ms *machineState) remoteUpgrade(ctx context.Context, out io.Writer, peer agentdPeer,
	rel release.Release, sumFor func(context.Context, string, string) (string, error)) outcome {
	name := ms.Ep.Name
	parts := strings.SplitN(ms.Platform, "/", 2)
	if len(parts) != 2 {
		fmt.Fprintf(out, "%-8s 失败   对端上报的平台 %q 格式非法\n", name, ms.Platform)
		return outcomeFail
	}

	usePull := !upgradePush && ms.Pull != nil && *ms.Pull
	slog.Default().Info("选择换版分发模式", "name", name, "platform", ms.Platform,
		"pull_capable", ms.Pull != nil && *ms.Pull, "force_push", upgradePush, "use_pull", usePull)

	var resp *proto.UpdateResp
	var err error
	if usePull {
		sum, serr := sumFor(ctx, parts[0], parts[1])
		if serr != nil {
			slog.Default().Error("取校验和失败", "name", name, "cause", serr)
			fmt.Fprintf(out, "%-8s 失败   %s\n", name, serr)
			return outcomeFail
		}
		slog.Default().Info("下发自拉换版请求", "name", name, "tag", rel.Tag, "sha256", sum)
		resp, err = peer.PullUpdate(ctx, rel.Tag, sum, upgradeForce)
	} else {
		slog.Default().Info("下载远端平台资产", "name", name, "platform", ms.Platform, "tag", rel.Tag)
		tgz, sum, ferr := newReleaseFetcher().FetchArchive(ctx, rel, parts[0], parts[1])
		if ferr != nil {
			slog.Default().Error("下载远端资产失败", "name", name, "cause", ferr)
			fmt.Fprintf(out, "%-8s 失败   %s\n", name, ferr)
			return outcomeFail
		}
		slog.Default().Info("下载完成，推送换版请求", "name", name, "tag", rel.Tag, "bytes", len(tgz))
		resp, err = peer.PushUpdate(ctx, rel.Tag, sum, tgz, upgradeForce)
	}

	if err != nil {
		var rej *client.UpdateRejected
		if errors.As(err, &rej) {
			slog.Default().Warn("换版被拒", "name", name, "reason", rej.Reason, "detail", rej.Msg)
			fmt.Fprintf(out, "%-8s 跳过   %s\n", name, rej.Msg)
			// 处置建议必须对症：三种拒绝的出路完全不同
			switch rej.Reason {
			case proto.UpdateReasonBusy:
				fmt.Fprintf(out, "         handoff upgrade --now --target %s --force\n", name)
			case proto.UpdateReasonUnmanaged:
				fmt.Fprintf(out, "         先在该机器上 handoff service install\n")
			case proto.UpdateReasonPullInProgress:
				// 不给 --force：它不越过这一条（两个自拉会写坏同一个临时文件）
				fmt.Fprintf(out, "         等它跑完，或 handoff status --target %s 看 pull_state\n", name)
			}
			return outcomeSkip
		}
		slog.Default().Error("发起换版失败", "name", name, "use_pull", usePull, "cause", err)
		fmt.Fprintf(out, "%-8s 失败   %s\n", name, err)
		return outcomeFail
	}

	slog.Default().Info("换版已受理，等待新版本上线", "name", name,
		"version", resp.Version, "prev", resp.Prev, "accepted", resp.Accepted)
	if err := peer.WaitVersion(ctx, rel.Tag, upgradeWaitTimeout, upgradeWaitInterval); err != nil {
		// 自拉与推送的失败措辞必须不同：推送模式下二进制已经在对端了，
		// 提 prev 与回滚是对的；自拉模式下可能连下载都没成，提回滚是误导
		slog.Default().Error("等待新版本上线失败", "name", name, "use_pull", usePull, "cause", err)
		if usePull {
			fmt.Fprintf(out, "%-8s 失败   %s\n", name, err)
			fmt.Fprintf(out, "         handoff status --target %s 看 pull_state 拿完整原因\n", name)
		} else {
			fmt.Fprintf(out, "%-8s 失败   已换版但新进程未在 %s 内上线\n", name, upgradeWaitTimeout)
			fmt.Fprintf(out, "         prev: %s  回滚：handoff upgrade --rollback\n", resp.Prev)
		}
		return outcomeFail
	}
	slog.Default().Info("新版本已上线", "name", name, "version", rel.Tag)
	fmt.Fprintf(out, "%-8s 成功   %s → %s\n", name, ms.Agentd, rel.Tag)
	return outcomeOK
}
```

`process` 的签名相应加 `sumFor` 参数并透传。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -count=1 && go build ./...`
Expected: PASS（既有的 `recordOrder`「本机最后」测试、闸测试、巡检表测试全部仍绿）

- [ ] **Step 5: 加关键节点日志**

Step 3 已含：选路一条 Info（带 `pull_capable` / `force_push` / `use_pull` 三个判据字段——**这条最要紧**，"为什么这台机器走了推送"是最容易被问到的问题）、两条分支各自的动作 Info、受理 Info 带 `accepted`、三类失败各一条 Error/Warn 带 `use_pull` 上下文。

- [ ] **Step 6: 加意图注释**

已含：`machineState.Pull` 为何是指针、`upgradeWaitTimeout` 为何能放宽到 10min（附「不会真等满」的理由）、checksums 为何要缓存、`sync.Mutex` 的防御性用途说明、`--push` 的用途、`pull_in_progress` 为何不给 `--force`、自拉与推送的失败措辞为何要分开。

- [ ] **Step 7: Commit**

```bash
go vet ./cmd/ && gofmt -l cmd/
git add cmd/
git commit -m "feat(upgrade): 默认让执行机自拉，--push 回退，老 agentd 自动降级"
```

---

## Task 11: 文档

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-08-11-update-and-skill-delivery-design.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: README 配置块加 `proxy`**

在 `path_dirs` 那行之后加：

```yaml
proxy: ""                     # handoff 自身出网代理；空 = 沿用 HTTPS_PROXY 等环境变量
                              # 支持 http:// https:// socks5:// socks5h://
```

- [ ] **Step 2: README 加 `proxy` 段说明**

紧跟在配置块下方、`env` 段说明之前加一段：

```markdown
**`proxy` 段**：给 **handoff 自己**的出网配代理。作用范围只有两处——更新链路
（查 release、下资产）与 agentd 的 `git clone` / `git fetch`。配置它比设
`HTTPS_PROXY` 环境变量更实用的地方在于：agentd 由 launchd / systemd 拉起，
**读不到你终端的 shell env**，而这台机器上本来就有一份 `config.yaml`。

三条边界值得记住：

- **不作用于协调者 ↔ agentd 那条链路**。那是 LAN / 虚拟组网 / loopback 地址，
  代理化只会给它凭空加失败模式。
- **不作用于 executor**。executor 的出网走 `env` 段（下一节），两者故障域不交叉
  ——代理挂了只影响升级，不影响任务执行。
- **SSH 协议的 git remote 吃不到它**。git 的 `http.proxy` 只对 `http(s)://` 的
  remote 生效。如果自动登记要 clone 的仓库是 `git@github.com:...`，改用 HTTPS
  地址即可（`git config --global url."https://github.com/".insteadOf git@github.com:`）。

值写错（scheme 不在支持范围、缺主机）时 agentd **启动就会失败并说明原因**——
这是刻意的：后台更新检查那条路径失败时是静默跳过的，坏代理若不在启动期拦下，
表现就是"什么都不发生"，可以数月无人察觉。
```

- [ ] **Step 3: README 升级章节改写分发方向**

找到讲 `handoff upgrade` 的段落，把「二进制由本机下载后推送，执行机无需出网」改为：

```markdown
默认由**执行机自己**去 GitHub 下载：协调者只查一次版本、下一份几百字节的
`checksums.txt`，把 tag 与 sha256 下发过去。这样一次多机升级在协调者与执行机
之间只走几十字节，而不是每台机器一份 20MB 的二进制——走云中转时这个差别是决定性的。

完整性由**协调者下发的** sha256 把关：执行机下完资产比对它，本机代理或镜像被
投毒时会当场被抓住（校验和与资产走两条不同的信任路径）。

执行机确实出不了网时用 `--push` 回退到「本机下载后推送」。对端 agentd 版本过旧
（不认自拉）时会**自动**降级推送，不需要你操心。
```

- [ ] **Step 4: README 排障表加一行**

```markdown
| `upgrade --now` 报失败，或卡着不动 | 默认走执行机自拉，原因在对端。`handoff status --target <名字>` 看 `update.pull_state`：`stage` 是到哪一步、`error` 是原文（代理连不上 / sha256 不符 / 自检没过）。执行机出不了网就用 `handoff upgrade --now --push` |
```

- [ ] **Step 5: 在 B59 spec 里留推翻记录**

在 `docs/superpowers/specs/2026-08-11-update-and-skill-delivery-design.md` 的 D1 决策处（以及 §4.2 数据流处）各加一条醒目标注：

```markdown
> ⚠️ **本条已被 B87 推翻（2026-08-13）**：D1 的「执行机无需出网」不再成立。
> 默认分发方向已反转为**执行机自拉**——协调者只下发 tag + sha256，20MB 的资产
> 由执行机自己去 GitHub 下。推翻的理由是云服务器中转场景下的流量成本（每台机器
> 每次换版 20MB 穿过中转，按机器数与频次线性放大）。推送模式作为 `--push` 回退
> 保留，且对端过旧时自动降级，所以 D1 描述的那条路径仍然可用，只是不再是默认。
> 见 [B87 spec](2026-08-13-proxy-config-and-executor-pull-update-design.md)。
```

**这一步不是可选的**：不留记录，下一个读到 B59 的人会照着一个已经不成立的前提做设计。

- [ ] **Step 6: CHANGELOG 加 Unreleased 小节**

```markdown
### 新增

- `proxy` 配置项：给 handoff 自身出网配代理，支持 `http` / `https` / `socks5` /
  `socks5h`。作用于更新链路与 agentd 的 git clone/fetch；不作用于协调者↔agentd
  链路与 executor（后者仍走 `env` 段）。空值时行为不变（沿用 `HTTPS_PROXY` 等环境变量）。
- `handoff upgrade --push`：强制由本机下载并推送二进制。

### 变更

- `handoff upgrade --now` 默认改为**让执行机自己下载**：协调者只下发 tag 与
  sha256。一次多机升级的跨机流量从每台 20MB 降到几十字节。对端 agentd 过旧时
  自动降级为推送，无需干预。
- `/api/update` 新增 `mode` 查询参数（`pull` / `push`）。省略时行为与此前一字不变。
- `/api/status` 的 `update` 段新增 `pull`（能力位）与 `pull_state`（自拉进度与失败原文）。
```

- [ ] **Step 7: Commit**

```bash
git add README.md CHANGELOG.md docs/superpowers/specs/
git commit -m "docs: proxy 配置项与自拉更新的说明，并在 B59 spec 留推翻记录"
```

---

## 收尾：全量验收闸

全部 task 完成后跑，逐条记录结果：

```bash
go build ./... && go vet ./... && gofmt -l $(git ls-files '*.go') && go test ./... -count=1 && GOOS=windows GOARCH=amd64 go build ./... && go test -race ./internal/agentd/ ./internal/config/ ./internal/proxycfg/ ./cmd/ -count=1
```

**真机验收（必须由人做，实现者不代劳）**：

1. 在一台执行机上配 `proxy: socks5://<可用代理>`，重启 agentd，从协调者跑
   `handoff upgrade --now --target <该机>`，确认 agentd 日志出现「自拉换版开始」
   与「自拉换版完成」，且**协调者侧没有 20MB 的上行流量**。
2. 故意把 `proxy` 配成一个连不上的地址，重跑升级，确认协调者**几秒内**就报失败
   并给出 `handoff status --target X 看 pull_state` 的提示，而不是干等 10 分钟。
3. 用一台还没换版的老 agentd 验降级：确认协调者选路日志 `use_pull=false`，
   走推送成功。
4. `--push` 在一台支持自拉的机器上强制推送，确认走老路成功。
