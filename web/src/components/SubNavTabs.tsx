import { useEffect, useRef, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { NavLink, useLocation } from 'react-router-dom'
import { type LucideIcon } from 'lucide-react'
import { useServerScope } from '@/hooks/useServerScope'

export interface SubNavItem {
  to: string
  icon: LucideIcon
  /** i18n key, translated inside the component. */
  label: string
}

/**
 * Pill-style sub-navigation tab strip shared by the section shells
 * (Network, Firewall, …). `children` renders after the tabs, for shells
 * that append trailing actions to the strip. With a `label` the strip is a
 * navigation landmark of that name. A tab whose address begins another
 * tab's (the map's /map, before /map/rack) is lit at its own address only.
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

  const Row = label ? 'nav' : 'div'
  return (
    <Row ref={row} aria-label={label} className="flex items-center gap-1 bg-secondary/30 rounded-xl p-1 overflow-x-auto no-scrollbar">
      {items.map(({ to, icon: Icon, label: key }) => (
        <NavLink
          key={to}
          to={href(to)}
          end={items.some((other) => other.to.startsWith(to + '/'))}
          className={({ isActive }) =>
            `flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium transition duration-200 whitespace-nowrap shrink-0 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring ${
              isActive
                ? 'bg-card card-shadow text-foreground'
                : 'text-muted-foreground hover:text-foreground'
            }`
          }
        >
          <Icon className="h-3.5 w-3.5 shrink-0" />
          {t(key)}
        </NavLink>
      ))}
      {children}
    </Row>
  )
}
