// TaskSection —— 渲染 GET /api/tasks 的结果。
//
// 职责：
//   - 展示任务列表（至少 id + state；附带 name / branch / updated_at 便于人肉定位）
//
// 边界：
//   - 只读列表；不做任务详情、不做看板交互——那是后续任务
//   - 空列表明确显示「暂无任务」而不是空页面
import { ListTodo } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import type { Task } from '../api/types'

// shortID 取任务 UUID 前 8 位，与人肉对照 handoff-<id8> 的惯例一致。
//
// 注意：只是展示；任何拿去当参数的地方都必须用完整 ID（store 是精确匹配）
function shortID(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id
}

export function TaskSection({ tasks }: { tasks: Task[] }) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-center gap-2 pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <ListTodo className="size-4" />
          任务列表
        </CardTitle>
        <Badge variant="secondary">{tasks.length}</Badge>
      </CardHeader>
      <CardContent>
        {tasks.length === 0 ? (
          <p className="text-sm text-muted-foreground">暂无任务</p>
        ) : (
          <ul className="flex flex-col divide-y">
            {tasks.map((t) => (
              <li key={t.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
                <span className="font-mono text-xs text-muted-foreground">{shortID(t.id)}</span>
                <Badge variant={t.state === 'running' ? 'default' : 'secondary'}>{t.state}</Badge>
                <span className="min-w-0 flex-1 truncate text-sm">{t.name || t.plan_summary || '（无名称）'}</span>
                <span className="font-mono text-xs text-muted-foreground">{t.branch}</span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
