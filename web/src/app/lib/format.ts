// format.ts —— 界面展示用的纯格式化函数（无 DOM、无副作用，可直接被 vitest 测）。
//
// 时间约定：agentd 把所有 time.Time 序列化为 RFC3339Nano 字符串，本层原样接收，
// 展示格式在这里统一（相对时间优先，完整时间可点开看）。
//
// 注意：display 层的「前 8 位短号」仅供人肉对照，任何拿去当参数的地方必须用
// 完整 ID（store 是精确匹配，短号会 404）。
import type { Cost, Task } from '../../api/types'

// shortID 取 UUID 的前 8 位，与 handoff-<id8> 的 CLI 惯例一致。
export function shortID(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id
}

// shortCommit 取 40 位 commit sha 的前 8 位；空串原样返回（老任务无 base_commit）。
export function shortCommit(sha: string): string {
  return sha.length > 8 ? sha.slice(0, 8) : sha
}

// formatRelative 把 RFC3339 时间换算成「N 秒/分钟/小时/天前」；解析失败回「—」。
//
// 参数：
//   - iso: agentd 的时间字符串
//   - now: 相对时间基准（毫秒时间戳），测试可注入；缺省为当前时刻
export function formatRelative(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return '—'
  const sec = Math.floor((now - t) / 1000)
  if (sec < 0) return '刚刚'
  if (sec < 60) return `${sec} 秒前`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min} 分钟前`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h} 小时前`
  const d = Math.floor(h / 24)
  return `${d} 天前`
}

// formatFull 输出完整的本地化时间（含日期时分秒）；解析失败原样返回输入。
export function formatFull(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return iso
  return new Date(t).toLocaleString()
}

// formatSize 把字节数格式化成人能读的大小。
//
// 用 1024 进制并保留一位小数：这里的读者是在判断「这文件为什么不给我编辑」，
// 3.2 MB 比 3355443 字节能直接回答那个问题
export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB']
  let v = bytes / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

// errorMessage 把任意抛出的值归一成人类可读的字符串。
//
// 为什么单独归口：catch 里拿到的不一定是 Error（fetch 网络层可能抛字符串、
// 事件回调里可能有别的东西），统一在这里收口保证界面展示的是稳定文案。
export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

// formatTokens 把 token 数格式化成人眼可读的短串：千位以上用 k 并保留一位小数。
export function formatTokens(n: number): string {
  if (n < 1000) return String(n)
  return `${(n / 1000).toFixed(1)}k`
}

// formatExecutorLine 组装任务详情页「执行器」行的整行文案。
//
// 三段各自「有才显示」，不占位、不显示 — 或 0：
//   执行器 · 实际模型名 · 24.7k / 258.4k (10%)
//
// 两条产品决策写死在这里：
//   - **只显实际模型名**（task.actual_model）。task.model 是 dispatch 的入参，
//     用户要知道的是「现在实际跑在什么上」，二者不一致也不提示。
//   - **只有拿到分母才显百分比**。分母缺席就只显绝对值，绝不由前端猜一个
//     ——猜错是静默错误，百分比照常显示只是错的。
export function formatExecutorLine(task: Task): string {
  const parts: string[] = [task.executor || '（缺省）']
  if (task.actual_model) parts.push(task.actual_model)
  const u = task.usage
  if (u && u.context_tokens > 0) {
    if (u.context_window && u.context_window > 0) {
      const pct = Math.round((u.context_tokens / u.context_window) * 100)
      parts.push(`${formatTokens(u.context_tokens)} / ${formatTokens(u.context_window)} (${pct}%)`)
    } else {
      parts.push(`${formatTokens(u.context_tokens)} tokens`)
    }
  }
  return parts.join(' · ')
}

// TICKS_PER_USD 是花费的内部单位换算：后端全程用整数 ticks 累加，
// 只在这里（展示的最后一步）转成美元。
const TICKS_PER_USD = 1e10

// formatCost 把累计花费格式化成「金额 + 可信度小标」。
//
// 返回 { text, hint }：text 进正文，hint 是紧跟其后的小字（空串=不显示小标）。
//
// 四种状态三种形态（用户已确认的形态决策）：
//   - reported  → `$4.20`，无标记：这是执行器自报的完整值
//   - estimated → `≈$4.20` +「估算」：handoff 按 API 牌价算的，可能不准
//   - partial   → `≈$4.20` +「不全」：**下界**，真实值只会更高
//   - unknown   → `—`：一次都没拿到
//
// 为什么 estimated 与 partial 不合并成一个「≈」：它们对用户的含义相反。
// 估算是近似值（可能高可能低），不全是下界（只会更高）。合并会把下界讲成
// 近似值——看到 `≈$4.20` 的人不会想到实际可能是 $8。
//
// 为什么 unknown 显「—」而不是 `$0.00`：花费的缺席意味着
// "unreported or incomplete, never free"。用 0 冒充「不知道」是在撒谎。
export function formatCost(cost: Cost): { text: string; hint: string } {
  if (cost.state === 'unknown') return { text: '—', hint: '' }
  const usd = cost.ticks / TICKS_PER_USD
  // 不足一分的金额用更细的位数，否则 $0.004 会显示成 $0.00——那和「免费」没区别
  const amount = usd >= 0.01 ? usd.toFixed(2) : usd.toFixed(4)
  if (cost.state === 'reported') return { text: `$${amount}`, hint: '' }
  return { text: `≈$${amount}`, hint: cost.state === 'estimated' ? '估算' : '不全' }
}

// formatCumulativeLine 组装「累计用量」视图的整行文案（不含「累计」前缀，
// 前缀由 TaskHeader 单独渲染成弱化样式）。
//
//   1200.0k · 输入 340.2k · 缓存 820.5k · 输出 39.3k · ≈$4.20
//
// 没有累计数据时返回空串，由调用方决定不渲染这一行。
// 花费缺席时只显四项 token——不知道花了多少钱，不代表不知道烧了多少 token。
export function formatCumulativeLine(task: Task): string {
  const c = task.cumulative
  if (!c) return ''
  const parts = [
    formatTokens(c.total_tokens),
    `输入 ${formatTokens(c.input_tokens)}`,
    `缓存 ${formatTokens(c.cached_tokens)}`,
    `输出 ${formatTokens(c.output_tokens)}`,
  ]
  if (c.cost) parts.push(formatCost(c.cost).text)
  return parts.join(' · ')
}
