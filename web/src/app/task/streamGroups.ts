// streamGroups.ts —— 会话流的连续工具块分组（纯函数）。
//
// 职责：把连续 ≥minGroupSize 个 tool 块折成一个 toolGroup，供渲染层做
// 「执行了 N 步操作」的折叠展示——真机走查发现执行器动辄连发十几条命令，
// 逐行平铺会把正文淹掉。
// 边界：
//   - 只分组 tool 块；有内容的 text/thinking/event/turn 一律打断分组（它们承载
//     因果，不能被折进组里）；不可见块直接剔除，不制造用户看不见的断点
//   - 不做展开状态管理（那是渲染层的事），也不判断任务状态
import type { Block, ToolBlock } from './frames'
import { eventPhrase } from './eventPhrase'

// minGroupSize 是折组的最小连续条数：1-2 条平铺反而更直观，3 条起才值得折。
export const minGroupSize = 3

// ToolGroupBlock 是一组连续工具块。failed/pending 供组行摘要与色调用：
// failed = status 为非 ok 非 null 的条数；pending = 尚无回音（null）的条数。
export interface ToolGroupBlock {
  kind: 'toolGroup'
  key: string
  tools: ToolBlock[]
  failed: number
  pending: number
}

// StreamItem 是渲染层消费的流单元：原始块或工具组。
export type StreamItem = Block | ToolGroupBlock

// isInvisibleBlock 判断渲染层会返回 null 或空内容的块。它们不占视觉空间，
// 若仍作为分隔物会把用户眼中的连续工具误拆成多个永远达不到阈值的小段。
function isInvisibleBlock(block: Block): boolean {
  if (block.kind === 'event') return eventPhrase(block.event) === null
  if (block.kind === 'text' || block.kind === 'thinking') return block.text.trim() === ''
  return false
}

// groupBlocks 把块序列折成流单元序列。组 key 取首成员 key 加 g- 前缀，
// 保证同一段数据重渲染时 key 稳定（React 不重挂）。
export function groupBlocks(blocks: Block[]): StreamItem[] {
  const items: StreamItem[] = []
  let run: ToolBlock[] = []

  const flush = () => {
    if (run.length >= minGroupSize) {
      items.push({
        kind: 'toolGroup',
        key: `g-${run[0].key}`,
        tools: run,
        failed: run.filter((t) => t.status !== null && t.status !== 'ok').length,
        pending: run.filter((t) => t.status === null).length,
      })
    } else {
      items.push(...run)
    }
    run = []
  }

  for (const b of blocks) {
    if (isInvisibleBlock(b)) continue
    if (b.kind === 'tool') {
      run.push(b)
      continue
    }
    flush()
    items.push(b)
  }
  flush()
  return items
}
