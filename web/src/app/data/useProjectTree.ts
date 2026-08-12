// useProjectTree —— 项目树：30s + 写操作后 refresh() 立即失效重拉。
// 为什么不进 2.5s 热路径：这个接口带 git worktree list 现场探测，
// 每 2.5s 对所有 location 探一遍纯属浪费；而结构（项目/机器/目录）变化极慢，
// 所有运行态都来自任务流，慢刷不影响体感（spec §6）。
import { usePoll } from './usePoll'
import { fetchProjectTree } from '../../api/client'
import type { ProjectTreeResp } from '../../api/types'

const TREE_INTERVAL = 30000
export function useProjectTree(): ReturnType<typeof usePoll<ProjectTreeResp>> {
  return usePoll(() => fetchProjectTree('all'), TREE_INTERVAL)
}
