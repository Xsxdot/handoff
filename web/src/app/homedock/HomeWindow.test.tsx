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

  it('点 + 调 onNew 时不传任何实参——传了就会变成 machine', () => {
    // why：onClick={onNew} 会把 MouseEvent 当第一个实参喂进
    // useHomeDock.newTerminal(machine?: string)，HomeTab.machine 存成事件对象，
    // 关会话时拼出 ?machine=[object Object] 当场炸。
    // TS 拦不住：(machine?: string) => void 对 () => void 是合法赋值
    const onNew = vi.fn()
    render(
      <HomeWindow
        tabs={[{ id: 'a', seq: 1, machine: '' }]}
        activeId="a"
        geom={{ x: 0, y: 0, w: 600, h: 300 }}
        onGeom={vi.fn()}
        onActivate={vi.fn()}
        onNew={onNew}
        onKill={vi.fn()}
        onCollapse={vi.fn()}
        renderTab={() => <div />}
      />,
    )
    fireEvent.click(screen.getByLabelText('新终端'))
    expect(onNew).toHaveBeenCalledTimes(1)
    expect(onNew.mock.calls[0]).toHaveLength(0)
  })
})
