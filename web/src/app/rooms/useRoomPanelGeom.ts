// useRoomPanelGeom —— 会话浮窗（RoomPanel 非持久形态）的几何：位置、尺寸、本机持久化。
//
// 职责：按视口给浮窗定位（贴右下收起球）、处理拖动/缩放增量并钳制下界、
// 把几何写进 localStorage（重开恢复用户的摆法）。
// 边界：不渲染任何元素；持久侧栏形态（persistent=true）不消费本 hook。
// 交互模式照抄 HomeWindow/useHomeDock：拖动把手 = 面板标题栏，缩放 = 右下角，
// 监听挂 document（指针拖出窗口也能收到 move）。
import { useCallback, useEffect, useRef, useState } from 'react'
import { topInset } from '../lib/desktopShell'

export interface RoomGeom { x: number; y: number; w: number; h: number }

// 悬浮球在视口里的位置与尺寸（px），与 RoomPanel.tsx 收起球的 Tailwind 类
// 一一对应：right-5 / bottom-[104px] / size-11。改那边要同步改这里。
const FAB_RIGHT = 20
const FAB_BOTTOM = 104
const FAB_SIZE = 44
const FAB_GAP = 8
const MARGIN = 8
const MIN_W = 320
const MIN_H = 360
const STORE_KEY = 'handoff:room-panel-geom.v1'

// defaultRoomGeom 算浮窗初始几何：尺寸取现状形态（360×520），右下贴收起球、
// 球上方留 FAB_GAP。视口装不下时被 MIN_* 与「视口减两倍边距」夹住，保证不出屏。
export function defaultRoomGeom(vw: number, vh: number): RoomGeom {
  const w = Math.max(MIN_W, Math.min(360, vw - MARGIN * 2))
  const h = Math.max(MIN_H, Math.min(520, vh - MARGIN * 2))
  return {
    x: Math.max(MARGIN, vw - FAB_RIGHT - w),
    y: Math.max(topInset() + MARGIN, vh - FAB_BOTTOM - FAB_SIZE - FAB_GAP - h),
    w,
    h,
  }
}

function loadStored(): RoomGeom | null {
  try {
    const raw = window.localStorage.getItem(STORE_KEY)
    if (!raw) return null
    const g = JSON.parse(raw) as Partial<RoomGeom>
    if ([g.x, g.y, g.w, g.h].some((n) => !Number.isFinite(n))) return null
    return { x: g.x!, y: g.y!, w: Math.max(MIN_W, g.w!), h: Math.max(MIN_H, g.h!) }
  } catch {
    return null // 隐私模式/配额/坏 JSON：几何不持久，会话内仍可拖
  }
}

// initialGeom 挂载那一刻就定位：jsdom/真实浏览器里组件挂载时视口已定稿，
// 同步摆位让浮窗首帧就位、不多打一次渲染。视口取不到（webview 首帧 0×0、SSR）
// 返回 null，交给 ensurePlaced 的 effect 在后续渲染重试。
function initialGeom(): RoomGeom | null {
  if (typeof window === 'undefined') return null
  const vw = window.innerWidth || document.documentElement.clientWidth
  const vh = window.innerHeight || document.documentElement.clientHeight
  if (vw <= 0 || vh <= 0) return null
  return defaultRoomGeom(vw, vh)
}

export function useRoomPanelGeom() {
  const [geom, setGeom] = useState<RoomGeom | null>(() => loadStored() ?? initialGeom())
  const placed = useRef(geom !== null)

  // 几何变化即落盘；null 不写（尚未定位时没有「用户的摆法」可记）。
  useEffect(() => {
    if (geom === null) return
    try {
      window.localStorage.setItem(STORE_KEY, JSON.stringify(geom))
    } catch {
      // 同 loadStored：持久化失败无害，会话内照常用
    }
  }, [geom])

  // ensurePlaced 首次需要摆放时按当下视口定位。与 useHomeDock 同款承重：
  // 必须在浮窗真正出现那一刻算（effect 里），不能在模块加载时算——首帧视口不可信。
  const ensurePlaced = useCallback(() => {
    if (placed.current) return
    const vw = window.innerWidth || document.documentElement.clientWidth
    const vh = window.innerHeight || document.documentElement.clientHeight
    if (vw <= 0 || vh <= 0) return // 视口未定稿：下个渲染周期再摆
    placed.current = true
    setGeom(defaultRoomGeom(vw, vh))
  }, [])

  // onGeom 拖动/缩放的增量入口；用户亲手摆过即视为已定位。
  const onGeom = useCallback((g: Partial<RoomGeom>) => {
    placed.current = true
    setGeom((prev) => {
      const next = { ...(prev ?? { x: 0, y: 0, w: 360, h: 520 }), ...g }
      next.w = Math.max(MIN_W, next.w)
      next.h = Math.max(MIN_H, next.h)
      next.x = Math.max(MARGIN, next.x)
      next.y = Math.max(topInset() + MARGIN, next.y)
      return next
    })
  }, [])

  return { geom, ensurePlaced, onGeom }
}
