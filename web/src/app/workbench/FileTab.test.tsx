import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { FileTab } from './FileTab'
import type { BaseDir } from './useWorkbench'
import { ApiError } from '../../api/client'

const base: BaseDir = {
  key: '/w/b2-b3',
  kind: 'workspace',
  path: '/w/b2-b3',
  label: 'b2-b3',
  projectName: 'handoff',
  machine: 'devbox',
}

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchWorkspaceFile: vi.fn() }
})
const { fetchWorkspaceFile } = await import('../../api/client')

afterEach(() => vi.mocked(fetchWorkspaceFile).mockReset())

describe('FileTab', () => {
  it('按基准目录 + 相对路径 + 机器名取文件并显示内容', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'module handoff\n' })
    render(<FileTab base={base} rel="go.mod" />)
    await waitFor(() => expect(screen.getByText(/module handoff/)).toBeInTheDocument())
    expect(fetchWorkspaceFile).toHaveBeenCalledWith('/w/b2-b3', 'go.mod', 'devbox')
  })

  it('加载中先给出提示而不是空白', () => {
    vi.mocked(fetchWorkspaceFile).mockReturnValue(new Promise(() => {}))
    render(<FileTab base={base} rel="go.mod" />)
    expect(screen.getByText(/正在读取/)).toBeInTheDocument()
  })

  it('agentd 的中文错误原文透传，不吞成「操作失败」', async () => {
    vi.mocked(fetchWorkspaceFile).mockRejectedValue(new ApiError(403, '路径不在已探测到的工作树白名单内'))
    render(<FileTab base={base} rel="../../etc/passwd" />)
    await waitFor(() =>
      expect(screen.getByText(/路径不在已探测到的工作树白名单内/)).toBeInTheDocument(),
    )
  })

  it('本期只读：不渲染保存按钮，且明示只读', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'x' })
    render(<FileTab base={base} rel="a.txt" />)
    await waitFor(() => expect(screen.getByText('x')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /保存/ })).not.toBeInTheDocument()
    expect(screen.getByText(/只读/)).toBeInTheDocument()
  })

  it('换文件时重新取数', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'a' })
    const { rerender } = render(<FileTab base={base} rel="a.txt" />)
    await waitFor(() => expect(screen.getByText('a')).toBeInTheDocument())
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'b' })
    rerender(<FileTab base={base} rel="b.txt" />)
    await waitFor(() => expect(screen.getByText('b')).toBeInTheDocument())
  })
})
