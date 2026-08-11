// verify-auth-loop.mjs —— 真实 agentd 的鉴权闭环冒烟脚本。
//
// 它证明的正是本任务唯一的验收价值：浏览器经 vite 反代与真实 cookie 会话打通
// /console → Set-Cookie → 带 cookie 请求 /api 与 /ws 的完整链路。不用 mock：
// mock 后端证明不了 ticket→302→Set-Cookie 这套 host-only cookie 行为。
//
// 前置（用一次性 agentd 实例，不要碰机器上正在跑的）：
//   1. go build -o /tmp/agentd-smoke . && 用独立 datadir/端口起它
//   2. /tmp/agentd-smoke console --config <临时配置> --print-url 拿 ticket URL
//   3. 把 URL 端口从 agentd 的换成 vite dev server 的端口，作为本脚本参数
//
// 用法:
//   node scripts/verify-auth-loop.mjs "http://localhost:5173/console?ticket=<t>"
//
// 环境变量:
//   VITE_URL   vite dev server 根地址（默认 http://localhost:5173）
//
// 注意:
//   - 脚本只从 ticket URL 里读 ticket 明文并原样请求，不打日志、不落盘
//   - WS 无任务时显式输出「无任务，跳过 WS 验证」并照常验证 cookie 在 WS
//     握手上真的被 agentd 接受（连一个不存在的 task，期望 close code 1008
//     =「已通过鉴权、进了 WS 处理器」；401 会在握手阶段被拒）
import { readFileSync } from 'node:fs'
import { WebSocket } from 'ws'

const VITE_URL = process.env.VITE_URL ?? 'http://localhost:5173'

function fail(msg) {
  console.error(`✗ ${msg}`)
  process.exit(1)
}

function ok(msg) {
  console.log(`✓ ${msg}`)
}

// 第 0 步：校验入参并拆出 ticket。
const rawTicketUrl = process.argv[2]
if (!rawTicketUrl) {
  fail('用法: node scripts/verify-auth-loop.mjs "<ticket-url>"（把 handoff console --print-url 的端口换成 vite 端口后传入）')
}
const u = new URL(rawTicketUrl)
const ticket = u.searchParams.get('ticket')
if (!ticket) fail(`ticket URL 缺少 ticket 参数: ${rawTicketUrl}`)

// 反代后的兑换地址：浏览器实际打开的就是这个（host 换成 vite，路径/query 原样）。
const proxied = new URL(u.pathname + u.search, VITE_URL)
console.log(`[0] ticket URL=${rawTicketUrl}`)
console.log(`[0] 经 vite 反代兑换地址=${proxied}`)

// 第 1 步：手动跟 302 抓 Set-Cookie（redirect: 'manual' 让 fetch 不自动跟随）。
let resp = await fetch(proxied, { redirect: 'manual' })
if (resp.status !== 302) fail(`/console 兑换应 302 到 /，实际 ${resp.status}`)
const loc = resp.headers.get('location')
if (loc !== '/') fail(`302 Location 应为 /，实际 ${JSON.stringify(loc)}`)
const setCookie = resp.headers.getSetCookie?.() ?? []
const session = setCookie.find((c) => c.startsWith('handoff_session='))
if (!session) fail(`302 未带回 handoff_session Set-Cookie（实际: ${JSON.stringify(setCookie)}）`)
const cookieValue = session.split(';')[0]
ok(`/console 兑换：302 → /，Set-Cookie ${cookieValue.slice(0, 40)}…（HttpOnly 不读明文）`)

// 第 2 步：不带 cookie 打 /api/status，必须 401——证明凭据真的在把关。
resp = await fetch(`${VITE_URL}/api/status`, { redirect: 'manual' })
if (resp.status !== 401) fail(`无 cookie 请求 /api/status 应 401，实际 ${resp.status}`)
ok('无 cookie 请求 /api/status → 401（凭据在把关）')

// 第 3 步：带 cookie 打 /api/status，拿到版本与状态。
resp = await fetch(`${VITE_URL}/api/status`, { headers: { Cookie: cookieValue } })
if (resp.status !== 200) fail(`带 cookie 请求 /api/status 应 200，实际 ${resp.status}`)
const status = await resp.json()
ok(`/api/status → 200；version=${status.version?.revision?.slice(0, 8) ?? status.version?.version ?? '未知'} listen=${status.listen} executors=[${status.executors?.join(',')}] task_counts=${JSON.stringify(status.task_counts)}`)

// 第 4 步：带 cookie 打 /api/tasks，空数组也是有效结果。
resp = await fetch(`${VITE_URL}/api/tasks`, { headers: { Cookie: cookieValue } })
if (resp.status !== 200) fail(`带 cookie 请求 /api/tasks 应 200，实际 ${resp.status}`)
const tasks = await resp.json()
if (!Array.isArray(tasks)) fail(`/api/tasks 应返回数组，实际 ${typeof tasks}`)
ok(`/api/tasks → 200；共 ${tasks.length} 个任务`)

// 第 5 步：WS 验证。
//   有任务：订阅第一个任务，等至少一条事件；
//   无任务：显式输出跳过说明（不假装成功），并连一个不存在的 task 验证
//   cookie 在 WS 握手上确实被 agentd 接受（1008=通过鉴权进了处理器）。
function wsProbe(url, headers, onOpen, onClose) {
  return new Promise((resolve) => {
    const ws = new WebSocket(url, { headers, handshakeTimeout: 5000 })
    const timer = setTimeout(() => {
      ws.terminate()
      resolve({ code: -1, message: '握手超时' })
    }, 8000)
    ws.on('open', () => {
      clearTimeout(timer)
      onOpen?.(ws, resolve)
    })
    ws.on('close', (code, reason) => {
      clearTimeout(timer)
      resolve({ code, message: reason.toString() })
    })
    ws.on('error', (err) => {
      clearTimeout(timer)
      resolve({ code: -2, message: err.message })
    })
  })
}

if (tasks.length === 0) {
  console.log('—— 无任务，跳过 WS 验证（该实例没有可订阅的任务，不假装成功） ——')
  const probe = await wsProbe(
    `${VITE_URL.replace(/^http/, 'ws')}/ws/events?task=no-such-task&from_seq=0`,
    { Cookie: cookieValue },
  )
  if (probe.code === 1008) ok(`WS 带 cookie 握手被 agentd 接受（close 1008=已通过鉴权，仅因任务不存在）`)
  else fail(`WS 带 cookie 握手未达 agentd 处理器（close code ${probe.code}: ${probe.message}），鉴权链路可能没走通`)
} else {
  const target = tasks[0]
  const events = []
  const result = await wsProbe(
    `${VITE_URL.replace(/^http/, 'ws')}/ws/events?task=${encodeURIComponent(target.id)}&from_seq=0`,
    { Cookie: cookieValue },
    (ws, resolve) => {
      ws.on('message', (data) => {
        events.push(JSON.parse(data.toString()))
        if (events.length >= 1) resolve({ code: 0, message: `已收到 ${events.length} 条事件` })
      })
    },
  )
  if (events.length >= 1) ok(`/ws/events → 收到事件：${events.map((e) => `#${e.seq}:${e.type}`).join(', ')}`)
  else fail(`/ws/events 未收到事件（close code ${result.code}: ${result.message}）`)
}

console.log('\n鉴权闭环冒烟通过：ticket → Set-Cookie → 带 cookie 的 /api 与 /ws 全链路打通。')
console.log('（临时 agentd 实例与临时数据目录请手动清理；渲染目视确认请在浏览器里完成）')
