// StateDot / TaskState —— 任务状态的行内呈现（圆点 + 文字）。
//
// 职责：
//   - StateDot：把 StateTone 渲染成一个 7px 的实色圆点
//   - TaskState：圆点 + 状态文案，干预态与失败态额外染文字色
//
// 边界：
//   - 不查任何状态语义，全部委托 columns.ts 的 stateTone / stateLabel；
//     这里只负责「基调 → 类名」这一层映射
//   - 不是通用 UI 原语，所以不放 components/ui/：它消费 columns.ts 的领域
//     映射。放在 app/board/ 与 columns.ts 同居，跨目录消费的先例是
//     TaskHeader.tsx 的 `import ... from '../board/columns'`
//   - 不管布局：外边距、排列由调用方给
//
// 形态基准：prototypes/desktop-console 的 .status-dot（7px 圆点）与
// .task-state.attention（文字色 #a66c09）。看板卡片刻意不用填充胶囊
// Badge——密集列表里胶囊的视觉噪声太高（spec §1.1、§3.1）。
import { type StateTone, stateLabel, stateTone } from './columns'
import { cn } from '@/lib/utils'

// DOT_CLASS 是基调到圆点样式的映射。
//
// 四种「填充状态」把生命周期排成一条线，一眼能读出走到哪了：
//   idle（未开始）  空心圈——还没填上
//   active（进行中）实心绿
//   intervention   实心琥珀
//   done（已完成）  实心灰——事情办完了，不该再抢眼
//   failed         实心红
//
// 原型里 done 与 active 共用绿色，靠所在列与文案区分。在看板上成立，在**左栏
// 树**里不成立：那里没有列、任务名旁边只有一个点，进行中与已完成长得一模一样。
// 改灰之后两者当场分得开，而绿色仍然只属于「此刻真的在跑」。
const DOT_CLASS: Record<StateTone, string> = {
  idle: 'border-[1.5px] border-muted-foreground/50',
  active: 'bg-state-active',
  intervention: 'bg-state-intervention',
  done: 'bg-muted-foreground/45',
  failed: 'bg-state-failed',
}

// TEXT_CLASS 是基调到文字色的映射。
// 只有 intervention 与 failed 染色——全都染色等于都不染色，原型里
// 其余状态的文案一律是次要灰。
const TEXT_CLASS: Record<StateTone, string> = {
  idle: 'text-muted-foreground',
  active: 'text-muted-foreground',
  intervention: 'text-state-intervention-text',
  done: 'text-muted-foreground',
  failed: 'text-state-failed',
}

// StateDot 渲染一个状态圆点。
//
// 参数：
//   - tone: 视觉基调，由 columns.ts 的 stateTone 得出
//
// 返回：
//   - 一个 7px 的圆形 span，aria-hidden（它是纯装饰，语义由相邻文字承载）
export function StateDot({ tone }: { tone: StateTone }) {
  return (
    <span
      aria-hidden
      className={cn('inline-block size-[7px] shrink-0 rounded-full', DOT_CLASS[tone])}
    />
  )
}

// TaskState 渲染「圆点 + 状态文案」。
//
// 参数：
//   - state: 任务状态机的状态字符串，未知状态原样透出（不吞数据）
//
// 返回：
//   - 一个 inline-flex 的 span，含圆点与文案
export function TaskState({ state }: { state: string }) {
  const tone = stateTone(state)
  return (
    <span className={cn('inline-flex items-center gap-1.5 text-xs', TEXT_CLASS[tone])}>
      <StateDot tone={tone} />
      {stateLabel(state)}
    </span>
  )
}
