// BlankTab —— 空白 tab 的种类选择面板（spec §2.2.1）。
//
// 职责：把「只有三种 tab」这条规则做成用户看得见的形态——新开一个 tab 默认
// 空白，中间列出可选种类，选中后该 tab 才变成对应内容。
//
// 边界：
//   - 不自己开 tab，只回调选中的种类。是 setContent（原地变）还是 open（新开），
//     由调用方决定
//   - 不做目标选择（选哪个文件、哪个任务）。那是选中种类之后的第二步，
//     由 WorkbenchPage 接管
//
// 为什么中央区在没有 tab 时也渲染它：中央区域一块死掉的空白会让人以为
// 「这里还没做好」，而它其实是整个工作台的起点。
import { useEffect, useRef } from 'react'
import { Bot, FilePlus, TerminalSquare } from 'lucide-react'
import type { BaseDir } from './useWorkbench'

// PickKind 是用户能选的三种 tab。与 TabContent 的三种正式种类一一对应。
export type PickKind = 'terminal' | 'newfile' | 'tui'

// PICK_ITEMS 是选择面板的三项。顺序与原型/Orca 一致：终端在最上（最常用）。
export const PICK_ITEMS: { kind: PickKind; label: string; hotkey: string; icon: typeof TerminalSquare }[] = [
  { kind: 'terminal', label: '新终端', hotkey: '⌘T', icon: TerminalSquare },
  { kind: 'newfile', label: '新建文件', hotkey: '⌘N', icon: FilePlus },
  { kind: 'tui', label: '打开任务', hotkey: '⌘⇧A', icon: Bot },
]

// LauncherItem 是面板上的一条启动项。
//
// 与 PICK_ITEMS 的内置项刻意不合并成一个数组：内置种类是闭集（PickKind），
// 启动项是一张会长的列表，两个轴正交。合并会让 PickKind 变成开集，快捷键
// 穷举与种类过滤都得开始解析字符串前缀。
export interface LauncherItem {
  name: string
  envMissing: boolean
}

export interface BlankTabProps {
  base: BaseDir
  onPick: (k: PickKind) => void
  // launchers 是当前基准目录所在机器的启动项。空数组也表示这台机器没有可展示的项，
  // 能力位的判断由 Shell 做，面板只消费已经过滤好的展示态。
  launchers?: LauncherItem[]
  onPickLauncher?: (name: string) => void
  // terminalUnavailable 非空 = 这台机器不能开终端，附带原因原文。
  // 此时**不渲染**终端项，改在面板底部说一句实话——置灰控件承诺「以后能用」，
  // 用户会反复点它（W3b §0 既有纪律）。
  terminalUnavailable?: string
}

// hotkeyOf 把一次按键映射成一种 tab。返回 null = 与本面板无关，交给浏览器。
//
// 为什么用 metaKey 而不区分平台：控制台目前只在 macOS 的桌面壳里用，面板上印的
// 也是 ⌘。将来上 Windows 时这里要一起改成 metaKey || ctrlKey，那时面板文案也得改，
// 两处必须同时动——所以刻意不在这里提前做半套。
function hotkeyOf(e: React.KeyboardEvent): PickKind | null {
  if (!e.metaKey) return null
  const k = e.key.toLowerCase()
  if (k === 't' && !e.shiftKey) return 'terminal'
  if (k === 'n' && !e.shiftKey) return 'newfile'
  if (k === 'a' && e.shiftKey) return 'tui'
  return null
}

// pickItemsFor 过滤出某个基准目录下真正能选的种类。
//
// 两条过滤叠加：home 基准只留终端（spec §2.6）——scratch 从不会被选中、也不会
// 渲染 BlankTab，因而不需要第三套入口；终端不可用时把它摘掉（**不置灰**：置灰
// 是在承诺「以后能用」，用户会反复点它，W3b §0 既有纪律）。
//
// 导出是为了让 tab 条上的 + 菜单与本面板用同一份判断——两处分别写就会出现
// 「面板里没有终端、+ 菜单里却有」。
export function pickItemsFor(base: BaseDir, terminalUnavailable?: string) {
  return (base.kind === 'home' ? PICK_ITEMS.filter((i) => i.kind === 'terminal') : PICK_ITEMS).filter(
    (i) => i.kind !== 'terminal' || !terminalUnavailable,
  )
}

// launchersFor 过滤出某个基准目录下能展示的启动项。
//
// 启动项开出来的就是终端，所以终端不可用时一条都不展示，不置灰。
// 与 pickItemsFor 不同，这里不按 base.kind 过滤：home 下的启动项与终端同类，照留。
// 导出让 + 菜单与空白面板共用同一份判断。
export function launchersFor(launchers: LauncherItem[] | undefined, terminalUnavailable?: string): LauncherItem[] {
  if (terminalUnavailable) return []
  return launchers ?? []
}

export function BlankTab({ base, onPick, launchers, onPickLauncher, terminalUnavailable }: BlankTabProps) {
  const items = pickItemsFor(base, terminalUnavailable)
  const launcherItems = launchersFor(launchers, terminalUnavailable)

  // 挂载即聚焦。**不能用 `autoFocus`**：React 只对表单元素实现它，写在普通 div 上
  // 只会落成一个 `autofocus` 属性，而该属性对动态插入的非表单元素不生效——走查里
  // 实测面板开出来后 activeElement 仍是 body，于是印在面板上的 ⌘T 按下去没反应。
  // 键盘处理本身是对的（手动聚焦后 ⌘T 正常开终端），缺的只是这一次 focus()。
  //
  const panelRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    panelRef.current?.focus()
  }, [])

  // 快捷键接在**这个面板自己身上**（容器可聚焦 + 挂载自动聚焦），不是 window 级监听。
  // 理由：分屏时可能有两个空白面板同时在屏上，window 级监听会让一次 ⌘T 开出两个终端；
  // 而印在面板上的提示如果按了没反应，就是一句 UI 说了不算的话（与「不置灰」同源）。
  const onKeyDown = (e: React.KeyboardEvent) => {
    const kind = hotkeyOf(e)
    if (kind === null) return
    // home 基准下只有终端可用，别让隐藏项被快捷键绕过
    if (!items.some((i) => i.kind === kind)) return
    e.preventDefault()
    onPick(kind)
  }
  return (
    // tabIndex={-1} 让容器能被上面那次 focus() 聚焦但不进 Tab 序：快捷键必须挂在
    // 面板自己身上（见 onKeyDown 的说明），否则分屏时一次 ⌘T 会开出两个终端。
    // 项目未装 jsx-a11y，无对应 role 会触发此规则，故不需要 disable 注释。
    <div
      ref={panelRef}
      tabIndex={-1}
      onKeyDown={onKeyDown}
      className="flex h-full flex-col items-center justify-center gap-4 p-8 outline-none"
    >
      <p className="text-xs text-muted-foreground">
        基准目录 <span className="font-mono text-foreground">{base.label}</span>
        {/* scratch 只服务浮窗 file tab，不会成为当前选中的 BlankTab 基准。 */}
        {base.kind === 'home' && '（不挂在任何项目上）'}
      </p>
      <ul className="flex w-full max-w-xs flex-col gap-1">
        {items.map((item) => (
          <li key={item.kind}>
            <button
              type="button"
              onClick={() => onPick(item.kind)}
              className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm hover:bg-accent"
            >
              <item.icon className="size-4 shrink-0 text-muted-foreground" />
              <span className="flex-1">{item.label}</span>
              <span className="font-mono text-[11px] text-muted-foreground">{item.hotkey}</span>
            </button>
          </li>
        ))}
      </ul>
      {launcherItems.length > 0 && (
        <ul aria-label="自定义启动项" className="flex w-full max-w-xs flex-col gap-1 border-t pt-3">
          {launcherItems.map((item) => (
            <li key={item.name}>
              <button
                type="button"
                onClick={() => onPickLauncher?.(item.name)}
                className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm hover:bg-accent"
              >
                <TerminalSquare className="size-4 shrink-0 text-muted-foreground" />
                <span className="flex-1">{item.name}</span>
                {item.envMissing && (
                  <span className="text-[11px] text-destructive">env 文件缺失</span>
                )}
                {/* 启动项不分配快捷键：数量不定，印在面板上却按不动的快捷键会变成一句 UI 说了不算的话。 */}
              </button>
            </li>
          ))}
        </ul>
      )}
      {terminalUnavailable && (
        <p className="max-w-xs text-center text-xs text-muted-foreground">{terminalUnavailable}</p>
      )}
    </div>
  )
}
