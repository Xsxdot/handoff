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

  it('home 基准单段：无 tail 显示基准名（既有回归）；tail 放行（B366 修——不再吞内容名）', () => {
    expect(breadcrumbSegments(home)).toEqual(['home'])
    expect(breadcrumbSegments(home, 'bash')).toEqual(['bash'])
  })

  it('B366 会话 tab：kind home + label 会话，第三段=会话标题（B358.6 review I-1）', () => {
    // Shell 给会话工作台 tab 的 base（useWorkbench#sessionBase）：kind 落 'home'
    // 白名单、label=「会话」；焦点内容 tail=tabTitle 的「会话 · 标题」。
    const sessionBase: BaseDir = { key: 'session:7', kind: 'home', path: '', label: '会话', projectName: '', machine: '' }
    expect(breadcrumbSegments(sessionBase, '会话 · 架构物理化')).toEqual(['会话 · 架构物理化'])
    // 无焦点内容回落基准名（不再是硬编码 'home'）。
    expect(breadcrumbSegments(sessionBase)).toEqual(['会话'])
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
