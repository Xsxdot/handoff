# B268 实现计划：键盘与鼠标对齐原生

读者：对仓库零上下文的执行者。对照 `docs/superpowers/specs/b268.md`（已批准，L2）。不改 `isTerminalHostResponse`、不碰 B269。

基线（动手前复核）：`cd web && npx vitest run src/app/workbench/terminalWheel.test.ts src/app/workbench/terminalInput.test.ts src/app/workbench/TerminalTab.test.tsx src/app/tree/ProjectTree.test.tsx` 应全绿。红了先停，不要在红基线上叠行为。

图：`TerminalTab` / `installTerminalInputFix` 在图（`d_web`，错位，不迁）。`altBufferWheelSgr` 未覆盖——本卡把它换成 `altBufferWheelReports`，仍记覆盖债，不跑 absorb。

---

## Task 1 — 滚轮报告：编码、修饰、横滑、划词放行

**缝 3。** 改 `web/src/app/workbench/terminalWheel.ts`（整文件替换）+ 其测试 + `TerminalTab.tsx` 的 wheel 调用。

**Consumes：** `WheelEvent` 的 delta/修饰/clientXY；`term.cols/rows`；`term.modes.mouseTrackingMode`；可选 `term._core.coreMouseService.activeEncoding`（xterm 5.5 公开 API 没有 encoding，见下）。

**Produces：**

```ts
export interface PointerRect { left: number; top: number; width: number; height: number }
export function pointerCell(clientX: number, clientY: number, rect: PointerRect, cols: number, rows: number): { col: number; row: number }
export function wheelForcesSelection(ev: { altKey: boolean; shiftKey: boolean }, isMac: boolean): boolean
export function mouseEncodingOf(term: { _core?: { coreMouseService?: { activeEncoding?: string } } }): 'SGR' | 'SGR_PIXELS' | 'DEFAULT'
export function altBufferWheelReports(p: {
  deltaX: number; deltaY: number; cellWidth: number; cellHeight: number
  remainder: { x: number; y: number }
  col: number; row: number; pixelX: number; pixelY: number
  shift: boolean; alt: boolean; ctrl: boolean
  encoding: 'SGR' | 'SGR_PIXELS' | 'DEFAULT'
}): string
```

删掉 `altBufferWheelSgr`（禁止留一个写死 1006 的旧入口给调用方走错）。

**测试范围：** `cd web && npx vitest run src/app/workbench/terminalWheel.test.ts src/app/workbench/TerminalTab.test.tsx`

### Step 1 — 基线

跑上面那条 vitest。记下通过数。

### Step 2 — 红：新报告形状（锁缝断言）

把 `terminalWheel.test.ts` 整文件换成下面。先跑，应红在 `altBufferWheelReports` / `wheelForcesSelection` 未导出。

```ts
import { describe, expect, it } from 'vitest'
import {
  altBufferWheelReports, mouseEncodingOf, pointerCell, wheelForcesSelection,
} from './terminalWheel'

describe('pointerCell', () => {
  const rect = { left: 0, top: 0, width: 800, height: 480 }
  it('按格子算 1-based 行列', () => {
    expect(pointerCell(50, 50, rect, 100, 30)).toEqual({ col: 7, row: 4 })
  })
  it('贴边不超过 cols/rows', () => {
    expect(pointerCell(799, 479, rect, 100, 30)).toEqual({ col: 100, row: 30 })
  })
})

describe('wheelForcesSelection', () => {
  it('Mac 上 Option 划词，Shift 不划', () => {
    expect(wheelForcesSelection({ altKey: true, shiftKey: false }, true)).toBe(true)
    expect(wheelForcesSelection({ altKey: false, shiftKey: true }, true)).toBe(false)
  })
  it('非 Mac 上 Shift 划词', () => {
    expect(wheelForcesSelection({ altKey: false, shiftKey: true }, false)).toBe(true)
    expect(wheelForcesSelection({ altKey: true, shiftKey: false }, false)).toBe(false)
  })
})

describe('mouseEncodingOf', () => {
  it('读 xterm core 的 activeEncoding，读不到当 SGR', () => {
    expect(mouseEncodingOf({ _core: { coreMouseService: { activeEncoding: 'SGR_PIXELS' } } })).toBe('SGR_PIXELS')
    expect(mouseEncodingOf({ _core: { coreMouseService: { activeEncoding: 'DEFAULT' } } })).toBe('DEFAULT')
    expect(mouseEncodingOf({})).toBe('SGR')
  })
})

describe('altBufferWheelReports', () => {
  const base = {
    cellWidth: 8, cellHeight: 16, remainder: { x: 0, y: 0 },
    col: 10, row: 4, pixelX: 80, pixelY: 64,
    shift: false, alt: false, ctrl: false, encoding: 'SGR' as const,
  }
  it('凑满一行才发，坐标是指针格子', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: -8 })).toBe('')
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: -8 })).toBe('\x1b[<64;10;4M')
  })
  it('一次像素距离换成多格同坐标报告', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: -160, col: 12, row: 5 }))
      .toBe('\x1b[<64;12;5M'.repeat(10))
  })
  it('向下是 65，一次最多 32 格', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: 1600, col: 3, row: 8 }))
      .toBe('\x1b[<65;3;8M'.repeat(32))
  })
  it('横滑是 66/67，与纵滑分开累计', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: -160, deltaY: 0 }))
      .toBe('\x1b[<66;10;4M'.repeat(10))
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 16, deltaY: 0 }))
      .toBe('\x1b[<67;10;4M')
  })
  it('Ctrl 加进按钮码（64+16=80）', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: -16, ctrl: true }))
      .toBe('\x1b[<80;10;4M')
  })
  it('SGR_PIXELS 用像素坐标不是格子', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({
      ...base, remainder: rem, deltaX: 0, deltaY: -16, encoding: 'SGR_PIXELS', pixelX: 80, pixelY: 64,
    })).toBe('\x1b[<64;80;64M')
  })
  it('非法格子不发', () => {
    const rem = { x: 0, y: 0 }
    expect(altBufferWheelReports({ ...base, remainder: rem, deltaX: 0, deltaY: -16, col: 0, row: 1 })).toBe('')
  })
})
```

### Step 3 — 绿：实现 `terminalWheel.ts`

整文件替换为：

```ts
// terminalWheel —— alt-screen 鼠标追踪下，把滚轮像素折成多格鼠标报告。
//
// 职责：按指针格子（或像素编码下的像素）生成与 xterm CoreMouseService 同形的
//       滚轮序列。xterm 每个 DOM wheel 只发一格；这里补格数、横滑、修饰位。
// 边界：
//   - 不写 PTY、不读 xterm 实例（encoding 由调用方传入）
//   - 不造假坐标。col/row 必须是指针格子
//   - 不改成方向键
//   - 不在这里决定「划词放行」——那是 wheelForcesSelection，调用方据此根本不调用本函数

const wheelTickCap = 32

export interface PointerRect {
  left: number
  top: number
  width: number
  height: number
}

export function pointerCell(
  clientX: number,
  clientY: number,
  rect: PointerRect,
  cols: number,
  rows: number,
): { col: number; row: number } {
  if (cols <= 0 || rows <= 0 || rect.width <= 0 || rect.height <= 0) {
    return { col: 0, row: 0 }
  }
  const cellW = rect.width / cols
  const cellH = rect.height / rows
  const col = Math.min(cols, Math.max(1, Math.floor((clientX - rect.left) / cellW) + 1))
  const row = Math.min(rows, Math.max(1, Math.floor((clientY - rect.top) / cellH) + 1))
  return { col, row }
}

// wheelForcesSelection 对齐 xterm SelectionService.shouldForceSelection：
// Mac + macOptionClickForcesSelection 时 Option 强制划词；其它平台是 Shift。
export function wheelForcesSelection(
  ev: { altKey: boolean; shiftKey: boolean },
  isMac: boolean,
): boolean {
  return isMac ? ev.altKey : ev.shiftKey
}

export function mouseEncodingOf(
  term: { _core?: { coreMouseService?: { activeEncoding?: string } } },
): 'SGR' | 'SGR_PIXELS' | 'DEFAULT' {
  const enc = term._core?.coreMouseService?.activeEncoding
  if (enc === 'SGR_PIXELS' || enc === 'SGR' || enc === 'DEFAULT') return enc
  return 'SGR'
}

function eventCode(wheelBtn: 64 | 65 | 66 | 67, shift: boolean, alt: boolean, ctrl: boolean): number {
  return wheelBtn | (shift ? 4 : 0) | (alt ? 8 : 0) | (ctrl ? 16 : 0)
}

function encodeOne(
  encoding: 'SGR' | 'SGR_PIXELS' | 'DEFAULT',
  code: number,
  col: number,
  row: number,
  pixelX: number,
  pixelY: number,
): string {
  if (encoding === 'SGR_PIXELS') return `\x1b[<${code};${pixelX};${pixelY}M`
  if (encoding === 'DEFAULT') {
    const pb = code + 32
    const px = col + 32
    const py = row + 32
    if (pb > 255 || px > 255 || py > 255) return ''
    return `\x1b[M${String.fromCharCode(pb, px, py)}`
  }
  return `\x1b[<${code};${col};${row}M`
}

function ticksFromDelta(delta: number, cell: number, rem: { value: number }): number {
  if (delta === 0) return 0
  const px = cell > 0 ? cell : 16
  rem.value += delta / px
  const lines = Math.trunc(rem.value)
  rem.value -= lines
  if (lines === 0) return 0
  return Math.min(Math.abs(lines), wheelTickCap) * Math.sign(lines)
}

export function altBufferWheelReports(p: {
  deltaX: number; deltaY: number; cellWidth: number; cellHeight: number
  remainder: { x: number; y: number }
  col: number; row: number; pixelX: number; pixelY: number
  shift: boolean; alt: boolean; ctrl: boolean
  encoding: 'SGR' | 'SGR_PIXELS' | 'DEFAULT'
}): string {
  if (!Number.isFinite(p.col) || !Number.isFinite(p.row) || p.col < 1 || p.row < 1) return ''
  const yHold = { value: p.remainder.y }
  const xHold = { value: p.remainder.x }
  const yTicks = ticksFromDelta(p.deltaY, p.cellHeight, yHold)
  const xTicks = ticksFromDelta(p.deltaX, p.cellWidth, xHold)
  p.remainder.y = yHold.value
  p.remainder.x = xHold.value
  let out = ''
  if (yTicks !== 0) {
    const btn: 64 | 65 = yTicks < 0 ? 64 : 65
    const one = encodeOne(p.encoding, eventCode(btn, p.shift, p.alt, p.ctrl), p.col, p.row, p.pixelX, p.pixelY)
    out += one.repeat(Math.abs(yTicks))
  }
  if (xTicks !== 0) {
    const btn: 66 | 67 = xTicks < 0 ? 66 : 67
    const one = encodeOne(p.encoding, eventCode(btn, p.shift, p.alt, p.ctrl), p.col, p.row, p.pixelX, p.pixelY)
    out += one.repeat(Math.abs(xTicks))
  }
  return out
}
```

纵滑先于横滑拼接。`lines` 为负时 `Math.min(abs, cap)*sign` 得到负格数。

跑 Step 2 的测试至绿。

### Step 4 — 红：TerminalTab 调用新 API

改 `web/src/app/workbench/TerminalTab.tsx`：

1. `new Terminal({...})` 加上 `macOptionIsMeta: true`、`macOptionClickForcesSelection: true`（保留现有 font/theme/scrollback）。
2. 滚轮 handler 换成（逻辑必须等价，不要再调用 `altBufferWheelSgr`）：

```ts
    const wheelRemainder = { x: 0, y: 0 }
    const isMac = /Mac|iPhone|iPod|iPad/.test(navigator.platform || navigator.userAgent)
    term.attachCustomWheelEventHandler((ev) => {
      if (term.buffer.active.type !== 'alternate') return true
      if (term.modes.mouseTrackingMode === 'none') return true
      if (wheelForcesSelection(ev, isMac)) return true
      if (ev.deltaX === 0 && ev.deltaY === 0) return true
      const screenEl = (host.querySelector('.xterm-screen') as HTMLElement | null) ?? host
      const rect = screenEl.getBoundingClientRect()
      const cellH = term.rows > 0 ? rect.height / term.rows : 16
      const cellW = term.cols > 0 ? rect.width / term.cols : 8
      const { col, row } = pointerCell(ev.clientX, ev.clientY, rect, term.cols, term.rows)
      const pixelX = Math.max(0, Math.floor(ev.clientX - rect.left))
      const pixelY = Math.max(0, Math.floor(ev.clientY - rect.top))
      const seq = altBufferWheelReports({
        deltaX: ev.deltaX, deltaY: ev.deltaY, cellWidth: cellW, cellHeight: cellH,
        remainder: wheelRemainder, col, row, pixelX, pixelY,
        shift: ev.shiftKey, alt: ev.altKey, ctrl: ev.ctrlKey,
        encoding: mouseEncodingOf(term),
      })
      if (seq !== '') {
        logTermWheel(label, seq.split('\x1b').length - 1, seq)
        term.input(seq)
      }
      return false
    })
```

import 改为 `altBufferWheelReports, mouseEncodingOf, pointerCell, wheelForcesSelection`。

3. 改 `TerminalTab.test.tsx`：
   - 现有「按像素连发」那条期望仍是 `'\x1b[<64;7;4M'.repeat(10)`（host 上没有 `.xterm-screen`，退回 host，坐标不变）。
   - 新增：

```ts
  it('Option（Mac 划词）时不拦截滚轮', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    const handler = termInstance.attachCustomWheelEventHandler.mock.calls[0][0] as (ev: {
      deltaY: number; altKey?: boolean; shiftKey?: boolean; deltaX?: number
      clientX?: number; clientY?: number
    }) => boolean
    termInstance.buffer.active.type = 'alternate'
    termInstance.modes.mouseTrackingMode = 'vt200'
    const mac = /Mac|iPhone|iPod|iPad/.test(navigator.platform || navigator.userAgent)
    if (mac) {
      expect(handler({ deltaY: -160, altKey: true, clientX: 50, clientY: 50 })).toBe(true)
      expect(termInstance.input).not.toHaveBeenCalled()
    } else {
      expect(handler({ deltaY: -16, shiftKey: true, clientX: 50, clientY: 50 })).toBe(true)
      expect(termInstance.input).not.toHaveBeenCalled()
    }
  })

  it('横滑发 66', async () => {
    const spy = vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
      width: 800, height: 480, x: 0, y: 0, top: 0, left: 0, right: 800, bottom: 480,
      toJSON: () => ({}),
    } as DOMRect)
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    const handler = termInstance.attachCustomWheelEventHandler.mock.calls[0][0] as (ev: {
      deltaX: number; deltaY: number; clientX: number; clientY: number
    }) => boolean
    termInstance.buffer.active.type = 'alternate'
    termInstance.modes.mouseTrackingMode = 'vt200'
    expect(handler({ deltaX: -80, deltaY: 0, clientX: 50, clientY: 50 })).toBe(false)
    expect(termInstance.input).toHaveBeenCalledWith('\x1b[<66;7;4M'.repeat(10))
    spy.mockRestore()
  })
```

cellW = 800/100 = 8；deltaX -80 / 8 = -10 格 → 66 重复 10。指针 (50,50) 仍是 col 7 row 4。

4. 断言构造选项（可放在「没有会话 id」那条里或单独）：

```ts
import { Terminal } from '@xterm/xterm'
// ...
expect(Terminal).toHaveBeenCalledWith(expect.objectContaining({
  macOptionIsMeta: true,
  macOptionClickForcesSelection: true,
}))
```

`Terminal` 已被 `vi.mock`，从 `@xterm/xterm` import 拿到的就是 mock。

先跑 TerminalTab 测试：新用例应红（handler 还认 `altBufferWheelSgr` 或还没放行划词）。

### Step 5 — 绿：接上 Step 4 的 handler

按 Step 4 片段改完，跑 `terminalWheel` + `TerminalTab` 至绿。

### Step 6 — 日志

`logTermWheel` 已有。在 handler 里 **划词放行** 也打一条（成功路径不能静默）：

在 `terminalDebug.ts` 增加：

```ts
export function logTermWheelBypass(label: string, reason: string): void {
  if (!terminalDebugEnabled()) return
  console.debug('[term:wheel]', { 终端: label, 放行: reason })
}
```

调用：`logTermWheelBypass(label, 'forces-selection')`。开关仍是 `handoff.debug.terminal`，不要新开 `console.log`。

### Step 7 — 注释

`terminalWheel.ts` 文件头已含职责/边界。`mouseEncodingOf` 必须写清：**为什么读 `_core`**——公开 `IModes` 没有 encoding，写死 SGR 会在 `SGR_PIXELS` 下把格子当像素。`wheelForcesSelection` 写清与 xterm `shouldForceSelection` 对齐、且依赖 `macOptionClickForcesSelection: true`。

### Step 8 — 提交（本卡最后一个 task 再提交也行；若分 commit，信息用）

`B268 滚轮报告走 xterm 编码并补横滑修饰`

测试范围收尾：触及包 vitest 如上；`cd web && npx tsc --noEmit`（或项目现用的类型检查；没有 tsc 脚本就 `npx vitest run` 能过编译即此步的编译闸）。

---

## Task 2 — 键盘：Option Meta 已在 Task 1 打开；⌘←/⌘→/⌘K 走唯一 key handler

**缝 1。** 只改 `web/src/app/workbench/terminalInput.ts` + `terminalInput.test.ts`。`attachCustomKeyEventHandler` 只有一个槽，**禁止**在 TerminalTab 再挂一个。

**Consumes：** `KeyboardEvent`；`term.input` / `term.clear`。

**Produces：** `installTerminalInputFix` 签名不变。

**测试范围：** `cd web && npx vitest run src/app/workbench/terminalInput.test.ts`

### Step 1 — 扩 key() helper 支持 metaKey/ctrlKey

`terminalInput.test.ts` 的 `key()` 增加 `metaKey?: boolean; ctrlKey?: boolean`，传入 `new KeyboardEvent` 的 init。

### Step 2 — 红

在 `terminalInput.test.ts` 末尾加：

```ts
describe('mac 终端键：⌘←/⌘→/⌘K', () => {
  it('⌘← 发出 0x01，⌘→ 发出 0x05，且不经普通字母路径双发', () => {
    rig = makeRig(true)
    rig.ta.dispatchEvent(key('keydown', { key: 'ArrowLeft', keyCode: 37, metaKey: true }))
    rig.ta.dispatchEvent(key('keydown', { key: 'ArrowRight', keyCode: 39, metaKey: true }))
    expect(rig.data).toEqual(['\x01', '\x05'])
  })

  it('⌘K 调用 clear 且 onData 没有任何字节', () => {
    rig = makeRig(true)
    const clear = vi.spyOn(rig.term, 'clear')
    const ev = key('keydown', { key: 'k', keyCode: 75, metaKey: true })
    rig.ta.dispatchEvent(ev)
    expect(clear).toHaveBeenCalledTimes(1)
    expect(rig.data).toEqual([])
    expect(ev.defaultPrevented).toBe(true)
  })

  it('Ctrl+K 不走清屏（readline 删到行尾仍归 xterm）', () => {
    rig = makeRig(true)
    const clear = vi.spyOn(rig.term, 'clear')
    rig.ta.dispatchEvent(key('keydown', { key: 'k', keyCode: 75, ctrlKey: true }))
    expect(clear).not.toHaveBeenCalled()
  })
})
```

跑红：⌘← 目前 xterm 对 meta+arrow 是忽略，`data` 为空。

### Step 3 — 绿：扩展 customKeyEventHandler

在 `installTerminalInputFix` 现有 `attachCustomKeyEventHandler` 里，**keydown 分支加在 injected-text 的 keypress 判断之前**：

```ts
  term.attachCustomKeyEventHandler((ev) => {
    if (disposed) return true
    if (ev.type === 'keydown') {
      if (ev.metaKey && !ev.ctrlKey && !ev.altKey && ev.key === 'ArrowLeft') {
        ev.preventDefault()
        ev.stopPropagation()
        logTermFix(label, '⌘← 行首', '\x01')
        term.input('\x01')
        return false
      }
      if (ev.metaKey && !ev.ctrlKey && !ev.altKey && ev.key === 'ArrowRight') {
        ev.preventDefault()
        ev.stopPropagation()
        logTermFix(label, '⌘→ 行尾', '\x05')
        term.input('\x05')
        return false
      }
      if (ev.metaKey && !ev.ctrlKey && !ev.shiftKey && ev.key.toLowerCase() === 'k') {
        ev.preventDefault()
        ev.stopPropagation()
        logTermFix(label, '⌘K 清屏', '')
        term.clear()
        return false
      }
    }
    if (ev.type !== 'keypress') return true
    if (!isInjectedText(ev)) return true
    logTermFix(label, '拦下注入键事件', ev.key)
    return false
  })
```

沿用 `logTermFix`，不要 `console.log`。文件头补一句：本模块现在还处理 ⌘←/⌘→/⌘K；清屏不上送 PTY。

跑 Step 2 至绿；原有 espanso / IME 用例必须仍绿。

### Step 4 — 注释

在 ⌘K 分支写清 **为什么不上送 `\x0c`**：TUI 会当成输入（B267 方向键同类）。`term.clear()` 把当前行留作第 0 行并丢掉 scrollback（xterm `Terminal.clear`，`node_modules/@xterm/xterm/src/browser/Terminal.ts` 约 1224 行）。

---

## Task 3 — 左栏 ⌘K：终端焦点时不抢搜索

**缝 2。** `web/src/app/tree/ProjectTree.tsx` + `ProjectTree.test.tsx`。

**测试范围：** `cd web && npx vitest run src/app/tree/ProjectTree.test.tsx`

### Step 1 — 红

在现有「⌘K 聚焦搜索框」旁加：

```ts
  it('焦点在 xterm textarea 时 ⌘K 不抢搜索', () => {
    render(<ProjectTree {...props()} />)
    const ta = document.createElement('textarea')
    ta.className = 'xterm-helper-textarea'
    document.body.appendChild(ta)
    ta.focus()
    const search = screen.getByPlaceholderText('搜索项目、机器或任务')
    fireEvent.keyDown(window, { key: 'k', metaKey: true })
    expect(document.activeElement).toBe(ta)
    expect(document.activeElement).not.toBe(search)
    ta.remove()
  })
```

应红：当前 listener 无条件 `searchRef.focus()`。

### Step 2 — 绿

`ProjectTree.tsx` 的 `onKey` 改成：

```ts
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.key.toLowerCase() !== 'k') return
      if (e.defaultPrevented) return
      const el = document.activeElement
      if (el instanceof HTMLElement && (
        el.classList.contains('xterm-helper-textarea') || el.closest('.xterm') !== null
      )) return
      e.preventDefault()
      searchRef.current?.focus()
      searchRef.current?.select()
    }
```

保留冒泡阶段。注释改成：xterm **不处理** ⌘K，不会 stopPropagation；终端侧 handler 会 preventDefault+clear，这里再按焦点漏一次。Ctrl+K 在终端里由 xterm 处理并 stopPropagation，到不了这里。

原「⌘K 聚焦搜索框」「Ctrl+K 同样聚焦」必须仍绿。

### Step 3 — 日志 / 注释

这一步没有终端 debug 开关。不要新打 console。文件里那段「让位次序」注释按 Step 2 改准（旧注释写「xterm 会吞掉自己的按键」，对 ⌘K 是错的）。

---

## Task 4 — CHANGELOG 一行（无独立红绿）

`CHANGELOG.md` 未发布节加：

```
- **桌面终端按键和滚轮更接近系统终端。** Option 当 Meta；⌘←/⌘→ 到行首行尾；焦点在终端时 ⌘K 清屏（不写进 PTY），其它地方仍是左栏搜索。TUI 滚轮按 xterm 自己的鼠标编码连发，含横滑和修饰键；Option（Mac）/ Shift（其它）划词不再被自定义滚轮吃掉。
```

---

## 接缝覆盖

| 缝 | 锁它的测试入口 |
|---|---|
| `installTerminalInputFix` ← TerminalTab | `terminalInput.test.ts`「mac 终端键」 |
| `ProjectTree` window keydown | `ProjectTree.test.tsx`「焦点在 xterm textarea 时 ⌘K 不抢搜索」 |
| `altBufferWheelReports` ← TerminalTab | `terminalWheel.test.ts` 全组 + `TerminalTab.test.tsx` 横滑/划词/原竖直连发 |

无内部锁声明。无占位符例外（测试代码已写全）。

## 缺陷族

- **生命周期：** 无新进程/无 PTY 会话生命周期变化。切 tab 仍拆 xterm；清屏只影响当前实例。无，因为不新建宿主资源。
- **静默失败：** 读不到 `_core` encoding 时退 SGR 并在 `mouseEncodingOf` 走默认；划词放行有 `logTermWheelBypass`。⌘K 不上送，失败模式是「没清」——由 `clear` spy 锁住。
- **跨平台：** `wheelForcesSelection` / `isMac` 显式分 Mac vs 其它；jsdom 的 platform 可能不是 Mac，TerminalTab 划词用例按运行时 platform 二选一，禁止写死 `altKey` 在 Linux jsdom 上假绿。
- **假红/假绿：** ⌘K 变异必须是 `term.input('\x0c')`（spec）；编译仍过、`onData` 有字节才算打中。不要删 `clear()` 整行（容易让 spy 路径编译/逻辑一起塌）。
- **门禁：** 无新写路径。
- **序列化边界：** 无新 wire 字段。鼠标序列是终端协议不是 JSON；用表驱动字符串相等锁，不走 roundtrip。
- **枚举新值：** encoding 三值在 `mouseEncodingOf` 白名单，未知退 SGR。
- **安全属性：** 无。

## 真机清单（本卡由协调者执行，不派发）

1. zsh：Option+B/F 按词跳；⌘←/⌘→ 行首行尾。
2. 焦点在终端 ⌘K：画面清掉、提示符还在、zsh 没收到一个字；焦点在编辑器 ⌘K 仍是左栏搜索。
3. OpenCode/Grok：竖直滚轮仍按指针翻对话；横滑有反应或至少不花屏；Option+拖能划词。
4. 提示符不再出现 DA/OSC 乱码（B267 回归，本卡没改回包过滤器）。

## 图覆盖债

`altBufferWheelReports` / `pointerCell` / `wheelForcesSelection` / `mouseEncodingOf` / `isTerminalHostResponse` 未进 baseline。本卡不 absorb。
