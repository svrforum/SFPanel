import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { NavLink, Outlet, useOutletContext } from 'react-router-dom'
import { Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import DockerPrune from '@/components/docker/DockerPrune'
import { OutputDialog, useSSEOutput } from '@/components/OutputDialog'
import { DockerStatusCard } from '@/pages/packages/components/DockerStatusCard'
import { useServerScope } from '@/hooks/useServerScope'
import { api } from '@/lib/api'
import { navTabs } from '@/lib/navigation'
import type { DockerStatus } from '@/types/api'

/**
 * Docker's pages and the app store, as tabs at their own addresses. The app
 * store reads the overview the shell already fetched from the shell's
 * context, so the context is handed on.
 *
 * A server where Docker is not installed gets the Packages page's Docker card
 * between the tabs and the page, to install it in place. The status is read
 * alongside the page, which does not wait for it; a Docker that is installed
 * but stopped gets no card, only each page's own error. Once the card reports
 * Docker installed it goes, and the page is mounted again so it reads again.
 * Switching servers remounts this layout, which reads the new server's
 * status.
 */
export default function Docker() {
  const { t } = useTranslation()
  const [pruneOpen, setPruneOpen] = useState(false)
  const { href } = useServerScope()
  const output = useSSEOutput()
  const [docker, setDocker] = useState<DockerStatus | null>(null)
  const [reads, setReads] = useState(0)
  // The tabs, whose open one takes the focus when the output dialog closes
  // over a card that went with the install (and its button with it).
  const tabs = useRef<HTMLElement>(null)

  useEffect(() => {
    let current = true
    // A failed read draws no card: the pages say what is wrong.
    api.getDockerStatus().then((status) => { if (current) setDocker(status) }).catch(() => {})
    return () => { current = false }
  }, [])

  const onStatus = useCallback((status: DockerStatus) => {
    setDocker(status)
    if (status.installed) setReads((n) => n + 1)
  }, [])

  return (
    <div className="space-y-4 docker-workspace">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-[22px] font-bold tracking-tight">{t('docker.title')}</h1>
        <Button variant="outline" size="sm" className="rounded-xl" onClick={() => setPruneOpen(true)}>
          <Trash2 />
          {t('docker.improvements.globalPrune')}
        </Button>
      </div>

      <nav ref={tabs} aria-label={t('docker.title')} className="grid grid-cols-3 gap-1 rounded-xl bg-secondary/30 p-1 sm:flex sm:flex-wrap">
        {navTabs('/docker').map(({ to, icon: Icon, label }) => <NavLink key={to} to={href(to)} className={({ isActive }) => `flex min-h-11 items-center justify-center gap-2 rounded-lg px-2 text-sm ${isActive ? 'bg-card text-primary shadow-sm' : 'text-muted-foreground'}`}><Icon className="h-4 w-4 shrink-0" />{t(label)}</NavLink>)}
      </nav>

      {docker?.installed === false && <DockerStatusCard output={output} onStatus={onStatus} />}

      {/* Content */}
      <div key={reads} className="min-h-[calc(100vh-220px)]">
        <Outlet context={useOutletContext()} />
      </div>

      <DockerPrune open={pruneOpen} onOpenChange={setPruneOpen} />
      <OutputDialog state={output.state} onClose={output.closeOutput} returnFocus={() => tabs.current?.querySelector<HTMLElement>('a[aria-current="page"]')} />
    </div>
  )
}
