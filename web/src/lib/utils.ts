// 前端通用工具。
//
// 职责：
//   - cn：合并 className，后者的类名覆盖前者（tailwind-merge 去冲突）
//
// 边界：
//   - 不放置业务逻辑；纯工具函数
import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

// cn 把多个 className（含条件类名）合并成一个去冲突后的类名字符串。
//
// 参数：
//   - inputs: 任意数量的类名或假值（false/null/undefined 被丢弃）
//
// 返回：
//   - 合并后的类名串；供 shadcn/ui 组件统一处理变体与外部类名
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
