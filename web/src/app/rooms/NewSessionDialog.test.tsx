// NewSessionDialog.test.tsx —— B358.9：表单只有标题，owner 由服务端按解析人名
// 缺省；未配 console_user 时给可行动提示并禁用创建（P-1 甲的组件半边）。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { NewSessionDialog } from './NewSessionDialog'

beforeEach(() => window.localStorage.clear())

const open = (over: { configured?: boolean } = {}) =>
  render(<NewSessionDialog open busy={false} error="" configured={over.configured ?? true}
    onCancel={() => {}} onCreate={() => {}} />)

describe('NewSessionDialog（B358.9：仅标题，owner 服务端缺省）', () => {
  it('表单只有标题：无群主输入框（B358.9 反例）', () => {
    open()
    expect(screen.getByRole('textbox', { name: '会话标题' })).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: '群主身份' })).toBeNull()
    expect(screen.queryByLabelText('群主身份')).toBeNull()
  })

  it('提交只带标题：onCreate(title)', async () => {
    const onCreate = vi.fn()
    const user = userEvent.setup()
    render(<NewSessionDialog open busy={false} error="" configured onCancel={() => {}} onCreate={onCreate} />)
    await user.type(screen.getByRole('textbox', { name: '会话标题' }), '新场')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(onCreate).toHaveBeenCalledWith('新场')
  })

  it('未配名：渲染含 console_user 的可行动提示且创建禁用', () => {
    open({ configured: false })
    expect(screen.getByRole('alert')).toHaveTextContent('console_user')
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
  })

  it('空标题禁用创建', () => {
    open()
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
  })
})
