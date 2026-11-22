import { useTranslation } from 'react-i18next'
import { AlertTriangle, X } from 'lucide-react'
import { deviceName, filterSegment, shownCount, type NetworkEntry } from '@/lib/netmap'
import { Heading, type HeadingLevel } from '../Heading'
import type { Scope } from '../targets'
import type { BoardSection } from '../useRunningBoard'
import { Diagram } from './Diagram'
import { ScanControl } from './ScanControl'
import { ServerNetwork } from './ServerNetwork'
import { useNetworkView, type NetworkModel } from './useNetworkView'

function Network({ entry, model, scope, localId, query, level, onClear }: {
  entry: NetworkEntry
  model: NetworkModel
  scope: Scope
  localId: string
  query: string
  level: HeadingLevel
  onClear: () => void
}) {
  const { t, i18n } = useTranslation()
  const segment = entry.segment ? filterSegment(entry.segment, query) : null
  const searching = query.trim() !== ''
  const when = segment ? new Date(segment.finishedAt).toLocaleString(i18n.language, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : ''
  return (
    <section data-testid="net-network" data-cidr={entry.cidr} className="space-y-3 rounded-2xl bg-card p-4 card-shadow">
      <header className="flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="min-w-0">
          <Heading level={level} className="break-all font-mono text-[17px] font-bold tabular-nums">{entry.cidr}</Heading>
          {segment ? (
            <p className="mt-0.5 text-[13px] text-muted-foreground">
              {t('running.network.segment.found', { count: segment.count })}
              {' · '}
              {t('running.network.segment.scanned', { node: segment.scannedBy.name })}{' '}
              <time dateTime={new Date(segment.finishedAt).toISOString()}>{when}</time>
              {' · '}
              {t(segment.portsChecked ? 'running.network.segment.withPorts' : 'running.network.segment.hostsOnly')}
            </p>
          ) : (
            <p className="mt-0.5 text-[13px] text-muted-foreground">
              {t('running.network.segment.notScanned', { nodes: entry.scanners.map((s) => s.nodeName).join(', ') })}
            </p>
          )}
        </div>
        <ScanControl entry={entry} scope={scope} localId={localId} onStart={model.start} onCancel={model.cancel} />
      </header>
      {/* Said whatever is searched for: two devices on one address breaks what talks to either. */}
      {entry.segment?.conflicts.map((d) => (
        <p key={d.key} role="alert" data-testid="net-conflict" className="flex items-start gap-2 rounded-xl border border-destructive bg-[color-mix(in_srgb,var(--destructive)_6%,var(--card))] px-3 py-2 text-[13px]">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden="true" />
          <span className="break-words">
            <span className="font-semibold">{t('running.network.conflict.title', { address: d.ip })}</span>{' '}
            {t('running.network.conflict.body', { node: d.conflict!.name, who: deviceName(d)?.text ?? t('running.network.device.unnamed'), mac: d.mac })}
          </span>
        </p>
      ))}
      {segment && (searching && shownCount(segment) === 0 ? (
        <p className="text-[13px] text-muted-foreground">
          {t('running.search.noMatch', { query })}{' '}
          <button type="button" onClick={onClear} className="min-h-11 rounded-md px-1 font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            {t('running.search.clear')}
          </button>
        </p>
      ) : <Diagram segment={segment} />)}
      {!segment && <p className="text-[13px] text-muted-foreground">{t('running.network.segment.explain')}</p>}
    </section>
  )
}

/**
 * The network view: each network the nodes are on as a diagram of what nmap
 * found there, then each node from the inside — its interfaces and what
 * listens on them. `round` changes when the operator asks for a refresh.
 */
export function NetworkView({ sections, scope, localId, query, round, level, onClearQuery }: {
  sections: BoardSection[]
  scope: Scope
  localId: string
  query: string
  round: number
  level: HeadingLevel
  onClearQuery: () => void
}) {
  const { t } = useTranslation()
  const model = useNetworkView(sections, round)
  const ended = model.ended
  const endedNode = ended ? sections.find((s) => s.node.id === ended.nodeId)?.node.name || ended.nodeId : ''
  return (
    <div className="space-y-4" data-testid="net-view">
      {ended && (
        <p role="alert" className="flex items-center justify-between gap-3 rounded-2xl bg-card px-4 py-2 text-[13px] card-shadow">
          <span className="break-words">{t(`running.network.scan.ended.${ended.state}`, { node: endedNode, network: ended.network })}</span>
          <button type="button" onClick={model.dismissEnded} aria-label={t('common.close')} className="grid size-11 shrink-0 place-items-center rounded-md text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <X className="size-4" aria-hidden="true" />
          </button>
        </p>
      )}
      {model.failed.map((f) => (
        <p key={f.id} role="alert" className="break-words rounded-2xl bg-card px-4 py-3 text-[13px] text-destructive card-shadow">
          {t('running.network.loadError', { node: f.name })} {f.error}
        </p>
      ))}
      {model.loading && <p role="status" className="rounded-2xl bg-card px-4 py-3 text-[13px] text-muted-foreground card-shadow">{t('common.loading')}</p>}
      {model.unsupported && <p className="rounded-2xl bg-card px-4 py-3 text-[13px] text-muted-foreground card-shadow">{t('running.network.unsupported')}</p>}
      {!model.loading && !model.unsupported && model.entries.length === 0 && model.failed.length === 0 && (
        <p className="rounded-2xl bg-card px-4 py-3 text-[13px] text-muted-foreground card-shadow">{t('running.network.none')}</p>
      )}
      {model.entries.map((entry) => (
        <Network key={entry.key} entry={entry} model={model} scope={scope} localId={localId} query={query} level={level} onClear={onClearQuery} />
      ))}
      <section className="space-y-2">
        <Heading level={level} className="px-1 pt-2 text-[15px] font-semibold">{t('running.network.server.title')}</Heading>
        <p className="px-1 text-[13px] text-muted-foreground">{t('running.network.server.explain')}</p>
        {model.servers.map((server) => <ServerNetwork key={server.id} server={server} open={model.servers.length <= 2} />)}
      </section>
    </div>
  )
}
