/**
 * Handoff 控制面纵切 E2E：fake agentd wire → Electron → ProjectTree DOM。
 *
 * 场景：
 *   1. fake server 启动后桌面加载项目树
 *   2. server 推送 workspace.upsert / task.upsert 后 DOM 无刷新更新
 *   3. 断开后保留行但显示不可用
 *   4. 重连后按 after cursor 重放 durable buffer，修复断线期间漏掉的差量
 *   5. 项目创建提交相同 operation ID 只执行一次（calls.createProject 计数断言）
 *
 * 边界：
 *   - 必须走真实 wire（fake agentd 实现 HTTP/WS），不直接调用 renderer store
 *   - 用 orca-app 的 electronApp fixture 启动，但自行 firstWindow（HandoffApp 是
 *     默认 renderer root，不依赖 Orca 全局 store 的 sharedPage fixture）
 *   - 断言用户可见 DOM（ProjectTree 文本），非 store 内部值
 *   - 每用例独立 fake（干净 buffer/计数），避免跨用例重放污染
 */
import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { test, expect } from './helpers/orca-app'
import { startFakeAgentd } from '../fixtures/fake-handoff-agentd'

// 配置路径在收集期确定（test.use 需要静态值），内容在 beforeEach 写入。
const configDir = mkdtempSync(path.join(os.tmpdir(), 'handoff-e2e-home-'))
const configPath = path.join(configDir, '.handoff', 'config.yaml')

const bootstrapFixture = {
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

test.describe('handoff catalog vertical slice', () => {
  test.use({ orcaAppExtraEnv: { HANDOFF_AGENTD_CONFIG: configPath } })

  let fake: Awaited<ReturnType<typeof startFakeAgentd>> | null = null

  test.beforeEach(async () => {
    fake = await startFakeAgentd({ token: 'test-token', bootstrap: bootstrapFixture })
    // 写指向 fake agentd 的 config.yaml（Main 的 Handoff IPC 据此连接）
    mkdirSync(path.dirname(configPath), { recursive: true })
    writeFileSync(
      configPath,
      `listen: "${new URL(fake.url).host}"\ntoken: "test-token"\n`,
      'utf8'
    )
  })

  test.afterEach(async () => {
    await fake?.close()
    fake = null
  })

  /** 等待控制流 WS 订阅就绪（首次连接计数从 0 起步）。 */
  async function waitForControlStream(): Promise<void> {
    await expect
      .poll(() => fake!.wsCount(), { timeout: 20_000 })
      .toBeGreaterThan(0)
  }

  test('loads project tree and updates on pushed control events', async ({ electronApp }) => {
    const page = await electronApp.firstWindow()
    await page.waitForLoadState('domcontentloaded')

    // 项目树出现项目名与机器名（DOM 断言）
    await expect(page.getByText('handoff', { exact: true })).toBeVisible()
    await expect(page.getByText('本机', { exact: true })).toBeVisible()

    await waitForControlStream()

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

  test('reconnects and replays durable buffer to repair gap', async ({ electronApp }) => {
    const page = await electronApp.firstWindow()
    await page.waitForLoadState('domcontentloaded')

    await expect(page.getByText('handoff', { exact: true })).toBeVisible()
    await waitForControlStream()

    // 断开前先推送一条（确保 cursor 已推进到 rev 2）
    fake!.push({
      revision: 2,
      kind: 'workspace.upsert',
      resource_id: 'ws-before',
      payload: {
        id: 'ws-before',
        machine_id: 'm-local',
        location_id: 'loc1',
        kind: 'worktree',
        path: '/Users/me/handoff/.handoff/worktrees/before',
        canonical_path: '/Users/me/handoff/.handoff/worktrees/before',
        branch: 'handoff/before',
        availability: 'available',
        last_scanned_at: '2026-08-09T00:00:02Z'
      },
      created_at: '2026-08-09T00:00:02Z'
    })
    await expect(page.getByText('handoff/before')).toBeVisible()

    // 断开：WS 关闭且 HTTP/WS 均拒绝，进入不可用
    fake!.disconnectAll()
    await expect(page.getByText(/不可用/).first()).toBeVisible()

    // 断线期间推送 rev 3：进入 durable buffer，在线订阅者为零不实时送达
    fake!.push({
      revision: 3,
      kind: 'workspace.upsert',
      resource_id: 'ws-after',
      payload: {
        id: 'ws-after',
        machine_id: 'm-local',
        location_id: 'loc1',
        kind: 'worktree',
        path: '/Users/me/handoff/.handoff/worktrees/after',
        canonical_path: '/Users/me/handoff/.handoff/worktrees/after',
        branch: 'handoff/after',
        availability: 'available',
        last_scanned_at: '2026-08-09T00:00:03Z'
      },
      created_at: '2026-08-09T00:00:03Z'
    })

    // 恢复 up：客户端退避重连，WS 握手携带 after=cursor(2)，fake 重放 rev>2
    fake!.setDown(false)
    await expect.poll(() => fake!.wsCount(), { timeout: 20_000 }).toBeGreaterThanOrEqual(2)
    // 断线期间漏掉的差量被重放修复
    await expect(page.getByText('handoff/after')).toBeVisible()
  })

  test('project create with same operation id executes once', async ({ electronApp }) => {
    const page = await electronApp.firstWindow()
    await page.waitForLoadState('domcontentloaded')

    // 直接经 window.handoff.createProject 提交相同 operation_id 两次
    // （与 ProjectCreateDialog 提交路径一致：Main → agentd /v1/projects/operations）。
    const command = {
      operation_id: 'op-idempotent-1',
      name: 'super-debug',
      locations: [
        {
          machine_id: 'm-local',
          role: 'local',
          source: 'existing_path',
          path: '/Users/me/repo'
        }
      ]
    }
    await page.evaluate((cmd) => window.handoff.createProject(cmd), command)
    await page.evaluate((cmd) => window.handoff.createProject(cmd), command)

    // 相同 operation_id 只执行一次：fake 幂等返回已有 Operation，计数不增。
    expect(fake!.calls.createProject()).toBe(1)
    expect(fake!.createdProjects()).toHaveLength(1)
  })
})
