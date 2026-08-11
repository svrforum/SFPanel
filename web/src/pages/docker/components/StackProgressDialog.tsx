import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { CheckCircle2, Loader2, XCircle } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'

export interface StackProgressState {
  open: boolean
  title: string
  lines: string[]
  done: boolean
  error: boolean
}

/**
 * useStackProgress owns the deploy/update progress-modal state plus the shared
 * SSE event handler that was previously copy-pasted across handleUp /
 * handleDeploy / handleUpdate. runProgressStream opens the modal, feeds every
 * stream event into it, marks it done (and rethrows) so call sites keep their
 * own success/failure toasts and refreshes.
 *
 * Co-located with StackProgressDialog on purpose; the react-refresh disable
 * mirrors ConfirmDialog.tsx.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function useStackProgress() {
  const [progress, setProgress] = useState<StackProgressState>({
    open: false,
    title: '',
    lines: [],
    done: false,
    error: false,
  })

  const runProgressStream = useCallback(
    async (
      title: string,
      run: (onEvent: (event: { phase: string; line: string }) => void) => Promise<void>,
    ) => {
      setProgress({ open: true, title, lines: [], done: false, error: false })
      try {
        await run((event) => {
          setProgress((prev) => {
            if (event.phase === 'error') {
              return { ...prev, error: true, lines: [...prev.lines, `❌ ${event.line}`] }
            }
            if (event.phase === 'complete') {
              return { ...prev, lines: [...prev.lines, `✅ ${event.line}`] }
            }
            return { ...prev, lines: [...prev.lines, event.line] }
          })
        })
        setProgress((prev) => ({ ...prev, done: true }))
      } catch (err) {
        setProgress((prev) => ({ ...prev, done: true, error: true }))
        throw err
      }
    },
    [],
  )

  const closeProgress = useCallback(() => {
    setProgress((prev) => ({ ...prev, open: false }))
  }, [])

  return { progress, runProgressStream, closeProgress }
}

export function StackProgressDialog({
  progress,
  onClose,
}: {
  progress: StackProgressState
  onClose: () => void
}) {
  const { t } = useTranslation()
  const endRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [progress.lines])

  return (
    <Dialog open={progress.open} onOpenChange={(open) => {
      if (!open && progress.done) onClose()
    }}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {!progress.done && <Loader2 className="h-4 w-4 animate-spin text-primary" />}
            {progress.done && !progress.error && <CheckCircle2 className="h-4 w-4 text-success" />}
            {progress.done && progress.error && <XCircle className="h-4 w-4 text-destructive" />}
            {progress.title}
          </DialogTitle>
        </DialogHeader>
        <div className="bg-terminal rounded-xl p-4 max-h-[400px] overflow-y-auto font-mono text-[12px] text-terminal-foreground leading-5">
          {progress.lines.map((line, i) => (
            <div key={i} className={`whitespace-pre-wrap break-all ${
              line.startsWith('✅') ? 'text-success' :
              line.startsWith('❌') ? 'text-destructive' :
              line.startsWith('[pull]') ? 'text-primary' :
              line.startsWith('[recreate]') ? 'text-warning' :
              ''
            }`}>
              {line}
            </div>
          ))}
          {!progress.done && (
            <div className="flex items-center gap-1.5 text-muted-foreground mt-1">
              <span className="inline-block w-1.5 h-1.5 rounded-full bg-primary animate-pulse" />
              {t('common.loading')}
            </div>
          )}
          <div ref={endRef} />
        </div>
        {progress.done && (
          <DialogFooter>
            <Button onClick={onClose} className="rounded-xl">
              {t('common.close')}
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  )
}
