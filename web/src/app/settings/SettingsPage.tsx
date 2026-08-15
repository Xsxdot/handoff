// SettingsPage —— 设置页（spec §6）。
//
// 职责：把配置性质的内容集中到一处：开发机、常规、Env 文件。
//
// 形态：**整页替换中央内容区，左栏保持可见——不是弹出层。** 三条理由：
//   - 内容重、有多个分区，塞进弹层会挤
//   - 低频配置动作，不需要「扫一眼就回去」的弹层语义
//   - 原型的开发机页本来就是这个形态，这点不必偏离
// 设置若也做成弹层，就要处理「弹层上开弹层」——spec §0 要求同时只有一个弹层。
//
// 边界：
//   - 退出设置回到工作台时，中央 tab 组与当前选中目录保持不变——它们由
//     useWorkbench 持有，与本页无关，天然满足
//   - 不自己取项目树：树流在 Shell 手里，这里按需拉一份只读的（useProjectTree
//     内部有共享，不会打出第二条轮询）。若实测出现双份轮询，改为由 Shell 传入
import { useState } from 'react'
import { ArrowLeft } from 'lucide-react'
import { useProjectTree } from '../data/useProjectTree'
import { MachinesPage } from '../machines/MachinesPage'
import { cn } from '@/lib/utils'

// SECTIONS 是设置页的三个分区。顺序即原型的顺序：开发机在最上（唯一有真内容的）。
const SECTIONS = [
  { key: 'machines', label: '开发机' },
  { key: 'general', label: '常规' },
  { key: 'env', label: 'Env 文件' },
] as const

type SectionKey = (typeof SECTIONS)[number]['key']

export function SettingsPage({ onClose }: { onClose: () => void }) {
  const [section, setSection] = useState<SectionKey>('machines')
  const treeState = useProjectTree()

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center gap-3 border-b px-4 py-2.5">
        <h1 className="text-sm font-semibold">设置</h1>
        <button
          type="button"
          onClick={onClose}
          className="ml-auto inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <ArrowLeft className="size-3.5" />
          返回工作台
        </button>
      </header>

      <div className="flex min-h-0 flex-1">
        <nav className="w-40 shrink-0 border-r p-2">
          {SECTIONS.map((s) => (
            <button
              key={s.key}
              type="button"
              onClick={() => setSection(s.key)}
              aria-current={section === s.key ? 'true' : undefined}
              className={cn(
                'block w-full rounded-md px-2 py-1.5 text-left text-[13px] hover:bg-accent',
                section === s.key && 'bg-accent font-medium',
              )}
            >
              {s.label}
            </button>
          ))}
        </nav>

        <div className="min-h-0 flex-1 overflow-auto">
          {section === 'machines' && <MachinesPage tree={treeState.data} />}
          {/* 空分区也要有话说：一块空白会让人以为页面坏了（spec §0 不置灰的同源纪律） */}
          {section === 'general' && (
            <p className="p-6 text-sm text-muted-foreground">
              常规设置本期没有可配置项。桌面行为、主题、快捷键等留待后续。
            </p>
          )}
          {section === 'env' && (
            <p className="p-6 text-sm text-muted-foreground">
              Env 文件管理（每台机器下的物理 .env 文件、每个文件多个变量）本期不做。
              原型里有完整设计，等它有真实落点时再实现。
            </p>
          )}
        </div>
      </div>
    </div>
  )
}
