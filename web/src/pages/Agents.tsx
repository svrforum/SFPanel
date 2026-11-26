import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Navigate } from 'react-router-dom'
import { RefreshCw } from 'lucide-react'
import { ListLoadState } from '@/components/ListLoadState'
import { Button } from '@/components/ui/button'
import { useServerScope } from '@/hooks/useServerScope'
import { api } from '@/lib/api'
import type { AgentSettingsView } from '@/types/api'
import { AgentServerTable } from './agents/AgentServerTable'
import { AgentSettingsCard } from './agents/AgentSettingsCard'
import { reapplyNodes } from './agents/agentsModel'
import { useAgentStatuses } from './agents/useAgentStatuses'

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

/** The cluster's connection values, from the manager this browser is attached to. */
function useAgentSettings() {
  const [view, setView] = useState<AgentSettingsView | null>(null)
  const [error, setError] = useState<string | null>(null)
  const load = useCallback(() => {
    api.getAgentSettings()
      .then((v) => { setView(v); setError(null) })
      .catch((err: unknown) => setError(message(err)))
  }, [])
  useEffect(() => { load() }, [load])
  return { view, error, reload: load, set: setView }
}

// Zabbix and Wazuh on every server of the cluster: the values they connect
// with, set once here, and whether each really reaches its server, read on
// the server itself. Deploying is a group task run, opened with the servers.
export default function Agents() {
  const { clusterEnabled } = useServerScope()
  // A single server has no fleet to deploy to: the cluster page offers to make one.
  if (clusterEnabled === false) return <Navigate to="/cluster/overview" replace />
  return <AgentsPage />
}

function AgentsPage() {
  const { t } = useTranslation()
  const { nodes, leaderId, loads, overviewError, refresh, retry } = useAgentStatuses()
  const settings = useAgentSettings()

  // Re-applying reaches the servers last seen online with the agent as its package.
  const reapply = useMemo(() => {
    const rows = nodes.map((n) => {
      const load = loads[n.id]
      return { id: n.id, status: n.status === 'online' && load?.phase === 'ready' ? load.status : undefined }
    })
    return { zabbix: reapplyNodes(rows, 'zabbix'), wazuh: reapplyNodes(rows, 'wazuh') }
  }, [nodes, loads])

  return (
    <div className="mx-auto max-w-[1400px] space-y-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-[22px] font-bold tracking-tight">{t('agents.title')}</h1>
          <p className="mt-1 break-words text-[13px] text-muted-foreground">{t('agents.subtitle')}</p>
        </div>
        <Button variant="outline" size="sm" className="rounded-xl" onClick={refresh}>
          <RefreshCw aria-hidden="true" />
          {t('common.refresh')}
        </Button>
      </header>

      <AgentSettingsCard settings={settings.view} loadError={settings.error} onRetry={settings.reload} onSaved={settings.set} reapply={reapply} />

      {nodes.length === 0 ? (
        <ListLoadState loading={!overviewError} error={overviewError} errorTitle={t('running.overviewError')} onRetry={refresh} skeletonCount={3} />
      ) : (
        <>
          {overviewError && <p role="alert" className="break-words text-[12px] text-destructive">{t('agents.readError', { error: overviewError })}</p>}
          <AgentServerTable nodes={nodes} leaderId={leaderId} loads={loads} settings={settings.view} retry={retry} />
        </>
      )}
    </div>
  )
}
