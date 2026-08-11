import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Boxes, CheckCircle2, Circle, ExternalLink, Loader2, XCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'

export interface InstallLogLine {
  stage: string
  message: string
  success: boolean
}

const STAGE_ORDER = ['fetch', 'prepare', 'pull', 'start', 'done'] as const

/**
 * Presentational install-progress block: stage indicators, streamed log lines
 * and the post-install action buttons. All stream state stays in the modal
 * (handleInstall writes it); this only renders it.
 */
export function InstallProgressPanel({
  logs,
  done,
  success,
  health,
  currentStage,
  installPort,
  onManage,
  onClose,
}: {
  logs: InstallLogLine[]
  done: boolean
  success: boolean
  health: string
  currentStage: string
  installPort: string
  onManage: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const logEndRef = useRef<HTMLDivElement>(null)

  // Auto-scroll logs within container only
  useEffect(() => {
    const el = logEndRef.current?.parentElement
    if (el) {
      el.scrollTop = el.scrollHeight
    }
  }, [logs])

  return (
    <div className="bg-secondary/20 rounded-xl p-5 animate-in slide-in-from-top-2 duration-200">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-[14px] font-semibold">
          {done
            ? success
              ? t('appStore.installComplete')
              : t('appStore.installFailed')
            : t('appStore.installing')}
        </h3>
        {done && (
          success ? (
            <CheckCircle2 className="h-5 w-5 text-success" />
          ) : (
            <XCircle className="h-5 w-5 text-destructive" />
          )
        )}
        {!done && (
          <Loader2 className="h-5 w-5 animate-spin text-primary" />
        )}
      </div>

      {/* Stage indicators */}
      <div className="flex items-center gap-2 mb-4">
        {STAGE_ORDER.map((stage) => {
          const stageLabels: Record<string, string> = {
            fetch: t('appStore.stageFetch'),
            prepare: t('appStore.stagePrepare'),
            pull: t('appStore.stagePull'),
            start: t('appStore.stageStart'),
            done: t('appStore.stageDone'),
          }
          const currentIdx = STAGE_ORDER.indexOf(currentStage as typeof STAGE_ORDER[number])
          const thisIdx = STAGE_ORDER.indexOf(stage)
          const isComplete = thisIdx < currentIdx || (done && success)
          const isCurrent = stage === currentStage && !done
          const isFailed = done && !success && stage === currentStage

          return (
            <div key={stage} className="flex items-center gap-1">
              {isComplete ? (
                <CheckCircle2 className="h-3.5 w-3.5 text-success" />
              ) : isFailed ? (
                <XCircle className="h-3.5 w-3.5 text-destructive" />
              ) : isCurrent ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin text-primary" />
              ) : (
                <Circle className="h-3.5 w-3.5 text-muted-foreground/30" />
              )}
              <span className={`text-[11px] ${isCurrent ? 'text-primary font-medium' : isComplete ? 'text-success' : isFailed ? 'text-destructive' : 'text-muted-foreground/50'}`}>
                {stageLabels[stage]}
              </span>
              {stage !== 'done' && (
                <span className="text-muted-foreground/20 mx-1">›</span>
              )}
            </div>
          )
        })}
      </div>

      {/* Log output */}
      <div className="rounded-xl bg-[#1e1e2e] p-4 max-h-64 overflow-y-auto font-mono text-[11px] leading-relaxed">
        {logs.map((log, idx) => (
          <div
            key={idx}
            className={`${log.success ? 'text-[#cdd6f4]' : 'text-destructive'}`}
          >
            <span className="text-[#89b4fa] select-none">[{log.stage}]</span>{' '}
            {log.message}
          </div>
        ))}
        <div ref={logEndRef} />
      </div>

      {done && success && (
        health === 'healthy' ? (
          <p className="text-[13px] text-success mt-4">{t('appStore.healthHealthy')}</p>
        ) : health === 'starting' ? (
          <p className="text-[13px] text-amber-600 mt-4">{t('appStore.healthStarting')}</p>
        ) : null
      )}

      {done && (
        <div className="flex flex-wrap gap-3 mt-4">
          {success && installPort && (
            <Button
              size="sm"
              className="rounded-xl"
              onClick={() =>
                window.open(
                  `${window.location.protocol}//${window.location.hostname}:${installPort}`,
                  '_blank',
                  'noopener'
                )
              }
            >
              <ExternalLink className="h-4 w-4 mr-1.5" />
              {t('appStore.openApp')}
            </Button>
          )}
          {success && (
            <Button
              size="sm"
              variant="outline"
              className="rounded-xl"
              onClick={onManage}
            >
              <Boxes className="h-4 w-4 mr-1.5" />
              {t('appStore.manageInStacks')}
            </Button>
          )}
          <Button
            size="sm"
            variant={success ? 'ghost' : 'default'}
            className="rounded-xl"
            onClick={onClose}
          >
            {t('common.close')}
          </Button>
        </div>
      )}
    </div>
  )
}
