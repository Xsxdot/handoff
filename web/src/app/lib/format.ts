// format.ts —— 界面展示用的纯格式化函数（无 DOM、无副作用，可直接被 vitest 测）。
//
// 时间约定：agentd 把所有 time.Time 序列化为 RFC3339Nano 字符串，本层原样接收，
// 展示格式在这里统一（相对时间优先，完整时间可点开看）。
//
// 注意：display 层的「前 8 位短号」仅供人肉对照，任何拿去当参数的地方必须用
// 完整 ID（store 是精确匹配，短号会 404）。

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
