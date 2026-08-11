// 控制台入口：路由骨架。
//
//   /          任务看板（顶层）：四列看板 + 轮询
//   /tasks/:id 任务详情 / 审核台：可深链
//
// 用 BrowserRouter（vite dev server 自带 history fallback，开发期没问题）；
// W5 把前端 embed 进 agentd 时，agentd 需要对未知路径回落 index.html——已记入
// web/README.md 的已知缺口，本轮不实现。
//
// 旧的 W1 冒烟组件（StatusSection / TaskSection / EventSection）已完成历史使命，
// 随本路由骨架一起删除。
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { BoardPage } from './app/board/BoardPage'
import { TaskPage } from './app/task/TaskPage'

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<BoardPage />} />
        <Route path="/tasks/:id" element={<TaskPage />} />
      </Routes>
    </BrowserRouter>
  )
}

export default App
