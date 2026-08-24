import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/button'

/**
 * The countdown after a network change, and the button that keeps it.
 *
 * `netplan generate` rejects a malformed file, but nothing rejects a perfectly
 * valid one that moves the host off the address the operator is reaching it
 * on — which is how this actually goes wrong, and why the handler's own
 * comment has always said a remote admin should not be able to brick a server
 * with one click. The server keeps the previous files and puts them back
 * unless it hears otherwise within a minute.
 *
 * Rendering this bar is itself part of the test: if the change broke the
 * connection, the page cannot load, the button cannot be pressed, and the
 * rollback happens exactly as it should.
 */
export function ApplyConfirmBar({
  deadline,
  onConfirm,
  onExpired,
}: {
  deadline: number | null
  onConfirm: () => void
  onExpired: () => void
}) {
  const { t } = useTranslation()
  const [remaining, setRemaining] = useState(0)

  useEffect(() => {
    if (!deadline) return
    const tick = () => {
      const left = Math.max(0, Math.round((deadline - Date.now()) / 1000))
      setRemaining(left)
      if (left === 0) onExpired()
    }
    tick()
    const timer = window.setInterval(tick, 1000)
    return () => window.clearInterval(timer)
  }, [deadline, onExpired])

  if (!deadline) return null

  return (
    <div className="flex flex-wrap items-center gap-3 rounded-2xl border border-warning/30 bg-warning/10 px-4 py-3">
      <AlertTriangle className="h-4 w-4 shrink-0 text-warning" aria-hidden="true" />
      <div className="min-w-0 flex-1">
        <p className="text-[13px] font-medium">{t('network.confirmTitle')}</p>
        <p className="text-[12px] text-muted-foreground">
          {t('network.confirmDesc', { count: remaining })}
        </p>
      </div>
      <Button size="sm" onClick={onConfirm} className="shrink-0 rounded-xl">
        {t('network.confirmAction')}
      </Button>
    </div>
  )
}
