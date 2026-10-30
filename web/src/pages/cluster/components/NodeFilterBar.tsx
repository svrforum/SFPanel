import { useTranslation } from 'react-i18next'
import { Search } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ALL_NODES, type NodeFilter } from '@/lib/clusterNodes'

// Radix Select refuses '' as an item value, and '' is the "no group" filter.
const ANY_GROUP = '__all__'
const NO_GROUP = '__none__'

function Segments<T extends string>({ label, value, options, onChange }: {
  label: string
  value: T
  options: { value: T; label: string }[]
  onChange: (value: T) => void
}) {
  // Captioned on screen like the group select: three "All" buttons in a row
  // say nothing about what each one is all of.
  return (
    <div className="flex items-center gap-1.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <div role="group" aria-label={label} className="flex items-center gap-1">
        {options.map((o) => (
          <Button
            key={o.value}
            variant={value === o.value ? 'default' : 'outline'}
            size="sm"
            aria-pressed={value === o.value}
            onClick={() => onChange(o.value)}
            className="h-7 text-xs rounded-xl"
          >
            {o.label}
          </Button>
        ))}
      </div>
    </div>
  )
}

export function NodeFilterBar({ filter, onChange, groups, total, shown }: {
  filter: NodeFilter
  onChange: (filter: NodeFilter) => void
  groups: string[]
  total: number
  shown: number
}) {
  const { t } = useTranslation()
  const set = (patch: Partial<NodeFilter>) => onChange({ ...filter, ...patch })
  const all = t('cluster.nodes.all')
  // A group from a link may name one no node carries any more; keep it
  // selectable so the trigger still says what is filtering the list.
  const groupOptions = filter.group && !groups.includes(filter.group) ? [...groups, filter.group] : groups
  const filtered = filter.kind !== ALL_NODES.kind || filter.status !== ALL_NODES.status
    || filter.group !== ALL_NODES.group || filter.query.trim() !== ''

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="relative flex-1 min-w-[200px]">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none" />
        <Input
          value={filter.query}
          onChange={(e) => set({ query: e.target.value })}
          placeholder={t('cluster.nodes.search')}
          aria-label={t('cluster.nodes.search')}
          className="pl-9 h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
        />
      </div>
      <Segments
        label={t('cluster.nodes.filterKind')}
        value={filter.kind}
        onChange={(kind) => set({ kind })}
        options={[
          { value: 'all', label: all },
          { value: 'manager', label: t('cluster.nodes.managers') },
          { value: 'agent', label: t('cluster.nodes.agents') },
        ]}
      />
      <Segments
        label={t('cluster.nodes.filterStatus')}
        value={filter.status}
        onChange={(status) => set({ status })}
        options={[
          { value: 'all', label: all },
          { value: 'online', label: t('cluster.nodes.online') },
          { value: 'offline', label: t('cluster.nodes.offline') },
        ]}
      />
      <div className="flex items-center gap-1.5">
        <span className="text-xs text-muted-foreground">{t('cluster.nodes.filterGroup')}</span>
        <Select
          value={filter.group === null ? ANY_GROUP : filter.group === '' ? NO_GROUP : filter.group}
          onValueChange={(v) => set({ group: v === ANY_GROUP ? null : v === NO_GROUP ? '' : v })}
        >
          <SelectTrigger size="sm" className="rounded-xl text-xs min-w-[120px]" aria-label={t('cluster.nodes.filterGroup')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ANY_GROUP}>{all}</SelectItem>
            <SelectItem value={NO_GROUP}>{t('cluster.nodes.noGroup')}</SelectItem>
            {groupOptions.map((g) => (
              <SelectItem key={g} value={g}>{g}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      {filtered && (
        <span className="text-xs text-muted-foreground">{t('cluster.nodes.shown', { shown, total })}</span>
      )}
    </div>
  )
}
