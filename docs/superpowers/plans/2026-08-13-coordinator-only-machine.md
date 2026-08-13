# 纯协调者机可用性（B84）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让一台不跑 agentd、不具备建软链特权的机器能完整走通派发与审阅回路——本机登记那一跳连不上就降级，skill 落点由目录软链改为实体副本。

**Architecture:** 两处改动互相独立。① `internal/client` 新增 `ErrUnreachable` 哨兵，判据是「一个 HTTP 响应都没拿到」；`cmd/project.go` 的两跳登记只在**本机**那一跳、只对这个哨兵降级，拿到响应的失败一律仍然致命。② `internal/skill` 把四个 agent 落点从软链改成各自一份副本，全平台一条路径，不加 `runtime.GOOS` 分支。

**Tech Stack:** Go 1.26.1、标准库 `log/slog`、cobra v1.10.2、httptest。**不引入任何新依赖。**

**Spec:** [2026-08-13-coordinator-only-machine-design.md](../specs/2026-08-13-coordinator-only-machine-design.md)

**分支:** `handoff/coordinator-only-machine`（已创建，spec 提交 `4661c012` 在上面）

## Global Constraints

- **不引入新依赖**：`go.mod` 的 require 段不得新增条目。
- **注释与日志一律中文**，跟随仓库既有风格；新文件必须有文件头注释（职责 + 边界），导出符号必须有 doc 注释。
- **日志用 `log/slog`，禁止 `fmt.Printf` 作为日志机制**。CLI 侧直接用 `slog.Info/Warn`（默认 handler 写 stderr，与 `cmd/init.go`、`cmd/upgrade.go` 既有做法一致），不需要 `logx.Setup`。
- **stdout 契约不可破**：`dispatch` 的 stdout 第一行必须是任务 JSON；一切人类可读的提示走 `cmd.ErrOrStderr()`。
- **`GOOS=windows GOARCH=amd64 go build ./...` 必须保持绿**（`internal/prochost.TestWindowsCrossCompiles` 是既有门禁）。
- **测试里模拟「连不上」一律用 `127.0.0.1:1`**，不要用 `127.0.0.1:7777`——开发机上很可能真的跑着 agentd，用 7777 会让用例在作者机器上偶然通过、在 CI 上又是另一种结果。
- 每个 task 结束时 `gofmt -l .` 无输出。

---

### Task 1: client 层的 `ErrUnreachable` 哨兵

**Files:**
- Modify: `internal/client/client.go`（`do()` 在 219-239 行；哨兵声明加在 `ErrStatusUnsupported`（266 行）旁边）
- Test: `internal/client/unreachable_test.go`（新建）

**Interfaces:**
- Consumes: 无（本 task 是最底层）
- Produces: `var client.ErrUnreachable error` —— 供 Task 2 用 `errors.Is(err, client.ErrUnreachable)` 判别。语义：**这次请求一个 HTTP 响应都没拿到**。所有经 `Client.do` 的方法（`ProjectAdd` / `ProjectList` / `Dispatch` / `Attach` / …）的传输失败都会带上它。

- [ ] **Step 1: 写失败的测试**

新建 `internal/client/unreachable_test.go`：

```go
// unreachable_test.go —— ErrUnreachable 哨兵的判别边界。
//
// 三条用例锁死同一件事的三个面：够不着要判为够不着，拿到了响应的失败不许
// 判为够不着，ctx 取消也不许判为够不着。中间那条是这个哨兵存在的理由——
// 调用方据它决定「降级继续」还是「就此失败」，判错方向就是脏登记或假失败。
package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/client"
)

// testOrigin 是三条用例共用的请求体填充值，内容本身不参与断言。
const testOrigin = "git@example.com:x/handoff.git"

// TestUnreachableWhenNoResponse：对着无人监听的端口请求 → 判为够不着。
//
// 为什么用端口 1：它必定 connection refused，且不发起任何真实网络访问，
// 在 CI 与离线机器上结论一致。
func TestUnreachableWhenNoResponse(t *testing.T) {
	cl := client.New("http://127.0.0.1:1", "tok")
	_, err := cl.ProjectAdd(context.Background(), client.ProjectAddOpts{OriginURL: testOrigin})
	if err == nil {
		t.Fatal("对着无人监听的端口请求应失败")
	}
	if !errors.Is(err, client.ErrUnreachable) {
		t.Fatalf("应判为够不着，实得 %v", err)
	}
}

// TestNotUnreachableOnHTTPError：409 拿到了响应 → 不是够不着。
//
// 能收到 409 说明 TCP 通、HTTP 正常、Bearer 已过——这是真冲突，调用方
// 必须失败而不是降级继续，否则就是往表里写脏登记。
func TestNotUnreachableOnHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "路径已被另一个项目占用", http.StatusConflict)
	}))
	defer ts.Close()

	cl := client.New(ts.URL, "tok")
	_, err := cl.ProjectAdd(context.Background(), client.ProjectAddOpts{OriginURL: testOrigin})
	if err == nil {
		t.Fatal("409 应返回错误")
	}
	if errors.Is(err, client.ErrUnreachable) {
		t.Fatalf("拿到了响应就不是够不着：%v", err)
	}
}

// TestNotUnreachableOnContextCancel：ctx 取消 → 不是够不着，且保留 context.Canceled。
//
// 为什么必须单列：取消同样从 hc.Do 的错误返回出来。混进 ErrUnreachable
// 会让调用方的降级分支在用户按下 Ctrl-C 之后继续往下走。
func TestNotUnreachableOnContextCancel(t *testing.T) {
	block := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // 挂住直到用例结束，保证取消一定发生在响应之前
	}))
	defer func() { close(block); ts.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	cl := client.New(ts.URL, "tok")
	_, err := cl.ProjectAdd(ctx, client.ProjectAddOpts{OriginURL: testOrigin})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应保留 ctx 取消语义，实得 %v", err)
	}
	if errors.Is(err, client.ErrUnreachable) {
		t.Fatalf("ctx 取消不是够不着：%v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/client/ -run 'Unreachable|NotUnreachable' -v`
Expected: 编译失败，`undefined: client.ErrUnreachable`

- [ ] **Step 3: 加哨兵声明**

在 `internal/client/client.go` 里 `ErrStatusUnsupported`（266 行）**之后**追加：

```go
// ErrUnreachable 表示这次请求**一个 HTTP 响应都没拿到**——TCP 拨不通、连接被拒、
// DNS 解析失败或读写中断，对端在不在都无从判断。
//
// why（必须是可判别的哨兵）：调用方要区分「对端不在」与「对端拒绝了这次请求」。
// 后者（400/409/500）拿到了响应，说明 agentd 在、Bearer 通过、语义上真的冲突了，
// 绝不能当成「机器不在」咽下去——那是往登记表里写脏数据。这个区分只有 client
// 知道；让调用方去 grep 错误文本里的 "connection refused" 是把 Go 的错误措辞与
// 平台差异变成契约。同 ErrStatusUnsupported 的理由。
//
// **不包含 ctx 取消与超时**（见 do 里的注释）。
var ErrUnreachable = errors.New("对端 agentd 够不着")
```

- [ ] **Step 4: 改 `do()` 包上哨兵**

把 `internal/client/client.go` 的 `do()` 末尾这一行：

```go
	return c.hc.Do(req)
```

替换为：

```go
	resp, err := c.hc.Do(req)
	if err != nil {
		// ctx 取消/超时不算「够不着」：它们同样从 hc.Do 的错误返回出来，但含义是
		// 「人按了 Ctrl-C」或「主动限时到了」，不是「那台机器不在」。混进
		// ErrUnreachable 会让调用方的降级分支在用户中断之后继续往下走。
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		// Debug 而非 Warn：status 轮询这类热路径会连续撞这里（upgrade 换版后每秒一次），
		// 在这一层打 Warn 会把真正需要注意的失败淹掉。这次够不着是致命还是可降级，
		// 只有调用方知道——由它决定要不要升级成 Warn（见 cmd/project.go 的降级点）。
		c.log().Debug("agentd 请求未拿到响应",
			"method", method, "path", path, "url", c.baseURL, "cause", err)
		return nil, fmt.Errorf("%w: %s %s: %w", ErrUnreachable, method, path, err)
	}
	return resp, nil
```

确认 `internal/client/client.go` 的 import 里已有 `context`、`errors`、`fmt`（都已有，无需改动 import 块）。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/client/ -run 'Unreachable|NotUnreachable' -v`
Expected: 三条全 PASS

- [ ] **Step 6: 跑全包回归**

Run: `go test ./internal/client/ -count=1`
Expected: ok。**特别关注**既有的 `dial_test.go` / `status_test.go` / `ws_backoff_test.go`——它们依赖拨号失败的错误形状；若有用例断言错误文本，改成 `errors.Is` 而不是放宽断言。

- [ ] **Step 7: 补日志与注释自检**

- 传输失败分支已有 Debug 日志，带 method / path / url / cause 四项上下文 ✓
- `ErrUnreachable` 有 doc 注释，说明 why 与不包含的情形 ✓
- ctx 例外分支有「为什么」注释 ✓
- 无 `fmt.Printf` 作为日志 ✓

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go vet ./internal/client/
git add internal/client/client.go internal/client/unreachable_test.go
git commit -m "feat(client): 传输失败带 ErrUnreachable 哨兵

判据是「一个 HTTP 响应都没拿到」，不是 grep 错误文本。ctx 取消与超时
显式排除——它们也从 hc.Do 返回，但含义是人中断了，不是机器不在。"
```

---

### Task 2: 本机登记跳连不上就降级

**Files:**
- Modify: `cmd/project.go:112-144`（`registerProjectBothHops`），import 加 `errors` 与 `log/slog`
- Test: `cmd/project_degrade_test.go`（新建）

**Interfaces:**
- Consumes: `client.ErrUnreachable`（Task 1）
- Produces: `registerProjectBothHops(cmd *cobra.Command, origin, name, localPath, remotePath string) error` —— 签名不变，行为变：本机跳撞 `ErrUnreachable` 且 `targetName != ""` 时降级继续；`targetName == ""` 时仍然报错。Task 3 依赖这个行为。

- [ ] **Step 1: 写失败的测试**

新建 `cmd/project_degrade_test.go`：

```go
// project_degrade_test.go —— 两跳登记在本机 agentd 缺席时的降级行为。
//
// 边界：只覆盖 registerProjectBothHops 这一个编排函数。dispatch 自动登记
// 走同一个函数，它的端到端回归在 dispatch_autoregister_test.go。
package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// projectHopJSON 是一条位置记录的响应体，字段名对齐 proto.ProjectLocation。
const projectHopJSON = `{"project_id":"pid1","name":"handoff",` +
	`"path":"/srv/repos/handoff","origin_url":"git@example.com:x/handoff.git"}`

// newProjectHopServer 起一个只认 POST /api/projects 的假 agentd。
//
// 参数：
//   - status: 期望返回的状态码；200 返回一条位置记录，其余返回错误体
//   - hits:   收到的登记请求计数（用来证明「目标机那一跳真的发出去了」）
//
// 返回：不含 scheme 的 host:port，直接填进测试配置的 addr。
func newProjectHopServer(t *testing.T, status int, hits *atomic.Int32) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		if status != http.StatusOK {
			http.Error(w, "路径已被另一个项目占用", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, projectHopJSON)
	}))
	t.Cleanup(ts.Close)
	return strings.TrimPrefix(ts.URL, "http://")
}

// newHopCmd 造一个只用来承载 ctx 与输出流的裸命令。
//
// 为什么必须 SetContext：cobra v1.10 的 Command.Context() 在未经 Execute
// 时返回 nil，而 http.NewRequestWithContext 收到 nil ctx 会 panic。
func newHopCmd() (*cobra.Command, *bytes.Buffer) {
	c := &cobra.Command{}
	c.SetContext(context.Background())
	var errBuf bytes.Buffer
	c.SetOut(io.Discard)
	c.SetErr(&errBuf)
	return c, &errBuf
}

// TestRegisterDegradesWhenLocalAgentdMissing：本机够不着 + 有 target
// → 目标机照常登记，整体成功，降级说人话。
//
// 这是纯协调者机（含 Windows）首次派发的主路径。
func TestRegisterDegradesWhenLocalAgentdMissing(t *testing.T) {
	var remoteHits atomic.Int32
	remoteAddr := newProjectHopServer(t, http.StatusOK, &remoteHits)
	// listen 指 127.0.0.1:1：必定 refused，且不受开发机上真跑着的 agentd 影响
	cfg := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n"+
		"targets:\n  devbox:\n    addr: \""+remoteAddr+"\"\n    token: \"remote-tok\"\n")
	resetFlags(t)
	configPath = cfg
	targetName = "devbox"

	c, errBuf := newHopCmd()
	err := registerProjectBothHops(c, "git@example.com:x/handoff.git", "", "/home/me/handoff", "")
	if err != nil {
		t.Fatalf("本机够不着不该让整次登记失败: %v", err)
	}
	if got := remoteHits.Load(); got != 1 {
		t.Fatalf("目标机应收到 1 次登记，实得 %d", got)
	}
	s := errBuf.String()
	if !strings.Contains(s, "跳过本机登记") {
		t.Errorf("降级必须说出来，stderr=%q", s)
	}
	if !strings.Contains(s, "handoff project add") {
		t.Errorf("降级必须给补救办法，stderr=%q", s)
	}
}

// TestRegisterFailsOnLocalConflict：本机返回 409 → 整体失败。
//
// 拿到了响应就是真冲突，降级不许吞它——吞了就是脏登记。
func TestRegisterFailsOnLocalConflict(t *testing.T) {
	var localHits, remoteHits atomic.Int32
	localAddr := newProjectHopServer(t, http.StatusConflict, &localHits)
	remoteAddr := newProjectHopServer(t, http.StatusOK, &remoteHits)
	cfg := writeTestConfig(t, "listen: \""+localAddr+"\"\ntoken: \"local-tok\"\n"+
		"targets:\n  devbox:\n    addr: \""+remoteAddr+"\"\n    token: \"remote-tok\"\n")
	resetFlags(t)
	configPath = cfg
	targetName = "devbox"

	c, _ := newHopCmd()
	err := registerProjectBothHops(c, "git@example.com:x/handoff.git", "", "/home/me/handoff", "")
	if err == nil {
		t.Fatal("本机 409 必须整体失败")
	}
	if !strings.Contains(err.Error(), "登记到本机") {
		t.Errorf("报文应指明是哪一跳失败的，实得 %q", err.Error())
	}
	if got := remoteHits.Load(); got != 0 {
		t.Errorf("本机跳失败后不应再打目标机，实得 %d 次", got)
	}
}

// TestRegisterFailsWhenNoLocalAndNoTarget：本机够不着 + 无 target → 报错。
//
// 两跳都没发生时报成功是撒谎。报文必须给出两条出路。
func TestRegisterFailsWhenNoLocalAndNoTarget(t *testing.T) {
	cfg := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n")
	resetFlags(t)
	configPath = cfg
	targetName = ""

	c, _ := newHopCmd()
	err := registerProjectBothHops(c, "git@example.com:x/handoff.git", "", "/home/me/handoff", "")
	if err == nil {
		t.Fatal("两跳都无处登记时必须报错")
	}
	for _, want := range []string{"没有 agentd", "--target"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报文缺少 %q：%q", want, err.Error())
		}
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./cmd/ -run 'TestRegister' -v`
Expected: 三条中 `TestRegisterDegradesWhenLocalAgentdMissing` 与 `TestRegisterFailsWhenNoLocalAndNoTarget` FAIL（前者报「登记到本机: … connection refused」，后者报的是同一条而不是新报文）；`TestRegisterFailsOnLocalConflict` 可能已经 PASS（既有行为本就失败），保留它是为了防降级改动把它一起放过去。

- [ ] **Step 3: 改 `registerProjectBothHops`**

把 `cmd/project.go` 的 113-142 行（`LocalEndpoint()` 起到远端那一跳的打印为止）替换为：

```go
	localAddr, localToken, err := LocalEndpoint()
	if err != nil {
		return err
	}
	// remoteName 默认取调用方给的名字（可空，由目标机从 origin 末段自行派生）；
	// 本机那一跳成功时改用它归一后的名字，让两台机器上的引用名一致
	remoteName := name
	local, err := client.New(localAddr, localToken).ProjectAdd(cmd.Context(), client.ProjectAddOpts{
		OriginURL: origin, Name: name, Path: localPath,
	})
	switch {
	case err == nil:
		remoteName = local.Name
		fmt.Fprintf(cmd.ErrOrStderr(), "本机 %s → %s\n", local.Name, local.Path)
	case errors.Is(err, client.ErrUnreachable):
		// 本机没有 agentd 是纯协调者机的正常形态（spec §1.3）：本机那一行位置
		// 记录是「顺带补上的免费信息」，它不免费的时候就不该否决整次登记。
		// 但两跳都做不成时必须报错——那时候「成功」是假的。
		if targetName == "" {
			return fmt.Errorf("本机没有 agentd（%s 连不上），且未指定 --target：两跳都无处登记。"+
				"在本机 handoff service install 起一个，或用 --target <执行机> 登到那台机器上", localAddr)
		}
		slog.Warn("本机 agentd 够不着，跳过本机登记",
			"addr", localAddr, "origin", origin, "target", targetName, "cause", err)
		// 走 stderr：dispatch 的 stdout 是「第一行任务 JSON」的既有契约。
		// 两行缺一不可——只说「跳过了」，人不知道后果，也不知道怎么补
		fmt.Fprintf(cmd.ErrOrStderr(), "本机 没有 agentd（%s 连不上），跳过本机登记\n", localAddr)
		fmt.Fprintln(cmd.ErrOrStderr(), "     本机项目树会缺这一行；本机起了 agentd 之后重跑 handoff project add 补上")
	default:
		// 拿到了响应的失败（409 位置冲突 / 400 origin 不符 / 500）一律致命：
		// 那是真冲突，咽下去就是往位置表里写脏登记
		return fmt.Errorf("登记到本机: %w", err)
	}
	if targetName == "" {
		slog.Info("项目登记完成", "origin", origin, "scope", "仅本机")
		return nil
	}
	addr, token, err := TargetEndpoint()
	if err != nil {
		return err
	}
	if remotePath == "" {
		// 服务端可能 clone 也可能认领已存在的落点（spec §12），CLI 事前无法分辨，
		// 措辞必须两种结局都成立——写成「克隆」会在认领路径下成为假话。
		fmt.Fprintf(cmd.ErrOrStderr(), "正在让 %s 落地项目 %s（首次需要 clone，可能较慢）…\n", targetName, origin)
	}
	remote, err := client.New(addr, token).ProjectAdd(cmd.Context(), client.ProjectAddOpts{
		OriginURL: origin, Name: remoteName, Path: remotePath,
	})
	if err != nil {
		return fmt.Errorf("登记到 %s: %w", targetName, err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s %s → %s\n", targetName, remote.Name, remote.Path)
	slog.Info("项目登记完成", "origin", origin, "target", targetName, "remote_path", remote.Path)
	return nil
```

同时把 `cmd/project.go` 的 import 块补成：

```go
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/client"
)
```

- [ ] **Step 4: 更新函数 doc 注释**

先订正「注意」段第一条里已成假话的措辞——它现在写着「本机位置已知且免费」，
而本次改动的全部理由就是它不总是免费：

```go
//   - --target 的语义是「本机与那台机器**一起**登记」，不是「只登记那台机器」：
//     项目身份是从 cwd 算的，本机位置已知，登它一并补上本机项目树那一行
```

再在「注意」段末尾追加一条（不要删既有各条）：

```go
//   - **本机那一跳连不上时降级、不失败**：纯协调者机（不跑 agentd）上本机 agentd
//     本就不存在，而本机位置只是「顺带补上的免费信息」（spec §6.2），让它否决
//     整次派发是把记账动作摆在了主线之上。判据只认 client.ErrUnreachable
//     ——拿到了响应的失败（409/400/500）仍然致命，那是真冲突。
//     两跳都做不成（无 --target 且本机连不上）时仍然报错
```

「返回」段那条「任一跳失败即返回」同样与新行为相悖，收紧成：

```go
// 返回：
//   - 错误：任一跳失败即返回（本机跳「够不着」除外，见「注意」）；
//     **不回滚另一跳**（登记是幂等的，重跑即可）
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./cmd/ -run 'TestRegister' -v`
Expected: 三条全 PASS

- [ ] **Step 6: 跑全包回归**

Run: `go test ./cmd/ -count=1`
Expected: ok（既有的 `project_test.go` / `dispatch_test.go` / `root_test.go` 全绿）

- [ ] **Step 7: 日志与注释自检**

- 降级分支：`slog.Warn` 带 addr / origin / target / cause 四项上下文 ✓
- 成功路径两个出口（仅本机、本机+目标机）各有 `slog.Info` 结论日志——**不留静默成功路径** ✓
- 致命分支：错误经 `%w` 上抛，由 cobra 打到 stderr，报文指明是哪一跳 ✓
- 降级、致命、两跳全空三处各有「为什么」注释 ✓
- 无 `fmt.Printf` 作为日志（`fmt.Fprintf(cmd.ErrOrStderr(), …)` 是给人看的界面输出，不是日志）✓

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go vet ./cmd/
git add cmd/project.go cmd/project_degrade_test.go
git commit -m "feat(project): 本机登记跳连不上就降级，不否决整次派发

本机位置只是顺带补上的免费信息，它不免费的时候不该挡住主线。判据只认
client.ErrUnreachable；拿到响应的 409/400/500 仍然致命。两跳都做不成
（无 --target 且本机连不上）时仍然报错。"
```

---

### Task 3: dispatch 自动登记路径的端到端回归

**Files:**
- Test: `cmd/dispatch_autoregister_test.go`（在既有文件末尾追加，不新建文件）

**Interfaces:**
- Consumes: Task 2 改造后的 `registerProjectBothHops`
- Produces: 无生产代码，只加一道回归门。

**为什么单列一个 task：** Task 2 的用例直接调编排函数，证明的是「函数行为对」；这里证明的是**真实入口**——`handoff dispatch` 在目标机不认识这个项目时触发自动登记、本机跳降级、任务照样派出去。两者一个塌了另一个未必红，值得各自一道门。

- [ ] **Step 1: 写失败的测试**

在 `cmd/dispatch_autoregister_test.go` 末尾追加（该文件现有 import 只有 `errors`/`strings`/`testing`/`proto`，需要补全）：

```go
// cleanRepoWithOrigin 在临时目录造一个「有 origin、工作区干净、有一个提交」
// 的仓库并 chdir 进去。dispatch 需要这三样：origin 派生项目身份、
// 干净工作区过 checkLocalWorktree、HEAD 算基线。
func cleanRepoWithOrigin(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	git("remote", "add", "origin", "git@example.com:x/handoff.git")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")
	t.Chdir(repo)
	return repo
}

// TestDispatchAutoRegisterSurvivesMissingLocalAgentd 是纯协调者机首次派发的
// 端到端回归：目标机不认识这个项目 → CLI 自动登记 → 本机那一跳够不着被降级
// → 目标机登记成功 → 重发派发成功。
//
// 修复前的症状：整条命令停在「登记到本机: … connection refused」，
// 目标机那一跳一个字节都没发出去。
func TestDispatchAutoRegisterSurvivesMissingLocalAgentd(t *testing.T) {
	cleanRepoWithOrigin(t)

	var taskHits, projectHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/projects":
			projectHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, projectHopJSON)
		case "/api/tasks":
			// 第一次派发：目标机不认识这个项目，报文以「项目未登记」开头
			// （CLI 靠这四个字触发自动登记，见 isProjectNotRegistered）
			if taskHits.Add(1) == 1 {
				http.Error(w, "项目未登记: project_id=pid1；本机已登记的项目：（本机尚无任何项目）",
					http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, dispatchTestTaskJSON)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	addr := strings.TrimPrefix(ts.URL, "http://")
	cfg := writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"local-tok\"\n"+
		"targets:\n  devbox:\n    addr: \""+addr+"\"\n    token: \"remote-tok\"\n")
	resetFlags(t)
	configPath = cfg
	targetName = "devbox"
	agentdURL = "http://127.0.0.1:7777"
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	t.Cleanup(func() { dispatchNoTerminal = false })

	rootCmd.SetArgs([]string{"dispatch", "--target", "devbox", "--prompt", "x", "--no-terminal"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	if err := Execute(); err != nil {
		t.Fatalf("本机没有 agentd 时首次派发应当成功: %v（stderr=%q）", err, errBuf.String())
	}
	if got := projectHits.Load(); got != 1 {
		t.Errorf("目标机应收到 1 次登记，实得 %d", got)
	}
	if got := taskHits.Load(); got != 2 {
		t.Errorf("派发应发生 2 次（首拒 + 重发），实得 %d", got)
	}
	if !strings.Contains(errBuf.String(), "跳过本机登记") {
		t.Errorf("降级必须说出来，stderr=%q", errBuf.String())
	}
	// stdout 契约：第一行必须是任务 JSON，降级提示一个字都不许漏进来
	first := strings.SplitN(strings.TrimSpace(out.String()), "\n", 2)[0]
	if !strings.HasPrefix(first, "{") {
		t.Errorf("stdout 第一行必须是任务 JSON，实得 %q", first)
	}
}
```

import 块补成：

```go
import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)
```

- [ ] **Step 2: 跑测试**

Run: `go test ./cmd/ -run TestDispatchAutoRegisterSurvivesMissingLocalAgentd -v`
Expected: PASS。**若 FAIL 在 `checkLocalWorktree` 或同步检查上**（报文含「未提交」或「未推送」），给命令行追加 `--no-sync-check`，并在用例注释里写明为什么加——不要改生产代码去迁就测试。

- [ ] **Step 3: 提交**

```bash
gofmt -l . && go test ./cmd/ -count=1
git add cmd/dispatch_autoregister_test.go
git commit -m "test(dispatch): 纯协调者机首次派发的端到端回归

目标机不认识项目 → 自动登记 → 本机跳降级 → 派发成功，
并钉住 stdout 第一行仍是任务 JSON。"
```

---

### Task 4: skill 落点由软链改副本

**Files:**
- Modify: `internal/skill/install.go`（文件头注释、`BasePath` 注释、`skillDirName` 注释、`Install` 的落点循环 96-110 行）
- Modify: `internal/skill/state.go:47`（软链那句注释）
- Modify: `internal/skill/install_test.go`（`TestInstallLinksPointAtBase` 改写 + 新增迁移用例）
- Modify: `cmd/skill.go`（`install` 子命令补日志）

**Interfaces:**
- Consumes: 无
- Produces: `skill.Install(content, home string) ([]Site, error)` —— 签名不变，落点形态从软链变实体目录。`Site` / `State*` 常量全部不变，`Status` / `InSync` 不需要改。

- [ ] **Step 1: 改写会作废的测试 + 写新的失败测试**

在 `internal/skill/install_test.go` 里，把 `TestInstallLinksPointAtBase`（67-80 行）**整个替换**为下面两个用例：

```go
// TestInstallWritesRealCopies 锁住落点形态：四家各自一份实体副本，内容与
// 基准一致。软链已废弃——它在 Windows 上需要管理员特权，而它买的
// 「改一处生效四处」在 go:embed + 每次全量重写的模型里收益为零（B84）。
func TestInstallWritesRealCopies(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".claude", ".codex", ".config/opencode", ".grok"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Install("内容 v1", home); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		".claude/skills", ".codex/skills", ".config/opencode/skills", ".grok/skills",
	} {
		p := filepath.Join(home, rel, "handoff", "SKILL.md")
		fi, err := os.Lstat(filepath.Join(home, rel, "handoff"))
		if err != nil {
			t.Fatalf("落点 %s 不存在: %v", rel, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			t.Errorf("落点 %s 仍是软链，应为实体目录", rel)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读落点副本 %s: %v", p, err)
		}
		if string(b) != "内容 v1" {
			t.Errorf("落点 %s 内容 = %q，期望 %q", rel, b, "内容 v1")
		}
	}
}

// TestInstallMigratesLegacySymlink：老装机的落点是指向基准副本的软链，
// 必须能被就地换成实体副本，**且基准副本还在**。
//
// why 必须钉死后半句：RemoveAll 对软链是摘链不删目标——这是本次改动唯一
// 会咬人的语义。万一哪天改成了先解析再删，基准副本会被连带删掉，而症状
// 是「装完之后 handoff status 说四个落点都在、基准却没了」。
func TestInstallMigratesLegacySymlink(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".grok", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 先手工造一个老形态：基准副本 + 指向它的软链
	base := BasePath(home)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "SKILL.md"), []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".grok", "skills", "handoff")
	if err := os.Symlink(base, link); err != nil {
		t.Skipf("本平台建不了软链，迁移用例无从构造: %v", err)
	}

	if _, err := Install("新内容", home); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("落点应存在: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("老软链应被换成实体目录")
	}
	b, err := os.ReadFile(filepath.Join(link, "SKILL.md"))
	if err != nil || string(b) != "新内容" {
		t.Fatalf("落点副本 = %q (err=%v)，期望 %q", b, err, "新内容")
	}
	// 基准副本必须还在，且已被同步成新内容
	bb, err := os.ReadFile(filepath.Join(base, "SKILL.md"))
	if err != nil {
		t.Fatalf("基准副本被误删了: %v", err)
	}
	if string(bb) != "新内容" {
		t.Errorf("基准副本 = %q，期望 %q", bb, "新内容")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/skill/ -run 'RealCopies|MigratesLegacy' -v`
Expected: `TestInstallWritesRealCopies` FAIL（落点是软链，`Lstat` 判定 ModeSymlink 命中）；`TestInstallMigratesLegacySymlink` FAIL（同上）

- [ ] **Step 3: 改 `Install` 的落点循环**

`internal/skill/install.go` 里，把变量名 `link` 全部改成 `site`（它不再是链），并把建链两行换成建目录 + 写文件。改完后的循环体为：

```go
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
		site := filepath.Join(dir, skillDirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			sites = append(sites, Site{Path: site, State: StateSkipped, Note: "创建目录失败: " + err.Error()})
			continue
		}
		// 先删再建：目标可能是上一次装的软链（老形态），也可能是手工放的实体目录。
		// RemoveAll 对软链只摘链、不动链指向的基准副本——迁移正是靠这条语义
		if err := os.RemoveAll(site); err != nil {
			sites = append(sites, Site{Path: site, State: StateSkipped, Note: "清理旧落点失败: " + err.Error()})
			continue
		}
		if err := os.MkdirAll(site, 0o755); err != nil {
			sites = append(sites, Site{Path: site, State: StateSkipped, Note: "创建落点目录失败: " + err.Error()})
			continue
		}
		if err := os.WriteFile(filepath.Join(site, fileName), []byte(content), 0o644); err != nil {
			sites = append(sites, Site{Path: site, State: StateSkipped, Note: "写落点副本失败: " + err.Error()})
			continue
		}
		sites = append(sites, Site{Path: site, State: StateInstalled})
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/skill/ -count=1 -v`
Expected: 全 PASS，包括既有的 `TestInstallSkipsMissingAgentDirs`（已装 2 / 跳过 3 的计数不变）、`TestInstallIsIdempotent`、`TestInstallReplacesRealDirectory`

- [ ] **Step 5: 订正注释（软链措辞已成假话）**

四处，逐一改掉：

1. `internal/skill/install.go` 文件头「给**存在的** agent 目录建软链指向基准副本」→
   「在**存在的** agent 目录里各写一份副本」
2. `BasePath` 上方那段「为什么用副本而不是让四家都软链到仓库」整段替换为：

```go
// BasePath 返回基准副本目录。
//
// 为什么四家各存一份副本、而不是软链到这里或软链到仓库：软链到仓库会在仓库
// 切分支/移动时让四个 agent 一起失效；软链到基准副本看似便宜，买到的
// 「改一处生效四处」却是零收益——内容来自 go:embed，Install 每次运行本来
// 就全量重写所有落点，没人手改基准副本（手改了也会被 Status 判成 stale）。
// 而它的代价是实打实的：Windows 上建目录软链需要管理员特权，非特权时四个
// 落点全部装不上，症状还是静默半残（B84）。副本形态全平台一条路径。
func BasePath(home string) string { return filepath.Join(home, ".handoff", "skill") }
```

3. `skillDirName` 的注释「软链在各家 skills 目录下的名字」→「落点目录在各家 skills 目录下的名字」
4. `internal/skill/state.go:47` 的「经软链读到的就是基准副本；落点是实体目录时读到的是它自己那份」→
   「落点是各自一份副本；老装机残留的软链读到的是基准副本——两种形态都按内容比对，本函数不关心是哪种」

顺带确认 `Install` 的 doc 注释里没有「软链」字样（有就一并改成「副本」）。

- [ ] **Step 6: 给 `skill install` 补日志**

`cmd/skill.go` 的 `install` 子命令 RunE 里，`sites` 拿到之后、打印之前插入：

```go
		// 逐个落点数出结论：安装是个「部分成功」的操作，只把表打给人看，
		// 事后排查（比如「为什么这台机器的 codex 没有 skill」）就没有任何痕迹
		var installed, skipped int
		for _, s := range sites {
			if s.State == skill.StateSkipped {
				skipped++
				slog.Warn("skill 落点跳过", "path", s.Path, "reason", s.Note)
				continue
			}
			installed++
		}
		slog.Info("skill 安装完成", "home", home, "installed", installed, "skipped", skipped)
```

`cmd/skill.go` 的 import 补 `"log/slog"`。

- [ ] **Step 7: 日志与注释自检**

- 每个跳过的落点各有一条带 path + reason 的 Warn ✓
- 成功路径有结论日志（installed / skipped 计数）——**不留静默成功路径** ✓
- 四处「软链」措辞已订正，注释不再与代码相悖 ✓
- `internal/skill` 保持纯净（不引 slog）：它把处置放在返回的 `Site.Note` 里，由命令层落日志——这是既有边界，不在本次打破 ✓

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go vet ./internal/skill/ ./cmd/
git add internal/skill/install.go internal/skill/state.go internal/skill/install_test.go cmd/skill.go
git commit -m "feat(skill): 落点由软链改实体副本，全平台一条路径

Status 本就按内容比对、不关心落点形态，改副本零改动即兼容。软链买的
「改一处生效四处」在 go:embed + 每次全量重写下收益为零，代价是 Windows
非管理员装不上且静默半残。老装机的软链由 RemoveAll 摘链后自动迁移。"
```

---

### Task 5: 收口——文档、门禁、backlog

**Files:**
- Modify: `README.md`（协调者/安装章节）
- Modify: `docs/superpowers/backlog.md`（B84 行）

- [ ] **Step 1: README 补一句「纯协调者机不需要本机 agentd」**

在 README「**2. 托管 agentd**」那段之后（讲 `init` 选了执行机角色顺带装 agentd 的那一段，
以「托管后 Ctrl-C 停不掉它…」结尾），另起一段追加：

```markdown
**只当协调机时不需要本机 agentd**：派发、`wait`、`reply`、`diff`、`attach` 全部直连
目标机的 agentd。首次给一个新项目派发时，CLI 会顺带把项目也登记到本机一份（用于
`handoff project ls` 的本机项目树）；本机没有 agentd 时这一跳自动跳过并提示，不影响
派发本身。
```

- [ ] **Step 2: README 给 Windows 那句补两条限制**

安装章节现有这一句（README 第 26 行前后）：

```markdown
macOS / Linux（amd64 / arm64）。Windows 暂不支持作为执行机，只能当协调机——协调者侧命令（dispatch / wait / reply / diff 等）可用，但没有安装脚本与 release 资产，需自行 `go build`。
```

方向已经对，缺两条会让人踩坑的后果，补成：

```markdown
macOS / Linux（amd64 / arm64）。Windows 暂不支持作为执行机，只能当协调机——协调者侧命令（dispatch / wait / reply / diff 等）可用，但没有安装脚本与 release 资产，需自行 `go build`；因此 `handoff upgrade` 升不了本机这一份（升远端执行机不受影响），`wait --notify` 的桌面通知也只有 macOS 有。
```

- [ ] **Step 3: 跑六闸门**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
GOOS=windows GOARCH=amd64 go build ./...
go test -race ./cmd/ ./internal/client/ ./internal/skill/
```

Expected: 全绿，`gofmt -l .` 无输出。任一条红就地修掉，不许带着红提交。

- [ ] **Step 4: 更新 backlog B84 行**

把状态 `📋 specced` 改成 `🔨 doing`→`✅ done(已验)`（按实际验收结果），并在「验收」列写入 Step 3 的实跑结论（哪些命令、多少包 ok、日期）。**真机验收诚实记账**：没有 Windows 机器实跑过 spec §5 那条端到端，就在验收列明写「Windows 真机未验（无机器）；macOS 上以 `listen: 127.0.0.1:1` 模拟本机 agentd 缺席，覆盖降级路径」。不许把模拟写成真机。

- [ ] **Step 5: 提交**

```bash
git add README.md docs/superpowers/backlog.md
git commit -m "docs(b84): README 补纯协调者机说明，backlog 记验收"
```

---

## Self-Review

**Spec 覆盖：**

| Spec 条目 | 落在哪 |
|---|---|
| §2.1 `ErrUnreachable` 落在 client 层、判据是「没拿到响应」 | Task 1 Step 3-4 |
| §2.1 排除 ctx 取消/超时 | Task 1 Step 4 + 测试三 |
| §2.1 范围只覆盖 `do()`、不动 `doStream()` | Task 1 Step 4（只改 `do`） |
| §2.2 只有本机跳降级、拿到响应的失败仍致命 | Task 2 Step 3 + 测试二 |
| §2.2 目标机那一跳永不降级 | Task 2 Step 3（远端分支未加降级） |
| §2.3 两跳全空时报错 | Task 2 Step 3 + 测试三 |
| §2.4 文案走 stderr、说后果 + 补救、同时打 Warn | Task 2 Step 3 + Step 7 |
| §3.2 软链改副本、全平台一条路径 | Task 4 Step 3 |
| §3.3 老装机迁移靠 `RemoveAll` 摘链 | Task 4 Step 1（迁移用例）+ Step 3 注释 |
| §5 测试 1-3（client 层） | Task 1 Step 1 |
| §5 测试 4-6（cmd 层） | Task 2 Step 1 |
| §5 测试 7（dispatch 自动登记） | Task 3 |
| §5 测试 8-10（skill 层） | Task 4 Step 1、Step 4 |
| §5 平台门禁 + 真机验收记账 | Task 5 Step 3、Step 4 |
| §4 非目标（不发 Windows 资产 / 不补通知 / `project ls` 不管） | 无 task——**故意的**，README 里如实说明现状（Task 5 Step 2） |

**占位符扫描：** 无 TBD / TODO / 「类似 Task N」/ 「加上适当的错误处理」。每个代码步骤都给了可直接落盘的代码。

**类型一致性：** `client.ErrUnreachable`（Task 1 产出）→ Task 2 Step 3 `errors.Is` 引用，名字一致；`registerProjectBothHops` 签名未变，Task 3 经 `Execute()` 间接调用；`skill.Install` 签名未变，`Site` / `State*` 常量未动，故 `Status` / `InSync` / `cmd/skill.go` 的渲染逻辑无需改。`projectHopJSON` 在 Task 2 定义（`cmd` 包），Task 3 同包复用——**两个 task 都在 `cmd` 包，不要重复声明**。
