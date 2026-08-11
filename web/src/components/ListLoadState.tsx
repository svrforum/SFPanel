import { useTranslation } from 'react-i18next'
import { AlertCircle, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

// First-load error banner + loading skeleton shared by list pages. Callers
// gate it on "no data yet" (list.length === 0) — once data exists the pages
// keep showing the stale list and surface refresh errors via toast instead.
export function ListLoadState({
  loading,
  error,
  errorTitle,
  onRetry,
  skeletonCount = 8,
}: {
  loading: boolean
  error: string | null
  /** Pre-translated headline for the error banner (e.g. t('services.loadError')). */
  errorTitle: string
  onRetry: () => void
  skeletonCount?: number
}) {
  const { t } = useTranslation()
  if (error) {
    return (
      <div className="bg-destructive/10 text-destructive rounded-xl p-3 flex items-start gap-2">
        <AlertCircle className="h-4 w-4 mt-0.5 shrink-0" />
        <div className="min-w-0 flex-1">
          <p className="text-[13px] font-medium">{errorTitle}</p>
          <p className="text-[12px] opacity-80 mt-0.5 break-words">{error}</p>
        </div>
        <Button variant="outline" size="sm" className="rounded-xl shrink-0" onClick={onRetry}>
          <RefreshCw className="h-3.5 w-3.5" />
          {t('common.retry')}
        </Button>
      </div>
    )
  }
  if (loading) {
    return (
      <div className="bg-card rounded-2xl p-3 card-shadow space-y-2">
        {Array.from({ length: skeletonCount }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full rounded-xl" />
        ))}
      </div>
    )
  }
  return null
}
