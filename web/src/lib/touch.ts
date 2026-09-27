// touch —— compact 触点基线类（B369.8 §3.3）。
//
// TOUCH_BASELINE 挂在 compact 档五个面根节点（CardsPage main / CardDrawer aside /
// SessionTab 根 / ProjectTree 根 / SettingsHub 根）的一行类，把全部行内钮/输入/
// 下拉抬到 24×24 底线（WCAG 2.2 AA 2.5.8 次级目标）；主动作 ≥44 仍逐枚点名
// min-h-11（plan §3.3 的两档分工）。
//
// 为什么带 :not(.min-h-11) 排除：基线是后代选择器（特异度 0,1,1），会压过元素
// 自身上的 .min-h-11（0,1,0）；且 Tailwind v4 把任意变体规则排在 utilities 层
// 末尾，改用 :where 降特异度后仍因源序更晚而赢（v4.3.3 编译产物实测）。
// 排除法让 44px 主动作完全退出基线的 min-height 竞争：带 min-h-11 的控件拿 44，
// 其余拿 24 底线，两档各得其所、不依赖源序。
export const TOUCH_BASELINE = [
  '[&_button:not(.min-h-11)]:min-h-6',
  '[&_button]:min-w-6',
  '[&_input:not(.min-h-11)]:min-h-6',
  '[&_select:not(.min-h-11)]:min-h-6',
].join(' ')
