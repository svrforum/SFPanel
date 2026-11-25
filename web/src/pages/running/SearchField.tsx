import { useTranslation } from 'react-i18next'
import { Search } from 'lucide-react'
import { Input } from '@/components/ui/input'

/** The search box above a board, a canvas or the network map; `network` names what it finds there. */
export function SearchField({ value, onChange, network }: { value: string; onChange: (value: string) => void; network?: boolean }) {
  const { t } = useTranslation()
  return (
    <div className="relative sm:max-w-xs">
      <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
      <Input
        type="search"
        aria-label={t(network ? 'running.network.search.label' : 'running.search.label')}
        placeholder={t(network ? 'running.network.search.placeholder' : 'running.search.placeholder')}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="h-9 rounded-xl border-0 bg-secondary/50 pl-9 text-base md:text-[13px]"
      />
    </div>
  )
}
