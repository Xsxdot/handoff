// register.ts —— handoff 唤醒信箱的会话内接收端（B434 契约 §3.13、§4.7 条 112–127）。
//
// 职责：
//   - session.start 后 1.5s 首查、此后每 5s 查一次本会话的信箱：
//     $HOME/.handoff/desktop-wake/<CLI 会话 id>.<19 位纳秒>.md，按文件名升序逐封
//     「读全文 → 删除 → $.prompt.submit({ text, asUser: true })」；
//   - 输入框预填以 [handoff] 开头时清空输入框并替人提交一次（深链 q 不自动发送的补位，
//     只对已有进程的会话有效）。
//
// 边界：
//   - 只搬运正文，不解析、不改写来信（围栏、名册、转义都由 agentd 的 desktopLetter 负责，
//     契约 §4.17、拍板 D7）；
//   - 不 import handoff 主模块任何东西（条 163）；字面量与 Go 侧逐字一致由测试回放锁定（条 127）；
//   - 先删后提交：提交失败则这封信丢失，重复投递与补投以账本为准（契约 §3.6）。
import type { EngineInterface, Register } from 'claude-code'

/** 与 internal/proto/desktop_coordinator.go#DesktopWakeMarker 逐字一致。 */
export const WAKE_MARKER = '[handoff]'
/** 与 internal/proto/desktop_coordinator.go#DesktopWakeDirRel 逐字一致。 */
export const WAKE_DIR_REL = '.handoff/desktop-wake'
/** session.start 之后的首查延迟（条 125）。 */
export const FIRST_CHECK_MS = 1500
/** 首查之后的查信间隔（条 126）。 */
export const POLL_MS = 5000

const TAG = 'handoff-desktop-wake'

/**
 * isOwnLetter 判断信箱里的文件名是否是本会话的一封完整来信。
 *
 * 只认 `<会话 id>.<19 位数字>.md`：别的会话的信（条 118/119）、写到一半的
 * `.<名>.tmp`（点开头，条 120）都不匹配。
 */
export function isOwnLetter(name: string, sessionId: string): boolean {
  const prefix = `${sessionId}.`
  if (!name.startsWith(prefix)) return false
  return /^[0-9]{19}\.md$/.test(name.slice(prefix.length))
}

export const register: Register = (on) => {
  on('session.start', async ($, e, next) => {
    const started = await next(e)
    const home = await $.env.get('HOME')
    const sessionId = await $.session.id()
    if (home === undefined || home === '') {
      // 取不到 HOME 就找不到信箱：如实说出来，不猜路径
      $.ui.log(`${TAG}: 取不到 HOME，信箱唤醒不可用`)
      return started
    }
    const box = `${home}/${WAKE_DIR_REL}`
    let busy = false
    let lastError = ''
    const check = async (): Promise<void> => {
      // 上一轮还没结束（比如提交在排队）就跳过本轮，避免同一封信被两轮各读一次
      if (busy) return
      busy = true
      try {
        await submitPrefill($)
        await drainBox($, box, sessionId)
        lastError = ''
      } catch (err) {
        const text = `${TAG}: 查信失败：${String(err)}`
        // 同一个错误每 5 秒重复一次没有信息量，只在变化时落一行
        if (text !== lastError) $.ui.log(text)
        lastError = text
      } finally {
        busy = false
      }
    }
    $.clock.after(FIRST_CHECK_MS, () => {
      void check()
      $.clock.every(POLL_MS, () => { void check() })
    })
    $.ui.log(`${TAG}: 监视信箱 ${box}/${sessionId}.*.md`, { to: 'debug' })
    return started
  })
}

/** submitPrefill：输入框以 [handoff] 开头 → 清空并按原文提交一次（条 121–124）。 */
async function submitPrefill($: EngineInterface): Promise<void> {
  const draft = await $.prompt.read()
  if (!draft.text.trimStart().startsWith(WAKE_MARKER)) return
  await $.prompt.fill({ text: '', mode: 'replace' })
  await $.prompt.submit({ text: draft.text, asUser: true })
  $.ui.log(`${TAG}: 已提交预填简报（${draft.text.length} 字符）`, { to: 'debug' })
}

/** drainBox：按文件名升序逐封「读 → 删 → 提交」本会话的来信（条 112–120）。 */
async function drainBox($: EngineInterface, box: string, sessionId: string): Promise<void> {
  let entries
  try {
    entries = await $.fs.list(box)
  } catch {
    // 信箱目录要等 agentd 第一次写信才建（0o700），不存在就是没有信
    return
  }
  const names = entries
    .filter((x) => x.kind === 'file' && isOwnLetter(x.name, sessionId))
    .map((x) => x.name)
    .sort()
  for (const name of names) {
    const path = `${box}/${name}`
    const text = await $.fs.read(path)
    // 先删后提交：删不掉就不提交，否则下一轮会把同一封信再提交一次
    const rm = await $.process.run(['rm', '-f', path])
    if (rm.exitCode !== 0) {
      throw new Error(`删除来信失败（exit ${rm.exitCode}）：${path} ${rm.stderr}`)
    }
    if (text.trim() === '') {
      $.ui.log(`${TAG}: 跳过空信 ${name}`, { to: 'debug' })
      continue
    }
    await $.prompt.submit({ text, asUser: true })
    $.ui.log(`${TAG}: 已提交来信 ${name}（${text.length} 字符）`, { to: 'debug' })
  }
}
