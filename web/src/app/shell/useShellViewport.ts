// useShellViewport —— 控制台响应式断点谱系（spec 实现决定：竖屏手机 / 折叠展开 / pad）。
//
// 职责：把当前视口宽度折成三档 phone/pad/desktop，并在 resize 时更新。
// 边界：
//   - 不渲染、不取数、不路由；只回答「现在是哪一档」
//   - 判据用 innerWidth 而不是 CSS media query：Shell 要按档位**切换渲染结构**
//     （挂/不挂底栏、桌面栏、工作台覆盖层），这是 React 分支，不是样式分支。
//     jsdom 不实现 matchMedia（实测 undefined），innerWidth 才是可测且真实存在的判据
import { useEffect, useState } from 'react'

// PHONE_MAX_WIDTH 对应 Tailwind 的 md 断点下沿（48rem=768px）之下一档：
// ≤767 为竖屏手机，768–1023 为折叠展开/pad，>1023 为桌面。
export const PHONE_MAX_WIDTH = 767
export const PAD_MAX_WIDTH = 1023

export type ShellViewport = 'phone' | 'pad' | 'desktop'

// viewportOf 是纯分档函数，供组件与测试共用。
export function viewportOf(width: number): ShellViewport {
  if (width <= PHONE_MAX_WIDTH) return 'phone'
  if (width <= PAD_MAX_WIDTH) return 'pad'
  return 'desktop'
}

// isCompactViewport 回答「是否走移动壳布局」：phone 与 pad 都是紧凑档。
export function isCompactViewport(viewport: ShellViewport): boolean {
  return viewport !== 'desktop'
}

// readViewportWidth 读数；非浏览器环境（SSR/测试无 window）回退到桌面档，
// 宁可渲染桌面壳也不要抛。
export function readViewportWidth(): number {
  return typeof window === 'undefined' ? PAD_MAX_WIDTH + 1 : window.innerWidth
}

// useShellViewport 返回当前档位。只监听 resize：视口变化在移动端由旋转/分屏触发。
export function useShellViewport(): ShellViewport {
  const [viewport, setViewport] = useState<ShellViewport>(() => viewportOf(readViewportWidth()))
  useEffect(() => {
    const update = () => setViewport(viewportOf(readViewportWidth()))
    update()
    window.addEventListener('resize', update)
    return () => window.removeEventListener('resize', update)
  }, [])
  return viewport
}
