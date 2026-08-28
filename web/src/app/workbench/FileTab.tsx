// FileTab —— 查看并编辑基准目录下的一个文件（B81 spec §6）。
//
// 职责：
//   - 取 GET /api/workspaces/file，按 sha256 有没有值分出三态：可编辑 / 二进制 / 超限
//   - 可编辑时提供 textarea、脏标记、保存按钮与 ⌘S
//   - 写回经 PUT /api/workspaces/file，带上读到那一版的 sha256 当前置条件
//
// 边界：
//   - **不做语法高亮、不引 Monaco/CodeMirror**（spec §0）。判据是「能不能改个配置、
//     改段文案」，不是在浏览器里重建一个 IDE
//   - 不自动保存：executor 就在同一个工作树里干活，自动保存等于让人在不看屏幕的
//     时候和 agent 抢写
//   - 不监听文件变更：agentd 没有推送通道。代价是「文件被 executor 改了」只在按
//     保存时才知道（spec §1.2 明知并接受的取舍）
//   - 不新建、不删除、不改名：只编辑已存在的文件
//
// 错误处理：agentd 的中文错误原文原样透传（诚实展示纪律），不吞成「操作失败」。
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, fetchWorkspaceFile, writeWorkspaceFile } from '../../api/client'
import type { FileRead } from '../../api/types'
import { errorMessage, formatSize } from '../lib/format'
import { clearDraft, draftKey, loadDraft, saveDraft } from './fileDraft'
import type { BaseDir } from './useWorkbench'

export function FileTab({
  base,
  rel,
  initial,
  onDraftChange,
  onDraftChangeLive,
}: {
  base: BaseDir
  rel: string
  initial?: { draft: string; baseSha: string }
  onDraftChange?: (d: { draft: string; baseSha: string } | null) => void
  /** 分屏工作台的内存寄存缝；仅由需要在 pane 常驻时保持脏态的宿主传入。 */
  onDraftChangeLive?: (d: { draft: string; baseSha: string } | null) => void
}) {
  const [read, setRead] = useState<FileRead | null>(
    // initial 命中时用草稿造一个临时的 read，让「editable + dirty」从第一帧就成立，
    // 不等网络。为什么 content 置空串而不是草稿本身：content 的语义是**磁盘那一版**，
    // 草稿是草稿——两者不等才是脏，填成一样反而把脏标记骗没了
    initial === undefined ? null : { content: '', size: 0, sha256: initial.baseSha },
  )
  const [error, setError] = useState<string | null>(null)
  // 草稿初值也来自 initial：网络回来之前 textarea 里就该是切走之前的内容
  const [draft, setDraft] = useState(initial?.draft ?? '')
  // baseSha 是「我这份草稿是从哪一版改出来的」。initial 带回来的是草稿当时存的基线；
  // 保存成功后换成服务端返回的新哈希，而不是重新读一次——那会在两次请求之间再开一个窗口
  const [baseSha, setBaseSha] = useState(initial?.baseSha ?? '')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  // ConflictState 是一次未解决的写入冲突。current 是服务端在 409 里附带的磁盘现状，
  // 两个出口都要用它：「放弃」要它的正文，「覆盖」要它的哈希当新基线
  //
  // reason 区分冲突来源：'save' 是保存时 409 撞出来的，'stale-draft' 是打开文件时
  // 发现本地草稿基于的版本已过期。两者 UI 相同、文案不同
  type ConflictState = { current: FileRead; confirming: boolean; reason: 'save' | 'stale-draft' }
  const [conflict, setConflict] = useState<ConflictState | null>(null)

  // initialRef 让 effect 在跑的那一刻读到**当前渲染**的 initial，同时不把 initial 写进
  // 依赖数组：Shell 每次渲染都会新建一个 initial 对象（tab 内容没变时语义也没变），
  // 放进去会让文件在「读取中→已读」之间来回闪
  const initialRef = useRef(initial)
  initialRef.current = initial
  useEffect(() => {
    // cancelled 防止「快速连点两个文件」时先发的请求后到，把后选的内容盖掉
    let cancelled = false
    // initial 命中时不能 setRead(null)：那会把「正在读取」盖到草稿上、textarea 消失。
    // 网络没回来就该先把草稿画出来，这里保持那个假 read 在位
    setRead(
      initialRef.current === undefined
        ? null
        : { content: '', size: 0, sha256: initialRef.current.baseSha },
    )
    setError(null)
    setSaveError('')
    fetchWorkspaceFile(base.path, rel, base.machine || undefined)
      .then((r) => {
        if (cancelled) return
        setRead(r)
        // 有 initial（tab 切换寄存的草稿）就用它；否则回退 localStorage 层。
        // 两层在同一把钥匙下，但优先级必须明确：内存层更新鲜（它活过的是切 tab，
        // 比刷新更近）
        const fromInitial = initialRef.current
        let restored: { draft: string; baseSha: string } | null = fromInitial
          ? { draft: fromInitial.draft, baseSha: fromInitial.baseSha }
          : null
        if (restored === null) {
          restored = loadDraft(draftKey(base.machine || 'local', base.path, rel))
        }
        // 有草稿就用草稿，但 read.content 仍是磁盘那一版——dirty 是两者之差，
        // 这样切回来时脏标记还在，而不是把草稿误当成干净内容。
        // baseSha 同样以草稿原始基线为准、而不是磁盘当前哈希：若换成磁盘哈希，
        // 普通保存会用新基线比对通过、绕过「确认覆盖」的二次确认；且去抖 effect
        // 会把草稿里的 baseSha 覆写成磁盘哈希，打开一次 500ms 后 stale 标记就被
        // 摧毁，刷新后不再判为过期草稿。草稿基线 ≠ 磁盘哈希正是冲突条的判据
        setDraft(restored?.draft ?? r.content)
        setBaseSha(restored?.baseSha ?? r.sha256 ?? '')
        // 草稿连 baseSha 一起存，就是为了这一刻：拿它和磁盘现在的 sha256 一比，
        // 不等说明磁盘在你离开期间变了。走**同一条冲突条、同两个出口**，
        // 不发明第二套逻辑——用户面对的是同一个问题
        if (restored !== null && r.sha256 !== undefined && restored.baseSha !== r.sha256) {
          setConflict({ current: r, confirming: false, reason: 'stale-draft' })
        }
      })
      .catch((err) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [base.path, base.machine, rel])

  // editable 的判据只有一个：sha256 有没有值。二进制与超限在服务端就不给哈希，
  // 前端不再按扩展名或大小另判一次——两边各判一次早晚会分叉
  const editable = read !== null && baseSha !== ''
  const dirty = editable && draft !== read.content

  // draftRef 记住最新草稿，卸载时一次性刷出去。
  //
  // 为什么不是每次 onChange 都回写：那会把整棵 WorkbenchPage 重渲染一遍。orca 正是
  // 在这儿栽过（issue #826：一次 reload dispatch 扇出成 N 次 EditorPanel 重建把渲染
  // 进程卡死，只能加 75ms 去抖）。我们不用去抖这种概率性方案——打字只动组件本地
  // state，卸载时刷一次，精确且打字期间父层零重渲染
  const draftRef = useRef<{ draft: string; baseSha: string } | null>(null)
  useEffect(() => {
    draftRef.current = dirty ? { draft, baseSha } : null
  }, [dirty, draft, baseSha])

  // 回调本身也用 ref 存住，卸载 effect 的依赖才能是空数组。
  //
  // 直接把 onDraftChange 写进依赖的话，调用方每次渲染传一个新的内联箭头函数就会
  // 触发一次「清理 + 重建」——而清理函数正是回写草稿的那一句，草稿会在用户还在
  // 打字的时候被提前刷出去。用 ref 就对调用方零要求，不必让 Shell 那边额外维护
  // 一个 useCallback（那种约束写在别处、坏在这里，最难查）
  const notifyRef = useRef(onDraftChange)
  notifyRef.current = onDraftChange
  useEffect(() => {
    return () => notifyRef.current?.(draftRef.current)
  }, [])

  // 草稿去抖 500ms 写一份进 localStorage，撑过刷新与误关。
  //
  // localStorage 这一层**不要求精确**：刷新丢掉最后半秒的输入可以接受，而每次
  // 按键写一次 localStorage 会掉帧。这与内存层（卸载时精确刷一次）是两种不同的
  // 要求，所以用两种不同的做法，不是一处该统一而没统一
  useEffect(() => {
    if (!dirty) return
    const key = draftKey(base.machine || 'local', base.path, rel)
    const t = setTimeout(() => saveDraft(key, draft, baseSha), 500)
    return () => clearTimeout(t)
  }, [dirty, draft, baseSha, base.machine, base.path, rel])

  // saveWith(sha) 用显式传入的哈希当基线的保存路径。为什么要显式传哈希而不是读
  // state：overwrite 需要先 setBaseSha(next) 再用 next 发请求，而 React 的 state
  // 更新是异步的，同一轮里从 state 读到的还是旧值——这是这里最容易踩的坑，
  // 传参把「这次保存用哪个基线」从渲染时序里剥离开
  const saveWith = useCallback(async (sha: string) => {
    if (!editable || !dirty || saving) return
    setSaving(true)
    setSaveError('')
    try {
      const res = await writeWorkspaceFile(
        base.path,
        rel,
        { content: draft, base_sha256: sha },
        base.machine || undefined,
      )
      // 回基线：read.content 换成刚存进去的内容，baseSha 换成新哈希。
      // 这样 dirty 立刻变 false，而下一次保存自动用新基线
      setRead((r) => (r === null ? r : { ...r, content: draft, size: res.size, sha256: res.sha256 }))
      setBaseSha(res.sha256)
      // 保存成功，localStorage 里那份草稿没有存在意义了——留着下次刷新还会
      // 被当成过期草稿再触发一遍冲突条
      clearDraft(draftKey(base.machine || 'local', base.path, rel))
      // 保存成功，内存层那份草稿也清掉：不清的话 tab 内容还带着旧 draft，
      // 关闭时会误弹「未保存」确认框（文件其实已经保存了）。卸载回写那条路
      // 只在卸载时走，这里要先主动通知
      notifyRef.current?.(null)
    } catch (err) {
      // 先认冲突：409 不是普通失败，它带着磁盘现状，要交给冲突条而不是 saveError
      const cur = conflictCurrent(err)
      if (cur !== null) {
        setConflict({ current: cur, confirming: false, reason: 'save' })
        setSaveError('')
        return
      }
      // 其他失败**不动草稿**：用户的输入是唯一一份，界面上再没有第二处能找回来
      setSaveError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  // baseSha 不进依赖数组：函数体里没直接用它（实际基线是传入的 sha，回基线用 res.sha256），
  // editable/dirty 已在依赖里覆盖了它对门卫的间接影响
  }, [base.path, base.machine, rel, draft, editable, dirty, saving])

  const save = useCallback(() => saveWith(baseSha), [saveWith, baseSha])

  // discard 把草稿整体换成磁盘现状，基线跟着换——等价于「重新打开这个文件」
  const discard = () => {
    if (conflict === null) return
    setRead(conflict.current)
    setDraft(conflict.current.content)
    setBaseSha(conflict.current.sha256 ?? '')
    setConflict(null)
    // 放弃的是「我的改动」，localStorage 里那份草稿也必须一起丢掉，
    // 否则下次刷新它又回来，白放弃一场
    clearDraft(draftKey(base.machine || 'local', base.path, rel))
  }

  // overwrite 拿 current.sha256 当**新的** base_sha256 重发一次。
  //
  // 为什么不是「跳过校验」：覆盖的语义是「我看过磁盘上那一版了，接受它当新基线」。
  // 若这中间磁盘又变了，第二次照样 409——这正确，而一个 force 标志会把它变成静默覆盖。
  //
  // 为什么要二次确认：orca 敢在「警告过了」之后直接放行手动保存，因为它有 watcher,
  // 横幅在你按保存之前就出现了。我们没有 watcher，冲突只在保存那一刻才暴露，
  // 用户在此之前从没被警告过，所以覆盖必须自带确认
  const overwrite = async () => {
    if (conflict === null) return
    const next = conflict.current.sha256 ?? ''
    setConflict(null)
    setBaseSha(next)
    await saveWith(next)
  }

  return (
    <div
      className="flex h-full flex-col"
      onKeyDown={(e) => {
        // ⌘S 挂在**本 tab 的容器上走冒泡**，不挂 window、更不用 capture：
        // 分屏时另一侧可能是终端，⌘S 在终端有焦点时应该归终端。这与 B74 的
        // ⌘K 是同一个教训的另一面
        if ((e.metaKey || e.ctrlKey) && e.key === 's') {
          e.preventDefault()
          void save()
        }
      }}
    >
      <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-muted-foreground">
        <span className="truncate font-mono text-foreground">{rel}</span>
        <span className="ml-auto shrink-0">{headerNote(read, dirty)}</span>
        {editable && (
          <button
            type="button"
            className="shrink-0 rounded border px-2 py-0.5 disabled:opacity-50"
            disabled={!dirty || saving}
            onClick={() => void save()}
          >
            {saving ? '保存中…' : '保存'}
          </button>
        )}
      </div>
      {saveError !== '' && <p className="border-b px-3 py-1.5 text-xs text-destructive">{saveError}</p>}
      {conflict !== null && (
        <div className="border-b bg-muted px-3 py-2 text-xs">
          <p className="text-foreground">
            {conflict.reason === 'stale-draft'
              ? '本地草稿基于的版本已经变了。'
              : '文件已在磁盘上变了（很可能是 executor 改的）。'}
          </p>
          {conflict.confirming ? (
            <div className="mt-1.5 flex items-center gap-2">
              <span className="text-destructive">
                覆盖会丢掉磁盘上那一版的改动，不可撤销。
              </span>
              <button type="button" className="rounded border px-2 py-0.5"
                onClick={() => void overwrite()}>确认覆盖</button>
              <button type="button" className="rounded border px-2 py-0.5"
                onClick={() => setConflict({ ...conflict, confirming: false })}>取消</button>
            </div>
          ) : (
            <div className="mt-1.5 flex items-center gap-2">
              <button type="button" className="rounded border px-2 py-0.5"
                onClick={() => discard()}>放弃我的改动，载入磁盘版本</button>
              <button type="button" className="rounded border px-2 py-0.5"
                onClick={() => setConflict({ ...conflict, confirming: true })}>用我的内容覆盖</button>
            </div>
          )}
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-auto">
        {error !== null ? (
          <p className="p-4 text-sm text-destructive">{error}</p>
        ) : read === null ? (
          <p className="p-4 text-sm text-muted-foreground">正在读取 {rel}…</p>
        ) : read.binary ? (
          <p className="p-4 text-sm text-muted-foreground">
            前 8 KiB 里出现了 NUL 字节，agentd 不会把它当文本返回，本版不支持在线编辑。
          </p>
        ) : editable ? (
          <textarea
            aria-label={rel}
            className="h-full w-full resize-none whitespace-pre p-4 font-mono text-xs leading-relaxed outline-none"
            spellCheck={false}
            value={draft}
            onChange={(e) => {
              const nextDraft = e.target.value
              setDraft(nextDraft)
              // 分屏 pane 不会因焦点切换而卸载，宿主必须在输入时寄存脏态，
              // 否则用户随后关闭文件时，外层看不到这份尚未落盘的内容。
              onDraftChangeLive?.(nextDraft === read.content ? null : { draft: nextDraft, baseSha })
            }}
          />
        ) : (
          <pre className="p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap">{read.content}</pre>
        )}
      </div>
    </div>
  )
}

// headerNote 给出头部右侧那句话：三态各一句，可编辑时只在脏的时候出现。
//
// 为什么把「为什么不能编辑」写在头部而不是只置灰保存按钮：一个灰按钮不解释任何
// 事情，用户会反复点它。二进制与超限是两个不同的原因，各说各的
function headerNote(read: FileRead | null, dirty: boolean): string {
  if (read === null) return ''
  if (read.binary) return '二进制文件，不支持在线编辑'
  if (read.truncated) return `文件 ${formatSize(read.size)}，仅显示开头 1 MB，不支持在线编辑`
  return dirty ? '未保存' : ''
}

// conflictCurrent 从一个错误里认出 409 冲突并取出磁盘现状；不是冲突时返回 null。
//
// 防御性地校验形状而不是直接断言：旧版 agentd 可能只回 {error}，那时应当退回
// 普通错误展示，而不是让界面因为读不到 current 崩掉
function conflictCurrent(err: unknown): FileRead | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null
  const body = err.body as { current?: FileRead } | undefined
  const cur = body?.current
  if (cur === undefined || typeof cur.content !== 'string') return null
  return cur
}
