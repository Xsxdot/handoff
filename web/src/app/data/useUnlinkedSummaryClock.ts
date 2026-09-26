// Keep age-dependent ledger summaries honest even when polling is paused while
// the tab is hidden or the last request failed.
import { useEffect, useState } from 'react'
import { UNLINKED_SUMMARY_TTL_MS } from '../../api/ledger'

export function useUnlinkedSummaryClock(observedAt?: string | null): number {
  const [, setNow] = useState(() => Date.now())

  useEffect(() => {
    let timer: number | undefined
    const refresh = () => {
      const current = Date.now()
      setNow(current)
      if (timer !== undefined) window.clearTimeout(timer)
      const observedMs = observedAt ? Date.parse(observedAt) : Number.NaN
      if (!Number.isFinite(observedMs)) return
      const untilExpired = observedMs + UNLINKED_SUMMARY_TTL_MS + 1 - current
      if (untilExpired > 0) timer = window.setTimeout(refresh, untilExpired)
    }
    const onVisibility = () => { if (!document.hidden) refresh() }
    refresh()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      if (timer !== undefined) window.clearTimeout(timer)
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [observedAt])

  return Date.now()
}
