# B269 实现计划：活查询要答、有旧录像才闭嘴

读者：对仓库零上下文的执行者。对照 `docs/superpowers/specs/b269.md`（已批准，L2）。基线分支 `cards/B268-charter`（含 B270）。

不改 B268 键盘；不改 B270 keep-alive、`[I]/[O]` 全丢、滚轮 8 格；不在 PTY 宿主里解析提问。

基线（动手前复核）：

```
cd web && npx vitest run src/api/pty.test.ts src/app/workbench/terminalHostResponse.test.ts src/app/workbench/TerminalTab.test.tsx
go test ./internal/agentd/ -count=1 -timeout 30s -run 'TestPtyWS'
```

应全绿。红了先停。

图：`TerminalTab` / `k_web_api_pty` 在图。`backlog_bytes` 未覆盖，记覆盖债，本卡不 absorb。

---

## Task 1 — 建连帧带旧录像长度（缝 1）

改：`internal/proto/pty.go`、`internal/agentd/pty_ws.go`、`internal/agentd/pty_ws_test.go`、`web/src/api/types.ts`、`web/src/api/pty.ts`、`web/src/api/pty.test.ts`。

**Consumes：** `att.Backlog`（`[]byte`，attach 当时的环形缓冲）。

**Produces：**

```go
// PtyControl 新增，无 omitempty：0 与缺键必须能从 JSON 原文分开。
BacklogBytes uint64 `json:"backlog_bytes"`
```

```ts
export interface PtyControl {
  type: string
  since: number
  truncated: boolean
  backlog_bytes?: number  // 缺席 = 旧服务端
  exit_code?: number
  message?: string
  cols?: number
  rows?: number
}

onAttached: (info: {
  since: number
  truncated: boolean
  backlog_bytes?: number  // 缺席不要写成 0
}) => void
```

**测试范围：** `go test ./internal/agentd/ -count=1 -timeout 30s -run 'TestPtyWS'`；`cd web && npx vitest run src/api/pty.test.ts`

### Step 1 — 基线

跑上面两条。记下通过数。

### Step 2 — 红：JSON 必须有键，0 ≠ 缺席（锁缝断言）

在 `pty_ws_test.go` 追加（Windows skip 与同文件其它用例一致）：

```go
func TestPtyWSAttachedBacklogBytesKeyPresent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	s := ptyCreate(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	t.Cleanup(func() { _ = env.srv.pty.Close(s.ID) })

	url := strings.Replace(env.ts.URL, "http://", "ws://", 1) +
		"/ws/pty?session=" + s.ID + "&since=0"
	c, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatalf("拨 /ws/pty 失败: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	typ, data, err := c.Read(context.Background())
	if err != nil {
		t.Fatalf("读首帧: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("首帧类型 = %v", typ)
	}
	if !bytes.Contains(data, []byte(`"backlog_bytes"`)) {
		t.Fatalf("attached 原文必须含 backlog_bytes 键（0 也要写出，不能 omitempty），原文=%s", data)
	}
	var ctrl proto.PtyControl
	if err := json.Unmarshal(data, &ctrl); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if ctrl.BacklogBytes != 0 {
		t.Fatalf("新会话 backlog_bytes = %d，期望 0", ctrl.BacklogBytes)
	}
}
```

在 `pty.test.ts` 的 `首帧 attached 转成回调` 旁边加：

```ts
  it('attached 带 backlog_bytes:0 时原样传给回调——0 不是缺席', () => {
    const { sockets, onAttached } = harness()
    sockets[0].emitText({ type: 'attached', since: 0, truncated: false, backlog_bytes: 0 })
    expect(onAttached).toHaveBeenCalledWith({ since: 0, truncated: false, backlog_bytes: 0 })
  })

  it('attached 不带 backlog_bytes 时回调没有该键——旧服务端', () => {
    const { sockets, onAttached } = harness()
    sockets[0].emitText({ type: 'attached', since: 0, truncated: false })
    expect(onAttached).toHaveBeenCalledWith({ since: 0, truncated: false })
    const info = onAttached.mock.calls[0][0] as { backlog_bytes?: number }
    expect('backlog_bytes' in info).toBe(false)
  })
```

先跑：Go 条红在原文没有 `backlog_bytes`；前端两条红在回调仍是 `{ since, truncated }`。

### Step 3 — 最小实现

`PtyControl` 加 `BacklogBytes uint64 \`json:"backlog_bytes"\``，**不要** `omitempty`。

`pty_ws.go` 写 attached 时：

```go
if err := writeCtrl(ctx, conn, proto.PtyControl{
	Type: proto.PtyCtrlAttached, Since: att.Since, Truncated: att.Truncated,
	BacklogBytes: uint64(len(att.Backlog)),
}); err != nil {
```

`pty.ts`：

```ts
onAttached: (info: { since: number; truncated: boolean; backlog_bytes?: number }) => void
```

```ts
case 'attached':
  cursor = ctrl.since
  const info: { since: number; truncated: boolean; backlog_bytes?: number } = {
    since: ctrl.since,
    truncated: ctrl.truncated,
  }
  if (typeof ctrl.backlog_bytes === 'number') info.backlog_bytes = ctrl.backlog_bytes
  options.onAttached(info)
  return
```

`types.ts` 的 `PtyControl` 加可选 `backlog_bytes?: number`。

现有 `pty.test.ts`「首帧 attached 转成回调」仍期望 `{ since: 0, truncated: false }`（不带键）。不要把 `undefined` 写进对象。

### Step 4 — 绿 + 回放长度不是 0

跑 Task 1 测试范围。再在 `TestPtyWSResumeSince` 里、第二次 `dialPty` 之后加（第一次连 since=0 灌过输出，再 `Get` 之前不要断言；第二次是 `since=cur.BytesOut` 可能 backlog=0。另开一条 since=0 重连）：

```go
func TestPtyWSAttachedBacklogBytesMatchesRing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	s := ptyCreate(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	t.Cleanup(func() { _ = env.srv.pty.Close(s.ID) })

	c1, _ := dialPty(t, env, s.ID, 0)
	_ = c1.Write(context.Background(), websocket.MessageBinary, []byte("echo ROUN\"D1\"\n"))
	readUntil(t, c1, "ROUND1")
	_ = c1.Close(websocket.StatusNormalClosure, "")

	c2, ctrl := dialPty(t, env, s.ID, 0)
	defer c2.Close(websocket.StatusNormalClosure, "")
	if ctrl.BacklogBytes == 0 {
		t.Fatal("已经有输出再 since=0 重连，backlog_bytes 不该是 0")
	}
}
```

这条必须绿。`BacklogBytes` 不必精确等于某常数（环里还有提示符），只要 `> 0`。

### Step 5 — 日志与注释

`pty_ws.go` 已有 `backlog_bytes` 打进建连 Info，确认用的是 `len(att.Backlog)`，与帧里的数字同一来源。`PtyControl.BacklogBytes` 上写：为什么无 omitempty（0 与旧服务端缺键）。`pty.ts` attached 分支写：缺键不要填 0。

### Step 6 — 提交

`git add` 本 task 文件后提交。不要把 `web/dist` 或 `internal/webui/dist` 加进去。

---

## Task 2 — 回放闭嘴、活查询放行（缝 2）

改：`web/src/app/workbench/TerminalTab.tsx`、`TerminalTab.test.tsx`、`terminalHostResponse.ts` 过期注释、`CHANGELOG.md`。

**Consumes：** Task 1 的 `onAttached.info.backlog_bytes?: number`；`isTerminalHostResponse`；`takeLeadingFocusReport`。

**Produces：** 无新导出。行为：

| attached | 灌旧录像期间 DA/OSC/CPR | 灌完 / 从未有旧录像 | `[I]/[O]` | 按键 |
|---|---|---|---|---|
| 缺 `backlog_bytes` | 丢（今日） | 一直丢 | 丢 | 上送 |
| `0` | 不进入回放 | 上送 | 丢 | 上送 |
| `N>0` | 丢，直到入站字节累计 ≥ N 且该次 `term.write` 回调跑完 | 上送 | 丢 | 上送 |

**测试范围：** `cd web && npx vitest run src/app/workbench/TerminalTab.test.tsx src/app/workbench/terminalHostResponse.test.ts`

### Step 1 — 基线

跑测试范围。现有「设备回包不上送」不先调 `onAttached`，缺字段 → 仍丢，这条必须继续绿。

### Step 2 — 红：活查询要上送（锁缝断言）

在 `TerminalTab.test.tsx`「设备回包不上送」后面加。`connectPty` mock 要带 `debug: vi.fn()` 以免取证路径炸，与现有用例一样可不带（组件用 `?.`）。

```ts
  it('backlog_bytes 为 0 时设备回包上送——新终端没有旧录像', async () => {
    const send = vi.fn()
    connectPty.mockReturnValue({ close: vi.fn(), send, resize: vi.fn(), debug: vi.fn() })
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    const opts = connectPty.mock.calls[0][0] as {
      onAttached: (info: { since: number; truncated: boolean; backlog_bytes?: number }) => void
    }
    opts.onAttached({ since: 0, truncated: false, backlog_bytes: 0 })
    termOnData!('\x1b[>0;276;0c')
    expect(new TextDecoder().decode(send.mock.calls[0][0])).toBe('\x1b[>0;276;0c')
  })

  it('缺 backlog_bytes 时设备回包仍不上送——旧服务端维持全丢', async () => {
    const send = vi.fn()
    connectPty.mockReturnValue({ close: vi.fn(), send, resize: vi.fn(), debug: vi.fn() })
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    const opts = connectPty.mock.calls[0][0] as {
      onAttached: (info: { since: number; truncated: boolean; backlog_bytes?: number }) => void
    }
    opts.onAttached({ since: 0, truncated: false })
    termOnData!('\x1b[>0;276;0c')
    expect(send).not.toHaveBeenCalled()
  })

  it('回放字节未灌完时 DA 不上送，灌完且 write 回调之后才上送', async () => {
    const send = vi.fn()
    let writeCb: (() => void) | undefined
    termInstance.write.mockImplementation((_data: unknown, cb?: () => void) => {
      writeCb = cb
    })
    connectPty.mockReturnValue({ close: vi.fn(), send, resize: vi.fn(), debug: vi.fn() })
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    const opts = connectPty.mock.calls[0][0] as {
      onAttached: (info: { since: number; truncated: boolean; backlog_bytes?: number }) => void
      onData: (bytes: Uint8Array) => void
    }
    opts.onAttached({ since: 0, truncated: false, backlog_bytes: 4 })
    opts.onData(new TextEncoder().encode('abcd'))
    termOnData!('\x1b[>0;276;0c')
    expect(send).not.toHaveBeenCalled()
    writeCb?.()
    send.mockClear()
    termOnData!('\x1b[>0;276;0c')
    expect(new TextDecoder().decode(send.mock.calls[0][0])).toBe('\x1b[>0;276;0c')
  })

  it('backlog_bytes 为 0 时 [I]/[O] 仍然不上送', async () => {
    const send = vi.fn()
    connectPty.mockReturnValue({ close: vi.fn(), send, resize: vi.fn(), debug: vi.fn() })
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    const opts = connectPty.mock.calls[0][0] as {
      onAttached: (info: { since: number; truncated: boolean; backlog_bytes?: number }) => void
    }
    opts.onAttached({ since: 0, truncated: false, backlog_bytes: 0 })
    termOnData!('\x1b[O')
    termOnData!('\x1b[I')
    expect(send).not.toHaveBeenCalled()
  })
```

先跑：`backlog_bytes 为 0 时设备回包上送` 必红（今日全丢）。确认红因是 `send` 没被调用，不是 typo。

### Step 3 — 最小实现

`TerminalTab.tsx` 里用三个闭包变量（名字可改，语义不许改）：

- `hostReply: 'drop-all' | 'replay' | 'live'`
- `replayLeft: number`（仅 `replay`）
- 保留 `replayingBacklog` 给 B270 的 `finishReplay` / 鼠标 nudge：**有旧录像时**仍在最后一截 write 回调里 `finishReplay()`；`backlog_bytes === 0` 不要无故 `replayingBacklog = true`（没有 1004h 可重放）。

`onAttached`：

```ts
onAttached: ({ truncated, backlog_bytes }) => {
  if (typeof backlog_bytes !== 'number') {
    hostReply = 'drop-all'
    replayingBacklog = true // 旧服务端：B270 第一帧回调仍 nudge
  } else if (backlog_bytes === 0) {
    hostReply = 'live'
    replayingBacklog = false
  } else {
    hostReply = 'replay'
    replayLeft = backlog_bytes
    replayingBacklog = true
  }
  if (truncated) term.clear()
  // 尺寸重申原样保留
  ...
}
```

`connectPty` 的 `onData`（入站 PTY 字节，不是 xterm onData）：

```ts
onData: (bytes) => {
  const n = bytes.byteLength
  const afterWrite = () => {
    const after = `${term.buffer.active.type}/...`
    if (after !== before) b270('tui-mode', ...)
  }
  if (hostReply === 'replay' && n > 0) {
    replayLeft = Math.max(0, replayLeft - n)
    const last = replayLeft === 0
    const before = `${term.buffer.active.type}/${term.modes.mouseTrackingMode}/${term.modes.sendFocusMode}`
    term.write(bytes, () => {
      afterWrite()
      if (last) {
        hostReply = 'live'
        logTermKeepalive(label, 'host-live', { 原因: 'backlog-done' })
        finishReplay()
      }
    })
    return
  }
  if (n === 0) {
    finishReplay()
    return
  }
  const before = `${term.buffer.active.type}/${term.modes.mouseTrackingMode}/${term.modes.sendFocusMode}`
  term.write(bytes, () => {
    afterWrite()
    if (hostReply !== 'live') finishReplay()
  })
}
```

xterm `onData`（仿真器 → PTY）：

```ts
term.onData((d) => {
  let rest = d
  for (let head = takeLeadingFocusReport(rest); head; head = takeLeadingFocusReport(rest)) {
    logTermHost(label, head.report)
    b270('drop-focus', { 报告: head.report === '\x1b[O' ? '[O]' : '[I]', ...snap() })
    rest = head.rest
  }
  if (rest === '') return
  if (isTerminalHostResponse(rest)) {
    if (hostReply !== 'live') {
      logTermHost(label, rest)
      return
    }
    logTermKeepalive(label, 'host-pass', { 字节: rest.length })
  }
  logTermInput(label, rest, wsStatus)
  handle?.send(new TextEncoder().encode(rest))
})
```

空入站帧：保持今日 `finishReplay()`，避免 B270 用例里 `onData(new Uint8Array())` 卡死。`hostReply === 'live'` 时空帧不要改回 `drop-all`。

### Step 4 — 绿

跑测试范围。现有「设备回包不上送」（不调 onAttached）仍绿。B270 的 `[I]/[O]` 用例仍绿。新四条绿。

`terminalHostResponse.ts` 里 `isFocusReport` 头注释「活着的必须上送」改成：B270 起一律不上送，本函数只做识别，送不送由 `TerminalTab` 决定。`isTerminalHostResponse` 测试那条「焦点报告不是设备回包」的标题若写「必须上送」，改成「不并进设备回包识别」。不断言行为改成上送。

### Step 5 — 日志与注释

成功路径：`host-live`（回放结束）、`host-pass`（活 DA 放行）。丢的继续 `logTermHost`。`onAttached` 写清三种 `hostReply` 为什么。禁止 `console.log`。

`CHANGELOG.md` Unreleased「修复」加一条用户能读懂的：新开终端会回答颜色/终端类型询问；刷新重连灌历史时仍不会把回答打进提示符。

### Step 6 — 提交

只提交本 task 文件。

---

## 缺陷族

| 族 | 结论 |
|---|---|
| 生命周期 | 重连再走 attached；`hostReply` 按新帧重置。不留上一条连接的 `replayLeft`。 |
| 静默失败 | 缺字段维持全丢，刷新不喷字。活放行走 `host-pass`。 |
| 跨平台 | 字段是 JSON 数字；Windows PTY 测试仍 skip。WKWebView 不新增按键路径。 |
| 假红假绿 | 0 与缺席分开测。回放用例必须等 write 回调。变异：0 当回放，或 live 仍丢 DA。 |
| 门禁 | 无新写权限。 |
| 序列化边界 | 缝 1：Go marshal 原文含键；TS 缺键不填 0。穿过真 JSON，不是两端各测各的。 |

## 接缝覆盖

- 缝 1：`TestPtyWSAttachedBacklogBytesKeyPresent` + `pty.test.ts` 两条 attached。入口是真 WS 首帧 / `JSON.parse` 后的 `onAttached`。
- 缝 2：Task 2 四条新用例入口是 `TerminalTab` 的 xterm `onData`（生产调用方）。
- 无内部锁顶替。

## 真机（acceptance，本 task 不跑）

1. 新开终端，starship/程序问颜色，能答上（允许偶发迟到）。
2. 刷新同一会话，提示符不冒 `>0;276;0c` / `rgb:`。
3. TUI 切 tab 仍能输入、滚轮按页（B270）。
4. `[I]/[O]` 仍不上送。

## 图覆盖债

`backlog_bytes` / `hostReply` 未入图。本卡不 absorb。

## 占位符扫描

无 TBD。测试 harness 复用 `pty_ws_test.go` 的 `newTestAgentdEnv` / `ptyCreate` / `dialPty` 与 `TerminalTab.test.tsx` 的 xterm 替身，新用例正文已写出。
