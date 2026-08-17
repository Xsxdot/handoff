// ConversationStream.test.tsx —— 会话流渲染：回合分隔/指令气泡/交付卡/提示行。
import { createRef } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ConversationStream, type ConversationStreamHandle } from './ConversationStream'
import type { Block } from './frames'

const noop = () => {}
const base = {
  taskId: 't1', taskState: 'waiting_review',
  badLines: 0, startOffset: 0, atCap: false, error: null,
  loadingEarlier: false, onLoadEarlier: noop, onRetry: noop, active: false,
}

describe('ConversationStream', () => {
  it('回合分隔线带序号与起因；send 回合渲染审核者气泡', () => {
    const blocks: Block[] = [
      { kind: 'turn', key: 'f1', turn: 1, reason: 'dispatch', ts: '2026-08-17T11:16:00Z', instructions: '' },
      { kind: 'turn', key: 'f2', turn: 2, reason: 'send', ts: '2026-08-17T14:20:00Z', instructions: '补测试' },
    ]
    render(<ConversationStream {...base} blocks={blocks} />)
    expect(screen.getByText(/回合 1/)).toBeInTheDocument()
    expect(screen.getByText(/派发/)).toBeInTheDocument()
    expect(screen.getByText(/续发指令/)).toBeInTheDocument()
    expect(screen.getByText('补测试')).toBeInTheDocument()
    expect(screen.getByText(/审核者/)).toBeInTheDocument()
  })

  it('text 块末尾的报工 trailer 拆成交付摘要卡', () => {
    const blocks: Block[] = [
      { kind: 'text', key: 'f3', turn: 1, text: '全部完成。\n\n{"branch":"bench/x","summary":"落地"}' },
    ]
    render(<ConversationStream {...base} blocks={blocks} />)
    expect(screen.getByText('全部完成。')).toBeInTheDocument()
    expect(screen.getByText('交付摘要')).toBeInTheDocument()
    expect(screen.getByText('bench/x')).toBeInTheDocument()
  })

  it('坏行与帧上限提示为流内元数据行；空流显示等待文案', () => {
    const { rerender } = render(<ConversationStream {...base} blocks={[]} badLines={3} atCap />)
    expect(screen.getByText(/3 行无法解析/)).toBeInTheDocument()
    expect(screen.getByText(/handoff frames/)).toBeInTheDocument()
    rerender(<ConversationStream {...base} blocks={[]} />)
    expect(screen.getByText(/等待模型输出/)).toBeInTheDocument()
  })

  it('startOffset>0 显示加载更早，点击回调', () => {
    const onLoadEarlier = vi.fn()
    render(<ConversationStream {...base} blocks={[]} startOffset={100} onLoadEarlier={onLoadEarlier} />)
    screen.getByRole('button', { name: /加载更早/ }).click()
    expect(onLoadEarlier).toHaveBeenCalled()
  })

  it('error 显示原文与重试按钮', () => {
    render(<ConversationStream {...base} blocks={[]} error="连接断了" />)
    expect(screen.getByRole('alert')).toHaveTextContent('连接断了')
    expect(screen.getByRole('button', { name: /重试/ })).toBeInTheDocument()
  })

  it('连续 ≥3 工具块折叠成组行，点开平铺', () => {
    const tools = ['a', 'b', 'c'].map((k) => ({
      kind: 'tool', key: k, turn: 1, tool: 'commandExecution', input: `cmd-${k}`,
      inputTruncated: false, inputBytes: 0, status: 'ok', output: '',
      outputTruncated: false, outputBytes: 0,
    })) as Block[]
    render(<ConversationStream {...base} blocks={tools} />)
    const row = screen.getByRole('button', { name: /执行了 3 步操作/ })
    expect(screen.queryByText('cmd-a')).not.toBeInTheDocument()
    fireEvent.click(row)
    expect(screen.getByText('cmd-a')).toBeInTheDocument()
  })

  it('组内含失败时摘要标红失败数', () => {
    const mk = (k: string, status: string) => ({
      kind: 'tool', key: k, turn: 1, tool: 'x', input: k, inputTruncated: false,
      inputBytes: 0, status, output: '', outputTruncated: false, outputBytes: 0,
    }) as Block
    render(<ConversationStream {...base} blocks={[mk('a', 'ok'), mk('b', 'error'), mk('c', 'ok')]} />)
    expect(screen.getByText(/1 失败/)).toBeInTheDocument()
  })

  it('running 且流活跃时显示运行中指示', () => {
    render(<ConversationStream {...base} blocks={[]} taskState="running" active />)
    expect(screen.getByText(/模型工作中/)).toBeInTheDocument()
  })

  it('jumpToTurn 对未加载回合触发回翻', () => {
    const onLoadEarlier = vi.fn()
    const ref = createRef<ConversationStreamHandle>()
    render(
      <ConversationStream {...base} ref={ref} blocks={[]} startOffset={100} onLoadEarlier={onLoadEarlier} />,
    )
    ref.current!.jumpToTurn(1)
    expect(onLoadEarlier).toHaveBeenCalled()
  })
})
