import { useTranslation } from 'react-i18next'
import type { DiffSummary } from '@/types/api'

interface Props {
  summary: DiffSummary
}

export function DiffSummaryHeader({ summary }: Props) {
  const { t } = useTranslation()
  const added = t('compose.diff.added', 'Added')
  const modified = t('compose.diff.modified', 'Changed')
  const removed = t('compose.diff.removed', 'Removed')
  return (
    <div
      className="flex items-center gap-4 text-[13px] bg-secondary/50 rounded-lg px-3 py-2"
      role="status"
      aria-label={`${added} ${summary.added}, ${modified} ${summary.modified}, ${removed} ${summary.removed}`}
    >
      <span className="flex items-center gap-1 text-emerald-600">
        <span className="font-mono">+</span>
        <span>{added} {summary.added}</span>
      </span>
      <span className="flex items-center gap-1 text-blue-600">
        <span className="font-mono">~</span>
        <span>{modified} {summary.modified}</span>
      </span>
      <span className="flex items-center gap-1 text-destructive">
        <span className="font-mono">−</span>
        <span>{removed} {summary.removed}</span>
      </span>
    </div>
  )
}
