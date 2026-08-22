// NodeEditor 控件 id 必须按节点区分，避免中文列名生成重复 id 破坏 label 关联。
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { NodeEditor } from './NodeEditor'

const props = {
  templates: [],
  disciplines: [],
  nodeNames: ['待办', '集成'],
  onRemove: () => {},
}

describe('NodeEditor 控件 id', () => {
  it('中文列名节点的控件 id 不重复，点第二个聚合闸 label 只更新第二个节点', () => {
    const firstOnChange = vi.fn()
    const secondOnChange = vi.fn()
    const { container } = render(
      <>
        <NodeEditor
          node={{ name: '待办', dispatch: true }}
          {...props}
          onChange={firstOnChange}
          index={0}
        />
        <NodeEditor
          node={{ name: '集成', dispatch: true }}
          {...props}
          onChange={secondOnChange}
          index={1}
        />
      </>,
    )

    const checkboxIds = [...container.querySelectorAll('input[type=checkbox]')]
      .map((input) => input.id)
    expect(new Set(checkboxIds).size).toBe(checkboxIds.length)

    const childrenDoneLabels = [...container.querySelectorAll('label')]
      .filter((label) => label.textContent === '需全部子卡完结')
    fireEvent.click(childrenDoneLabels[1])
    expect(firstOnChange).not.toHaveBeenCalled()
    expect(secondOnChange).toHaveBeenCalledTimes(1)
  })
})
