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
  vi.mocked(fetchTaskBranches).mockResolvedValue({ branches: ['main', 'dev'], default: 'main', task_base: '' })
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
  it('自动推导括注：有任务基线时显示前 8 位 sha', async () => {
    vi.mocked(fetchTaskBranches).mockResolvedValue({
      branches: ['main'], default: 'main', task_base: '0123456789abcdef0123456789abcdef01234567',
    })
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())
    expect(screen.getByRole('option', { name: '自动推导（任务基线 01234567）' })).toBeInTheDocument()
  })
  it('自动推导括注：无任务基线时退回分支名', async () => {
    vi.mocked(fetchTaskBranches).mockResolvedValue({ branches: ['main'], default: 'main', task_base: '' })
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())
    expect(screen.getByRole('option', { name: '自动推导（main）' })).toBeInTheDocument()
  })
  it('自动推导括注：两者皆空时不带括注', async () => {
    vi.mocked(fetchTaskBranches).mockResolvedValue({ branches: [], default: '', task_base: '' })
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())
    expect(screen.getByRole('option', { name: '自动推导' })).toBeInTheDocument()
  })

  // —— B369.10 岔口 4：compact 全宽抽屉 vs 桌面 44% 侧滑（类串逐字节反例锁）——
  it('桌面（缺省）侧滑类串逐字节保留', async () => {
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())
    const aside = screen.getByRole('complementary')
    expect(aside.className).toContain('w-[44%]')
    expect(aside.className).toContain('min-w-[400px]')
    expect(aside.className).toContain('max-w-[620px]')
    expect(aside.className).not.toContain('w-full')
  })

  it('compact 全宽抽屉：w-full 在场、44%/min-w/max-w 不在场', async () => {
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} compact />)
    await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())
    const aside = screen.getByRole('complementary')
    expect(aside.className).toContain('absolute inset-y-0 right-0 z-40 w-full')
    expect(aside.className).not.toContain('w-[44%]')
    expect(aside.className).not.toContain('min-w-[400px]')
    expect(aside.className).not.toContain('max-w-[620px]')
  })
})
