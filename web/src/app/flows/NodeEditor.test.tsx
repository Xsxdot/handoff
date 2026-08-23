// NodeEditor 测试：验证单节点控件的字段冒泡与派发语义。
// 边界：不测试整条工作流保存，跨节点校验由后端负责。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { NodeEditor } from './NodeEditor'

const base = { name: '待审阅', dispatch: true, verdict: true, template: 'review-generic' }
const props = {
  templates: ['feature-impl', 'review-generic'],
  disciplines: ['implement', 'review', 'finishing'],
  nodeNames: ['待办', '进行中', '待审阅', '已完成'],
}

describe('节点编辑器', () => {
  it('能改执行者与纪律块覆盖，改动原样冒泡给上层', () => {
    const onChange = vi.fn()
    render(<NodeEditor node={base} {...props} index={0} onChange={onChange} onRemove={() => {}} />)
    fireEvent.change(screen.getByLabelText('纪律块'), { target: { value: 'finishing' } })
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      override: expect.objectContaining({ discipline: 'finishing' }),
    }))
  })

  it('关掉派发时，裁决/模板/轮次一并失效——它们没有派发就没有意义', () => {
    const onChange = vi.fn()
    render(<NodeEditor node={base} {...props} index={0} onChange={onChange} onRemove={() => {}} />)
    fireEvent.click(screen.getByLabelText('派发'))
    const next = onChange.mock.calls[0][0]
    expect(next.dispatch).toBe(false)
    expect(next.verdict).toBeFalsy()
    expect(next.max_rounds).toBeFalsy()
  })

  it('路由下拉的候选是别的节点名，不含自己', () => {
    render(<NodeEditor node={base} {...props} index={0} onChange={() => {}} onRemove={() => {}} />)
    const options = [...screen.getByLabelText('通过后去').querySelectorAll('option')].map((o) => o.textContent)
    expect(options).toContain('已完成')
    expect(options).not.toContain('待审阅')
  })

  it('纯人工列不显示模板与纪律块——避免让人以为配了会生效', () => {
    render(<NodeEditor node={{ name: '待办' }} {...props} index={0} onChange={() => {}} onRemove={() => {}} />)
    expect(screen.queryByLabelText('模板')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('纪律块')).not.toBeInTheDocument()
  })
})
