// useMobileNav 的行为契约测试（B369.7）：派生规则、写入 URL 形状、push/replace
// 纪律、桌面直通、normalize 三条。URL 断言经 Router 内探针（useLocation +
// useNavigationType）读取，不 mock navigate。
import { act, cleanup, renderHook } from '@testing-library/react'
import { MemoryRouter, useLocation, useNavigationType } from 'react-router-dom'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it } from 'vitest'
import { useMobileNav, type MobileNav } from './useMobileNav'

// lastNav 记录探针最近一次渲染到的 URL 与导航动作类型（PUSH/REPLACE/POP）。
let lastNav: { url: string; action: string } | null = null

function NavProbe() {
  const location = useLocation()
  const action = useNavigationType()
  lastNav = { url: location.pathname + location.search, action }
  return null
}

function makeWrapper(entry: string) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <MemoryRouter initialEntries={[entry]}>
        <NavProbe />
        {children}
      </MemoryRouter>
    )
  }
}

function mountNav(entry = '/', compact = true) {
  return renderHook(() => useMobileNav({ compact }), { wrapper: makeWrapper(entry) })
}

// drive 在 act 内调用一个导航动作，返回动作后的 { url, action }。
function drive(nav: MobileNav, run: (n: MobileNav) => void): { url: string; action: string } {
  act(() => { run(nav) })
  return lastNav!
}

beforeEach(() => {
  cleanup()
  lastNav = null
})

describe('useMobileNav 派生规则（compact 读 URL）', () => {
  it.each([
    ['/', 'sessions'],
    ['/?tab=projects', 'projects'],
    ['/?tab=settings', 'settings'],
    ['/?tab=bogus', 'sessions'],
    ['/cards', 'cards'],
    ['/cards?card=B1&from=session-s1', 'cards'],
  ])('初值 %s → tab=%s', (entry, expected) => {
    const { result } = mountNav(entry)
    expect(result.current.tab).toBe(expected)
  })

  it('detail / dirKey / from 按参数派生；非法 from 按无来源处理', () => {
    const detailed = mountNav('/?detail=1')
    expect(detailed.result.current.detail).toBe(true)
    cleanup()
    const withDir = mountNav(`/?tab=projects&dir=${encodeURIComponent('/w/b2-b3')}`)
    expect(withDir.result.current.dirKey).toBe('/w/b2-b3')
    cleanup()
    const withFrom = mountNav('/?from=session-s1')
    expect(withFrom.result.current.from).toBe('session-s1')
    cleanup()
    const badFrom = mountNav('/?from=weird')
    expect(badFrom.result.current.from).toBeNull()
    cleanup()
    const cardFrom = mountNav('/cards?card=B1&from=card-B2')
    expect(cardFrom.result.current.from).toBe('card-B2')
  })
})

describe('useMobileNav 写入 URL 形状（compact）', () => {
  it('setTab：cards 写 pathname /cards，其余写 /?tab=<t>', () => {
    const { result } = mountNav('/')
    expect(drive(result.current, (n) => n.setTab('projects')).url).toBe('/?tab=projects')
    expect(drive(result.current, (n) => n.setTab('cards')).url).toBe('/cards')
  })

  it('enterDetail：/ ?tab=<tab>&detail=1[&from=…]；显式 ctx 覆盖派生 tab', () => {
    const { result } = mountNav('/')
    expect(drive(result.current, (n) => n.enterDetail()).url).toBe('/?tab=sessions&detail=1')
    expect(drive(result.current, (n) => n.enterDetail({ from: 'card-B1', tab: 'cards' })).url)
      .toBe('/?tab=cards&detail=1&from=card-B1')
  })

  it('enterDetail 随行 dir：目录覆盖层里下钻后返回条能逐级回到目录', () => {
    const { result } = mountNav('/?tab=projects&dir=%2Fw%2Fb2-b3')
    const after = drive(result.current, (n) => n.enterDetail())
    expect(after.url).toBe('/?tab=projects&detail=1&dir=%2Fw%2Fb2-b3')
  })

  it('exitDetail：清 detail+from、留 tab/dir（replace）', () => {
    const { result } = mountNav('/?tab=sessions&detail=1&from=session-s1')
    const after = drive(result.current, (n) => n.exitDetail())
    expect(after.url).toBe('/?tab=sessions')
    expect(after.action).toBe('REPLACE')
    cleanup()
    const withDir = mountNav('/?tab=projects&detail=1&dir=%2Fw%2Fb2-b3')
    expect(drive(withDir.result.current, (n) => n.exitDetail()).url).toBe('/?tab=projects&dir=%2Fw%2Fb2-b3')
  })

  it('setDir：开=push /?tab=projects&dir=<k>；关=replace 清 dir 留 tab', () => {
    const { result } = mountNav('/?tab=projects')
    const opened = drive(result.current, (n) => n.setDir('/w/b2-b3'))
    expect(opened.url).toBe(`/?tab=projects&dir=${encodeURIComponent('/w/b2-b3')}`)
    expect(opened.action).toBe('PUSH')
    const closed = drive(result.current, (n) => n.setDir(null))
    expect(closed.url).toBe('/?tab=projects')
    expect(closed.action).toBe('REPLACE')
  })

  it('openCard：push /cards?card=<id>[&from=…]；保留 project、丢 / 宿主语汇与旧 from', () => {
    const { result } = mountNav('/')
    const entered = drive(result.current, (n) => n.openCard('B1', 'session-s1'))
    expect(entered.url).toBe('/cards?card=B1&from=session-s1')
    expect(entered.action).toBe('PUSH')
    cleanup()
    // 从带 project 的看板再点别的卡：project 保留（筛选不清），旧 from 不跟随
    //（来源链到此为止）
    const filtered = mountNav('/cards?project=p1&card=B1&from=session-s1')
    const switched = drive(filtered.result.current, (n) => n.openCard('B2'))
    expect(switched.url).toBe('/cards?project=p1&card=B2')
    cleanup()
    const fromSlash = mountNav('/?tab=projects&detail=1&dir=k')
    const jumped = drive(fromSlash.result.current, (n) => n.openCard('B9'))
    expect(jumped.url).toBe('/cards?card=B9')
  })

  it('closeCard：replace 剥 card+from、保留 project；无残参时落 /cards', () => {
    const { result } = mountNav('/cards?card=B1&project=p1&from=session-s1')
    const closed = drive(result.current, (n) => n.closeCard())
    expect(closed.url).toBe('/cards?project=p1')
    expect(closed.action).toBe('REPLACE')
    cleanup()
    const bare = mountNav('/cards?card=B1')
    expect(drive(bare.result.current, (n) => n.closeCard()).url).toBe('/cards')
  })

  it('幂等写：同址动作不产生第二次导航', () => {
    const { result } = mountNav('/')
    drive(result.current, (n) => n.setTab('projects'))
    const before = lastNav!
    drive(result.current, (n) => n.setTab('projects'))
    expect(lastNav).toBe(before)
  })
})

describe('useMobileNav push/replace 纪律（compact）', () => {
  it('前进类 push；返回/取消类与瞬态跳板、normalize 改写 replace', () => {
    const { result } = mountNav('/')
    expect(drive(result.current, (n) => n.setTab('projects')).action).toBe('PUSH')
    expect(drive(result.current, (n) => n.enterDetail()).action).toBe('PUSH')
    expect(drive(result.current, (n) => n.exitDetail()).action).toBe('REPLACE')
    expect(drive(result.current, (n) => n.setDir('k')).action).toBe('PUSH')
    expect(drive(result.current, (n) => n.setDir(null)).action).toBe('REPLACE')
    expect(drive(result.current, (n) => n.openCard('B1')).action).toBe('PUSH')
    expect(drive(result.current, (n) => n.closeCard()).action).toBe('REPLACE')
    cleanup()
    // /tasks/:id 跳板是瞬态路径：enterDetail replace，不留历史格
    const onJump = mountNav('/tasks/T1')
    expect(drive(onJump.result.current, (n) => n.enterDetail()).action).toBe('REPLACE')
    cleanup()
    const gate = mountNav('/')
    expect(drive(gate.result.current, (n) => n.normalize({ ledgerEnabled: false, ledgerLoading: false })).action).toBe('REPLACE')
  })
})

describe('useMobileNav normalize 三条（compact）', () => {
  it('① 账本未启用（探测已结束）：sessions/cards（含 /cards 直达与缺省首屏）改写 /?tab=projects', () => {
    const gate = { ledgerEnabled: false, ledgerLoading: false }
    const home = mountNav('/')
    expect(drive(home.result.current, (n) => n.normalize(gate)).url).toBe('/?tab=projects')
    cleanup()
    const cards = mountNav('/cards?card=B1&from=session-s1')
    expect(drive(cards.result.current, (n) => n.normalize(gate)).url).toBe('/?tab=projects')
    cleanup()
    const projects = mountNav('/?tab=projects')
    drive(projects.result.current, (n) => n.normalize(gate))
    expect(lastNav!.url).toBe('/?tab=projects')
  })

  it('① 探测未结束（loading）不动手，避免加载期误判', () => {
    const { result } = mountNav('/')
    drive(result.current, (n) => n.normalize({ ledgerEnabled: false, ledgerLoading: true }))
    expect(lastNav!.url).toBe('/')
  })

  it('② / 上 tab=cards 且无 detail replace 成 /cards；有 detail（任务现场）不动', () => {
    const gate = { ledgerEnabled: true, ledgerLoading: false }
    const stray = mountNav('/?tab=cards')
    const fixed = drive(stray.result.current, (n) => n.normalize(gate))
    expect(fixed.url).toBe('/cards')
    expect(fixed.action).toBe('REPLACE')
    cleanup()
    const scene = mountNav('/?tab=cards&detail=1&from=card-B1')
    drive(scene.result.current, (n) => n.normalize(gate))
    expect(lastNav!.url).toBe('/?tab=cards&detail=1&from=card-B1')
  })

  it('③ 残参清理：dir 只在项目 tab 消费；from 在 / 上只有 detail=1 消费（/cards 的 from 是任务跳转 seam 的消费者，不清）', () => {
    const gate = { ledgerEnabled: true, ledgerLoading: false }
    const strayDir = mountNav('/?tab=sessions&dir=k')
    expect(drive(strayDir.result.current, (n) => n.normalize(gate)).url).toBe('/?tab=sessions')
    cleanup()
    const strayFrom = mountNav('/?from=session-s1')
    expect(drive(strayFrom.result.current, (n) => n.normalize(gate)).url).toBe('/')
    cleanup()
    const legitFrom = mountNav('/cards?card=B1&from=session-s1')
    drive(legitFrom.result.current, (n) => n.normalize(gate))
    expect(lastNav!.url).toBe('/cards?card=B1&from=session-s1')
    cleanup()
    const legitDir = mountNav('/?tab=projects&dir=k')
    drive(legitDir.result.current, (n) => n.normalize(gate))
    expect(lastNav!.url).toBe('/?tab=projects&dir=k')
  })
})

describe('useMobileNav 桌面直通（compact=false）', () => {
  it('读：三状态走内部 state，URL 参数不参与派生', () => {
    const { result } = mountNav('/?tab=projects&detail=1&dir=k&from=session-s1', false)
    expect(result.current.tab).toBe('sessions')
    expect(result.current.detail).toBe(false)
    expect(result.current.dirKey).toBeNull()
    expect(result.current.from).toBeNull()
  })

  it('写：setter 只改 state，URL 一字不动', () => {
    const { result } = mountNav('/', false)
    drive(result.current, (n) => n.setTab('cards'))
    expect(result.current.tab).toBe('cards')
    drive(result.current, (n) => n.enterDetail())
    expect(result.current.detail).toBe(true)
    drive(result.current, (n) => n.setDir('k'))
    expect(result.current.dirKey).toBe('k')
    drive(result.current, (n) => n.openCard('B1', 'session-s1'))
    drive(result.current, (n) => n.closeCard())
    expect(lastNav!.url).toBe('/')
    drive(result.current, (n) => n.exitDetail())
    expect(result.current.detail).toBe(false)
    expect(lastNav!.url).toBe('/')
  })
})
