// persist.ts —— 工作台状态的编解码层（2026-08-20 状态同步 spec §5.1）。
//
// 职责：
//   - Workbench + BaseDir ↔ 落盘用的 JSON 字符串
//   - 读回来时逐字段校验，坏数据整行丢弃
//   - 规则二（抹掉已死的 sessionId）与写回时的差分，两个纯函数
//
// 边界：
//   - 不碰 React、不发请求、不认识 localStorage
//   - **不落草稿**：file tab 的 draft / baseSha 在编码时被剥掉（spec §1.2 的明确决定）
//   - 不认识「哪些会话是活的」：liveIds 由调用方给
//
// 为什么逐字段查类型而不是信 `as`：这份数据来自服务端，而服务端只是原样搬运
// 前端**上一个版本**写进去的东西。字段改名、结构变形、用户手改数据库，
// 三条路径都会让 `as` 在运行时炸在离现场很远的地方。这与 treePrefs.isPrefs 同款纪律。
import { MAX_GROUPS, type Tab, type TabContent, type TabGroup, type Workbench } from './tabs'
import type { BaseDir } from './useWorkbench'

// PERSIST_VERSION 是落盘格式版本。形状将来不兼容地变了就 +1，
// 老数据在 decodeBase 里整份丢弃——迁移一份「工作现场」不值得，重开一下就有。
export const PERSIST_VERSION = 1

// PersistedBase 是落在 payload 里的完整结构。
//
// 它同时装 BaseDir 元数据与 Workbench：只存 Workbench 的话，恢复时拿着一个 key
// 却不知道面包屑该写什么——key 本身（path@machine）还原不出 label 与 projectName。
// **不存 key**：key 是行的身份，由行本身提供；payload 里再存一份就有了两个真相。
interface PersistedBase {
  v: number
  base: Omit<BaseDir, 'key'>
  wb: Workbench
}

// encodeBase 把一个基准目录的现场序列化成 payload 字符串。
//
// 参数：base 是该目录的元数据；wb 是它的 tab 组。
// 返回：JSON 字符串，直接作为 PUT 的 payload 发出。
// 注意：file tab 的 draft / baseSha 会被剥掉，草稿继续留在 localStorage。
export function encodeBase(base: BaseDir, wb: Workbench): string {
  const payload: PersistedBase = {
    v: PERSIST_VERSION,
    base: { kind: base.kind, path: base.path, label: base.label, projectName: base.projectName, machine: base.machine },
    wb: {
      groups: wb.groups.map((g) => ({ tabs: g.tabs.map(stripTab), activeId: g.activeId })),
      active: wb.active,
      sizes: [...wb.sizes],
    },
  }
  return JSON.stringify(payload)
}

// stripTab 去掉一个 tab 里不该落盘的部分。
//
// 两类：file tab 的草稿两字段，以及 terminal tab 的 incompatible。
// 写成一个独立函数而不是内联三元，是为了将来再多一种「不落盘字段」时只有一处要改。
function stripTab(t: Tab): Tab {
  if (t.content.kind === 'file') {
    return { id: t.id, content: { kind: 'file', rel: t.content.rel } }
  }
  if (t.content.kind === 'terminal' && t.content.incompatible !== undefined) {
    // incompatible 是**服务端此刻**的结论，不是布局的一部分：换回兼容版本之后
    // 它就该消失，存下来会让那个 tab 一直显示成不可用。落盘的形状也因此保持稳定，
    // 不会因为这个标记翻转而白白多一次 PUT
    const out: { kind: 'terminal'; seq: number; sessionId?: string; rel?: string } = {
      kind: 'terminal',
      seq: t.content.seq,
    }
    if (t.content.sessionId !== undefined) out.sessionId = t.content.sessionId
    if (t.content.rel !== undefined) out.rel = t.content.rel
    return { id: t.id, content: out }
  }
  return { id: t.id, content: t.content }
}

// decodeBase 把一行 payload 解回基准目录与它的 tab 组。
//
// 参数：
//   - baseKey: 这一行的 key，直接作为返回 BaseDir 的 key
//   - raw: 服务端存的 payload 字符串
//
// 返回：解析并校验通过时返回 { base, wb }；**任何一处不对就返回 null**
//（调用方丢弃整行并 warn，绝不半信半疑地用一部分）。
export function decodeBase(baseKey: string, raw: string): { base: BaseDir; wb: Workbench } | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return null
  }
  if (!isObject(parsed)) return null
  if (parsed.v !== PERSIST_VERSION) return null

  const b = parsed.base
  if (!isObject(b)) return null
  if (b.kind !== 'workspace' && b.kind !== 'home' && b.kind !== 'scratch') return null
  if (!isStr(b.path) || !isStr(b.label) || !isStr(b.projectName) || !isStr(b.machine)) return null

  const wb = parseWorkbench(parsed.wb)
  if (wb === null) return null

  return {
    base: { key: baseKey, kind: b.kind, path: b.path, label: b.label, projectName: b.projectName, machine: b.machine },
    wb,
  }
}

// parseWorkbench 校验并归一化一个 Workbench。返回 null = 形状不对。
function parseWorkbench(raw: unknown): Workbench | null {
  if (!isObject(raw)) return null
  if (!Array.isArray(raw.groups) || raw.groups.length === 0 || raw.groups.length > MAX_GROUPS) return null
  if (!Array.isArray(raw.sizes) || raw.sizes.length !== raw.groups.length) return null
  if (!raw.sizes.every((n) => typeof n === 'number' && Number.isFinite(n) && n > 0)) return null
  // active 越界会让渲染层去取 groups[5] —— 那是一次静默的 undefined，
  // 表现为「中央区一片空白但左栏是选中的」，比整行丢弃难查得多
  if (typeof raw.active !== 'number' || !Number.isInteger(raw.active)) return null
  if (raw.active < 0 || raw.active >= raw.groups.length) return null

  const groups: TabGroup[] = []
  for (const g of raw.groups) {
    if (!isObject(g) || !Array.isArray(g.tabs)) return null
    if (g.activeId !== null && !isStr(g.activeId)) return null
    const tabs: Tab[] = []
    for (const t of g.tabs) {
      if (!isObject(t) || !isStr(t.id)) return null
      const content = parseContent(t.content)
      if (content === null) return null
      tabs.push({ id: t.id, content })
    }
    // activeId 指向一个已经不在列表里的 tab 是坏数据：渲染层会显示空面板
    if (g.activeId !== null && !tabs.some((t) => t.id === g.activeId)) return null
    // 反过来，有 tab 却没有 activeId 也不成立（closeTab 保证了这条不变式）
    if (g.activeId === null && tabs.length > 0) return null
    groups.push({ tabs, activeId: g.activeId })
  }
  return { groups, active: raw.active, sizes: raw.sizes as number[] }
}

// parseContent 校验一个 tab 的内容。返回 null = 种类不认识或字段不对。
function parseContent(raw: unknown): TabContent | null {
  if (!isObject(raw)) return null
  switch (raw.kind) {
    case 'blank':
      return { kind: 'blank' }
    case 'terminal': {
      if (typeof raw.seq !== 'number' || !Number.isFinite(raw.seq)) return null
      const out: { kind: 'terminal'; seq: number; sessionId?: string; rel?: string } = {
        kind: 'terminal',
        seq: raw.seq,
      }
      if (raw.sessionId !== undefined) {
        if (!isStr(raw.sessionId)) return null
        out.sessionId = raw.sessionId
      }
      if (raw.rel !== undefined) {
        if (!isStr(raw.rel)) return null
        out.rel = raw.rel
      }
      return out
    }
    case 'file':
      if (!isStr(raw.rel)) return null
      // 草稿即使被塞进来了也不采信：编码时剥掉的东西，解码时也不认
      return { kind: 'file', rel: raw.rel }
    case 'tui':
      if (!isStr(raw.taskId)) return null
      return { kind: 'tui', taskId: raw.taskId }
    default:
      return null
  }
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function isStr(v: unknown): v is string {
  return typeof v === 'string'
}

// isEmptyWorkbench 判断一个工作台是不是一个 tab 都没有。
//
// 参数：wb 是要检查的工作台。
// 返回：所有组都没有 tab 时为 true，否则为 false。
// 注意：只看 tab 数量，不因 active 或 sizes 形状异常改变「空」的语义。
// 用途：空的工作台**编码为删除**（PUT payload: null），不存一行空记录——
// 用户把一个目录的 tab 全关掉就是不想再看见它，存空记录只会白占 50 行配额里的一格。
export function isEmptyWorkbench(wb: Workbench): boolean {
  return wb.groups.every((g) => g.tabs.length === 0)
}

// pruneDeadSessions 抹掉不在 liveIds 里的 sessionId（spec §2 规则二）。
//
// 参数：wb 是刚恢复出来的工作台；liveIds 是服务端会话列表里**还活着**的那些 id。
// 返回：新的 Workbench；tab 一个都不删，只把死掉的 sessionId 字段去掉。
//
// 为什么留着 tab 而不是删掉：「我在这一栏放了个终端」本身就是布局的一部分。
// 抹掉 id 之后 TerminalTab 挂载时会原地建一个新会话，位置不变。
export function pruneDeadSessions(wb: Workbench, liveIds: Set<string>): Workbench {
  return {
    ...wb,
    groups: wb.groups.map((g) => ({
      ...g,
      tabs: g.tabs.map((t) => {
        if (t.content.kind !== 'terminal') return t
        const id = t.content.sessionId
        if (id === undefined || liveIds.has(id)) return t
        const next: { kind: 'terminal'; seq: number; rel?: string } = { kind: 'terminal', seq: t.content.seq }
        if (t.content.rel !== undefined) next.rel = t.content.rel
        return { id: t.id, content: next }
      }),
    })),
  }
}

// markIncompatibleSessions 给指向「协议不兼容会话」的终端 tab 打上标记。
//
// 参数：wb 是刚恢复出来的工作台；ids 是服务端报为 incompatible 的会话 id。
// 返回：新的 Workbench；不删 tab、不抹 sessionId，只加一个标记。
//
// 为什么不能像死会话那样直接抹掉 sessionId：那个会话**还活着**，抹掉 id
// 等于让 TerminalTab 原地再建一个新 shell，而旧的还在后台跑着没人管得着。
// 打标记之后 TerminalTab 不建连、不重连，直接给「重开一个终端」的出口，
// 由用户决定要不要放弃它（A spec 的协议错配降级）。
export function markIncompatibleSessions(wb: Workbench, ids: Set<string>): Workbench {
  if (ids.size === 0) return wb
  return {
    ...wb,
    groups: wb.groups.map((g) => ({
      ...g,
      tabs: g.tabs.map((t) => {
        if (t.content.kind !== 'terminal') return t
        const id = t.content.sessionId
        if (id === undefined || !ids.has(id)) return t
        return { id: t.id, content: { ...t.content, incompatible: true } }
      }),
    })),
  }
}

// diffPayloads 比较两份「key → payload 字符串」，分出要写的与要删的。
//
// 参数：prev 是上次已落盘的快照；next 是当前应该落盘的内容。
// 返回：changed 是新增或内容变了的 key；removed 是 prev 有而 next 没有的 key。
//
// 为什么比字符串而不是比对象：payload 本来就要序列化成字符串才能发出去，
// 顺手拿它当比较依据，就不必写一个深比较，也不会因为对象字段顺序不同而误判。
export function diffPayloads(
  prev: Record<string, string>,
  next: Record<string, string>,
): { changed: string[]; removed: string[] } {
  const changed: string[] = []
  for (const [k, v] of Object.entries(next)) {
    if (prev[k] !== v) changed.push(k)
  }
  const removed: string[] = []
  for (const k of Object.keys(prev)) {
    if (!(k in next)) removed.push(k)
  }
  return { changed, removed }
}
