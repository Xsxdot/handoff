import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import type { Machine } from '../../api/types'
import { ApiError, fetchDiscipline, saveDisciplineMapping } from '../../api/client'
import { MachineDiscipline } from './MachineDiscipline'

vi.mock('../../api/client', async () => {
	const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
	return { ...actual, fetchDiscipline: vi.fn(), saveDisciplineMapping: vi.fn() }
})

const online: Machine = {
	name: 'mac-02', addr: '10.0.0.2:7777', reachable: true, version: 'v1',
	executors: ['opencode', 'codex', 'grok'], default_executor: 'codex',
	probe_ms: 42, active_tasks: 0, error: '',
}
const offline: Machine = { ...online, reachable: false, error: 'connection refused' }
const RESP = {
	dir: '/d',
	builtins: [
		{ tier: 'subagent', content: 'A' },
		{ tier: 'single-context', content: 'B' },
	],
	files: [{ name: 'codex-strict.md', size: 12, sha256: 'abc' }],
	bindings: [
		{ executor: 'codex', mode: 'file' as const, file: 'codex-strict.md', default_tier: 'single-context' },
		{ executor: 'grok', mode: 'off' as const, default_tier: 'single-context' },
		{ executor: 'opencode', mode: 'default' as const, default_tier: 'subagent' },
	],
}

beforeEach(() => {
	vi.clearAllMocks()
	vi.mocked(fetchDiscipline).mockResolvedValue(RESP as never)
})

it('三档下拉按当前配置回显', async () => {
	render(<MachineDiscipline machine={online} />)
	expect(await screen.findByLabelText('codex 的纪律块')).toHaveValue('file:codex-strict.md')
	expect(screen.getByLabelText('opencode 的纪律块')).toHaveValue('default')
	expect(screen.getByLabelText('grok 的纪律块')).toHaveValue('off')
})

it('默认档的选项文案写明是哪一版内置', async () => {
  render(<MachineDiscipline machine={online} />)
  expect(await screen.findByRole('option', { name: '内置默认（subagent）' })).toBeInTheDocument()
  expect(screen.getAllByRole('option', { name: '内置默认（single-context）' })).not.toHaveLength(0)
})

it('改动标脏，保存后送出三档并清脏', async () => {
	vi.mocked(saveDisciplineMapping).mockResolvedValue(RESP as never)
	render(<MachineDiscipline machine={online} />)
	await userEvent.selectOptions(await screen.findByLabelText('opencode 的纪律块'), 'off')
	expect(screen.getByText('有未保存的改动')).toBeInTheDocument()
	await userEvent.click(screen.getByRole('button', { name: '保存' }))
	await waitFor(() => expect(saveDisciplineMapping).toHaveBeenCalledWith('mac-02', [
		{ executor: 'codex', mode: 'file', file: 'codex-strict.md', default_tier: 'single-context' },
		{ executor: 'grok', mode: 'off', default_tier: 'single-context' },
		{ executor: 'opencode', mode: 'off', default_tier: 'subagent' },
	]))
	await waitFor(() => expect(screen.queryByText('有未保存的改动')).not.toBeInTheDocument())
})

it('保存失败时原文展示后端错误且不清脏', async () => {
	vi.mocked(saveDisciplineMapping).mockRejectedValue(new ApiError(400, 'codex 指定的纪律块文件不可用：读取纪律块文件 …/nope.md: no such file'))
	render(<MachineDiscipline machine={online} />)
	await userEvent.selectOptions(await screen.findByLabelText('codex 的纪律块'), 'default')
	await userEvent.click(screen.getByRole('button', { name: '保存' }))
	expect(await screen.findByText(/纪律块文件不可用/)).toBeInTheDocument()
	expect(screen.getByText('有未保存的改动')).toBeInTheDocument()
})

it('断开的机器不发请求也不给控件', async () => {
	render(<MachineDiscipline machine={offline} />)
	expect(await screen.findByText(/机器已断开/)).toBeInTheDocument()
	expect(screen.getByText(/connection refused/)).toBeInTheDocument()
	expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
	expect(fetchDiscipline).not.toHaveBeenCalled()
})
