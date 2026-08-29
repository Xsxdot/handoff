# B300 实现计划：终端 TUI 复制落到本机剪贴板（OSC 52）

状态：**待实现**（spec 已批准，L2）
卡：B300 · spec：`docs/superpowers/specs/b300.md` · 台账：`docs/superpowers/specs/b300-ledger.md`
日期：2026-08-29

## 基线读数（判据动手前已复核，2026-08-29）

- `cd web && npm test`（vitest run）全绿：**1181 passed，约 32s**。
- `npx vitest run src/app/workbench/TerminalTab.test.tsx`：**47 passed**。
- `npm run lint`：**存量 23 problems（5 errors, 18 warnings）**——本卡判据是「不新增」，不是清零。
- `npm run typecheck`（tsc -b）：基线通过。
- 库事实出处：`web/node_modules/@xterm/xterm/typings/xterm.d.ts:1822`
  `registerOscHandler(ident: number, callback: (data: string) => boolean | Promise<boolean>): IDisposable`。
  callback 收到的是 `52;` 之后的载荷原文（如 `c;aGVsbG8=`），序列不完整时 xterm 自行缓冲。
- 门状态复用现状：`TerminalTab.tsx` 挂载 effect 内已有 `hostReply: 'drop-all' | 'replay' | 'live'`
  （`onAttached` 按 `backlog_bytes` 置位，重放结束转 `'live'`，见 `TerminalTab.tsx:252` 与 `:420-435`）——
  OSC 门直接读它，不新造计数。

## Interfaces

Consumes：

- `term.parser.registerOscHandler(ident: number, callback: (data: string) => boolean | Promise<boolean>): IDisposable`（@xterm/xterm ^5.5.0）
- `navigator.clipboard.writeText(text: string): Promise<void>`（浏览器/WebView API）

Produces：

- `web/src/app/workbench/terminalOsc52.ts`：
  `export function parseOsc52(data: string): Osc52Copy | null`；
  `export interface Osc52Copy { selection: string; text: string }`。调用方：TerminalTab。
- `web/src/app/workbench/terminalDebug.ts`：
  `export function logTermOsc52(label: string, event: string, extra?: Record<string, unknown>): void`。调用方：TerminalTab。

## Task 1（缝 1）：OSC 52 载荷解码纯函数

文件：新建 `web/src/app/workbench/terminalOsc52.ts` + `web/src/app/workbench/terminalOsc52.test.ts`。
测试范围：只跑 `npx vitest run src/app/workbench/terminalOsc52.test.ts`。

1. 写下述测试文件 → `npx vitest run src/app/workbench/terminalOsc52.test.ts` 跑红（模块不存在）。
2. 写下述实现 → 跑绿。
3. 提交（git add 仅这两个文件；工作区里 `web/src/app/workbench/WorkbenchPage.tsx` 等存量脏改动与本卡无关，**不得 add**）。

`terminalOsc52.ts` 全文：

```ts
// terminalOsc52 —— OSC 52 载荷的解析与解码（B300）。
//
// 职责：把 xterm parser 交来的 OSC 52 data（`<selection>;<payload>`）翻译成
//       「要写入本机剪贴板的文本」；不该写的输入一律返回 null。
// 边界：
//   - 不碰剪贴板、不挂 xterm——那是 TerminalTab 的事；本文件是可表驱动的纯函数
//   - 「不该写」的输入：载荷为 `?`（读查询，远端不得读本机剪贴板）、空载荷
//     （「清剪贴板」语义，远端不得清本机剪贴板）、解码失败（坏 base64 / 坏 UTF-8）

export interface Osc52Copy {
  // TUI 声明的目标选择区：c（clipboard）/ s / p（primary）。浏览器只有一个
  // 剪贴板，三种都写它——本文件只透传，不据此拦截。
  selection: string
  text: string
}

// parseOsc52 解析 OSC 52 载荷。data 是 registerOscHandler(52, cb) 交给 cb 的
// 参数（`52;` 之后的原文）。返回 null 表示本条不产生剪贴板写入。
export function parseOsc52(data: string): Osc52Copy | null {
  const semi = data.indexOf(';')
  if (semi < 0) return null
  const selection = data.slice(0, semi)
  const payload = data.slice(semi + 1).replace(/\s+/g, '')
  // `?` 是「读剪贴板」查询：绝不回应——本功能的安全边界（spec 实现决定 2）。
  if (payload === '?') return null
  // 空载荷是「清空剪贴板」：忽略——远端不应能清掉本机剪贴板（spec 实现决定 3）。
  if (payload === '') return null
  const text = decodeBase64Utf8(payload)
  return text === null ? null : { selection, text }
}

// decodeBase64Utf8 容忍缺 padding 与换行包裹的 base64，按 UTF-8 严格解码。
// 任何一步失败都返回 null，由调用方决定丢弃。
function decodeBase64Utf8(b64: string): string | null {
  try {
    const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4)
    const bin = atob(padded)
    const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0))
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    return null
  }
}
```

`terminalOsc52.test.ts` 全文：

```ts
import { describe, expect, it } from 'vitest'
import { parseOsc52 } from './terminalOsc52'

// 表驱动：每行一条缝级断言（缝 1：OSC 52 载荷解码，spec「测试决定」）。
const cases: Array<{ name: string; data: string; want: { selection: string; text: string } | null }> = [
  { name: '常规 clipboard 写', data: 'c;aGVsbG8=', want: { selection: 'c', text: 'hello' } },
  { name: 'primary selection 写', data: 's;5Lit5paH', want: { selection: 's', text: '中文' } },
  { name: '缺 padding', data: 'c;aGVsbG8', want: { selection: 'c', text: 'hello' } },
  { name: 'base64 被换行包裹', data: 'c;5Lit\n5paH', want: { selection: 'c', text: '中文' } },
  { name: '空 selection', data: ';aGVsbG8=', want: { selection: '', text: 'hello' } },
  { name: '读查询不写', data: 'c;?', want: null },
  { name: '空载荷（清剪贴板）不写', data: 'c;', want: null },
  { name: '无分号不写', data: 'c', want: null },
  { name: '坏 base64 不写', data: 'c;@@@@', want: null },
  { name: '坏 UTF-8 不写', data: 'c;/w==', want: null },
]

describe('parseOsc52', () => {
  for (const c of cases) {
    it(c.name, () => {
      expect(parseOsc52(c.data)).toEqual(c.want)
    })
  }

  // roundtrip 属性测试：TextEncoder→btoa 编码 ∘ parseOsc52 解码对随机文本恒等，
  // 一条属性覆盖多字节、代理对、控制字符一整族序列化边界（缺陷族·序列化边界）。
  it('roundtrip：任意文本经 base64 编码后解码恒等', () => {
    const alphabet = 'aZ0 九;q✓\x1b]52;. '
    for (let i = 0; i < 200; i++) {
      let s = ''
      const n = 1 + Math.floor(Math.random() * 80)
      for (let j = 0; j < n; j++) s += alphabet[Math.floor(Math.random() * alphabet.length)]
      const bytes = new TextEncoder().encode(s)
      let bin = ''
      for (const b of bytes) bin += String.fromCharCode(b)
      expect(parseOsc52(`c;${btoa(bin)}`)).toEqual({ selection: 'c', text: s })
    }
  })
})
```

## Task 2（缝 2）：TerminalTab 注册 handler + 三道门 + 失败提示

文件：`web/src/app/workbench/TerminalTab.tsx`、`web/src/app/workbench/TerminalTab.test.tsx`、`web/src/app/workbench/terminalDebug.ts`。
测试范围：只跑 `npx vitest run src/app/workbench/TerminalTab.test.tsx`（既有 47 支必须保持全绿）。

1. 测试替身改造 + 新增下述 7 支测试 → 跑红（未注册 handler / 无 parser 键）。
2. 实现下述三处改动 → 跑绿（47 + 新增全过）。
3. 提交（git add 仅这三个文件）。

### 2a. terminalDebug.ts 追加（文件末尾）

```ts
// logTermOsc52 记一次 OSC 52 的处置结果（B300）。
//
// 参数：label 是终端标识；event 是短名（copy = 发起写入 / skip = 门挡下）；
// extra 带 selection、字符数或拦截原因。
// 被门挡下也必须打：「TUI 说复制了、本机剪贴板没变」要能分清是
// 序列没到、门挡了、还是浏览器拒了——三者的处置完全不同。
export function logTermOsc52(label: string, event: string, extra?: Record<string, unknown>): void {
  if (!terminalDebugEnabled()) return
  console.debug('[term:osc52]', { 终端: label, 事件: event, ...extra })
}
```

### 2b. TerminalTab.tsx 三处改动

- import 增两行：`import { parseOsc52 } from './terminalOsc52'`；terminalDebug 的既有 import 列表加 `logTermOsc52`。
- 组件 state 区（`const [dead, setDead] = ...` 附近）加：

```ts
  // copyNotice 是 OSC 52 复制失败的用户提示（B300）。成功不提示——TUI 自带
  // 复制反馈，再加成功提示是噪声；失败必须出声，否则就是「以为复制了」的老问题。
  const [copyNotice, setCopyNotice] = useState<string | null>(null)
```

- 挂载 effect 内：`let reassertPending = false` 旁加 `let noticeTimer = 0`；在 `term.onData((d) => {...})` 注册块之后加：

```ts
    // OSC 52：TUI（grok / opencode 等）复制时发的「终端，请写剪贴板」序列。
    // xterm 默认丢弃它——这就是「TUI 说复制了、本机剪贴板没变」的根因（B300）。
    // 门按序：replay 门（积压重放里的历史复制不许改写现在的剪贴板；旧服务端
    // 'drop-all' 说明不了回放长度，按 spec 不设门）→ active 门（后台 keep-alive
    // 的 tab 不许动用户剪贴板）→ 载荷门（'?' / 空载荷在 parseOsc52 里拦截）。
    // 写失败必须给用户可见提示：静默失败等于「以为复制了」。
    const osc52 = term.parser.registerOscHandler(52, (data) => {
      if (hostReply === 'replay') {
        logTermOsc52(label, 'skip', { 原因: 'replay' })
        return true
      }
      if (!activeRef.current) {
        logTermOsc52(label, 'skip', { 原因: 'inactive' })
        return true
      }
      const parsed = parseOsc52(data)
      if (parsed === null) return true
      logTermOsc52(label, 'copy', { selection: parsed.selection, 字符数: parsed.text.length })
      navigator.clipboard.writeText(parsed.text).then(
        () => setCopyNotice(null),
        () => {
          const msg = '复制到本机剪贴板失败（浏览器未授权或页面未聚焦），可选中文字后用右键复制'
          setCopyNotice(msg)
          if (noticeTimer !== 0) window.clearTimeout(noticeTimer)
          noticeTimer = window.setTimeout(() => {
            noticeTimer = 0
            setCopyNotice(null)
          }, 6000)
        },
      )
      return true
    })
```

- effect cleanup（`inputFix.dispose()` 附近）加 `window.clearTimeout(noticeTimer)` 与 `osc52.dispose()`。
- JSX：error 块之后、exit 块之前加：

```tsx
      {copyNotice !== null && (
        <div data-testid="copy-notice" className="border-t px-3 py-1.5 text-xs text-destructive">
          {copyNotice}
        </div>
      )}
```

### 2c. TerminalTab.test.tsx：替身 + 7 支测试

替身改造（模块级）：

```ts
// osc52Handler 收着组件注册的 OSC 52 回调，测试直接喂它来驱动各道门。
let osc52Handler: ((data: string) => boolean) | undefined
// writeText 替身：clipboard 写入的观测点（jsdom 没有 navigator.clipboard）。
const writeText = vi.fn(() => Promise.resolve())
```

- `termInstance` 加一个键：

```ts
  parser: {
    registerOscHandler: vi.fn((ident: number, cb: (data: string) => boolean) => {
      if (ident === 52) osc52Handler = cb
      return { dispose: vi.fn() }
    }),
  },
```

- 既有 `beforeAll` 里追加（jsdom 没有 navigator.clipboard，defineProperty 可覆盖只读属性）：

```ts
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
```

- 既有 `beforeEach` 里追加：`osc52Handler = undefined`、`writeText.mockClear()`。

新测试（全部经 `connectPty.mock.calls[0][0]` 拿 opts，照抄本文件既有惯用法）：

```ts
  it('挂载即注册 52 号 OSC handler（B300）', async () => {
    render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    expect(termInstance.parser.registerOscHandler).toHaveBeenCalledWith(52, expect.any(Function))
    expect(osc52Handler).toBeTypeOf('function')
  })

  it('活流 + 激活 + 合法载荷 → 写本机剪贴板（B300）', async () => {
    render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    const opts = connectPty.mock.calls[0][0]
    opts.onAttached({ since: 0, truncated: false, backlog_bytes: 0 })
    osc52Handler!('c;aGVsbG8=')
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('hello'))
  })

  it('积压重放期间不写、回放结束恢复写（B300 重放门）', async () => {
    render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    const opts = connectPty.mock.calls[0][0]
    opts.onAttached({ since: 0, truncated: false, backlog_bytes: 10 })
    osc52Handler!('c;aGVsbG8=')
    expect(writeText).not.toHaveBeenCalled()
    // 回放的最后一帧：write 的完成回调把 hostReply 转成 live——替身必须替它调
    termInstance.write.mockImplementationOnce((_data: unknown, cb?: () => void) => { cb?.() })
    opts.onData(new Uint8Array(10))
    osc52Handler!('c;aGVsbG8=')
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('hello'))
  })

  it('后台 tab 的 keep-alive 解析不写剪贴板（B300 active 门）', async () => {
    render(<TerminalTab base={WS} seq={1} active={false} onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onAttached({ since: 0, truncated: false, backlog_bytes: 0 })
    osc52Handler!('c;aGVsbG8=')
    expect(writeText).not.toHaveBeenCalled()
  })

  it('读查询与清剪贴板请求都不写（B300 载荷门）', async () => {
    render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onAttached({ since: 0, truncated: false, backlog_bytes: 0 })
    osc52Handler!('c;?')
    osc52Handler!('c;')
    osc52Handler!('nosemi')
    expect(writeText).not.toHaveBeenCalled()
  })

  it('写入失败给出可见提示（B300 静默失败族）', async () => {
    render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onAttached({ since: 0, truncated: false, backlog_bytes: 0 })
    writeText.mockRejectedValueOnce(new Error('denied'))
    osc52Handler!('c;aGVsbG8=')
    await waitFor(() => expect(screen.getByTestId('copy-notice')).toHaveTextContent('失败'))
  })

  it('写入成功不出提示（成功提示是噪声，TUI 自带反馈）', async () => {
    render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    connectPty.mock.calls[0][0].onAttached({ since: 0, truncated: false, backlog_bytes: 0 })
    osc52Handler!('c;aGVsbG8=')
    await waitFor(() => expect(writeText).toHaveBeenCalled())
    expect(screen.queryByTestId('copy-notice')).toBeNull()
  })
```

注意：`onAttached` / `onData` 的实参形态与 `connectPty` 的 `PtyHandle` 回调一致（`web/src/api/pty.ts`）；`onData` 的 Uint8Array 长度必须等于 `backlog_bytes`（10）才能让 `replayLeft` 归零。

## Task 3：全量门禁（编译全量 + 全量测试 + lint 不新增）

测试范围：`cd web && npm run typecheck && npm test && npm run lint`。
预期：typecheck 通过；**1181 + 新增全部 passed**；lint problems ≤ 23（5 errors / 18 warnings 基线不新增）。有红即修，修复只许在 Task 1/2 触及的文件内。完成后若有未提交改动一并提交（仍只 add 本卡三个文件）。

## 真机清单（acceptance 用，本 task 由协调者执行，不派发）

1. 系统浏览器：远程终端 `printf '\x1b]52;c;%s\x07' "$(printf 'hello-b300' | base64)"` → 本机 ⌘V 粘出 `hello-b300`。
2. 桌面壳（WKWebView）同 1 复验。
3. grok CLI 真选中复制 → 本机 ⌘V。
4. opencode TUI 真选中复制 → 本机 ⌘V。
5. 刷新工作台页面 → 本机剪贴板内容不被终端历史改写。
6. 任一 TUI 不发 OSC 52 → 如实记录，升级裁决（不静默算过）。

## 五项检查结论

1. **缺陷族逐族**：生命周期——handler 随 effect 注册、cleanup `dispose()`，notice timer 在 cleanup 清掉，无孤儿；静默失败——失败有可见提示 + 取证日志，skip 有日志，成功静默是设计决定（TUI 自带反馈）；跨平台——WKWebView 剪贴板需用户激活，失败提示兜底 + 真机清单第 2 项覆盖；假红/假绿——测试锁 TerminalTab 依赖的 parseOsc52 行为与注册行为，非 xterm 内部，clipboard 替身 afterEach 清；门禁绕过——写剪贴板唯一入口是 52 handler，门与动作同步执行无 TOCTOU 窗口。
2. **序列化边界**：base64+UTF-8 解码即边界，roundtrip 属性测试覆盖；`?`/空载荷返回 null 与「写文本」用可空返回区分，无「缺失 vs 零值」混淆面。
3. **上下文预算**：Task1 圈 `terminalOsc52.*`；Task2 圈 `TerminalTab.tsx` / `TerminalTab.test.tsx` / `terminalDebug.ts`；均有界。
4. **类型标注（边界型真机清单）**：见上节六条。
5. **接缝覆盖（双向）**：测试→缝——terminalOsc52.test 入口 `parseOsc52`（缝1），TerminalTab 新测试入口 `osc52Handler`（缝2），真机清单（缝3）；缝→测试——缝1 表驱动 + roundtrip，缝2 七支门断言，缝3 真机六条。无内部锁；无条件退路。

## 自审三查

- **spec 覆盖**：故事1（TUI 复制到本机）← Task1+Task2+真机1-4；故事2（失败有提示）← Task2 失败提示 + 真机兜底；故事3（重放不改写）← Task2 replay 门 + 真机5。
- **占位符扫描**：全文无 TBD/「适当处理」；Task2 测试声明使用「断言逐条列全 + 照抄既有 harness（`TerminalTab.test.tsx` 既有惯用法）」例外——测试体已给全，无骨架。
- **跨 task 签名一致**：`parseOsc52` / `logTermOsc52` 在 Interfaces 与 Task1/2 代码块中逐字一致。
