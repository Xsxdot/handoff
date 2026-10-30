// useNativeShellContext —— read-only display context published by the native WebView shell.
// The DOM attributes are presentation data only; this hook never writes native selection state.
import { useEffect, useState } from 'react'

export interface NativeShellContext {
  enabled: boolean
  machine: string
}

function readContext(): NativeShellContext {
  if (typeof document === 'undefined') return { enabled: false, machine: '' }
  const root = document.documentElement
  return {
    enabled: root.dataset.handoffNativeShell === '1',
    machine: root.dataset.handoffNativeMachine ?? '',
  }
}

export function useNativeShellContext(): NativeShellContext {
  const [context, setContext] = useState(readContext)
  useEffect(() => {
    const refresh = () => setContext(readContext())
    window.addEventListener('handoff-native-context', refresh)
    refresh()
    return () => window.removeEventListener('handoff-native-context', refresh)
  }, [])
  return context
}
