// SeqView 占位：Task 11 替换为真实的时序图视角。
import type { CgView } from './graphmath'

interface SeqViewProps {
  view: CgView; entry: string; onSelect: (id: string) => void
}

export function SeqView(props: SeqViewProps) {
  return <div data-entry={props.entry} className="min-w-0 flex-1">待实现</div>
}
