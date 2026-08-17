// 块组件的行为测试：只断言行为，不测样式。
//
// 四条硬要求：思维链默认折叠；工具卡默认折叠且三种状态各自可辨；事件标记
// **不可点**（审批入口唯一在工单面板）；未知类型可展开看原始 JSON。
import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import type { ToolBlock } from './frames'
import { TextBlock } from './TextBlock'
import { ThinkingBlock } from './ThinkingBlock'
import { ToolCard } from './ToolCard'
import { UnknownBlock } from './UnknownBlock'
import { EventChip } from './EventChip'
import { UserInstructionBlock } from './UserInstructionBlock'
import { DeliverySummaryCard } from './DeliverySummaryCard'

const tool = (o: Partial<ToolBlock>): ToolBlock => ({
  kind: 'tool', key: 'f1', turn: 1,
  tool: 'bash', input: 'go test ./...',
  inputTruncated: false, inputBytes: 0,
  status: 'ok', output: 'ok\t0.2s',
  outputTruncated: false, outputBytes: 0,
  ...o,
})

describe('TextBlock', () => {
  it('正文永远展开', () => {
    render(<TextBlock text="我来实现它。" />)
    expect(screen.getByText('我来实现它。')).toBeInTheDocument()
  })
})

describe('ThinkingBlock', () => {
  it('默认折叠：只显示字数摘要，正文不可见', () => {
    render(<ThinkingBlock text="先看一下测试怎么写的" />)
    expect(screen.getByRole('button', { name: /思维链/ })).toBeInTheDocument()
    expect(screen.queryByText('先看一下测试怎么写的')).not.toBeInTheDocument()
  })

  it('点开后正文可见', () => {
    render(<ThinkingBlock text="先看一下测试怎么写的" />)
    fireEvent.click(screen.getByRole('button', { name: /思维链/ }))
    expect(screen.getByText('先看一下测试怎么写的')).toBeInTheDocument()
  })
})

describe('ToolCard', () => {
  it('默认折叠：显示工具名与参数摘要，输入输出不可见', () => {
    render(<ToolCard block={tool({})} taskState="completed" />)
    expect(screen.getByText('跑命令')).toBeInTheDocument()
    expect(screen.queryByText('ok\t0.2s')).not.toBeInTheDocument()
  })

  it('展开后能看到输入与输出', () => {
    render(<ToolCard block={tool({})} taskState="completed" />)
    fireEvent.click(screen.getByRole('button', { name: /跑命令/ }))
    // 入参「go test ./...」既出现在折叠态的参数摘要里，也出现在展开后的输入区，
    // 用 getAllByText 断言展开态确实多渲染了一份
    expect(screen.getAllByText(/go test \.\/\.\.\./).length).toBeGreaterThan(0)
    expect(screen.getByText(/ok/)).toBeInTheDocument()
  })

  it('三种状态各自可辨：成功 / 失败 / 未返回', () => {
    const { unmount: u1 } = render(<ToolCard block={tool({ status: 'ok' })} taskState="completed" />)
    expect(screen.getByText('成功')).toBeInTheDocument()
    u1()
    const { unmount: u2 } = render(<ToolCard block={tool({ status: 'error' })} taskState="completed" />)
    expect(screen.getByText('失败')).toBeInTheDocument()
    u2()
    render(<ToolCard block={tool({ status: null, output: '' })} taskState="waiting_review" />)
    expect(screen.getByText('未返回')).toBeInTheDocument()
  })

  it('任务还在跑时未配对的调用显示「进行中」而不是「未返回」', () => {
    render(<ToolCard block={tool({ status: null, output: '' })} taskState="running" />)
    expect(screen.getByText('进行中')).toBeInTheDocument()
  })

  it('截断提示带原始字节数', () => {
    render(<ToolCard block={tool({ outputTruncated: true, outputBytes: 141882 })} taskState="completed" />)
    fireEvent.click(screen.getByRole('button', { name: /跑命令/ }))
    expect(screen.getByText(/141882/)).toBeInTheDocument()
    expect(screen.getByText(/已截断/)).toBeInTheDocument()
  })
})

describe('ToolCard 工具名中文化', () => {
  const mk = (toolName: string) => ({
    kind: 'tool', key: 'k1', turn: 1, tool: toolName, input: 'x', inputTruncated: false,
    inputBytes: 0, status: 'ok', output: '', outputTruncated: false, outputBytes: 0,
  }) as ToolBlock
  it('已知工具名映射为中文', () => {
    render(<ToolCard block={mk('commandExecution')} taskState="waiting_review" />)
    expect(screen.getByText('跑命令')).toBeInTheDocument()
  })
  it('未知工具名原样透出', () => {
    render(<ToolCard block={mk('someNewTool')} taskState="waiting_review" />)
    expect(screen.getByText('someNewTool')).toBeInTheDocument()
  })
})

describe('EventChip', () => {
  it('白名单事件渲染人话短语', () => {
    render(<EventChip event="completed" ts="2026-08-17T10:00:00Z" />)
    expect(screen.getByText(/一轮结束，进入待审/)).toBeInTheDocument()
  })
  it('未知事件原样透出', () => {
    render(<EventChip event="mystery_event" ts="2026-08-17T10:00:00Z" />)
    expect(screen.getByText(/mystery_event/)).toBeInTheDocument()
  })
  it.each(['approver_decision', 'permission_reuse', 'progress'])('后台审计噪声不渲染（%s）', (event) => {
    render(<EventChip event={event} ts="2026-08-17T10:00:00Z" />)
    expect(screen.queryByText(event)).not.toBeInTheDocument()
  })
})

describe('UserInstructionBlock', () => {
  it('渲染审核者身份行与指令原文', () => {
    render(<UserInstructionBlock text="补上变异测试记录" ts="2026-08-17T14:20:00Z" />)
    expect(screen.getByText(/审核者/)).toBeInTheDocument()
    expect(screen.getByText('补上变异测试记录')).toBeInTheDocument()
  })
})

describe('DeliverySummaryCard', () => {
  it('渲染命中的字段，缺席字段不渲染行', () => {
    render(<DeliverySummaryCard delivery={{ branch: 'bench/b93', summary: '全落地' }} />)
    expect(screen.getByText('bench/b93')).toBeInTheDocument()
    expect(screen.getByText('全落地')).toBeInTheDocument()
    expect(screen.queryByText('commit')).not.toBeInTheDocument()
  })
})

describe('UnknownBlock', () => {
  it('默认折叠，展开后能看到原始 JSON', () => {
    render(<UnknownBlock type="checkpoint" raw='{"seq":20,"type":"checkpoint"}' />)
    expect(screen.getByText(/checkpoint/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /未知帧类型/ }))
    expect(screen.getByText(/"seq":20/)).toBeInTheDocument()
  })
})
