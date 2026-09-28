import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { getSquads } from '../../api/scheduling'
import { SettingsPage } from './SettingsPage'
import { __resetWebPrefsForTest } from './useWebPrefs'

vi.mock('../data/useMachines', () => ({
  useMachines: () => ({
    data: {
      machines: [{
        name: '', addr: '127.0.0.1:7777', reachable: true, version: 'v1',
        executors: [], default_executor: '', probe_ms: 0, active_tasks: 0, error: '',
      }],
    },
    disconnected: false,
    sessionExpired: false,
    errorText: '',
    refresh: vi.fn(),
  }),
}))
// B369.10 T10：「执行机与配对」副题要报「已连接 N 台」，N 由树流的探活结果算出来
// ——用 hoisted 容器让用例能改这批机器与「树到没到」（projects 恒空）。
const treeMock = vi.hoisted(() => ({
  loaded: true,
  machines: [] as { name: string; ok: boolean; fetched_at: string; error: string }[],
}))
vi.mock('../data/useProjectTree', () => ({
  useProjectTree: () => ({
    data: treeMock.loaded ? { projects: [], machines: treeMock.machines, unowned: [] } : null,
    disconnected: false,
    sessionExpired: false,
    errorText: '',
    refresh: vi.fn(),
  }),
}))
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    fetchDiscipline: vi.fn().mockResolvedValue({ dir: '/d', builtins: [], files: [], bindings: [] }),
    fetchEnv: vi.fn().mockResolvedValue({ dir: '/d/env', files: [], bindings: [] }),
  }
})
vi.mock('../../api/scheduling', async () => {
  const actual = await vi.importActual<typeof import('../../api/scheduling')>('../../api/scheduling')
  return { ...actual, getSquads: vi.fn() }
})

describe('SettingsPage', () => {
  it('四个分区都在，缺省停在开发机', async () => {
    render(<SettingsPage onClose={vi.fn()} />)
    expect(screen.getByRole('heading', { name: '设置' })).toBeInTheDocument()
    for (const label of ['开发机', '执行纪律', '常规', 'Env 文件']) {
      expect(screen.getByRole('button', { name: label })).toBeInTheDocument()
    }
    await waitFor(() => expect(screen.getAllByText('本机').length).toBeGreaterThan(0))
  })

  it('点「执行纪律」能切到该分区', async () => {
    render(<SettingsPage onClose={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: '执行纪律' }))
    expect(screen.getByRole('heading', { name: '执行纪律' })).toBeInTheDocument()
  })

  it('切到常规分区显示当前浏览器范围说明', () => {
    render(<SettingsPage onClose={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: '常规' }))
    expect(screen.getByText(/只保存在当前浏览器/)).toBeInTheDocument()
  })

  it('切到 Env 文件分区显示真实配置面', async () => {
    render(<SettingsPage onClose={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'Env 文件' }))
    expect(screen.getByRole('heading', { name: 'Env 文件' })).toBeInTheDocument()
    expect(await screen.findByText(/暂无用户文件/)).toBeInTheDocument()
  })

  it('返回工作台调 onClose', () => {
    const onClose = vi.fn()
    render(<SettingsPage onClose={onClose} />)
    fireEvent.click(screen.getByRole('button', { name: '返回工作台' }))
    expect(onClose).toHaveBeenCalled()
  })

  it('opens automation directly from the query string', async () => {
    window.history.pushState({}, '', '/settings?section=automation')
    vi.mocked(getSquads).mockResolvedValue({ carriers: [], squads: [] })
    render(<SettingsPage onClose={vi.fn()} />)
    expect(await screen.findByRole('heading', { name: '自动化' })).toBeVisible()
    expect(screen.getByRole('button', { name: '自动化' })).toHaveAttribute('aria-current', 'true')
    window.history.pushState({}, '', '/')
  })

  it('桌面分支零漂移：双栏 w-40 nav 与六分区按钮仍在（B369.8 反例锁）', () => {
    render(<SettingsPage onClose={vi.fn()} />)
    const nav = document.querySelector('nav')
    expect(nav?.className).toContain('w-40')
    for (const label of ['开发机', '执行纪律', '自动化', '常规', 'Env 文件', '更新']) {
      expect(screen.getByRole('button', { name: label })).toBeInTheDocument()
    }
  })
})

// —— B369.8 T2：compact 两级设置 IA（SettingsHub）——
describe('SettingsPage compact 设置中心', () => {
  beforeEach(() => {
    localStorage.clear()
    __resetWebPrefsForTest()
    treeMock.loaded = true
    treeMock.machines = []
  })

  function renderHub(sub: string | null = null, onSubChange = vi.fn()) {
    return render(<SettingsPage onClose={vi.fn()} compact sub={sub} onSubChange={onSubChange} />)
  }

  it('三节行文（工作方式 → 执行机 → 关于）按序在场；块标题与五入口行同现，pairing 行出列', () => {
    renderHub()
    const heads = screen.getAllByRole('heading', { level: 3 }).map((h) => h.textContent)
    expect(heads.indexOf('工作方式')).toBeLessThan(heads.indexOf('执行机'))
    expect(heads.indexOf('执行机')).toBeLessThan(heads.indexOf('关于'))
    // 行文对齐原型：块标题（就地呈现下由 h3 扮原型 row title）
    expect(screen.getByText('会话打开方式')).toBeInTheDocument()
    expect(screen.getByText('需要你提醒')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '显示与可访问性' })).toBeInTheDocument()
    expect(screen.getByText('关于 handoff')).toBeInTheDocument()
    // hub-note 在场（壳阶段语义）
    expect(screen.getByTestId('settings-hub-note')).toBeInTheDocument()
    // B369.8 的既有 testid 全保绿
    expect(screen.getByTestId('pref-session-open-mode')).toBeInTheDocument()
    expect(screen.getByTestId('pref-badges')).toBeInTheDocument()
    expect(screen.getByTestId('settings-about')).toBeInTheDocument()
    // 入口行：machines 合一 + 原型未覆盖的其余四项；pairing 不在 hub（词表项保留）
    for (const key of ['machines', 'discipline', 'automation', 'env', 'update']) {
      expect(screen.getByTestId(`settings-sub-${key}`)).toBeInTheDocument()
    }
    expect(screen.queryByTestId('settings-sub-pairing')).toBeNull()
    // 二级入口行是主动作触控档（plan §3.3 min-h-11）
    expect(screen.getByTestId('settings-sub-machines').className).toContain('min-h-11')
  })

  it('「执行机与配对」合一入口：副题报已连接台数（只数探活 ok 的），树未到时数字缺席', () => {
    treeMock.loaded = false
    const notLoaded = renderHub()
    expect(screen.getByTestId('settings-sub-machines')).toHaveTextContent('执行机与配对')
    // 树未到：还没问过 ≠ 问过且 0 台，不编数字
    expect(screen.getByTestId('settings-sub-machines-desc').textContent).toBe('管理位置与扫码')
    notLoaded.unmount()

    treeMock.loaded = true
    const loaded = renderHub()
    // 树已到且 machines 空：如实报 0 台
    expect(screen.getByTestId('settings-sub-machines-desc').textContent).toBe('已连接 0 台 · 管理位置与扫码')
    loaded.unmount()

    treeMock.machines = [
      { name: '', ok: true, fetched_at: '', error: '' },
      { name: 'devbox', ok: true, fetched_at: '', error: '' },
      { name: 'offline', ok: false, fetched_at: '', error: 'connection refused' },
    ]
    renderHub()
    expect(screen.getByTestId('settings-sub-machines-desc').textContent).toBe('已连接 2 台 · 管理位置与扫码')
  })

  it('入口行调 onSubChange(key)；update 行带 updateAvailable 红点（数据源同桌面计算）', () => {
    const onSubChange = vi.fn()
    renderHub(null, onSubChange)
    fireEvent.click(screen.getByTestId('settings-sub-machines'))
    expect(onSubChange).toHaveBeenCalledWith('machines')
    // updateAvailable 由桌面同款 hasNewer 计算注入；未注入 desktopState 时无红点
    expect(screen.queryByTestId('update-available-dot')).toBeNull()
  })

  it('sub=machines → 二级页全宽（MachinesPage 在场）+ 返回行调 onSubChange(null)', async () => {
    const onSubChange = vi.fn()
    renderHub('machines', onSubChange)
    expect(screen.getByTestId('settings-sub-back')).toBeInTheDocument()
    // 机器卡片列表在场（机器名 + 详情标题都渲染）
    expect(await screen.findAllByText('本机').then((rows) => rows.length)).toBeGreaterThan(0)
    fireEvent.click(screen.getByTestId('settings-sub-back'))
    expect(onSubChange).toHaveBeenCalledWith(null)
  })

  it('sub=pairing → 静态配对说明（无相机入口）；sub 非法值落设置中心', () => {
    const { unmount } = renderHub('pairing')
    expect(screen.getByTestId('pairing-guide')).toBeInTheDocument()
    expect(screen.getByText(/handoff console --qr/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '扫码' })).toBeNull()
    unmount()
    // 白名单外 sub 自愈成中心首屏（不白屏、不误渲染二级页）
    renderHub('bogus')
    expect(screen.queryByTestId('settings-sub-back')).toBeNull()
    expect(screen.getByTestId('pref-session-open-mode')).toBeInTheDocument()
    // B369.10 T10：hub 里没有 pairing 行（合一入口承接），唯一的入口是这条 URL 深链
    expect(screen.queryByTestId('settings-sub-pairing')).toBeNull()
  })

  it('偏好控件读写 useWebPrefs：切「任务现场」、关角标（落盘 + 同步订阅方）', async () => {
    renderHub()
    fireEvent.click(screen.getByLabelText('任务现场'))
    await waitFor(() => {
      expect(JSON.parse(localStorage.getItem('handoff.web.prefs')!).sessionOpenMode).toBe('scene')
    })
    fireEvent.click(screen.getByLabelText(/底栏显示/))
    await waitFor(() => {
      expect(JSON.parse(localStorage.getItem('handoff.web.prefs')!).badges).toBe(false)
    })
  })
})
