import { useTranslation } from 'react-i18next'
import type { ResourceStatus as Status } from '@/hooks/usePolledResource'

export default function ResourceStatus({ resource, label }: { resource: Status; label?: string }) {
  const { t, i18n } = useTranslation()
  const time = resource.updatedAt == null ? '' : new Date(resource.updatedAt).toLocaleTimeString(i18n.language)
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
      {label && <span>{label}</span>}
      {resource.error ? (
        <>
          <span role="status" className="text-destructive">{t(resource.updatedAt ? 'dashboard.refreshFailed' : 'dashboard.loadFailed')}</span>
          <button type="button" onClick={resource.retry} disabled={resource.loading}
            className="min-h-11 rounded-md px-2 font-medium text-primary hover:underline focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-50">
            {t(resource.loading ? 'common.loading' : 'dashboard.retry')}
          </button>
        </>
      ) : resource.updatedAt == null ? <span>{t('common.loading')}</span> : null}
      {resource.updatedAt != null && <time dateTime={new Date(resource.updatedAt).toISOString()}>{t('dashboard.updatedAt', { time })}</time>}
    </div>
  )
}
