import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Loader2, ArrowRightLeft } from 'lucide-react'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import type { ClusterNode, MigratePreflightReport, MigratePhaseEvent, MigrateDisposition } from '@/types/api'

const DISPOSITIONS: MigrateDisposition[] = ['retain', 'delete', 'clone']

export function MigrateStackDialog({
  open,
  onOpenChange,
  project,
  sourceNodeId,
  onMigrated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  project: string
  // The node the stack lives on. Defaults to the current node (detail-page use);
  // the cluster-wide view passes it explicitly so it can migrate any node's stack.
  sourceNodeId?: string
  // Fired once when a migration completes successfully, so the caller can refresh
  // its stack lists (the stack has moved nodes).
  onMigrated?: () => void
}) {
  const { t } = useTranslation()
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [sourceName, setSourceName] = useState('')
  const [targetId, setTargetId] = useState('')
  const [disposition, setDisposition] = useState<MigrateDisposition>('retain')
  const [overwriteAck, setOverwriteAck] = useState(false)
  const [report, setReport] = useState<MigratePreflightReport | null>(null)
  const [checking, setChecking] = useState(false)
  const [running, setRunning] = useState(false)
  const [events, setEvents] = useState<MigratePhaseEvent[]>([])
  const [terminal, setTerminal] = useState<MigratePhaseEvent | null>(null)

  useEffect(() => {
    if (!open) return
    setReport(null)
    setEvents([])
    setTerminal(null)
    setRunning(false)
    setTargetId('')
    setOverwriteAck(false)
    setDisposition('retain')
    ;(async () => {
      try {
        const [nodesRes, status] = await Promise.all([api.getClusterNodes(), api.getClusterStatus()])
        const sourceId = sourceNodeId ?? api.currentNode ?? status.local_id ?? ''
        setSourceName((nodesRes.nodes || []).find((n) => n.id === sourceId)?.name ?? '')
        const targets = (nodesRes.nodes || []).filter((n) => n.id !== sourceId && n.status === 'online')
        setNodes(targets)
        if (targets.length === 1) setTargetId(targets[0].id)
      } catch {
        toast.error(t('docker.migrate.loadNodesFailed'))
      }
    })()
  }, [open, t, sourceNodeId])

  const runPreflight = async () => {
    if (!targetId) return
    setChecking(true)
    setReport(null)
    try {
      setReport(await api.migratePreflight(project, { targetNodeId: targetId, overwriteAcked: overwriteAck }, sourceNodeId))
    } catch (e) {
      toast.error(String(e))
    } finally {
      setChecking(false)
    }
  }

  const start = async () => {
    if (!targetId) return
    setRunning(true)
    setEvents([])
    setTerminal(null)
    let sawTerminal = false
    try {
      await api.migrateStream(
        project,
        { targetNodeId: targetId, disposition, overwriteAcked: overwriteAck },
        (ev) => {
          setEvents((prev) => [...prev, ev])
          if (ev.done) {
            sawTerminal = true
            setTerminal(ev)
            // Only a 'done' phase means the stack actually landed on the target;
            // 'error'/'rollback' leave it on the source, so don't claim success.
            if (ev.phase === 'done') onMigrated?.()
          }
        },
        sourceNodeId,
      )
      // Stream ended without a terminal event (relay timeout / reset) — show a
      // definite end state instead of silently reverting to the form. The
      // migration itself continues detached on the server.
      if (!sawTerminal) {
        setTerminal({ phase: 'error', message: t('docker.migrate.streamEnded'), done: true })
      }
    } catch (e) {
      setTerminal({ phase: 'error', message: String(e), done: true })
    } finally {
      setRunning(false)
    }
  }

  const blocked = (report?.blocks?.length ?? 0) > 0
  // Require a pre-flight to have been run (and passed) before Start — otherwise
  // disk/arch/port blocks and binds/large-transfer/external-volume warnings are
  // only enforced server-side, after the source is already stopped. Changing the
  // target or overwrite ack clears `report`, forcing a fresh pre-flight.
  const canStart = !!targetId && !running && !blocked && report !== null
  const inForm = !running && !terminal

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!running) onOpenChange(o)
      }}
    >
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ArrowRightLeft className="h-4 w-4" />
            {t('docker.migrate.title', { stack: project })}
          </DialogTitle>
          <DialogDescription>{t('docker.migrate.desc')}</DialogDescription>
        </DialogHeader>

        {inForm && (
          <div className="space-y-4">
            {sourceName && (
              <p className="text-xs text-muted-foreground">{t('docker.migrate.fromNode', { node: sourceName })}</p>
            )}
            <div className="space-y-1.5">
              <Label htmlFor="migrate-target">{t('docker.migrate.target')}</Label>
              <select
                id="migrate-target"
                value={targetId}
                onChange={(e) => {
                  setTargetId(e.target.value)
                  setReport(null)
                }}
                className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm"
              >
                <option value="">{t('docker.migrate.selectTarget')}</option>
                {nodes.map((n) => (
                  <option key={n.id} value={n.id}>
                    {n.name}
                  </option>
                ))}
              </select>
              {nodes.length === 0 && (
                <p className="text-xs text-muted-foreground">{t('docker.migrate.noTargets')}</p>
              )}
            </div>

            <div className="space-y-1.5">
              <Label>{t('docker.migrate.disposition')}</Label>
              <div className="grid grid-cols-3 gap-2">
                {DISPOSITIONS.map((d) => (
                  <button
                    key={d}
                    type="button"
                    aria-pressed={disposition === d}
                    onClick={() => setDisposition(d)}
                    className={cn(
                      'rounded-lg border p-2 text-xs font-medium transition',
                      disposition === d ? 'border-primary bg-primary/10' : 'border-border hover:bg-accent',
                    )}
                  >
                    {t(`docker.migrate.disp.${d}`)}
                  </button>
                ))}
              </div>
              <p className="text-xs text-muted-foreground">{t(`docker.migrate.dispDesc.${disposition}`)}</p>
            </div>

            <label className="flex items-start gap-2 text-sm">
              <Checkbox
                checked={overwriteAck}
                onCheckedChange={(v) => {
                  setOverwriteAck(!!v)
                  setReport(null)
                }}
                className="mt-0.5"
              />
              <span className="text-muted-foreground">{t('docker.migrate.overwriteAck')}</span>
            </label>

            {report && (
              <div className="space-y-2 rounded-lg border p-3 text-xs">
                {report.blocks?.length > 0 && (
                  <div className="text-[#f04452]">
                    <div className="font-semibold">{t('docker.migrate.blocked')}</div>
                    {report.blocks.map((b, i) => (
                      <div key={i}>• {b.message}</div>
                    ))}
                  </div>
                )}
                {report.warnings?.length > 0 && (
                  <div className="text-[#f59e0b]">
                    <div className="font-semibold">{t('docker.migrate.warnings')}</div>
                    {report.warnings.map((w, i) => (
                      <div key={i}>• {w.message}</div>
                    ))}
                  </div>
                )}
                {report.blocks?.length === 0 && report.warnings?.length === 0 && (
                  <div className="text-[#00c471]">{t('docker.migrate.preflightOk')}</div>
                )}
              </div>
            )}
          </div>
        )}

        {terminal && (
          <div
            className={cn(
              'rounded-lg border p-3 text-sm',
              terminal.phase === 'error' && 'border-[#f04452]/30 bg-[#f04452]/10 text-[#f04452]',
              terminal.phase === 'rollback' && 'border-[#f59e0b]/30 bg-[#f59e0b]/10 text-[#f59e0b]',
              terminal.phase === 'done' && 'border-[#00c471]/30 bg-[#00c471]/10 text-[#00c471]',
            )}
          >
            {terminal.message}
          </div>
        )}

        {(running || terminal) && (
          <div className="max-h-72 space-y-1 overflow-auto rounded-lg border p-3 font-mono text-xs">
            {events.map((ev, i) => (
              <div
                key={i}
                className={cn(
                  ev.phase === 'error' && 'text-[#f04452]',
                  ev.phase === 'rollback' && 'text-[#f59e0b]',
                  ev.phase === 'done' && 'text-[#00c471]',
                )}
              >
                [{ev.phase}] {ev.message}
              </div>
            ))}
            {running && (
              <div className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="h-3 w-3 animate-spin" />
                {t('docker.migrate.running')}
              </div>
            )}
          </div>
        )}

        {inForm && !!targetId && report === null && !checking && (
          <p className="text-xs text-muted-foreground">{t('docker.migrate.preflightRequired')}</p>
        )}
        <DialogFooter>
          {inForm && (
            <>
              <Button variant="outline" onClick={runPreflight} disabled={!targetId || checking}>
                {checking && <Loader2 className="h-4 w-4 animate-spin" />}
                {t('docker.migrate.preflight')}
              </Button>
              <Button onClick={start} disabled={!canStart}>
                {t('docker.migrate.start')}
              </Button>
            </>
          )}
          {terminal && <Button onClick={() => onOpenChange(false)}>{t('common.close')}</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
