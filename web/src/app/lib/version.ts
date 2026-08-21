// 更新版本比较工具。
//
// 职责：复刻 internal/selfupdate.CompareVersion 的版本排序语义，供所有更新面共用。
// 边界：只回答「a 是否严格新于 b」；不决定是否允许安装或如何展示版本。

type ParsedVersion = {
  core: [string, string, string]
  pre: string[]
}

// isComparableVersion 判断一个版本戳能否参与比较。
//
// 为什么必须单独有它：hasNewer 返回 false 有两种截然不同的含义——「确实不更新」
// 和「我根本比不出来」。开发构建的版本戳是提交号（如 7dec31185aaa），解析失败也
// 得到 false，调用方若直接把 false 当成「已是最新」，就是把不知道说成了没事。
export function isComparableVersion(value: string): boolean {
  return parseVersion(value) !== null
}

// hasNewer 判断 latest 是否严格新于 current；任一版本格式不合法时返回 false。
// **false 不等于「已是最新」**：不可比时也是 false，调用方要先问 isComparableVersion。
export function hasNewer(latest: string, current: string): boolean {
  if (!latest || !current) return false
  const a = parseVersion(latest)
  const b = parseVersion(current)
  if (!a || !b) return false
  return compareVersion(a, b) > 0
}

// parseVersion 对齐 Go 侧：允许一个 v 前缀，构建元数据不参与比较，核心号必须三段。
function parseVersion(input: string): ParsedVersion | null {
  let value = input.startsWith('v') ? input.slice(1) : input
  const plus = value.indexOf('+')
  if (plus >= 0) value = value.slice(0, plus)

  let coreText = value
  let pre: string[] = []
  const dash = value.indexOf('-')
  if (dash >= 0) {
    coreText = value.slice(0, dash)
    const preText = value.slice(dash + 1)
    if (!preText) return null
    pre = preText.split('.')
    if (pre.some((id) => id.length === 0)) return null
  }

  const core = coreText.split('.')
  if (core.length !== 3 || core.some((part) => !/^\d+$/.test(part))) return null
  return { core: core as [string, string, string], pre }
}

// compareVersion 比较已经解析的版本，核心段用数字语义而非字典序。
function compareVersion(a: ParsedVersion, b: ParsedVersion): number {
  for (let i = 0; i < a.core.length; i++) {
    const c = compareNumeric(a.core[i], b.core[i])
    if (c !== 0) return c
  }
  return comparePre(a.pre, b.pre)
}

// compareNumeric 以去前导零后的长度与字典序比较，避免 JS 大整数精度破坏排序。
function compareNumeric(a: string, b: string): number {
  const aa = a.replace(/^0+(?=\d)/, '')
  const bb = b.replace(/^0+(?=\d)/, '')
  if (aa.length !== bb.length) return aa.length < bb.length ? -1 : 1
  return aa === bb ? 0 : aa < bb ? -1 : 1
}

// comparePre 对齐 Go 侧：正式版高于预发布版，预发布标识符按自然序比较。
function comparePre(a: string[], b: string[]): number {
  if (a.length === 0 && b.length === 0) return 0
  if (a.length === 0) return 1
  if (b.length === 0) return -1
  for (let i = 0; i < Math.min(a.length, b.length); i++) {
    const c = compareIdent(a[i], b[i])
    if (c !== 0) return c
  }
  return a.length === b.length ? 0 : a.length < b.length ? -1 : 1
}

// compareIdent 把数字段与非数字段交替拆开，数字按数值、文字按字典序比较。
function compareIdent(a: string, b: string): number {
  while (a || b) {
    if (!a) return -1
    if (!b) return 1
    const aDigit = /^\d/.test(a)
    const bDigit = /^\d/.test(b)
    const aMatch = a.match(aDigit ? /^\d+/ : /^\D+/)
    const bMatch = b.match(bDigit ? /^\d+/ : /^\D+/)
    const ar = aMatch?.[0] ?? ''
    const br = bMatch?.[0] ?? ''
    if (aDigit && bDigit) {
      const c = compareNumeric(ar, br)
      if (c !== 0) return c
    } else if (aDigit !== bDigit) {
      return aDigit ? -1 : 1
    } else if (ar !== br) {
      return ar < br ? -1 : 1
    }
    a = a.slice(ar.length)
    b = b.slice(br.length)
  }
  return 0
}
