// Breadcrumb.test.tsx —— 顶部面包屑行（workspace-context 形态）与分段纯函数测试。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Breadcrumb, breadcrumbSegments } from './Breadcrumb'
import type { BaseDir } from '../workbench/useWorkbench'

const local: BaseDir = { key: '/local', kind: 'workspace', path: '/local', label: 'main', projectName: 'handoff', machine: '' }
const home: BaseDir = { key: '~', kind: 'home', path: '~', label: 'home', projectName: '', machine: '' }

describe('breadcrumbSegments', () => {
  it('默认三段：项目 / 机器（本机）/ 目录', () => {
    expect(breadcrumbSegments(local)).toEqual(['handoff', '本机', 'main'])
  })

  it('tail 非空时替换第三段（目录名 → 焦点内容名）', () => {
    expect(breadcrumbSegments(local, 'go.mod')).toEqual(['handoff', '本机', 'go.mod'])
  })

  it('home 基准只显示一段，tail 不参与', () => {
    expect(breadcrumbSegments(home)).toEqual(['home'])
    expect(breadcrumbSegments(home, 'bash')).toEqual(['home'])
  })
})

describe('Breadcrumb 行渲染', () => {
  it('段间用字面「 / 」分隔，tail 出现在行内，title 属性带全文', () => {
    render(<Breadcrumb base={local} tail="go.mod" />)
    // aria-label 在内层 nav 上；title 属性在外层整行 div 上（原型 .workspace-context）
    const row = screen.getByLabelText('当前位置').parentElement as HTMLElement
    expect(row.textContent).toBe('handoff / 本机 / go.mod')
    expect(row).toHaveAttribute('title', 'handoff / 本机 / go.mod')
  })

  it('不传 tail 时显示目录名；整行不可点（零交互）', () => {
    render(<Breadcrumb base={local} />)
    const row = screen.getByLabelText('当前位置').parentElement as HTMLElement
    expect(row.textContent).toBe('handoff / 本机 / main')
    expect(row.querySelector('button, a')).toBeNull()
    fireEvent.click(row)
  })
})
