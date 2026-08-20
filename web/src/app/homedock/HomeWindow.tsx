// HomeWindow —— home 基准浮窗容器：标题栏（可拖）+ tab 条 + 内容 + 右下拉伸角。
//
// 职责：
//   - 用 geom 摆出一个 fixed 浮窗，只负责「怎么摆、怎么拖、怎么切 tab」
//   - 标题栏上有「最大化 / 还原」：铺满时忽略 geom 改用四边定位，拖动与拉伸
//     一并停掉（那两个动作在铺满状态下只会造出中间态）
//   - 拖动标题栏改位置、拉右下角改尺寸，都通过 onGeom 回报给上层
//   - 只渲染激活 tab 的内容；非激活的终端卸载 = 断开 WS 但会话继续活着，
//     文件 tab 的草稿则由 useHomeDock 寄存，切回来时由调用方恢复
//   - onKill 只向上抛 id，真的删服务端会话是调用方的事
//
// 边界：
//   - **不认识 TerminalTab**。内容由调用方用 renderTab 注入——这是可测性驱动
//     的边界：终端要 canvas/WebGL，jsdom 里跑不动，让浮窗容器脱离 PTY 单测
//     才有 HomeWindow.test.tsx 的容器用例。不是过度设计
//   - 不持有 tabs / activeId / geom 的任何状态，全部从 props 来、变化回调走
import { type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'
import { ChevronDown, FilePlus, FileText, House, Maximize2, Minimize2, Plus, TerminalSquare, X } from 'lucide-react'
import { IconMenu, type IconMenuItem } from '../lib/IconMenu'
import { topInset } from '../lib/desktopShell'
import type { HomeTab } from './useHomeDock'

export interface HomeWindowProps {
  tabs: HomeTab[]
  activeId: string | null
  geom: { x: number; y: number; w: number; h: number }
  onGeom: (g: Partial<{ x: number; y: number; w: number; h: number }>) => void
  onActivate: (id: string) => void
  onNew: () => void
  onNewFile?: () => void
  onKill: (id: string) => void // 由调用方负责真的删服务端会话
  onCollapse: () => void
  // maximized = 铺满视口。为真时忽略 geom，且拖动与拉伸都停掉（没有意义）
  maximized?: boolean
  onToggleMaximize?: () => void
  renderTab: (t: HomeTab) => ReactNode // 内容由调用方给，本组件不认识 TerminalTab
}

// tabLabel 给一个浮窗 tab 出标题：终端按序号，文件按 scratch 根下的文件名。
function tabLabel(tab: HomeTab): string {
  if (tab.kind === 'file') return tab.rel ?? '未命名'
  return tab.seq === 1 ? 'bash · home' : `bash · home ${tab.seq}`
}

export function HomeWindow({
  tabs,
  activeId,
  geom,
  onGeom,
  onActivate,
  onNew,
  onNewFile,
  onKill,
  onCollapse,
  maximized = false,
  onToggleMaximize,
  renderTab,
}: HomeWindowProps) {
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
    document.addEventListener('pointercancel', up, { once: true })
  }

  const onTitleDown = (event: ReactPointerEvent) => {
    if (maximized) return // 铺满时没有「摆哪儿」可言，拖了也只会让位置记忆对不上
    const from = { ...geom }
    grab(event, (dx, dy) => onGeom({ x: Math.max(8, from.x + dx), y: Math.max(8, from.y + dy) }))
  }

  const onCornerDown = (event: ReactPointerEvent) => {
    const from = { ...geom }
    grab(event, (dx, dy) => onGeom({ w: Math.max(360, from.w + dx), h: Math.max(200, from.h + dy) }))
  }

  const active = tabs.find((t) => t.id === activeId) ?? null

  // 铺满时用四边定位（left/top/right/bottom + width:auto）而不是 100vw/100vh：
  // vw 含滚动条宽度，会让右沿溢出一点点。顶部还要让开桌面薄壳的窗口拖动区，
  // 否则标题栏上的「还原 / 收起」两个按钮会落进那条被 AppKit 吃掉点击的区域。
  const style = maximized
    ? { left: 8, top: topInset() + 8, right: 8, bottom: 8, width: 'auto', height: 'auto' }
    : { left: geom.x, top: geom.y, width: geom.w, height: geom.h }

  // 「新建」合成一个入口：+ 弹出菜单选终端还是临时文件。
  //
  // 为什么不留两个图标：两个相邻的小图标（+ 与文件）没人分得清哪个是哪个，
  // 只能靠 hover 出 title 才知道——而它们是同一件事的两种目标，合成一个
  // 菜单后「新建什么」由文字说清楚，工具条也少一个图标
  const newItems: IconMenuItem[] = [
    {
      key: 'terminal',
      label: '新终端',
      icon: <TerminalSquare className="size-3.5" />,
      onSelect: () => onNew(),
    },
    // onNewFile 缺省 = 这台 agentd 不支持临时文件（scratch 能力探测不到），
    // 此时菜单里就不该出现这一项：置灰是在承诺「以后能用」
    ...(onNewFile ? [{
      key: 'file',
      label: '新建临时文件',
      icon: <FilePlus className="size-3.5" />,
      onSelect: () => onNewFile(),
    }] : []),
  ]

  return (
    <section
      // dark + text-foreground：本窗是一块**深色表面**，而控制台其余部分是浅色。
      // 放进来的 tab（FileTab / TerminalTab）用主题令牌，令牌不换档就仍然解析成
      // 浅色主题下的近黑色，落在这块深色底上等于隐形——走查实测：悬浮窗里新建
      // 文件，正文与背景同色，不选中根本看不见。
      //
      // **两个都要，缺一不可**：dark 只是把 --foreground 的值换成浅色，而 textarea
      // 经 Tailwind preflight 是 color:inherit、背景透明；本窗到 body 之间若没有
      // 任何一处真正写下 color: var(--foreground)，它就一路继承到 body 上那个
      // 浅色主题的近黑色。只加 dark 时实测仍是 oklch(0.196)——变量对了，颜色没变。
      className="dark fixed z-40 flex flex-col overflow-hidden rounded-[10px] border border-[#2b3542] bg-[#09111c] text-foreground shadow-2xl"
      // z-40：必须低于 Overlay 的 z-50。看板/工单弹层打开时应当盖住浮窗，否则弹层遮罩上会露出一个亮洞
      style={style}
    >
      <header
        data-testid="home-window-title"
        onPointerDown={onTitleDown}
        className={`flex h-[31px] shrink-0 select-none items-center gap-1.5 border-b border-[#2b3542] bg-[#0c1622] px-2 text-[11.5px] text-[#d7dde5] ${
          maximized ? '' : 'cursor-move'
        }`}
      >
        <span className="inline-flex items-center gap-1.5">
          <House className="size-3.5" />
          home
        </span>
        <span className="flex-1" />
        {onToggleMaximize && (
          <button
            type="button"
            aria-label={maximized ? '还原窗口' : '最大化'}
            title={maximized ? '还原窗口' : '最大化（铺满整个页面）'}
            // stopPropagation：标题栏按下即开始拖动，不拦的话点这个按钮会顺手把浮窗拖走
            onPointerDown={(e) => e.stopPropagation()}
            onClick={onToggleMaximize}
            className="inline-flex cursor-pointer rounded p-0.5 text-[#93a0b1] hover:bg-[#1a2430] hover:text-[#d7dde5]"
          >
            {maximized ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}
          </button>
        )}
        <button
          type="button"
          aria-label="收起（会话保留）"
          title="收起（会话保留）"
          onPointerDown={(e) => e.stopPropagation()}
          onClick={onCollapse}
          className="inline-flex cursor-pointer rounded p-0.5 text-[#93a0b1] hover:bg-[#1a2430] hover:text-[#d7dde5]"
        >
          <ChevronDown className="size-3.5" />
        </button>
      </header>
      <div className="flex h-[29px] shrink-0 items-stretch gap-px overflow-x-auto border-b border-[#2b3542] bg-[#0c1622] px-1.5">
        {tabs.map((tab) => {
          const label = tabLabel(tab)
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
                {tab.kind === 'file' ? <FileText className="size-3" /> : <TerminalSquare className="size-3" />}
                {label}
              </button>
              <button
                type="button"
                aria-label={`关闭 ${label}`}
                title={tab.kind === 'file' ? '关闭（文件保留在草稿区）' : '关闭并结束会话'}
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
        {/* 菜单项里必须包一层箭头：onSelect={onNew} 会把事件/参数漏给
            useHomeDock.newTerminal(machine?: string)，HomeTab.machine 存成非字符串，
            关会话时发出 ?machine=[object Object] 当场炸。TS 拦不住这类多传参 */}
        <IconMenu
          label="新建"
          dark
          icon={<Plus className="size-3.5" />}
          items={newItems}
          className="my-auto ml-0.5 inline-flex shrink-0 cursor-pointer rounded p-1 text-[#8e9bab] hover:bg-[#1a2430] hover:text-[#d7dde5]"
        />
      </div>
      <div className="min-h-0 flex-1 overflow-hidden">{active ? renderTab(active) : null}</div>
      {/* 铺满时不给拉伸角：它只会把窗口拉成一个既不是全屏又不是 geom 的中间态 */}
      {!maximized && (
        <span
          data-testid="home-window-corner"
          onPointerDown={onCornerDown}
          aria-hidden="true"
          className="absolute right-0 bottom-0 size-[15px] cursor-nwse-resize"
          style={{ background: 'linear-gradient(135deg, transparent 50%, #2b3542 50%)' }}
        />
      )}
    </section>
  )
}
