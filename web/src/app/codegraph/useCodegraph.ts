// useCodegraph —— 按项目一次性取代码图（基线 + 视图 + 保鲜报告）。
// 不轮询：图数据只随扫描/合并变化，页内提供手动刷新即可。
import { useCallback, useEffect, useState } from 'react'
import { fetchCodegraph } from '../../api/client'
import type { CodegraphResp } from '../../api/types'

export function useCodegraph(project: string) {
  const [data, setData] = useState<CodegraphResp | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const reload = useCallback(() => {
    if (!project) return
    setLoading(true)
    setError('')
    fetchCodegraph(project)
      .then(setData)
      .catch((e: Error) => { setData(null); setError(e.message) })
      .finally(() => setLoading(false))
  }, [project])
  useEffect(reload, [reload])
  return { data, error, loading, reload }
}
