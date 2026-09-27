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
vi.mock('../data/useProjectTree', () => ({
  useProjectTree: () => ({
    data: { projects: [], machines: [], unowned: [] },
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
  })

  function renderHub(sub: string | null = null, onSubChange = vi.fn()) {
    return render(<SettingsPage onClose={vi.fn()} compact sub={sub} onSubChange={onSubChange} />)
  }

  it('四分区就地呈现：会话打开方式 / 提醒 / 显示与可访问性 / 关于 + 六入口行', () => {
    renderHub()
    expect(screen.getByTestId('pref-session-open-mode')).toBeInTheDocument()
    expect(screen.getByTestId('pref-badges')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '显示与可访问性' })).toBeInTheDocument()
    expect(screen.getByTestId('settings-about')).toBeInTheDocument()
    for (const key of ['machines', 'pairing', 'discipline', 'automation', 'env', 'update']) {
      expect(screen.getByTestId(`settings-sub-${key}`)).toBeInTheDocument()
    }
    // 二级入口行是主动作触控档（plan §3.3 min-h-11）
    expect(screen.getByTestId('settings-sub-machines').className).toContain('min-h-11')
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
