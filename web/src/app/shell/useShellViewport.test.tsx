// 断点谱系的缝侧守卫：纯分档 + hook 真实视口读数（jsdom 默认 1024）。
// 不依赖 matchMedia（jsdom 不实现）；用 innerWidth，测试以 defineProperty 改写。
import { render, screen } from '@testing-library/react'
import { useEffect, useState } from 'react'
import { afterEach, describe, expect, it } from 'vitest'
import { isCompactViewport, PAD_MAX_WIDTH, PHONE_MAX_WIDTH, useShellViewport, viewportOf } from './useShellViewport'

function setWidth(width: number) {
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true, writable: true })
}

// Probe 把 hook 结果渲染出来——不用 renderHook，避免依赖版本差异。
function Probe() {
  const v = useShellViewport()
  const [seen, setSeen] = useState('')
  useEffect(() => { setSeen(`${v}/${isCompactViewport(v)}`) }, [v])
  return <span data-testid="vp">{seen}</span>
}

afterEach(() => setWidth(1024))

describe('useShellViewport', () => {
  it('分档：phone ≤767 / pad ≤1023 / desktop >1023', () => {
    expect(viewportOf(375)).toBe('phone')
    expect(viewportOf(PHONE_MAX_WIDTH)).toBe('phone')
    expect(viewportOf(PHONE_MAX_WIDTH + 1)).toBe('pad')
    expect(viewportOf(PAD_MAX_WIDTH)).toBe('pad')
    expect(viewportOf(PAD_MAX_WIDTH + 1)).toBe('desktop')
  })

  it('紧凑 = phone 或 pad', () => {
    expect(isCompactViewport('phone')).toBe(true)
    expect(isCompactViewport('pad')).toBe(true)
    expect(isCompactViewport('desktop')).toBe(false)
  })

  it('hook 按真实视口宽度取值（375 → phone）', async () => {
    setWidth(375)
    render(<Probe />)
    expect(await screen.findByText('phone/true')).toBeInTheDocument()
  })

  it('1024 宽判 desktop', async () => {
    setWidth(1024)
    render(<Probe />)
    expect(await screen.findByText('desktop/false')).toBeInTheDocument()
  })
})
