import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../api/client'
import * as client from '../../api/client'
import type { CreateProjectResp } from '../../api/types'
import { registerAll } from './register'

// mockOk 是登记成功响应的最小 mock 形态。CreateProjectResp 是完整 ProjectLocation
// （origin_url/created_at 必填），mock 只给断言关心的字段，故显式收窄。
function mockOk(): CreateProjectResp {
  return { project_id: 'p', name: 'a', path: '/a' } as CreateProjectResp
}

describe('registerAll', () => {
  it('按选中位置数发起对应次数的 POST，每次带正确的 machine', async () => {
    const spy = vi.spyOn(client, 'createProject').mockResolvedValue(mockOk())
    await registerAll([
      { machine: '', gitUrl: 'git@x:/a.git', path: '/Users/me/a' },
      { machine: 'devbox', gitUrl: 'git@x:/a.git', path: '' },
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
      { machine: '', gitUrl: 'g', path: '/a' },
      { machine: 'devbox', gitUrl: 'g', path: '' },
    ])
    expect(out).toHaveLength(2)
    expect(out[0]).toMatchObject({ machine: '', ok: true })
    expect(out[1]).toMatchObject({ machine: 'devbox', ok: false })
    // agentd 的报错原文必须透传——里面带着解法
    expect(out[1].error).toContain('Permission denied')
  })

  it('全部失败也返回逐条结果而不是抛异常', async () => {
    vi.spyOn(client, 'createProject').mockRejectedValue(new ApiError(400, 'origin_url 不能为空'))
    const out = await registerAll([{ machine: '', gitUrl: '', path: '' }])
    expect(out[0].ok).toBe(false)
  })
})
