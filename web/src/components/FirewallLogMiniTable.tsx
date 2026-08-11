import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { FIREWALL_ACTION_COLORS } from '@/lib/logParsers'
import type { FirewallLogEntry } from '@/lib/logParsers'

const PILL_CLASS = 'inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-medium'

interface FirewallLogMiniTableProps {
  entries: FirewallLogEntry[]
}

// Compact firewall-log table for the dashboard's recent-logs card — a small
// subset of the Logs page's parser-column pipeline, sharing the same action
// palette (FIREWALL_ACTION_COLORS) and hex + '15' alpha-suffix tint recipe as
// the Logs pill renderer.
export default function FirewallLogMiniTable({ entries }: FirewallLogMiniTableProps) {
  const { t } = useTranslation()

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-[11px]">
        <thead>
          <tr className="text-left text-muted-foreground border-b border-border">
            <th className="pb-2 pr-3 font-medium">{t('logs.col.timestamp')}</th>
            <th className="pb-2 pr-3 font-medium">{t('logs.col.source')}</th>
            <th className="pb-2 pr-3 font-medium">{t('logs.col.action')}</th>
            <th className="pb-2 pr-3 font-medium">{t('logs.col.sourceIP')}</th>
            <th className="pb-2 pr-3 font-medium">{t('logs.col.destPort')}</th>
            <th className="pb-2 font-medium">{t('logs.col.protocol')}</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((entry, i) => {
            const ts = entry.timestamp.includes('T')
              ? entry.timestamp.replace(/\d{4}-(\d{2}-\d{2})T(\d{2}:\d{2}:\d{2}).*/, '$1 $2')
              : entry.timestamp
            // Logs prepend new rows; using i alone re-keys every
            // existing row on each refresh, wiping hover state.
            // Composite key from timestamp + source + port stays
            // stable across the slide-window updates.
            const stableKey = `${entry.timestamp}-${entry.sourceIP}-${entry.destPort}-${i}`
            const actionColor = FIREWALL_ACTION_COLORS[entry.action]
            return (
              <tr key={stableKey} className="border-b border-border/50 hover:bg-secondary/30">
                <td className="py-1.5 pr-3 font-mono text-muted-foreground whitespace-nowrap">{ts}</td>
                <td className="py-1.5 pr-3">
                  <span
                    className={cn(
                      PILL_CLASS,
                      entry.source === 'UFW' ? 'bg-primary/8 text-primary' : 'bg-warning/8 text-warning'
                    )}
                  >
                    {entry.source}
                  </span>
                </td>
                <td className="py-1.5 pr-3">
                  {actionColor ? (
                    <span
                      className={PILL_CLASS}
                      style={{ backgroundColor: actionColor + '15', color: actionColor }}
                    >
                      {entry.action}
                    </span>
                  ) : (
                    <span className={cn(PILL_CLASS, 'bg-secondary text-muted-foreground')}>
                      {entry.action}
                    </span>
                  )}
                </td>
                <td className="py-1.5 pr-3 font-mono">{entry.sourceIP}</td>
                <td className="py-1.5 pr-3 font-mono">{entry.destPort}</td>
                <td className="py-1.5 font-mono">{entry.protocol}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
