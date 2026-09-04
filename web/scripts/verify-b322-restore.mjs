// verify-b322-restore.mjs —— 隔离 agentd + 无头 Chrome 验恢复泵已停。
//
// 不碰 ~/.handoff。临时 datadir、临时端口、指向自身的 local target。
// 断言：死 sessionId 的 tab 不建会话；无引用的 workspace 活会话不收成新组；
// 刷新后组数不变。
import { spawn } from 'node:child_process'
import { mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { createConnection } from 'node:net'
import { setTimeout as sleep } from 'node:timers/promises'
import { WebSocket } from 'ws'

const ROOT = new URL('../..', import.meta.url).pathname.replace(/\/$/, '')
const WEB = `${ROOT}/web`
const BIN = process.env.HANDOFF_BIN ?? '/tmp/handoff-b322'
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
const TOKEN = 'b322b322b322b322b322b322b322b322'
const AGENTD_PORT = Number(process.env.B322_AGENTD_PORT ?? 18777)
const VITE_PORT = Number(process.env.B322_VITE_PORT ?? 5174)
const CDP_PORT = Number(process.env.B322_CDP_PORT ?? 9223)
const WORK = process.env.B322_WORK ?? `/tmp/b322-isolate-${process.pid}`
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

async function api(path, { method = 'GET', body, cookie, bearer } = {}) {
  const headers = { accept: 'application/json' }
  if (body !== undefined) headers['content-type'] = 'application/json'
  if (bearer) headers.authorization = `Bearer ${bearer}`
  if (cookie) headers.cookie = cookie
  const resp = await fetch(`http://127.0.0.1:${AGENTD_PORT}${path}`, {
    method, headers, body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await resp.text()
  let json = null
  try { json = JSON.parse(text) } catch { /* not json */ }
  return { status: resp.status, json, text, headers: resp.headers }
}

function seedPayload() {
  const base = {
    key: WS_DIR, kind: 'workspace', path: WS_DIR, label: 'b322', projectName: 'b322', machine: '',
  }
  return JSON.stringify({
    v: 2,
    wb: {
      activeGroupId: 'g1',
      groups: [{
        id: 'g1', name: '组 1', autoName: true,
        columns: [{ panes: [{
          id: 't1', base,
          content: { kind: 'terminal', seq: 1, sessionId: 'dead-aaaa-1111-2222-333333333333' },
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
        this.console.push({ type: msg.params.type, args })
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
    'targets:',
    '  local:',
    `    addr: http://127.0.0.1:${AGENTD_PORT}`,
    `    token: ${TOKEN}`,
    'ledger:',
    '  dsn: ""',
    'terminal:',
    '  auto: false',
    '',
  ].join('\n'))

  const agentd = run(BIN, ['--config', `${WORK}/config.yaml`, 'agentd'], { env: { ...process.env, HANDOFF_LOG_LEVEL: 'info' } })
  await waitPort(AGENTD_PORT)
  ok(`隔离 agentd 在 :${AGENTD_PORT}`)

  const put = await api('/api/workbench/state/base', {
    method: 'PUT', bearer: TOKEN,
    body: { base_key: '__global_workbench__', payload: seedPayload() },
  })
  if (put.status !== 200) throw new Error(`seed workbench ${put.status} ${put.text}`)
  ok('写入含死 sessionId 的 1 组快照')

  let liveId = ''
  const created = await api('/api/pty/sessions', {
    method: 'POST', bearer: TOKEN,
    body: { base_kind: 'workspace', base_path: WS_DIR, cols: 80, rows: 24 },
  })
  if (created.status === 200 && created.json?.id) {
    liveId = created.json.id
    ok(`建了一条无 tab 引用的活会话 ${liveId.slice(0, 8)}…`)
  } else {
    console.log(`! 本机无法建 PTY（${created.status}），跳过活孤儿组数断言`)
  }

  const fanout = await api('/api/pty/sessions?scope=all', { bearer: TOKEN })
  const machineNames = (fanout.json?.machines ?? []).map((m) => m.name)
  if (machineNames.includes('local')) fail(`自扇出仍在 machines: ${JSON.stringify(machineNames)}`)
  else ok(`扇出 machines 不含 local（${JSON.stringify(machineNames)}）`)

  const vite = run('npx', ['vite', '--port', String(VITE_PORT), '--strictPort', '--host', '127.0.0.1'], {
    cwd: WEB,
    env: { ...process.env, AGENTD_URL: `http://127.0.0.1:${AGENTD_PORT}` },
  })
  await waitPort(VITE_PORT)
  ok(`vite :${VITE_PORT} 反代隔离 agentd`)

  const ticket = await api('/api/auth/tickets', { method: 'POST', bearer: TOKEN, body: { device_name: 'b322-isolate' } })
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

  await cdp.send('Page.navigate', { url: consoleUrl })
  await sleep(3500)

  const countGroups = () => cdp.eval(`document.querySelectorAll('[data-testid="tab-surface"]').length`)
  const reopenButtons = () => cdp.eval(`document.querySelectorAll('button').length && Array.from(document.querySelectorAll('button')).filter(b => b.textContent.includes('重开一个终端')).length`)

  const groups1 = await countGroups()
  const reopen1 = await reopenButtons()
  const sess1 = await api('/api/pty/sessions?scope=all', { bearer: TOKEN })
  const live1 = (sess1.json?.sessions ?? []).filter((s) => s.exit_code == null)
  const unique1 = new Set(live1.map((s) => s.id)).size
  console.log(`  打开#1 groups=${groups1} reopen=${reopen1} uniqueLive=${unique1}`)
  if (groups1 !== 1) fail(`第一次打开组数应为 1，实得 ${groups1}`)
  else ok('第一次打开仍是 1 组（活孤儿未收编）')
  if (reopen1 < 1) fail('死 tab 应出现重开按钮')
  else ok('死 tab 给出重开入口')
  const expectedLive = liveId ? 1 : 0
  if (unique1 !== expectedLive) fail(`死 tab 不该建会话：uniqueLive=${unique1} 期望 ${expectedLive}`)
  else ok('死 tab 没有静默建会话')

  await cdp.send('Page.reload', { ignoreCache: true })
  await sleep(2500)
  const groups2 = await countGroups()
  const sess2 = await api('/api/pty/sessions?scope=all', { bearer: TOKEN })
  const unique2 = new Set((sess2.json?.sessions ?? []).filter((s) => s.exit_code == null).map((s) => s.id)).size
  console.log(`  打开#2 groups=${groups2} uniqueLive=${unique2}`)
  if (groups2 !== groups1) fail(`刷新后组数变化 ${groups1} → ${groups2}`)
  else ok('刷新后组数不变')
  if (unique2 !== unique1) fail(`刷新后会话数变化 ${unique1} → ${unique2}`)
  else ok('刷新后活会话数不变')

  ws.close()
  if (process.exitCode) throw new Error('有断言失败')
  ok('B322 隔离实例无头验收通过')
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
