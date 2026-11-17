import { useEffect, useMemo, useState } from 'react'
import { api } from '@/lib/api'
import { freeDevices } from './freeDevices'
import type { BlockDevice } from '@/types/api'

// useFreeDevices loads the host's disks while a dialog that picks one is open,
// and the devices among them that can be taken (see freeDevices). failed says
// the list could not be read, which is not the same as there being none.
export function useFreeDevices(open: boolean) {
  const [disks, setDisks] = useState<BlockDevice[]>([])
  const [loading, setLoading] = useState(open)
  const [failed, setFailed] = useState(false)

  // Each opening starts over (adjust-state-during-render, as in
  // TypeToConfirmDialog).
  const [prevOpen, setPrevOpen] = useState(open)
  if (prevOpen !== open) {
    setPrevOpen(open)
    if (open) {
      setLoading(true)
      setFailed(false)
    }
  }

  useEffect(() => {
    if (!open) return
    let cancelled = false
    api.getDiskOverview()
      .then((data) => {
        if (!cancelled) setDisks(data || [])
      })
      .catch(() => {
        if (cancelled) return
        setDisks([])
        setFailed(true)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
  }, [open])

  const free = useMemo(() => freeDevices(disks), [disks])
  return { disks, free, loading, failed }
}
