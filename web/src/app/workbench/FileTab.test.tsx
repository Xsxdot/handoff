import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { FileTab } from './FileTab'
import type { BaseDir } from './useWorkbench'
import { ApiError } from '../../api/client'
import { draftKey, loadDraft, saveDraft } from './fileDraft'

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
  return { ...actual, fetchWorkspaceFile: vi.fn(), writeWorkspaceFile: vi.fn() }
})
const { fetchWorkspaceFile, writeWorkspaceFile } = await import('../../api/client')

afterEach(() => {
  vi.mocked(fetchWorkspaceFile).mockReset()
  vi.mocked(writeWorkspaceFile).mockReset()
  // 草稿走 localStorage，用例之间不留残留——上一个用例留下的草稿会影响
  // 下一个用例「无 initial 挂载」的恢复路径
  localStorage.clear()
})

const TEXT = { content: 'module handoff\n', size: 15, sha256: 'basehash' }

describe('FileTab', () => {
  it('按基准目录 + 相对路径 + 机器名取文件并显示内容', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'module handoff\n', size: 15, sha256: 's1' })
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    expect(box).toHaveValue('module handoff\n')
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

  it('状态上报：读取正常报 ok，404 报 deleted，其他错误不报 deleted（2026-08-29 左栏圆点缝）', async () => {
    const onStatus = vi.fn()
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'a', size: 1, sha256: 'sa' })
    const { rerender } = render(<FileTab base={base} rel="a.txt" onStatus={onStatus} />)
    await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('a'))
    expect(onStatus).toHaveBeenLastCalledWith('ok')

    vi.mocked(fetchWorkspaceFile).mockRejectedValue(new ApiError(404, '文件不存在'))
    rerender(<FileTab base={base} rel="gone.txt" onStatus={onStatus} />)
    await waitFor(() => expect(onStatus).toHaveBeenLastCalledWith('deleted'))

    // 403 之类不是「被删」：不喊 deleted，保留上一次上报值
    const calls = onStatus.mock.calls.length
    vi.mocked(fetchWorkspaceFile).mockRejectedValue(new ApiError(403, '白名单外'))
    rerender(<FileTab base={base} rel="secret.txt" onStatus={onStatus} />)
    await waitFor(() => expect(screen.getByText(/白名单外/)).toBeInTheDocument())
    expect(onStatus).not.toHaveBeenLastCalledWith('deleted')
    expect(onStatus.mock.calls.length).toBeGreaterThanOrEqual(calls)
  })

  it('冲突（保存 409）报 conflict，解决（放弃改动）后回报 ok', async () => {
    const onStatus = vi.fn()
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ ...TEXT, sha256: 'old' })
    vi.mocked(writeWorkspaceFile).mockRejectedValue(
      new ApiError(409, '文件已在磁盘上变了', { current: { content: 'disk', size: 4, sha256: 'diskhash' } }),
    )
    render(<FileTab base={base} rel="go.mod" onStatus={onStatus} />)
    const box = await screen.findByRole('textbox')
    fireEvent.change(box, { target: { value: 'edited' } })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.getByText(/已在磁盘上变了/)).toBeInTheDocument())
    expect(onStatus).toHaveBeenLastCalledWith('conflict')

    fireEvent.click(screen.getByRole('button', { name: /放弃我的改动/ }))
    await waitFor(() => expect(onStatus).toHaveBeenLastCalledWith('ok'))
  })

  it('换文件时重新取数', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'a', size: 1, sha256: 'sa' })
    const { rerender } = render(<FileTab base={base} rel="a.txt" />)
    await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('a'))
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: 'b', size: 1, sha256: 'sb' })
    rerender(<FileTab base={base} rel="b.txt" />)
    await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('b'))
  })
})

describe('FileTab 编辑', () => {
  it('打字后出现脏标记，保存按钮从禁用变可点', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    expect(screen.getByRole('button', { name: /保存/ })).toBeDisabled()
    fireEvent.change(box, { target: { value: 'module handoff\nx' } })
    expect(screen.getByText('未保存')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /保存/ })).toBeEnabled()
  })

  it('保存成功后回基线：脏标记消失、按钮变灰、下一次保存用新哈希当 base', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockResolvedValue({ sha256: 'newhash', size: 16 })
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    fireEvent.change(box, { target: { value: 'module handoff\nx' } })
    fireEvent.click(screen.getByRole('button', { name: /保存/ }))
    await waitFor(() => expect(screen.queryByText('未保存')).not.toBeInTheDocument())
    expect(writeWorkspaceFile).toHaveBeenCalledWith(
      '/w/b2-b3', 'go.mod', { content: 'module handoff\nx', base_sha256: 'basehash' }, 'devbox',
    )
    // 再改一次，base 必须换成上一次返回的新哈希，而不是原始基线
    fireEvent.change(box, { target: { value: 'module handoff\nxy' } })
    fireEvent.click(screen.getByRole('button', { name: /保存/ }))
    await waitFor(() =>
      expect(vi.mocked(writeWorkspaceFile).mock.calls[1][2].base_sha256).toBe('newhash'),
    )
  })

  it('二进制：无编辑框、无保存按钮，说明为什么不能编辑', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ content: '', size: 49382, binary: true })
    render(<FileTab base={base} rel="logo.png" />)
    expect(await screen.findByText(/二进制文件，不支持在线编辑/)).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /保存/ })).not.toBeInTheDocument()
  })

  it('超限：显示真实大小与「仅显示开头」，只读', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({
      content: 'a'.repeat(100), size: 3_355_443, truncated: true,
    })
    render(<FileTab base={base} rel="fixtures.json" />)
    expect(await screen.findByText(/仅显示开头/)).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /保存/ })).not.toBeInTheDocument()
  })

  it('⌘S 在 tab 内触发保存', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockResolvedValue({ sha256: 'newhash', size: 16 })
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    fireEvent.change(box, { target: { value: 'module handoff\nx' } })
    fireEvent.keyDown(box, { key: 's', metaKey: true })
    await waitFor(() => expect(writeWorkspaceFile).toHaveBeenCalled())
  })

  it('⌘S 挂在 tab 容器上，容器外按不触发', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    render(<FileTab base={base} rel="go.mod" />)
    await screen.findByRole('textbox')
    fireEvent.keyDown(document.body, { key: 's', metaKey: true })
    expect(writeWorkspaceFile).not.toHaveBeenCalled()
  })

  it('保存失败时原文透传，草稿不丢', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockRejectedValue(new ApiError(400, '不允许写入 .git 目录'))
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    fireEvent.change(box, { target: { value: 'module handoff\nx' } })
    fireEvent.click(screen.getByRole('button', { name: /保存/ }))
    expect(await screen.findByText('不允许写入 .git 目录')).toBeInTheDocument()
    expect(box).toHaveValue('module handoff\nx')
  })
})

const CONFLICT = new ApiError(409, '文件已被改动', {
  error: '文件已被改动',
  current: { content: 'executor 改过的内容\n', size: 20, sha256: 'diskhash' },
})

describe('FileTab 冲突', () => {
  it('409 亮冲突条，两个出口都在', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockRejectedValue(CONFLICT)
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    await fireEvent.change(box, { target: { value: 'module handoff\nx' } })
    await fireEvent.click(screen.getByRole('button', { name: /^保存$/ }))
    expect(await screen.findByText(/文件已在磁盘上变了/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /放弃我的改动/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /用我的内容覆盖/ })).toBeInTheDocument()
  })

  it('放弃我的改动：草稿换成磁盘版本，基线换成磁盘哈希，脏标记清掉', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockRejectedValue(CONFLICT)
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    await fireEvent.change(box, { target: { value: 'module handoff\nx' } })
    await fireEvent.click(screen.getByRole('button', { name: /^保存$/ }))
    await fireEvent.click(await screen.findByRole('button', { name: /放弃我的改动/ }))
    expect(box).toHaveValue('executor 改过的内容\n')
    expect(screen.queryByText('未保存')).not.toBeInTheDocument()
    expect(screen.queryByText(/文件已在磁盘上变了/)).not.toBeInTheDocument()
  })

  it('用我的内容覆盖：二次确认后拿 current.sha256 当新 base 重发', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile)
      .mockRejectedValueOnce(CONFLICT)
      .mockResolvedValueOnce({ sha256: 'afterforce', size: 17 })
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    await fireEvent.change(box, { target: { value: 'module handoff\nx' } })
    await fireEvent.click(screen.getByRole('button', { name: /^保存$/ }))
    await fireEvent.click(await screen.findByRole('button', { name: /用我的内容覆盖/ }))
    // 二次确认：覆盖是不可逆的，而我们没有 watcher，用户在按保存之前从没被警告过
    await fireEvent.click(await screen.findByRole('button', { name: /确认覆盖/ }))
    await waitFor(() =>
      expect(vi.mocked(writeWorkspaceFile).mock.calls[1][2]).toEqual({
        content: 'module handoff\nx',
        base_sha256: 'diskhash', // 不是空串、不是原始 basehash
      }),
    )
  })

  it('覆盖时磁盘又变了：第二次照样 409，冲突条重新出现', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockRejectedValue(CONFLICT)
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    await fireEvent.change(box, { target: { value: 'module handoff\nx' } })
    await fireEvent.click(screen.getByRole('button', { name: /^保存$/ }))
    await fireEvent.click(await screen.findByRole('button', { name: /用我的内容覆盖/ }))
    await fireEvent.click(await screen.findByRole('button', { name: /确认覆盖/ }))
    expect(await screen.findByText(/文件已在磁盘上变了/)).toBeInTheDocument()
  })
})

describe('FileTab 草稿寄存', () => {
  it('卸载时把草稿回写出去，不是每敲一个字都回写', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    const onDraftChange = vi.fn()
    const { unmount } = render(
      <FileTab base={base} rel="go.mod" onDraftChange={onDraftChange} />,
    )
    const box = await screen.findByRole('textbox')
    fireEvent.change(box, { target: { value: 'module handoff\nabc' } })
    expect(onDraftChange).not.toHaveBeenCalled()
    unmount()
    expect(onDraftChange).toHaveBeenCalledWith({
      draft: 'module handoff\nabc',
      baseSha: 'basehash',
    })
  })

  it('干净时卸载回写 null，不留一份和磁盘一样的假草稿', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    const onDraftChange = vi.fn()
    const { unmount } = render(<FileTab base={base} rel="go.mod" onDraftChange={onDraftChange} />)
    await screen.findByRole('textbox')
    unmount()
    expect(onDraftChange).toHaveBeenCalledWith(null)
  })

  it('带 initial 挂载时直接用草稿，不等网络', async () => {
    vi.mocked(fetchWorkspaceFile).mockReturnValue(new Promise(() => {}))
    render(
      <FileTab
        base={base}
        rel="go.mod"
        initial={{ draft: '切走之前改的内容', baseSha: 'basehash' }}
        onDraftChange={vi.fn()}
      />,
    )
    expect(await screen.findByRole('textbox')).toHaveValue('切走之前改的内容')
    expect(screen.getByText('未保存')).toBeInTheDocument()
  })
})

describe('FileTab 草稿存 localStorage', () => {
  it('草稿写 localStorage 去抖 500ms，不是每按一次键写一次', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    render(<FileTab base={base} rel="go.mod" />)
    // 加载用真实 timer 完成；之后打字这一段用 fake timer 精确控制 500ms 边界
    await screen.findByRole('textbox')
    vi.useFakeTimers()
    try {
      const box = screen.getByRole('textbox')
      fireEvent.change(box, { target: { value: 'module handoff\nx' } })
      const key = draftKey('devbox', '/w/b2-b3', 'go.mod')
      expect(localStorage.getItem(key)).toBeNull()
      vi.advanceTimersByTime(499)
      expect(localStorage.getItem(key)).toBeNull()
      vi.advanceTimersByTime(1)
      expect(loadDraft(key)).toMatchObject({ draft: 'module handoff\nx', baseSha: 'basehash' })
    } finally {
      vi.useRealTimers()
    }
  })

  it('刷新后（重新挂载且无 initial）从 localStorage 恢复草稿', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    saveDraft(draftKey('devbox', '/w/b2-b3', 'go.mod'), 'module handoff\n草稿', 'basehash')
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    expect(box).toHaveValue('module handoff\n草稿')
    expect(screen.getByText('未保存')).toBeInTheDocument()
  })

  it('过期草稿（baseSha 与磁盘对不上）直接亮冲突条，走同一套 UI', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue({ ...TEXT, sha256: 'diskchanged' })
    saveDraft(draftKey('devbox', '/w/b2-b3', 'go.mod'), '我的草稿', 'basehash')
    render(<FileTab base={base} rel="go.mod" />)
    expect(await screen.findByText(/本地草稿基于的版本已经变了/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /放弃我的改动/ })).toBeInTheDocument()
  })

  it('保存成功后删掉 localStorage 里的草稿', async () => {
    vi.mocked(fetchWorkspaceFile).mockResolvedValue(TEXT)
    vi.mocked(writeWorkspaceFile).mockResolvedValue({ sha256: 'newhash', size: 16 })
    const key = draftKey('devbox', '/w/b2-b3', 'go.mod')
    saveDraft(key, 'module handoff\nx', 'basehash')
    render(<FileTab base={base} rel="go.mod" />)
    const box = await screen.findByRole('textbox')
    fireEvent.change(box, { target: { value: 'module handoff\nx' } })
    fireEvent.click(screen.getByRole('button', { name: /保存/ }))
    await waitFor(() => expect(loadDraft(key)).toBeNull())
  })
})
