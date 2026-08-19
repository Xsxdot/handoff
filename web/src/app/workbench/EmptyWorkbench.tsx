// EmptyWorkbench —— 一个目录都没选中时的全局空态（spec §2.2.1）。
//
// 职责：给出下一步该做什么，而不是一块空白。
// 边界：不渲染任何要求「先选目录」才有意义的入口。
import { Plus } from 'lucide-react'

export function EmptyWorkbench({ onAddProject }: { onAddProject: () => void }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-8 text-center">
      {/* 用品牌标志而不是首字母方块：空态是产品第一眼，字母 h 看起来像占位。 */}
      <img
        src="/handoff-mark.svg"
        alt="handoff"
        className="size-16"
      />
      <p className="text-sm text-muted-foreground">从侧边栏选择一个目录开始</p>
      <button
        type="button"
        onClick={onAddProject}
        className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm hover:bg-accent"
      >
        <Plus className="size-4" />
        添加项目
      </button>
      {/* 这里的快捷键是「选中目录之后能用什么」的预告：没有基准目录时它们无处可去，
          所以写成「选中目录后」，而不是印一行按了没反应的键 */}
      <p className="font-mono text-[11px] text-muted-foreground">
        选中目录后：⌘T 新终端 · ⌘N 新建文件 · ⌘⇧A 打开任务
      </p>
    </div>
  )
}
