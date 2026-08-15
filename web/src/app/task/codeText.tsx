// codeText —— 正文里的代码渲染，范围钉死在三段围栏与行内 code。
//
// 职责：
//   - splitFences: 按闭合的三段围栏切分
//   - splitInline: 按行内反引号切分（不跨行）
//   - CodeText: 把上面两级切分渲染成 <pre> / <code> / 纯文本
//
// 边界：
//   - **不是 markdown 渲染器**。标题、粗体、列表、链接一律当纯文本
//   - 不做语法高亮，不引任何依赖
//   - 不接受 HTML：文本原样进 React 文本节点，天然没有 XSS 面
//
// 为什么不引 react-markdown：收益分布很不均匀。审阅时真正需要和散文区分开的
// 是代码块；标题/粗体/列表不渲染也完全读得懂。为这点边际收益引一个体积不小的
// 依赖，外加 XSS 面（必须禁 raw HTML）和**流式抖动**（增量逐字到达，围栏闭合
// 瞬间整块重排）——不划算。范围被钉死在围栏上，所以它也不会长成一个必须维护的
// 半吊子 markdown 实现；将来真需要，换成 react-markdown 是纯替换。

// Segment 是一段切分结果：code 为真表示按代码样式渲染。
export interface Segment {
  code: boolean
  text: string
}

// splitFences 按三段围栏 ``` 切分文本。
//
// 参数：text 原始文本（可能是流式中途的半截）
// 返回：交替的纯文本段与代码段
//
// 注意：**围栏未闭合时整段按纯文本降级**。这是流式渲染不抖的关键——增量是
// 逐字到达的，闭合前先按代码块画，闭合瞬间就会整块重排。判据是围栏标记的
// 个数为奇数（首段之后每两个标记围出一个代码段）。
export function splitFences(text: string): Segment[] {
  const parts = text.split('```')
  // 1 段 = 没有围栏；偶数段 = 有未闭合的围栏。两种都整段降级为纯文本。
  if (parts.length < 3 || parts.length % 2 === 0) return [{ code: false, text }]
  return parts.map((p, i) => {
    if (i % 2 === 0) return { code: false, text: p }
    // 代码段的首行是语言标注（可能为空），不属于代码内容
    const nl = p.indexOf('\n')
    return { code: true, text: nl >= 0 ? p.slice(nl + 1) : p }
  })
}

// splitInline 按行内反引号切分单段纯文本。
//
// 参数：text 一段不含围栏的纯文本
// 返回：交替的纯文本段与行内代码段
//
// 注意：行内 code 不跨行——跨行的那是围栏的事。未闭合时整段按纯文本降级，
// 理由与 splitFences 相同。
export function splitInline(text: string): Segment[] {
  const out: Segment[] = []
  let rest = text
  for (;;) {
    const open = rest.indexOf('`')
    if (open < 0) break
    const close = rest.indexOf('`', open + 1)
    if (close < 0) break
    const inner = rest.slice(open + 1, close)
    if (inner.includes('\n')) break // 跨行不算行内 code，整段降级
    out.push({ code: false, text: rest.slice(0, open) })
    out.push({ code: true, text: inner })
    rest = rest.slice(close + 1)
  }
  if (out.length === 0) return [{ code: false, text }]
  out.push({ code: false, text: rest })
  return out
}

// CodeText 渲染一段正文：围栏成 <pre>，行内反引号成 <code>，其余纯文本保留换行。
//
// 参数：text 正文（可能是流式中途的半截）
// 返回：可直接放进块组件的 React 元素
export function CodeText({ text }: { text: string }) {
  return (
    <>
      {splitFences(text).map((seg, i) =>
        seg.code ? (
          <pre
            key={i}
            className="my-1.5 overflow-x-auto rounded-md bg-muted/60 p-2 font-mono text-xs leading-relaxed"
          >
            {seg.text}
          </pre>
        ) : (
          <span key={i} className="whitespace-pre-wrap break-words">
            {splitInline(seg.text).map((s, j) =>
              s.code ? (
                <code key={j} className="rounded bg-muted/60 px-1 py-0.5 font-mono text-xs">
                  {s.text}
                </code>
              ) : (
                <span key={j}>{s.text}</span>
              ),
            )}
          </span>
        ),
      )}
    </>
  )
}
