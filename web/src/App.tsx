// 控制台入口：路由骨架。
//
// W4 起中央不再是「一个路由一个页面」，而是一套 tab 工作台。路由只剩三件事：
//
//   /          工作台（三栏：导航树 / tab 工作台 / 文件树）
//   /settings  三栏不变，中央整页换成设置页（spec §6：设置不是弹层）
//   /tasks/:id 深链承接：选中任务所在目录 + 开它的 TUI tab + 换回 /
//   /machines  重定向到 /settings（开发机页收进设置页，spec §8.4）
//
// 具体的路由分发在 Shell 内部（它要在三栏布局里只替换中央那一块），这里只负责
// 把整棵树交给 Shell 并套上 Router。
//
// 用 BrowserRouter（vite dev server 自带 history fallback）；W5 把前端 embed 进
// agentd 时，agentd 需要对未知路径回落 index.html——已记入 web/README.md。
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { Shell } from './app/shell/Shell'

// AppRoutes 单独导出，供测试用 MemoryRouter 指定初始路径。
export function AppRoutes() {
  return (
    <Routes>
      <Route path="*" element={<Shell />} />
    </Routes>
  )
}

function App() {
  return (
    <BrowserRouter>
      <AppRoutes />
    </BrowserRouter>
  )
}

export default App
