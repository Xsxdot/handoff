// StateDot / TaskState 的呈现契约测试。
//
// 形态基准是 prototypes/desktop-console 的 .task-state + .status-dot：
// 圆点 + 文字，不是填充胶囊（spec §1.1）。
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { StateDot, TaskState } from './StateDot'

describe('StateDot', () => {
  it('干预态是琥珀圆点', () => {
    const { container } = render(<StateDot tone="intervention" />)
    expect(container.firstChild).toHaveClass('bg-state-intervention')
  })

  it('running / completed 是绿点，failed 是红点，pending 是灰点', () => {
    const { container: a } = render(<StateDot tone="active" />)
    expect(a.firstChild).toHaveClass('bg-state-active')
    const { container: d } = render(<StateDot tone="done" />)
    expect(d.firstChild).toHaveClass('bg-state-active')
    const { container: f } = render(<StateDot tone="failed" />)
    expect(f.firstChild).toHaveClass('bg-state-failed')
    const { container: i } = render(<StateDot tone="idle" />)
    expect(i.firstChild).toHaveClass('bg-muted-foreground/40')
  })
})

describe('TaskState', () => {
  it('干预态的文字染琥珀', () => {
    render(<TaskState state="waiting_answer" />)
    const el = screen.getByText('等你答复')
    expect(el.className).toContain('text-state-intervention-text')
  })

  it('waiting_review 同为干预态', () => {
    render(<TaskState state="waiting_review" />)
    expect(screen.getByText('Review').className).toContain('text-state-intervention-text')
  })

  it('failed 的文字染红', () => {
    render(<TaskState state="failed" />)
    expect(screen.getByText('失败').className).toContain('text-state-failed')
  })

  // 只有需要你注意的才染色——全都染色等于都不染色
  it('其余状态文字保持次要色', () => {
    render(<TaskState state="running" />)
    expect(screen.getByText('进行中').className).toContain('text-muted-foreground')
  })

  it('未知状态原文透出，不吞数据', () => {
    render(<TaskState state="new_unknown_state" />)
    expect(screen.getByText('new_unknown_state')).toBeInTheDocument()
  })
})
