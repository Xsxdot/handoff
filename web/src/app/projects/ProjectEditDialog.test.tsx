import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { ProjectNode } from '../../api/types'
import { patchProject } from '../../api/client'
import { ProjectEditDialog } from './ProjectEditDialog'

vi.mock('../../api/client', () => ({ patchProject: vi.fn() }))

// projectNode 造一个两位置的 ProjectNode：本机（''）+ 远程（'devbox'）。
// 覆盖「本机不带 machine、远程带 machine」两条调用形态。本机带 2 个工作树，
// 供改路径的后果文案断言 N 使用。
function projectNode(): ProjectNode {
  return {
    project_id: 'p1',
    origin_url: 'git@x:/handoff.git',
    name: 'handoff',
    locations: [
      {
        machine: '',
        name: 'handoff',
        path: '/w',
        workspaces: [
          { path: '/w', branch: 'main', head: 'abc', is_main: true, managed: false },
          { path: '/w/b2-b3', branch: 'integration/b2-b3', head: 'def', is_main: false, managed: true },
        ],
        probe_error: '',
      },
      {
        machine: 'devbox',
        name: 'handoff-devbox',
        path: '/srv/handoff',
        workspaces: [],
        probe_error: '',
      },
    ],
  }
}

function renderDialog(project: ProjectNode | null = projectNode()) {
  const onClose = vi.fn()
  const onDone = vi.fn()
  const view = render(<ProjectEditDialog open project={project} onClose={onClose} onDone={onDone} />)
  return { onClose, onDone, ...view }
}

describe('ProjectEditDialog', () => {
  it('open=false 时不渲染', () => {
    render(<ProjectEditDialog open={false} project={null} onClose={vi.fn()} onDone={vi.fn()} />)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('弹层标题是项目名，按 location 分 tab，输入框预填当前值', () => {
    renderDialog()
    expect(screen.getByRole('heading', { name: 'handoff' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '本机' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'devbox' })).toBeInTheDocument()
    // 默认停在第一个 location（本机），引用名与路径都预填登记值
    expect((screen.getByLabelText('本机 引用名') as HTMLInputElement).value).toBe('handoff')
    expect((screen.getByLabelText('本机 路径') as HTMLInputElement).value).toBe('/w')
    fireEvent.click(screen.getByRole('tab', { name: 'devbox' }))
    expect((screen.getByLabelText('devbox 引用名') as HTMLInputElement).value).toBe('handoff-devbox')
    expect((screen.getByLabelText('devbox 路径') as HTMLInputElement).value).toBe('/srv/handoff')
  })

  it('机器维度不可编辑，给出理由文案', () => {
    renderDialog()
    expect(screen.getByText(/机器维度不可编辑/)).toBeInTheDocument()
    expect(screen.getByText(/注销后重新添加/)).toBeInTheDocument()
  })

  it('无改动时「本次改动」为空且保存禁用', () => {
    renderDialog()
    expect(screen.getByText('本次改动')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
  })

  it('改一个字段 → 列出 1 项；改回原值 → 回到 0 项且保存禁用', () => {
    renderDialog()
    const nameInput = screen.getByLabelText('本机 引用名') as HTMLInputElement
    fireEvent.change(nameInput, { target: { value: 'handoff-new' } })
    expect(screen.getByText(/handoff → handoff-new/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存' })).toBeEnabled()
    // 改回原值：改动清空，保存回到禁用
    fireEvent.change(nameInput, { target: { value: 'handoff' } })
    expect(screen.queryByText(/handoff → handoff-new/)).toBeNull()
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
  })

  it('改路径时后果说明带该 location 已登记的工作树数', () => {
    renderDialog()
    fireEvent.change(screen.getByLabelText('本机 路径'), { target: { value: '/w-new' } })
    expect(screen.getByText(/已登记 2 个工作树/)).toBeInTheDocument()
    expect(screen.getByText(/新的派发将使用新路径/)).toBeInTheDocument()
  })

  it('保存时对每个有改动的 location 各发一次 patchProject：本机不带 machine、远程带 machine', async () => {
    vi.mocked(patchProject).mockResolvedValue({
      project_id: 'p1', name: 'handoff-new', path: '/w', origin_url: '', created_at: '',
    })
    const { onDone } = renderDialog()
    fireEvent.change(screen.getByLabelText('本机 引用名'), { target: { value: 'handoff-new' } })
    fireEvent.click(screen.getByRole('tab', { name: 'devbox' }))
    fireEvent.change(screen.getByLabelText('devbox 路径'), { target: { value: '/srv/handoff2' } })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(patchProject).toHaveBeenCalledTimes(2))
    // 本机：旧名寻址、只带改了的字段、machine 不传
    expect(patchProject).toHaveBeenCalledWith('handoff', { new_name: 'handoff-new' }, undefined)
    // 远程：带 machine=devbox
    expect(patchProject).toHaveBeenCalledWith('handoff-devbox', { path: '/srv/handoff2' }, 'devbox')
    expect(onDone).toHaveBeenCalled()
  })

  it('某台失败时逐条列出每台结果，成功的那台不回滚', async () => {
    vi.mocked(patchProject)
      .mockResolvedValueOnce({ project_id: 'p1', name: 'handoff-new', path: '/w', origin_url: '', created_at: '' })
      .mockRejectedValueOnce(new Error('agentd: dial tcp 10.0.0.8:7777: connect: connection refused'))
    const { onDone } = renderDialog()
    fireEvent.change(screen.getByLabelText('本机 引用名'), { target: { value: 'handoff-new' } })
    fireEvent.click(screen.getByRole('tab', { name: 'devbox' }))
    fireEvent.change(screen.getByLabelText('devbox 路径'), { target: { value: '/srv/handoff2' } })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    // 失败项透传 agentd 错误原文
    await waitFor(() => expect(screen.getByText(/connection refused/)).toBeInTheDocument())
    // 成功的那台保持「已保存」，不被失败项带垮
    expect(screen.getByText(/已保存/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /重试/ })).toBeInTheDocument()
    // 有成功项 → onDone
    expect(onDone).toHaveBeenCalled()
  })

  it('Esc 关闭弹层', () => {
    const { onClose } = renderDialog()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })
})
