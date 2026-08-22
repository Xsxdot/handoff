// TuiHeader.test.tsx —— 两行页头：身份/动作行 + 遥测行（模型、回合下拉、ctx）。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { TuiHeader } from './TuiHeader'
import type { Task } from '../../api/types'

const task = {
  id: '7a8334f4-0000-0000-0000-000000000000',
  name: 'B93 基准评测', state: 'waiting_review', executor: 'opencode',
  actual_model: 'qwen3-coder',
  created_at: '2026-08-17T11:16:00Z', updated_at: '2026-08-17T14:31:00Z',
  usage: { context_tokens: 41236, context_window: 200000 },
  cumulative: { input_tokens: 182400, cached_tokens: 1210000, output_tokens: 96800, total_tokens: 1489200 },
} as unknown as Task

const base = {
  task, turns: [1, 2], turnsPartial: false, onJumpTurn: vi.fn(),
  reviewAvailable: true, reviewOpen: false, onToggleReview: vi.fn(), onOpenDebug: vi.fn(),
  wsStatus: 'open' as const, disconnected: false,
}

describe('TuiHeader', () => {
  it('第一行：任务名、状态徽章、审阅栏与调试按钮', () => {
    render(<TuiHeader {...base} />)
    expect(screen.getByText('B93 基准评测')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /审阅栏/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /调试/ })).toBeInTheDocument()
  })
  it('遥测行：executor、实际模型、ctx 读数', () => {
    render(<TuiHeader {...base} />)
    expect(screen.getByText(/opencode/)).toBeInTheDocument()
    expect(screen.getByText(/qwen3-coder/)).toBeInTheDocument()
    expect(screen.getByText(/41\.2k/)).toBeInTheDocument()
    expect(screen.getByText(/200\.0k/)).toBeInTheDocument()
  })
  it('回合下拉列出回合并回调跳转', () => {
    const onJumpTurn = vi.fn()
    render(<TuiHeader {...base} onJumpTurn={onJumpTurn} />)
    fireEvent.click(screen.getByRole('button', { name: /回合 2/ }))
    fireEvent.click(screen.getByRole('button', { name: /回合 1/ }))
    expect(onJumpTurn).toHaveBeenCalledWith(1)
  })
  it('turnsPartial 时下拉给出自动加载提示', () => {
    render(<TuiHeader {...base} turnsPartial />)
    fireEvent.click(screen.getByRole('button', { name: /回合 2/ }))
    expect(screen.getByText(/边跳边加载/)).toBeInTheDocument()
  })
  it('非 review 态不显示审阅栏按钮；无 usage 不渲染 ctx', () => {
    render(<TuiHeader {...base} reviewAvailable={false} task={{ ...task, usage: undefined } as Task} />)
    expect(screen.queryByRole('button', { name: /审阅栏/ })).not.toBeInTheDocument()
    expect(screen.queryByText(/ctx/)).not.toBeInTheDocument()
  })

  it('遥测行：有 timing 时挂出耗时 chip', () => {
    const withTiming = {
      ...base,
      task: { ...task, timing: {
        total_ms: 184_300, api_ms: 121_500, tool_ms: 71_200,
        tool_span_ms: 58_400, other_ms: 4_400, partial: false,
      } } as unknown as Task,
    }
    render(<TuiHeader {...withTiming} />)
    expect(screen.getByRole('button', { name: /耗时 3m4s/ })).toBeInTheDocument()
  })

  it('分隔点跟着耗时 chip 一起有无，不留悬空的「·」', () => {
    const dots = (t: string) => (t.match(/·/g) ?? []).length
    const plain = render(<TuiHeader {...base} />).container.textContent ?? ''
    expect(plain).not.toContain('耗时')

    const withTiming = render(
      <TuiHeader {...base} task={{ ...task, timing: {
        total_ms: 1000, api_ms: 700, tool_ms: 200,
        tool_span_ms: 200, other_ms: 100, partial: false,
      } } as unknown as Task} />,
    ).container.textContent ?? ''
    expect(withTiming).toContain('耗时')
    expect(dots(withTiming)).toBe(dots(plain) + 1)
  })
})
