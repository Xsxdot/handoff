// sessionDetailOpener —— 会话详情开启注册表（走查 09-17 #3）：⋯ 按钮住在窗格
// 标题行（WorkbenchPage 渲染），抽屉开关状态住在 SessionTab——两者跨层投递。
// 按 sessionId 精确投递（会话 tab 全局按 session:${sessionId} 去重，同时最多
// 一个实例；tab 重建时后注册覆盖先注册，注销比对身份不带走后来者）。
// 无 React 依赖，WorkbenchPage ↔ SessionTab 双向引用不断环。
type DetailOpener = () => void

const openers = new Map<string, DetailOpener>()

export function registerSessionDetailOpener(sessionId: string, open: DetailOpener): () => void {
  openers.set(sessionId, open)
  return () => {
    if (openers.get(sessionId) === open) openers.delete(sessionId)
  }
}

export function openSessionDetail(sessionId: string): void {
  openers.get(sessionId)?.()
}
