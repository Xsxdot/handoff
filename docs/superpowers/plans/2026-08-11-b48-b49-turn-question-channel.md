# 回合终结解析与 opencode 提问通路（B48 + B49）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `turn.ParseTrailer` 认得出与正文混排的协议 JSON，并把 opencode 早就存在、handoff 从没订阅的 `question` 协议接进 adapter，消灭原生 `question` 工具导致的任务死锁。

**Architecture:** 两块互不依赖。B48 是 `internal/executor/turn/protocol.go` 里一个纯函数的两级提取改造（主路径放宽末行、回退保留旧规则），四个 adapter 共用。B49 在 `internal/executor/opencode/` 内照抄已经在生产运行的 `permission.asked` 路径：SSE 事件入口 → 工单渲染 → `Send` 分流应答 → 回合级去重 → 生命周期兜底。

**Tech Stack:** Go 1.26，标准库 `encoding/json` / `net/http` / `net/http/httptest`，项目自有 `slog` 封装（`a.log` / `a.log()`）。无新增依赖。

## Global Constraints

- **Spec 是唯一真相源**：[2026-08-11-turn-termination-and-question-channel-design.md](../specs/2026-08-11-turn-termination-and-question-channel-design.md)。与本计划冲突时以 spec 为准，并停下来报告，不要自行取舍。
- **日志一律用项目的结构化 logger**：adapter 侧 `a.log.Info/Warn/Error/Debug`，API 侧 `a.log().Info/...`。**禁止 `fmt.Printf` / `println`**。
- **每条日志带 `"task", taskID`**（API 层带 `"path"` / `"session"`），否则多任务并发时日志无法关联。
- **注释写中文，解释「为什么」不解释「做了什么」**。新增文件写文件头注释（职责 + 边界），导出函数写 doc 注释（参数/返回/注意）。
- **锁序固定 `turnMu` → `sessMu`，不得反向**；持 `turnMu` 时**不得**做网络 I/O（见 `runState` 的注释）。
- **`emit` 不回取 `turnMu`**，因此持 `turnMu` 调 `emit` 是安全的（既有约定，照做即可）。
- 每个 task 结束前跑 `go build ./... && go vet ./... && gofmt -l .`（`gofmt -l .` 必须无输出）。
- 每个 task 独立提交，提交信息用中文，结尾带 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

---

### Task 1: B48 — ParseTrailer 两级提取

**Files:**
- Modify: `internal/executor/turn/protocol.go:77-122`（`ParseTrailer` 的 doc 与实现）
- Test: `internal/executor/turn/protocol_test.go:22`（`TestParseTrailer` 的 cases 表）

**Interfaces:**
- Consumes: 无（本 task 是起点）
- Produces: `turn.ParseTrailer(text string) (kind string, t Trailer)` 签名**不变**，行为放宽。四个 adapter 已在调用它，无需改调用方。

**关于日志的刻意豁免（不要"修正"它）：** `ParseTrailer` 的 doc 明写「纯函数：不打日志，由调用方记录提取结果」。这是既有设计——四个 adapter 各自在自己的上下文里记录分类结果（如 opencode 的 `"回合未输出协议 trailer，走 git 兜底"` 带 `turn_tail`）。**本 task 不要往 turn 包里加 logger**，那会破坏它「不认识任何具体 executor」的边界。Step 5 改为验证调用方一侧的日志已经够用。

- [ ] **Step 1: 写失败测试**

在 `internal/executor/turn/protocol_test.go` 的 `TestParseTrailer` cases 表**末尾追加**这 6 条（保留现有 7 条不动，它们是回退路径的回归保护）：

```go
		{"末行前缀正文 + finish（B48 现场）",
			`g.{"branch":"handoff/T1","commit":"abc123","summary":"done"}`, "finish", "abc123"},
		{"末行后缀正文 + ask", `{"ask":"用哪个库？"} 好的`, "ask", "用哪个库？"},
		{"末行前后都有正文", `前缀 {"ask":"问题"} 后缀`, "ask", "问题"},
		{"末行是正文时回退到更早的以 { 开头的行",
			"{\"ask\":\"更早的问题\"}\n收尾说明没有花括号", "ask", "更早的问题"},
		{"末行含 { 但不是合法 JSON", "见 {} 占位\n真的没有协议行", "none", ""},
		{"末行是合法 JSON 但无协议字段", `说明 {"foo":1}`, "none", ""},
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/turn/ -run TestParseTrailer -v`

Expected: FAIL。`末行前缀正文 + finish（B48 现场）` 报 `kind = "none"，期望 "finish"`；`末行后缀正文 + ask`、`末行前后都有正文`、`末行是合法 JSON 但无协议字段` 同样翻红（前两条报 none，最后一条**可能**已经通过——若它本来就过，保留即可，它是防回归的）。

**若 `末行是正文时回退到更早的以 { 开头的行` 也翻红，说明你改错了方向——它测的是现有行为，此刻应当是绿的。**

- [ ] **Step 3: 实现两级提取**

把 `ParseTrailer` 的实现整体替换为：

```go
func ParseTrailer(text string) (kind string, t Trailer) {
	lines := strings.Split(text, "\n")

	// 主路径：只在最后一个非空行上宽容提取（B48）。模型会把正文和协议 JSON
	// 写在同一行（真机现场 `g.{"branch":...}`），旧的「整行以 { 开头」判据
	// 认不出，于是判 none 走 git 兜底、用 git 实况顶掉模型自己报的结论。
	//
	// 为什么只放宽最后一行：放宽必然扩大误吞面——正文里复述协议格式的 JSON
	// 会被当成本回合结论，这是 grok adapter 已经踩过的坑。限制在末行与收尾
	// 纪律「作为本回合最后一行」对齐，正文中间写什么都不受影响。
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue // 末尾空行不算「最后一行」
		}
		if k, tr, ok := decodeProtocolJSON(line); ok {
			return k, tr
		}
		break // 只试最后一个非空行，不向前扫
	}

	// 回退：现有规则原样保留——取最后一个「以 { 开头」的行。模型写完 trailer
	// 又追加了一整行正文时，末行没有 {，靠这条兜住。
	var last string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			last = line
		}
	}
	if last == "" {
		return "none", t
	}
	if k, tr, ok := decodeProtocolJSON(strings.TrimSpace(last)); ok {
		return k, tr
	}
	return "none", t
}

// decodeProtocolJSON 从 line 中第一个 { 起解码一个 JSON 值，并按协议字段分类。
//
// 参数：line 为已 TrimSpace 的单行文本
//
// 返回：
//   - kind: "ask" | "finish"
//   - t: 解析出的协议数据
//   - ok: 是否解出了带协议字段的 JSON；false 时前两个返回值无意义
//
// 注意：
//   - 用 json.Decoder 而非 Unmarshal：Decode 读满第一个完整 JSON 值即停，
//     因此该值之后的正文（`{"ask":"q"} 好的`）不会让解析失败
//   - 宽容解码：不设 DisallowUnknownFields，模型多带字段时仍能提取已知字段
//   - 解出的 JSON 不含任何协议字段时返回 ok=false，由调用方继续往下判
func decodeProtocolJSON(line string) (kind string, t Trailer, ok bool) {
	i := strings.Index(line, "{")
	if i < 0 {
		return "", t, false
	}
	var payload struct {
		Question string `json:"ask"`
		Branch   string `json:"branch"`
		Commit   string `json:"commit"`
		Summary  string `json:"summary"`
	}
	if err := json.NewDecoder(strings.NewReader(line[i:])).Decode(&payload); err != nil {
		return "", t, false
	}
	t = Trailer{Question: payload.Question, Branch: payload.Branch,
		Commit: payload.Commit, Summary: payload.Summary}
	// ask 与 finish 协议互斥（模型按纪律一次只输出一种），问号优先判定
	switch {
	case t.Question != "":
		return "ask", t, true
	case t.Branch != "" || t.Commit != "" || t.Summary != "":
		return "finish", t, true
	default:
		return "", Trailer{}, false
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/turn/ -v`

Expected: PASS，13 个子用例全绿（原 7 + 新 6）。

- [ ] **Step 5: 更新 doc 注释，并核对调用方日志**

把 `ParseTrailer` 的 doc 注释头两段改为：

```go
// ParseTrailer 从回合末文本宽容提取协议 JSON（ask/finish）。
//
// 提取分两级：
//   - 主路径：最后一个非空行，从该行第一个 { 起解码一个 JSON 值（容忍前缀
//     与后缀正文）
//   - 回退：主路径无果时，取最后一个「以 { 开头」的行按整行解码（旧规则）
//
// 返回：
//   - kind: "ask"（附 Question）| "finish"（附 Branch/Commit/Summary）| "none"
//   - t: 解析出的协议数据；kind 为 "none" 时为零值
//
// 注意：
//   - 放宽只作用于最后一个非空行：正文中间复述协议 JSON 不会被误当成结论
//   - 找不到或 JSON 损坏时返回 "none"，绝不 panic（模型输出不可信，防御在边界上做）
//   - 纯函数：不打日志，由调用方记录提取结果
```

然后**只读不改**地确认调用方日志已足够定位问题：`grep -n "turn_tail" internal/executor/` 应命中四个 adapter 的兜底日志（形如 `a.log.Warn("回合未输出协议 trailer，走 git 兜底", ..., "turn_tail", turn.TailRunes(text, 120))`）。命中即可，**不要修改它们**——那条日志正是 B48 现场被发现的渠道。

- [ ] **Step 6: 全量校验并提交**

```bash
go build ./... && go vet ./... && gofmt -l .
go test ./internal/executor/turn/ ./internal/executor/opencode/ ./internal/executor/grok/ ./internal/executor/codex/ ./internal/executor/claudecode/
```

Expected: `gofmt -l .` 无输出；五个包全绿（四个 adapter 都要跑——`ParseTrailer` 是共用的，放宽后的行为必须不破坏任何一家的既有测试）。

```bash
git add internal/executor/turn/protocol.go internal/executor/turn/protocol_test.go
git commit -m "fix(turn): ParseTrailer 两级提取，认出与正文混排的协议 JSON

旧规则要求整行以 { 开头，模型把正文和协议 JSON 写在同一行时（真机现场
g.{\"branch\":...}）整行被跳过，判 none 走 git 兜底、用 git 实况顶掉模型
自己报的结论。改为主路径在最后一个非空行上从第一个 { 起用 Decoder 解一个
值（容忍前后缀正文），回退保留旧规则。

放宽只作用于末行：正文中间复述协议 JSON 不受影响，避免重演 grok 那次
推理流误吞。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: B49 — API 层：question 三个端点

**Files:**
- Modify: `internal/executor/opencode/api.go`（在 `RespondPermission` 之后追加）
- Test: `internal/executor/opencode/api_test.go`

**Interfaces:**
- Consumes: 既有的 `a.do(ctx, method, path string, body any) (*http.Response, error)`、`a.httpError(op string, resp *http.Response) error`、`a.log() *slog.Logger`
- Produces（后续 task 依赖这些**确切**名字与签名）：
  ```go
  type QuestionOption struct{ Label, Description string }
  type QuestionInfo struct {
      Question string
      Header   string
      Options  []QuestionOption
      Multiple bool
      Custom   bool
  }
  type PendingQuestion struct {
      ID        string
      SessionID string
      Questions []QuestionInfo
  }
  func (a *API) ReplyQuestion(ctx context.Context, requestID string, answers [][]string) error
  func (a *API) RejectQuestion(ctx context.Context, requestID string) error
  func (a *API) ListPendingQuestions(ctx context.Context) ([]PendingQuestion, error)
  var ErrCustomAnswerRejected = errors.New("opencode 拒绝了自定义答案")
  ```

**协议出处**（08-11 devbox 探针，opencode 1.18.16 的 `@opencode-ai/sdk` 生成类型）：`POST /question/{requestID}/reply` 请求体 `{"answers": [["label"]]}`（按问题顺序，每项是该问选中的 label 数组）；`POST /question/{requestID}/reject` 无请求体；`GET /question` 返回 `[{id, sessionID, questions:[{question, header, options:[{label, description}], multiple, custom}]}]`。

- [ ] **Step 1: 写失败测试**

在 `internal/executor/opencode/api_test.go` 末尾追加：

```go
func TestReplyQuestionPostsAnswers(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	api := NewAPI(srv.URL, "pw")
	if err := api.ReplyQuestion(context.Background(), "req_1", [][]string{{"照此实现"}}); err != nil {
		t.Fatalf("ReplyQuestion 返回错误: %v", err)
	}
	if gotPath != "/question/req_1/reply" {
		t.Errorf("path = %q，期望 /question/req_1/reply", gotPath)
	}
	if !strings.Contains(gotBody, `"answers":[["照此实现"]]`) {
		t.Errorf("body = %q，期望含 \"answers\":[[\"照此实现\"]]", gotBody)
	}
}

func TestReplyQuestion4xxMapsToCustomRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid answer"}`))
	}))
	defer srv.Close()

	api := NewAPI(srv.URL, "pw")
	err := api.ReplyQuestion(context.Background(), "req_1", [][]string{{"我自己写的答案"}})
	if !errors.Is(err, ErrCustomAnswerRejected) {
		t.Fatalf("err = %v，期望可 errors.Is 命中 ErrCustomAnswerRejected", err)
	}
}

func TestReplyQuestion5xxIsNotCustomRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	api := NewAPI(srv.URL, "pw")
	err := api.ReplyQuestion(context.Background(), "req_1", [][]string{{"x"}})
	if err == nil {
		t.Fatal("5xx 应当返回错误")
	}
	if errors.Is(err, ErrCustomAnswerRejected) {
		t.Fatal("5xx 是服务端故障，不能被当成「自定义答案不被接受」")
	}
}

func TestRejectQuestionPostsToRejectPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	api := NewAPI(srv.URL, "pw")
	if err := api.RejectQuestion(context.Background(), "req_9"); err != nil {
		t.Fatalf("RejectQuestion 返回错误: %v", err)
	}
	if gotPath != "/question/req_9/reject" {
		t.Errorf("path = %q，期望 /question/req_9/reject", gotPath)
	}
}

func TestListPendingQuestionsDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"req_1","sessionID":"ses_a","questions":[
			{"question":"选哪个？","header":"选型","multiple":true,"custom":true,
			 "options":[{"label":"A","description":"甲"},{"label":"B","description":"乙"}]}]}]`))
	}))
	defer srv.Close()

	api := NewAPI(srv.URL, "pw")
	got, err := api.ListPendingQuestions(context.Background())
	if err != nil {
		t.Fatalf("ListPendingQuestions 返回错误: %v", err)
	}
	if len(got) != 1 || got[0].ID != "req_1" || got[0].SessionID != "ses_a" {
		t.Fatalf("got = %+v，期望一条 req_1/ses_a", got)
	}
	q := got[0].Questions[0]
	if q.Header != "选型" || !q.Multiple || !q.Custom || len(q.Options) != 2 || q.Options[1].Label != "B" {
		t.Errorf("question 解码不完整: %+v", q)
	}
}
```

确认 `api_test.go` 的 import 块含 `errors`、`io`、`strings`、`net/http`、`net/http/httptest`、`context`，缺哪个补哪个。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run 'TestReplyQuestion|TestRejectQuestion|TestListPendingQuestions' -v`

Expected: 编译失败，`undefined: ErrCustomAnswerRejected` / `api.ReplyQuestion undefined` 等。

- [ ] **Step 3: 实现三个端点**

在 `api.go` 的 `RespondPermission` 之后追加。注意 `ErrCustomAnswerRejected` 与类型定义放在方法之前：

```go
// ErrCustomAnswerRejected 表示 opencode 拒绝了本次 reply 携带的答案——最可能的
// 原因是该问不接受自定义答案（服务端按选项 label 白名单校验）。
//
// 为什么要一个专门的哨兵：审核者填了一个不在选项里的答案时，调用方要把它
// 降级成「重问」而不是报一个语焉不详的 HTTP 错误。只有 4xx 归入本哨兵，
// 5xx 是服务端故障，与答案内容无关（见 ReplyQuestion）。
var ErrCustomAnswerRejected = errors.New("opencode 拒绝了自定义答案")

// QuestionOption 是一个问题的一个候选项。
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// QuestionInfo 是 opencode question 工具的单个问题。
//
// Multiple 为真表示该问可多选，Custom 为真表示该问接受选项之外的自定义答案。
type QuestionInfo struct {
	Question string           `json:"question"`
	Header   string           `json:"header"`
	Options  []QuestionOption `json:"options"`
	Multiple bool             `json:"multiple"`
	Custom   bool             `json:"custom"`
}

// PendingQuestion 是一条挂起的 question 请求（一次可含多道问题）。
type PendingQuestion struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionID"`
	Questions []QuestionInfo `json:"questions"`
}

// questionReplyRequest 是 POST /question/{id}/reply 的请求体。
type questionReplyRequest struct {
	// Answers 按问题顺序排列，每项是该问选中的 label 数组（多选时多元素）
	Answers [][]string `json:"answers"`
}

// ReplyQuestion 把审核者的答案回填给 opencode 的 question 工具，工具随即返回、
// 回合继续。
//
// 参数：
//   - requestID: question.asked 事件里的 properties.id
//   - answers: 按问题顺序排列，每项是该问选中的 label 数组
//
// 返回：
//   - 4xx 时返回可 errors.Is 命中 ErrCustomAnswerRejected 的错误（答案不被接受）
//   - 其余失败返回普通错误
func (a *API) ReplyQuestion(ctx context.Context, requestID string, answers [][]string) (err error) {
	if requestID == "" {
		return fmt.Errorf("应答提问：请求 id 为空")
	}
	start := time.Now()
	path := "/question/" + requestID + "/reply"
	a.log().Info("opencode 应答提问", "path", path, "request", requestID,
		"answer_count", len(answers))
	defer func() {
		if err != nil {
			a.log().Error("opencode 应答提问失败", "path", path, "request", requestID,
				"cause", err)
		} else {
			a.log().Info("opencode 提问应答完成", "path", path, "request", requestID,
				"elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodPost, path, questionReplyRequest{Answers: answers})
	if err != nil {
		return fmt.Errorf("应答提问: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		// 4xx：请求本身被拒。答案内容是这次请求里唯一由审核者决定的部分，
		// 因此归因到「答案不被接受」，由调用方降级重问。5xx 不能这样归因
		return fmt.Errorf("%w: %v", ErrCustomAnswerRejected, a.httpError("应答提问", resp))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return a.httpError("应答提问", resp)
	}
	return nil
}

// RejectQuestion 拒绝一条挂起的提问，解除 question 工具的阻塞。
//
// 参数：requestID 为 question.asked 事件里的 properties.id
//
// 注意：
//   - 用于「任务要停了但提问还挂着」的兜底解阻塞，不是审核者的正常答复通道
func (a *API) RejectQuestion(ctx context.Context, requestID string) (err error) {
	if requestID == "" {
		return fmt.Errorf("拒绝提问：请求 id 为空")
	}
	start := time.Now()
	path := "/question/" + requestID + "/reject"
	a.log().Info("opencode 拒绝提问", "path", path, "request", requestID)
	defer func() {
		if err != nil {
			a.log().Error("opencode 拒绝提问失败", "path", path, "request", requestID,
				"cause", err)
		} else {
			a.log().Info("opencode 提问已拒绝", "path", path, "request", requestID,
				"elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("拒绝提问: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return a.httpError("拒绝提问", resp)
	}
	return nil
}

// ListPendingQuestions 拉取当前全部挂起的提问请求（跨会话）。
//
// 返回：挂起请求列表；请求失败或解析失败时返回错误，列表为 nil
//
// 注意：
//   - 返回的是**全部会话**的挂起请求，调用方必须按 SessionID 过滤出自己的
//   - agentd 重启后重新发现挂起提问的唯一途径：SSE 无重放语义，重启窗口里
//     发生的 question.asked 永远收不到
func (a *API) ListPendingQuestions(ctx context.Context) (out []PendingQuestion, err error) {
	start := time.Now()
	const path = "/question"
	a.log().Info("opencode 查询挂起提问", "path", path)
	defer func() {
		if err != nil {
			a.log().Error("opencode 查询挂起提问失败", "path", path, "cause", err)
		} else {
			a.log().Info("opencode 挂起提问已取得", "path", path, "count", len(out),
				"elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("查询挂起提问请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, a.httpError("查询挂起提问", resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析挂起提问: %w", err)
	}
	return out, nil
}
```

确认 `api.go` 的 import 块含 `errors`，缺就补。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run 'TestReplyQuestion|TestRejectQuestion|TestListPendingQuestions' -v`

Expected: 5 个用例 PASS。

- [ ] **Step 5: 核对日志覆盖**

对照 `instrumenting-code` 的清单逐条确认（三个方法都要）：进入时 Info 带 `path`；外部调用失败 Error 带 `cause`；成功路径 Info 带 `elapsed_ms`（**不许静默成功**）；无 `fmt.Printf`。

Run: `grep -n "fmt.Printf\|println(" internal/executor/opencode/api.go` → 期望无输出。

- [ ] **Step 6: 全量校验并提交**

```bash
go build ./... && go vet ./... && gofmt -l .
go test ./internal/executor/opencode/
```

```bash
git add internal/executor/opencode/api.go internal/executor/opencode/api_test.go
git commit -m "feat(opencode): API 层接入 question 三端点（reply/reject/list）

opencode 1.18.16 本来就有与 permission 同构的 question 协议，08-11 devbox
探针从 @opencode-ai/sdk 生成类型确认了路径与载荷形态。本提交只加 API 层。

4xx 单独映射为 ErrCustomAnswerRejected：答案内容是该请求里唯一由审核者
决定的部分，归因到「答案不被接受」才能让调用方降级重问；5xx 是服务端
故障，不归入该哨兵。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: B49 — 事件入口与工单渲染

**Files:**
- Create: `internal/executor/opencode/question.go`（提问通路的全部纯逻辑：渲染 + 答复解析）
- Create: `internal/executor/opencode/question_test.go`
- Modify: `internal/executor/opencode/adapter.go`（`runState` 加字段、`newRun` 初始化、`taskScopedEvents` 加键、`mapEvent` 加 case、新增 `mapQuestionAsked`）

**为什么新开 `question.go` 而不是塞进 `adapter.go`**：`adapter.go` 已经 1600+ 行。渲染与答复解析是**纯函数**（不碰 runState、不发 HTTP、不打日志），单独成文件后可以被单测直接覆盖，也让 `adapter.go` 只留「事件怎么接、状态怎么转」。与 `taskenv.go` / `reap.go` 的既有切分方式一致。

**Interfaces:**
- Consumes: Task 2 的 `QuestionInfo` / `QuestionOption` / `PendingQuestion`
- Produces（Task 4、5、6 依赖）：
  ```go
  func renderQuestionTicket(qs []QuestionInfo) string
  func (a *Adapter) mapQuestionAsked(r *runState, props json.RawMessage)
  // runState 新增字段：
  //   pendingQuestionID string       // 当前挂起的 question 请求 id（空=无）
  //   pendingQuestions  []QuestionInfo // 该请求的问题结构（答复解析用）
  //   seenQuestionIDs   map[string]bool // 已上报过的 requestID（SSE 重放去重）
  //   askedViaTool      bool          // Task 5 使用
  ```

- [ ] **Step 1: 写失败测试（渲染）**

创建 `internal/executor/opencode/question_test.go`：

```go
package opencode

import "strings"
import "testing"

func TestRenderQuestionTicketSingleQuestion(t *testing.T) {
	got := renderQuestionTicket([]QuestionInfo{{
		Question: "回合边界判据用哪个信号？",
		Header:   "回合边界",
		Options: []QuestionOption{
			{Label: "照此实现", Description: "加 Finish 字段"},
			{Label: "空值按保守处理", Description: "宁可不补也不误判"},
		},
	}})
	for _, want := range []string{
		"回合边界判据用哪个信号？",
		"1.1 照此实现 — 加 Finish 字段",
		"1.2 空值按保守处理 — 宁可不补也不误判",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染结果缺 %q\n实际:\n%s", want, got)
		}
	}
}

func TestRenderQuestionTicketMarksMultipleAndCustom(t *testing.T) {
	got := renderQuestionTicket([]QuestionInfo{{
		Question: "选哪些？", Multiple: true, Custom: true,
		Options: []QuestionOption{{Label: "A", Description: "甲"}},
	}})
	if !strings.Contains(got, "可多选") {
		t.Errorf("multiple 未标注:\n%s", got)
	}
	if !strings.Contains(got, "可自定义") {
		t.Errorf("custom 未标注:\n%s", got)
	}
}

func TestRenderQuestionTicketNumbersAcrossQuestions(t *testing.T) {
	got := renderQuestionTicket([]QuestionInfo{
		{Question: "第一问", Options: []QuestionOption{{Label: "A"}}},
		{Question: "第二问", Options: []QuestionOption{{Label: "B"}, {Label: "C"}}},
	})
	for _, want := range []string{"问题 1", "1.1 A", "问题 2", "2.1 B", "2.2 C"} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染结果缺 %q\n实际:\n%s", want, got)
		}
	}
}

func TestRenderQuestionTicketNoOptionsStillReadable(t *testing.T) {
	got := renderQuestionTicket([]QuestionInfo{{Question: "开放问题，随便答"}})
	if !strings.Contains(got, "开放问题，随便答") {
		t.Errorf("无选项时问题正文丢失:\n%s", got)
	}
	if !strings.Contains(got, "直接作答") {
		t.Errorf("无选项时应提示直接作答:\n%s", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestRenderQuestionTicket -v`

Expected: 编译失败 `undefined: renderQuestionTicket`。

- [ ] **Step 3: 实现渲染**

创建 `internal/executor/opencode/question.go`：

```go
// Package opencode 的提问通路纯逻辑。
//
// 职责：
//   - renderQuestionTicket：把 opencode 的 question 请求渲染成审核者可读的工单文本
//   - parseQuestionAnswers：把审核者的自由文本答复折算回 opencode 要的 answers
//
// 边界：
//   - 全部是纯函数：不碰 runState、不发 HTTP、不打日志、不读时钟
//   - 不认识 SSE 事件结构（解析在 adapter.go 的 mapQuestionAsked 里做）
//   - 不做截断（由调用方用 turn.ClampQuestion 收口）
package opencode

import (
	"fmt"
	"strings"
)

// renderQuestionTicket 把一组问题渲染成一张工单的文本。
//
// 参数：qs 为 opencode question 请求里的问题数组（顺序即应答顺序）
//
// 返回：多段文本，每问一段，选项按 `<问号>.<选项号>` 编号
//
// 注意：
//   - 编号跨问连续编排（1.1/1.2/2.1），审核者据此作答，parseQuestionAnswers
//     按同一套编号回读——两者是同一契约的两半，改一个必须改另一个
//   - 无选项的问题不编号，提示直接作答：opencode 允许问题只有正文
func renderQuestionTicket(qs []QuestionInfo) string {
	var b strings.Builder
	b.WriteString("executor 需要你决策：\n")
	for i, q := range qs {
		fmt.Fprintf(&b, "\n问题 %d", i+1)
		if h := strings.TrimSpace(q.Header); h != "" {
			fmt.Fprintf(&b, "（%s）", h)
		}
		b.WriteString("：")
		b.WriteString(strings.TrimSpace(q.Question))
		b.WriteString("\n")
		if len(q.Options) == 0 {
			b.WriteString("  （无候选项，直接作答）\n")
			continue
		}
		for j, o := range q.Options {
			fmt.Fprintf(&b, "  %d.%d %s", i+1, j+1, o.Label)
			if d := strings.TrimSpace(o.Description); d != "" {
				b.WriteString(" — " + d)
			}
			b.WriteString("\n")
		}
		var marks []string
		if q.Multiple {
			marks = append(marks, "可多选（逗号分隔）")
		}
		if q.Custom {
			marks = append(marks, "可自定义（直接写答案）")
		}
		if len(marks) > 0 {
			b.WriteString("  [" + strings.Join(marks, "；") + "]\n")
		}
	}
	b.WriteString("\n用 handoff reply --answer 作答，填编号（如 1.2）或选项原文；" +
		"多问按顺序用分号分隔（如 \"1.2; 2.1\"）。")
	return b.String()
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run TestRenderQuestionTicket -v`

Expected: 4 个用例 PASS。

- [ ] **Step 5: 写失败测试（事件入口）**

在 `internal/executor/opencode/question_test.go` 末尾追加。**沿用本包既有的测试助手**（定义在 `reconcile_internal_test.go`，同包可直接用）：`newTestAdapter(t) *Adapter`、`a.newRun(taskID, taskDir, repoPath) *runState`（**它自己会把运行态注册进 `a.runs`**，因此之后可以直接 `a.Send(ctx, taskID, ...)` / `a.lookup(taskID)`）、`drainOne(r) (executor.AdapterEvent, bool)`（非阻塞取一条事件）。

```go
func TestMapQuestionAskedEmitsTicketAndKeepsTurn(t *testing.T) {
	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	// 回合里已经累积了文本：提问不结束回合，这份缓冲必须原样保留
	r.turnOrder = []string{"k1"}
	r.partSeen = map[string]string{"k1": "已经写了一些正文"}

	props := []byte(`{"id":"req_1","sessionID":"ses_a","questions":[
		{"question":"选哪个？","header":"选型",
		 "options":[{"label":"A","description":"甲"},{"label":"B","description":"乙"}]}]}`)
	a.mapQuestionAsked(r, props)

	ev, ok := drainOne(r)
	if !ok || ev.Type != "question" {
		t.Fatalf("事件 = %+v ok=%v，期望一条 question", ev, ok)
	}
	if !strings.Contains(ev.Text, "1.1 A") {
		t.Errorf("工单缺选项编号:\n%s", ev.Text)
	}
	if r.pendingQuestionID != "req_1" {
		t.Errorf("pendingQuestionID = %q，期望 req_1", r.pendingQuestionID)
	}
	// 回合未结束：缓冲与水位都不能动
	if len(r.turnOrder) != 1 || r.partSeen["k1"] != "已经写了一些正文" {
		t.Error("提问不结束回合，回合缓冲不得被清空")
	}
}

func TestMapQuestionAskedDedupesByRequestID(t *testing.T) {
	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	props := []byte(`{"id":"req_1","sessionID":"ses_a","questions":[{"question":"Q"}]}`)

	a.mapQuestionAsked(r, props)
	a.mapQuestionAsked(r, props) // SSE 重连重放同一事件

	if _, ok := drainOne(r); !ok {
		t.Fatal("第一次应当出单")
	}
	if ev, ok := drainOne(r); ok {
		t.Fatalf("重放必须去重，却又收到 %+v", ev)
	}
}

func TestMapQuestionAskedEmptyQuestionsStillWakesReviewer(t *testing.T) {
	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"

	a.mapQuestionAsked(r, []byte(`{"id":"req_1","sessionID":"ses_a","questions":[]}`))

	ev, ok := drainOne(r)
	if !ok || ev.Type != "question" {
		t.Fatalf("事件 = %+v ok=%v，期望 question（不得静默丢弃）", ev, ok)
	}
	if !strings.Contains(ev.Text, "req_1") {
		t.Errorf("降级文本应带请求 id 供 attach 排查:\n%s", ev.Text)
	}
}

func TestQuestionAskedIsTaskScoped(t *testing.T) {
	if !taskScopedEvents["question.asked"] {
		t.Error("question.asked 必须是任务级事件：它直接产出面向审核者的工单")
	}
}
```

- [ ] **Step 6: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run 'TestMapQuestionAsked|TestQuestionAskedIsTaskScoped' -v`

Expected: 编译失败 `r.pendingQuestionID undefined` / `a.mapQuestionAsked undefined`。

- [ ] **Step 7: 加 runState 字段并初始化**

在 `adapter.go` 的 `runState` 结构体里，紧跟 `permText`/`turnRejected` 那组字段之后插入：

```go
	// 提问通路状态（B49）。opencode 原生 question 工具会阻塞等人作答，而它的
	// 应答通道（question.asked → /question/{id}/reply）handoff 此前从没订阅，
	// 于是工具永远等不到应答、回合不结束、任务挂死到 stall 超时。
	//
	// pendingQuestionID / pendingQuestions: 当前挂起的请求及其问题结构。工具
	// 阻塞保证同一任务至多一个挂起请求，故用单值而非 map。Send 据此分流：
	// 有挂起请求就把审核者的答复打到 reply 端点，没有才发新 prompt。
	// seenQuestionIDs: 已上报过的 requestID。question 事件不像 permission 那样
	// 带幂等 id 给 manager 派生 ticket，SSE 重放的去重只能在本层做。
	pendingQuestionID string
	pendingQuestions  []QuestionInfo
	seenQuestionIDs   map[string]bool
```

在 `newRun` 的结构体字面量里，`permSession: map[string]string{},` 之后加一行：

```go
		seenQuestionIDs: map[string]bool{},
```

- [ ] **Step 8: 加事件路由**

在 `adapter.go` 的 `taskScopedEvents` map 里，`"permission.replied":   true,` 之后加两行：

```go
	"question.asked":       true,
	"question.replied":     true,
	"question.rejected":    true,
```

在 `mapEvent` 的 switch 里，`case ev.Type == "permission.replied":` 那一段之后插入：

```go
	case ev.Type == "question.asked":
		a.mapQuestionAsked(r, ev.Properties)
	case ev.Type == "question.replied", ev.Type == "question.rejected":
		// 应答回显：与 permission.replied 同因——把回显当成新提问，审核者的
		// 答复会被当作再次提问，流程死循环
		a.log.Debug("question 应答回显，忽略", "task", r.taskID, "type", ev.Type)
```

- [ ] **Step 9: 实现 mapQuestionAsked**

在 `adapter.go` 的 `mapPermissionAsked` 之后插入：

```go
// mapQuestionAsked 处理 question.asked：opencode 原生 question 工具的提问请求。
//
// 与 permission.asked 同构（同一条 SSE 流上的并排事件），但有一处根本差别：
// **提问不结束回合**。question 工具阻塞等应答，session 不会转 idle，答复之后
// 是同一个回合继续——因此本函数**不得** clearTurn / advanceWatermark，
// 清缓冲会丢掉该回合已累积的文本，推进水位会让后续对账错位。
//
// 参数：props 为事件的 properties 原文（{id, sessionID, questions[], tool{}}）
//
// 注意：
//   - 本函数在 turnMu 下执行（见 mapEvent 的 switch 契约），可安全读写回合状态；
//     不得在此做网络 I/O
//   - 按 requestID 去重：question 事件不带幂等 id 给 manager，SSE 重放的去重
//     只能在本层做
func (a *Adapter) mapQuestionAsked(r *runState, props json.RawMessage) {
	var qa struct {
		ID        string         `json:"id"`
		SessionID string         `json:"sessionID"`
		Questions []QuestionInfo `json:"questions"`
	}
	if err := json.Unmarshal(props, &qa); err != nil {
		a.log.Debug("question.asked 载荷解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	if qa.ID == "" {
		a.log.Debug("question.asked 事件缺 id，跳过", "task", r.taskID)
		return
	}
	if r.seenQuestionIDs[qa.ID] {
		a.log.Debug("question.asked 重复到达（SSE 重放），已忽略",
			"task", r.taskID, "request", qa.ID)
		return
	}
	r.seenQuestionIDs[qa.ID] = true

	text := renderQuestionTicket(qa.Questions)
	// 描述下限：questions 为空或全无正文时，渲染结果对审核者没有信息量。
	// 与 mapPermissionAsked 的空描述兜底同理——宁可给出「未提供内容 + 请求 id」，
	// 让他知道要去 handoff attach 里看现场，也不能静默丢弃（丢了就是死锁）
	if len(qa.Questions) == 0 {
		a.log.Warn("question.asked 无问题内容，按未说明提问交审核者",
			"task", r.taskID, "request", qa.ID)
		text = "opencode 提出了一个空提问（id " + qa.ID + "），请 handoff attach 查看现场后作答"
	}
	// 子会话标注（B52 同款）：子 agent 的提问与主 agent 的提问含义不同
	qSess := qa.SessionID
	if qSess == "" {
		qSess = r.session
	}
	if qSess != r.session {
		r.sessMu.RLock()
		title := r.childSessions[qSess]
		r.sessMu.RUnlock()
		if title != "" {
			text = "[子 agent: " + title + "] " + text
		} else {
			text = "[子 agent] " + text
		}
	}

	r.pendingQuestionID = qa.ID
	r.pendingQuestions = qa.Questions
	// 回合级去重标记（B49 §4.4）：本回合已通过工具问过，回合末的 trailer ask
	// 不再重复出单。Task 5 消费它
	r.askedViaTool = true

	a.log.Info("收到 executor 提问，转工单交审核者", "task", r.taskID,
		"request", qa.ID, "session", qSess, "is_child", qSess != r.session,
		"question_count", len(qa.Questions))
	a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
	// 注意：此处刻意不调 clearTurn / advanceWatermark / captureStartCommit——
	// 回合还在跑（见函数头注释）
}
```

**`r.askedViaTool = true` 依赖 Task 5 才会加的字段。** 本 task 先在 `runState` 里把它一并加上（紧跟 `seenQuestionIDs` 之后）：

```go
	// askedViaTool 是回合级取走式标记（B49 §4.4）：本回合已通过 question 工具
	// 问过审核者。mapIdle 判出 trailer ask 时取走它并抑制那张工单——否则同一
	// 回合会给审核者两张单（grok 那次 askedViaTool 踩过的同一个坑）
	askedViaTool bool
```

- [ ] **Step 10: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run 'TestMapQuestionAsked|TestQuestionAskedIsTaskScoped|TestRenderQuestionTicket' -v`

Expected: 8 个用例全 PASS。

- [ ] **Step 11: 核对日志与注释覆盖**

对照 `instrumenting-code` 清单：
- 进入关键操作有 Info（`"收到 executor 提问，转工单交审核者"` 带 request/session/question_count）✓
- 每条早退分支都有日志（载荷解析失败 Debug、缺 id Debug、重放 Debug、空问题 Warn）✓
- 新文件 `question.go` 有文件头注释（职责 + 边界）✓
- 导出/非导出函数有 doc 注释 ✓
- 「为什么不 clearTurn」这个最容易被后人"修好"的点有 why 注释 ✓

Run: `grep -n "fmt.Printf\|println(" internal/executor/opencode/question.go internal/executor/opencode/adapter.go` → 期望无输出。

- [ ] **Step 12: 全量校验并提交**

```bash
go build ./... && go vet ./... && gofmt -l .
go test ./internal/executor/opencode/
```

```bash
git add internal/executor/opencode/question.go internal/executor/opencode/question_test.go internal/executor/opencode/adapter.go
git commit -m "feat(opencode): 订阅 question.asked，把原生提问转成审核者工单

此前 question.asked 落进 mapEvent 的 default 分支被 Debug 静默丢掉，
taskScopedEvents 里也没有它——opencode 侧的 question 工具因此永远等不到
应答，回合不结束、无 idle、trailer 永不解析，任务挂死到 2h stall。

关键差别：提问不结束回合，故 mapQuestionAsked 刻意不 clearTurn、不
advanceWatermark——答复后是同一个回合继续。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: B49 — Send 分流与答复解析

**Files:**
- Modify: `internal/executor/opencode/question.go`（追加 `parseQuestionAnswers`）
- Modify: `internal/executor/opencode/question_test.go`
- Modify: `internal/executor/opencode/adapter.go:429-448`（`Send`）

**Interfaces:**
- Consumes: Task 2 的 `ReplyQuestion` / `ErrCustomAnswerRejected`；Task 3 的 `renderQuestionTicket` / `pendingQuestionID` / `pendingQuestions`
- Produces:
  ```go
  func parseQuestionAnswers(qs []QuestionInfo, reply string) (answers [][]string, err error)
  ```
  `err` 非 nil 表示答复无法折算，调用方须重发工单而不是猜。

- [ ] **Step 1: 写失败测试**

在 `question_test.go` 末尾追加：

```go
func TestParseQuestionAnswersByNumber(t *testing.T) {
	qs := []QuestionInfo{{Options: []QuestionOption{{Label: "甲"}, {Label: "乙"}}}}
	got, err := parseQuestionAnswers(qs, "1.2")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got) != 1 || len(got[0]) != 1 || got[0][0] != "乙" {
		t.Fatalf("got = %v，期望 [[乙]]", got)
	}
}

func TestParseQuestionAnswersSingleQuestionBareNumber(t *testing.T) {
	qs := []QuestionInfo{{Options: []QuestionOption{{Label: "甲"}, {Label: "乙"}}}}
	got, err := parseQuestionAnswers(qs, "2")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got[0][0] != "乙" {
		t.Fatalf("got = %v，期望 [[乙]]（单问允许省略问号）", got)
	}
}

func TestParseQuestionAnswersByLabel(t *testing.T) {
	qs := []QuestionInfo{{Options: []QuestionOption{{Label: "照此实现"}, {Label: "保守处理"}}}}
	got, err := parseQuestionAnswers(qs, "  保守处理 ")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got[0][0] != "保守处理" {
		t.Fatalf("got = %v，期望 [[保守处理]]", got)
	}
}

func TestParseQuestionAnswersCustomPassthrough(t *testing.T) {
	qs := []QuestionInfo{{Custom: true, Options: []QuestionOption{{Label: "甲"}}}}
	got, err := parseQuestionAnswers(qs, "我要第三种做法")
	if err != nil {
		t.Fatalf("custom=true 应当透传自由文本: %v", err)
	}
	if got[0][0] != "我要第三种做法" {
		t.Fatalf("got = %v，期望原文透传", got)
	}
}

func TestParseQuestionAnswersRejectsUnmatchedWhenNoCustom(t *testing.T) {
	qs := []QuestionInfo{{Options: []QuestionOption{{Label: "甲"}}}}
	if _, err := parseQuestionAnswers(qs, "不存在的答案"); err == nil {
		t.Fatal("custom=false 且不匹配时必须报错重问，不许猜")
	}
}

func TestParseQuestionAnswersMultiSelect(t *testing.T) {
	qs := []QuestionInfo{{Multiple: true,
		Options: []QuestionOption{{Label: "甲"}, {Label: "乙"}, {Label: "丙"}}}}
	got, err := parseQuestionAnswers(qs, "1.1, 1.3")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got[0]) != 2 || got[0][0] != "甲" || got[0][1] != "丙" {
		t.Fatalf("got = %v，期望 [[甲 丙]]", got)
	}
}

func TestParseQuestionAnswersMultiQuestion(t *testing.T) {
	qs := []QuestionInfo{
		{Options: []QuestionOption{{Label: "甲"}, {Label: "乙"}}},
		{Options: []QuestionOption{{Label: "A"}, {Label: "B"}}},
	}
	got, err := parseQuestionAnswers(qs, "1.2; 2.1")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got) != 2 || got[0][0] != "乙" || got[1][0] != "A" {
		t.Fatalf("got = %v，期望 [[乙] [A]]", got)
	}
}

func TestParseQuestionAnswersCountMismatchRejected(t *testing.T) {
	qs := []QuestionInfo{
		{Options: []QuestionOption{{Label: "甲"}}},
		{Options: []QuestionOption{{Label: "A"}}},
	}
	if _, err := parseQuestionAnswers(qs, "1.1"); err == nil {
		t.Fatal("两问只给一答必须报错重问")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestParseQuestionAnswers -v`

Expected: 编译失败 `undefined: parseQuestionAnswers`。

- [ ] **Step 3: 实现答复解析**

在 `question.go` 末尾追加（并把 import 补上 `strconv`）：

```go
// parseQuestionAnswers 把审核者的自由文本答复折算成 opencode 要的 answers。
//
// 参数：
//   - qs: 本次请求的问题数组（顺序即 answers 的顺序）
//   - reply: 审核者 `handoff reply --answer` 的原文
//
// 返回：
//   - answers: 按问题顺序排列，每项是该问选中的 label 数组
//   - err: 无法折算（答数不匹配 / 某问答不上且不接受自定义）；此时调用方
//     必须重发工单，**不得**猜一个最接近的选项
//
// 注意：
//   - 分级匹配：编号 `问.选`（单问时允许裸选项号）→ label 原文（TrimSpace +
//     大小写归一后精确匹配）→ 该问 Custom 时原文透传
//   - 多问用分号分隔，多选用逗号分隔；两级分隔符不重叠，故可先分号后逗号
//   - 猜错一个选项的代价是模型按错误前提继续干活，重问的代价只是审核者多按
//     一次——错误方向必须选后者（与 B6「误升级好过漏放行」同一取舍）
func parseQuestionAnswers(qs []QuestionInfo, reply string) ([][]string, error) {
	if len(qs) == 0 {
		return nil, fmt.Errorf("本次请求没有问题，无法折算答复")
	}
	segs := splitAnswerSegments(reply, len(qs))
	if len(segs) != len(qs) {
		return nil, fmt.Errorf("有 %d 道问题但只解析出 %d 段答复，请按 \"1.2; 2.1\" 的形式按顺序作答",
			len(qs), len(segs))
	}
	answers := make([][]string, len(qs))
	for i, q := range qs {
		seg := strings.TrimSpace(segs[i])
		if seg == "" {
			return nil, fmt.Errorf("问题 %d 没有对应的答复", i+1)
		}
		var picked []string
		tokens := []string{seg}
		if q.Multiple {
			tokens = splitAndTrim(seg, ",")
		}
		for _, tok := range tokens {
			label, ok := matchOption(q, i, tok)
			if !ok {
				if !q.Custom {
					return nil, fmt.Errorf("问题 %d 的答复 %q 既不是编号也不是选项原文，且该问不接受自定义答案；请填编号（如 %d.1）或选项原文",
						i+1, tok, i+1)
				}
				label = tok // custom：原文透传，服务端若拒绝由调用方降级重问
			}
			picked = append(picked, label)
		}
		answers[i] = picked
	}
	return answers, nil
}

// splitAnswerSegments 把答复切成「每问一段」。
//
// 单问时整段就是一段（分号可能是答案本身的一部分，不能切）；多问时按分号切。
func splitAnswerSegments(reply string, questionCount int) []string {
	if questionCount == 1 {
		return []string{strings.TrimSpace(reply)}
	}
	return splitAndTrim(reply, ";")
}

// splitAndTrim 按 sep 切分并去掉每段首尾空白，丢弃空段。
//
// 中文全角分隔符（；、）一并接受：审核者在中文输入法下很容易打出全角，
// 因此而重问是纯粹的摩擦。
func splitAndTrim(s, sep string) []string {
	switch sep {
	case ";":
		s = strings.ReplaceAll(s, "；", ";")
	case ",":
		s = strings.ReplaceAll(s, "，", ",")
	}
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// matchOption 把一个答复 token 折算成选项 label。
//
// 参数：
//   - q: 该问
//   - idx: 该问的下标（0 起），用于校验 `问.选` 里的问号
//   - tok: 已 TrimSpace 的单个 token
//
// 返回：命中的 label 与是否命中；未命中时由调用方按 Custom 决定透传或重问
func matchOption(q QuestionInfo, idx int, tok string) (string, bool) {
	// 一级：编号。`问.选`（1.2）或单问时的裸选项号（2）
	qn, on, ok := parseOptionNumber(tok)
	if ok && (qn == 0 || qn == idx+1) && on >= 1 && on <= len(q.Options) {
		return q.Options[on-1].Label, true
	}
	// 二级：label 原文，归一化后精确匹配。归一化只做 TrimSpace + 大小写折叠，
	// 不做模糊匹配——模糊匹配会在选项相近时静默选错，那正是重问要防的
	norm := strings.ToLower(strings.TrimSpace(tok))
	for _, o := range q.Options {
		if strings.ToLower(strings.TrimSpace(o.Label)) == norm {
			return o.Label, true
		}
	}
	return "", false
}

// parseOptionNumber 解析 "1.2"（问号.选项号）或 "2"（裸选项号，问号返回 0）。
//
// 返回 ok=false 表示 tok 不是编号形态（含小数点但两侧非数字、或整体非数字）。
func parseOptionNumber(tok string) (questionNo, optionNo int, ok bool) {
	if i := strings.Index(tok, "."); i >= 0 {
		q, err1 := strconv.Atoi(strings.TrimSpace(tok[:i]))
		o, err2 := strconv.Atoi(strings.TrimSpace(tok[i+1:]))
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return q, o, true
	}
	o, err := strconv.Atoi(tok)
	if err != nil {
		return 0, 0, false
	}
	return 0, o, true
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run TestParseQuestionAnswers -v`

Expected: 8 个用例 PASS。

- [ ] **Step 5: 写 Send 分流的失败测试**

在 `question_test.go` 末尾追加。用与 Step 1 同一套助手，另加 `r.api = NewAPI(srv.URL, "pw")` 把 API 指向假服务端（`NewAPI(baseURL, password string) *API`，见 `api.go:100`）。

```go
func TestSendRoutesToQuestionReplyWhenPending(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")
	r.pendingQuestionID = "req_1"
	r.pendingQuestions = []QuestionInfo{{Options: []QuestionOption{{Label: "甲"}, {Label: "乙"}}}}

	if err := a.Send(context.Background(), "task-1", "1.2"); err != nil {
		t.Fatalf("Send 返回错误: %v", err)
	}
	if gotPath != "/question/req_1/reply" {
		t.Fatalf("path = %q，期望打到 reply 端点而不是 prompt", gotPath)
	}
	if !strings.Contains(gotBody, "乙") {
		t.Errorf("body = %q，期望含折算后的 label 乙", gotBody)
	}
	if r.pendingQuestionID != "" {
		t.Error("应答成功后必须清掉挂起请求")
	}
}

func TestSendFallsBackToPromptWhenNoPendingQuestion(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")

	if err := a.Send(context.Background(), "task-1", "继续干"); err != nil {
		t.Fatalf("Send 返回错误: %v", err)
	}
	if !strings.Contains(gotPath, "/session/ses_a/") {
		t.Fatalf("path = %q，期望走 session prompt 端点而非 question", gotPath)
	}
	if strings.Contains(gotPath, "/question/") {
		t.Fatalf("无挂起提问时不得打到 question 端点，实际 %q", gotPath)
	}
}

func TestSendUnparsableAnswerRepromptsAndKeepsPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("答复折算不出来时不应触达服务端，却收到了 %s", r.URL.Path)
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")
	r.pendingQuestionID = "req_1"
	r.pendingQuestions = []QuestionInfo{{Options: []QuestionOption{{Label: "甲"}}}}

	if err := a.Send(context.Background(), "task-1", "驴唇不对马嘴"); err != nil {
		t.Fatalf("重问路径不应返回错误（错误要以工单形式给审核者）: %v", err)
	}
	ev, ok := drainOne(r)
	if !ok || ev.Type != "question" {
		t.Fatalf("事件 = %+v ok=%v，期望重发 question 工单", ev, ok)
	}
	if r.pendingQuestionID != "req_1" {
		t.Error("重问期间挂起请求必须保留，否则下一次答复就无处可投")
	}
}

func TestSendCustomRejectedByServerRepromptsAndKeepsPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")
	r.pendingQuestionID = "req_1"
	r.pendingQuestions = []QuestionInfo{{Custom: true, Options: []QuestionOption{{Label: "甲"}}}}

	if err := a.Send(context.Background(), "task-1", "我自己想的答案"); err != nil {
		t.Fatalf("服务端拒绝自定义答案应降级重问而不是报错: %v", err)
	}
	ev, ok := drainOne(r)
	if !ok || ev.Type != "question" || !strings.Contains(ev.Text, "不接受自定义答案") {
		t.Fatalf("期望重发工单并说明原因，实际: %+v ok=%v", ev, ok)
	}
	if r.pendingQuestionID != "req_1" {
		t.Error("挂起请求必须保留")
	}
}
```

- [ ] **Step 6: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestSend -v`

Expected: `TestSendRoutesToQuestionReplyWhenPending` 报 path 是 `/session/ses_a/prompt_async`（现有行为，未分流）；后两条报没有事件。

- [ ] **Step 7: 改造 Send**

把 `adapter.go` 的 `Send` 方法体在 `a.log.Info("adapter 收到续接指令", ...)` 与 `defer` 之后、`return r.api.PromptAsync(...)` 之前，插入分流逻辑，并把 doc 注释补一段。改完后的尾部形如：

```go
	// 提问分流（B49）：有挂起的 question 请求时，审核者的答复必须打到 reply
	// 端点而不是发新 prompt——question 工具阻塞时回合还在跑，再发 prompt 会
	// 开出第二个回合，而阻塞的工具依然等不到应答
	if reqID, qs := r.takePendingQuestionSnapshot(); reqID != "" {
		return a.replyPendingQuestion(ctx, r, reqID, qs, text)
	}
	return r.api.PromptAsync(ctx, r.session, text)
```

并在 `Send` 之后新增两个辅助：

```go
// takePendingQuestionSnapshot 读一份挂起提问的快照（不清除）。
//
// 返回：请求 id 与问题结构；无挂起请求时 id 为空串
//
// 为什么只读不清：答复可能折算失败或被服务端拒绝，那两条路都要重发工单并
// **保留**挂起请求——提前清掉，审核者的下一次答复就无处可投，任务重新死锁。
// 清除只发生在应答成功之后（见 replyPendingQuestion）。
func (r *runState) takePendingQuestionSnapshot() (string, []QuestionInfo) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	return r.pendingQuestionID, r.pendingQuestions
}

// clearPendingQuestion 清除挂起提问（应答成功后调用）。
func (r *runState) clearPendingQuestion() {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.pendingQuestionID = ""
	r.pendingQuestions = nil
}

// replyPendingQuestion 把审核者的答复折算并回填给 opencode 的 question 工具。
//
// 参数：
//   - reqID/qs: 挂起请求的 id 与问题结构
//   - text: 审核者答复原文
//
// 返回：
//   - nil: 应答成功，或答复折算不了/被服务端拒绝而已重发工单（两者对审核者
//     都不是错误，是「再答一次」）
//   - 非 nil: 网络/服务端故障等真错误，冒泡给审核者终端
//
// 注意：
//   - 折算失败**不触达服务端**：猜一个最接近的选项会让模型按错误前提继续干活
func (a *Adapter) replyPendingQuestion(ctx context.Context, r *runState,
	reqID string, qs []QuestionInfo, text string) error {

	answers, perr := parseQuestionAnswers(qs, text)
	if perr != nil {
		a.log.Warn("答复无法折算成选项，重发工单请审核者再答",
			"task", r.taskID, "request", reqID, "cause", perr)
		a.emit(r, executor.AdapterEvent{Type: "question",
			Text: turn.ClampQuestion("上一次答复没能对上选项（" + perr.Error() + "）。\n\n" +
				renderQuestionTicket(qs))})
		return nil
	}
	a.log.Info("答复已折算，回填 executor 提问", "task", r.taskID,
		"request", reqID, "answers", answers)
	if err := r.api.ReplyQuestion(ctx, reqID, answers); err != nil {
		if errors.Is(err, ErrCustomAnswerRejected) {
			a.log.Warn("opencode 不接受该自定义答案，重发工单请审核者改填选项",
				"task", r.taskID, "request", reqID, "cause", err)
			a.emit(r, executor.AdapterEvent{Type: "question",
				Text: turn.ClampQuestion("opencode 不接受自定义答案，请改填编号或选项原文。\n\n" +
					renderQuestionTicket(qs))})
			return nil
		}
		return err
	}
	r.clearPendingQuestion()
	a.log.Info("提问已应答，回合继续", "task", r.taskID, "request", reqID)
	return nil
}
```

确认 `adapter.go` 的 import 含 `errors`，缺就补。

**Send 的 doc 注释追加一条 `注意`：**

```go
//   - 有挂起的 question 请求时不发 prompt，改把答复回填给该请求（B49）：
//     question 工具阻塞时回合并未结束，发 prompt 会开出第二个回合
```

- [ ] **Step 8: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run 'TestSend|TestParseQuestionAnswers' -v`

Expected: 12 个用例全 PASS。

- [ ] **Step 9: 核对日志覆盖**

对照 `instrumenting-code` 清单确认 `replyPendingQuestion` 三条出口都有日志且带 `request`：折算失败 Warn + cause、服务端拒绝 Warn + cause、成功 Info（`"提问已应答，回合继续"`——**成功路径不许静默**）。

- [ ] **Step 10: 全量校验并提交**

```bash
go build ./... && go vet ./... && gofmt -l .
go test ./internal/executor/opencode/
```

```bash
git add internal/executor/opencode/question.go internal/executor/opencode/question_test.go internal/executor/opencode/adapter.go
git commit -m "feat(opencode): Send 按挂起提问分流，答复分级折算回 opencode

有挂起 question 请求时把答复打到 reply 端点而不是发新 prompt——工具阻塞
时回合还在跑，发 prompt 会开出第二个回合而阻塞的工具依然等不到应答。

折算分级：编号 → label 原文 → Custom 时原文透传。折算不出来不触达服务端，
直接重发工单；服务端 4xx 拒绝自定义答案时同样降级重问。两条路都保留挂起
请求，否则审核者的下一次答复无处可投。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: B49 — 回合级去重

**Files:**
- Modify: `internal/executor/opencode/adapter.go`（`mapIdle` 的 `case "ask"` 分支、新增 `takeAskedViaTool`）
- Modify: `internal/executor/opencode/question_test.go`

**Interfaces:**
- Consumes: Task 3 加的 `runState.askedViaTool`（Task 3 已置位，本 task 只做消费）
- Produces: `func (r *runState) takeAskedViaTool() bool`

- [ ] **Step 1: 写失败测试**

在 `question_test.go` 末尾追加：

```go
func TestTakeAskedViaToolIsOneShot(t *testing.T) {
	r := &runState{askedViaTool: true}
	if !r.takeAskedViaTool() {
		t.Fatal("第一次取应当为 true")
	}
	if r.takeAskedViaTool() {
		t.Fatal("取走式标记第二次必须为 false，否则下一回合的真提问会被误抑制")
	}
}

func TestMapIdleSuppressesTrailerAskAfterToolQuestion(t *testing.T) {
	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.askedViaTool = true // 本回合已通过 question 工具问过
	r.turnOrder = []string{"k1"}
	r.partSeen = map[string]string{"k1": `我已经用工具问过了。
{"ask":"同一个问题的复述"}`}

	a.mapIdle(r, json.RawMessage(`{"type":"session.idle"}`))

	if ev, ok := drainOne(r); ok {
		t.Fatalf("工具已问过，回合末的 trailer ask 不应再出单，却收到 %+v", ev)
	}
	if r.askedViaTool {
		t.Error("标记必须在回合终结时被取走")
	}
}

func TestMapIdleEmitsTrailerAskWhenToolNotUsed(t *testing.T) {
	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.turnOrder = []string{"k1"}
	r.partSeen = map[string]string{"k1": `{"ask":"没用工具时的正常提问"}`}

	a.mapIdle(r, json.RawMessage(`{"type":"session.idle"}`))

	ev, ok := drainOne(r)
	if !ok || ev.Type != "question" || !strings.Contains(ev.Text, "没用工具时的正常提问") {
		t.Fatalf("未用工具时 trailer ask 必须照常出单，实际: %+v ok=%v", ev, ok)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run 'TestTakeAskedViaTool|TestMapIdle.*Trailer' -v`

Expected: `TestTakeAskedViaToolIsOneShot` 编译失败 `undefined: takeAskedViaTool`；`TestMapIdleSuppressesTrailerAskAfterToolQuestion` 报收到了不该有的事件。

- [ ] **Step 3: 实现取走式标记与抑制**

在 `adapter.go` 的 `takeTurnRejected` 之后新增：

```go
// takeAskedViaTool 取走「本回合已通过 question 工具提问」的标记（读后即清）。
//
// 返回：本回合是否已通过工具问过审核者
//
// 为什么必须取走式：标记的生命周期是一个回合。常驻会让下一回合的真 trailer
// 提问被误抑制，任务停在 running 无人知晓——那正是 B49 要消灭的形态。
//
// 注意：调用方须已持 turnMu（mapIdle 的既有契约）
func (r *runState) takeAskedViaTool() bool {
	asked := r.askedViaTool
	r.askedViaTool = false
	return asked
}
```

把 `mapIdle` 的 `case "ask":` 分支改为：

```go
	case "ask":
		// 回合级去重（B49 §4.4）：本回合已通过 question 工具问过审核者时，
		// 回合末的 trailer ask 多半是同一个问题的复述——出第二张单会让审核者
		// 面对两份措辞不同的同一件事（grok 那次 askedViaTool 踩过的同一个坑）。
		// 兜底通道存在的目的是「保证回合不静默结束」，工具已经问过时该诉求
		// 已经满足
		if r.takeAskedViaTool() {
			a.log.Debug("本回合已通过 question 工具提问，抑制 trailer 提问工单",
				"task", r.taskID, "trailer_tail", turn.TailRunes(t.Question, 80))
			break
		}
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(t.Question)})
```

**同时把 `mapIdle` 里两条早退路径（被拒终止的空回合、零文本回合）各补一行 `r.takeAskedViaTool()`**，紧挨着它们已有的 `r.clearTurn()`：这两条路也是回合结束，标记必须随回合一起清掉，否则会漏到下一回合。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run 'TestTakeAskedViaTool|TestMapIdle' -v`

Expected: 全 PASS（含 `mapIdle` 的既有用例——它们保护着空回合两条早退路径的行为）。

- [ ] **Step 5: 核对注释**

确认三处 why 注释到位：`takeAskedViaTool` 为何取走式、`case "ask"` 为何抑制、两条早退路径为何也要清标记。**这三处都是「后人会觉得多余而删掉」的代码**，没有 why 注释就会在下一次重构里消失。

- [ ] **Step 6: 全量校验并提交**

```bash
go build ./... && go vet ./... && gofmt -l .
go test ./internal/executor/opencode/
```

```bash
git add internal/executor/opencode/adapter.go internal/executor/opencode/question_test.go
git commit -m "feat(opencode): 回合级去重，工具问过后抑制 trailer 提问工单

接通 question 工具后模型有两条提问通路，同一回合先调工具再输出
{\"ask\":...} 会给审核者两张措辞不同的同一件事的工单——grok 那次
askedViaTool 踩过的同一个坑。

标记取走式而非常驻：生命周期是一个回合，常驻会让下一回合的真提问被误
抑制。回合结束的三条路径（trailer 分类、被拒终止空回合、零文本回合）都
清标记。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: B49 — 生命周期兜底（Stop 解阻塞 + 重启重新发现）

**Files:**
- Modify: `internal/executor/opencode/adapter.go`（`Stop`）
- Modify: `internal/executor/opencode/resume.go`（`Resume` 尾部）
- Modify: `internal/executor/opencode/question_test.go`

**Interfaces:**
- Consumes: Task 2 的 `RejectQuestion` / `ListPendingQuestions`；Task 3 的 `pendingQuestionID` / `seenQuestionIDs` / `renderQuestionTicket`
- Produces: `func (a *Adapter) rediscoverPendingQuestions(ctx context.Context, taskID string)`

- [ ] **Step 1: 写失败测试**

在 `question_test.go` 末尾追加：

```go
func TestStopRejectsPendingQuestion(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")
	r.pendingQuestionID = "req_1"
	// handle 留 nil：Stop 的 kill 分支有 `if r.handle != nil` 守卫，
	// 本用例只关心「杀进程之前有没有先解阻塞」

	if err := a.Stop("task-1"); err != nil {
		t.Fatalf("Stop 返回错误: %v", err)
	}
	if gotPath != "/question/req_1/reject" {
		t.Fatalf("path = %q，期望 Stop 前先 reject 解阻塞", gotPath)
	}
}

func TestRediscoverPendingQuestionsFiltersBySession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":"req_other","sessionID":"ses_别人","questions":[{"question":"不是我的"}]},
			{"id":"req_mine","sessionID":"ses_a","questions":[{"question":"是我的"}]}]`))
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")

	a.rediscoverPendingQuestions(context.Background(), "task-1")

	ev, ok := drainOne(r)
	if !ok || !strings.Contains(ev.Text, "是我的") {
		t.Fatalf("补发的工单不对: %+v ok=%v", ev, ok)
	}
	if r.pendingQuestionID != "req_mine" {
		t.Errorf("pendingQuestionID = %q，期望 req_mine", r.pendingQuestionID)
	}
	if extra, ok := drainOne(r); ok {
		t.Fatalf("别的会话的提问不应补发，却收到 %+v", extra)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run 'TestStopRejectsPendingQuestion|TestRediscoverPendingQuestions' -v`

Expected: 编译失败 `undefined: rediscoverPendingQuestions`；`TestStopRejectsPendingQuestion` 报 path 为空。

- [ ] **Step 3: Stop 里先解阻塞**

在 `adapter.go` 的 `Stop` 方法体开头、`select { case <-r.stopCh: ... }` 那类既有守卫之后、真正 kill 之前，插入：

```go
	// 挂起的提问先解阻塞再杀进程（B49）：opencode 的 question 工具在等应答，
	// 直接杀掉会留下一条永远挂起的请求。失败只 Warn 不阻断——进程马上就没了，
	// 为一次解阻塞失败挡住 Stop 是本末倒置
	if reqID, _ := r.takePendingQuestionSnapshot(); reqID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), unaryTimeout)
		if rerr := r.api.RejectQuestion(ctx, reqID); rerr != nil {
			a.log.Warn("停止前拒绝挂起提问失败，该请求可能仍挂在 opencode 侧",
				"task", taskID, "request", reqID, "cause", rerr)
		} else {
			a.log.Info("停止前已拒绝挂起提问", "task", taskID, "request", reqID)
		}
		cancel()
		r.clearPendingQuestion()
	}
```

`unaryTimeout` 是本包既有常量（`api.go:63`，30s），直接用。

- [ ] **Step 4: 实现重启后重新发现**

在 `resume.go` 末尾新增：

```go
// rediscoverPendingQuestions 在恢复后重新发现本任务挂起的提问并补发工单。
//
// 参数：taskID 为已完成运行态重建的任务 id
//
// 为什么必须有这一步：SSE 没有重放语义，agentd 重启窗口里发生的
// question.asked 永远收不到。而 opencode 侧的 question 工具还在阻塞等应答——
// 不重新发现，任务就是个谁也叫不醒的孤儿（与 B18/B20/B24 同一条纪律：
// 重启后一切「executor 那边还等着人」的状态都必须被重新发现）。
//
// 注意：
//   - GET /question 返回全部会话的挂起请求，必须按本任务的会话 id 过滤
//   - 本函数在 goroutine 里跑，不阻塞 Resume 返回；失败只记日志（挂起提问
//     发现不了仍有 stall 看门狗兜底，不该让恢复本身失败）
func (a *Adapter) rediscoverPendingQuestions(ctx context.Context, taskID string) {
	r := a.lookup(taskID)
	if r == nil || r.api == nil {
		a.log.Debug("重新发现挂起提问跳过：该任务无运行态", "task", taskID)
		return
	}
	pending, err := r.api.ListPendingQuestions(ctx)
	if err != nil {
		a.log.Warn("重新发现挂起提问失败，若确有挂起提问将由 stall 看门狗兜底",
			"task", taskID, "cause", err)
		return
	}
	for _, p := range pending {
		if p.SessionID != r.session {
			continue // 别的任务/会话的提问
		}
		r.turnMu.Lock()
		if r.seenQuestionIDs[p.ID] {
			r.turnMu.Unlock()
			continue
		}
		r.seenQuestionIDs[p.ID] = true
		r.pendingQuestionID = p.ID
		r.pendingQuestions = p.Questions
		r.turnMu.Unlock()

		a.log.Info("恢复后发现挂起提问，补发工单", "task", taskID,
			"request", p.ID, "question_count", len(p.Questions))
		a.emit(r, executor.AdapterEvent{Type: "question",
			Text: turn.ClampQuestion(renderQuestionTicket(p.Questions))})
		return // 工具阻塞保证至多一个挂起请求，发现一个即可
	}
	a.log.Info("恢复后未发现本任务的挂起提问", "task", taskID, "total_pending", len(pending))
}
```

在 `Resume` 里，紧挨着既有的 `go a.reconcileAfterRecovery(...)` 之后加一行：

```go
		go a.rediscoverPendingQuestions(context.Background(), req.TaskID)
```

放在同一个 `if mode != executor.ResumeModeFresh {` 块内——fresh 是新会话，不可能有属于它的挂起提问。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run 'TestStopRejectsPendingQuestion|TestRediscoverPendingQuestions' -v`

Expected: 2 个用例 PASS。

- [ ] **Step 6: 核对日志覆盖**

`rediscoverPendingQuestions` 四条出口都要有日志：无运行态 Debug、查询失败 Warn + cause、发现并补发 Info、未发现 Info（**「没发现」也要记**——否则「任务卡住了，恢复时到底查没查过」无从对证）。

- [ ] **Step 7: 全量回归并提交**

```bash
go build ./... && go vet ./... && gofmt -l .
go test ./...
go test -race ./internal/executor/opencode/ ./internal/executor/turn/ ./internal/agentd/
```

Expected: 全绿。`-race` 必须跑 opencode 包——本次新增字段被订阅 goroutine、idle 定时器 goroutine、`Send` 调用方三处共访。

```bash
git add internal/executor/opencode/adapter.go internal/executor/opencode/resume.go internal/executor/opencode/question_test.go
git commit -m "feat(opencode): 提问的生命周期兜底——Stop 先解阻塞、重启后重新发现

Stop 前先 reject 挂起提问，否则直接杀进程会在 opencode 侧留下永远挂起的
请求；失败只 Warn 不阻断。

恢复后走 GET /question 重新发现本会话的挂起提问并补发工单：SSE 无重放
语义，重启窗口里的 question.asked 永远收不到，不重新发现任务就是谁也
叫不醒的孤儿（与 B18/B20/B24 同一条纪律）。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 7: 真机验收（devbox）

**Files:** 无代码改动；产出写进 backlog 的验收列与 spec §5.4 的结论回填。

**前置**：Task 1-6 全部合入，二进制已部署到 devbox。

- [ ] **Step 1: B48 真机验证**

派发一个任务，在 prompt 里要求模型**故意**把协议 JSON 与正文写在同一行收尾（如 `完成。{"branch":"...","commit":"...","summary":"..."}`）。

Expected: 任务正常判 finish；`grep "回合未输出协议 trailer" ~/.handoff/agentd.log` 在该任务时间窗内**无命中**（旧实现必然命中）。

- [ ] **Step 2: B49 主路径真机验证**

派发一个任务，prompt 里要求模型用它自己的 `question` 工具向用户提一个带选项的问题。

Expected 逐条记录：
1. 工单在秒级到达（不是 2h），文本含 `1.1` / `1.2` 编号与 description
2. `handoff reply --answer "1.2"` 后 `{"ok":true}`
3. 模型在**同一回合**续接——`serve.log` 里该 message 的 `completed` 由 null 变为时间戳，且**没有**新的 user 消息（有就是开了新回合，说明分流没生效）
4. 任务走完，`done` 归档干净

- [ ] **Step 3: 回填 custom 分支的实际结论**

看 Step 2 那轮里 `custom` 字段的实际取值与走的分支：若审核者填过自定义答案，记录服务端接受还是 4xx。把结论回填进 spec §5.4（替换「待 e2e 揭示」那句）。

- [ ] **Step 4: 重启恢复验证**

Step 2 的工单到达后**先不答**，重启 agentd。

Expected: 启动日志出现「恢复后发现挂起提问，补发工单」，工单再次到达；此时再 `reply --answer` 仍能应答成功。

- [ ] **Step 5: 记录验收证据**

把 Step 1-4 的真实命令与输出写进 backlog 的 B48/B49 验收列，状态转 `✅ done(已验)`。**任一步没跑到就记 `done(未验)` 并写明缺哪条**——不许用「应该没问题」代替证据。

---

## Self-Review

**Spec 覆盖核对：**

| Spec 章节 | 对应 Task |
|---|---|
| §3.2 两级提取 | Task 1 Step 3 |
| §3.3 只放宽末行 | Task 1 Step 3（注释）+ Step 1 的第 4 条用例 |
| §4.1 事件入口 | Task 3 Step 8 |
| §4.2 mapQuestionAsked（去重/渲染/不清缓冲/描述下限） | Task 3 Step 5、9 |
| §4.3 Send 分流 + 分级折算 | Task 4 |
| §4.4 回合级去重 | Task 5 |
| §4.5 生命周期（Stop reject / 重启重新发现） | Task 6 |
| §5.1 turn 六组用例 | Task 1 Step 1 |
| §5.2 opencode 单测 | Task 3/4/5/6 各自的测试 step |
| §5.3 真机 e2e | Task 7 |
| §5.4 custom 运行时降级 | Task 2（`ErrCustomAnswerRejected`）+ Task 4 Step 7 |
| §6 不做 | 无 task（正确——不做的事没有 task） |

无遗漏。

**类型一致性核对：** `QuestionInfo` / `QuestionOption` / `PendingQuestion` 在 Task 2 定义，Task 3/4/6 引用一致；`parseQuestionAnswers` 在 Task 4 定义并只在 Task 4 使用；`renderQuestionTicket` 在 Task 3 定义，Task 4/6 引用；`takePendingQuestionSnapshot` / `clearPendingQuestion` 在 Task 4 定义，Task 6 引用；`askedViaTool` 字段在 Task 3 随结构体一并加入（因为 Task 3 就要置位），`takeAskedViaTool` 在 Task 5 定义——**这条跨 task 依赖已在 Task 3 Step 9 显式说明**。

**已核对的仓库现状**（写计划时逐个 grep 确认，测试代码可直接照抄）：`newTestAdapter(t) *Adapter`、`drainOne(r) (executor.AdapterEvent, bool)`、`testHandle(dir)` 定义在 `reconcile_internal_test.go`（同包可用）；`a.newRun(taskID, taskDir, repoPath)` 会自行把运行态注册进 `a.runs`，因此测试里 `a.Send(ctx, "task-1", ...)` / `a.lookup("task-1")` 能直接命中；`NewAPI(baseURL, password)` 见 `api.go:100`；`unaryTimeout = 30s` 见 `api.go:63`；`mapIdle(r *runState, raw json.RawMessage)` 见 `adapter.go:1401`，其空回合两条早退路径已各自调 `r.clearTurn()`，Task 5 在那两处补 `takeAskedViaTool()`；`Stop` 的 kill 分支有 `if r.handle != nil` 守卫，故 Task 6 的测试可留 handle 为 nil。
