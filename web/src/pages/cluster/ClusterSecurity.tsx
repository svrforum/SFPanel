import { useCallback, useId, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { AlertTriangle, Bot, KeyRound, Network, Server, ShieldCheck } from 'lucide-react'
import { api } from '@/lib/api'
import type { CANodeView, ClusterRotationStatus, ClusterStatus, RotationAgent, RotationManager } from '@/types/api'
import {
  DEFAULT_PHASE_TIMEOUT,
  PHASE_TIMEOUTS,
  caActive,
  pinRole,
  rotationFailure,
  shortPin,
  type RotationFailure,
} from '@/lib/clusterRotation'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { useConfirm } from '@/components/ConfirmDialog'
import { ListLoadState } from '@/components/ListLoadState'
import { StatusPill } from '@/components/StatusPill'
import { TypeToConfirmDialog } from '@/components/TypeToConfirmDialog'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { KindBadge } from './components/KindBadge'
import { RotationCard, RotationFailureNote, type RotationColumn } from './components/RotationCard'
import { CARotationStepper } from './components/CARotationStepper'

type Secret = 'jwt' | 'proxy' | 'agentKeys' | 'ca'

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

// The cluster's four secrets and where each rotation stands, from the leader's
// GET /cluster/rotation every five seconds. Starting one is a confirmation
// away; the leader's own gate decides, and a refusal stays on the card with
// whom it waits for. Rotations run on the leader whichever manager this
// browser is on: /cluster/* is never sent to a chosen node.
export default function ClusterSecurity() {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const [status, setStatus] = useState<ClusterStatus | null>(null)
  const [rotation, setRotation] = useState<ClusterRotationStatus | null>(null)
  // The last poll's failure: with a state already shown it stays, under a warning.
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<Secret | 'abort' | null>(null)
  const [failures, setFailures] = useState<Partial<Record<Secret, RotationFailure>>>({})
  const [caOpen, setCaOpen] = useState(false)
  const polling = useRef(false)

  const load = useCallback(() => {
    if (polling.current) return
    polling.current = true
    Promise.allSettled([api.getClusterStatus(), api.getClusterRotation()])
      .then(([s, r]) => {
        if (s.status === 'fulfilled') setStatus(s.value)
        if (r.status === 'fulfilled') {
          setRotation(r.value)
          setError(null)
        } else {
          setError(message(r.reason))
        }
      })
      .finally(() => {
        polling.current = false
        setLoading(false)
      })
  }, [])

  useVisibleInterval(load, 5000)

  const fail = (secret: Secret, err: unknown) => setFailures((f) => ({ ...f, [secret]: rotationFailure(err) }))
  const clear = (secret: Secret) => setFailures((f) => ({ ...f, [secret]: undefined }))

  const rotate = async (secret: 'jwt' | 'proxy' | 'agentKeys') => {
    const ask = {
      jwt: { title: t('cluster.security.jwt'), description: t('cluster.security.jwtConfirm'), danger: true },
      proxy: { title: t('cluster.security.proxy'), description: t('cluster.security.proxyConfirm') },
      agentKeys: { title: t('cluster.security.agentKeys'), description: t('cluster.security.agentKeysConfirm') },
    }[secret]
    if (!(await confirm({ ...ask, confirmLabel: t('cluster.security.rotate') }))) return
    setBusy(secret)
    clear(secret)
    try {
      if (secret === 'jwt') {
        await api.rotateClusterJWT()
        // The leader ended every session, this one with it: the answer was its last.
        toast.success(t('cluster.security.jwtDone'))
        api.clearToken()
        navigate('/login', { replace: true })
        return
      }
      await (secret === 'proxy' ? api.rotateClusterProxy() : api.rotateClusterAgentKeys())
      // 202: the leader moves it on from here; the next poll shows its phase.
      load()
    } catch (err) {
      fail(secret, err)
    } finally {
      setBusy(null)
    }
  }

  const abort = async () => {
    const ok = await confirm({
      title: t('cluster.security.abort'),
      description: t('cluster.security.abortConfirm'),
      confirmLabel: t('cluster.security.abort'),
      danger: true,
    })
    if (!ok) return
    setBusy('abort')
    clear('ca')
    try {
      await api.abortClusterCARotation()
      load()
    } catch (err) {
      // Refused between two polls, the rotation runs on: it was not stopped,
      // which is not the same as not started.
      setFailures((f) => ({ ...f, ca: { ...rotationFailure(err), title: t('cluster.security.abortRefused') } }))
    } finally {
      setBusy(null)
    }
  }

  if (status?.enabled === false) {
    return (
      <div className="bg-card rounded-2xl p-8 card-shadow text-center">
        <Server className="h-12 w-12 text-muted-foreground mx-auto mb-3" />
        <p className="text-[13px] text-muted-foreground">{t('cluster.notEnabled.title')}</p>
      </div>
    )
  }

  if (rotation === null) {
    return (
      <ListLoadState
        loading={loading}
        error={loading ? null : error ?? ''}
        errorTitle={t('cluster.security.loadError')}
        onRetry={load}
        skeletonCount={4}
      />
    )
  }

  const when = (iso?: string) => (iso ? new Date(iso).toLocaleString(i18n.language) : '')
  const generation = (n: number) => <span>{t('cluster.security.generation', { generation: n })}</span>
  const rotateButton = (secret: Secret, onClick: () => void, disabled = false) => (
    <Button
      size="sm"
      variant={secret === 'jwt' || secret === 'ca' ? 'destructive' : 'outline'}
      className="rounded-xl"
      onClick={onClick}
      loading={busy === secret}
      disabled={disabled || (busy !== null && busy !== secret)}
    >
      {t('cluster.security.rotate')}
    </Button>
  )
  const managerGeneration: RotationColumn<RotationManager> = {
    header: t('cluster.security.generationColumn'),
    // A manager whose heartbeat has not said is not assumed to be on any one.
    cell: (m) => <span className="font-mono">{m.generation ?? '—'}</span>,
  }
  const agentKey: RotationColumn<RotationAgent> = {
    header: t('cluster.security.newKey'),
    cell: (a) =>
      a.pending ? (
        <StatusPill tone="warning">{a.issued ? t('cluster.security.keyIssued') : t('cluster.security.keyPending')}</StatusPill>
      ) : (
        <StatusPill tone="success">{t('cluster.security.keyMoved')}</StatusPill>
      ),
  }

  const ca = rotation.ca
  const caRot = ca.rotation
  // A CA by which of the rotation's two it is; any other by its short pin.
  const pin = (p: string, testId?: string) => {
    const role = p && caRot ? pinRole(p, caRot) : null
    return (
      <span data-testid={testId}>
        {!p ? '—' : role ? (
          <StatusPill tone={role === 'next' ? 'primary' : role === 'current' ? 'secondary' : 'muted'}>
            {{ current: t('cluster.security.currentCA'), next: t('cluster.security.nextCA'), previous: t('cluster.security.previousCA') }[role]}
          </StatusPill>
        ) : (
          <span className="font-mono text-[12px]" title={p}>{shortPin(p)}</span>
        )}
      </span>
    )
  }
  const caColumns: RotationColumn<CANodeView>[] = [
    { header: t('cluster.nodes.kind'), cell: (n) => <KindBadge kind={n.kind === 'agent' ? 'agent' : 'manager'} /> },
    {
      header: t('cluster.security.trusts'),
      cell: (n) => (n.trusts.length ? <span className="inline-flex flex-wrap gap-1">{n.trusts.map((p) => <span key={p}>{pin(p)}</span>)}</span> : '—'),
    },
    { header: t('cluster.security.certCA'), cell: (n) => pin(n.cert_ca, 'cert-ca') },
  ]

  return (
    <div className="space-y-4">
      <p className="text-[13px] text-muted-foreground max-w-3xl">{t('cluster.security.intro')}</p>

      {error && (
        <div role="status" className="bg-warning/10 text-warning rounded-xl p-3 flex items-start gap-2">
          <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" aria-hidden />
          <div className="min-w-0">
            <p className="text-[13px] font-medium">{t('cluster.security.stale')}</p>
            <p className="text-[12px] opacity-80 mt-0.5 break-words">{error}</p>
          </div>
        </div>
      )}

      <RotationCard
        id="jwt"
        icon={KeyRound}
        title={t('cluster.security.jwt')}
        summary={
          <>
            {generation(rotation.jwt.generation)}
            {rotation.jwt.rotated_at && <span>· {t('cluster.security.rotatedAt', { time: when(rotation.jwt.rotated_at) })}</span>}
          </>
        }
        action={rotateButton('jwt', () => rotate('jwt'))}
        failure={failures.jwt}
        columns={[managerGeneration]}
        rows={rotation.jwt.managers ?? []}
      />

      <RotationCard
        id="proxy"
        icon={Network}
        title={t('cluster.security.proxy')}
        summary={
          <>
            {generation(rotation.proxy.generation)}
            {rotation.proxy.phase && (
              <span className="inline-flex items-center gap-1.5">
                · {t('cluster.security.phase')}
                <span data-testid="phase">
                  <StatusPill tone={rotation.proxy.phase === 'done' ? 'success' : 'primary'} className="font-mono">
                    {rotation.proxy.phase}
                  </StatusPill>
                </span>
              </span>
            )}
          </>
        }
        action={rotateButton('proxy', () => rotate('proxy'))}
        failure={failures.proxy}
        waiting={rotation.proxy.phase === 'next' ? rotation.proxy.waiting : undefined}
        columns={[managerGeneration]}
        rows={rotation.proxy.managers ?? []}
      />

      <RotationCard
        id="agent-keys"
        icon={Bot}
        title={t('cluster.security.agentKeys')}
        summary={
          <>
            {generation(rotation.agent_keys.generation)}
            {rotation.agent_keys.generation > 0 && (
              rotation.agent_keys.pending > 0 ? (
                <StatusPill tone="warning">{t('cluster.security.pendingAgents', { count: rotation.agent_keys.pending })}</StatusPill>
              ) : (
                <StatusPill tone="success">{t('cluster.security.allMoved')}</StatusPill>
              )
            )}
          </>
        }
        action={rotateButton('agentKeys', () => rotate('agentKeys'))}
        failure={failures.agentKeys}
        columns={[agentKey]}
        rows={rotation.agent_keys.agents ?? []}
      />

      <RotationCard
        id="ca"
        icon={ShieldCheck}
        title={t('cluster.security.ca')}
        summary={
          <span className="inline-flex items-center gap-1.5">
            {t('cluster.security.fingerprint')}
            <span className="font-mono text-[12px] text-foreground" title={ca.current_pin}>{shortPin(ca.current_pin)}</span>
          </span>
        }
        // One rotation at a time: the leader refuses a second, going back included.
        action={rotateButton('ca', () => { clear('ca'); setCaOpen(true) }, caActive(caRot?.phase))}
        failure={failures.ca}
        waiting={caRot?.waiting}
        columns={caColumns}
        rows={caRot?.nodes ?? []}
      >
        {!ca.raft_tls && <p className="text-[13px] text-muted-foreground">{t('cluster.security.raftNoTLS')}</p>}
        {caRot ? (
          <CARotationStepper rotation={caRot} onAbort={abort} aborting={busy === 'abort'} />
        ) : (
          <p className="text-[13px] text-muted-foreground">{t('cluster.security.caNever')}</p>
        )}
      </RotationCard>

      <StartCADialog
        open={caOpen}
        onOpenChange={setCaOpen}
        clusterName={status?.name ?? ''}
        onStarted={load}
      />
    </div>
  )
}

// Starting a CA rotation asks for the cluster's name, the way disbanding it
// does: it reaches every node, and past promote it cannot be undone. A
// refusal stays in the dialog with the leader's reason.
function StartCADialog({ open, onOpenChange, clusterName, onStarted }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  clusterName: string
  onStarted: () => void
}) {
  const { t } = useTranslation()
  const timeoutId = useId()
  const [phaseTimeout, setPhaseTimeout] = useState<string>(DEFAULT_PHASE_TIMEOUT)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<RotationFailure | null>(null)

  // Each opening starts from the default and without the last refusal (TypeToConfirmDialog's pattern).
  const [prevOpen, setPrevOpen] = useState(open)
  if (prevOpen !== open) {
    setPrevOpen(open)
    setPhaseTimeout(DEFAULT_PHASE_TIMEOUT)
    setFailure(null)
  }

  const label = {
    '15m': t('cluster.security.minutes', { count: 15 }),
    '30m': t('cluster.security.minutes', { count: 30 }),
    '1h': t('cluster.security.hours', { count: 1 }),
    '2h': t('cluster.security.hours', { count: 2 }),
  }

  const start = async () => {
    setBusy(true)
    setFailure(null)
    try {
      await api.rotateClusterCA(phaseTimeout)
      onOpenChange(false)
      onStarted()
    } catch (err) {
      setFailure(rotationFailure(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <TypeToConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('cluster.security.ca')}
      description={t('cluster.security.caConfirm')}
      confirmPhrase={clusterName}
      confirmLabel={t('cluster.security.rotate')}
      prompt={t('cluster.security.typeName')}
      loading={busy}
      onConfirm={start}
    >
      <div className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <label htmlFor={timeoutId} className="text-[13px] font-medium">{t('cluster.security.phaseTimeout')}</label>
          <Select value={phaseTimeout} onValueChange={setPhaseTimeout}>
            <SelectTrigger id={timeoutId} size="sm" className="rounded-xl text-[13px] min-w-[140px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PHASE_TIMEOUTS.map((d) => <SelectItem key={d} value={d}>{label[d]}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
        {failure && <RotationFailureNote failure={failure} />}
      </div>
    </TypeToConfirmDialog>
  )
}
