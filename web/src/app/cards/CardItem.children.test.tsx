// 看板父卡徽标：children_total>0 时显示「⧉ 子卡 done/total」，无子卡
// 不渲染（普通卡不为用不上的机制付视觉税）。
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { CardView } from '../../api/ledger'
import { CardItem } from './CardItem'

const base: CardView = {
  id: 'B1', title: '卡', status: '进行中', priority: '中', project: 'p',
  parent: '', workflow: 'feature', base_branch: '', attachments: [], following: '',
  blocked: false, blocked_by: [], merged_count: 0, needs: '', open_decisions: 0,
  conflict: false, open_tickets: 0, children_total: 0, children_done: 0,
}

describe('CardItem 子卡徽标', () => {
  it('有子卡时显示 done/total', () => {
    render(<CardItem card={{ ...base, children_total: 3, children_done: 2 }} onOpen={vi.fn()} />)
    expect(screen.getByText('⧉ 子卡 2/3')).toBeInTheDocument()
  })

  it('无子卡不渲染徽标', () => {
    render(<CardItem card={base} onOpen={vi.fn()} />)
    expect(screen.queryByText(/子卡/)).toBeNull()
  })

  it('有队列位次和节点标签时显示可选胶囊', () => {
    render(<CardItem card={base} onOpen={vi.fn()} queuePosition={2} nodeTag="待审阅" />)
    expect(screen.getByText('⧗ 排队 #2')).toBeInTheDocument()
    expect(screen.getByText('待审阅')).toBeInTheDocument()
  })
})

describe('CardItem 状态唯一化（B287）', () => {
  it('节点标签只出现在右上角 chip 一处，下方标签行不再重复', () => {
    // 多节点列时 nodeLabelFor 返回状态名本身（nodeTag === status）——
    // 这正是用户截图里的「spec 两遍」形态，重复只发生在同文本时。
    render(<CardItem card={{ ...base, status: '待审阅' }} onOpen={vi.fn()} nodeTag="待审阅" />)
    expect(screen.getAllByText('待审阅')).toHaveLength(1)
    const chip = screen.getAllByText('待审阅')[0]!
    expect(chip.className).toContain('bg-slate-900')
  })

  it('无节点标签时右上角显示状态名，同样只出现一次', () => {
    render(<CardItem card={base} onOpen={vi.fn()} />)
    expect(screen.getAllByText('进行中')).toHaveLength(1)
    expect(screen.getAllByText('进行中')[0]!.className).toContain('bg-slate-900')
  })
})
