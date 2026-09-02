import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
  return {
    ...actual,
    fetchWorkspaceDir: vi.fn(),
    fetchTaskDiff: vi.fn(),
    createWorkspaceEntry: vi.fn(),
    copyWorkspaceEntry: vi.fn(),
    renameWorkspaceEntry: vi.fn(),
    deleteWorkspaceEntry: vi.fn(),
    searchWorkspace: vi.fn(),
    revealInFinder: vi.fn(),
  }
})
const {
  fetchWorkspaceDir,
  fetchTaskDiff,
  createWorkspaceEntry,
  copyWorkspaceEntry,
  renameWorkspaceEntry,
  deleteWorkspaceEntry,
  searchWorkspace,
  revealInFinder,
} = await import('../../api/client')

let restoreHostname: (() => void) | null = null

afterEach(() => {
  restoreHostname?.()
  restoreHostname = null
  vi.mocked(fetchWorkspaceDir).mockReset()
  vi.mocked(fetchTaskDiff).mockReset()
  vi.mocked(createWorkspaceEntry).mockReset()
  vi.mocked(copyWorkspaceEntry).mockReset()
  vi.mocked(renameWorkspaceEntry).mockReset()
  vi.mocked(deleteWorkspaceEntry).mockReset()
  vi.mocked(searchWorkspace).mockReset()
  vi.mocked(revealInFinder).mockReset()
})

// 默认目录列举：一个目录 internal + 一个文件 go.mod。想覆盖的用例自己再设
// mock；没设的（右键菜单那批）走这里。
beforeEach(() => {
  vi.mocked(fetchWorkspaceDir).mockResolvedValue(
    dir([
      { name: 'internal', is_dir: true },
      { name: 'go.mod', is_dir: false },
    ]),
  )
})

function dir(entries: { name: string; is_dir: boolean; ignored?: boolean }[]) {
  return { entries: entries.map((e) => ({ ...e, size: 0 })) }
}

// stubHostname 临时替换 window.location.hostname，由 afterEach 统一还原。
// jsdom 默认 hostname 是 'localhost'，本批用例正靠它当「本机」用；stub 时
// 换成一个克隆体再覆盖 hostname，不动原宿主对象，还原即换回。
function stubHostname(host: string): void {
  const original = window.location
  const stub = { ...original, hostname: host }
  Object.defineProperty(window, 'location', { configurable: true, get: () => stub })
  restoreHostname = () => {
    Object.defineProperty(window, 'location', { configurable: true, get: () => original })
  }
}

// renderTree 是这批用例的默认渲染辅助：machine 按 opts 覆盖，
// revealSupported 默认 true（本机 macOS 最常态），onOpenFile/onOpenTerminal
// 都传 vi.fn() 并返回，方便用例断言回调。
function renderTree(opts?: { machine?: string; revealSupported?: boolean | null }) {
  const onOpenFile = vi.fn()
  const onOpenTerminal = vi.fn()
  const b = opts?.machine !== undefined ? { ...base, machine: opts.machine } : base
  const revealSupported = opts?.revealSupported !== undefined ? opts.revealSupported : true
  render(
    <FileTree
      base={b}
      taskId={null}
      onOpenFile={onOpenFile}
      onOpenTerminal={onOpenTerminal}
      revealSupported={revealSupported}
    />,
  )
  return { onOpenFile, onOpenTerminal }
}

describe('FileTree', () => {
  it('头部有「文件」与刷新，根标题是当前目录名', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'Makefile', is_dir: false }]))
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
    expect(screen.getByText('文件')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '刷新' })).toBeInTheDocument()
    expect(screen.getByText('integration/b2-b3')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('Makefile')).toBeInTheDocument())
  })

  it('抽屉模式显示关闭入口，默认右栏不显示', () => {
    const onClose = vi.fn()
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} onClose={onClose} />)
    fireEvent.click(screen.getByRole('button', { name: '关闭文件抽屉' }))
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('refreshKey 变化只重取根目录，不把已展开的层全部刷新', async () => {
    const calls: string[] = []
    vi.mocked(fetchWorkspaceDir).mockImplementation(async (_path, rel) => {
      calls.push(rel ?? '')
      if (!rel) return dir([{ name: 'internal', is_dir: true }])
      return dir([{ name: 'server.go', is_dir: false }])
    })
    const props = {
      base,
      taskId: null,
      onOpenFile: vi.fn(),
      onOpenTerminal: vi.fn(),
      revealSupported: true,
    }
    const { rerender } = render(<FileTree {...props} refreshKey={0} />)
    await waitFor(() => expect(screen.getByText('internal')).toBeInTheDocument())
    fireEvent.click(screen.getByText('internal'))
    await waitFor(() => expect(screen.getByText('server.go')).toBeInTheDocument())

    rerender(<FileTree {...props} refreshKey={1} />)
    await waitFor(() => expect(calls.filter((rel) => rel === '').length).toBe(2))
    expect(calls.filter((rel) => rel === 'internal')).toHaveLength(1)
  })

  it('点文件回调相对路径', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'Makefile', is_dir: false }]))
    const onOpenFile = vi.fn()
    render(<FileTree base={base} taskId={null} onOpenFile={onOpenFile} onOpenTerminal={vi.fn()} revealSupported={true} />)
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
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
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
    render(<FileTree base={base} taskId="T1" onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
    const badge = await screen.findByText('M')
    expect(badge.getAttribute('title')).toContain('相对基线已改动')
    expect(badge.getAttribute('title')).not.toContain('工作区已修改')
  })

  it('没有任务的目录不显示角标，也不去取 diff', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'Makefile', is_dir: false }]))
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
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
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
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
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
    await waitFor(() => expect(screen.getByText('secret')).toBeInTheDocument())
    fireEvent.click(screen.getByText('secret'))
    await waitFor(() => expect(screen.getByText(/白名单内/)).toBeInTheDocument())
    expect(screen.getByText('ok.txt')).toBeInTheDocument()
  })

  it('未改动的文件与文件夹图标同为次要灰——颜色只留给状态', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(
      dir([
        { name: 'src', is_dir: true },
        { name: 'a.go', is_dir: false },
      ]),
    )
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
    const fileIcon = await screen.findByTestId('file-icon')
    const dirIcon = screen.getByTestId('dir-icon')
    expect(fileIcon.getAttribute('class')).toMatch(/text-muted-foreground/)
    expect(fileIcon.getAttribute('class')).not.toMatch(/text-file-accent/)
    expect(dirIcon.getAttribute('class')).toMatch(/text-muted-foreground/)
  })

  it('M 标记用状态 token，不用裸 Tailwind 调色板类', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'a.go', is_dir: false }]))
    vi.mocked(fetchTaskDiff).mockResolvedValue({ diff: 'diff --git a/a.go b/a.go' })
    render(<FileTree base={base} taskId="T1" onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
    const mark = await screen.findByText('M')
    expect(mark.className).toMatch(/text-state-intervention-text/)
    expect(mark.className).not.toMatch(/amber-\d/)
  })

  it('新增文件：文件名与角标都走新增色，字母是 A', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'new.go', is_dir: false }]))
    vi.mocked(fetchTaskDiff).mockResolvedValue({
      diff: ['diff --git a/new.go b/new.go', 'new file mode 100644'].join('\n'),
    })
    render(<FileTree base={base} taskId="T1" onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
    const mark = await screen.findByText('A')
    expect(mark.className).toMatch(/text-state-active/)
    expect(screen.getByText('new.go').className).toMatch(/text-state-active/)
  })

  it('有改动时文件图标跟着状态染色', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'a.go', is_dir: false }]))
    vi.mocked(fetchTaskDiff).mockResolvedValue({ diff: 'diff --git a/a.go b/a.go' })
    render(<FileTree base={base} taskId="T1" onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
    await screen.findByText('M')
    const icon = screen.getByTestId('file-icon')
    expect(icon.getAttribute('class')).toMatch(/text-state-intervention-text/)
  })

  it('被 .gitignore 排除的条目弱化显示，文件与目录都带 ⊘ 说明', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(
      dir([
        { name: 'node_modules', is_dir: true, ignored: true },
        { name: 'coverage.out', is_dir: false, ignored: true },
        { name: 'main.go', is_dir: false },
      ]),
    )
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
    await waitFor(() => expect(screen.getByText('main.go')).toBeInTheDocument())
    expect(screen.getByText('node_modules').className).toMatch(/italic/)
    expect(screen.getByText('coverage.out').className).toMatch(/italic/)
    // 未被忽略的文件保持正常字重与颜色
    expect(screen.getByText('main.go').className).not.toMatch(/italic/)
    expect(screen.getAllByTitle(/被 \.gitignore 排除/)).toHaveLength(2)
  })

  it('条目没有 ignored 键时按未忽略渲染——缺席不等于「查过且是垃圾」', async () => {
    vi.mocked(fetchWorkspaceDir).mockResolvedValue(dir([{ name: 'main.go', is_dir: false }]))
    render(<FileTree base={base} taskId={null} onOpenFile={vi.fn()} onOpenTerminal={vi.fn()} revealSupported={true} />)
    await waitFor(() => expect(screen.getByText('main.go')).toBeInTheDocument())
    expect(screen.queryByTitle(/被 \.gitignore 排除/)).toBeNull()
  })

  it('目录行的菜单有「折叠文件夹」，文件行没有', async () => {
    renderTree()
    await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
    expect(screen.getByRole('menuitem', { name: '折叠文件夹' })).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')
    await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
    expect(screen.queryByRole('menuitem', { name: '折叠文件夹' })).not.toBeInTheDocument()
  })

  it('远程目录：Reveal in Finder 置灰，理由点名 machine', async () => {
    renderTree({ machine: 'devbox', revealSupported: true })
    await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
    const item = screen.getByRole('menuitem', { name: /Reveal in Finder/ })
    expect(item).toBeDisabled()
    expect(item.getAttribute('title')).toContain('远程目录无法在本机的访达中打开（machine: devbox）')
  })

  it('通过网络访问 agentd：置灰，理由说访达会开在 agentd 那台机器上', async () => {
    // location.hostname stub 成 '100.73.238.21'，base.machine 传 ''，revealSupported 传 true
    stubHostname('100.73.238.21')
    renderTree({ revealSupported: true })
    await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
    const item = screen.getByRole('menuitem', { name: /Reveal in Finder/ })
    expect(item).toBeDisabled()
    expect(item.getAttribute('title')).toContain('你在通过网络访问这台 agentd，访达会开在 agentd 那台机器上')
  })

  it('平台不支持：置灰，理由说仅 macOS', async () => {
    renderTree({ revealSupported: false })
    await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
    const item = screen.getByRole('menuitem', { name: /Reveal in Finder/ })
    expect(item).toBeDisabled()
    expect(item.getAttribute('title')).toContain('这台机器的系统不支持在访达中显示（仅 macOS）')
  })

  it('本机 + macOS：可点，点了调 revealInFinder', async () => {
    stubHostname('localhost')
    renderTree({ revealSupported: true })
    await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
    const item = screen.getByRole('menuitem', { name: /Reveal in Finder/ })
    expect(item).not.toBeDisabled()
    await userEvent.click(item)
    expect(revealInFinder).toHaveBeenCalledWith(base.path, 'go.mod')
  })

  it('能力位为 null 时照常放行（三态门不禁用）', async () => {
    renderTree({ revealSupported: null })
    await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
    const item = screen.getByRole('menuitem', { name: /Reveal in Finder/ })
    expect(item).not.toBeDisabled()
  })

  it('reveal 失败时把服务端原文透传到面板', async () => {
    vi.mocked(revealInFinder).mockRejectedValue(new Error('你在通过网络访问这台 agentd，访达会开在 agentd 那台机器上'))
    stubHostname('localhost')
    renderTree({ revealSupported: true })
    await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: /Reveal in Finder/ }))
    expect(await screen.findByText(/通过网络访问这台 agentd/)).toBeInTheDocument()
  })

  it('删除确认必须点名「未跟踪的文件删除后无法恢复」', async () => {
    renderTree()
    await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: '删除' }))
    expect(screen.getByText(/未被 git 跟踪的文件删除后无法恢复/)).toBeInTheDocument()
  })

  it('服务端的中文错误原文被显示出来，不吞成「操作失败」', async () => {
    vi.mocked(deleteWorkspaceEntry).mockRejectedValue(new Error('不允许写入 .git 目录'))
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    renderTree()
    await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: '删除' }))
    await userEvent.click(screen.getByRole('button', { name: '删除' }))
    expect(await screen.findByText(/不允许写入 \.git 目录/)).toBeInTheDocument()
    expect(warn).toHaveBeenCalledWith('file_tree.delete.failed', expect.objectContaining({
      project: 'handoff', machine: '', path: '/w/b2-b3', rel: 'go.mod',
    }))
    warn.mockRestore()
  })

  it('新建成功后只刷新该层目录', async () => {
    const calls: string[] = []
    vi.mocked(fetchWorkspaceDir).mockImplementation(async (_p: string, rel?: string) => {
      calls.push(rel ?? '')
      if (!rel) return dir([{ name: 'internal', is_dir: true }])
      return dir([])
    })
    vi.mocked(createWorkspaceEntry).mockResolvedValue({ name: 'x.go', is_dir: false, size: 0 })
    renderTree()
    await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: '新文件' }))
    await userEvent.type(screen.getByLabelText('名称'), 'x.go')
    await userEvent.click(screen.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(calls.filter((r) => r === 'internal')).toHaveLength(1))
  })

  it('名字含 / 时保存按钮禁用并给出理由', async () => {
    renderTree()
    await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: '新文件' }))
    await userEvent.type(screen.getByLabelText('名称'), 'a/b.go')
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
    expect(screen.getByText(/名字不能包含/)).toBeInTheDocument()
  })

  it('删除目录后刷新的是父层，不是目录自身', async () => {
    // 回归锚：目录条目挂在父层列表里，删掉它要刷新父层（rel==''），
    // 刷新目录自身只会让被删条目在新列表里 404
    const calls: string[] = []
    vi.mocked(fetchWorkspaceDir).mockImplementation(async (_p: string, rel?: string) => {
      calls.push(rel ?? '')
      if (!rel) return dir([{ name: 'internal', is_dir: true }])
      return dir([])
    })
    vi.mocked(deleteWorkspaceEntry).mockResolvedValue({ ok: true })
    renderTree()
    await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: '删除' }))
    await userEvent.click(screen.getByRole('button', { name: '删除' }))
    // 挂载时取过根层一次，删除成功后应再次刷新根层
    await waitFor(() => expect(calls.filter((r) => r === '')).toHaveLength(2))
    expect(calls.filter((r) => r === 'internal')).toHaveLength(0)
  })

  it('重命名目录后刷新的是父层，不是目录自身', async () => {
    const calls: string[] = []
    vi.mocked(fetchWorkspaceDir).mockImplementation(async (_p: string, rel?: string) => {
      calls.push(rel ?? '')
      if (!rel) return dir([{ name: 'internal', is_dir: true }])
      return dir([])
    })
    vi.mocked(renameWorkspaceEntry).mockResolvedValue({ name: 'internal2', is_dir: true, size: 0 })
    renderTree()
    await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: '重命名' }))
    await userEvent.type(screen.getByLabelText('名称'), 'internal2')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(calls.filter((r) => r === '')).toHaveLength(2))
    expect(calls.filter((r) => r === 'internal')).toHaveLength(0)
  })

  it('复制目录后刷新的是父层，不是目录自身', async () => {
    const calls: string[] = []
    vi.mocked(fetchWorkspaceDir).mockImplementation(async (_p: string, rel?: string) => {
      calls.push(rel ?? '')
      if (!rel) return dir([{ name: 'internal', is_dir: true }])
      return dir([])
    })
    vi.mocked(copyWorkspaceEntry).mockResolvedValue({ name: 'internal copy', is_dir: true, size: 0 })
    renderTree()
    await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: '复制' }))
    await waitFor(() => expect(calls.filter((r) => r === '')).toHaveLength(2))
    expect(calls.filter((r) => r === 'internal')).toHaveLength(0)
  })

  it('右键目录「在终端中打开」回调父目录自身 rel', async () => {
    const { onOpenTerminal } = renderTree()
    await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: '在终端中打开' }))
    expect(onOpenTerminal).toHaveBeenCalledWith('internal')
  })

  it('右键文件「在终端中打开」回调父目录', async () => {
    const { onOpenTerminal } = renderTree()
    await userEvent.pointer({ target: await screen.findByText('go.mod'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: '在终端中打开' }))
    expect(onOpenTerminal).toHaveBeenCalledWith('')
  })

  it('在文件夹中查找：输入关键词后搜索，命中行点开文件', async () => {
    const { onOpenFile } = renderTree()
    vi.mocked(searchWorkspace).mockResolvedValue({
      hits: [{ rel: 'internal/a.go', line: 3, text: 'needle' }],
      truncated: false,
    })
    await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: '在文件夹中查找' }))
    await userEvent.type(screen.getByLabelText('关键词'), 'needle')
    await userEvent.click(screen.getByRole('button', { name: '搜索' }))
    await waitFor(() => expect(searchWorkspace).toHaveBeenCalledWith('/w/b2-b3', 'internal', 'needle', undefined))
    fireEvent.click(await screen.findByText(/internal\/a\.go/))
    expect(onOpenFile).toHaveBeenCalledWith('internal/a.go')
  })

  it('搜索截断时如实说「仅显示前 N 条」', async () => {
    renderTree()
    vi.mocked(searchWorkspace).mockResolvedValue({ hits: [{ rel: 'a.go', line: 1, text: 'x' }], truncated: true })
    await userEvent.pointer({ target: await screen.findByText('internal'), keys: '[MouseRight]' })
    await userEvent.click(screen.getByRole('menuitem', { name: '在文件夹中查找' }))
    await userEvent.type(screen.getByLabelText('关键词'), 'x')
    await userEvent.click(screen.getByRole('button', { name: '搜索' }))
    expect(await screen.findByText(/结果过多，仅显示前 1 条/)).toBeInTheDocument()
  })
})
