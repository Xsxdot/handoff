// verify-tab-resize-storm.mjs —— 隔离 agentd + 无头 Chrome 验「整页往返 resize 风暴」已断。
//
// 背景（2026-09-12 排查，B270/B280 的病以新形状复发）：工作台组容器是 Shell 中央
// flex-ROW 的子项，xterm 画布有固有宽度。组容器缺 min-w-0 时，flex 的自动最小尺寸
// 把整组撑到画布宽：切进设置页时容器变宽（面包屑撤行 + 文件抽屉卸载，544→824），
// 切回的瞬间容器已缩回 544、组却还停在画布旧固有宽上——ResizeObserver → fit →
// PTY resize → SIGWINCH 每帧一拍，21 连击。TUI 在这条尺寸风暴里被反复重排，
// WKWebView 的 WebGL 画布更是会被直接打坏（用户看到的「切走再回来 TUI 花屏、
// 无法滚动」）。修复：组容器补 min-w-0，链上最小宽度在这里断掉。
//
// 断言：从设置页返回工作台后的 1.5s 窗口内，
//   1. pty-host 的实测宽度从不越过其父容器（min-content 撑越必须消失）；
//   2. [term:resize] 触发=observer 的次数 ≤ 3（修复前是 ~21）；
//   3. 终端会话在往返后仍然活着。
//
// 用法（与 verify-b322-restore.mjs 同范式）：
//   go build -o /tmp/handoff-tabfix ./cmd/handoff   # 仓库根
//   node web/scripts/verify-tab-resize-storm.mjs
// 环境变量：HANDOFF_TABFIX_BIN（agentd 二进制，默认 /tmp/handoff-tabfix）、
//   TABFIX_AGENTD_PORT / TABFIX_VITE_PORT / TABFIX_CDP_PORT / TABFIX_WORK
import { spawn } from 'node:child_process'
import { mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { createConnection } from 'node:net'
import { setTimeout as sleep } from 'node:timers/promises'
import { WebSocket } from 'ws'

const ROOT = new URL('../..', import.meta.url).pathname.replace(/\/$/, '')
const WEB = `${ROOT}/web`
const BIN = process.env.HANDOFF_TABFIX_BIN ?? '/tmp/handoff-tabfix'
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
const TOKEN = 'tabfixtabfixtabfixtabfixtabfixtabf'
const AGENTD_PORT = Number(process.env.TABFIX_AGENTD_PORT ?? 18787)
const VITE_PORT = Number(process.env.TABFIX_VITE_PORT ?? 5175)
const CDP_PORT = Number(process.env.TABFIX_CDP_PORT ?? 9224)
const WORK = process.env.TABFIX_WORK ?? `/tmp/tabfix-isolate-${process.pid}`
const WS_DIR = `${WORK}/ws`
const children = []

function fail(msg) {
  console.error(`✗ ${msg}`)
  process.exitCode = 1
}

function ok(msg) {
  console.log(`✓ ${msg}`)
}

function waitPort(port, timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs
  return new Promise((resolve, reject) => {
    const tryOnce = () => {
      const sock = createConnection({ port, host: '127.0.0.1' }, () => {
        sock.end()
        resolve()
      })
      sock.on('error', () => {
        sock.destroy()
        if (Date.now() > deadline) reject(new Error(`port ${port} not up`))
        else setTimeout(tryOnce, 150)
      })
    }
    tryOnce()
  })
}

function run(cmd, args, opts = {}) {
  const child = spawn(cmd, args, { stdio: ['ignore', 'pipe', 'pipe'], ...opts })
  children.push(child)
  let buf = ''
  child.stderr.on('data', (d) => { buf += d.toString() })
  child.stdout.on('data', (d) => { buf += d.toString() })
  child.on('exit', (code) => { child._exit = code; child._log = buf })
  return child
}

async function api(path, { method = 'GET', body, bearer } = {}) {
  const headers = { accept: 'application/json' }
  if (body !== undefined) headers['content-type'] = 'application/json'
  if (bearer) headers.authorization = `Bearer ${bearer}`
  const resp = await fetch(`http://127.0.0.1:${AGENTD_PORT}${path}`, {
    method, headers, body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await resp.text()
  let json = null
  try { json = JSON.parse(text) } catch { /* not json */ }
  return { status: resp.status, json, text }
}

function seedPayload(sessionId) {
  const base = {
    key: WS_DIR, kind: 'workspace', path: WS_DIR, label: 'tabfix', projectName: 'tabfix', machine: '',
  }
  return JSON.stringify({
    v: 2,
    wb: {
      activeGroupId: 'g1',
      groups: [{
        id: 'g1', name: '组 1', autoName: true,
        columns: [{ panes: [{
          id: 't1', base,
          // 注意：persist 编码对 terminal 只认 kind/seq/sessionId/rel/launcher，
          // spawn 是运行时字段不落盘——多带一个键整个 tab 会被解码拒绝。
          content: { kind: 'terminal', seq: 1, sessionId },
        }] }],
        sizes: [1], focus: [0, 0],
      }],
    },
  })
}

class Cdp {
  constructor(ws) {
    this.ws = ws
    this.seq = 0
    this.pending = new Map()
    this.console = []
    ws.on('message', (raw) => {
      const msg = JSON.parse(String(raw))
      if (msg.id && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id)
        this.pending.delete(msg.id)
        if (msg.error) reject(new Error(JSON.stringify(msg.error)))
        else resolve(msg.result)
        return
      }
      if (msg.method === 'Runtime.consoleAPICalled') {
        const args = (msg.params.args ?? []).map((a) => a.value ?? a.description ?? '')
        this.console.push({ type: msg.params.type, args, t: Date.now() })
      }
    })
  }
  send(method, params = {}) {
    const id = ++this.seq
    this.ws.send(JSON.stringify({ id, method, params }))
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject })
      setTimeout(() => {
        if (this.pending.has(id)) {
          this.pending.delete(id)
          reject(new Error(`cdp timeout ${method}`))
        }
      }, 20000)
    })
  }
  async eval(expression) {
    const r = await this.send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.text ?? 'eval exception')
    return r.result.value
  }
}

async function openCdp() {
  let list = []
  for (let i = 0; i < 30; i++) {
    try {
      list = await fetch(`http://127.0.0.1:${CDP_PORT}/json/list`).then((r) => r.json())
      if (Array.isArray(list) && list.length > 0) break
    } catch { /* chrome still starting */ }
    await sleep(200)
  }
  const page = list.find((t) => t.type === 'page') ?? list[0]
  if (!page?.webSocketDebuggerUrl) throw new Error(`no cdp page: ${JSON.stringify(list)}`)
  const ws = new WebSocket(page.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => { ws.once('open', resolve); ws.once('error', reject) })
  const cdp = new Cdp(ws)
  await cdp.send('Runtime.enable')
  await cdp.send('Page.enable')
  return { cdp, ws }
}

async function main() {
  rmSync(WORK, { recursive: true, force: true })
  mkdirSync(WS_DIR, { recursive: true })
  writeFileSync(`${WORK}/config.yaml`, [
    `listen: 127.0.0.1:${AGENTD_PORT}`,
    `token: ${TOKEN}`,
    `datadir: ${WORK}/data`,
    'targets: {}',
    'ledger:',
    '  dsn: ""',
    'terminal:',
    '  auto: false',
    '',
  ].join('\n'))

  const agentd = run(BIN, ['agentd', '--config', `${WORK}/config.yaml`], { env: { ...process.env, HANDOFF_LOG_LEVEL: 'info' } })
  await waitPort(AGENTD_PORT)
  ok(`隔离 agentd 在 :${AGENTD_PORT}`)

  // PTY 建会话要求 base_path 是已登记的本机工作树：先 git init（带名义 origin）
  // 再登记成项目。commit 需要身份，用 -c 现给。
  const { execSync } = await import('node:child_process')
  execSync(`git init -q && git remote add origin https://example.com/tabfix/ws.git`, { cwd: WS_DIR })
  writeFileSync(`${WS_DIR}/README.md`, 'tabfix verify\n')
  execSync(`git add README.md && git -c user.email=t@t -c user.name=t commit -qm init`, { cwd: WS_DIR })
  const reg = await api('/api/projects', { method: 'POST', bearer: TOKEN, body: { name: 'tabfix', path: WS_DIR } })
  if (reg.status !== 200) throw new Error(`登记项目失败 ${reg.status} ${reg.text}`)
  ok(`工作树 ${WS_DIR} 已登记`)

  const created = await api('/api/pty/sessions', {
    method: 'POST', bearer: TOKEN,
    body: { base_kind: 'workspace', base_path: WS_DIR, cols: 76, rows: 44 },
  })
  if (created.status !== 200 || !created.json?.id) throw new Error(`建 PTY 会话失败 ${created.status} ${created.text}`)
  const sessionId = created.json.id
  ok(`活会话 ${sessionId.slice(0, 8)}…`)

  const put = await api('/api/workbench/state/base', {
    method: 'PUT', bearer: TOKEN,
    body: { base_key: '__global_workbench__', payload: seedPayload(sessionId) },
  })
  if (put.status !== 200) throw new Error(`seed workbench ${put.status} ${put.text}`)
  ok('写入 1 组 1 终端 tab 的快照')

  const vite = run('npx', ['vite', '--port', String(VITE_PORT), '--strictPort', '--host', '127.0.0.1'], {
    cwd: WEB,
    env: { ...process.env, AGENTD_URL: `http://127.0.0.1:${AGENTD_PORT}` },
  })
  await waitPort(VITE_PORT)
  ok(`vite :${VITE_PORT} 反代隔离 agentd`)

  const ticket = await api('/api/auth/tickets', { method: 'POST', bearer: TOKEN, body: { device_name: 'tabfix-isolate' } })
  if (ticket.status !== 200 || !ticket.json?.url) throw new Error(`ticket ${ticket.status} ${ticket.text}`)
  const ticketUrl = new URL(ticket.json.url)
  const consoleUrl = `http://127.0.0.1:${VITE_PORT}/console?ticket=${ticketUrl.searchParams.get('ticket')}`

  run(CHROME, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--disable-extensions',
    `--remote-debugging-port=${CDP_PORT}`, `--user-data-dir=${WORK}/chrome`,
    'about:blank',
  ])
  await waitPort(CDP_PORT)
  const { cdp, ws } = await openCdp()
  ok('无头 Chrome CDP 已连接')

  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 720, deviceScaleFactor: 1, mobile: false })
  await cdp.send('Page.navigate', { url: consoleUrl })
  await sleep(3500)
  if (!(await cdp.eval(`Boolean(document.querySelector('[data-testid="pty-host"]'))`))) {
    throw new Error('终端窗格没挂出来，页面状态不对')
  }
  ok('控制台已加载且终端窗格在场')

  // 探针：RO 记录 host 与父容器的每帧宽度；开终端取证日志并补丁 console，
  // 让 [term:resize] 落进页内数组（CDP 对 debug 级 console 投递不可靠）。
  await cdp.eval(`(() => {
    localStorage.setItem('handoff.debug.terminal', '1')
    window.__storm = { t0: performance.now(), events: [], resizes: [] }
    const origDebug = console.debug.bind(console)
    console.debug = (...args) => {
      const line = args.map((a) => typeof a === 'string' ? a : JSON.stringify(a)).join(' ')
      if (line.includes('[term:resize]')) window.__storm.resizes.push({ t: Math.round(performance.now() - window.__storm.t0), line })
      origDebug(...args)
    }
    const host = document.querySelector('[data-testid="pty-host"]')
    const parent = host.parentElement
    const record = (who, el) => {
      const r = el.getBoundingClientRect()
      window.__storm.events.push({ t: Math.round(performance.now() - window.__storm.t0), who, w: Math.round(r.width) })
    }
    new ResizeObserver(() => record('host', host)).observe(host)
    new ResizeObserver(() => record('parent', parent)).observe(parent)
  })()`)
  ok('RO 探针与取证日志已挂')

  const clickButton = (text) => cdp.eval(`(() => {
    const want = ${JSON.stringify(text)}
    const hits = [...document.querySelectorAll('button')].filter((x) =>
      x.textContent.trim() === want || x.getAttribute('aria-label') === want ||
      (x.textContent.includes(want) && x.textContent.trim().length < want.length + 6))
    if (hits.length !== 1) return { ok: false, hits: hits.length }
    hits[0].click()
    return { ok: true }
  })()`)
  const clickButtonOrThrow = async (text) => {
    const r = await clickButton(text)
    if (!r?.ok) {
      const dump = await cdp.eval(`(() => {
        const buttons = [...document.querySelectorAll('button')].slice(0, 40).map((b) => b.getAttribute('aria-label') ?? b.textContent.trim().slice(0, 20))
        const sidebar = document.querySelector('aside[aria-label="项目导航"]')?.innerText.replace(/\\n+/g, ' | ').slice(0, 300) ?? '(no sidebar)'
        return { buttons, sidebar }
      })()`)
      throw new Error(`找不到唯一按钮「${text}」：${JSON.stringify(r)}；现场：${JSON.stringify(dump)}`)
    }
  }

  // 拉出右栏文件抽屉（左栏点工作树目录）：抽屉 280px 是回程宽度回缩的前提，
  // 没有它「设置页往返」宽度不变，量不到 resize 行为。这也是真实用户路径。
  // 目录行默认全展开；若没有 workspace 行先点机器行展开。名字走 testid——
  // 目录名是分支名，文本匹配会被环境差异绊倒。
  const wsCount = await cdp.eval(`document.querySelectorAll('[data-testid="workspace-row"]').length`)
  if (wsCount < 1) {
    await cdp.eval(`document.querySelector('[data-testid="machine-row"]')?.click()`)
    await sleep(800)
  }
  await cdp.eval(`document.querySelector('[data-testid="workspace-row"]')?.click()`)
  await sleep(1200)
  const narrowed = await cdp.eval(`window.__storm.events.some((e) => e.who === 'host' && e.w < 700)`)
  if (!narrowed) throw new Error('文件抽屉没拉开（host 没有变窄）——前置状态不对')
  ok('文件抽屉已拉开，host 变窄')
  // 清零基準：之后的事件全部属于本轮往返
  await cdp.eval(`(() => {
    window.__storm.events = []
    window.__storm.resizes = []
    window.__storm.t0 = performance.now()
  })()`)

  // 进设置页（盖层：工作台在下面变宽；返回时缩回）
  await clickButtonOrThrow('设置')
  await sleep(2500)
  await clickButtonOrThrow('返回工作台')
  const backAt = Date.now()
  await sleep(1800)
  const pathname = await cdp.eval(`location.pathname`)
  if (pathname !== '/') fail(`返回后路由不在 / （实际 ${pathname}），返回点击没生效`)
  else ok('返回点击已生效（路由回到 /）')

  const events = await cdp.eval(`window.__storm.events`)
  const hostEvents = events.filter((e) => e.who === 'host')
  const parentEvents = events.filter((e) => e.who === 'parent')
  const widePhase = hostEvents.filter((e) => e.w >= 700)
  const returnPhase = hostEvents.filter((e) => e.w < 700 && e.t > (widePhase[widePhase.length - 1]?.t ?? 0))
  const parentAtReturn = [...parentEvents].reverse().find((e) => e.t >= (returnPhase[0]?.t ?? Infinity))?.w
  const overshoot = returnPhase.filter((e) => parentAtReturn !== undefined && e.w > parentAtReturn + 1)
  const returnT = returnPhase[0]?.t
  const observerResizes = returnT === undefined
    ? 0
    : (await cdp.eval(`window.__storm.resizes.filter((r) => r.t >= ${returnT} - 100).length`))

  console.log(`  盖下层 host ${widePhase.length} 条（宽 ${widePhase[0]?.w ?? '—'}），返回层 host ${returnPhase.length} 条，父容器宽 ${parentAtReturn ?? '—'}`)
  if (overshoot.length !== 0) fail(`host 宽度 ${overshoot.length} 次越过父容器（min-content 撑越仍在）：${JSON.stringify(overshoot.slice(0, 5))}`)
  else ok('host 宽度从未越过父容器（组容器 min-w-0 生效）')
  if (observerResizes > 3) fail(`返回窗口内 observer resize ${observerResizes} 次（>3，风暴仍在）`)
  else ok(`返回窗口内 observer resize 仅 ${observerResizes} 次（修复前 ~21）`)

  const sess = await api('/api/pty/sessions?scope=all', { bearer: TOKEN })
  const live = (sess.json?.sessions ?? []).find((s) => s.id === sessionId && s.exit_code == null)
  if (!live) fail('往返后终端会话丢了')
  else if (live.cols >= 100) fail(`往返后会话宽度停在盖下层的宽尺寸（${live.cols}x${live.rows}）——返回的 resize 没到 PTY`)
  else ok(`往返后会话仍在且宽度已缩回（${live.cols}x${live.rows}，attached=${live.attached}）`)

  ws.close()
  if (process.exitCode) throw new Error('有断言失败')
  ok('tab resize 风暴回归验证通过')
  cleanup()
  process.exit(0)
}

function cleanup() {
  for (const child of children) {
    try { child.kill('SIGTERM') } catch { /* gone */ }
  }
  setTimeout(() => {
    for (const child of children) {
      try { child.kill('SIGKILL') } catch { /* gone */ }
    }
    rmSync(WORK, { recursive: true, force: true })
  }, 500)
}

process.on('exit', cleanup)
process.on('SIGINT', () => { cleanup(); process.exit(1) })

main().catch((err) => {
  fail(err.stack ?? String(err))
  cleanup()
  process.exit(1)
})
