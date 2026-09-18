// MobileTabBar —— 紧凑视口（phone/pad）下的底栏四个一级 tab（spec 实现决定）。
//
// 职责：渲染「会话 | 卡 | 项目 | 设置」四个切换钮与可选角标；选择动作经 onSelect 上抛。
// 边界：
//   - 不取数、不路由、不判断视口：视口判定归 useShellViewport，内容渲染归 Shell
//   - P1=A：它与桌面三栏是同一份 Shell 的条件渲染产物，不新开 /m 路由树；
//     本组件自身不持有任何页面状态
import { FolderGit2, MessagesSquare, Settings, SquareKanban, type LucideIcon } from 'lucide-react'

export type MobileTab = 'sessions' | 'cards' | 'projects' | 'settings'

const TABS: { key: MobileTab; label: string; Icon: LucideIcon }[] = [
  { key: 'sessions', label: '会话', Icon: MessagesSquare },
  { key: 'cards', label: '卡', Icon: SquareKanban },
  { key: 'projects', label: '项目', Icon: FolderGit2 },
  { key: 'settings', label: '设置', Icon: Settings },
]

export interface MobileTabBarProps {
  active: MobileTab
  onSelect: (tab: MobileTab) => void
  // needsCount 卡 tab 的「需要你」数；unread 会话 tab 未读聚合。两者 0 时不渲染角标。
  needsCount?: number
  unread?: number
}

export function MobileTabBar({ active, onSelect, needsCount = 0, unread = 0 }: MobileTabBarProps) {
  return (
    <nav
      data-testid="mobile-tabbar"
      aria-label="移动底栏导航"
      // pb-[env(safe-area-inset-bottom)]：iOS 底部安全区；桌面浏览器求值为 0。
      className="flex shrink-0 items-stretch border-t bg-background pb-[env(safe-area-inset-bottom)]"
    >
      {TABS.map(({ key, label, Icon }) => {
        const badge = key === 'sessions' ? unread : key === 'cards' ? needsCount : 0
        return (
          <button
            key={key}
            type="button"
            role="tab"
            aria-selected={active === key}
            data-testid={`mobile-tab-${key}`}
            onClick={() => onSelect(key)}
            className={`relative flex flex-1 flex-col items-center gap-0.5 py-2 text-[11px] ${active === key ? 'font-semibold text-foreground' : 'text-muted-foreground'}`}
          >
            <Icon className="size-5" />
            <span>{label}</span>
            {badge > 0 && (
              <span className="absolute right-4 top-1 min-w-4 rounded-full bg-red-500 px-1 text-center text-[10px] leading-4 text-white">
                {badge}
              </span>
            )}
          </button>
        )
      })}
    </nav>
  )
}
