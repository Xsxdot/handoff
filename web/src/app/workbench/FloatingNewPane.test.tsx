import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { FloatingNewPane } from './FloatingNewPane'

describe('FloatingNewPane', () => {
  it('缺省是收起的一个按钮', () => {
    render(<FloatingNewPane onNewTerminal={vi.fn()} />)
    expect(screen.getByRole('button', { name: '新建（以 home 为基准）' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /新终端/ })).not.toBeInTheDocument()
  })

  it('展开后只有「新终端」一项——本期不放文件与 TUI（spec §2.6）', () => {
    render(<FloatingNewPane onNewTerminal={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: '新建（以 home 为基准）' }))
    expect(screen.getByRole('button', { name: /新终端/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /打开文件/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /打开任务 TUI/ })).not.toBeInTheDocument()
  })

  it('明说基准是 home、不挂在任何项目上', () => {
    render(<FloatingNewPane onNewTerminal={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: '新建（以 home 为基准）' }))
    expect(screen.getByText(/不挂在任何项目上/)).toBeInTheDocument()
  })

  it('点新终端回调并收起面板', () => {
    const onNewTerminal = vi.fn()
    render(<FloatingNewPane onNewTerminal={onNewTerminal} />)
    fireEvent.click(screen.getByRole('button', { name: '新建（以 home 为基准）' }))
    fireEvent.click(screen.getByRole('button', { name: /新终端/ }))
    expect(onNewTerminal).toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: /新终端/ })).not.toBeInTheDocument()
  })
})
