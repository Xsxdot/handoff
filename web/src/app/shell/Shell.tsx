// Shell —— 控制台的三段外框：顶部 tab 条 + 常驻左栏项目树 + 中央内容区。
//
// 职责：
//   - 提供三条路由共用的外框，内容区用 <Outlet> 承载
//   - 持有跨页共享的两条数据流（任务流、项目树流）并下发，避免每页各拉一份
//     （Task 6 挂上树后实现）
//
// 边界：
//   - 不渲染任何未实现功能的入口（左栏齿轮、设置页、配对开发机）——
//     置灰控件承诺"以后能用"，用户会反复点；缺一个按钮反而诚实（spec §0）
//   - 不持有机器流：那只在 /machines 可见时开表（spec §6）
import { Outlet } from 'react-router-dom'
import { TopTabs } from './TopTabs'

export function Shell() {
  return (
    <div className="grid h-dvh grid-cols-[260px_1fr] grid-rows-[auto_1fr] bg-background">
      <div className="col-span-2 border-b bg-background">
        <TopTabs />
      </div>
      <aside role="complementary" className="min-h-0 overflow-y-auto border-r bg-sidebar">
        {/* 左栏项目树：Task 6 挂入 */}
      </aside>
      <main className="min-h-0 overflow-y-auto bg-muted/40">
        <Outlet />
      </main>
    </div>
  )
}
