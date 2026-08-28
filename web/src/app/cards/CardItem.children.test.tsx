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
