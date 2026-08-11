// StatusSection —— 渲染 GET /api/status 的结果。
//
// 职责：
//   - 展示 agentd 版本（release 版本号，退 revision）、监听地址、数据目录、
//     运行时长、执行器、按状态分组的任务计数与活跃任务探活
//
// 边界：
//   - 纯展示；不做格式化之外的计算
//
// 注意（与 proto/status.go 的注释同义）：
//   - Version 空串 = 非 release 构建，展示应退回 Revision
//   - Revision 空串 = 非 go build 产物，展示「版本未知」而不是空
import { Server } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import type { ActiveTask, BuildInfo, StatusResp } from '../api/types'

// buildLabel 从 BuildInfo 挑出最可展示的版本标识。
//
// 为什么先 Version 后 Revision：Version 回答「该不该更新」，Revision 回答
// 「是哪个提交」；release 构建两者都有，展示 Version 即可，非 release 构建
// 退到 Revision（前 8 位足够人肉对照）。
export function buildLabel(b: BuildInfo): string {
  if (b.version) return b.version
  if (b.revision) return `rev ${b.revision.slice(0, 8)}`
  return '版本未知'
}

// uptime 把 started_at 换算成「1h23m」样式的运行时长；解析失败回「—」。
export function uptime(startedAt: string): string {
  const start = Date.parse(startedAt)
  if (Number.isNaN(start)) return '—'
  const mins = Math.floor((Date.now() - start) / 60000)
  if (mins < 1) return '刚刚启动'
  if (mins < 60) return `${mins}m`
  const h = Math.floor(mins / 60)
  return `${h}h${mins % 60}m`
}

const LIVE_BADGE: Record<ActiveTask['live'], 'default' | 'secondary' | 'destructive'> = {
  alive: 'default',
  dead: 'destructive',
  unknown: 'secondary',
}

export function StatusSection({ status }: { status: StatusResp }) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-2 pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Server className="size-4" />
          agentd 状态
        </CardTitle>
        <Badge variant="outline">{buildLabel(status.version)}</Badge>
      </CardHeader>
      <CardContent className="grid gap-3 text-sm sm:grid-cols-2">
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
          <dt className="text-muted-foreground">监听</dt>
          <dd className="font-mono">{status.listen}</dd>
          <dt className="text-muted-foreground">数据目录</dt>
          <dd className="font-mono break-all">{status.data_dir}</dd>
          <dt className="text-muted-foreground">运行时长</dt>
          <dd>{uptime(status.started_at)}</dd>
          <dt className="text-muted-foreground">默认执行器</dt>
          <dd>{status.default_executor || '（未设置）'}</dd>
          <dt className="text-muted-foreground">已装执行器</dt>
          <dd>{status.executors.length > 0 ? status.executors.join(', ') : '（无）'}</dd>
        </dl>
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap gap-1.5">
            {Object.entries(status.task_counts).map(([state, count]) => (
              <Badge key={state} variant={count > 0 ? 'default' : 'secondary'}>
                {state}: {count}
              </Badge>
            ))}
          </div>
          {status.active.length > 0 && (
            <div className="flex flex-col gap-1">
              <p className="text-xs text-muted-foreground">活跃任务探活：</p>
              {status.active.map((a) => (
                <div key={a.id} className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-xs">{a.id.slice(0, 8)}</span>
                  <span className="truncate">{a.name}</span>
                  <Badge variant={LIVE_BADGE[a.live]}>{a.live}</Badge>
                </div>
              ))}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
