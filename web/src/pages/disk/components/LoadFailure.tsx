import { useTranslation } from 'react-i18next'
import { AlertTriangle } from 'lucide-react'
import { ListLoadState } from '@/components/ListLoadState'
import { Button } from '@/components/ui/button'
import type { ApiError } from '@/lib/api'

/**
 * What a tab shows when its list could not be read, in place of the list: an
 * empty table said "no physical volumes" over a live Create button while the
 * reason sat in a toast that went away. A missing tool (the server answers
 * 503 TOOL_NOT_INSTALLED) gets the smartmontools banner's shape, naming the
 * package and the command that installs it; anything else, a retry.
 */
export function LoadFailure({ error, title, toolTitle, command, onRetry }: {
  error: ApiError
  /** Pre-translated headline for a failure that is not a missing tool. */
  title: string
  /** Pre-translated "X is not installed" for this tab's tool. */
  toolTitle: string
  /** The command that installs it, e.g. "apt install lvm2". */
  command: string
  onRetry: () => void
}) {
  const { t } = useTranslation()
  if (error.code !== 'TOOL_NOT_INSTALLED') {
    return <ListLoadState loading={false} error={error.message} errorTitle={title} onRetry={onRetry} />
  }
  return (
    <div className="flex flex-wrap items-center gap-3 bg-warning/10 border border-warning/30 rounded-2xl px-5 py-3.5" role="alert">
      <AlertTriangle className="h-5 w-5 text-warning shrink-0" aria-hidden="true" />
      <div className="min-w-0 flex-1">
        <p className="text-[13px] font-medium">{toolTitle}</p>
        <p className="text-[12px] text-muted-foreground mt-0.5">{t('disk.toolMissingHint')}</p>
        <code className="mt-1.5 inline-block rounded-md bg-secondary px-2 py-0.5 font-mono text-[12px] break-all">{command}</code>
      </div>
      <Button variant="outline" size="sm" onClick={onRetry} className="rounded-xl shrink-0">
        {t('common.retry')}
      </Button>
    </div>
  )
}
