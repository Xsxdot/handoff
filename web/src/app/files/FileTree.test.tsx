import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { FileTree } from './FileTree'
import type { BaseDir } from '../workbench/useWorkbench'

const base: BaseDir = {
  key: '/w/b2-b3',
  kind: 'workspace',
  path: '/w/b2-b3',
  label: 'integration/b2-b3',
  projectName: 'handoff',
  machine: '',
}

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, fetchWorkspaceDir: vi.fn(), fetchTaskDiff: vi.fn() }
})
const { fetchWorkspaceDir, fetchTaskDiff } = await import('../../api/client')

afterEach(() => {
  vi.mocked(fetchWorkspaceDir).mockReset()
  vi.mocked(fetchTaskDiff).mockReset()
})

function dir(entries: { name: string; is_dir: boolean }[]) {
  return { entries: entries.map((e) => ({ ...e, size: 0 })) }
}

describe('FileTree', () => {
  it('头部有「文件」与刷新，根标题是当前目录名', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'Makefile', is_dir: false }]))
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} />)
    expect(screen.getByText('文件')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '刷新' })).toBeInTheDocument()
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('Makefile')).toBeInTheDocument())
  })

  it('点文件回调相对路径', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'Makefile', is_dir: false }]))
    const onOpenFile = vi.fn()
    render(<FileTree base={base} taskId={null} onOpenFile={onOpenFile} />)
    await waitFor(() => expect(screen.getByText('Makefile')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Makefile'))
    expect(onOpenFile).toHaveBeenCalledWith('Makefile')
  })

  it('展开目录时按需取下一层，路径拼接正确', async () => {
    vi.mocked(fetchWorkspaceDir).mockImplementation(async (_p, rel) => {
      if (!rel) return dir([{ name: 'internal', is_dir: true }])
      if (rel === 'internal') return dir([{ name: 'agentd', is_dir: true }])
      return dir([{ name: 'server.go', is_dir: false }])
    })
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('internal')).toBeInTheDocument())
    fireEvent.click(screen.getByText('internal'))
    await waitFor(() => expect(screen.getByText('agentd')).toBeInTheDocument())
    fireEvent.click(screen.getByText('agentd'))
    await waitFor(() => expect(screen.getByText('server.go')).toBeInTheDocument())
    expect(fetchWorkspaceDir).toHaveBeenCalledWith('/w/b2-b3', 'internal/agentd', undefined)
  })

  it('M 角标的 tooltip 说的是「相对基线已改动」，不是 git status', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'Makefile', is_dir: false }]))
    vi.mocked(fetchTaskDiff).mockResolvedValue({ diff: 'diff --git a/Makefile b/Makefile' })
    render(<FileTree base={base} taskId="T1" onOpenFile={vi.fn()} />)
    const badge = await screen.findByText('M')
    expect(badge.getAttribute('title')).toContain('相对基线已改动')
    expect(badge.getAttribute('title')).not.toContain('工作区已修改')
  })

  it('没有任务的目录不显示角标，也不去取 diff', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'Makefile', is_dir: false }]))
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Makefile')).toBeInTheDocument())
    expect(screen.queryByText('M')).not.toBeInTheDocument()
    expect(fetchTaskDiff).not.toHaveBeenCalled()
  })

  it('搜索框只做前端过滤，不发请求', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(
      dir([
        { name: 'Makefile', is_dir: false },
        { name: 'go.mod', is_dir: false },
      ]),
    )
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('go.mod')).toBeInTheDocument())
    const before = vi.mocked(fetchWorkspaceDir).mock.calls.length
    fireEvent.change(screen.getByPlaceholderText('搜索文件…'), { target: { value: 'make' } })
    expect(screen.queryByText('go.mod')).not.toBeInTheDocument()
    expect(screen.getByText('Makefile')).toBeInTheDocument()
    expect(vi.mocked(fetchWorkspaceDir).mock.calls.length).toBe(before)
  })

  it('某一层取数失败时只有那一层显示原文，整棵树仍可用', async () => {
    vi.mocked(fetchWorkspaceDir).mockImplementation(async (_p, rel) => {
      if (!rel) {
        return dir([
          { name: 'secret', is_dir: true },
          { name: 'ok.txt', is_dir: false },
        ])
      }
      throw new Error('路径不在已探测到的工作树白名单内')
    })
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('secret')).toBeInTheDocument())
    fireEvent.click(screen.getByText('secret'))
    await waitFor(() => expect(screen.getByText(/白名单内/)).toBeInTheDocument())
    expect(screen.getByText('ok.txt')).toBeInTheDocument()
  })

  it('文件图标着强调色，文件夹图标保持次要灰', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(
      dir([
        { name: 'src', is_dir: true },
        { name: 'a.go', is_dir: false },
      ]),
    )
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} />)
    const fileIcon = await screen.findByTestId('file-icon')
    const dirIcon = screen.getByTestId('dir-icon')
    expect(fileIcon.getAttribute('class')).toMatch(/text-file-accent/)
    expect(dirIcon.getAttribute('class')).toMatch(/text-muted-foreground/)
  })

  it('M 标记用状态 token，不用裸 Tailwind 调色板类', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'a.go', is_dir: false }]))
    vi.mocked(fetchTaskDiff).mockResolvedValue({ diff: 'diff --git a/a.go b/a.go' })
    render(<FileTree base={base} taskId="T1" onOpenFile={vi.fn()} />)
    const mark = await screen.findByText('M')
    expect(mark.className).toMatch(/text-state-intervention-text/)
    expect(mark.className).not.toMatch(/amber-\d/)
  })
})
