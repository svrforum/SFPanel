import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowUpFromLine, ChevronRight, Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { useApiAction } from '@/hooks/useApiAction'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

import type { ExpandCandidate } from '@/types/api'

/**
 * Filesystem expand wizard: check for candidates → pick one → review the
 * planned steps → execute. Runs the candidate check itself each time it opens.
 */
export function ExpandFilesystemDialog({ open, onOpenChange, onExpanded }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Called after a successful expand so the parent can refresh its listing. */
  onExpanded: () => void
}) {
  const { t } = useTranslation()
  const [candidates, setCandidates] = useState<ExpandCandidate[]>([])
  const [target, setTarget] = useState<ExpandCandidate | null>(null)

  const { run: runCheck, loading: checking } = useApiAction(
    api.checkFilesystemExpand.bind(api),
    {
      errorMsg: t('disk.filesystems.expandFailed'),
      onSuccess: (data) => setCandidates(data || []),
    },
  )

  const { run: runExpand, loading: expanding } = useApiAction(
    api.expandFilesystem.bind(api),
    {
      successMsg: t('disk.filesystems.expandSuccess'),
      errorMsg: t('disk.filesystems.expandFailed'),
      onSuccess: () => {
        onOpenChange(false)
        setTarget(null)
        setCandidates([])
        onExpanded()
      },
    },
  )

  // Reset whenever the dialog opens or closes (adjust-state-during-render,
  // same pattern as TypeToConfirmDialog).
  const [prevOpen, setPrevOpen] = useState(open)
  if (prevOpen !== open) {
    setPrevOpen(open)
    setTarget(null)
    setCandidates([])
  }

  useEffect(() => {
    if (open) void runCheck()
    // runCheck is not referentially stable (see useApiAction); check once per open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle>{t('disk.filesystems.expandTitle')}</DialogTitle>
          <DialogDescription>{t('disk.filesystems.expandDescription')}</DialogDescription>
        </DialogHeader>

        {checking ? (
          <div className="flex items-center justify-center py-8 gap-2 text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />
            <span className="text-[13px]">{t('disk.filesystems.expandChecking')}</span>
          </div>
        ) : target ? (
          /* Selected target: show steps and confirm */
          <div className="space-y-4">
            <div className="bg-muted/30 rounded-xl p-4 text-[13px]">
              <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2">
                <span className="text-muted-foreground shrink-0">{t('disk.filesystems.source')}</span>
                <span className="font-mono font-medium truncate min-w-0" title={target.source}>{target.source}</span>
                <span className="text-muted-foreground shrink-0">{t('disk.filesystems.fsType')}</span>
                <span className="font-mono">{target.fstype}{target.is_lvm && (
                  <span className="ml-1.5 text-[10px] px-1.5 py-0.5 rounded bg-primary/10 text-primary font-medium">
                    {t('disk.filesystems.expandLVM')}
                  </span>
                )}</span>
                <span className="text-muted-foreground">{t('disk.filesystems.expandCurrentSize')}</span>
                <span className="font-mono">{formatBytes(target.current_size)}</span>
                <span className="text-muted-foreground">{t('disk.filesystems.expandFreeSpace')}</span>
                <span className="font-mono font-semibold text-success">+{formatBytes(target.free_space)}</span>
              </div>
            </div>

            <div>
              <p className="text-[11px] text-muted-foreground uppercase tracking-wider mb-2">
                {t('disk.filesystems.expandSteps')}
              </p>
              <div className="space-y-1.5">
                {target.steps.map((step, i) => (
                  <div key={i} className="flex items-start gap-2 bg-muted/20 rounded-lg px-3 py-2">
                    <span className="flex-shrink-0 w-5 h-5 rounded-full bg-primary/10 text-primary text-[11px] font-semibold flex items-center justify-center mt-0.5">
                      {i + 1}
                    </span>
                    <div className="min-w-0">
                      <p className="text-[12px] font-medium font-mono break-all">{step.command}</p>
                      <p className="text-[11px] text-muted-foreground">{step.description}</p>
                    </div>
                  </div>
                ))}
              </div>
            </div>

            <p className="text-[12px] text-muted-foreground">{t('disk.filesystems.expandConfirm')}</p>
          </div>
        ) : candidates.length === 0 ? (
          /* No candidates */
          <div className="flex flex-col items-center justify-center py-8 text-center">
            <ArrowUpFromLine className="h-8 w-8 text-muted-foreground/40 mb-3" />
            <p className="text-[13px] font-medium text-muted-foreground">{t('disk.filesystems.expandNoTarget')}</p>
            <p className="text-[11px] text-muted-foreground/70 mt-1 max-w-[300px]">{t('disk.filesystems.expandNoTargetDesc')}</p>
          </div>
        ) : (
          /* Candidate list */
          <div className="space-y-2">
            {candidates.map((c) => (
              <button
                key={c.source}
                type="button"
                className="w-full text-left bg-muted/20 hover:bg-muted/40 rounded-xl p-4 transition-colors group outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                onClick={() => setTarget(c)}
              >
                <div className="flex items-center justify-between">
                  <div className="min-w-0">
                    <p className="text-[13px] font-medium font-mono truncate" title={c.source}>{c.source}</p>
                    <div className="flex items-center gap-2 mt-1">
                      <span className="inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium border border-border">
                        {c.fstype}
                      </span>
                      {c.is_lvm && (
                        <span className="text-[10px] px-1.5 py-0.5 rounded bg-primary/10 text-primary font-medium">
                          {t('disk.filesystems.expandLVM')}
                        </span>
                      )}
                      <span className="text-[11px] text-muted-foreground">
                        {formatBytes(c.current_size)}
                      </span>
                      <span className="text-[11px] font-semibold text-success">
                        +{formatBytes(c.free_space)}
                      </span>
                    </div>
                    <p className="text-[11px] text-muted-foreground mt-1">
                      {c.steps.length} {t('disk.filesystems.expandSteps').toLowerCase()}
                    </p>
                  </div>
                  <ChevronRight className="h-4 w-4 text-muted-foreground group-hover:text-foreground transition-colors flex-shrink-0" />
                </div>
              </button>
            ))}
          </div>
        )}

        <DialogFooter>
          {target ? (
            <>
              <Button variant="outline" onClick={() => setTarget(null)}>
                {t('common.back')}
              </Button>
              <Button onClick={() => void runExpand(target.source)} disabled={expanding}>
                {expanding ? (
                  <>
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    {t('disk.filesystems.expanding')}
                  </>
                ) : t('disk.filesystems.expand')}
              </Button>
            </>
          ) : (
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              {t('common.close')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
