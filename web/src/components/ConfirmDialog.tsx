import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'

export interface ConfirmOptions {
  title: string
  description?: string
  confirmLabel?: string
  cancelLabel?: string
  /** Render the confirm button in the destructive (red) style. */
  danger?: boolean
}

type ConfirmFn = (opts: ConfirmOptions) => Promise<boolean>

const ConfirmContext = createContext<ConfirmFn | null>(null)

/**
 * useConfirm returns an async confirm(opts) that resolves true/false — a styled,
 * on-brand replacement for the native window.confirm. Drop-in at call sites:
 *   if (!(await confirm({ title: t('...') }))) return
 *
 * Co-located with its provider on purpose (one small module owns the confirm
 * machinery); the only cost is HMR fast-refresh for this file, which the disable
 * below acknowledges.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function useConfirm(): ConfirmFn {
  const ctx = useContext(ConfirmContext)
  if (!ctx) throw new Error('useConfirm must be used within <ConfirmProvider>')
  return ctx
}

interface PendingState {
  opts: ConfirmOptions
  resolve: (result: boolean) => void
}

/**
 * ConfirmProvider renders a single shared confirmation dialog and exposes the
 * async confirm() via context. One instance wraps the app so every page can
 * raise a consistent styled prompt instead of the browser's native confirm.
 */
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const [pending, setPending] = useState<PendingState | null>(null)

  const confirm = useCallback<ConfirmFn>((opts) => {
    return new Promise<boolean>((resolve) => {
      setPending({ opts, resolve })
    })
  }, [])

  const settle = useCallback((result: boolean) => {
    setPending((cur) => {
      cur?.resolve(result)
      return null
    })
  }, [])

  const opts = pending?.opts

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <Dialog open={!!pending} onOpenChange={(open) => { if (!open) settle(false) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className={opts?.danger ? 'text-[#f04452]' : undefined}>
              {opts?.title}
            </DialogTitle>
            {opts?.description && <DialogDescription>{opts.description}</DialogDescription>}
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" className="rounded-xl" onClick={() => settle(false)}>
              {opts?.cancelLabel ?? t('common.cancel')}
            </Button>
            <Button
              className={opts?.danger
                ? 'rounded-xl bg-[#f04452] hover:bg-[#f04452]/90 text-white'
                : 'rounded-xl'}
              onClick={() => settle(true)}
            >
              {opts?.confirmLabel ?? t('common.confirm')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </ConfirmContext.Provider>
  )
}
