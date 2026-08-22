// TimingChip —— TUI tab 的任务级耗时 chip + 三分法弹出（需求 A · T7）。
//
// 职责：一眼读总时长，点开看「模型 / 工具 / 未归类」三档与工具排行。
// 边界：
//   - timing 缺席时整体不渲染（返回 null）：没有账目不画空表，与 UsageChip 同款
//   - **不并进 UsageChip**（P5=(a)）：耗时问「花了多少时间」，ctx 问「花了多少
//     钱」，两组数字挤在一个弹出里互相干扰，还会把 UsageChip 那条「都缺席就
//     不渲染」的二元规则变成三元
//   - **tool_ms 与 tool_span_ms 必须同时显示**。前者是各次调用的时长之和，
//     后者是它们占用的墙钟；并发工具时前者更大。取其一当另一个用就是在撒谎
//   - partial=true 时必须说出「未归类偏大」，不得把 other_ms 当真实空档
import { useEffect, useRef, useState } from 'react'
import type { TaskTiming, TimingBucket } from '../../api/types'
import { formatDuration } from '../lib/format'

// Row 是弹出里的一行「名称 —— 时长」。
function Row({ label, ms, hint }: { label: string; ms: number; hint?: string }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="text-right font-mono">
        {formatDuration(ms)}
        {hint && <span className="ml-1 font-sans text-[10px] text-muted-foreground">{hint}</span>}
      </dd>
    </>
  )
}

// BucketRow 渲染排行的一格及其下钻层。下钻只有一层，所以这里刻意**不递归**
// ——写成递归组件等于给「将来加第三层」开了门，而那条规则不在契约里。
function BucketRow({ b }: { b: TimingBucket }) {
  return (
    <li>
      <div className="flex items-baseline gap-2">
        <span className="min-w-0 flex-1 truncate">{b.label}</span>
        <span className="shrink-0 text-[10px] text-muted-foreground">×{b.count}</span>
        <span className="shrink-0 font-mono">{formatDuration(b.dur_ms)}</span>
      </div>
      {b.sub && b.sub.length > 0 && (
        <ul className="ml-3 border-l pl-2 text-[11px] text-muted-foreground">
          {b.sub.map((s) => (
            <li key={s.label} className="flex items-baseline gap-2">
              <span className="min-w-0 flex-1 truncate font-mono">{s.label}</span>
              <span className="shrink-0 text-[10px]">×{s.count}</span>
              <span className="shrink-0 font-mono">{formatDuration(s.dur_ms)}</span>
            </li>
          ))}
        </ul>
      )}
    </li>
  )
}

// TimingChip 渲染任务级耗时。timing 缺席即不渲染。
export function TimingChip({ timing }: { timing?: TaskTiming }) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLSpanElement>(null)

  // 点外部 / Esc 关闭。挂 mousedown 而非 click：click 要等按键抬起，
  // 期间浮层还盖在你正要点的东西上面，点击会先被浮层吃掉一次。
  //
  // 这个 effect 必须在下面的 `return null` **之前**——早退在它后面的话，
  // 账目从有到无的那一帧 hook 数量会变，React 直接报错（UsageChip 同款）。
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  if (!timing) return null

  return (
    <span ref={rootRef} className="relative">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1.5 hover:text-foreground"
      >
        耗时 {formatDuration(timing.total_ms)}
      </button>
      {open && (
        <div className="absolute left-0 top-6 z-10 w-72 rounded-lg border bg-background p-3 text-xs shadow-lg">
          <div className="mb-1 font-semibold">耗时三分</div>
          <dl className="mb-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
            <Row label="模型" ms={timing.api_ms} />
            <Row label="工具（墙钟）" ms={timing.tool_span_ms} />
            <Row label="工具（时长合计）" ms={timing.tool_ms} hint="并发时大于墙钟" />
            <Row label="未归类" ms={timing.other_ms} hint="排队/等审批/框架开销" />
            <Row label="合计" ms={timing.total_ms} />
          </dl>
          {timing.partial && (
            <p className="mb-2 text-[11px] text-amber-600 dark:text-amber-500">
              账目不全：有回合缺少分段条目（多半是还在跑，或 executor 中途退出），
              未归类偏大，别把它当成真实空档。
            </p>
          )}
          {timing.buckets && timing.buckets.length > 0 && (
            <>
              <div className="mb-1 font-semibold">工具排行</div>
              <ul className="flex flex-col gap-0.5">
                {timing.buckets.map((b) => (
                  <BucketRow key={b.label} b={b} />
                ))}
              </ul>
            </>
          )}
        </div>
      )}
    </span>
  )
}
