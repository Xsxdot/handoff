// diff.test.ts —— parseUnifiedDiff 的穷举测试：多文件/新增删除/二进制/trailer/非法输入。
import { describe, expect, it } from 'vitest'
import { parseUnifiedDiff } from './diff'

const TWO_FILES = `diff --git a/README.md b/README.md
index 1457913..99c4d50 100644
--- a/README.md
+++ b/README.md
@@ -247,7 +247,8 @@ Task state machine
 retry with continue.
-old line
+new line one
+new line two
diff --git a/internal/agentd/task.go b/internal/agentd/task.go
index aaa..bbb 100644
--- a/internal/agentd/task.go
+++ b/internal/agentd/task.go
@@ -118,3 +118,4 @@ func handleDone
 ctx line
+added
`

// agentd 的 Diff() 会在 diff 后拼 "\n\n" + git log --oneline 提交列表
const WITH_TRAILER = TWO_FILES + `
4e3de5e feat: done 幂等
a41f8c2 fix: watchdog`

describe('parseUnifiedDiff', () => {
  it('多文件分组与 ± 统计', () => {
    const r = parseUnifiedDiff(TWO_FILES)
    expect(r).not.toBeNull()
    expect(r!.files.map((f) => f.path)).toEqual(['README.md', 'internal/agentd/task.go'])
    expect(r!.files[0].adds).toBe(2)
    expect(r!.files[0].dels).toBe(1)
    expect(r!.files[1].adds).toBe(1)
    expect(r!.files[1].dels).toBe(0)
  })

  it('行类型标注正确', () => {
    const lines = parseUnifiedDiff(TWO_FILES)!.files[0].lines
    expect(lines[0]).toEqual({ kind: 'hunk', text: '@@ -247,7 +247,8 @@ Task state machine' })
    expect(lines.find((l) => l.kind === 'del')!.text).toBe('-old line')
    expect(lines.filter((l) => l.kind === 'add')).toHaveLength(2)
    expect(lines.find((l) => l.kind === 'ctx')!.text).toBe(' retry with continue.')
  })

  it('diff 后的提交列表进 trailer，不混进文件行', () => {
    const r = parseUnifiedDiff(WITH_TRAILER)!
    expect(r.files).toHaveLength(2)
    expect(r.trailer).toContain('4e3de5e feat: done 幂等')
    expect(r.files[1].lines.some((l) => l.text.includes('4e3de5e'))).toBe(false)
  })

  it('新文件与二进制文件不炸：头部行归为 ctx', () => {
    const t = `diff --git a/new.bin b/new.bin
new file mode 100644
Binary files /dev/null and b/new.bin differ`
    const r = parseUnifiedDiff(t)!
    expect(r.files[0].path).toBe('new.bin')
    expect(r.files[0].adds).toBe(0)
    expect(r.files[0].lines.every((l) => l.kind === 'ctx')).toBe(true)
  })

  it('非 diff 文本返回 null（调用方回退裸文本）', () => {
    expect(parseUnifiedDiff('随便一段话')).toBeNull()
    expect(parseUnifiedDiff('')).toBeNull()
  })
})
