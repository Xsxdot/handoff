// dockPersist.ts —— 悬浮窗现场的编解码层（2026-08-20 状态同步 spec §5.1、§5.5）。
//
// 职责：
//   - DockSnapshot ↔ 落盘用的 JSON 字符串，读回时逐字段校验
//   - 规则二用在悬浮窗 tab 上（抹掉已死的 sessionId）
//   - 恢复几何时按**当前**视口夹紧
//
// 边界：
//   - 不碰 React、不发请求
//   - **不落草稿**：file tab 的 draft / baseSha 编码时剥掉（同工作台侧的决定）
//   - 不认识 useHomeDock 的内部 ref（seq / tabId 计数器的播种在那边做）
//
// 为什么与 workbench/persist.ts 分开：悬浮窗与工作台是两套互不认识的状态
//（见 useHomeDock 的边界注释）。合并会让工作台反过来依赖 HomeTab。
import type { HomeTab } from './useHomeDock'

// DOCK_PERSIST_VERSION 与工作台侧的 PERSIST_VERSION 各自独立：
// 两份数据形状无关，一边改了不该让另一边的老数据一起作废。
export const DOCK_PERSIST_VERSION = 1

// Geom 是浮窗在视口里的位置与尺寸，单位 px。
export interface Geom {
  x: number
  y: number
  w: number
  h: number
}

// DockSnapshot 是悬浮窗的完整现场。
export interface DockSnapshot {
  tabs: HomeTab[]
  activeId: string | null
  windowOpen: boolean
  geom: Geom
  maximized: boolean
}

// 几何夹紧用的下界，与 useHomeDock 里那四个常量一一对应。
//
// 为什么在这里重复一遍而不是从 useHomeDock 导出：那边它们是模块私有常量，
// 导出会把「浮窗内部尺寸约定」变成公开接口。四个数字重复的代价，小于
// 多一个跨模块耦合点；两边同时要改的场景（改浮窗最小尺寸）本来就要一起看。
const MIN_W = 360
const MIN_H = 200
const MARGIN = 8

// encodeDock 把悬浮窗现场序列化成 payload 字符串。
//
// 参数：d 是当前现场。
// 返回：JSON 字符串。file tab 的 draft / baseSha 被剥掉。
// 注意：返回值直接作为服务端单例 payload，调用方不应再包一层 JSON。
export function encodeDock(d: DockSnapshot): string {
  return JSON.stringify({
    v: DOCK_PERSIST_VERSION,
    tabs: d.tabs.map(stripDockTab),
    activeId: d.activeId,
    windowOpen: d.windowOpen,
    geom: { x: d.geom.x, y: d.geom.y, w: d.geom.w, h: d.geom.h },
    maximized: d.maximized,
  })
}

// stripDockTab 去掉一个悬浮窗 tab 里不该落盘的部分（目前只有草稿两字段）。
function stripDockTab(t: HomeTab): HomeTab {
  const out: HomeTab = { id: t.id, kind: t.kind, seq: t.seq, machine: t.machine }
  if (t.sessionId !== undefined) out.sessionId = t.sessionId
  if (t.rel !== undefined) out.rel = t.rel
  return out
}

// decodeDock 把 payload 解回悬浮窗现场。
//
// 参数：raw 是服务端存的字符串。
// 返回：校验通过时返回 DockSnapshot；**任何一处不对就返回 null**，调用方整份丢弃。
// 注意：只接受当前版本，旧版本不做猜测式迁移。
export function decodeDock(raw: string): DockSnapshot | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (!isObject(parsed)) return null
  if (parsed.v !== DOCK_PERSIST_VERSION) return null
  if (typeof parsed.windowOpen !== 'boolean' || typeof parsed.maximized !== 'boolean') return null
  if (parsed.activeId !== null && typeof parsed.activeId !== 'string') return null
  if (!Array.isArray(parsed.tabs)) return null

  const geom = parseGeom(parsed.geom)
  if (geom === null) return null

  const tabs: HomeTab[] = []
  for (const t of parsed.tabs) {
    if (!isObject(t)) return null
    if (typeof t.id !== 'string' || typeof t.machine !== 'string') return null
    if (typeof t.seq !== 'number' || !Number.isFinite(t.seq)) return null
    if (t.kind !== 'terminal' && t.kind !== 'file') return null
    const tab: HomeTab = { id: t.id, kind: t.kind, seq: t.seq, machine: t.machine }
    if (t.sessionId !== undefined) {
      if (typeof t.sessionId !== 'string') return null
      tab.sessionId = t.sessionId
    }
    if (t.rel !== undefined) {
      if (typeof t.rel !== 'string') return null
      tab.rel = t.rel
    }
    tabs.push(tab)
  }
  // activeId 指向一个不存在的 tab，浮窗会显示一片空白且没人能解释为什么
  if (parsed.activeId !== null && !tabs.some((t) => t.id === parsed.activeId)) return null

  return { tabs, activeId: parsed.activeId, windowOpen: parsed.windowOpen, geom, maximized: parsed.maximized }
}

function parseGeom(raw: unknown): Geom | null {
  if (!isObject(raw)) return null
  const nums = [raw.x, raw.y, raw.w, raw.h]
  if (!nums.every((n) => typeof n === 'number' && Number.isFinite(n))) return null
  return { x: raw.x as number, y: raw.y as number, w: raw.w as number, h: raw.h as number }
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

// pruneDeadDockSessions 抹掉不在 liveIds 里的 sessionId（spec §2 规则二）。
//
// 参数：tabs 是刚恢复出来的悬浮窗 tab；liveIds 是还活着的会话 id。
// 返回：新数组；tab 一个都不删，只去掉死掉的 sessionId 字段。
// 注意：非终端 tab 与没有 sessionId 的终端 tab 原样保留。
export function pruneDeadDockSessions(tabs: HomeTab[], liveIds: Set<string>): HomeTab[] {
  return tabs.map((t) => {
    if (t.kind !== 'terminal' || t.sessionId === undefined) return t
    if (liveIds.has(t.sessionId)) return t
    const out: HomeTab = { id: t.id, kind: t.kind, seq: t.seq, machine: t.machine }
    if (t.rel !== undefined) out.rel = t.rel
    return out
  })
}

// clampGeom 把恢复出来的几何夹进当前视口。
//
// 参数：
//   - g: 上次落盘的几何
//   - vw / vh: 当前视口宽高
//   - inset: 页面顶部要让出的高度（桌面薄壳的拖动区，浏览器里为 0）
//
// 返回：夹紧后的几何。
// 注意：先夹尺寸再夹位置；视口装不下最小尺寸时优先保证窗口仍可见。
//
// 为什么必须夹：上次在 27 寸屏上摆到 x=2000，这次在笔记本上打开，不夹就是一个
// 看不见的浮窗——用户会以为悬浮窗坏了。
//
// 夹紧次序是**先尺寸后位置**，且视口装不下最小尺寸时「不出屏」优先于「不小于下界」：
// 一个比最小尺寸还小、但看得见的浮窗，好过一个尺寸达标却在屏幕外的浮窗。
export function clampGeom(g: Geom, vw: number, vh: number, inset: number): Geom {
  const topLimit = inset + MARGIN
  // 可用区：扣掉四周边距与顶部让位
  const maxW = Math.max(1, vw - MARGIN * 2)
  const maxH = Math.max(1, vh - topLimit - MARGIN)

  const w = Math.min(Math.max(MIN_W, g.w), maxW)
  const h = Math.min(Math.max(MIN_H, g.h), maxH)
  const x = Math.min(Math.max(MARGIN, g.x), Math.max(MARGIN, vw - MARGIN - w))
  const y = Math.min(Math.max(topLimit, g.y), Math.max(topLimit, vh - MARGIN - h))
  return { x, y, w, h }
}
