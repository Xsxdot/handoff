# B295 实现计划

状态：可执行计划。L2，协调者写。基线 `acc/b156.2-156.3`。输入：`docs/superpowers/specs/2026-08-29-b295-carrier-detect-via-turn-design.md`（已批准）。

本节点只落计划，不写实现。单 task：把 `WakeHome` 从 `--version` 改成同包 `RunTurn` 发固定短消息。

## 0. 基线（本工作树实跑）

```
go test ./internal/hostapi/ -count=1
=> ok  github.com/Xsxdot/handoff/internal/hostapi	1.769s
```

`runWake` 现状（`internal/hostapi/probe.go`）：`args := []string{"--version"}`，成功后再 `ProbeHome` 看凭据文件。`DefaultDetectTimeout = 30 * time.Second`。B293 契约条 36 禁 `RunTurn`，由本卡废止。

`Host.RunTurn` 已实装 opencode（`driver.go` `driveTurn`）：`run --format json -- <prompt>`，隔离 HOME 经 `buildEnv`。未实装 CLI 错误含名字和「未实装」。假 CLI 夹具 `installFakeCLI` 在 `runturn_test.go`（同包，本卡测试直接复用）。

## Task 1：WakeHome 改调 RunTurn

### 文件边界

只允许改：

```
internal/hostapi/probe.go
internal/hostapi/probe_test.go
docs/superpowers/specs/b293-contract.md
docs/superpowers/specs/b295-ledger.md
```

不得改 `driver.go` / `RunTurn` 名单 / gateway detect 编排 / 四态 `ApplyDetect`。

### 常量与语义

`probe.go`：

```go
const DefaultDetectTimeout = 3 * time.Minute
const DetectPrompt = "ping"
```

`Timeout==0` 用 `DefaultDetectTimeout`。传入 `RunTurn` 的 `TurnRequest.Timeout` 必须是这个值，**禁止**让它走 `DefaultTurnTimeout`（30 分钟）。

`runWake` 改为：供给（`main_home_sync` 且目标目录为空）仍在 `WakeHome` 里、先于本函数；本函数只发消息。删除 `--version`、删除 `commandContext` 变量（若已无引用）、删除「唤起后再 ProbeHome 看凭据文件」的成功判据。

```go
func (h *Host) runWake(ctx context.Context, req WakeRequest, targetHome string, _ ProbeKind) (WakeReply, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultDetectTimeout
	}
	log().Info("开始检测回合", "cli", filepath.Base(req.CLI), "target", targetHome,
		"timeout", timeout.String())
	reply, err := h.RunTurn(ctx, TurnRequest{
		CLI: req.CLI, HomeDir: targetHome, Model: req.Model,
		Prompt: DetectPrompt, Timeout: timeout,
	})
	if err != nil {
		log().Warn("检测回合失败", "cli", filepath.Base(req.CLI),
			"target", targetHome, "cause", err)
		return classifyTurnError(err), nil
	}
	if strings.TrimSpace(reply.Output) == "" {
		log().Warn("检测回合无输出", "cli", filepath.Base(req.CLI),
			"session_id", reply.SessionID)
		return WakeReply{Outcome: WakeUnreachable, Detail: "回合无输出"}, nil
	}
	log().Info("检测回合成功", "cli", filepath.Base(req.CLI),
		"session_id", reply.SessionID, "output_bytes", len(reply.Output))
	return WakeReply{Outcome: WakeReady, Detail: "回合成功"}, nil
}
```

`classifyTurnError` 把 `RunTurn` 的错误变成 `WakeReply`（**返回 reply、不返回 error**），这样 `handleCarrierDetect` 才会 `ApplyDetect` 写四态。映射（大小写不敏感，搜 `err.Error()`）：

| 错误含子串 | Outcome |
|---|---|
| `未实装` | `unreachable` |
| `超时` | `unreachable` |
| `quota` / `rate limit` / `too many requests` | `quota` |
| `login` / `auth` / `unauthorized` / `not authenticated` | `need_login` |
| 其余 | `unreachable` |

不得把未知错误当 `ready`。不得把 `未实装` 当 HTTP 500 吞掉状态写入。

`WakeHome` 注释改为：有时限地用该 HOME 走 `RunTurn` 发 `DetectPrompt`；不进控制台登录 TUI；不经 keystone、不经派发状态机。删除「不发送 prompt / 不是 RunTurn」。

包注释同步：检测是发一条消息，不是 `--version`。

日志：入口带 cli/target/timeout；`RunTurn` 前后；每条错误分支带 cause；成功带 session_id 与 output_bytes。prompt 正文和凭据不得进日志。

### 改 B293 冻结文案

`docs/superpowers/specs/b293-contract.md`：

- `DefaultDetectTimeout = 30 * time.Second` 改为 `3 * time.Minute`，并加一句「B295 废止 30s」。
- 「`WakeHome` **不是** `RunTurn`：不准喂模型 prompt」改为「`WakeHome` **经** `RunTurn` 发固定短消息 `ping`（B295）；仍不准在控制台拉登录 TUI」。
- 条 34：30s → 3 分钟。
- 条 36：整条替换为「检测必须调用 `hostapi.Host.RunTurn` 发 `DetectPrompt`；禁止 `--version`；禁止凭据文件存在当作 ready」。

### 测试（同包，复用 `installFakeCLI`）

基线先跑：`go test ./internal/hostapi/ -count=1`（期望仍绿，本卡测试尚未改）。

然后改 `probe_test.go`：

1. 删除 `replaceWakeCommandContext`、`TestWakeHomeFakeProcess`、对 `commandContext` 的依赖。
2. `TestWakeHomeSuppliesMainCredentialBeforeNoPromptCLI` 改名为 `TestWakeHomeSuppliesMainCredentialBeforeTurn`：`installFakeCLI` + `withArgvCapture`；断言拷了凭据、没拷 skill；argv **不含** `--version`，**含** `run`、`--format`、`json`、`--`、`ping`；`env:HOME=` 等于目标 HOME；`Outcome==WakeReady`。
3. `TestWakeHomeOccupiedNeverOverwrites`：同样改用 `installFakeCLI`，occupied 不拷主凭据、`keep` 文件仍在。
4. `TestWakeHomeHonorsTimeoutWithoutRunTurn` 改名为 `TestWakeHomeHonorsTimeoutViaRunTurn`：`installFakeCLI` + `FAKECLI_SLEEP=30` + `Timeout: 300 * time.Millisecond`；`err==nil` 且 `Outcome==WakeUnreachable`（超时映射进 reply，不返回 error）；耗时 < 10s。
5. 新增 `TestWakeHomeUnsupportedCLIWritesUnreachableNotReady`：`CLI:"grok"`，`Outcome==WakeUnreachable`，Detail 含「未实装」，**不是** `ready`。
6. 新增 `TestWakeHomeReadyRequiresTurnOutputNotCredFile`：目标目录预先放好 `auth.json`，假 CLI 以 exit 1 / stderr `unauthorized` 失败（在 `installFakeCLI` 旁加一个可由 `FAKECLI_FAIL=unauthorized` 触发的失败桩，或本测专用脚本）；`Outcome==WakeNeedLogin`，证明凭据文件不能当 ready。

失败桩最小形态（可写在本测试文件内，与 `installFakeCLI` 并列）：

```go
func installFakeCLIFail(t *testing.T, stderr string) {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\necho '" + stderr + "' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
```

红绿周期只套在「`--version` 消失 + `RunTurn` argv/HOME + 未实装非 ready + 凭据文件非 ready」这四条断言上。先改测试跑红，再改 `runWake`。

测试范围：`go test ./internal/hostapi/ -count=1`。不要跑 `./internal/agentd/` 全包，除非本卡改了 agentd（不改）。

### 注释

`probe.go` 文件头：职责改成「路径探测 + 用 RunTurn 发检测消息」。边界：不写编制域状态；不绑卡；跨机仍由 gateway 转发 `/api/host/wake`。

### 验收栏（缺陷族）

- **生命周期**：检测回合超时由 `RunTurn` 杀进程树（已有 `watchDeadline`）。检测会话不绑卡、不 resume。中途重启：未 `ApplyDetect` 则停在原状态，用户再点检测。无，因为不新开进程承载。
- **静默失败**：未知错误 → unreachable，不是 ready。`未实装` 写进 Outcome 而不是只 500。空 Output 不是 ready。
- **跨平台**：检测走 `RunTurn` 的 unix 进程组约定，Windows 行为与协调者拉起同一条，本卡不单开 Windows 真机（标未验证）。
- **假红假绿**：锁的是 `WakeHome` 入口的 argv/Outcome，不是内部 helper 名。反面：凭据文件在 + 回合失败 ≠ ready；`--version` 不得出现。
- **门禁**：无新写路径；仍走已登录控制台的 detect。无，因为不新开匿名端点。
- **序列化**：无新字段。无，因为 HTTP 形状不变。
- **枚举**：不新增 WakeOutcome。无，因为沿用 ready/need_login/quota/unreachable。

接缝覆盖：入口全部是 `WakeHome`（spec 缝 1）。`ProbeHome` 既有测试保持，证明探测仍不发消息。

### 真机（协调者，不派发）

本机已登录 opencode，空白隔离 HOME，点检测：3 分钟内 → 已上线。该 HOME 里不止有拷来的凭据——有一次真实回合留下的 CLI 文件。`--version` 不得作为检测命令出现在 agentd 日志。

### 提交

`fix(B295): detect carriers by a ping turn instead of --version`

台账追加本轮命令与原始输出。不 push。
