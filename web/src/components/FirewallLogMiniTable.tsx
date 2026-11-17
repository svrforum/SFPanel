import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import type { FirewallLogEntry } from '@/lib/logParsers'

const PILL_CLASS = 'inline-flex items-center px-1.5 py-0.5 rounded-full text-[12px] font-medium'

// The action pill in the theme's text-role colours. The log viewer's hex
// palette (FIREWALL_ACTION_COLORS) is drawn for its dark surface; on this
// white card its green and orange read about 2:1.
const ACTION_TONES: Record<string, string> = {
  BLOCK: 'bg-destructive/10 text-destructive',
  DROP: 'bg-destructive/10 text-destructive',
  ALLOW: 'bg-success/10 text-success',
  ACCEPT: 'bg-success/10 text-success',
  AUDIT: 'bg-primary/10 text-primary',
  LIMIT: 'bg-warning/10 text-warning',
}

interface FirewallLogMiniTableProps {
  entries: FirewallLogEntry[]
}

// Compact firewall-log table for the dashboard's recent-logs card — a small
// subset of the Logs page's parser-column pipeline.
export default function FirewallLogMiniTable({ entries }: FirewallLogMiniTableProps) {
  const { t } = useTranslation()

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-[12px]">
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
            const actionTone = Object.hasOwn(ACTION_TONES, entry.action)
              ? ACTION_TONES[entry.action]
              : 'bg-secondary text-muted-foreground'
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
                  <span className={cn(PILL_CLASS, actionTone)}>
                    {entry.action}
                  </span>
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
