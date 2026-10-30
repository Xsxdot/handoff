// sidebarResize —— 左侧导航栏宽度的约束与持久化常量。
//
// 职责：
//   - 集中维护左栏默认值、可调范围和键盘步长
//   - 提供无副作用的宽度夹紧函数，供交互组件与测试共用
//
// 边界：
//   - 不读取浏览器状态，不渲染组件，也不处理拖拽事件
//   - 不参与中央工作区窗格的宽度计算

// Dense approved desktop defaults to 250px; retain the v2 key so explicit user resizing survives.
export const DEFAULT_SIDEBAR_WIDTH = 250
export const MIN_SIDEBAR_WIDTH = 220
export const MAX_SIDEBAR_WIDTH = 560
export const SIDEBAR_WIDTH_KEY = 'handoff.shell.sidebarWidth.v2'
export const SIDEBAR_KEYBOARD_STEP = 16

export function clampSidebarWidth(width: number): number {
  return Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, Math.round(width)))
}
