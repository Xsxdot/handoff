# B329 实现计划：扩展 CLI 当前会话出示来源

读者：零上下文执行者。工作目录：本分支工作树。规格：
`docs/superpowers/specs/b329.md`（已批准，L2，单子系统 `d_cli`）。法定产出物：
`docs/superpowers/plans/b329-plan.md`。事实台账：
`docs/superpowers/specs/b329-ledger.md`；每确立一个事实、每条命令原始输出、每个判断
都追加台账，与本计划同批提交。

实现只能在当前分支 `cards/B329-charter` 完成；不切分支、不改 git 配置、不 push。
本节点只提交本计划和台账，不实现运行时代码。后续实现者必须先读本计划与
`docs/superpowers/specs/b329.md`，按任务顺序执行。

## 0. 冻结边界与不可变项

本卡只改 CLI 出示函数及其四个真实调用方如何取得 `(cli, session_id)`：

```text
cmd/card_seat.go#currentSeatIdentity
  ├─ cmd/card_driver.go#cardBindCmd.RunE
  ├─ cmd/card_driver.go#cardRebindCmd.RunE（仅 --self）
  ├─ cmd/card_node.go#runStepDispatch（仅已有席位）
  └─ cmd/room.go#roomSendCmd.RunE（仅 kind != user）
```

必须保持不变：

- 席位编码仍只调用 `proto.EncodeSeatIdentity`，结果仍为
  `cli:<cli>#<session_id>`；不新增字段、不改 `BindSeat`/`RebindSeat` 签名、不改
  HTTP body 或账本写面。
- `HANDOFF_SESSION_CLI`/`HANDOFF_SESSION_ID` 的 Resume 注入不改；不改
  `internal/agentd/server.go#resumeTurnRequest`，不改
  `internal/hostapi/driver.go#buildEnv`，不通过清理宿主环境来实现本卡。
- 不读取 `USER`、hostname、PID、`ledgerActor()` 或 `web:<addr>` 推导席位；不认
  `CLAUDE_CODE_REMOTE_SESSION_ID`。
- 不把 `--cli`/`--session` 注册到 `PersistentFlags`；不在本机创建持久身份文件；
  `card coordinate` 不注册这两个 flag，`rebind --launch` 不读这两个值指定机器人。
- 不改 `web/src/app/cards/CoordinatorPanel.tsx`：工作台继续不提供坐下/换绑给我，
  不加一对身份表单。

## 1. 基线、图证据与判据

### 1.1 已在当前基线真实运行的命令

以下命令均在动手前真实运行并退出 0；基线目标测试末尾原文为：

```text
PASS
ok  github.com/Xsxdot/handoff/cmd  1.173s
```

命令是：

```bash
go test ./cmd/ -count=1 -run 'TestCurrentSeatIdentityRequiresInjectedPair|TestCardBindUsesCurrentSeat|TestCardRebindSelfUsesLocalLedger|TestCardRebindSelfAndTakeoverEvent|TestCardRebindRequiresExplicitModeAndRejectsLegacyFlags|TestCardRebindHelpUsesExplicitModes|TestCardDispatchStep(UsesActorIdentity|SubmitsToLocalAgentd)|TestRoomSendLandsRoomMessageWithUserKind' -v
go build ./...
```

第二条命令真实成功且无输出。基线帮助面也已真实核对：

```text
go run . card bind --help       # Flags 只有 -h/--help
go run . card dispatch --help   # 没有 --cli/--session
go run . room send --help      # 没有 --cli/--session
go run . card rebind --help    # 只有 --self/--launch，没有 --cli/--session
go run . card coordinate B329 --cli grok --session s
```

最后一条真实原始错误为：

```text
Error: unknown flag: --cli
Usage:
  handoff card coordinate <卡号> [flags]
exit status 1
```

实现后的最小行为判据是：`go test ./cmd/ -count=1` 退出 0；`go build ./...` 退出 0；
四个出示入口的真实 CLI 参数测试全部通过；不适用入口带身份 flag 全部在命令层拒绝，
且不产生 HTTP、账本席位或房间事件副作用。测试必须断言行为，不用文件数或新增行数
作为验收指标。

### 1.2 代码图结果与覆盖债

仓库存在 `codegraph/`，已先用已安装的 `codegraph` 查询最佳领域 `d_cli`：

```text
codegraph context d_cli --repo .
→ outputNodes=46 outputEdges=30 truncated=false
codegraph sym currentSeatIdentity --repo .
→ cmd/card_seat.go:18, func currentSeatIdentity() (string, error)
codegraph who-calls currentSeatIdentity --repo .
→ 仅 n_cmd_cardBindCmd_RunE → n_cmd_currentSeatIdentity
→ 警告：基线仍有 5 个未扫描入口：查询结果为空不等于没有调用方
codegraph sym n_cmdrunStepDispatch --repo .
→ cmd/card_node.go:122, 图签名是过期的旧签名
codegraph flow n_cmd_cardBindCmd_RunE --repo .
→ degraded=true, steps=[]
```

`flow` 没有控制流数据，不能用 `chain` 冒充流程图；本计划按已读源码写分支。以下
是实现者必须保留的图覆盖债，不以图的空边判断没有调用方：

- `cmd/card_driver.go#cardRebindCmd.RunE` → `currentSeatIdentity`；
- `cmd/card_node.go#runStepDispatch` → `currentSeatIdentity`（图为旧签名）；
- `cmd/room.go#roomSendCmd.RunE` → `currentSeatIdentity`；
- `codegraph` 的 `n_cmdrunStepDispatch` 旧签名与当前源码不一致；
- `codegraph flow n_cmd_cardBindCmd_RunE` 无 steps，bind 流程以源码和 CLI 测试为准。

### 1.3 依赖与现有代码行为出处

实现只依赖已核对的现有行为：

- `internal/proto/seat.go:31-44` 的 `EncodeSeatIdentity` 拒绝空值、首尾空白、
  Unicode 空白、`#`，并拒绝 `cli` 中的 `:`；来源算法不得另写编码器。
- `cmd/card_node.go:122-175` 当前 `runStepDispatch` 读取账本席位后，已有席位才调用
  出示函数，最终把 actor 放进 `proto.CardStepReq`，再由 client 发 HTTP；空座使用
  `ledgerActor()`。
- `cmd/room.go:120-145` 当前 `roomSendCmd.RunE` 仅对 `kind != user` 调用出示函数，
  user 走 `ledgerActor()`；`internal/collab/service.go:80` 的精确写入口是
  `func (s *Service) Send(roomID string, msg proto.RoomMessage, actor string) (int64, error)`。
- `cmd/ledgercli_test.go:33-55` 的 `runLedgerCLI` 复用真实 `rootCmd`，每次执行后调用
  `resetAllFlags`；所有 CLI 边界测试复用它，不另造命令执行器。
- `go doc github.com/spf13/pflag.Flag.Changed` 真实返回
  `Changed bool // If the user set the value (or if left to default)`；
  `go doc github.com/spf13/cobra.Command.Flags` 真实返回 Flags 包含当前命令本地及父级
  persistent flags。实现使用每个子命令的本地 flag，并用 `Changed` 识别显式传入的
  空值禁用分支。

## 2. 任务接口合同与文件边界

本卡只有一个实现任务，避免四个调用方各自拥有不同的身份解析规则。

### 2.1 Consumes / Produces（逐字签名）

| 方向 | 精确接口 |
| --- | --- |
| Consumes | `func EncodeSeatIdentity(cli, sessionID string) (string, error)` |
| Consumes | `func openLedger() (*ledger.Store, error)` |
| Consumes | `func ledgerActor() string` |
| Consumes | `func (s *Store) BindSeat(id, identity string, source proto.SeatSource) error` |
| Consumes | `func (s *Store) RebindSeat(id, identity string, source proto.SeatSource, expect string) error` |
| Consumes | `func runStepDispatch(cmd *cobra.Command, id, node string) error` |
| Consumes | `func (s *Service) Send(roomID string, msg proto.RoomMessage, actor string) (int64, error)` |
| Consumes | `func (c *Client) CardStep(ctx context.Context, cardID string, req proto.CardStepReq) error` |
| Produces | `func currentSeatIdentity(flagCLI, flagSession string) (string, error)` |
| Produces | `card bind <id> [--cli <cli>] [--session <id>]`，两个 flag 只挂在 `cardBindCmd` |
| Produces | `card rebind <id> --self [--cli <cli>] [--session <id>]`；`--launch` 带任一身份 flag 必拒 |
| Produces | `card dispatch <id> --step <node> [--cli <cli>] [--session <id>]`；无席位带身份 flag 必拒 |
| Produces | `room send <room> <text...> --kind <kind> [--cli <cli>] [--session <id>]`；`kind=user` 带身份 flag 必拒 |
| Produces | `card coordinate <id>` 继续不认识 `--cli`/`--session` |

`currentSeatIdentity` 的两个参数只承载当前命令本地 `--cli`/`--session` 的原值，不改变
公开 CLI 的 flag 名。值为空的 flag 不作为有效来源；命令分支另用
`cmd.Flags().Changed("cli")` 和 `cmd.Flags().Changed("session")` 判定“用户是否带过
flag”，以拒绝空座 step、裸 dispatch、user room 和 rebind launch 的禁用场景。

### 2.2 精确修改文件集

生产文件：

- `cmd/card_seat.go`：唯一出示函数及来源算法；不放账本写入。
- `cmd/card_driver.go`：bind/rebind flags、参数传递、launch 模式拒绝、日志。
- `cmd/card_dispatch.go`：dispatch 本地 flags、裸 dispatch 拒绝分支。
- `cmd/card_node.go`：有席位把 flags 传入出示函数、空座 step 拒绝分支。
- `cmd/room.go`：room send 本地 flags、`kind=user` 拒绝分支、非 user 传参。
- `docs/superpowers/specs/b312-contract.md`：回写出示来源规则；Resume 注入条目不改。
- `skills/handoff/SKILL.md`：更新坐下/换绑身份缺失排障话术。

测试文件：

- `cmd/card_driver_test.go`：来源算法、bind/rebind 真实 CLI flag、帮助/模式负例。
- `cmd/card_dispatch_test.go`：占座 step 的真实 CLI flag、空座 step/裸 dispatch 负例。
- `cmd/room_test.go`：非 user 的真实 CLI flag、user kind 负例及事件 actor 序列化。

不修改 `internal/agentd/server.go`、`internal/hostapi/driver.go`、
`internal/proto/seat.go`、`internal/ledger/*`、`internal/collab/*`、任何 Web 文件。

## 3. T1：唯一出示函数、四入口接线、契约/skill 回写

### 3.1 任务目标与测试范围

T1 锁住一条接缝 `cmd/card_seat.go#currentSeatIdentity`，并由四个真实 CLI 命令
进入它。T1 只跑 `./cmd/...` 测试；不把 `internal/agentd`、Web 或全仓测试塞进本任务。

### 3.2 先写失败测试，再跑红

测试全部复用既有 `runLedgerCLI`、`newCardStepCLIEndpoint`、`cardStepBody`、
`createStepTestCard`、`mustAddCard` 夹具。按测试决定，新增测试开头必须显式将以下键
设为空值（不依赖运行测试进程恰好没有宿主键）：

```go
func clearSeatSourceEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HANDOFF_SESSION_CLI",
		"HANDOFF_SESSION_ID",
		"GROK_SESSION_ID",
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDE_CODE_REMOTE_SESSION_ID",
	} {
		t.Setenv(key, "")
	}
}
```

在 `cmd/card_driver_test.go` 调整旧的 `TestCurrentSeatIdentityRequiresInjectedPair`：
完整注入调用改为 `currentSeatIdentity("", "")`；缺 `HANDOFF_SESSION_ID` 的负例同时
清空 grok/claude/remote 键，设置 `USER=fallback-user`，断言返回 error 且不回退 USER。

新增 `TestCurrentSeatIdentitySourceOrder`，用上述 helper 和 `t.Setenv`，逐条断言：

1. 仅 `GROK_SESSION_ID=grok-01`，无注入/flag → `cli:grok#grok-01`。
2. 仅 `CLAUDE_CODE_SESSION_ID=claude-01`，同时设置不认的
   `CLAUDE_CODE_REMOTE_SESSION_ID=remote-01` → `cli:claude#claude-01`。
3. 仅完整 `HANDOFF_SESSION_CLI=opencode`、`HANDOFF_SESSION_ID=agent-01`，并设置
   不同 `GROK_SESSION_ID=parent-01` → `cli:opencode#agent-01`，不报环境冲突。
4. `HANDOFF_SESSION_CLI=opencode` 且 `HANDOFF_SESSION_ID` 为空，宿主 grok 非空 →
   error，文案含 `HANDOFF_SESSION_ID`，不返回 grok 身份。
5. `HANDOFF_SESSION_ID=agent-01` 且 `HANDOFF_SESSION_CLI` 为空，宿主 claude 非空 →
   error，文案含 `HANDOFF_SESSION_CLI`，不返回 claude 身份。
6. 无任何来源且 `USER=fallback-user` → error，文案同时含 `grok/claude`、`--cli`、
   `--session`；不返回 USER。
7. 两个宿主 id 同时非空、无完整注入/flag → error，文案要求去掉其中一个或改用
   `--cli`/`--session`。
8. 完整 flag `grok/grok-flag` 且无环境来源 → `cli:grok#grok-flag`；仅 flag CLI 或
   仅 flag session → error，且不使用任何环境值拼接。
9. flag 与仅有 grok 来源不一致 → error；一致 →成功；flag 与完整注入来源不一致 →
   error；flag 与完整注入来源一致 →成功。
10. flag、完整注入、不同 grok 宿主同时存在 →仍使用 flag 与完整注入一致的身份；
    完整注入来源本身忽略 grok/claude 宿主互斥。
11. `--cli` 值含 `:`、session 值含 `#` 或任一值带首尾空白 → error，错误来自
    `EncodeSeatIdentity`，不自行放宽编码规则。

在四个真实调用方分别新增/扩展以下测试；每条命令都清空环境源，仅以 flag 提供身份：

| 测试文件 | 测试入口与断言 |
| --- | --- |
| `cmd/card_driver_test.go` | `TestCardBindAcceptsExplicitSeatFlags`：执行 `out, _, err := runLedgerCLI(t, dir, "card", "bind", id, "--cli", "grok", "--session", "manual-bind")`；命令成功，SQLite `Card.DriverSession == "cli:grok#manual-bind"`、`DriverSource == "bind"`，stdout 为 `{"ok":true}`。 |
| `cmd/card_driver_test.go` | `TestCardRebindSelfAcceptsExplicitSeatFlags`：先以 flag 对写入旧席位，再以另一 flag 对运行 `card rebind id --self`；断言新席位是 `cli:claude#manual-rebind`、来源仍 `bind`，且已有 `TestCardRebindSelfAndTakeoverEvent` 的真实事件断言继续通过。 |
| `cmd/card_dispatch_test.go` | `TestCardDispatchStepAcceptsExplicitSeatFlagsForOccupiedCard`：用 `ledger.Store.BindSeat` 预置 `cli:grok#step-seat`，真实 CLI 运行有 `--step` 的 dispatch 并带相同 flags；mock HTTP 通过既有 `cardStepBody` 解 JSON，断言 `actor == "cli:grok#step-seat"`，且命令仍按既有 202/首态短等契约成功。 |
| `cmd/room_test.go` | `TestRoomSendCoordinatorAcceptsExplicitSeatFlags`：SQLite 预置卡席位 `cli:claude#room-seat`，真实 CLI 运行 `room send id 文本 --kind reply --cli claude --session room-seat`；断言 stdout 的 seq 有效，事件 `Actor == "cli:claude#room-seat"`，payload 解出的 `RoomMessage.Kind == "reply"`、`Body` 与输入一致。 |
```

在同一批测试中加入命令级禁用负例：

1. `card dispatch id --step 进行中 --cli grok`（空座）返回 error；mock endpoint 请求数
   为 0；账本 `DriverSession` 与 `DriverSource` 仍为空；测试证明没有调用出示函数、
   没有改变 actor、没有占座。
2. `card dispatch id --cli grok --session s`（无 `--step`）返回 error；在打开模板、
   目标机探活或 HTTP 前拒绝，账本没有派发事件、席位仍为空。
3. `room send id 文本 --kind user --cli grok` 返回 error；事件数只保留建卡已有事件，
   没有 `room_message`，没有调用 `ledgerActor()` 后写入 user 消息。
4. `card rebind id --launch --cli grok --session s` 返回 error；fake endpoint 请求数
   为 0，错误明确 `--launch` 不接受身份 flag；不读这一对去指定机器人。
5. `card coordinate id --cli grok --session s` 仍由 Cobra 返回 `unknown flag: --cli`；
   不增加 coordinate 的本地 flag。
6. 每个四入口命令的 `--help` 只显示该命令自己的 `--cli`/`--session`；
   `rootCmd.PersistentFlags().Lookup("cli")` 与 `rootCmd.PersistentFlags().Lookup("session")`
   都为 nil，`cardCmd.PersistentFlags()` 也不含这两个名字。

跑红命令（只跑触及的 cmd 包）：

```bash
go test ./cmd/ -count=1 -run 'TestCurrentSeatIdentitySourceOrder|TestCurrentSeatIdentityRequiresInjectedPair|TestCardBindAcceptsExplicitSeatFlags|TestCardRebindSelfAcceptsExplicitSeatFlags|TestCardDispatchStepAcceptsExplicitSeatFlagsForOccupiedCard|TestRoomSendCoordinatorAcceptsExplicitSeatFlags|TestSeatFlagsRejectedByNonPresentingPaths' -v
```

基线当前没有新测试与 flag，因此该组至少会在新测试符号/新行为处失败；执行者必须把
该次真实失败原文追加台账，不能把预期红写成已验证结果。

### 3.3 最小实现：来源算法完整代码块

在 `cmd/card_seat.go` 更新 import 增加 `log/slog`、`strings`，保留 `fmt`、`os` 与
`proto`。以下是 `currentSeatIdentity` 的完整实现形状；不另建只供测试调用的纯
resolver。四个生产调用方必须直接调用这个函数，函数成功/失败不得写出 session 值到
日志：

```go
// currentSeatIdentity 按 flag → HANDOFF 注入 → 已核对宿主键的顺序出示席位身份。
// 参数：flagCLI、flagSession 是当前命令本地 --cli/--session 的原值；空值表示该
// flag 没有提供有效段。返回：规范 cli:<cli>#<session_id>；错误表示来源残缺、
// 来源冲突、来源歧义或现有编码规则拒绝。注意：完整 HANDOFF 对忽略宿主键，绝不
// 读取 USER/hostname/PID/ledgerActor/web actor，也不把 session 值写日志。
func currentSeatIdentity(flagCLI, flagSession string) (string, error) {
	encode := func(source, cli, sessionID string) (string, error) {
		identity, err := proto.EncodeSeatIdentity(cli, sessionID)
		if err != nil {
			return "", fmt.Errorf("编码%s席位身份: %w", source, err)
		}
		return identity, nil
	}

	var flagIdentity string
	flagPresent := flagCLI != "" || flagSession != ""
	if flagPresent {
		if flagCLI == "" || flagSession == "" {
			return "", fmt.Errorf("席位 flag 不完整：--cli 与 --session 必须同时填写，不能与环境变量拼接")
		}
		var err error
		flagIdentity, err = encode("flag", flagCLI, flagSession)
		if err != nil {
			return "", err
		}
	}

	injectedCLI, _ := os.LookupEnv("HANDOFF_SESSION_CLI")
	injectedSession, _ := os.LookupEnv("HANDOFF_SESSION_ID")
	var environmentIdentity string
	environmentPresent := false
	if injectedCLI != "" || injectedSession != "" {
		if injectedCLI == "" || injectedSession == "" {
			missing := make([]string, 0, 2)
			if injectedCLI == "" {
				missing = append(missing, "HANDOFF_SESSION_CLI")
			}
			if injectedSession == "" {
				missing = append(missing, "HANDOFF_SESSION_ID")
			}
			return "", fmt.Errorf("当前会话出示失败：注入环境缺少或为空 %s，不回退宿主会话", strings.Join(missing, "、"))
		}
		var err error
		environmentIdentity, err = encode("HANDOFF 注入", injectedCLI, injectedSession)
		if err != nil {
			return "", err
		}
		environmentPresent = true
	} else {
		grokSession, _ := os.LookupEnv("GROK_SESSION_ID")
		claudeSession, _ := os.LookupEnv("CLAUDE_CODE_SESSION_ID")
		switch {
		case grokSession != "" && claudeSession != "":
			return "", fmt.Errorf("当前会话出示失败：同时存在 GROK_SESSION_ID 与 CLAUDE_CODE_SESSION_ID，请去掉其中一个或改用 --cli 与 --session")
		case grokSession != "":
			var err error
			environmentIdentity, err = encode("GROK_SESSION_ID", "grok", grokSession)
			if err != nil {
				return "", err
			}
			environmentPresent = true
		case claudeSession != "":
			var err error
			environmentIdentity, err = encode("CLAUDE_CODE_SESSION_ID", "claude", claudeSession)
			if err != nil {
				return "", err
			}
			environmentPresent = true
		}
	}

	if flagIdentity != "" {
		if environmentPresent && flagIdentity != environmentIdentity {
			return "", fmt.Errorf("当前会话出示失败：--cli/--session 与当前环境会话不一致，请使用当前会话的一对或去掉环境来源")
		}
		slog.Default().Info("CLI 席位身份出示完成", "source", "flag")
		return flagIdentity, nil
	}
	if environmentPresent {
		slog.Default().Info("CLI 席位身份出示完成", "source", "environment")
		return environmentIdentity, nil
	}
	return "", fmt.Errorf("当前会话未出示席位身份：请在 grok/claude 对话里重试，或使用 --cli <物种> 与 --session <会话 id> 出示自己的一对")
}
```

实现者必须按这个分支顺序保留以下细节：flag 两段只要有一段非空就失败；两个
HANDOFF 值都空才进入宿主分支，HANDOFF 一段非空而另一段空则整体失败；完整 HANDOFF
候选优先并忽略宿主；两个宿主同时非空失败；完整 flag 与环境候选不一致失败、一致
通过；不认 remote Claude 键；没有来源时文案同时给出 grok/claude、`--cli`、
`--session`。不能将所有环境键先收集后做互斥校验，因为那会打穿完整注入机器人回归。

### 3.4 最小实现：四个命令的完整接线块

#### `cmd/card_driver.go`

在文件包级变量增加 bind/rebind 两对值，新增 flag 只使用命令自己的 `Flags()`；以下
为相关完整接线块：

```go
var cardBindCLI, cardBindSession string
var cardRebindCLI, cardRebindSession string

// card bind RunE 内身份出示行
identity, err := currentSeatIdentity(cardBindCLI, cardBindSession)

// card rebind RunE 开头模式守卫与 self 身份出示行
flagsProvided := cmd.Flags().Changed("cli") || cmd.Flags().Changed("session")
if cardRebindSelf == cardRebindLaunch {
	return fmt.Errorf("--self 与 --launch 必须二选一")
}
if cardRebindLaunch && flagsProvided {
	err := fmt.Errorf("card rebind --launch 不接受 --cli/--session：机器人会话由 --launch 自己创建")
	slog.Default().Warn("CLI 机器人换绑拒绝身份 flag", "card", args[0], "cause", err)
	return err
}
// 只有 cardRebindSelf 分支到达这里
identity, err := currentSeatIdentity(cardRebindCLI, cardRebindSession)

// card_driver.go#init
cardBindCmd.Flags().StringVar(&cardBindCLI, "cli", "", "手填当前会话物种名（需与 --session 成对）")
cardBindCmd.Flags().StringVar(&cardBindSession, "session", "", "手填当前会话 id（需与 --cli 成对）")
cardRebindCmd.Flags().StringVar(&cardRebindCLI, "cli", "", "手填接班会话物种名（仅 --self，需与 --session 成对）")
cardRebindCmd.Flags().StringVar(&cardRebindSession, "session", "", "手填接班会话 id（仅 --self，需与 --cli 成对）")
```

原有 bind/rebind 的 `openLedger`、`BindSeat`、`RebindSeat`、`CoordinatorForget` 和
`--launch` HTTP 流程逐字保留；只将零参 helper 调用替换为带本命令 flag 值的调用，并
在每个新拒绝分支使用 `slog.Default().Warn` 携带 card/mode/cause。成功日志只记来源
类型或 card，不记 session 值。

#### `cmd/card_dispatch.go` 与 `cmd/card_node.go`

在 dispatch 命令上注册本地 flag，并在模板分支进入账本前拒绝身份 flag：

```go
var cardDispatchCLI, cardDispatchSession string

// cardDispatchCmd.RunE 的 step/非 step 分叉
flagsProvided := cmd.Flags().Changed("cli") || cmd.Flags().Changed("session")
if cardDispatchStep != "" {
	return runStepDispatch(cmd, id, cardDispatchStep)
}
if flagsProvided {
	err := fmt.Errorf("不带 --step 的 card dispatch 不接受 --cli/--session：它不走当前会话出示")
	slog.Default().Warn("裸卡派发拒绝席位 flag", "card", id, "cause", err)
	return err
}

// card_dispatch.go#init
cardDispatchCmd.Flags().StringVar(&cardDispatchCLI, "cli", "", "手填当前会话物种名（需与 --session 成对）")
cardDispatchCmd.Flags().StringVar(&cardDispatchSession, "session", "", "手填当前会话 id（需与 --cli 成对）")
```

`runStepDispatch` 读取 card 后保留空座 actor 规则，并把已有席位与空座分支写成：

```go
actor := ledgerActor()
flagsProvided := cmd.Flags().Changed("cli") || cmd.Flags().Changed("session")
if card.DriverSession != "" || card.DriverSource != "" {
	actor, err = currentSeatIdentity(cardDispatchCLI, cardDispatchSession)
	if err != nil {
		slog.Default().Warn("card step 无法出示协调者席位", "card", id, "node", node, "cause", err)
		return err
	}
	if actor != card.DriverSession {
		err := fmt.Errorf("卡 %s 当前席位不是本会话，请使用 rebind 接班", id)
		slog.Default().Warn("card step 席位不匹配", "card", id, "node", node, "cause", err)
		return err
	}
} else if flagsProvided {
	err := fmt.Errorf("空座 card dispatch --step 不接受 --cli/--session：--step 不负责坐下，请先 card bind")
	slog.Default().Warn("空座 card step 拒绝席位 flag", "card", id, "node", node, "cause", err)
	return err
}
```

保留后面的 `CardStepReq{Actor: actor}`、`client.CardStep`、202 短等和输出；不把空座
flag 转成 actor，不写席位。

#### `cmd/room.go`

在 `roomSendCmd` 上注册本地 flag；user 禁用分支放在 `openRoomService` 之前，非 user
才调用唯一出示函数：

```go
var roomSendCLI, roomSendSession string

// roomSendCmd.RunE 完整相关分支
flagsProvided := cmd.Flags().Changed("cli") || cmd.Flags().Changed("session")
if roomSendKind == proto.RoomMsgUser && flagsProvided {
	err := fmt.Errorf("room send --kind user 不接受 --cli/--session：user 发言继续使用人尺度 actor")
	slog.Default().Warn("CLI user 房间消息拒绝席位 flag", "room", args[0], "cause", err)
	return err
}
svc, st, err := openRoomService()
if err != nil {
	return err
}
defer st.Close()
actor := ledgerActor()
msg := proto.RoomMessage{
	Kind: roomSendKind, Body: strings.Join(args[1:], " "),
	Refs: roomSendRefs, Mentions: roomSendMention,
}
if msg.Kind != proto.RoomMsgUser {
	actor, err = currentSeatIdentity(roomSendCLI, roomSendSession)
	if err != nil {
		slog.Default().Warn("CLI 协调者房间消息身份出示失败", "room", args[0], "kind", msg.Kind, "cause", err)
		return err
	}
}
seq, err := svc.Send(args[0], msg, actor)
if err != nil {
	slog.Default().Warn("CLI 房间消息发送失败", "room", args[0], "kind", msg.Kind, "actor", actor, "cause", err)
	return fmt.Errorf("发送到 %s: %w", args[0], err)
}
slog.Default().Info("CLI 房间消息已发送", "room", args[0], "kind", msg.Kind, "actor", actor, "seq", seq)
return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"ok": true, "seq": seq})

// room.go#init
roomSendCmd.Flags().StringVar(&roomSendCLI, "cli", "", "手填当前会话物种名（需与 --session 成对）")
roomSendCmd.Flags().StringVar(&roomSendSession, "session", "", "手填当前会话 id（需与 --cli 成对）")
```

上述代码块包含当前完整的 `roomSendCmd.RunE` 相关路径；实现者只在标出的拒绝和出示
位置接线，不删 `Service.Send`、真实 SQLite 事件落账或 stdout JSON。新拒绝分支在
任何打开房间/写事件前返回。

### 3.5 跑绿、日志与注释

1. 先实现 `card_seat.go` 和 bind/rebind 接线，运行来源算法与 bind/rebind 的局部测试：

   ```bash
   go test ./cmd/ -count=1 -run 'TestCurrentSeatIdentitySourceOrder|TestCurrentSeatIdentityRequiresInjectedPair|TestCardBindAcceptsExplicitSeatFlags|TestCardRebindSelfAcceptsExplicitSeatFlags' -v
   ```

   必须保存原始输出；退出非 0 时不写 pass。
2. 再实现 dispatch/room 接线与所有禁用分支，运行：

   ```bash
   go test ./cmd/ -count=1 -run 'TestCardDispatchStepAcceptsExplicitSeatFlagsForOccupiedCard|TestRoomSendCoordinatorAcceptsExplicitSeatFlags|TestSeatFlagsRejectedByNonPresentingPaths|TestCardDispatchStep|TestRoomSend' -v
   ```
3. 完成所有新注释与结构化日志后，运行本任务最小测试范围：

   ```bash
   go test ./cmd/... -count=1
   go build ./...
   git diff --check
   ```

   `cmd/card_seat.go` 文件头必须写清“唯一出示入口、只读来源、不写账本”的职责和边界；
   `currentSeatIdentity(flagCLI, flagSession string) (string, error)` 注释必须说明两
   参数、返回规范编码、四层来源规则和不记录 session 值。四个命令入口日志必须覆盖
   card/room/node、mode/kind、外部 client 前后、每条新错误分支和成功路径；日志只记
   source/has_flag 等元数据，不记环境值或完整身份字符串。非显然的“完整 HANDOFF 忽略
   宿主”原因写在 helper 旁，解释机器人继承父进程宿主键时仍须出示自身席位。

### 3.6 契约与 skill 回写

在运行代码通过局部测试后，更新 `docs/superpowers/specs/b312-contract.md` 的冻结文字，
只改 §2.1 第 2 款与 §3.1–§3.2 出示来源句；Resume 注入第 3 款第 3 项原文保留。
将 §2.1 第 2 款改为以下完整文字：

```text
2. session_id 是该 CLI 返回/继续使用的会话 id。用户不得通过 --to 式接班者指定别人的
   会话；允许在 card bind、rebind --self、已有席位的 card dispatch --step 和
   kind != user 的 room send 上以 --cli/--session 出示自己的这一对。编码仍严格遵守
   本节 EncodeSeatIdentity 规则。
```

将 §3 的共享出示规则改为以下完整文字：

```text
1. cmd 的 func currentSeatIdentity(flagCLI, flagSession string) (string, error) 先看
   本命令 --cli/--session：两者非空才编码 flag 对，只非空一个即失败，不与环境拼接。
   再看 HANDOFF_SESSION_CLI/HANDOFF_SESSION_ID：完整非空对编码为注入对；一段非空而
   另一段缺失或为空即失败，不回退宿主；两段都空才继续宿主来源。完整注入对存在时
   忽略 GROK_SESSION_ID/CLAUDE_CODE_SESSION_ID。只有注入对不存在时，单独的非空
   GROK_SESSION_ID 编为 cli:grok#(GROK_SESSION_ID 的值)，单独的非空
   CLAUDE_CODE_SESSION_ID 编为 cli:claude#(CLAUDE_CODE_SESSION_ID 的值)；两者同时非空即失败；不认 CLAUDE_CODE_REMOTE_SESSION_ID。没有来源
   时失败文案同时给出 grok/claude、--cli、--session。flag 对与环境候选都存在时，
   编码不一致失败，一致通过。四个生产入口共用本函数，不从 USER、hostname、PID、
   ledgerActor 或 web actor 推导席位。
2. card bind、rebind --self、已有席位的 card dispatch --step 和 kind != user 的
   room send 都把本命令的 flag 值传给同一个函数。card dispatch 无 --step、空座
   --step、kind=user 的 room send、rebind --launch 带身份 flag 都拒绝且不改 actor、
   席位或事件；card coordinate 不注册这两个 flag。
```

契约中 `coordinatorRunner.Resume` 继续保留 `HANDOFF_SESSION_CLI/ID` 注入，不新增
任何环境清理规则。`docs/superpowers/specs/b312-contract.md` 的“协调者 kind”文字
统一成 `kind != user`。

在 `skills/handoff/SKILL.md`：

- 占座段保留 `--self`/`--launch` 二选一和不支持 `--to`/`--carrier`/`--expect`；
  增加 `--cli`/`--session` 仅属于 `bind`、`rebind --self`、已有席位 step、非 user
  room send 的命令本地 flag，明确 `--launch`/coordinate 不接受它们。
- 将 `skills/handoff/SKILL.md:720` 现有排障行替换为：

  ```text
  | `card bind` / `rebind --self` 报未出示席位身份 | 当前来源依次是完整 HANDOFF_SESSION_CLI/ID、单独的 GROK_SESSION_ID 或 CLAUDE_CODE_SESSION_ID；普通终端/已关会话没有来源，或环境残缺、双宿主、手填与当前来源不一致 | 在 grok/claude 对话里直接重试；没有当前来源时在同一命令带 `--cli <物种> --session <id>`，两项必须成对且与当前来源一致。完整 HANDOFF 对优先；不要用 `USER`/hostname/PID，不要给 `rebind --launch` 或 coordinate 带这两个 flag。`--step`/非 user room send 也沿用同一对。 |
  ```

仓内没有 `product-backlog` skill 文件；不创建替代文件，不改无对应路径的文档。

### 3.7 T1 验收清单

缺陷族逐族对抗：

| 缺陷族 | 设问 | 必须由哪条断言回答 |
| --- | --- | --- |
| 来源优先级 | 完整 HANDOFF 是否被宿主键打穿？ | `TestCurrentSeatIdentitySourceOrder` 第 3、10 条成功为注入/一致 flag 身份 |
| 残缺与歧义 | 半套 HANDOFF、双宿主、无来源是否 fail-closed？ | 同测试第 4–7 条，逐条检查 error 文案与无身份返回 |
| 冲突 | 手填是否被忽略，或不一致是否坐错席？ | 同测试第 8–10 条；不一致 error、一致 identity |
| 编码/旧来源 | 非法 cli/session、USER、remote 键是否绕过编码？ | 同测试第 2、6、11 条；只接受 `EncodeSeatIdentity` 结果 |
| 接线/模式 | 四个入口是否都真正传 flags？非入口是否偷偷消费 flags？ | 四个正向 CLI 测试；dispatch/room/rebind/coordinate 六条负例 |
| 状态副作用 | 禁用分支是否写席位、actor、房间事件或发 HTTP？ | 空座 step、裸 dispatch、user room、launch rebind 的零请求/零写断言 |
| 测试隔离 | 本场 grok 宿主环境是否造成假绿/假红？ | 每支出示相关测试显式清空五个环境键 |

类型标注明细/真机清单：

- Go helper 签名固定为 `func currentSeatIdentity(flagCLI, flagSession string) (string, error)`；
  `proto.SeatSource`、`ledger.Store`、`proto.CardStepReq`、`proto.RoomMessage` 使用现有类型，
  不新增 DTO。
- CLI 真机必须验证：grok 只设 `GROK_SESSION_ID` 能绑定；claude 只设
  `CLAUDE_CODE_SESSION_ID` 能绑定；Pi/Codex Desktop/Kimi/普通终端无宿主键时使用
  `--cli/--session`；半套 HANDOFF、双宿主和冲突 pair 失败。
- 必须验证从 grok 父进程继承 `GROK_SESSION_ID` 的无头机器人在完整 HANDOFF 对存在时
  仍以 HANDOFF 席位通过后续 step/room 出示；实现不能触碰 Resume/buildEnv。

### 3.8 序列化边界与接缝双向核对

新增身份值从产生到消费的手写/投影边界逐一锁定：

1. Cobra `--cli`/`--session` → 各命令本地 string 变量 →
   `currentSeatIdentity(flagCLI, flagSession)`；使用 `Flags().Changed` 区分“未传”与
   “显式传空”，负例覆盖显式 flag 但不可用分支。
2. helper → `ledger.Store.BindSeat`/`RebindSeat` 的 string 参数；无新 JSON 字段，
   通过真实 SQLite 读回 `DriverSession`/`DriverSource` 断言非空身份与来源。
3. helper → `proto.CardStepReq.Actor` → `client.CardStep` JSON body；
   `TestCardDispatchStepAcceptsExplicitSeatFlagsForOccupiedCard` 经真实 HTTP handler 的
   `cardStepBody` 解码断言 actor，不能直接调用 `runStepDispatch` 替代。
4. helper → `Service.Send(roomID, msg, actor)` → ledger `room_message` 的 Actor 与
   `RoomMessage` JSON payload；`TestRoomSendCoordinatorAcceptsExplicitSeatFlags` 用
   `EventsFromAsc` 解码断言 actor/kind/body。
5. 环境来源 `os.LookupEnv` 的“未设置/空串/非空”边界：测试清空五个键，并分别覆盖
   完全空、半套非空、双宿主非空；空值不能被当作有效身份，也不能拼接成第三套身份。

接缝双向清单：

- 测试 → 缝：`TestCurrentSeatIdentitySourceOrder`/旧 helper 测试入口是
  `currentSeatIdentity`；bind/rebind 正向入口是 `cardBindCmd.RunE`/
  `cardRebindCmd.RunE`；step 正向入口是 `cardDispatchCmd.RunE → runStepDispatch →
  currentSeatIdentity`；room 正向入口是 `roomSendCmd.RunE → currentSeatIdentity`。
- 缝 → 测试：四条接缝分别由上述四支真实 CLI 正向测试锁住；来源算法由 helper 接缝
  测试锁住；`card coordinate`、模板 dispatch、空座 step、user room、rebind launch
  的非出示路径由负例锁住其“拒绝且无副作用”行为。
- 负例是附加内部锁，不替代唯一出示缝。它们不能从直接调用
  `currentSeatIdentity` 构造：只有真实 Cobra 命令同时拥有 flag 的 Changed 状态、
  `--step`/`kind`/mode 分支与可观测 HTTP/账本副作用；因此必须从相应命令入口构造，
  各自理由已逐条写在 3.2，不能改成直接喂 helper 的纯测试。

## 4. 用户故事与规格归属

| 用户故事 | 具体任务/断言 |
| --- | --- |
| 1 grok 不带 flag bind，之后 step/非 user room | T1 helper 第 1 条 + 四入口接线；协调者真机清单 |
| 2 claude 不带 flag bind | T1 helper 第 2 条 + bind 真实 SQLite 测试 |
| 3 普通终端手填一对用于 bind/rebind/step/room | 四条真实 CLI 正向测试，逐条经对应命令入口 |
| 4 只给一个 flag 失败 | helper 第 8 条；各命令拒绝分支沿用同一 helper |
| 5 flag 与进程已有另一对冲突 | helper 第 9 条及无副作用错误断言 |
| 6 普通终端无 flag 失败但文案给手填方式 | helper 第 6 条 |
| 7 工作台无坐下/换绑给我与表单 | 触及文件清单明确不改 Web；实现后 `git diff -- web/src/app/cards/CoordinatorPanel.tsx` 必须为空 |
| 8 coordinate/rebind launch 不用身份 flag | coordinate unknown-flag 与 rebind launch 零 HTTP 负例 |
| 9 完整 HANDOFF + 不同 grok 宿主仍用注入 | helper 第 3、10 条；真机无头回归清单 |
| 10 半套 HANDOFF 不落到宿主 | helper 第 4、5 条 |
| 11 空座 step/裸 dispatch/user room 带 flag 拒绝 | T1 六条命令级负例中的前三条 |

## 5. 占位符扫描与收口

本计划没有 `TBD`、`TODO`、“同 Task N”或未定义的错误处理占位符。测试代码复用既有
夹具是本计划唯一声明的形态例外：`runLedgerCLI`、`newCardStepCLIEndpoint`、
`cardStepBody`、`createStepTestCard`、`mustAddCard` 的具体构造不复制到计划；但每支
测试的入口符号、环境清理、命令参数、成功/失败、序列化与副作用断言已在 3.2 逐条列全。
内部负例不是缝级断言的替代，合法理由和对应入口已在 3.8 明示。

占位符扫描时，命令中的 `./cmd/...`、`./...` 是 Go 工具的真实包模式，命令语法中的
`<text...>` 是现有 `room send` 的 variadic 参数表示，均不是待填写文本；除此之外不
接受省略号或未定义名称。

实现者完成 T1 后，按顺序执行并把每条命令和原始输出追加
`docs/superpowers/specs/b329-ledger.md`：

```bash
go test ./cmd/... -count=1
go build ./...
git diff --check
git status --short --branch
```

卡级真机验收（本 task 由协调者执行，不派发）在本场 grok 与 main 门各跑一遍：

1. 进程只有 `GROK_SESSION_ID` 时，空座 `handoff card bind <id>` 成功，账本席位为
   `cli:grok#<该进程 id>`；随后同一身份的 `card dispatch --step` 与 `room send --kind reply`
   成功。
2. 普通终端清空五个来源键，`card bind <id> --cli grok --session <id>` 成功，
   席位精确等于编码结果；只带一项失败。
3. 代理/无头场景同时存在完整 HANDOFF 对和不同 `GROK_SESSION_ID` 时，使用 HANDOFF
   席位；不能通过修改 Resume/buildEnv 达成。
4. `coordinate`、`rebind --launch`、空座 step、裸 dispatch、user room 的身份 flag
   负例按 3.2 的零 HTTP/零写断言执行。

实现者只提交代码、测试、B312 回写、skill 回写和台账；不提交 codegraph 快照，不
push。提交事实必须先把 `git add`/`git commit` 的命令与原始输出追加台账，再只 amend
一次将台账收进同批提交；amend 换 hash 后不回写 hash，收口以工作树干净为准。
