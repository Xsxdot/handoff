// frames.ts —— frames.jsonl 的解析与聚合（纯函数）。
//
// 职责：
//   - scanLines: ndjson 文本增量 → 帧数组 + 坏行计数 + 残留半行
//   - buildBlocks: 帧数组 → 可渲染块（delta 按 part 合并、tool_call/tool_result
//     按 (turn, part) 配对、未知 type 保留成中性块）
//   - toolState: 工具卡状态判定
//   - turnsOf: 已加载帧里出现过的回合号
//
// 边界：
//   - 不碰 DOM、不发请求、不 import react——它必须能脱离浏览器被穷举测试
//   - 不认识任何具体 executor：四家的差异在 W4a 侧已经抹平成 Frame
//   - 不做截断、不做上限裁剪：那是 useFramesStream 的事
//
// 为什么坏行是「跳过并计数」而不是抛异常：一行坏数据不该让整条时间线白屏；
// 但也不能静默——计数会显示在面板顶部，那是「采集侧出问题了」的唯一信号。
import type { Frame } from '../../api/types'

// maxLoadedFrames 是已加载帧数的硬上限。
//
// 本期不做虚拟化（YAGNI，且会让 stick-bottom 与 prepend 补偿都变复杂），
// 那就必须有一个说得出口的边界：没有边界的后果不是「偶尔慢」，而是在最长、
// 最需要审阅的那些任务上悄悄卡死。到顶后停止回翻并提示改用 handoff frames。
export const maxLoadedFrames = 5000

// ScanResult 是一次增量扫描的产物。
export interface ScanResult {
  // frames 是本次解析成功的帧，保持到达顺序
  frames: Frame[]
  // bad 是本次跳过的坏行数（累加由调用方负责）
  bad: number
  // rest 是本次仍不完整的尾行，下次调用必须原样带回来
  rest: string
}

// scanLines 把「上次残留的半行 + 本次新到的文本」切成完整帧。
//
// 参数：
//   - buffered: 上次调用返回的 rest（首次传 ''）
//   - chunk: 本次到达的文本增量
//
// 返回：ScanResult
//
// 注意：
//   - 服务端保证只在完整行边界切（W4a §7.2）。这层缓冲是**防御**不是依赖——
//     服务端的保证是契约，客户端仍要能扛住半行
//   - follow 空闲时 agentd 每 20s 发一个换行保活，空行必须当正常跳过、不计坏行
//   - 缺 seq 或缺 type 的 JSON 也算坏行：没有 seq 无法排序与去重，
//     没有 type 无法分发渲染，留着它只会在下游炸得更远
export function scanLines(buffered: string, chunk: string): ScanResult {
  const text = buffered + chunk
  const nl = text.lastIndexOf('\n')
  // 一个换行都没有：整段都还是半行，原样留到下次
  if (nl < 0) return { frames: [], bad: 0, rest: text }

  const frames: Frame[] = []
  let bad = 0
  for (const raw of text.slice(0, nl).split('\n')) {
    if (raw.trim() === '') continue // 保活换行，正常
    let parsed: unknown
    try {
      parsed = JSON.parse(raw)
    } catch {
      bad++
      continue
    }
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      bad++
      continue
    }
    const f = parsed as Partial<Frame>
    if (typeof f.seq !== 'number' || typeof f.type !== 'string') {
      bad++
      continue
    }
    frames.push(parsed as Frame)
  }
  return { frames, bad, rest: text.slice(nl + 1) }
}
