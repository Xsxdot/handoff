// UsageChip 的行为测试。重点是浮层的**关闭**路径：它盖在会话流上方，
// 关不掉就是挡着正文，比开不开还烦人。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { UsageChip } from './UsageChip'

const usage = { context_tokens: 47_341, context_window: 258_400 }
const cumulative = {
  input_tokens: 719_000,
  cached_tokens: 24_900_000,
  output_tokens: 59_900,
  total_tokens: 25_600_000,
}

function open() {
  render(
    <div>
      <button type="button">外部</button>
      <UsageChip usage={usage} cumulative={cumulative as never} />
    </div>,
  )
  fireEvent.click(screen.getByText(/ctx/))
  expect(screen.getByText('当前占用')).toBeInTheDocument()
}

describe('UsageChip', () => {
  it('账目都缺席时整体不渲染', () => {
    const { container } = render(<UsageChip />)
    expect(container).toBeEmptyDOMElement()
  })

  it('点 chip 打开账目，再点一次收起', () => {
    open()
    fireEvent.click(screen.getByText(/ctx/))
    expect(screen.queryByText('当前占用')).toBeNull()
  })

  it('点浮层外部自动收起', () => {
    open()
    fireEvent.mouseDown(screen.getByText('外部'))
    expect(screen.queryByText('当前占用')).toBeNull()
  })

  it('点浮层内部不收起——里面的数字要能选中复制', () => {
    open()
    fireEvent.mouseDown(screen.getByText('累计消耗'))
    expect(screen.getByText('当前占用')).toBeInTheDocument()
  })

  it('Esc 收起', () => {
    open()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByText('当前占用')).toBeNull()
  })

  it('大额用量按 M 显示，不再一路 k 到底', () => {
    open()
    expect(screen.getByText('25.6M')).toBeInTheDocument()
  })
})
