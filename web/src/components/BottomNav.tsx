import { NavLink, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Menu } from 'lucide-react'
import { cn } from '@/lib/utils'
import { BOTTOM_NAV_ITEMS } from '@/lib/navigation'

interface BottomNavProps {
  onMorePress: () => void
}

export default function BottomNav({ onMorePress }: BottomNavProps) {
  const { t } = useTranslation()
  const location = useLocation()

  // Terminal page has its own mobile toolbar — hide bottom nav there
  if (location.pathname === '/terminal') return null

  const navItems = BOTTOM_NAV_ITEMS.map((i) => ({
    to: i.to,
    icon: i.icon,
    label: t(i.mobileLabelKey ?? i.labelKey),
  }))

  return (
    <nav className="fixed bottom-0 left-0 right-0 z-50 border-t border-border bg-card md:hidden">
      <div className="flex items-center justify-around h-14 pb-safe">
        {navItems.map((tab) => (
          <NavLink
            key={tab.to}
            to={tab.to}
            className={({ isActive }) =>
              cn(
                'flex flex-col items-center justify-center gap-0.5 flex-1 h-full active:opacity-70 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40',
                // Require exact match OR a trailing-slash boundary so e.g.
                // '/dashboard' doesn't highlight on a hypothetical
                // '/dashboard-foo' route.
                (isActive || location.pathname === tab.to || location.pathname.startsWith(tab.to + '/'))
                  ? 'text-primary'
                  : 'text-muted-foreground'
              )
            }
          >
            <tab.icon className="h-[22px] w-[22px]" />
            <span className="text-[10px] font-medium">{tab.label}</span>
          </NavLink>
        ))}
        <button
          onClick={onMorePress}
          aria-label={t('layout.mobileNav.more')}
          className="flex flex-col items-center justify-center gap-0.5 flex-1 h-full text-muted-foreground active:opacity-70 transition-opacity outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
        >
          <Menu className="h-[22px] w-[22px]" />
          <span className="text-[10px] font-medium">{t('layout.mobileNav.more')}</span>
        </button>
      </div>
    </nav>
  )
}
