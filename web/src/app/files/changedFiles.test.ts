import { describe, expect, it } from 'vitest'
import { parseChangedFiles } from './changedFiles'

// paths 取出解析结果里的路径，供「只关心覆盖面、不关心类别」的用例断言
function paths(diff: string): string[] {
  return [...parseChangedFiles(diff).keys()].sort()
}

describe('parseChangedFiles', () => {
  it('从 diff --git 行取出相对路径，默认类别是 modified', () => {
    const diff = [
      'diff --git a/internal/agentd/transport_test.go b/internal/agentd/transport_test.go',
      'index 1111111..2222222 100644',
      '--- a/internal/agentd/transport_test.go',
      '+++ b/internal/agentd/transport_test.go',
      '@@ -1 +1 @@',
      '-x',
      '+y',
      'diff --git a/Makefile b/Makefile',
      '--- a/Makefile',
      '+++ b/Makefile',
    ].join('\n')
    expect(paths(diff)).toEqual(['Makefile', 'internal/agentd/transport_test.go'])
    expect(parseChangedFiles(diff).get('Makefile')).toBe('modified')
  })

  it('新增文件记为 added', () => {
    const diff = ['diff --git a/new.txt b/new.txt', 'new file mode 100644', '--- /dev/null', '+++ b/new.txt'].join('\n')
    expect(paths(diff)).toEqual(['new.txt'])
    expect(parseChangedFiles(diff).get('new.txt')).toBe('added')
  })

  it('删除文件取 a/ 侧路径并记为 deleted', () => {
    const diff = ['diff --git a/gone.txt b/gone.txt', 'deleted file mode 100644', '--- a/gone.txt', '+++ /dev/null'].join('\n')
    expect(paths(diff)).toEqual(['gone.txt'])
    expect(parseChangedFiles(diff).get('gone.txt')).toBe('deleted')
  })

  it('重命名两侧都在：旧路径 deleted、新路径 added', () => {
    const diff = ['diff --git a/old.go b/new.go', 'similarity index 95%', 'rename from old.go', 'rename to new.go'].join('\n')
    expect(paths(diff)).toEqual(['new.go', 'old.go'])
    const m = parseChangedFiles(diff)
    expect(m.get('old.go')).toBe('deleted')
    expect(m.get('new.go')).toBe('added')
  })

  it('类别修正不串到下一个文件', () => {
    const diff = [
      'diff --git a/new.txt b/new.txt',
      'new file mode 100644',
      '@@ -0,0 +1 @@',
      '+x',
      'diff --git a/old.txt b/old.txt',
      '@@ -1 +1 @@',
      '-a',
      '+b',
    ].join('\n')
    const m = parseChangedFiles(diff)
    expect(m.get('new.txt')).toBe('added')
    expect(m.get('old.txt')).toBe('modified')
  })

  it('带空格的路径不被截断', () => {
    const diff = 'diff --git a/docs/my notes.md b/docs/my notes.md'
    expect(paths(diff)).toEqual(['docs/my notes.md'])
  })

  it('空 diff 得到空映射，不抛异常', () => {
    expect(parseChangedFiles('').size).toBe(0)
  })
})
