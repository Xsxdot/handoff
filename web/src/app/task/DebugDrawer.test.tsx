// DebugDrawer.test.tsx —— 调试抽屉：原始事件列表 + 原始正文两个子 tab。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { DebugDrawer } from './DebugDrawer'
import type { Event } from '../../api/types'

// RenderPanel 挂常驻流，测试里 mock 掉（按需连接语义由 RenderPanel 自己的实现保证）
vi.mock('./RenderPanel', () => ({ RenderPanel: () => <p>render-log-stream</p> }))

const events = [
  { seq: 23, type: 'progress', created_at: '2026-08-17T11:16:46Z', payload: { text: '会话就绪' } },
] as unknown as Event[]

describe('DebugDrawer', () => {
  it('默认展示原始事件（#seq/type/摘要）', () => {
    render(<DebugDrawer taskId="t1" events={events} status="open" error={null} onClose={() => {}} />)
    expect(screen.getByText(/#23/)).toBeInTheDocument()
    expect(screen.getByText('progress')).toBeInTheDocument()
    expect(screen.getByText('会话就绪')).toBeInTheDocument()
  })

  it('切到原始正文才挂 RenderPanel（按需连接）', () => {
    render(<DebugDrawer taskId="t1" events={events} status="open" error={null} onClose={() => {}} />)
    expect(screen.queryByText('render-log-stream')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /原始正文/ }))
    expect(screen.getByText('render-log-stream')).toBeInTheDocument()
  })
})
