import { describe, expect, it } from 'vitest'
import { parseChangedFiles } from './changedFiles'

describe('parseChangedFiles', () => {
  it('从 diff --git 行取出相对路径', () => {
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
    expect([...parseChangedFiles(diff)].sort()).toEqual(['Makefile', 'internal/agentd/transport_test.go'])
  })

  it('新增文件也算改动', () => {
    const diff = ['diff --git a/new.txt b/new.txt', 'new file mode 100644', '--- /dev/null', '+++ b/new.txt'].join('\n')
    expect([...parseChangedFiles(diff)]).toEqual(['new.txt'])
  })

  it('删除文件取 a/ 侧路径', () => {
    const diff = ['diff --git a/gone.txt b/gone.txt', 'deleted file mode 100644', '--- a/gone.txt', '+++ /dev/null'].join('\n')
    expect([...parseChangedFiles(diff)]).toEqual(['gone.txt'])
  })

  it('重命名两侧都算改动', () => {
    const diff = ['diff --git a/old.go b/new.go', 'similarity index 95%', 'rename from old.go', 'rename to new.go'].join('\n')
    expect([...parseChangedFiles(diff)].sort()).toEqual(['new.go', 'old.go'])
  })

  it('带空格的路径不被截断', () => {
    const diff = 'diff --git a/docs/my notes.md b/docs/my notes.md'
    expect([...parseChangedFiles(diff)]).toEqual(['docs/my notes.md'])
  })

  it('空 diff 得到空集合，不抛异常', () => {
    expect(parseChangedFiles('').size).toBe(0)
  })
})
