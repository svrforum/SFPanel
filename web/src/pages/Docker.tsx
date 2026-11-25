import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { NavLink, Outlet, useOutletContext } from 'react-router-dom'
import { Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import DockerPrune from '@/components/docker/DockerPrune'
import { useServerScope } from '@/hooks/useServerScope'
import { navTabs } from '@/lib/navigation'

/**
 * Docker's pages and the app store, as tabs at their own addresses. The app
 * store reads the overview the shell already fetched from the shell's
 * context, so the context is handed on.
 */
export default function Docker() {
  const { t } = useTranslation()
  const [pruneOpen, setPruneOpen] = useState(false)
  const { href } = useServerScope()

  return (
    <div className="space-y-4 docker-workspace">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-[22px] font-bold tracking-tight">{t('docker.title')}</h1>
        <Button variant="outline" size="sm" className="rounded-xl" onClick={() => setPruneOpen(true)}>
          <Trash2 />
          {t('docker.improvements.globalPrune')}
        </Button>
      </div>

      <nav aria-label={t('docker.title')} className="grid grid-cols-3 gap-1 rounded-xl bg-secondary/30 p-1 sm:flex sm:flex-wrap">
        {navTabs('/docker').map(({ to, icon: Icon, label }) => <NavLink key={to} to={href(to)} className={({ isActive }) => `flex min-h-11 items-center justify-center gap-2 rounded-lg px-2 text-sm ${isActive ? 'bg-card text-primary shadow-sm' : 'text-muted-foreground'}`}><Icon className="h-4 w-4 shrink-0" />{t(label)}</NavLink>)}
      </nav>

      {/* Content */}
      <div className="min-h-[calc(100vh-220px)]">
        <Outlet context={useOutletContext()} />
      </div>

      <DockerPrune open={pruneOpen} onOpenChange={setPruneOpen} />
    </div>
  )
}
