// codeText 的切分边界：闭合围栏、未闭合围栏（流式中途）、行内 code。
//
// 关键不变式：**未闭合时按纯文本降级**。增量是逐字到达的，若围栏未闭合就先按
// 代码块渲染，闭合瞬间整块会重排——那正是本方案不引 markdown 渲染器的理由之一，
// 自己实现时更不能把这个坑重新挖出来。
import { describe, expect, it } from 'vitest'
import { splitFences, splitInline } from './codeText'

describe('splitFences', () => {
  it('无围栏：整段一个纯文本段', () => {
    expect(splitFences('普通文字')).toEqual([{ code: false, text: '普通文字' }])
  })

  it('闭合围栏：切成 文本 / 代码 / 文本 三段，去掉语言行', () => {
    expect(splitFences('前\n```go\nfmt.Println()\n```\n后')).toEqual([
      { code: false, text: '前\n' },
      { code: true, text: 'fmt.Println()\n' },
      { code: false, text: '\n后' },
    ])
  })

  it('无语言标注的围栏也能切', () => {
    expect(splitFences('```\nls -la\n```')).toEqual([
      { code: false, text: '' },
      { code: true, text: 'ls -la\n' },
      { code: false, text: '' },
    ])
  })

  it('未闭合围栏：整段按纯文本降级（流式中途不抖）', () => {
    const partial = '我来写：\n```go\nfunc main() {'
    expect(splitFences(partial)).toEqual([{ code: false, text: partial }])
  })

  it('两个闭合围栏都识别', () => {
    const segs = splitFences('a\n```\nx\n```\nb\n```\ny\n```\nc')
    expect(segs.filter((s) => s.code).map((s) => s.text)).toEqual(['x\n', 'y\n'])
  })

  it('空串返回单个空文本段，不抛异常', () => {
    expect(splitFences('')).toEqual([{ code: false, text: '' }])
  })
})

describe('splitInline', () => {
  it('行内 code 切出来', () => {
    expect(splitInline('用 `go test` 跑')).toEqual([
      { code: false, text: '用 ' },
      { code: true, text: 'go test' },
      { code: false, text: ' 跑' },
    ])
  })

  it('未闭合的反引号按纯文本', () => {
    expect(splitInline('未闭合 `go test')).toEqual([{ code: false, text: '未闭合 `go test' }])
  })

  it('反引号内不跨行（跨行的那是围栏的事）', () => {
    expect(splitInline('a `b\nc` d')).toEqual([{ code: false, text: 'a `b\nc` d' }])
  })
})
