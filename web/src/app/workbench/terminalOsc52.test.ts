import { describe, expect, it } from 'vitest'
import { parseOsc52 } from './terminalOsc52'

// 表驱动：每行一条缝级断言（缝 1：OSC 52 载荷解码，spec「测试决定」）。
const cases: Array<{ name: string; data: string; want: { selection: string; text: string } | null }> = [
  { name: '常规 clipboard 写', data: 'c;aGVsbG8=', want: { selection: 'c', text: 'hello' } },
  { name: 'primary selection 写', data: 's;5Lit5paH', want: { selection: 's', text: '中文' } },
  { name: '缺 padding', data: 'c;aGVsbG8', want: { selection: 'c', text: 'hello' } },
  { name: 'base64 被换行包裹', data: 'c;5Lit\n5paH', want: { selection: 'c', text: '中文' } },
  { name: '空 selection', data: ';aGVsbG8=', want: { selection: '', text: 'hello' } },
  { name: '读查询不写', data: 'c;?', want: null },
  { name: '空载荷（清剪贴板）不写', data: 'c;', want: null },
  { name: '无分号不写', data: 'c', want: null },
  { name: '坏 base64 不写', data: 'c;@@@@', want: null },
  { name: '坏 UTF-8 不写', data: 'c;/w==', want: null },
]

describe('parseOsc52', () => {
  for (const c of cases) {
    it(c.name, () => {
      expect(parseOsc52(c.data)).toEqual(c.want)
    })
  }

  // roundtrip 属性测试：TextEncoder→btoa 编码 ∘ parseOsc52 解码对随机文本恒等，
  // 一条属性覆盖多字节、代理对、控制字符一整族序列化边界（缺陷族·序列化边界）。
  it('roundtrip：任意文本经 base64 编码后解码恒等', () => {
    const alphabet = 'aZ0 九;q✓\x1b]52;. '
    for (let i = 0; i < 200; i++) {
      let s = ''
      const n = 1 + Math.floor(Math.random() * 80)
      for (let j = 0; j < n; j++) s += alphabet[Math.floor(Math.random() * alphabet.length)]
      const bytes = new TextEncoder().encode(s)
      let bin = ''
      for (const b of bytes) bin += String.fromCharCode(b)
      expect(parseOsc52(`c;${btoa(bin)}`)).toEqual({ selection: 'c', text: s })
    }
  })
})
