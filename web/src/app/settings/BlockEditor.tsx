// BlockEditor —— 设置页里「一个纯文本文件的正文编辑器」（B158 从 B157 抽出）。
//
// 职责：正文 textarea + 保存 / 只读态的「以此为模板新建」 + 错误与冲突提示。
//
// 由「执行纪律」与「Env 文件」两个分区共用。抽取时唯一的行为改动：冲突态
// 从「按错误文案字符串相等判定」改成显式的 conflict 布尔——文案是给人看的，
// 拿它当控制流，改一个字就会静默失去「重新加载」按钮。
//
// 边界：
//   - **不发请求**：读、写、冲突判定全在调用方，本组件只是受控展示
//   - 不认识 env 或纪律块的语义：aria-label 与底部提示都由调用方给
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export interface BlockEditorProps {
  /** 标题（文件名或「内置 <tier>」） */
  title: string
  /** textarea 的 aria-label，各分区自定（如「env 文件正文」） */
  ariaLabel: string
  content: string
  readOnly: boolean
  loading?: boolean
  onChange?: (value: string) => void
  onSave?: () => void
  /** 只读态的主按钮；给了 templateLabel 才渲染 */
  onTemplate?: () => void
  templateLabel?: string
  saving?: boolean
  error?: string
  /** 冲突态（HTTP 409）。为真才显示「重新加载」——不看 error 的文案 */
  conflict?: boolean
  notice?: string
  onReload?: () => void
  /** 磁盘上的字节数，显示在底部提示里 */
  size?: number
  /** 底部提示的前半句，各分区自定 */
  footerHint?: string
  /** 大小上限提示里的字节数，默认 64 KiB */
  maxLabel?: string
}

// BlockEditor 渲染一个受控的纯文本正文编辑器。行为见 BlockEditorProps 各字段。
export function BlockEditor({
  title, ariaLabel, content, readOnly, loading = false, onChange, onSave, onTemplate,
  templateLabel, saving = false, error = '', conflict = false, notice = '', onReload, size,
  footerHint = '保存后下一个任务即生效（正在跑的任务不受影响）', maxLabel = '64 KiB',
}: BlockEditorProps) {
  return (
    <>
      <div className="flex items-center justify-between gap-2">
        <div>
          <h3 className="text-sm font-semibold">{title}</h3>
          {readOnly && <span className="text-[11px] text-muted-foreground">只读</span>}
        </div>
        {readOnly ? (
          templateLabel !== undefined && <Button size="sm" onClick={onTemplate}>{templateLabel}</Button>
        ) : (
          <Button size="sm" onClick={onSave} disabled={saving || loading}>保存</Button>
        )}
      </div>
      <textarea
        aria-label={ariaLabel}
        value={content}
        readOnly={readOnly}
        disabled={loading}
        onChange={(event) => onChange?.(event.target.value)}
        className={cn(
          'mt-3 min-h-[28rem] w-full resize-y rounded-md border p-3 font-mono text-xs leading-5 outline-none focus-visible:ring-1 focus-visible:ring-ring',
          readOnly ? 'bg-muted/50' : 'bg-background',
        )}
      />
      {!readOnly && (
        <p className="mt-2 text-[11px] text-muted-foreground">
          {footerHint}；上限 {maxLabel}{size !== undefined && `；当前 ${size} 字节`}
        </p>
      )}
      {error && (
        <div role="alert" className="mt-2 flex flex-wrap items-center gap-2 text-xs text-destructive">
          <span>{error}</span>
          {conflict && <Button type="button" variant="outline" size="sm" onClick={onReload}>重新加载</Button>}
        </div>
      )}
      {notice && <p className="mt-2 text-xs text-emerald-700">{notice}</p>}
    </>
  )
}
