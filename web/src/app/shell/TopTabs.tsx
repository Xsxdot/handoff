// TopTabs —— 顶部 tab 条：应用标记 + 两个导航入口。
//
// 选中规则：/ 与 /tasks/* 高亮「任务看板」，/machines 高亮「开发机」。
// 为什么不用 NavLink：NavLink 对 to="/" 默认按前缀匹配，/machines 也会命中
// 「任务看板」；加 end 后 /tasks/:id 又不高亮。手算选中态最诚实。
import type { ReactNode } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { cn } from '@/lib/utils'

export function TopTabs() {
  const { pathname } = useLocation()
  const boardActive = pathname === '/' || pathname.startsWith('/tasks/')
  const machinesActive = pathname.startsWith('/machines')
  return (
    <nav aria-label="主导航" className="flex items-center gap-1 px-3">
      <div className="mr-4 flex items-center gap-2">
        <span className="flex size-7 items-center justify-center rounded-md bg-[#10151b] text-xs font-semibold text-white">h</span>
        <span className="text-sm font-semibold">handoff 控制台</span>
      </div>
      <TabLink to="/" active={boardActive}>任务看板</TabLink>
      <TabLink to="/machines" active={machinesActive}>开发机</TabLink>
    </nav>
  )
}

function TabLink({ to, active, children }: { to: string; active: boolean; children: ReactNode }) {
  return (
    <Link
      to={to}
      aria-current={active ? 'page' : undefined}
      className={cn(
        'inline-flex items-center border-b-2 px-3 py-2.5 text-sm transition-colors',
        active ? 'border-foreground font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground',
      )}
    >
      {children}
    </Link>
  )
}
