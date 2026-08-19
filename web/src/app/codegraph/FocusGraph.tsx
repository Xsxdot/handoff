// FocusGraph 占位：Task 10 替换为真实的竖向焦点子图。
import type { CgView } from './graphmath'

interface FocusGraphProps {
  view: CgView; foci: string[]; depth: number; staleIds: Set<string>
  onDepth: (d: number) => void
  onFocus: (id: string, additive: boolean) => void
  onSelect: (id: string) => void
  canBack: boolean; canFwd: boolean; onBack: () => void; onFwd: () => void
}

export function FocusGraph(props: FocusGraphProps) {
  return <div className="min-w-0 flex-1" data-depth={props.depth}>待实现</div>
}
