import { useTranslation } from 'react-i18next'
import { ChevronRight, Server } from 'lucide-react'
import { serverInterfaces, serverPorts, type ServerPort } from '@/lib/netmap'
import { cn } from '@/lib/utils'
import type { NetworkServer } from './useNetworkView'

const FIREWALL_WORD: Record<string, string> = { ALLOW: 'allow', DENY: 'deny', REJECT: 'deny', LIMIT: 'limit' }

function PortRows({ ports, label }: { ports: ServerPort[]; label: string }) {
  const { t } = useTranslation()
  return (
    <ul aria-label={label} className="divide-y divide-border/60">
      {ports.map((p) => {
        const owner = p.containers.length > 0
          ? p.containers.map((c) => (c.stack ? t('running.network.server.container', { name: c.name, stack: c.stack }) : c.name)).join(', ')
          : p.process ?? t('running.host.unknownProcess')
        const word = p.firewall ? FIREWALL_WORD[p.firewall.action] : undefined
        return (
          <li
            key={`${p.port}/${p.proto}`}
            data-testid="net-port"
            data-port={`${p.port}/${p.proto}`}
            className="grid grid-cols-[5.5rem_minmax(0,1fr)] gap-x-3 gap-y-0.5 py-1.5 text-[13px] sm:grid-cols-[5.5rem_minmax(0,11rem)_minmax(0,1fr)_minmax(0,9rem)]"
          >
            <span className="font-mono tabular-nums">
              <span className="font-semibold">{p.port}</span>
              <span className="text-muted-foreground">/{p.proto}</span>
            </span>
            <span className={cn('break-all', p.exposure === 'all' ? 'font-medium' : 'text-muted-foreground')}>
              {p.exposure === 'address' ? p.addresses.join(', ') : t(`running.network.reach.${p.exposure}`)}
            </span>
            <span className="col-start-2 break-words sm:col-start-auto">{owner}</span>
            <span className="col-start-2 break-words text-muted-foreground sm:col-start-auto">
              {p.firewall
                ? `${word ? t(`running.network.firewall.${word}`) : p.firewall.action}${p.firewall.scope ? ` · ${p.firewall.scope}` : ''}`
                : t('running.network.firewall.none')}
            </span>
          </li>
        )
      })}
    </ul>
  )
}

/**
 * One node from the inside: the networks it is on and what listens on it,
 * told apart by who can reach each port — which nmap, looking from outside,
 * cannot say of a port a firewall hides, and the board does not say at all.
 */
export function ServerNetwork({ server, open }: { server: NetworkServer; open: boolean }) {
  const { t } = useTranslation()
  const nics = server.interfaces?.phase === 'ready' ? serverInterfaces(server.interfaces.list) : null
  const ports = server.ports?.phase === 'ready' ? serverPorts(server.ports.rows) : null
  const failed = server.interfaces?.phase === 'error' ? server.interfaces.error : server.ports?.phase === 'error' ? server.ports.error : null
  const address = nics?.shown[0]?.addresses[0]
  return (
    <details data-testid="net-server" data-node={server.id} open={open || undefined} className="group rounded-2xl bg-card card-shadow">
      <summary className="flex min-h-12 cursor-pointer list-none flex-wrap items-center gap-x-3 gap-y-0.5 rounded-2xl px-4 py-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
        <ChevronRight className="size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-90 motion-reduce:transition-none" aria-hidden="true" />
        <Server className="size-4 shrink-0 text-primary" aria-hidden="true" />
        <span className="min-w-0 break-words text-[15px] font-semibold">{server.name}</span>
        {address && <span className="font-mono text-[13px] text-muted-foreground">{address}</span>}
        <span className="ml-auto text-[13px] text-muted-foreground">
          {!server.online
            ? t('running.network.server.offline')
            : ports
              ? t('running.network.server.summary', { open: ports.open.length, local: ports.local.length })
              : failed ? t('running.network.server.failed') : t('common.loading')}
        </span>
      </summary>
      {server.online && (
        <div className="grid gap-x-8 gap-y-4 px-4 pb-4 pt-1 lg:grid-cols-[minmax(0,2fr)_minmax(0,5fr)]">
          {failed && (
            <p role="alert" className="flex flex-wrap items-center gap-x-2 text-[13px] text-destructive lg:col-span-2">
              <span className="break-words">{t('running.network.server.loadError')} {failed}</span>
              <button type="button" onClick={server.retry} className="min-h-11 rounded-md px-2 font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                {t('common.retry')}
              </button>
            </p>
          )}
          <div>
            <p className="pb-1 text-xs font-semibold text-muted-foreground">{t('running.network.server.interfaces')}</p>
            {nics ? (
              <>
                {nics.shown.length === 0 && <p className="text-[13px] text-muted-foreground">{t('running.network.server.noInterfaces')}</p>}
                <ul className="space-y-1.5">
                  {nics.shown.map((i) => (
                    <li key={i.name} data-testid="net-interface" className="text-[13px]">
                      <span className="font-semibold">{i.name}</span>
                      {i.isDefault && <span className="ml-1.5 rounded-md bg-secondary px-1.5 py-0.5 text-xs text-secondary-foreground">{t('running.network.server.defaultRoute')}</span>}
                      {!i.up && <span className="ml-1.5 text-xs text-muted-foreground">{t('running.network.server.down')}</span>}
                      {i.addresses.map((a) => <span key={a} className="block break-all font-mono text-[13px]">{a}</span>)}
                      {i.mac && <span className="block font-mono text-xs text-muted-foreground">{i.mac}</span>}
                    </li>
                  ))}
                </ul>
                {nics.inner > 0 && <p className="pt-2 text-xs text-muted-foreground">{t('running.network.server.inner', { count: nics.inner })}</p>}
              </>
            ) : !failed && <p role="status" className="text-[13px] text-muted-foreground">{t('common.loading')}</p>}
          </div>
          <div>
            <p className="pb-1 text-xs font-semibold text-muted-foreground">{t('running.network.server.ports')}</p>
            {ports ? (
              <>
                {ports.open.length === 0
                  ? <p className="text-[13px] text-muted-foreground">{t('running.network.server.noOpen')}</p>
                  : <PortRows ports={ports.open} label={t('running.network.server.ports')} />}
                {ports.local.length > 0 && (
                  <details className="group/local pt-1">
                    <summary className="flex min-h-11 cursor-pointer list-none items-center gap-1.5 rounded-md text-xs font-semibold text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
                      <ChevronRight className="size-3.5 shrink-0 transition-transform group-open/local:rotate-90 motion-reduce:transition-none" aria-hidden="true" />
                      {t('running.network.server.local', { count: ports.local.length })}
                    </summary>
                    <PortRows ports={ports.local} label={t('running.network.server.local', { count: ports.local.length })} />
                  </details>
                )}
              </>
            ) : !failed && <p role="status" className="text-[13px] text-muted-foreground">{t('common.loading')}</p>}
          </div>
        </div>
      )}
    </details>
  )
}
