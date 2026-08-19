import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { RowCounts } from './RowCounts'

describe('RowCounts', () => {
  it('项目/机器行：三段各带语义与数字', () => {
    render(<RowCounts dirs={2} running={1} pending={3} />)
    expect(screen.getByTitle('开发目录')).toHaveTextContent('2')
    expect(screen.getByTitle('运行中的 handoff')).toHaveTextContent('1')
    expect(screen.getByTitle('需要处理')).toHaveTextContent('3')
  })

  it('目录行：省略 dirs 时不渲染目录段', () => {
    render(<RowCounts running={1} pending={0} />)
    expect(screen.queryByTitle('开发目录')).toBeNull()
    expect(screen.getByTitle('运行中的 handoff')).toHaveTextContent('1')
  })

  it('运行中 / 需要处理为 0 时整段不渲染', () => {
    render(<RowCounts dirs={0} running={0} pending={0} />)
    expect(screen.queryByTitle('运行中的 handoff')).toBeNull()
    expect(screen.queryByTitle('需要处理')).toBeNull()
  })

  it('目录数为 0 仍渲染，不跟着省略', () => {
    render(<RowCounts dirs={0} running={0} pending={0} />)
    expect(screen.getByTitle('开发目录')).toHaveTextContent('0')
  })
})
