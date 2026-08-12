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

// Block 是时间线上的一个可渲染单元。
//
// 它比 Frame 高一层：多条 delta 帧合成一个 text/thinking 块，一对 tool_call +
// tool_result 合成一张 tool 卡。渲染层只认识 Block，不再碰 Frame。
export type Block =
  | { kind: 'turn'; key: string; turn: number; reason: string; ts: string }
  | { kind: 'text'; key: string; turn: number; text: string }
  | { kind: 'thinking'; key: string; turn: number; text: string }
  | {
      kind: 'tool'
      key: string
      turn: number
      tool: string
      input: string
      inputTruncated: boolean
      inputBytes: number
      // status 为 null 表示「还没有配上 tool_result」——它与 status: '' 是两回事，
      // 前者是没有回音，后者是上游给了个空状态。判定交给 toolState。
      status: string | null
      output: string
      outputTruncated: boolean
      outputBytes: number
    }
  | { kind: 'event'; key: string; turn: number; event: string; ts: string }
  | { kind: 'unknown'; key: string; turn: number; type: string; raw: string }

// ToolBlock 是工具卡块，单独取出来供组件签名使用。
export type ToolBlock = Extract<Block, { kind: 'tool' }>

// buildBlocks 把帧序列聚合成可渲染块。
//
// 参数：
//   - frames: 按到达顺序排列的帧（调用方保证 seq 升序）
//
// 返回：块序列，顺序即渲染顺序
//
// 两条聚合规则：
//   1. delta 合并——**连续**的同 (turn, type, part) 帧拼成一个块。中间插入任何
//      别的帧就断开，之后同 part 再来也另起一块（W4b spec §8.1 的「连续」）
//   2. 工具配对——键是 (turn, part)，不是 ref_seq。ref_seq 是 event 帧指向
//      events 表的库级 seq；part 才是调用与结果的唯一纽带，且只在回合内唯一
//
// 谁先到谁建块：正常是 tool_call 先到，但结果先于调用到达时反过来也成立，
// 后到的一半把自己的字段填进已建的块，不会裂成两张卡。
export function buildBlocks(frames: Frame[]): Block[] {
  const blocks: Block[] = []
  // open 记录「上一个块是不是一个还能续接的 text/thinking 块」。
  // 只要来了别的帧就置空——这是「连续」二字的实现。
  let open: { type: string; turn: number; part: string } | null = null
  const tools = new Map<string, ToolBlock>()

  for (const fr of frames) {
    const turn = fr.turn ?? 0
    const key = `f${fr.seq}`

    if (fr.type === 'text' || fr.type === 'reasoning') {
      const part = fr.part ?? ''
      if (open && open.type === fr.type && open.turn === turn && open.part === part) {
        const prev = blocks[blocks.length - 1] as Extract<Block, { kind: 'text' | 'thinking' }>
        prev.text += fr.delta ?? ''
      } else {
        blocks.push(
          fr.type === 'text'
            ? { kind: 'text', key, turn, text: fr.delta ?? '' }
            : { kind: 'thinking', key, turn, text: fr.delta ?? '' },
        )
        open = { type: fr.type, turn, part }
      }
      continue
    }

    open = null // 以下任何一种帧都打断 delta 续接

    switch (fr.type) {
      case 'turn_start':
        blocks.push({ kind: 'turn', key, turn, reason: fr.reason ?? '', ts: fr.ts })
        break
      case 'tool_call': {
        const k = `${turn}/${fr.part ?? ''}`
        const hit = tools.get(k)
        if (hit) {
          // 结果先到过：补上调用侧的字段，不新建卡
          hit.tool = fr.tool ?? ''
          hit.input = fr.input ?? ''
          hit.inputTruncated = fr.truncated ?? false
          hit.inputBytes = fr.bytes ?? 0
          break
        }
        const b: ToolBlock = {
          kind: 'tool', key, turn,
          tool: fr.tool ?? '', input: fr.input ?? '',
          inputTruncated: fr.truncated ?? false, inputBytes: fr.bytes ?? 0,
          status: null, output: '',
          outputTruncated: false, outputBytes: 0,
        }
        tools.set(k, b)
        blocks.push(b)
        break
      }
      case 'tool_result': {
        const k = `${turn}/${fr.part ?? ''}`
        const hit = tools.get(k)
        if (hit) {
          hit.status = fr.status ?? ''
          hit.output = fr.output ?? ''
          hit.outputTruncated = fr.truncated ?? false
          hit.outputBytes = fr.bytes ?? 0
          break
        }
        const b: ToolBlock = {
          kind: 'tool', key, turn,
          tool: '', input: '',
          inputTruncated: false, inputBytes: 0,
          status: fr.status ?? '', output: fr.output ?? '',
          outputTruncated: fr.truncated ?? false, outputBytes: fr.bytes ?? 0,
        }
        tools.set(k, b)
        blocks.push(b)
        break
      }
      case 'event':
        blocks.push({ kind: 'event', key, turn, event: fr.event ?? '', ts: fr.ts })
        break
      default:
        // 未知 type 必须渲染而不是丢弃：契约会演进，而前端比后端晚部署是常态。
        // 遇到新类型就白屏或静默吞掉，都是不可接受的失败模式。
        blocks.push({ kind: 'unknown', key, turn, type: fr.type, raw: JSON.stringify(fr) })
    }
  }
  return blocks
}

// ToolState 是工具卡的四种展示状态。
export type ToolState = 'ok' | 'error' | 'running' | 'gone'

// toolState 判定一张工具卡该显示成什么状态。
//
// 参数：
//   - status: 块的 status（null 表示没有配上 tool_result）
//   - taskState: 任务当前状态（running / waiting_answer / waiting_review / …）
//
// 返回：ok（成功）/ error（失败）/ running（进行中）/ gone（未返回）
//
// 未配上结果时分两种，不含糊成同一种：
//   - running / waiting_answer → 进行中。waiting_answer 归到这里是刻意的：
//     那说明回合被工单挡住了，工具调用确实还在等，不是没有回音
//   - 其余 → 未返回。这是真实信号：executor 半路死掉时，工具调用就是发出去
//     没有回音。把它显示成「进行中」是在撒谎——W4a 的第一轮派发就是这么死的，
//     审阅者当时从页面上看不出任何异常
export function toolState(status: string | null, taskState: string): ToolState {
  if (status === null) {
    return taskState === 'running' || taskState === 'waiting_answer' ? 'running' : 'gone'
  }
  return status === 'ok' ? 'ok' : 'error'
}

// turnsOf 返回已加载帧里出现过的回合号，升序去重。
//
// 锚点条据此生成，不需要任何新接口。注意它只覆盖**已加载**范围——
// 面板必须把这一点写在界面上，不能假装是全量目录。
export function turnsOf(frames: Frame[]): number[] {
  return [...new Set(frames.map((f) => f.turn ?? 0))].sort((a, b) => a - b)
}
