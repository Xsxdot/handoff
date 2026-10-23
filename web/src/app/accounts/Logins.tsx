// Logins.tsx —— 账号卡里的登录子表（B464 S3/S4）：机器、HOME、凭据、状态、代理、载体与动作。
//
// 职责：逐条列出账号名下的登录；行内编辑代理（PUT /api/logins/{id}/proxy）、复制服务端给的
// 重新登录命令；已过期/已失效的登录标红并给行动提示。
// 边界：数据来自 AccountView.logins；写回执经 onLogin（store.applyLogin）合入，不在本地造状态；
// 凭据值与原始身份永不出现在这里，只有服务端给的来源名与短指纹。
import { useState } from 'react'
import { putLoginProxy, type LoginView } from '../../api/accounts'
import { copyToClipboard } from '../lib/clipboard'
import { errorMessage } from '../lib/format'
import { homeText, loginCommandText, loginCredText, loginStateHint, loginStateLabel } from './model'

const INPUT = 'h-7 w-full rounded-md border bg-background px-2 font-mono text-[11px]'

/** LoginState 登录状态单元格：正常弱前景；已过期/已失效红字 + 行动提示。 */
function LoginState({ login }: { login: LoginView }) {
  if (login.state === 'ok') return <span className="q-mute">正常</span>
  return (
    <span className="q-out">
      {loginStateLabel(login.state)}
      <span className="lt-hint">{loginStateHint(login.state)}</span>
    </span>
  )
}

/** Logins 账号的一条登录子表；空登录给「在其他机器登录」引导。 */
export function Logins({ kind, logins, onLogin }: {
  kind: string
  logins: LoginView[]
  onLogin: (login: LoginView) => void
}) {
  const [editing, setEditing] = useState('')
  const [proxy, setProxy] = useState('')
  const [saving, setSaving] = useState('')
  const [note, setNote] = useState('')
  const [error, setError] = useState('')

  const startProxy = (l: LoginView) => { setEditing(l.id); setProxy(l.proxy ?? ''); setError(''); setNote('') }
  const saveProxy = async (l: LoginView) => {
    setSaving(l.id)
    setError('')
    setNote('')
    console.info('accounts.login.proxy.start', { login: l.id, machine: l.machine })
    try {
      const view = await putLoginProxy(l.id, proxy.trim())
      console.info('accounts.login.proxy.success', { login: l.id, has_proxy: proxy.trim() !== '' })
      onLogin(view)
      setEditing('')
      setNote(`已保存代理：${view.machine} · ${homeText(view.home)} 这条登录 → ${view.proxy ?? '随该机环境'}。只影响这条登录上的探测与派发。`)
    } catch (cause) {
      console.error('accounts.login.proxy.failure', { login: l.id, cause })
      setError(errorMessage(cause))
    } finally {
      setSaving('')
    }
  }
  const copyCommand = async (l: LoginView) => {
    const text = loginCommandText(l.relogin)
    if (!text) { setError('这条登录没有可用的重新登录命令（服务端未下发 relogin）'); return }
    const ok = await copyToClipboard(text)
    console.info('accounts.login.copy_command', { login: l.id, copied: ok })
    setError('')
    setNote(ok ? `已复制登录命令：${text} —— 在该机重新登录后，下次发现会更新这条登录的状态。`
      : `请手动复制：${text} —— 在该机重新登录后，下次发现会更新这条登录的状态。`)
  }
  const copyRemoteCommand = async (l: LoginView) => {
    const text = loginCommandText(l.relogin, 'remote_run')
    if (!text) { setError('这条登录没有远端登录命令（服务端未下发 relogin.remote_run）'); return }
    const ok = await copyToClipboard(text)
    console.info('accounts.login.copy_remote_command', { login: l.id, copied: ok })
    setError('')
    setNote(ok ? `已复制远端登录命令：${text} —— 在该机重新登录后，下次发现会更新这条登录的状态。`
      : `请手动复制：${text} —— 在该机重新登录后，下次发现会更新这条登录的状态。`)
  }

  if (logins.length === 0) return <div className="lg-note">还没有登录。点「在其他机器登录」加一条。</div>
  return (
    <div className="lgs">
      <div className="lgs-t">登录 · {logins.length} 条</div>
      <div className="lt-wrap">
        <table className="lt">
          <thead><tr><th>机器</th><th>HOME</th><th>凭据</th><th>状态</th><th>代理</th><th>载体</th><th /></tr></thead>
          <tbody>
            {logins.map((l) => (
              <tr key={l.id} data-testid={`login-${l.id}`}>
                <td className="nw">{l.machine}</td>
                <td className="mono">{homeText(l.home)}</td>
                <td>{l.source}<div className="sub mono">{l.fingerprint} · {loginCredText(l, kind, Date.now())}</div></td>
                <td><LoginState login={l} /></td>
                <td>
                  {editing === l.id ? (
                    <div className="px-edit">
                      <input aria-label={`代理 ${l.id}`} className={INPUT} placeholder="空 = 随该机环境" value={proxy} onChange={(e) => setProxy(e.target.value)} />
                      <button type="button" className="tbtn" aria-label="保存代理" disabled={saving === l.id} onClick={() => void saveProxy(l)}>{saving === l.id ? '保存中…' : '保存'}</button>
                      <button type="button" className="tbtn" onClick={() => { setEditing(''); setError('') }}>取消</button>
                    </div>
                  ) : l.proxy ? <span className="mono">{l.proxy}</span> : <span className="q-mute">随该机环境</span>}
                </td>
                <td>{l.carriers.length === 0 ? <span className="q-mute">无载体</span> : <LoginCarriers names={l.carriers} />}</td>
                <td className="ops">
                  {editing !== l.id && <button type="button" className="tbtn" aria-label={`编辑代理 ${l.id}`} onClick={() => startProxy(l)}>编辑代理</button>}
                  <button type="button" className="tbtn" onClick={() => void copyCommand(l)}>复制登录命令</button>
                  {l.relogin?.remote_run && <button type="button" className="tbtn" onClick={() => void copyRemoteCommand(l)}>复制远端登录命令</button>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {error && <p role="alert" className="lg-note q-out">{error}</p>}
      {note && <p className="lg-note">{note}</p>}
    </div>
  )
}

/** LoginCarriers 使用这条登录的载体名；多个时显示「共用」。 */
function LoginCarriers({ names }: { names: string[] }) {
  return <span>{names.length > 1 && <span className="q-mute">共用：</span>}{names.map((c, i) => <span key={c}>{i > 0 && '、'}<span className="mono">{c}</span></span>)}</span>
}
