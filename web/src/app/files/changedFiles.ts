// changedFiles —— 从 `handoff diff` 的正文里解析出「相对基线已改动」的路径集合。
//
// 职责：给右栏文件树提供 M 角标的数据来源（spec §4）。
//
// 边界：
//   - **不是 git status**。`handoff diff` 是 `git diff base...HEAD`，只反映**已提交**
//     的改动，看不见工作区里未提交的编辑。角标含义因此是「相对基线已改动」，
//     tooltip 必须原样说清楚，不能让人当成 IDE 里那个 M
//   - 只解析 `diff --git` 头行与 rename 行，不解析 hunk。要的只是路径集合
//   - 没有任务挂在这个目录上时就没有数据源，返回空集合——不为一个装饰性角标
//     去开 git status 接口（那是文件写入期真正需要时才做的事）
//
// 为什么不用 `--- a/` / `+++ b/` 两行来取：新增文件那侧是 `/dev/null`，删除文件
// 另一侧也是 `/dev/null`，还要分别处理；`diff --git` 头行两侧永远都在。
import { useEffect, useState } from 'react'
import { fetchTaskDiff } from '../../api/client'

// RENAME_FROM 与 RENAME_TO 用于把重命名的两侧都算作改动——旧路径消失了、
// 新路径出现了，两个位置在树上都该有标记。
const RENAME_FROM = 'rename from '
const RENAME_TO = 'rename to '

// ChangeStatus 是一个路径相对基线的改动类别。
//
// 判据全部来自 diff 的块内头行，不猜：`new file mode`=新增、`deleted file mode`=
// 删除、`rename from/to` 两侧分别当删除与新增，其余一律 modified。
export type ChangeStatus = 'added' | 'modified' | 'deleted'

// headPaths 从 `diff --git a/<路径> b/<路径>` 头行取出两侧路径。
//
// 路径可能含空格，所以不能按空格切；改为找 ` b/` 这个分隔点，左右两段分别
// 剥掉 `a/` 与 `b/` 前缀。取不出来时返回空数组（调用方跳过该行）。
function headPaths(line: string): string[] {
  const rest = line.slice('diff --git '.length)
  const sep = rest.indexOf(' b/')
  if (sep < 0) return []
  const left = rest.slice(0, sep)
  const right = rest.slice(sep + 1)
  const out: string[] = []
  if (left.startsWith('a/')) out.push(left.slice(2))
  if (right.startsWith('b/')) out.push(right.slice(2))
  return out
}

// parseChangedFiles 解析 diff 正文，返回「相对路径 → 改动类别」的映射。
//
// 参数：
//   - diff: `GET /api/tasks/{id}/diff` 返回的正文（可能为空串）
//
// 返回：路径到 ChangeStatus 的映射。解析不出任何头行时返回空映射，不抛异常。
//
// 注意：类别是**块内后置修正**——先按头行把两侧记为 modified，块里再出现
// `new file mode` / `deleted file mode` / rename 行时覆盖。git 的输出顺序保证
// 这些行一定跟在自己的头行之后，所以覆盖不会串到别的文件上。
export function parseChangedFiles(diff: string): Map<string, ChangeStatus> {
  const out = new Map<string, ChangeStatus>()
  // block 是当前 diff 块涉及的路径（新增/删除时两侧同名，重命名时是一对）
  let block: string[] = []
  for (const line of diff.split('\n')) {
    if (line.startsWith('diff --git ')) {
      block = headPaths(line)
      for (const p of block) out.set(p, 'modified')
      continue
    }
    if (line.startsWith('new file mode')) {
      for (const p of block) out.set(p, 'added')
      continue
    }
    if (line.startsWith('deleted file mode')) {
      for (const p of block) out.set(p, 'deleted')
      continue
    }
    // 重命名：旧路径在树上已经不存在（按删除），新路径是这次才出现的（按新增）
    if (line.startsWith(RENAME_FROM)) out.set(line.slice(RENAME_FROM.length), 'deleted')
    else if (line.startsWith(RENAME_TO)) out.set(line.slice(RENAME_TO.length), 'added')
  }
  return out
}

// useChangedFiles 取「这个目录上挂着的任务」的改动映射。
//
// 参数：
//   - taskId: 目录上正在执行的任务 id；为 null 表示这个目录没有任务
//
// 返回：路径 → ChangeStatus 的映射。没有任务、或取 diff 失败时都返回空映射——
// 角标是装饰，缺了不影响文件树可用，所以失败静默降级，不弹错误。
export function useChangedFiles(taskId: string | null): Map<string, ChangeStatus> {
  const [files, setFiles] = useState<Map<string, ChangeStatus>>(() => new Map())
  useEffect(() => {
    if (!taskId) {
      setFiles(new Map())
      return
    }
    let cancelled = false
    fetchTaskDiff(taskId)
      .then((r) => {
        if (!cancelled) setFiles(parseChangedFiles(r.diff))
      })
      .catch(() => {
        if (!cancelled) setFiles(new Map())
      })
    return () => {
      cancelled = true
    }
  }, [taskId])
  return files
}
