/**
 * Handoff agentd-config 测试：从 ~/.handoff/config.yaml 读取本机 Listen/Token。
 *
 * 职责：
 *   - 只读取本机配置的 Listen 与 Token
 *   - 不得解析/暴露 targets.*.token 给 renderer
 *
 * 边界：
 *   - 使用注入路径，不直接污染真实 ~/.handoff
 *   - 纯文件解析，不发起网络
 */
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { readAgentdConfig } from './agentd-config'

describe('readAgentdConfig', () => {
  it('reads listen and token from yaml', () => {
    const dir = mkdtempSync(join(tmpdir(), 'handoff-cfg-'))
    const path = join(dir, 'config.yaml')
    writeFileSync(
      path,
      'listen: "127.0.0.1:7777"\ntoken: "abc123"\ntargets:\n  devbox:\n    addr: "10.0.0.5:7777"\n    token: "remote-secret"\n'
    )
    const cfg = readAgentdConfig(path)
    expect(cfg.listen).toBe('127.0.0.1:7777')
    expect(cfg.token).toBe('abc123')
  })

  it('returns unavailable when file missing', () => {
    const dir = mkdtempSync(join(tmpdir(), 'handoff-cfg-'))
    const cfg = readAgentdConfig(join(dir, 'missing.yaml'))
    expect(cfg.unavailable).toBe(true)
    expect(cfg.listen).toBe('')
    expect(cfg.token).toBe('')
  })

  it('does not expose targets tokens', () => {
    const dir = mkdtempSync(join(tmpdir(), 'handoff-cfg-'))
    const path = join(dir, 'config.yaml')
    writeFileSync(path, 'listen: "127.0.0.1:7777"\ntoken: "local-token"\n')
    const cfg = readAgentdConfig(path)
    // 返回值只有 listen/token/unavailable，不含任何 targets 字段
    expect(Object.keys(cfg).sort()).toEqual(['listen', 'token', 'unavailable'])
    expect(cfg.token).toBe('local-token')
  })
})
