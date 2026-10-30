import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { AlertTriangle, CheckCircle2, Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import { detectJoined } from '@/lib/clusterNodes'
import { openNode } from '@/lib/nodeSelection'
import type { ClusterNode, ClusterTokenResponse } from '@/types/api'
import { useNow } from '@/hooks/useNow'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { CopyBlock } from '@/components/CopyBlock'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { NODE_NAME_PATTERN, agentJoinCommands } from '../joinCommands'
import { ManagerTokenSection } from './ManagerTokenSection'
import { PendingTokens } from './PendingTokens'

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

/** 01:02:03 — a token's lifetime runs to a day or more, so hours stay unbounded. */
function hms(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  return [Math.floor(s / 3600), Math.floor((s % 3600) / 60), s % 60].map((v) => String(v).padStart(2, '0')).join(':')
}

interface Minted {
  token: ClusterTokenResponse
  /** The nodes there were when the token came back; null when that list could not be had. */
  before: string[] | null
}

// The add-agent dialog. Each opening makes an agent token (the server's
// default lifetime), shows the two lines that join a server with it, and
// watches the node list until an agent that was not there turns up. Closing
// it leaves the token valid: a command copied now may be pasted later, and the
// pending list under "Add a manager instead" is where it is taken back.
export function AddAgentDialog({ open, onOpenChange, localId, onJoined }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  localId: string
  /** The agent is in: the page reloads its list and tells the shell. */
  onJoined: () => void
}) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="text-[15px]">{t('cluster.addAgent.title')}</DialogTitle>
          <DialogDescription className="text-[13px]">{t('cluster.addAgent.intro')}</DialogDescription>
        </DialogHeader>
        {/* Inside the content, so it mounts on every opening: a fresh token,
            a fresh baseline and no name left over from the last time. */}
        <AddAgentBody localId={localId} onJoined={onJoined} onClose={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

function AddAgentBody({ localId, onJoined, onClose }: { localId: string; onJoined: () => void; onClose: () => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const nameId = useId()
  const now = useNow(1000)

  const [attempt, setAttempt] = useState(0)
  const [token, setToken] = useState<ClusterTokenResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [asRoot, setAsRoot] = useState(false)
  const [nameInput, setNameInput] = useState('')
  const [joined, setJoined] = useState<ClusterNode | null>(null)
  const [tokensChanged, setTokensChanged] = useState(0)

  // One token per attempt. The request is kept across the effect's re-run, so
  // StrictMode's second mount waits for the same token instead of making
  // another one nobody will see.
  const minting = useRef<Promise<Minted> | null>(null)
  // The node ids the joined agent must not be among.
  const baseline = useRef<Set<string> | null>(null)
  const polling = useRef(false)

  useEffect(() => {
    let live = true
    minting.current ??= api.createClusterToken(undefined, 'agent').then(async (tok) => {
      let before: string[] | null = null
      try {
        before = ((await api.getClusterNodes()).nodes ?? []).map((n) => n.id)
      } catch {
        // The first poll that answers takes the baseline instead.
      }
      return { token: tok, before }
    })
    minting.current.then(
      ({ token: tok, before }) => {
        if (!live) return
        baseline.current = before ? new Set(before) : null
        setToken(tok)
        setTokensChanged((n) => n + 1)
      },
      (err) => { if (live) setError(message(err)) },
    )
    return () => { live = false }
  }, [attempt])

  const newToken = () => {
    minting.current = null
    baseline.current = null
    setToken(null)
    setError(null)
    setAttempt((n) => n + 1)
  }

  // An older leader ignores the kind it is asked for and makes a manager
  // token, which names no kind. In an agent's command it would make that
  // server a manager, holding the cluster's JWT key, CA key and admin account.
  const oldLeader = token !== null && token.kind !== 'agent'
  const agentToken = token !== null && !oldLeader
  const name = nameInput.trim()
  const nameOk = !name || NODE_NAME_PATTERN.test(name)
  const remaining = token ? Date.parse(token.expires_at) - now : 0
  const expired = agentToken && remaining <= 0

  // Every 3s while an agent token is out. A join just before the token runs
  // out still shows up, so expiry does not stop it.
  const poll = () => {
    if (!agentToken || joined || polling.current) return
    polling.current = true
    api.getClusterNodes()
      .then((res) => {
        const nodes = res.nodes ?? []
        if (!baseline.current) {
          baseline.current = new Set(nodes.map((n) => n.id))
          return
        }
        const found = detectJoined(baseline.current, nodes, nameOk && name ? name : undefined)
        if (found) {
          setJoined(found)
          onJoined()
        }
      })
      // A 503 while the managers elect a leader, or any other miss: the next
      // poll asks again.
      .catch(() => {})
      .finally(() => { polling.current = false })
  }
  useVisibleInterval(poll, 3000)

  const commands = agentToken && token
    ? agentJoinCommands(token.advertise_address || '<manager-ip>', token.grpc_port || 3629, token.token, token.ca_pin ?? '', asRoot, nameOk && name ? name : undefined)
    : null

  let main: ReactNode
  if (joined) {
    main = (
      <div role="status" className="rounded-xl bg-success/10 p-4 space-y-3">
        <p className="flex items-center gap-2 text-[13px] font-medium text-success">
          <CheckCircle2 className="h-4 w-4 shrink-0" />
          {t('cluster.addAgent.joined', { name: joined.name })}
        </p>
        <div className="flex gap-2">
          <Button
            className="rounded-xl"
            onClick={() => {
              onClose()
              openNode(joined.id, localId, navigate)
            }}
          >
            {t('cluster.nodes.open')}
          </Button>
          <Button variant="outline" className="rounded-xl" onClick={onClose}>{t('common.close')}</Button>
        </div>
      </div>
    )
  } else if (error) {
    main = (
      <div role="alert" className="rounded-xl bg-destructive/10 p-4 space-y-3">
        <p className="text-[13px] text-destructive break-words">{error}</p>
        <Button variant="outline" className="rounded-xl" onClick={newToken}>{t('cluster.addAgent.newToken')}</Button>
      </div>
    )
  } else if (!token) {
    main = (
      <div className="space-y-3" aria-busy="true">
        <Skeleton className="h-12 w-full rounded-xl" />
        <Skeleton className="h-12 w-full rounded-xl" />
      </div>
    )
  } else if (oldLeader) {
    main = (
      <div role="alert" className="rounded-xl bg-warning/10 text-warning p-4 flex items-start gap-2">
        <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" />
        <p className="text-[13px]">{t('cluster.addAgent.oldLeader')}</p>
      </div>
    )
  } else if (expired) {
    main = (
      <div className="rounded-xl bg-secondary/50 p-4 space-y-3">
        <p className="text-[13px]">{t('cluster.addAgent.expired')}</p>
        <Button variant="outline" className="rounded-xl" onClick={newToken}>{t('cluster.addAgent.newToken')}</Button>
      </div>
    )
  } else {
    main = (
      <div className="space-y-4">
        <div className="space-y-1.5">
          <label htmlFor={nameId} className="text-[11px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.addAgent.name')}
          </label>
          <Input
            id={nameId}
            value={nameInput}
            onChange={(e) => setNameInput(e.target.value)}
            aria-invalid={!nameOk}
            aria-describedby={`${nameId}-hint`}
            className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
            placeholder="agent-01"
          />
          <p id={`${nameId}-hint`} className={nameOk ? 'text-[11px] text-muted-foreground' : 'text-[11px] text-destructive'}>
            {nameOk ? t('cluster.addAgent.nameHint') : t('cluster.addAgent.nameInvalid')}
          </p>
        </div>

        {/* The server that joins may have no sudo: Proxmox VE's operator is root */}
        <label className="flex items-center gap-2 cursor-pointer">
          <Checkbox checked={asRoot} onCheckedChange={(v) => setAsRoot(v === true)} />
          <span className="text-[13px]">{t('cluster.tokens.asRoot')}</span>
        </label>

        {/* A name the agent would refuse makes a command that cannot work. */}
        {commands && nameOk && (
          <>
            <div>
              <span className="text-[11px] text-muted-foreground uppercase tracking-wider block mb-2">{t('cluster.addAgent.installAndJoin')}</span>
              <CopyBlock text={commands.installAndJoin} />
            </div>
            <div>
              <span className="text-[11px] text-muted-foreground uppercase tracking-wider block mb-2">{t('cluster.addAgent.join')}</span>
              <CopyBlock text={commands.join} />
            </div>
          </>
        )}

        <div className="flex flex-wrap items-center justify-between gap-2 text-[12px] text-muted-foreground">
          <span className="flex items-center gap-2">
            <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
            {t('cluster.addAgent.waiting')}
          </span>
          <span className="tabular-nums">{t('cluster.addAgent.remaining', { time: hms(remaining) })}</span>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-4 min-w-0">
      {main}

      {/* The native <summary> keeps the disclosure keyboard behaviour and its
          accessible name without a Collapsible component. */}
      <details className="rounded-xl border bg-secondary/20 px-3 py-2">
        <summary className="cursor-pointer text-[13px] font-medium">{t('cluster.addAgent.advanced')}</summary>
        <div className="mt-3 space-y-5">
          <p className="text-[12px] text-muted-foreground">{t('cluster.addAgent.managerNote')}</p>
          <ManagerTokenSection onCreated={() => setTokensChanged((n) => n + 1)} />
          <PendingTokens reload={tokensChanged} />
        </div>
      </details>
    </div>
  )
}
