// SettingsHub —— compact 设置首页与分级偏好页。每个桌面已有能力沿原组件可达。
//
// 边界：
//   - **只做编排不做内容**：二级页组件单一来源（MachinesPage 等原样复用），
//     数据（tree/desktopState/latest/updateAvailable）由 SettingsPage 传入，
//     本组件不自起任何轮询——hub 与桌面双栏的漂移风险被压到文案层（plan §9.4）
//   - 与桌面双栏零共享 JSX：桌面回归面被 SettingsPage 的分支隔离，塞进同一个
//     return 只会增加桌面回归面（plan §2 岔口 1 的裁决理由）
//   - 偏好读写走 useWebPrefs；「显示与可访问性」的既有偏好仍是 useTreePrefs
//     （GeneralPage 内部），两族偏好不混键（webPrefs.ts 文件头）
//   - 根节点挂触点基线类：移动端行与开关均保留至少 44px 触区
import { ArrowLeftRight, ChevronLeft, ChevronRight, CircleArrowUp, CirclePlay, Eye, FileText, Info, Laptop, ListChecks, Settings2 } from 'lucide-react'
import { normalizeSettingsSub, type SettingsSub } from '../shell/useMobileNav'
import type { DesktopState, LatestResp, ProjectTreeResp } from '../../api/types'
import { TOUCH_BASELINE } from '@/lib/touch'
import { useWebPrefs } from './useWebPrefs'
import { GeneralPage } from './GeneralPage'
import { MachinesPage } from '../machines/MachinesPage'
import { DisciplinePage } from './DisciplinePage'
import { SchedulingPage } from './SchedulingPage'
import { EnvPage } from './EnvPage'
import { UpdatePage } from './UpdatePage'
import { useNativeShellContext } from './useNativeShellContext'

const SUB_LABELS: Record<SettingsSub, string> = {
  work: '工作方式',
  display: '显示与可访问性',
  projects: '显示哪些项目',
  machines: '执行机与配对',
  pairing: '扫码配对',
  discipline: '执行纪律',
  automation: '自动化',
  env: 'Env 文件',
  update: '检查更新',
  about: '关于 Handoff',
}

const MANAGEMENT_KEYS: SettingsSub[] = ['discipline', 'automation', 'env']
const SECTION_HEAD = 'px-1 pb-2 pt-6 text-sm font-medium text-muted-foreground'

// machineDesc 是「执行机与配对」行的副题（原型 mobile-pairing:135）。树没到时
// 只留后半句：还没问过 与 问过且一台没连上 是两件事，不报「0 台」。
function machineDesc(tree: ProjectTreeResp | null): string {
  const ok = tree?.machines?.filter((m) => m.ok).length
  return ok === undefined ? '管理位置与扫码' : `已连接 ${ok} 台 · 管理位置与扫码`
}

export function SettingsHub({
  tree,
  desktopState,
  latest,
  updateAvailable,
  sub,
  onSubChange,
}: {
  tree: ProjectTreeResp | null
  desktopState: DesktopState | null
  latest: LatestResp | null
  updateAvailable: boolean
  sub: SettingsSub | null
  onSubChange: (key: string | null) => void
}) {
  const [prefs, updatePrefs] = useWebPrefs()
  const native = useNativeShellContext()
  // 接收方兜一道白名单：SettingsPage 的 prop 是 string|null（plan §2 岔口 1），
  // 词表外的值按「落设置中心」处理，不信任注入方已归一。
  const active = normalizeSettingsSub(sub)

  if (active !== null) {
    return (
      <div className={`${TOUCH_BASELINE} mobile-sub-settings mobile-sub-${active}`}>
        <header className="mobile-sub-header"><button
          type="button"
          data-testid="settings-sub-back"
          onClick={() => onSubChange(active === 'projects' ? 'display' : null)}
          className="inline-flex min-h-11 items-center gap-1 px-2 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <ChevronLeft className="size-4" />
          {active === 'projects' ? '显示' : '设置'}
        </button><h1>{SUB_LABELS[active]}</h1></header>
        {active === 'machines' && <MachinesPage tree={tree} />}
        {active === 'work' && <WorkPreferences prefs={prefs} updatePrefs={updatePrefs} />}
        {active === 'display' && <GeneralPage title="显示与可访问性" tree={tree} mode="display" onOpenProjects={() => onSubChange('projects')} />}
        {active === 'projects' && <GeneralPage title="显示哪些项目" tree={tree} mode="projects" />}
        {active === 'pairing' && <PairingGuide />}
        {active === 'discipline' && <DisciplinePage />}
        {active === 'automation' && <SchedulingPage compact />}
        {active === 'env' && <EnvPage compact />}
        {active === 'update' && <UpdatePage compact desktopState={desktopState} latest={latest} />}
        {active === 'about' && <AboutPage />}
      </div>
    )
  }

  return (
    <div className={TOUCH_BASELINE}>
      <div className="px-4 pt-4 pb-8">
        {native.enabled && (
          <section data-testid="native-current-machine" className="mb-2 flex min-h-[72px] items-center gap-3 rounded-lg border bg-card px-4">
            <Laptop className="size-6 shrink-0 text-muted-foreground" aria-hidden="true" />
            <div className="min-w-0 flex-1">
              <p className="text-xs text-muted-foreground">当前机器</p>
              <span data-testid="native-current-machine-name" className="mt-1 block truncate text-base font-medium">
                {native.machine || '未选择'}
              </span>
            </div>
            <a href="/_handoff/native/switch-machine" className="min-h-11 shrink-0 px-2 text-sm font-medium text-primary inline-flex items-center">更换</a>
          </section>
        )}
        <button type="button" data-testid="settings-sub-machines" onClick={() => onSubChange('machines')}
          className="flex min-h-[64px] w-full items-center gap-3 rounded-lg border bg-card px-4 py-3 text-left">
          <ArrowLeftRight className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span className="min-w-0 flex-1"><span className="block text-[15px] leading-5">执行机与配对</span><span data-testid="settings-sub-machines-desc" className="mt-1 block text-xs leading-4 text-muted-foreground">{machineDesc(tree)}</span></span>
          <ChevronRight className="size-4 text-muted-foreground" aria-hidden="true" />
        </button>

        <h3 data-testid="settings-section-work" className={SECTION_HEAD}>使用偏好</h3>
        <nav aria-label="使用偏好" className="overflow-hidden rounded-lg border bg-card">
          <SettingsRow testId="settings-sub-work" icon={Settings2} label="工作方式" description="会话打开方式与提醒" onClick={() => onSubChange('work')} />
          <SettingsRow testId="settings-sub-display" icon={Eye} label="显示与可访问性" description="显示、项目排序与可见项目" onClick={() => onSubChange('display')} />
        </nav>

        <h3 data-testid="settings-section-machine" className={SECTION_HEAD}>执行与管理</h3>
        <nav aria-label="执行与管理" className="overflow-hidden rounded-lg border bg-card">
          {MANAGEMENT_KEYS.map((key) => (
            <SettingsRow key={key} testId={`settings-sub-${key}`} icon={key === 'discipline' ? ListChecks : key === 'automation' ? CirclePlay : FileText}
              label={SUB_LABELS[key]} onClick={() => onSubChange(key)} />
          ))}
        </nav>

        <h3 data-testid="settings-section-about" className={SECTION_HEAD}>关于</h3>
        <nav aria-label="关于" className="overflow-hidden rounded-lg border bg-card">
          <SettingsRow testId="settings-sub-update" icon={CircleArrowUp} label="检查更新" hasDot={updateAvailable} onClick={() => onSubChange('update')} />
          <SettingsRow testId="settings-sub-about" icon={Info} label="关于 Handoff" onClick={() => onSubChange('about')} />
        </nav>
      </div>
    </div>
  )
}

function SettingsRow({ label, description, icon: Icon, testId, hasDot = false, onClick }: { label: string; description?: string; icon: typeof Settings2; testId: string; hasDot?: boolean; onClick: () => void }) {
  return (
    <button type="button" data-testid={testId} onClick={onClick} className={`flex ${description ? 'min-h-[64px] py-3' : 'min-h-[52px] py-2'} w-full items-center gap-3 border-b px-4 text-left last:border-b-0`}>
      <Icon className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="min-w-0 flex-1"><span className="block text-[15px] leading-5">{label}</span>{description && <span className="mt-1 block text-xs leading-4 text-muted-foreground">{description}</span>}</span>
      {hasDot && <span aria-label="有可用更新" data-testid="update-available-dot" className="inline-block size-1.5 rounded-full bg-amber-500" />}
      <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
    </button>
  )
}

function AboutPage({ version }: { version?: string }) {
  return (
    <section data-testid="settings-about" className="p-5">
      <p className="mt-5 text-sm">handoff 控制台</p>
      <p className="mt-2 text-sm text-muted-foreground">版本：{version || '未知（随部署更新）'}</p>
    </section>
  )
}

function WorkPreferences({ prefs, updatePrefs }: {
  prefs: ReturnType<typeof useWebPrefs>[0]
  updatePrefs: ReturnType<typeof useWebPrefs>[1]
}) {
  return (
    <div className="flex flex-col gap-6 p-4">
      <section className="border-b pb-4" data-testid="pref-session-open-mode">
        <h3 className="text-sm font-medium">会话打开方式</h3>
        <div className="mt-2 flex flex-col gap-1.5">
          <label className="flex min-h-11 items-center gap-2 text-sm"><input type="radio" name="session-open-mode" checked={prefs.sessionOpenMode === 'chat'} onChange={() => updatePrefs({ ...prefs, sessionOpenMode: 'chat' })} />群聊态</label>
          <label className="flex min-h-11 items-center gap-2 text-sm"><input type="radio" name="session-open-mode" checked={prefs.sessionOpenMode === 'scene'} onChange={() => updatePrefs({ ...prefs, sessionOpenMode: 'scene' })} />任务现场</label>
        </div>
        <p className="mt-1.5 text-xs text-muted-foreground">选「任务现场」时，点开有在跑任务的会话会尽力直接跳进该任务；解析不到仍落群聊。</p>
      </section>
      <section data-testid="pref-badges">
        <h3 className="text-sm font-medium">需要你提醒</h3>
        <label className="mt-2 flex min-h-11 items-center gap-2 text-sm"><input type="checkbox" checked={prefs.badges} onChange={() => updatePrefs({ ...prefs, badges: !prefs.badges })} />底栏显示「需要你 / 未读」角标</label>
      </section>
    </div>
  )
}

// PairingGuide —— 扫码配对的 web 侧静态说明（B369.8 §10：原生配对、相机不在
// 本卡；这里只承载「配对是什么、产物从哪来、在哪扫」的说明语义）。
function PairingGuide() {
  return (
    <div data-testid="pairing-guide" className="flex flex-col gap-3 p-4 text-sm">
      <h2 className="text-sm font-semibold">扫码配对</h2>
      <p className="text-muted-foreground">
        配对把浏览器里的控制台身份与一台开发机关联起来：终端、项目登记等写操作只在
        已配对的机器上放行。
      </p>
      <p className="text-muted-foreground">
        在装有 CLI 的机器上运行 <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">handoff console --qr</code>
        ，终端会打印配对二维码与一次性配对码。
      </p>
      <p className="text-muted-foreground">
        二维码在原生应用壳内扫码录入；Web 页面不调用相机，也没有扫码入口。
      </p>
    </div>
  )
}
