// b283-redloop.test.ts —— B283 红色反馈回路（排查产物，未定稿前不并入 restore.test.ts）。
//
// 复现的循环（真机证据：~/.handoff/handoff.db dock 快照 4 个 UUID tab、
// agentd.log 11:17:57.331/.348/.361/.372 的 41ms 四连发）：
//
//   打开 N   会话列表扇出缺了某台机器（冷启动/超时，日志 114 条扇出失败），
//           快照里引用的活会话 S1 被误判死亡 → prune 剥掉 sessionId，tab 留位；
//           窗口一开 TerminalTab 无会话即自建 S2 并写回 → S1 在远端永久存活成孤儿。
//   打开 N+1 S1 活着回到列表，且不被快照引用 → restore ③ 当孤儿收编 → tab +1。
//
// 不变式（用户语言）：同一份现场连续两次打开桌面端，悬浮窗 tab 数不得增长。
import { describe, expect, it } from 'vitest'
import type { PtySession, WorkbenchStateResp } from '../../api/types'
import { encodeDock } from '../homedock/dockPersist'
import type { HomeTab } from '../homedock/useHomeDock'
import { buildRestore } from './restore'

function homeSession(id: string, machine: string): PtySession {
  return {
    id, machine, base_path: '', base_kind: 'home', shell: '/bin/zsh',
    created_at: '2026-08-28T11:17:57+08:00', cols: 166, rows: 44, attached: 0,
    foreground: false, pid: 1, bytes_out: 0, incompatible: false,
  }
}

function dockWith(tabs: HomeTab[]): string {
  return encodeDock({ tabs, activeId: tabs[0]?.id ?? null, windowOpen: false, geom: { x: 8, y: 8, w: 620, h: 340 }, maximized: false })
}

function state(dock: string): WorkbenchStateResp {
  return { selected: '', dock, bases: [] }
}

const VIEW = { vw: 1280, vh: 800, inset: 0 }

describe('B283 红色回路：两次打开之间悬浮窗 tab 数不得增长', () => {
  it('上一轮被误判剥掉引用的会话，这一轮活着回来不得被收编成新 tab', () => {
    // 打开 N：快照引用 mac-02 的 home 会话 S1，但扇出没带回它（机器缺席）。
    const open1 = buildRestore({
      state: state(dockWith([{ id: 'h1', kind: 'terminal', seq: 1, machine: 'mac-02', sessionId: 'S1' }])),
      sessions: [], // 扇出缺机器：S1 明明活着，列表里却没有
      ...VIEW,
    })
    expect(open1.dock?.tabs[0]?.sessionId).toBeUndefined() // 引用被剥，tab 留位（现状行为，先钉住）

    // 两次打开之间：窗口打开，h1 无会话即自建 S2 并写回快照；S1 在 mac-02 上还活着。
    const snapshotBetweenOpens = dockWith([{ id: 'h1', kind: 'terminal', seq: 1, machine: 'mac-02', sessionId: 'S2' }])

    // 打开 N+1：S1 与 S2 都活着回到列表。
    const open2 = buildRestore({
      state: state(snapshotBetweenOpens),
      sessions: [homeSession('S1', 'mac-02'), homeSession('S2', 'mac-02')],
      ...VIEW,
    })

    // 不变式：悬浮窗还是那 1 个 tab，不许把 S1 当孤儿再收编一个。
    // 现状：S1 live 且不在 used 集合 → 收编 → 2 个 tab → 本断言红。
    expect(open2.dock?.tabs).toHaveLength(1)
  })
})
