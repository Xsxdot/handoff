# B302 实现计划：终端 TUI 的 Shift+Enter 发送 CSI u 换行

读者：对仓库没有上下文的前端执行者。依据 docs/superpowers/specs/b302.md（已批准，L2）和同目录台账；本卡只修工作台终端输入层，不改 Go/PTY、TERM、agent 二进制、Composer、布局或完整 Kitty keyboard protocol。

## 基线、图与已核事实

目标合并基线：cards/B302-spec。执行者只在当前任务分支工作，不切分支、不改 git 配置。

本计划出稿前已在当前基线真实复核以下判据：

~~~text
cd web && npm ci --ignore-scripts
  added 290 packages, and audited 291 packages in 3s
  found 0 vulnerabilities

cd web && npm test -- --run src/app/workbench/terminalInput.test.ts
  Test Files  1 passed (1)
  Tests  21 passed (21)
  另有 xterm/jsdom 的 Not implemented: HTMLCanvasElement's getContext() method... 提示，但进程成功退出。

cd web && npm test -- --run src/app/workbench/TerminalTab.test.tsx
  Test Files  1 passed (1)
  Tests  47 passed (47)

cd web && npm run typecheck
  成功退出，无错误输出。
~~~

如果执行环境尚无 web/node_modules，先执行上面的锁文件安装命令；安装产物不入 git。实现前必须重跑两组局部测试和类型检查，先确认基线仍绿；不要把首次缺少 vitest/tsc 的环境错误当成测试结果。

仓内存在 codegraph/，best 领域为 d_web_workbench；已按规定运行 go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . context d_web_workbench，但 Go 模块缓存为只读，原始结果是：

~~~text
go: downloading github.com/Xsxdot/charter/graph v0.10.0
go: writing go.mod cache: open /root/go/pkg/mod/cache/download/github.com/!xsxdot/charter/graph/@v/v0.10.0.mod173803237.tmp: read-only file system
open /root/go/pkg/mod/cache/download/github.com/!xsxdot/charter/graph/@v/v0.10.0.lock: read-only file system
~~~

因此本计划的调用面按源码 rg 核实，记一笔图覆盖债：

- 生产接缝是 web/src/app/workbench/terminalInput.ts:110 的 installTerminalInputFix；它在 web/src/app/workbench/TerminalTab.tsx:365 被真实调用。
- 唯一的 xterm 键盘槽是 installTerminalInputFix 内 term.attachCustomKeyEventHandler（terminalInput.ts:211）；TerminalTab 不得再挂第二个槽。
- web/src/app/workbench/terminalInput.test.ts 的 makeRig 使用真 Terminal、真实 textarea 与 onData，不是脱离生产调用方的纯函数夹具。
- TerminalTab.tsx:273-296 是既有消费链：term.onData((d: string) => ...) 过滤设备回包后，以 handle.send(new TextEncoder().encode(rest)) 进入 PTY；本卡不改这段。

已安装依赖的源码事实：web/package-lock.json:2569-2573 锁定 @xterm/xterm 5.5.0；web/node_modules/@xterm/xterm/src/common/input/Keyboard.ts:100-104 的 Enter 分支只读取 altKey，所以 Shift+Enter 当前也是 \r；src/browser/Terminal.ts:1001-1007 规定 custom handler 返回 false 时 xterm 不再处理该 keydown；typings/xterm.d.ts:1014-1040 的签名是 (event: KeyboardEvent) => boolean；src/common/InputHandler.ts:1939-1948 让 DECSET 1049 激活 alternate buffer；typings/xterm.d.ts:1461-1465,1527-1542 定义 buffer.active.type 为 normal 或 alternate。CoreTerminal.ts:150-159 要求异步 write 用 callback，CoreTerminal.ts:171-173 说明 term.input(data) 直接触发既有 data event。

## 文件范围与接口

| 文件 | 变更 | 边界 |
|---|---|---|
| web/src/app/workbench/terminalInput.ts | 在现有唯一 attachCustomKeyEventHandler 的 keydown 分支拦截精确的 alt-screen Shift+Enter | 只处理该组合；保留 Option、⌘←/⌘→、⌘K、注入文本与 dispose 逻辑 |
| web/src/app/workbench/terminalInput.test.ts | 在现有真 xterm harness 增加 alt-screen/主屏/修饰键断言 | 测 term.input、onData、默认行为；不抽无生产调用方的纯函数 |
| web/src/app/workbench/terminalDebug.ts | 给既有 logTermFix 增加 B302 动作字面量与注释 | 沿用 handoff.debug.terminal，不新增 logger/输出通道 |
| web/src/app/workbench/TerminalTab.tsx | 只读核对生产调用 | 不改；它已在 term.open() 后调用 installTerminalInputFix |
| web/src/api/pty.ts | 只读核对 PtyHandle.send | 不改；CSI u 仍以普通 PTY 字节进入既有链路 |

### Interfaces

Consumes：

~~~ts
export function installTerminalInputFix(
  term: Terminal,
  host: HTMLElement,
  label: string,
): TerminalInputFix

// 本 task 使用的 Terminal/DOM 表面
term.textarea: HTMLTextAreaElement | undefined
term.buffer.active.type: 'normal' | 'alternate'
term.onData(listener: (data: string) => void): IDisposable
term.attachCustomKeyEventHandler(handler: (event: KeyboardEvent) => boolean): void
term.input(data: string, wasUserInput?: boolean): void
KeyboardEvent: { type: string; key: string; shiftKey: boolean; altKey: boolean; ctrlKey: boolean; metaKey: boolean }
~~~

Produces：

~~~ts
export interface TerminalInputFix {
  dispose: () => void
}

// 对精确组合，term.input 交给已有 onData；TerminalTab 的已有消费签名保持不变
term.onData((data: string) => void): IDisposable
PtyHandle.send(bytes: Uint8Array): void
~~~

新增常量只在 terminalInput.ts 模块内使用，不新增 wire 字段、HTTP/WS/PTY 接口、枚举或序列化格式：

~~~ts
const TUI_SHIFT_ENTER = '\x1b[13;2u'
~~~

判定表固定为：

| 输入 | term.buffer.active.type | 结果 |
|---|---|---|
| Shift+Enter，且无 Alt/Ctrl/Meta | alternate | preventDefault、stopPropagation、记录 B302 日志、term.input('\x1b[13;2u')，返回 false；onData 只能收到该序列，不能收到 \r |
| Shift+Enter，且无 Alt/Ctrl/Meta | normal | 不进入 B302 分支，xterm 原样产生 \r |
| 裸 Enter、Alt+Enter、Ctrl+Enter、带额外 Meta 的 Enter | 任一 | 不进入 B302 分支；原有 xterm 键义保持 |
| Shift 后输入中文标点 ？/¥/% | 任一 | key !== 'Enter'，继续走现有 input/IME 补漏逻辑 |

## Task 1 — 唯一键盘接缝：alt-screen Shift+Enter

接缝：installTerminalInputFix(term: Terminal, host: HTMLElement, label: string): TerminalInputFix ← TerminalTab 已有真实调用。

文件集：

- 修改 web/src/app/workbench/terminalInput.ts（现有 handler，约 211-260 行）
- 修改 web/src/app/workbench/terminalDebug.ts（现有 logTermFix 类型与说明，约 109-127 行）
- 修改 web/src/app/workbench/terminalInput.test.ts（复用 Rig、makeRig、key，在现有输入边界 describe 后追加 B302 describe）
- 只读核对 web/src/app/workbench/TerminalTab.tsx:361-365、:273-296 与 web/src/api/pty.ts:55-60

测试范围：只跑 cd web && npm test -- --run src/app/workbench/terminalInput.test.ts；最后在本 task 收尾跑 cd web && npm test -- --run src/app/workbench/TerminalTab.test.tsx 与 cd web && npm run typecheck。不把全量前端测试归入本 task 的局部红绿循环。

### Step 1 — 基线复核（动手前）

在任何源码/测试修改前执行：

~~~bash
cd web && npm test -- --run src/app/workbench/terminalInput.test.ts
cd web && npm test -- --run src/app/workbench/TerminalTab.test.tsx
cd web && npm run typecheck
~~~

判据：第一条保持现有 21 条测试全绿，第二条保持现有 47 条测试全绿，类型检查成功。允许出现已观察到的 jsdom canvas 提示，但进程必须成功退出；任一失败必须保留原始输出并停止叠加 B302 行为。

### Step 2 — 红：先锁真实 alt-screen 行为

在 web/src/app/workbench/terminalInput.test.ts 追加以下完整代码。enterAlternateBuffer 使用 xterm 真实解析的 DECSET 1049，并等待 write callback；key() 直接复用现有 helper，已经支持 shiftKey/altKey/ctrlKey/metaKey。

~~~ts
async function enterAlternateBuffer(r: Rig): Promise<void> {
  await new Promise<void>((resolve) => {
    r.term.write('\x1b[?1049h', () => resolve())
  })
  expect(r.term.buffer.active.type).toBe('alternate')
}

function dispatchEnter(
  r: Rig,
  modifiers: { shiftKey?: boolean; altKey?: boolean; ctrlKey?: boolean; metaKey?: boolean } = {},
): KeyboardEvent {
  const ev = key('keydown', { key: 'Enter', keyCode: 13, ...modifiers })
  r.ta.dispatchEvent(ev)
  return ev
}

describe('B302：alt-screen 的 Shift+Enter', () => {
  it('交替屏 Shift+Enter 发 CSI u，不再让 xterm 发 CR', async () => {
    rig = makeRig(true)
    await enterAlternateBuffer(rig)
    const input = vi.spyOn(rig.term, 'input')

    const ev = dispatchEnter(rig, { shiftKey: true })

    expect(input).toHaveBeenCalledTimes(1)
    expect(input).toHaveBeenCalledWith('\x1b[13;2u')
    expect(rig.data).toEqual(['\x1b[13;2u'])
    expect(rig.data).not.toContain('\r')
    expect(ev.defaultPrevented).toBe(true)
    expect(ev.cancelBubble).toBe(true)
  })

  it('主屏 Shift+Enter 不走补发，仍由 xterm 产生 CR', () => {
    rig = makeRig(true)
    const input = vi.spyOn(rig.term, 'input')

    dispatchEnter(rig, { shiftKey: true })

    expect(input).not.toHaveBeenCalled()
    expect(rig.data).toEqual(['\r'])
  })

  it('交替屏裸 Enter 仍走 xterm 的 CR', async () => {
    rig = makeRig(true)
    await enterAlternateBuffer(rig)
    const input = vi.spyOn(rig.term, 'input')

    dispatchEnter(rig)

    expect(input).not.toHaveBeenCalled()
    expect(rig.data).toEqual(['\r'])
  })

  it('交替屏 Alt+Enter、Ctrl+Enter 与带额外 Meta 的组合都不走补发', async () => {
    rig = makeRig(true)
    await enterAlternateBuffer(rig)
    const input = vi.spyOn(rig.term, 'input')

    dispatchEnter(rig, { altKey: true })
    dispatchEnter(rig, { ctrlKey: true })
    dispatchEnter(rig, { shiftKey: true, metaKey: true })

    expect(input).not.toHaveBeenCalled()
    expect(rig.data).toEqual(['\x1b\r', '\r', '\r'])
  })
})
~~~

立即执行：

~~~bash
cd web && npm test -- --run src/app/workbench/terminalInput.test.ts
~~~

预期：新增的交替屏 Shift+Enter 用例在未实现的基线失败，实际输出以测试进程为准；失败点必须体现现状收到 ['\r'] 或没有调用 term.input('\x1b[13;2u')。主屏、裸 Enter、Alt+Enter、Ctrl+Enter 边界用例可保持通过。不要把预期失败行号或计数写成未实际看到的输出。

### Step 3 — 绿：在现有 handler 内最小接入

在 terminalInput.ts 的 let disposed = false 前加入常量，并在同一个 term.attachCustomKeyEventHandler 的 keydown 分支最前面插入 B302 判断。不得在 TerminalTab.tsx 新增第二个 attachCustomKeyEventHandler。

常量及其职责注释：

~~~ts
// TUI 约定的单键 CSI u：13 是 Enter，2 是 Shift；只在 alternate buffer 发送。
// 这是一个单键兼容补漏，不是完整 Kitty keyboard protocol。
const TUI_SHIFT_ENTER = '\x1b[13;2u'
~~~

handler 的完整结果应保持既有分支，并在 Option/⌘ 分支之前加入下面这段：

~~~ts
  term.attachCustomKeyEventHandler((ev) => {
    if (disposed) return true
    if (ev.type === 'keypress') {
      if (optionMetaConsumed || optionHeld || optionMetaLetter(ev) !== null) {
        ev.preventDefault()
        ev.stopPropagation()
        return false
      }
    }
    if (ev.type === 'keydown') {
      if (
        ev.key === 'Enter' &&
        ev.shiftKey &&
        !ev.altKey &&
        !ev.ctrlKey &&
        !ev.metaKey &&
        term.buffer.active.type === 'alternate'
      ) {
        ev.preventDefault()
        ev.stopPropagation()
        logTermFix(label, 'Shift+Enter 换行', TUI_SHIFT_ENTER)
        term.input(TUI_SHIFT_ENTER)
        return false
      }
      const metaLetter = optionMetaLetter(ev)
      if (metaLetter !== null) {
        ev.preventDefault()
        ev.stopPropagation()
        optionMetaConsumed = true
        logTermFix(label, 'Option Meta', metaLetter)
        term.input(\x1b\${metaLetter})
        return false
      }
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
~~~

关键顺序：

1. 在 xterm 的 keydown 默认翻译前由 custom handler 判断，return false 阻止默认 \r。
2. 读取 term.buffer.active.type 的时机是每次 keydown，而不是安装时缓存；TUI 退出 alternate 后立即恢复主屏语义。
3. !altKey && !ctrlKey && !metaKey 保证只处理无额外修饰的 Shift+Enter；普通 Enter、Alt/Ctrl/Meta 组合仍由 xterm 处理。
4. term.input 是唯一上送入口，与既有 onData 合流；不得直接调用 WS、PtyHandle.send 或写裸 \n。

跑红用例至绿：

~~~bash
cd web && npm test -- --run src/app/workbench/terminalInput.test.ts
~~~

判据：alt-screen 用例收到且只收到 \x1b[13;2u；主屏 Shift+Enter 仍是 \r；裸 Enter/Alt+Enter/Ctrl+Enter 不触发 term.input 这一层补发；文件原有 espanso、IME、Option、⌘ 键、dispose 用例全部仍绿。

### Step 4 — 日志与注释

在 web/src/app/workbench/terminalDebug.ts 现有 logTermFix 说明和 union 中加入明确动作名，不新建 logger：

~~~ts
//     '⌘← 行首' / '⌘→ 行尾' / '⌘K 清屏' = 本模块转发的 mac 终端键；
//     'Option Meta' = WKWebView 下 Option+字母按物理键发 ESC+字母；
//     'Shift+Enter 换行' = alternate buffer 中转发给 TUI 的 CSI u。
//   - text: 涉及的原文
export function logTermFix(
  label: string,
  kind:
    | '补发'
    | '让给 xterm'
    | '拦下注入键事件'
    | '⌘← 行首'
    | '⌘→ 行尾'
    | '⌘K 清屏'
    | 'Option Meta'
    | 'Shift+Enter 换行',
  text: string,
): void {
  if (!terminalDebugEnabled()) return
  console.debug('[term:fix]', { 终端: label, 动作: kind, 原文: JSON.stringify(text) })
}
~~~

在 terminalInput.ts 文件头更新旧边界注释：控制键仍原样留给 xterm，唯一例外是 alternate buffer 的无额外修饰 Shift+Enter；说明发送 CSI u 是为了避免主屏把裸 LF/CR 当 accept-line，也说明这不是完整 Kitty 协议。成功拦截路径必须先用 logTermFix(label, 'Shift+Enter 换行', TUI_SHIFT_ENTER) 留下输入序列，再调用 term.input；没有新增错误分支，不添加 console.log/print。

### Step 5 — 变异与接缝回归

用 apply_patch 暂时把 TUI_SHIFT_ENTER 的值从 \x1b[13;2u 改为 \r，运行：

~~~bash
cd web && npm test -- --run src/app/workbench/terminalInput.test.ts
~~~

判据：交替屏首个用例必须失败（期望 CSI u，实际为 CR）；恢复常量后再次运行该局部测试，回到全绿。恢复动作也用 apply_patch，不得把变异留在工作树。

然后执行任务范围收尾命令：

~~~bash
cd web && npm test -- --run src/app/workbench/terminalInput.test.ts
cd web && npm test -- --run src/app/workbench/TerminalTab.test.tsx
cd web && npm run typecheck
git diff --check
~~~

### Step 6 — 提交

确认 git diff --check 成功、只包含计划列出的三份源码/测试文件（加本计划和台账），再提交：

~~~bash
git add web/src/app/workbench/terminalInput.ts \
  web/src/app/workbench/terminalInput.test.ts \
  web/src/app/workbench/terminalDebug.ts \
  docs/superpowers/plans/b302-plan.md \
  docs/superpowers/specs/b302-ledger.md
git commit -m "fix(web): preserve Shift+Enter in alternate terminal TUI (B302)"
~~~

## 验收栏

### 机制门（执行者可判）

- terminalInput.test.ts 的真实 Terminal harness 在 alternate buffer 通过 term.input 发出精确 ESC[13;2u，onData 只收到一次该序列，没有 \r。
- 主屏 Shift+Enter 的 onData 仍为 \r，且没有调用本卡的 term.input 补发；裸 Enter、Alt+Enter、Ctrl+Enter 不走 B302 分支。
- term.buffer.active.type 每次动态读取；不存在第二个 attachCustomKeyEventHandler。
- 现有 21 条终端输入回归、47 条 TerminalTab 回归与类型检查通过；数字只是观测结果，验收依据是上述行为而不是测试数量。
- 变异 CSI u → CR 能使 alt-screen 机制断言变红，恢复后局部测试再绿。

### 真机门（协调者在合 main 前执行，不能由 jsdom 代替）

在工作台终端 tab 的真实部署宿主（浏览器/WKWebView 对应实际发布形态）分别启动 Grok、Codex、Claude Code、OpenCode TUI：

1. TUI 进入 alt-screen 后，在输入框输入一段文本，按 Shift+Enter；光标进入下一行，内容仍留在输入框/编辑区，未触发发送。
2. 同一输入框按裸 Enter；内容按各 TUI 原有语义发送。
3. 退出 TUI 回到 zsh 主屏，Enter 与 Shift+Enter 都按现有 shell 语义执行。
4. 在 Shift 组合下输入中文标点 ？、¥、%，字符不丢失、不重复、不触发 CSI u。
5. 若现场开 localStorage['handoff.debug.terminal']='1'，可看到 [term:fix] 的 Shift+Enter 换行与 JSON 转义后的 ESC[13;2u；关闭调试开关后不得产生常开流水。

### 缺陷族对抗审查

| 缺陷族 | 结论与锁点 |
|---|---|
| 生命周期/状态机中断 | 本卡不增加异步任务、PTY 会话状态或重连状态；沿用 disposed 失效闸，已有 dispose 回归继续覆盖卸载后不插手。 |
| 静默失败/误导报错 | 成功拦截先记结构化 [term:fix]，再经 term.input 合流；没有吞错误的新分支。onData 的精确序列断言能发现未发、双发和误发 CR。 |
| 跨平台假设 | 只按标准 key === 'Enter'、shiftKey 与 xterm 公布的 alternate buffer 判据处理；额外 Alt/Ctrl/Meta 明确放行。中文标点的 key 不是 Enter，现有 IME 用例继续锁住；WKWebView/真实 TUI 另过真机门。 |
| 假红测试 | 测试调用生产导出的 installTerminalInputFix，使用真 xterm + textarea + DOM event；不是新造纯函数。变异 TUI_SHIFT_ENTER = '\r' 必须让 alt-screen 断言失败。 |
| 门禁绕过 | 唯一键盘槽仍在 terminalInput.ts；禁止 TerminalTab 第二 handler、禁止改 xterm fork、禁止直接 WS/PTY 写入。rg -n "attachCustomKeyEventHandler" web/src/app/workbench 应只显示既有生产槽位和其文档/测试引用。 |
| 序列化边界 | 本卡无新增 JSON/DTO/CLI 字段，故没有新增手写序列化投影。行为字节链逐段核对为 term.input(data: string) → term.onData((d: string) => ...) → 既有 TextEncoder().encode(rest) → PtyHandle.send(bytes: Uint8Array)；机制测试锁前两段，TerminalTab 既有测试与真机门守后两段，源码不改。 |
| webview/平台表现差异 | xterm 5.5.0 的键盘与 buffer 行为已按本地依赖源码核实；真实浏览器/WKWebView 中四种 TUI、主屏、中文标点仍需协调者逐条执行，不能用 jsdom 结论替代。 |

### 上下文预算、类型与双向接缝覆盖

- 文件集有界：一个 d_web_workbench task 只改 terminalInput.ts、其真 harness terminalInput.test.ts、既有 logger terminalDebug.ts；TerminalTab.tsx 与 pty.ts 只读核对，不需要竖切。
- 类型标注：这是 Web 工作台输入边界；自动化锁的是 KeyboardEvent → xterm Terminal.input → onData(string)，外部 PTY/WKWebView/TUI 行为列入真机清单。
- 测试 → 缝：每支新增测试的入口都是 makeRig(true) → installTerminalInputFix(...)，并通过 ta.dispatchEvent(KeyboardEvent) 进入现有 attachCustomKeyEventHandler；没有内部纯函数测试顶替接缝。
- 缝 → 测：唯一接缝 installTerminalInputFix ← TerminalTab 由交替屏 CSI u/无 CR、主屏不补发、原有边界回归共同锁住；生产调用现存于 TerminalTab.tsx:365。没有第二条新增接缝。

## 计划自审

- Spec 故事 1 → 机制门首个 alt-screen 测试 + 四家 TUI 真机清单第 1 条。
- Spec 故事 2 → 机制门裸 Enter 保留 CR + 真机清单第 2 条。
- Spec 故事 3 → 主屏 Shift+Enter 测试 + 真机清单第 3 条。
- Spec 故事 4 → 现有 IME 回归、key !== 'Enter' 边界 + 真机清单第 4 条。
- Out of Scope → 文件集没有 Go/PTY/TERM/Composer/布局/xterm fork/完整 Kitty protocol 改动。
- 占位符扫描声明：本计划没有使用未决标记、跨任务代称或省略实现的条件退路；测试直接复用既有 terminalInput.test.ts 的 Rig/makeRig/key harness，新增断言逐条列在 Step 2 完整代码块中。
