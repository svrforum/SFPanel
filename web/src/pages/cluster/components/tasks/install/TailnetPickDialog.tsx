import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2 } from 'lucide-react'
import { taskErrorMessage } from '@/lib/taskErrors'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { StatusPill } from '@/components/StatusPill'
import type { AgentInstallTailnet, AgentInstallTailnetPeer } from '@/types/api'
import { SEARCH_FROM, filterPeers, pickable } from './installTailnet'

interface TailnetPickDialogProps {
  /**
   * The leader's answer, asked for by the click that opened the dialog: one
   * request an opening, however often an effect runs.
   */
  tailnet: Promise<AgentInstallTailnet>
  onClose: () => void
  /** The devices picked, in the list's order. */
  onAdd: (picks: AgentInstallTailnetPeer[]) => void
}

// The leader's tailnet, read once each time the dialog is opened: its Linux
// devices, a line each, to tick for the install. A device already in the
// cluster or offline is shown and cannot be ticked. Mounted while open.
export function TailnetPickDialog({ tailnet, onClose, onAdd }: TailnetPickDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const [answer, setAnswer] = useState<AgentInstallTailnet | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [picked, setPicked] = useState<string[]>([])
  const [search, setSearch] = useState('')

  useEffect(() => {
    let live = true
    tailnet.then(
      (a) => { if (live) setAnswer({ ...a, peers: a.peers ?? [] }) },
      (err) => { if (live) setError(taskErrorMessage(err, t)) },
    )
    return () => { live = false }
  }, [tailnet, t])

  const reasonText = (reason: string) => {
    switch (reason) {
      case 'not_installed': return t('cluster.tasks.install.tailnet.notInstalled')
      case 'not_running': return t('cluster.tasks.install.tailnet.notRunning')
      case 'no_address': return t('cluster.tasks.install.tailnet.noAddress')
      default: return t('cluster.tasks.install.tailnet.offTailnet')
    }
  }

  const peers = answer?.peers ?? []
  const shown = filterPeers(peers, search)
  const picks = peers.filter((p) => picked.includes(p.ip) && pickable(p))

  let body
  if (error) {
    body = <p role="alert" className="rounded-xl bg-destructive/10 px-3 py-2 text-[13px] text-destructive break-words">{error}</p>
  } else if (!answer) {
    body = (
      <p className="flex items-center gap-2 text-[13px] text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
        {t('cluster.tasks.install.tailnet.loading')}
      </p>
    )
  } else if (!answer.on_tailscale) {
    body = (
      <div data-testid="install-tailnet-off" className="rounded-xl bg-warning/10 px-3 py-2 text-[13px] text-warning space-y-1">
        <p className="font-medium break-words">{reasonText(answer.reason)}</p>
        <p className="break-words">{t('cluster.tasks.install.tailnet.leaderHint')}</p>
      </div>
    )
  } else if (peers.length === 0) {
    body = <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.install.tailnet.none')}</p>
  } else {
    body = (
      <div className="space-y-3 min-w-0">
        {peers.length >= SEARCH_FROM && (
          <Input
            type="search"
            aria-label={t('cluster.tasks.install.tailnet.search')}
            placeholder={t('cluster.tasks.install.tailnet.search')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
          />
        )}
        {shown.length === 0 ? (
          <p className="text-[13px] text-muted-foreground">{t('cluster.tasks.install.tailnet.noMatch')}</p>
        ) : (
          // The list scrolls within half the screen, so the buttons below it
          // stay in view on a phone.
          <ul data-testid="install-tailnet-peers" className="max-h-[50vh] overflow-y-auto overscroll-contain rounded-xl border border-border divide-y divide-border">
            {shown.map((p) => {
              const box = `${id}-peer-${p.ip}`
              const can = pickable(p)
              return (
                <li key={p.ip} data-testid={`install-tailnet-${p.ip}`} className="flex items-start gap-2.5 px-3 py-2 min-w-0">
                  <Checkbox
                    id={box}
                    aria-label={p.name || p.ip}
                    className="mt-0.5"
                    checked={can && picked.includes(p.ip)}
                    disabled={!can}
                    onCheckedChange={(on) => setPicked(on === true ? [...picked, p.ip] : picked.filter((ip) => ip !== p.ip))}
                  />
                  {/* The badges move under the name when both do not fit, so
                      neither the name nor the address is cut mid-word. A
                      device that cannot be picked is dimmed. */}
                  <div className={can ? 'flex min-w-0 flex-1 flex-wrap items-start justify-between gap-x-2 gap-y-1' : 'flex min-w-0 flex-1 flex-wrap items-start justify-between gap-x-2 gap-y-1 opacity-60'}>
                    <label htmlFor={box} className={can ? 'min-w-0 space-y-0.5 cursor-pointer' : 'min-w-0 space-y-0.5'}>
                      <span className="block text-[13px] font-medium break-words">{p.name || p.ip}</span>
                      <span className="block font-mono text-[12px] text-muted-foreground whitespace-nowrap">{p.ip}</span>
                      {p.node && (
                        <span className="block text-[12px] text-muted-foreground break-words">
                          {t('cluster.tasks.install.tailnet.inCluster', { node: p.node })}
                        </span>
                      )}
                    </label>
                    <span className="flex shrink-0 flex-wrap gap-1">
                      {p.ssh && <StatusPill tone="primary">{t('cluster.tasks.install.tailnet.ssh')}</StatusPill>}
                      <StatusPill tone={p.online ? 'success' : 'muted'}>
                        {p.online ? t('cluster.tasks.install.tailnet.online') : t('cluster.tasks.install.tailnet.offline')}
                      </StatusPill>
                    </span>
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </div>
    )
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{t('cluster.tasks.install.tailnet.title')}</DialogTitle>
          <DialogDescription>{t('cluster.tasks.install.tailnet.description')}</DialogDescription>
        </DialogHeader>
        {body}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
          <Button disabled={picks.length === 0} onClick={() => onAdd(picks)}>
            {t('cluster.tasks.install.tailnet.add', { count: picks.length })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
