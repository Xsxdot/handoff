// Badge intervention 变体的契约测试。
//
// 为什么要单独钉这个变体：它是任务详情页顶栏干预态（等你答复 / Review）的
// 唯一视觉出口，类名一旦改错，页面不会报错、只会静默变回灰色。
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Badge } from './badge'

describe('Badge', () => {
  it('intervention 变体用琥珀实色背景 + 白字', () => {
    render(<Badge variant="intervention">等你答复</Badge>)
    const el = screen.getByText('等你答复')
    expect(el.className).toContain('bg-state-intervention')
    expect(el.className).toContain('text-white')
  })

  it('既有四档保持不变', () => {
    const { rerender } = render(<Badge variant="destructive">失败</Badge>)
    expect(screen.getByText('失败').className).toContain('bg-destructive')
    rerender(<Badge variant="outline">Review</Badge>)
    expect(screen.getByText('Review').className).toContain('text-foreground')
  })
})
