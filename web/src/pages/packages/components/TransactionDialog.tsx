import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertCircle, CheckCircle2, Loader2 } from 'lucide-react'
import type { SSEOutputState } from '@/components/OutputDialog'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

// The output dialog of a package transaction. It looks like OutputDialog and
// differs where a transaction differs from a request: apt runs in a unit of
// its own, so the dialog can be put away while it runs — the banner on the
// page brings it back — and failure is how apt exited, which the page passes
// in as state.error, never a guess from the words in the log.
export function TransactionDialog({
  state,
  onClose,
  onHide,
}: {
  state: SSEOutputState
  onClose: () => void
  onHide: () => void
}) {
  const { t } = useTranslation()
  const outputRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (outputRef.current) {
      outputRef.current.scrollTop = outputRef.current.scrollHeight
    }
  }, [state.output])

  const dismiss = state.done ? onClose : onHide

  return (
    <Dialog
      open={state.open}
      onOpenChange={(open) => {
        if (!open) dismiss()
      }}
    >
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {!state.done && <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />}
            {state.done && state.error && <AlertCircle className="h-4 w-4 text-destructive" aria-hidden="true" />}
            {state.done && !state.error && <CheckCircle2 className="h-4 w-4 text-success" aria-hidden="true" />}
            {state.title}
          </DialogTitle>
          <DialogDescription>
            {!state.done
              ? t('packages.txRunningNote')
              : state.error
                ? t('common.output.operationFailed')
                : t('common.output.operationComplete')}
          </DialogDescription>
        </DialogHeader>
        <div ref={outputRef} className="bg-zinc-950 text-zinc-100 rounded-xl p-4 max-h-96 overflow-y-auto">
          <pre className="text-xs font-mono whitespace-pre-wrap break-words">
            {state.output || t('common.output.waitingForOutput')}
          </pre>
        </div>
        <DialogFooter>
          <Button variant="outline" className="rounded-xl" onClick={dismiss}>
            {state.done ? t('common.close') : t('packages.txHide')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
