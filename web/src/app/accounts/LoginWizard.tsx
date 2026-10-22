// LoginWizard.tsx —— 「＋ 新增登录」三步对话框（B464 S2）。
//
// 职责：① 选机器 + agent 并给新 HOME 目录名（由目标机建目录）→ ② 展示该机登录命令，每 5 秒
// 请目标机检查这个 HOME，另有「我已登录，立即检查」→ ③ 识别结果（挂到已有账号 / 新账号）。
// 关掉对话框不清任何状态：定时发现会把新登录挂上（所以第二步明确写「关掉也没关系」）。
//
// 边界：建 HOME 与检查都经本机 agentd 转发到目标机（A11/A12）；本组件不直接连他机、不读凭据、
// 不猜识别结果——第三步完全按 /api/logins/check 的 LoginCheckItem 渲染。
import { useEffect, useRef, useState } from 'react'
import { checkLoginHome, createLoginHome, type AccountView, type LoginCheckItem, type LoginCommand } from '../../api/accounts'
import { copyToClipboard } from '../lib/clipboard'
import { errorMessage } from '../lib/format'
import { homeText, kindMeta, loginCommandText } from './model'

const INPUT = 'h-8 w-full rounded-md border bg-background px-2 text-xs'
const LABEL = 'mb-1 block text-xs text-muted-foreground'
const AGENTS = ['opencode', 'codex', 'grok', 'agy', 'claude']
const SHORT: Record<string, string> = { opencode: 'oc', codex: 'cx', grok: 'gk', agy: 'agy', claude: 'cl' }

/** randomSuffix 4 位小写字母/数字：只是缺省目录名，人可改；真正的冲突由目标机 409 兜住。 */
function randomSuffix(): string {
  return Math.random().toString(36).replace(/[^a-z0-9]/g, '').padEnd(4, '0').slice(0, 4)
}

function defaultDir(agent: string): string {
  return `${SHORT[agent] ?? 'login'}-${randomSuffix()}`
}

interface Props {
  open: boolean
  machines: string[]
  /** 本机（GET /api/accounts discovery 里 self=true 的机器名）：新增登录时默认不选远端变体。 */
  selfMachines: string[]
  /** 从账号卡「在其他机器登录」进来时的目标账号（agent 预选、机器避开已有登录）。 */
  account: AccountView | null
  /** ?login=new&machine=&agent= 或载体对话框空态链接带来的预选。 */
  preset?: { machine?: string; agent?: string }
  onClose: () => void
  onDone: () => void
  onNotice: (text: string) => void
}

/** LoginWizard 三步新增登录；open=false 时不渲染。 */
export function LoginWizard({ open, machines, selfMachines, account, preset, onClose, onDone, onNotice }: Props) {
  const [step, setStep] = useState(1)
  const [machine, setMachine] = useState('')
  const [agent, setAgent] = useState('opencode')
  const [dir, setDir] = useState('')
  const [created, setCreated] = useState<{ home: string; command: LoginCommand } | null>(null)
  /** 第二步是否选用远端变体（该机没有浏览器）；默认按目标机是否本机决定。 */
  const [remoteLogin, setRemoteLogin] = useState(false)
  const [items, setItems] = useState<LoginCheckItem[]>([])
  const [error, setError] = useState('')
  const [checkNote, setCheckNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [checking, setChecking] = useState(false)
  const poll = useRef<number | null>(null)

  // 打开时按来源初始化：preset > 账号已有登录避让 > 第一台机器。
  useEffect(() => {
    if (!open) return
    const nextAgent = preset?.agent || (account ? kindMeta(account.kind).cli : 'opencode')
    const nextMachine = preset?.machine
      || (account ? (machines.find((m) => !account.logins.some((l) => l.machine === m)) ?? machines[0]) : '')
      || machines[0] || ''
    setStep(1)
    setAgent(nextAgent)
    setMachine(nextMachine)
    setDir(defaultDir(nextAgent))
    setCreated(null)
    setRemoteLogin(false)
    setItems([])
    setError('')
    setCheckNote('')
    setBusy(false)
    setChecking(false)
    console.info('accounts.login_wizard.open', { machine: nextMachine, agent: nextAgent, account: account?.name ?? '' })
  // 账号对象每次轮询会换引用；只按名字与预选参数重置，避免第二步被后台刷新打回第一步。
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, account?.name, preset?.machine, preset?.agent, machines.join('|')])

  const stopPoll = () => {
    if (poll.current !== null) { window.clearInterval(poll.current); poll.current = null }
  }
  useEffect(() => stopPoll, [])

  const check = async (manual: boolean) => {
    if (!created) return
    setChecking(true)
    setError('')
    if (manual) setCheckNote('')
    console.info('accounts.login_wizard.check.start', { machine, home: created.home, manual })
    try {
      const resp = await checkLoginHome(machine, created.home)
      if (resp.logins.length > 0) {
        console.info('accounts.login_wizard.check.found', { machine, home: created.home, logins: resp.logins.length })
        stopPoll()
        setItems(resp.logins)
        setStep(3)
      } else if (manual) {
        setCheckNote('还没有识别到登录：请确认命令已在目标机跑完，再点「我已登录，立即检查」。')
      }
    } catch (cause) {
      console.error('accounts.login_wizard.check.failure', { machine, home: created.home, cause })
      setError(errorMessage(cause))
    } finally {
      setChecking(false)
    }
  }

  // 第二步：每 5 秒检查一次；关掉/进入第三步立即停表。
  useEffect(() => {
    if (!open || step !== 2 || !created) return
    poll.current = window.setInterval(() => { void check(false) }, 5000)
    return stopPoll
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, step, created?.home, machine])

  const make = async () => {
    const name = dir.trim()
    if (!/^[a-z0-9-]+$/.test(name)) { setError('目录名只能用 小写字母、数字与横线（服务端建在 ~/.handoff/home/ 下）'); return }
    setBusy(true)
    setError('')
    console.info('accounts.login_wizard.home.start', { machine, agent, dir: name })
    try {
      const resp = await createLoginHome(machine, agent.trim(), name)
      console.info('accounts.login_wizard.home.success', { machine, agent, home: resp.home })
      setCreated(resp)
      setRemoteLogin(!!resp.command.remote_run && !selfMachines.includes(machine))
      setStep(2)
    } catch (cause) {
      console.error('accounts.login_wizard.home.failure', { machine, agent, dir: name, cause })
      setError(errorMessage(cause))
    } finally {
      setBusy(false)
    }
  }

  const copy = async () => {
    if (!created) return
    const text = remoteLogin && created.command.remote_run ? created.command.remote_run : loginCommandText(created.command)
    const ok = await copyToClipboard(text)
    console.info('accounts.login_wizard.copy', { machine, remote: remoteLogin, copied: ok })
    onNotice(ok ? `已复制登录命令：${text}` : `请手动复制：${text}`)
  }

  const close = () => {
    stopPoll()
    if (step === 2 && created) onNotice(`已建好 ${homeText(created.home)}（${machine}），等待登录；定时发现会自动挂上。`)
    onClose()
  }

  if (!open) return null
  const title = account ? `在其他机器登录 · ${account.name}` : '新增登录'
  const makeDisabled = busy || !/^[a-z0-9-]+$/.test(dir.trim()) || machine.trim() === ''
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/30 p-4" role="dialog" aria-modal="true" aria-label={title}>
      <div className="wiz w-full max-w-[560px] rounded-lg border bg-background p-5 shadow-lg">
        <h3 className="text-sm font-semibold">{title}</h3>
        <ol className="steps" aria-label="步骤">
          <li className={step === 1 ? 'on' : step > 1 ? 'done' : ''}>① 选机器与 agent</li>
          <li className={step === 2 ? 'on' : step > 2 ? 'done' : ''}>② 在该机登录</li>
          <li className={step === 3 ? 'on' : ''}>③ 识别</li>
        </ol>

        {step === 1 && <>
          <div className="grid gap-x-3 gap-y-2.5 sm:grid-cols-2">
            <div><label htmlFor="lg-machine" className={LABEL}>机器</label>
              <select id="lg-machine" aria-label="机器" className={INPUT} value={machine} onChange={(e) => { setMachine(e.target.value); setDir(defaultDir(agent)) }}>
                {machines.length === 0 && <option value="">（没有可用机器）</option>}
                {machines.map((m) => <option key={m} value={m}>{m}</option>)}
              </select></div>
            <div><label htmlFor="lg-agent" className={LABEL}>Agent</label>
              <select id="lg-agent" aria-label="Agent" className={INPUT} value={agent} disabled={account !== null} onChange={(e) => { setAgent(e.target.value); setDir(defaultDir(e.target.value)) }}>
                {AGENTS.map((a) => <option key={a} value={a}>{a} · {kindMeta(a === 'opencode' ? 'opencode-go' : a).name}</option>)}
              </select></div>
            <div className="sm:col-span-2"><label htmlFor="lg-dir" className={LABEL}>将新建的 HOME（目录名可改）</label>
              <div className="home-in"><span>~/.handoff/home/</span><input id="lg-dir" aria-label="HOME 目录名" className={`${INPUT} font-mono`} spellCheck={false} value={dir} onChange={(e) => setDir(e.target.value)} /></div>
              <p className="field-hint">将在 {machine || '…'} 上新建 <span className="mono">~/.handoff/home/{dir || '…'}</span>，登录只写进这个目录，不动该机主 HOME。</p></div>
          </div>
          <p className="hint">主 HOME（<span className="mono">~</span>）里已有的登录不用在这里加——定时发现会自动认出并挂上。</p>
          {account && <p className="hint">这是给 {account.name} 再加一条登录；agent 由账号类型决定。</p>}
          {error && <p role="alert" className="break-words text-xs text-destructive">{error}</p>}
          <div className="flex justify-end gap-2">
            <button type="button" className="rounded-md border px-3 py-1.5 text-xs" onClick={close}>取消</button>
            <button type="button" className="rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground" disabled={makeDisabled} onClick={() => void make()}>{busy ? '建目录中…' : '建目录并给出登录命令'}</button>
          </div>
        </>}

        {step === 2 && created && <>
          <p className="text-xs">已在 {machine} 建好 <span className="mono">{created.home}</span>。{created.command.ssh ? `在 ${machine} 上执行：` : '在本机终端执行：'}</p>
          <div className="cmd"><pre>{created.command.ssh && <>{created.command.ssh} 后执行：<br /></>}{remoteLogin && created.command.remote_run ? created.command.remote_run : created.command.run}</pre><button type="button" className="tbtn copy" onClick={() => void copy()}>复制</button></div>
          {created.command.remote_run && (
            <label className="field-hint flex items-center gap-1.5">
              <input type="checkbox" checked={remoteLogin} onChange={(e) => setRemoteLogin(e.target.checked)} />
              远端登录（该机没有浏览器）
            </label>
          )}
          {created.command.note && <p className="field-hint">{created.command.note}</p>}
          <div className="wait"><span className="spin" /><span>等待登录… 每 5 秒检查一次</span></div>
          <p className="hint">关掉也没关系，定时发现会自动挂上。</p>
          {checkNote && <p className="hint">{checkNote}</p>}
          {error && <p role="alert" className="break-words text-xs text-destructive">{error}</p>}
          <div className="flex justify-end gap-2">
            <button type="button" className="rounded-md border px-3 py-1.5 text-xs" onClick={() => { stopPoll(); setStep(1) }}>上一步</button>
            <span className="flex-1" />
            <button type="button" className="rounded-md border px-3 py-1.5 text-xs" onClick={close}>关闭</button>
            <button type="button" className="rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground" disabled={checking} onClick={() => void check(true)}>{checking ? '检查中…' : '我已登录，立即检查'}</button>
          </div>
        </>}

        {step === 3 && <>
          {items.map((item) => (
            <div className="found" key={item.login}>
              <div><span className="q-mute">✓</span> 已识别新登录</div>
              <div>→ {item.new_account
                ? <>新账号 <b>{item.account_name}</b><span className="q-mute">（没拿到邮箱时按「类型-短 id」起名，可改名）</span></>
                : <>已挂到账号 <b>{item.account_name}</b><span className="q-mute">（已有 {item.account_logins} 条登录）</span></>}</div>
              <div className="q-mute">新登录：{machine} · <span className="mono">{created ? homeText(created.home) : ''}</span></div>
            </div>
          ))}
          <p className="hint">{items.some((i) => !i.new_account)
            ? '额度、阈值沿用已有账号；设置里这台机器上的载体现在可以选这条登录。'
            : '额度读数等首次探测；阈值用默认，可在账号卡上展开修改。'}</p>
          <div className="flex justify-end gap-2">
            <button type="button" className="rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground" onClick={() => { onDone(); onClose() }}>完成</button>
          </div>
        </>}
      </div>
    </div>
  )
}

