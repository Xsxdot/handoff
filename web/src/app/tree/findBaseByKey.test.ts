// findBaseByKey.test.ts —— 按稳定 key 反查项目树目录的纯函数测试。
//
// 职责：验证选中目录恢复时的 key 与树节点生成器保持一致。
// 边界：不测试 ProjectTree 的渲染，也不触发网络或 React 状态。
import { describe, expect, it } from 'vitest'
import type { ProjectTreeResp } from '../../api/types'
import { findBaseByKey, workspaceBase } from './ProjectTree'

const tree = {
  projects: [
    {
      project_id: 'p1',
      name: 'handoff',
      locations: [
        { machine: '', workspaces: [{ path: '/repo/a', branch: 'main', is_main: true }] },
        { machine: 'linux-01', workspaces: [{ path: '/repo/a', branch: 'feature/x', is_main: false }] },
      ],
    },
  ],
} as unknown as ProjectTreeResp

describe('findBaseByKey', () => {
  it('按 key 反查得到与 workspaceBase 完全一致的基准', () => {
    const p = tree.projects[0]
    const local = workspaceBase(p as never, '', p.locations[0].workspaces[0] as never)
    expect(findBaseByKey(tree, local.key)).toEqual(local)
  })

  it('同路径不同机器不会认错', () => {
    const p = tree.projects[0]
    const remote = workspaceBase(p as never, 'linux-01', p.locations[1].workspaces[0] as never)
    const got = findBaseByKey(tree, remote.key)
    expect(got).toEqual(remote)
    expect(got!.machine).toBe('linux-01')
  })

  it('目录已经不在树上时返回 null', () => {
    expect(findBaseByKey(tree, '/repo/gone')).toBeNull()
  })
})
