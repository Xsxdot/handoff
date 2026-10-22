// MobileAccounts.tsx —— 移动端设置中心「账号与额度」一节（B453 spec S10，形态基准
// prototypes/b453-accounts/pages/mobile-settings.html）。
//
// 职责：样式同底栏弹层「详细」——按类型分组、组内问题账号排前、每账号两行（首行注记，
// 次行全部窗口横排，agy 按模型组分行）；点一行展开各窗口重置、失败原因与「立即探测」。
// 边界：只读 + 立即探测；登记/编辑/删除留在桌面账号页（要填路径与确认，手机上不提供）。
// 数据读 Shell 级共享快照；快照为 null（账本未启用/未拉到）时整节不渲染。
import { useState } from 'react'
import type { AccountView } from '../../api/accounts'
import { errorMessage } from '../lib/format'
import { byProblem, KIND_ORDER, kindMeta, updatedText } from './model'
import { DetailNote, KindIcon, WinsCompact, WinsInline } from './QuotaBits'
import { useAccounts, useNow } from './store'
import './accounts.css'

function Row({ a, open, now, onToggle }: { a: AccountView; open: boolean; now: number; onToggle: () => void }) {
  const { probe } = useAccounts()
  const [probing, setProbing] = useState(false)
  const [error, setError] = useState('')
  const run = async () => {
    setProbing(true)
    setError('')
    try { await probe(a.name) } catch (cause) { setError(errorMessage(cause)) } finally { setProbing(false) }
  }
  const hasWins = (a.reading?.windows.length ?? 0) > 0
  return (
    <div className={`m-acct-row${open ? ' open' : ''}`} data-testid={`m-acct-${a.name}`}>
      <button type="button" className="l1" aria-expanded={open} onClick={onToggle}>
        <span className="r1"><span className="nm">{a.name}</span><span className="mc">{a.machine}</span><span className="val"><DetailNote a={a} now={now} /></span><span className="chev">›</span></span>
        {hasWins && <span className="r2"><WinsInline a={a} /></span>}
      </button>
      {open && (
        <div className="more">
          {hasWins && <WinsCompact a={a} now={now} />}
          {a.state === 'none' && <div className="anote">尚未探测</div>}
          {a.last_error && a.state !== 'none' && <div className="anote">△ {a.last_error.message}{a.state === 'fail' && a.reading && <><br />显示的是旧读数</>}</div>}
          <div className="l3">
            <span className="grow">{kindMeta(a.kind).name}{a.reading?.plan_type ? ` · ${a.reading.plan_type}` : ''} · {updatedText(a, now)}</span>
            <button type="button" className="probe-btn" disabled={probing} onClick={() => void run()}>{probing ? '探测中…' : '立即探测'}</button>
          </div>
          {error && <div role="alert" className="anote">探测失败：{error}</div>}
        </div>
      )}
    </div>
  )
}

/** MobileAccountsSection 设置中心首页的一节；sectionClass 是 SettingsHub 的小节标题样式。 */
export function MobileAccountsSection({ sectionClass }: { sectionClass: string }) {
  const { accounts } = useAccounts()
  const now = useNow()
  const [openRows, setOpenRows] = useState<Set<string>>(() => new Set())
  if (accounts === null) return null
  const machines = new Set(accounts.map((a) => a.machine)).size
  const kinds = [...KIND_ORDER, ...new Set(accounts.map((a) => a.kind).filter((k) => !KIND_ORDER.includes(k)))]
  const toggle = (name: string) => setOpenRows((cur) => {
    const next = new Set(cur)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    return next
  })
  return (
    <div className="acct-scope">
      <h3 data-testid="settings-section-accounts" className={sectionClass}>账号与额度<span className="ml-2 text-xs font-normal">{accounts.length} 个 · {machines} 台机器</span></h3>
      <div className="m-acct-card" data-testid="mobile-accounts">
        {accounts.length === 0 && <p className="px-4 py-3 text-xs text-muted-foreground">还没有登记账号。</p>}
        {kinds.map((kind) => {
          const group = byProblem(accounts.filter((a) => a.kind === kind))
          if (group.length === 0) return null
          return [
            <div key={`g-${kind}`} className="m-acct-group"><KindIcon kind={kind} />{kindMeta(kind).name}</div>,
            ...group.map((a) => <Row key={a.name} a={a} now={now} open={openRows.has(a.name)} onToggle={() => toggle(a.name)} />),
          ]
        })}
      </div>
      <p className="mt-1.5 text-xs leading-5 text-muted-foreground">登记、编辑和删除账号请到桌面控制台的「账号」页。</p>
    </div>
  )
}
