import { useTranslation } from 'react-i18next'
import { Input } from '@/components/ui/input'

export function ResourceFilters({ query, onQuery, usage, onUsage, sort, onSort, count, sizeSort = true }: {
  query: string; onQuery: (value: string) => void
  usage: string; onUsage: (value: string) => void
  sort: string; onSort: (value: string) => void; count: number; sizeSort?: boolean
}) {
  const { t } = useTranslation()
  return <div className="flex flex-wrap items-center gap-2">
    <Input className="min-w-40 flex-1" aria-label={t('docker.improvements.search')} placeholder={t('docker.improvements.search')} value={query} onChange={e => onQuery(e.target.value)} />
    <select className="min-h-11 rounded-xl bg-secondary px-3 text-sm" aria-label={t('common.status')} value={usage} onChange={e => onUsage(e.target.value)}>
      <option value="all">{t('docker.improvements.all')}</option><option value="used">{t('docker.inUse')}</option><option value="unused">{t('docker.unused')}</option>
    </select>
    {sizeSort && <select className="min-h-11 rounded-xl bg-secondary px-3 text-sm" aria-label={t('docker.improvements.sort')} value={sort} onChange={e => onSort(e.target.value)}>
      <option value="name">{t('common.name')}</option><option value="size">{t('docker.improvements.sizeSort')}</option>
    </select>}
    <span className="text-xs text-muted-foreground" role="status">{t('docker.improvements.results', { count })}</span>
  </div>
}
