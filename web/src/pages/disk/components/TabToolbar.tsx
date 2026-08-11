import { useTranslation } from 'react-i18next'
import { RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import type { ReactNode } from 'react'

/** Shared loading early-return block for the disk sub-tab pages. */
export function TabLoading() {
  const { t } = useTranslation()
  return (
    <div className="flex items-center justify-center py-12 text-muted-foreground">
      {t('common.loading')}
    </div>
  )
}

/** Count pill shown on the left side of a disk tab toolbar. */
export function CountPill({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
      {children}
    </span>
  )
}

/** Refresh button shared by the disk tab toolbars. */
export function RefreshButton({ onClick, loading }: { onClick: () => void; loading: boolean }) {
  const { t } = useTranslation()
  return (
    <Button variant="outline" size="sm" onClick={onClick} disabled={loading} className="rounded-xl">
      <RefreshCw className={loading ? 'animate-spin' : ''} />
      {t('common.refresh')}
    </Button>
  )
}
