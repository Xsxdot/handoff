# B38 断连窗口的会话对账 —— 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** agentd 与 executor 断连恢复后，把窗口内错过的回合终态补回事件流，让「回合其实已完成」的任务不再冻死在 `running`；并给审核者一条不撒谎的手动出口。

**Architecture:** 各 adapter 在自己的凭据文件里持久化一个「已消费到哪条 assistant 消息」的水位（借用 claudecode 已验证的 `proc.json.Offset` 机制）。对账 = 查会话尾部 → 与水位比对 → 若有未消费的已完结回合，取回文本交给**既有**的回合分类逻辑 → 补发同形事件 → 前进水位。补发的事件走既有 `evCh`，manager / 工单 / 状态机 / `wait` 零改动。

**Tech Stack:** Go 1.x（无新依赖）、`net/http`、`net/http/httptest`、SQLite（既有）、opencode HTTP API

## Global Constraints

- **不改 claudecode**：它靠 `out.jsonl` + `proc.json.Offset` 结构上免疫，改它是净风险（spec §1.6）
- **不新增事件语义**：补发的事件与实时事件同形，不加标记；补发的痕迹只留在日志与 `resume` 报告里（spec §9）
- **对账失败不阻断恢复**：`Resume` 照常返回 `Alive=true`，`onReconnect` 照常继续消费（spec §6.3，硬要求）
- **adapter 不写 store**：沿用 `internal/executor` 包级边界；水位是 adapter 自己的持久化，不碰任务库
- **接口归属跟随既有约定**：数据类型放 `internal/executor`，**能力接口由消费方 manager 定义并做类型断言**——这是 `internal/executor/resume.go:6-9` 明写的既有约定，`restorer`/`volatilePermitter` 都这么做
- **本期只实现 opencode 的对账**：grok/codex 的协议能力未实证，本计划最后一个 task 产出探针文档，它们的实现另出一份计划（理由见下方「范围」）
- **opencode 的 `Pending` 恒为 0**：悬而未决的权限请求捧不回来已由更早的 spike 实证（spec §7.1）——消息流的 tool part 只有 `callID` 无权限 id，应答端点要求真实 id、伪造即 404。**不建假工单**，退为检测 + 播报
- 日志一律用各文件已有的 `a.log` / `s.log` / `m.log`（`log/slog`），**禁止 `fmt.Printf`**
- 每个新建文件写文件头注释（职责 + 边界），每个导出方法写 doc 注释（参数/返回/注意）

## 范围：为什么 grok/codex 不在本计划

spec §7 把 grok/codex 的协议能力列为实现前置探针，而**探针结论未知时无法写出无占位符的实现 task**——写「实现 grok 对账（细节待探针）」就是 plan failure。

本计划因此交付一个**自身完整可用**的增量：契约 + opencode 对账 + 两个自动触发点 + CLI 出口 + 真机验收。opencode 是三个受影响 adapter 里唯一已实证可行的。最后一个 task 产出探针文档，其结论直接套用本计划确立的接口，grok/codex 的实现另出计划。

**顺序说明**：探针排在最后而不是最前，因为它不阻塞 opencode 那条路，而且有了 opencode 的实现做参照，探针要问的问题会更准。

---

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/executor/reconcile.go` | 新建 | 只放 `ReconcileOutcome` 数据类型（对齐 `resume.go` 的「只有数据没有接口」约定） |
| `internal/executor/opencode/proc.go` | 修改 | `procInfo` 增加水位字段；水位读写与清零 |
| `internal/executor/opencode/api.go` | 修改 | 新增 `LastAssistantMessage`：查会话尾部 |
| `internal/executor/opencode/reconcile.go` | 新建 | opencode 的 `Reconcile` 实现（本期唯一的对账实现） |
| `internal/executor/opencode/adapter.go` | 修改 | 记录当前回合的 assistant 消息 id；正常路径分类后前进水位；`onReconnect` 触发对账 |
| `internal/executor/opencode/resume.go` | 修改 | `Resume` 末尾触发对账（`fresh` 不触发且清零水位） |
| `internal/agentd/manager.go` | 修改 | 私有 `reconciler` 接口；`RecoverStuck` 接入对账与 `--force` 收口；`RecoverReport` 加字段 |
| `internal/agentd/session_reconcile_test.go` | 新建 | 上一行的测试。**不要**叫 `reconcile_test.go` —— 见下方「两种对账」 |
| `internal/agentd/server.go` | 修改 | `handleResume` 接受 `force` 查询参数 |
| `internal/client/client.go` | 修改 | `Resume` 传递 `force` |
| `cmd/resume.go` | 修改 | `--force` flag 与帮助文案 |
| `docs/superpowers/plans/2026-08-10-b38-grok-codex-probe.md` | 新建 | 探针文档（Task 8 产出） |

### agentd 里有两种「对账」，别搞混

`internal/agentd/reconcile.go` **已经存在**，它是**另一件事**：「任务运行态与 executor **实际存活性**的对账」（`reconcileExecutorGone`——已经知道 executor 没了之后怎么收尾）。

本计划做的是**会话内容对账**：executor **还活着**，丢的是事件。两者互不替代：

| | 既有 `reconcile.go` | 本计划 |
|---|---|---|
| 前提 | executor 已经死了 | executor 还活着 |
| 缺的东西 | 无（任务该收尾了） | 断连窗口里的事件 |
| 出口 | 转 `waiting_review` 交裁决 | 补发终态，任务自然迁移 |

因此：
- **不要**把本计划的代码写进 `internal/agentd/reconcile.go`——它归 `manager.go`
- 测试文件叫 `session_reconcile_test.go`，不叫 `reconcile_test.go`
- `internal/executor/reconcile.go`（Task 3 新建）在**另一个包**，与它不冲突

---

## Task 1: opencode 水位的持久化契约

**Files:**
- Modify: `internal/executor/opencode/proc.go:289-330`（`procInfo` 结构、`writeProcInfo`、`readProcInfo`）
- Test: `internal/executor/opencode/proc_internal_test.go`（新建）

**Interfaces:**
- Consumes: 无（本计划第一个 task）
- Produces: `procInfo.LastTurnMsgID string`（JSON 键 `last_turn_msg_id`）；`writeProcInfo(taskDir string, pi *procInfo) error` 与 `readProcInfo(taskDir string) (*procInfo, error)` 签名不变

**为什么水位是「消息 id」而不是时间戳**：spec §2.2 的不变量保证一个断连窗口内至多跨越**一个**回合边界，因此「最后一条 assistant 消息的 id 与水位不同」就无歧义地等于「有一个新的已完结回合没被消费」——不需要任何时间序假设，也不怕两条消息落在同一毫秒。

- [ ] **Step 1: 写失败的测试**

在 `internal/executor/opencode/proc_internal_test.go` 新建：

```go
package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/prochost"
)

// TestProcInfoWatermarkRoundTrip 验水位字段能写进去也能读回来。
func TestProcInfoWatermarkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &procInfo{
		Handle:         prochost.Handle{PID: 4242, LockPath: filepath.Join(dir, "proc.lock")},
		Port:           7788,
		Password:       "deadbeef",
		LastTurnMsgID:  "msg_abc123",
	}
	if err := writeProcInfo(dir, in); err != nil {
		t.Fatalf("写恢复凭据失败: %v", err)
	}
	out, err := readProcInfo(dir)
	if err != nil {
		t.Fatalf("读恢复凭据失败: %v", err)
	}
	if out.LastTurnMsgID != "msg_abc123" {
		t.Fatalf("水位未往返：want msg_abc123, got %q", out.LastTurnMsgID)
	}
}

// TestProcInfoOldFormatReadsAsEmptyWatermark 验旧格式（无水位字段）的 proc.json
// 仍然可读，水位读出空串而不是报错。
//
// why：本字段是给存量任务加的。若旧文件被判「字段不完整」，agentd 升级后所有
// 在跑的任务会一起变成「恢复凭据缺失」→ 判死 → 转 waiting_review，是升级即事故。
func TestProcInfoOldFormatReadsAsEmptyWatermark(t *testing.T) {
	dir := t.TempDir()
	old := map[string]any{
		"handle":   map[string]any{"pid": 4242, "lock_path": filepath.Join(dir, "proc.lock")},
		"port":     7788,
		"password": "deadbeef",
	}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, procInfoFileName), b, 0o600); err != nil {
		t.Fatalf("写旧格式凭据失败: %v", err)
	}
	out, err := readProcInfo(dir)
	if err != nil {
		t.Fatalf("旧格式凭据应可读，却报错: %v", err)
	}
	if out.LastTurnMsgID != "" {
		t.Fatalf("旧格式的水位应为空串，got %q", out.LastTurnMsgID)
	}
	if out.Port != 7788 {
		t.Fatalf("旧格式其余字段应正常读出，port got %d", out.Port)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestProcInfo -v`
Expected: 编译失败，`in.LastTurnMsgID undefined (type procInfo has no field or method LastTurnMsgID)`

- [ ] **Step 3: 加字段**

`internal/executor/opencode/proc.go`，把 `procInfo` 改为：

```go
// procInfo 是 serve 进程连接凭据的持久化形态，agentd 重启后凭它重建订阅。
//
// LastTurnMsgID 是对账水位（B38）：最后一条**已被翻译成终态事件**的 assistant
// 消息 id。断连恢复后拿它与会话尾部比对——不同即说明有一个已完结的回合没被
// 消费，需要补发。
//
// 为什么用消息 id 而不是时间戳：一个断连窗口内至多跨越一个回合边界（spec §2.2，
// 因为新回合只能由经过 agentd 的 Start/Send 发起），因此「id 不同」就无歧义地
// 等于「有新的已完结回合」，不需要任何时间序假设。
//
// 空串是合法值：老任务的 proc.json 没有这个字段，读出空串即「从未对过账」，
// 首次对账会把当前尾部认作已消费（见 reconcile.go 的首次对账语义）。
type procInfo struct {
	Handle        prochost.Handle `json:"handle"`
	Port          int             `json:"port"`
	Password      string          `json:"password"`
	LastTurnMsgID string          `json:"last_turn_msg_id,omitempty"`
}
```

`readProcInfo` 的完整性校验**不动**——`LastTurnMsgID` 不进校验，空串合法。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run TestProcInfo -v`
Expected: 两条都 PASS

- [ ] **Step 5: 加日志**

`writeProcInfo` 目前无日志（它是纯函数、由调用方记）。本 task 不给它加日志，改为在 Task 3 的水位前进处记录——**水位变化才是要观测的事件，写文件不是**。

本步骤实际动作：确认 `writeProcInfo`/`readProcInfo` 没有引入静默失败路径（两者都已返回带上下文的 error，调用方负责记日志）。执行者只需通读这两个函数确认无新增静默分支，不改代码。

- [ ] **Step 6: 加注释**

Step 3 的结构体注释已覆盖「为什么是消息 id」「空串为什么合法」。补一条 `readProcInfo` 的行内注释，说明为什么水位不进完整性校验：

```go
	// 水位不进完整性校验：它是 B38 新加的字段，存量 proc.json 里没有。
	// 若把它算进「字段不完整」，agentd 升级后所有在跑的任务会一起被判死
	if pi.Handle.LockPath == "" || pi.Port == 0 || pi.Password == "" {
		return nil, fmt.Errorf("恢复凭据 %s 字段不完整", procInfoFileName)
	}
```

- [ ] **Step 7: 提交**

```bash
git add internal/executor/opencode/proc.go internal/executor/opencode/proc_internal_test.go
git commit -m "feat(opencode): proc.json 增加对账水位字段，旧格式向后兼容"
```

---

## Task 2: 查会话尾部的 API（含真实报文夹具）

**Files:**
- Modify: `internal/executor/opencode/api.go`（在 `HasSession` 之后新增方法）
- Create: `internal/executor/opencode/testdata/session_messages.json`（真实报文夹具）
- Test: `internal/executor/opencode/api_messages_test.go`（新建）

**Interfaces:**
- Consumes: `(*API).do(ctx, method, path, body)`、`(*API).httpError(op, resp)`（`api.go:159`、`api.go:186`）
- Produces:
  ```go
  type SessionMessage struct {
      ID          string // 消息 id
      Role        string // "assistant" | "user"
      CompletedMS int64  // 完结时刻（毫秒 epoch）；0 表示尚未完结
      ErrorText   string // 非空表示该回合以错误告终
      Text        string // 该消息的文本部分拼接结果
  }
  func (a *API) LastAssistantMessage(ctx context.Context, sessionID string) (*SessionMessage, error)
  ```
  返回 `(nil, nil)` 表示会话里还没有任何 assistant 消息（合法状态，不是错误）。

- [ ] **Step 1: 抓一份真实报文当夹具**

**不要按想象写 JSON 结构体。** 本仓库对「按 schema 名字推断」有过教训——B28 的 spike 一次推翻四处。

在 devbox 上取一个仍有 serve 存活的 opencode 任务，读它的凭据并抓真实响应：

```bash
ssh sycm@100.73.238.21 'TD=$(ls -td ~/.handoff/tasks/*/ | head -1); PORT=$(python3 -c "import json,sys;print(json.load(open(\"$TD/proc.json\"))[\"port\"])"); PASS=$(python3 -c "import json,sys;print(json.load(open(\"$TD/proc.json\"))[\"password\"])"); SID=$(python3 -c "import json,sys;print(json.load(open(\"$TD/serve.json\"))[\"session_id\"])" 2>/dev/null || echo ""); echo "task=$TD port=$PORT session=$SID"; curl -s -u "opencode:$PASS" "http://127.0.0.1:$PORT/session/$SID/message" | head -c 4000'
```

若 `serve.json` 里没有 session id，改从 handoff 取：`handoff --target devbox show <task> --json` 里的 `executor_session`。

把响应存成 `internal/executor/opencode/testdata/session_messages.json`，**脱敏**（把仓库路径、分支名、模型输出里的业务内容替换成占位文本；保留结构、字段名、`completed`/`error` 的真实形态）。

夹具必须包含至少：一条 `user` 消息、一条**已完结**的 `assistant` 消息（`completed` 非零）。

- [ ] **Step 2: 按夹具的真实形态写失败的测试**

在 `internal/executor/opencode/api_messages_test.go` 新建。**先读 Step 1 抓到的夹具，按它的真实字段路径填下面的断言值**（`wantID`/`wantCompleted` 取夹具里的真实值）：

```go
package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestLastAssistantMessageParsesRealPayload 用真实抓包的报文验解析。
//
// why 用真实夹具而不是手写 JSON：本仓库对「按 schema 名字推断」有过教训
// （B28 的 spike 一次推翻四处推断）。手写的夹具只能证明代码与我的想象一致。
func TestLastAssistantMessageParsesRealPayload(t *testing.T) {
	raw, err := os.ReadFile("testdata/session_messages.json")
	if err != nil {
		t.Fatalf("读夹具失败（先按 Step 1 抓真实报文）: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/ses_test/message" {
			t.Errorf("请求路径不对: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	defer srv.Close()

	api := NewAPI(srv.URL, "pw")
	msg, err := api.LastAssistantMessage(context.Background(), "ses_test")
	if err != nil {
		t.Fatalf("查会话尾部失败: %v", err)
	}
	if msg == nil {
		t.Fatal("夹具里有已完结的 assistant 消息，却返回 nil")
	}
	if msg.Role != "assistant" {
		t.Fatalf("role want assistant, got %q", msg.Role)
	}
	if msg.CompletedMS == 0 {
		t.Fatal("夹具里的消息已完结，CompletedMS 不应为 0")
	}
	if msg.ID == "" {
		t.Fatal("消息 id 不应为空——它是对账水位的载体")
	}
}

// TestLastAssistantMessageEmptySession 验空会话返回 (nil, nil) 而不是错误。
func TestLastAssistantMessageEmptySession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	msg, err := NewAPI(srv.URL, "pw").LastAssistantMessage(context.Background(), "ses_test")
	if err != nil {
		t.Fatalf("空会话不应报错: %v", err)
	}
	if msg != nil {
		t.Fatalf("空会话应返回 nil，got %+v", msg)
	}
}

// TestLastAssistantMessageHTTPError 验非 2xx 转成带状态码的错误。
func TestLastAssistantMessageHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := NewAPI(srv.URL, "pw").LastAssistantMessage(context.Background(), "ses_test"); err == nil {
		t.Fatal("500 应转成错误")
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestLastAssistantMessage -v`
Expected: 编译失败，`api.LastAssistantMessage undefined`

- [ ] **Step 4: 实现**

在 `internal/executor/opencode/api.go` 的 `HasSession` 之后加。**下面的 `sessionMessageEnvelope` 字段路径必须按 Step 1 抓到的真实报文调整**——若真实形态与此不同，以真实报文为准并在结构体注释里记下真实形态：

```go
// SessionMessage 是会话里一条消息的最小形状（对账只需要这几个字段）。
//
// 字段说明：
//   - ID: 消息 id，同时是对账水位的载体
//   - Role: "assistant" | "user"
//   - CompletedMS: 完结时刻（毫秒 epoch）；**0 表示尚未完结**，对账据此判「回合还在跑」
//   - ErrorText: 非空表示该回合以错误告终
//   - Text: 该消息全部文本 part 的拼接结果，交给 turn.ParseTrailer 分类
type SessionMessage struct {
	ID          string
	Role        string
	CompletedMS int64
	ErrorText   string
	Text        string
}

// sessionMessageEnvelope 是 GET /session/{id}/message 列表里每一项的形状。
//
// 注意：字段路径按真实抓包确定（testdata/session_messages.json），不是按
// schema 名字推断的。改动前请先重新抓包核对。
type sessionMessageEnvelope struct {
	Info struct {
		ID   string `json:"id"`
		Role string `json:"role"`
		Time struct {
			Completed int64 `json:"completed"`
		} `json:"time"`
		Error json.RawMessage `json:"error"`
	} `json:"info"`
	Parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"parts"`
}

// LastAssistantMessage 取会话里最后一条 assistant 消息（对账的数据源，B38）。
//
// 参数：
//   - ctx: 控制单次请求超时
//   - sessionID: 目标会话
//
// 返回：
//   - (*SessionMessage, nil): 找到了。CompletedMS==0 表示该回合仍在进行
//   - (nil, nil): 会话里还没有任何 assistant 消息——**合法状态，不是错误**
//   - (nil, err): 请求或解析失败
//
// 注意：
//   - 只看最后一条 assistant 消息就够，依据是「一个断连窗口内至多跨越一个回合
//     边界」（spec §2.2）；不需要全量拉取比对
//   - 权限请求**查不回来**：本端点的 tool part 只有 callID 没有权限 id，而
//     RespondPermission 要求真实 id、伪造即 404（更早的 spike 结论，见
//     adapter.go 的 onReconnect 降级告警）。故本方法不尝试提取权限
func (a *API) LastAssistantMessage(ctx context.Context, sessionID string) (msg *SessionMessage, err error) {
	start := time.Now()
	path := "/session/" + sessionID + "/message"
	a.log().Info("opencode 查会话尾部", "path", path, "session", sessionID)
	defer func() {
		switch {
		case err != nil:
			a.log().Error("opencode 查会话尾部失败", "path", path, "session", sessionID, "cause", err)
		case msg == nil:
			a.log().Info("opencode 会话尾部无 assistant 消息", "path", path,
				"session", sessionID, "elapsed_ms", time.Since(start).Milliseconds())
		default:
			a.log().Info("opencode 会话尾部已取得", "path", path, "session", sessionID,
				"msg", msg.ID, "completed_ms", msg.CompletedMS, "has_error", msg.ErrorText != "",
				"text_runes", len([]rune(msg.Text)), "elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("查会话消息请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, a.httpError("查会话消息", resp)
	}
	var list []sessionMessageEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("解析会话消息: %w", err)
	}
	// 从尾部往前找第一条 assistant：列表按时间正序，最后一条 assistant 才是
	// 「当前回合」的载体；user 消息夹在中间（reply 的应答就是一条 user 消息）
	for i := len(list) - 1; i >= 0; i-- {
		e := list[i]
		if e.Info.Role != "assistant" {
			continue
		}
		out := &SessionMessage{
			ID:          e.Info.ID,
			Role:        e.Info.Role,
			CompletedMS: e.Info.Time.Completed,
		}
		// error 字段的形态在不同版本里可能是 null / 字符串 / 对象，统一按原始
		// JSON 处理：非 null 即视为出错，原文进 ErrorText 供审核者看
		if s := strings.TrimSpace(string(e.Info.Error)); s != "" && s != "null" {
			out.ErrorText = s
		}
		var sb strings.Builder
		for _, p := range e.Parts {
			if p.Type == "text" && p.Text != "" {
				sb.WriteString(p.Text)
			}
		}
		out.Text = sb.String()
		return out, nil
	}
	return nil, nil
}
```

若 `api.go` 尚未 import `strings`，加上。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run TestLastAssistantMessage -v`
Expected: 三条全 PASS

- [ ] **Step 6: 加日志**

Step 4 的实现已含：进入时 Info（带 path/session）、三条出口分支各一条（错误 Error 带 cause、空 Info、成功 Info 带 msg id / completed / has_error / 文本长度 / 耗时）。

执行者核对：**成功路径也有日志**（不是只在失败时才打），且日志里**不含模型输出正文**（只打 `text_runes` 长度）。

- [ ] **Step 7: 加注释**

Step 4 已含 `SessionMessage` 与 `LastAssistantMessage` 的 doc 注释、`sessionMessageEnvelope` 的「按真实抓包确定」警示、倒序查找的 why、error 字段形态兼容的 why。

执行者补一条：若 Step 1 抓到的真实形态与 Step 4 的结构体不同，**必须在 `sessionMessageEnvelope` 注释里记下真实形态与调整原因**。

- [ ] **Step 8: 提交**

```bash
git add internal/executor/opencode/api.go internal/executor/opencode/api_messages_test.go internal/executor/opencode/testdata/session_messages.json
git commit -m "feat(opencode): 新增 LastAssistantMessage，按真实抓包夹具解析会话尾部"
```

---

## Task 3: opencode 的 Reconcile 实现

**Files:**
- Create: `internal/executor/reconcile.go`
- Create: `internal/executor/opencode/reconcile.go`
- Modify: `internal/executor/opencode/adapter.go`（`runState` 加 `lastAssistantMsgID`；`mapPart`/`mapPartDelta` 记录它；`mapIdle` 分类后前进水位）
- Test: `internal/executor/opencode/reconcile_internal_test.go`（新建）

**Interfaces:**
- Consumes: `procInfo.LastTurnMsgID`（Task 1）、`(*API).LastAssistantMessage`（Task 2）、`(*Adapter).emit`（`adapter.go:744`）、`turn.ParseTrailer`（`internal/executor/turn`）
- Produces:
  ```go
  // internal/executor/reconcile.go
  type ReconcileOutcome struct {
      TurnEnded bool
      Emitted   int
      Pending   int
      Note      string
  }
  // internal/executor/opencode/reconcile.go
  func (a *Adapter) Reconcile(ctx context.Context, taskID string) (executor.ReconcileOutcome, error)
  ```

- [ ] **Step 1: 写失败的测试**

在 `internal/executor/opencode/reconcile_internal_test.go` 新建。这五条覆盖 spec §8.1 的 1–5：

```go
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

// fakeSession 起一个只回 /session/{id}/message 的假 serve，按传入的消息列表回应。
func fakeSession(t *testing.T, msgs []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(msgs)
	}))
}

// assistantMsg 造一条 assistant 消息；completed=0 表示回合仍在进行。
func assistantMsg(id string, completed int64, text string) map[string]any {
	return map[string]any{
		"info": map[string]any{
			"id": id, "role": "assistant",
			"time": map[string]any{"completed": completed},
		},
		"parts": []map[string]any{{"type": "text", "text": text}},
	}
}

// newTestRun 建一个挂在假 serve 上的运行态，水位设为 watermark。
func newTestRun(t *testing.T, a *Adapter, srvURL, watermark string) *runState {
	t.Helper()
	taskDir := t.TempDir()
	r := a.newRun("task-1", taskDir, taskDir)
	r.session = "ses_test"
	r.api = NewAPI(srvURL, "pw")
	if err := writeProcInfo(taskDir, &procInfo{
		Handle:        testHandle(taskDir),
		Port:          1,
		Password:      "pw",
		LastTurnMsgID: watermark,
	}); err != nil {
		t.Fatalf("写凭据失败: %v", err)
	}
	return r
}

// drainOne 读一条事件；没有事件时返回 ok=false。
func drainOne(r *runState) (executor.AdapterEvent, bool) {
	select {
	case ev := <-r.evCh:
		return ev, true
	default:
		return executor.AdapterEvent{}, false
	}
}

// TestReconcileEmitsLostTerminalEvent —— spec §8.1 断言 1：
// 回合已完结、水位落后 → 补发 1 条，水位前进。
func TestReconcileEmitsLostTerminalEvent(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantMsg("msg_new", 1786348485642,
			"活干完了\n"+`{"branch":"handoff/x","commit":"abc1234","summary":"改完了"}`),
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "msg_old")

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if !out.TurnEnded || out.Emitted != 1 {
		t.Fatalf("应补发 1 条终态，got TurnEnded=%v Emitted=%d note=%s",
			out.TurnEnded, out.Emitted, out.Note)
	}
	ev, ok := drainOne(r)
	if !ok {
		t.Fatal("事件通道里没有补发的事件")
	}
	if ev.Type != "result" || ev.Result == nil || !ev.Result.OK {
		t.Fatalf("应补发成功结果，got %+v", ev)
	}
	pi, err := readProcInfo(r.taskDir)
	if err != nil {
		t.Fatalf("读凭据失败: %v", err)
	}
	if pi.LastTurnMsgID != "msg_new" {
		t.Fatalf("水位应前进到 msg_new，got %q", pi.LastTurnMsgID)
	}
}

// TestReconcileIsIdempotent —— spec §8.1 断言 2（幂等的核心断言）：
// 回合已完结但水位已过 → 补 0 条。
func TestReconcileIsIdempotent(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantMsg("msg_same", 1786348485642, "活干完了"),
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "msg_same") // 水位 == 尾部消息

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 0 {
		t.Fatalf("水位已过，不应补发，got Emitted=%d note=%s", out.Emitted, out.Note)
	}
	if _, ok := drainOne(r); ok {
		t.Fatal("水位已过却补发了事件")
	}
}

// TestReconcileSkipsWhenTurnStillRunning —— spec §8.1 断言 3：
// 会话仍在忙 → 补 0 条，不改水位。
func TestReconcileSkipsWhenTurnStillRunning(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantMsg("msg_running", 0, "正在干"), // completed=0
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "msg_old")

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.TurnEnded || out.Emitted != 0 {
		t.Fatalf("回合仍在跑，不应补发，got %+v", out)
	}
	pi, _ := readProcInfo(r.taskDir)
	if pi.LastTurnMsgID != "msg_old" {
		t.Fatalf("回合未完结时水位不应动，got %q", pi.LastTurnMsgID)
	}
}

// TestReconcileQueryFailureDoesNotEmit —— spec §8.1 断言 4：
// 查询失败 → 补 0 条并返回 error（调用方据此只记 WARN，不改状态）。
func TestReconcileQueryFailureDoesNotEmit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "msg_old")

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err == nil {
		t.Fatal("查询失败应返回 error")
	}
	if out.Emitted != 0 {
		t.Fatalf("查询失败不应补发，got Emitted=%d", out.Emitted)
	}
	if _, ok := drainOne(r); ok {
		t.Fatal("查询失败却补发了事件")
	}
}

// TestReconcileRestoresQuestionNotResult —— spec §8.1 断言 5，**本设计的核心断言**：
// 以提问收尾的回合必须还原成 question 工单，而不是一条假的「做完了」。
//
// why 这条最重要：若对账一律合成 result，审核者会以为任务完成，实际模型正在等
// 他回答——任务换个姿势继续冻死，而且这次连 stalled 都不会再报（状态已离开 running）。
func TestReconcileRestoresQuestionNotResult(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantMsg("msg_ask", 1786348485642,
			"我需要确认一件事\n"+`{"question":"用 A 方案还是 B 方案？"}`),
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "msg_old")

	out, err := a.Reconcile(context.Background(), r.taskID)
	if err != nil {
		t.Fatalf("对账失败: %v", err)
	}
	if out.Emitted != 1 {
		t.Fatalf("应补发 1 条，got %d note=%s", out.Emitted, out.Note)
	}
	ev, ok := drainOne(r)
	if !ok {
		t.Fatal("没有补发事件")
	}
	if ev.Type != "question" {
		t.Fatalf("以提问收尾的回合必须还原成 question，got %q（内容 %q）", ev.Type, ev.Text)
	}
	if ev.Text == "" {
		t.Fatal("提问文本不应为空")
	}
	_ = fmt.Sprint() // 保留 fmt 引用
}
```

**注意**：`newTestAdapter` 与 `testHandle` 是本包既有测试助手。执行者先 `grep -rn "func newTestAdapter\|func testHandle" internal/executor/opencode/*_test.go` 确认它们的真实签名；若不存在，按本包既有测试文件里构造 Adapter 与 `prochost.Handle` 的写法就地补两个助手（`newTestAdapter` 返回一个带 `slog` 与空 `runs` 的 `*Adapter`；`testHandle` 返回一个 `LockPath` 指向临时目录、`PID` 非零的 `prochost.Handle`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestReconcile -v`
Expected: 编译失败，`a.Reconcile undefined`

- [ ] **Step 3: 建共享数据类型**

新建 `internal/executor/reconcile.go`：

```go
// reconcile.go —— 断连窗口会话对账的共享数据契约（B38）。
//
// 职责：
//   - 定义 ReconcileOutcome，供 manager 与各 adapter 共用
//
// 边界：
//   - 只有数据，没有接口：与 resume.go 同规格——对账能力的接口由消费方
//     （manager）定义并做类型断言，这样「不支持对账的 adapter」仍是自然语义，
//     executor.Adapter 的五动作核心契约也不被污染
//   - 无 I/O、无实现
package executor

// ReconcileOutcome 是一次对账的结论，供 CLI 呈现与日志记录。
//
// 字段说明：
//   - TurnEnded: 断连期间回合是否已完结
//   - Emitted: 补发的终态事件数。取值只有 0 或 1——一个断连窗口内至多跨越
//     一个回合边界（新回合只能由经过 agentd 的 Start/Send 发起）
//   - Pending: 重新上报的悬而未决权限请求数。**opencode 恒为 0**：它的消息流
//     里 tool part 只有 callID 没有权限 id，应答端点要求真实 id、伪造即 404，
//     故建出的工单批了也送不回去——宁可不建
//   - Note: 一句话结论，直接给审核者看
type ReconcileOutcome struct {
	TurnEnded bool
	Emitted   int
	Pending   int
	Note      string
}
```

- [ ] **Step 4: 记录当前回合的 assistant 消息 id**

`internal/executor/opencode/adapter.go`：

1. `runState` 结构体里，在 `lastProgress` 之后加字段（并入 `turnMu` 保护的那组）：

```go
	// lastAssistantMsgID 是本回合最后一条 assistant 消息的 id（turnMu 保护）。
	// 它是对账水位的来源：mapIdle 正常分类完一个回合后，把它写进 proc.json，
	// 使断连恢复后的对账能判出「这个回合我已经消费过了」。不写就会重复补发。
	lastAssistantMsgID string
```

2. 在 `mapPart` 里，判定 `isText` 为真之后（`adapter.go:1061` 附近）记录：

```go
	if isText {
		r.lastAssistantMsgID = p.MessageID
	}
```

3. 在 `mapPartDelta` 里，通过 `userMsgs` 过滤之后（`adapter.go:1124` 附近）同样记录：

```go
	r.lastAssistantMsgID = pd.MessageID
```

两处都在 `turnMu` 已持有的调用路径内（`mapEvent` 持锁调用），执行者需核对确认，若不在锁内则就地补锁。

- [ ] **Step 5: 正常路径分类后前进水位**

`internal/executor/opencode/adapter.go` 的 `mapIdle`：在**三条 emit 分支各自的 `r.clearTurn()` 之前**调用新助手。为避免重复三份，把它加进 `clearTurn` 的调用点前统一处理——最简做法是在 `mapIdle` 函数体开头取出消息 id，并在每条 `r.clearTurn()` 前一行插入：

```go
	a.advanceWatermark(r)
```

在 `adapter.go` 末尾加助手：

```go
// advanceWatermark 把本回合最后一条 assistant 消息 id 落进 proc.json，作为对账水位。
//
// why（正常路径也必须写）：对账靠「会话尾部消息 id != 水位」判定「有未消费的
// 已完结回合」。若只在对账成功时写水位，一个正常送达的终态就不会推进水位，
// 下一次对账会把它当成丢失事件**重复补发**一遍。
//
// 失败只 Warn 不中断：水位写不进去的后果是下次可能重复补发一条终态（事件表多
// 一条、状态机 waiting_review→waiting_review 被 ErrBadTransit 挡掉），比中断
// 回合分类轻得多。
func (a *Adapter) advanceWatermark(r *runState) {
	msgID := r.lastAssistantMsgID
	if msgID == "" {
		a.log.Debug("本回合无 assistant 消息 id，跳过水位前进", "task", r.taskID)
		return
	}
	pi, err := readProcInfo(r.taskDir)
	if err != nil {
		a.log.Warn("前进对账水位失败：读凭据出错，下次对账可能重复补发",
			"task", r.taskID, "msg", msgID, "cause", err)
		return
	}
	if pi.LastTurnMsgID == msgID {
		return // 已是当前值，不必写盘
	}
	old := pi.LastTurnMsgID
	pi.LastTurnMsgID = msgID
	if err := writeProcInfo(r.taskDir, pi); err != nil {
		a.log.Warn("前进对账水位失败：写凭据出错，下次对账可能重复补发",
			"task", r.taskID, "msg", msgID, "cause", err)
		return
	}
	a.log.Info("对账水位已前进", "task", r.taskID, "from", old, "to", msgID)
}
```

- [ ] **Step 6: 实现 Reconcile**

新建 `internal/executor/opencode/reconcile.go`：

```go
// reconcile.go —— 断连窗口的会话对账（B38）。
//
// 职责：
//   - Reconcile：查会话尾部与持久化水位比对，把断连期间错过的回合终态补回事件流
//
// 边界：
//   - 不写 store、不改任务状态：补发的事件经既有 evCh 交给 manager，状态迁移归它
//   - 不发明事件语义：取回的文本交给既有的 turn.ParseTrailer 分类，产出与实时
//     路径同形的 question / result
//   - **不捧回权限请求**：opencode 的消息流里 tool part 只有 callID 没有权限 id，
//     而 RespondPermission 要求真实 id、伪造即 404（更早的 spike 结论，见
//     adapter.go 的 onReconnect 降级告警）。建一张批了也送不回去的工单比不建更糟，
//     故 ReconcileOutcome.Pending 在本 adapter 恒为 0
package opencode

import (
	"context"
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

// Reconcile 把断连期间错过的回合终态补回事件流。
//
// 参数：
//   - ctx: 控制查询超时
//   - taskID: 目标任务；运行态不在（未 Start / 已 Stop）时返回「无运行态」结论而非错误
//
// 返回：
//   - ReconcileOutcome: 结论。Emitted 只可能是 0 或 1（spec §2.2 的不变量）；
//     Pending 恒为 0（见文件头注释）
//   - err: 查会话失败。**调用方收到错误时只记 WARN、不改任何状态**——一次网络
//     抖动不该把能恢复的任务判成不可恢复
//
// 注意：
//   - 首次对账（水位为空串）**不补发**，只把当前尾部认作已消费。否则升级到本
//     版本的存量任务会在第一次恢复时集体重放最后一个回合
//   - 「补发 → 前进水位」在 turnMu 下串行完成，与实时路径的 mapIdle 互斥；
//     拆成两步会让两条路同时判「未消费」而补出两条终态
func (a *Adapter) Reconcile(ctx context.Context, taskID string) (executor.ReconcileOutcome, error) {
	a.log.Info("adapter 开始对账", "task", taskID)

	a.mu.Lock()
	r := a.runs[taskID]
	a.mu.Unlock()
	if r == nil || r.api == nil {
		a.log.Info("对账跳过：该任务无运行态", "task", taskID)
		return executor.ReconcileOutcome{Note: "该任务当前无运行态，无需对账"}, nil
	}

	msg, err := r.api.LastAssistantMessage(ctx, r.session)
	if err != nil {
		a.log.Warn("对账查会话尾部失败，不改任何状态", "task", taskID, "cause", err)
		return executor.ReconcileOutcome{Note: "查会话尾部失败"},
			fmt.Errorf("对账查会话尾部: %w", err)
	}
	if msg == nil {
		a.log.Info("对账结论：会话尚无 assistant 消息", "task", taskID)
		return executor.ReconcileOutcome{Note: "会话里还没有模型消息，无需对账"}, nil
	}
	if msg.CompletedMS == 0 {
		a.log.Info("对账结论：回合仍在进行", "task", taskID, "msg", msg.ID)
		return executor.ReconcileOutcome{Note: "executor 的回合仍在进行中，没有丢失的终态"}, nil
	}

	// 补发与水位前进必须在同一把锁下完成：实时路径的 mapIdle 也走这条判据，
	// 拆开就是 check-then-act，两条路会同时判「未消费」而补出两条终态
	r.turnMu.Lock()
	defer r.turnMu.Unlock()

	pi, err := readProcInfo(r.taskDir)
	if err != nil {
		a.log.Warn("对账读凭据失败，不改任何状态", "task", taskID, "cause", err)
		return executor.ReconcileOutcome{Note: "读恢复凭据失败"},
			fmt.Errorf("对账读恢复凭据: %w", err)
	}
	if pi.LastTurnMsgID == msg.ID {
		a.log.Info("对账结论：终态已送达过，无需补发", "task", taskID, "msg", msg.ID)
		return executor.ReconcileOutcome{TurnEnded: true,
			Note: "回合已完结，且终态此前已送达，无需补发"}, nil
	}
	if pi.LastTurnMsgID == "" {
		// 首次对账：把当前尾部认作已消费。存量任务升级到本版本后水位是空的，
		// 不做这条保护会让它们在第一次恢复时集体重放最后一个回合
		pi.LastTurnMsgID = msg.ID
		if werr := writeProcInfo(r.taskDir, pi); werr != nil {
			a.log.Warn("首次对账写水位失败", "task", taskID, "cause", werr)
		}
		a.log.Info("对账结论：首次对账，已把当前会话尾部认作基线，不补发",
			"task", taskID, "msg", msg.ID)
		return executor.ReconcileOutcome{TurnEnded: true,
			Note: "首次对账，已记录当前进度为基线（不回溯此前的回合）"}, nil
	}

	ev, note := a.classifyReconciled(r, msg)
	if !a.emit(r, ev) {
		a.log.Warn("对账补发失败：事件通道已关闭", "task", taskID, "msg", msg.ID)
		return executor.ReconcileOutcome{TurnEnded: true,
			Note: "回合已完结但事件通道已关闭，未能补发"}, nil
	}
	pi.LastTurnMsgID = msg.ID
	if werr := writeProcInfo(r.taskDir, pi); werr != nil {
		a.log.Warn("对账后写水位失败，下次对账可能重复补发",
			"task", taskID, "msg", msg.ID, "cause", werr)
	}
	a.log.Info("对账完成：已补发断连期间丢失的终态",
		"task", taskID, "msg", msg.ID, "event", ev.Type, "note", note)
	return executor.ReconcileOutcome{TurnEnded: true, Emitted: 1, Note: note}, nil
}

// classifyReconciled 把取回的消息翻译成一条与实时路径同形的 AdapterEvent。
//
// 分类**复用** turn.ParseTrailer——与 mapIdle 走同一套判据，于是以提问收尾的
// 回合会正确地还原成 question 工单，而不是一条假的「做完了」。
//
// 返回：事件本身，以及一句给审核者看的结论。
func (a *Adapter) classifyReconciled(r *runState, msg *SessionMessage) (executor.AdapterEvent, string) {
	if msg.ErrorText != "" {
		a.log.Warn("对账发现回合以错误告终", "task", r.taskID, "msg", msg.ID,
			"error", turn.TruncateRunes(msg.ErrorText, 200))
		return executor.AdapterEvent{Type: "result", SessionID: r.session,
				Result: &executor.Result{OK: false,
					FailReason: "回合在 agentd 断连期间以错误告终：" +
						turn.TruncateRunes(msg.ErrorText, 200)}},
			"补回了一条断连期间丢失的失败结果"
	}
	kind, t := turn.ParseTrailer(msg.Text)
	switch kind {
	case "ask":
		return executor.AdapterEvent{Type: "question", SessionID: r.session,
			Text: turn.ClampQuestion(t.Question)}, "补回了一条断连期间丢失的提问"
	case "finish":
		return executor.AdapterEvent{Type: "result", SessionID: r.session,
			Result: &executor.Result{OK: true, Branch: t.Branch, CommitHash: t.Commit,
				Summary: t.Summary, SessionID: r.session}}, "补回了一条断连期间丢失的完成结果"
	}
	// 无协议 trailer：不走 mapIdle 的 git 兜底（那套依赖 startCommit 基线，而
	// 断连期间基线已失去意义）。交审核者裁决，把回合原文给他
	a.log.Warn("对账发现回合无协议 trailer，转提问交审核者裁决",
		"task", r.taskID, "msg", msg.ID)
	return executor.AdapterEvent{Type: "question", SessionID: r.session,
		Text: turn.ClampQuestion("agentd 断连期间该回合已结束，但未输出协议结论。" +
			"回合原文：\n" + turn.TailRunes(msg.Text, 1000))}, "补回了一条断连期间丢失的回合，需人工裁决"
}
```

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run 'TestReconcile|TestProcInfo|TestLastAssistantMessage' -v`
Expected: 全 PASS。若 `TestReconcileEmitsLostTerminalEvent` 因水位为空串走了「首次对账」分支，说明测试的 `newTestRun` 没把 watermark 写进去——修测试夹具，不要改生产代码的首次对账语义。

- [ ] **Step 8: 跑整包与竞态检查**

Run: `go test ./internal/executor/opencode/ && go test -race ./internal/executor/opencode/`
Expected: 两条都 ok。`-race` 是本 task 的重点——新增的 `lastAssistantMsgID` 被订阅 goroutine 与 idle 定时器 goroutine 共访。

- [ ] **Step 9: 加日志**

Step 6 的实现已含：进入 Info；五条提前返回分支各一条 Info/Warn（无运行态、查询失败、无消息、回合仍在跑、已送达过）；首次对账 Info；补发成功 Info（带 msg id 与事件类型）；三条写盘失败 Warn（都写明「下次可能重复补发」这个实际后果）。Step 5 的 `advanceWatermark` 已含成功 Info 与两条失败 Warn。

执行者核对：**每一条 return 路径都有一条日志**，没有静默出口；失败日志都带 `cause`；日志里不含模型输出正文（错误文本截断 200 字）。

- [ ] **Step 10: 加注释**

Step 6 已含文件头（职责/边界/为什么 Pending 恒为 0）、`Reconcile` doc（参数/返回/首次对账语义/锁的 why）、`classifyReconciled` doc（为什么复用 ParseTrailer）、无 trailer 分支为什么不走 git 兜底。Step 4 已含 `lastAssistantMsgID` 的 why，Step 5 已含 `advanceWatermark` 的「正常路径也必须写」的 why。

执行者核对 `internal/executor/reconcile.go` 的文件头注释写明了「只有数据没有接口」的既有约定来由。

- [ ] **Step 11: 提交**

```bash
git add internal/executor/reconcile.go internal/executor/opencode/reconcile.go internal/executor/opencode/reconcile_internal_test.go internal/executor/opencode/adapter.go
git commit -m "feat(opencode): 实现断连窗口的会话对账，补发丢失的回合终态"
```

---

## Task 4: 接到两个自动触发点

**Files:**
- Modify: `internal/executor/opencode/resume.go:159-165`（`Resume` 末尾）
- Modify: `internal/executor/opencode/adapter.go:604-613`（`onReconnect` 回调）
- Test: `internal/executor/opencode/reconcile_trigger_internal_test.go`（新建）

**Interfaces:**
- Consumes: `(*Adapter).Reconcile(ctx, taskID)`（Task 3）
- Produces: 无新导出符号；`Resume` 与 `onReconnect` 的行为变更

- [ ] **Step 1: 写失败的测试**

```go
package opencode

import (
	"context"
	"testing"
	"time"
)

// TestResumeTriggersReconcile 验热重连成功后会自动对一次账。
func TestResumeTriggersReconcile(t *testing.T) {
	srv := fakeSession(t, []map[string]any{
		assistantMsg("msg_lost", 1786348485642,
			"活干完了\n"+`{"branch":"handoff/x","commit":"abc1234","summary":"改完了"}`),
	})
	defer srv.Close()
	a := newTestAdapter(t)
	r := newTestRun(t, a, srv.URL, "msg_old")

	// 直接调对账触发点的那段逻辑：Resume 的完整路径需要真实 shim，
	// 此处验的是「reattach 成功后确实调了 Reconcile」这一条接线
	a.reconcileAfterRecovery(context.Background(), r.taskID, "startup")

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-r.evCh:
			if ev.Type == "result" {
				return // 接线生效
			}
		case <-deadline:
			t.Fatal("恢复后未触发对账：2 秒内没有补发的终态事件")
		}
	}
}

// TestReconcileAfterRecoverySwallowsError 验对账失败不向上冒泡。
//
// why：spec §6.3 的硬要求——一次网络抖动不该把能恢复的任务判成不可恢复。
func TestReconcileAfterRecoverySwallowsError(t *testing.T) {
	a := newTestAdapter(t)
	// 不建运行态：Reconcile 会走「无运行态」分支；再造一个必失败的场景
	// 由 Reconcile 内部返回 error，此处只验本函数不 panic 不阻塞
	a.reconcileAfterRecovery(context.Background(), "no-such-task", "reconnect")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run 'TestResumeTriggers|TestReconcileAfterRecovery' -v`
Expected: 编译失败，`a.reconcileAfterRecovery undefined`

- [ ] **Step 3: 实现触发助手**

在 `internal/executor/opencode/reconcile.go` 末尾加：

```go
// reconcileAfterRecovery 是两个自动触发点共用的对账入口。
//
// 参数：
//   - ctx: 控制查询超时
//   - taskID: 目标任务
//   - trigger: 触发来源，只用于日志（"startup" = agentd 启动恢复，
//     "reconnect" = 连接断开重连）
//
// 注意：
//   - **不返回错误，且绝不 panic**：spec §6.3 的硬要求——对账失败不能阻断恢复。
//     一次网络抖动若能让 Resume 判不可恢复，比 B38 本身还糟
func (a *Adapter) reconcileAfterRecovery(ctx context.Context, taskID, trigger string) {
	out, err := a.Reconcile(ctx, taskID)
	if err != nil {
		a.log.Warn("恢复后对账失败，恢复本身不受影响",
			"task", taskID, "trigger", trigger, "cause", err)
		return
	}
	a.log.Info("恢复后对账完成", "task", taskID, "trigger", trigger,
		"turn_ended", out.TurnEnded, "emitted", out.Emitted, "note", out.Note)
}
```

- [ ] **Step 4: 接到 Resume**

`internal/executor/opencode/resume.go`，把结尾的返回段改为：

```go
	r.captureStartCommit(a)
	go r.subscribeLoop(a)
	go a.watchdog(r)
	// 对账（B38）：断连窗口内完成的回合，其终态事件在 /event 上永久丢失
	// （无重放语义），不对账就会冻死在 running。fresh 模式不对账——那是新会话，
	// 没有「错过的进展」，且水位已随新会话失去意义
	if mode != executor.ResumeModeFresh {
		go a.reconcileAfterRecovery(context.Background(), req.TaskID, "startup")
	}
	return executor.ResumeOutcome{
		Alive: true, Mode: mode, SessionID: sessionID,
		Note: resumeNote(mode, sessionID),
	}, nil
```

**为什么用 `go`**：`Resume` 在 `RecoverOnStartup` 的串行循环里被调用（`watchdog.go:207`），而对账要发一次 HTTP。同步做会让一台机器上 N 个任务的启动恢复串行等 N 次网络往返；且对账的产物走 `evCh`，本就不需要 `Resume` 等它。

**同时**：`fresh` 分支要清零水位。在 `sessionID, mode = newID, executor.ResumeModeFresh` 之后加：

```go
			// 新会话让旧水位失去意义：不清零的话下次对账会拿旧会话的消息 id
			// 去比新会话的尾部，必然判「有未消费的回合」而补出一条假终态
			if werr := writeProcInfo(req.TaskDir, &procInfo{
				Handle: proc.Handle, Port: proc.Port, Password: proc.Password,
			}); werr != nil {
				a.log.Warn("新会话清零对账水位失败", "task", req.TaskID, "cause", werr)
			}
```

- [ ] **Step 5: 接到 onReconnect**

`internal/executor/opencode/adapter.go` 的 `subscribeLoop`，把 `onReconnect` 回调体改为（**保留**既有的权限丢失告警，它描述的是对账修不了的那半边）：

```go
	}, func() {
		// P1-10b：/event 无重放语义，断连间隙服务端产出的事件永久丢失。
		// B38 起，回合终态那半边由对账补回；权限请求那半边补不回来——消息流的
		// tool part 只有 callID 没有权限 id，应答端点要求真实 id、伪造即 404，
		// 故仍保留本告警，它是审核者知道「可能需要 attach 人工兜底」的唯一信号
		a.log.Warn("SSE 断连已恢复：断连间隙的权限请求可能丢失（/event 无重放语义），"+
			"若任务卡在等待决策请 handoff attach 查看或 handoff resume --force 收口",
			"task", r.taskID, "session", r.session)
		go a.reconcileAfterRecovery(context.Background(), r.taskID, "reconnect")
	})
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -v && go test -race ./internal/executor/opencode/`
Expected: 全 PASS

- [ ] **Step 7: 加日志**

Step 3 的 `reconcileAfterRecovery` 已含成功 Info（带 trigger / turn_ended / emitted / note）与失败 Warn（带 trigger / cause）。Step 5 保留了既有的断连 Warn 并更新了其中的可行动指引（从「重启任务」改为「attach 或 resume --force」——后者是本计划新增的真实出口）。

执行者核对：`trigger` 字段在两个触发点分别是 `"startup"` 与 `"reconnect"`，日志可据此区分两条路。

- [ ] **Step 8: 加注释**

Step 3/4/5 已含 `reconcileAfterRecovery` 的 doc（含「不返回错误」的 why）、`Resume` 里 `fresh` 不对账的 why、用 `go` 异步的 why、`fresh` 清零水位的 why、`onReconnect` 里保留旧告警的 why。

- [ ] **Step 9: 提交**

```bash
git add internal/executor/opencode/resume.go internal/executor/opencode/adapter.go internal/executor/opencode/reconcile_trigger_internal_test.go
git commit -m "feat(opencode): 恢复与重连后自动对账，fresh 模式清零水位"
```

---

## Task 5: manager 接入对账与 --force 收口

**Files:**
- Modify: `internal/agentd/manager.go:1627-1638`（`RecoverReport` 加字段）、`manager.go:1670`（`RecoverStuck` 改签名与逻辑）
- Test: `internal/agentd/session_reconcile_test.go`（新建，**不叫 reconcile_test.go**——同包已有另一种对账）

**Interfaces:**
- Consumes: `executor.ReconcileOutcome`（Task 3）、`(*Adapter).Reconcile`（Task 3，经类型断言）
- Produces:
  ```go
  type reconciler interface {
      Reconcile(ctx context.Context, taskID string) (executor.ReconcileOutcome, error)
  }
  func (m *Manager) RecoverStuck(taskID string, force bool) (*RecoverReport, error)
  // RecoverReport 新增：Reconciled bool / TurnEnded bool / Emitted int / Forced bool
  ```

**接口归属**：`reconciler` 定义在 `manager.go` 里且**私有**——`internal/executor/resume.go:6-9` 明写这是本仓库的既有约定（`restorer`、`volatilePermitter` 同形），照抄不另发明。

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/session_reconcile_test.go` 新建。执行者先 `grep -rn "func newTestManager\|mgrWithAdapter" internal/agentd/*_test.go` 找本包既有的 Manager 测试搭建方式并复用；下面用 `newTestManager` 指代它：

```go
package agentd

import (
	"context"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
)

// fakeReconciler 是一个可编程的对账 adapter 桩。
type fakeReconciler struct {
	executor.Adapter
	out    executor.ReconcileOutcome
	err    error
	called int
}

func (f *fakeReconciler) Reconcile(ctx context.Context, taskID string) (executor.ReconcileOutcome, error) {
	f.called++
	return f.out, f.err
}

// TestRecoverStuckFallsThroughToReconcile 验「没有未送达应答」时转入对账，
// 而不是像从前一样直接回「无需恢复」。
func TestRecoverStuckFallsThroughToReconcile(t *testing.T) {
	fr := &fakeReconciler{out: executor.ReconcileOutcome{
		TurnEnded: true, Emitted: 1, Note: "补回了一条断连期间丢失的完成结果"}}
	m, taskID := newTestManager(t, fr, proto.TaskStateRunning)

	rep, err := m.RecoverStuck(taskID, false)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if fr.called != 1 {
		t.Fatalf("应调用一次对账，got %d", fr.called)
	}
	if !rep.Reconciled || rep.Emitted != 1 {
		t.Fatalf("报告应体现对账结果，got %+v", rep)
	}
	if rep.Note == "没有卡在半路的应答，无需恢复" {
		t.Fatal("不应再回旧文案——那正是 B38 里审核者撞上的死路")
	}
}

// TestRecoverStuckUnsupportedAdapterIsHonest 验 adapter 未实现对账时如实说明，
// 不伪装成「对账过了」。
func TestRecoverStuckUnsupportedAdapterIsHonest(t *testing.T) {
	m, taskID := newTestManager(t, &noReconcileAdapter{}, proto.TaskStateRunning)

	rep, err := m.RecoverStuck(taskID, false)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if rep.Reconciled {
		t.Fatal("adapter 不支持对账时 Reconciled 必须为 false")
	}
	if rep.State != proto.TaskStateRunning {
		t.Fatalf("不支持对账时不应改状态，got %s", rep.State)
	}
}

// TestRecoverStuckForceTransitsToReview 验 --force 在对账判「仍在忙」时
// 仍把任务收口到 waiting_review，并留下人工强制的事件。
func TestRecoverStuckForceTransitsToReview(t *testing.T) {
	fr := &fakeReconciler{out: executor.ReconcileOutcome{
		TurnEnded: false, Note: "executor 的回合仍在进行中，没有丢失的终态"}}
	m, taskID := newTestManager(t, fr, proto.TaskStateRunning)

	rep, err := m.RecoverStuck(taskID, true)
	if err != nil {
		t.Fatalf("强制收口失败: %v", err)
	}
	if !rep.Forced {
		t.Fatal("报告应标明是人工强制收口")
	}
	if rep.State != proto.TaskStateWaitingReview {
		t.Fatalf("强制收口后应落 waiting_review，got %s", rep.State)
	}
	evs, err := m.st.ListEvents(taskID, 0)
	if err != nil {
		t.Fatalf("读事件失败: %v", err)
	}
	found := false
	for _, e := range evs {
		if containsAll(e.Text, "人工强制收口", "未经 executor 确认") {
			found = true
		}
	}
	if !found {
		t.Fatal("强制收口必须留下写明「人工强制、未经 executor 确认」的事件")
	}
}
```

执行者需按本包既有 store API 核对 `ListEvents` 与事件文本字段的真实名字，并就地补 `containsAll` 助手与 `noReconcileAdapter`（一个只实现 `executor.Adapter` 五动作、不实现 `Reconcile` 的空桩）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestRecoverStuck -v`
Expected: 编译失败，`m.RecoverStuck` 参数个数不对 / `rep.Reconciled undefined`

- [ ] **Step 3: 扩展 RecoverReport**

`internal/agentd/manager.go`：

```go
// RecoverReport 是显式恢复操作的结果快照，原样作为 HTTP 响应体回给 CLI。
type RecoverReport struct {
	Task string `json:"task"`
	// Redelivered 是本次成功重投给 executor 的应答条数
	Redelivered int `json:"redelivered"`
	// ExecutorGone 为真表示 executor 已不在，任务已被交给审核者裁决
	ExecutorGone bool `json:"executor_gone"`
	// Reconciled 为真表示本次真的执行了会话对账（adapter 支持且任务状态合适）；
	// 为假时 TurnEnded/Emitted 无意义
	Reconciled bool `json:"reconciled"`
	// TurnEnded 表示对账查到的回合是否已完结
	TurnEnded bool `json:"turn_ended"`
	// Emitted 是对账补发的终态事件数（0 或 1）
	Emitted int `json:"emitted"`
	// Forced 为真表示本次走了 --force 强制收口（状态由人工推动，未经 executor 确认）
	Forced bool `json:"forced"`
	// State 是操作完成后的任务状态
	State proto.TaskState `json:"state"`
	// Note 是给审核者看的一句话结论
	Note string `json:"note"`
}

// reconciler 是 adapter 的可选对账能力（B38）。
//
// 为什么定义在 manager 而不是 executor 包：沿用 internal/executor/resume.go
// 明写的既有约定——能力接口由消费方定义并做类型断言，这样「不支持对账的
// adapter」是自然语义，executor.Adapter 的五动作核心契约也不被污染。
// restorer / volatilePermitter 都是这个形状。
type reconciler interface {
	Reconcile(ctx context.Context, taskID string) (executor.ReconcileOutcome, error)
}
```

- [ ] **Step 4: 改 RecoverStuck**

把 `manager.go:1670` 起的函数签名改为 `func (m *Manager) RecoverStuck(taskID string, force bool) (*RecoverReport, error)`，并把「无未送达应答」那段（`manager.go:1684-1688`）替换为转入对账：

```go
	if len(stuck) == 0 {
		m.log.Info("恢复操作：无未送达应答，转入会话对账", "task", taskID, "state", task.State)
		m.reconcileInto(rep, taskID, task.State)
		if force {
			m.forceToReview(rep, taskID, task.State)
		}
		return rep, nil
	}
```

在 `RecoverStuck` 之后加两个助手：

```go
// reconcileInto 执行一次会话对账并把结论写进报告（B38）。
//
// 参数：
//   - rep: 待填充的报告；本函数只写 Reconciled/TurnEnded/Emitted/State/Note
//   - taskID / state: 目标任务与其当前状态
//
// 注意：
//   - 只对 running / waiting_answer 生效。其余状态如实说明而不是静默成功：
//     pending 尚未启动、waiting_review 本就该走 continue/done、终态已结束
//   - adapter 未实现 reconciler 时不改状态、不伪装成「对账过了」
//   - 对账失败只记 WARN 并把原因写进 Note，**不返回错误**——审核者要的是
//     「现在怎么办」，不是一个让 CLI 退非零的堆栈
func (m *Manager) reconcileInto(rep *RecoverReport, taskID string, state proto.TaskState) {
	if state != proto.TaskStateRunning && state != proto.TaskStateWaitingAnswer {
		rep.Note = fmt.Sprintf("没有卡在半路的应答；任务处于 %s，不在对账范围"+
			"（pending 尚未启动、waiting_review 请用 continue/done）", state)
		m.log.Info("恢复操作：状态不在对账范围", "task", taskID, "state", state)
		return
	}
	rc, ok := m.adapter.(reconciler)
	if !ok {
		rep.Note = "没有卡在半路的应答；当前 executor 不支持会话对账，" +
			"可 handoff attach 查看现场，或 handoff resume --force 强制收口交审核"
		m.log.Info("恢复操作：adapter 不支持对账", "task", taskID)
		return
	}
	out, err := rc.Reconcile(context.Background(), taskID)
	if err != nil {
		rep.Note = "没有卡在半路的应答；会话对账失败（" + err.Error() +
			"），未改动任何状态，可稍后重试或 --force 强制收口"
		m.log.Warn("恢复操作：会话对账失败", "task", taskID, "cause", err)
		return
	}
	rep.Reconciled = true
	rep.TurnEnded = out.TurnEnded
	rep.Emitted = out.Emitted
	rep.Note = out.Note
	if out.Emitted > 0 {
		// 补发的事件已走 evCh 进中介循环，状态由它推动；此处重读一次让报告
		// 里的 State 是对账之后的真实值而不是进函数时的快照
		if t, gerr := m.st.GetTask(taskID); gerr == nil {
			rep.State = t.State
		}
	}
	m.log.Info("恢复操作：会话对账完成", "task", taskID,
		"turn_ended", out.TurnEnded, "emitted", out.Emitted, "note", out.Note)
}

// forceToReview 把任务强制收口到 waiting_review（handoff resume --force）。
//
// 为什么需要它：对账判不出来（adapter 不支持 / 会话确实还在忙 / 查询失败）时，
// 审核者此前只剩 handoff stop——而 stop 会把一个其实成功了的任务落成 failed，
// 并杀掉 executor。本操作**保住会话**，只把状态推到可 continue/done 的位置。
//
// 风险与护栏：executor 可能真的还在跑，收口后 continue 会往忙碌会话里塞指令。
// 护栏只有事件文本与报告文案——不加更硬的拦截，因为更硬的拦截就是 stop，
// 而这个场景的全部意义恰恰是不杀会话。
func (m *Manager) forceToReview(rep *RecoverReport, taskID string, state proto.TaskState) {
	rep.Forced = true
	text := "审核者人工强制收口（handoff resume --force）：未经 executor 确认。" +
		"对账当时的结论是：" + rep.Note + "。若 executor 其实仍在执行，" +
		"后续 continue 的指令会进入一个忙碌会话，请先 handoff attach 确认现场。"
	m.log.Warn("恢复操作：人工强制收口", "task", taskID, "from", state, "note", rep.Note)
	m.appendProgress(taskID, text)
	if err := recoverTransit(m.st, taskID, state); err != nil {
		rep.Note = "强制收口失败：" + err.Error()
		m.log.Error("恢复操作：强制收口迁移失败", "task", taskID, "cause", err)
		return
	}
	rep.State = proto.TaskStateWaitingReview
	rep.Note = "已人工强制收口到待审核（未经 executor 确认）；" +
		"可 continue 续接或 done 归档。对账当时的结论：" + rep.Note
}
```

**执行者须先核对两处既有符号**：`m.adapter` 的真实字段名（`grep -n "adapter " internal/agentd/manager.go` 找 Manager 结构体）、以及追加 progress 事件的既有助手真实名字（`grep -rn "EventTypeProgress" internal/agentd/manager.go`，本仓库已有多处「产出 progress 事件提示人工」的写法，照抄那一处，不要新造）。`recoverTransit` 在 `internal/agentd/watchdog.go:235` 已存在且同包可用。

- [ ] **Step 5: 修所有调用点**

Run: `go build ./... 2>&1 | head`
按报错把 `RecoverStuck(taskID)` 的调用点补上第二个参数。已知调用点：`internal/agentd/server.go:639`（Task 6 会一并改）、可能的既有测试文件。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestRecoverStuck -v && go test ./internal/agentd/`
Expected: 全 PASS

- [ ] **Step 7: 加日志**

Step 4 已含：转入对账 Info、状态不在范围 Info、adapter 不支持 Info、对账失败 Warn（带 cause）、对账完成 Info（带 turn_ended/emitted/note）、强制收口 Warn（带 from 状态与对账结论）、迁移失败 Error。

执行者核对：**强制收口用 Warn 而不是 Info**——它是人工绕过正常流程的动作，值得在日志里显眼。

- [ ] **Step 8: 加注释**

Step 3/4 已含 `RecoverReport` 每个新字段的说明、`reconciler` 接口为什么定义在 manager 的 why、`reconcileInto` 的状态范围与「不返回错误」的 why、`forceToReview` 的「为什么需要」与「风险与护栏」。

同时更新 `RecoverStuck` 自身的 doc 注释（`manager.go:1640-1668`），补上它现在也处理「终态丢失」这一类卡死，并说明与既有「应答未送达」那类的分工。

- [ ] **Step 9: 提交**

```bash
git add internal/agentd/manager.go internal/agentd/session_reconcile_test.go
git commit -m "feat(agentd): resume 接入会话对账，新增 --force 强制收口"
```

---

## Task 6: HTTP / client / CLI 贯通 --force

**Files:**
- Modify: `internal/agentd/server.go:630-650`（`handleResume`）
- Modify: `internal/client/client.go:423-440`（`Resume`）
- Modify: `cmd/resume.go`
- Test: `internal/agentd/server_resume_test.go`（新建）

**Interfaces:**
- Consumes: `(*Manager).RecoverStuck(taskID string, force bool)`（Task 5）
- Produces: `(*Client).Resume(ctx context.Context, taskID string, force bool) (string, error)`；HTTP `POST /api/tasks/{id}/resume?force=true`

- [ ] **Step 1: 写失败的测试**

```go
package agentd

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleResumeParsesForce 验 force 查询参数被正确透传给 manager。
func TestHandleResumeParsesForce(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  bool
	}{
		{"", false},
		{"?force=true", true},
		{"?force=1", true},
		{"?force=false", false},
	} {
		got := parseForce(httptest.NewRequest(http.MethodPost,
			"/api/tasks/t1/resume"+tc.query, nil))
		if got != tc.want {
			t.Fatalf("query %q: force want %v got %v", tc.query, tc.want, got)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestHandleResumeParsesForce -v`
Expected: 编译失败，`parseForce undefined`

- [ ] **Step 3: 实现服务端**

`internal/agentd/server.go`，在 `handleResume` 之前加：

```go
// parseForce 解析 resume 的 force 查询参数。
//
// 为什么自己解析而不用 strconv.ParseBool 的错误：非法值（如 force=yes）一律
// 按 false 处理——强制收口是绕过正常流程的动作，看不懂的输入必须走保守的那边。
func parseForce(r *http.Request) bool {
	switch r.URL.Query().Get("force") {
	case "true", "1":
		return true
	}
	return false
}
```

把 `handleResume` 里的调用改为：

```go
	force := parseForce(r)
	s.log.Info("resume 请求", "method", r.Method, "path", r.URL.Path,
		"task", taskID, "force", force)
	...
	rep, err := s.mgr.RecoverStuck(taskID, force)
```

（把原来那条 `s.log.Info("resume 请求", ...)` 替换成上面带 `force` 的版本，不要留两条。）

同时更新 `handleResume` 的 doc 注释，补上 `force` 参数与它的语义。

- [ ] **Step 4: 实现客户端**

`internal/client/client.go` 的 `Resume` 改签名为 `Resume(ctx context.Context, taskID string, force bool) (string, error)`，请求路径按 `force` 追加 `?force=true`，并更新其 doc 注释说明新参数。执行者按该文件既有的 URL 拼接写法改，不引入新的 URL 构造方式。

- [ ] **Step 5: 实现 CLI**

`cmd/resume.go`：

```go
// resumeForce 对应 --force：对账判不出来时仍强制收口到待审核。
var resumeForce bool

// resumeCmd 恢复卡死的任务。
//
// 使用方式：handoff resume <task> [--force]
//
// 两类卡死都走这条命令，审核者不必自行诊断是哪一类：
//   - 应答已落库但没送到 executor（reply 拿到 502）→ 重投
//   - agentd 与 executor 断连期间回合已完结、终态事件丢失（B38）→ 会话对账补发
//
// --force：对账判不出来时（executor 不支持对账 / 会话确实还在忙 / 查询失败）
// 仍把任务收口到待审核，使 continue/done 可用。**保住 executor 会话**——
// 这是它与 handoff stop 的根本区别：stop 会杀掉会话并把任务落成 failed。
// 收口会留下一条写明「人工强制、未经 executor 确认」的事件。
var resumeCmd = &cobra.Command{
	Use:   "resume <task>",
	Short: "恢复卡死的任务（重投未送达的应答，或对账补回丢失的回合终态）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		report, err := client.New(addr, token).Resume(cmd.Context(), taskID, resumeForce)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), report)
		return nil
	},
}

func init() {
	resumeCmd.Flags().BoolVar(&resumeForce, "force", false,
		"对账判不出来时仍强制收口到待审核（保住 executor 会话，不同于 stop）")
	rootCmd.AddCommand(resumeCmd)
}
```

同时更新文件头注释的「职责」与「边界」两段，把对账这条职责写进去。

- [ ] **Step 6: 跑全量测试与跨平台编译门禁**

Run:
```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... && GOOS=windows GOARCH=amd64 go build ./...
```
Expected: `gofmt -l .` 只剩既有的 `internal/executor/grok/askquestion_internal_test.go`（历史遗留，非本次引入）；其余全绿。

- [ ] **Step 7: 加日志**

Step 3 已把 `force` 加进 `resume 请求` 那条 Info——这是审核者事后追查「谁强制收口了这个任务」的入口。

执行者核对：CLI 侧不加日志（它只是转发与打印，日志归 agentd），且 `--force` 的实际执行痕迹在 Task 5 的 Warn 与那条 progress 事件里。

- [ ] **Step 8: 加注释**

Step 3/4/5 已含 `parseForce` 的保守解析 why、`handleResume` 的 doc 更新、client `Resume` 的 doc 更新、CLI 的两类卡死说明与 `--force` 和 `stop` 的区别。

- [ ] **Step 9: 更新 README 与 handoff skill**

- `README.md`：`handoff resume` 的说明补上第二类卡死与 `--force`
- `skills/handoff/SKILL.md`：「任务卡在 running」一节补上 `handoff resume` 作为第一处方（在 `stop` 之前）。**这一条是本计划的用户可见价值所在**——B38 的现场里，审核者知道该敲 resume，但当时它帮不上忙

- [ ] **Step 10: 提交**

```bash
git add internal/agentd/server.go internal/agentd/server_resume_test.go internal/client/client.go cmd/resume.go README.md skills/handoff/SKILL.md
git commit -m "feat(cli): handoff resume 贯通 --force，README 与 skill 同步"
```

---

## Task 7: devbox 真机验收

**Files:**
- Modify: `docs/superpowers/specs/2026-08-10-handoff-reconnect-reconciliation-design.md`（回填 §7.1 的水位形态结论与 §8.3 的实测证据）

**Interfaces:**
- Consumes: 全部前六个 task 的产物
- Produces: spec 里的验收证据段落；backlog B38 的验收列内容

**前置**：按 B36 的做法起**旁挂实例**（独立 `--config`、独立端口与 datadir），不碰生产 7777 与 `~/.handoff`。生产 agentd 上可能有在跑的任务，切换二进制会一次性打断它们。

- [ ] **Step 1: 复现 B38 原始现场**

在旁挂实例上派发一个会产生提交的任务，等它进 `waiting_answer`，`reply` 之后**立即** kill agentd（目标是在回合完结与事件送达之间的窗口内杀掉）：

```bash
handoff --target e2e reply <task> --answer "把 README 标题改成 B38-VERIFY"
# 盯 agentd.log，见到 relayed=true 后等约 15 秒（让回合跑完），再 kill
pkill -f "handoff agentd --config .*e2e"
```

确认现场成立：`git log` 里有新提交，而 `handoff show` 的事件流停在 `question`、状态 `running`。

- [ ] **Step 2: 重启并观察自动对账**

重启旁挂 agentd，检查日志里依次出现：

```
执行器存活，重建订阅继续消费 ... alive=true
adapter 开始对账 task=<id>
opencode 会话尾部已取得 ... completed_ms=<非零>
对账完成：已补发断连期间丢失的终态 ... event=result
恢复后对账完成 ... trigger=startup emitted=1
```

然后 `handoff show <task>` 必须显示状态已变 `waiting_review`、事件流里有那条补发的 `result`。

**这一条是整个计划的存在理由**——若它不通过，前六个 task 全部白做。

- [ ] **Step 3: 验 continue 真的能续接**

```bash
handoff --target e2e continue <task> --instruction "再加一行 B38-SECOND"
```
必须产出**第二个真实提交**，证明补发终态之后会话仍然可用（对账没有以损坏会话为代价）。

- [ ] **Step 4: 验幂等**

对同一个已对过账的任务再跑一次 `handoff resume <task>`，报告里 `emitted` 必须为 **0**，事件流**不得**多出第二条 `result`。

- [ ] **Step 5: 验以提问收尾的回合**

重跑一遍 Step 1，但这次让模型的回合以**提问**收尾（`reply` 里给一个信息不足的指令，逼它反问）。窗口内 kill agentd 并重启，补发的必须是 `question`、任务落 `waiting_answer`、`handoff show` 里有一张可回答的提问工单，`reply --answer` 之后模型真的续接。

**这条验的是 spec §3.3 的核心断言**：以提问收尾的回合不能被合成假的「做完了」。

- [ ] **Step 6: 验中途断连**

不杀 agentd，改为让 SSE 断开（在旁挂实例上 `kill -STOP` opencode serve 进程数秒再 `kill -CONT`，或用防火墙短暂阻断该端口）。日志里应出现 `trigger=reconnect` 的对账。若这条在真机上难以稳定制造，如实记录「未能制造出该现场」而**不要**改成一条弱验证冒充。

- [ ] **Step 7: 验 --force**

造一个对账判「仍在忙」的现场（任务真的在跑），执行 `handoff resume <task> --force`：任务落 `waiting_review`，事件流里有那条写明「人工强制收口、未经 executor 确认」的事件，且 **executor 会话仍然活着**（`handoff attach` 或 `ps` 可证），这正是它与 `stop` 的区别。

- [ ] **Step 8: 回填 spec 与 backlog**

把 §7.1 剩下的那条待验项（水位用消息 id 还是时间戳）按实现结论改写为已定；把 §8.3 的四条真机项改写为实测证据（带真实 task id、提交 hash、日志原文）。

backlog B38 行：状态 `📋 specced` → `🔨 doing`（若尚未改）→ 完成后按证据填 `✅ done(已验)` 或 `✅ done(未验)`。**未做到的项如实写「未覆盖」**，照 B36 那行的写法。

- [ ] **Step 9: 提交**

```bash
git add docs/superpowers/specs/2026-08-10-handoff-reconnect-reconciliation-design.md docs/superpowers/backlog.md
git commit -m "docs(b38): 回填真机验收证据与水位形态结论"
```

---

## Task 8: grok / codex 探针文档

**Files:**
- Create: `docs/superpowers/plans/2026-08-10-b38-grok-codex-probe.md`

**Interfaces:**
- Consumes: Task 3 确立的 `Reconcile` 契约与 opencode 的实现形态（作为参照答案）
- Produces: 探针文档，供 grok/codex 对账的第二份计划使用

- [ ] **Step 1: 写探针文档骨架**

按 spec §7.2 / §7.3 的问题清单建文档，每个问题写明：**怎么问**（具体命令或协议调用）、**什么算答上了**（判据）、**答案是什么**（留空待填）。

grok 的四个问题：
1. `session/load` 是否重放历史 `session/update`？判据：热重连后 `acpHandler` 是否收到载入前已发生的更新
2. **若重放，现有热重连路径是不是已经在产生重复事件？**（这是独立于 B38 的既有正确性问题，必须一并答）
3. ACP 有无「查会话历史 / 当前状态」的调用
4. 悬而未决的 `session/request_permission` 断连后是否可查、会不会自行重发

codex 的三个问题：
1. app-server 有无列 thread items 的方法
2. rollout 落在 `~/.codex/sessions/**`，能否直接读盘取回最后一条 assistant 消息与其完结状态
3. 悬而未决的 `requestApproval` 断连后是否可查

每个问题额外记：**该协议的水位该用什么载体**（对应 opencode 的消息 id）。

- [ ] **Step 2: 在 devbox 上真跑**

按文档里写好的命令逐条执行，抓真实报文。**不接受按协议文档或 schema 名字推断的答案**——B28 的 spike 一次推翻四处推断，本仓库对此有明确教训。

抓到的报文脱敏后随文档存档。

- [ ] **Step 3: 填结论并标注可行性**

每个 adapter 给一个明确结论：**能实现对账** / **不能实现对账（原因）**。不能实现的，在 spec §7.4 与 backlog 里如实记录原因——照 opencode 权限那条的写法（那条就是「已实证不可行」的范本）。

- [ ] **Step 4: 提交**

```bash
git add docs/superpowers/plans/2026-08-10-b38-grok-codex-probe.md
git commit -m "docs(b38): grok/codex 对账能力探针结论"
```

---

## 自审记录

**1. Spec 覆盖**

| Spec 章节 | 落在哪个 Task |
|---|---|
| §1.5 目标 1（自动对账） | Task 3 + Task 4 |
| §1.5 目标 2（补发不新增语义） | Task 3 Step 6（复用 `turn.ParseTrailer`）、§9 不加标记 |
| §1.5 目标 3（权限尽可能捧回） | Task 3 文件头注释记录 opencode 恒为 0；grok/codex 见 Task 8 |
| §1.5 目标 4（人工出口） | Task 5 + Task 6 |
| §2.2 不变量 | Task 1 Step 3 注释、Task 2 `LastAssistantMessage` 注释 |
| §3.1 接口 | Task 3 Step 3（数据）+ Task 5 Step 3（接口，按既有约定放 manager） |
| §3.2 水位 | Task 1 + Task 3 Step 5（正常路径前进）+ Task 4 Step 4（fresh 清零） |
| §3.3 分类不新增语义 | Task 3 Step 6 `classifyReconciled` + Step 1 的核心断言测试 |
| §4 三个触发点 | Task 4（前两个）+ Task 5/6（手动） |
| §5 扩展 resume | Task 5 + Task 6 |
| §6.1 幂等 | Task 3 Step 1 断言 2 + Task 7 Step 4 真机 |
| §6.2 竞态 | Task 3 Step 6（`turnMu` 下串行）+ Step 8（`-race`） |
| §6.3 失败语义 | Task 4 Step 3（不冒泡）+ Task 5 Step 4（不返回错误） |
| §7 探针 | Task 8 |
| §8 测试 | Task 3 Step 1（§8.1 的 1-5）、Task 5 Step 1（§8.2 的 7-8）、Task 7（§8.3） |
| §9 可观测性 | 每个 Task 的「加日志」step |

**缺口**：spec §8.1 断言 6（权限重新上报后不出第二张工单）在本计划里**无对应 task**——opencode 已实证捧不回权限（§7.1），该断言对本期唯一的实现无从验证。它随 grok/codex 的实现一起落地，Task 8 的探针会先回答那两个协议能否做到。此处如实记录而不是硬凑一个测试。

**2. 占位符扫描**：无 TBD/TODO；每个代码 step 都有可直接粘贴的代码；三处「执行者须先核对既有符号」是**明确的核对动作**（附了具体 `grep` 命令与判据），不是「自行发挥」。

**3. 类型一致性**：`ReconcileOutcome` 四字段在 Task 3/4/5 中名字一致；`RecoverStuck(taskID string, force bool)` 在 Task 5/6 一致；`LastAssistantMessage` 返回 `*SessionMessage` 在 Task 2/3 一致；`procInfo.LastTurnMsgID` 在 Task 1/3/4 一致；`reconcileAfterRecovery(ctx, taskID, trigger)` 在 Task 4 内部一致。
