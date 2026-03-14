import { NavLink, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { LayoutDashboard, Container, Terminal, FileText, Menu, type LucideIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

interface NavItem {
  to: string
  icon: LucideIcon
  label: string
  end?: boolean
}

interface BottomNavProps {
  onMorePress: () => void
}

export default function BottomNav({ onMorePress }: BottomNavProps) {
  const { t } = useTranslation()
  const location = useLocation()

  // Terminal page has its own mobile toolbar — hide bottom nav there
  if (location.pathname === '/terminal') return null

  const navItems: NavItem[] = [
    { to: '/dashboard', icon: LayoutDashboard, label: t('layout.mobileNav.dashboard') },
    { to: '/docker', icon: Container, label: t('layout.mobileNav.docker') },
    { to: '/terminal', icon: Terminal, label: t('layout.mobileNav.terminal') },
    { to: '/logs', icon: FileText, label: t('layout.nav.logs') },
  ]

  return (
    <nav className="fixed bottom-0 left-0 right-0 z-50 border-t border-border bg-card md:hidden">
      <div className="flex items-center justify-around h-14 pb-safe">
        {navItems.map((tab) => (
          <NavLink
            key={tab.to}
            to={tab.to}
            end={tab.end}
            className={({ isActive }) =>
              cn(
                'flex flex-col items-center justify-center gap-0.5 flex-1 h-full active:opacity-70 transition-colors',
                (isActive || location.pathname.startsWith(tab.to))
                  ? 'text-[#3182f6]'
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
          className="flex flex-col items-center justify-center gap-0.5 flex-1 h-full text-muted-foreground active:opacity-70 transition-opacity"
        >
          <Menu className="h-[22px] w-[22px]" />
          <span className="text-[10px] font-medium">{t('layout.mobileNav.more')}</span>
        </button>
      </div>
    </nav>
  )
}
