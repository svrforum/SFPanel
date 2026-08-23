import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { AlertTriangle, ChevronRight } from 'lucide-react'
import { cn, formatDate } from '@/lib/utils'
import type { AttentionItem } from './attention'

/**
 * The short list of things that went wrong, or nothing at all.
 *
 * Renders null when the list is empty, which is the normal case — a healthy
 * host must not gain a permanent empty box, and a single-node install with no
 * alert rules and no Docker should see no change to the page whatsoever.
 */
export function AttentionStrip({ items }: { items: AttentionItem[] }) {
  const { t } = useTranslation()
  const navigate = useNavigate()

  if (items.length === 0) return null

  return (
    <div className="bg-card rounded-2xl p-4 md:p-5 card-shadow">
      <div className="mb-3 flex items-center gap-2">
        <AlertTriangle className="h-4 w-4 text-warning" aria-hidden="true" />
        <span className="text-[13px] font-semibold">{t('dashboard.attentionTitle')}</span>
      </div>
      <ul className="space-y-1">
        {items.map((item) => (
          <li key={item.key}>
            <button
              type="button"
              onClick={() => navigate(item.to)}
              className="flex w-full items-center gap-2 rounded-xl px-2 py-2 text-left transition-colors hover:bg-secondary/60 outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
            >
              <span
                className={cn(
                  'h-1.5 w-1.5 shrink-0 rounded-full',
                  item.severity === 'critical' ? 'bg-destructive' : 'bg-warning',
                )}
                aria-hidden="true"
              />
              <span className="truncate text-[13px] font-medium">{item.subject}</span>
              <span className="truncate text-[12px] text-muted-foreground">{describe(item, t)}</span>
              <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">
                {item.kind === 'service' ? '' : formatDate(new Date(item.ts).toISOString())}
              </span>
              <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

/** Turns the builder's machine detail into a sentence, translated. */
function describe(item: AttentionItem, t: (k: string, o?: Record<string, unknown>) => string): string {
  if (item.kind === 'service') return t('dashboard.attention.serviceFailed')
  if (item.kind === 'alert') return item.detail
  const [type, code] = item.detail.split(':')
  if (code) return t('dashboard.attention.exited', { code })
  return t(`dashboard.attention.${type}`, { defaultValue: type })
}
