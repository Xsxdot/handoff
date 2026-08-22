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
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
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

// POPOVER_MARGIN 是弹出层与裁剪边界之间留的空隙。
const POPOVER_MARGIN = 8

// clampIntoView 把弹出层横向拉回第一个会裁剪它的祖先之内。
//
// 为什么不能只靠锚定方向（left-0 / right-0）解决：chip 在遥测行里的位置随窗口
// 宽度变——行会换行，chip 就不再是最右的元素。2026-08-22 真机实测，两个方向
// 各错一半：1280px 下 left-0 向右溢出 32px（数字整列被裁掉），900px 下行换行后
// right-0 向左溢出 34px（标签被裁成「三分」「（墙钟）」）。**两次 DOM 里文本都
// 俱在**，所以 jsdom 测试全绿——布局这一层机内验不了，只能量。
//
// 直接改 transform 而不走 state：state 会让「量位置 → 改位置 → 再量」变成一个
// 会自激的回路；这里先清掉 transform 量原始位置，再一次性设好，天然收敛。
function clampIntoView(pop: HTMLDivElement) {
  pop.style.transform = ''
  let el: HTMLElement | null = pop.parentElement
  let clip: HTMLElement | null = null
  while (el && el !== document.body) {
    const cs = getComputedStyle(el)
    if (cs.overflowX !== 'visible' || cs.overflowY !== 'visible') {
      clip = el
      break
    }
    el = el.parentElement
  }
  const b = clip
    ? clip.getBoundingClientRect()
    : { left: 0, right: window.innerWidth }
  const r = pop.getBoundingClientRect()
  let dx = 0
  if (r.right > b.right - POPOVER_MARGIN) dx = b.right - POPOVER_MARGIN - r.right
  if (r.left + dx < b.left + POPOVER_MARGIN) dx = b.left + POPOVER_MARGIN - r.left
  if (dx) pop.style.transform = `translateX(${Math.round(dx)}px)`
}

// TimingChip 渲染任务级耗时。timing 缺席即不渲染。
export function TimingChip({ timing }: { timing?: TaskTiming }) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLSpanElement>(null)
  const popRef = useRef<HTMLDivElement>(null)

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

  // 开面板与窗口变化时各夹一次。**必须在下面的 return null 之前**——早退在它
  // 后面的话，账目从有到无的那一帧 hook 数量会变，React 直接报错。
  useLayoutEffect(() => {
    if (!open) return
    const fit = () => {
      if (popRef.current) clampIntoView(popRef.current)
    }
    fit()
    window.addEventListener('resize', fit)
    return () => window.removeEventListener('resize', fit)
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
        // 右锚而不是左锚（UsageChip 用的是 left-0，别照抄）：本 chip 永远是遥测行
        // 最右的元素，向右展 288px 必然出界。2026-08-22 真机实测：left-0 时弹出层
        // 溢出它的 overflow-auto 祖先 32px，数字那一列被整列裁掉，而 DOM 里文本俱在
        // ——jsdom 测试全绿、页面上却读不到数。
        <div ref={popRef} className="absolute right-0 top-6 z-10 w-72 rounded-lg border bg-background p-3 text-xs shadow-lg">
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
