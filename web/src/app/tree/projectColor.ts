// projectColor —— 项目身份色的取色规则。
//
// 为什么是哈希而不是后端字段：真实 API 的 ProjectNode 上没有颜色字段
// （原型的 project.color 是 mock 数据）。为了配色去加一个后端字段 +
// 存储 + 设置 UI 不划算，所以由前端从固定调色板里按项目 id 取一色。
//
// 边界：
//   - **绝不能按数组下标取色**。下标取色时，在列表头部插入一个新项目会让
//     后面所有项目集体换色——用户只会当成 bug。所以取色必须只依赖 id 本身
//   - 不保证不撞色。项目数超过调色板容量必然有重复，这是可接受的：
//     色是辅助识别，不是唯一标识，项目名才是

// CLASSES 必须是**字面量**数组，不能用模板字符串拼类名。
// 两个理由，缺一不可：
//   1. Tailwind v4 按需产出——拼出来的类名构建器扫不到，产物 CSS 里根本没有
//      这条规则，颜色会**静默失效**（不报错，就是没颜色）
//   2. 顺手消掉越界：下标只能落在数组内
// 改这里必须同步改 index.css 的 --project-N，两边组数要对齐。
const CLASSES = [
  'text-project-1',
  'text-project-2',
  'text-project-3',
  'text-project-4',
  'text-project-5',
] as const

// PROJECT_COLOR_COUNT 是调色板容量，供测试与调用方判断。
export const PROJECT_COLOR_COUNT = CLASSES.length

// hash 用 FNV-1a：短、无依赖、对短字符串分布够用。
// 取 >>> 0 是为了让结果落在无符号 32 位，避免负数取模得到负下标。
function hash(input: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

// projectColorClass 返回项目行图标该用的文字色类名。
// 参数：projectId —— 项目的稳定标识（不是名字，改名不该换色）
// 返回：形如 'text-project-3' 的 Tailwind 类名
export function projectColorClass(projectId: string): string {
  return CLASSES[hash(projectId) % CLASSES.length]
}
