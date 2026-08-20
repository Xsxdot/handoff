// FlowsPage 测试：验证工作流编辑加载、发布新版本与原文错误展示。
// 边界：节点字段细节由 NodeEditor 测试覆盖，本文件只检查页面编排。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { FlowsPage } from './FlowsPage'

vi.mock('../../api/ledger', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../api/ledger')>()),
  fetchFlows: vi.fn().mockResolvedValue({
    workflows: [{ name: 'feature', version: 3, def: { states: ['待办', '已完成'] } }],
    templates: [{ name: 'review-generic', version: 1, def: {} }],
  }),
  fetchFlow: vi.fn().mockResolvedValue({
    name: 'feature', version: 3, states: ['待办', '已完成'],
    nodes: [{ name: '待办', next: '已完成' }, { name: '已完成' }],
  }),
  fetchDisciplineNames: vi.fn().mockResolvedValue(['implement', 'review', 'finishing']),
  putFlow: vi.fn().mockResolvedValue({ name: 'feature', version: 4 }),
}))

describe('工作流页可编辑', () => {
  it('保存调 putFlow 并把新版本号显示出来', async () => {
    const ledger = await import('../../api/ledger')
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    fireEvent.click(await screen.findByRole('button', { name: '保存为新版本' }))
    await waitFor(() => expect(vi.mocked(ledger.putFlow)).toHaveBeenCalledWith('feature', expect.any(Array)))
    expect(await screen.findByText(/v4/)).toBeInTheDocument()
  })

  it('保存前明说这是「发新版本」，老卡不受影响', async () => {
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    expect(await screen.findByText(/发布一个新版本.*已有的卡仍走各自钉住的版本/)).toBeInTheDocument()
  })

  it('后端拒绝时把真因显示出来，不是「保存失败」四个字', async () => {
    const ledger = await import('../../api/ledger')
    vi.mocked(ledger.putFlow).mockRejectedValueOnce(new Error('节点 "A" 的 Next 指向不存在的节点 "B"'))
    render(<FlowsPage />)
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }))
    fireEvent.click(await screen.findByRole('button', { name: '保存为新版本' }))
    expect(await screen.findByText(/Next 指向不存在的节点/)).toBeInTheDocument()
  })
})
