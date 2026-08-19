// bindingRow —— 「按 executor 逐行配一项」那类块的共用行布局（执行纪律 / Env 文件）。
//
// 职责：单一来源地定下名字列的宽度，让同一台机器上下两个块的下拉框左边缘
//       落在同一条竖线上。
// 边界：只出类名，不出组件——两个块的控件、选项与文案都不同，共用的只有这条
//       对齐契约。谁再加同形状的块，用这两个常量而不是自己抄一份类名。
//
// 为什么不能用 `max-content`：每一行是一个独立的 `<label>` grid，`max-content`
// 因此按**行**解析。结果是 `opencode` 那行的下拉框比 `fake` 那行往右缩一截，
// 两个块之间更是各自为政——这正是它看着难受的原因。固定列宽是让「跨行 + 跨块」
// 都对齐的唯一办法（同一个 grid 只能管住块内，管不到另一个块）。
//
// 5rem 容得下 opencode（text-xs 下约 55px）。更长的自定义 executor 名会换行而
// 不是把列撑开：行高变大，但对齐不破。

// BINDING_ROW 是一行的容器类：固定名字列 + 自适应控件列。
export const BINDING_ROW = 'grid grid-cols-[5rem_minmax(0,1fr)] items-center gap-3 text-xs'

// BINDING_LABEL 是名字列的类。break-words 让超长名字换行而不是溢出到控件上。
export const BINDING_LABEL = 'font-medium break-words'
