import { describe, expect, it } from 'vitest'
import { nextUntitledName } from './newFile'

describe('nextUntitledName', () => {
  it('空目录得到 untitled-1.md', () => {
    expect(nextUntitledName([])).toBe('untitled-1.md')
  })

  it('跳过已占用的编号', () => {
    expect(nextUntitledName(['untitled-1.md', 'untitled-2.md'])).toBe('untitled-3.md')
  })

  it('中间空出来的编号会被捡回来——连着建删几次不该一直往上爬', () => {
    expect(nextUntitledName(['untitled-1.md', 'untitled-3.md'])).toBe('untitled-2.md')
  })

  it('不相干的文件不影响编号', () => {
    expect(nextUntitledName(['README.md', 'untitled.md', 'untitled-a.md'])).toBe('untitled-1.md')
  })
})
