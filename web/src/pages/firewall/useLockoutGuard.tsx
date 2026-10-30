import { useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2 } from 'lucide-react'
import type { ApiError } from '@/lib/api'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'

/**
 * The lockout guard's force-override prompt, shared by every page that changes
 * ufw rules: the rules page and the listening-ports page send the same request
 * and get the same 409 UFW_LOCKOUT, and both offer to go ahead with ?force=true.
 *
 * handleLockoutError returns true if it consumed a lockout refusal by opening
 * the prompt; callers toast only when it is false. Only the lockout guard can
 * be overridden: a Proxmox host's refusal is also a 409, and forcing it would
 * get the same answer. A refusal naming 8006 is about the Proxmox web UI, and
 * one with cluster_ports about the ports the other nodes reach this one on —
 * not SSH or the panel, which is what the code alone is worded as.
 */
export function useLockoutGuard() {
  const { t } = useTranslation()
  const [lockout, setLockout] = useState<{ message: string; cluster: boolean; busy: boolean; retry: () => Promise<void> } | null>(null)

  const handleLockoutError = useCallback((err: unknown, retry: () => Promise<void>): boolean => {
    const e = err as ApiError
    if (e?.status === 409 && (!e.code || e.code === 'UFW_LOCKOUT')) {
      const message = e.details?.cluster_ports
        // "3629, 3630": a denied port names one, enabling every one left closed.
        ? t('firewall.rules.lockoutBodyCluster', { cluster_ports: e.details.cluster_ports, count: e.details.cluster_ports.split(',').length })
        : e.details?.port === '8006' ? t('firewall.rules.lockoutBodyPve') : e.message
      setLockout({ message, cluster: !!e.details?.cluster_ports, busy: false, retry })
      return true
    }
    return false
  }, [t])

  const clearLockout = useCallback(() => setLockout(null), [])

  const lockoutDialog = (
    <Dialog open={!!lockout} onOpenChange={(open) => !open && setLockout(null)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="text-destructive">
            {lockout?.cluster ? t('firewall.lockout.titleCluster') : t('firewall.lockout.title')}
          </DialogTitle>
          {/* A cluster port lockout leaves the operator's own access alone, so
              the generic "only if you can still reach this server" test would
              pass while the node drops out; its body already says "only if
              you are sure". */}
          <DialogDescription className="whitespace-pre-line">
            {lockout?.message}
            {!lockout?.cluster && <>{'\n\n'}{t('firewall.lockout.warning')}</>}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter className="gap-2">
          <Button variant="outline" onClick={() => setLockout(null)} className="rounded-xl">
            {t('common.cancel')}
          </Button>
          <Button
            variant="destructive"
            disabled={lockout?.busy}
            onClick={async () => {
              if (!lockout) return
              setLockout((l) => (l ? { ...l, busy: true } : l))
              try {
                await lockout.retry()
              } finally {
                setLockout((l) => (l ? { ...l, busy: false } : l))
              }
            }}
            className="rounded-xl"
          >
            {lockout?.busy && <Loader2 className="h-4 w-4 animate-spin" />}
            {t('firewall.lockout.proceed')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )

  return { handleLockoutError, clearLockout, lockoutDialog }
}
