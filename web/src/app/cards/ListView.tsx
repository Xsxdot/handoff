import type { CardView } from '../../api/ledger'

export function ListView({
  cards,
  includeArchived,
  onIncludeArchivedChange,
  onOpen,
}: {
  cards: CardView[]
  includeArchived: boolean
  onIncludeArchivedChange: (value: boolean) => void
  onOpen: (id: string) => void
}) {
  return (
    <div className="min-h-0 flex-1 overflow-auto px-4 py-3">
      <div className="mb-2 flex items-center gap-3 text-xs">
        <label className="flex items-center gap-1.5">
          <input type="checkbox" checked={includeArchived} onChange={(event) => onIncludeArchivedChange(event.target.checked)} />
          含归档（已完成 / 终止）
        </label>
        <span className="text-muted-foreground">列表视图复刻 markdown 总账——领活与考古入口</span>
      </div>
      <table className="w-full border-collapse text-xs">
        <thead>
          <tr className="border-b text-left text-[11px] text-muted-foreground">
            {['ID', '标题', '状态', '协调者', '验收', '优先级', '附件', '备注'].map((label) => <th key={label} className="whitespace-nowrap px-2 py-1.5 font-medium">{label}</th>)}
          </tr>
        </thead>
        <tbody>
          {cards.map((card) => (
            <tr key={card.id} onClick={() => onOpen(card.id)} className="cursor-pointer border-b hover:bg-muted/60">
              <td className="whitespace-nowrap px-2 py-2 font-mono text-[11px] text-muted-foreground">{card.id}</td>
              <td className="max-w-[28rem] px-2 py-2">
                <div className="truncate">{card.title}</div>
                {card.following && <div className="text-[11px] text-muted-foreground">跟随 {card.following}</div>}
              </td>
              <td className="whitespace-nowrap px-2 py-2">{card.status}</td>
              <td className="max-w-[16rem] px-2 py-2">{card.driver_session ? <><div className="truncate font-mono text-[11px]">{card.driver_session}</div><div className="text-[11px] text-muted-foreground">{card.driver_source === 'bind' ? '坐下' : card.driver_source === 'coordinate' ? '叫机器人' : '席位异常'}</div></> : <span className="text-muted-foreground">空座</span>}</td>
              <td className="whitespace-nowrap px-2 py-2 text-muted-foreground">待真机验</td>
              <td className="whitespace-nowrap px-2 py-2">{card.priority || '—'}</td>
              <td className="whitespace-nowrap px-2 py-2 text-muted-foreground">{card.attachments?.length || '—'}</td>
              <td className="px-2 py-2 text-muted-foreground">{card.needs || '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {cards.length === 0 && <p className="py-8 text-center text-sm text-muted-foreground">（空）</p>}
    </div>
  )
}
