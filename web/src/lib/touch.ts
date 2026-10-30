// touch —— compact 触点基线类（B369.8 §3.3）。
//
// TOUCH_BASELINE 挂在 compact 档五个面根节点（CardsPage main / CardDrawer aside /
// SessionTab 根 / ProjectTree 根 / SettingsHub 根）的一行类，把全部行内钮/输入/
// 下拉抬到 24×24 底线（WCAG 2.2 AA 2.5.8 次级目标）；主动作 ≥44 仍逐枚点名
// min-h-11（plan §3.3 的两档分工）。
//
// 规则放在 index.css 的 components 层；明确的尺寸 utilities 始终优先。
// 同层后代规则会压过任意 min-h-[…]，仅排除 min-h-11 会把更高的设置行压回24px。
export const TOUCH_BASELINE = 'touch-baseline'
