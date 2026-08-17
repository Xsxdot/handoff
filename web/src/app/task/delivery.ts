// delivery.ts —— 模型报工 trailer 的提取（纯函数，best-effort）。
//
// 职责：从回合正文块的**末尾**探测报工 JSON（branch/commit/summary），
// 拆成交付摘要 + 剩余正文，供 DeliverySummaryCard 渲染成卡片。
//
// 边界：
//   - 不改协议、不假设 trailer 一定存在：提取失败返回 null，正文原样展示——
//     宁可少画一张卡，不能把正文吃掉
//   - 只认末尾的 JSON 对象：JSON 后面还有正文说明它不是 trailer

// Delivery 是报工摘要的已知字段（全部可选，至少命中一个才算 trailer）。
export interface Delivery {
  branch?: string
  commit?: string
  summary?: string
}

// extractDelivery 从文本末尾提取报工 trailer。
//
// 返回：
//   - { delivery, body }: 提取成功；body 是去掉 trailer 后的正文（已 trim）
//   - null: 末尾不是 JSON 对象 / 解析失败 / 无任何已知字段
export function extractDelivery(text: string): { delivery: Delivery; body: string } | null {
  const trimmed = text.trimEnd()
  if (!trimmed.endsWith('}')) return null
  // 从最后一个 '{' 往前逐个尝试：trailer 是扁平对象，正常一次命中；
  // 正文里出现 '{' 时多试几次也只是常数开销
  for (let i = trimmed.lastIndexOf('{'); i >= 0; i = trimmed.lastIndexOf('{', i - 1)) {
    const candidate = trimmed.slice(i)
    let parsed: unknown
    try {
      parsed = JSON.parse(candidate)
    } catch {
      continue
    }
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return null
    const o = parsed as Record<string, unknown>
    const delivery: Delivery = {}
    if (typeof o.branch === 'string') delivery.branch = o.branch
    if (typeof o.commit === 'string') delivery.commit = o.commit
    if (typeof o.summary === 'string') delivery.summary = o.summary
    if (!delivery.branch && !delivery.commit && !delivery.summary) return null
    return { delivery, body: trimmed.slice(0, i).trimEnd() }
  }
  return null
}
