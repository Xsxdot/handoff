// HomeWindow —— home 基准终端的浮窗容器：标题栏（可拖）+ tab 条 + 内容 + 右下拉伸角。
//
// 职责：
//   - 用 geom 摆出一个 fixed 浮窗，只负责「怎么摆、怎么拖、怎么切 tab」
//   - 拖动标题栏改位置、拉右下角改尺寸，都通过 onGeom 回报给上层
//   - 只渲染激活 tab 的内容；非激活的终端卸载 = 断开 WS 但会话继续活着，
//     切回来时 TerminalTab 会拿同一个 sessionId 重连（既有事实，本组件不做
//     任何会话侧处理）
//   - onKill 只向上抛 id，真的删服务端会话是调用方的事
//
// 边界：
//   - **不认识 TerminalTab**。内容由调用方用 renderTab 注入——这是可测性驱动
//     的边界：终端要 canvas/WebGL，jsdom 里跑不动，让浮窗容器脱离 PTY 单测
//     才有 HomeWindow.test.tsx 那五个用例。不是过度设计
//   - 不持有 tabs / activeId / geom 的任何状态，全部从 props 来、变化回调走
import { type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'
import { ChevronDown, House, Plus, TerminalSquare, X } from 'lucide-react'
import type { HomeTab } from './useHomeDock'

export interface HomeWindowProps {
  tabs: HomeTab[]
  activeId: string | null
  geom: { x: number; y: number; w: number; h: number }
  onGeom: (g: Partial<{ x: number; y: number; w: number; h: number }>) => void
  onActivate: (id: string) => void
  onNew: () => void
  onKill: (id: string) => void // 由调用方负责真的删服务端会话
  onCollapse: () => void
  renderTab: (t: HomeTab) => ReactNode // 内容由调用方给，本组件不认识 TerminalTab
}

function tabLabel(seq: number): string {
  return seq === 1 ? 'bash · home' : `bash · home ${seq}`
}

export function HomeWindow({ tabs, activeId, geom, onGeom, onActivate, onNew, onKill, onCollapse, renderTab }: HomeWindowProps) {
  // grab —— 拖动 / 拉伸共用的指针会话：按下记起点，移动算增量，抬起一次性解绑。
  const grab = (event: ReactPointerEvent, apply: (dx: number, dy: number) => void) => {
    event.preventDefault()
    const sx = event.clientX
    const sy = event.clientY
    const move = (e: PointerEvent) => apply(e.clientX - sx, e.clientY - sy)
    const up = () => document.removeEventListener('pointermove', move)
    // 监听挂在 document 上而不是元素上：指针拖出窗口时元素收不到 move，窗口会卡在半路
    document.addEventListener('pointermove', move)
    document.addEventListener('pointerup', up, { once: true })
  }

  const onTitleDown = (event: ReactPointerEvent) => {
    const from = { ...geom }
    grab(event, (dx, dy) => onGeom({ x: Math.max(8, from.x + dx), y: Math.max(8, from.y + dy) }))
  }

  const onCornerDown = (event: ReactPointerEvent) => {
    const from = { ...geom }
    grab(event, (dx, dy) => onGeom({ w: Math.max(360, from.w + dx), h: Math.max(200, from.h + dy) }))
  }

  const active = tabs.find((t) => t.id === activeId) ?? null

  return (
    <section
      className="fixed z-40 flex flex-col overflow-hidden rounded-[10px] border border-[#2b3542] bg-[#09111c] shadow-2xl"
      // z-40：必须低于 Overlay 的 z-50。看板/工单弹层打开时应当盖住浮窗，否则弹层遮罩上会露出一个亮洞
      style={{ left: geom.x, top: geom.y, width: geom.w, height: geom.h }}
    >
      <header
        data-testid="home-window-title"
        onPointerDown={onTitleDown}
        className="flex h-[31px] shrink-0 cursor-move select-none items-center gap-1.5 border-b border-[#2b3542] bg-[#0c1622] px-2 text-[11.5px] text-[#d7dde5]"
      >
        <span className="inline-flex items-center gap-1.5">
          <House className="size-3.5" />
          home
        </span>
        <span className="flex-1" />
        <button
          type="button"
          aria-label="收起（会话保留）"
          title="收起（会话保留）"
          onClick={onCollapse}
          className="inline-flex cursor-pointer rounded p-0.5 text-[#93a0b1] hover:bg-[#1a2430] hover:text-[#d7dde5]"
        >
          <ChevronDown className="size-3.5" />
        </button>
      </header>
      <div className="flex h-[29px] shrink-0 items-stretch gap-px overflow-x-auto border-b border-[#2b3542] bg-[#0c1622] px-1.5">
        {tabs.map((tab) => {
          const label = tabLabel(tab.seq)
          const isActive = tab.id === activeId
          return (
            <span
              key={tab.id}
              className={`inline-flex items-center rounded-t-md ${
                isActive ? 'bg-[#09111c] text-[#d7dde5]' : 'text-[#8e9bab]'
              }`}
            >
              <button
                type="button"
                onClick={() => onActivate(tab.id)}
                className="inline-flex cursor-pointer items-center gap-1 px-2 py-0.5 font-mono text-[10.5px] whitespace-nowrap hover:bg-[#1a2430]"
              >
                <TerminalSquare className="size-3" />
                {label}
              </button>
              <button
                type="button"
                aria-label={`关闭 ${label}`}
                title="关闭并结束会话"
                onClick={(e) => {
                  // stopPropagation：不加的话点 × 会连带激活这个 tab，于是"关闭"变成"先切过去再关掉"，看起来像闪了一下
                  e.stopPropagation()
                  onKill(tab.id)
                }}
                className="inline-flex cursor-pointer px-1.5 py-0.5 text-inherit opacity-55 hover:opacity-100 hover:text-[#df554f]"
              >
                <X className="size-3" />
              </button>
            </span>
          )
        })}
        <button
          type="button"
          aria-label="新终端"
          title="新终端"
          onClick={onNew}
          className="my-auto ml-0.5 inline-flex shrink-0 cursor-pointer rounded p-1 text-[#8e9bab] hover:bg-[#1a2430] hover:text-[#d7dde5]"
        >
          <Plus className="size-3.5" />
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-hidden">{active ? renderTab(active) : null}</div>
      <span
        data-testid="home-window-corner"
        onPointerDown={onCornerDown}
        aria-hidden="true"
        className="absolute right-0 bottom-0 size-[15px] cursor-nwse-resize"
        style={{ background: 'linear-gradient(135deg, transparent 50%, #2b3542 50%)' }}
      />
    </section>
  )
}
