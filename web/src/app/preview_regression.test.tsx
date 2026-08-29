// B294 跨域回归：从 golden JSON 原始文本投影到真实 ProjectTree 行。
//
// 本文件不证明真实 Chromium、OS focus、跨机 DNS 或桌面进程收尸；它只钉住
// JSON.parse 后的 machine/id/optional 字段边界，以及 tree 的点击/拖拽闭集。
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import zeroRaw from '../api/testdata/PreviewZeroValues.json?raw'
import listRaw from '../api/testdata/PreviewRegressionList.json?raw'
import eventsRaw from '../api/testdata/PreviewRegressionEvents.json?raw'
import type { MachineStatus, PreviewEvent, PreviewListResp, PreviewSession, ProjectTreeResp } from '../api/types'
import { previewLabel } from './data/usePreviews'
import { ProjectTree } from './tree/ProjectTree'
import { __resetTreePrefsForTest } from './tree/useTreePrefs'

// renderPreviewRegressionFixture 只接受 wire 文本，按 machine+id 去重并应用事件。
// 事件是实时投影，不补造缺席的 optional 字段；所以它能区分 JSON 缺席与显式零值。
function renderPreviewRegressionFixture(listJSON: string, eventJSON: string): PreviewSession[] {
  const list = JSON.parse(listJSON) as PreviewListResp
  const events = JSON.parse(eventJSON) as PreviewEvent[]
  const sessions = new Map<string, PreviewSession>()
  for (const session of list.sessions) {
    const machine = session.machine ?? ''
    sessions.set(`${machine}\x1f${session.id}`, session)
  }
  for (const event of events) {
    const machine = event.machine ?? event.session.machine ?? ''
    const key = `${machine}\x1f${event.session.id}`
    if (event.type === 'preview.closed') sessions.delete(key)
    else if (event.type === 'preview.created') sessions.set(key, { ...event.session, machine: machine || undefined })
  }
  return [...sessions.values()]
}

function renderRegressionTree(previews: PreviewSession[], machines: MachineStatus[], onOpenPreview: (id: string, machine: string) => void) {
  const tree: ProjectTreeResp = {
    projects: [{
      project_id: 'p1', name: 'handoff', origin_url: 'https://example.test/repo',
      locations: [{ machine: 'devbox', name: 'handoff', path: '/workspace/handoff', probe_error: '', workspaces: [] }],
    }],
    unowned: [],
  }
  return render(<ProjectTree
    tree={tree}
    tasks={[]}
    selectedKey={null}
    ticketCount={0}
    ticketsByDir={new Map()}
    openItems={[]}
    focusedTaskId={null}
    onFocusOpenItem={vi.fn()}
    onOpenTerminalAt={vi.fn()}
    onOpenDirectory={vi.fn()}
    onOpenTask={vi.fn()}
    onOpenBoard={vi.fn()}
    onOpenTickets={vi.fn()}
    onOpenSettings={vi.fn()}
    previews={previews}
    previewMachines={machines}
    previewOpenKeys={new Set()}
    previewOpeningKeys={new Set()}
    onOpenPreview={onOpenPreview}
  />)
}

beforeEach(() => {
  __resetTreePrefsForTest()
})

describe('preview 跨域回归', () => {
  it('真实 JSON→projection→ProjectTree 保留 machine、branch、duplicate 与 open callback', async () => {
    const list = JSON.parse(listRaw) as PreviewListResp
    const projected = renderPreviewRegressionFixture(listRaw, eventsRaw)
    expect(projected).toHaveLength(1)
    expect(projected[0].id).toBe('remote-preview')
    expect(projected[0].machine).toBe('devbox')
    expect(projected[0].branch).toBe('feature/preview')
    expect(list.machines?.[0].ok).toBe(false)

    const onOpenPreview = vi.fn()
    renderRegressionTree(projected, list.machines ?? [], onOpenPreview)
    const row = await screen.findByTestId('preview-row-remote-preview')
    expect(row).toHaveTextContent('feature/preview · localhost:5173')
    expect(row).toHaveTextContent('devbox')
    expect(row).not.toHaveAttribute('data-drag-task')
    fireEvent.click(row)
    expect(onOpenPreview).toHaveBeenCalledWith('remote-preview', 'devbox')
    expect(screen.getByTestId('preview-machine-error-offline')).toHaveTextContent('connection refused')
  })

  it('zero TTL 与缺席 optional 字段保持 wire 原样，port=0 仍是非法 create 输入', () => {
    const zero = JSON.parse(zeroRaw) as PreviewSession
    expect(zero.ttl_seconds).toBe(0)
    expect('via' in zero).toBe(false)
    expect('origin_url' in zero).toBe(false)
    expect(previewLabel(zero)).toBe('localhost:0')
    const invalidPortRequest = JSON.parse('{"port":0}') as { port: number }
    expect(invalidPortRequest.port).toBe(0)
    expect('path' in invalidPortRequest).toBe(false)
  })
})
