/**
 * Handoff 控制面纵切 E2E：fake agentd wire → Electron → ProjectTree DOM。
 *
 * 场景：
 *   1. fake server 启动后桌面加载项目树
 *   2. server 推送 workspace.upsert / task.upsert 后 DOM 无刷新更新
 *   3. 断开后保留行但显示不可用
 *
 * 边界：
 *   - 必须走真实 wire（fake agentd 实现 HTTP/WS），不直接调用 renderer store
 *   - 用 orca-app 的 electronApp fixture 启动，但自行 firstWindow（HandoffApp 是
 *     默认 renderer root，不依赖 Orca 全局 store 的 sharedPage fixture）
 *   - 断言用户可见 DOM（ProjectTree 文本），非 store 内部值
 */
import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { test, expect } from './helpers/orca-app'
import { startFakeAgentd } from '../fixtures/fake-handoff-agentd'

// 配置路径在收集期确定（test.use 需要静态值），内容在 beforeAll 写入。
const configDir = mkdtempSync(path.join(os.tmpdir(), 'handoff-e2e-home-'))
const configPath = path.join(configDir, '.handoff', 'config.yaml')

test.describe('handoff catalog vertical slice', () => {
  test.use({ orcaAppExtraEnv: { HANDOFF_AGENTD_CONFIG: configPath } })

  let fake: Awaited<ReturnType<typeof startFakeAgentd>> | null = null

  test.beforeAll(async () => {
    fake = await startFakeAgentd({
      token: 'test-token',
      bootstrap: {
        machines: [
          { id: 'm-local', display_name: '本机', kind: 'local', endpoint: 'http://127.0.0.1:7777', protocol_version: 1, capabilities: { catalog: 1 }, status: 'connected', last_seen_at: null },
          { id: 'm-remote', display_name: '开发机', kind: 'remote', endpoint: 'http://10.0.0.5:7777', protocol_version: 1, capabilities: { catalog: 1 }, status: 'connected', last_seen_at: null }
        ],
        projects: [
          { id: 'p1', name: 'handoff', created_at: '2026-08-09T00:00:00Z', updated_at: '2026-08-09T00:00:00Z' }
        ],
        locations: [
          { id: 'loc1', project_id: 'p1', machine_id: 'm-local', role: 'local', main_workspace_id: 'ws-main', source: 'existing_path', created_at: '2026-08-09T00:00:00Z', updated_at: '2026-08-09T00:00:00Z' }
        ],
        workspaces: [
          { id: 'ws-main', machine_id: 'm-local', location_id: 'loc1', kind: 'main', path: '/Users/me/handoff', canonical_path: '/Users/me/handoff', availability: 'available', last_scanned_at: '2026-08-09T00:00:00Z' }
        ],
        git_refs: [],
        active_task_summaries: [],
        operations: [],
        control_revision: 1
      }
    })
    // 写指向 fake agentd 的 config.yaml（Main 的 Handoff IPC 据此连接）
    mkdirSync(path.dirname(configPath), { recursive: true })
    writeFileSync(
      configPath,
      `listen: "${new URL(fake.url).host}"\ntoken: "test-token"\n`,
      'utf8'
    )
  })

  test.afterAll(async () => {
    await fake?.close()
    fake = null
  })

  test('loads project tree and updates on pushed control events', async ({ electronApp }) => {
    const page = await electronApp.firstWindow()
    await page.waitForLoadState('domcontentloaded')

    // 项目树出现项目名与机器名（DOM 断言）
    await expect(page.getByText('handoff', { exact: true })).toBeVisible()
    await expect(page.getByText('本机', { exact: true })).toBeVisible()

    // 等待控制流 WS 订阅就绪后再推送，避免推送早于订阅被丢弃
    await expect
      .poll(() => fake!.wsCount(), { timeout: 20_000 })
      .toBeGreaterThan(0)

    // server 推送 workspace.upsert → 新 worktree 出现（DOM 无刷新更新）
    fake!.push({
      revision: 2,
      kind: 'workspace.upsert',
      resource_id: 'ws-wt',
      payload: {
        id: 'ws-wt',
        machine_id: 'm-local',
        location_id: 'loc1',
        kind: 'worktree',
        path: '/Users/me/handoff/.handoff/worktrees/t1',
        canonical_path: '/Users/me/handoff/.handoff/worktrees/t1',
        branch: 'handoff/t1',
        availability: 'available',
        last_scanned_at: '2026-08-09T00:00:01Z'
      },
      created_at: '2026-08-09T00:00:01Z'
    })
    await expect(page.getByText('handoff/t1')).toBeVisible()

    // server 推送 task.upsert → 任务出现在其 Workspace 下
    fake!.push({
      revision: 3,
      kind: 'task_summary.upsert',
      resource_id: 't1',
      payload: {
        task_id: 't1',
        machine_id: 'm-local',
        workspace_id: 'ws-wt',
        name: '修复登录态',
        executor: 'opencode',
        state: 'running',
        attention: 1,
        updated_at: '2026-08-09T00:00:01Z'
      },
      created_at: '2026-08-09T00:00:01Z'
    })
    await expect(page.getByText('修复登录态')).toBeVisible()
  })

  test('keeps rows but marks machine unavailable on disconnect', async ({ electronApp }) => {
    const page = await electronApp.firstWindow()
    await page.waitForLoadState('domcontentloaded')

    await expect(page.getByText('handoff', { exact: true })).toBeVisible()
    // 断开后：不可用标记出现（DOM 无刷新，保留行）
    fake!.disconnectAll()
    await expect(page.getByText(/不可用/).first()).toBeVisible()
    // last-known 子树保留
    await expect(page.getByText('handoff', { exact: true })).toBeVisible()
  })
})
