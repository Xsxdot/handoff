// verify-auth-loop.mjs —— 真实 agentd 的鉴权闭环冒烟脚本。
//
// 它证明的正是本任务唯一的验收价值：浏览器经 vite 反代与真实 cookie 会话打通
// /console → Set-Cookie → 带 cookie 请求 /api 与 /ws 的完整链路。不用 mock：
// mock 后端证明不了 ticket→302→Set-Cookie 这套 host-only cookie 行为。
//
// 前置（用一次性 agentd 实例，不要碰机器上正在跑的；最小配置见 web/README.md
// 「开发流程」第 1 步，字段名是 datadir 不是 data_dir）：
//   1. go build -o /tmp/agentd-smoke . && /tmp/agentd-smoke agentd --config /tmp/agentd-smoke.yaml
//   2. /tmp/agentd-smoke console --config /tmp/agentd-smoke.yaml --print-url 拿 ticket URL
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
import { WebSocket } from 'ws'

const VITE_URL = process.env.VITE_URL ?? 'http://localhost:5173'

function fail(msg) {
  console.error(`✗ ${msg}`)
  process.exit(1)
}

function ok(msg) {
  console.log(`✓ ${msg}`)
}

// redactTicket 把 ticket URL 里的凭据明文脱敏后再打印。
//
// 为什么必须脱敏：ticket 是 60 秒有效的一次性凭据，打日志/输出等于把凭据写进
// 排障记录与 CI 日志——与本文件头注释、全仓凭据纪律（「不在日志或代码里打印
// token、ticket、cookie 明文」）直接冲突。与下面 Set-Cookie 那行一样只露前 4 位。
function redactTicket(raw) {
  const u = new URL(raw)
  const ticket = u.searchParams.get('ticket')
  u.searchParams.delete('ticket')
  const suffix = ticket ? `?ticket=${ticket.slice(0, 4)}…` : ''
  return `${u.origin}${u.pathname}${suffix}`
}

// 第 0 步：校验入参并拆出 ticket。
const rawTicketUrl = process.argv[2]
if (!rawTicketUrl) {
  fail('用法: node scripts/verify-auth-loop.mjs "<ticket-url>"（把 handoff console --print-url 的端口换成 vite 端口后传入）')
}
const u = new URL(rawTicketUrl)
const ticket = u.searchParams.get('ticket')
if (!ticket) fail(`ticket URL 缺少 ticket 参数: ${redactTicket(rawTicketUrl)}`)

// 反代后的兑换地址：浏览器实际打开的就是这个（host 换成 vite，路径/query 原样）。
const proxied = new URL(u.pathname + u.search, VITE_URL)
console.log(`[0] ticket URL=${redactTicket(rawTicketUrl)}`)
console.log(`[0] 经 vite 反代兑换地址=${redactTicket(proxied)}`)

// 第 1 步：手动跟 302 抓 Set-Cookie（redirect: 'manual' 让 fetch 不自动跟随）。
let resp = await fetch(proxied, { redirect: 'manual' })
if (resp.status !== 302) fail(`/console 兑换应 302 到 /，实际 ${resp.status}`)
const loc = resp.headers.get('location')
if (loc !== '/') fail(`302 Location 应为 /，实际 ${JSON.stringify(loc)}`)
const setCookie = resp.headers.getSetCookie?.() ?? []
const session = setCookie.find((c) => c.startsWith('handoff_session='))
if (!session) {
  // 失败信息也只露 cookie 名不露值：值是会话凭据明文，打印它违反凭据纪律
  fail(`302 未带回 handoff_session Set-Cookie（实际: ${setCookie.map((c) => `${c.split('=')[0]}=…`).join(', ') || '（空）'}）`)
}
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
// wsProbe 打开一条 WS 并等待「首条事件」或「连接关闭」，两者到点都没来就失败。
//
// 返回 {code, message}：
//   code=0       已收到首条事件（onMessage 返回 true 时结束）
//   code=1008    agentd 以 PolicyViolation 关闭（任务不存在/会话被吊销）
//   code=-1      8s 内握手未完成
//   code=-2      握手/连接出错
//   code=-3      已连上，但 postOpenTimeoutMs 内既没收到事件也没被关闭
//
// 参数：
//   - onMessage: 每收到一条数据帧调用一次；返回 true 视为成功收尾
//
// 为什么 open 之后还要一个独立超时：风险区在 open 之后。任务可能一直 running
// 却零事件（fake 空脚本正是这样），服务器也可能连上后不按 1008 关闭。若没有
// 这个超时，有任务分支会永久挂起、无人值守静默吊死——一个会挂死的验收脚本
// 比没有还糟。
//
// 所有出口都走 settle：解绑全部回调并 terminate。不 terminate 会留下悬挂句柄
// 让 node 进程不退出；不 resolve 会让脚本永久等待。两者都是必须堵死的死法。
function wsProbe(url, headers, { onMessage, postOpenTimeoutMs = 10000 } = {}) {
  return new Promise((resolve) => {
    const ws = new WebSocket(url, { headers, handshakeTimeout: 5000 })
    let settled = false
    let postOpenTimer = null

    const settle = (code, message) => {
      if (settled) return
      settled = true
      if (postOpenTimer !== null) clearTimeout(postOpenTimer)
      ws.onopen = null
      ws.onmessage = null
      ws.onerror = null
      ws.onclose = null
      ws.terminate()
      resolve({ code, message })
    }

    const handshakeTimer = setTimeout(() => settle(-1, '握手超时'), 8000)
    ws.on('open', () => {
      clearTimeout(handshakeTimer)
      postOpenTimer = setTimeout(
        () => settle(-3, `已连上但 ${postOpenTimeoutMs / 1000}s 内既没收到事件也没被关闭`),
        postOpenTimeoutMs,
      )
    })
    ws.on('message', (data) => {
      if (onMessage?.(data)) settle(0, '已收到首条事件')
    })
    ws.on('close', (code, reason) => settle(code, reason.toString()))
    ws.on('error', (err) => settle(-2, err.message))
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
    {
      onMessage: (data) => {
        try {
          events.push(JSON.parse(data.toString()))
        } catch (err) {
          // 帧解析失败不该当场抛崩脚本，记下错误按失败收尾
          events.push({ seq: '?', type: `解析失败: ${err.message}` })
        }
        return events.length >= 1
      },
    },
  )
  if (result.code === 0) ok(`/ws/events → 收到事件：${events.map((e) => `#${e.seq}:${e.type}`).join(', ')}`)
  else fail(`/ws/events 未收到事件（${result.message}，code ${result.code}）`)
}

console.log('\n鉴权闭环冒烟通过：ticket → Set-Cookie → 带 cookie 的 /api 与 /ws 全链路打通。')
console.log('（临时 agentd 实例与临时数据目录请手动清理；渲染目视确认请在浏览器里完成）')
