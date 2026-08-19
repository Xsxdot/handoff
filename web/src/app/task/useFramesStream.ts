// useFramesStream —— 结构化回合帧流的读取 hook。
//
// 数据源：GET /api/tasks/{id}/frames?tail=65536&follow=1，application/x-ndjson。
// 与 useRenderStream 同一套 I/O 形状（fetch + ReadableStream + AbortController），
// 差别只在解析：那边是纯文本，这边每行是一个 Frame。
//
// 职责：加载 / 跟随 / 回翻 / 坏行计数 / 帧数上限
// 边界：只管 I/O 与状态，不决定长什么样；不做块聚合（那是 frames.ts 的纯函数）
//
// 语义注意（与 /render 完全一致）：
//   - 文件不存在时返回 200 空内容（任务刚派发、模型还没吐第一帧是正常态），不是错误
//   - 响应头 X-Handoff-Frames-Size 是响应开始时的文件大小
//   - offset 与 tail 的单位都是**字节**，且两者互斥
//   - follow 空闲时 agentd 每 20s 发一个换行保活，会出现空行（scanLines 已按正常跳过）
//   - 回翻用 offset（与 tail 互斥），并按帧 seq 去重——服务端会跳过 offset 落进的
//     那半行，实际起点比请求的 offset 靠后，按字节算会错位
//
// 组件卸载必须 AbortController 中止——否则每次进出详情页都泄漏一条常驻连接。
// 这个坑 RenderPanel 的头注释已经记了一次，这里是第二条常驻连接，同样适用。
import { useCallback, useEffect, useRef, useState } from 'react'
import type { Frame } from '../../api/types'
import { errorMessage } from '../lib/format'
import { maxLoadedFrames, scanLines } from './frames'

// pageBytes 是一页的字节数：进入时的 tail 量，也是「加载更早」每次往前的量。
// 与 RenderPanel 的 tail=65536 同量级——两个接口的「默认看多少」不该无缘无故不同。
export const pageBytes = 65536

// FramesStream 是 hook 的返回形状。
export interface FramesStream {
  // frames 是已加载帧，按 seq 升序
  frames: Frame[]
  // badLines 是累计跳过的坏行数（面板必须把它显示出来，见下方「不静默」一节）
  badLines: number
  // startOffset 是已加载区间的起始字节偏移；0 表示已到文件头，「加载更早」应消失
  startOffset: number
  // sizeUnknown 表示服务端未回送文件大小；此时 startOffset=0 不是「已到文件头」
  sizeUnknown: boolean
  // error 是流错误的人类可读原因；非 null 时面板显示错误条 + 重试
  error: string | null
  // active 表示是否仍在跟随（流未结束）
  active: boolean
  // atCap 表示已加载帧数触到 maxLoadedFrames：停止回翻并提示改用 handoff frames
  atCap: boolean
  // loadingEarlier 表示一次回翻正在进行中
  loadingEarlier: boolean
  loadEarlier: () => void
  retry: () => void
}

// 不静默：这个 hook 的所有降级路径都必须有一个对应的返回字段，让面板画出来。
//   - 网络/协议错 → error（面板给错误条 + 重试按钮）
//   - 坏行        → badLines（面板顶部计数）
//   - 帧数到顶    → atCap（面板停用「加载更早」并提示）
//   - 流结束      → active=false（面板把「跟随中」换成「已结束」）
//   - 大小头缺失  → sizeUnknown（面板说明当前只显示尾部窗口）
// 少任何一条，界面上就会出现「看着正常、其实不是」的状态——那是最坏的一种。

export function useFramesStream(taskId: string | undefined): FramesStream {
  const [frames, setFrames] = useState<Frame[]>([])
  const [badLines, setBadLines] = useState(0)
  const [startOffset, setStartOffset] = useState(0)
  const [sizeUnknown, setSizeUnknown] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [active, setActive] = useState(true)
  // loadingEarlier 表示一次回翻正在进行中（loadEarlier 里置位，finally 复位）
  const [loadingEarlier, setLoadingEarlier] = useState(false)
  // reloadKey 只用来强制重跑主 effect（重试按钮）
  const [reloadKey, setReloadKey] = useState(0)

  useEffect(() => {
    if (!taskId) return
    const ac = new AbortController()
    let cancelled = false
    setFrames([])
    setBadLines(0)
    setStartOffset(0)
    setSizeUnknown(false)
    setError(null)
    setActive(true)

    const run = async () => {
      try {
        const resp = await fetch(
          `/api/tasks/${encodeURIComponent(taskId)}/frames?tail=${pageBytes}&follow=1`,
          { credentials: 'same-origin', signal: ac.signal },
        )
        if (cancelled) return
        if (resp.status === 401) {
          setError('未授权：会话已失效，请重新打开控制台')
          setActive(false)
          return
        }
        if (!resp.ok) {
          // 非 2xx 时尽量透出 agentd 的错误原文，读不到再回退状态码文案
          let msg = `agentd 返回 ${resp.status} ${resp.statusText}`
          try {
            const body = (await resp.json()) as { error?: string }
            if (body.error) msg = body.error
          } catch {
            // 响应体不是 JSON，保留兜底文案
          }
          setError(msg)
          setActive(false)
          return
        }
        const hdr = resp.headers.get('X-Handoff-Frames-Size')
        const total = hdr !== null && hdr !== '' ? Number(hdr) : null
        const hasKnownSize = total !== null && Number.isFinite(total)
        setSizeUnknown(!hasKnownSize)
        // 文件大小本身不对外暴露（没有界面用它，YAGNI），只用来推起始偏移。
        // tail=pageBytes 时服务端的起点就是 max(0, size - pageBytes)（再向后对齐到
        // 下一个换行）。没有专门的响应头告诉我们起点，但它可以从 size 推出来，
        // 用来判断「还有没有更早的」——推小了只会多请求一次，不会漏。
        setStartOffset(hasKnownSize && total !== null ? Math.max(0, total - pageBytes) : 0)
        if (!resp.body) {
          setError('响应没有可读流（浏览器不支持 ReadableStream？）')
          setActive(false)
          return
        }
        const reader = resp.body.getReader()
        const decoder = new TextDecoder()
        let buffered = ''
        for (;;) {
          const { done, value } = await reader.read()
          if (cancelled || done) break
          const scan = scanLines(buffered, decoder.decode(value, { stream: true }))
          buffered = scan.rest
          if (scan.bad > 0) setBadLines((n) => n + scan.bad)
          if (scan.frames.length > 0) {
            setFrames((prev) => {
              const next = prev.concat(scan.frames)
              // 到顶后丢最旧的：长跑任务不该把 DOM 撑爆。丢弃后 startOffset
              // 不再准确，但那时「加载更早」已被 atCap 停用，不会被用到。
              return next.length > maxLoadedFrames ? next.slice(next.length - maxLoadedFrames) : next
            })
          }
        }
        if (!cancelled) setActive(false)
      } catch (err) {
        if (cancelled) return // 组件卸载中止是预期收尾，不算错误
        if (err instanceof DOMException && err.name === 'AbortError') return
        setError(errorMessage(err))
        setActive(false)
      }
    }

    void run()
    return () => {
      cancelled = true
      ac.abort() // 离开页面必须中止常驻连接，否则每次进出都泄漏一条
    }
  }, [taskId, reloadKey])

  const retry = useCallback(() => setReloadKey((k) => k + 1), [])

  // earlierAcRef 持有正在进行的回翻请求，卸载时一并中止。
  // 与主流的 AbortController 分开：回翻是一次性请求，中止它不该影响跟随。
  const earlierAcRef = useRef<AbortController | null>(null)
  useEffect(() => () => earlierAcRef.current?.abort(), [])

  // loadEarlier 往前取一页并 prepend。
  //
  // 为什么用 offset 而不是 tail：接口的 offset 与 tail 互斥，且 tail 只能从文件尾
  // 回溯——回翻要的是「从更早的某个字节开始」，只有 offset 能表达。
  //
  // 为什么按 seq 去重而不是按字节数截断：服务端会跳过 offset 落进的那半行
  // （W4a §7.2），所以实际起点会比请求的 offset 靠后一点，按字节算会错位。
  // 帧的 seq 是任务内单调递增的，用它去重既精确又能直接当停止条件——
  // 一旦读到 seq >= 当前最小 seq，说明已经追上已加载区间，可以中止请求了。
  const loadEarlier = useCallback(() => {
    if (!taskId) return
    if (loadingEarlier) return
    if (startOffset <= 0) return // 已到文件头
    if (frames.length >= maxLoadedFrames) return // 到顶，改用 handoff frames

    const from = Math.max(0, startOffset - pageBytes)
    const minSeq = frames.length > 0 ? frames[0].seq : Number.MAX_SAFE_INTEGER
    const ac = new AbortController()
    earlierAcRef.current?.abort()
    earlierAcRef.current = ac
    setLoadingEarlier(true)

    const run = async () => {
      try {
        const resp = await fetch(
          `/api/tasks/${encodeURIComponent(taskId)}/frames?offset=${from}`,
          { credentials: 'same-origin', signal: ac.signal },
        )
        if (resp.status === 401) {
          setError('未授权：会话已失效，请重新打开控制台')
          return
        }
        if (!resp.ok) {
          let msg = `agentd 返回 ${resp.status} ${resp.statusText}`
          try {
            const body = (await resp.json()) as { error?: string }
            if (body.error) msg = body.error
          } catch {
            // 响应体不是 JSON，保留兜底文案
          }
          setError(msg)
          return
        }
        if (!resp.body) {
          setError('响应没有可读流（浏览器不支持 ReadableStream？）')
          return
        }
        const reader = resp.body.getReader()
        const decoder = new TextDecoder()
        let buffered = ''
        let bad = 0
        const earlier: Frame[] = []
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          const scan = scanLines(buffered, decoder.decode(value, { stream: true }))
          buffered = scan.rest
          bad += scan.bad
          let caughtUp = false
          for (const fr of scan.frames) {
            if (fr.seq >= minSeq) {
              caughtUp = true // 已追上已加载区间，后面的都是重复
              break
            }
            earlier.push(fr)
          }
          if (caughtUp || earlier.length >= maxLoadedFrames) {
            ac.abort() // 主动收线：不把整个文件读完
            break
          }
        }
        if (bad > 0) setBadLines((n) => n + bad)
        setFrames((prev) => {
          const next = earlier.concat(prev)
          // 回翻方向到顶时丢最新的：用户正在往前看，保留他要看的那一头
          return next.length > maxLoadedFrames ? next.slice(0, maxLoadedFrames) : next
        })
        setStartOffset(from)
        setError(null)
      } catch (err) {
        if (err instanceof DOMException && err.name === 'AbortError') return
        setError(errorMessage(err))
      } finally {
        setLoadingEarlier(false)
      }
    }
    void run()
  }, [taskId, loadingEarlier, startOffset, frames])

  return {
    frames,
    badLines,
    startOffset,
    sizeUnknown,
    error,
    active,
    atCap: frames.length >= maxLoadedFrames,
    loadingEarlier,
    loadEarlier,
    retry,
  }
}
