import { useTranslation } from 'react-i18next'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface PaginationRowProps {
  /** 1-based current page. */
  page: number
  /** Total item count (not page count). */
  total: number
  /** Page size — the row renders nothing while everything fits on one page. */
  limit: number
  /** Pre-translated "Page x / y" indicator (the i18n key differs per caller). */
  label: string
  onPage: (page: number) => void
}

// Shared pagination footer for paged tables (audit log, alert history) — the
// two callers used to duplicate this row down to the aria-labels.
export default function PaginationRow({ page, total, limit, label, onPage }: PaginationRowProps) {
  const { t } = useTranslation()
  const totalPages = Math.ceil(total / limit)
  if (total <= limit) return null
  return (
    <div className="flex items-center justify-between mt-3">
      <span className="text-[12px] text-muted-foreground">{label}</span>
      <div className="flex gap-1.5">
        <Button
          variant="outline"
          size="sm"
          className="rounded-xl h-7 px-2"
          aria-label={t('common.previous')}
          disabled={page <= 1}
          onClick={() => onPage(page - 1)}
        >
          <ChevronLeft className="h-3.5 w-3.5" />
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="rounded-xl h-7 px-2"
          aria-label={t('common.next')}
          disabled={page >= totalPages}
          onClick={() => onPage(page + 1)}
        >
          <ChevronRight className="h-3.5 w-3.5" />
        </Button>
      </div>
    </div>
  )
}
