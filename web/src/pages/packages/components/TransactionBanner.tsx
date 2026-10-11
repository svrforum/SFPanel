import { useTranslation } from 'react-i18next'
import { Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import type { AptTransactions } from '@/pages/packages/useAptTransaction'

// A package transaction runs on the node and this page is not showing it:
// started from another browser, before a reload, or its dialog put away. apt
// carries on in its own unit either way; this brings its output back.
export function TransactionBanner({ tx }: { tx: AptTransactions }) {
  const { t } = useTranslation()
  const running = tx.running
  if (!running || tx.following) return null

  const time = new Date(running.started_at).toLocaleTimeString()
  const text =
    running.kind === 'install'
      ? t('packages.txBannerInstall', { name: running.packages.join(' '), time })
      : running.kind === 'upgrade'
        ? t('packages.txBannerUpgrade', { time })
        : t('packages.txBanner', { time })

  return (
    <div
      role="status"
      className="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-primary/30 bg-primary/10 px-4 py-3"
    >
      <p className="text-[13px] flex items-center gap-2">
        <Loader2 className="h-4 w-4 animate-spin text-primary shrink-0" aria-hidden="true" />
        {text}
      </p>
      <Button size="sm" className="rounded-xl" onClick={() => tx.follow()}>
        {t('packages.txShowProgress')}
      </Button>
    </div>
  )
}
