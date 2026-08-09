/**
 * Handoff 架构守卫测试：扫描 features/handoff imports，拒绝 Orca SSH/旧 store。
 *
 * 拒绝片段：/ssh, ssh-, Ssh, ProjectHostSetup, store/slices/repos,
 * store/slices/worktrees, launch-agent, native-chat
 *
 * 边界：
 *   - 只读扫描源码，不执行
 */
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const HANDOFF_DIR = join(process.cwd(), 'src/renderer/src/features/handoff')
const FORBIDDEN = ['/ssh', 'ssh-', 'Ssh', 'ProjectHostSetup', 'store/slices/repos', 'store/slices/worktrees', 'launch-agent', 'native-chat']

function collectTsFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      collectTsFiles(full, out)
    } else if (entry.endsWith('.ts') || entry.endsWith('.tsx')) {
      out.push(full)
    }
  }
  return out
}

describe('handoff architecture boundary', () => {
  it('does not import Orca SSH / old project/worktree persistence / native chat', () => {
    const files = collectTsFiles(HANDOFF_DIR)
    expect(files.length).toBeGreaterThan(0)
    const offenders: string[] = []
    for (const file of files) {
      const content = readFileSync(file, 'utf8')
      for (const line of content.split('\n')) {
        const trimmed = line.trim()
        if (!trimmed.startsWith('import ')) {
          continue
        }
        for (const frag of FORBIDDEN) {
          if (trimmed.includes(frag)) {
            offenders.push(`${file}: ${trimmed}`)
          }
        }
      }
    }
    expect(offenders).toEqual([])
  })
})
