# W4 PTY 终端：真机走查发现的三个缺陷修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> 本计划由审核者在真机走查后写出，请在**内部用 subagent 逐个 task 执行**：一个 task 一个 subagent，做完自审再进下一个。

**Goal:** 修掉审核者在 `feat/w4-pty-terminal` 真机走查中发现的三个缺陷——WebGL 上下文丢失后终端永久白屏、StrictMode 下 `ranRef + cancelled` 组合让加载期请求自毁、建会话后被卸载会漏孤儿 shell。

**Architecture:** 三处都在前端，互不依赖，可按任意顺序做。缺陷 B 是同一套写法出现在两个 hook 里（`usePtyRestore` / `usePtySupport`），一并修；缺陷 A、C 都在 `TerminalTab.tsx`，但改的是两段互不相干的代码。**不动后端**：`internal/ptyhost` 与 REST/WS 契约在本次修复中零改动。

**Tech Stack:** React 19 + TypeScript + vitest + @testing-library/react；xterm.js 5.5 + @xterm/addon-webgl 0.18。

## 背景：这三条是怎么被发现的

审核者在本地起了浏览器跑完整走查（12 条验收里 10 条通过），过程中定性了这三个缺陷。完整证据见审核者的走查记录 `docs/superpowers/notes/2026-08-13-w4-pty-terminal-walkthrough.md` §3（该文件在另一个分支上，本分支看不到，所以下面每条都把关键证据抄了过来，不需要去找那份文件）。

## Global Constraints

- **不改后端。** `internal/ptyhost`、`internal/agentd/pty_api.go`、`pty_ws.go`、`forward_ws.go` 一行都不动。特别是**不要**给 ptyhost 加空闲回收/超时清理——会话长活是 spec §6.2 的明确设计（切 tab 不能杀掉跑了一晚上的 build），孤儿问题要在「源头别漏」上解决，不是靠后台清扫。
- **不改 spec。** `docs/superpowers/specs/2026-08-12-w4-pty-terminal-design.md` 保持原样，本次修的是实现没兑现 spec，不是 spec 错了。
- 前端目录一律在 `web/` 下操作，测试命令 `cd web && npm test`（vitest run）。
- 注释与日志按仓库现有风格：中文，解释「为什么」；前端降级路径用 `console.warn` 并写清发生了什么、后果是什么。
- 每个 task 独立提交，提交信息用中文，格式与分支上已有提交一致（`fix(web): …`）。

---

### Task 1: 修 `ranRef + cancelled` 自毁（缺陷 B）

**缺陷原文（审核者走查记录）**

> `usePtyRestore.ts` 与 `usePtySupport.ts` 用了同一套写法：`ranRef` 保证只跑一次 + cleanup 里 `cancelled = true`。React StrictMode 下 effect 被双调用：第一次跑 → 发出唯一那次请求；第一次 cleanup → 把 `cancelled` 置真；第二次 effect → 被 `ranRef` 挡住直接返回，于是没人把 `cancelled` 撤回。请求回来时回调看到 `cancelled === true`，直接 bail。
>
> 后果：**开发模式下 `usePtyRestore` 100% 恢复不出任何终端 tab**（走查第 5 条一度被记成失败，临时去掉 `<StrictMode>` 后立刻正常，据此定的性）。`usePtySupport` 同样写法，后果温和些——能力表停在 `null`，三态门降级为「一律放行」，安全但等于没生效。
>
> 生产构建不双调用 effect，所以线上不受影响。但**验收就是在开发模式做的**，这个 bug 会持续把真功能诬告成坏功能。

**根因**：`cancelled` 是 per-effect-run 的局部变量，而 `ranRef` 是跨 run 的。两者生命周期不一致——「上一轮的 cleanup」取消了「这一轮仍然有效」的请求。修法是让取消标志也跨 run（用 ref），并在每次 effect 进入时**撤销**取消。

**Files:**
- Modify: `web/src/app/workbench/usePtyRestore.ts:57-76`
- Modify: `web/src/app/data/usePtySupport.ts:30-50`
- Test: `web/src/app/workbench/usePtyRestore.test.ts`
- Test: `web/src/app/data/usePtySupport.test.ts`

**Interfaces:**
- Consumes: 无（两个 hook 的对外签名不变）
- Produces: 无签名变化。`usePtyRestore(restore) => { error }`、`usePtySupport() => { supported, error }` 保持原样，调用方不需要改。

- [ ] **Step 1: 写失败测试——StrictMode 下也必须恢复**

在 `web/src/app/workbench/usePtyRestore.test.ts` 的 `describe` 里追加（文件顶部已有的 mock 与 `session()` 工厂直接复用，不要重写）：

```tsx
  it('StrictMode 双调用 effect 时仍然恢复——上一轮的 cleanup 不该取消这一轮的请求', async () => {
    fetchPtySessions.mockResolvedValue({ sessions: [session()] })
    const restore = vi.fn()
    renderHook(() => usePtyRestore(restore), { wrapper: StrictMode })
    await waitFor(() => expect(restore).toHaveBeenCalledTimes(1))
    // 只跑一次的保证不能因为这个修复丢掉：双调用不能变成两次跨机探活
    expect(fetchPtySessions).toHaveBeenCalledTimes(1)
  })

  it('真的卸载之后不再回灌——组件都没了还往里写就是脏写', async () => {
    let resolve!: (v: unknown) => void
    fetchPtySessions.mockReturnValue(new Promise((r) => { resolve = r }))
    const restore = vi.fn()
    const { unmount } = renderHook(() => usePtyRestore(restore))
    unmount()
    resolve({ sessions: [session()] })
    await Promise.resolve()
    expect(restore).not.toHaveBeenCalled()
  })
```

同一文件顶部的 import 补上 `StrictMode`：

```tsx
import { StrictMode } from 'react'
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/workbench/usePtyRestore.test.ts`
Expected: FAIL —— 「StrictMode 双调用」那条超时或断言 `restore` 未被调用（`toHaveBeenCalledTimes(1)` 收到 0 次）。「真的卸载之后不再回灌」那条此时应该已经通过（现有实现在真卸载路径上是对的），不用管它。

- [ ] **Step 3: 修 `usePtyRestore`**

把 `web/src/app/workbench/usePtyRestore.ts` 里的 effect（第 57-76 行）整段替换为：

```ts
  // cancelledRef 必须跨 effect run 存活，才能和同样跨 run 的 ranRef 对齐。
  // 用局部变量会出这个 bug：StrictMode 下第一轮 cleanup 把它置真，第二轮
  // effect 被 ranRef 挡住、没人撤回，唯一那次请求回来时自己把自己丢掉——
  // 开发模式下终端 tab 一个都恢复不出来。
  const cancelledRef = useRef(false)

  useEffect(() => {
    // 每次挂载（含 StrictMode 的第二次）先撤销上一次 cleanup 的取消
    cancelledRef.current = false
    if (!ranRef.current) {
      ranRef.current = true
      fetchPtySessions('all')
        .then((resp) => {
          if (cancelledRef.current) return
          for (const s of resp.sessions) {
            // exit_code 出现 = 已退出。恢复一个死会话只会让人以为它还能用
            if (s.exit_code !== undefined && s.exit_code !== null) continue
            restoreRef.current(baseOfSession(s), s.id)
          }
        })
        .catch((err: unknown) => {
          if (cancelledRef.current) return
          // 拉不到列表 = 用户会看到「终端都不见了」，必须说清为什么
          console.warn('恢复终端会话失败，本次不恢复任何 tab', err)
          setError(errorMessage(err))
        })
    }
    return () => {
      cancelledRef.current = true
    }
  }, [])
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/workbench/usePtyRestore.test.ts`
Expected: PASS，全部用例（含原有的「只跑一次」「已退出的会话不恢复」「拉不到列表时给出原文」）。

- [ ] **Step 5: 给 `usePtySupport` 写同样的失败测试**

在 `web/src/app/data/usePtySupport.test.ts` 里追加（沿用该文件已有的 mock 名称与工厂；若文件里 mock 的函数名不是 `fetchMachines`，以文件现状为准）：

```tsx
  it('StrictMode 双调用 effect 时能力表仍然落表——否则三态门永远停在 null', async () => {
    fetchMachines.mockResolvedValue({ machines: [{ name: 'devbox', pty_supported: false }] })
    const { result } = renderHook(() => usePtySupport(), { wrapper: StrictMode })
    await waitFor(() => expect(result.current.supported('devbox')).toBe(false))
    expect(fetchMachines).toHaveBeenCalledTimes(1)
  })
```

同样在该文件顶部 import `StrictMode`。

- [ ] **Step 6: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/data/usePtySupport.test.ts`
Expected: FAIL —— `supported('devbox')` 一直是 `null`，`waitFor` 超时。

- [ ] **Step 7: 修 `usePtySupport`**

把 `web/src/app/data/usePtySupport.ts` 的 effect（第 30-50 行）整段替换为：

```ts
  // 与 usePtyRestore 同因同修：cancelledRef 必须跨 effect run，否则 StrictMode
  // 下第一轮 cleanup 会取消掉唯一那次请求，能力表永远停在 null。
  // 停在 null 不会出错（三态门对 null 的反应是放行），但等于这个门没生效。
  const cancelledRef = useRef(false)

  useEffect(() => {
    cancelledRef.current = false
    if (!ranRef.current) {
      ranRef.current = true
      fetchMachines()
        .then((resp) => {
          if (cancelledRef.current) return
          const next: Record<string, boolean> = {}
          for (const m of resp.machines) {
            // 只收明确上报的：缺席/null 不进表，查询时自然落到 null
            if (typeof m.pty_supported === 'boolean') next[m.name] = m.pty_supported
          }
          setMap(next)
        })
        .catch((err: unknown) => {
          if (cancelledRef.current) return
          console.warn('拉取机器能力位失败，PTY 三态门降级为一律放行', err)
          setError(errorMessage(err))
        })
    }
    return () => {
      cancelledRef.current = true
    }
  }, [])
```

- [ ] **Step 8: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/data/usePtySupport.test.ts`
Expected: PASS。

- [ ] **Step 9: 补注释（文件头 + 为什么）**

两个文件顶部的文件头注释里，把 ranRef 那段说明更新为解释这对 ref 的**配对关系**：`ranRef` 管「只跑一次」，`cancelledRef` 管「结果还要不要」，两者都必须跨 effect run，缺一不可。不要只写「修了个 bug」——写清楚为什么局部变量在这里是错的。

- [ ] **Step 10: 提交**

```bash
git add web/src/app/workbench/usePtyRestore.ts web/src/app/workbench/usePtyRestore.test.ts web/src/app/data/usePtySupport.ts web/src/app/data/usePtySupport.test.ts
git commit -m "fix(web): 取消标志跨 effect run，StrictMode 下不再自毁加载期请求"
```

---

### Task 2: WebGL 上下文丢失后回退到 DOM 渲染（缺陷 A）

**缺陷原文（审核者走查记录）**

> `TerminalTab.tsx` 只 `try/catch` 了 `new WebglAddon()` 的**构造期**。WebGL 上下文是运行期可以丢的（GPU 复位/驱动重启、浏览器对 WebGL 上下文数量的驱逐——终端 tab 开多了就够得着）。上下文一丢，addon 还挂在 term 上，渲染器已经死了，屏幕永久白屏，控制台刷 `Cannot read properties of undefined (reading 'dimensions')`。
>
> 定性依据：把 `WebglAddon` 那两行注释掉之后，同一套操作全程正常，终端画面、输入、resize 都对。
>
> 而 spec §6.3 明写「不能白屏」——那条线在运行期这一侧是断的。代码里那段注释也**说错了覆盖面**（它声称 catch 住就等于回退发生了，实际上只覆盖构造期）。

**修法**：按 xterm 官方的用法，构造后先注册 `onContextLoss`，在回调里 `dispose()` 该 addon —— dispose 会把渲染路径交回 xterm 内建的 DOM 渲染器，画面继续。

**Files:**
- Modify: `web/src/app/workbench/TerminalTab.tsx:74-81`
- Test: `web/src/app/workbench/TerminalTab.test.tsx`

**Interfaces:**
- Consumes: `WebglAddon` from `@xterm/addon-webgl`（已在依赖里，版本 0.18，带 `onContextLoss`）
- Produces: 无对外签名变化。

- [ ] **Step 1: 写失败测试**

先把 `web/src/app/workbench/TerminalTab.test.tsx` 顶部的 webgl mock 换成能捕获回调、能断言 dispose 的版本（替换现有那一行 `vi.mock('@xterm/addon-webgl', …)`）：

```tsx
// webgl addon 的替身要能捕获 onContextLoss 回调并记录 dispose：
// 「上下文丢了之后有没有回退」正是本组件的职责，必须能测
const webglAddon = { onContextLoss: vi.fn(), dispose: vi.fn() }
vi.mock('@xterm/addon-webgl', () => ({ WebglAddon: vi.fn(function () { return webglAddon }) }))
```

然后在 `describe('TerminalTab', …)` 里追加两条：

```tsx
  it('WebGL 上下文丢失时 dispose 掉渲染器，交回 DOM 渲染——不能白屏', async () => {
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(webglAddon.onContextLoss).toHaveBeenCalled())
    // 触发上下文丢失
    webglAddon.onContextLoss.mock.calls[0][0]({})
    expect(webglAddon.dispose).toHaveBeenCalledTimes(1)
  })

  it('构造期就不可用时照样活着——不抛出去，终端仍然能连', async () => {
    const { WebglAddon } = await import('@xterm/addon-webgl')
    vi.mocked(WebglAddon).mockImplementationOnce(() => { throw new Error('no webgl') })
    render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
  })
```

注意：`webglAddon` 的两个 `vi.fn()` 会被文件里已有的 `beforeEach(() => vi.clearAllMocks())` 清掉，不需要额外重置。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/workbench/TerminalTab.test.tsx`
Expected: FAIL —— 「WebGL 上下文丢失时 dispose」那条超时（`onContextLoss` 从未被调用，因为现在的代码压根没注册）。

- [ ] **Step 3: 实现回退**

把 `web/src/app/workbench/TerminalTab.tsx` 第 74-81 行（`try { term.loadAddon(new WebglAddon()) } catch …` 整段）替换为：

```ts
    // WebGL 渲染器有两条失效路径，两条都不能白屏（spec §6.3）：
    //   1. 构造期不可用（远程桌面、禁用硬件加速、老显卡）——构造直接抛，
    //      catch 住即可，xterm 用内建 DOM 渲染器继续。
    //   2. 运行期上下文丢失（GPU 复位、驱动重启、浏览器驱逐 WebGL 上下文——
    //      终端 tab 开多了就够得着）。这条 try/catch **管不着**：addon 已经
    //      挂上去了，不 dispose 它就留下一个死渲染器，画面永久停住，控制台
    //      刷 `dimensions` 的 TypeError。所以必须注册 onContextLoss 主动摘除。
    try {
      const webgl = new WebglAddon()
      webgl.onContextLoss(() => {
        console.warn('WebGL 上下文丢失，已摘除 WebGL 渲染器回退到 DOM 渲染（会慢一些，但画面继续）')
        webgl.dispose()
      })
      term.loadAddon(webgl)
    } catch (err) {
      console.warn('WebGL 渲染器不可用，已回退到 DOM 渲染', err)
    }
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/workbench/TerminalTab.test.tsx`
Expected: PASS，含文件里原有的全部用例。

- [ ] **Step 5: 提交**

```bash
git add web/src/app/workbench/TerminalTab.tsx web/src/app/workbench/TerminalTab.test.tsx
git commit -m "fix(web): WebGL 上下文运行期丢失时摘除渲染器回退 DOM，不再白屏"
```

---

### Task 3: 建会话后被卸载不再漏孤儿 shell（缺陷 C）

**缺陷原文（审核者走查记录）**

> `TerminalTab` 的 `start()` 里：
>
> ```ts
> const created = await createPtySession(…)
> id = created.id
> if (disposed) return      // ← 服务端已经建好了，这里直接扔掉
> onSession(id)
> ```
>
> 服务端会话**已经建成**（shell 进程已 fork），但 id 从未回报给上层，也没发 DELETE。没人知道它的存在，界面上没有任何入口能连上它或杀掉它，而 `ptyhost` 的 `reap` 只等 shell 退出、没有空闲回收——于是它就一直挂在那儿。
>
> 实测：StrictMode 下每点一次「新终端」必漏一个（抓到两组 pid）。真实卸载路径（用户建完立刻切走/关 tab）同样触发。

**修法**：这一路的会话是**这次挂载建的、且从没告诉过任何人**——它和「切 tab 不能杀掉跑了一晚上的 build」不是一回事（那种会话的 id 早已回报、tab 里记着）。所以这一路必须 DELETE 掉。判据要精确：**只有本 effect 自己新建、且卸载发生在回报之前**才删。

**Files:**
- Modify: `web/src/app/workbench/TerminalTab.tsx:84-94`（`start()` 的建会话分支）
- Modify: `web/src/app/workbench/TerminalTab.tsx:26`（import 补 `deletePtySession`）
- Test: `web/src/app/workbench/TerminalTab.test.tsx`

**Interfaces:**
- Consumes: `deletePtySession(id: string, machine?: string): Promise<{ ok: boolean }>` from `../../api/client`（已存在，`web/src/api/client.ts:259`）
- Produces: 无对外签名变化。

- [ ] **Step 1: 写失败测试**

先把 `TerminalTab.test.tsx` 顶部的 client mock 扩成两个函数（替换现有那一行 `vi.mock('../../api/client', …)`）：

```tsx
const createPtySession = vi.fn()
const deletePtySession = vi.fn()
vi.mock('../../api/client', () => ({
  createPtySession: (...a: unknown[]) => createPtySession(...a),
  deletePtySession: (...a: unknown[]) => deletePtySession(...a),
}))
```

并在 `beforeEach` 里补一行默认返回：

```tsx
  deletePtySession.mockResolvedValue({ ok: true })
```

然后追加两条用例：

```tsx
  it('建会话的过程中被卸载：把这个没人知道的会话删掉，不留孤儿 shell', async () => {
    let resolveCreate!: (v: unknown) => void
    createPtySession.mockReturnValue(new Promise((r) => { resolveCreate = r }))
    const { unmount } = render(<TerminalTab base={WS} seq={1} onSession={vi.fn()} />)
    await waitFor(() => expect(createPtySession).toHaveBeenCalled())
    unmount()
    resolveCreate({ id: 'orphan-1', base_path: WS.path })
    await waitFor(() => expect(deletePtySession).toHaveBeenCalledWith('orphan-1', ''))
  })

  it('已回报过的会话在卸载时不删——切 tab 不能杀掉跑了一晚上的 build', async () => {
    const { unmount } = render(<TerminalTab base={WS} seq={1} sessionId="s" onSession={vi.fn()} />)
    await waitFor(() => expect(connectPty).toHaveBeenCalled())
    unmount()
    expect(deletePtySession).not.toHaveBeenCalled()
  })
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd web && npx vitest run src/app/workbench/TerminalTab.test.tsx`
Expected: FAIL —— 第一条 `waitFor` 超时（`deletePtySession` 从未被调用）。第二条此时就通过，它是防回归的护栏。

- [ ] **Step 3: 实现回收**

`web/src/app/workbench/TerminalTab.tsx` 顶部 import 改为：

```ts
import { createPtySession, deletePtySession } from '../../api/client'
```

`start()` 的建会话分支（第 85-94 行）替换为：

```ts
      let id = sessionId
      if (!id) {
        const created = await createPtySession(
          { ...ptyBase(base), cols: term.cols, rows: term.rows },
          base.machine,
        )
        if (disposed) {
          // 会话已在服务端建成（shell 已 fork），但 id 从没回报给上层——
          // 界面上没有任何入口能连上它或杀掉它，而 ptyhost 只在 shell 退出时
          // 回收、没有空闲清扫，不删就是一个永远挂着的孤儿。
          //
          // 这跟「卸载不删会话」的纪律不冲突：那条护的是**已回报**的会话
          // （tab 里记着 id，切回来还能接上）。这里的会话没人知道它存在。
          void deletePtySession(created.id, base.machine).catch((err: unknown) => {
            console.warn('回收孤儿终端会话失败，服务端可能残留一个 shell', created.id, err)
          })
          return
        }
        id = created.id
        onSession(id)
      }
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd web && npx vitest run src/app/workbench/TerminalTab.test.tsx`
Expected: PASS，含原有的「卸载时只断连接，不删会话」。

- [ ] **Step 5: 提交**

```bash
git add web/src/app/workbench/TerminalTab.tsx web/src/app/workbench/TerminalTab.test.tsx
git commit -m "fix(web): 建会话中途被卸载时回收孤儿会话，不再漏 shell 进程"
```

---

### Task 4: 全量验证与走查记录回填

**Files:**
- Modify: `docs/superpowers/walkthroughs/2026-08-12-w4-pty-terminal.md`

- [ ] **Step 1: 前端全量测试**

Run: `cd web && npm test`
Expected: PASS，0 failed。若有别的用例被前三个 task 带崩，**先查是不是修复暴露了真问题**，不要直接改测试迁就实现。

- [ ] **Step 2: 类型与 lint**

Run: `cd web && npm run typecheck && npm run lint`
Expected: 两条都 0 错误。

- [ ] **Step 3: 后端回归（应当零影响，跑一遍确认没手滑）**

Run: `go build ./... && go vet ./... && go test ./internal/ptyhost/... ./internal/agentd/...`
Expected: 全部通过。本次不该有任何后端改动，`git diff --stat` 里不应出现 `.go` 文件。

- [ ] **Step 4: 回填走查记录**

在 `docs/superpowers/walkthroughs/2026-08-12-w4-pty-terminal.md` 末尾追加一节：

```markdown
## 缺陷修复回填（2026-08-13）

审核者在本地带浏览器的环境跑完了本分支的真机走查（12 条验收：10 通过 / 1 部分 / 1 未验），
并定性了三个缺陷。三条均已在本分支修复：

| 缺陷 | 现象 | 修法 | 提交 |
|---|---|---|---|
| A | WebGL 上下文运行期丢失后终端永久白屏；spec §6.3「不能白屏」在运行期这侧是断的 | 注册 `onContextLoss` 并 dispose addon，交回 DOM 渲染 | <填入实际短 sha> |
| B | `ranRef` + 局部 `cancelled` 在 StrictMode 下互相拆台，开发模式恢复不出任何终端 tab | 取消标志改为跨 effect run 的 ref，进入 effect 时撤销 | <填入实际短 sha> |
| C | 建会话中途被卸载会漏孤儿 shell（服务端已建、id 从未回报、无人能连能杀） | 该路径显式 DELETE 掉这个没人知道的会话 | <填入实际短 sha> |

仍未验证、不在本次修复范围内的两条：
- 验收第 3 条只做到「两个本机 agentd 之间验通反代链路」，真 devbox + 界面远程终端入口未验（devbox 上跑的是 main，没有 PTY 接口；界面机器节点来自项目位置，假远程不上树）。
- 验收第 9 条（`pty_supported=false` 的对端）无 Windows 对端，未验。
```

把表格里三处 `<填入实际短 sha>` 换成 Task 1-3 的实际提交短 sha（`git log --oneline -3`）。

- [ ] **Step 5: 提交**

```bash
git add docs/superpowers/walkthroughs/2026-08-12-w4-pty-terminal.md
git commit -m "docs(walkthrough): 回填三个缺陷的修复结论与仍未验证的两条"
```

---

## 完工自检

声明完成前逐项确认：

- [ ] 三个缺陷各有一条**先失败后通过**的回归测试，不是「改完补个测试」
- [ ] `cd web && npm test` 全绿；`npm run typecheck`、`npm run lint` 均 0 错误
- [ ] `git diff --stat <本次起点>..HEAD` 里没有 `.go` 文件，没有 spec 文件
- [ ] 三处降级/失败路径都有 `console.warn` 且写清了后果（白屏回退、恢复失败、孤儿回收失败）
- [ ] 改动过的注释说的是「为什么」，并且**说对了覆盖面**——缺陷 A 的老注释正是因为说错覆盖面才让人以为已经兜住了
- [ ] 走查记录里的三个 sha 是真实的
