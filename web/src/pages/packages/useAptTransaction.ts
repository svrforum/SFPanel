import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { toastWarnings } from '@/lib/warnings'
import type { AptTransaction } from '@/types/api'
import { streamErrorMessage, type SSEOutputState } from '@/components/OutputDialog'
import { followTransaction, type TxIO } from './aptTransaction'

const CLOSED: SSEOutputState = { open: false, title: '', output: '', done: false }

// How often the page asks whether a transaction it is not following has ended.
const POLL_MS = 5000

const io: TxIO = {
  open: (path, onChunk, method, body, signal) => api.aptTransactionStream(path, onChunk, { method, body, signal }),
  status: async () => (await api.getAptTransaction()).transaction,
  wait: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
}

/** What the page says about a transaction: its dialog title and how it ended. */
export function describeTransaction(tx: Pick<AptTransaction, 'kind' | 'packages'>, t: TFunction) {
  const name = tx.packages.join(' ')
  if (tx.kind === 'install') {
    return {
      title: t('packages.installingPackage', { name }),
      success: t('packages.installSuccess', { name }),
      failure: t('packages.installFailed', { name }),
    }
  }
  if (tx.kind === 'upgrade') {
    return {
      title:
        tx.packages.length > 0
          ? t('packages.upgradingSelected', { count: tx.packages.length })
          : t('packages.upgradingAll'),
      success: t('packages.upgradeComplete'),
      failure: t('packages.upgradeFailed'),
    }
  }
  return { title: t('packages.txTitle'), success: t('packages.txSucceeded'), failure: t('packages.txFailed') }
}

export interface AptTransactions {
  /** The transaction dialog's state, for <TransactionDialog>. */
  dialog: SSEOutputState
  /** Closes the dialog once its transaction has ended. */
  close: () => void
  /** Puts the dialog away while apt runs on; the banner brings it back. */
  hide: () => void
  /** The node's transaction while it runs, for the banner. */
  running: AptTransaction | null
  /** The page is streaming a transaction into its dialog. */
  following: boolean
  /** Something runs or is starting: apt actions wait. */
  busy: boolean
  /** Start one; each resolves when it has ended, with null when it never started. */
  upgrade: (packages?: string[]) => Promise<AptTransaction | null>
  install: (name: string) => Promise<AptTransaction | null>
  /** Opens the dialog on the transaction that is running. */
  follow: () => Promise<AptTransaction | null>
  refresh: () => Promise<void>
}

/**
 * useAptTransaction runs the page's package transactions — upgrades and
 * installs — in their own dialog. Success or failure comes from how apt
 * exited, not from the words in the log: on a Proxmox host without a
 * subscription a good upgrade's log carries apt-get update's "E:" lines for
 * the enterprise repository.
 */
export function useAptTransaction(): AptTransactions {
  const { t } = useTranslation()
  const [dialog, setDialog] = useState<SSEOutputState>(CLOSED)
  const [running, setRunning] = useState<AptTransaction | null>(null)
  const [following, setFollowing] = useState(false)
  const stream = useRef<AbortController | null>(null)

  const refresh = useCallback(async () => {
    try {
      const { transaction } = await api.getAptTransaction()
      setRunning(transaction?.running ? transaction : null)
    } catch {
      setRunning(null)
    }
  }, [])

  useEffect(() => {
    refresh()
    return () => stream.current?.abort()
  }, [refresh])

  // One running that this page is not following: notice when it ends.
  useEffect(() => {
    if (!running || following) return
    const timer = setInterval(refresh, POLL_MS)
    return () => clearInterval(timer)
  }, [running, following, refresh])

  const run = useCallback(
    async (
      words: { title: string; success: string; failure: string },
      start: { path: string; method: 'GET' | 'POST'; body?: unknown },
      id?: string,
    ): Promise<AptTransaction | null> => {
      const ctl = new AbortController()
      stream.current = ctl
      setFollowing(true)
      setDialog({ open: true, title: words.title, output: '', done: false })
      const append = (line: string) => setDialog((prev) => ({ ...prev, output: prev.output + line + '\n' }))
      try {
        const tx = await followTransaction(io, start, append, id, ctl.signal)
        if (ctl.signal.aborted) return null
        const ok = tx?.result === 'success'
        const end = ok ? words.success : tx?.result === 'interrupted' ? t('packages.txInterrupted') : words.failure
        append('')
        append(end)
        setDialog((prev) => ({ ...prev, done: true, error: !ok }))
        if (ok) toast.success(end)
        else toast.error(end)
        toastWarnings(tx?.warnings, t)
        return tx
      } catch (err: unknown) {
        // Refused before it started: one already running, Docker on Proxmox.
        const message = streamErrorMessage(err, t('packages.streamStartFailed'))
        append(t('packages.error') + ': ' + message)
        setDialog((prev) => ({ ...prev, done: true, error: true }))
        toast.error(message)
        return null
      } finally {
        if (stream.current === ctl) stream.current = null
        setFollowing(false)
        refresh()
      }
    },
    [refresh, t],
  )

  const upgrade = useCallback(
    (packages: string[] = []) =>
      run(describeTransaction({ kind: 'upgrade', packages }, t), {
        path: '/packages/upgrade',
        method: 'POST',
        body: { packages },
      }),
    [run, t],
  )

  const install = useCallback(
    (name: string) =>
      run(describeTransaction({ kind: 'install', packages: [name] }, t), {
        path: '/packages/install-stream',
        method: 'POST',
        body: { name },
      }),
    [run, t],
  )

  const follow = useCallback(async () => {
    if (!running) return null
    return run(
      describeTransaction(running, t),
      { path: `/packages/transaction/stream?id=${encodeURIComponent(running.id)}`, method: 'GET' },
      running.id,
    )
  }, [run, running, t])

  const close = useCallback(() => setDialog(CLOSED), [])

  const hide = useCallback(() => {
    stream.current?.abort()
    setDialog(CLOSED)
  }, [])

  return {
    dialog,
    close,
    hide,
    running,
    following,
    busy: following || running !== null,
    upgrade,
    install,
    follow,
    refresh,
  }
}
