import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { fetchDiscipline, fetchDisciplineFile, saveDisciplineFile } from '../../api/client'
import { useMachines } from '../data/useMachines'
import { DisciplinePage } from './DisciplinePage'

vi.mock('../data/useMachines', () => ({ useMachines: vi.fn() }))
vi.mock('../../api/client', async () => {
	const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
	return {
		...actual,
		fetchDiscipline: vi.fn(),
		fetchDisciplineFile: vi.fn(),
		saveDisciplineFile: vi.fn(),
	}
})

const RESP = {
	dir: '/home/dev/.handoff/discipline',
	builtins: [
		{ tier: 'subagent', content: '内置 A 版正文' },
		{ tier: 'single-context', content: '内置 B 版正文' },
	],
	files: [{ name: 'codex-strict.md', size: 12, sha256: 'abc' }],
	bindings: [
		{ executor: 'codex', mode: 'file' as const, file: 'codex-strict.md', default_tier: 'single-context' },
		{ executor: 'opencode', mode: 'default' as const, default_tier: 'subagent' },
	],
}

beforeEach(() => {
	vi.clearAllMocks()
	vi.mocked(useMachines).mockReturnValue({
		data: { machines: [
			{ name: '', addr: '127.0.0.1:7777', reachable: true, version: 'v1', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '' },
			{ name: 'mac-02', addr: '10.0.0.2:7777', reachable: false, version: '', executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: 'connection refused' },
		] },
		disconnected: false, sessionExpired: false, errorText: '', refresh: vi.fn(),
	} as never)
	vi.mocked(fetchDiscipline).mockResolvedValue(RESP as never)
	vi.mocked(fetchDisciplineFile).mockResolvedValue({ content: '我的纪律', size: 12, sha256: 'abc' } as never)
})

it('内置版只读且给出「以此为模板新建」', async () => {
	render(<DisciplinePage />)
	await userEvent.click(await screen.findByRole('button', { name: /subagent/ }))
	expect(await screen.findByRole('textbox', { name: /纪律块正文/ })).toHaveAttribute('readonly')
	expect(screen.getByRole('button', { name: '以此为模板新建' })).toBeInTheDocument()
	expect(screen.queryByRole('button', { name: '保存' })).not.toBeInTheDocument()
})

it('用户文件可编辑并保存，正文与 base_sha256 一并送出', async () => {
	vi.mocked(saveDisciplineFile).mockResolvedValue({ sha256: 'def', size: 20 } as never)
	render(<DisciplinePage />)
	await userEvent.click(await screen.findByRole('button', { name: /codex-strict\.md/ }))
	const box = await screen.findByRole('textbox', { name: /纪律块正文/ })
	await userEvent.clear(box)
	await userEvent.type(box, '新正文')
	await userEvent.click(screen.getByRole('button', { name: '保存' }))
	await waitFor(() => expect(saveDisciplineFile).toHaveBeenCalledWith(
		'', 'codex-strict.md', { content: '新正文', base_sha256: 'abc' }))
})

it('每个文件标注被哪些 executor 引用', async () => {
	render(<DisciplinePage />)
	expect(await screen.findByText(/codex 在用/)).toBeInTheDocument()
	// opencode 是 default 档，引用的是内置 subagent 版
	expect(screen.getByText(/opencode 在用/)).toBeInTheDocument()
})

it('断开的机器不给编辑器，给断开原因原文', async () => {
	render(<DisciplinePage />)
	await userEvent.click(screen.getByRole('button', { name: /mac-02/ }))
	expect(await screen.findByText(/connection refused/)).toBeInTheDocument()
	expect(screen.queryByRole('textbox', { name: /纪律块正文/ })).not.toBeInTheDocument()
	expect(fetchDiscipline).not.toHaveBeenCalledWith('mac-02')
})

it('正在编辑时机器列表刷新不覆盖输入', async () => {
	const { rerender } = render(<DisciplinePage />)
	await userEvent.click(await screen.findByRole('button', { name: /codex-strict\.md/ }))
	const box = await screen.findByRole('textbox', { name: /纪律块正文/ })
	await userEvent.clear(box)
	await userEvent.type(box, '编辑中')
	rerender(<DisciplinePage />)
	expect(box).toHaveValue('编辑中')
})
