// BlockEditor.test.tsx —— 共用正文编辑器的只读、保存与冲突态测试。
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BlockEditor } from './BlockEditor'

describe('BlockEditor', () => {
  it('只读态：textarea 带 readonly，显示模板按钮而非保存', () => {
    render(<BlockEditor title="内置 subagent" ariaLabel="纪律块正文" content="正文" readOnly
      templateLabel="以此为模板新建" onTemplate={() => {}} />)
    expect(screen.getByRole('textbox', { name: /纪律块正文/ })).toHaveAttribute('readonly')
    expect(screen.getByRole('button', { name: '以此为模板新建' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '保存' })).not.toBeInTheDocument()
  })

  it('可写态：改动走 onChange，保存走 onSave', async () => {
    const onChange = vi.fn()
    const onSave = vi.fn()
    render(<BlockEditor title="a.env" ariaLabel="env 文件正文" content="A=1" readOnly={false}
      onChange={onChange} onSave={onSave} />)
    await userEvent.type(screen.getByRole('textbox', { name: /env 文件正文/ }), '2')
    expect(onChange).toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('冲突由 conflict 布尔决定，不看错误文案', () => {
    // 这条正是 B157 记账的 Minor：以前用 error === '已被改动' 判定，
    // 改一下文案就会静默失去「重新加载」按钮
    const { rerender } = render(<BlockEditor title="a.env" ariaLabel="env 文件正文" content=""
      readOnly={false} error="随便什么别的错误" />)
    expect(screen.queryByRole('button', { name: '重新加载' })).not.toBeInTheDocument()

    rerender(<BlockEditor title="a.env" ariaLabel="env 文件正文" content="" readOnly={false}
      error="盘上的内容和你打开时不一样了" conflict onReload={() => {}} />)
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
  })
})
