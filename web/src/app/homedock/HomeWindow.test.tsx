import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { HomeWindow } from './HomeWindow'

const tabs = [
  { id: 'a', seq: 1, machine: '' },
  { id: 'b', seq: 2, machine: '' },
]
const geom = { x: 100, y: 100, w: 600, h: 300 }
const base = () => ({
  tabs, activeId: 'a', geom,
  onGeom: vi.fn(), onActivate: vi.fn(), onNew: vi.fn(),
  onKill: vi.fn(), onCollapse: vi.fn(),
  renderTab: (t: { id: string }) => <div data-testid={`content-${t.id}`} />,
})

describe('HomeWindow', () => {
  it('只渲染激活 tab 的内容', () => {
    render(<HomeWindow {...base()} />)
    expect(screen.getByTestId('content-a')).toBeInTheDocument()
    expect(screen.queryByTestId('content-b')).toBeNull()
  })

  it('收起走 onCollapse，不走 onKill', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    fireEvent.click(screen.getByLabelText('收起（会话保留）'))
    expect(p.onCollapse).toHaveBeenCalledTimes(1)
    expect(p.onKill).not.toHaveBeenCalled()
  })

  it('tab 上的 × 走 onKill，且不误触发激活', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    fireEvent.click(screen.getByLabelText('关闭 bash · home 2'))
    expect(p.onKill).toHaveBeenCalledWith('b')
    expect(p.onActivate).not.toHaveBeenCalled()
  })

  it('拖标题栏改位置', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    const title = screen.getByTestId('home-window-title')
    fireEvent.pointerDown(title, { clientX: 0, clientY: 0 })
    fireEvent.pointerMove(document, { clientX: 40, clientY: 25 })
    fireEvent.pointerUp(document)
    expect(p.onGeom).toHaveBeenCalledWith(expect.objectContaining({ x: 140, y: 125 }))
  })

  it('拉右下角改尺寸', () => {
    const p = base()
    render(<HomeWindow {...p} />)
    fireEvent.pointerDown(screen.getByTestId('home-window-corner'), { clientX: 0, clientY: 0 })
    fireEvent.pointerMove(document, { clientX: 30, clientY: 20 })
    fireEvent.pointerUp(document)
    expect(p.onGeom).toHaveBeenCalledWith(expect.objectContaining({ w: 630, h: 320 }))
  })
})
