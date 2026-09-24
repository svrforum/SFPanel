import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'

/**
 * in_use / unused status pill shared by the Images / Networks / Volumes pages
 * (the same markup used to be copy-pasted ~12 times across the three).
 */
export function UsagePill({ inUse, usedBy }: { inUse: boolean; usedBy?: string[] }) {
  const { t } = useTranslation()
  // docker.inUse/docker.unused are the shared keys; until they land in the
  // locale files, fall back to the (identical) per-page images translation so
  // neither locale regresses.
  return <div className="flex flex-wrap items-center gap-1">{inUse ? (
    <span
      className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-success/10 text-success"
      title={usedBy && usedBy.length > 0 ? usedBy.join(', ') : undefined}
    >
      {t('docker.inUse', t('docker.images.inUse'))}
    </span>
  ) : (
    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">
      {t('docker.unused', t('docker.images.unused'))}
    </span>
  )}
    {usedBy?.map(name => <Link key={name} className="inline-flex min-h-11 items-center break-all rounded-md px-2 text-xs text-primary underline" to={`/docker/containers?search=${encodeURIComponent(name.replace(/^\//, ''))}`}>{name.replace(/^\//, '')}</Link>)}
  </div>
}
