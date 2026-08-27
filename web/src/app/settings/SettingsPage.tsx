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
import { useDesktopState, useLatest } from '../data/useUpdate'
import { hasNewer } from '../lib/version'
import { MachinesPage } from '../machines/MachinesPage'
import { DisciplinePage } from './DisciplinePage'
import { EnvPage } from './EnvPage'
import { GeneralPage } from './GeneralPage'
import { UpdatePage } from './UpdatePage'
import { SchedulingPage } from './SchedulingPage'
import { cn } from '@/lib/utils'

// SECTIONS 是设置页的六个分区。自动化编制接在 Env 文件之后，更新保持末尾。
const SECTIONS = [
  { key: 'machines', label: '开发机' },
  { key: 'discipline', label: '执行纪律' },
  { key: 'general', label: '常规' },
  { key: 'env', label: 'Env 文件' },
  { key: 'scheduling', label: '自动化编制' },
  { key: 'update', label: '更新' },
] as const

type SectionKey = (typeof SECTIONS)[number]['key']

export function SettingsPage({ onClose }: { onClose: () => void }) {
  const [section, setSection] = useState<SectionKey>(() => {
    // 提示框的「查看详情」用 query 指向更新分区；读取 window 而不是 router hook，
    // 保持 SettingsPage 在现有的独立单测与嵌入场景中也能直接渲染。
    if (typeof window !== 'undefined' && new URLSearchParams(window.location.search).get('section') === 'update') {
      return 'update'
    }
    return 'machines'
  })
  const treeState = useProjectTree()
  const desktopState = useDesktopState()
  const latest = useLatest()
  const updateAvailable = desktopState.data !== null && desktopState.data !== undefined && (
    desktopState.data.sync_plan === 'blocked' || desktopState.data.sync_plan === 'failed' ||
    (desktopState.data.app_version !== '' && latest.data?.tag !== undefined && latest.data.tag !== '' &&
      hasNewer(latest.data.tag, desktopState.data.app_version))
  )

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
              {s.key === 'update' && updateAvailable && (
                <span aria-label="有可用更新" data-testid="update-available-dot" className="ml-1 inline-block size-1.5 rounded-full bg-amber-500 align-middle" />
              )}
            </button>
          ))}
        </nav>

        <div className="min-h-0 flex-1 overflow-auto">
          {section === 'machines' && <MachinesPage tree={treeState.data} />}
          {section === 'discipline' && <DisciplinePage />}
          {section === 'general' && <GeneralPage tree={treeState.data} />}
          {section === 'env' && <EnvPage />}
          {section === 'scheduling' && <SchedulingPage />}
          {section === 'update' && <UpdatePage desktopState={desktopState.data} latest={latest.data} />}
        </div>
      </div>
    </div>
  )
}
