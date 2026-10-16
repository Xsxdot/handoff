// register.test.ts —— claude plugin test 用例：锁 B434 契约 §4.7 条 112–127 与条 279。
//
// 世界由测试的 on 钩子扮演（kit 下面没有真 fs / process）：
//   - fs.list / fs.read / process.run(rm) 读写一个内存信箱 files；
//   - prompt.read / prompt.fill / prompt.submit 记录输入框与提交；
//   - mock.env 给 HOME，mock.clock 给可拨的假时钟（条 125/126 不靠真等待）。
import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'
import type { On } from 'claude-code'

const HOME = '/home/tester'
const BOX = `${HOME}/.handoff/desktop-wake` // 回放 Go 侧 proto.DesktopWakeDirRel（条 127）
const SID = 'cli-self-0001'
const OTHER = 'cli-other-0002'

type Submitted = { text: string; asUser: boolean | undefined; kind: string; existedAtSubmit: boolean[] }

/** world 搭一个内存世界并挂在测试的 on 上；返回可读的记录。 */
function world(on: On, init: { files?: Record<string, string>; draft?: string } = {}) {
  const files = new Map<string, string>(Object.entries(init.files ?? {}))
  const box = { text: init.draft ?? '' }
  const reads: string[] = []
  const removed: string[] = []
  const submitted: Submitted[] = []
  const fills: string[] = []
  const watch: string[] = []
  const lists: string[] = []
  let promptReads = 0
  on('session.id', () => ({ value: SID }))
  on('session.start', (_$, e) => ({ cwd: e.cwd }))
  on('ui.log', () => ({ value: undefined }))
  on('fs.list', (_$, e) => {
    lists.push(e.path)
    if (e.path !== BOX) return { deny: `no such dir ${e.path}` }
    const value = [...files.keys()].map((name) => ({ name, kind: 'file' as const, size: 1, mtimeMs: 0, isLink: false }))
    return { value }
  })
  on('fs.read', (_$, e) => {
    const name = e.path.slice(BOX.length + 1)
    reads.push(name)
    const text = files.get(name)
    return text === undefined ? { deny: `ENOENT ${e.path}` } : { value: text }
  })
  on('process.run', (_$, e) => {
    const [cmd, flag, path] = e.argv
    if (cmd !== 'rm' || flag !== '-f' || path === undefined) return { deny: `unexpected argv ${e.argv.join(' ')}` }
    const name = path.slice(BOX.length + 1)
    removed.push(name)
    files.delete(name)
    return { value: { exitCode: 0, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('prompt.read', () => {
    promptReads++
    return { value: { text: box.text, cursor: box.text.length } }
  })
  on('prompt.fill', (_$, e) => {
    fills.push(e.text)
    box.text = e.text
    return { isFilled: true }
  })
  on('prompt.submit', (_$, e) => {
    const origin = e.origin
    submitted.push({
      text: e.text,
      asUser: origin.kind === 'plugin' ? origin.asUser : undefined,
      kind: origin.kind,
      existedAtSubmit: watch.map((n) => files.has(n)),
    })
    return { text: e.text, origin: e.origin }
  })
  return { files, box, reads, removed, submitted, fills, watch, lists, promptReads: () => promptReads }
}

async function start($: Engine): Promise<void> {
  await $.session.start({ cwd: '/work', surface: 'desktop', isInteractive: true })
}

test('一封本会话的信：恰一次提交、全文、提交时文件已删、asUser:true（条 112/113/279）', async ($, on) => {
  const letter = `${SID}.0000000000000000001.md`
  const w = world(on, { files: { [letter]: '[handoff] 卡 B1 有 1 件新事\n正文' } })
  w.watch.push(letter)
  mock.env(on, { HOME })
  const clock = mock.clock(on)
  await start($)
  await clock.advance(1500)
  expect(w.submitted.length).toBe(1)
  expect(w.submitted[0]?.text).toBe('[handoff] 卡 B1 有 1 件新事\n正文')
  expect(w.submitted[0]?.existedAtSubmit).toEqual([false])
  expect(w.submitted[0]?.kind).toBe('plugin')
  expect(w.submitted[0]?.asUser).toBe(true)
})

test('空信箱：零提交（条 114）', async ($, on) => {
  const w = world(on)
  mock.env(on, { HOME })
  const clock = mock.clock(on)
  await start($)
  await clock.advance(1500 + 5000 * 3)
  expect(w.lists).toEqual([BOX, BOX, BOX, BOX]) // 确实查了 4 轮（反证「没查所以没提交」）
  expect(w.submitted.length).toBe(0)
})

test('两封信：两次提交、文件名升序、各等于一封全文（条 115–117）', async ($, on) => {
  const a = `${SID}.0000000000000000002.md`
  const b = `${SID}.0000000000000000010.md`
  const w = world(on, { files: { [b]: 'second', [a]: 'first' } })
  mock.env(on, { HOME })
  const clock = mock.clock(on)
  await start($)
  await clock.advance(1500)
  expect(w.submitted.map((s) => s.text)).toEqual(['first', 'second'])
})

test('别的会话的信不读不删；点开头与 .tmp 不读（条 118–120）', async ($, on) => {
  const mine = `${SID}.0000000000000000009.md`
  const other = `${OTHER}.0000000000000000001.md`
  const tmp = `.${SID}.0000000000000000003.md.tmp`
  const tmp2 = `${SID}.0000000000000000004.md.tmp`
  const w = world(on, { files: { [other]: 'not mine', [tmp]: 'half', [tmp2]: 'half2', [mine]: 'mine' } })
  mock.env(on, { HOME })
  const clock = mock.clock(on)
  await start($)
  await clock.advance(1500 + 5000)
  expect(w.submitted.map((s) => s.text)).toEqual(['mine']) // 正对照：本会话的信照常取走
  expect(w.reads).toEqual([mine])
  expect(w.removed).toEqual([mine])
  expect(w.files.has(other)).toBe(true)
  expect(w.files.has(tmp)).toBe(true)
  expect(w.files.has(tmp2)).toBe(true)
})

test('预填 [handoff]：清空输入框并恰一次提交原文（条 121/122）', async ($, on) => {
  const w = world(on, { draft: '[handoff] 你是卡 B1 的协调者' })
  mock.env(on, { HOME })
  const clock = mock.clock(on)
  await start($)
  await clock.advance(1500)
  expect(w.fills).toEqual([''])
  expect(w.box.text).toBe('')
  expect(w.submitted.map((s) => s.text)).toEqual(['[handoff] 你是卡 B1 的协调者'])
  expect(w.submitted[0]?.asUser).toBe(true)
})

test('预填非 [handoff]：零提交、输入框不变（条 123/124）', async ($, on) => {
  const w = world(on, { draft: '我自己在打字 [handoff]' })
  mock.env(on, { HOME })
  const clock = mock.clock(on)
  await start($)
  await clock.advance(1500 + 5000)
  expect(w.promptReads()).toBe(2) // 确实看了两轮输入框
  expect(w.submitted.length).toBe(0)
  expect(w.fills).toEqual([])
  expect(w.box.text).toBe('我自己在打字 [handoff]')
})

test('节拍：1.5s 首查、之后每 5s（条 125/126）', async ($, on) => {
  const w = world(on)
  mock.env(on, { HOME })
  const clock = mock.clock(on)
  await start($)
  await clock.advance(1499)
  w.files.set(`${SID}.0000000000000000001.md`, 'one')
  await clock.advance(0)
  expect(w.submitted.length).toBe(0)
  await clock.advance(1)
  expect(w.submitted.length).toBe(1)
  w.files.set(`${SID}.0000000000000000002.md`, 'two')
  await clock.advance(4999)
  expect(w.submitted.length).toBe(1)
  await clock.advance(1)
  expect(w.submitted.length).toBe(2)
})
