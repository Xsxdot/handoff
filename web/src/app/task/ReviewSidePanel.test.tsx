// ReviewSidePanel.test.tsx —— 审阅栏：diff 自动加载/基准下拉/裸文本回退/跑命令。
import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ReviewSidePanel } from './ReviewSidePanel'

vi.mock('../../api/client', () => ({
  fetchTaskDiff: vi.fn(),
  fetchTaskBranches: vi.fn(),
  fetchTaskFile: vi.fn(),
  runTaskCommand: vi.fn(),
}))
import { fetchTaskBranches, fetchTaskDiff } from '../../api/client'

const DIFF = `diff --git a/a.md b/a.md
index 1..2 100644
--- a/a.md
+++ b/a.md
@@ -1 +1,2 @@
 x
+y
`

beforeEach(() => {
  vi.mocked(fetchTaskDiff).mockResolvedValue({ diff: DIFF })
  vi.mocked(fetchTaskBranches).mockResolvedValue({ branches: ['main', 'dev'], default: 'main' })
})

describe('ReviewSidePanel', () => {
  it('进栏自动加载 diff 并按文件分组展示 ± 统计', async () => {
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('a.md')).toBeInTheDocument())
    expect(screen.getAllByText('+1', { exact: true })).toHaveLength(2)
    expect(fetchTaskDiff).toHaveBeenCalledWith('t1', undefined)
  })
  it('基准下拉列出分支；选择后带 base 重取', async () => {
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())
    const sel = screen.getByRole('combobox') as HTMLSelectElement
    expect(screen.getByRole('option', { name: /dev/ })).toBeInTheDocument()
    sel.value = 'dev'
    sel.dispatchEvent(new Event('change', { bubbles: true }))
    await waitFor(() => expect(fetchTaskDiff).toHaveBeenCalledWith('t1', 'dev'))
  })
  it('不可解析的 diff 整体回退裸文本', async () => {
    vi.mocked(fetchTaskDiff).mockResolvedValue({ diff: '一段解析不了的输出' })
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('一段解析不了的输出')).toBeInTheDocument())
  })
  it('分支接口失败：下拉退化为仅自动推导，diff 不受影响', async () => {
    vi.mocked(fetchTaskBranches).mockRejectedValue(new Error('探活失败'))
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByText('a.md')).toBeInTheDocument())
    expect(screen.getByRole('option', { name: /自动推导/ })).toBeInTheDocument()
    expect(screen.getAllByRole('option')).toHaveLength(1)
  })
})
