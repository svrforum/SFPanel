import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import { Container, Network, HardDrive, Settings, RotateCw, Heart } from 'lucide-react'
import type { DiffByCategory } from '@/types/api'
import { DiffServiceRow } from './DiffServiceRow'

interface Props {
  byCategory: DiffByCategory
}

interface CatMeta {
  key: keyof DiffByCategory
  label: string
  icon: React.ComponentType<{ className?: string }>
}

const CATEGORIES: CatMeta[] = [
  { key: 'image',       label: '이미지',     icon: Container },
  { key: 'ports',       label: '포트',       icon: Network },
  { key: 'volumes',     label: '볼륨',       icon: HardDrive },
  { key: 'env',         label: '환경변수',   icon: Settings },
  { key: 'restart',     label: 'restart',   icon: RotateCw },
  { key: 'healthcheck', label: 'healthcheck', icon: Heart },
]

export function DiffCategoryList({ byCategory }: Props) {
  // Categories with ≥1 change are open by default. The shadcn Accordion
  // accepts a `defaultValue` array (multi-mode) to express this.
  const defaultOpen = CATEGORIES
    .filter(c => (byCategory[c.key]?.length ?? 0) > 0)
    .map(c => c.key as string)

  return (
    <Accordion type="multiple" defaultValue={defaultOpen} className="w-full">
      {CATEGORIES.map(({ key, label, icon: Icon }) => {
        const items = byCategory[key] ?? []
        const count = items.length
        const isEmpty = count === 0
        return (
          <AccordionItem key={key} value={key} className={isEmpty ? 'opacity-50' : ''}>
            <AccordionTrigger className="text-[13px]" disabled={isEmpty}>
              <span className="flex items-center gap-2 flex-1">
                <Icon className="h-3.5 w-3.5" />
                <span>{label}</span>
              </span>
              <span className="text-[12px] text-muted-foreground mr-2">
                {isEmpty ? '변경 없음' : `${count} 변경`}
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
