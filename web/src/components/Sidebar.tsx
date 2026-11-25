import { useId } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ArrowUpCircle, ChevronDown, Coffee, LogOut, PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import GithubIcon from '@/components/GithubIcon'
import Logo, { LogoMark } from '@/components/Logo'
import { NodeStatusMark } from '@/components/cluster/NodeStatusMark'
import { ServerSwitcher } from '@/components/server/ServerSwitcher'
import { serverRole } from '@/components/server/serverRole'
import { useServerScope } from '@/hooks/useServerScope'
import { navFor, navOwner, type NavEntry } from '@/lib/navigation'
import { cn } from '@/lib/utils'

export interface SidebarProps {
  collapsed: boolean
  onToggleCollapsed: () => void
  /** The open server's panel version; '' while unknown. */
  version: string
  /** What is behind and where to see it; null when nothing is. */
  update: { label: string; to: string } | null
  /** Where the version leads when nothing is behind. */
  versionTo: string
  onLogout: () => void
}

/** A name's first two characters, by code point: a Korean or emoji name is not cut in half. */
const initials = (name: string) => Array.from(name).slice(0, 2).join('')

const iconButton = 'flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-muted-foreground transition-colors hover:bg-accent hover:text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring'

/**
 * The server the pages act on, at the top of the menu. In a cluster it is the
 * button that picks another; a single server has nothing to pick and shows
 * its name. Collapsed, the state and the name's first two letters stay.
 */
function ServerHead({ collapsed }: { collapsed: boolean }) {
  const { t } = useTranslation()
  const { clusterEnabled, nodes, current, currentId, currentName, localId } = useServerScope()
  const descriptionId = useId()

  if (clusterEnabled !== true) {
    // The line is held while the name is on its way, so the menu does not move.
    return (
      <p title={currentName || undefined} className={cn('h-5 truncate text-[13px] leading-5 font-semibold text-foreground', collapsed ? 'px-2 text-center' : 'px-4')}>
        {collapsed ? initials(currentName) : currentName}
      </p>
    )
  }

  const role = serverRole(current, t)
  const attached = localId !== '' && currentId === localId
  const offline = nodes.filter((n) => n.status === 'offline').length
  const detail = [role, attached ? t('layout.server.attached') : ''].filter(Boolean).join(' · ')
  const offlineText = offline > 0 ? t('layout.server.offline', { count: offline }) : ''
  // What the head shows besides the name, for a screen reader: the name is
  // only which server; collapsed, the line is not drawn at all.
  const description = [detail, offlineText].filter(Boolean).join(' · ')

  return (
    <div className={collapsed ? 'px-2' : 'px-3'}>
      <ServerSwitcher align="start">
        <button
          type="button"
          aria-label={`${t('layout.server.pick')}: ${currentName}`}
          aria-describedby={description ? descriptionId : undefined}
          title={collapsed ? currentName : undefined}
          className={cn(
            'w-full rounded-xl text-left transition-colors hover:bg-accent outline-none focus-visible:ring-2 focus-visible:ring-ring',
            collapsed ? 'flex items-center justify-center gap-1 px-0 py-2 text-[13px] font-semibold' : 'block bg-secondary/50 px-3 py-2',
          )}
        >
          {collapsed ? (
            <>
              <NodeStatusMark status={current?.status ?? ''} />
              <span>{initials(currentName)}</span>
            </>
          ) : (
            <>
              <span className="flex min-w-0 items-center gap-2">
                <NodeStatusMark status={current?.status ?? ''} />
                <span className="truncate text-[13px] font-semibold text-foreground">{currentName}</span>
                <ChevronDown aria-hidden="true" className="ml-auto h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              </span>
              {/* Held while the list is on its way, so the menu does not move. */}
              <span className="mt-0.5 flex h-4 min-w-0 items-center gap-2 text-[12px] leading-4 text-muted-foreground">
                <span className="truncate">{detail}</span>
                {offlineText && (
                  <span className="ml-auto shrink-0 font-medium text-destructive">{offlineText}</span>
                )}
              </span>
            </>
          )}
        </button>
      </ServerSwitcher>
      {description && <span id={descriptionId} hidden>{description}</span>}
    </div>
  )
}

/**
 * The one menu, the same in both modes: the chosen server's pages, then the
 * pages about all servers, then settings — in registry order. In a cluster a
 * server page's link names the server shown (useServerScope's href).
 */
export default function Sidebar({ collapsed, onToggleCollapsed, version, update, versionTo, onLogout }: SidebarProps) {
  const { t } = useTranslation()
  const { pathname } = useLocation()
  const { clusterEnabled, href } = useServerScope()
  const cluster = clusterEnabled === true
  const owner = navOwner(pathname)
  const items = navFor(cluster)
  const byGroup = (group: NavEntry['group']) => items.filter((e) => e.group === group)

  const link = (item: NavEntry) => {
    // The entry a page belongs to, tabs included: /processes lights System.
    const active = owner?.entry === item
    const label = t(item.labelKey)
    return (
      <Link
        key={item.to}
        to={href(item.to)}
        aria-current={active ? 'page' : undefined}
        aria-label={collapsed ? label : undefined}
        title={collapsed ? label : undefined}
        className={cn(
          'relative flex items-center rounded-xl text-[13px] font-medium transition duration-200 outline-none focus-visible:ring-2 focus-visible:ring-ring',
          collapsed ? 'justify-center px-0 py-2' : 'gap-3 px-3 py-2',
          active ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-accent hover:text-foreground',
        )}
      >
        <item.icon aria-hidden="true" className="h-[18px] w-[18px] shrink-0" />
        {!collapsed && label}
      </Link>
    )
  }
  const rule = <hr className="mx-1 my-2 border-border" />

  const versionText = version ? `v${version}` : ''
  // The count rides in the name too: collapsed, it is an arrow.
  const versionName = [versionText || 'SFPanel', update?.label].filter(Boolean).join(' · ')

  return (
    <aside className={cn(
      'hidden h-full shrink-0 flex-col border-r border-border bg-card transition-[width] duration-300 ease-in-out md:flex',
      collapsed ? 'w-[68px]' : 'w-60',
    )}>
      <div className={cn('flex shrink-0 items-center', collapsed ? 'justify-center px-3 pt-5 pb-3' : 'px-4 pt-4 pb-3')}>
        <Link to={href('/dashboard')} aria-label="SFPanel" className={cn('rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring', !collapsed && 'block w-full')}>
          {collapsed ? <LogoMark className="h-8 w-8" /> : <Logo />}
        </Link>
      </div>

      <div className="shrink-0 pb-2">
        <ServerHead collapsed={collapsed} />
      </div>

      <nav aria-label={t('layout.mainMenu')} className={cn('min-h-0 flex-1 space-y-0.5 overflow-y-auto no-scrollbar pb-2', collapsed ? 'px-2' : 'px-3')}>
        {byGroup('server').map(link)}
        {cluster ? (
          collapsed ? (
            <>
              {rule}
              <span className="sr-only">{t('layout.groups.fleet')}</span>
            </>
          ) : (
            <p className="px-3 pt-3 pb-1 text-[12px] font-semibold text-muted-foreground">{t('layout.groups.fleet')}</p>
          )
        ) : rule}
        {byGroup('fleet').map(link)}
        {rule}
        {byGroup('tail').map(link)}
      </nav>

      {collapsed ? (
        <div className="flex shrink-0 flex-col items-center gap-1 border-t border-border px-2 py-2">
          <Link
            to={href(update?.to ?? versionTo)}
            aria-label={versionName}
            title={versionName}
            className="flex w-full flex-col items-center gap-0.5 rounded-lg py-1 outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <span className="text-[12px] font-medium text-muted-foreground">
              {version ? `v${version.split('.').slice(0, 2).join('.')}` : '…'}
            </span>
            {update && <ArrowUpCircle aria-hidden="true" className="h-3.5 w-3.5 text-primary" />}
          </Link>
          <div className="flex items-center">
            <a href="https://github.com/svrforum/SFPanel" target="_blank" rel="noopener noreferrer" title="GitHub" aria-label="GitHub" className={iconButton}>
              <GithubIcon className="h-3.5 w-3.5" />
            </a>
            <a href="https://buymeacoffee.com/svrforum" target="_blank" rel="noopener noreferrer" title="Buy me a coffee" aria-label={t('layout.sponsor')} className={cn(iconButton, 'hover:text-[#FFDD00]')}>
              <Coffee aria-hidden="true" className="h-3.5 w-3.5" />
            </a>
          </div>
          <button type="button" onClick={onToggleCollapsed} title={t('layout.expand')} aria-label={t('layout.expand')} className={cn(iconButton, 'h-8 w-8')}>
            <PanelLeftOpen aria-hidden="true" className="h-4 w-4" />
          </button>
          <button type="button" onClick={onLogout} title={t('layout.logout')} aria-label={t('layout.logout')} className={cn(iconButton, 'h-8 w-8')}>
            <LogOut aria-hidden="true" className="h-4 w-4" />
          </button>
        </div>
      ) : (
        <div className="flex shrink-0 items-center gap-0.5 border-t border-border px-2 py-2">
          <Link
            to={href(update?.to ?? versionTo)}
            aria-label={versionName}
            title={versionName}
            className="flex min-w-0 flex-1 items-center gap-1.5 rounded-lg px-1.5 py-1 outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring"
          >
            {/* Beside a long count a long version is cut short: the title keeps it whole. */}
            <span className="truncate text-[12px] font-medium text-muted-foreground">{versionText || '…'}</span>
            {update && (
              <span className="shrink-0 whitespace-nowrap rounded-full bg-primary/10 px-1.5 py-0.5 text-[12px] font-semibold text-primary">{update.label}</span>
            )}
          </Link>
          <a href="https://github.com/svrforum/SFPanel" target="_blank" rel="noopener noreferrer" title="GitHub" aria-label="GitHub" className={iconButton}>
            <GithubIcon className="h-3.5 w-3.5" />
          </a>
          <a href="https://buymeacoffee.com/svrforum" target="_blank" rel="noopener noreferrer" title="Buy me a coffee" aria-label={t('layout.sponsor')} className={cn(iconButton, 'hover:text-[#FFDD00]')}>
            <Coffee aria-hidden="true" className="h-3.5 w-3.5" />
          </a>
          <button type="button" onClick={onToggleCollapsed} title={t('layout.collapse')} aria-label={t('layout.collapse')} className={iconButton}>
            <PanelLeftClose aria-hidden="true" className="h-4 w-4" />
          </button>
          <button type="button" onClick={onLogout} title={t('layout.logout')} aria-label={t('layout.logout')} className={iconButton}>
            <LogOut aria-hidden="true" className="h-4 w-4" />
          </button>
        </div>
      )}
    </aside>
  )
}
