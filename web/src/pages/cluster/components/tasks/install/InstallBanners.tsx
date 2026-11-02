import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { CircleCheck, ExternalLink, KeyRound, ListChecks, ShieldAlert, TriangleAlert } from 'lucide-react'
import { taskErrorMessage } from '@/lib/taskErrors'
import { Button } from '@/components/ui/button'
import type { RunRecord } from '@/types/api'
import { api } from '@/lib/api'
import type { RunState } from '../taskRunProgress'
import { useMounted } from '../useMounted'
import { installBanner, installReview, type CheckLink, type InstallReview } from './installRun'

interface InstallBannersProps {
  run: RunRecord
  state: RunState
  /** A row's name as the table shows it. */
  rowName: (key: string) => string
}

// What the install waits on the operator for, above the table: a server's
// host key to compare, its Tailscale SSH check to approve, its standalone
// panel to convert or skip, each by that server; and, in a review, how many
// servers passed their pre-flight check until every server is ready, then
// one [Continue install] for those that passed. A question goes once the row
// stops waiting on it.
export function InstallBanners({ run, state, rowName }: InstallBannersProps) {
  const review = installReview(run, state.nodes)
  const asks = run.nodes.flatMap((key) => {
    const banner = installBanner(state.nodes[key])
    return banner && banner.kind !== 'review' ? [{ key, banner }] : []
  })
  if (asks.length === 0 && !review) return null

  return (
    <div className="space-y-2">
      {asks.map(({ key, banner }) => {
        const name = rowName(key)
        // Keyed by the question: a row that asks another starts afresh.
        const id = `${key}/${banner.kind}`
        switch (banner.kind) {
          case 'host_key':
            return <HostKeyQuestion key={id} runId={run.id} rowKey={key} name={name} hostKey={banner.hostKey} tailscale={banner.tailscale} />
          case 'check':
            return <CheckWait key={id} rowKey={key} name={name} link={banner.link} sent={state.nodes[key]?.check_url} />
          case 'standalone':
            return <StandaloneQuestion key={id} runId={run.id} rowKey={key} name={name} />
        }
      })}
      {review && (review.open
        ? <ReviewQuestion key={review.waiting.join(',')} runId={run.id} names={review.waiting.map(rowName)} />
        : <ReviewProgress review={review} />)}
    </div>
  )
}

// An answer sent once: its buttons stay off after the server took it, until
// the row's record moves on, and a refusal reads as a sentence by the row.
// Nothing is sent again by itself.
function useAnswer() {
  const { t } = useTranslation()
  const mounted = useMounted()
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const send = async (call: () => Promise<unknown>) => {
    setBusy(true)
    setError(null)
    try {
      await call()
      if (mounted.current) setSent(true)
    } catch (err) {
      if (mounted.current) setError(taskErrorMessage(err, t))
    } finally {
      if (mounted.current) setBusy(false)
    }
  }
  return { busy: busy || sent, error, send }
}

interface BannerProps {
  testId: string
  icon: ReactNode
  children: ReactNode
  error?: string | null
  tone?: 'warning' | 'primary'
}

function Banner({ testId, icon, children, error, tone = 'warning' }: BannerProps) {
  return (
    <div
      data-testid={testId}
      className={
        tone === 'warning'
          ? 'rounded-xl border border-warning/30 bg-warning/10 p-3 space-y-2 min-w-0'
          : 'rounded-xl border border-primary/20 bg-primary/5 p-3 space-y-2 min-w-0'
      }
    >
      <div className="flex gap-2 min-w-0">
        <span className="mt-0.5 shrink-0 text-muted-foreground">{icon}</span>
        <div className="flex-1 min-w-0 space-y-2 text-[13px]">{children}</div>
      </div>
      {error && <p role="alert" className="rounded-lg bg-destructive/10 px-3 py-2 text-[13px] text-destructive break-words">{error}</p>}
    </div>
  )
}

interface RowQuestionProps {
  runId: string
  rowKey: string
  name: string
}

// The whole fingerprint and its key type, never shortened: the operator
// compares it with what the server itself prints. The answer carries the
// fingerprint as shown, so a key that changed since is refused. Under it,
// whether Tailscale reports the same key for the device, when the leader
// compared them: said only, the approval stays the operator's.
function HostKeyQuestion({ runId, rowKey, name, hostKey, tailscale }: RowQuestionProps & { hostKey: string; tailscale?: 'match' | 'mismatch' }) {
  const { t } = useTranslation()
  const { busy, error, send } = useAnswer()
  return (
    <Banner testId={`install-wait-${rowKey}`} icon={<KeyRound className="h-4 w-4" />} error={error}>
      <p className="break-words">{t('cluster.tasks.install.run.hostKey', { server: name })}</p>
      <code data-testid="install-host-key" className="block rounded-lg bg-background/80 px-3 py-2 font-mono text-[12px] break-all select-all">
        {hostKey}
      </code>
      {tailscale === 'match' && (
        <p data-testid="install-host-key-tailscale" className="flex items-start gap-1.5 text-[12px] text-success break-words">
          <CircleCheck className="h-4 w-4 mt-px shrink-0" aria-hidden />
          {t('cluster.tasks.install.run.hostKeyTailscaleMatch')}
        </p>
      )}
      {tailscale === 'mismatch' && (
        <p data-testid="install-host-key-tailscale" className="flex items-start gap-1.5 rounded-lg bg-warning/15 px-3 py-2 text-[12px] font-medium text-warning break-words">
          <TriangleAlert className="h-4 w-4 mt-px shrink-0" aria-hidden />
          {t('cluster.tasks.install.run.hostKeyTailscaleMismatch')}
        </p>
      )}
      <p className="text-[12px] text-muted-foreground break-words">{t('cluster.tasks.install.run.hostKeyHint')}</p>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" disabled={busy} onClick={() => send(() => api.answerHostKey(runId, rowKey, hostKey, true))}>
          {t('cluster.tasks.install.run.accept')}
        </Button>
        <Button size="sm" variant="outline" disabled={busy} onClick={() => send(() => api.answerHostKey(runId, rowKey, hostKey, false))}>
          {t('cluster.tasks.install.run.reject')}
        </Button>
      </div>
    </Banner>
  )
}

// The address comes from the server being installed: only an https link is
// followed, its host shown beside it; anything else is shown as text.
function CheckWait({ rowKey, name, link, sent }: { rowKey: string; name: string; link: CheckLink | null; sent: string | undefined }) {
  const { t } = useTranslation()
  return (
    <Banner testId={`install-wait-${rowKey}`} icon={<ShieldAlert className="h-4 w-4" />}>
      <p className="break-words">{t('cluster.tasks.install.run.check', { server: name })}</p>
      {link ? (
        <>
          <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <a
              href={link.href}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 font-medium text-primary underline underline-offset-2"
            >
              {t('cluster.tasks.install.run.checkOpen')}
              <ExternalLink className="h-3.5 w-3.5" />
            </a>
            <span className="font-mono text-[12px] text-muted-foreground break-all">{link.host}</span>
          </p>
          <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.install.run.checkHint')}</p>
        </>
      ) : sent ? (
        <>
          <p className="break-words">{t('cluster.tasks.install.run.checkUnsafe')}</p>
          <code className="block rounded-lg bg-background/80 px-3 py-2 font-mono text-[12px] break-all">{sent}</code>
        </>
      ) : (
        <p className="text-muted-foreground">{t('cluster.tasks.install.run.checkNone')}</p>
      )}
    </Banner>
  )
}

function StandaloneQuestion({ runId, rowKey, name }: RowQuestionProps) {
  const { t } = useTranslation()
  const { busy, error, send } = useAnswer()
  return (
    <Banner testId={`install-wait-${rowKey}`} icon={<ShieldAlert className="h-4 w-4" />} error={error}>
      <p className="break-words">{t('cluster.tasks.install.run.standalone', { server: name })}</p>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" disabled={busy} onClick={() => send(() => api.confirmStandalone(runId, rowKey, true))}>
          {t('cluster.tasks.install.run.convert')}
        </Button>
        <Button size="sm" variant="outline" disabled={busy} onClick={() => send(() => api.confirmStandalone(runId, rowKey, false))}>
          {t('cluster.tasks.install.run.skip')}
        </Button>
      </div>
    </Banner>
  )
}

// Before the review opens: how many servers passed, of how many.
function ReviewProgress({ review }: { review: InstallReview }) {
  const { t } = useTranslation()
  return (
    <Banner testId="install-review" icon={<ListChecks className="h-4 w-4" />} tone="primary">
      <p className="break-words">{t('cluster.tasks.install.run.reviewProgress', { passed: review.passed, total: review.total })}</p>
    </Banner>
  )
}

// One answer for the whole review: every server that passed goes on.
function ReviewQuestion({ runId, names }: { runId: string; names: string[] }) {
  const { t } = useTranslation()
  const { busy, error, send } = useAnswer()
  return (
    <Banner testId="install-review" icon={<ListChecks className="h-4 w-4" />} error={error} tone="primary">
      <p className="break-words">{t('cluster.tasks.install.run.review', { servers: names.join(', ') })}</p>
      <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.install.run.reviewHint')}</p>
      <Button size="sm" disabled={busy} onClick={() => send(() => api.proceedAgentInstall(runId))}>
        {t('cluster.tasks.install.run.proceed')}
      </Button>
    </Banner>
  )
}
