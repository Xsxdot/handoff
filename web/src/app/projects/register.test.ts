import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../api/client'
import * as client from '../../api/client'
import type { CreateProjectResp } from '../../api/types'
import { absPathHint, registerAll, registerFromForm } from './register'

// 每个用例都重建 createProject spy，调用次数断言不跨用例累计。
beforeEach(() => {
  vi.restoreAllMocks()
})

// mockOk 是登记成功响应的最小 mock 形态。CreateProjectResp 是完整 ProjectLocation
// （origin_url/created_at 必填），mock 只给断言关心的字段，故显式收窄。
function mockOk(): CreateProjectResp {
  return { project_id: 'p', name: 'a', path: '/a' } as CreateProjectResp
}

describe('registerAll', () => {
  it('按选中位置数发起对应次数的 POST，每次带正确的 machine', async () => {
    const spy = vi.spyOn(client, 'createProject').mockResolvedValue(mockOk())
    await registerAll([
      { machine: '', originUrl: 'git@x:/a.git', path: '/Users/me/a' },
      { machine: 'devbox', originUrl: 'git@x:/a.git', path: '' },
    ])
    expect(spy).toHaveBeenCalledTimes(2)
    expect(spy).toHaveBeenNthCalledWith(1, { origin_url: 'git@x:/a.git', path: '/Users/me/a' }, '')
    // 不填目录 = 不带 path，让该机 clone 到自己的 repo_root（B62 的两种形态）
    expect(spy).toHaveBeenNthCalledWith(2, { origin_url: 'git@x:/a.git' }, 'devbox')
  })

  it('一成一败时逐位置返回结果，不整体抛错', async () => {
    vi.spyOn(client, 'createProject')
      .mockResolvedValueOnce(mockOk())
      .mockRejectedValueOnce(new ApiError(500, 'clone 失败：Permission denied (publickey)'))
    const out = await registerAll([
      { machine: '', originUrl: 'g', path: '/a' },
      { machine: 'devbox', originUrl: 'g', path: '' },
    ])
    expect(out).toHaveLength(2)
    expect(out[0]).toMatchObject({ machine: '', ok: true })
    expect(out[1]).toMatchObject({ machine: 'devbox', ok: false })
    // agentd 的报错原文必须透传——里面带着解法
    expect(out[1].error).toContain('Permission denied')
  })

  it('全部失败也返回逐条结果而不是抛异常', async () => {
    vi.spyOn(client, 'createProject').mockRejectedValue(new ApiError(400, 'origin_url 不能为空'))
    const out = await registerAll([{ machine: '' }])
    expect(out[0].ok).toBe(false)
  })
})

describe('registerFromForm', () => {
  it('本机只填 path 时不传 origin_url（path 已有仓由 agentd 现读 origin）', async () => {
    const spy = vi.spyOn(client, 'createProject').mockResolvedValue({
      project_id: 'p',
      name: 'handoff',
      path: '/Users/me/handoff',
      origin_url: 'git@x:h.git',
      created_at: 't',
    })
    const out = await registerFromForm({
      name: '',
      localPath: '/Users/me/handoff',
      gitUrl: '',
      remoteMachine: null,
      remotePath: '',
    })
    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy).toHaveBeenCalledWith({ path: '/Users/me/handoff' }, '')
    expect(out).toHaveLength(1)
    expect(out[0]).toMatchObject({ machine: '', ok: true })
  })

  it('本机成功后再打远程，远程 origin/name 用本机响应里的权威值', async () => {
    const spy = vi.spyOn(client, 'createProject')
      .mockResolvedValueOnce({
        project_id: 'p',
        name: 'handoff',
        path: '/Users/me/h',
        origin_url: 'git@x:h.git',
        created_at: 't',
      })
      .mockResolvedValueOnce({
        project_id: 'p',
        name: 'handoff',
        path: '/root/h',
        origin_url: 'git@x:h.git',
        created_at: 't',
      })
    const out = await registerFromForm({
      name: '',
      localPath: '/Users/me/h',
      gitUrl: 'git@x:h.git',
      remoteMachine: 'devbox',
      remotePath: '',
    })
    expect(spy).toHaveBeenCalledTimes(2)
    // 远程不猜 origin：用本机登记响应里的 authoritative origin_url / name；
    // 远程 path 空则不传——由该机 clone 到自己的 repo_root/<name>
    expect(spy).toHaveBeenNthCalledWith(2, { origin_url: 'git@x:h.git', name: 'handoff' }, 'devbox')
    expect(out).toHaveLength(2)
    expect(out[1]).toMatchObject({ machine: 'devbox', ok: true })
  })

  it('远程填了 path 时远程请求带上该 path', async () => {
    const spy = vi.spyOn(client, 'createProject')
      .mockResolvedValueOnce({
        project_id: 'p',
        name: 'handoff',
        path: '/Users/me/h',
        origin_url: 'git@x:h.git',
        created_at: 't',
      })
      .mockResolvedValueOnce({
        project_id: 'p',
        name: 'handoff',
        path: '/srv/h',
        origin_url: 'git@x:h.git',
        created_at: 't',
      })
    await registerFromForm({
      name: '',
      localPath: '/Users/me/h',
      gitUrl: '',
      remoteMachine: 'devbox',
      remotePath: '/srv/h',
    })
    expect(spy).toHaveBeenNthCalledWith(
      2,
      { origin_url: 'git@x:h.git', name: 'handoff', path: '/srv/h' },
      'devbox',
    )
  })

  it('本机失败时不请求远程，但补一条 skipped 的远程行（让用户看到它没被漏掉）', async () => {
    const spy = vi.spyOn(client, 'createProject').mockRejectedValue(new ApiError(400, '路径不存在'))
    const out = await registerFromForm({
      name: '',
      localPath: '/nope',
      gitUrl: '',
      remoteMachine: 'devbox',
      remotePath: '',
    })
    expect(spy).toHaveBeenCalledTimes(1)
    expect(out).toHaveLength(2)
    expect(out[0]).toMatchObject({ machine: '', ok: false })
    expect(out[0].error).toContain('路径不存在')
    // skipped 与 ok:false 是两回事：一个是没发起，一个是发起了但失败。
    // 结果页要靠这个区分才能算出正确的文案。
    expect(out[1]).toMatchObject({ machine: 'devbox', ok: false, skipped: true })
  })

  it('没勾远程时本机失败只回一条，不无中生有一行 skipped', async () => {
    vi.spyOn(client, 'createProject').mockRejectedValue(new ApiError(400, '路径不存在'))
    const out = await registerFromForm({
      name: '',
      localPath: '/nope',
      gitUrl: '',
      remoteMachine: null,
      remotePath: '',
    })
    expect(out).toHaveLength(1)
  })

  it('本机请求带非空 name / gitUrl，空字段不进 body', async () => {
    const spy = vi.spyOn(client, 'createProject').mockResolvedValue({
      project_id: 'p',
      name: 'handoff',
      path: '/Users/me/h',
      origin_url: 'git@x:h.git',
      created_at: 't',
    })
    await registerFromForm({
      name: '  my-handoff ',
      localPath: '/Users/me/h',
      gitUrl: 'git@x:h.git',
      remoteMachine: null,
      remotePath: '',
    })
    expect(spy).toHaveBeenCalledWith(
      { name: 'my-handoff', origin_url: 'git@x:h.git', path: '/Users/me/h' },
      '',
    )
  })
})

describe('absPathHint', () => {
  it('绝对路径没有提示', () => {
    expect(absPathHint('/Users/me/handoff')).toBe('')
  })

  it('空串没有提示——「必填」由提交按钮管，不在这里重复说一遍', () => {
    expect(absPathHint('')).toBe('')
    expect(absPathHint('   ')).toBe('')
  })

  it('~ 开头给出明确提示（agentd 不展开 ~）', () => {
    expect(absPathHint('~/code/handoff')).toContain('~')
  })

  it('相对路径给出明确提示', () => {
    expect(absPathHint('code/handoff')).toContain('绝对路径')
  })
})
