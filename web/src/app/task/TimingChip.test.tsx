// TimingChip 的行为测试。重点三条：缺席时不渲染、partial 必须读得出来、
// tool_ms 与 tool_span_ms 同时可见（取其一当另一个用就是在撒谎）。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { TimingChip } from './TimingChip'
import type { TaskTiming } from '../../api/types'

const timing: TaskTiming = {
  total_ms: 184_300, api_ms: 121_500, tool_ms: 71_200, tool_span_ms: 58_400,
  other_ms: 4_400, partial: false,
  buckets: [
    { label: 'Bash', dur_ms: 52_100, count: 9, sub: [
      { label: 'go test', dur_ms: 41_800, count: 4 },
      { label: 'git status', dur_ms: 10_300, count: 5 },
    ] },
    { label: 'Read', dur_ms: 19_100, count: 23 },
  ],
}

function openPanel(t: TaskTiming = timing) {
  render(
    <div>
      <button type="button">外部</button>
      <TimingChip timing={t} />
    </div>,
  )
  fireEvent.click(screen.getByRole('button', { name: /耗时/ }))
}

describe('TimingChip', () => {
  it('timing 缺席时整体不渲染，不画空表', () => {
    const { container } = render(<TimingChip />)
    expect(container).toBeEmptyDOMElement()
  })

  it('折叠态给总时长', () => {
    render(<TimingChip timing={timing} />)
    expect(screen.getByRole('button', { name: /耗时 3m4s/ })).toBeInTheDocument()
  })

  it('展开后三分法各档可读', () => {
    openPanel()
    expect(screen.getByText('模型')).toBeInTheDocument()
    expect(screen.getByText('2m2s')).toBeInTheDocument()
    expect(screen.getByText('58.4s')).toBeInTheDocument()
    expect(screen.getByText('4.4s')).toBeInTheDocument()
  })

  it('tool_ms > tool_span_ms 时两个数同时可见，不取其一', () => {
    openPanel()
    expect(screen.getByText('58.4s')).toBeInTheDocument()
    expect(screen.getByText('1m11s')).toBeInTheDocument()
  })

  it('partial=true 时能读出「未归类偏大」', () => {
    openPanel({ ...timing, partial: true })
    expect(screen.getByText(/账目不全/)).toBeInTheDocument()
    expect(screen.getByText(/未归类偏大/)).toBeInTheDocument()
  })

  it('partial=false 时不出现那句提示（不制造无谓的警报）', () => {
    openPanel()
    expect(screen.queryByText(/账目不全/)).not.toBeInTheDocument()
  })

  it('排行列出工具名与下钻的命令首词', () => {
    openPanel()
    expect(screen.getByText('Bash')).toBeInTheDocument()
    expect(screen.getByText('go test')).toBeInTheDocument()
    expect(screen.getByText('git status')).toBeInTheDocument()
    expect(screen.getByText('Read')).toBeInTheDocument()
  })

  it('没有 buckets 时不画排行区（历史任务/刚起步的任务）', () => {
    openPanel({ ...timing, buckets: undefined })
    expect(screen.queryByText('工具排行')).not.toBeInTheDocument()
  })

  it('点外部关掉浮层', () => {
    openPanel()
    fireEvent.mouseDown(screen.getByText('外部'))
    expect(screen.queryByText('模型')).not.toBeInTheDocument()
  })

  it('按 Esc 关掉浮层', () => {
    openPanel()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByText('模型')).not.toBeInTheDocument()
  })
})
