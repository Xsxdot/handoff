// NewSessionDialog.test.tsx —— 新建会话 owner 记忆方案（B358.8 #7）：
// 预填可直接提交、改选保留、清空恢复前缀预检、storage 不可用容错、成员候选。
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { NewSessionDialog } from './NewSessionDialog'
import { LAST_SESSION_OWNER_KEY, saveLastSessionOwner } from './sessionOwnerPrefs'

const openDialog = (over: { memberIdentities?: string[] } = {}) =>
  render(
    <NewSessionDialog open busy={false} error="" memberIdentities={over.memberIdentities ?? []}
      onCancel={() => {}} onCreate={() => {}} />,
  )

beforeEach(() => {
  window.localStorage.clear()
})

describe('NewSessionDialog（owner 记忆方案）', () => {
  it('无记忆值：owner 空、创建禁用、留有提示（首次使用需输一次）', () => {
    openDialog()
    expect(screen.getByRole('combobox', { name: '群主身份' })).toHaveValue('')
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
    expect(screen.getByText(/留空不可创建/)).toBeInTheDocument()
  })

  it('记忆值预填：不碰 owner 字段即可直接提交，载荷 = 预填 owner（B358.8 #7 判据）', async () => {
    saveLastSessionOwner('user:sy')
    const onCreate = vi.fn()
    const user = userEvent.setup()
    render(<NewSessionDialog open busy={false} error="" onCancel={() => {}} onCreate={onCreate} />)
    const ownerInput = screen.getByRole('combobox', { name: '群主身份' })
    await waitFor(() => expect(ownerInput).toHaveValue('user:sy'))
    await user.type(screen.getByRole('textbox', { name: '会话标题' }), '新场')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(onCreate).toHaveBeenCalledWith('新场', 'user:sy')
  })

  it('改选保留：覆盖预填值后按新 owner 提交', async () => {
    saveLastSessionOwner('user:sy')
    const onCreate = vi.fn()
    const user = userEvent.setup()
    render(<NewSessionDialog open busy={false} error="" onCancel={() => {}} onCreate={onCreate} />)
    const ownerInput = await waitFor(() => screen.getByRole('combobox', { name: '群主身份' }))
    await waitFor(() => expect(ownerInput).toHaveValue('user:sy'))
    await user.clear(ownerInput)
    await user.type(ownerInput, 'agent:claude')
    await user.type(screen.getByRole('textbox', { name: '会话标题' }), '新场')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(onCreate).toHaveBeenCalledWith('新场', 'agent:claude')
  })

  it('清空 owner 恢复前缀预检：禁用创建并提示记法', async () => {
    saveLastSessionOwner('user:sy')
    const user = userEvent.setup()
    openDialog()
    const ownerInput = await waitFor(() => screen.getByRole('combobox', { name: '群主身份' }))
    await waitFor(() => expect(ownerInput).toHaveValue('user:sy'))
    await user.clear(ownerInput)
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
    await user.type(ownerInput, 'web:host')
    expect(screen.getByText('须形如 user:<名字> 或 agent:<名字>')).toBeInTheDocument()
  })

  it('成员候选：memberIdentities 渲染进 datalist 供改选', () => {
    openDialog({ memberIdentities: ['user:sy', 'agent:claude'] })
    // datalist 的 option 不进 a11y 树，按 DOM 查
    const options = Array.from(document.querySelectorAll('#new-session-owner-candidates option')) as HTMLOptionElement[]
    expect(options.map((option) => option.value)).toEqual(['user:sy', 'agent:claude'])
  })

  it('storage 不可用（隐私模式）容错：不崩、退回手输', async () => {
    const getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    openDialog()
    expect(screen.getByRole('combobox', { name: '群主身份' })).toHaveValue('')
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
    getItem.mockRestore()
  })

  it('saveLastSessionOwner 写入约定键；非法记法也照记（服务端权威）', async () => {
    saveLastSessionOwner('agent:claude')
    expect(window.localStorage.getItem(LAST_SESSION_OWNER_KEY)).toBe('agent:claude')
  })
})
