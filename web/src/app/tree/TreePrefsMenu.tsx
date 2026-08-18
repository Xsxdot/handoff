// TreePrefsMenu —— 左栏「项目 N」那行右侧的显示偏好菜单（spec §1.2）。
//
// 职责：把 TreePrefs 与项目列表装配成 IconMenu 的 items，仅此而已。
//
// 边界：
//   - 不持有状态、不落盘：改动经 onChange 交回 ProjectTree，由它统一 setState + savePrefs
//   - **projects 必须是未经隐藏过滤的全量项目**：被藏起来的项目要能在菜单里勾回来，
//     传过滤后的列表等于藏一个少一个、再也拿不回来
//   - 触发器用推子图标而不是齿轮：左栏底部已有一个齿轮通向设置页，两个齿轮
//     会让人以为点哪个都一样，而它们不是一类东西
import { SlidersHorizontal } from 'lucide-react'
import { IconMenu, type IconMenuItem } from '../lib/IconMenu'
import type { ProjectSort, TreePrefs } from './treePrefs'

/** TreePrefsMenu 的输入；projects 必须是未经过隐藏过滤的全量项目。 */
export interface TreePrefsMenuProps {
  prefs: TreePrefs
  projects: { project_id: string; name: string }[]
  onChange: (next: TreePrefs) => void
}

// SORT_LABELS 是三档排序的人话标签，顺序即菜单里的顺序。
const SORT_LABELS: { value: ProjectSort; label: string }[] = [
  { value: 'active', label: '活跃优先' },
  { value: 'name', label: '名称' },
  { value: 'recent', label: '最近活动' },
]

/** 把偏好选项渲染为左栏项目标题旁的 IconMenu。 */
export function TreePrefsMenu({ prefs, projects, onChange }: TreePrefsMenuProps) {
  const hidden = new Set(prefs.hiddenProjects)
  const items: IconMenuItem[] = [
    { key: 'h-display', label: '显示', kind: 'header' },
    {
      key: 'hide-idle',
      label: '隐藏无活跃任务的工作树',
      kind: 'check',
      checked: prefs.hideIdleWorktrees,
      keepOpen: true,
      onSelect: () => onChange({ ...prefs, hideIdleWorktrees: !prefs.hideIdleWorktrees }),
    },
    { key: 'h-sort', label: '排序方式', kind: 'header' },
    ...SORT_LABELS.map((s) => ({
      key: `sort-${s.value}`,
      label: s.label,
      kind: 'radio' as const,
      checked: prefs.projectSort === s.value,
      keepOpen: true,
      onSelect: () => onChange({ ...prefs, projectSort: s.value }),
    })),
    { key: 'h-projects', label: `项目 · ${projects.length}`, kind: 'header' },
    {
      key: 'all-on',
      label: '全选',
      keepOpen: true,
      onSelect: () => onChange({ ...prefs, hiddenProjects: [] }),
    },
    {
      key: 'all-off',
      label: '全不选',
      keepOpen: true,
      onSelect: () => onChange({ ...prefs, hiddenProjects: projects.map((p) => p.project_id) }),
    },
    ...projects.map((p) => ({
      key: `p-${p.project_id}`,
      label: p.name,
      kind: 'check' as const,
      checked: !hidden.has(p.project_id),
      keepOpen: true,
      onSelect: () => {
        const next = new Set(prefs.hiddenProjects)
        // 勾 = 从隐藏名单里拿掉；取消勾 = 加进名单。名单存的是「不显示谁」，
        // 所以这里的取反方向与直觉相反，改的时候看清楚
        if (next.has(p.project_id)) next.delete(p.project_id)
        else next.add(p.project_id)
        onChange({ ...prefs, hiddenProjects: [...next] })
      },
    })),
  ]
  return (
    <IconMenu
      label="显示偏好"
      icon={<SlidersHorizontal className="size-3.5" />}
      items={items}
      className="rounded p-0.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
    />
  )
}
