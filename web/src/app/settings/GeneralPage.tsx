// GeneralPage —— 设置页「常规」分区（B160 spec §2.1）。
//
// 职责：把左栏那个紧凑的显示偏好菜单在设置页里**平铺展开**。
//
// **这里只放客户端偏好**（落 localStorage、只影响当前浏览器）。归属判据见
// spec §1.1：某台机器的运行参数（config.yaml）归开发机详情；协调者 CLI 本机的
// 行为（sync.auto / terminal.auto）两处都不放——控制台连的 agentd 与你敲 CLI
// 那台机可能不是同一台，一个改了不生效的开关比没有这个开关更糟。
//
// **不为了填满一屏去发明设置**：今天就显示三项 + 排序 + 项目勾选，主题与快捷键都还不存在。
//
// 边界：
//   - 不复用 TreePrefsMenu 的紧凑形态：设置页有空间，菜单没有。共用的是
//     treePrefs.ts 的类型与 useTreePrefs 的状态，不是那个下拉的渲染
//   - 不自己取项目树：由 SettingsPage 传进来（它已经有一份）
import type { ProjectTreeResp } from '../../api/types'
import { useTreePrefs } from '../tree/useTreePrefs'
import type { ProjectSort } from '../tree/treePrefs'

// SORT_LABELS 与左栏菜单同源同序：两处标签不一致会让人以为是两套设置。
const SORT_LABELS: { value: ProjectSort; label: string }[] = [
  { value: 'active', label: '活跃优先' },
  { value: 'name', label: '名称' },
  { value: 'recent', label: '最近活动' },
]

// GeneralPage 渲染当前浏览器的显示偏好。tree 为 null 表示项目树还没到。
export function GeneralPage({ tree }: { tree: ProjectTreeResp | null }) {
  const [prefs, update] = useTreePrefs()
  const hidden = new Set(prefs.hiddenProjects)
  const projects = tree?.projects ?? []

  return (
    <div className="flex flex-col gap-5 p-4">
      <div className="border-b pb-3">
        <h2 className="text-sm font-semibold">常规</h2>
        <p className="mt-1 text-xs text-muted-foreground">
          这些设置只保存在当前浏览器里，不同步到其他设备，也不影响任何一台开发机。
        </p>
      </div>

      <section>
        <h3 className="text-xs font-medium text-muted-foreground">显示</h3>
        <label className="mt-2 flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={prefs.hideIdleWorktrees}
            onChange={() => update({ ...prefs, hideIdleWorktrees: !prefs.hideIdleWorktrees })}
          />
          隐藏无活跃任务的工作树
        </label>
        <label className="mt-2 flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={prefs.hideArchived}
            onChange={() => update({ ...prefs, hideArchived: !prefs.hideArchived })}
          />
          隐藏已结束分组
        </label>
        <label className="mt-2 flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={prefs.hideDirCounts}
            onChange={() => update({ ...prefs, hideDirCounts: !prefs.hideDirCounts })}
          />
          隐藏文件夹数量
        </label>
      </section>

      <section>
        <h3 className="text-xs font-medium text-muted-foreground">项目排序</h3>
        <div className="mt-2 flex flex-col gap-1.5">
          {SORT_LABELS.map((s) => (
            <label key={s.value} className="flex items-center gap-2 text-sm">
              <input
                type="radio"
                name="project-sort"
                aria-label={s.label}
                checked={prefs.projectSort === s.value}
                onChange={() => update({ ...prefs, projectSort: s.value })}
              />
              {s.label}
            </label>
          ))}
        </div>
      </section>

      <section>
        <h3 className="text-xs font-medium text-muted-foreground">左栏显示哪些项目</h3>
        {projects.length === 0 ? (
          // 空分区也要有话说：一块空白会让人以为页面坏了
          <p className="mt-2 text-xs text-muted-foreground">项目树还没加载出来。</p>
        ) : (
          <>
            <div className="mt-2 flex flex-col gap-1.5">
              {projects.map((p) => (
                <label key={p.project_id} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    aria-label={p.name}
                    checked={!hidden.has(p.project_id)}
                    onChange={() => {
                      // 名单存的是「不显示谁」：勾 = 从名单拿掉，取消勾 = 加进名单。
                      // 取反方向与直觉相反，改的时候看清楚（与左栏菜单同一条注释）
                      const next = new Set(prefs.hiddenProjects)
                      if (next.has(p.project_id)) next.delete(p.project_id)
                      else next.add(p.project_id)
                      update({ ...prefs, hiddenProjects: [...next] })
                    }}
                  />
                  {p.name}
                </label>
              ))}
            </div>
            <div className="mt-2 flex gap-3 text-xs">
              <button type="button" className="text-primary hover:underline"
                onClick={() => update({ ...prefs, hiddenProjects: [] })}>全选</button>
              <button type="button" className="text-primary hover:underline"
                onClick={() => update({ ...prefs, hiddenProjects: projects.map((p) => p.project_id) })}>全不选</button>
            </div>
          </>
        )}
      </section>
    </div>
  )
}
