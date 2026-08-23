// NodeEditor 聚合闸开关：勾选写 require_children_done=true，取消勾选把
// 字段从 gate 里清掉（不是写 false）——与 require_acceptance 同款语义，
// 后端 omitempty 才不会存一堆无意义的 false。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { NodeEditor } from './NodeEditor'

const props = {
  templates: [],
  disciplines: [],
  nodeNames: ['进行中', '集成'],
  onRemove: () => {},
}

describe('NodeEditor 聚合闸', () => {
  it('勾选写 require_children_done，取消勾选清掉字段', () => {
    const onChange = vi.fn()
    const { rerender } = render(
      <NodeEditor node={{ name: '集成' }} {...props} index={0} onChange={onChange} />,
    )
    fireEvent.click(screen.getByLabelText('需全部子卡完结'))
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      gate: { require_children_done: true },
    }))

    onChange.mockClear()
    rerender(
      <NodeEditor
        node={{ name: '集成', gate: { require_children_done: true } }}
        {...props}
        index={0}
        onChange={onChange}
      />,
    )
    fireEvent.click(screen.getByLabelText('需全部子卡完结'))
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ gate: undefined }))
  })
})
