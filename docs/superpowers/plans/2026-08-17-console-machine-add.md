# 控制台新增/删除开发机 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让用户能在控制台里新增和删除远程开发机，从而把远程配对从首次配置向导里移出去。

**Architecture:** agentd 今天从不写自己的配置，且 `Server.cfg` 被并发 handler 直接读。先把配置快照改成 `atomic.Pointer[config.Config]`（读取一律经 `s.conf()`，写入走写时复制 + 落盘 + 失败回滚），再在其上加 `POST /api/machines` 与 `DELETE /api/machines/{name}`，最后接前端。新增时默认做一次可达性探测，探不通给出原文并允许显式强存。

**Tech Stack:** Go 1.26（`net/http` 的 Go 1.22 路由模式、`sync/atomic.Pointer`）、既有 `internal/client` 探活、React + TypeScript + Vitest。

## Global Constraints

以下为 spec 的项目级约束，**每个 task 的需求都隐含包含本节**：

- **token 只进不出**：请求体接受 token，任何**响应体**与任何**日志**都不得包含它。日志只打机器名与地址。
- 配置的真相只有一份：`~/.handoff/config.yaml` 的 `targets` 段。**不建第二张表**。
- 落盘失败必须回滚内存快照——内存与磁盘不一致会造成「加了一台机器、重启后消失」这种最难查的现象。
- agentd 的错误响应统一 `writeJSON(w, code, map[string]string{"error": "..."})`（与 `server.go:429` 等处一致）。
- 日志用 `s.log`（`*slog.Logger`），**禁止 `fmt.Printf`**。
- 新建文件顶部写「职责 + 边界」注释；导出方法写 doc 注释（参数、返回、注意事项）；非显然分支写「为什么」的中文注释。
- 探测请求只发往请求体里给定的 addr，**不跟随任何跳转**。
- 不改 `config.Load` 的首次运行语义；不做控制台的完整设置面（只做开发机增删）。

## File Structure

| 文件 | 责任 |
|---|---|
| `internal/agentd/server.go`（改） | `Server.cfg` 改 `atomic.Pointer[config.Config]`，新增 `cfgPath` 字段、`conf()` / `swapConf()` / `SetConfigPath()`；注册两条新路由 |
| `internal/agentd/*.go`（改，14 个文件共 30 处） | 所有 `s.cfg.X` 改为 `s.conf().X` |
| `internal/agentd/machineadmin.go`（新建） | 开发机增删的**领域逻辑**：校验、写时复制、落盘。不含 HTTP 编解码，不做网络探测 |
| `internal/agentd/machineadmin_test.go`（新建） | 上者的表驱动用例 + 并发竞态用例 |
| `internal/agentd/machines.go`（改） | 新增 `handleAddMachine` / `handleDeleteMachine`；可达性探测在这一层 |
| `internal/proto/projects.go`（改） | 新增 `AddMachineReq` |
| `cmd/agentd.go`（改） | `srv.SetConfigPath(p)` |
| `web/src/api/types.ts`（改） | 新增 `AddMachineReq` |
| `web/src/api/client.ts`（改） | 新增 `addMachine` / `deleteMachine` |
| `web/src/app/machines/MachinesPage.tsx`（改） | 「新增开发机」按钮与表单、删除入口与二次确认 |

**为什么单开 `machineadmin.go` 而不是并进 `machines.go`**：后者的文件头注释明确写着「只读」「不建表」，它的职责是投影与探活。写操作是另一件事，混进去会让那句边界声明当场失效。

---

## Task 1: 配置快照原子化

**Files:**
- Modify: `internal/agentd/server.go`（`Server` 结构体、`NewServer`）
- Modify: `internal/agentd/{eventframes,forward,forward_ws,frames_stream,hostguard,machines,projectfanout,projecttree,pty_api,render_stream,taskroute,tasksfanout,workspacefiles}.go`（共 30 处 `s.cfg.` → `s.conf().`）
- Modify: `cmd/agentd.go:146` 附近
- Test: `internal/agentd/machineadmin_test.go`（新建，本 task 只放竞态用例）

**Interfaces:**
- Consumes: `config.Save(path string, cfg *Config) error`（`internal/config/config.go:525`）
- Produces:
  - `func (s *Server) conf() *config.Config`
  - `func (s *Server) swapConf(mutate func(*config.Config) error) error`
  - `func (s *Server) SetConfigPath(p string)`

- [ ] **Step 1: 写失败的竞态测试**

新建 `internal/agentd/machineadmin_test.go`：

```go
package agentd

import (
	"fmt"
	"log/slog"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/store"
)

// newAdminServer 造一台只用于配置读写测试的 Server：真实临时配置文件 + 空存储。
func newAdminServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{
		Listen:  "127.0.0.1:0",
		Token:   "t",
		DataDir: dir,
		Targets: map[string]config.Target{},
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("准备配置失败: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "handoff.db"))
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := NewServer(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.SetConfigPath(cfgPath)
	return s
}

// 并发读快照 + 并发换配置不得报竞态，且不得丢更新。
// 用 -race 跑才有意义。
func TestConfSnapshotConcurrent(t *testing.T) {
	s := newAdminServer(t)
	const writers = 10
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = len(s.conf().Targets) }()
	}
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("m%d", i)
			if err := s.swapConf(func(c *config.Config) error {
				c.Targets[name] = config.Target{Addr: "127.0.0.1:1", Token: "x"}
				return nil
			}); err != nil {
				t.Errorf("换配置失败: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := len(s.conf().Targets); got != writers {
		t.Fatalf("并发写丢更新：期望 %d 台，实际 %d 台", writers, got)
	}
}

// 落盘失败时内存快照必须回滚，否则重启后配置凭空消失。
func TestSwapConfRollbackOnSaveFailure(t *testing.T) {
	s := newAdminServer(t)
	// 把配置路径指到一个不可写的位置：父目录不存在
	s.SetConfigPath(filepath.Join(t.TempDir(), "nope", "config.yaml"))
	before := len(s.conf().Targets)
	err := s.swapConf(func(c *config.Config) error {
		c.Targets["x"] = config.Target{Addr: "127.0.0.1:1", Token: "x"}
		return nil
	})
	if err == nil {
		t.Fatal("落盘应当失败")
	}
	if got := len(s.conf().Targets); got != before {
		t.Fatalf("落盘失败后内存未回滚：期望 %d 台，实际 %d 台", before, got)
	}
	_ = os.Remove("")
}

// mutate 返回错误时不得落盘、不得换快照。
func TestSwapConfMutateErrorAborts(t *testing.T) {
	s := newAdminServer(t)
	sentinel := fmt.Errorf("不干了")
	err := s.swapConf(func(c *config.Config) error {
		c.Targets["x"] = config.Target{Addr: "127.0.0.1:1", Token: "x"}
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("应原样返回 mutate 的错误，实际 %v", err)
	}
	if len(s.conf().Targets) != 0 {
		t.Fatal("mutate 失败后不该换快照")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestConfSnapshot|TestSwapConf' -count=1`
Expected: 编译失败，`s.conf undefined` / `s.swapConf undefined` / `s.SetConfigPath undefined`

- [ ] **Step 3: 改 Server 结构体与构造**

`internal/agentd/server.go`，把 `cfg *config.Config` 换掉，并订正结构体的并发声明：

```go
// Server 是 agentd 的 HTTP/WS 服务端，持有配置、存储与进程内实时路由 hub。
//
// 并发安全：**除 cfg 外**所有字段只读（构造后不变），hub 自身线程安全。
// cfg 是可变的——控制台增删开发机会整体换掉它（写时复制），因此一律经
// s.conf() 读取；**禁止再引入直接持有 *config.Config 的字段**，那会让
// 同样的错误从编译错误退化成静默竞态。
type Server struct {
	cfg atomic.Pointer[config.Config]
	// cfgMu 只序列化写入方（swapConf）。读取方走 atomic 快照，不加锁。
	// 它防的是「两个写入方各自读到同一份旧配置、后写者覆盖前写者」的丢更新。
	cfgMu sync.Mutex
	// cfgPath 是配置文件路径，写配置时落盘用。由 SetConfigPath 注入
	//（与 mgr 同款：NewServer 有 50 个调用点，改签名的代价远大于收益）。
	// 未注入时 swapConf 直接报错，绝不猜一个路径写下去。
	cfgPath string
	st  *store.Store
	// ……其余字段不变
}
```

`NewServer` 里把 `cfg: cfg,` 从字面量里去掉，改为构造后 `s.cfg.Store(cfg)`（`atomic.Pointer` 不能用字面量初始化）。

- [ ] **Step 4: 加三个方法**

追加到 `internal/agentd/server.go`：

```go
// conf 返回当前配置快照。
//
// 返回的指针在调用方持有期间恒定：写入方永不原地修改 Config，只整体换新，
// 因此读者看到的始终是一份自洽的配置，而不是改到一半的状态。
func (s *Server) conf() *config.Config { return s.cfg.Load() }

// SetConfigPath 注入配置文件路径，供写配置时落盘。
//
// 参数：
//   - p: 配置文件绝对路径；空串表示不允许写配置（swapConf 会报错）
//
// 注意：与 SetManager 同款的构造后注入，必须在 Handler 开始服务前调用。
func (s *Server) SetConfigPath(p string) { s.cfgPath = p }

// swapConf 以写时复制的方式修改配置并落盘。
//
// 参数：
//   - mutate: 在一份可安全修改的副本上施加改动；返回非 nil 则整体中止，
//     既不换快照也不落盘
//
// 返回：
//   - mutate 的错误、或落盘错误；成功时 nil
//
// 注意：
//   - 落盘失败会**回滚内存快照**。内存与磁盘不一致会让「加了机器、重启后
//     消失」这种最难查的现象出现，宁可整个操作失败
//   - 只深拷贝 Targets 这一层。其余字段在 agentd 运行期不可变，共享是安全的
func (s *Server) swapConf(mutate func(*config.Config) error) error {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	old := s.conf()
	next := *old
	next.Targets = make(map[string]config.Target, len(old.Targets)+1)
	for k, v := range old.Targets {
		next.Targets[k] = v
	}
	if err := mutate(&next); err != nil {
		return err
	}
	if s.cfgPath == "" {
		s.log.Error("未注入配置文件路径，拒绝写配置")
		return errors.New("agentd 未注入配置文件路径，无法写配置")
	}
	s.cfg.Store(&next)
	if err := config.Save(s.cfgPath, &next); err != nil {
		s.cfg.Store(old) // 磁盘没写成，内存也不能算数
		s.log.Error("配置落盘失败，已回滚内存快照", "path", s.cfgPath, "cause", err)
		return fmt.Errorf("保存配置 %s: %w", s.cfgPath, err)
	}
	s.log.Info("配置已更新并落盘", "path", s.cfgPath, "targets", len(next.Targets))
	return nil
}
```

- [ ] **Step 5: 把 30 处 `s.cfg.` 改成 `s.conf().`**

Run: `grep -rn "s\.cfg\." internal/agentd/*.go | grep -v _test`
逐处替换为 `s.conf().`。编译器保证覆盖完全——`cfg` 字段类型已变，任何漏改都是编译错误。

- [ ] **Step 6: 注入配置路径**

`cmd/agentd.go`，在 `srv := agentd.NewServer(cfg, st, logger)` 之后紧接一行（`p` 是同一个 RunE 里已算好的配置路径）：

```go
srv.SetConfigPath(p)
```

- [ ] **Step 7: 跑测试确认通过（必须带 -race）**

Run: `go test ./internal/agentd/ -run 'TestConfSnapshot|TestSwapConf' -race -count=1`
Expected: PASS，无 `DATA RACE` 报告

- [ ] **Step 8: 跑全量回归**

Run: `go test ./... -count=1`
Expected: 全部包 ok（本 task 不改任何行为，只改读取方式）

- [ ] **Step 9: Commit**

```bash
git add internal/agentd cmd/agentd.go
git commit -m "refactor(agentd): 配置快照改原子指针，为增删开发机让路

Server.cfg 原本被并发 handler 直接读且构造后不变，加入写操作会是数据竞态。
改为 atomic.Pointer + 写时复制：读走 s.conf() 无锁快照，写走 swapConf
（cfgMu 序列化、落盘失败回滚内存）。删掉 cfg 字段使得任何漏改都变成编译
错误，而不是静默竞态。配置路径按 SetManager 同款注入（NewServer 有 50 个
调用点，改签名代价远大于收益）。"
```

---

## Task 2: 增删开发机的领域逻辑

**Files:**
- Create: `internal/agentd/machineadmin.go`
- Modify: `internal/proto/projects.go`（新增 `AddMachineReq`）
- Test: `internal/agentd/machineadmin_test.go`（追加）

**Interfaces:**
- Consumes: `s.conf()`、`s.swapConf(func(*config.Config) error) error`（Task 1）
- Produces:
  - `proto.AddMachineReq{Name, Addr, Token, User string; Force bool}`
  - `var ErrMachineExists, ErrMachineNotFound error`
  - `func (s *Server) addMachine(req proto.AddMachineReq) error`
  - `func (s *Server) removeMachine(name string) error`

- [ ] **Step 1: 加请求体类型**

`internal/proto/projects.go` 追加：

```go
// AddMachineReq 是 POST /api/machines 的请求体。
//
// **Token 只进不出**：本结构仅用于反序列化请求。任何响应体、任何日志
// 都不得包含它——proto.Machine 从设计之初就没有 Token 字段，这条性质
// 必须保持。
//
// Force=true 跳过可达性探测直接落库，用于「对端临时离线但确认地址无误」
// 的场景；默认 false，让粘错的地址或令牌当场暴露。
type AddMachineReq struct {
	Name  string `json:"name"`
	Addr  string `json:"addr"`
	Token string `json:"token"`
	User  string `json:"user"`
	Force bool   `json:"force"`
}
```

- [ ] **Step 2: 写失败的测试**

`internal/agentd/machineadmin_test.go` 追加：

```go
func TestValidateAddMachine(t *testing.T) {
	existing := map[string]config.Target{"dup": {Addr: "1.2.3.4:7777", Token: "t"}}
	cases := []struct {
		name    string
		req     proto.AddMachineReq
		wantErr bool
		isDup   bool
	}{
		{"正常", proto.AddMachineReq{Name: "box", Addr: "10.0.0.1:7777", Token: "t"}, false, false},
		{"名字为空", proto.AddMachineReq{Name: "", Addr: "10.0.0.1:7777", Token: "t"}, true, false},
		{"名字含空格", proto.AddMachineReq{Name: "my box", Addr: "10.0.0.1:7777", Token: "t"}, true, false},
		{"重名", proto.AddMachineReq{Name: "dup", Addr: "10.0.0.1:7777", Token: "t"}, true, true},
		{"地址缺端口", proto.AddMachineReq{Name: "box", Addr: "10.0.0.1", Token: "t"}, true, false},
		{"令牌为空", proto.AddMachineReq{Name: "box", Addr: "10.0.0.1:7777", Token: ""}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateAddMachine(c.req, existing)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v，实际 %v", c.wantErr, err)
			}
			if c.isDup && !errors.Is(err, ErrMachineExists) {
				t.Fatalf("重名应可被 errors.Is(ErrMachineExists) 识别，实际 %v", err)
			}
		})
	}
}

func TestAddAndRemoveMachine(t *testing.T) {
	s := newAdminServer(t)
	req := proto.AddMachineReq{Name: "box", Addr: "10.0.0.1:7777", Token: "secret", User: "me"}
	if err := s.addMachine(req); err != nil {
		t.Fatalf("新增失败: %v", err)
	}
	got, ok := s.conf().Targets["box"]
	if !ok || got.Addr != "10.0.0.1:7777" || got.Token != "secret" || got.User != "me" {
		t.Fatalf("落库内容不对: %+v ok=%v", got, ok)
	}
	// 落盘后重新读文件，必须还在（否则重启即丢）
	reloaded, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatalf("重读配置失败: %v", err)
	}
	if _, ok := reloaded.Targets["box"]; !ok {
		t.Fatal("配置文件里没有新增的机器")
	}
	if err := s.removeMachine("box"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, ok := s.conf().Targets["box"]; ok {
		t.Fatal("删除后仍在内存里")
	}
	if err := s.removeMachine("box"); !errors.Is(err, ErrMachineNotFound) {
		t.Fatalf("删除不存在的机器应返回 ErrMachineNotFound，实际 %v", err)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestValidateAddMachine|TestAddAndRemoveMachine' -count=1`
Expected: 编译失败，`validateAddMachine undefined` 等

- [ ] **Step 4: 实现**

新建 `internal/agentd/machineadmin.go`：

```go
// 本文件实现开发机的增删：校验、写时复制落盘。
//
// 职责：
//   - 校验新增请求（名字、地址、令牌）并判重名
//   - 把改动落进配置文件（经 Server.swapConf）
//
// 边界：
//   - **不做 HTTP 编解码**：状态码与响应体由 machines.go 的 handler 决定
//   - **不做网络探测**：可达性探测是 I/O，属 handler 层，本文件保持纯逻辑
//   - **不建表**：机器的真相仍是 config.yaml 的 targets 段（同 machines.go）
package agentd

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// ErrMachineExists 表示同名开发机已存在，调用方应答 409。
var ErrMachineExists = errors.New("同名开发机已存在")

// ErrMachineNotFound 表示要删的开发机不存在，调用方应答 404。
var ErrMachineNotFound = errors.New("开发机不存在")

// validateAddMachine 校验新增开发机的请求。
//
// 参数：
//   - req: 请求体
//   - existing: 现有 targets，用于判重名
//
// 返回：
//   - 包装了 ErrMachineExists 的错误表示重名（调用方应答 409）
//   - 其余非 nil 错误都是请求体本身的问题（调用方应答 400）
func validateAddMachine(req proto.AddMachineReq, existing map[string]config.Target) error {
	// 空名字就是本机的保留名（proto.Machine 里 Name=="" 即本机），必须挡住
	if req.Name == "" {
		return errors.New("name 不能为空")
	}
	if strings.ContainsAny(req.Name, " \t\r\n") {
		return errors.New("name 不能含空白字符")
	}
	if _, ok := existing[req.Name]; ok {
		return fmt.Errorf("%w: %s", ErrMachineExists, req.Name)
	}
	if _, _, err := net.SplitHostPort(req.Addr); err != nil {
		return fmt.Errorf("addr 需形如 host:port: %w", err)
	}
	if req.Token == "" {
		return errors.New("token 不能为空")
	}
	return nil
}

// addMachine 校验并把一台开发机写入配置、落盘。
//
// 参数：
//   - req: 已由 handler 反序列化的请求体（Force 在本层无意义，探测在 handler）
//
// 返回：
//   - 校验错误（可能包装 ErrMachineExists）或落盘错误；成功时 nil
//
// 注意：重名判定在 swapConf 的临界区内再做一次。校验时的那次是为了尽早
// 返回清晰的错误，但两次请求并发到达时只有临界区内的判定作数。
func (s *Server) addMachine(req proto.AddMachineReq) error {
	if err := validateAddMachine(req, s.conf().Targets); err != nil {
		return err
	}
	// 只打名字与地址，绝不打 token
	s.log.Info("新增开发机", "name", req.Name, "addr", req.Addr, "user", req.User)
	return s.swapConf(func(c *config.Config) error {
		if _, ok := c.Targets[req.Name]; ok {
			return fmt.Errorf("%w: %s", ErrMachineExists, req.Name)
		}
		c.Targets[req.Name] = config.Target{Addr: req.Addr, Token: req.Token, User: req.User}
		return nil
	})
}

// removeMachine 从配置里删除一台开发机并落盘。
//
// 参数：
//   - name: 机器名（不能为空；空串是本机的保留名）
//
// 返回：
//   - 包装了 ErrMachineNotFound 的错误表示不存在（调用方应答 404）
func (s *Server) removeMachine(name string) error {
	if name == "" {
		return fmt.Errorf("%w: 名字为空", ErrMachineNotFound)
	}
	s.log.Info("删除开发机", "name", name)
	return s.swapConf(func(c *config.Config) error {
		if _, ok := c.Targets[name]; !ok {
			return fmt.Errorf("%w: %s", ErrMachineNotFound, name)
		}
		delete(c.Targets, name)
		return nil
	})
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestValidateAddMachine|TestAddAndRemoveMachine' -race -count=1`
Expected: PASS

- [ ] **Step 6: 关键节点日志自检**

确认以下都已具备（本 task 的日志已写在 Step 4 的实现里，这一步是核对）：
- 进入 `addMachine` / `removeMachine` 时各一条 Info，带机器名与地址
- `swapConf` 落盘成功一条 Info、失败一条 Error 带 `cause`（Task 1 已写）
- **grep 自检**：`grep -n "token" internal/agentd/machineadmin.go` 的结果里不得有任何一处出现在日志参数中

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/machineadmin.go internal/agentd/machineadmin_test.go internal/proto/projects.go
git commit -m "feat(agentd): 开发机增删的领域逻辑

校验（名字非空/无空白/不重名、地址 host:port、令牌非空）+ 写时复制落盘。
重名在 swapConf 临界区内二次判定，避免并发新增互相覆盖。单开
machineadmin.go 而不并进 machines.go——后者的文件头写明「只读、不建表」，
写操作混进去会让那句边界声明失效。token 只进不出：日志只打名字与地址。"
```

---

## Task 3: `POST /api/machines`（含可达性探测）

**Files:**
- Modify: `internal/agentd/machines.go`（追加 handler）
- Modify: `internal/agentd/server.go`（注册路由）
- Test: `internal/agentd/machines_test.go`（追加）

**Interfaces:**
- Consumes: `s.addMachine(proto.AddMachineReq) error`、`ErrMachineExists`（Task 2）；`client.New(addr, token).Status(ctx)`（`internal/client/client.go:186/334`）；`s.probeMachines(ctx) proto.MachinesResp`（`machines.go`）
- Produces: `func (s *Server) handleAddMachine(w http.ResponseWriter, r *http.Request)`

- [ ] **Step 1: 写失败的测试**

`internal/agentd/machines_test.go` 追加（沿用该文件已有的 `testAgentdEnv` 与 `testToken`）：

```go
// postMachine 带 Bearer 发一次新增请求，返回状态码与响应体原文。
func postMachine(t *testing.T, e *testAgentdEnv, req proto.AddMachineReq) (int, string) {
	t.Helper()
	b, _ := json.Marshal(req)
	hr, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/api/machines", bytes.NewReader(b))
	hr.Header.Set("Authorization", "Bearer "+testToken)
	hr.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// 地址不可达时必须 400 并带上探测失败原文，且不得落库。
func TestAddMachineUnreachableRejected(t *testing.T) {
	e := newTestAgentdEnv(t)
	// 127.0.0.1:1 上不会有服务
	code, body := postMachine(t, e, proto.AddMachineReq{
		Name: "box", Addr: "127.0.0.1:1", Token: "t",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d，体=%s", code, body)
	}
	if !strings.Contains(body, "error") || len(body) < 20 {
		t.Fatalf("响应应带探测失败原文，实际 %s", body)
	}
	if got := getMachines(t, e); len(got.Machines) != 1 {
		t.Fatalf("探测失败不该落库，机器数应仍为 1（本机），实际 %d", len(got.Machines))
	}
}

// force=true 跳过探测直接落库。
func TestAddMachineForceSkipsProbe(t *testing.T) {
	e := newTestAgentdEnv(t)
	code, body := postMachine(t, e, proto.AddMachineReq{
		Name: "box", Addr: "127.0.0.1:1", Token: "t", Force: true,
	})
	if code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d，体=%s", code, body)
	}
	if strings.Contains(body, "\"t\"") || strings.Contains(body, "token") {
		t.Fatalf("响应体不得包含令牌，实际 %s", body)
	}
	names := map[string]bool{}
	for _, m := range getMachines(t, e).Machines {
		names[m.Name] = true
	}
	if !names["box"] {
		t.Fatal("force 之后机器列表里应有 box")
	}
}

// 重名返回 409。
func TestAddMachineDuplicateConflict(t *testing.T) {
	e := newTestAgentdEnv(t)
	req := proto.AddMachineReq{Name: "box", Addr: "127.0.0.1:1", Token: "t", Force: true}
	if code, body := postMachine(t, e, req); code != http.StatusOK {
		t.Fatalf("首次新增应成功，实际 %d %s", code, body)
	}
	if code, _ := postMachine(t, e, req); code != http.StatusConflict {
		t.Fatalf("重名应返回 409，实际 %d", code)
	}
}

// 地址不合法返回 400，且不做探测（快速失败）。
func TestAddMachineBadAddr(t *testing.T) {
	e := newTestAgentdEnv(t)
	if code, _ := postMachine(t, e, proto.AddMachineReq{Name: "box", Addr: "nope", Token: "t"}); code != http.StatusBadRequest {
		t.Fatalf("非法地址应返回 400，实际 %d", code)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestAddMachine -count=1`
Expected: FAIL —— 路由未注册，返回 404

- [ ] **Step 3: 实现 handler**

`internal/agentd/machines.go` 追加：

```go
// addMachineProbeBudget 是新增开发机时那一次可达性探测的时限。
//
// 比整轮扇出的 machineProbeBudget 宽松：这里是用户点了「添加」在等结果，
// 一次往返慢一点可以接受，误判成不可达才是真的坏体验。
const addMachineProbeBudget = 5 * time.Second

// handleAddMachine 处理 POST /api/machines。
//
// 流程：反序列化 → 校验 → （非 force 时）可达性探测 → 落库 → 返回新列表。
//
// 状态码：
//   - 400 请求体不合法，或探测不通（体内带探测失败原文，供前端原样展示）
//   - 409 同名开发机已存在
//   - 500 落盘失败
//
// 注意：响应体是 proto.MachinesResp，其中的 proto.Machine 没有 Token 字段
// ——令牌只进不出，这条由类型本身保证。
func (s *Server) handleAddMachine(w http.ResponseWriter, r *http.Request) {
	var req proto.AddMachineReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("新增开发机：请求体无法解析", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	// 先做纯校验：地址粘错时不必浪费一次 5 秒探测
	if err := validateAddMachine(req, s.conf().Targets); err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, ErrMachineExists) {
			code = http.StatusConflict
		}
		s.log.Warn("新增开发机：校验未通过", "name", req.Name, "addr", req.Addr, "cause", err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	if !req.Force {
		ctx, cancel := context.WithTimeout(r.Context(), addMachineProbeBudget)
		defer cancel()
		s.log.Info("新增开发机：开始可达性探测", "name", req.Name, "addr", req.Addr)
		if _, err := client.New(req.Addr, req.Token).Status(ctx); err != nil {
			// 原文回给前端：绝大多数失败是地址或令牌粘错，原文是唯一能让人
			// 一眼看出「是连不上还是没授权」的东西
			s.log.Warn("新增开发机：探测不通", "name", req.Name, "addr", req.Addr, "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("探测 %s 失败：%v", req.Addr, err),
			})
			return
		}
		s.log.Info("新增开发机：探测通过", "name", req.Name, "addr", req.Addr)
	}
	if err := s.addMachine(req); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrMachineExists) {
			code = http.StatusConflict
		}
		s.log.Error("新增开发机失败", "name", req.Name, "cause", err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("新增开发机成功", "name", req.Name, "addr", req.Addr, "force", req.Force)
	writeJSON(w, http.StatusOK, s.probeMachines(r.Context()))
}
```

- [ ] **Step 4: 注册路由**

`internal/agentd/server.go:281` 附近，紧跟现有那行：

```go
api.HandleFunc("GET /api/machines", s.handleMachines)
api.HandleFunc("POST /api/machines", s.handleAddMachine)
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestAddMachine -race -count=1`
Expected: 四个用例全部 PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/machines.go internal/agentd/server.go internal/agentd/machines_test.go
git commit -m "feat(agentd): POST /api/machines 新增开发机

先纯校验（地址粘错不浪费探测），再可达性探测（复用 client.Status，5s），
探不通返回 400 并带原文——绝大多数失败是地址或令牌粘错，原文是唯一能让
人一眼分清「连不上」与「没授权」的东西。force=true 跳过探测，覆盖对端
临时离线的合理场景。响应体是 proto.MachinesResp，其 Machine 类型没有
Token 字段，令牌只进不出由类型保证。"
```

---

## Task 4: `DELETE /api/machines/{name}`

**Files:**
- Modify: `internal/agentd/machines.go`（追加 handler）
- Modify: `internal/agentd/server.go`（注册路由）
- Test: `internal/agentd/machines_test.go`（追加）

**Interfaces:**
- Consumes: `s.removeMachine(name string) error`、`ErrMachineNotFound`（Task 2）
- Produces: `func (s *Server) handleDeleteMachine(w http.ResponseWriter, r *http.Request)`

- [ ] **Step 1: 写失败的测试**

```go
// deleteMachine 带 Bearer 发一次删除请求。
func deleteMachine(t *testing.T, e *testAgentdEnv, name string) (int, string) {
	t.Helper()
	hr, _ := http.NewRequest(http.MethodDelete, e.ts.URL+"/api/machines/"+name, nil)
	hr.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestDeleteMachine(t *testing.T) {
	e := newTestAgentdEnv(t)
	if code, body := postMachine(t, e, proto.AddMachineReq{
		Name: "box", Addr: "127.0.0.1:1", Token: "t", Force: true,
	}); code != http.StatusOK {
		t.Fatalf("准备数据失败: %d %s", code, body)
	}
	if code, body := deleteMachine(t, e, "box"); code != http.StatusOK {
		t.Fatalf("删除应成功，实际 %d %s", code, body)
	}
	for _, m := range getMachines(t, e).Machines {
		if m.Name == "box" {
			t.Fatal("删除后列表里仍有 box")
		}
	}
	if code, _ := deleteMachine(t, e, "box"); code != http.StatusNotFound {
		t.Fatalf("删除不存在的机器应返回 404，实际 %d", code)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestDeleteMachine -count=1`
Expected: FAIL —— 405 或 404（路由未注册）

- [ ] **Step 3: 实现 handler**

`internal/agentd/machines.go` 追加：

```go
// handleDeleteMachine 处理 DELETE /api/machines/{name}。
//
// 状态码：
//   - 404 该名字不存在
//   - 500 落盘失败
//
// 注意：删除只改本机配置里的 targets，**不去动对端**——对端 agentd 与其
// 上正在跑的任务与本操作无关，删的只是「本机记得这台机器」这件事。
func (s *Server) handleDeleteMachine(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.removeMachine(name); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrMachineNotFound) {
			code = http.StatusNotFound
		}
		s.log.Warn("删除开发机失败", "name", name, "cause", err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("删除开发机成功", "name", name)
	writeJSON(w, http.StatusOK, s.probeMachines(r.Context()))
}
```

- [ ] **Step 4: 注册路由**

```go
api.HandleFunc("DELETE /api/machines/{name}", s.handleDeleteMachine)
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestDeleteMachine|TestAddMachine' -race -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agentd/machines.go internal/agentd/server.go internal/agentd/machines_test.go
git commit -m "feat(agentd): DELETE /api/machines/{name} 删除开发机

只能加不能删的话，地址粘错又选了「仍然保存」的用户就只能去改配置文件
——那正是本轮要消灭的场景。删除只改本机 targets，不触碰对端。"
```

---

## Task 5: 前端 API 层

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Test: `web/src/api/client.test.ts`（追加）

**Interfaces:**
- Consumes: `POST /api/machines`、`DELETE /api/machines/{name}`（Task 3/4）；`postJSON<T>`、`request<T>`（`client.ts:105/116`）
- Produces:
  - `export interface AddMachineReq { name: string; addr: string; token: string; user: string; force?: boolean }`
  - `export function addMachine(req: AddMachineReq): Promise<MachinesResp>`
  - `export function deleteMachine(name: string): Promise<MachinesResp>`

- [ ] **Step 1: 写失败的测试**

`web/src/api/client.test.ts` 追加：

```ts
import { addMachine, deleteMachine } from './client'

it('addMachine 以 JSON 体 POST 到 /api/machines', async () => {
  const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ machines: [] }), { status: 200 }),
  )
  await addMachine({ name: 'box', addr: '10.0.0.1:7777', token: 't', user: 'me' })
  const [path, init] = spy.mock.calls[0]
  expect(path).toBe('/api/machines')
  expect(init?.method).toBe('POST')
  expect(JSON.parse(String(init?.body))).toMatchObject({ name: 'box', addr: '10.0.0.1:7777' })
  spy.mockRestore()
})

it('deleteMachine 对机器名做 URL 编码', async () => {
  const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify({ machines: [] }), { status: 200 }),
  )
  await deleteMachine('my box')
  expect(spy.mock.calls[0][0]).toBe('/api/machines/my%20box')
  spy.mockRestore()
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/api/client.test.ts`
Expected: FAIL —— `addMachine is not a function`

- [ ] **Step 3: 实现**

`web/src/api/types.ts` 追加：

```ts
// AddMachineReq 是 POST /api/machines 的请求体。
//
// token 只进不出：后端接受它，但 Machine 类型没有对应字段，任何响应里
// 都不会回显。force=true 跳过后端的可达性探测（对端临时离线时用）。
export interface AddMachineReq {
  name: string
  addr: string
  token: string
  user: string
  force?: boolean
}
```

`web/src/api/client.ts` 追加：

```ts
// addMachine 新增一台远程开发机（POST /api/machines）。
//
// 参数：
//   - req: 机器名、地址、令牌、ssh 用户；force=true 跳过可达性探测
//
// 返回：新的机器列表（与 fetchMachines 同结构）
//
// 注意：后端在 force 未置时会做一次可达性探测，探不通抛 ApiError(400)，
// message 即探测失败原文——调用方应原样展示给用户，那是判断「连不上」
// 还是「没授权」的唯一线索。
export function addMachine(req: AddMachineReq): Promise<MachinesResp> {
  return postJSON<MachinesResp>('/api/machines', req)
}

// deleteMachine 删除一台远程开发机（DELETE /api/machines/{name}）。
//
// 注意：机器名可能含需要转义的字符，必须 encodeURIComponent。
export function deleteMachine(name: string): Promise<MachinesResp> {
  return request<MachinesResp>(`/api/machines/${encodeURIComponent(name)}`, { method: 'DELETE' })
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/api/client.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/api/types.ts web/src/api/client.ts web/src/api/client.test.ts
git commit -m "feat(web): 新增/删除开发机的 API 封装"
```

---

## Task 6: 机器页的新增与删除

**Files:**
- Modify: `web/src/app/machines/MachinesPage.tsx`
- Test: `web/src/app/machines/MachinesPage.test.tsx`（追加）

**Interfaces:**
- Consumes: `addMachine(req: AddMachineReq): Promise<MachinesResp>`、`deleteMachine(name: string): Promise<MachinesResp>`（Task 5）；`ConfirmDialog`（`web/src/app/lib/ConfirmDialog.tsx`）

- [ ] **Step 1: 写失败的测试**

```tsx
it('提交新增表单后调用 addMachine 并刷新列表', async () => {
  const spy = vi.spyOn(api, 'addMachine').mockResolvedValue({ machines: [] })
  render(<MachinesPage />)
  await userEvent.click(screen.getByRole('button', { name: '新增开发机' }))
  await userEvent.type(screen.getByLabelText('名字'), 'box')
  await userEvent.type(screen.getByLabelText('地址'), '10.0.0.1:7777')
  await userEvent.type(screen.getByLabelText('令牌'), 'secret')
  await userEvent.click(screen.getByRole('button', { name: '添加' }))
  expect(spy).toHaveBeenCalledWith(
    expect.objectContaining({ name: 'box', addr: '10.0.0.1:7777', token: 'secret' }),
  )
})

it('探测失败时展示后端原文并提供「仍然保存」', async () => {
  vi.spyOn(api, 'addMachine').mockRejectedValueOnce(new ApiError(400, '探测 10.0.0.1:7777 失败：连接被拒绝'))
  render(<MachinesPage />)
  await userEvent.click(screen.getByRole('button', { name: '新增开发机' }))
  await userEvent.type(screen.getByLabelText('名字'), 'box')
  await userEvent.type(screen.getByLabelText('地址'), '10.0.0.1:7777')
  await userEvent.type(screen.getByLabelText('令牌'), 'secret')
  await userEvent.click(screen.getByRole('button', { name: '添加' }))
  expect(await screen.findByText(/连接被拒绝/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '仍然保存' })).toBeInTheDocument()
})

it('「仍然保存」以 force 重发', async () => {
  const spy = vi.spyOn(api, 'addMachine')
    .mockRejectedValueOnce(new ApiError(400, '探测失败：连接被拒绝'))
    .mockResolvedValueOnce({ machines: [] })
  render(<MachinesPage />)
  await userEvent.click(screen.getByRole('button', { name: '新增开发机' }))
  await userEvent.type(screen.getByLabelText('名字'), 'box')
  await userEvent.type(screen.getByLabelText('地址'), '10.0.0.1:7777')
  await userEvent.type(screen.getByLabelText('令牌'), 'secret')
  await userEvent.click(screen.getByRole('button', { name: '添加' }))
  await userEvent.click(await screen.findByRole('button', { name: '仍然保存' }))
  expect(spy.mock.calls[1][0]).toMatchObject({ force: true })
})

it('令牌输入框是密码型', async () => {
  render(<MachinesPage />)
  await userEvent.click(screen.getByRole('button', { name: '新增开发机' }))
  expect(screen.getByLabelText('令牌')).toHaveAttribute('type', 'password')
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/machines/MachinesPage.test.tsx`
Expected: FAIL —— 找不到「新增开发机」按钮

- [ ] **Step 3: 实现 UI**

在 `MachinesPage.tsx` 里加入：

- 页面标题行右侧一个「新增开发机」按钮，点击展开表单（同页面内联，不用弹窗——机器列表本身要保持可见，方便对照已有的名字）
- 表单四个字段，`<label>` 与 `<input>` 用 `htmlFor`/`id` 关联（测试按 `getByLabelText` 取）：
  - 名字（text，必填）
  - 地址（text，必填，placeholder `100.73.238.21:7777`）
  - 令牌（**password**，必填，提示文案「对方机器 `handoff init` 末尾会打出来」）
  - ssh 用户（text，可选，提示文案「attach / pull 要用」）
- 「添加」按钮：调 `addMachine`，成功则收起表单并用返回的列表刷新
- 失败：把 `ApiError.message` 原样渲染在表单下方的错误区；**若状态码是 400**，同时渲染「仍然保存」按钮，点击后以 `{...req, force: true}` 重发
- 机器卡片上（`name !== ''` 的才有）加删除入口，点击后经 `ConfirmDialog` 二次确认，确认则调 `deleteMachine`

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/machines/MachinesPage.test.tsx`
Expected: 四个用例全部 PASS

- [ ] **Step 5: 跑前端全量 + 类型检查**

Run: `cd web && npx vitest run && npx tsc --noEmit`
Expected: 全绿，无类型错误

- [ ] **Step 6: 加注释**

- 「仍然保存」那段分支写「为什么」的注释：机器临时离线是合理场景，不该因此完全加不进来；但默认仍探测，因为绝大多数失败是粘错。
- 令牌输入框旁注明：后端不回显令牌，编辑已有机器时该字段为空即表示不改。

- [ ] **Step 7: Commit**

```bash
git add web/src/app/machines
git commit -m "feat(web): 机器页支持新增与删除开发机

内联表单而非弹窗——机器列表要保持可见，方便对照已有名字。探测失败时
原样展示后端原文并提供「仍然保存」（以 force 重发）。令牌用密码型输入框，
后端本就不回显。"
```

---

## Task 7: 端到端验收

**Files:** 无（纯验证）

- [ ] **Step 1: 全量测试**

Run: `go test ./... -count=1 && go test ./internal/agentd/ -race -count=1 && cd web && npx vitest run && npx tsc --noEmit`
Expected: 全绿

- [ ] **Step 2: token 泄漏自检**

Run:
```bash
grep -rn "Token" internal/agentd/machineadmin.go internal/agentd/machines.go | grep -iE "log\.|Info\(|Warn\(|Error\("
```
Expected: **无输出**。任何一行输出都是令牌进日志的证据，必须修掉。

- [ ] **Step 3: 真机走查**

在本机 agentd 上：打开控制台机器页 → 新增一台真实可达的开发机（用另一台机器的 addr + token）→ 确认列表出现且 `reachable=true` → 重启 agentd → 确认它仍在 → 删除它 → 确认列表与 `~/.handoff/config.yaml` 都不再有它。

再试一次错误路径：地址填 `127.0.0.1:1` → 确认页面上出现探测失败原文 → 点「仍然保存」→ 确认能加进去且标为不可达。

把每一步的实际观察记进 ledger。**「代码看起来对」不能替代观察。**

- [ ] **Step 4: Commit ledger**

```bash
git add docs/ledger-console-machine-add.md
git commit -m "docs(ledger): 控制台新增开发机真机走查记录"
```
