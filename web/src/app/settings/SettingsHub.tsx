// SettingsHub —— compact 设置中心（B369.8 §2 岔口 1 / §3.4）。
//
// 职责：sub=null 时四分区就地呈现（会话打开方式 / 提醒 / 显示与可访问性 / 关于）+
// 六个二级入口行；sub=<key> 时对应二级页全宽承载 + 「‹ 设置中心」返回行。
//
// 边界：
//   - **只做编排不做内容**：二级页组件单一来源（MachinesPage 等原样复用），
//     数据（tree/desktopState/latest/updateAvailable）由 SettingsPage 传入，
//     本组件不自起任何轮询——hub 与桌面双栏的漂移风险被压到文案层（plan §9.4）
//   - 与桌面双栏零共享 JSX：桌面回归面被 SettingsPage 的分支隔离，塞进同一个
//     return 只会增加桌面回归面（plan §2 岔口 1 的裁决理由）
//   - 偏好读写走 useWebPrefs；「显示与可访问性」的既有偏好仍是 useTreePrefs
//     （GeneralPage 内部），两族偏好不混键（webPrefs.ts 文件头）
//   - 根节点挂触点基线类（plan §3.3）：一行类把全部行内钮/输入/下拉抬到 24×24
import { ChevronLeft } from 'lucide-react'
import { SETTINGS_SUB_KEYS, normalizeSettingsSub, type SettingsSub } from '../shell/useMobileNav'
import type { DesktopState, LatestResp, ProjectTreeResp } from '../../api/types'
import { useWebPrefs } from './useWebPrefs'
import { GeneralPage } from './GeneralPage'
import { MachinesPage } from '../machines/MachinesPage'
import { DisciplinePage } from './DisciplinePage'
import { SchedulingPage } from './SchedulingPage'
import { EnvPage } from './EnvPage'
import { UpdatePage } from './UpdatePage'

// SUB_LABELS 二级入口行文案。machines 用「执行机」、pairing 用「扫码配对」：
// compact 语境的叫法（plan §11 实走第 1 步口径），桌面「开发机」双栏不动。
// 其余四项与桌面 SECTIONS 同词——两处标签不一致会让人以为是两套设置。
const SUB_LABELS: Record<SettingsSub, string> = {
  machines: '执行机',
  pairing: '扫码配对',
  discipline: '执行纪律',
  automation: '自动化',
  env: 'Env 文件',
  update: '检查更新',
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
  // 接收方兜一道白名单：SettingsPage 的 prop 是 string|null（plan §2 岔口 1），
  // 词表外的值按「落设置中心」处理，不信任注入方已归一。
  const active = normalizeSettingsSub(sub)

  if (active !== null) {
    return (
      <div className="[&_button]:min-h-6 [&_button]:min-w-6 [&_input]:min-h-6 [&_select]:min-h-6">
        <button
          type="button"
          data-testid="settings-sub-back"
          onClick={() => onSubChange(null)}
          className="inline-flex min-h-11 items-center gap-1 px-2 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <ChevronLeft className="size-4" />
          设置中心
        </button>
        {active === 'machines' && <MachinesPage tree={tree} />}
        {active === 'pairing' && <PairingGuide />}
        {active === 'discipline' && <DisciplinePage />}
        {active === 'automation' && <SchedulingPage />}
        {active === 'env' && <EnvPage />}
        {active === 'update' && <UpdatePage desktopState={desktopState} latest={latest} />}
      </div>
    )
  }

  return (
    <div className="[&_button]:min-h-6 [&_button]:min-w-6 [&_input]:min-h-6 [&_select]:min-h-6">
      {/* ① 会话打开方式（B369.8 §3.4）：compact 打开会话行的落点偏好。
          scene 档是尽力解析不是承诺，解析不到静默回落群聊（Shell.openSession）。 */}
      <section data-testid="pref-session-open-mode" className="border-b p-4">
        <h3 className="text-xs font-medium text-muted-foreground">会话打开方式</h3>
        <div className="mt-2 flex flex-col gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="radio"
              name="session-open-mode"
              checked={prefs.sessionOpenMode === 'chat'}
              onChange={() => updatePrefs({ ...prefs, sessionOpenMode: 'chat' })}
            />
            群聊态
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="radio"
              name="session-open-mode"
              checked={prefs.sessionOpenMode === 'scene'}
              onChange={() => updatePrefs({ ...prefs, sessionOpenMode: 'scene' })}
            />
            任务现场
          </label>
        </div>
        <p className="mt-1.5 text-xs text-muted-foreground">
          选「任务现场」时，点开有在跑任务的会话会尽力直接跳进该任务；解析不到仍落群聊。
        </p>
      </section>

      {/* ② 提醒（B369.8 §3.4）：门控底栏角标（需要你/未读双双归 0），
          会话列表行内未读点不受影响。 */}
      <section data-testid="pref-badges" className="border-b p-4">
        <h3 className="text-xs font-medium text-muted-foreground">提醒</h3>
        <label className="mt-2 flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={prefs.badges}
            onChange={() => updatePrefs({ ...prefs, badges: !prefs.badges })}
          />
          底栏显示「需要你 / 未读」角标
        </label>
      </section>

      {/* ③ 显示与可访问性：GeneralPage 的既有三项 + 排序 + 项目勾选，
          与左栏菜单共享同一份 useTreePrefs 状态。 */}
      <section className="border-b">
        <GeneralPage title="显示与可访问性" tree={tree} />
      </section>

      {/* ④ 关于：版本信息只读呈现（此前只藏在桌面「更新」分区里）。 */}
      <section data-testid="settings-about" className="p-4">
        <h3 className="text-xs font-medium text-muted-foreground">关于</h3>
        <p className="mt-2 text-sm">handoff 控制台</p>
        <p className="mt-1 text-xs text-muted-foreground">
          版本：{desktopState?.app_version !== undefined && desktopState?.app_version !== ''
            ? desktopState.app_version
            : '未知（浏览器使用，随部署更新）'}
        </p>
      </section>

      {/* 二级入口行 ×6：min-h-11 触控（plan §3.3 主动作档）；update 行带
          updateAvailable 红点，对齐桌面 SettingsPage:88-90 的同一计算。 */}
      <nav aria-label="更多设置" className="border-t p-2 pb-4">
        {SETTINGS_SUB_KEYS.map((key) => (
          <button
            key={key}
            type="button"
            data-testid={`settings-sub-${key}`}
            onClick={() => onSubChange(key)}
            className="flex min-h-11 w-full items-center gap-2 rounded-md px-2 text-left text-sm hover:bg-accent"
          >
            {SUB_LABELS[key]}
            {key === 'update' && updateAvailable && (
              <span aria-label="有可用更新" data-testid="update-available-dot" className="inline-block size-1.5 rounded-full bg-amber-500 align-middle" />
            )}
          </button>
        ))}
      </nav>
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
