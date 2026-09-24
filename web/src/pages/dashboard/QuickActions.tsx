import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Settings2 } from 'lucide-react'
import { NAV_ITEMS } from '@/lib/navigation'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'

const STORAGE_KEY = 'sfpanel-dashboard-shortcuts'
const DEFAULTS = ['/terminal', '/files', '/docker', '/logs']
const choices = NAV_ITEMS.filter((item) => item.to !== '/dashboard')

function readShortcuts(): string[] {
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) || 'null')
    if (Array.isArray(saved)) {
      const valid = [...new Set(saved.filter((to): to is string => typeof to === 'string' && choices.some((item) => item.to === to)))].slice(0, 4)
      if (valid.length) return valid
    }
  } catch { /* Restricted storage must not prevent opening the dashboard. */ }
  return DEFAULTS
}

export default function QuickActions() {
  const { t } = useTranslation()
  const [selected, setSelected] = useState(readShortcuts)
  const [editing, setEditing] = useState(false)
  const [storageError, setStorageError] = useState(false)
  const toggle = (to: string) => {
    const next = selected.includes(to) ? selected.filter((item) => item !== to) : [...selected, to]
    if (next.length < 1 || next.length > 4) return
    setSelected(next)
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
      setStorageError(false)
    } catch { setStorageError(true) }
  }
  return (
    <nav aria-label={t('dashboard.quickActions')}>
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">{t('dashboard.quickActions')}</h2>
        <button type="button" onClick={() => setEditing(true)} className="flex min-h-11 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring">
          <Settings2 className="size-4" aria-hidden="true" />{t('dashboard.customize')}
        </button>
      </div>
      <div className="grid grid-cols-4 gap-2">
        {selected.map((to) => {
          const item = choices.find((choice) => choice.to === to)!
          return <Link key={to} to={to} title={t(item.labelKey)} className="flex min-h-11 min-w-0 flex-col items-center justify-center gap-1 rounded-xl border border-border bg-card px-2 py-2 text-xs font-medium hover:bg-secondary focus-visible:outline-2 focus-visible:outline-ring sm:flex-row sm:justify-start sm:gap-2 sm:px-3 sm:text-sm">
            <item.icon className="size-4 shrink-0 text-primary" aria-hidden="true" /><span className="max-w-full truncate">{t(item.labelKey)}</span>
          </Link>
        })}
      </div>
      <Dialog open={editing} onOpenChange={setEditing}>
        <DialogContent>
          <DialogHeader><DialogTitle>{t('dashboard.customize')}</DialogTitle><DialogDescription>{t('dashboard.shortcutHelp')}</DialogDescription></DialogHeader>
          <div className="grid grid-cols-2 gap-2">
            {choices.map((item) => <label key={item.to} className="flex min-h-11 cursor-pointer items-center gap-2 rounded-lg border p-2 text-sm has-disabled:opacity-50">
              <input type="checkbox" checked={selected.includes(item.to)} onChange={() => toggle(item.to)}
                disabled={selected.includes(item.to) ? selected.length === 1 : selected.length >= 4} className="size-4 accent-primary" />
              {t(item.labelKey)}
            </label>)}
          </div>
          {storageError && <p role="status" className="text-sm text-destructive">{t('dashboard.shortcutStorageError')}</p>}
          <button type="button" onClick={() => setEditing(false)} className="min-h-11 rounded-lg bg-primary px-4 text-sm font-medium text-primary-foreground">{t('dashboard.done')}</button>
        </DialogContent>
      </Dialog>
    </nav>
  )
}
