// Composer.test.tsx —— 对话式收口的状态联动与动作契约。
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { Composer } from './Composer'
import type { Task } from '../../api/types'

vi.mock('../../api/client', () => ({
  continueTask: vi.fn().mockResolvedValue({ ok: true }),
  doneTask: vi.fn().mockResolvedValue({ ok: true }),
  stopTask: vi.fn().mockResolvedValue({ worktree_removed: true }),
  resumeTask: vi.fn().mockResolvedValue({ forced: false, note: '' }),
}))
import { continueTask } from '../../api/client'

const task = (state: string) => ({ id: 't1', state } as Task)

describe('Composer', () => {
  beforeEach(() => vi.clearAllMocks())

  it('waiting_review：可输入，Enter 发送 continue，发送后清空', async () => {
    render(<Composer task={task('waiting_review')} disabled={false} onChanged={() => {}} />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: '补测试' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(continueTask).toHaveBeenCalledWith('t1', '补测试'))
    expect((box as HTMLTextAreaElement).value).toBe('')
  })

  it('running：输入禁用并说明原因；停止仍可用', () => {
    render(<Composer task={task('running')} disabled={false} onChanged={() => {}} />)
    expect(screen.getByRole('textbox')).toBeDisabled()
    expect(screen.getByText(/回合结束/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /停止任务/ })).toBeEnabled()
    expect(screen.queryByRole('button', { name: /完成任务/ })).not.toBeInTheDocument()
  })

  it('done 需二次确认才调接口', async () => {
    render(<Composer task={task('waiting_review')} disabled={false} onChanged={() => {}} />)
    fireEvent.click(screen.getByRole('button', { name: /完成任务/ }))
    expect(screen.getByText(/不可撤销/)).toBeInTheDocument()
  })

  it('waiting_answer：显示恢复执行与强制收口选项', () => {
    render(<Composer task={task('waiting_answer')} disabled={false} onChanged={() => {}} />)
    expect(screen.getByRole('button', { name: /恢复执行/ })).toBeInTheDocument()
    expect(screen.getByText(/强制收口/)).toBeInTheDocument()
  })

  it('终态：只读说明，无任何动作', () => {
    render(<Composer task={task('completed')} disabled={false} onChanged={() => {}} />)
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /停止任务/ })).not.toBeInTheDocument()
    expect(screen.getByText(/已归档|已终结|终态/)).toBeInTheDocument()
  })

  it('断线：全部禁用但保留已填内容', () => {
    render(<Composer task={task('waiting_review')} disabled onChanged={() => {}} />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: '还没发的话' } })
    expect(screen.getByRole('button', { name: /续发修改/ })).toBeDisabled()
    expect((box as HTMLTextAreaElement).value).toBe('还没发的话')
  })
})
