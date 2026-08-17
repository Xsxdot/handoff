// ToolCard —— 时间线上的一次工具调用（调用与结果合成一张卡）。
//
// 职责：
//   - 折叠态显示工具名、参数摘要、状态徽章
//   - 展开态显示完整输入与输出，截断时标出原始字节数
//
// 边界：
//   - 不提供「看全文」的旁路。要看被截断的全文，出口是 handoff diff / fetch / run
//   - 不解析工具语义：input 是 executor 给的原文（多为 JSON），原样显示
import { useState } from 'react'
import { cn } from '@/lib/utils'
import { toolState, type ToolBlock, type ToolState } from './frames'

// STATE_LABEL 是四种工具状态的中文标签。
//
// 「未返回」与「进行中」必须分开：前者是 executor 半路死掉、调用发出去没有回音，
// 把它显示成后者是在撒谎。
const STATE_LABEL: Record<ToolState, string> = {
  ok: '成功',
  error: '失败',
  running: '进行中',
  gone: '未返回',
}

const DOT_CLS: Record<ToolState, string> = {
  ok: 'bg-green-600',
  error: 'bg-destructive',
  running: 'bg-amber-500 animate-pulse',
  gone: 'border border-amber-500 bg-transparent',
}

// argSummary 从工具入参里挑一行可读摘要。
//
// 只读已知形状的字段，其余回退原文；绝不因为解析失败而吞掉整张卡
// （与 EventsPanel.eventSummary 同一条纪律）。
function argSummary(input: string): string {
  try {
    const o = JSON.parse(input) as Record<string, unknown>
    for (const k of ['path', 'cmd', 'command', 'pattern', 'file_path', 'query']) {
      const v = o[k]
      if (typeof v === 'string' && v !== '') return v
    }
  } catch {
    // 入参不是 JSON（grok 给的是人类摘要），原样当摘要用
  }
  return input
}

// truncNote 生成截断提示文案。
function truncNote(bytes: number): string {
  return `已截断（原始 ${bytes} 字节，保留头 4KB + 尾 4KB）；要看全文用 handoff diff / fetch / run`
}

// ToolCard 渲染一次工具调用。
//
// 参数：
//   - block: 已配对好的工具块（status 为 null 表示还没有结果）
//   - taskState: 任务当前状态，决定未配对时显示「进行中」还是「未返回」
export function ToolCard({ block, taskState }: { block: ToolBlock; taskState: string }) {
  const [open, setOpen] = useState(false)
  const st = toolState(block.status, taskState)
  return (
    <div className="my-1 text-xs">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 py-0.5 text-left text-muted-foreground hover:text-foreground"
      >
        <span className="flex w-3.5 shrink-0 justify-center">
          <span className={cn('size-[7px] rounded-full', DOT_CLS[st])} />
        </span>
        <span className="shrink-0 font-medium text-foreground">
          {block.tool || '(未知工具)'}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono">{argSummary(block.input)}</span>
        <span className="shrink-0 text-[11px]">{STATE_LABEL[st]}</span>
      </button>
      {open && (
        <div className="ml-[7px] border-l-2 border-border pl-3 text-xs">
          <div className="px-2.5 py-2">
            <span className="mb-1 block text-[11px] text-muted-foreground">输入</span>
            <pre className="overflow-x-auto whitespace-pre-wrap break-words font-mono">{block.input}</pre>
            {block.inputTruncated && (
              <p className="mt-1 text-[11px] text-amber-600 dark:text-amber-500">{truncNote(block.inputBytes)}</p>
            )}
          </div>
          <div className="border-t border-dashed px-2.5 py-2">
            <span className="mb-1 block text-[11px] text-muted-foreground">输出</span>
            {block.status === null ? (
              // 没有回音是真实信号，必须写出来，不能留空让人以为「输出为空」
              <p className="text-amber-600 dark:text-amber-500">
                {st === 'running' ? '仍在等待结果…' : 'executor 已不在，此调用没有回音'}
              </p>
            ) : (
              <pre className="overflow-x-auto whitespace-pre-wrap break-words font-mono">{block.output}</pre>
            )}
            {block.outputTruncated && (
              <p className="mt-1 text-[11px] text-amber-600 dark:text-amber-500">{truncNote(block.outputBytes)}</p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
