// QuotaBits.test.tsx —— kind 单色图标（B468）的渲染接缝。
// 边界：只断言 KindIcon 产出的 SVG 结构（viewBox、填充只走 currentColor）；
// 图形是否像官方标志交浏览器截图验收。
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { KindIcon } from './QuotaBits'

const svgOf = (kind: string) => {
  const { container } = render(<KindIcon kind={kind} />)
  return container.querySelector('svg')
}

describe('KindIcon 官方标志', () => {
  it.each(['codex', 'grok'])('%s 用 24×24 官方标志单色路径', (kind) => {
    const svg = svgOf(kind)
    expect(svg).not.toBeNull()
    expect(svg!.getAttribute('viewBox')).toBe('0 0 24 24')
    const paths = svg!.querySelectorAll('path')
    expect(paths).toHaveLength(1)
    expect(paths[0].getAttribute('fill')).toBe('currentColor')
    expect(svg!.outerHTML).not.toMatch(/#[0-9a-fA-F]{3,8}|rgba?\(/)
  })

  it('其他 kind 仍是 16×16 线稿', () => {
    expect(svgOf('opencode-go')!.getAttribute('viewBox')).toBe('0 0 16 16')
  })
})
