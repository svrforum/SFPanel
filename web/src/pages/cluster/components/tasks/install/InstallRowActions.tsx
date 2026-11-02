import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Clock, RotateCcw, Undo2 } from 'lucide-react'
import { api } from '@/lib/api'
import { openNode } from '@/lib/nodeSelection'
import { Button } from '@/components/ui/button'
import { CopyBlock } from '@/components/CopyBlock'
import { StatusPill } from '@/components/StatusPill'
import { useConfirm } from '@/components/ConfirmDialog'
import { useNow } from '@/hooks/useNow'
import type { RunNodeRecord, RunRecord } from '@/types/api'
import type { RunState } from '../taskRunProgress'
import { nodeStatusText, nodeStatusTone } from '../taskRunViewModel'
import { useMounted } from '../useMounted'
import {
  PANEL_START_COMMAND, cleanupCommand, clockText, credentialsGone, credentialsLeft, installReasonText, installRowActions,
  keptPanelNote, needsCleanup, rowActionErrorText, type KeptPanelNote,
} from './installRun'

interface InstallRowActionsProps {
  run: RunRecord
  state: RunState
  /** A row's name as the table shows it. */
  rowName: (key: string) => string
  /** This manager's node ID: opening it is opening the local node. */
  localId: string
}

// Below the table: how long the leader keeps the credentials a recheck or
// a revert needs, then, server by server, why it ended and what can be done
// about it — Recheck and Revert for one that registered and failed, Revert
// for one stopped after it registered, the command to clean one up by hand
// once nothing else will, Open node for one that became an agent. A server
// that had SFPanel before is never given that command: it is told nothing
// was removed, and how to start its panel if it may be stopped.
export function InstallRowActions({ run, state, rowName, localId }: InstallRowActionsProps) {
  const { t } = useTranslation()
  const now = useNow(1000)
  const left = credentialsLeft(run.credentials_until, now)
  const gone = credentialsGone(run, now)
  const version = run.inputs?.version ?? ''

  const entries = run.nodes.flatMap((key) => {
    const record = state.nodes[key]
    const reason = installReasonText(record, t)
    const can = installRowActions(run, record)
    const cleanup = needsCleanup(record, gone)
    const kept = keptPanelNote(record, t)
    return reason || cleanup || kept || can.recheck || can.revert || can.openNode ? [{ key, record, reason, can, cleanup, kept }] : []
  })
  const anyCleanup = entries.some((e) => e.cleanup)
  if (entries.length === 0 && left === null) return null

  return (
    <div className="space-y-2">
      {(left !== null || (gone && anyCleanup)) && (
        <p data-testid="install-credentials" className="flex items-start gap-2 text-[13px] text-muted-foreground">
          <Clock className="mt-0.5 h-4 w-4 shrink-0" />
          <span className="break-words">
            {left !== null && left > 0
              ? t('cluster.tasks.install.run.credentialsLeft', { left: clockText(left) })
              : t('cluster.tasks.install.run.credentialsGone')}
          </span>
        </p>
      )}
      {entries.length > 0 && (
        <ul className="space-y-2">
          {entries.map(({ key, record, reason, can, cleanup, kept }) => (
            <RowEntry
              key={key}
              runId={run.id}
              rowKey={key}
              name={rowName(key)}
              record={record}
              reason={reason}
              can={can}
              gone={gone}
              cleanup={cleanup ? cleanupCommand(version) : null}
              kept={kept}
              localId={localId}
            />
          ))}
        </ul>
      )}
    </div>
  )
}

interface RowEntryProps {
  runId: string
  rowKey: string
  name: string
  record: RunNodeRecord | undefined
  reason: string | null
  can: ReturnType<typeof installRowActions>
  gone: boolean
  cleanup: string | null
  kept: KeptPanelNote | null
  localId: string
}

// A server's line: a refusal of Recheck or Revert reads as a sentence here,
// and nothing is sent again by itself.
function RowEntry({ runId, rowKey, name, record, reason, can, gone, cleanup, kept, localId }: RowEntryProps) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const navigate = useNavigate()
  const mounted = useMounted()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const status = record?.status ?? 'pending'
  const nodeId = record?.node_id

  const send = async (call: () => Promise<unknown>) => {
    setBusy(true)
    setError(null)
    try {
      await call()
    } catch (err) {
      if (mounted.current) setError(rowActionErrorText(err, record, t))
    } finally {
      if (mounted.current) setBusy(false)
    }
  }

  const revert = async () => {
    const yes = await confirm({
      title: t('cluster.tasks.install.run.revertTitle', { server: name }),
      description: record?.sfpanel_before
        ? t('cluster.tasks.install.run.revertDescriptionKept', { server: name })
        : gone
          ? t('cluster.tasks.install.run.revertDescriptionGone', { server: name })
          : t('cluster.tasks.install.run.revertDescription', { server: name }),
      confirmLabel: t('cluster.tasks.install.run.revert'),
      danger: true,
    })
    if (yes) await send(() => api.revertAgentInstall(runId, rowKey))
  }

  return (
    <li data-testid={`install-row-${rowKey}`} className="rounded-xl border border-border p-3 space-y-2 min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 break-all text-[13px] font-semibold">{name}</span>
        <StatusPill tone={nodeStatusTone(status)}>{nodeStatusText(status, t)}</StatusPill>
        {status === 'succeeded' && nodeId && (
          <span className="text-[12px] text-muted-foreground">{t('cluster.tasks.install.run.online')}</span>
        )}
      </div>
      {/* On a server that succeeded, a reason is a warning: its group label is missing. */}
      {reason && (
        <p className={status === 'succeeded' ? 'text-[13px] text-warning break-words' : 'text-[13px] text-muted-foreground break-words'}>
          {reason}
        </p>
      )}
      {(can.recheck || can.revert || can.openNode) && (
        <div className="flex flex-wrap gap-2">
          {can.recheck && (
            <Button size="sm" variant="outline" disabled={busy} onClick={() => send(() => api.recheckAgentInstall(runId, rowKey))}>
              <RotateCcw className="h-4 w-4" />
              {t('cluster.tasks.install.run.recheck')}
            </Button>
          )}
          {can.revert && (
            <Button size="sm" variant="outline" disabled={busy} onClick={revert}>
              <Undo2 className="h-4 w-4" />
              {t('cluster.tasks.install.run.revert')}
            </Button>
          )}
          {can.openNode && nodeId && (
            <Button size="sm" variant="outline" onClick={() => openNode(nodeId, localId, navigate)}>
              {t('cluster.nodes.open')}
            </Button>
          )}
        </div>
      )}
      {error && <p role="alert" className="rounded-lg bg-destructive/10 px-3 py-2 text-[13px] text-destructive break-words">{error}</p>}
      {cleanup && (
        <div className="space-y-1.5">
          <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.install.run.cleanup')}</p>
          <CopyBlock text={cleanup} />
        </div>
      )}
      {kept && (
        <div data-testid="install-kept" className="space-y-1.5">
          <p className="text-[12px] text-muted-foreground break-words">{kept.text}</p>
          {kept.start && (
            <>
              <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.install.run.panelStart')}</p>
              <CopyBlock text={PANEL_START_COMMAND} />
            </>
          )}
        </div>
      )}
    </li>
  )
}
