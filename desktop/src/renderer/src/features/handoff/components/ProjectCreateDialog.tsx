/**
 * Handoff ProjectCreateDialog：项目创建表单。
 *
 * 职责：
 *   - local/remote 至少一个、remote 单选
 *   - Finder 按钮只在 local existing path 出现；remote existing path 只能粘贴
 *   - clone URL 必填；clone path 自动预填 ~/.handoff/<repo-name> 并可改
 *   - 提交后显示 Operation 状态而非伪造「已创建」
 *   - 一次「提交意图」对应一个稳定 operation_id；失败可重试且复用同一 id
 *
 * 边界：
 *   - 使用 shadcn Dialog/Select/Input/Button
 *   - Finder 只返回选择结果，最终由 agentd InspectPath 校验
 */
import { useEffect, useMemo, useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue
} from '@/components/ui/select'
import type { Operation } from '../../../../../shared/handoff/contracts'

export type CreateDialogMachine = {
  id: string
  display_name: string
  kind: string
}

export type ProjectCreateDialogProps = {
  machines: CreateDialogMachine[]
  onSubmit: (command: unknown) => Promise<Operation>
  onOpenChange: (open: boolean) => void
  open: boolean
}

type LocationForm = {
  machineId: string
  role: 'local' | 'remote'
  source: 'existing_path' | 'git_clone'
  path: string
  gitUrl: string
  clonePath: string
}

function emptyLocal(): LocationForm {
  return { machineId: '', role: 'local', source: 'existing_path', path: '', gitUrl: '', clonePath: '' }
}

function emptyRemote(): LocationForm {
  return { machineId: '', role: 'remote', source: 'existing_path', path: '', gitUrl: '', clonePath: '' }
}

/** 从 Git URL 提取仓库名（用于默认 clone path）。 */
function repoNameFromUrl(url: string): string {
  const cleaned = url.replace(/\.git$/, '').replace(/\/$/, '')
  const parts = cleaned.split('/')
  return parts.at(-1) ?? ''
}

/** 项目创建对话框。 */
export function ProjectCreateDialog({
  machines,
  onSubmit,
  onOpenChange,
  open
}: ProjectCreateDialogProps): React.JSX.Element {
  const localMachines = machines.filter((m) => m.kind === 'local')
  const remoteMachines = machines.filter((m) => m.kind === 'remote')
  const [localLoc, setLocalLoc] = useState<LocationForm>(emptyLocal)
  const [remoteLoc, setRemoteLoc] = useState<LocationForm>(emptyRemote)
  const [useLocal] = useState(true)
  const [useRemote] = useState(false)
  const [name, setName] = useState('')
  const [submitted, setSubmitted] = useState<Operation | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // 稳定 operation_id：一次「提交意图」对应一个 id。
  //
  // 为什么必须存进 state 而非每次 submit 现生成（Task 5/7 的 durable operation
  // 机制前提）：失败后重试若换新 id，服务端会当成全新 operation，对第一次可能
  // 已建了一半的目录再来一遍——「同 ID 重试只补失败目标」永远走不到。因此
  // operation_id 在首次 submit 时生成并固定，重试复用；只有成功提交后关闭
  // 对话框（下次打开 = 新提交意图）才换新 id。
  const [operationId, setOperationId] = useState<string | null>(null)

  // 对话框每次打开 = 新的提交意图：重置表单/结果/错误/operation_id。
  // 为什么用 ref 记录上一次 open：open 从 false→true 才重置；保持打开期间
  // 的编辑与重试状态不受影响。
  const prevOpen = useRef(open)
  useEffect(() => {
    if (open && !prevOpen.current) {
      setLocalLoc(emptyLocal())
      setRemoteLoc(emptyRemote())
      setName('')
      setSubmitted(null)
      setError(null)
      setOperationId(null)
    }
    prevOpen.current = open
  }, [open])

  const canSubmit = useMemo(() => {
    const hasAny = useLocal || useRemote
    if (!hasAny || !name.trim()) {
      return false
    }
    const locs: LocationForm[] = []
    if (useLocal) {
      locs.push(localLoc)
    }
    if (useRemote) {
      locs.push(remoteLoc)
    }
    return locs.every((l) => {
      if (!l.machineId) {
        return false
      }
      if (l.source === 'git_clone') {
        return l.gitUrl.trim().length > 0
      }
      return l.path.trim().length > 0
    })
  }, [useLocal, useRemote, localLoc, remoteLoc, name])

  const pickLocalDirectory = async (): Promise<void> => {
    const api = (window as unknown as {
      handoff?: { pickLocalDirectory: () => Promise<{ canceled: boolean; path?: string }> }
    }).handoff
    if (!api) {
      return
    }
    const result = await api.pickLocalDirectory()
    if (!result.canceled && result.path) {
      setLocalLoc((prev) => ({ ...prev, path: result.path ?? '' }))
    }
  }

  const submit = async (): Promise<void> => {
    if (!canSubmit || submitting) {
      return
    }
    // 稳定 operation_id：首次 submit 生成并固定，重试复用（见字段 why 注释）。
    const id = operationId ?? crypto.randomUUID()
    if (operationId === null) {
      setOperationId(id)
    }
    setSubmitting(true)
    setError(null)
    try {
      const locations: {
        machine_id: string
        role: string
        source: string
        path?: string
        git_url?: string
        clone_path?: string
      }[] = []
      if (useLocal) {
        const l = localLoc
        locations.push(
          l.source === 'git_clone'
            ? {
                machine_id: l.machineId,
                role: 'local',
                source: 'git_clone',
                git_url: l.gitUrl,
                clone_path: l.clonePath || `~/.handoff/${repoNameFromUrl(l.gitUrl)}`
              }
            : { machine_id: l.machineId, role: 'local', source: 'existing_path', path: l.path }
        )
      }
      if (useRemote) {
        const l = remoteLoc
        locations.push(
          l.source === 'git_clone'
            ? {
                machine_id: l.machineId,
                role: 'remote',
                source: 'git_clone',
                git_url: l.gitUrl,
                clone_path: l.clonePath || `~/.handoff/${repoNameFromUrl(l.gitUrl)}`
              }
            : { machine_id: l.machineId, role: 'remote', source: 'existing_path', path: l.path }
        )
      }
      const operation = await onSubmit({
        operation_id: id,
        name,
        locations
      })
      setSubmitted(operation)
    } catch (e) {
      // 异步失败：显示可行动错误信息，保留重试入口；重试复用同一 operation_id。
      setError(e instanceof Error ? e.message : '项目创建失败，请重试')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建项目</DialogTitle>
          <DialogDescription>选择本机与远端目录（至少一个）</DialogDescription>
        </DialogHeader>

        <div className="handoff-create-name">
          <Label htmlFor="handoff-project-name">项目名</Label>
          <Input
            id="handoff-project-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="super-debug"
          />
        </div>

        <div className="handoff-create-location">
          <Label>本机目录</Label>
          <Select
            value={localLoc.source}
            onValueChange={(v) => setLocalLoc((prev) => ({ ...prev, source: v as LocationForm['source'] }))}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="existing_path">已有目录</SelectItem>
              <SelectItem value="git_clone">Git clone</SelectItem>
            </SelectContent>
          </Select>
          {localLoc.source === 'existing_path' ? (
            <div className="handoff-create-path-row">
              <Input
                value={localLoc.path}
                onChange={(e) => setLocalLoc((prev) => ({ ...prev, path: e.target.value }))}
                placeholder="/Users/me/repo"
              />
              <Button type="button" onClick={() => void pickLocalDirectory()}>
                选择目录…
              </Button>
            </div>
          ) : (
            <div className="handoff-create-clone">
              <Input
                value={localLoc.gitUrl}
                onChange={(e) => setLocalLoc((prev) => ({ ...prev, gitUrl: e.target.value }))}
                placeholder="git@github.com:o/r.git"
              />
              <Input
                value={localLoc.clonePath}
                onChange={(e) => setLocalLoc((prev) => ({ ...prev, clonePath: e.target.value }))}
                placeholder="~/.handoff/<repo-name>"
              />
            </div>
          )}
          {localMachines.length > 0 && (
            <Select
              value={localLoc.machineId}
              onValueChange={(v) => setLocalLoc((prev) => ({ ...prev, machineId: v }))}
            >
              <SelectTrigger>
                <SelectValue placeholder="选择本机" />
              </SelectTrigger>
              <SelectContent>
                {localMachines.map((m) => (
                  <SelectItem key={m.id} value={m.id}>
                    {m.display_name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>

        {remoteMachines.length > 0 && (
          <div className="handoff-create-location">
            <Label>远端目录</Label>
            <Select
              value={remoteLoc.source}
              onValueChange={(v) => setRemoteLoc((prev) => ({ ...prev, source: v as LocationForm['source'] }))}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="existing_path">已有目录</SelectItem>
                <SelectItem value="git_clone">Git clone</SelectItem>
              </SelectContent>
            </Select>
            {remoteLoc.source === 'existing_path' ? (
              // 远端已有路径只收绝对 path，无 Finder
              <Input
                value={remoteLoc.path}
                onChange={(e) => setRemoteLoc((prev) => ({ ...prev, path: e.target.value }))}
                placeholder="/absolute/remote/path"
              />
            ) : (
              <div className="handoff-create-clone">
                <Input
                  value={remoteLoc.gitUrl}
                  onChange={(e) => setRemoteLoc((prev) => ({ ...prev, gitUrl: e.target.value }))}
                  placeholder="git@github.com:o/r.git"
                />
                <Input
                  value={remoteLoc.clonePath}
                  onChange={(e) => setRemoteLoc((prev) => ({ ...prev, clonePath: e.target.value }))}
                  placeholder="~/.handoff/<repo-name>"
                />
              </div>
            )}
            <Select
              value={remoteLoc.machineId}
              onValueChange={(v) => setRemoteLoc((prev) => ({ ...prev, machineId: v }))}
            >
              <SelectTrigger>
                <SelectValue placeholder="选择远端开发机" />
              </SelectTrigger>
              <SelectContent>
                {remoteMachines.map((m) => (
                  <SelectItem key={m.id} value={m.id}>
                    {m.display_name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}

        {error && (
          <div className="handoff-create-error" data-testid="handoff-create-error" role="alert">
            <span>{error}</span>
            <Button type="button" variant="outline" size="sm" onClick={() => void submit()}>
              重试
            </Button>
          </div>
        )}

        {submitted && (
          <div className="handoff-create-result" data-testid="handoff-operation-result">
            操作状态：{submitted.state}
          </div>
        )}

        <DialogFooter>
          <Button type="button" onClick={() => void submit()} disabled={!canSubmit || submitting}>
            {submitting ? '提交中…' : submitted ? '再次创建' : '创建项目'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
