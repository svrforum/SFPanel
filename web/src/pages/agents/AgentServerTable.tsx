import { useId, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { ListChecks, RefreshCw } from 'lucide-react'
import { StatusPill } from '@/components/StatusPill'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { agentLabel } from '@/lib/codeMessages'
import { nodeRoleKind } from '@/lib/clusterNodes'
import { cn } from '@/lib/utils'
import { KindBadge } from '@/pages/cluster/components/KindBadge'
import type { AgentSettingsView, ClusterNode } from '@/types/api'
import {
  actionState, connectionText, deployHref, holdsDescribedBy, installText, matchText, sourceText, staleCell, wazuhCell, zabbixCell,
  type ActionHold, type AgentCell, type AgentName,
} from './agentsModel'
import type { StatusLoad } from './useAgentStatuses'

const AGENTS: readonly AgentName[] = ['zabbix', 'wazuh']

/**
 * A link that opens a group task's run setup, or, while there is nothing to
 * open it with, a button that says it is off and points at why. The button
 * stays in the tab order (aria-disabled, not disabled), so a keyboard user
 * reaches it and hears the reason.
 */
export function ActionLink({ to, enabled, describedBy, children }: { to: string; enabled: boolean; describedBy?: string; children: ReactNode }) {
  if (!enabled) {
    return (
      <Button type="button" variant="outline" size="sm" className="rounded-xl aria-disabled:pointer-events-none aria-disabled:opacity-50" aria-disabled="true" aria-describedby={describedBy}>
        <ListChecks aria-hidden="true" />
        {children}
      </Button>
    )
  }
  return (
    <Button asChild variant="outline" size="sm" className="rounded-xl">
      <Link to={to}>
        <ListChecks aria-hidden="true" />
        {children}
      </Link>
    </Button>
  )
}

/**
 * The lines that say why a deploy or re-apply is off, each with the id its
 * controls point at: values typed and not saved, and an agent without saved
 * values (one line per agent).
 */
export function HoldLines({ holds, unsavedId, valuesId }: {
  holds: Record<AgentName, ActionHold[]>
  unsavedId: string
  valuesId: (agent: AgentName) => string
}) {
  const { t } = useTranslation()
  const unsaved = AGENTS.some((a) => holds[a].includes('unsaved'))
  return (
    <>
      {unsaved && <p id={unsavedId} className="text-[12px] text-warning">{t('agents.actions.unsaved')}</p>}
      {AGENTS.filter((a) => holds[a].includes('values')).map((a) => (
        <p key={a} id={valuesId(a)} className="text-[12px] text-muted-foreground">{t('agents.actions.needValues', { agent: agentLabel(a) })}</p>
      ))}
    </>
  )
}

/** One agent on one server: how it is installed, whether its file holds the central values, and whether it reaches its server. */
function AgentCellView({ cell }: { cell: AgentCell }) {
  const { t, i18n } = useTranslation()
  // An agent that is not there, or the old Zabbix agent, has no agent config to compare and no connection to judge.
  const judged = cell.install !== 'none' && cell.install !== 'legacy'
  return (
    <div className="space-y-1.5 text-[13px]">
      <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="font-medium">{installText(cell, t)}</span>
        {cell.version && <span className="break-all text-muted-foreground">{cell.version}</span>}
        {cell.source && <span className="text-muted-foreground">{sourceText(cell.source, t)}</span>}
        {cell.held && <StatusPill tone="secondary">{t('agents.held')}</StatusPill>}
      </p>
      {judged && (cell.configError ? (
        <p className="break-words text-[12px] text-destructive">{t('agents.readError', { error: cell.configError })}</p>
      ) : (
        <p className={cn('break-words text-[12px]', cell.match === 'different' ? 'text-warning' : 'text-muted-foreground')}>{matchText(cell, t)}</p>
      ))}
      {judged && cell.duplicates && <p className="break-words text-[12px] text-warning">{t('agents.duplicates', { keys: cell.duplicates.join(', ') })}</p>}
      {cell.connection && (
        <div className="space-y-0.5">
          <StatusPill tone={cell.connection.tone}>{connectionText(cell.connection.state, t)}</StatusPill>
          {cell.connection.detail && <p className="break-words text-[12px] text-muted-foreground">{cell.connection.detail}</p>}
          {cell.connection.lastKeepalive && <p className="text-[12px] text-muted-foreground">{t('agents.lastKeepalive', { time: cell.connection.lastKeepalive })}</p>}
        </div>
      )}
      {cell.readAt !== undefined && <p className="text-[12px] text-muted-foreground">{t('agents.readAt', { time: new Date(cell.readAt).toLocaleTimeString(i18n.language) })}</p>}
    </div>
  )
}

/** What stands in both agents' place on a server that has not answered with them. */
function NodeState({ online, load, onRetry }: { online: boolean; load?: StatusLoad; onRetry: () => void }) {
  const { t } = useTranslation()
  if (!online) return <p className="text-[13px] text-muted-foreground">{t('agents.offline')}</p>
  // The table's one status region says that servers are being read.
  if (!load || load.phase === 'loading') return <p className="text-[13px] text-muted-foreground">{t('common.loading')}</p>
  if (load.phase === 'absent') return <p className="text-[13px] text-warning">{t('agents.absent')}</p>
  if (load.phase === 'error') {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <p className="break-words text-[13px] text-destructive">{t('agents.readError', { error: load.error })}</p>
        <Button variant="outline" size="sm" className="rounded-xl" onClick={onRetry}>
          <RefreshCw aria-hidden="true" />
          {t('agents.retry')}
        </Button>
      </div>
    )
  }
  return null
}

interface Row {
  node: ClusterNode
  online: boolean
  load?: StatusLoad
  cells: { zabbix: AgentCell; wazuh: AgentCell } | null
  refreshError?: string
}

function ServerName({ row, leaderId, id }: { row: Row; leaderId: string; id: string }) {
  const { t } = useTranslation()
  return (
    <div className="min-w-0 space-y-1">
      <div className="flex flex-wrap items-center gap-2">
        <span id={id} className="break-all text-[13px] font-medium">{row.node.name}</span>
        <KindBadge kind={nodeRoleKind(row.node, leaderId)} />
      </div>
      {row.refreshError && <p className="break-words text-[12px] text-warning">{t('agents.readError', { error: row.refreshError })}</p>}
    </div>
  )
}

/**
 * Every server with its two agents: a table from md up, a card per server
 * below. Servers are picked for a deployment here; only online ones can be.
 */
export function AgentServerTable({ nodes, leaderId, loads, settings, dirty, retry }: {
  nodes: ClusterNode[]
  leaderId: string
  loads: Record<string, StatusLoad>
  settings: AgentSettingsView | null
  /** The connection values card holds values not saved yet: a run would not use them. */
  dirty: boolean
  retry: (id: string) => void
}) {
  const { t } = useTranslation()
  const ids = useId()
  const [picked, setPicked] = useState<ReadonlySet<string>>(() => new Set())

  const rows = useMemo<Row[]>(() => nodes.map((node) => {
    const online = node.status === 'online'
    const load = online ? loads[node.id] : undefined
    const ready = load?.phase === 'ready' ? load : null
    // An answer a later refresh could not replace is shown dimmed, with when it was read.
    const shown = (cell: AgentCell) => (ready?.refreshError ? staleCell(cell, ready.at) : cell)
    return {
      node,
      online,
      load,
      cells: ready ? { zabbix: shown(zabbixCell(ready.status.zabbix, settings)), wazuh: shown(wazuhCell(ready.status.wazuh, settings)) } : null,
      refreshError: ready?.refreshError,
    }
  }), [nodes, loads, settings])

  const chosen = rows.filter((r) => r.online && picked.has(r.node.id)).map((r) => r.node.id)
  const toggle = (id: string, on: boolean) => setPicked((prev) => {
    const next = new Set(prev)
    if (on) next.add(id)
    else next.delete(id)
    return next
  })
  const reading = rows.some((r) => r.online && (!r.load || r.load.phase === 'loading'))

  const titleId = `${ids}-title`
  const unsavedId = `${ids}-unsaved`
  const valuesId = (agent: AgentName) => `${ids}-values-${agent}`
  const pickId = `${ids}-pick`
  const deploy = Object.fromEntries(AGENTS.map((a) => [a, actionState(a, { settings, dirty, servers: chosen.length })])) as Record<AgentName, ReturnType<typeof actionState>>
  const lineOf = (agent: AgentName) => (hold: ActionHold) => (hold === 'unsaved' ? unsavedId : hold === 'values' ? valuesId(agent) : pickId)

  const pick = (row: Row, nameId: string) => (
    <Checkbox
      checked={picked.has(row.node.id) && row.online}
      disabled={!row.online}
      onCheckedChange={(v) => toggle(row.node.id, v === true)}
      aria-labelledby={nameId}
    />
  )

  return (
    <section aria-labelledby={titleId} className="space-y-3">
      <h2 id={titleId} className="text-[15px] font-semibold">{t('agents.serversTitle')}</h2>
      <div className="space-y-1.5">
        <div className="flex flex-wrap gap-2">
          {AGENTS.map((agent) => (
            <ActionLink key={agent} to={deployHref(agent, chosen)} enabled={deploy[agent].enabled} describedBy={holdsDescribedBy(deploy[agent].holds, lineOf(agent))}>
              {agent === 'zabbix' ? t('agents.actions.deployZabbix') : t('agents.actions.deployWazuh')}
            </ActionLink>
          ))}
        </div>
        <HoldLines holds={{ zabbix: deploy.zabbix.holds, wazuh: deploy.wazuh.holds }} unsavedId={unsavedId} valuesId={valuesId} />
        {chosen.length === 0 && <p id={pickId} className="text-[12px] text-muted-foreground">{t('agents.actions.needSelection')}</p>}
      </div>

      {/* One region for every row still being read, not one per row. */}
      <p role="status" className="sr-only">{reading ? t('common.loading') : ''}</p>

      <div className="hidden overflow-hidden rounded-2xl bg-card card-shadow md:block">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-10"><span className="sr-only">{t('agents.select')}</span></TableHead>
              <TableHead>{t('agents.server')}</TableHead>
              <TableHead>{agentLabel('zabbix')}</TableHead>
              <TableHead>{agentLabel('wazuh')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => {
              const nameId = `${ids}-row-${row.node.id}`
              const { cells } = row
              return (
                <TableRow key={row.node.id} data-testid={`agent-row-${row.node.id}`} className="align-top">
                  <TableCell className="align-top">{pick(row, nameId)}</TableCell>
                  <TableCell className="align-top"><ServerName row={row} leaderId={leaderId} id={nameId} /></TableCell>
                  {cells ? (
                    <>
                      <TableCell className="whitespace-normal align-top"><AgentCellView cell={cells.zabbix} /></TableCell>
                      <TableCell className="whitespace-normal align-top"><AgentCellView cell={cells.wazuh} /></TableCell>
                    </>
                  ) : (
                    <TableCell colSpan={2} className="whitespace-normal align-top">
                      <NodeState online={row.online} load={row.load} onRetry={() => retry(row.node.id)} />
                    </TableCell>
                  )}
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>

      <div className="space-y-2 md:hidden">
        {rows.map((row) => {
          const nameId = `${ids}-card-${row.node.id}`
          const { cells } = row
          return (
            <div key={row.node.id} data-testid={`agent-card-${row.node.id}`} className="space-y-3 rounded-2xl bg-card p-4 card-shadow">
              <div className="flex items-start gap-3">
                <div className="pt-0.5">{pick(row, nameId)}</div>
                <ServerName row={row} leaderId={leaderId} id={nameId} />
              </div>
              {cells ? (
                <div className="space-y-3">
                  {AGENTS.map((agent) => (
                    <div key={agent} className="space-y-1.5">
                      <p className="text-[12px] font-semibold text-muted-foreground">{agentLabel(agent)}</p>
                      <AgentCellView cell={cells[agent]} />
                    </div>
                  ))}
                </div>
              ) : (
                <NodeState online={row.online} load={row.load} onRetry={() => retry(row.node.id)} />
              )}
            </div>
          )
        })}
      </div>

      {/* "Matches" compares addresses; these two take effect only at enrollment. */}
      <p className="text-[12px] text-muted-foreground">{t('agents.settings.enrollNote')}</p>
    </section>
  )
}
