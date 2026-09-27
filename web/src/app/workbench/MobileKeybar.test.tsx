// 键条缝侧守卫：受控键集、序列载荷、点击上抛。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { KEYBAR_KEYS, MobileKeybar } from './MobileKeybar'

// 直发键 = seq 非空的键位；ctrl（seq 为空）是粘滞开关，不在这条通道上。
const DIRECT_KEYS = KEYBAR_KEYS.filter((k) => k.seq !== '')

describe('MobileKeybar', () => {
  it('渲染受控键集（与 KEYBAR_KEYS 一一对应）', () => {
    render(<MobileKeybar onKey={vi.fn()} />)
    expect(screen.getByTestId('mobile-keybar')).toBeInTheDocument()
    for (const key of KEYBAR_KEYS) {
      expect(screen.getByTestId(`keybar-${key.id}`)).toBeInTheDocument()
    }
  })

  it('点击送入对应终端序列（逐键，直发键集）', () => {
    const onKey = vi.fn()
    render(<MobileKeybar onKey={onKey} />)
    for (const key of DIRECT_KEYS) {
      fireEvent.click(screen.getByTestId(`keybar-${key.id}`))
      expect(onKey).toHaveBeenLastCalledWith(key.seq)
    }
    expect(onKey).toHaveBeenCalledTimes(DIRECT_KEYS.length)
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

  // —— B369.10：12 键扩集与粘滞 Ctrl ——
  it('键集扩到原型 12 键；既有 8 键 id/label/seq 冻结（B369.9 口径底线）', () => {
    expect(KEYBAR_KEYS).toHaveLength(12)
    const of = (id: string) => KEYBAR_KEYS.find((k) => k.id === id)
    expect(of('esc')).toMatchObject({ label: 'Esc', seq: '\x1b' })
    expect(of('ctrl-c')).toMatchObject({ label: '^C', seq: '\x03' })
    expect(of('tab')).toMatchObject({ label: 'Tab', seq: '\t' })
    expect(of('shift-tab')).toMatchObject({ label: '⇧Tab', seq: '\x1b[Z' })
    // 新增 4 键的序列（管道/波浪/退格；ctrl 的 seq 留空表示走粘滞开关）
    expect(of('ctrl')).toMatchObject({ seq: '' })
    expect(of('pipe')).toMatchObject({ label: '|', seq: '\x7c' })
    expect(of('tilde')).toMatchObject({ label: '~', seq: '\x7e' })
    expect(of('backspace')).toMatchObject({ label: '⌫', seq: '\x7f' })
  })

  it('ctrl 键位点击走 onCtrlToggle、不发 onKey（粘滞开关不是直发键）', () => {
    const onKey = vi.fn()
    const onCtrlToggle = vi.fn()
    render(<MobileKeybar onKey={onKey} onCtrlToggle={onCtrlToggle} />)
    fireEvent.click(screen.getByTestId('keybar-ctrl'))
    expect(onCtrlToggle).toHaveBeenCalledTimes(1)
    expect(onKey).not.toHaveBeenCalled()
  })

  it('armed 时 ctrl 键位加 on 态高亮类并报 aria-pressed；非 armed 不含', () => {
    const { rerender } = render(<MobileKeybar onKey={vi.fn()} />)
    const ctrl = screen.getByTestId('keybar-ctrl')
    expect(ctrl.className).not.toContain('bg-[#636366]')
    expect(ctrl.getAttribute('aria-pressed')).toBe('false')
    rerender(<MobileKeybar onKey={vi.fn()} ctrlArmed />)
    expect(ctrl.className).toContain('bg-[#636366]')
    expect(ctrl.getAttribute('aria-pressed')).toBe('true')
  })

  it('容器与键钮是暗色字面量（岔口 7：原型逐值转写）', () => {
    render(<MobileKeybar onKey={vi.fn()} />)
    expect(screen.getByTestId('mobile-keybar').className).toContain('bg-[#1c1c1e]')
    expect(screen.getByTestId('keybar-esc').className).toContain('bg-[#3a3a3c]')
  })
})
