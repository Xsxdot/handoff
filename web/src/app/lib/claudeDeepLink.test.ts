// claudeDeepLink.test.ts —— B434 深链字面量金样本（契约 §4 条 24/25）。
// 对侧执法点：internal/proto/desktop_coordinator.go#ClaudeNewQMaxChars /
// #ClaudeNewSessionURLPrefix（Go 侧 TestB434DesktopWireLiterals 锁同值）。
// 编码行为的表驱动用例（条 15–23）归实现节点。
import { describe, expect, it } from 'vitest'
import { CLAUDE_NEW_SESSION_URL_PREFIX, CLAUDE_Q_MAX_CHARS } from './claudeDeepLink'

describe('claudeDeepLink 字面量', () => {
  it('q 上限与 Go 侧 ClaudeNewQMaxChars 同值', () => {
    expect(CLAUDE_Q_MAX_CHARS).toBe(14336)
  })

  it('new 深链前缀与 Go 侧 ClaudeNewSessionURLPrefix 同值', () => {
    expect(CLAUDE_NEW_SESSION_URL_PREFIX).toBe('claude://code/new?')
  })
})
