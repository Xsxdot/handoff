// MobileProjectList —— 移动项目列表（S2/B426）。
//
// 职责：对齐原型 mobile-projects.html 的两段形态——apphead（「项目」标题 +
//       「＋添加项目」钮）+ 项目卡流（图标 = 名称前两字符 + 哈希底色块、名称、›；
//       第二行位置 chips = 机器名 · N 活跃 / 离线，零活跃位置裸机器名同原型卡三）。
// 边界：
//   - 纯投影呈现：项目/机器/任务由 Shell（useProjectTree + useTasks）持有并下传，
//     本组件只渲染与回调，不发请求、不持轮询（MobileProjectDetail 同款纪律）
//   - 点卡 = nav.setProject 下钻既有 MobileProjectDetail（其 props/行为零改动）；
//     ＋添加项目 = Shell 既有添加项目向导通道（onAddProject），零新端点
//   - 活跃数口径与 ProjectTree.locationActiveCount（= tasksOfWorkspace +
//     running/waiting_answer/waiting_review，MobileProjectDetail.isAwaitingUs 同族）
//     同源，不另立第二套
//   - 底色：projectColor 哈希取色经 inline style 消费 CSS 变量（color-mix 调淡）。
//     why 不走 Tailwind bg 类：v4 按需产出对拼出来的类名静默失效（不报错、就是
//     没颜色）；var 引用是运行时解析的 inline style，不吃静态扫描，零 index.css
//     同步负担（projectColorVar 注释同此）
//   - 不认识 router：下钻与向导都经回调，本组件可在隔离环境渲染（组件测试即证）
import { machineLabel, locationProblem, locationActiveCount } from './ProjectTree'
import { projectColorVar } from './projectColor'
import { TOUCH_BASELINE } from '@/lib/touch'
import { cn } from '@/lib/utils'
import type { MachineStatus, ProjectNode, Task } from '../../api/types'

export interface MobileProjectListProps {
  projects: ProjectNode[]
  // 跨机汇总信封的机器应答行：位置离线判定的第二源（probe_error 优先、ok=false
  // 兜底），经 locationProblem 与详情面/桌面树同一判据消费。undefined = 树已到
  // 但机器行未到，全部位置按探测结果呈现，不猜测。
  machines: MachineStatus[] | undefined
  tasks: Task[]
  onOpenProject: (projectId: string) => void
  onAddProject: () => void
}

/** 参数：树投影（项目/机器/任务）与两个回调；返回：原型卡流形态的项目列表。 */
export function MobileProjectList({ projects, machines, tasks, onOpenProject, onAddProject }: MobileProjectListProps) {
  return (
    <div data-testid="mobile-project-list" className={cn('flex min-h-0 flex-1 flex-col', TOUCH_BASELINE)}>
      {/* apphead（原型 .apphead）：标题 flex-1 占位，添加钮收右 */}
      <div className="flex items-center gap-2.5 px-3.5 pb-2.5 pt-4">
        <div className="min-w-0 flex-1 text-2xl font-bold">项目</div>
        <button
          type="button"
          data-testid="mobile-add-project"
          onClick={onAddProject}
          className="shrink-0 rounded-full border px-2.5 py-1 text-xs text-muted-foreground hover:bg-accent"
        >
          ＋ 添加项目
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-3.5 pb-3.5">
        {projects.length === 0 && (
          <p className="p-2 text-sm text-muted-foreground">还没有登记任何项目。</p>
        )}
        {projects.map((project) => (
          <button
            key={project.project_id}
            type="button"
            data-testid="mobile-project-card"
            onClick={() => onOpenProject(project.project_id)}
            className="mb-2.5 flex w-full flex-col items-stretch rounded-[14px] border bg-background p-3.5 text-left hover:bg-accent/40"
          >
            <span className="flex items-center gap-2.5">
              <span
                aria-hidden
                style={{ backgroundColor: `color-mix(in srgb, ${projectColorVar(project.project_id)} 18%, white)` }}
                className="flex size-9 shrink-0 items-center justify-center rounded-[10px] text-[15px] font-bold text-foreground"
              >
                {project.name.slice(0, 2)}
              </span>
              <span className="min-w-0 flex-1 truncate text-[15px] font-semibold">{project.name}</span>
              <span aria-hidden className="shrink-0 text-muted-foreground">›</span>
            </span>
            <span className="mt-2.5 flex flex-wrap items-center gap-1.5">
              {project.locations.map((loc) => {
                const problem = locationProblem(loc, machines)
                const active = locationActiveCount(tasks, project, loc)
                return (
                  <span
                    key={loc.machine}
                    data-testid="mobile-project-loc"
                    className="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] text-muted-foreground"
                  >
                    {/* 状态点：绿=在线、灰=该位置机器断开（原型 .loc .dot / .loc.off）。
                        分隔符用显式字符串段：JSX 会吞掉换行缩进的空白，'· ' 写进
                        子元素会丢前导空格（textContent 变「本机· 2 活跃」）。 */}
                    <span aria-hidden className={cn('size-1.5 rounded-full', problem === '' ? 'bg-state-active' : 'bg-[#d4d4d4]')} />
                    {machineLabel(loc.machine)}
                    {problem !== '' ? (
                      ' · 离线'
                    ) : active > 0 ? (
                      <> · <span className="font-semibold text-amber-600">{active} 活跃</span></>
                    ) : null}
                  </span>
                )
              })}
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}
