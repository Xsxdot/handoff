// useDirEntries —— 按需、单层的目录列举缓存。
//
// 职责：为文件树提供「展开哪一层就取哪一层」的数据；已取过的层留在内存里，
// 折叠再展开不重复请求。
//
// 边界：
//   - **不递归**。递归列举一个大仓库会打出上万条目，而树上同时可见的只有几十行
//   - 不做搜索：搜索框是对已列举内容的前端过滤（spec §4），不发请求。真正的
//     全仓搜索需要后端支持，本期不做
//   - 换基准目录时整份缓存清空——不同工作树的同名相对路径是不同的东西
//
// 失败按层记录：某一层 403/404 只让那一层显示原因，不把整棵树打成错误态。
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { fetchWorkspaceDir } from '../../api/client'
import type { DirEntry } from '../../api/types'
import { errorMessage } from '../lib/format'
import type { BaseDir } from '../workbench/useWorkbench'

export interface DirEntriesApi {
  entriesOf: (rel: string) => DirEntry[] | undefined
  errorOf: (rel: string) => string | undefined
  ensure: (rel: string) => void
  refresh: () => void
  reload: (rel: string) => void
}

export function useDirEntries(base: BaseDir | null): DirEntriesApi {
  const [entries, setEntries] = useState<Record<string, DirEntry[]>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  // loaded 记「这一层已经发过请求」，挡住重复请求：树上一次渲染里 ensure 会被
  // 每个展开的层各调一次，而 entries/errors 要等响应回来才有值
  const loaded = useRef<Set<string>>(new Set())
  // entriesRef/errorsRef 镜像 state：让 entriesOf/errorOf 的引用不随 entries 变化
  // 而变（见返回处的「为什么 useMemo」）——每次渲染同步最新值到 ref
  const entriesRef = useRef<Record<string, DirEntry[]>>({})
  entriesRef.current = entries
  const errorsRef = useRef<Record<string, string>>({})
  errorsRef.current = errors
  const baseKey = base?.key ?? ''

  // 换基准目录 = 换一棵树，旧缓存全部作废
  useEffect(() => {
    setEntries({})
    setErrors({})
    loaded.current = new Set()
  }, [baseKey])

  const load = useCallback(
    (rel: string) => {
      if (!base) return
      if (loaded.current.has(rel)) return
      loaded.current.add(rel)
      fetchWorkspaceDir(base.path, rel || undefined, base.machine || undefined)
        .then((r) => {
          setEntries((prev) => ({ ...prev, [rel]: r.entries }))
          setErrors((prev) => {
            const next = { ...prev }
            delete next[rel]
            return next
          })
        })
        .catch((err) => {
          setErrors((prev) => ({ ...prev, [rel]: errorMessage(err) }))
        })
    },
    [base],
  )

  // refresh 丢掉全部缓存；已展开的层由树在下一次渲染时经 ensure 重新取回
  const refresh = useCallback(() => {
    setEntries({})
    setErrors({})
    loaded.current = new Set()
    load('')
  }, [load])

  // reload 强制重取一层：清掉该层的 loaded 标记再走一遍 load。
  //
  // 与 ensure 的分工：ensure 是「已取过就空转」——建/改名/复制/删除后目标层
  // 很可能已在内存里，ensure 会直接返回旧数据；reload 是「操作成功后才刷新
  // 这一层」的实现基础，别的层不动。
  const reload = useCallback(
    (rel: string) => {
      loaded.current.delete(rel)
      load(rel)
    },
    [load],
  )

  // 为什么经 ref 读取而不是闭包 entries/errors：entriesOf/errorOf 依赖 entries 的
  // 话，某层响应回来一更新 state，dirs 对象引用就变，FileTree 的挂载 effect
  // 会把 expanded 清空、把正在展开的目录折叠掉。所以这里只依赖 ref，引用稳定。
  const entriesOf = useCallback((rel: string) => entriesRef.current[rel], [])
  const errorOf = useCallback((rel: string) => errorsRef.current[rel], [])

  // 为什么 useMemo 包一层：FileTree 的 useEffect 依赖 dirs 引用，不包的话每次
  // 渲染都产生新对象，effect 会无限重跑（base 没变也触发）
  return useMemo(
    () => ({ entriesOf, errorOf, ensure: load, refresh, reload }),
    [entriesOf, errorOf, load, refresh, reload],
  )
}
