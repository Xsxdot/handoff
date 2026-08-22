// NodeEditor 用途与判据开关：验证节点配置能写入并清理对应字段。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { NodeEditor } from './NodeEditor'

// index 是 B169 之后的必填 prop：控件 id 用节点下标当稳定键，不再由列名生成。
const props = {
  index: 0,
  templates: ['feature-impl'],
  disciplines: ['implement', 'review'],
  nodeNames: ['进行中', '待审阅'],
  onRemove: () => {},
}

describe('NodeEditor 用途与验收判据', () => {
  it('选择 review 写入覆盖，选回空时清掉 override', () => {
    const onChange = vi.fn()
    const { rerender } = render(
      <NodeEditor node={{ name: '待审阅', dispatch: true }} {...props} onChange={onChange} />,
    )
    fireEvent.change(screen.getByLabelText('用途'), { target: { value: 'review' } })
    const reviewNode = onChange.mock.calls[0][0]
    expect(reviewNode.override).toEqual({ purpose: 'review' })

    onChange.mockClear()
    rerender(<NodeEditor node={reviewNode} {...props} onChange={onChange} />)
    fireEvent.change(screen.getByLabelText('用途'), { target: { value: '' } })
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ override: undefined }))
  })

  it('已有用户自建用途时保留它并并入候选', () => {
    render(
      <NodeEditor
        node={{ name: '待审阅', dispatch: true, override: { purpose: 'recon' } }}
        {...props}
        onChange={() => {}}
      />,
    )
    const purpose = screen.getByLabelText('用途') as HTMLSelectElement
    expect(purpose.value).toBe('recon')
    expect([...purpose.options].map((option) => option.value)).toEqual(['', 'recon', 'implement', 'review'])
  })

  it('勾选与取消不注入验收判据分别写 true 与 undefined', () => {
    const onChange = vi.fn()
    const { rerender } = render(
      <NodeEditor node={{ name: '待审阅', dispatch: true }} {...props} onChange={onChange} />,
    )
    fireEvent.click(screen.getByLabelText('不注入验收判据'))
    const enabledNode = onChange.mock.calls[0][0]
    expect(enabledNode.omit_acceptance).toBe(true)

    onChange.mockClear()
    rerender(<NodeEditor node={enabledNode} {...props} onChange={onChange} />)
    fireEvent.click(screen.getByLabelText('不注入验收判据'))
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ omit_acceptance: undefined }))
  })

  it('关闭派发时两个新控件都不渲染', () => {
    render(
      <NodeEditor
        node={{ name: '待审阅', override: { purpose: 'review' }, omit_acceptance: true }}
        {...props}
        onChange={() => {}}
      />,
    )
    expect(screen.queryByLabelText('用途')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('不注入验收判据')).not.toBeInTheDocument()
  })
})
