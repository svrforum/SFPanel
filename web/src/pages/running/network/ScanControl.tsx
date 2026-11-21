import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Radar, Square } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { canScan, defaultScanner, prefixOf, type NetworkEntry } from '@/lib/netmap'
import { NodeLink } from '../NodeLink'
import type { Scope } from '../targets'

const PORTS_KEY = 'sfpanel-netmap-ports'

/** Whether a scan also looks for open ports: on unless the operator turned it off, kept across visits. */
function usePortsChoice(): [boolean, (on: boolean) => void] {
  const [on, setOn] = useState(() => {
    try {
      return localStorage.getItem(PORTS_KEY) !== 'off'
    } catch {
      return true
    }
  })
  const choose = (next: boolean) => {
    setOn(next)
    try {
      localStorage.setItem(PORTS_KEY, next ? 'on' : 'off')
    } catch {
      // Storage refused: the choice holds for this visit.
    }
  }
  return [on, choose]
}

/** Seconds since the page first saw the scan running: the browser's own clock, so a node's clock that is off shows no nonsense. */
function Elapsed() {
  const { t } = useTranslation()
  const [seconds, setSeconds] = useState(0)
  useEffect(() => {
    const since = Date.now()
    const timer = setInterval(() => setSeconds(Math.floor((Date.now() - since) / 1000)), 1000)
    return () => clearInterval(timer)
  }, [])
  return <span className="tabular-nums">{t('running.network.scan.elapsed', { count: seconds })}</span>
}

/**
 * Starting a scan of one network, or stopping the one that runs: which node
 * scans (where more than one can), whether ports are looked for, and why not
 * where nobody can.
 */
export function ScanControl({ entry, scope, localId, onStart, onCancel }: {
  entry: NetworkEntry
  scope: Scope
  localId: string
  onStart: (nodeId: string, network: string, ports: boolean) => Promise<string | null>
  onCancel: (nodeId: string) => Promise<void>
}) {
  const { t } = useTranslation()
  const id = useId()
  const [picked, setPicked] = useState<string | null>(null)
  const [ports, setPorts] = usePortsChoice()
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState<string | null>(null)

  if (entry.running) {
    const run = entry.running
    return (
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <p role="status" className="flex items-center gap-1.5 text-[13px]">
          <Radar className="size-4 animate-pulse text-primary motion-reduce:animate-none" aria-hidden="true" />
          <span>{t(run.ports ? 'running.network.scan.runningPorts' : 'running.network.scan.runningHosts', { node: run.nodeName })}</span>
          <Elapsed />
        </p>
        <Button variant="outline" size="sm" className="rounded-xl" onClick={() => void onCancel(run.nodeId)}>
          <Square aria-hidden="true" />
          {t('running.network.scan.stop')}
        </Button>
      </div>
    )
  }

  const able = entry.scanners.filter(canScan)
  const chosen = able.find((s) => s.nodeId === picked) ?? defaultScanner(entry)
  if (!chosen) {
    const online = entry.scanners.filter((s) => s.online)
    const noTool = online.find((s) => s.scannable && !s.installed)
    const tooLarge = online.length > 0 && online.every((s) => !s.scannable)
    return (
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px] text-muted-foreground">
        {noTool ? (
          <>
            <p>{t('running.network.scan.noTool', { node: noTool.nodeName })}</p>
            <NodeLink
              target={{ to: '/packages', onNode: scope === 'cluster' }}
              nodeId={noTool.nodeId}
              localId={localId}
              className="inline-flex min-h-11 items-center rounded-md px-1 font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {t('running.network.scan.openPackages')}
            </NodeLink>
          </>
        ) : (
          <p>{tooLarge ? t('running.network.scan.tooLarge', { prefix: prefixOf(entry.cidr) }) : t('running.network.scan.nobody')}</p>
        )}
      </div>
    )
  }

  const start = async () => {
    setBusy(true)
    setProblem(null)
    const failed = await onStart(chosen.nodeId, chosen.network, ports)
    setProblem(failed)
    setBusy(false)
  }
  return (
    <div className="space-y-1">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        {able.length > 1 && (
          <label className="flex items-center gap-1.5 text-[13px]">
            <span className="text-muted-foreground">{t('running.network.scan.from')}</span>
            <select
              value={chosen.nodeId}
              onChange={(e) => setPicked(e.target.value)}
              className="h-9 rounded-xl border-0 bg-secondary px-2 text-base focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:text-[13px]"
            >
              {able.map((s) => <option key={s.nodeId} value={s.nodeId}>{s.nodeName}</option>)}
            </select>
          </label>
        )}
        <label htmlFor={`${id}-ports`} className="flex min-h-9 cursor-pointer items-center gap-1.5 text-[13px]">
          <input
            id={`${id}-ports`}
            type="checkbox"
            checked={ports}
            onChange={(e) => setPorts(e.target.checked)}
            className="size-4 accent-[var(--primary)]"
            aria-describedby={`${id}-hint`}
          />
          {t('running.network.scan.withPorts')}
        </label>
        <Button size="sm" className="rounded-xl" onClick={() => void start()} disabled={busy}>
          <Radar aria-hidden="true" />
          {entry.segment ? t('running.network.scan.again') : t('running.network.scan.start')}
        </Button>
      </div>
      <p id={`${id}-hint`} className="text-xs text-muted-foreground">
        {t(ports ? 'running.network.scan.hintPorts' : 'running.network.scan.hintHosts', { node: chosen.nodeName })}
      </p>
      {problem && <p role="alert" className="break-words text-xs text-destructive">{t('running.network.scan.startFailed')} {problem}</p>}
    </div>
  )
}
