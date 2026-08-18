import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import type { ProjectTreeResp, Task } from '../../api/types'
import type { BaseDir } from './useWorkbench'
import { TaskPickerDialog, isTerminalState, projectIdOfBase } from './TaskPickerDialog'

const base: BaseDir = {
  key: '/r/a',
  kind: 'workspace',
  path: '/r/a',
  label: 'feature/a',
  projectName: 'alpha',
  machine: '',
}

const tree: ProjectTreeResp = {
  projects: [
    {
      project_id: 'p1', origin_url: '', name: 'alpha',
      locations: [{
        machine: '', name: 'alpha', path: '/r', probe_error: '',
        workspaces: [
          { path: '/r/a', branch: 'feature/a', head: 'a', is_main: false, managed: true, created_at: '' },
          { path: '/r/b', branch: 'feature/b', head: 'b', is_main: false, managed: true, created_at: '' },
        ],
      }],
    },
    {
      project_id: 'p2', origin_url: '', name: 'beta',
      locations: [{
        machine: '', name: 'beta', path: '/other', probe_error: '',
        workspaces: [{ path: '/other/main', branch: 'main', head: 'c', is_main: true, managed: false, created_at: '' }],
      }],
    },
  ],
  unowned: [],
}

function task(over: Partial<Task>): Task {
  return {
    id: 'T', target: '', repo_path: '', branch: '', plan_path: '', plan_summary: '',
    executor_session: '', state: 'running', created_at: '', updated_at: '2026-08-17T00:00:00Z', name: '任务',
    executor: 'opencode', model: '', work_dir: '/r/a', worktree_managed: false,
    base_commit: '', base_ahead: 0, repo_dirty_count: 0, repo_dirty_files: '', done_note: '',
    machine: '', project_id: 'p1', ...over,
  }
}

describe('TaskPickerDialog', () => {
  it('只列当前基准所属项目的任务，别的项目不出现', () => {
    render(<TaskPickerDialog open base={base} tree={tree} tasks={[
      task({ id: 'T1', name: 'alpha task' }),
      task({ id: 'T2', name: 'beta task', project_id: 'p2', work_dir: '/other/main' }),
    ]} onPick={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText('alpha task')).toBeInTheDocument()
    expect(screen.queryByText('beta task')).not.toBeInTheDocument()
  })

  it('别的分支的任务也在列表里——这正是这个弹层存在的理由', () => {
    render(<TaskPickerDialog open base={base} tree={tree} tasks={[
      task({ id: 'T1', name: '当前分支任务', branch: 'feature/a' }),
      task({ id: 'T2', name: '另一个分支任务', branch: 'feature/b', work_dir: '/r/b' }),
    ]} onPick={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText('另一个分支任务')).toBeInTheDocument()
    expect(screen.getByText('feature/b')).toBeInTheDocument()
  })

  it('已结束任务默认折叠，标题上带条数；点开才显示', () => {
    render(<TaskPickerDialog open base={base} tree={tree} tasks={[
      task({ id: 'T1', name: '进行中' }),
      task({ id: 'T2', name: '已完成 1', state: 'completed' }),
      task({ id: 'T3', name: '已完成 2', state: 'failed' }),
    ]} onPick={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText('进行中')).toBeInTheDocument()
    const done = screen.getByRole('button', { name: '已结束 2' })
    expect(screen.queryByText('已完成 1')).not.toBeInTheDocument()
    fireEvent.click(done)
    expect(screen.getByText('已完成 1')).toBeInTheDocument()
    expect(screen.getByText('已完成 2')).toBeInTheDocument()
  })

  it('每行带目录短名，同名任务能区分开', () => {
    render(<TaskPickerDialog open base={base} tree={tree} tasks={[
      task({ id: 'T1', name: '同名任务', branch: 'feature/a' }),
      task({ id: 'T2', name: '同名任务', branch: 'feature/b', work_dir: '/r/b' }),
    ]} onPick={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getAllByText('同名任务')).toHaveLength(2)
    expect(screen.getByText('feature/a')).toBeInTheDocument()
    expect(screen.getByText('feature/b')).toBeInTheDocument()
  })

  it('搜索框按任务名过滤', () => {
    render(<TaskPickerDialog open base={base} tree={tree} tasks={[
      task({ id: 'T1', name: '重构认证' }),
      task({ id: 'T2', name: '修复部署', work_dir: '/r/b' }),
    ]} onPick={vi.fn()} onClose={vi.fn()} />)
    fireEvent.change(screen.getByPlaceholderText('搜索任务'), { target: { value: '认证' } })
    expect(screen.getByText('重构认证')).toBeInTheDocument()
    expect(screen.queryByText('修复部署')).not.toBeInTheDocument()
  })

  it('点一行触发 onPick 带上 taskId', () => {
    const onPick = vi.fn()
    render(<TaskPickerDialog open base={base} tree={tree} tasks={[task({ id: 'T9', name: '要打开的任务' })]} onPick={onPick} onClose={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: /要打开的任务/ }))
    expect(onPick).toHaveBeenCalledWith('T9')
  })

  it('Esc 触发 onClose', () => {
    const onClose = vi.fn()
    render(<TaskPickerDialog open base={base} tree={tree} tasks={[]} onPick={vi.fn()} onClose={onClose} />)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('↑↓移动选中项，Enter 确认当前任务', () => {
    const onPick = vi.fn()
    render(<TaskPickerDialog open base={base} tree={tree} tasks={[
      task({ id: 'T1', name: '第一个' }),
      task({ id: 'T2', name: '第二个' }),
    ]} onPick={onPick} onClose={vi.fn()} />)
    fireEvent.keyDown(window, { key: 'ArrowDown' })
    fireEvent.keyDown(window, { key: 'Enter' })
    expect(onPick).toHaveBeenCalledWith('T2')
  })

  it('这个项目没有任务时显示空态文案，不是空列表', () => {
    render(<TaskPickerDialog open base={base} tree={tree} tasks={[task({ project_id: 'p2', work_dir: '/other/main' })]} onPick={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText('这个项目下还没有任务。')).toBeInTheDocument()
  })
})

describe('projectIdOfBase', () => {
  it('按 base.path 在树上反查所属项目', () => {
    expect(projectIdOfBase(tree, base)).toBe('p1')
  })

  it('树还没到位或路径不在树上时返回 null', () => {
    expect(projectIdOfBase(null, base)).toBeNull()
    expect(projectIdOfBase(tree, { ...base, path: '/gone', key: '/gone' })).toBeNull()
  })
})

describe('isTerminalState', () => {
  it('与看板终态口径一致', () => {
    expect(isTerminalState('completed')).toBe(true)
    expect(isTerminalState('failed')).toBe(true)
    expect(isTerminalState('running')).toBe(false)
  })
})
