import { useEffect, useRef } from 'react'

/**
 * Like setInterval but pauses when the tab is hidden.
 * Fires immediately on mount and when the tab becomes visible again.
 */
export function useVisibleInterval(callback: () => void, intervalMs: number) {
  const savedCallback = useRef(callback)
  savedCallback.current = callback

  useEffect(() => {
    let timer: ReturnType<typeof setInterval> | null = null

    const start = () => {
      if (timer) return
      savedCallback.current()
      timer = setInterval(() => savedCallback.current(), intervalMs)
    }

    const stop = () => {
      if (timer) {
        clearInterval(timer)
        timer = null
      }
    }

    const handleVisibility = () => {
      if (document.hidden) {
        stop()
      } else {
        start()
      }
    }

    document.addEventListener('visibilitychange', handleVisibility)

    if (!document.hidden) {
      start()
    }

    return () => {
      stop()
      document.removeEventListener('visibilitychange', handleVisibility)
    }
  }, [intervalMs])
}
