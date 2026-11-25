import { Link, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Menu } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useServerScope } from '@/hooks/useServerScope'
import { BOTTOM_NAV_ITEMS, isTerminalRoute, navOwner } from '@/lib/navigation'

interface BottomNavProps {
  onMorePress: () => void
}

export default function BottomNav({ onMorePress }: BottomNavProps) {
  const { t } = useTranslation()
  const location = useLocation()
  const { href } = useServerScope()

  // The terminal pages have their own mobile key bar — hide the bottom nav
  // there rather than stacking the two.
  if (isTerminalRoute(location.pathname)) return null

  // The entry a page belongs to, tabs at their own addresses included:
  // /running lights the dashboard, /appstore Docker — as in the sidebar and More.
  const owner = navOwner(location.pathname)

  // The home-indicator inset pads the bar, under its 56px row: inside the row
  // it left an iPhone 22px for each icon and its label, and the icons shrank.
  return (
    <nav className="fixed bottom-0 left-0 right-0 z-50 border-t border-border bg-card pb-safe md:hidden">
      <div className="flex items-center justify-around h-14">
        {BOTTOM_NAV_ITEMS.map((entry) => {
          const current = owner?.entry === entry
          // The open tab has a bar over its icon and a heavier label, not
          // only a colour: blue and grey differ by 1.2:1 in lightness.
          return (
            <Link
              key={entry.to}
              to={href(entry.to)}
              aria-current={current ? 'page' : undefined}
              className={cn(
                'relative flex flex-col items-center justify-center gap-0.5 flex-1 h-full active:opacity-70 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring',
                current ? 'text-primary' : 'text-muted-foreground',
              )}
            >
              {current && <span aria-hidden="true" className="absolute top-0 left-1/2 h-0.5 w-8 -translate-x-1/2 rounded-full bg-primary" />}
              <entry.icon className="h-[22px] w-[22px]" />
              <span className={cn('text-[12px]', current ? 'font-semibold' : 'font-medium')}>{t(entry.mobileLabelKey ?? entry.labelKey)}</span>
            </Link>
          )
        })}
        <button
          onClick={onMorePress}
          aria-label={t('layout.mobileNav.more')}
          className="flex flex-col items-center justify-center gap-0.5 flex-1 h-full text-muted-foreground active:opacity-70 transition-opacity outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        >
          <Menu className="h-[22px] w-[22px]" />
          <span className="text-[12px] font-medium">{t('layout.mobileNav.more')}</span>
        </button>
      </div>
    </nav>
  )
}
