// 键条缝侧守卫：受控键集、序列载荷、点击上抛。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { KEYBAR_KEYS, MobileKeybar } from './MobileKeybar'

describe('MobileKeybar', () => {
  it('渲染受控键集（与 KEYBAR_KEYS 一一对应）', () => {
    render(<MobileKeybar onKey={vi.fn()} />)
    expect(screen.getByTestId('mobile-keybar')).toBeInTheDocument()
    for (const key of KEYBAR_KEYS) {
      expect(screen.getByTestId(`keybar-${key.id}`)).toBeInTheDocument()
    }
  })

  it('点击送入对应终端序列（逐键）', () => {
    const onKey = vi.fn()
    render(<MobileKeybar onKey={onKey} />)
    for (const key of KEYBAR_KEYS) {
      fireEvent.click(screen.getByTestId(`keybar-${key.id}`))
      expect(onKey).toHaveBeenLastCalledWith(key.seq)
    }
    expect(onKey).toHaveBeenCalledTimes(KEYBAR_KEYS.length)
  })

  it('mousedown 被 preventDefault（不抢 xterm 焦点）', () => {
    render(<MobileKeybar onKey={vi.fn()} />)
    const esc = screen.getByTestId('keybar-esc')
    const ev = new MouseEvent('mousedown', { bubbles: true, cancelable: true })
    esc.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
  })

  it('方向键序列是 CSI 标准形态', () => {
    expect(KEYBAR_KEYS.find((k) => k.id === 'up')?.seq).toBe('\x1b[A')
    expect(KEYBAR_KEYS.find((k) => k.id === 'left')?.seq).toBe('\x1b[D')
  })
})
