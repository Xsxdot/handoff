#!/usr/bin/env node
/**
 * 只运行 handoff catalog E2E 纵切（单 worker）。
 *
 * 职责：
 *   - 构建 Electron（--mode e2e，暴露 window.__store）
 *   - 用固定单 worker 跑 handoff-catalog.spec.ts
 *   - 结束后清理 fake server 与 Electron（Playwright afterAll/globalTeardown 承担）
 *
 * 用法：pnpm run test:e2e:handoff-catalog
 */
import { spawnSync } from 'node:child_process'

function run(command, args, opts = {}) {
  const res = spawnSync(command, args, { stdio: 'inherit', ...opts })
  if (res.status !== 0) {
    process.exit(res.status ?? 1)
  }
}

// 1. 构建 Electron app（e2e 模式暴露 window.__store）
run('pnpm', ['exec', 'electron-vite', 'build', '--mode', 'e2e'])

// 2. 跑单个纵切，固定单 worker
run('npx', ['playwright', 'test', '--config', 'tests/playwright.config.ts', '--project=electron-headless', '--workers=1', 'handoff-catalog.spec.ts'])

console.log('handoff-catalog E2E 通过')
