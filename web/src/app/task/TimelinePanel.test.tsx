// TimelinePanel 的容器行为测试：hook 用 vi.mock 打桩，不发真实请求。
//
// 断言的都是「不静默」相关的行为：坏行计数在顶部、错误条带重试、到达文件头时
// 「加载更早」消失、上限提示、事件标记不可操作。样式一概不测。
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { Frame } from '../../api/types'
import type { FramesStream } from './useFramesStream'
import { useFramesStream } from './useFramesStream'
import { TimelinePanel } from './TimelinePanel'

vi.mock('./useFramesStream', async (orig) => ({
  ...(await orig<typeof import('./useFramesStream')>()),
  useFramesStream: vi.fn(),
}))
// RenderPanel 会真发 fetch，切到原始视图的用例里用一个哑桩顶替
vi.mock('./RenderPanel', () => ({ RenderPanel: () => <div>原始实况桩</div> }))

afterEach(cleanup)

const frame = (o: Partial<Frame> & { seq: number; type: string }): Frame =>
  ({ ts: '2026-08-12T10:00:00+08:00', turn: 1, ...o }) as Frame

const stream = (o: Partial<FramesStream> = {}): FramesStream => ({
  frames: [], badLines: 0, startOffset: 0, error: null,
  active: true, atCap: false, loadingEarlier: false,
  loadEarlier: vi.fn(), retry: vi.fn(),
  ...o,
})

describe('TimelinePanel', () => {
  it('无帧时显示「等待模型输出…」而不是报错', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream())
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByText(/等待模型输出/)).toBeInTheDocument()
  })

  it('坏行计数显示在顶部', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({ badLines: 2 }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByText(/2 行无法解析/)).toBeInTheDocument()
  })

  it('错误时给错误条 + 重试按钮，点了会调 retry', () => {
    const retry = vi.fn()
    vi.mocked(useFramesStream).mockReturnValue(stream({ error: '连接中断', retry }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByRole('alert')).toHaveTextContent('连接中断')
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(retry).toHaveBeenCalled()
  })

  it('startOffset=0 时「加载更早」消失（已到文件头）', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({
      startOffset: 0,
      frames: [frame({ seq: 1, type: 'text', part: 'p01', delta: 'a' })],
    }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.queryByRole('button', { name: /加载更早/ })).not.toBeInTheDocument()
  })

  it('startOffset>0 时「加载更早」出现，点了会调 loadEarlier', () => {
    const loadEarlier = vi.fn()
    vi.mocked(useFramesStream).mockReturnValue(stream({ startOffset: 65536, loadEarlier }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    fireEvent.click(screen.getByRole('button', { name: /加载更早/ }))
    expect(loadEarlier).toHaveBeenCalled()
  })

  it('到达帧数上限时提示改用 handoff frames，且不再提供「加载更早」', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({ atCap: true, startOffset: 65536 }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByText(/handoff frames/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /加载更早/ })).not.toBeInTheDocument()
  })

  it('回合锚点从已加载帧生成，并标注只覆盖已加载范围', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({
      startOffset: 65536,
      frames: [
        frame({ seq: 1, turn: 7, type: 'turn_start', reason: 'send' }),
        frame({ seq: 2, turn: 8, type: 'turn_start', reason: 'send' }),
      ],
    }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByRole('button', { name: '7' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '8' })).toBeInTheDocument()
    expect(screen.getByText(/更早的需先加载/)).toBeInTheDocument()
  })

  it('已到文件头时锚点提示改成「已覆盖全部回合」', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({
      startOffset: 0,
      frames: [frame({ seq: 1, turn: 1, type: 'turn_start', reason: 'dispatch' })],
    }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.getByText(/已覆盖全部回合/)).toBeInTheDocument()
  })

  it('时间线里的事件标记不可操作（整个面板里没有审批按钮）', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({
      frames: [frame({ seq: 1, type: 'event', ref_seq: 88, event: 'permission_request' })],
    }))
    render(<TimelinePanel taskId="t1" taskState="running" />)
    expect(screen.queryByRole('button', { name: /批准|拒绝/ })).not.toBeInTheDocument()
    expect(screen.getByText(/裁决入口在右侧工单区/)).toBeInTheDocument()
  })

  it('开关能切到原始实况正文，并能切回来', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream())
    render(<TimelinePanel taskId="t1" taskState="running" />)
    fireEvent.click(screen.getByRole('button', { name: /原始正文/ }))
    expect(screen.getByText('原始实况桩')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /回合时间线/ }))
    expect(screen.getByText(/等待模型输出/)).toBeInTheDocument()
  })

  it('流结束后徽章从「跟随中」变「已结束」', () => {
    vi.mocked(useFramesStream).mockReturnValue(stream({ active: false }))
    render(<TimelinePanel taskId="t1" taskState="completed" />)
    expect(screen.getByText('已结束')).toBeInTheDocument()
  })
})
