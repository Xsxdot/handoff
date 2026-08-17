// RowCounts —— 左栏树里每一行右端的计数。
//
// 形态基准：原型的 SummaryCounts（prototypes/desktop-console/src/App.jsx:247）
// ——三段「图标 + 数字」，等宽字体、次要灰、段间 gap 7px。
//
// 边界：
//   - 只负责呈现。数字怎么算是 counts.ts 的事，这里不查任何任务状态
//   - 不接受预拼好的字符串。改成结构化入参正是本次修复的核心：裸 `2·0·0`
//     没有任何东西说明三个数字各自是什么
import { Activity, Folders, TriangleAlert } from 'lucide-react'

export interface RowCountsProps {
  // dirs 省略 = 这一行没有「目录数」这个概念（目录行本身就是目录，不嵌套统计）。
  // 注意与 dirs={0} 区分：0 是「有这个概念，值为零」，要照常渲染。
  dirs?: number
  running: number
  pending: number
}

export function RowCounts({ dirs, running, pending }: RowCountsProps) {
  return (
    <span className="ml-auto flex shrink-0 items-center gap-[7px] font-mono text-[9.5px] tabular-nums text-muted-foreground">
      {/* 目录数 0 也照常显示：它是这一行的固有属性（「这里有几个开发目录」），
          恒在才能横向比较 */}
      {dirs !== undefined && (
        <span title="开发目录" className="flex items-center gap-0.5">
          <Folders className="size-3" />
          {dirs}
        </span>
      )}
      {/* 运行中 / 需要处理为 0 时整段不渲染。这两个数是**信号**不是属性：
          一屏几十行清一色的 `0 0` 会把真有事的那几行淹掉，有数字才亮才抓得住眼 */}
      {running > 0 && (
        <span title="运行中的 handoff" className="flex items-center gap-0.5">
          <Activity className="size-3" />
          {running}
        </span>
      )}
      {pending > 0 && (
        <span title="需要处理" className="flex items-center gap-0.5">
          <TriangleAlert className="size-3" />
          {pending}
        </span>
      )}
    </span>
  )
}
