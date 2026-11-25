import { useEffect, useRef, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { NavLink, matchPath, useLocation } from 'react-router-dom'
import { Check, ChevronDown, type LucideIcon } from 'lucide-react'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useServerScope } from '@/hooks/useServerScope'

export interface SubNavItem {
  to: string
  icon: LucideIcon
  /** i18n key, translated inside the component. */
  label: string
  /** Drawn inside the strip's one Setup menu, which stands where the first such item would. */
  fold?: 'setup'
}

const tabClass = (active: boolean) =>
  `flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium transition duration-200 whitespace-nowrap shrink-0 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring ${
    active
      ? 'bg-card card-shadow text-foreground'
      : 'text-muted-foreground hover:text-foreground'
  }`

/**
 * Pill-style sub-navigation tab strip shared by the section shells
 * (Network, Firewall, …). `children` renders after the tabs, for shells
 * that append trailing actions to the strip. With a `label` the strip is a
 * navigation landmark of that name. A tab whose address begins another
 * tab's (the map's /map, before /map/rack) is lit at its own address only.
 * Items with a `fold` are pages used rarely enough to sit behind one menu
 * button; while one of them is open the button names it and is lit.
 */
export function SubNavTabs({ items, label, children }: { items: SubNavItem[]; label?: string; children?: ReactNode }) {
  const { t } = useTranslation()
  const { pathname } = useLocation()
  const { href } = useServerScope()
  const row = useRef<HTMLDivElement>(null)

  // The strip hides its scrollbar, so a tab opened from a link or a bookmark
  // could sit cut off past the edge on a phone. Same as the cluster tabs.
  useEffect(() => {
    row.current?.querySelector<HTMLElement>('[aria-current="page"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [pathname])

  const end = (to: string) => items.some((other) => other.to.startsWith(to + '/'))
  const folded = items.filter((item) => item.fold === 'setup')
  // NavLink's own rule, so the menu button is lit exactly when a link would be.
  const openFolded = folded.find(({ to }) => matchPath({ path: to, end: end(to) }, pathname))

  const Row = label ? 'nav' : 'div'
  return (
    <Row ref={row} aria-label={label} className="flex items-center gap-1 bg-secondary/30 rounded-xl p-1 overflow-x-auto no-scrollbar">
      {items.map(({ to, icon: Icon, label: key, fold }) => {
        if (fold === 'setup') {
          if (to !== folded[0].to) return null
          return (
            <DropdownMenu key={to}>
              <DropdownMenuTrigger asChild>
                <button type="button" aria-current={openFolded ? 'page' : undefined} className={tabClass(!!openFolded)}>
                  {openFolded ? t('layout.tabs.setupCurrent', { tab: t(openFolded.label) }) : t('layout.tabs.setup')}
                  <ChevronDown aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
                </button>
              </DropdownMenuTrigger>
              {/* The open page's item says so by sight too (aria-current is NavLink's). */}
              <DropdownMenuContent align="start">
                {folded.map(({ to: itemTo, icon: ItemIcon, label: itemKey }) => (
                  <DropdownMenuItem key={itemTo} asChild className={itemTo === openFolded?.to ? 'font-semibold text-foreground' : undefined}>
                    <NavLink to={href(itemTo)} end={end(itemTo)}>
                      <ItemIcon />
                      {t(itemKey)}
                      {itemTo === openFolded?.to && <Check aria-hidden="true" className="ml-auto" />}
                    </NavLink>
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          )
        }
        return (
          <NavLink key={to} to={href(to)} end={end(to)} className={({ isActive }) => tabClass(isActive)}>
            <Icon className="h-3.5 w-3.5 shrink-0" />
            {t(key)}
          </NavLink>
        )
      })}
      {children}
    </Row>
  )
}
