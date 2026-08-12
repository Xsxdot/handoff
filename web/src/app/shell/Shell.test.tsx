import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { Shell } from './Shell'

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route element={<Shell />}>
          <Route path="/" element={<div>看板内容</div>} />
          <Route path="/machines" element={<div>机器内容</div>} />
          <Route path="/tasks/:id" element={<div>详情内容</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  )
}

describe('Shell', () => {
  it('三条路由都嵌在 shell 里，左栏常驻', () => {
    for (const [path, text] of [['/', '看板内容'], ['/machines', '机器内容'], ['/tasks/abc', '详情内容']] as const) {
      const { unmount } = renderAt(path)
      expect(screen.getByText(text)).toBeInTheDocument()
      expect(screen.getByRole('complementary')).toBeInTheDocument() // 左栏
      unmount()
    }
  })

  it('当前 tab 有选中态', () => {
    renderAt('/machines')
    expect(screen.getByRole('link', { name: '开发机' })).toHaveAttribute('aria-current', 'page')
  })

  it('不渲染未实现功能的入口（齿轮/设置）', () => {
    renderAt('/')
    expect(screen.queryByRole('button', { name: /设置/ })).toBeNull()
  })
})
