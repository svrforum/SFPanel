import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, Download, Loader2, Radar, Square } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { canScan, defaultScanner, prefixOf, type NetworkEntry } from '@/lib/netmap'

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
/** How often a node may scan by itself, in minutes; 0 is never. */
const INTERVALS = [0, 15, 60, 360, 1440] as const

/**
 * A node's own scanning of the network at intervals: off unless set. It looks
 * for devices only; ports, once a day, are a choice of their own. The node
 * stops by itself while an address of the network is in conflict — a scan
 * there can cut the scanning server off from the address's real owner.
 */
function Schedule({ entry, nodeId, nodeName, onSchedule }: {
  entry: NetworkEntry
  nodeId: string
  nodeName: string
  onSchedule: (nodeId: string, intervalMinutes: number, portsDaily: boolean) => Promise<string | null>
}) {
  const { t, i18n } = useTranslation()
  const id = useId()
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState<string | null>(null)
  // What was just chosen, shown until the node has answered: a box that
  // waits for the answer before it is ticked reads as a box that did not take.
  const [chosen, setChosen] = useState<{ minutes: number; portsDaily: boolean } | null>(null)
  const mine = entry.schedules.find((x) => x.nodeId === nodeId)
  const others = entry.schedules.filter((x) => x.nodeId !== nodeId)
  const interval = chosen?.minutes ?? mine?.intervalMinutes ?? 0
  const daily = chosen?.portsDaily ?? mine?.portsDaily ?? false
  const set = async (minutes: number, portsDaily: boolean) => {
    setChosen({ minutes, portsDaily })
    setBusy(true)
    setProblem(null)
    setProblem(await onSchedule(nodeId, minutes, portsDaily))
    setChosen(null)
    setBusy(false)
  }
  const time = (ms: number) => new Date(ms).toLocaleTimeString(i18n.language, { hour: '2-digit', minute: '2-digit' })
  return (
    <div className="space-y-1" data-testid="net-schedule">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <label htmlFor={`${id}-interval`} className="flex items-center gap-1.5 text-[13px]">
          <span className="text-muted-foreground">{t('running.network.schedule.label', { node: nodeName })}</span>
          <select
            id={`${id}-interval`}
            value={interval}
            disabled={busy}
            onChange={(e) => void set(Number(e.target.value), daily)}
            className="h-9 rounded-xl border-0 bg-secondary px-2 text-base focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:text-[13px]"
          >
            {INTERVALS.map((m) => <option key={m} value={m}>{t(`running.network.schedule.every.${m}`)}</option>)}
          </select>
        </label>
        {interval > 0 && (
          <label htmlFor={`${id}-daily`} className="flex min-h-9 cursor-pointer items-center gap-1.5 text-[13px]">
            <input id={`${id}-daily`} type="checkbox" checked={daily} disabled={busy} onChange={(e) => void set(interval, e.target.checked)} className="size-4 accent-[var(--primary)]" />
            {t('running.network.schedule.portsDaily')}
          </label>
        )}
      </div>
      {mine && (mine.pausedReason
        ? <p role="status" className="text-xs font-medium text-warning">{t(`running.network.schedule.paused.${mine.pausedReason}`, { defaultValue: t('running.network.schedule.paused.other') })}</p>
        : mine.nextRun > 0 && <p className="text-xs text-muted-foreground">{t(mine.portsDaily ? 'running.network.schedule.nextDaily' : 'running.network.schedule.next', { time: time(mine.nextRun) })}</p>)}
      {others.map((x) => (
        <p key={x.nodeId} className="text-xs text-muted-foreground">
          {t('running.network.schedule.other', { node: x.nodeName, interval: t(`running.network.schedule.every.${x.intervalMinutes}`, { defaultValue: String(x.intervalMinutes) }) })}
        </p>
      ))}
      {problem && <p role="alert" className="break-words text-xs text-destructive">{t('running.network.schedule.failed')} {problem}</p>}
    </div>
  )
}

export function ScanControl({ entry, onStart, onCancel, onInstall, onSchedule }: {
  entry: NetworkEntry
  onStart: (nodeId: string, network: string, ports: boolean) => Promise<string | null>
  onCancel: (nodeId: string) => Promise<void>
  /** Installs nmap on the node; resolves to what went wrong, or null. */
  onInstall: (nodeId: string) => Promise<string | null>
  onSchedule: (nodeId: string, intervalMinutes: number, portsDaily: boolean) => Promise<string | null>
}) {
  const { t } = useTranslation()
  const id = useId()
  const [picked, setPicked] = useState<string | null>(null)
  const [ports, setPorts] = usePortsChoice()
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState<string | null>(null)
  const [installProblem, setInstallProblem] = useState<string | null>(null)
  // The server nmap was just installed on. The button pressed went with the
  // install, which left the focus on the page's body: it moves to the scan
  // the install made possible, which says what happened.
  const [installedOn, setInstalledOn] = useState<string | null>(null)
  const focusScan = useRef(false)
  const scanButton = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (!focusScan.current) return
    focusScan.current = false
    scanButton.current?.focus()
  })

  if (entry.running) {
    const run = entry.running
    return (
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <p role="status" className="flex items-center gap-1.5 text-[13px]">
          <Radar className="size-4 animate-pulse text-primary motion-reduce:animate-none" aria-hidden="true" />
          <span>{t(run.scheduled ? 'running.network.scan.runningScheduled' : run.ports ? 'running.network.scan.runningPorts' : 'running.network.scan.runningHosts', { node: run.nodeName })}</span>
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
    // Installed here, on the node that lacks it: once it has nmap, its next read offers the scan.
    const install = async (nodeId: string, nodeName: string) => {
      setBusy(true)
      setInstallProblem(null)
      const failed = await onInstall(nodeId)
      setInstallProblem(failed)
      if (failed === null) {
        setInstalledOn(nodeName)
        focusScan.current = true
      }
      setBusy(false)
    }
    return (
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px] text-muted-foreground">
        {noTool ? (
          <>
            <p>{t('running.network.scan.noTool', { node: noTool.nodeName })}</p>
            <Button variant="outline" size="sm" className="rounded-xl" disabled={busy} onClick={() => void install(noTool.nodeId, noTool.nodeName)}>
              {busy ? <Loader2 className="animate-spin" aria-hidden="true" /> : <Download aria-hidden="true" />}
              {t('running.network.scan.installNmap')}
            </Button>
            {installProblem && <p role="alert" className="basis-full break-words text-xs text-destructive">{t('running.network.scan.installFailed')} {installProblem}</p>}
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
    setInstalledOn(null)
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
        <Button ref={scanButton} size="sm" className="rounded-xl" onClick={() => void start()} disabled={busy} aria-describedby={installedOn ? `${id}-installed` : undefined}>
          <Radar aria-hidden="true" />
          {entry.segment ? t('running.network.scan.again') : t('running.network.scan.start')}
        </Button>
      </div>
      {installedOn && <p id={`${id}-installed`} role="status" className="text-xs text-success">{t('running.network.scan.installed', { node: installedOn })}</p>}
      <p id={`${id}-hint`} className="text-xs text-muted-foreground">
        {t(ports ? 'running.network.scan.hintPorts' : 'running.network.scan.hintHosts', { node: chosen.nodeName })}
      </p>
      {problem && <p role="alert" className="break-words text-xs text-destructive">{t('running.network.scan.startFailed')} {problem}</p>}
      {/* Not refused: said. A scan of a network with a duplicated address can turn the scanning server's own table against it. */}
      {(entry.segment?.conflicts.length ?? 0) > 0 && (
        <p className="flex items-start gap-1 text-xs text-warning" data-testid="net-scan-caution">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          <span>{t('running.network.scan.conflictCaution', { node: chosen.nodeName })}</span>
        </p>
      )}
      {chosen.registry && <Schedule entry={entry} nodeId={chosen.nodeId} nodeName={chosen.nodeName} onSchedule={onSchedule} />}
    </div>
  )
}
