# B399 实现计划：唤醒回合的上界、续接、认领续租与增量简报

> 卡 B399 · 入口节点 charter · spec `docs/superpowers/specs/b399.md`（**修订 r2**，提交 `235996304`）
> 基线分支 `cards/B233.1-charter-7`，起手 HEAD `63fd6c5e`（本节点所在树是 r1 spec；r2 差异三处已按协调者
> 回执写进本计划）。凡引用行号者动手前重核，漂了以符号为准。
> 台账 `docs/superpowers/ledgers/2026-09-22-b399-plan-ledger.md`（含全部亲跑命令、原始输出、图查询记录）。
> 读者假设：对 handoff 仓零上下文的执行者。**本节点只出计划，不写实现**。
> 「未验证」= 本节点没亲自跑到结果，实现卡必须自己复核。

---

## 0. spec r2 口径与用户裁定（本计划的事实底座）

**r1 前提已被本节点证伪并经协调者复核**：r1 §3.1 断言「驱动租约本身可续租 ⇒ 席位不是 5 分钟硬上限」。
实测（命令与原文见台账 §2）：
- `RenewDriverLease`（`internal/ledger/binding.go:312`）**全仓非测试零调用方**；`DriverLease` 只被
  `internal/collab` 的展示面（`service.go:619`、`sessions.go:261`）读，不参与任何互斥判定。
- 跨机唤醒真正的互斥闸是 **`wake_claims`**：认领租期 `wakeClaimTTL = 5m`（`internal/agentd/wakeconsumer.go:32`），
  认领点 `:506` → `ClaimWake`（`internal/ledger/wakeclaim.go:31`），**回合执行期间不续租**。
- ⇒ 只把上界提到 30m，5 分钟后他机会重复认领同一 `(card,seq)`、对同一会话再跑一轮（B393 注释警告的同卡双跑）。

**用户 r2 裁定 A（本计划唯一新增机制）**：认领租期**仍 5m**（崩溃恢复窗口不变）＋回合存活期间心跳续租
（2m 量级）＋上界取 `hostapi.DefaultTurnTimeout = 30m`。`TestB393WakeTurnTimeoutWithinLease` 按新不变量重写。

**用户同日逐条裁定（协调者回执 247a6623，四条，逐条落进本计划）**：
1. 非超时、非 `Session not found` 的 resume 错误**也**不重建（`Launch` 只留给真正的 `Session not found`）；
   本轮按失败收口、会话保留、下一轮仍续接同一 session。
2. 超时轮落**恰一次** `needs_human`（每轮一条，轮内重试不刷屏）。
3. 超时轮**正常消费收口**（`completeWakeBatch` + 标 `seen` + 退避 30s），会话不销毁，下一个新事件到来时
   仍以同一 session 唤醒；**不引入** B390 式同 seq 自动重试。
4. 失败路径补的「唤醒回合结束」是**日志行**（消息串恰为 `唤醒回合结束`，带 `round_id` 与失败分类）；
   账本侧保持既有 `phase=fail` 行并**加 `class` 字段**，不新增 `phase=end` 行。

**弃选不变**：不做 A（拆小唤醒回合 / 重活改派发）；不动租约语义、动派发/路由、动 wire 契约。

---

## 1. 基线事实（本节点亲跑，原文见台账）

- `go build ./...` → EXIT=0；`go vet ./internal/agentd/ ./internal/keystone/ ./internal/hostapi/` → EXIT=0。
- 全量测试基线绿：`go test ./internal/agentd/ -count=1` → `ok … 155.628s`；
  `./internal/keystone/` → `ok … 0.265s`；`./internal/hostapi/` → `ok … 0.941s`；`./internal/ledger/` → `ok … 17.319s`。
- 现状上界：`internal/agentd/server.go:1182` `var coordWakeTurnTimeout = ledger.DriverLeaseTTL / 2`（=2m30s）。
- 现状 resume 分流：`internal/keystone/keystone.go:147-157` 对**任何** resume 错误调 `rebuildAfterResumeFailure`
  （`:160`）⇒ `Launch` 新会话。
- 现状超时错误形态：`internal/hostapi/driver.go:152-156` `hostapi: 回合超时（上界 %v），已终止进程树: %w`。
- 现状「会话不存在」通道：`internal/hostapi/driver.go:157-165` stderr 尾部进错误串（注释即为此留的通道）。
- 现状简报：`internal/keystone/keystone.go:322-344` 首句「醒来第一件事：读卡、查依赖、看基线新鲜度…」，
  末句「点火前看上一节点产出：先看文件清单再看裁决块。」。
- 现状失败出口：`internal/agentd/scheddrain.go:539-547`（只 `writeWakeRoundFail` + 打「唤醒回合失败」日志，
  不写「唤醒回合结束」）。
- 现状认领：`wakeClaimBatch` 仅由 `consumeAutomationEventsOnce`（`wakeconsumer.go:682`）调用；队列路径
  `drainIgnitionRequest`（`scheddrain.go:178`）**不认领**（`PopReady` 自己 CAS 出队，跨机不重复）。
- 现状 `wakeClaimTTL` 是 `const`（`wakeconsumer.go:32`）；`DriverLeaseRenewInterval = 2m`
  （`internal/ledger/binding.go:26`，生产零活续租，仅常量）。
- 现状无任何 `keysclient`/`hostapi` 的错误哨兵（`grep ErrTurnTimeout|ErrSessionNotFound` 全仓零命中）。
- 跨机写超时（**本节点发现，见 §8 残余**）：`cmd/agentd.go:442-449` `WriteTimeout = workspace.RunCmdTimeout + 1m = 11m`，
  而 `/coordinator/wake` 与 `/coordinator/launch` 在 handler 内**同步**跑完整回合（`coordapi.go:209`、`:123`）。

**现状红形态（判据先在基线复核）**：本计划的锁定测试今天必红，两种形态都合法：
- **编译红**：`coordWakeTurnTimeout == hostapi.DefaultTurnTimeout` 断言之外的符号（`startWakeClaimRenewal`、
  `keysclient.ErrTurnTimeout`、`WakeRoundEvent.Class`）今天不存在。
- **行为红**：`coordWakeTurnTimeout` 今天 = 2m30s ≠ 30m；`TestAutomationFallbackResumeRebuildFailure` 今天对
  generic resume 错误走 `Launch`（`launches==2`，本节点亲跑 PASS）——新判据「generic 错误不 Launch」今天必红。

---

## 2. 任务 DAG

```
T0（判据基线复核 + 三处新符号缺席确认，只读不改）
  └→ T1（转发层错误哨兵：hostapi 两个哨兵 + keysclient 两个哨兵 + agentd 适配器翻译）
        ├→ T2（keystone 错误分流：只有 Session not found 才重建；其余保留会话 + 简报瘦身）
        └→ T3（agentd 上界 30m + 回合内认领续租 + 失败收口分类/end 行/恰一次需要人）
              └→ T4（回归改写：既有 B393/B389 测试按新语义改写 + 序列化边界 round-trip）
（独立、无依赖）§8 残余发现（跨机 WriteTimeout 11m）→ 本计划只记录，不改代码
```

次序承重：T2 的「注入超时错误不重建」断言需要 T1 的 `keysclient.ErrTurnTimeout`；T3 的失败分类日志/
账本行需要 T2 让错误带上分类穿透到 agentd。T3 的续租与 T2 无依赖，但同批交付。

---

## 3. 接口契约（执行者只看本 task，故这里列全）

### 3.1 Consumes（既有签名，一字不改）

```go
// internal/keysclient/keysclient.go
type SessionSpec struct { CLI, HomeDir, Model, Workdir string; Env []string }
type SessionRef  struct { CLI, SessionID, Machine, HomeDir, Workdir, Model string }
type TurnResult  struct { SessionID, Output string }
type Runner interface {
	Launch(spec SessionSpec, prompt string) (TurnResult, error)   // 无 ctx
	Resume(ref SessionRef, prompt string) (TurnResult, error)     // 无 ctx
}
type LedgerView interface {
	GetCard(id string) (proto.Card, error)
	EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]proto.LedgerEvent, error)
	EffectiveBaseBranch(id string) (string, error)
	MarkNeedsHuman(cardID, reason, actor string) error
}

// internal/hostapi/hostapi.go（冻结面）
const DefaultTurnTimeout = 30 * time.Minute            // driver.go:46
func (h *Host) RunTurn(ctx context.Context, req TurnRequest) (TurnReply, error)

// internal/ledger/wakeclaim.go
func (s *Store) ClaimWake(seq int64, card, holder string, ttl time.Duration) (bool, error)
func (s *Store) CompleteWake(seq int64, card, holder string) error
// ClaimWake 同 holder + done_at 为空时即 upsert 续期 → 续租复用本方法，无需新账本方法。

// internal/ledger/events.go
func (s *Store) EnsureComment(cardID, dedupeKey, body, actor string) (wrote bool, err error)
// wrote=true 仅在本行首次写入时；同键重试返回 false。T3 的「恰一次 needs_human」用它去重。

// internal/keystone/keystone.go
func (s *Service) Wake(ctx context.Context, card string, evs []WakeEvent, spec keysclient.SessionSpec) (RoundResult, error)
type RoundResult struct { Woke bool; SessionID string; Rebuilt, Escalated bool; Output string }
```

### 3.2 Produces（本卡新增，跨 task 逐字对齐）

```go
// internal/hostapi/driver.go（T1）
var ErrTurnTimeout     = errors.New("hostapi: 回合被时限终止")
var ErrSessionNotFound = errors.New("hostapi: 会话不存在")

// internal/keysclient/keysclient.go（T1，消费方契约哨兵）
var ErrTurnTimeout     = errors.New("keysclient: 回合被时限终止")
var ErrSessionNotFound = errors.New("keysclient: 会话不存在")

// internal/agentd/server.go（T3）
var coordWakeTurnTimeout = hostapi.DefaultTurnTimeout          // 30m，替换 DriverLeaseTTL/2
func classifyRunnerErr(err error) error                          // hostapi 哨兵 → keysclient 哨兵

// internal/agentd/wakeconsumer.go（T3）
var wakeClaimTTL = 5 * time.Minute                              // const → var，测试可缩到秒级
var wakeClaimRenewInterval = ledger.DriverLeaseRenewInterval    // 2m 节拍
const (
	wakeFailClassTimeout         = "timeout"
	wakeFailClassSessionNotFound = "session_not_found"
	wakeFailClassOther           = "other"
)
func (s *Server) startWakeClaimRenewal(card string, seqs []int64) func()
func wakeFailClass(err error) string
func (s *Server) writeWakeRoundFail(card, roundID, class, reason string) // 新增 class 参数

// internal/agentd/wakeconsumer.go（T3，WakeRoundEvent 增 Class）
type WakeRoundEvent struct {
	Phase      string  `json:"phase"`
	Class      *string `json:"class,omitempty"`        // 新增：timeout/session_not_found/other
	Session    *string `json:"session,omitempty"`
	Err        *string `json:"err,omitempty"`
	DurationMs *int64  `json:"duration_ms,omitempty"`
}

// internal/keystone/keystone.go（T2）
func (s *Service) failResumeKeepingSession(card string, ref keysclient.SessionRef, resumeErr error) (RoundResult, error)
```

> **序列化边界**：`WakeRoundEvent.Class *string` 经 `json.Marshal` 写进 `EnsureComment` 的 body、消费方
> `json.Unmarshal` 读回。**必须**扩展既有 round-trip 测试覆盖 Class 的「缺失 vs 零值」两态（T4）。

---

## 4. 任务

### T0（前置）：判据基线复核与符号缺席确认（只读，本 task 由协调者执行，不派发）

**判据先在基线复核（必做）**：实现卡动手前在 `63fd6c5e` 上跑一遍并记录：

```sh
go build ./... && echo BUILD_OK
go test ./internal/agentd/  -run 'TestB393|TestAutomationFallbackResumeRebuildFailure|TestB389WakeEndpoint' -count=1
go test ./internal/keystone/ -run 'TestB393|TestIgnitionVerticalSlice' -count=1
go test ./internal/hostapi/  -run 'TestB393' -count=1
```

预期（本节点已跑，原文见台账）：
- `BUILD_OK`；
- `TestAutomationFallbackResumeRebuildFailure` **PASS**（现状 generic resume 错误 ⇒ `launches==2`，这就是 T2 要推翻的行为）；
- `TestB393WakeTurnTimeoutWithinLease` **PASS**（现状 2m30s < 5m）；
- 新增符号 grep 全仓零命中：`grep -rn "ErrTurnTimeout\|ErrSessionNotFound\|startWakeClaimRenewal\|wakeFailClass" internal/` → 空。

**测试范围声明**：本 task 不新增测试；只允许跑上面三条定向命令。
**日志与注释**：无（只读复核）。

---

### T1：转发层错误哨兵（hostapi → keysclient）

**目标**：让 keystone 能区分「回合被时限终止」「会话不存在」「其他」三类 resume 结果——这是 T2 分流的唯一判据来源。

**T1.1 `internal/hostapi/driver.go` 新增两个哨兵**（放在 `wakeRoundLogLevel` 之后，`:59` 附近）

```go
// ErrTurnTimeout 表示回合被挂钟上界终止（进程树已杀）。它**不等于**会话不存在：
// keystone 据此保留会话、下轮续接同一 session（B399 spec r2 §5）。
var ErrTurnTimeout = errors.New("hostapi: 回合被时限终止")

// ErrSessionNotFound 表示载体 CLI 明确报告会话不存在（stderr 含 "Session not found"）。
// 这是唯一允许 keystone 降级 Launch 重建的判据（B399 spec r2 §5）。
var ErrSessionNotFound = errors.New("hostapi: 会话不存在")
```

同时 import 增加 `"errors"`（driver.go 现无此 import）。

**T1.2 超时分支包哨兵**（`driver.go:152-157`，替换 `if ctx.Err() != nil { … }` 整块）

```go
	if ctx.Err() != nil {
		log().Warn("协调者回合超时终止", "cli", req.CLI,
			"timeout", effective.String(), "duration", dur.String())
		// errors.Join 保留 ctx.Err() 文本，同时让 errors.Is(err, ErrTurnTimeout) 成真；
		// 报错前缀与既有形态逐字保留（排障读数不变）。
		return TurnReply{}, fmt.Errorf("hostapi: 回合超时（上界 %v），已终止进程树: %w",
			effective, errors.Join(ErrTurnTimeout, ctx.Err()))
	}
```

**T1.3 会话不存在分支包哨兵**（`driver.go:158-166`，替换 `if waitErr != nil { … }` 整块）

```go
	if waitErr != nil {
		tail := tailBytes(&stderr, 4096)
		log().Warn("载体 CLI 回合失败", "cli", req.CLI, "duration", dur.String(),
			"stderr_tail_bytes", len(tail))
		// stderr 尾部进错误消息：resume 打错 id 时 keystone 兜底链需要看到
		// 「Session not found」（plan §三 F3）才能正确降级重建。
		// B399 r2：只有这一判据允许降级重建，包成哨兵供 keystone errors.Is 分流。
		if strings.Contains(tail, "Session not found") {
			return TurnReply{}, fmt.Errorf("hostapi: 回合失败（%v）stderr 尾部: %s: %w",
				waitErr, tail, ErrSessionNotFound)
		}
		return TurnReply{}, fmt.Errorf("hostapi: 回合失败（%v）stderr 尾部: %s",
			waitErr, tail)
	}
```

**T1.4 `internal/keysclient/keysclient.go` 新增消费方哨兵**（该文件当前 import 只有
`import "github.com/Xsxdot/handoff/internal/proto"`；改为 import 块并加 `"errors"`，如下）

```go
import (
	"errors"

	"github.com/Xsxdot/handoff/internal/proto"
)
```


```go
// ErrTurnTimeout / ErrSessionNotFound 是 keystone 消费 Runner 结果时的契约哨兵
// （B399 r2 §5）：实现（agentd.coordinatorRunner）负责把承载层错误翻译成它们，
// keystone 只用 errors.Is 判定——超时保留会话，只有会话不存在才重建。
var ErrTurnTimeout = errors.New("keysclient: 回合被时限终止")
var ErrSessionNotFound = errors.New("keysclient: 会话不存在")
```

**T1.5 `internal/agentd/server.go` 新增翻译器**（放在 `NewCoordinatorRunner` 之前，`:1286` 附近）

```go
// classifyRunnerErr 把承载层（hostapi）错误翻译成 keysclient 契约哨兵：
// 「回合超时」与会话不存在必须可判，keystone 据此分流（超时保留会话，只有
// 会话不存在才降级重建，B399 spec r2 §5）。其余错误原样透传。
func classifyRunnerErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, hostapi.ErrTurnTimeout) {
		return fmt.Errorf("%w: %v", keysclient.ErrTurnTimeout, err)
	}
	if errors.Is(err, hostapi.ErrSessionNotFound) {
		return fmt.Errorf("%w: %v", keysclient.ErrSessionNotFound, err)
	}
	return err
}
```

`coordinatorRunner.Launch`（`server.go:1234-1238`）与 `Resume`（`:1276-1280`）的失败返回改成：

```go
		return keysclient.TurnResult{}, classifyRunnerErr(err)
```

（两处 `if err != nil { … return keysclient.TurnResult{}, err }` 的最后一行 `return` 改为上面这行；
`errors`/`fmt`/`keysclient`/`hostapi` 在 server.go 均已 import。）

**T1.6 红色回路（缝 = `hostapi.Host.RunTurn`）**，新文件 `internal/hostapi/b399_error_class_test.go`：

```go
// b399_error_class_test.go —— B399 r2：两类可判错误必须有哨兵。
//
// 职责：钉住 hostapi 的超时与「会话不存在」分别包裹 ErrTurnTimeout/ErrSessionNotFound，
// 供 keystone 用 errors.Is 分流（超时保留会话，只有会话不存在才重建）。
// 缝：hostapi.Host.RunTurn（消费方：agentd.coordinatorRunner → keystone）。
// 边界：只经既有夹具 installFakeCLI/installFakeCLIFail；不碰真 CLI/账本。
package hostapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestB399TimeoutErrorCarriesSentinel 锁：超时回合的错误 errors.Is 命中 ErrTurnTimeout，
// 且不命中 ErrSessionNotFound（两分类不可混）。
//
// 红（当前 HEAD）：driver.go 超时错误无哨兵，errors.Is 恒 false。
// 绿（T1.2）：超时错误包裹 ErrTurnTimeout。
// 变异：把 errors.Join(ErrTurnTimeout, …) 改回只包 ctx.Err() → 复红。
func TestB399TimeoutErrorCarriesSentinel(t *testing.T) {
	installFakeCLI(t)
	withArgvCapture(t)
	parent, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	h := New()
	_, err := h.RunTurn(parent, TurnRequest{CLI: "opencode", Prompt: "超时", Env: []string{"FAKECLI_SLEEP=5"}})
	if err == nil {
		t.Fatalf("超时回合应失败")
	}
	if !errors.Is(err, ErrTurnTimeout) {
		t.Fatalf("超时错误未携带 ErrTurnTimeout: %v", err)
	}
	if errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("超时不得被判为会话不存在: %v", err)
	}
}

// TestB399SessionNotFoundErrorCarriesSentinel 锁：stderr 含 "Session not found" 的错误
// errors.Is 命中 ErrSessionNotFound，且不命中 ErrTurnTimeout。
//
// 红（当前 HEAD）：waitErr 分支无哨兵。
// 绿（T1.3）：stderr 含判据时包裹 ErrSessionNotFound。
// 变异：删掉 strings.Contains(tail, "Session not found") 分支 → 复红。
func TestB399SessionNotFoundErrorCarriesSentinel(t *testing.T) {
	installFakeCLIFail(t, "Session not found")
	h := New()
	_, err := h.RunTurn(context.Background(), TurnRequest{
		CLI: "opencode", SessionID: "ses_gone", Prompt: "续接",
	})
	if err == nil {
		t.Fatalf("会话不存在应失败")
	}
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("错误未携带 ErrSessionNotFound: %v", err)
	}
	if errors.Is(err, ErrTurnTimeout) {
		t.Fatalf("会话不存在不得被判为超时: %v", err)
	}
	if !strings.Contains(err.Error(), "Session not found") {
		t.Fatalf("错误文本应保留 stderr 判据: %v", err)
	}
}
```

> 夹具：`installFakeCLI`/`withArgvCapture` 复用 `internal/hostapi/runturn_test.go`；
> `installFakeCLIFail` 复用 `internal/hostapi/probe_test.go:157`。

**测试范围声明**：`go test ./internal/hostapi/ -run TestB399 -count=1`。
**日志**：T1.2/T1.3 的 `log().Warn` 分支保持既有形态（本 task 不改日志级别）。
**注释**：哨兵与翻译器的参数/返回/注意事项已写在代码块内（两分类为何可判、重建判据唯一）。

---

### T2：keystone 错误分流与简报瘦身

**目标**：除真正的 `Session not found` 外，resume 失败一律**保留会话、不重建**；简报只带增量（删「先读卡」类开场指令）。

**T2.1 改写 `Wake` 的 resume 段**（`internal/keystone/keystone.go:145-158`，替换 `prompt := …` 到函数尾）

```go
	prompt := s.briefing(card, evs)
	result, err := s.runner.Resume(ref, prompt)
	if err == nil {
		return RoundResult{Woke: true, SessionID: result.SessionID, Output: result.Output}, nil
	}
	// 错误分流是契约（B399 spec r2 §5）：只有「会话真的不存在」才降级重建；
	// 回合超时与其他错误一律保留会话，下一轮仍 resume 同一 session。
	if errors.Is(err, keysclient.ErrSessionNotFound) {
		return s.rebuildAfterResumeFailure(card, prompt, spec, ref, err)
	}
	return s.failResumeKeepingSession(card, ref, err)
```

同时把上方读取段 `s.mu.Lock(); ref, ok := s.sessions[card]; s.mu.Unlock()` 改为
`s.mu.Lock(); ref, _ := s.sessions[card]; s.mu.Unlock()`（**保留锁**：map 并发读必须持锁；
`ok` 不再用于分支，两分支合并为一个）。

**T2.2 新增 `failResumeKeepingSession`**（放在 `rebuildAfterResumeFailure` 之前或之后）

```go
// failResumeKeepingSession 是「回合失败但不重建」的收口（B399 spec r2 §5②）：
// 会话保留（sessions[card] 仍是 ref 指向的同一 session），下一轮仍 resume 同一
// session；错误上抛由 agentd 侧按分类落可见（超时落恰一次「需要人」）。
// 绝不调用 Launch——重建只在真正 Session not found 时才是净收益（用户 2026-09-22 裁定）。
func (s *Service) failResumeKeepingSession(card string, ref keysclient.SessionRef, resumeErr error) (RoundResult, error) {
	log().Warn("协调者唤醒回合失败，保留会话不重建", "card", card,
		"session", ref.SessionID, "cause", resumeErr)
	return RoundResult{SessionID: ref.SessionID}, fmt.Errorf("resume: %w", resumeErr)
}
```

`rebuildAfterResumeFailure`（`:160-175`）**不动**（仍只在会话不存在且重建也失败时落 needs_human）。

**T2.3 简报瘦身**（`internal/keystone/keystone.go:322-344`，整函数替换）

```go
// briefing 把本轮的增量上下文拼成回合简报：卡最小指针（卡号、标题、基线）与
// 本次唤醒事件。会话里已有的上下文留在会话里，账本只补充增量——不再指示模型
// 「先读卡」（B399 spec r2 §5：是否读卡由协调者自己判断）。以 ledger 为准不信
// 记忆——每回合重读，天然幂等。
func (s *Service) briefing(card string, evs []WakeEvent) string {
	b := "你是本卡的机器协调者。**你是一次性回合**：没有交互窗口，本轮跑完即结束；" +
		"下一次有事会被再次唤醒（续跑同一会话）。\n" +
		"需要人知道的事**发到 IM 房间**：`handoff session send <会话> <正文>`" +
		"（本卡所在会话可用 `handoff session list` 找到，会话条目里列着本卡）。" +
		"不适合现在推就在房间说明原因并结束本轮。\n\n## 本卡上下文\n\n- 卡号：" + card + "\n"
	if c, err := s.ledger.GetCard(card); err == nil {
		b += "- 标题：" + c.Title + "\n"
	}
	if base, err := s.ledger.EffectiveBaseBranch(card); err == nil && base != "" {
		b += "- 有效基线分支：" + base + "（本卡的合并目标以此为准，不要越过它碰别的分支）\n"
	}
	if len(evs) > 0 {
		b += "\n## 本次唤醒事件\n"
		for _, ev := range evs {
			b += "- [" + string(ev.Kind) + "] " + ev.Summary + "\n"
		}
	}
	return b
}
```

（删除首句「醒来第一件事：读卡、查依赖、看基线新鲜度；要推动工作流就用 `handoff card ...` 命令」与
末句「点火前看上一节点产出：先看文件清单再看裁决块。」；其余 ID/IM 行保留——它们是运行契约不是读卡指令。）

**T2.4 红色回路（缝 = `keystone.Service.Wake`）**，新文件 `internal/keystone/b399_resume_class_test.go`（包 `keystone_test`，复用 `slice_test.go` 的 `fakeRunner`）。

> **前置依赖**：本文件编译依赖 T4.1 给 `fakeRunner` 增的 `resumeErr error` 与 `refs []keysclient.SessionRef`
> 两个字段——实现卡先落 T4.1 的字段扩展（其余 T4 改动可后置），再落本文件。
> 本文件采用「runner 直传」形态（不新增任何生产 API）：`resumeClassSeed` 返回 runner 指针，测试直接读其字段。

```go
// b399_resume_class_test.go —— B399 r2：resume 错误分流（超时保留会话，只有
// Session not found 才重建）。
//
// 职责：钉住 Wake 对两类 resume 错误的不同出口——超时/其他不 Launch、会话保留，
// 下一轮仍以同一 session resume；Session not found 才 Launch。
// 缝：keystone.Service.Wake（生产入口）；夹具复用 slice_test.go 的 fakeRunner/
// fakeNarrator/recordingLedger（fakeRunner 的 resumeErr/refs 字段见 T4.1）。
// 边界：不复制 agentd 的回合收口规则；只观察 Launch/Resume 调用与回执。
package keystone_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
)

// resumeClassSeed 建一张有 coordinate 席位的卡 + 真实账本门面，返回 (service, cardID, runner)。
func resumeClassSeed(t *testing.T) (*keystone.Service, string, *fakeRunner) {
	t.Helper()
	st, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatalf("打开临时账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	if _, err := st.PutWorkflow("rc", ledger.WorkflowDef{States: []string{"进行中", "已完成"}}); err != nil {
		t.Fatalf("建工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{
		Title: "分流卡", Project: "handoff", Workflow: "rc", BaseBranch: "main", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	if err := st.BindSeat(card.ID, "cli:opencode#sess-old", proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "test-carrier", Machine: "local"}); err != nil {
		t.Fatalf("写席位: %v", err)
	}
	runner := &fakeRunner{}
	return keystone.New(runner, &fakeNarrator{}, &recordingLedger{f: facade}, nil), card.ID, runner
}

// TestB399ResumeTimeoutKeepsSessionNoLaunch 锁 r2 §5①：resume 报「回合超时」时
// 不调用 Launch，回执保留原会话；下一轮仍以同一 session resume。
//
// 红（当前 HEAD）：任何 resume 错误都走 rebuildAfterResumeFailure → Launch，
// launches≠0；新语义应 launches==0。
// 绿（T2.1/T2.2）：超时 → failResumeKeepingSession，launches 恒 0。
// 变异：把 errors.Is(err, keysclient.ErrSessionNotFound) 分支删掉、无条件 rebuild → 复红。
func TestB399ResumeTimeoutKeepsSessionNoLaunch(t *testing.T) {
	svc, cardID, runner := resumeClassSeed(t)
	runner.failNext = 99
	runner.resumeErr = fmt.Errorf("resume 不可用: %w", keysclient.ErrTurnTimeout)

	spec := keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h", Workdir: "/w"}
	result, err := svc.Wake(context.Background(), cardID, []keystone.WakeEvent{
		{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "x"},
	}, spec)
	if err == nil {
		t.Fatalf("超时 resume 应返回错误")
	}
	if !errors.Is(err, keysclient.ErrTurnTimeout) {
		t.Fatalf("错误必须穿透 keysclient.ErrTurnTimeout: %v", err)
	}
	if len(runner.launches) != 0 {
		t.Fatalf("超时不得重建（Launch 次数=%d）", len(runner.launches))
	}
	if len(runner.resumes) != 1 {
		t.Fatalf("应尝试一次 Resume，实得 %d", len(runner.resumes))
	}
	if result.SessionID != "sess-old" {
		t.Fatalf("回执应保留原会话 sess-old，实得 %q", result.SessionID)
	}

	// 下一轮仍续接同一 session：failNext 放行一次，断言 ref.SessionID 仍是 sess-old。
	runner.failNext = 0
	if _, err := svc.Wake(context.Background(), cardID, []keystone.WakeEvent{
		{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "y"},
	}, spec); err != nil {
		t.Fatalf("下一轮续接应成功: %v", err)
	}
	if len(runner.refs) == 0 || runner.refs[len(runner.refs)-1].SessionID != "sess-old" {
		t.Fatalf("下一轮必须续接同一 session sess-old，实得 %+v", runner.refs)
	}
}

// TestB399ResumeNotFoundTriggersLaunch 锁 r2 §5②：只有 stderr 判据
// keysclient.ErrSessionNotFound 才降级 Launch 重建。
//
// 红（当前 HEAD）：generic 与 not-found 无区分；本测试在旧码上会「恰好也重建」，
// 与 TestB399ResumeTimeoutKeepsSessionNoLaunch 成对才钉住分流（旧码必红于后者）。
// 绿（T2.1）：not-found → rebuildAfterResumeFailure → Launch。
func TestB399ResumeNotFoundTriggersLaunch(t *testing.T) {
	svc, cardID, runner := resumeClassSeed(t)
	runner.failNext = 99
	runner.resumeErr = fmt.Errorf("resume 不可用: %w", keysclient.ErrSessionNotFound)

	result, err := svc.Wake(context.Background(), cardID, []keystone.WakeEvent{
		{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "x"},
	}, keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h", Workdir: "/w"})
	if err != nil {
		t.Fatalf("not-found 应降级重建成功: %v", err)
	}
	if len(runner.launches) != 1 {
		t.Fatalf("not-found 必须重建（Launch 次数=%d）", len(runner.launches))
	}
	if !result.Rebuilt || result.SessionID != "sess-new" {
		t.Fatalf("重建回执不完整: %+v", result)
	}
}
```

**T2.5 简报测试（缝 = `keystone.Service.Wake` 送入 Runner 的 prompt）**，新文件
`internal/keystone/b399_briefing_test.go`（包 `keystone_test`），完整代码：

```go
// b399_briefing_test.go —— B399 r2 §3③：简报只带增量，不指示模型「先读卡」。
//
// 职责：钉住 Wake 送入 Runner 的 prompt 只含卡最小指针（卡号/标题/基线）+ 本轮事件，
// 不含「读卡」类开场指令，也不塞入未在本轮事件里的旧上下文。
// 缝：keystone.Service.Wake（生产入口；prompt 观察点是 fakeRunner.resumes）。
// 边界：只断言 prompt 文本，不复制 agentd 回合规则。
package keystone_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
)

// TestB399BriefingCarriesIncrementOnly 锁简报瘦身。
//
// 红（当前 HEAD）：简报首句含「醒来第一件事：读卡…」、末句含「点火前看上一节点产出」。
// 绿（T2.3）：两串消失；卡指针与本轮事件保留。
// 变异：把首句改回「醒来第一件事：读卡…」→ 复红。
func TestB399BriefingCarriesIncrementOnly(t *testing.T) {
	svc, cardID, runner := resumeClassSeed(t)

	if _, err := svc.Wake(context.Background(), cardID, []keystone.WakeEvent{
		{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "本轮增量事件"},
	}, keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h", Workdir: "/w"}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if len(runner.resumes) != 1 {
		t.Fatalf("应恰一次 Resume，实得 %d", len(runner.resumes))
	}
	got := runner.resumes[0]
	for _, banned := range []string{"读卡", "查依赖", "醒来第一件事", "点火前看上一节点产出"} {
		if strings.Contains(got, banned) {
			t.Fatalf("简报不得含开场指令 %q:\n%s", banned, got)
		}
	}
	for _, want := range []string{"- 卡号：" + cardID, "- 标题：", "- 有效基线分支：", "本轮增量事件"} {
		if !strings.Contains(got, want) {
			t.Fatalf("简报缺 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "旧事件不应出现") {
		t.Fatalf("简报不得塞入未在本轮事件里的旧上下文:\n%s", got)
	}
}
```

**测试范围声明**：`go test ./internal/keystone/ -run 'TestB399' -count=1`（T2 只跑 keystone 包；T4 再跑全量 keystone 回归）。
**日志**：T2.2 的 `log().Warn` 带 card/session/cause（失败路径不静默）。
**注释**：`failResumeKeepingSession` 的 why、`briefing` 的边界（只带增量、不指示读卡）已写在代码块内。

---

### T3：agentd 上界 30m + 回合内认领续租 + 失败收口

**目标**：唤醒回合上界取 hostapi 缺省 30m；回合存活期间续租 `wake_claims`（5m 租期不失效）；失败路径补
「唤醒回合结束」日志行、失败分类进账本、超时恰一次 needs_human。

**T3.1 上界改值**（`internal/agentd/server.go:1174-1195`，替换注释块与 `var`）

```go
// coordWakeTurnTimeout 是协调者无头回合的挂钟上界（B399 spec r2）：取 hostapi 既有
// 缺省 30 分钟，不再由 DriverLeaseTTL 派生。跨机互斥由 wake_claims 认领保证，且回合
// 存续期间由 startWakeClaimRenewal 续租（wakeconsumer.go）——认领租期仍 5m 不变
// （崩溃恢复窗口不变），但回合不再自缚于 5m 之内；30m 上限仍在，不回到无界挂死。
// 变量而非 const：agentd 包内测试覆盖它到秒级，避免 30m 真等。
var coordWakeTurnTimeout = hostapi.DefaultTurnTimeout

// readyCtx 返回带 coordWakeTurnTimeout 上界的 ctx。
// 为什么不用入参 ctx：keysclient.Runner 接口冻结、无 ctx 参数；coordinatorRunner
// 的调用方（keystone.Wake）虽带 ctx，但接口消化不到这里。自持期限是接口冻结下
// 唯一能在「不返回的 CLI」上兜底的落点。
func readyCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), coordWakeTurnTimeout)
}
```

（原「依据（B393 MAJOR-2）」注释段整体删除，避免与新语义矛盾。`hostapi` 在 server.go 已 import。）

**T3.2 认领租期常量改 var + 续租节拍**（`internal/agentd/wakeconsumer.go:29-45` 区域）

现状 `const` 块（`wakeconsumer.go:29-45`）含 `wakeClaimTTL`、`wakeRetryBackoff`、`wakeRetryMax`、
`automationStallEscalateAfter` 四项。改动：**只把 `wakeClaimTTL` 移出 const 块**（其余三项原样留在 const 块），
其下新增两个包级 var：

```go
// wakeClaimTTL 是唤醒认领租期：短于 agentd stalltimeout（默认 2h），够一台机器
// 崩溃后被另一台接管；不设无限期，避免认领行永久挡住宿主游标。
// 变量而非 const（B399 r2）：测试缩到秒级验证「回合内续租 / 收尾停续」。
var wakeClaimTTL = 5 * time.Minute

// wakeClaimRenewInterval 是回合存活期间认领续租的心跳节拍（B399 r2 §3）：取既有
// DriverLeaseRenewInterval 的量级 2m，远小于 wakeClaimTTL(5m)，保证租约不失效。
var wakeClaimRenewInterval = ledger.DriverLeaseRenewInterval
```

（`wakeRetryBackoff`/`wakeRetryMax`/`automationStallEscalateAfter` 三项留在现有 const 块内不动。）

**T3.3 续租实现**（`internal/agentd/wakeconsumer.go`，紧随 `completeWakeBatch` 之后，`:533` 附近）

```go
// startWakeClaimRenewal 在回合存续期间按 wakeClaimRenewInterval 续租本批认领，
// 返回停止函数（幂等，必须 defer/显式调用）。回合收尾即停续：认领回到 5m 窗口，
// 崩溃时他机 5m 后可接管（B399 r2 §5，恢复窗口不变）。
// 为什么复用 ClaimWake：它的语义是「同 holder 未终局即 upsert 续期」（wakeclaim.go:52），
// 正是续租所需，无需新增账本方法。
func (s *Server) startWakeClaimRenewal(card string, seqs []int64) func() {
	if s.ledger == nil || len(seqs) == 0 {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	holder := s.wakeClaimHolder()
	go func() {
		ticker := time.NewTicker(wakeClaimRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				for _, seq := range seqs {
					got, err := s.ledger.ClaimWake(seq, card, holder, wakeClaimTTL)
					if err != nil {
						s.log.Warn("唤醒认领续租失败", "card", card, "seq", seq, "cause", err)
						continue
					}
					if !got {
						s.log.Warn("唤醒认领续租未生效（可能已收尾或被接管）", "card", card, "seq", seq)
					}
				}
			}
		}
	}()
	return stop
}
```

`wakeconsumer.go` import 增加 `"sync"`（现无；`"time"`、`"ledger"` 已在）。

**T3.4 消费循环挂续租**（`internal/agentd/wakeconsumer.go:724`，替换那一行为三行）

```go
		stopRenew := s.startWakeClaimRenewal(card, claimedSeqs)
		result, wakeErr := s.wakeCoordinatorRoundRaw(ctx, card, evs, raws)
		stopRenew()
```

（`wakeCoordinatorRoundRaw` 同步返回；其后既有分支不变：准入满员**不**收尾、非准入错误
completeWakeBatch+backoff+seen。这就是用户裁定 3 的「正常消费收口」，不需要新增逻辑。）

**T3.5 失败分类与「唤醒回合结束」日志行**（`internal/agentd/scheddrain.go:539-547`，替换 `if err != nil { … }` 块）

```go
	if err != nil {
		class := wakeFailClass(err)
		// 失败终态行：phase=fail + class + 原因摘要（与 start 同 roundID，一轮一组）。
		// 失败分支过去只打日志不下账；本行给账本「没跑 vs 跑了但失败」的区分度，
		// 并让超时轮恰一次落 needs_human（B399 r2 §5）。
		s.writeWakeRoundFail(card, roundID, class, truncateRunes(err.Error(), 400))
		s.log.Log(ctx, wakeRoundLogLevel, "唤醒回合失败", "card", card,
			"event_count", len(evs), "squad", binding.Squad, "carrier", binding.Carrier,
			"round_id", roundID, "class", class, "cause", err)
		// 失败路径补 end 行（B399 r2 用户裁定 4）：start/end 对称，可按 round_id 对账。
		s.log.Log(ctx, wakeRoundLogLevel, "唤醒回合结束", "card", card,
			"round_id", roundID, "class", class, "ok", false)
		return result, fmt.Errorf("唤醒协调者回合失败: %w", err)
	}
```

同时给成功路径的既有「唤醒回合结束」日志（`:558-560`）加 `"class", ""` 与 `"ok", true`，保持两行同形
（可选但推荐，便于按 ok 过滤）。

**T3.6 `writeWakeRoundFail` 增 class 与恰一次 needs_human**（`internal/agentd/wakeconsumer.go:430-447`，整函数替换）

```go
// wakeFailClass 把失败的分类映成账本/日志用词（B399 r2 §5③）：超时、会话不存在、
// 其他三态可区分。
func wakeFailClass(err error) string {
	switch {
	case err == nil:
		return wakeFailClassOther
	case errors.Is(err, keysclient.ErrTurnTimeout):
		return wakeFailClassTimeout
	case errors.Is(err, keysclient.ErrSessionNotFound):
		return wakeFailClassSessionNotFound
	default:
		return wakeFailClassOther
	}
}

// writeWakeRoundFail 落一行 phase=fail 的 wake_round 注释（键含 roundID，含 class）。
// 写失败只留 Warn，不覆盖唤醒结果本身。恰一次「需要人」（B399 r2 §5）：仅当 class=timeout
// 且本行是本轮**首次**写入（EnsureComment 返回 wrote=true）——同轮 2s 重试的重复写
// (wrote=false) 不重复刷屏。
func (s *Server) writeWakeRoundFail(card, roundID, class, reason string) {
	if s.ledger == nil {
		s.log.Error("唤醒回合失败行未落账：账本未装配", "card", card, "round_id", roundID, "reason", reason)
		return
	}
	payload, err := json.Marshal(WakeRoundEvent{Phase: "fail", Class: &class, Err: &reason})
	if err != nil {
		s.log.Error("唤醒回合失败行序列化失败", "card", card, "round_id", roundID, "cause", err)
		return
	}
	wrote, werr := s.ledger.EnsureComment(card, WakeRoundDedupePrefix+":fail:"+roundID,
		string(payload), "agentd")
	if werr != nil {
		s.log.Warn("唤醒回合失败事件写入失败（不覆盖唤醒结果）", "card", card,
			"round_id", roundID, "cause", werr)
		return
	}
	if class == wakeFailClassTimeout && wrote {
		reasonText := fmt.Sprintf("协调者唤醒回合超时（会话已保留，下一轮仍续接同一会话）：%s", reason)
		if nerr := s.ledger.MarkNeedsHuman(card, reasonText, "agentd"); nerr != nil {
			s.log.Error("唤醒超时落 needs_human 失败", "card", card, "round_id", roundID, "cause", nerr)
		}
	}
}
```

`wakeconsumer.go` 增加 class 常量与 `keysclient` import：

```go
const (
	wakeFailClassTimeout         = "timeout"
	wakeFailClassSessionNotFound = "session_not_found"
	wakeFailClassOther           = "other"
)
```

**T3.7 其余 `writeWakeRoundFail` 调用点补 class**（`internal/agentd/scheddrain.go` 共 **14** 个调用点；其中 `:543`
由 T3.5 改写为传 `class`，**其余 13 处**全部传 `wakeFailClassOther`）。行号（动手前重核）：`:369`、`:377`、`:457`、
`:462`、`:469`、`:479`、`:491`、`:504`、`:509`、`:519`、`:564`、`:572`、`:575`。形如：

```go
		s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("转交唤醒取目标客户端 %s 失败: %v", machine, err))
```

**T3.8 `WakeRoundEvent` 增 Class**（`internal/agentd/wakeconsumer.go:420-425`，替换结构体）

见 §3.2 Produces；在 `Phase` 之后插入 `Class *string \`json:"class,omitempty"\``。

**T3.9 红色回路（缝 = `agentd.Server.consumeAutomationEventsOnce` 与 `drainIgnitionRequest`）**，
新文件 `internal/agentd/b399_wake_renewal_test.go`。完整代码（夹具均为既有：`newNoPTYAutomationEnv`、
`createCoordCard`、`prebindConsumerSession`、`appendMirroredForConsumer`、`mustScheduling`、
`readWakeRoundComments`、`alwaysNoSlotScheduling`、`waitFor`）：

```go
// b399_wake_renewal_test.go —— B399 r2：回合内认领续租、上界取值、失败收口。
//
// 职责：钉住 (1) 回合存活期间 wake_claims 被续租（5m 租约不失效，他机拿不到）；
// (2) 回合收尾即停续、认领按 TTL 过期可被他机接管；(3) 上界取 hostapi 缺省 30m；
// (4) 失败路径写 class + 「唤醒回合结束」日志行，且超时恰一次 needs_human。
// 缝：agentd.Server.consumeAutomationEventsOnce（消费循环入口）与
// agentd.Server.drainIgnitionRequest（队列出队入口）。
// 边界：不复制 keystone 分流规则；只观察认领行、账本 wake_round 行与日志。
package agentd

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// blockingRunner 的 Resume 阻塞到测试关闭 release，模拟「回合仍在跑」。
type blockingRunner struct {
	release chan struct{}
}

func (r *blockingRunner) Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error) {
	return keysclient.TurnResult{SessionID: "sess-b"}, nil
}

func (r *blockingRunner) Resume(ref keysclient.SessionRef, _ string) (keysclient.TurnResult, error) {
	<-r.release
	return keysclient.TurnResult{SessionID: ref.SessionID}, nil
}

// TestB399WakeClaimRenewedDuringRound 锁 r2 §5：回合存活期间认领被续租，
// 租期（500ms）过后他机仍拿不到该 (card,seq)。
//
// 红（删掉 T3.4 的 startWakeClaimRenewal 调用）：500ms 后认领过期，他机拿到 → 复红。
// 绿：续租使认领不过期，他机 ClaimWake 返回 false。
func TestB399WakeClaimRenewedDuringRound(t *testing.T) {
	prevTTL, prevInt := wakeClaimTTL, wakeClaimRenewInterval
	wakeClaimTTL, wakeClaimRenewInterval = 500*time.Millisecond, 25*time.Millisecond
	defer func() { wakeClaimTTL, wakeClaimRenewInterval = prevTTL, prevInt }()

	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	release := make(chan struct{})
	env.srv.SetKeystone(keystone.New(&blockingRunner{release: release},
		&fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	seq := appendMirroredForConsumer(t, env.ledger, cardID, "renew", "completed", 1, `{"text":"done"}`)

	processed := make(chan int, 1)
	go func() {
		n, _, _ := env.srv.consumeAutomationEventsOnce(context.Background())
		processed <- n
	}()

	// 等回合确实开始（认领已落行），再等到超过原 500ms TTL。
	waitFor(t, func() bool {
		db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)")
		if err != nil {
			return false
		}
		defer db.Close()
		var n int
		return db.QueryRow(`SELECT count(*) FROM wake_claims WHERE seq = ? AND done_at IS NULL`, seq).Scan(&n) == nil && n > 0
	})
	time.Sleep(900 * time.Millisecond)

	got, err := env.ledger.ClaimWake(seq, cardID, "other-holder", wakeClaimTTL)
	if err != nil {
		t.Fatalf("他机认领探测: %v", err)
	}
	if got {
		t.Fatalf("回合存活期间认领被续租，他机不得拿到（若 got=true 说明续租未生效）")
	}

	close(release)
	if n := <-processed; n != 1 {
		t.Fatalf("回合应正常收尾 processed=1，实得 %d", n)
	}
}

// TestB399WakeClaimRenewalStopsAfterRound 锁 r2 §5：回合收尾即停续，认领按
// TTL（300ms）过期后他机可接管（崩溃恢复窗口不变）。
//
// 红（续租 goroutine 未随回合停止）：认领被一直续租，450ms 后他机仍拿不到 → 复红。
// 绿：停续后 300ms TTL 过期，他机 ClaimWake 返回 true。
func TestB399WakeClaimRenewalStopsAfterRound(t *testing.T) {
	prevTTL, prevInt := wakeClaimTTL, wakeClaimRenewInterval
	wakeClaimTTL, wakeClaimRenewInterval = 300*time.Millisecond, 25*time.Millisecond
	defer func() { wakeClaimTTL, wakeClaimRenewInterval = prevTTL, prevInt }()

	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	// 准入恒满员：回合在 AdmitSeatCarrier 即失败，认领留在飞（B390 P2 不收尾）。
	stub := &alwaysNoSlotScheduling{Service: mustScheduling(t, env.srv)}
	env.srv.SetScheduling(stub)
	seq := appendMirroredForConsumer(t, env.ledger, cardID, "renew-stop", "completed", 1, `{"text":"done"}`)

	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err == nil {
		t.Fatalf("准入满员应返回错误")
	}
	time.Sleep(450 * time.Millisecond) // > 300ms TTL；续租已在回合返回时停止

	got, err := env.ledger.ClaimWake(seq, cardID, "other-holder", wakeClaimTTL)
	if err != nil {
		t.Fatalf("他机认领探测: %v", err)
	}
	if !got {
		t.Fatalf("回合收尾后认领应按 TTL 过期、他机可接管（got=false 说明续租未随回合停止）")
	}
}

// TestB399WakeTurnBoundIsHostapiDefault 锁 r2 §5：上界取 hostapi 缺省（30m），
// 不再由 DriverLeaseTTL 派生。
//
// 红（当前 HEAD）：coordWakeTurnTimeout = DriverLeaseTTL/2 = 2m30s。
// 绿（T3.1）：== hostapi.DefaultTurnTimeout。
// 变异：改回 ledger.DriverLeaseTTL / 2 → 复红。
func TestB399WakeTurnBoundIsHostapiDefault(t *testing.T) {
	if coordWakeTurnTimeout != hostapi.DefaultTurnTimeout {
		t.Fatalf("上界应取 hostapi 缺省 %v，实得 %v", hostapi.DefaultTurnTimeout, coordWakeTurnTimeout)
	}
	if coordWakeTurnTimeout == ledger.DriverLeaseTTL/2 {
		t.Fatalf("上界不得再由 DriverLeaseTTL 派生")
	}
}

// TestB399TimeoutWritesClassAndEndAndOneNeedsHuman 锁 r2 用户裁定 2/4：超时轮
// 落恰一条 needs_human（同轮重试不刷屏）、phase=fail 注释带 class=timeout、日志含
// 「唤醒回合结束」。
//
// 红（当前 HEAD）：writeWakeRoundFail 无 class、不落 needs_human、无结束行。
// 绿（T3.5/T3.6）：class=timeout 的 fail 行恰 1、needs_human 恰 1、日志含结束行。
// 变异：删掉 T3.6 的 `&& wrote` → needs_human 5 条 → 复红。
func TestB399TimeoutWritesClassAndEndAndOneNeedsHuman(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	runner := &fallbackConsumerRunner{failResume: true, resumeTimeout: true}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))

	var buf bytes.Buffer
	prev := env.srv.log
	env.srv.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	defer func() { env.srv.log = prev }()

	req := scheduling.IgnitionRequest{
		Card: cardID, Squad: "coord", Node: "implement", Actor: "test", Ready: true,
	}
	for i := 0; i < 5; i++ {
		_ = env.srv.drainIgnitionRequest(context.Background(), req)
	}

	// 断言 A：phase=fail 且 Class=timeout 的注释恰 1 行。
	fails := 0
	for _, r := range readWakeRoundComments(t, env, cardID) {
		if r.Phase == "fail" && r.Class != nil && *r.Class == wakeFailClassTimeout {
			fails++
		}
	}
	if fails != 1 {
		t.Fatalf("超时轮应恰一行 class=timeout 的 fail，实得 %d", fails)
	}
	// 断言 B：needs_human 恰 1 条（跨 5 次重试不刷屏）。
	if n := b390NeedsHumanCount(t, env, cardID); n != 1 {
		t.Fatalf("超时轮应恰一条 needs_human，实得 %d", n)
	}
	// 断言 C：日志含「唤醒回合结束」且带 round_id/class=timeout。
	logs := buf.String()
	if !strings.Contains(logs, "唤醒回合结束") {
		t.Fatalf("失败路径缺「唤醒回合结束」日志行:\n%s", logs)
	}
	if !strings.Contains(logs, "class=timeout") {
		t.Fatalf("结束行应带 class=timeout:\n%s", logs)
	}
}
```

> 注（导入与辅助）：Go 的 import 是**按文件**的，`database/sql` 在 `wakeconsumer_test.go` 有 import 不会让本文件可用
> ——**实现卡必须在本文件 import 增 `"database/sql"`**。既有 `wakeClaimRowExists` 不可直接复用（它判 `done_at != nil`，
> 不是「在飞」），故本文件内联 raw 查询。`readWakeRoundComments`（`b393_wakeround_fail_test.go`）、
> `b390NeedsHumanCount`（`b390_wake_stall_test.go`）、`waitFor`（`cardstep_test.go`）、`appendMirroredForConsumer` /
> `prebindConsumerSession`（`wakeconsumer_test.go`）均为同包既有辅助；`fallbackConsumerRunner.resumeTimeout`
> 字段由 T4.4 提供；`alwaysNoSlotScheduling` 在 `b390_wake_stall_test.go`。

**测试范围声明**：`go test ./internal/agentd/ -run 'TestB399' -count=1`。
**日志**：T3.3 续租失败 Warn；T3.5 失败/结束两行；T3.6 落账失败 Error——均已带 card/round_id/cause。
**注释**：`coordWakeTurnTimeout`/`wakeClaimTTL`/`wakeClaimRenewInterval`/`startWakeClaimRenewal`/`wakeFailClass`/
`writeWakeRoundFail` 的 why 与边界已写在代码块内。

---

### T4：既有测试回归改写 + 序列化边界

**目标**：把「任何 resume 错误 ⇒ 重建」时代的测试改成新语义；扩展 `WakeRoundEvent` round-trip。

**T4.1 `internal/keystone/slice_test.go` 的 `fakeRunner` 增错误分类字段**

```go
type fakeRunner struct {
	launches     []string
	resumes      []string                // 成功 Resume 的 prompt（既有）
	refs         []keysclient.SessionRef // 每次 Resume 的 ref（T2.4 断言续接同一 session）
	failNext     int                     // >0 时接下来 N 次 Resume 返回错误
	failLaunches bool                    // 置真后 Launch 一律失败（重建也不可用的兜底场景）
	resumeErr    error                   // 非空时作 Resume 错误（可含 keysclient 哨兵）；nil = 通用错误 "resume 不可用"
}

func (f *fakeRunner) Resume(ref keysclient.SessionRef, prompt string) (keysclient.TurnResult, error) {
	f.refs = append(f.refs, ref)
	if f.failNext > 0 {
		f.failNext--
		if f.resumeErr != nil {
			return keysclient.TurnResult{}, f.resumeErr
		}
		return keysclient.TurnResult{}, errors.New("resume 不可用")
	}
	f.resumes = append(f.resumes, prompt)
	return keysclient.TurnResult{SessionID: ref.SessionID, Output: "verdict: pass"}, nil
}
```

`TestIgnitionVerticalSlice`（`:258-281`）两段改写：
- 重建段：`runner.failNext = 99` 后**追加** `runner.resumeErr = fmt.Errorf("resume 不可用: %w", keysclient.ErrSessionNotFound)`
  （`keysclient` 已 import；**slice_test.go 需新增 `"fmt"` import**），断言不变（`rebuilt.Rebuilt`、narrator「载体已更换」）。
- 兜底终点段：保持 `runner.failLaunches = true`（`resumeErr` 仍是上一段设置的 ErrSessionNotFound）→ 重建失败 → 转等人断言不变。

**T4.2 `internal/keystone/b393_degrade_test.go`**：`runner := &fakeRunner{failNext: 1, failLaunches: true}` 改为
`&fakeRunner{failNext: 1, failLaunches: true, resumeErr: fmt.Errorf("resume 不可用: %w", keysclient.ErrSessionNotFound)}`
（**本文件需新增 `"fmt"` import**）。理由断言（含 `resume 不可用`、`拉起不可用`）不变。

**T4.3 `internal/agentd/coordapi_test.go` 的 `wakeEndpointRunner` 增 `resumeErr error` 字段**（结构体与 `Resume` 一并替换）：

```go
type wakeEndpointRunner struct {
	failResume bool
	resumeErr  error // 非空时作 Resume 错误（可含 keysclient 哨兵）
	launchID   string
	resumes    int
	launches   int
}

func (r *wakeEndpointRunner) Resume(ref keysclient.SessionRef, _ string) (keysclient.TurnResult, error) {
	r.resumes++
	if r.failResume {
		if r.resumeErr != nil {
			return keysclient.TurnResult{}, r.resumeErr
		}
		return keysclient.TurnResult{}, errors.New("resume failed")
	}
	return keysclient.TurnResult{SessionID: ref.SessionID, Output: "ok"}, nil
}
```

`TestB389WakeEndpointExecutesAndRebinds`（`:961`）构造改为
`&wakeEndpointRunner{failResume: true, resumeErr: fmt.Errorf("resume failed: %w", keysclient.ErrSessionNotFound), launchID: "sess-new"}`
（import 增 `"fmt"`）。其余断言不变。

**T4.4 `internal/agentd/wakeconsumer_test.go` 的 `fallbackConsumerRunner` 增两个分类开关**

```go
type fallbackConsumerRunner struct {
	mu            sync.Mutex
	launches      int
	resumes       int
	failLaunch    bool
	failResume    bool
	resumeTimeout bool // Resume 错误包裹 keysclient.ErrTurnTimeout
	resumeNotFound bool // Resume 错误包裹 keysclient.ErrSessionNotFound
}

func (r *fallbackConsumerRunner) Resume(keysclient.SessionRef, string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resumes++
	if r.failResume {
		switch {
		case r.resumeTimeout:
			return keysclient.TurnResult{}, fmt.Errorf("resume timeout: %w", keysclient.ErrTurnTimeout)
		case r.resumeNotFound:
			return keysclient.TurnResult{}, fmt.Errorf("resume failed: %w", keysclient.ErrSessionNotFound)
		default:
			return keysclient.TurnResult{}, errors.New("resume failed")
		}
	}
	return keysclient.TurnResult{SessionID: "fallback-session"}, nil
}
```

`TestAutomationFallbackResumeRebuildFailure`（`:864-865`）改为
`runner.failResume = true; runner.failLaunch = true; runner.resumeNotFound = true`
（该测试要走到重建失败；generic 错误不再重建）。断言（`launches==2`、needs 含 resume/重建）不变。

**T4.5 删除 `internal/agentd/b393_timeout_invariant_test.go`**（原 `TestB393WakeTurnTimeoutWithinLease` 的
「上界 < `DriverLeaseTTL`」数值断言按 r2 新不变量作废）。其职责由 T3.9 新建的 `b399_wake_renewal_test.go`
承接：B399-3 锁上界取值（`== hostapi.DefaultTurnTimeout`）、B399-1/2 锁新不变量（回合内存活续租、收尾停续）。
**禁止**在新旧两处并存同一断言。删除时同步确认 `ledger` import 在 `b399_wake_renewal_test.go` 仍被使用
（B399-3 引用了它）。

**T4.6 `internal/agentd/b393_timeout_test.go`** 的 `TestB393ResumeHangsBoundedByTimeout` **保留**（「有界退出」
红线不变），仅更新注释：绿态仍是 1s 覆盖后秒级返回；补一句「被界砍掉之后不许丢会话、不许无脑重建，见
T2 的分流测试」。断言不动。

**T4.7 序列化边界 round-trip**（`internal/agentd/b393_wakeround_event_test.go`，扩展 `cases`）

在两态用例里加入 Class：

```go
	zero := ""
	var zeroMs int64 = 0
	cases := []WakeRoundEvent{
		{Phase: "start"},                                          // 全缺省
		{Phase: "end", Session: &zero, DurationMs: &zeroMs},       // 显式零值
		{Phase: "fail", Class: &zero, Err: &zero},                 // class 显式零值
	}
```

并在逐个断言里加 `(got.Class == nil) != (want.Class == nil)` 与 `want.Class != nil && *got.Class != *want.Class`
的零值校验（与 Session 同法）。**变异**：把 `Class *string` 改成 `Class string`（去指针）→「缺失 vs 零值」
断言复红。

**T4.8** 全量回归：`go test ./internal/keystone/ ./internal/agentd/ ./internal/hostapi/ ./internal/ledger/ -count=1`。

**测试范围声明**：本 task 跑上述四包全量（仅这四个包；全仓测试不属于任何单个 task）。
**日志与注释**：改写处补 why（为何改用哨兵分类）；测试文件头更新职责/边界/缝。

---

## 5. 占位符扫描（自我声明）

- **正当出口（夹具形态随包而异）**，逐条声明：
  - T2.4/T2.5 与 T4.1 的夹具形态：`fakeRunner` 字段扩展是**精确代码**（T4.1）；`resumeClassSeed` 不是占位，
    已给完整实现，其依赖的 `fakeRunner.resumeErr`/`refs` 由 T4.1 提供，照 `internal/keystone/slice_test.go`
    既有 `fakeRunner`/`recordingLedger` 逐行构造。
  - T3.9 的 `blockingRunner` 是完整代码；断言逐条可判；复用 `b390_wake_stall_test.go#alwaysNoSlotScheduling`、
    `readWakeRoundComments`、`b390NeedsHumanCount`、`waitFor`、`appendMirroredForConsumer`、`prebindConsumerSession`。
  - T4.1/T4.3/T4.4 的 runner 字段扩展是**精确代码**（非占位）。
  - 各新测试文件用到的同包既有辅助已逐处点名（见 T3.9 注、T2.4 前置依赖）；Go import 按文件，实现卡需在
    新文件各自补 import（已注明 `database/sql`、`fmt`、`sync` 等）。
- **内部锁声明**：唯一内部锁是 T4.7 的 `WakeRoundEvent` round-trip（序列化边界），它以**附加**形态存在，
  不顶替任何缝级断言。其余锁定测试入口全在声明缝上：
  - `hostapi.Host.RunTurn`（T1.6）
  - `keystone.Service.Wake`（T2.4/T2.5，prompt 与 ref 观察都经该入口的 Resume）
  - `agentd.Server.consumeAutomationEventsOnce` / `drainIgnitionRequest`（T3.9）
- **退路声明**：T3.9 的续租测试依赖「Resume 阻塞」来控制回合时长——若环境导致 `waitFor` 超时，**不得**改成
  直接调 `startWakeClaimRenewal` 的内部锁（那会改变入口符号）；处置是排查夹具（认领行为何未落）而非降格入口。
  无其它条件退路。
- 无 TBD /「加适当的错误处理」/「同 Task N」式占位；每条测试给出完整代码块或列全断言。

## 6. 自审三查

1. **spec 覆盖（逐条指到 task）**：
   - §3 第 1 条（30m + 回合内认领续租）→ T3.1/T3.2/T3.3/T3.4/T3.9-1/2/3。
   - §3 第 2 条（超时仍续接同一 session；只有 Session not found 才 Launch）→ T1.1-T1.5、T2.1/T2.2、T2.4。
   - §3 第 3 条（简报只带增量、删「读卡」）→ T2.3、T2.5。
   - §3 失败收口（到点杀进程、会话保留、恰一次需要人、下一轮续接）→ T3.9-4（沿用既有 completeWakeBatch/backoff 收口）。
   - §5 错误分流是契约（三类）→ T1、T2、T3.5/T3.6。
   - §5 失败收口恰一次 + 补 end 行 → T3.5/T3.6/T3.9-4。
   - §6 接缝 1（上界与错误分流）→ T1.6、T2.4、T3.9-3。
   - §6 接缝 2（简报）→ T2.5。
   - §6 接缝 3（可见性与收口对称）→ T3.9-4。
   - §6 不变量改写（`TestB393WakeTurnTimeoutWithinLease` 按新不变量）→ T3.9-1/2/3、T4.5。
   - §6 回归改写（`TestB393ResumeHangsBoundedByTimeout`、`b393_timeout_bound_test.go`）→ T4.6（hostapi 侧
     `b393_timeout_bound_test.go` 不受影响：仍断言报生效值，本计划不改其断言）。
   - §7 Out of Scope（不做 A、不动租约语义重写、不动 Launch 语义、不动唤醒映射/派发/路由）→ T 未触碰这些面；
     唯一新增机制是认领续租（用既有 `ClaimWake` 同 holder 续期，**不新增账本方法、不改 schema**）。
2. **占位符扫描**：见 §5，已声明。
3. **跨 task 类型/签名一致性**：`hostapi.ErrTurnTimeout`/`ErrSessionNotFound`（T1.1）→ `classifyRunnerErr`（T1.5）→
   `keysclient.ErrTurnTimeout`/`ErrSessionNotFound`（T1.4）→ `wakeFailClass`（T3.6）→ keystone `errors.Is`（T2.1）——
   五处逐字对齐；`WakeRoundEvent.Class *string`（T3.8）↔ round-trip（T4.7）；`writeWakeRoundFail(card,roundID,class,reason)`
   定义（T3.6）↔ 14 个调用点（T3.5/T3.7）；`wakeClaimTTL`/`wakeClaimRenewInterval`（T3.2）↔ 续租（T3.3）↔ 测试（T3.9-1/2）。

## 7. 五项检查

1. **缺陷族对抗审查（逐族结论进验收栏）**：
   - 静默失败族：失败分支必写 fail 行 + end 日志；哨兵分类保证不误吞（T3.5/T3.6）。
   - 同卡双跑族：本卡核心——续租使 5m 认领在本轮不失效（T3.9-1 直接锁）。
   - 有界退出族：30m 上界仍在（`readyCtx` 自持期限），T4.6 保留有界红绿。
   - 幂等/重复族：`EnsureComment` 的 `wrote` 去重 needs_human；`ClaimWake` 同 holder 续期幂等（T3.9-4）。
   - 序列化边界族：`WakeRoundEvent.Class` 的缺失/零值两态经 round-trip 锁（T4.7）。
2. **序列化边界设问**：本卡新增字段只有 `WakeRoundEvent.Class`，链路 = agentd 写 `json.Marshal` →
   `EnsureComment` body → 测试/消费方 `json.Unmarshal`；T4.7 的 round-trip 用可空指针区分缺失与零值。
3. **上下文预算检查**：每个 task 文件集有界（T1：hostapi+keysclient+agentd 各 1 文件；T2：keystone 1 文件；
   T3：agentd 2 文件；T4：4 个既有测试文件），圈得出。
4. **类型标注**：本卡非「边界型子系统」新增——不新增跨语言/跨进程 wire 字段；`RoundResult` 的 Go 字段不变，
   `CoordinatorWakeResp` wire 不变（T3 不新增响应字段）。
5. **接缝覆盖（双向）**：
   - 测试 → 缝：T1.6→`Host.RunTurn`；T2.4/T2.5→`Service.Wake`；T3.9-1/2/3/4→`consumeAutomationEventsOnce`/`drainIgnitionRequest`。
   - 缝 → 测试：spec 三条缝各至少一支缝级断言锁住（缝 1：T2.4 + T3.9-3；缝 2：T2.5；缝 3：T3.9-4）。
   - 内部锁：仅 T4.7（round-trip）以内部锁形态附加，不顶替缝级断言（已声明）。

## 8. 发现 / 残余（本计划记录，不改代码）

**跨机转交唤醒的 HTTP 写超时 11m < 新上界 30m（本节点读码发现，未做 11 分钟真机验证）**：
- `cmd/agentd.go:441-449` `WriteTimeout = workspace.RunCmdTimeout + 1m = 11m`；`/coordinator/launch`（`coordapi.go:123`）
  与转交的 `/coordinator/wake`（`coordapi.go:209`）在 handler 内**同步**跑完整回合。
- 推论（**读码事实，非实测**）：跨机唤醒（B382 现场正是走「转交唤醒」到 linux-01）若回合 >11m，被叫机的响应写会
  在 11m 后失败，发起机 `transferCoordinatorWake` 记一条 fail 并退避；而被叫机因 `readyCtx` 用
  `context.Background()`（不绑 `r.Context()`），回合仍会跑完并按重建落席位。
- 影响：发起机出现「假失败标记」（对端已成功）。此族在 `docs/roadmap.md:817` 已记为 B389 面残余。
- **本计划不改** HTTP 写超时/wire（spec §2「不改任何 wire 契约」、§7 未含此项）。建议：B399 合入后单开卡处理
  （在转交 handler 用独立长超时或异步化，需回 spec 定级）。**不阻塞本卡**——跨机回合即使响应超时，会话与席位
  仍被正确续接/重建，核心目标（唤醒不再永久失败）成立。

## 9. 图覆盖债（本节点）

`codegraph sym` 未命中（记债，回退 grep 取源码与签名）：`wakeClaimTTL`、`WakeRoundEvent`、
`WakeRoundDedupePrefix`、`ClaimWake`、`completeWakeBatch`、`WakeClaimsBefore`、`classifyRunnerErr`、
`startWakeClaimRenewal`、`wakeFailClass`、`ErrTurnTimeout`、`ErrSessionNotFound`、`coordWakeTurnTimeout`、`readyCtx`。
命中：`n_keystone_Service_Wake`、`n_keystone_Service_rebuildAfterResumeFailure`、`n_keystone_Service_briefing`、
`n_hostapi_Host_RunTurn`、`n_agentd_NewCoordinatorRunner`、`m_agentd_coordinatorRunner`、
`n_ledger_Store_RenewDriverLease`、`n_agentd_Server_consumeAutomationEventsOnce`、`n_keystone_Service_LaunchForCard`。
`flow Wake` 返回 `degraded=true, steps=0`（基线无 flows 段），已按纪律读源码补控制流。
本计划不新增跨域依赖方向：新增符号落在 `internal/hostapi`、`internal/keysclient`、`internal/agentd`、
`internal/keystone` 既有域内；`hostapi` 仍不 import `keysclient`/`keystone`，翻译在 agentd 组装点完成。
