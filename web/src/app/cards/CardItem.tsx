import { cn } from '@/lib/utils'
import type { ReactNode } from 'react'
import type { CardView } from '../../api/ledger'
import { needsAttention } from './columns'

export interface CardItemProps {
  card: CardView
  onOpen: (focus?: 'merge') => void
  mergedCount?: number
  verified?: boolean
}

function Chip({ children, className, title, onClick }: {
  children: ReactNode
  className?: string
  title?: string
  onClick?: () => void
}) {
  if (onClick) {
    return (
      <button type="button" title={title} onClick={onClick} className={cn('rounded-full border px-1.5 text-[10px]', className)}>
        {children}
      </button>
    )
  }
  return <span title={title} className={cn('rounded-full border px-1.5 text-[10px]', className)}>{children}</span>
}

export function CardItem({ card, onOpen, mergedCount = card.merged_count, verified }: CardItemProps) {
  const needs = needsAttention(card)
  const attachments = card.attachments ?? []
  const blockedBy = card.blocked_by ?? []
  return (
    <article
      role="button"
      tabIndex={0}
      onClick={() => onOpen()}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault()
          onOpen()
        }
      }}
      className={cn(
        'flex cursor-pointer flex-col gap-1.5 rounded-lg border bg-background p-2.5 text-left text-xs shadow-sm transition-colors hover:border-foreground/40 hover:bg-accent/30',
        needs && 'border-amber-400 border-l-2',
        card.conflict && 'border-destructive border-l-2',
        card.blocked && 'opacity-60',
      )}
    >
      <div className="flex items-center gap-2">
        <span className="font-mono text-[11px] text-muted-foreground">{card.id}</span>
        <span className="ml-auto truncate text-[10px] text-muted-foreground">{card.status}</span>
      </div>
      <h3 className="line-clamp-2 leading-5">{card.title}</h3>
      <div className="flex flex-wrap items-center gap-1.5">
        <Chip className={card.priority === '高' ? 'border-destructive/40 text-destructive' : 'text-muted-foreground'}>
          {card.priority || '—'}
        </Chip>
        {attachments.filter((attachment) => attachment.kind === 'spec').map((attachment) => (
          <Chip key={attachment.path} title={attachment.path} className="text-foreground">▤ spec</Chip>
        ))}
        {mergedCount > 0 && (
          <Chip onClick={() => onOpen('merge')} className="text-foreground">⊕ 并入 {mergedCount}</Chip>
        )}
        {card.open_decisions > 0 && <Chip className="border-amber-300 bg-amber-50 text-amber-700">⚖ 裁决 {card.open_decisions}</Chip>}
        {card.open_tickets > 0 && <Chip className="border-amber-300 bg-amber-50 text-amber-700">🄠 工单 {card.open_tickets}</Chip>}
        {verified !== undefined && <Chip className="border-green-300 bg-green-50 text-green-700">{verified ? '已验' : '待真机验'}</Chip>}
        {blockedBy.length > 0 && <Chip className="text-muted-foreground">⛓ {blockedBy.join(', ')}</Chip>}
        {card.needs && <Chip className="border-amber-300 bg-amber-50 text-amber-700">⚑ {card.needs}</Chip>}
        {card.conflict && <Chip className="border-destructive/40 bg-destructive/5 text-destructive">✕ 状态冲突</Chip>}
        {card.base_branch && <Chip className="font-mono text-muted-foreground">⎇ {card.base_branch}</Chip>}
        {card.project && <Chip className="text-muted-foreground">{card.project}</Chip>}
      </div>
    </article>
  )
}
