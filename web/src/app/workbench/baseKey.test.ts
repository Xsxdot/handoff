// 两处 key 生成必须产出同一个字符串。对不上就会出现「左栏点进这个目录，
// 恢复出来的终端却在另一个组里」——这是 restore.ts 里早就写下的告诫。
//
// 职责：验证树节点与 PTY 会话反解对工作树 key 的机器维度约定。
// 边界：不测试目录树渲染或会话恢复副作用。
import { describe, expect, it } from 'vitest'
import { workspaceBase } from '../tree/ProjectTree'
import { baseOfSession } from './restore'
import type { PtySession } from '../../api/types'

const project = { project_id: 'p1', name: 'handoff', locations: [] } as never
const ws = { path: '/repo/a', branch: 'feature/x', is_main: false } as never

function session(machine: string): PtySession {
  return {
    id: 'S1', machine, base_path: '/repo/a', base_kind: 'workspace', shell: '/bin/bash',
    created_at: '2026-08-20T10:00:00+08:00', cols: 80, rows: 24, attached: 0,
    foreground: false, pid: 1, bytes_out: 0, incompatible: false,
  }
}

describe('工作树基准的 key 带机器维度', () => {
  it('本机（machine 为空串）时 key 逐字节等于 path', () => {
    expect(workspaceBase(project, '', ws).key).toBe('/repo/a')
    expect(baseOfSession(session('')).key).toBe('/repo/a')
  })

  it('远端机器时 key 是 path@machine', () => {
    expect(workspaceBase(project, 'linux-01', ws).key).toBe('/repo/a@linux-01')
    expect(baseOfSession(session('linux-01')).key).toBe('/repo/a@linux-01')
  })

  it('两台机器上同路径的工作树不再撞 key', () => {
    const a = workspaceBase(project, 'linux-01', ws).key
    const b = workspaceBase(project, 'win-b37', ws).key
    expect(a).not.toBe(b)
  })

  it('两处生成器对同一台机器产出同一个 key', () => {
    for (const m of ['', 'linux-01', 'win-b37']) {
      expect(workspaceBase(project, m, ws).key).toBe(baseOfSession(session(m)).key)
    }
  })
})
