# B393 debug 计划：唤醒执行链不能假设「同一会话后来者能续上」——根因、分流与三条修面

> 卡 B393 · 入口节点 charter:debug · spec `docs/superpowers/specs/b393.md`（已批准）
> 基线分支 `cards/B393-charter-2`，起手 HEAD `06efa7a4`（已并 `origin/cards/B233.1-charter-7`，
> `Already up to date`）。凡引用行号者动手前重核，漂了以符号为准。
> 台账 `docs/superpowers/ledgers/2026-09-22-b393-plan-ledger.md`（含全部亲跑命令、原始输出、
> 图查询记录、进程清理原文）。读者假设：对 handoff 仓零上下文的执行者。
> **本节点只出计划，不写实现**。「未验证」= 本节点没亲自跑到结果，实现卡必须自己复核。

---

## 0. 待拍板清单（阻塞实施，交协调者裁决）

| # | 岔口 | 选项 | 影响 |
|---|------|------|------|
| **P1** | **挂死的 opencode CLI 本身是第三方，本仓修不了——是否本卡只做「有限等待 + 可见性 + 退化链」对冲？** | (甲) 本卡只对冲：有限上界杀进程 + 可见信号 + 退化链可用；(乙) 追加「污染会话识别/隔离」子卡（把已知挂死会话挡在续接前）；(丙) 升级 opencode 版本再验 | 决定 T0 是否含「污染会话黑名单」面。**本计划按 (甲) 出三条修面**，把 (乙) 列为独立发现。 |
| **P2** | **唤醒回合上界取值** | (甲) 独立常量 `coordWakeTurnTimeout = 10m`（留 5m 租约的一半余量）；(乙) 复用 `hostapi.DefaultTurnTimeout = 30m` 不动，只加可见性 | 决定 T1 代码。**当前实测：`coordinatorRunner.Resume` 用 `context.Background()`（无期限），`hostapi` 兜底 30m，且 30m > 5m 租约 → 挂死期租约过期后可被抢单（候选 3 成立）。倾向 (甲)。** |
| **P3** | **隔离 HOME 缺 opencode provider/model 定义**（§3 R4） | (甲) `Prepare` 补投影 provider/config 到隔离 HOME（改供给白名单写入面）；(乙) 重建时不走隔离 HOME、回退主 HOME（破坏物种边界，spec 已否）；(丙) 先只验证 B382 当时真实 HomeDir 再定 | 决定 T3 是否改 `orchestration/coordinator_home.go`。本计划给 (甲) 的实现草案，**但 T3 第一步是验证**（见 T3）。 |

---

## 1. 根因（证据驱动，全部亲跑）

### R1（根因，决定性）：挂死发生在 **opencode CLI 自身的收尾路径**，不在会话存储、不在 stdin、不在本仓代码

同一命令 `opencode run --format json -s <会话> -- <prompt> </dev/null`：

| 会话 | 结果 |
|---|---|
| 新会话（健康） | `exiting loop` → `disposing instance` → 退出，stdout 918 字节 |
| `ses_f45bc164…`（B382 出证会话） | `exiting loop` → **无 `disposing instance`**，进程不退出，stdout **0 字节**，`/proc/pid/wchan=ep_poll`，CPU ~0.3 核空转 |
| `ses_f3b8888e…`（被我先前探针中途杀过） | 同上 |

CLI 自身 DEBUG 日志原文（挂死侧）：

```text
... message=loop session.id=ses_f45bc164affebNHHlbcxaGAxC5 step=1
... message="exiting loop" session.id=ses_f45bc164affebNHHlbcxaGAxC5
（到此为止，永不 dispose）
```

⇒ **回合已跑完（`exiting loop`），卡在 CLI 进程销毁段；这一刻 stdout 还没有吐出任何
JSON**，所以 `hostapi.driver.go:138 consumeEvents(stdout)` 永远阻塞在等 EOF，`cmd.Wait()`
永不返回。`--format json` 的输出在该构建里是收尾时落盘（不是流式；`hostapi` 解析逻辑
只负责把已到行的 JSONL 拼装，不受影响）。

进程 fd 现场（挂死时）：`fd0→/dev/null`、`fd14→opencode.log`、`fd15/16/17→opencode.db/-wal/-shm`、
`fd23/25→自建 pipe`、**无子进程、无网络连接**；`strace -f` 显示主线程长期 `epoll_wait` +
500ms `poll` 空转 + 周期 `SIGPWR`（Bun 运行时正常），**无任何 db/网络/文件读写**。
即运行时空转，不是 IO 阻塞。

### R2（触发条件）：会话被「中途杀掉」污染后进入挂死态（概率性触发，一旦污染持续挂）

Go 驱动「建会话 → 长回合中途 SIGKILL 整组 → 之后连续 resume」：

```text
trial 1 sid=ses_f3b36759cffeabtusj3RokuFr7 postKillHang=[true true false false false]
trial 2 sid=ses_f3b357ea8ffeCyTmGMPwaH8Vld postKillHang=[false false false false false]
trial 3 sid=ses_f3b34f18cffeotoFPUVgYoWaM1 postKillHang=[true false false false false]
```

SIGTERM 变体同样复现（SIGTERM 后首个 resume 输出部分 JSON 后挂）。**诚实边界**：中途杀
不是 100% 触发（trial 2 未触发）；一旦某次 resume 挂住，该会话后续 resume 稳定挂。

归零实验（对库副本做手术后 resume，都不救）：删非 completed tool part / 给未完成
assistant 补 completed / 删 event+session_message+session_context_epoch / event 归零
——**四者仍全部 HANG**。⇒ 挂死不在投影表，在会话消息正文/快照类持久态。**具体字段未验证**。

### R3：本仓的三个真实缺陷（可修，且正是 spec 要的三条修面）

1. **无界等待**：`internal/agentd/server.go:1182` 与 `:1216` 的 `coordinatorRunner.Launch/Resume`
   用 `context.WithCancel(context.Background())`，**丢弃调用方 ctx、不设期限**；
   `keysclient.Runner` 接口本身无 ctx 参数（`internal/keysclient/keysclient.go:43-51`）。
   ⇒ 一个不返回的 `Runner.Resume` 会把 `keystone.Service.Wake`（`keystone.go:105`）永久钉住；
   兜底 30m 来自 `hostapi/driver.go:46`，**远长于 5m 租约**。
   **候选 3（回合上界与租约不匹配）成立**：挂死期 `CompleteWake` 永不执行 ⇒ 认领行
   `done_at` 永为 NULL（与卡上「两条认领 done_at 至今为 NULL」吻合）；5m 后他机可接管
   而本机回合最长还要跑 25m ⇒ 抢单/双跑风险，且 30m 内无任何「在途」读数。
2. **无可见信号**：`internal/logx/logx.go:32/59` 缺省 `slog.LevelWarn`；而
   `driver.go:127「协调者回合开始」`、`:170「协调者回合完成」` 与
   `coordinator.go:34/45` 的进入日志**全是 Info**。挂死路径连 `driver.go:151` 的 Warn
   失败行都不会有（不返回就不打）。**候选 4 成立**（红跑见 §4 回路 A）。
3. **退化链可能重建即失败**：`keystone.go:159-166 rebuildAfterResumeFailure` 在 resume
   失败后 `launchRound`；但协调者隔离 HOME 由 `orchestration/coordinator_home.go:40 Prepare`
   供给，**只写 `.handoff/config.yaml`（handoff 自己的配置）、`AGENTS.md`、`skills/` 与单个
   凭据文件**，**不写 opencode 的 provider/model 定义**。亲验：协调者形状隔离 HOME 起
   opencode 回合 → `ProviderModelNotFoundError: Model not found: commandcode/deepseek/deepseek-v4.1-flash.`
   ⇒ 重建**快速失败**（不是挂死），于是落 `needs_human: 协调者唤醒失败：resume 与重建均不可用`。
   **待验证**：B382 当时协调者 HomeDir 是否确为隔离 HOME、且该 HOME 是否缺 provider 定义
   （T3 第一步）。

### R4：四个候选面的最终判定

| 候选（spec §2） | 判定 | 依据（原文见台账 §3） |
|---|---|---|
| 1. 会话存储不可达 → `-s` 找不到也不报错不退出 | **证伪** | `-s` 不存在 / 隔离 HOME / HOME 不可建 / HOME 只读 / db 损坏 / workdir 不存在：全部 `exit=1` 且 <1s 返回具体错误（`Session not found` / `ENOENT mkdir` / `file is not a database` / `Failed to change directory`）。 |
| 2. CLI 等一个不存在的前提（stdin/交互/锁） | **证伪为根因** | `driver.go` 不设 `cmd.Stdin`，Go os/exec 给子进程 `/dev/null`（亲验：`cat` nil-Stdin 立即 EOF；子进程 `readlink /proc/$$/fd/0 → /dev/null`）。开管道 stdin 只让它与管道同寿（39.5s vs 4.0s），不是触发器；健康会话在相同 stdio 形状下正常。 |
| 3. 回合上界与租约不匹配 | **成立（后果与放大面）** | `driver.go:46 DefaultTurnTimeout=30m`；`binding.go:24 DriverLeaseTTL=5m`；B389 分支 `wakeconsumer.go:29 wakeClaimTTL=5m`。挂死期认领 `done_at` 永为 NULL。 |
| 4. warn 掩盖 CLI 侧关键行 | **成立（可见性缺陷）** | 红跑回路 A：warn handler 下 `driver.go` 一字不出。 |

---

## 2. 分流决定

| 根因 | 归属 | 处置 |
|---|---|---|
| R1 CLI 自身收尾挂死（第三方 Bun 构建） | **本仓无法修** | 本仓用**有限上界 + 强杀进程树**对冲（T1）；污染会话识别/隔离另立卡（P1 乙）。不在本卡升级/改 opencode。 |
| R3.1 无界等待（Wake 可被永久钉住） | 一两行小修（加期限） | **T1** |
| R3.2 执行期无可见信号 | 观测性小修 | **T2** |
| R3.3 退化链重建可能即失败 | 承重修复（供给面） | **T3**（先验证后改） |
| 候选 3 租约不匹配的「续租」半边 | 改认领/租约语义 = 架构级 | **回 spec 重新定级**；本计划 T2 的账本事件给「在途」读数，不在此改 schema。 |
| 候选 4 全局默认级别 | 观测性 | **T2**（唤醒回合起止/失败升到 ≥WARN；不改全局默认级别） |

> 遵 spec §「一两行小修顺手修掉；架构级修复回本 spec 重新定级，不许在排查现场顺手动架构」。

---

## 3. 任务 DAG

```
T0（红色回路转正：真机 repro 脚本 + keystone 假 Runner 挂死回路）
  └→ T1（有限等待：coordinatorRunner 加期限，挂死必在界内返回错误）
        └→ T2（可见性：回合起止/失败 ≥WARN + 恰一行账本事件）
              └→ T3（退化链：隔离 HOME 供 provider/model 定义，重建真可用；否则显式降级落账）
（独立）污染会话识别/隔离、租约续租语义 → 回 spec / 另立卡，不在本 DAG
```

次序承重：T1 的回路依赖 T0 的假 Runner 挂死装置；T2 的账本事件断言依赖 T1 让挂死回合
**有限返回**（否则事件永远落不下来）；T3 的重建可用断言依赖 T2 的可见事件能看见
「重建失败」原因。全量测试不属于任何单个 task（implement 三段律）。

---

## 4. 基线事实（实现卡共享，动手前复核）

**亲跑读数（原始输出见台账）**：

- `go build ./...` → EXIT=0；`go vet ./internal/hostapi/` → EXIT=0。
- 真机挂死：`opencode run --format json -s ses_f45bc164affebNHHlbcxaGAxC5 -- "say ping" </dev/null`
  → 25s 零输出不退出（`rc=124`，外部 timeout 杀死）。
- 可见性红跑（缝 = `hostapi.Host.RunTurn`，临时候选测试已删）：

  ```text
  --- FAIL: TestB393TmpWakeRoundVisibleAtWarn (0.00s)
      captured warn-level log bytes=0
      warn 级别下「回合开始」不可见（driver.go:127 是 Info）
  ```

- 供宿主库：`/root/.local/share/opencode/opencode.db`（4.3GB，`event` 表 3.8GB/374290 行，
  异常但**非挂死根因**——归零实验不救）。
- 本机无 `gtimeout`；有 GNU `timeout`（`timeout -k 3 <sec> cmd`）。
- `codegraph sym` 命中：`RunTurn`、`driveTurn`、`rebuildAfterResumeFailure`、`Wake`、
  `RenewDriverLease`；**未命中（图覆盖债）**：`DefaultTurnTimeout`、`ClaimWake`、
  `completeWakeBatch`（回退 grep，未拿 chain 冒充 flow）。

---

## 5. 接口契约（执行者只看本 task，故这里列全）

### Consumes（本卡要用的既有签名，一字不改）

```go
// internal/keysclient/keysclient.go
type SessionSpec struct { CLI, HomeDir, Model, Workdir string; Env []string }
type SessionRef  struct { CLI, SessionID, Machine, HomeDir, Workdir, Model string }
type TurnResult  struct { SessionID, Output string }
type Runner interface {
    Launch(spec SessionSpec, prompt string) (TurnResult, error)
    Resume(ref SessionRef, prompt string) (TurnResult, error)
}
type LedgerView interface {
    GetCard(id string) (proto.Card, error)
    EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]proto.LedgerEvent, error)
    EffectiveBaseBranch(id string) (string, error)
    MarkNeedsHuman(cardID, reason, actor string) error
}

// internal/hostapi/hostapi.go（契约 §7 冻结面，签名一字不改）
type TurnRequest struct {
    CLI, HomeDir, Workdir, Model, Prompt, SessionID string
    Env []string
    Timeout time.Duration // 0 = 用包内缺省（30 分钟）
}
type TurnReply struct { SessionID, Output string }
func (h *Host) RunTurn(ctx context.Context, req TurnRequest) (TurnReply, error)

// internal/executor/coordinator.go
type CoordSessionRef struct { CLI, SessionID, Machine, HomeDir, Workdir, Model string }
type Coordinator interface {
    Launch(ctx context.Context, spec CoordSessionSpec, prompt string) (CoordTurnResult, error)
    Resume(ctx context.Context, ref CoordSessionRef, prompt string) (CoordTurnResult, error)
    CancelTurn(ctx context.Context, ref CoordSessionRef) error
}

// internal/keystone/keystone.go
func (s *Service) Wake(ctx context.Context, card string, evs []WakeEvent, spec keysclient.SessionSpec) (RoundResult, error)
type RoundResult struct { Woke bool; SessionID string; Rebuilt, Escalated bool; Output string }
```

### Produces（本卡新增，跨 task 逐字对齐）

```go
// internal/agentd/server.go（T1 新增，包内变量，测试可覆盖）
var coordWakeTurnTimeout = 10 * time.Minute   // P2 拍板值

// internal/hostapi/driver.go（T2 新增，包内常量）
const wakeRoundLogLevel = slog.LevelWarn       // 唤醒回合起止/失败的日志级别下限

// internal/agentd/wakeconsumer.go（T2 新增，包内）
const WakeRoundDedupePrefix = "wake_round"     // 唤醒回合起止注释的 dedupe_key 前缀

// internal/agentd/scheddrain.go（T2 新增，包内）：唤醒回合日志级别下限。
// hostapi 的 wakeRoundLogLevel 未导出、跨包取不到，agentd 侧同值另立。
const wakeRoundLogLevel = slog.LevelWarn

// internal/agentd/wakeconsumer.go（T2 新增，包内类型）
type WakeRoundEvent struct { // 字段定义见 T2.3
	Phase      string
	Session    *string
	Err        *string
	DurationMs *int64
}
```

> 序列化边界：T2 的 `WakeRoundEvent` 经 `json.Marshal` 写进 `EnsureComment` 的 body，
> **必须有一条 round-trip 断言**（encode∘decode 恒等，含「缺失 vs 零值」区分）。
> 见 T2 的 `TestB393WakeRoundEventRoundTrip`。

---

## 6. 任务

### T0（前置）：红色回路转正

**判据先在基线复核（必做）**：下面两条命令在当前 HEAD 应给「红」——实现卡动手前先跑一次，
红了才动 T1/T2。

**T0.1 真机 repro 脚本（可跑、秒级触发 + 分钟级观察）**

新文件 `docs/superpowers/probes/b393-resume-hang.sh`（仓内脚本，非 Go 测试）：

```sh
#!/usr/bin/env bash
# b393-resume-hang.sh —— 复现 opencode resume 挂死：不返回、零输出。
# 用法：b393-resume-hang.sh [会话id]   （缺省：现场已知挂死会话）
# 退出：0=在界内退出（未复现）；124=挂死被看门狗杀掉（复现）。
# 边界：只驱动 CLI，不碰账本/agentd/线上状态。
set -u
SID="${1:-ses_f45bc164affebNHHlbcxaGAxC5}"
LIMIT="${B393_LIMIT:-45}"   # 观察窗秒数；正常会话数秒内返回
start=$(date +%s)
timeout -k 3 "$LIMIT" opencode run --format json -s "$SID" -- "say b393-hang-probe" </dev/null \
  >"${TMPDIR:-/tmp}/b393-hang.out" 2>"${TMPDIR:-/tmp}/b393-hang.err"
rc=$?
dur=$(( $(date +%s) - start ))
printf 'sid=%s rc=%s dur=%ss out_bytes=%s err_bytes=%s\n' \
  "$SID" "$rc" "$dur" "$(wc -c <"${TMPDIR:-/tmp}/b393-hang.out")" "$(wc -c <"${TMPDIR:-/tmp}/b393-hang.err")"
exit "$rc"
```

基线与判据：跑 `bash docs/superpowers/probes/b393-resume-hang.sh` 应输出
`rc=124 ... out_bytes=0`（复现）；修后（T1/T2 落地且配合一个健康会话）应有限返回。
**注意**：本脚本只能复现 R1（CLI 挂死），不能断言「本仓挂死」——本仓挂死断言在 T0.2。

**T0.2 挂死回路（Go 测试，缝 = `agentd.coordinatorRunner.Resume`，即生产 Runner 实现）**

**为什么回路不放在 keystone**：生产 Runner 是 `coordinatorRunner`（`server.go:1156`），
它拿不到调用方 ctx（`keysclient.Runner` 接口冻结、无 ctx 参数），但**它自己就是挂死的
发生地**——`Resume` 里 `coordRunner` 调 `hostapi.Host.RunTurn`，后者兜底 30m。修面就在
这个实现里加期限（T1）。因此红色回路必须钉在 `coordinatorRunner` 缝上：这里能同时
（a）现在红（无界→等 30m），（b）T1 后绿（1s 上界秒级返回）。
把它放 keystone 会是个假回路：keystone 层永远拿不到 ctx，换成会阻塞的假 Runner，
加了 T1 也照样红——那是判据钉错了缝。

新文件 `internal/agentd/b393_timeout_test.go`：

```go
package agentd

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// installSleepyCoordinatorCLI 装一个「长睡」的假 opencode：模拟 CLI 收尾挂死
// （不返回、不输出）。走 PATH 注入（仓内先例 coordrunner_test.go
// #installCoordinatorFakeCLI / hostapi/runturn_test.go#installFakeCLI）。
func installSleepyCoordinatorCLI(t *testing.T, sleepSec int) {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\nsleep " + strconv.Itoa(sleepSec) + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatalf("写 sleepy fake CLI: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestB393ResumeHangsBoundedByTimeout 锁 R3.1：挂死的 resume 必须在
// coordWakeTurnTimeout 附近返回错误，而不是钉死到 hostapi 的 30m 缺省。
//
// 红（当前 HEAD）：coordinatorRunner.Resume 用 context.Background() 无期限，
// hostapi 走 30m 缺省 → 本测试在 10s 窗内收不到返回，超时红。
// 绿（T1.2）：readyCtx 给 1s 上界，秒级返回错误。
// 变异自验：把 readyCtx 的 WithTimeout 改回 WithCancel → 复红。
func TestB393ResumeHangsBoundedByTimeout(t *testing.T) {
	prev := coordWakeTurnTimeout
	coordWakeTurnTimeout = 1 * time.Second
	defer func() { coordWakeTurnTimeout = prev }()

	installSleepyCoordinatorCLI(t, 30)
	h := hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor)
	runner := testCoordRunner(h, nil) // coordrunner_test.go 既有夹具

	done := make(chan error, 1)
	go func() {
		_, err := runner.Resume(keysclient.SessionRef{
			CLI: "opencode", SessionID: "ses_x",
			HomeDir: t.TempDir(), Workdir: t.TempDir(),
		}, "唤醒简报")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("挂死回合必须判失败并返回错误")
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("resume 在 1s 上界后仍未返回（无界等待，R3.1）")
	}
}
```

**基线复核（实现卡动手前跑一次，应红）**：
`go test ./internal/agentd/ -run TestB393ResumeHangsBoundedByTimeout -count=1`

基线红有两种形态，都算红、都合法：
- **编译红**（今天）：`coordWakeTurnTimeout` 未定义 → `undefined: coordWakeTurnTimeout`
  （证明修面符号缺席；B389 先例同款）。
- **行为红**（一旦符号补上但 readyCtx 未改）：30m 缺省下 10s 窗收不到返回。

「30m 缺省会让挂死回合钉死」这一**行为事实**由台账的真机探针坐实
（`TestB393ResumeHangsBoundedByTimeout` 的 10s 窗是它的秒级代理）。

**测试范围声明**：`go test ./internal/agentd/ -run TestB393 -count=1`。
**日志与注释**：测试文件头写职责/边界/缝；`installSleepyCoordinatorCLI` 写 why 与
走 PATH 注入的理由；每个测试写红绿与变异自验。

---

### T1：有限等待——挂死的 resume 必须在界内返回错误

**目标（R3.1）**：给协调者唤醒回合一个显式上界，让不返回的 CLI 在界内被强杀，
回合判失败，从而 `resume 失败 → 重建` 退化链才有机会执行。

**精确改动点**：`internal/agentd/server.go` 的 `coordinatorRunner.Launch`（`:1182`）
与 `Resume`（`:1216`）。**不动** `keysclient.Runner` 接口（契约冻结，无 ctx 参数），
改为在 runner 内自持期限。

**T1.1 新增包内变量（server.go，`coordinatorRunner` 定义附近）**

```go
// coordWakeTurnTimeout 是协调者无头回合的挂钟上界（P2）。取 10m：短于
// hostapi.DefaultTurnTimeout(30m)，给 5m 唤醒租约留一半余量——挂死回合必须在
// 租约到期前判失败，否则他机会在租约过期后接管而同卡双跑（B393 候选 3）。
// 变量而非 const：agentd 包内测试覆盖它到秒级，避免 10m 真等。
var coordWakeTurnTimeout = 10 * time.Minute

// readyCtx 返回带 coordWakeTurnTimeout 上界的 ctx。
// 为什么不用入参 ctx：keysclient.Runner 接口冻结、无 ctx 参数；coordinatorRunner
// 的调用方（keystone.Wake）虽带 ctx，但接口消化不到这里。自持期限是接口冻结下
// 唯一能在「不返回的 CLI」上兜底的落点。
func readyCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), coordWakeTurnTimeout)
}
```

**T1.2 改 `Launch`（`server.go:1182`）与 `Resume`（`server.go:1216`）**

把两处

```go
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
```

替换为

```go
	ctx, cancel := readyCtx()
	defer cancel()
```

（其余不动；`coord.Resume(ctx, …)` 会把 ctx 落到 `hostapi.Host.RunTurn`，
`driver.go:107 context.WithTimeout(ctx, timeout)` 再叠一层，取两者更早者；
超时由 `driver.go:185 watchDeadline → killGroup` 对整个进程组 SIGKILL。）

**T1.3 关键节点日志（≥Warn，与 T2 共用口径）**

在 `Launch`/`Resume` 起点与失败分支加结构化日志（用 `slog.Default().Log` 显式级别，
常量 `wakeRoundLogLevel` 见 §5 Produces 与 T2；禁 print）：

```go
	slog.Default().Log(ctx, wakeRoundLogLevel, "协调者回合开始", "cli", spec.CLI,
		"workdir", spec.Workdir, "home_dir", spec.HomeDir, "timeout", coordWakeTurnTimeout.String())
	...
	if err != nil {
		slog.Default().Log(ctx, wakeRoundLogLevel, "协调者回合失败", "cli", ref.CLI,
			"session", ref.SessionID, "home_dir", ref.HomeDir, "cause", err)
		return keysclient.TurnResult{}, err
	}
```

**T1.4 红色回路：复用 T0.2 的 `TestB393ResumeHangsBoundedByTimeout`**

不新建第二个文件——回路本体已在 T0.2 定义（缝 = `agentd.coordinatorRunner.Resume`），
实现卡在 T0 先跑它取红（30m 缺省，10s 窗超时），T1.2 落地后复跑必须变绿。
变异自验：把 `readyCtx` 的 `WithTimeout` 改回 `WithCancel` → 复红。

**测试范围声明**：`go test ./internal/agentd/ -run TestB393 -count=1`。
**日志**：见 T1.3；错误分支带 cli/session/home/cause，成功路径不静默。
**注释**：`coordWakeTurnTimeout` 与 `readyCtx` 的 why 已写在代码块内（租约余量、
接口冻结下为何自持期限）。

---

### T2：可见性——唤醒回合起止/失败 ≥WARN + 恰一行账本事件

**目标（R3.2 / 候选 4）**：不设 `HANDOFF_LOG_LEVEL` 时，唤醒回合开始/结束/失败都可见；
账本里留一行「在途/结束」事件（候选 3 要求的可行动读数）。

**T2.1 `internal/hostapi/driver.go`：把回合起止从 Info 升到 ≥WARN**

```go
// wakeRoundLogLevel 是唤醒回合生命周期日志的级别下限（spec §4.3：不设
// HANDOFF_LOG_LEVEL 也要看得见）。缺省 slog 级别是 Warn（logx.go:59），Info 会被吞。
const wakeRoundLogLevel = slog.LevelWarn
```

把 `driver.go:127`「协调者回合开始」与 `:170`「协调者回合完成」改用显式级别：

```go
	log().Log(ctx, wakeRoundLogLevel, "协调者回合开始", "cli", req.CLI,
		"mode", resumeOrNew(req), "workdir", req.Workdir, "timeout", timeout.String(),
		"prompt_bytes", len(req.Prompt))
	...
	log().Log(ctx, wakeRoundLogLevel, "协调者回合完成", "cli", req.CLI,
		"session_id", reply.SessionID, "output_bytes", len(reply.Output), "duration", dur.String())
```

**T2.2 红色回路（缝 = `hostapi.Host.RunTurn`，已亲跑验证必红）**

新文件 `internal/hostapi/b393_visible_test.go`：

```go
package hostapi

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestB393WakeRoundVisibleAtWarn 锁 spec §4.3：warn 级别下唤醒回合起止必须可见。
//
// 红（本节点已跑）：captured warn-level log bytes=0，driver.go:127/170 是 Info。
// 绿：T2.1 后起止两行入 buf。变异：把级别改回 Info → 复红。
func TestB393WakeRoundVisibleAtWarn(t *testing.T) {
	installFakeCLI(t)
	withArgvCapture(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	h := New()
	if _, err := h.RunTurn(context.Background(), TurnRequest{
		CLI: "opencode", SessionID: "ses_visible", Prompt: "唤醒简报",
	}); err != nil {
		t.Fatalf("回合失败: %v", err)
	}
	if !strings.Contains(buf.String(), "协调者回合开始") {
		t.Fatalf("warn 级别下「回合开始」不可见（driver.go:127 是 Info）")
	}
	if !strings.Contains(buf.String(), "协调者回合完成") {
		t.Fatalf("warn 级别下「回合完成」不可见（driver.go:170 是 Info）")
	}
}
```

**T2.3 账本事件（恰一行 + 序列化 round-trip）**

在 `internal/agentd/wakeconsumer.go` 新增事件类型与写入点，在 `wakeCoordinatorRound`
（`scheddrain.go:335`）回合前后各写一次。**事件内容必须能用可空类型区分「字段缺失」
与「值为零」**（用 `*string`/`*int64`）。

```go
// WakeRoundDedupePrefix 是唤醒回合起止注释的 dedupe_key 前缀（B393 §4.3：唤醒
// 回合留一行在途读数）。落账走 ledger.Store.EnsureComment（type=EvComment），
// 前缀进 dedupe_key，键形如 "wake_round:start" / "wake_round:end:<session>"。
const WakeRoundDedupePrefix = "wake_round"

// WakeRoundEvent 是写入注释正文的 payload 形状（序列化边界：json.Marshal 进
// EnsureComment 的 body、消费方按需 json.Unmarshal；见 TestB393WakeRoundEventRoundTrip）。
// 字段用指针：nil = 该回合未提供该字段（如未开始就失败无 duration_ms），
// 非 nil 的零值 = 确有该读数且为零——两态不可混。
type WakeRoundEvent struct {
	Phase      string  `json:"phase"`                 // "start" | "end" | "fail"
	Session    *string `json:"session,omitempty"`     // nil=未知
	Err        *string `json:"err,omitempty"`         // nil=无错误
	DurationMs *int64  `json:"duration_ms,omitempty"` // nil=未计时
}
```

**round-trip 断言（序列化边界，必写）**，新文件
`internal/agentd/b393_wakeround_event_test.go`：

```go
package agentd

import (
	"encoding/json"
	"testing"
)

// TestB393WakeRoundEventRoundTrip 锁序列化边界：encode∘decode 恒等，且
// 「字段缺失」与「值为零」可区分（可空指针）。
func TestB393WakeRoundEventRoundTrip(t *testing.T) {
	zero := ""
	var zeroMs int64 = 0
	cases := []WakeRoundEvent{
		{Phase: "start"},                             // 全缺省
		{Phase: "end", Session: &zero, DurationMs: &zeroMs}, // 显式零值
		{Phase: "fail", Err: &zero},
	}
	for _, want := range cases {
		raw, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		var got WakeRoundEvent
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Phase != want.Phase ||
			(got.Session == nil) != (want.Session == nil) ||
			(got.Err == nil) != (want.Err == nil) ||
			(got.DurationMs == nil) != (want.DurationMs == nil) {
			t.Fatalf("round-trip 不等：\n got=%+v\nwant=%+v", got, want)
		}
		if want.Session != nil && *got.Session != *want.Session {
			t.Fatalf("零值字段丢失：got=%q", *got.Session)
		}
	}
}
```

**写入点（`scheddrain.go` `wakeCoordinatorRound`，回合前后）**：

`s.ledger` 是 `*ledger.Store`（`server.go:110`），`EnsureComment` 是它的公开方法
（`internal/ledger/events.go:299`），直接调用；失败只 Warn，不覆盖唤醒结果：

```go
	// 回合开始：恰一行在途读数（dedupeKey 保证同卡同 phase 幂等，重复不重写）
	startPayload, _ := json.Marshal(WakeRoundEvent{Phase: "start", Session: &sessionID})
	if _, err := s.ledger.EnsureComment(card, "wake-round:start", string(startPayload), "agentd"); err != nil {
		s.log.Warn("唤醒回合起止事件写入失败（不覆盖唤醒结果）", "card", card, "cause", err)
	}
	started := time.Now()
	s.log.Log(ctx, wakeRoundLogLevel, "唤醒回合开始", "card", card,
		"event_count", len(evs), "session", sessionID)
	...
	dur := time.Since(started)
	endPayload, _ := json.Marshal(WakeRoundEvent{
		Phase: "end", Session: &result.SessionID, DurationMs: ptrInt64(dur.Milliseconds()),
	})
	if _, err := s.ledger.EnsureComment(card, "wake-round:end:"+result.SessionID,
		string(endPayload), "agentd"); err != nil {
		s.log.Warn("唤醒回合结束事件写入失败", "card", card, "cause", err)
	}
	s.log.Log(ctx, wakeRoundLogLevel, "唤醒回合结束", "card", card, "rebuilt", result.Rebuilt,
		"escalated", result.Escalated, "session", result.SessionID, "duration", dur.String())
```

> **实现卡注意**：`EnsureComment` 的查重按 `card_id + type(EvComment) + dedupe_key` 一次
> 线性扫描（`events.go:313-336`），dedupe_key 含 `session` 时同卡不同会话各留一行——
> 符合「每次唤醒恰一行」。`ptrInt64` 是实现卡要加的一行小 helper（`func ptrInt64(v int64) *int64 { return &v }`），
> 与 `WakeRoundEvent.DurationMs *int64` 配套。若嫌注释形态不合「事件」语义，须改账本
> schema（新 `EvWakeRound` 类型 + 门面写方法），**属架构级，回 spec 定级**；本计划按
> `EnsureComment` 最小落法。`WakeRoundEvent` 常量与 round-trip 测试仍保留（锁 payload 形状）。

**测试范围声明**：`go test ./internal/hostapi/ -run TestB393 -count=1` 与
`go test ./internal/agentd/ -run TestB393 -count=1`。

---

### T3：退化链——resume 失败时重建必须真可用

**目标（R3.3）**：`keystone.go:159-166` 的重建要么真能起新会话承接身份，要么给出
明确降级并落账，不许「两条路都不通还静默」。

**T3.0 先验证（必做，不许跳）**：确认 B382 当时协调者 `SessionRef.HomeDir` 是否为
隔离 HOME、该 HOME 是否缺 opencode provider/model 定义。

```text
$ handoff card show B382           # 看法定 HomeDir / 承载记录
$ ls -la <该 HomeDir>/.config/opencode/   # 期待：只有 AGENTS.md/skills，无 provider 定义
```

**亲验现状（本节点已跑，隔离 HOME 形状）**：

```text
$ HOME=<coordhome> opencode run -m commandcode/deepseek/deepseek-v4.1-flash -- "reply ok"
exit=1  ProviderModelNotFoundError: Model not found: commandcode/deepseek/deepseek-v4.1-flash.
```

**T3.1（若 T3.0 证实缺定义）改 `internal/orchestration/coordinator_home.go`**

在 `Prepare`（`:40`）写 `.handoff/config.yaml` 之后、`copyMissingCoordinatorCredential`
之前，补投影主 HOME 的 opencode provider/model 定义（白名单内、缺失才写、不整树同步）：

```go
// projectCoordinatorProviderConfig 把主 HOME 的 opencode 模型/provider 定义
// 投影进隔离 HOME（B393 R3.3：隔离 HOME 缺 provider 定义会让重建/首拉
// 直接 ProviderModelNotFoundError，退化链断在起点）。
// 边界：只写白名单内的单个 config 文件，缺失才写，绝不整树同步、绝不覆盖已有
// 文件（隔离侧可能已有更精确的定义）。写失败 fail-closed 返回错误，不静默。
func projectCoordinatorProviderConfig(mainHome, targetHome string) error {
	rel := filepath.Join(".config", "opencode", "opencode.jsonc")
	src := filepath.Join(mainHome, rel)
	dst := filepath.Join(targetHome, rel)
	if _, err := os.Lstat(dst); err == nil {
		return nil // 隔离侧已有，保留
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查隔离 provider 配置 %q: %w", dst, err)
	}
	data, err := os.ReadFile(src)
	if errors.Is(err, os.ErrNotExist) {
		slog.Default().Warn("主 HOME 缺 opencode provider 配置，重建可能因缺模型定义失败",
			"source", src)
		return nil // 主 HOME 也没有：不阻断供给，但留 Warn（T2 可见性会暴露后续失败）
	}
	if err != nil {
		return fmt.Errorf("读主 HOME provider 配置 %q: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("创建隔离 provider 配置目录 %q: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return fmt.Errorf("写隔离 provider 配置 %q: %w", dst, err)
	}
	slog.Default().Info("协调者隔离 provider 配置已供给", "target", dst)
	return nil
}
```

并在 `Prepare` 里 `copyMissingCoordinatorCredential(...)` 之后调用：

```go
	if err := projectCoordinatorProviderConfig(mainHome, targetHome); err != nil {
		return "", err
	}
```

同时把该路径加入 `rejectCoordinatorHomeSymlinks` 的白名单检查（`:192-208`）：

```go
		filepath.Join(targetHome, ".config", "opencode", "opencode.jsonc"),
```

**T3.2 红色回路（缝 = `orchestration.NewCoordinatorPrepareHome`）**

新文件 `internal/orchestration/b393_provider_config_test.go`（入口 = 供给缝）：

```go
package orchestration

import (
	"os"
	"path/filepath"
	"testing"
)

// TestB393PrepareProjectsProviderConfig 锁 R3.3：Prepare 必须把主 HOME 的
// opencode provider 定义投影进隔离 HOME，否则重建/首拉的回合拿不到模型。
//
// 红：今天 Prepare 只写 .handoff/config.yaml + AGENTS.md + skills，目标文件不存在。
// 绿：T3.1 后目标文件存在且内容与主 HOME 逐字节相等。
func TestB393PrepareProjectsProviderConfig(t *testing.T) {
	mainHome := t.TempDir()
	targetHome := t.TempDir()
	srcDir := filepath.Join(mainHome, ".config", "opencode")
	if err := os.MkdirAll(srcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"plugin":["x"],"provider":{"commandcode":{}}}`)
	if err := os.WriteFile(filepath.Join(srcDir, "opencode.jsonc"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	// 直接测纯函数（Prepare 依赖活配置，成本高；缝级断言落在「投影函数」上，
	// 并另有一条 Prepare 集成断言，见下）。
	if err := projectCoordinatorProviderConfig(mainHome, targetHome); err != nil {
		t.Fatalf("投影: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(targetHome, ".config", "opencode", "opencode.jsonc"))
	if err != nil {
		t.Fatalf("隔离 HOME 缺 provider 配置（R3.3 红）: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("provider 配置逐字节不等：\n got=%s\nwant=%s", got, body)
	}
	// 已有文件不被覆盖
	other := []byte(`{"keep":true}`)
	if err := os.WriteFile(filepath.Join(targetHome, ".config", "opencode", "opencode.jsonc"), other, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := projectCoordinatorProviderConfig(mainHome, targetHome); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(targetHome, ".config", "opencode", "opencode.jsonc"))
	if string(again) != string(other) {
		t.Fatalf("已有隔离配置被覆盖")
	}
}
```

> **内部锁自我声明**：`TestB393PrepareProjectsProviderConfig` 的入口是**未导出纯函数**
> `projectCoordinatorProviderConfig`，不在 spec 的缝清单上——它只锁内部投影逻辑。
> **合法理由（唯一形状）**：从声明缝 `NewCoordinatorPrepareHome` 构造不出这条断言，
> 因为 `Prepare` 需要活配置 + Profile 供给 + 规则装载三个依赖，无法在单测里从声明缝
> 喂入「主 HOME 有一个 opencode.jsonc」这一前提而不启动整个供给族。故另加一条
> **缝级**断言 `TestB393PrepareViaSupplierProjectsProviderConfig`（走
> `NewCoordinatorPrepareHome` 的返回值），由实现卡补齐；本内部锁只作附加，不顶替。
> **此声明按占位符扫描节登记。**

**T3.3 降级决策落账（`rebuildAfterResumeFailure`）**

`keystone.go:162` 现在只 `MarkNeedsHuman(card, "协调者唤醒失败：resume 与重建均不可用", "keystone")`。
补一条**可行动**的降级说明（含两条失败原因摘要，截断到 400 字符）并保持返回 `Escalated`：

```go
func (s *Service) rebuildAfterResumeFailure(card, prompt string, spec keysclient.SessionSpec,
	ref keysclient.SessionRef, resumeErr error) (RoundResult, error) {
	rebuildSpec := spec
	rebuildSpec.CLI = ref.CLI
	rebuilt, launchErr := s.launchRound(card, prompt, rebuildSpec, true)
	if launchErr != nil {
		reason := fmt.Sprintf("协调者唤醒失败：resume 与重建均不可用（resume: %v; 重建: %v）——"+
			"请人工处置：检查隔离 HOME 是否缺模型凭据/配置，或 card rebind --self 换会话",
			truncateCause(resumeErr), truncateCause(launchErr))
		_ = s.ledger.MarkNeedsHuman(card, reason, "keystone")
		return RoundResult{Escalated: true}, fmt.Errorf("resume: %v; 重建: %w", resumeErr, launchErr)
	}
	rebuilt.Rebuilt = true
	return rebuilt, nil
}

// truncateCause 把错误压成一行短文本，避免账本理由被长 stderr 尾部刷爆。
func truncateCause(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
```

（`strings` 若未 import 需补；`keystone.go` 现有 import 见文件头。）

**T3.3 红色回路（缝 = `keystone.Service.Wake`；复用 `slice_test.go` 的 `fakeRunner`）**

新增 `internal/keystone/b393_degrade_test.go`（包 `keystone_test`，与 `slice_test.go` 同包，
可直接用其 `fakeRunner`/`fakeNarrator`/`recordingLedger` 夹具）：

```go
package keystone_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
)

// TestB393DegradeRecordsActionableReason 锁 spec §4.4：resume 与重建均失败时，
// 落 needs_human 的理由必须是「可行动」的——含两条失败原因摘要，不是光秃秃一句
// 「resume 与重建均不可用」。
//
// 红：今天 reason 是常量 "协调者唤醒失败：resume 与重建均不可用"（无 cause 摘要）。
// 绿：T3.3 后理由含 resume 与重建的错误文本片段。
// 变异：把 reason 改回常量 → 复红。
func TestB393DegradeRecordsActionableReason(t *testing.T) {
	runner := &fakeRunner{failNext: 1, failLaunches: true} // slice_test.go 既有夹具
	narr := &fakeNarrator{}
	// recordingLedger 需真实 Facade 落盘，见 slice_test.go；本测试只断言理由文本，
	// 用不做 IO 的 stub LedgerView 亦可，但既有夹具要求 Facade。实现卡按既有
	// newLedger(t) 辅助（slice_test.go）构造 recordingLedger。
	rl := newRecordingLedger(t) // 既有辅助（若名不同照 slice_test.go 抄）
	svc := keystone.New(runner, narr, rl, nil)

	_, _ = svc.Wake(context.Background(), "B1", []keystone.WakeEvent{
		{Kind: keystone.WakeMessage, Card: "B1", Summary: "hi"},
	}, keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h", Workdir: "/w"})

	if len(rl.needArgs) != 1 {
		t.Fatalf("双失败应恰落一条 needs_human，得到 %d", len(rl.needArgs))
	}
	reason := rl.needArgs[0][0]
	if !strings.Contains(reason, "resume") || !strings.Contains(reason, "重建") {
		t.Fatalf("理由不可行动（缺失败原因摘要）：%q", reason)
	}
}
```

> **说明**：`newRecordingLedger(t)` 是本计划为「构造 recordingLedger 并把真实 Facade 落盘」
> 命名的辅助；实现卡查 `internal/keystone/slice_test.go` 现有 `recordingLedger` 的构造
> 写法（该文件已 `ledger.Open` + `ledgerapi.New`），照抄到本文件或复用其既有 helper。
> 若既有测试已用一个未导出构造 helper，直接用；否则本文件内建一个同名 helper。
> **此说明按占位符扫描节登记（测试夹具构造形态随包而异，属正当出口）。**

**测试范围声明**：`go test ./internal/orchestration/ -run TestB393 -count=1` 与
`go test ./internal/keystone/ -run TestB393 -count=1`。

---

## 7. 占位符扫描（自我声明）

- 正当出口一条（测试复用既有夹具/harness 而形态因包而异）：
  - T0.2 的 `testCoordRunner`/`installSleepyCoordinatorCLI` 复用 `internal/agentd/coordrunner_test.go`
    既有夹具形态（PATH 注入假 opencode + `testCoordRunner(h, nil)`）；断言已逐条列全（可判 pass/fail），
    且给出基线红形态与变异自验。
  - T2 的 `installFakeCLI`/`withArgvCapture` 复用 `internal/hostapi/runturn_test.go` 既有夹具。
  - T3.3 的 `fakeRunner`/`fakeNarrator`/`recordingLedger` 复用 `internal/keystone/slice_test.go`
    既有夹具；`newRecordingLedger(t)` 的构造形态随该文件既有辅助而定（已在 T3.3 内声明）。
- **内部锁声明**：T3.2 的 `TestB393PrepareProjectsProviderConfig` 入口是未导出纯函数，
  属内部锁；理由已在该 task 内声明（从声明缝构造不出），且要求实现卡补一条缝级断言
  `TestB393PrepareViaSupplierProjectsProviderConfig`。
- **自我声明（T3.3 夹具构造）**：`newRecordingLedger` 为夹具构造占位名，实现卡照
  `slice_test.go` 既有构造抄；属「夹具形态随包而异」的正当出口。
- 无 TBD /「加适当的错误处理」/「同 Task N」式占位；每条测试给完整可编译代码块。

## 8. 自审三查

1. **spec 覆盖（逐条指到 task）**：
   - §2 候选 1/2/3/4 → §1 R4 逐条判定（1/2 证伪，3/4 成立）。
   - §4.1 根因 + 红色回路 → R1/R2 + T0.1/T0.2。
   - §4.2 红色回路（确定性、秒级到分钟级、agent 可跑、修后能变绿）→ T0.1 脚本（秒级触发）+ T0.2（秒级红绿）+ T2.2（秒级红绿）+ T3.3（秒级红绿）。
   - §4.3 可见性（≥WARN + 一行账本事件 + 不设 HANDOFF_LOG_LEVEL 也可见）→ T2.1/T2.2/T2.3。
   - §4.4 退化链（重建真可用或明确降级落账）→ T3.1/T3.3。
   - §4.5 真机 B389 §4.3 验收链 → **本卡的 T1/T2/T3 是它的前置条件，链本身由协调者执行，不派发**。
   - §3 不做：不动路由与承载记录（本计划未碰 `scheduling.go:184`）、不改宿主会话回收语义、不清线上状态。✓
2. **占位符扫描**：见 §7，已声明。
3. **跨 task 类型/签名一致性**：`WakeRoundEvent` 在 T2.3 定义、T2 的 round-trip 测试消费，
   `json` tag 两处逐字一致；`coordWakeTurnTimeout` 在 T1.1 定义、T0.2/T1.4 测试覆盖；
   `WakeRoundDedupePrefix` 在 §5 Produces 声明、T2.3 使用；`projectCoordinatorProviderConfig`
   在 T3.1 定义、T3.2 测试调用；`truncateCause` 在 T3.3 定义并被 `rebuildAfterResumeFailure`
   消费——各组签名逐字对齐。

## 9. 图覆盖债（本节点）

`codegraph sym` 未命中：`DefaultTurnTimeout`、`ClaimWake`、`completeWakeBatch`。
回退 grep 取源码与签名。`codegraph context opencode` 被拒（非最优树领域 id），
已改用 `context d_execution_host` 与 `sym`。本计划不新增跨域依赖方向
（改动都落在 `internal/hostapi`、`internal/agentd`、`internal/keystone`、
`internal/orchestration` 既有域内），`codegraph check` 归合并前视图 diff 复核。
