// diff.ts —— unified diff 的解析（纯函数）。
//
// 职责：把 agentd Diff() 返回的文本（git diff + 空行 + git log --oneline）切成
// 按文件分组的行级结构，供 DiffView 着色渲染。
//
// 边界：
//   - 不碰 DOM、不发请求：必须能脱离浏览器被穷举测试（与 frames.ts 同一纪律）
//   - 只认标准 `diff --git` 输出；认不出返回 null，由调用方整体回退裸文本——
//     解析失败绝不能吞掉内容，diff 是审阅的核心证据
//   - 不做语法高亮、不做并排视图（spec §10 范围外）

// DiffLine 是 diff 的一行：add/del 着色，hunk 是 @@ 头，其余（上下文、
// index/mode/Binary 等头部行）一律 ctx——审阅者要看得到它们，但不着色。
export interface DiffLine {
  kind: 'add' | 'del' | 'ctx' | 'hunk'
  text: string
}

// FileDiff 是一个文件的改动组。
export interface FileDiff {
  path: string
  adds: number
  dels: number
  lines: DiffLine[]
}

// ParsedDiff 是整份 diff 的解析产物；trailer 是 diff 之后的非 diff 尾巴
// （agentd 拼上的提交列表），原样保留展示。
export interface ParsedDiff {
  files: FileDiff[]
  trailer: string
}

// FILE_HEAD 匹配 `diff --git a/<path> b/<path>`，取 b 侧路径（新路径为准，
// 重命名时审阅者关心改成了什么名字）。
const FILE_HEAD = /^diff --git a\/.* b\/(.*)$/

// parseUnifiedDiff 解析一份 unified diff 文本。
//
// 返回：
//   - ParsedDiff: 至少解析出一个文件
//   - null: 文本里没有任何 `diff --git` 头（含空串）——不是 diff，调用方回退
//
// 判定 trailer 的规则：最后一个文件块结束后、且与 diff 隔了空行的剩余文本。
// 实现上：遇到空行且当前不在 hunk 连续行里，即认为 diff 部分结束。
export function parseUnifiedDiff(text: string): ParsedDiff | null {
  const lines = text.split('\n')
  const files: FileDiff[] = []
  let cur: FileDiff | null = null
  let trailerStart = -1

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const head = FILE_HEAD.exec(line)
    if (head) {
      cur = { path: head[1], adds: 0, dels: 0, lines: [] }
      files.push(cur)
      continue
    }
    if (!cur) continue

    // diff 与提交列表之间由空行分隔（agentd Diff() 的拼接约定）；
    // 空行后如果再没有文件头，剩余全是 trailer
    if (line === '') {
      const rest = lines.slice(i + 1)
      if (rest.length > 0 && !rest.some((l) => FILE_HEAD.test(l))) {
        trailerStart = i + 1
        break
      }
      continue
    }

    // index/---/+++ 只是文件组已展示的定位噪声，跳过后让首个可读行从 hunk 开始。
    if (line.startsWith('index ') || line.startsWith('--- ') || line.startsWith('+++ ')) {
      continue
    }

    if (line.startsWith('@@')) {
      cur.lines.push({ kind: 'hunk', text: line })
    } else if (line.startsWith('+') && !line.startsWith('+++')) {
      cur.adds++
      cur.lines.push({ kind: 'add', text: line })
    } else if (line.startsWith('-') && !line.startsWith('---')) {
      cur.dels++
      cur.lines.push({ kind: 'del', text: line })
    } else {
      cur.lines.push({ kind: 'ctx', text: line })
    }
  }

  if (files.length === 0) return null
  const trailer = trailerStart >= 0 ? lines.slice(trailerStart).join('\n').trim() : ''
  return { files, trailer }
}
