import { useTranslation } from 'react-i18next'
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import { Container, Network, HardDrive, Settings, RotateCw, Heart } from 'lucide-react'
import type { DiffByCategory } from '@/types/api'
import { DiffServiceRow } from './DiffServiceRow'

interface Props {
  byCategory: DiffByCategory
}

interface CatMeta {
  key: keyof DiffByCategory
  icon: React.ComponentType<{ className?: string }>
}

const CATEGORIES: CatMeta[] = [
  { key: 'image',       icon: Container },
  { key: 'ports',       icon: Network },
  { key: 'volumes',     icon: HardDrive },
  { key: 'env',         icon: Settings },
  { key: 'restart',     icon: RotateCw },
  { key: 'healthcheck', icon: Heart },
]

export function DiffCategoryList({ byCategory }: Props) {
  const { t } = useTranslation()
  // restart/healthcheck stay as literal compose terms; the rest are translated.
  const labels: Record<keyof DiffByCategory, string> = {
    image: t('compose.diff.categoryImage', 'Image'),
    ports: t('compose.diff.categoryPorts', 'Ports'),
    volumes: t('compose.diff.categoryVolumes', 'Volumes'),
    env: t('compose.diff.categoryEnv', 'Environment'),
    restart: 'restart',
    healthcheck: 'healthcheck',
  }

  // Categories with ≥1 change are open by default. The shadcn Accordion
  // accepts a `defaultValue` array (multi-mode) to express this.
  const defaultOpen = CATEGORIES
    .filter(c => (byCategory[c.key]?.length ?? 0) > 0)
    .map(c => c.key as string)

  return (
    <Accordion type="multiple" defaultValue={defaultOpen} className="w-full">
      {CATEGORIES.map(({ key, icon: Icon }) => {
        const items = byCategory[key] ?? []
        const count = items.length
        const isEmpty = count === 0
        return (
          <AccordionItem key={key} value={key} className={isEmpty ? 'opacity-50' : ''}>
            <AccordionTrigger className="text-[13px]" disabled={isEmpty}>
              <span className="flex items-center gap-2 flex-1">
                <Icon className="h-3.5 w-3.5" />
                <span>{labels[key]}</span>
              </span>
              <span className="text-[12px] text-muted-foreground mr-2">
                {isEmpty
                  ? t('compose.diff.categoryNoChanges', 'No changes')
                  : t('compose.diff.changeCount', '{{n}} changed', { n: count })}
              </span>
            </AccordionTrigger>
            <AccordionContent>
              <div className="px-2">
                {items.map((change, i) => {
                  if (key === 'image') return <DiffServiceRow key={i} kind="image" change={change as never} />
                  if (key === 'restart') return <DiffServiceRow key={i} kind="scalar" change={change as never} />
                  if (key === 'healthcheck') return <DiffServiceRow key={i} kind="healthcheck" change={change as never} />
                  return <DiffServiceRow key={i} kind="set" change={change as never} />
                })}
              </div>
            </AccordionContent>
          </AccordionItem>
        )
      })}
    </Accordion>
  )
}
