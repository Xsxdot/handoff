// webPrefs —— Web 客户端偏好：读写 localStorage（B369.8 §3.4）。
//
// 职责：
//   - 偏好的形状、默认值与持久化（单键 handoff.web.prefs）
//   - 会话打开方式（chat/scene）与底栏角标门控两条规则本身
//
// 边界：
//   - **不并入 treePrefs**：那是「左栏显示偏好」（B160），键与语义都是它的；
//     混键会让两边的 v 迁移互相绑架。这里是「Web 客户端行为偏好」另一族。
//   - 不认识 React：状态与订阅在 useWebPrefs.ts（镜像 useTreePrefs 的模式），
//     本文件只有类型、默认值与 load/save 纯函数
//   - 桌面不读：消费点只有 Shell 的 compact 分支（openSession scene 档、
//     MobileTabBar 角标门控）与 SettingsHub 的两个控件
export type SessionOpenMode = 'chat' | 'scene'

// WebPrefs 是落盘的全部偏好。v 用于将来改形状时判断要不要整份丢弃。
export interface WebPrefs {
  v: 1
  // sessionOpenMode：会话打开方式（compact 设置中心①）。
  // chat = 群聊态（现状缺省）；scene = 点会话行后尽力解析该会话在跑任务并跳进
  // 任务现场，解析不到静默回落群聊——解析是尽力而为不是承诺（Shell.openSession）。
  sessionOpenMode: SessionOpenMode
  // badges：底栏角标（需要你/未读）。false = needsCount/unread 双双归 0，
  // 只门控 MobileTabBar，会话列表行内未读点不受影响。
  badges: boolean
}

export const PREFS_KEY = 'handoff.web.prefs'

export const DEFAULT_WEB_PREFS: WebPrefs = {
  v: 1,
  sessionOpenMode: 'chat',
  badges: true,
}

// isPrefs 校验一份解析出来的对象是否真是 WebPrefs。
//
// 逐字段查类型而不是信 as：这份数据落在用户可手改的 localStorage 里，
// 形状不认识就整份回默认，不让一个坏字段带崩消费方。
function isPrefs(v: unknown): v is WebPrefs {
  if (typeof v !== 'object' || v === null) return false
  const p = v as Record<string, unknown>
  return (
    p.v === 1 &&
    (p.sessionOpenMode === 'chat' || p.sessionOpenMode === 'scene') &&
    typeof p.badges === 'boolean'
  )
}

// loadPrefs 读偏好；任何异常都静默回退默认值。
//
// 读不出偏好的正确反应是「按默认显示」而不是报错打断——它是视图偏好，
// 不是业务数据。但会 console.warn 一次带上被丢弃的原文，坏偏好是真实排查线索。
export function loadPrefs(): WebPrefs {
  let raw: string | null = null
  try {
    raw = localStorage.getItem(PREFS_KEY)
  } catch {
    return DEFAULT_WEB_PREFS   // 隐私模式下 localStorage 可能直接抛
  }
  if (raw === null) return DEFAULT_WEB_PREFS
  try {
    const parsed: unknown = JSON.parse(raw)
    if (isPrefs(parsed)) return parsed
    console.warn('[webPrefs] 偏好形状不认识，已回退默认值：', raw.slice(0, 200))
  } catch (err) {
    console.warn('[webPrefs] 偏好不是合法 JSON，已回退默认值：', raw.slice(0, 200), err)
  }
  return DEFAULT_WEB_PREFS
}

// savePrefs 落盘一份偏好；写失败只警告不抛（配额满/隐私模式）。
export function savePrefs(p: WebPrefs): void {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(p))
  } catch (err) {
    console.warn('[webPrefs] 偏好写入失败，本次改动只在内存里生效', err)
  }
}
