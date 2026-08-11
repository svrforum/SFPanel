import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { NavLink } from 'react-router-dom'
import { type LucideIcon } from 'lucide-react'

export interface SubNavItem {
  to: string
  icon: LucideIcon
  /** i18n key, translated inside the component. */
  label: string
}

/**
 * Pill-style sub-navigation tab strip shared by the section shells
 * (Network, Firewall, …). `children` renders after the tabs, for shells
 * that append trailing actions to the strip (e.g. Docker's prune button).
 */
export function SubNavTabs({ items, children }: { items: SubNavItem[]; children?: ReactNode }) {
  const { t } = useTranslation()

  return (
    <div className="flex items-center gap-1 bg-secondary/30 rounded-xl p-1 overflow-x-auto no-scrollbar">
      {items.map(({ to, icon: Icon, label }) => (
        <NavLink
          key={to}
          to={to}
          className={({ isActive }) =>
            `flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium transition-all duration-200 whitespace-nowrap shrink-0 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40 ${
              isActive
                ? 'bg-card card-shadow text-foreground'
                : 'text-muted-foreground hover:text-foreground'
            }`
          }
        >
          <Icon className="h-3.5 w-3.5 shrink-0" />
          {t(label)}
        </NavLink>
      ))}
      {children}
    </div>
  )
}
