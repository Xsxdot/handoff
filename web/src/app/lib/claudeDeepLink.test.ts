// claudeDeepLink.test.ts —— B434 深链字面量金样本（契约条 24/25）与编码行为（契约条 15–23）。
// 对侧执法点：internal/proto/desktop_coordinator.go#ClaudeNewQMaxChars /
// #ClaudeNewSessionURLPrefix（Go 侧 TestB434DesktopWireLiterals 锁同值）。
// 还原一律走 new URL(url).searchParams（与桌面端同为 URL 标准解码），不比对手写的整串期望值
// ——手写期望串会和实现一起写错（缺陷族·假绿）。
import { describe, expect, it } from 'vitest'
import { buildClaudeNewSessionURL, CLAUDE_NEW_SESSION_URL_PREFIX, CLAUDE_Q_MAX_CHARS } from './claudeDeepLink'
import type { ClaudeDeepLinkResult } from './claudeDeepLink'

const FOLDER = '/Users/me/workspace/handoff'

// okURL 断言编码成功并取出 url；拒绝时把原因带进报错，红灯一眼可读。
function okURL(result: ClaudeDeepLinkResult): string {
  if (!result.ok) throw new Error(`期望编码成功，实得拒绝：${result.reason}`)
  return result.url
}

// mulberry32 是确定性 32 位 PRNG：属性测试必须可复现，红灯时按报错里的 seed 重放。
function mulberry32(seed: number): () => number {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

// POOL 是随机串字符池：URL 保留字、空白、引号、反斜杠、中文、不换行空格、代理对（😀）与 U+FFFD。
// 不含孤立代理项——服务端 JSON 产不出它（Go encoding/json 把非法 UTF-8 写成 �）。
const POOL = ['a', 'Z', '0', ' ', '\n', '\r', '\t', '"', "'", '&', '#', '+', '%', '=', '?', '/', '\\', ':', '[', ']', '中', '文', '·', ' ', '😀', '�']

describe('claudeDeepLink 字面量', () => {
  it('q 上限与 Go 侧 ClaudeNewQMaxChars 同值', () => {
    expect(CLAUDE_Q_MAX_CHARS).toBe(14336)
  })

  it('new 深链前缀与 Go 侧 ClaudeNewSessionURLPrefix 同值', () => {
    expect(CLAUDE_NEW_SESSION_URL_PREFIX).toBe('claude://code/new?')
  })
})

describe('buildClaudeNewSessionURL', () => {
  it('条15 成功值以 claude://code/new?folder= 开头', () => {
    expect(okURL(buildClaudeNewSessionURL(FOLDER, '[handoff] 你好')).startsWith('claude://code/new?folder=')).toBe(true)
  })

  it('条16 folder 以 encodeURIComponent 编码并逐字还原', () => {
    const folder = '/Users/张 三/a&b#c+d'
    const url = okURL(buildClaudeNewSessionURL(folder, 'x'))
    expect(url.startsWith(`${CLAUDE_NEW_SESSION_URL_PREFIX}folder=${encodeURIComponent(folder)}&q=`)).toBe(true)
    expect(new URL(url).searchParams.get('folder')).toBe(folder)
  })

  it('条17 brief 以 encodeURIComponent 编码', () => {
    const brief = '[handoff] 卡 B1\n第二行 & #'
    const url = okURL(buildClaudeNewSessionURL(FOLDER, brief))
    expect(url.endsWith(`&q=${encodeURIComponent(brief)}`)).toBe(true)
  })

  it('条18 成功值不含 +，空格编为 %20', () => {
    const url = okURL(buildClaudeNewSessionURL('/a b', 'x y+z'))
    expect(url).not.toContain('+')
    expect(url).toContain('%20')
    expect(new URL(url).searchParams.get('q')).toBe('x y+z')
  })

  it.each([
    ['中文', '你是卡 B434 的协调者'],
    ['换行', '第一行\n第二行\r\n第三行'],
    ['双引号', 'say "hi"'],
    ['&', 'a&q=evil&b'],
    ['#', 'a#fragment'],
  ])('条19 %s 经 searchParams 逐字还原', (_label, brief) => {
    const url = okURL(buildClaudeNewSessionURL(FOLDER, brief))
    expect(new URL(url).searchParams.get('q')).toBe(brief)
  })

  it('条20 超上限一字符即 brief_too_long，不截断', () => {
    expect(buildClaudeNewSessionURL(FOLDER, 'x'.repeat(CLAUDE_Q_MAX_CHARS + 1))).toEqual({ ok: false, reason: 'brief_too_long' })
  })

  it('条21 恰等于上限成功且 q 原样还原', () => {
    const brief = 'x'.repeat(CLAUDE_Q_MAX_CHARS)
    const url = okURL(buildClaudeNewSessionURL(FOLDER, brief))
    expect(new URL(url).searchParams.get('q')).toBe(brief)
  })

  it('条20/21 口径：上限按 JS length（UTF-16 码元）计', () => {
    expect(buildClaudeNewSessionURL(FOLDER, '中'.repeat(CLAUDE_Q_MAX_CHARS)).ok).toBe(true)
    expect(buildClaudeNewSessionURL(FOLDER, '😀'.repeat(CLAUDE_Q_MAX_CHARS / 2 + 1))).toEqual({ ok: false, reason: 'brief_too_long' })
  })

  it.each(['Users/me/handoff', '', '~/workspace/handoff', './handoff'])('条22 folder %j 非绝对路径即 folder_not_absolute', (folder) => {
    expect(buildClaudeNewSessionURL(folder, 'x')).toEqual({ ok: false, reason: 'folder_not_absolute' })
  })

  it('条23 brief 为空串即 brief_empty', () => {
    expect(buildClaudeNewSessionURL(FOLDER, '')).toEqual({ ok: false, reason: 'brief_empty' })
  })

  it('属性：随机目录与简报 encode∘decode 恒等且不含 +', () => {
    for (let seed = 1; seed <= 200; seed++) {
      const rand = mulberry32(seed)
      const pick = (n: number) => Array.from({ length: n }, () => POOL[Math.floor(rand() * POOL.length)]).join('')
      const folder = `/${pick(1 + Math.floor(rand() * 40))}`
      const brief = pick(1 + Math.floor(rand() * 400))
      const url = okURL(buildClaudeNewSessionURL(folder, brief))
      const params = new URL(url).searchParams
      expect(params.get('folder'), `seed=${seed}`).toBe(folder)
      expect(params.get('q'), `seed=${seed}`).toBe(brief)
      expect(url.includes('+'), `seed=${seed}`).toBe(false)
    }
  })
})
