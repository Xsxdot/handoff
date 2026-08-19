// fileDraft —— 文件编辑草稿的 localStorage 层（B81 spec §7.2）。
//
// 职责：
//   - 按「机器 + 工作树路径 + 相对路径」给每份草稿一个稳定的键
//   - 存/取/删草稿，配额满时按最近使用淘汰
//
// 边界：
//   - **不碰工作树**。草稿绝不能落在工作树里——那是 executor 正在干活的 git 仓库，
//     草稿文件会进 git status、会被 agent 顺手 commit、会污染 handoff diff、
//     会让下一次 dispatch 的「工作区必须干净」检查直接拒发
//   - 不做跨机接管（换了机器连浏览器 tab 都没了）。服务端草稿是 B82
//   - 不判断草稿新旧对错：baseSha 的比对归 FileTab
//   - 写不进去就静默放弃，绝不抛错——调用它的时候用户正在打字
const PREFIX = 'handoff:draft:'

// StoredDraft 是落在 localStorage 里的一条草稿。
//
// savedAt 存在的唯一理由是配额满时的淘汰排序，不用于展示——「你的草稿存于 3 分钟前」
// 对用户没有任何可行动的价值
interface StoredDraft {
  draft: string
  baseSha: string
  savedAt: number
}

// draftKey 组出一份草稿的键。
//
// 三段都必须进键：同一个 rel 在两台机器上、在同一台机器的两个工作树里，
// 是三份互不相干的文件。少一段就会串味
export function draftKey(machine: string, workspacePath: string, rel: string): string {
  return `${PREFIX}${machine}:${workspacePath}:${rel}`
}

// loadDraft 取回一份草稿；不存在或数据损坏时返回 null。
//
// 损坏当作没有：一份存坏的草稿救不回来，而让 JSON.parse 抛到渲染层会把整个
// 文件 tab 弄白屏——那比丢一份草稿糟得多
export function loadDraft(key: string): StoredDraft | null {
  try {
    const raw = localStorage.getItem(key)
    if (raw === null) return null
    const v = JSON.parse(raw) as StoredDraft
    if (typeof v.draft !== 'string' || typeof v.baseSha !== 'string') return null
    return v
  } catch {
    return null
  }
}

// saveDraft 写入一份草稿，配额满时按 savedAt 淘汰最旧的再重试一次。
//
// **永不抛错**：调用它的时候用户正在打字，一个存储配额的报错帮不上任何忙，
// 而草稿在内存里（TabContent）还有一份，退回去就是了
export function saveDraft(key: string, draft: string, baseSha: string): void {
  const payload = JSON.stringify({ draft, baseSha, savedAt: Date.now() } satisfies StoredDraft)
  try {
    localStorage.setItem(key, payload)
    return
  } catch {
    // 落到淘汰逻辑
  }
  if (!evictOldest()) return
  try {
    localStorage.setItem(key, payload)
  } catch {
    // 淘汰一份还是不够就放弃：内存草稿仍在，静默降级
  }
}

// clearDraft 删掉一份草稿（保存成功 / 放弃草稿时调用）。
export function clearDraft(key: string): void {
  try {
    localStorage.removeItem(key)
  } catch {
    // 删不掉也没有可行动的补救
  }
}

// evictOldest 淘汰 savedAt 最旧的一份草稿；没有可淘汰的返回 false。
//
// 只按草稿自己的键前缀扫，绝不动别人的 localStorage 条目
function evictOldest(): boolean {
  let oldestKey: string | null = null
  let oldestAt = Infinity
  for (let i = 0; i < localStorage.length; i++) {
    const k = localStorage.key(i)
    if (k === null || !k.startsWith(PREFIX)) continue
    const v = loadDraft(k)
    const at = v?.savedAt ?? 0 // 坏数据优先淘汰
    if (at < oldestAt) {
      oldestAt = at
      oldestKey = k
    }
  }
  if (oldestKey === null) return false
  clearDraft(oldestKey)
  return true
}
