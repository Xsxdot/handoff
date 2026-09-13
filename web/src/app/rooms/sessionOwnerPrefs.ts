// sessionOwnerPrefs —— 新建会话 owner 记忆（B358.8 #7）：localStorage 读写 +
// 两个容错函数（独立模块仿 treePrefs.ts 先例，组件文件不混导出工具函数）。
//
// 边界：不认识 React、不认识对话框；键值就是上次成功创建的 owner 统一记法
// （user:<name>/agent:<name>）。控制台无「当前登录用户」概念（web:<host> 是机器
// 位不配当 owner，B358.4 拍板①），记忆方案是有意的落点：首次使用仍需输一次。
// 隐私模式等 storage 不可用时读退空串、写静默失败——记忆是体验优化不是功能依赖。

export const LAST_SESSION_OWNER_KEY = 'handoff.last-session-owner'

export function loadLastSessionOwner(): string {
  try {
    const value = window.localStorage.getItem(LAST_SESSION_OWNER_KEY)
    return typeof value === 'string' ? value : ''
  } catch {
    return ''
  }
}

export function saveLastSessionOwner(owner: string): void {
  try {
    window.localStorage.setItem(LAST_SESSION_OWNER_KEY, owner)
  } catch {
    // 写失败不阻塞创建流程
  }
}
