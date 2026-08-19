// DetailPanel 占位：Task 11 替换为真实的节点详情与源码窗口。
import type { CgView } from './graphmath'

interface DetailPanelProps {
  project: string; view: CgView; nodeId: string; stale: Set<string>; onJump: (id: string) => void
}

export function DetailPanel(props: DetailPanelProps) {
  return <aside data-node={props.nodeId} className="w-[340px] shrink-0 border-l p-3.5">待实现</aside>
}
