import { useCallback, useEffect, useRef, useState } from 'react'

// usePolling завантажує дані і повторює запит кожні intervalMs мілісекунд,
// поки enabled. Коли змінюється key (наприклад, id задачі) — починає заново.
// Поки вкладка прихована, сервер не смикаємо.
export function usePolling<T>(key: string, load: () => Promise<T>, intervalMs: number, enabled = true) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const loadRef = useRef(load)
  loadRef.current = load

  const refresh = useCallback(async () => {
    try {
      setData(await loadRef.current())
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  useEffect(() => {
    setData(null)
    void refresh()
  }, [key, refresh])

  useEffect(() => {
    if (!enabled) return
    const timer = setInterval(() => {
      if (!document.hidden) void refresh()
    }, intervalMs)
    return () => clearInterval(timer)
  }, [enabled, intervalMs, refresh])

  return { data, error, refresh }
}
